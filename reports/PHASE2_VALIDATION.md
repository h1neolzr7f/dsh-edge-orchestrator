# 第二轮变更与 Phase 2 验收报告

任务基线：main @ 8391cf16d42a37cea7783b1f6550115c2feb5b98。B1–B5 修复提交：12cdb1f0485cc8d707a8c9197e837c2206b571fd。后续提交实现 Phase 2、独立审查修复和验收材料。

本轮先复现并修复全部五个 Phase 1 缺陷，在全模块测试与 vet 通过后才开始 Phase 2。所有工作仍使用 Go Workspace 与纯 Go SQLite；未引入 CGO 产品依赖。

## B1–B5 变更核对

| 条目 | 修改文件 | 核心行为 | 回归覆盖 |
|---|---|---|---|
| B1 | apps/coordinator/scheduler.go、server.go、receipts.go | 仅 dispatched 每 500ms 重传；accepted/running/waiting_approval 不重复 submit；上线时只做一次 reconcile | TestRunningTaskDoesNotRetransmitSubmit：真实 TLS running 持续 1.2 秒，submit 仅首次一次 |
| B2 | apps/edge-agent/store.go、agent.go、ack_test.go | outbox(afterSeq) 使用 WHERE worker_seq > ? 分页；重连从服务端水位恢复；累计 ACK 每轮合并落库，非法 ACK 保持校验与错误传播 | TestOutboxDrainsOver100UnackedEvents：250 条未 ACK 事件三批读出后完整清空；TestPhase2DelayedACKDrainsOver100Events：中继丢弃 1–249 的业务 ACK，真实 Coordinator 仍收到后续事件并最终清空 outbox |
| B3 | apps/coordinator/store.go、round2_test.go | 单事件 savepoint 使非法后缀不回滚合法前缀；提交连续水位后返回错误；已应用 inbox payload 清空，只保留身份键/哈希 | TestOutOfOrderPoisonEventDoesNotRollbackPriorValidEvent、TestWorkerEventInboxPayloadCompacted；历史重复仍幂等，变更内容仍冲突 |
| B4 | apps/coordinator/store.go、receipts.go、server.go | devicesLocked(dbQueryer) 显式接收 DB/Tx；完整快照使用同一 Tx；RecordTaskAccepted 校验设备归属、状态更新和 plan.updated 同事务 | TestAcceptedReceiptProducesPlanEvent；原快照/重启与幂等测试保持覆盖 |
| B5 | packages/protocol/types.go、cmd/schema；coordinator/store.go、scheduler.go | TaskSnapshot.pending_reason 给出六种排队原因，变化发 plan.updated；非 queued 清空；支持旧数据库升级 | TestQueuedTaskExposesPendingReason；生成 Schema 的 enum 与规范一致 |

B1、B3、B4、B5 的原代码都已运行出对应行为失败；B2 的新游标回归先以旧 API 编译失败，再实现分页并用真实 ACK 延迟传输验证。不是仅补一组立即通过的测试。

## P2-1 至 P2-5 实现

| 条目 | 修改文件 | 实现与验收 |
|---|---|---|
| P2-1 授权工作区 | apps/edge-agent/agent.go、workspace.go、cmd/edge-agent/main.go、workspace_test.go | Workspaces/AllowedPrograms、重复 --workspace、DSH_WORKSPACES、--allow-programs/DSH_ALLOWED_PROGRAMS；默认两逻辑别名；规范化路径及已存在父目录的 symlink 检查；程序解析为授权路径 |
| P2-2 Git 基准 | workspace.go、coordinator/store.go、e2e_phase2_exec_test.go | 完整 HEAD/干净状态/未跟踪检查；不匹配不启动程序；通过后每任务独立 worktree；Artifact/patch 尚不支持时明确拒绝 |
| P2-3 本机能力 | capabilities.go、process.go、process_store.go、agent.go | 注册 process.exec/cancel、filesystem.read/write、git.inspect，版本 1.0.0；结构化退出码/摘要、日志、超时、原子写入、完整 SHA-256、Git 状态/diff/log；保留原两个内置能力 |
| P2-4 进程与日志 | runner.go、proc_linux.go、proc_windows.go、process_store.go、store.go | 旧 ledger 增加身份三元组、日志路径/偏移和退出状态；独立同二进制 runner，保存身份后释放执行门；Linux 进程组及 subreaper；Windows 挂起创建、入 Job 后恢复，KILL_ON_JOB_CLOSE/TerminateJobObject；恢复复用原 runner/worktree |
| P2-5 实际闭环 | tests/e2e_phase2_exec_test.go、phase2_log_transport_test.go、process_probe_*；scripts/demo*.sh | 真 Git 仓库、两 TLS Worker、真实命令/Git/汇总；工作区错误阻止执行；取消清理子进程；文件读写及路径约束；新增真实 unittest + Git 演示 |

主任务中的四个指定 E2E 名称全部存在：TestPhase2ParallelTestAndGitDiffWorkflow、TestPhase2WorkspaceMismatchRejectsExecution、TestPhase2CancelKillsChildProcessTree、TestPhase2FilesystemReadWriteConfinement。原 Phase 1 测试继续运行。

## 独立审查后的修复

一次独立全分支审查找出以下六个重要问题及一个文本分块问题；全部补回归并修复：

