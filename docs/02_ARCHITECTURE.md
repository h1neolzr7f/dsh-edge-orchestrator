# 02. 系统架构

## 2.1 设计目标

系统必须保留 DSH Go 的移动端 Agent 优势，同时让不同设备成为可调度的执行节点。架构首先优化实际可用性：连接建立快、网络波动可恢复、任务不依赖控制端常在线、传输大文件不拖垮控制通道。

**核心原则：编排协议与传输实现分离；DS Harness Remote 不处于 V1 核心链路。**

## 2.2 四个逻辑平面

1. **Control Plane**：DSH Go UI、设备列表、目标输入、计划预览、审批和结果呈现。
2. **Orchestration Plane**：任务 DAG、能力匹配、资源调度、Task Ledger、事件汇总和产物索引。
3. **Execution Plane**：Edge Agent、能力适配器、本机命令/文件/浏览器/Android/外设执行器。
4. **Transport Plane**：Edge Link、Coordinator Gateway、配对认证、消息 ACK、重连恢复和 Artifact Transfer。

## 2.3 组件职责

### A. DSH Go Controller

保留原有 DSH Go 的 Agent Loop、Cordis 插件体系、模型渠道、会话、语音、打断、审批和 Android Phone Control。

`integrations/dsh-cordis-plugin/` 使用 TypeScript / Node.js，运行在手机 PRoot Ubuntu 内的 DSH Node.js 引擎中，向现有 Agent Loop 注册恰好 8 个工具：fleet.list_devices、fleet.inspect_device、fleet.list_capabilities、fleet.plan_tasks、fleet.dispatch_task、fleet.get_task、fleet.cancel_task、fleet.get_artifacts。

插件通过本地回环（仅同机 Coordinator）或 HTTPS/WSS 访问 Coordinator。fleet.plan_tasks 生成 Plan 预览并在提交时发送 plan.submit；fleet.dispatch_task 将单任务包装为单节点 Plan 后提交，不在手机维护依赖队列。

`integrations/dsh-flutter-ui/` 使用 Flutter/Dart，负责绑定 plan_id 的任务总览卡、绑定 task_id + device_id 的节点卡、审批卡、扫码配对页及次级设备状态页。它通过 `packages/edge-link-dart/` 订阅 Coordinator 快照和事件流；Android 系统桥接由 Kotlin 承担。Flutter 页面不加载 Cordis，也不承担后台调度。

不新建第二个 Agent Loop。DSH Agent（LLM）在 Controller 端生成 Plan DAG，通过 plan.submit 提交后由 Coordinator 独立推进。

### B. Edge Coordinator

Coordinator 是跨设备通信与任务状态的稳定汇合点，可运行在常开 PC、Linux 小主机或自托管 VPS。

职责：
- 设备注册、配对和在线状态；
- 为每个设备维护独立逻辑连接和连接租约；
- 原子接收 plan.submit，持久化 Plan DAG，内嵌 Go packages/orchestrator；
- 自主完成拓扑校验、depends_on 依赖解锁、能力匹配、并发门控与重试状态机；
- 将已解锁任务投递给目标 Worker；
- 持久化任务、Receipt 和事件；
- Worker 离线时保存待投递任务；
- 设备重连后恢复未确认消息、事件游标和任务快照；
- 提供独立 Artifact 上传/下载接口。

Coordinator 不负责 LLM 推理，也不要求 Worker 彼此互联。

### C. Edge Agent

Edge Agent 是 Windows/Linux/ARM 设备上的常驻进程。它负责本机探测、能力上报、任务持久化、能力执行、事件日志和断线续传。已接受任务不依赖手机或 Coordinator 一直在线。

### D. DSH Edge Link

V1 使用 TLS 之上的 WebSocket 作为控制通道。Dart、Go、Android 和常见反向代理均有成熟支持，适合快速落地。

一个 Edge Agent 对 Coordinator 保持一个逻辑 Control Connection。连接承载心跳、任务投递、Receipt、状态事件和轻量日志。协议层支持 ACK、序号、恢复游标和幂等键。

连接管理由 Link Manager 统一处理：在线/降级/重连/离线状态、心跳与超时、指数退避加随机抖动、网络切换重连、按最后确认序号恢复、消息去重。

### E. Artifact Transfer

大文件不能通过控制 WebSocket 长时间传输。Worker 通过独立 HTTPS 接口分块上传，Coordinator 返回上传会话和已确认偏移量；断线后从偏移量续传。上传完成后验证 SHA-256，再发送 Artifact Ref。

## 2.4 网络拓扑与部署模式

### Local LAN 模式（V1 首选）

Coordinator 运行在一台常开 PC/Linux 主机上。手机、PC Worker 和边缘设备连接该主机的 LAN 地址。局域网发现可使用 mDNS 或二维码地址交换；发现只负责找到 Coordinator，不承担任务协议。

### Remote / WAN 模式（V1 同一架构）

Coordinator 运行在 VPS 或可公网访问的自托管服务器。手机和 Worker 都主动建立出站 TLS/WSS 连接，因此不要求设备有公网 IP，也不要求用户配置路由器端口转发。

### P2P 模式（后续）

只有在测量证明 Relay 带宽或延迟成为瓶颈后，才增加 WebRTC/QUIC/P2P 直连。P2P 失败时仍回退到 Coordinator Relay。V1 不将 ICE、TURN、NAT 穿透作为交付阻塞项。

## 2.5 任务执行模型

任务执行生命周期属于 Worker Agent；已提交 Plan 的调度生命周期属于常驻 Go Coordinator，二者都不属于 WebSocket 连接或手机进程。

