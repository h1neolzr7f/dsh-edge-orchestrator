# 03. 开发计划与里程碑

## 总体策略

不先重写 DSH Go，不先做复杂大屏，不先 fork Remote。先完成一个可真实使用的垂直闭环：一台手机发出目标，两台 PC Worker 通过 Coordinator 收到任务并行执行，手机断网后回来仍能看到原任务和结果。

采用激进但可验证的开发方式：先做真实链路，不用大量抽象层预留不存在的需求。每个阶段交付可运行代码和可复现演示。

## Phase 0 — Edge Link 最小连通（1–2 天）

目标：不依赖 DS Harness Remote，证明手机、Coordinator、两个 Worker 可以稳定连通。

实现：
1. Go Coordinator 提供 HTTPS/WSS endpoint。
2. Go Edge Agent 主动连接 Coordinator，完成 device hello、身份注册和 heartbeat。
3. packages/edge-link-dart 最小客户端能查看在线设备，Flutter UI 与 Node.js Cordis 插件分开。
4. 同 LAN 与跨公网两种部署模式各跑一次。
5. 断网 30 秒再恢复，观察连接恢复和状态续接。

技术选择先定 WSS/TLS；只有遇到明确性能或兼容性问题才换 gRPC/QUIC。Phase 0 是尽快做出可测原型，不做长时间调研。

出口：一台手机客户端看到两个在线 Worker，网络中断后自动恢复；不要求 Remote 参与。

## Phase 1 — 协议、Coordinator 与 Agent

交付：
- Device / Capability / Plan / Task / Receipt / Event / Artifact Schema，含 workspace_context、input_artifacts 与三层序号。
- Coordinator 设备注册、配对、连接管理、Plan/Task 持久化和在线状态；常驻内嵌 packages/orchestrator。
- Edge Agent 本地身份、系统探测、心跳和本地 SQLite local_task_ledger 与持久化 worker_seq。
- device.inspect capability。
- Fake Worker 与协议测试；Phase 0/1 的闭环门槛提前包含最小 DAG：两前置并行、一后继自动解锁、Controller 离线恢复。完整调度评分在 Phase 3 扩展。

出口：真实 Worker 安装 Agent 后自动登记，Controller 可读取平台、架构、CPU、内存、磁盘和 Agent 版本。

## Phase 2 — 任务执行闭环

交付：
- task.submit / accepted / started / progress / succeeded / failed。
- Worker 本地任务队列和持久化事件 outbox。
- process.exec、filesystem.read/write、git.inspect。
- workspace_context 预检、输入产物下载校验、磁盘日志与进程身份三元组。
- Linux 进程组、Windows Job Object 的取消/超时及 Agent 崩溃恢复。
- 同 task_id / idempotency_key 重复提交返回原任务，不重复启动进程。

出口：从手机提交一个命令，在 Worker 上执行，返回退出码、日志和产物。

## Phase 3 — 多设备调度与并行

交付：
- 扩展 Coordinator 内嵌 packages/orchestrator 的 Plan / Task DAG；手机只生成并提交计划。
- Capability matching、资源评分、并发控制。
- 任务投递队列和离线 Worker 待处理任务。
- 两台 Worker 并行执行；依赖任务在上游完成后解锁。
- unknown 状态 reconcile；Worker 事件按 (device_id, worker_seq) 去重，Controller 按 coord_seq 恢复。
- 显式下游汇总任务消费上游 Artifact；Controller 离线时也由 Coordinator 派发。

出口：手机输入“A 电脑运行测试，B 电脑审查 diff”，两个任务时间窗口重叠，最终返回带证据的汇总。

## Phase 4 — DSH Go 产品集成（Chat-first，参考 Grok Bot 交互）

交付：
- integrations/dsh-cordis-plugin：TypeScript / Node.js 引擎层 8 个 Fleet 工具。
- integrations/dsh-flutter-ui：Flutter 卡片与配对页；依赖 packages/edge-link-dart 订阅快照及事件。
- 保留 DSH Go 聊天/语音为默认入口；聊天内嵌总任务卡、节点执行卡；设备列表/详情是次级页面。
- 自然语言/语音目标、计划预览、运行中追加关联工作（新 Plan 绑定原聊天）、必要时指定 Worker。
- 聊天中的进度、节点日志摘要、失败原因、结果卡片与可展开时间线。
- 取消、重试、审批、产物查看。
- 保留 DSH Go 原有模型配置、Agent Loop、聊天、语音、Phone Control 和插件生态。

出口：用户只在 DSH Go 对话中交代一次任务，无需先选择或切换主机；能实时看到两个节点的真实执行进展、审批并收取结果。详细标准见 docs/10_CHAT_FIRST_UX.md。

## Phase 5 — 网络可靠性与文件传输

交付：
- 指数退避 + jitter、heartbeat、连接 sequence/ack、Worker last_worker_seq 与 Controller last_coord_seq。
- Wi-Fi/移动网络切换后重连。
- Worker 离线任务排队；Controller 离线不影响已接受任务及 Coordinator 后继解锁。
- Artifact 分块上传/下载、断点续传、SHA-256 校验。
- 连接状态、延迟、最近错误和任务同步状态可视化。
- 可导出的诊断包。

出口：网络中断、重连和大文件传输测试通过；不能因为一条日志流卡住整个控制通道。

## Phase 6 — 自主探索与更多设备

顺序：
1. Linux ARM / SBC Worker。
2. Android Worker（系统信息、文件、ADB、Accessibility、摄像头/音频等已授权能力）。
3. Browser / SSH / Docker / GPU 等工具适配。
4. Serial、MQTT、USB、GPIO 和机器人设备适配。
5. 设备能力插件包，可独立安装、升级和卸载。

出口：新设备加入后自动发现平台和工具，用户授权后就能被调度；增加一种能力不要求改 Orchestrator 核心。

## Phase 7 — 分发与生态

- Windows 安装包与开机自启服务。
- Linux deb/rpm/单文件安装。
- Android 配对入口与 Worker 模式。
- Docker Compose 一键部署 Coordinator。
- QR 配对、设备组、标签和工作区模板。
- 插件 SDK、示例能力和第三方 Worker 文档。

## 第一版开发顺序

1. Edge Link + Coordinator + 两个 Go Worker。
2. Task protocol + local task ledger。
3. 多设备任务 DAG 和并行执行。
4. 断线恢复与 Artifact transfer。
5. DSH Go Cordis 集成和界面。
6. Android/ARM 扩展。

先做通信和执行闭环，后做 UI 精修。不要把 UI 做完才发现任务无法跨网络持续运行。

## 暂不投入

- 不把 DS Harness Remote 作为 V1 的必需依赖。
- 不先开发 P2P/ICE/TURN/QUIC。
- 不先引入 Kubernetes、复杂微服务或分布式数据库。
- 不做多控制器一致性和领导者选举。
- 不让 LLM 直接承担高频硬件闭环控制。
- 不为极低概率异常堆多层框架；优先实现协议、任务持久化、权限和重连这些核心不变量。
