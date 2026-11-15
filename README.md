# DSH Edge Orchestrator

**让手机、PC 与边缘设备组成一个可被 AI 统一调度的协作网络。**

DSH Edge Orchestrator 是围绕 DSH Go 建设的跨设备 Agent 编排层。它将设备发现、能力登记、任务拆解、跨设备派发、并行执行、结果汇总和人工接管组织成完整工作流。

它不是另一个远程桌面，也不是 SSH 面板。核心抽象是：**设备是执行节点，能力是可组合工具，任务是可追踪工作单元，编排器负责把用户目标变成跨设备协作。**

> 当前阶段：Architecture / Planning。仓库文档描述目标架构和实施计划，不代表功能已经实现。

## V1 要交付什么

- Android DSH Go 作为统一控制端。
- 至少两台 Windows/Linux PC 作为 Worker。
- Worker 安装 Agent 后能自描述平台、资源和已授权能力。
- AI 将一个目标拆成有依赖关系的任务 DAG。
- 调度器按 capability、资源和负载分配任务。
- 多个 Worker 能同时执行任务。
- 控制端能查看状态、事件、日志摘要和文件产物。
- 控制端断线后可恢复任务视图，不重复执行已接受任务。

## 关键技术路线

1. **复用 DS Harness Remote**：优先复用设备身份、配对、Noise 加密和 LAN/P2P/TURN/Relay 传输。
2. **复用 DSH Go**：将编排核心作为独立 Dart 包集成，不把任务调度逻辑散落在 Flutter 页面中。
3. **自研真正的差异化部分**：Device/Capability Registry、Task DAG、Scheduler、Task Ledger、Event Collector、Artifact Index。
4. **V1 顺序派发、并行执行**：当前 Remote 的同一 Client 身份不能假定可同时保持多个 Host Control 连接。先逐个建立短连接提交任务，Worker 接受后独立运行，再回收状态和结果。
5. **任务与连接解耦**：任务生命周期归 Worker Agent，不能依赖 Controller 常连。

## 开发前必须验证的关键点

现有 Remote Runtime 有短连接访问目标 Host 的模式，但“Control 连接关闭后远程任务是否持续执行”必须在真实 Harness 后端实测。Phase 0 将用一台 Controller 和两台 Worker 验证任务并行、断线恢复和幂等。如果 Session 会随连接关闭而终止，则由常驻 Edge Agent 承担任务生命周期，不在 UI 层绕过问题。

## 文档

- [产品定义](docs/01_PRODUCT_BRIEF.md)
- [系统架构](docs/02_ARCHITECTURE.md)
- [开发计划与里程碑](docs/03_EXECUTION_PLAN.md)
- [任务与设备协议](docs/04_PROTOCOL_CONTRACT.md)
- [Edge Agent 设计](docs/05_DEVICE_AGENT.md)
- [权限与执行权模型](docs/06_SECURITY_AND_AUTHORITY.md)
- [验收与测试计划](docs/07_VALIDATION.md)
- [开源复用与技术调研](docs/08_REUSE_AND_RESEARCH.md)
- [实施 Backlog](docs/IMPLEMENTATION_BACKLOG.md)
- [架构决策记录](docs/decisions/)

## 参考项目

- [DSH Go](https://github.com/ccch1mneyyy/dsh-Go)
- [DS Harness Remote](https://github.com/liguobao/ds-harness-remote)

## 第一版演示

用户输入：“检查这个项目，A 电脑运行测试，B 电脑审查 diff，最后汇总。”

- A：执行测试并输出 JUnit 报告。
- B：执行静态分析、审查 diff 并输出 JSON。
- Controller：等待两项任务完成，汇总证据和产物。

**验收以真实双 Worker 并行执行和断线恢复为准，不以设备列表能显示作为完成。**

## 仓库初始化文档包

完整 17 文件的中文开发计划、架构、协议、Agent、安全、验收和 ADR 文档已整理成初始化包，可从本次对话下载并解压到仓库根目录。
