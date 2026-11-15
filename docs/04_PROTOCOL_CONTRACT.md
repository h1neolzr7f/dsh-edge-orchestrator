# 04. 设备、任务与通信协议

V1 使用 TLS/WSS 控制通道及独立 HTTPS Artifact 通道。本文是字段、状态与恢复语义的规范来源；本契约覆盖完整 V1。Phase 0/1 已实现的子集与暂不支持字段见 [运行指南](PHASE1_QUICKSTART.md)，不能把全文视为已全部实现。

## 4.1 统一消息 Envelope

~~~json
{
  "protocol": "dsh-edge/1",
  "message_id": "msg_01J_submit",
  "kind": "plan.submit",
  "sent_at": "2026-11-15T12:00:00Z",
  "correlation_id": "plan_01J_review",
  "sequence": 104,
  "ack": 103,
  "payload": {}
}
~~~

序号严格分为三层：

| 字段 | 分配者和作用域 | 持久化与恢复 |
|---|---|---|
| sequence / ack | 每条 WebSocket 物理连接、每个方向独立 | sequence 从 1 起，ack 初值 0，确认本连接对端最高连续帧号；重连 sequence 从 1、ack 从 0 重新开始，不用于业务恢复 |
| worker_seq | Worker，按 device_id 全局递增 | 本地 event_outbox 分配；跨任务、连接、Agent 重启不重置 |
| coord_seq | Coordinator，全局递增 | task_events 主键；Controller 用单一 last_coord_seq 恢复所有设备及 Plan 事件 |

`message_id` 是持久化控制消息的稳定 ID，重发不变；`correlation_id` 关联 Plan、Task 或请求。旧连接未确认消息重封装时保留 message_id，分配新 sequence。帧 ack 仅证明收帧，不证明业务落库，不能据此删除事件。link.ack 合并发送，不产生无限 ACK 回声。

序号均为非负整数，JSON 数值上限 9007199254740991，避免 Dart/JavaScript 精度分歧。0 表示尚无记录，实际帧号和事件序号从 1 起。全局 coord_seq 严格递增但不要求整数连续；task_seq 只用于任务内展示。

## 4.2 连接握手

Controller 的 client.hello payload：

~~~json
{
  "role": "controller",
  "device_id": "dev_01J_phone",
  "agent_version": "0.1.0",
  "protocol_versions": ["dsh-edge/1"],
  "last_coord_seq": 1040,
  "pending_message_ids": ["msg_01J_submit"]
}
~~~

Worker 的 client.hello payload：

~~~json
{
  "role": "worker",
  "device_id": "dev_01J_pc_a",
  "agent_version": "0.1.0",
  "protocol_versions": ["dsh-edge/1"],
  "last_worker_seq": 87,
  "pending_message_ids": []
}
~~~

1. 先验证配对身份与短期连接凭证，再接受 client.hello。Worker 上报的 last_worker_seq 是本地保存的 Coordinator 最高连续事件确认值，不是最新生成值。
2. server.welcome 返回 connection_id、server_time、selected_protocol、heartbeat_interval（秒，默认 20）和 resume。Controller 的 resume 为 `{ "after_coord_seq": 1040, "high_watermark": 1050, "snapshot_required": false }`；Worker 的 resume 为 `{ "last_worker_seq": 87 }`，后者以 Coordinator 持久化记录为准。
3. Worker 通过 device.register / device.registered 更新清单和能力 revision；Controller 不注册执行能力。
4. link.ready 后恢复业务流：Controller 按 4.9 回放全局事件；Worker 补发 worker_seq 大于确认水位的 outbox。

link.resume 使用相同 role、device_id 和对应业务游标字段，在已认证连接上重新对账。Controller 不用 last_worker_seq 恢复视图，Worker 不用 last_coord_seq 恢复 outbox。首次连接相应游标为 0。

身份密钥在首次配对时生成，短期令牌不替代设备身份。Worker 数据库丢失必须重新配对并分配新 device_id，不能用旧身份把 worker_seq 重置为 1。

## 4.3 Device Manifest

~~~json
{
  "device_id": "dev_01J_pc_a",
  "display_name": "build-pc",
  "platform": {"os": "windows", "arch": "x86_64", "version": "11"},
  "agent_version": "0.1.0",
  "labels": ["desktop", "gpu"],
  "resources": {"cpu_threads": 16, "memory_bytes": 34359738368, "disk_free_bytes": 500000000000},
  "capabilities_revision": 4
}
~~~

