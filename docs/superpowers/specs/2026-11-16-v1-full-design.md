# DSH Edge Orchestrator — 第三轮终极施工任务书（一口气完成 V1 全量闭环）

## 1. 任务目标与当前基线

你是 [h1neolzr7f/dsh-edge-orchestrator](https://github.com/h1neolzr7f/dsh-edge-orchestrator?utm_source=chatgpt.com) 项目的核心研发子智能体。该项目围绕 [ccch1mneyyy/dsh-go](https://github.com/ccch1mneyyy/dsh-go) 构建跨设备 AI Agent 编排系统（严格遵守其 [AGPL-3.0 许可证](https://github.com/ccch1mneyyy/dsh-go/blob/main/LICENSE) 与 [ASSETS.md 素材边界](https://github.com/ccch1mneyyy/dsh-go/blob/main/ASSETS.md)），不将 [liguobao/ds-harness-remote](https://github.com/liguobao/ds-harness-remote) 作为 V1 核心通信依赖，并对标 [Grok Bot Chat and Collaboration](https://docs.x.ai/grok-bot/chat-and-collaboration) 实现对话优先（Chat-first）的跨设备协作体验。

目前仓库已完成 Phase 0、Phase 1 和 Phase 2（共 80 个文件、10,308 行，45 项顶层测试通过）。本轮施工的目标是：**按 5 个严格递进的里程碑（M-A 至 M-E），一口气把剩余的 Phase 2 边界修复、Phase 3/5 HTTPS Artifact 断点续传与跨机产物/补丁流转、Phase 6 审批门禁与动态配对、以及 Phase 4 的 Node.js Cordis 插件与 Dart/Flutter 聊天卡片状态机全部高质量实现并验证闭环。**

> **铁律**：严禁使用空壳函数、假数据或占位符应付！每完成一个里程碑，必须先跑通对应的 RED → GREEN 自动化测试，再进入下一个里程碑。

## 2. 里程碑 M-A：修复 Phase 2 遗留的 4 个边界隐患（P2-B1 至 P2-B4）

在编写新功能前，先修复以下 4 个源码隐患并补齐回归测试：

### P2-B1：修复任务级 Detached Git Worktree 磁盘与元数据泄漏
- **文件**：`apps/edge-agent/workspace.go`、`apps/edge-agent/agent.go`、`apps/edge-agent/process.go`
- **问题**：`prepareTask`（`workspace.go:232`）执行 `git worktree add --detach <dir> <head>` 创建了任务独立工作树，但任务进入终态（`succeeded` / `failed` / `cancelled`）并完成产物收集后，从未清理该 worktree，导致宿主仓库 `.git/worktrees/` 残留僵尸条目且 `runs/<hash>/workspace` 无限占用磁盘。
- **修复要求**：
  1. 在任务完全终结（日志已 drain、且 M-B 中的输出 Artifact 已上传完毕）后，调用新增的 `cleanupTaskWorkspace(t protocol.TaskEnvelope)`：执行 `git -C <root> worktree remove --force <dir>` 与 `git -C <root> worktree prune`，并删除临时 `runs/<hash>/workspace` 目录（保留 `runs/<hash>/stdout.log`、`stderr.log`、`exit.json` 供诊断与摘要读取）。
  2. 新增回归测试 `TestPhase2WorktreeCleanedUpAfterCompletion`：验证带 `workspace_context` 的任务完成后，`runs/<hash>/workspace` 目录被清理且宿主仓库 `git worktree list` 不残留临时条目。

### P2-B2：修复 `AppendEvent` 乱序毒丸（Poison Pill）导致的 Worker 重连无限死循环
- **文件**：`apps/coordinator/store.go`（第 406–509 行）、`apps/coordinator/server.go`（第 353–365 行）
- **问题**：当乱序事件（如 `worker_seq=2` 含非法状态跳变）先到达并落入 `worker_event_inbox` 后，随后到达的合法 `worker_seq=1` 触发 `for` 循环连带消费 `seq=2` 时，`commitPrefix` 虽然回滚了 `seq=2` 并提交了 `last_worker_seq=1`，但**未清除或隔离 `worker_event_inbox` 中的毒丸 `seq=2`**，且 `server.go:360` 立即发 `link.error` 断连。Worker 重连后发送任何后续事件都会在 `watermark+1`（即 `2`）再次撞上同一颗毒丸，陷入“重连 → 报错 → 断连”死循环。
- **修复要求**：
  1. 当 `AppendEvent` 在 `for` 循环中消费 `watermark+1` 遇到业务校验失败（如非法状态跳变或 `non-contiguous task_seq`）时，在回滚 `advance_event` savepoint 后，将该条非法的 `worker_event_inbox` 记录标记为已隔离（例如置 `payload=''`），同时推进 `watermark++`（若对应任务尚未终结，可自动将该任务置为 `failed` / `PROTOCOL_VIOLATION`），使后续 `worker_seq` 不再被单颗历史毒丸永久阻塞。
  2. 新增回归测试 `TestPoisonBufferedEventDoesNotWedgeSubsequentWorkerReconnect`：验证 `seq=2`（毒丸）先到、`seq=1`（合法）后到时，`seq=1` 被正常确认，且后续 `seq=3`（另一任务的合法事件）能正常推进而不陷入死循环。

### P2-B3：补齐 Windows `terminateOwned` 的错误返回与 Runner 进程自身强杀兜底
- **文件**：`apps/edge-agent/proc_windows.go`（第 59–96 行）
- **问题**：`terminateOwned` 在 `openJob(job)` 失败时直接 `return nil`；且 `spec.JobName` 仅包含用户子进程，不包含独立 Runner 进程本身（`id.PID`）。若 Runner 进程卡死，`TerminateJobObject` 无法终止 Runner。
- **修复要求**：
  1. 在 `terminateOwned` 中，若 `openJob(job)` 失败且 `treeBusy` / `sameProcess(id)` 仍为真，不得静默忽略；
  2. 在执行 `TerminateJobObject` 并等待 `grace`（最多 500ms）后，若 `sameProcess(id)` 仍然存活，通过 `windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(id.PID))` + `windows.TerminateProcess(h, 1)` 强制终止 Runner 进程本身。

### P2-B4：消除 `schedule()` 循环内的 N+1 活跃任务全表扫描
- **文件**：`apps/coordinator/scheduler.go`（第 36–95 行）
- **修复要求**：将统计全局活跃任务负载的 `SELECT payload FROM tasks WHERE status IN ('dispatched','accepted','running','waiting_approval')` 移到 `for _, id := range ids` 循环外执行一次，在内存构建 `deviceLoad` 与 `capLoad` 映射；当内层循环派发新任务时直接原地递增 `deviceLoad[chosen]++` 和 `capLoad[chosen+":"+t.Capability.ID]++`。

## 3. 里程碑 M-B：实现 HTTPS Artifact 分块断点续传、跨机产物挂载与脏工作区补丁（Phase 3 & Phase 5 完整闭环）

彻底移除 `apps/coordinator/store.go:132` 与 `apps/edge-agent/workspace.go:206` 中的 `UNSUPPORTED_PRECONDITION` 拦截，真正跑通 `docs/04_PROTOCOL_CONTRACT.md`（第 4.5、4.10 节）与 `docs/09_NETWORK_RELIABILITY.md`（第 9.5 节）定义的全部文件流转能力。

### M-B1：新增 `packages/artifact/` 与 Coordinator HTTPS 分块续传服务
1. **Coordinator SQLite 表扩展（`apps/coordinator/store.go`）**：
   ```sql
   CREATE TABLE IF NOT EXISTS artifacts(
     id TEXT PRIMARY KEY,
     plan_id TEXT NOT NULL,
     task_id TEXT NOT NULL,
     device_id TEXT NOT NULL,
     name TEXT NOT NULL,
     media_type TEXT NOT NULL,
     size_bytes INTEGER NOT NULL,
     sha256 TEXT NOT NULL,
     storage_path TEXT NOT NULL,
     created_at TEXT NOT NULL,
     UNIQUE(task_id, name)
   );
   CREATE TABLE IF NOT EXISTS artifact_uploads(
     upload_id TEXT PRIMARY KEY,
     artifact_id TEXT NOT NULL,
     plan_id TEXT NOT NULL,
     task_id TEXT NOT NULL,
     device_id TEXT NOT NULL,
     name TEXT NOT NULL,
     media_type TEXT NOT NULL,
     total_size INTEGER NOT NULL,
     expected_sha256 TEXT NOT NULL,
     chunk_size INTEGER NOT NULL,
     confirmed_offset INTEGER NOT NULL DEFAULT 0,
     temp_path TEXT NOT NULL,
     completed INTEGER NOT NULL DEFAULT 0,
     UNIQUE(task_id, name)
   );
   ```
2. **Coordinator HTTPS Artifact 接口（`apps/coordinator/`）**：
   - 支持 Controller Bearer Token（可上传如 `patch_artifact_id` 补丁、下载任意 Artifact）与 Worker Bearer Token（可为分配给自己的 Task 上传输出 Artifact、下载该 Task 所需的 `input_artifacts` 或 `patch_artifact_id`）。
   - `POST /v1/artifacts/uploads`：请求 `{plan_id, task_id, name, media_type, size_bytes, sha256}`。若同一 `(task_id, name)` 且相同 `sha256` 已存在上传会话，直接返回现有 `{upload_id, artifact_id, chunk_size: 4194304, confirmed_offset}` 以支持幂等续传；若内容冲突返回 `409 IDEMPOTENCY_CONFLICT`。
   - `PATCH /v1/artifacts/uploads/{upload_id}?offset=<N>`：校验请求方权限与 `offset == confirmed_offset`（不匹配返回 `409 OFFSET_MISMATCH` 并附当前 `confirmed_offset`）；将请求体写入 `temp_path` 对应偏移并 `Sync()`，更新 `confirmed_offset`，返回 `{upload_id, confirmed_offset}`。单块上限 4 MiB（`4 << 20`）。
   - `POST /v1/artifacts/uploads/{upload_id}/complete`：校验 `confirmed_offset == total_size` 且临时文件完整 `SHA-256 == expected_sha256`（不匹配返回 `400 ARTIFACT_HASH_MISMATCH`）；原子重命名为正式存储文件，写入 `artifacts` 表，并在 `task_events` 中写入一条 `artifact.ready` 事件（携带完整 `ArtifactRef`），返回 `ArtifactRef`。
   - `GET /v1/artifacts/{artifact_id}`：支持标准 HTTP `Range: bytes=<start>-<end>` 断点续传下载（返回 `206 Partial Content`、`Content-Range` 与 `ETag`）及全量 `200 OK` 下载。
   - 在 `loadPlan()` 中查询 `artifacts` 表，将每个 Task 已就绪的 `ArtifactRef` 列表填充到 `TaskSnapshot.Artifacts` 中。

### M-B2：Coordinator 端 `input_artifact_bindings` 动态解析与依赖门控
- 在 `apps/coordinator/scheduler.go` 中：
  - 当某个 `queued` 任务的所有 `depends_on` 前置任务均已 `succeeded` 时，检查其 `InputArtifactBindings`：
  - 对每个 `{from_task_id, name, mount_path}`，在 `artifacts` 表中查询 `WHERE task_id = from_task_id AND name = name`。
  - 若找不到对应产物，将该下游任务直接置为 `rejected`，错误码 `ARTIFACT_BINDING_FAILED`，并调用 `refreshPlan(tx, planID)`；
  - 若全部找到，将其追加转换为标准 `InputArtifact {ArtifactID, SHA256, MountPath}`，清空 `InputArtifactBindings`，持久化更新该任务的 `payload` 后派发给目标 Worker。

### M-B3：Worker 端输出产物上传（`output_artifacts` & `artifact.upload`）、`input_artifacts` 挂载与 `patch_artifact_id` 应用
1. **输出产物上传**：
   - 在 `TaskEnvelope`（或 `process.exec` 的 `input`）中支持声明 `output_artifacts: [{"path": "out/junit.xml", "name": "pytest-report.xml", "media_type": "application/xml"}]`，同时注册独立能力 `artifact.upload`。
   - 当 `process.exec` 退出码为 `0` 时，在发出 `task.succeeded` 之前，Worker 自动将声明的 `output_artifacts` 通过分块上传接口上传至 Coordinator（每块默认 4 MiB，遇网络错误从服务端 `confirmed_offset` 断点续传）；若文件不存在或上传失败，任务记为 `task.failed`（`ARTIFACT_UPLOAD_FAILED`）。
2. **`input_artifacts` 预检下载与挂载（`apps/edge-agent/workspace.go`）**：
   - 在 `prepareTask()` 中，对 `t.InputArtifacts` 中的每一项，先通过 `GET /v1/artifacts/{artifact_id}` 下载到临时文件（若中断则使用 `Range: bytes=<have>-` 续传），校验完整 `SHA-256 == item.SHA256`（不匹配报 `ARTIFACT_HASH_MISMATCH`，不存在报 `ARTIFACT_NOT_FOUND`），然后原子移动到任务工作目录的 `mount_path`（严格经过 `confinedPath` 校验并拒绝覆盖冲突）。
3. **`workspace_context.patch_artifact_id` 脏工作区补丁预检**：
   - 当 `w.PatchArtifactID != ""` 时，Worker 先验证宿主仓库 `HEAD == w.ExpectedGitHead`，创建干净的独立 detached worktree，下载并校验 `PatchArtifactID` 对应的补丁文件，然后在该独立 worktree 中执行 `git apply --binary --index <patch_file>`。若补丁无法干净应用，返回 `failed` / `WORKSPACE_MISMATCH`，绝不污染原仓库。

### M-B4：必测验收用例（`tests/e2e_phase3_artifact_test.go`）
- `TestArtifactChunkedUploadResumeAndRangeDownload`：构造一个 10 MiB（跨 3 个 4 MiB 分块）的二进制测试文件，上传到 50%（第 1 块完成、第 2 块中断）时断开连接，恢复后从 `confirmed_offset = 4194304` 续传剩余分块，验证最终 `SHA-256` 完全一致，且 `Range` 分片下载准确无误。
- `TestDAGArtifactBindingAndDirtyPatchWorkflow`：模拟 Controller 先上传一份 `git diff` 补丁作为 `patch_artifact_id`；Worker A 应用补丁并运行测试生成 `junit.xml` 上传为 Artifact；Worker B 应用同一补丁运行 `git.inspect` 审查 diff；下游任务 C 通过 `input_artifact_bindings` 自动挂载 Worker A 的 `junit.xml` 并在本地读取验证内容。

## 4. 里程碑 M-C：权限审批门禁（Approval Gate）与一次性配对码（Pairing）闭环（Phase 6 & Phase 7 核心）

对齐 `docs/06_SECURITY_AND_AUTHORITY.md` 与 `docs/04_PROTOCOL_CONTRACT.md`，补齐高风险操作审批与零配置设备配对：

1. **审批门禁（Approval Gate）**：
   - 在 `TaskEnvelope` 中支持 `RequiresApproval bool`（`json:"requires_approval,omitempty"`）与 `ApprovalReason string`（`json:"approval_reason,omitempty"`）。
   - 在 Coordinator SQLite 中新增 `approvals(id TEXT PRIMARY KEY, plan_id TEXT NOT NULL, task_id TEXT UNIQUE NOT NULL, device_id TEXT, scope TEXT NOT NULL, reason TEXT NOT NULL, status TEXT NOT NULL, decision_reason TEXT, created_at TEXT NOT NULL, resolved_at TEXT)`。
   - 当 `schedule()` 遇到前序依赖已就绪但 `RequiresApproval == true` 且尚未批准的 `queued` 任务时，不直接派发给 Worker，而是将其状态设为 `waiting_approval`，在 `approvals` 表生成待批记录，并在 `task_events` 发射 `approval.requested` 事件（此时 `orchestrator.Aggregate` 将无运行任务但有待批任务的 Plan 状态聚合为 `waiting_approval`）。
   - 新增 Controller HTTPS 接口 `GET /v1/approvals` 与 `POST /v1/approvals/{approval_id}/resolve`（请求体 `{"decision": "approved" | "denied", "reason": "..."}`），并在 `edge-cli` 中增加 `approvals` 与 `approve <id>` / `deny <id>` 子命令：
     - `approved`：更新审批记录为 `approved`，写入 `approval.resolved` 事件，将任务恢复为 `queued`（标记已批准），由下一轮调度立即派发；
     - `denied`：更新审批记录为 `denied`，写入 `approval.resolved` 事件，将任务置为 `rejected`（错误码 `APPROVAL_DENIED`），并触发 `refreshPlan` 使下游依赖任务自动收敛为 `DEPENDENCY_FAILED`。
2. **动态设备配对（One-Time Pairing Code）**：
   - 在 Coordinator SQLite 中新增 `pairings(code TEXT PRIMARY KEY, device_hint TEXT, expires_at INTEGER NOT NULL, used INTEGER NOT NULL DEFAULT 0)` 与 `device_credentials(device_id TEXT PRIMARY KEY, token_hash TEXT NOT NULL, created_at TEXT NOT NULL)`。
   - 新增 Controller 接口 `POST /v1/pairings`（生成 6 位或短一次性配对码及二维码 JSON 载荷 `{pairing_code, coordinator_url, expires_at}`，默认有效期 10 分钟）和公开兑换接口 `POST /v1/pairings/claim`（Worker 提交 `{pairing_code, device_id, display_name}`，Coordinator 原子核销配对码、生成并返回专属高熵 `worker_token`，持久化 `token_hash`）。
   - `edge-agent` 新增 `--pair <pairing_code>` 参数：首次启动时自动调用 `/v1/pairings/claim` 换取 `worker_token` 并加密/0600 保存在本地 SQLite `identity` 表中，后续启动直接读取本地保存的凭证连接 Coordinator，无需每次手动注入 `DSH_WORKER_TOKEN` 环境变量（同时完全向下兼容静态 `DSH_WORKER_TOKENS` 配置）。

## 5. 里程碑 M-D：实现 `integrations/dsh-cordis-plugin`（Node.js / TypeScript AI 工具层）

按照 `docs/02_ARCHITECTURE.md`（第 2.3.A 节），在 `integrations/dsh-cordis-plugin/` 下实现可直接挂载到 DeepSeek Harness（及 [ccch1mneyyy/dsh-go](https://github.com/ccch1mneyyy/dsh-go) 内置 Node.js 引擎）的 Cordis 插件包：

1. **目录与工程结构**：
   - `integrations/dsh-cordis-plugin/package.json`（零外部重依赖，使用 Node 18/22+ 原生 `fetch` / `https` 与 `node:test`）
   - `integrations/dsh-cordis-plugin/src/client.mjs`（封装访问 Coordinator HTTPS API 的客户端，支持自定义 CA 证书、Bearer 鉴权、Plan 提交、快照拉取、增量事件游标轮询、审批处理与 Artifact 上传/下载）
   - `integrations/dsh-cordis-plugin/src/index.mjs`（Cordis 插件入口，导出工具定义表与插件挂载函数 `apply(ctx, config)`）
2. **完整实现 8 个标准 `fleet.*` 工具（及审批辅助）**：
   - `fleet.list_devices`：调用 `GET /v1/devices` 返回在线/离线设备、平台、CPU/内存/磁盘余量与已授权能力摘要；
   - `fleet.inspect_device`：查询指定 `device_id` 的完整 Manifest，或提交单节点 `device.inspect` 任务并等待实时探测结果；
   - `fleet.list_capabilities`：汇总整个 Fleet 各节点支持的能力、SemVer 版本与并发上限；
   - `fleet.plan_tasks`：接收自然语言拆解出的多步任务列表（含 `depends_on`、`workspace_context`、`input_artifact_bindings`、`requires_approval`），做本地静态预校验后调用 `POST /v1/plans` 提交给 Coordinator，返回 `plan_id`、`coord_seq` 与初始卡片投影；
   - `fleet.dispatch_task`：将单个独立任务包装为单节点 `PlanEnvelope` 提交，支持可选等待完成或异步返回；
   - `fleet.get_task`：调用 `GET /v1/plans/{plan_id}` 与增量 `/v1/events` 获取任务最新状态、`pending_reason`、日志尾部摘要与产物引用；
   - `fleet.cancel_task`：调用 `POST /v1/plans/{plan_id}/cancel`（或发送 `process.cancel`）幂等取消任务；
   - `fleet.get_artifacts`：获取指定任务生成的 `ArtifactRef` 列表，并支持按需拉取文本报告内容或保存到本地工作区。
3. **跨语言真实联调测试（`integrations/dsh-cordis-plugin/test/plugin.test.mjs`）**：
   - 在 `node --test` 中直接拉起编译好的 Go `coordinator` 与 `edge-agent` 子进程，通过 `dsh-cordis-plugin` 的 8 个工具函数完成“查设备 → 提交多节点并行测试与 Git 检查 Plan → 查询进度与日志 → 下载汇总产物”的端到端断言，并将其加入 `scripts/check.sh`。

## 6. 里程碑 M-E：实现 `packages/edge-link-dart` 与 `integrations/dsh-flutter-ui`（Chat-First 卡片状态模型）

按照 `docs/10_CHAT_FIRST_UX.md`，在 `packages/edge-link-dart/` 和 `integrations/dsh-flutter-ui/` 下提供纯正、结构清晰的 Dart 客户端与聊天内嵌卡片状态机代码：

1. **`packages/edge-link-dart/`**：
   - `pubspec.yaml`、`lib/edge_link_dart.dart`、`lib/src/models.dart`、`lib/src/client.dart`、`lib/src/projection_store.dart`。
   - 严格实现 `docs/10_CHAT_FIRST_UX.md` 第 6 节的 **快照屏障与游标状态机（`FleetProjectionStore`）**：
     - 首次进入或 `CURSOR_EXPIRED` 时调用 `GET /v1/snapshot`，以 `snapshot_coord_seq` 建立全局基线；
     - 增量恢复时按固定 `high_watermark` 分页拉取 `/v1/events?after_coord_seq=...`，严格按 `coord_seq` 升序去重更新内存中的 `PlanCardModel` 与 `WorkerStepCardModel`；
     - 单张卡片刷新（`GET /v1/plans/{id}`）仅更新该卡片的 `cardBarrierSeq`，**绝不推进全局 `lastCoordSeq`**；
     - 严格实施 **L1 / L2 / L3 可见性防伪**：当节点无截图能力或未上报 `preview.available` 时，`WorkerStepCardModel.canPreviewScreen` 恒为 `false`，UI 绝不渲染假屏幕占位框。
2. **`integrations/dsh-flutter-ui/`**：
   - 提供可直接在 [ccch1mneyyy/dsh-go](https://github.com/ccch1mneyyy/dsh-go) 聊天消息流中挂载的 Flutter Widget 组件（`lib/dsh_flutter_ui.dart`：含 `FleetPlanOverviewCard`、`FleetWorkerExecutionCard`、`FleetApprovalBanner`、`FleetPairingSheet`），绑定 `plan.pause`（明确标注“暂停后续步骤，已运行进程继续”）、`plan.resume`、`plan.cancel` 与审批同意/拒绝回调。

## 7. 全局验收标准与交付要求（Definition of Done）

全部 5 个里程碑完成后，必须执行并通过以下全量检查：

1. **Schema 同步**：运行 `go run ./packages/protocol/cmd/schema > schemas/protocol.schema.json`，确保新增的 `RequiresApproval`、`ApprovalReason`、`OutputArtifacts`、`ArtifactUpload` 等结构体全部写入 Schema 且通过 Draft 2020-12 校验。
2. **全量测试与静态检查**：运行 `bash scripts/check.sh`（覆盖全部 Go 模块测试、`go vet` 以及 `node --test integrations/dsh-cordis-plugin/test/plugin.test.mjs`），100% 通过。
3. **并发竞态检测**：运行 `go test -race ./tests ./apps/coordinator ./apps/edge-agent ./packages/edge-link`，零竞态告警。
4. **三平台零 CGO 构建**：运行 `bash scripts/build.sh`，验证 `linux/amd64`、`linux/arm64`、`windows/amd64` 共 9 个二进制文件静态编译通过。
5. **端到端旗舰演示升级（`scripts/demo-v1-full.sh`）**：新增一键完整演示脚本——使用动态配对码配对 2 台 Worker、提交带脏补丁 `patch_artifact_id` + 并行单元测试输出 JUnit 文件产物 + Git diff 检查 + 下游挂载产物汇总的完整 DAG，验证全链路 `succeeded`。
6. **交付文档**：生成 `reports/V1_FULL_VALIDATION.md` 与 `docs/V1_QUICKSTART.md`，汇总 M-A 至 M-E 的全部改动文件、API 清单与实测结果。
