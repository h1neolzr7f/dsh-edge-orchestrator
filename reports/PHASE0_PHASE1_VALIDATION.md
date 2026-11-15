# Phase 0/1 实现与验收记录

日期：2026-11-15。设计基线：2a123cc0faa8674c09a6f5b62672602b0617c5fd。

## 已交付

- Go Workspace：Coordinator、Edge Agent、Edge CLI、protocol、edge-link、orchestrator 与根 E2E 模块。
- 共享协议的 Go 结构体、严格 JSON 解码/语义校验及可从 Go 类型重新生成的 JSON Schema。
- TLS/WSS Worker 接入、按设备绑定的独立令牌、注册与资源探测、心跳、退避重连。
- Coordinator SQLite Plan/Task、幂等键、乱序事件 inbox、全局 coord_seq、持久化依赖与投递；Plan 暂停后续调度/继续/取消。
- Worker SQLite 原始 Receipt、执行 claim/计数、本地连续 worker_seq/outbox、业务 ACK 清理及取消 tombstone。
- 内置 device.inspect 与 report.aggregate；汇总消费前置任务结构化结果，Controller 离线无需回调即可推进。
- HTTPS Controller CLI、示例 Plan、一键本地 TLS 演示、检查与三平台构建脚本、CI 配置。

## 本机实测结果

环境：Linux amd64，Go 1.24.2。SQLite 为 modernc.org/sqlite v1.34.5，网络库 coder/websocket v1.8.13。

| 检查 | 命令/方式 | 结果 |
|---|---|---|
| 全模块测试与静态检查 | bash scripts/check.sh | 20 个顶层测试及其子测试通过，go vet 通过 |
| 竞态检查 | go test -race ./tests ./apps/coordinator ./apps/edge-agent ./packages/edge-link | 全部通过 |
| 交叉构建 | bash scripts/build.sh | 3 程序 × 3 目标 = 9 个二进制构建通过，CGO_ENABLED=0 |
| Linux amd64 / arm64 | file 检查构建输出 | 对应 ELF 架构，静态链接 |
| Windows amd64 | file 检查构建输出 | PE32+ x86-64；未在 Windows 实机运行 |
| 一键运行演示 | bash scripts/demo.sh | 1 Coordinator + 2 独立 Worker 进程；三任务 Plan succeeded；汇总 count=2 |
| JSON Schema | Draft 2020-12 校验 | Schema、示例 Plan、协议文档全部 10 个 JSON 示例通过 |
| 源码格式 | gofmt；git diff --check | 通过 |

全模块检查覆盖根模块与每个嵌套模块；不能仅在根目录 go test ./... 就宣称跑遍 Workspace。竞态检查使用 CGO_ENABLED=1 是 Go race 工具要求，与产品二进制的纯 Go / CGO_ENABLED=0 构建分开。

## 核心验收对应测试

| 要求 | 实际断言 |
|---|---|
| 两 Worker 握手、注册、能力上报 | TestPhase0Phase1OfflineDAG 使用真实 TLS 服务、两 Agent 和临时 SQLite；两设备均在线 |
| 两前置并行 | 两个执行器均进入屏障才放行，串行实现无法通过 |
| Controller 离线 DAG 推进 | 放行后至第三个任务启动之间不发送任何 Controller 请求；下游自动启动 |
| 幂等与重连 | 运行中主动断 Worker 链路并重投相同 Task；原 Receipt 返回，三个任务各自总执行计数严格为 1 |
| 全局恢复 | 保存 plan.accepted 的 coord_seq，单事件分页重放所有设备/Plan 事件，排序去重且集合与完整查询一致 |
| Coordinator 重启 | TestCoordinatorRestartContinuesDAG 在前置运行中关闭并重新打开同一数据库；下游完成且无二次执行 |
| 真实内置汇总 | TestBuiltinAggregateAndPersistedSnapshot 验证设备报告进入汇总结果，快照水位后无旧事件 |
| Worker 崩溃恢复 | 已 claim、未终结的任务恢复为 interrupted_by_restart，不重执行；Receipt 和计数持久化 |
| 乱序及重复事件 | seq=2 先到不推进，补 seq=1 后按顺序处理；重复不增全局事件或二次解锁 |
| 丢失投递后取消 | TestCancelAfterLostSubmitDoesNotStartWork：原 Worker 未接受投递便断开，取消后重连，Plan cancelled、执行次数 0 |
| 取消记录持久化 | TestCancellationTombstoneSurvivesRestart：取消未知任务后重启，迟到投递仍不执行 |
| Worker 任务归属 | TestUnassignedQueuedTaskRejectsWorkerEvents：未获分配 Worker 不能失败/取消排队任务，状态不变 |
| 输入与身份校验 | 无权限 HTTP、尾随 JSON、无效 DAG、同幂等键改内容、版本不匹配、未来游标、非对象 input 与缺失 depends_on 均拒绝 |

## 审查及实现裁定

一次独立全分支审查发现两个重要问题，均先用回归测试复现失败再修复：丢失投递后取消永久等待；空目标任务允许无归属 Worker 写失败事件。修复加入 task.reconcile / task.snapshot、持久化取消 tombstone 与严格归属检查；完整测试、race、构建全部复核通过。无剩余重要问题或暂缓小问题。

沿用已批准分层，Phase 0/1 只实现内置能力。任意进程、工作区补丁、Artifact 传输、手机 UI、扫码配对及真实弱网/设备验收不在本轮实现范围；收到未支持的 workspace_context/input_artifacts 请求明确拒绝。若把这些也列为当前能力，会给用户不真实的可用性预期。完整实现范围和后续边界见 [运行指南](../docs/PHASE1_QUICKSTART.md)。

当前未裁剪事件历史，因此无 CURSOR_EXPIRED 实测；仅实现当前历史内的有效游标与越界拒绝。Coordinator 离线期间 Worker 可继续已接受的本地内置能力；跨设备下游待 Coordinator 恢复。Windows/ARM 构建成功不能替代对应平台运行验收。现有 CI 仅配置，本文不宣称 GitHub Actions 已运行通过。

为保持本轮接口与端到端闭环可一起审阅，源码以一个实现提交交付；后续按能力扩展单独提交。本轮不选择项目 LICENSE、不分发修改版上游 APK、不自动合并第一阶段 PR。