1. Controller 生成 plan_id、task_id 和 idempotency_key。
2. Coordinator 原子保存 Plan、Task、依赖与创建事件，校验 DAG，匹配能力并派发无未完成前置的任务。
3. Worker 收到后先写入本地 Task Ledger，再返回 accepted。
4. Worker 预检 workspace_context、下载校验 input_artifacts 后启动执行器；事件先入本地 outbox。
5. Coordinator 按 (device_id, worker_seq) 去重落库，分配 coord_seq，提交事务后返回 event.ack {last_worker_seq}；前置成功及产物 ready 后自动解锁后继。
6. Controller 断线不影响 Worker 或 Coordinator 的 DAG 推进；恢复后按 snapshot_coord_seq / last_coord_seq 读取一致快照与增量。
7. 对未知状态先 reconcile 原 task_id，不直接创建新任务重跑。

交付语义是 at-least-once delivery + Worker 幂等去重，不宣称 exactly-once。

## 2.6 任务调度

调度器常驻于 Coordinator 内嵌 packages/orchestrator，手机不参与依赖解锁或重试计时。V1 采用确定性调度。候选节点必须满足 capability、版本、授权和最低资源条件，再按资源余量、网络位置、当前负载和可用性排序。

任务图支持独立并行、depends_on、能力约束、concurrency_limit、waiting_approval，以及按 replay_policy 对账后重投递。已接受任务不自动重执行；具体状态、依赖失败传播、取消与幂等规则以 04_PROTOCOL_CONTRACT.md 为准。汇总步骤必须是可由 Worker 执行的显式下游任务；手机恢复后再生成聊天总结。

## 2.7 数据模型与存储

Coordinator SQLite 表：devices、device_capabilities、device_connections、plans、tasks、task_dependencies、task_events、worker_event_inbox、dispatch_outbox、approvals、artifacts、artifact_uploads。

Coordinator 的 task_events 使用 `coord_seq INTEGER PRIMARY KEY AUTOINCREMENT`，可空的 worker_seq 配合 UNIQUE(device_id, worker_seq) 去重；Plan 自身事件无 Worker 序号。plans 按 Fleet 内幂等键唯一，tasks 按 task_id 及幂等键唯一。乱序节点事件先持久化到 worker_event_inbox，按连续 worker_seq 顺序进入 task_events；事件发布、状态变化与依赖解锁同事务提交。

Worker 本地保存 identity、local_task_ledger、event_outbox、artifact_upload_queue、capability_cache。local_task_ledger 记录 (pid, process_start_time_ns, os_boot_id, stdout_path, stderr_path)、日志偏移、进程组或 Job 标识和退出状态路径。

三层序号严格分离：连接 sequence/ack 重连重置；worker_seq 按 device_id 全局持久递增；coord_seq 跨所有设备及 Plan 全局递增。Controller 用 last_coord_seq 恢复，Worker 用 last_worker_seq 补 outbox。快照携带同一读事务的 snapshot_coord_seq，再读大于此值的事件，不能漏掉快照与订阅之间的变化。

## 2.8 推荐代码结构

~~~text
dsh-edge-orchestrator/
├── apps/
│   ├── coordinator/             # Go 服务：设备接入、任务路由、事件、Artifact API
│   ├── edge-agent/              # Go 常驻 Worker
│   └── edge-cli/                # 配对、诊断、手工任务和开发调试
├── packages/
│   ├── protocol/                # JSON Schema / message envelope
│   ├── orchestrator/            # 常驻 Coordinator 内的 DAG、调度、状态汇总
│   ├── edge-link/               # Go Link Manager / reconnect / ACK
│   ├── edge-link-dart/          # Flutter 快照及事件流客户端
│   ├── capability-runtime/      # Adapter registry / executor
│   └── artifact/                # 分块传输 / hash / resume
├── integrations/
│   ├── dsh-cordis-plugin/       # TypeScript / Node.js Cordis Fleet 工具
│   ├── dsh-flutter-ui/          # Flutter 聊天任务卡、审批与配对界面
│   ├── android/                 # Kotlin Android adapter
│   ├── windows/
│   └── linux/
├── schemas/
├── examples/
├── tests/
└── docs/
~~~

## 2.9 技术选型

- DSH Go 引擎集成：TypeScript / Node.js Cordis 插件（PRoot Ubuntu 内）。
- DSH Go UI：Flutter/Dart，通过 packages/edge-link-dart 订阅快照与事件。
- Coordinator 与通用 Edge Agent：Go，优先单二进制、低资源、易部署。
- Android 专属 API：Kotlin；复用 DSH Go 已有手机操控代码。
- 控制协议：JSON Envelope + WebSocket/TLS；协议版本显式协商。
- Artifact：HTTPS 分块上传/下载，SHA-256 校验。
- 数据库：SQLite 起步，Go 驱动使用 modernc.org/sqlite 等纯 Go 实现，CGO_ENABLED=0；接口留出 PostgreSQL 迁移空间。
- Go 1.22+ Workspace：go.work 管理 apps/coordinator、apps/edge-agent、apps/edge-cli、packages/protocol、packages/edge-link；packages/orchestrator 在引入时加入工作区，由 Coordinator 内嵌。

不为语言统一重写 DSH Go，也不把所有逻辑塞进 Flutter 页面。

## 2.10 演进路线

- V1：单 Coordinator、多 Worker、主动出站连接、任务并行、断线恢复。
- V1.5：LAN 直连与 Relay 自动选择、连接质量统计、多路事件流优化。
- V2：可选 P2P/QUIC、多个控制端共享设备目录、分层 Coordinator。
- V3：机器人、传感器、执行器与实时控制网关。毫秒级控制闭环由设备本地控制器负责，LLM 只下发高层目标。
