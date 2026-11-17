# DSH Edge Fleet Cordis plugin

Native Node18+/22+ HTTPS client and eight fleet tools. No runtime dependencies or new model/chat loop. Upstream host `ccch1mneyyy/dsh-go` f5fa9256db4c82d601078b8064056b93dc3560ec uses Node>=24.9 and `@deepseek-ai/dsh-tools`0.2.2-rc.1; this plugin supplies the registry-ready JSON Schema contract accepted by `ToolRegistry.register`, with `inject=['tools']`, output.schema/render and execute(args,exec.signal).

```js
import {apply} from './src/index.mjs';
import {readFileSync} from 'node:fs';
apply(ctx, {
  url: 'https://coordinator.example:8740',
  token: process.env.DSH_CONTROLLER_TOKEN,
  ca: readFileSync('/private/fleet-ca.pem'),
  workspaceRoot: '/private/fleet-downloads',
});
```

With upstream DSH, install this directory as a local Cordis bundle using `dsh plugin --profile PROFILE add /absolute/path/to/integrations/dsh-cordis-plugin` and configure its URL/token/CA in the profile. `cordis.patch.yml` declares the actual bundle insert. The tools register in the existing host tool service; the host retains its chat UI, provider routing and agent loop. A token is configuration, never a tool argument.

Tools: fleet.list_devices, inspect_device, list_capabilities, plan_tasks, dispatch_task, get_task, cancel_task, get_artifacts. Exported `toolDefinitions` contains schemas; `createTools(config)` returns executable registry definitions. `plan_tasks` accepts an explicit DAG and locally checks IDs, dependencies/cycles, bindings and paths; Coordinator remains authoritative for SemVer/input schemas/assignment. `dispatch_task` accepts `{task,wait?}`; `get_task/get_artifacts` accept `{plan_id,task_id}`. Artifact options are `text:true` and `save_dir:'relative/path'` under workspaceRoot. Filenames use artifact IDs, never untrusted display names. Download directories must be owned by the DSH user and not writable by untrusted local users; symlink traversal and existing destination replacement are rejected.

`FleetClient` exports devices/snapshot/plan/submit/events/control, approvals/resolveApproval/createPairing, waitPlan and uploadArtifact/downloadArtifact. HTTPS verifies server certificates and supports custom CA; redirects are not followed. Calls propagate AbortSignal, enforce per-request deadlines; waiting has a separate bounded timeout. Artifacts use confirmed-offset4MiB upload sessions, validate size/SHA256 on downloads; buffered download responses are limited to64MiB. Tool execution defaults to180s, waitPlan120s. Approval/pairing helpers are explicit controller methods rather than extra AI tools; show decisions in the host operator UI.

`node --test test/plugin.test.mjs` compiles and launches the real Go Coordinator and two paired Worker processes, with a fresh TLS certificate and actual Git/process/artifact operations. Source contract inspection is pinned and documented; this is an embeddable plugin package, not a claim that a modified upstream APK was built.

Idempotent tool retries: supply plan_id and/or idempotency_key. Missing counterparts are derived from the supplied stable identity; creation time is recovered from the matching stored Plan, and Coordinator fingerprinting still rejects semantic changes. dispatch_task derives a stable Plan/key from its globally unique task_id. The test covers a committed submit with its response deliberately discarded and a subsequent replay, as well as changed-meaning conflicts and repeated dispatch.

Known recovery limits: a disk-full save can leave its newly created partial destination, so remove that file before retrying; no existing destination is overwritten. A full upload rejected for a digest mismatch cannot currently be reset in place; retry with a new artifact name/session after stabilizing the source file. Ready artifacts remain immutable.
