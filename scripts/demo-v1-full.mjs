import assert from "node:assert/strict";
import { mkdtemp, mkdir, writeFile, readFile, rm } from "node:fs/promises";
import path from "node:path";
import { tmpdir } from "node:os";
import { execFileSync, spawn } from "node:child_process";
import { once } from "node:events";
import net from "node:net";
import { fileURLToPath } from "node:url";
import { FleetClient } from "../integrations/dsh-cordis-plugin/src/client.mjs";
const repo = fileURLToPath(new URL("../", import.meta.url));
const root = await mkdtemp(path.join(tmpdir(), "fleet-v1-demo-"));
const children = [];
let log = "";
const run = (exe, args, opts = {}) =>
  execFileSync(exe, args, { cwd: repo, encoding: "utf8", ...opts });
const delay = (ms) => new Promise((r) => setTimeout(r, ms));
async function poll(f) {
  for (let i = 0; i < 200; i++) {
    try {
      const v = await f();
      if (v) return v;
    } catch {}
    await delay(50);
  }
  throw Error("demo readiness timeout\n" + log);
}
try {
  const bin = (n) =>
    path.join(root, n + (process.platform === "win32" ? ".exe" : ""));
  for (const n of ["coordinator", "edge-agent"])
    run("go", [
      "build",
      "-buildvcs=false",
      "-o",
      bin(n),
      `./apps/${n}/cmd/${n}`,
    ]);
  run("go", ["run", "./tests/fixtures/tls-cert", root]);
  const server = net.createServer();
  server.listen(0, "127.0.0.1");
  await once(server, "listening");
  const port = server.address().port;
  await new Promise((r) => server.close(r));
  const url = `https://127.0.0.1:${port}`;
  const start = (exe, args, extra = {}) => {
    const p = spawn(exe, args, {
      stdio: ["ignore", "pipe", "pipe"],
      env: {
        ...process.env,
        DSH_WORKER_TOKENS: "",
        DSH_WORKER_TOKEN: "",
        ...extra,
      },
    });
    p.stdout.on("data", (b) => (log += b));
    p.stderr.on("data", (b) => (log += b));
    children.push(p);
    return p;
  };
  start(
    bin("coordinator"),
    [
      "--listen",
      `127.0.0.1:${port}`,
      "--db",
      path.join(root, "coordinator.db"),
      "--tls-cert",
      path.join(root, "cert.pem"),
      "--tls-key",
      path.join(root, "key.pem"),
    ],
    { DSH_CONTROLLER_TOKEN: "demo-controller" },
  );
  const c = new FleetClient({
    url,
    token: "demo-controller",
    ca: await readFile(path.join(root, "cert.pem")),
  });
  await poll(() => c.devices());
  const workspace = path.join(root, "workspace");
  await mkdir(workspace);
  run("git", ["init", "-q", workspace]);
  const git = (args) => run("git", ["-C", workspace, ...args]);
  git(["config", "user.name", "Fleet fixture"]);
  git(["config", "user.email", "fixture@example.invalid"]);
  await writeFile(path.join(workspace, "package.json"), '{"type":"module"}');
  await writeFile(
    path.join(workspace, "sum.mjs"),
    "export const sum=(a,b)=>a+b-1;\n",
  );
  await writeFile(
    path.join(workspace, "unit.test.mjs"),
    "import {test} from 'node:test'; import assert from 'node:assert/strict';import {sum} from './sum.mjs';test('dirty patch fixes sum',async()=>{await new Promise(r=>setTimeout(r,500));assert.equal(sum(1,1),2)});\n",
  );
  await writeFile(
    path.join(workspace, "runner.mjs"),
    "import {spawnSync} from 'node:child_process';import {writeFileSync} from 'node:fs';const r=spawnSync(process.execPath,['--test','--test-reporter=junit','unit.test.mjs'],{encoding:'utf8'});writeFileSync('junit.xml',r.stdout);console.log('real unit tests exit='+r.status);if(r.status!==0){console.error(r.stderr);process.exit(r.status??1)}\n",
  );
  git(["add", "."]);
  git(["commit", "-qm", "base fixture"]);
  const head = git(["rev-parse", "HEAD"]).trim();
  await writeFile(
    path.join(workspace, "sum.mjs"),
    "export const sum=(a,b)=>a+b;\n",
  );
  const patch = Buffer.from(git(["diff", "--binary", "HEAD"]));
  assert.ok(patch.length > 0);
  git(["checkout", "--", "sum.mjs"]);
  const patchRef = await c.uploadArtifact(
    {
      plan_id: "demo_full",
      task_id: "unit_a",
      name: "dirty.patch",
      media_type: "text/x-diff",
    },
    patch,
  );
  for (const id of ["demo-a", "demo-b"]) {
    const pair = await c.createPairing({
      device_hint: id,
      coordinator_url: url,
    });
    start(bin("edge-agent"), [
      "--id",
      id,
      "--pair",
      pair.pairing_code,
      "--db",
      path.join(root, id + ".db"),
      "--coordinator",
      url,
      "--ca",
      path.join(root, "cert.pem"),
      "--workspace",
      `workspace/project=${workspace}`,
      "--allow-programs",
      JSON.stringify([process.execPath]),
    ]);
  }
  await poll(async () => {
    const d = await c.devices();
    return d.length === 2 && d.every((x) => x.online);
  });
  const context = {
    root_dir: "workspace/project",
    expected_git_head: head,
    patch_artifact_id: patchRef.artifact_id,
    policy: "require_clean_or_match",
  };
  const task = (task_id, device, capability, input) => ({
    task_id,
    target_device_id: device,
    capability: { id: capability, version: "^1.0.0" },
    input,
    depends_on: [],
  });
  const unit = (id, device) => ({
    ...task(id, device, "process.exec", {
      program: process.execPath,
      args: ["runner.mjs"],
      cwd: "workspace/project",
      timeout_ms: 10000,
    }),
    workspace_context: context,
    output_artifacts: [
      { path: "junit.xml", name: "junit", media_type: "application/xml" },
    ],
  });
  const a = unit("unit_a", "demo-a"),
    b = unit("unit_b", "demo-b");
  a.requires_approval = true;
  a.approval_reason = "Run patched repository tests";
  // Each isolated patch checkout is independent, including the diff inspection.
  const diff = {
    ...task("git_diff", "demo-b", "git.inspect", {
      workspace: "workspace/project",
      mode: "diff",
    }),
    workspace_context: context,
  };
  const summary = {
    ...task("summary", "demo-a", "process.exec", {
      program: process.execPath,
      args: [
        "-e",
        "const fs=require('fs'),assert=require('assert');const reports=['reports/a.xml','reports/b.xml'].map(p=>fs.readFileSync(p,'utf8'));for(const r of reports){assert.match(r,/<testcase /);assert.match(r,/<!-- fail 0 -->/)};fs.writeFileSync('summary.txt','2 real test suites passed; dirty patch applied on both workers\\n');console.log('summary verified')",
      ],
      cwd: "workspace/project",
      timeout_ms: 5000,
    }),
    depends_on: ["unit_a", "unit_b", "git_diff"],
    input_artifact_bindings: [
      { from_task_id: "unit_a", name: "junit", mount_path: "reports/a.xml" },
      { from_task_id: "unit_b", name: "junit", mount_path: "reports/b.xml" },
    ],
    output_artifacts: [
      { path: "summary.txt", name: "summary", media_type: "text/plain" },
    ],
  };
  await c.submit({
    plan_id: "demo_full",
    idempotency_key: "demo_full_key",
    goal: "Patched parallel tests, diff and bound JUnit summary",
    concurrency_limit: 4,
    created_at: new Date().toISOString(),
    tasks: [a, b, diff, summary],
  });
  const approval = await poll(async () =>
    (await c.approvals()).find(
      (x) => x.plan_id === "demo_full" && x.status === "pending",
    ),
  );
  await c.resolveApproval(
    approval.id,
    "approved",
    "demo operator authorization",
  );
  const final = await c.waitPlan("demo_full", { timeoutMs: 30000, pollMs: 50 });
  assert.equal(final.status, "succeeded", JSON.stringify(final) + log);
  const gitResult = final.tasks.find(
    (x) => x.task.task_id === "git_diff",
  ).result;
  assert.match(JSON.stringify(gitResult), /a\+b/);
  let after = 0,
    through = 0;
  const events = [];
  do {
    const p = await c.events(after, through);
    events.push(...p.events);
    through = p.high_watermark;
    after = p.next_coord_seq;
    if (!p.has_more) break;
  } while (true);
  const window = (id) => {
    const e = events.filter((x) => x.task_id === id);
    return {
      start: Date.parse(e.find((x) => x.type === "task.started").timestamp),
      end: Date.parse(e.find((x) => x.type === "task.succeeded").timestamp),
    };
  };
  const wa = window("unit_a"),
    wb = window("unit_b");
  assert.ok(
    Math.max(wa.start, wb.start) < Math.min(wa.end, wb.end),
    "unit task intervals must actually overlap",
  );
  const ref = final.tasks
    .find((x) => x.task.task_id === "summary")
    .artifacts.find((x) => x.name === "summary");
  const text = (await c.downloadArtifact(ref)).toString();
  assert.match(text, /2 real test suites passed/);
  const evidence = {
    status: final.status,
    workers: 2,
    patch_bytes: patch.length,
    unit_windows: { a: wa, b: wb },
    overlap_ms: Math.min(wa.end, wb.end) - Math.max(wa.start, wb.start),
    summary: text,
    artifact: ref,
  };
  if (process.argv[2])
    await writeFile(process.argv[2], JSON.stringify(evidence, null, 2) + "\n");
  console.log(JSON.stringify(evidence, null, 2));
} finally {
  for (const p of children) p.kill();
  await Promise.all(
    children.map((p) =>
      p.exitCode !== null ? null : Promise.race([once(p, "exit"), delay(3000)]),
    ),
  );
  if (process.env.DSH_KEEP_DEMO === "1")
    console.log("Demo retained at " + root);
  else await rm(root, { recursive: true, force: true });
}
