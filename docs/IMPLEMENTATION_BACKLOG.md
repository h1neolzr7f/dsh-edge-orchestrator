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

- [ ] E1-1 Device / Capability / Plan / Task / Receipt / Event / Artifact Schema，含 plan.*、workspace_context、input_artifacts 与 input_artifact_bindings。
- [ ] E1-2 稳定 message_id；连接 sequence/ack、按 device_id 全局 worker_seq、全局 coord_seq；Controller last_coord_seq 与 Worker last_worker_seq 分别恢复。
- [ ] E1-3 Coordinator SQLite schema：task_events.coord_seq 自增主键、UNIQUE(device_id, worker_seq)、worker_event_inbox 乱序缓冲、Plan/Task 幂等唯一约束和依赖解锁事务。
- [ ] E1-4 Worker 本地 local_task_ledger 与 event_outbox；进程身份三元组、磁盘日志及事务化 tail 偏移。
- [ ] E1-5 协议版本协商和兼容测试。
- [ ] E1-6 Go 1.22+ go.work、纯 Go SQLite、CGO_ENABLED=0 三平台交叉编译。
- [ ] E1-7 tests/e2e_phase0_phase1_test.go：两 Worker、两前置一汇总、Controller 离线自动解锁、幂等一次执行、单 last_coord_seq 无重复漏事件。

## EPIC 2 — Edge Agent MVP

- [ ] E2-1 Windows/Linux/ARM 单文件 Agent。
- [ ] E2-2 Device Inspector 和能力自动发现。
- [ ] E2-3 process.exec。
- [ ] E2-4 filesystem.read/write。
- [ ] E2-5 git.inspect。
- [ ] E2-6 Linux Setpgid 进程组 + SIGTERM/5秒/SIGKILL、Windows Job Object + TerminateJobObject；runner、PID 防复用、Agent 崩溃恢复与退出状态。
- [ ] E2-7 Artifact upload 与 SHA-256。
- [ ] E2-8 workspace_context 基准校验与隔离补丁；input_artifacts 下载、哈希及安全挂载，预检成功才 running。

## EPIC 3 — Coordinator Task Runtime

- [ ] E3-1 task.submit / accepted / rejected。
- [ ] E3-2 持久化任务队列和离线 Worker 待派发。
- [ ] E3-3 at-least-once 投递和幂等去重。
- [ ] E3-4 event_outbox / event.ack {last_worker_seq} / coord_seq replay；一致快照屏障与游标过期。
- [ ] E3-5 Task snapshot / reconcile。
- [ ] E3-6 Artifact 分块续传。

## EPIC 4 — Orchestrator

- [ ] E4-1 常驻 Coordinator 内嵌 packages/orchestrator；plan.submit 事务接收及 DAG 校验。
- [ ] E4-2 capability matching。
- [ ] E4-3 资源评分与并发上限。
- [ ] E4-4 多 Worker 并行派发。
- [ ] E4-5 Controller 离线自动 dependency unlock；失败传播、取消与 replay_policy 对账。
- [ ] E4-6 下游汇总任务、input_artifact_bindings 解析及不可变产物哈希传递。

## EPIC 5 — DSH Go Integration

- [ ] E5-1 TypeScript Cordis Fleet 插件（Node.js 引擎层）：integrations/dsh-cordis-plugin，注册规范的 8 个工具。
- [ ] E5-2 Dart Edge Link Client 与 Flutter 聊天内嵌卡片组件：packages/edge-link-dart + integrations/dsh-flutter-ui。
- [ ] E5-3 对话内任务总览卡 + 至少两个节点执行卡；设备总览与能力详情为次级入口。
- [ ] E5-4 自然语言/语音委派、计划预览、运行中追加指令；不强制先选主机。
- [ ] E5-5 对话中显示真实任务进度；展开任务时间线、日志、证据文件与结果。
- [ ] E5-6 审批、暂停后续调度/继续、取消和产物下载；手机离线恢复原任务卡片。
- [ ] E5-7 保留 DSH Go 原有手机操控和全部模型/插件能力。

- [ ] E5-8 对话与 plan_id 绑定、snapshot_coord_seq / last_coord_seq 事件同步；Fleet 关闭时原 DSH Go 聊天完全不变。
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
