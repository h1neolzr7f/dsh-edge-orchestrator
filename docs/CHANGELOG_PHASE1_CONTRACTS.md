# 第一阶段变更核对清单

日期：2026-11-15。基线：5bbe32532fe2b29cfe4178976f08ba29feee61f6。

本轮交付为文档与协议契约修订，不包含 Go 模块、JSON Schema 文件或可运行服务。第二阶段须在用户确认文档并要求写代码后开展。以下“闭环”仅表示文档规则及代码验收条件已明确。

| 工程盲区 | 文档闭环状态 | 主要落点 |
|---|---|---|
| M1 双层集成与开源边界 | 已拆分 Node.js/TypeScript Cordis 工具和 Flutter/Dart UI，保留 8 个 Fleet 工具；核对 AGPL、SDK MIT 与受限素材 | 02、03、05、08、Backlog、Setup |
| M2 DAG 驻留及 Plan 协议 | Controller 生成，Coordinator 内嵌 orchestrator 校验、匹配、解锁与重试；Plan 事务接收及终态规则完整 | 02、04、10、ADR-003 |
| M3 序号与恢复 | 连接帧、节点事件、全局事件三层独立；乱序缓冲、连续 ACK、一致快照屏障及单游标恢复 | 02、04、09 |
| M4 工作区与产物 | HEAD/补丁校验、隔离执行、输入 SHA-256 与挂载；上游绑定解析成不可变 Artifact 引用 | 04、05、07 |
| M5 进程存续与清理 | 身份三元组、磁盘日志、tail 偏移、独立 runner；Linux 进程组及 Windows Job Object | 05、07、ADR-004 |

## 修改文件

| 路径 | 内容 |
|---|---|
| README.md | 双层集成、Coordinator DAG 及离线汇总入口说明 |
| docs/01_PRODUCT_BRIEF.md | Event 对象改用 worker_seq / task_seq / coord_seq |
| docs/02_ARCHITECTURE.md | 职责、调度、存储、三层序号、目录及技术选型 |
| docs/03_EXECUTION_PLAN.md | 各阶段与新契约对齐；Phase 0/1 提前最小 DAG 验收 |
| docs/04_PROTOCOL_CONTRACT.md | Plan/Task/Event 完整契约、消息、幂等、状态、工作区、产物、恢复 |
| docs/05_DEVICE_AGENT.md | 预检生命周期、日志、runner、身份及取消恢复 |
| docs/07_VALIDATION.md | 五类盲区验收矩阵、Phase 0/1 E2E 和原生平台边界 |
| docs/08_REUSE_AND_RESEARCH.md | 上游真实双层架构及许可/品牌/素材分发边界 |
| docs/09_NETWORK_RELIABILITY.md | 分角色重连、三层确认、乱序与快照恢复 |
| docs/10_CHAT_FIRST_UX.md | Plan 控制与 UI 投影映射、全局/单卡快照、调度暂停语义 |
| docs/IMPLEMENTATION_BACKLOG.md | EPIC 1–5 与新契约逐项对齐 |
| docs/REPO_SETUP.md | 新目录、Go Workspace、纯 Go SQLite 和交叉编译约束 |
| docs/decisions/ADR-003-controller-first.md | 计划生成与常驻调度职责分离 |
| docs/decisions/ADR-004-task-lifetime.md | 进程身份、日志恢复与进程树清理决策 |
| docs/CHANGELOG_PHASE1_CONTRACTS.md | 本变更核对清单 |

## 新增及统一字段

- Plan：plan_id、idempotency_key、goal、concurrency_limit、created_at、tasks；scheduling_paused 为服务端状态。
- 连接及恢复：role、pending_message_ids、last_coord_seq、last_worker_seq、snapshot_coord_seq、high_watermark；移除旧混合恢复游标。
- Event：worker_seq、task_seq、coord_seq；连接 sequence/ack 不承担持久化事件恢复。
- Task：workspace_context（root_dir、expected_git_head、patch_artifact_id、policy）、input_artifacts（artifact_id、sha256、mount_path）；计划可用 input_artifact_bindings 指向前置任务输出。
- 本地 ledger：pid、process_start_time_ns、os_boot_id、stdout_path、stderr_path、stdout_offset、stderr_offset、pgid/job_name、exit_status_path。
- 消息：plan.submit / accepted / rejected / cancel / snapshot；补充 plan.pause / resume，使原 UI 的暂停/继续明确为“暂停后续调度”，当前执行继续。

## 跨文档裁定

1. 暂停计划不等于挂起运行进程；已接受 DAG 不原地改结构，追加工作以新 Plan 关联原聊天。此选择保持 V1 协议可实现；若后续要求任务迁移或运行进程挂起，需独立扩展协议。
2. Coordinator 乱序事件先入 worker_event_inbox；补齐连续 worker_seq 后才发布到 task_events、推进状态或解锁后继。只延迟 ACK 无法防止错误解锁。
3. 单步骤取消也必须使全终态 Plan 收敛；显式 Plan 取消、失败、步骤取消、全成功按规范优先级决定终态。
4. 单 Plan 快照不能推进全局 last_coord_seq；完整 Fleet 快照才可建立全局恢复屏障。
5. 上游指定素材可用于其自身构建/测试，衍生发行必须换用自有素材。本仓库现无 LICENSE；本轮不擅自选许可，代码分发前需落定，不能把公开源码等同于已授权开源。

## 本轮验证与未执行项

已执行：全部 Markdown 的 JSON 示例解析、围栏配对、本地链接、旧目录/游标字段扫描、M1–M5 关键字段及 8 个 Fleet 工具核对、git diff --check；另做独立交叉审查并修订取消收敛和乱序推进规则。

未执行：Go 编译、跨平台进程实验、断线恢复 E2E、实际 APK 集成；仓库尚无实现。07_VALIDATION.md 的测试矩阵为后续必须执行的验收要求，不能据此宣称功能已通过。
