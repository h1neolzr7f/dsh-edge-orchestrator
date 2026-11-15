# ADR-003: Controller 生成计划，常驻 Coordinator 驱动 DAG

- 状态：Accepted
- 日期：2026-11-15

## 背景

手机会断网、关屏或被后台回收。如果 depends_on 解锁和重试计时在 Controller，前置任务完成后下游将停摆。V1 不引入多 Coordinator 一致性与领导者选举。

## 决策

DSH Agent（LLM）在 Controller 生成 Plan DAG，经 plan.submit 提交。常驻 Go Coordinator 内嵌 packages/orchestrator，独立负责拓扑校验、持久化、depends_on 解锁、能力匹配、并发门控与重试状态机。Controller 仅负责意图、计划生成、审批和结果呈现，不承担调度循环。

Plan、依赖、任务和 plan.created 原子落库后才 plan.accepted；任务事件、状态变化、后继解锁同事务提交。前置全部 succeeded 且所需产物 ready 后解锁下游；失败传播、取消和幂等规则以 04_PROTOCOL_CONTRACT.md 为准。汇总必须是显式可执行的下游任务，不依赖手机 LLM 在线。

Coordinator 持有 Device Registry、Plan、Task Ledger、dispatch_outbox 与全局 coord_seq；Worker 持有本地执行事实及 device_id 范围 worker_seq。Controller 用 last_coord_seq 恢复同一 Plan，不重新创建任务。

## 后果

手机离线不影响已提交 DAG 下游推进；Worker 离线时 Coordinator 保留待派发任务。Coordinator 离线时 Worker 继续已接受任务，新的跨设备依赖解锁等服务恢复。单 Coordinator 必须常驻并持久保存 SQLite。

V1 不原地修改已接受 DAG。追加意图以新 Plan 关联同一聊天；进程重执行与传输重试分开。多控制端共享目录及分层 Coordinator 在后续阶段处理。
