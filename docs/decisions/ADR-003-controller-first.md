# ADR-003: V1 采用单 Coordinator 中心化编排

- 状态：Accepted
- 日期：2026-11-15

## 背景

V1 需要快速证明跨设备任务拆分、并行执行和状态恢复。多 Coordinator 一致性、分布式调度和领导者选举会显著扩大实现范围。

## 决策

V1 由一个 Coordinator 持有 Device Registry、Plan、Task Ledger 和调度状态。Worker 负责本地任务执行与事件记录。DSH Go 手机端或 PC 端作为 Controller，连接到该 Coordinator。

## 后果

优点：数据模型简单、容易调试、容易复现和验收。

代价：Coordinator 是协调中心；V2 再考虑多个控制端共享任务目录和租约。
