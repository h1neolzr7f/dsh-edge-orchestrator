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

新增 Fleet/Orchestrator Cordis 插件，向 Agent 暴露工具：fleet.list_devices、fleet.inspect_device、fleet.list_capabilities、fleet.plan_tasks、fleet.dispatch_task、fleet.get_task、fleet.cancel_task、fleet.get_artifacts。

不新建第二个 Agent Loop。模型仍由原有 DSH Agent 规划和判断；Fleet 只提供可调用的设备与任务工具。

### B. Edge Coordinator

Coordinator 是跨设备通信与任务状态的稳定汇合点，可运行在常开 PC、Linux 小主机或自托管 VPS。

职责：
- 设备注册、配对和在线状态；
- 为每个设备维护独立逻辑连接和连接租约；
- 接收任务并投递给目标 Worker；
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

任务生命周期属于 Worker Agent，而不是 WebSocket 连接或手机进程。

1. Controller 生成 plan_id、task_id 和 idempotency_key。
2. Coordinator 保存 Task Envelope，并尝试投递。
3. Worker 收到后先写入本地 Task Ledger，再返回 accepted。
4. Worker 启动执行器；任务运行期间将事件写入本地 outbox。
5. Coordinator 确认事件后推进 event cursor。
6. Controller 断线不影响 Worker；恢复后从 Coordinator 读取快照和事件。
7. 对未知状态先 reconcile 原 task_id，不直接创建新任务重跑。

交付语义是 at-least-once delivery + Worker 幂等去重，不宣称 exactly-once。

## 2.6 任务调度

V1 采用确定性调度。候选节点必须满足 capability、版本、授权和最低资源条件，再按资源余量、网络位置、当前负载和可用性排序。

任务图支持独立任务并行、depends_on 依赖、节点标签与能力约束、最大并发数、按 replay_policy 重试，以及 waiting_approval。

## 2.7 数据模型与存储

Coordinator SQLite 表：devices、device_capabilities、device_connections、plans、tasks、task_dependencies、task_events、dispatch_outbox、approvals、artifacts、artifact_uploads。

Worker 本地保存 identity、task_ledger、event_outbox、artifact_upload_queue、capability_cache。

事件使用单调递增 seq。Controller 重连先拉 snapshot，再从 last_event_seq 补事件。

## 2.8 推荐代码结构

~~~text
dsh-edge-orchestrator/
├── apps/
│   ├── coordinator/             # Go 服务：设备接入、任务路由、事件、Artifact API
│   ├── edge-agent/              # Go 常驻 Worker
│   └── edge-cli/                # 配对、诊断、手工任务和开发调试
├── packages/
│   ├── protocol/                # JSON Schema / message envelope
│   ├── orchestrator/            # DAG、调度、状态汇总
│   ├── edge-link/               # Go Link Manager / reconnect / ACK
│   ├── capability-runtime/      # Adapter registry / executor
│   └── artifact/                # 分块传输 / hash / resume
├── integrations/
│   ├── dsh-go/                  # Cordis plugin 与 Dart client
│   ├── android/                 # Kotlin Android adapter
│   ├── windows/
│   └── linux/
├── schemas/
├── examples/
├── tests/
└── docs/
~~~

## 2.9 技术选型

- DSH Go UI 与 Agent 集成：Flutter/Dart。
- Coordinator 与通用 Edge Agent：Go，优先单二进制、低资源、易部署。
- Android 专属 API：Kotlin；复用 DSH Go 已有手机操控代码。
- 控制协议：JSON Envelope + WebSocket/TLS；协议版本显式协商。
- Artifact：HTTPS 分块上传/下载，SHA-256 校验。
- 数据库：SQLite 起步，接口留出 PostgreSQL 迁移空间。

不为语言统一重写 DSH Go，也不把所有逻辑塞进 Flutter 页面。

## 2.10 演进路线

- V1：单 Coordinator、多 Worker、主动出站连接、任务并行、断线恢复。
- V1.5：LAN 直连与 Relay 自动选择、连接质量统计、多路事件流优化。
- V2：可选 P2P/QUIC、多个控制端共享设备目录、分层 Coordinator。
- V3：机器人、传感器、执行器与实时控制网关。毫秒级控制闭环由设备本地控制器负责，LLM 只下发高层目标。
