import { FleetClient } from "./client.mjs";
import { randomUUID, createHash } from "node:crypto";
import path from "node:path";
import { mkdir, realpath, lstat, open } from "node:fs/promises";
import { constants } from "node:fs";
export const name = "dsh-edge-fleet";
export const inject = ["tools"];
const ident = /^[A-Za-z0-9][A-Za-z0-9_.:-]{0,199}$/;
const safePath = (s) =>
  typeof s === "string" &&
  s.length > 0 &&
  !s.includes("\\") &&
  !s.includes(":") &&
  !s.includes("\0") &&
  !path.isAbsolute(s) &&
  s.split("/").every((x) => x && x !== "." && x !== "..");
export function validatePlan(input) {
  const p = {
    plan_id: input.idempotency_key
      ? "plan_" +
        createHash("sha256").update(input.idempotency_key).digest("hex")
      : "plan_" + randomUUID(),
    idempotency_key: input.plan_id ? "fleet:" + input.plan_id : randomUUID(),
    concurrency_limit: 2,
    created_at: new Date().toISOString(),
    ...input,
  };
  if (
    !ident.test(p.plan_id) ||
    !p.idempotency_key ||
    !p.goal ||
    !Number.isInteger(p.concurrency_limit) ||
    p.concurrency_limit < 1 ||
    !Array.isArray(p.tasks) ||
    !p.tasks.length
  )
    throw Error("invalid plan");
  const tasks = new Map();
  for (const t of p.tasks) {
    if (
      !ident.test(t.task_id) ||
      tasks.has(t.task_id) ||
      !t.capability?.id ||
      !t.capability.version ||
      !t.input ||
      typeof t.input !== "object" ||
      Array.isArray(t.input) ||
      !Array.isArray(t.depends_on)
    )
      throw Error("invalid task");
    tasks.set(t.task_id, t);
  }
  const state = new Map();
  function visit(k) {
    if (!tasks.has(k)) throw Error("missing dependency");
    if (state.get(k) === 1) throw Error("dependency cycle");
    if (state.get(k) === 2) return;
    state.set(k, 1);
    const deps = tasks.get(k).depends_on;
    if (new Set(deps).size !== deps.length) throw Error("duplicate dependency");
    for (const d of deps) visit(d);
    state.set(k, 2);
  }
  for (const k of tasks.keys()) visit(k);
  for (const t of tasks.values()) {
    const ancestors = new Set();
    function walk(k) {
      for (const d of tasks.get(k).depends_on) {
        if (!ancestors.has(d)) {
          ancestors.add(d);
          walk(d);
        }
      }
    }
    walk(t.task_id);
    for (const b of t.input_artifact_bindings ?? []) {
      if (!ancestors.has(b.from_task_id) || !b.name || !safePath(b.mount_path))
        throw Error("invalid artifact binding");
    }
    for (const a of t.input_artifacts ?? [])
      if (!safePath(a.mount_path)) throw Error("invalid artifact mount path");
    for (const a of t.output_artifacts ?? [])
      if (!a.name || !a.media_type || !safePath(a.path))
        throw Error("invalid artifact output path");
    if (
      t.workspace_context &&
      (!/^[a-f0-9]{40,64}$/i.test(t.workspace_context.expected_git_head) ||
        t.workspace_context.policy !== "require_clean_or_match" ||
        !t.workspace_context.root_dir)
    )
      throw Error("invalid workspace_context");
  }
  return p;
}
async function saveArtifact(root, dir, ref, data) {
  if (!root) throw Error("workspaceRoot required to save artifacts");
  if (
    !safePath(dir) ||
    !safePath(ref.artifact_id) ||
    ref.artifact_id.includes("/")
  )
    throw Error("invalid workspace path");
  await mkdir(root, { recursive: true });
  const base = await realpath(root);
  let dest = base;
  for (const segment of dir.split("/")) {
    dest = path.join(dest, segment);
    try {
      await mkdir(dest);
    } catch (e) {
      if (e.code !== "EEXIST") throw e;
    }
    const st = await lstat(dest);
    if (st.isSymbolicLink() || !st.isDirectory())
      throw Error("symlink workspace path rejected");
  }
  // Artifact names are untrusted display text; use coordinator-generated IDs.
  dest = path.join(dest, ref.artifact_id);
  const f = await open(
    dest,
    constants.O_WRONLY |
      constants.O_CREAT |
      constants.O_EXCL |
      (constants.O_NOFOLLOW ?? 0),
    0o600,
  );
  try {
    await f.writeFile(data);
  } finally {
    await f.close();
  }
  return dest;
}
const text = { type: "string" };
const bool = { type: "boolean" };
export const toolDefinitions = [
  [
    "fleet.list_devices",
    "List fleet devices, resources and authorized capabilities",
    {},
    [],
  ],
  [
    "fleet.inspect_device",
    "Read manifest or run fresh device.inspect",
    { device_id: text, fresh: bool },
    ["device_id"],
  ],
  [
    "fleet.list_capabilities",
    "List fleet capabilities with versions and concurrency",
    {},
    [],
  ],
  [
    "fleet.plan_tasks",
    "Validate and submit an explicit task DAG",
    {
      plan_id: text,
      idempotency_key: text,
      goal: text,
      tasks: { type: "array", items: { type: "object" } },
      concurrency_limit: {
        type: "integer",
        description: "Positive Plan concurrency limit",
      },
    },
    ["goal", "tasks"],
  ],
  [
    "fleet.dispatch_task",
    "Submit an independent task, optionally wait",
    { task: { type: "object" }, wait: bool, goal: text },
    ["task"],
  ],
  [
    "fleet.get_task",
    "Read latest task state, log tail and artifacts",
    { plan_id: text, task_id: text },
    ["plan_id", "task_id"],
  ],
  [
    "fleet.cancel_task",
    "Idempotently cancel a Plan including live work",
    { plan_id: text },
    ["plan_id"],
  ],
  [
    "fleet.get_artifacts",
    "Read task artifact refs, optional text/download",
    { plan_id: text, task_id: text, text: bool, save_dir: text },
    ["plan_id", "task_id"],
  ],
].map(([name, description, properties, required]) => ({
  name,
  description,
  parameters: {
    type: "object",
    properties,
    required,
    additionalProperties: false,
  },
}));
export function createTools(config) {
  const client = config.client ?? new FleetClient(config);
  const options = (exec) => ({ signal: exec?.signal });
  const submit = async (p, exec) => {
    const explicitTime = p.created_at != null;
    p = validatePlan(p);
    // Reuse the authoritative creation time on retries, keeping all semantic
    // arguments under the Coordinator's existing full-envelope fingerprint.
    const recoverTime = async () => {
      const old = await client.plan(p.plan_id, options(exec));
      if (!explicitTime && old.plan.idempotency_key === p.idempotency_key)
        p.created_at = old.plan.created_at;
    };
    try {
      await recoverTime();
    } catch (e) {
      if (e.code !== "PLAN_NOT_FOUND" && e.status !== 404) throw e;
    }
    let accepted;
    try {
      accepted = await client.submit(p, options(exec));
    } catch (e) {
      if (e.code !== "IDEMPOTENCY_CONFLICT") throw e;
      await recoverTime();
      accepted = await client.submit(p, options(exec));
    }
    return {
      ...accepted,
      projection: await client.plan(p.plan_id, options(exec)),
    };
  };
  const handlers = {
    "fleet.list_devices": (_a, e) => client.devices(options(e)),
    "fleet.inspect_device": async (a, e) => {
      if (a.fresh) {
        const r = await submit(
          {
            goal: "Inspect device",
            tasks: [
              {
                task_id: "inspect_" + randomUUID(),
                target_device_id: a.device_id,
                capability: { id: "device.inspect", version: "^1.0.0" },
                input: {},
                depends_on: [],
              },
            ],
          },
          e,
        );
        return client.waitPlan(r.plan_id, {
          ...options(e),
          timeoutMs: config.waitMs,
          pollMs: config.pollMs,
        });
      }
      const d = (await client.devices(options(e))).find(
        (d) => d.device_id === a.device_id,
      );
      if (!d) throw Error("device not found");
      return d;
    },
    "fleet.list_capabilities": async (_a, e) =>
      (await client.devices(options(e))).flatMap((d) =>
        d.capabilities.map((c) => ({
          ...c,
          device_id: d.device_id,
          online: d.online,
        })),
      ),
    "fleet.plan_tasks": submit,
    "fleet.dispatch_task": async (a, e) => {
      if (a.task.depends_on?.length) throw Error("independent task required");
      const r = await submit(
        {
          goal: a.goal ?? "Independent task",
          tasks: [a.task],
          plan_id:
            a.task.plan_id ||
            "dispatch_" +
              createHash("sha256").update(a.task.task_id).digest("hex"),
          idempotency_key:
            a.task.idempotency_key || "dispatch:" + a.task.task_id,
        },
        e,
      );
      return a.wait
        ? client.waitPlan(r.plan_id, {
            ...options(e),
            timeoutMs: config.waitMs,
            pollMs: config.pollMs,
          })
        : r;
    },
    "fleet.get_task": async (a, e) => {
      const p = await client.plan(a.plan_id, options(e));
      const task = p.tasks.find((t) => t.task.task_id === a.task_id);
      if (!task) throw Error("task not found");
      let after = 0,
        through = 0,
        logs = "";
      do {
        const page = await client.events(after, through, 500, options(e));
        through = page.high_watermark;
        for (const ev of page.events)
          if (
            ev.plan_id === a.plan_id &&
            ev.task_id === a.task_id &&
            ev.type === "task.log"
          )
            logs = (logs + (ev.payload.text ?? "")).slice(-16384);
        after = page.next_coord_seq;
        if (!page.has_more) break;
      } while (true);
      return {
        ...task,
        plan_id: a.plan_id,
        log_tail: logs,
        snapshot_coord_seq: p.snapshot_coord_seq,
      };
    },
    "fleet.cancel_task": (a, e) =>
      client.control(a.plan_id, "cancel", options(e)),
    "fleet.get_artifacts": async (a, e) => {
      const p = await client.plan(a.plan_id, options(e));
      const task = p.tasks.find((t) => t.task.task_id === a.task_id);
      if (!task) throw Error("task not found");
      const out = [];
      for (const ref of task.artifacts) {
        const item = { ...ref };
        if (a.text || a.save_dir) {
          const data = await client.downloadArtifact(ref, options(e));
          if (a.text) {
            if (
              !/^(text\/|application\/(json|xml|junit\+xml))/.test(
                ref.media_type,
              )
            )
              throw Error("text artifact required");
            item.text = data.toString("utf8");
          }
          if (a.save_dir)
            item.saved_path = await saveArtifact(
              config.workspaceRoot,
              a.save_dir,
              ref,
              data,
            );
        }
        out.push(item);
      }
      return out;
    },
  };
  return toolDefinitions.map((d) => ({
    ...d,
    timeoutMs: config.toolTimeoutMs ?? 180000,
    isConcurrencySafe: () => true,
    output: {
      schema: {},
      render: (_args, value) => [{ type: "text", text: JSON.stringify(value) }],
    },
    execute: (args, exec) => handlers[d.name](args, exec),
  }));
}
export function apply(ctx, config) {
  const tools = createTools(config);
  const dispose = tools.map((d) => ctx.tools.register(d));
  return () => {
    for (const f of dispose) f?.();
  };
}
