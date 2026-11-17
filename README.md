# DSH Edge Orchestrator

**让手机、PC、服务器和边缘设备组成一个可被 AI 统一调度的协作网络。**

DSH Edge Orchestrator 是围绕 DSH Go 建设的跨设备 Agent 编排系统。它让一台手机或 PC 上的 DSH Agent 能发现其他设备、理解设备能力、拆分工作、并行派发任务，并收集可验证的结果。

它不是远程桌面，也不是 SSH 管理面板。核心抽象是：**设备是执行节点，能力是可组合工具，任务是持久化工作单元，编排器负责跨设备协作。**

> 当前状态：V1 运行时与 DSH 接入包已实现。包含可续传产物、隔离补丁执行、审批/动态配对、8个 Cordis 工具、Dart 状态同步与 Flutter 聊天卡片。见 [V1 运行指南](docs/V1_QUICKSTART.md) 与 [完整验证报告](reports/V1_FULL_VALIDATION.md)。接入包已测试，尚未构建修改版上游 APK。

## 重要架构决定：不把 DS Harness Remote 作为核心通信依赖

项目发起者的实际使用反馈是：现有 Remote 经常卡退，网络不佳时连接体验不稳定。因此本项目不应把它作为 V1 的关键路径，也不应在其上继续叠加复杂编排。

新的路线：

- DSH Go：保留原有 Agent、Cordis 插件、模型渠道、Android 本机操控和移动端交互。
- DSH Edge Link：项目自有的轻量通信层，负责连接、心跳、重连、消息确认、状态恢复和文件传输。
- Edge Coordinator：运行在常开 PC、服务器或 VPS 上，负责节点登记、Plan DAG 依赖解锁、任务路由和事件汇集。
- Edge Agent：运行在 Windows/Linux/ARM 设备上的常驻执行节点；Android 使用原生适配器。
- DS Harness Remote：仅作为可选兼容适配器和代码/协议参考，不在核心链路中。

## 交互原则：像 Grok Bot 一样委派工作

**聊天是默认入口，Fleet 在背后自主调度设备。** 用户只需自然语言/语音交代目标；聊天中出现跨设备任务卡、执行进度和结果。任务持续运行并支持网络恢复。能否看画面、能否人工接管取决于设备适配器，不以空白预览假冒已实现能力。详见 [交互体验规范](docs/10_CHAT_FIRST_UX.md)。

## 现在可以运行

```bash
bash scripts/demo-v1-full.sh
```

一个 HTTPS Coordinator、两台动态配对 Worker，执行带真实补丁的并行单元测试与 Git diff，将 JUnit 产物挂载给下游任务汇总。演示同时验证审批、产物完整性和实际执行时间重叠。旧 Phase2 指南和脚本保留用于基础回归。

## V1 要交付什么

1. DSH Go 手机端作为统一控制入口。
2. 一台 Coordinator 可管理至少两台 Windows/Linux Worker。
3. Worker 安装 Edge Agent 后能自主盘点系统、运行环境、工具和已连接外设。
4. 用户能授予设备能力；AI 能按能力和资源拆分任务。
5. 两台以上 Worker 能并行执行不同任务。
6. 任务、事件和产物有稳定 ID；控制端断线后能恢复状态。
7. 网络短时中断不会杀死 Worker 已接受的任务。
8. 大文件使用可续传传输，不挤占控制消息通道。

## V1 网络拓扑

~~~mermaid
flowchart TB
  PHONE[DSH Go 手机控制端] -->|HTTPS / WSS| COORD[Edge Coordinator]
  PC[PC 控制端 / CLI] -->|HTTPS / WSS| COORD
  COORD <-->|持久 WSS| A[Windows Worker]
  COORD <-->|持久 WSS| B[Linux / ARM Worker]
  COORD <-->|持久 WSS| C[Android Edge Adapter]
  A --> A1[本机工具 / 文件 / Git]
  B --> B1[脚本 / 服务 / 外设]
  C --> C1[Android API / ADB / Accessibility]
  A1 --> ART[Artifact Store]
  B1 --> ART
  C1 --> ART
  ART --> COORD
~~~

- 同一局域网：Coordinator 可运行在本地常开 PC，手机和 Worker 直接连接 LAN 地址。
- 跨网络：Worker 和手机都主动连接自托管 Coordinator/VPS，不要求设备开放入站端口。
- V1 不依赖 P2P、TURN 或端口映射；它们是后续可选的传输优化，不是基础功能前提。

## 技术路线

- Controller：Node.js/TypeScript Cordis Fleet 插件负责工具调用；Flutter/Dart UI 通过独立 Edge Link client 接入快照和事件。
- Coordinator / Edge Agent：优先采用 Go，构建单文件、低依赖的 Windows/Linux/ARM 部署包。
- Android 原生能力：Kotlin Adapter，复用 DSH Go 已有手机操控能力。
- 控制通道：TLS 之上的 WebSocket，采用单节点逻辑连接、消息 ID、ACK、心跳、恢复游标和任务幂等。
- 文件通道：独立 HTTPS 分块上传/下载，支持断点续传和 SHA-256 校验。
- 数据：Coordinator SQLite 起步；Worker 本地 SQLite local_task_ledger、事件 outbox 与磁盘日志。需要多人或多控制器时再迁移 PostgreSQL。

## 文档

- [产品定义](docs/01_PRODUCT_BRIEF.md)
- [系统架构](docs/02_ARCHITECTURE.md)
- [开发计划与里程碑](docs/03_EXECUTION_PLAN.md)
- [设备、任务与事件协议](docs/04_PROTOCOL_CONTRACT.md)
- [Edge Agent 设计](docs/05_DEVICE_AGENT.md)
- [权限与执行权模型](docs/06_SECURITY_AND_AUTHORITY.md)
- [验收与测试计划](docs/07_VALIDATION.md)
- [开源复用与技术调研](docs/08_REUSE_AND_RESEARCH.md)
- [网络可靠性设计](docs/09_NETWORK_RELIABILITY.md)
- [Grok Bot 式交互体验规范](docs/10_CHAT_FIRST_UX.md)
- [实施 Backlog](docs/IMPLEMENTATION_BACKLOG.md)
- [第一阶段变更核对清单](docs/CHANGELOG_PHASE1_CONTRACTS.md)
- [Phase 0/1 运行指南](docs/PHASE1_QUICKSTART.md)
- [Phase 2 运行指南](docs/PHASE2_QUICKSTART.md)
- [第二轮变更与验收报告](reports/PHASE2_VALIDATION.md)
- [架构决策记录](docs/decisions/)

## 第一版演示

用户在手机上输入：“检查这个项目，A 电脑运行测试，B 电脑审查 diff，最后汇总。”

- A：运行测试并上传 JUnit 报告。
- B：检查 diff、运行静态分析并上传 JSON 报告。
- 两台 Worker 并行工作。
- Coordinator 保存任务和事件；手机短暂断网后重新连接，继续看到原任务进度。
- Coordinator 自动派发显式下游汇总任务，即使手机离线也能生成报告；DSH Agent 恢复后将证据总结到聊天。

验收标准是完整任务闭环、真实并行和网络恢复，不是设备列表能显示。