## 4.4 Capability Manifest

~~~json
{
  "capability_id": "process.exec",
  "version": "1.0.0",
  "description": "Run an approved process on this Worker",
  "input_schema": {"type": "object", "required": ["program", "args"]},
  "output_schema": {"type": "object"},
  "permission_scope": "process.exec",
  "resource_class": "cpu",
  "max_concurrency": 2,
  "discovery": "static",
  "authorized": true
}
~~~

能力状态区分 detected、available、authorized；探测不等于授权。Manifest 版本使用 SemVer；任务约束 `>=1.0 <2.0` 表示大于等于 1.0.0 且小于 2.0.0。

### 4.4.1 Plan Envelope（任务计划报文）

DSH Agent（LLM）在 Controller 生成 Plan DAG，通过 plan.submit 提交。**常驻 Go Coordinator 内嵌 packages/orchestrator，独立负责拓扑校验、depends_on 依赖解锁、能力匹配、并发门控、重试状态机与状态汇总。** 手机离线不阻止已提交 DAG 推进。需要新的模型判断时必须显式建立可调度任务，不能依赖手机隐式回调。

~~~json
{
  "plan_id": "plan_01J_review",
  "idempotency_key": "session_01J_chat:turn_12",
  "goal": "检查这个项目，A 电脑运行测试，B 电脑审查 diff，最后汇总",
  "concurrency_limit": 4,
  "created_at": "2026-11-15T12:00:00Z",
  "tasks": [
    {
      "task_id": "task_01J_test",
      "target_device_id": "dev_01J_pc_a",
      "capability": {"id": "process.exec", "version": ">=1.0 <2.0"},
      "depends_on": [],
      "input": {"program": "pytest", "args": ["-q", "--junitxml=out/junit.xml"], "cwd": "workspace/project"}
    },
    {
      "task_id": "task_01J_diff",
      "target_device_id": "dev_01J_pc_b",
      "capability": {"id": "git.inspect", "version": ">=1.0 <2.0"},
      "depends_on": [],
      "input": {"mode": "diff", "cwd": "workspace/project"}
    }
  ]
}
~~~

上述 Plan 字段必填，concurrency_limit 为正整数；每个任务必填 task_id、capability、depends_on、input，target_device_id 可省略，由 Coordinator 匹配。提交前校验无重复任务 ID、无缺失依赖、无环、能力约束与输入合法；依赖限同一 Plan，task_id 在 Coordinator 内全局唯一。不合法整份拒绝，不部分创建。

Plan、Task、依赖及 plan.created 事件原子落库后，返回 `plan.accepted {plan_id, status, coord_seq}`。失败返回 `plan.rejected {plan_id, code, message}`。Plan idempotency_key 在已认证 Fleet 内唯一；重复且业务内容一致返回原 Plan 当前状态，内容不同返回 IDEMPOTENCY_CONFLICT。比较包含全部 Plan 字段，忽略 JSON 对象键顺序。Coordinator 补齐任务缺省 plan_id 与 idempotency_key，后者为 `<plan_id>:<task_id>`。

Plan 状态为 queued、running、waiting_approval、succeeded、failed、cancelled：有运行任务为 running；无运行任务且存在未决审批为 waiting_approval；其余未完成为 queued。Plan 在所有任务终结后按固定优先级收敛：已请求 plan.cancel 则 cancelled；否则有 failed/rejected 则 failed；否则有 cancelled 则 cancelled；否则全部 succeeded 则 succeeded。单独 task.cancel 也必收敛，不能永远 queued。取消请求阻止新派发，向已派发任务发 task.cancel，收齐终态后才结束。终态不改写。

只有前置任务全部 succeeded 且输入产物 ready 才解锁后继。前置 failed/rejected/cancelled 使未派发后继 rejected / DEPENDENCY_FAILED；独立分支继续。并发上限统计 dispatched、accepted、running、waiting_approval，另受能力与授权额度限制。Coordinator 不以 Controller 在线状态作为调度条件。

V1 的暂停/继续指调度门控：plan.pause / plan.resume 使用 `{plan_id}`，幂等设置持久化 scheduling_paused（默认 false），返回 plan.snapshot 并在值变化时生成 plan.updated。暂停仅阻止新派发，已接受进程继续；UI 必须标注“暂停后续步骤”。取消优先于恢复，终态不允许恢复。它不改变 DAG 结构或基础 Plan 状态，不声称能挂起任意运行进程。

