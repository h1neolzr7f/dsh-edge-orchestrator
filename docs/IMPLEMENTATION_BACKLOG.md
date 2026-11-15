# Implementation Backlog

按交付闭环组织，不以代码文件数量代替功能。优先做真实可用的跨设备执行链路。

## EPIC 0 — Edge Link Vertical Slice

- [ ] E0-1 Go Coordinator 提供 HTTPS/WSS 服务。
- [ ] E0-2 Go Agent 主动连接并完成 hello/welcome。
- [ ] E0-3 Device register + heartbeat。
- [ ] E0-4 Dart 最小客户端查看两个 Worker。
- [ ] E0-5 同 LAN 和公网 Coordinator 各验证一次。
- [ ] E0-6 断网 30 秒后自动恢复。

**出口：** 不依赖 DS Harness Remote，手机端可见两台 Worker，连接恢复可复现。

## EPIC 1 — Protocol & Persistence

- [ ] E1-1 Device / Capability / Task / Receipt / Event / Artifact Schema。
- [ ] E1-2 message_id、sequence、ack、resume cursor。
- [ ] E1-3 Coordinator SQLite schema。
- [ ] E1-4 Worker 本地 task ledger 与 event outbox。
- [ ] E1-5 协议版本协商和兼容测试。

## EPIC 2 — Edge Agent MVP

- [ ] E2-1 Windows/Linux/ARM 单文件 Agent。
- [ ] E2-2 Device Inspector 和能力自动发现。
- [ ] E2-3 process.exec。
- [ ] E2-4 filesystem.read/write。
- [ ] E2-5 git.inspect。
- [ ] E2-6 cancel / timeout / exit code。
- [ ] E2-7 Artifact upload 与 SHA-256。

## EPIC 3 — Coordinator Task Runtime

- [ ] E3-1 task.submit / accepted / rejected。
- [ ] E3-2 持久化任务队列和离线 Worker 待派发。
- [ ] E3-3 at-least-once 投递和幂等去重。
- [ ] E3-4 Event outbox / ACK / replay。
- [ ] E3-5 Task snapshot / reconcile。
- [ ] E3-6 Artifact 分块续传。

## EPIC 4 — Orchestrator

- [ ] E4-1 Plan / Task DAG。
- [ ] E4-2 capability matching。
- [ ] E4-3 资源评分与并发上限。
- [ ] E4-4 多 Worker 并行派发。
- [ ] E4-5 dependency unlock。
- [ ] E4-6 result aggregation。

## EPIC 5 — DSH Go Integration

- [ ] E5-1 Cordis Fleet 插件。
- [ ] E5-2 Dart Edge Link client。
- [ ] E5-3 对话内任务总览卡 + 至少两个节点执行卡；设备总览与能力详情为次级入口。
- [ ] E5-4 自然语言/语音委派、计划预览、运行中追加指令；不强制先选主机。
- [ ] E5-5 对话中显示真实任务进度；展开任务时间线、日志、证据文件与结果。
- [ ] E5-6 审批、暂停/继续、取消和产物下载；手机离线恢复原任务卡片。
- [ ] E5-7 保留 DSH Go 原有手机操控和全部模型/插件能力。

- [ ] E5-8 对话与 plan_id 绑定、事件快照/游标同步；Fleet 关闭时原 DSH Go 聊天完全不变。
- [ ] E5-9 执行可见性分级：V1 日志/事件，按能力截图预览，桌面人工接管单独验收。

## EPIC 6 — Edge Expansion

- [ ] E6-1 Linux ARM/SBC。
- [ ] E6-2 Android Worker / Kotlin service。
- [ ] E6-3 Browser / SSH / Docker。
- [ ] E6-4 ADB / Accessibility。
- [ ] E6-5 Serial / MQTT / GPIO。
- [ ] E6-6 Capability Pack SDK。

## EPIC 7 — Productization

- [ ] E7-1 Windows Service 安装器。
- [ ] E7-2 Linux systemd 安装包。
- [ ] E7-3 Coordinator Docker Compose。
- [ ] E7-4 QR 配对与设备组。
- [ ] E7-5 连接质量、诊断包和任务历史。
- [ ] E7-6 自托管部署文档。

## 第一版演示

手机输入：“检查仓库，A 执行测试，B 审查 diff，最后汇总。”

验收必须显示自动选择设备及依据、task_id/capability/执行节点、并行时间窗口、断网恢复、产物哈希与最终汇总。
