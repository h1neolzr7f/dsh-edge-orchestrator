# Contributing

## 开发原则

- 先跑通真实端到端链路，再扩展抽象层。
- 所有跨组件数据结构必须来自 protocol schema。
- 任务状态以 Worker 的持久化状态为执行事实；Controller 状态是可恢复的投影。
- 每个派发操作使用稳定 task_id 和 idempotency_key。
- 不因网络超时盲目重做非幂等任务。
- 不为不存在的场景添加多层包装；保持清晰的数据流和状态机。
- 新能力必须附输入输出 schema、权限 scope、资源类别和可执行测试。
- DS Harness Remote 不是核心依赖；任何新 Transport Adapter 都必须通过相同弱网验收。

## Pull Request 要求

每个 PR 描述解决的问题、用户价值、影响组件与协议版本、本地测试命令和结果，以及连接/任务恢复行为的 E2E 证据。

## Definition of Done

- 功能有测试，不以“代码能编译”作为唯一完成标准。
- 状态变化有可追踪事件。
- 断线场景下任务行为明确。
- 对外协议有 schema 或示例。
- 文档更新与实现同步。
