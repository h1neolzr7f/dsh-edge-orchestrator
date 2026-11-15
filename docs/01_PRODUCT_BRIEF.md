# 01. 产品定义与范围

## 1.1 产品定位

DSH Edge Orchestrator 是面向个人开发者、AI Agent 用户和边缘设备开发者的跨设备 Agent 编排系统。用户向手机或 PC 上的 DSH Agent 表达目标，系统识别可用设备及其能力，把工作拆分并分配到合适节点。

典型体验：

> “检查这个项目。A 电脑负责代码审查，B 电脑跑测试，Linux 小主机收集服务日志，最后汇总问题并给出修复顺序。”

系统自动完成：
1. 读取已配对设备的能力和负载；
2. 将目标转成任务 DAG；
3. 按能力与资源选择节点；
4. 并发派发可并行任务；
5. 收集状态、日志、文件和结构化结果；
6. 解锁依赖任务；
7. 对需要用户决定的动作请求审批；
8. 返回带证据和产物引用的结果。

## 1.2 核心用户与场景

### 个人开发者
- 手机统一控制家中多台电脑。
- 一台机器开发，另一台运行测试或静态分析。
- VPS 负责部署和日志，家中 PC 负责构建。
- 按设备的 GPU、内存、工具和网络条件分配工作。

### 边缘设备开发者
- 给 Linux SBC、旧手机或 Android 设备安装 Edge Agent。
- Agent 自主读取 CPU、内存、系统版本、网络、工具和可识别外设。
- 控制端按能力选择设备，而不是按品牌或型号写死逻辑。

### 自动化与机器人
- 手机负责视觉、语音和高层规划。
- 边缘主机负责传感器采集和执行器控制。
- PC/云端负责重计算、模型推理、仿真和数据分析。
- LLM 负责高层任务，不承担毫秒级运动闭环。

## 1.3 产品核心对象

| 对象 | 定义 | 关键字段 |
|---|---|---|
| Device | 物理或虚拟节点 | device_id、name、platform、online、last_seen |
| Agent | 节点上的 Edge 执行进程 | agent_version、runtime、health、capabilities |
| Capability | 节点可执行动作 | capability_id、version、schema、permission、resource_cost |
| Plan | 用户目标对应的任务计划 | plan_id、goal、tasks、dependencies、status |
| Task | 独立派发和追踪的工作单元 | task_id、plan_id、target、input、deadline、idempotency_key |
| Event | 任务过程的追加记录 | event_id、task_id、worker_seq、task_seq、coord_seq、type、timestamp、payload |
| Artifact | 文件或结构化产物 | artifact_id、sha256、size、media_type、storage_uri |
| Approval | 需要用户确认的授权动作 | approval_id、task_id、scope、reason、decision |

## 1.4 V1 交付目标

- 一台 Android DSH Go 控制端管理至少两台 Worker。
- Worker 自动上报系统信息和能力清单。
- 编排器把一个目标拆成至少 3 个有依赖/并行关系的任务。
- 两台 Worker 能同时执行不同任务。
- 控制端展示任务状态和每个 Worker 的结果。
- 节点断线后，已接受任务继续运行；恢复后补取状态。
- 结果可引用文件产物，不只返回聊天文本。
- 网络质量有可见状态，弱网不导致 UI 整体卡死。

## 1.5 V1 不做

- Kubernetes 级容器编排和自动扩缩容。
- 多主一致性和复杂分布式数据库。
- V1 就实现 P2P、TURN、QUIC 和自研 VPN。
- 让 LLM 直接控制高频硬件闭环。
- 一次覆盖所有桌面、移动和 RTOS 平台。
- 无边界任意 Shell 执行。
- 先做大屏 UI，再补任务执行链路。

## 1.6 北极星指标

**从自然语言目标到多个设备完成有证据的协作结果所需的时间。**

辅助指标：任务派发成功率、并行利用率、断线恢复成功率、结果可追溯率、人工介入次数、弱网下控制操作成功率和单位任务传输开销。