V1 已接受 DAG 不原地改结构；追加工作建立新 Plan，在同一聊天关联。已接受任务不能无对账迁移 Worker。plan.updated 是状态投影，不是编辑命令。

## 4.5 Task Envelope

~~~json
{
  "task_id": "task_01J_summary",
  "plan_id": "plan_01J_review",
  "idempotency_key": "plan_01J_review:task_01J_summary",
  "target_device_id": "dev_01J_pc_a",
  "capability": {"id": "process.exec", "version": ">=1.0 <2.0"},
  "input": {"program": "python", "args": ["tools/summarize.py", "inputs/pytest-report.xml"], "cwd": "workspace/project"},
  "depends_on": ["task_01J_test", "task_01J_diff"],
  "workspace_context": {
    "root_dir": "workspace/project",
    "expected_git_head": "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678",
    "patch_artifact_id": "art_01J_dirty_diff",
    "policy": "require_clean_or_match"
  },
  "input_artifacts": [
    {
      "artifact_id": "art_01J_report",
      "sha256": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
      "mount_path": "inputs/pytest-report.xml"
    }
  ],
  "priority": "normal",
  "deadline": "2026-11-15T14:00:00Z",
  "replay_policy": "never",
  "requested_permissions": ["process.exec"]
}
~~~

本例是给 4.4.1 的两个前置任务追加下游汇总节点时，其首次派发的完整 Task Envelope；上游产物已 ready，绑定已解析。

workspace_context 与 input_artifacts 是标准可选字段，独立于 capability input。未传工作区字段不要求 Git 仓库。路径按目标 Worker 授权工作区映射解析，不假定各机绝对路径相同。

### 工作区预检

- 指定 workspace_context 时 root_dir、expected_git_head、policy 必填。HEAD 必须是完整 Git 对象 ID（40 或 64 位十六进制），不接受缩写；V1 policy 固定 require_clean_or_match。
- 无补丁时要求 HEAD 一致、index/worktree 干净且无未跟踪文件，否则 WORKSPACE_MISMATCH。V1 默认不自动 fetch、checkout、reset 或清理用户目录。
- 有 patch_artifact_id 时，Coordinator 从 ready Artifact Ref 取得哈希；Worker 下载校验，在匹配 HEAD 的干净任务隔离 worktree 应用补丁。只认同一基准叠加同一补丁，失败返回 WORKSPACE_MISMATCH，不修改原工作区。二进制使用 Git binary patch；未跟踪文件显式收入补丁或输入产物。
- 检查与执行使用同一任务隔离 worktree，input.cwd 映射到其中。预检完成前不得启动用户程序。

### 输入产物与 DAG 绑定

- input_artifacts 每项必填 artifact_id、sha256、mount_path；哈希为 64 位小写十六进制。Worker 在 accepted → running 前通过 `GET /v1/artifacts/{artifact_id}` 下载，支持 Range 续传；先写临时文件，校验后原子改名。
- mount_path 相对任务工作目录，拒绝绝对路径、`..`、符号链接逃逸和覆盖冲突。输入与隔离 worktree 位于同一任务运行目录，程序可直接读取 inputs 下文件。
- 不存在或哈希失败分别为 ARTIFACT_NOT_FOUND / ARTIFACT_HASH_MISMATCH。中断下载保持 accepted 并续传；到 deadline 则 failed / DEADLINE_EXCEEDED。预检失败不进入 running。
- 上游产物 ID 未知时，Plan 中任务可带 input_artifact_bindings，每项格式为 `{ "from_task_id": "task_01J_test", "name": "pytest-report.xml", "mount_path": "inputs/pytest-report.xml" }`。来源任务必须属于 depends_on 的传递前置集合。
- Coordinator 在上游 succeeded 后按 `(task_id, name)` 唯一定位 ready 产物，生成标准 input_artifacts，在首次派发前持久化，重投递保持相同 ID 与哈希。缺失或重名使后继 rejected / ARTIFACT_BINDING_FAILED；Worker 不接收未解析绑定，不依赖跨机共享路径。

## 4.6 Receipt 与状态

~~~json
{
  "task_id": "task_01J_test",
  "device_id": "dev_01J_pc_a",
  "status": "accepted",
  "accepted_at": "2026-11-15T12:01:00Z",
  "task_seq": 0
}
~~~

