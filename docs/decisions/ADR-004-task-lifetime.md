# ADR-004: 任务生命周期归 Edge Agent

- 状态：Accepted
- 日期：2026-11-15

## 决策

Worker 返回 accepted 前先将 Task 写入本地持久化 ledger。任务执行与手机、Controller、Coordinator 的连接生命周期分离。事件先写本地 outbox，再按 ACK 游标同步。

## 原因

网络断开是常态，不应成为任务失败的同义词。手机可关屏、切换网络或暂时退出应用；Worker 已接受的构建、测试和分析任务应继续运行。

## 语义

消息 at-least-once；任务按 idempotency_key 去重；事件按 seq 重放；最终状态可由 snapshot 对账。不承诺 exactly-once。