| 问题 | 修复 | 回归 |
|---|---|---|
| 带 workspace_context 的任务重启重新创建已有 worktree | 恢复分支直接复用原工作树和已授权 runner，不重复预检/checkout | TestPhase2AgentRestartResumesProcessLogs 改为带真实 workspace_context；重启后成功、执行计数仍为 1 |
| ready 已发布、go 未释放时重启 | 已保存身份的恢复路径也能幂等释放未完成的启动门；已有退出记录不会再启动 | TestReadyRunnerHandshakeResumesAfterRestart |
| 取消时只补一个 64 KiB 日志批次 | 取消与正常结束共用完整 drain 路径；确认进程树停止、字节偏移追平后才终态，错误不忽略 | TestPhase2CancelledProcessDrainsLogBacklog：8 MiB 日志 offset 与磁盘长度完全一致 |
| 运行中取消记录因重启丢失 | 恢复持久取消集合；取消写 marker；已取消的恢复进程仍进入清理监控 | TestPhase2CancellationPersistsAcrossAgentRestart；实际 process.cancel 能力另有端到端覆盖 |
| FIFO 打开阻塞且无法取消 | 普通文件限制；Linux 使用非阻塞打开并复查 fd 文件类型 | TestFilesystemReadRejectsFIFOWithoutBlocking |
| content 缺失/null 被当空串覆盖原文件 | 明确区分缺失与合法空字符串；拒绝后原文件保持不变 | TestMissingWriteContentDoesNotEraseFile |
| 4096 字节处拆坏 UTF-8 | 对有效 UTF-8 保留字符边界，另提供原始 data 字节 base64 | TestLogChunkPreservesUTF8Boundary |

此外，恢复标记在监控结束时清除，避免迟到重复投递重新监控终态任务；累计 ACK 按 50ms 轮询合并持久化，断线未落盘 ACK 不删除 outbox，仍依赖服务端已落库水位恢复。TestBatchedACKRejectsUnproducedEvents 确认非法 ACK 不被批处理吞掉。

## 语义与边界

- 并行按完整 task.running 区间验证，其中包含任务书要求的 claim 后 Git 预检；原 Phase 1 的双 Worker 屏障测试继续证明真实并发。没有用极短 Git 子命令与另一程序启动瞬间的偶然重叠判断调度是否并行。
- 受管 PID 为独立 runner：Linux pgid=runner pid，用户程序继承其组；Windows runner 持有命名 Job。这样同时满足进程树清理与 Agent 重启存续。
- 默认 SIGTERM 宽限按本轮任务书设为 500ms，可配置；更新了旧设计文档中的 5 秒默认值。自行 setsid 脱组的 daemon 不在 V1 process.exec 范围。
- 8 MiB 完整性压力测试在纯 Go SQLite 的 -race 模式下开销明显，使用独立 60 秒测试预算；2 秒进程树清理测试仍单独保留严格时限。普通文件日志落盘、事件 outbox、网络传输、Coordinator ACK 是分层检查，没有把本地 offset 追平等同于已发送成功。
- 文件路径与 cwd 受到授权映射约束；显式授权的 shell/解释器可运行其脚本，不将程序白名单描述为内核级文件系统隔离。
- input_artifacts、input_artifact_bindings、patch_artifact_id 的传输仍未实现，执行请求会明确拒绝。Artifact、手机 Flutter/Cordis 集成和公网/蜂窝弱网真机属于后续范围。
- 本机运行验收为 Linux amd64；Windows/ARM 的交叉编译成功不能代替原生运行验收。Windows 进程生命周期代码包含挂起入 Job、启动时间校验和 Job 清理，但没有据此宣称本机已运行 Windows 测试。

运行入口与参数见 [Phase 2 指南](../docs/PHASE2_QUICKSTART.md)。

## 最终本地验证结果

环境：Linux amd64，Go 1.24.2。以下四项按任务书顺序执行，全部退出码为 0：

| 检查 | 结果 |
|---|---|
| go run ./packages/protocol/cmd/schema > schemas/protocol.schema.json | 生成成功；Draft 2020-12 Schema、协议文档 10 个 JSON 示例与 UTF-8/base64 日志 payload 校验通过 |
| bash scripts/check.sh | 44 项顶层测试及其子测试通过；全部模块 go vet 通过；根集成测试包约 8.93 秒 |
| go test -race ./tests ./apps/coordinator ./apps/edge-agent ./packages/edge-link | 全部通过；根集成测试包约 42.71 秒；无竞态报告 |
| bash scripts/build.sh | CGO_ENABLED=0，coordinator / edge-agent / edge-cli × linux/amd64、linux/arm64、windows/amd64，全 9 个构建通过 |

8 MiB 取消日志压力回归单独在 -race 下通过约 27.81 秒，stdout_offset=磁盘完整长度；独立的进程树取消案例继续要求从 Control(cancel) 到 cancelled 不超过 2 秒，并检查派生进程不存活。压力测试时限不是进程停止时限。

一键演示从干净数据目录实际启动 1 Coordinator + 2 Worker 进程：Python unittest 退出码 0、stderr 包含 OK、Git HEAD 与基准一致、汇总 count=2，最终 Plan succeeded。演示和产品构建均显式使用 CGO_ENABLED=0。

验收材料包含源码、回归测试、运行指南、示例及演示脚本；不把这份本机报告解释为 GitHub Actions 或 Windows 原生运行结果。