任务状态：planned → queued → dispatched → accepted → running → succeeded/failed/cancelled。派发前验证/依赖失败为 rejected；accepted 后预检失败为 failed。queued 或 accepted 可进入 waiting_approval，批准回到原状态；拒绝为 rejected（未接受）或 failed（已接受）。unknown 只是 Controller 观测状态，不写入执行事实。

Worker 返回 accepted 前原子写入 local_task_ledger、原始 Receipt 与幂等映射。相同 idempotency_key、相同业务内容返回原 Receipt，可另查当前快照；不得再启进程。相同键不同内容返回 IDEMPOTENCY_CONFLICT。短期 message_id 缓存不替代持久任务去重。

task.reconcile 的 payload 为 `{task_id}`，返回 task.snapshot（task_id、status、task_seq、error、artifacts），不存在返回 TASK_NOT_FOUND。Coordinator 先查原 Worker，再决定是否重投递。replay_policy 仅支持 never（默认）与 safe：两者都可重传同一请求，但不自动重执行已接受任务；safe 仅在确认原任务不存在或明确未执行的拒绝后允许再派发。已执行任务需重跑时显式建立新任务。

task.cancel 的 payload 为 `{task_id}`，请求幂等；未派发直接 cancelled，已接受任务先清理受管进程树再产生终态。deadline 由 Worker 本地执行，超时清理后 failed / DEADLINE_EXCEEDED。错误对象统一 `{code, message}`；恢复后无可信退出状态为 failed / interrupted_by_restart，详见 05 与 ADR-004。

## 4.7 Event

Coordinator 对 Controller 发布的事件：

~~~json
{
  "event_id": "evt_01J_progress",
  "plan_id": "plan_01J_review",
  "task_id": "task_01J_test",
  "device_id": "dev_01J_pc_a",
  "coord_seq": 1042,
  "worker_seq": 88,
  "task_seq": 12,
  "type": "task.progress",
  "timestamp": "2026-11-15T12:03:00Z",
  "payload": {"message": "Tests running", "completed": 24, "total": 80}
}
~~~

Worker 在 event_outbox 事务中按 device_id 分配 worker_seq 并更新任务 task_seq；发送的 event.append 不带 coord_seq。Coordinator 先以 UNIQUE(device_id, worker_seq) 写持久化 worker_event_inbox。只有从上次连续水位开始无缺口的事件才按 worker_seq 顺序进入 task_events、分配全局 coord_seq 并更新 Task/Plan 与依赖队列；这些操作和连续水位更新同事务提交。先到 90、缺 89 时，90 留在 inbox，不发布到 Controller、不推进任务终态或解锁下游；补齐 89 后依次处理 89、90。task_events 同样保留 UNIQUE(device_id, worker_seq)。重复返回原确认，不新增 coord_seq 或二次解锁；同一键不同内容返回 EVENT_CONFLICT。终态不被较旧 task_seq 覆盖。

事务提交后才返回 `event.ack {"last_worker_seq": 88}`，只确认最高连续节点事件；收到 90 但缺 89 时仍确认 88。Worker 仅按此值清理 outbox。已分配序号的事件不能合并丢弃，限频合并必须发生在分配前。

Coordinator 自身 Plan 状态事件也写 task_events 并分配 coord_seq，但不带 device_id、worker_seq、task_seq，可无 task_id，以 plan_id 关联。Controller 按 coord_seq 去重升序应用；task_seq 仅展示，不能恢复多设备事件。

## 4.8 V1 消息类型与 UI 映射

| 分类 | Envelope.kind |
|---|---|
| link | client.hello、server.welcome、link.ready、link.ping、link.pong、link.ack、link.resume |
| device | device.register、device.registered、device.heartbeat、device.capabilities.changed |
| plan | plan.submit、plan.accepted、plan.rejected、plan.cancel、plan.snapshot、plan.pause、plan.resume |
| task | task.submit、task.accepted、task.rejected、task.cancel、task.snapshot、task.reconcile |
| event | event.append、event.ack、event.replay |
| approval | approval.request、approval.resolve |
| artifact | artifact.create_upload、artifact.upload_progress、artifact.complete、artifact.download |

plan.snapshot 请求 `{plan_id}`，响应同 kind，以 correlation_id 关联，包含 Plan、任务列表、snapshot_coord_seq。单 Plan 快照仅替换该卡片，不推进 Controller 的全局 last_coord_seq；卡片记录其自身屏障，忽略已被该快照覆盖的旧事件，而全局恢复仍需应用其他 Plan 的事件。plan.cancel 请求 `{plan_id}`，幂等处理并返回 plan.snapshot；状态语义见 4.4.1。

UI 事件通过持久化事件流呈现，plan.created/updated/completed 是 Event.type，不是控制 kind：

| 业务来源 | UI 投影 | 约束 |
|---|---|---|
| 首次 plan.submit 成功 | plan.created | 与 Plan 同事务；重复提交不重复创建 |
| 调度、审批、任务状态变化 | plan.updated | Coordinator 聚合后持久化 |
| Plan 首次进入 succeeded/failed/cancelled | plan.completed | 每 Plan 一次，payload.status 为实际终态 |
| plan.rejected | 请求错误 | 无 Plan，不生成 created/completed |
| plan.snapshot | 完整任务卡 | 替换至 snapshot_coord_seq，再续事件 |

Worker 的 task.started/progress/log/succeeded/failed/cancelled 映射 UI step.started/progress/finished；step.assigned 来自派发记录。审批和产物映射 approval.requested/resolved、artifact.ready。step/node/preview/handoff 是 UI 投影名，只有对应持久化事实及适配器存在时展示，不生成第二份任务终态或额外 worker_seq。详见 10_CHAT_FIRST_UX.md。

## 4.9 网络与重放语义

- 控制消息 at-least-once，稳定 message_id 去重；业务 Receipt / event.ack 才证明落库，连接 ack 不证明执行。
- Controller 在握手或 link.resume 上传 last_coord_seq，Coordinator 以 `WHERE coord_seq > :last_coord_seq ORDER BY coord_seq ASC` 一次补齐所有设备与 Plan 事件；V1 为单 Fleet，查询仍受身份授权约束。
- event.replay 请求 `{ "after_coord_seq": 1040, "limit": 500 }`，响应 `{ "events": [], "next_coord_seq": 1040, "high_watermark": 1040, "has_more": false }`。同一恢复批次固定 high_watermark，先分页补齐到该水位，再接实时 coord_seq 大于该水位的事件。非空页游标只能推进到已应用事件，空页且无剩余可见事件时可推进到水位。投影与 last_coord_seq 原子保存。
- 首次进入或游标超出保留范围时，经 GET /v1/snapshot 在同一读事务取得当前 Fleet 的完整状态（设备、Plans、Tasks、审批、产物引用）和 snapshot_coord_seq = S，替换投影并设 last_coord_seq = S，再回放 coord_seq > S。实时订阅缓存屏障后事件或持续从数据库补拉，不能在快照与订阅间漏事件。
- 游标过期为 CURSOR_EXPIRED，要求快照；错误实例或超出当前水位为 CURSOR_INVALID，不能静默跳过。全局主键可有空洞，不以相邻整数判断漏序。
- Worker 按 Coordinator last_worker_seq 补 outbox。Coordinator 水位低于 Worker 曾保存的确认值时报告 EVENT_HISTORY_LOST 并停止自动重放，不能静默丢数据或重置身份。
- 未确认控制消息持久化重传，重连重新分配帧号；超时先对账原 task_id。心跳不载业务数据，日志独立限流。
- Controller 离线不影响 Coordinator 解锁下游；Coordinator 离线时 Worker 继续已接受任务，跨设备后继待 Coordinator 恢复再派发。
- 大文件仅走 HTTPS，artifact.* 控制报文只交换会话与元数据。

## 4.10 Artifact Ref

~~~json
{
  "artifact_id": "art_01J_report",
  "task_id": "task_01J_test",
  "name": "pytest-report.xml",
  "media_type": "application/xml",
  "size_bytes": 0,
  "sha256": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
  "download_uri": "/v1/artifacts/art_01J_report"
}
~~~

示例为真实空文件哈希；生产值必须匹配文件。分块默认 4 MiB，记录确认 offset，完成后校验总大小和 SHA-256，才标记 ready 并发布 artifact.ready。ready 内容不可覆盖；(task_id, name) 唯一。任务要求的输出必须在 succeeded 前上传完成，失败则任务不能报成功。下载为鉴权的 GET /v1/artifacts/{artifact_id}，支持 Range；WSS artifact.download 只返回引用，不内嵌文件。
