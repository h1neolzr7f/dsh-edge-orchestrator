# Phase 2：真实本机执行

当前版本增加 process.exec / process.cancel、filesystem.read / write、git.inspect，保留 device.inspect 与 report.aggregate。Coordinator 驱动 DAG；任务、事件和日志不依赖 Controller 持续在线。

## 一键演示

在仓库根目录运行：

```bash
bash scripts/demo-phase2.sh
```

脚本创建一个真实 Git 仓库和 Python unittest，启动一个 TLS Coordinator 与两个独立 Worker 进程：A 运行单元测试，B 检查 Git，最后自动汇总真实结果。证书、独立令牌、数据库与工作树保存在 .local/demo-phase2。再次运行沿用原 Plan，不重复执行已完成任务。原 `bash scripts/demo.sh` 的 Phase 1 演示仍可运行。

需要 Go 1.22+（实测 1.24.2）、Git、Python 3、Bash、OpenSSL、curl。演示节点名称含 win-sim 不代表已经在 Windows 运行；实际平台以设备报告为准。

## 工作区与程序授权

```bash
./edge-agent --id worker-a --db .local/worker-a/agent.db \
  --coordinator https://YOUR_HOST:8740 --ca cert.pem \
  --workspace workspace/project=/absolute/path/to/project \
  --allow-programs git,go,python3,pytest \
  --termination-grace 500ms
```

- `--workspace` 可重复；或设置 DSH_WORKSPACES 为 JSON 映射，例如 `{"workspace/project":"/absolute/project"}`。宿主路径必须是绝对路径。未指定时，workspace/default 和 workspace/project 都映射到 `<agent_db_dir>/workspaces/default`。
- DSH_ALLOWED_PROGRAMS 支持逗号列表或 JSON 数组，命令行覆盖环境值。未指定时默认允许 git、go、python、python3、pytest、node、npm、sh、bash、cmd、powershell；不存在的可执行程序不进入可用白名单。空 JSON 数组关闭所有 process.exec 程序授权。
- 程序在启动时解析为获准的可执行路径；参数按数组传递，不额外拼接 shell。显式允许 shell/解释器意味着允许其脚本执行能力。工作区 API 的路径约束与 cwd 检查不构成宿主系统的内核沙盒。
- Workspaces 限制文件 API 和 cwd；拒绝未授权逻辑根、绝对相对路径参数、`..`、盘符/ADS、NUL 与越界符号链接。文件读取仅接受普通文件，FIFO/设备不作为普通文件读取。
- Worker Bearer 使用原 DSH_WORKER_TOKEN；Controller 及其他节点不得共用该令牌。

## 工作区基准

带 workspace_context 的任务要求完整 HEAD 一致，且 index/worktree 干净、没有未跟踪文件；不满足返回 WORKSPACE_MISMATCH，用户程序不启动。

通过后创建每任务独立的 detached Git worktree；process.cwd、文件和 Git API 在该任务中解析到此工作树。原仓库不会被 checkout/reset 或清理；不同任务不会共用测试产生的未跟踪文件。日志和输出目录由 ledger 中保存的路径定位。

想读取/写入授权原目录或检查其现有脏 diff 时，可以不带 workspace_context。带基准的任务输出保留在其独立工作树，跨任务文件传递仍需要后续 Artifact 实现。本轮不接收 patch_artifact_id、input_artifacts 或 input_artifact_bindings 的执行请求。

## 当前能力

| 能力 | input 示例 | result |
|---|---|---|
| process.exec | `{"program":"python3","args":["-m","unittest","-v"],"cwd":"workspace/project","timeout_ms":60000}` | exit_code、duration_ms、stdout_summary、stderr_summary、started_at、finished_at |
| process.cancel | `{"task_id":"task_to_cancel"}` | task_id、cancel_requested；目标终态随后通过事件到达 |
| filesystem.read | `{"workspace":"workspace/project","path":"README.md","offset":0,"max_bytes":65536}` | content、truncated、完整文件 size_bytes / sha256 |
| filesystem.write | `{"workspace":"workspace/project","path":"out/note.txt","content":"hello","overwrite":false}` | path、size_bytes、sha256 |
| git.inspect | `{"workspace":"workspace/project","mode":"summary","max_count":10}` | HEAD、分支、porcelain 状态、diff stat、受限 diff、提交列表 |

process.exec 默认超时 60 秒，与 Task.deadline 取较早值；摘要每流最多 8 KiB。非零退出为 PROCESS_EXIT_NONZERO，结果保留退出码和尾部日志；超时为 DEADLINE_EXCEEDED。文件读默认最多 256 KiB，允许 offset，哈希覆盖完整文件。写入使用同目录临时文件；覆盖使用原子 rename，不覆盖使用原子 hard-link 发布以避免检查与重命名之间覆盖竞态。省略或传 null 的 content 会拒绝，显式空字符串仍可写入。

git.inspect 支持 status / diff / log / summary，max_count 默认 10、上限 1000，diff 摘要最多 64 KiB。禁用 pager、外部 diff/textconv、fsmonitor 和 checkout hooks；不把 Git 输出作为可执行指令。

## 生命周期、日志与恢复

- 独立执行器使用同一个 edge-agent 二进制的内部模式，不需要第四个程序或 Python 常驻服务。Agent 先保存任务 claim，执行器发布进程身份，Agent 持久化身份后才释放用户命令。ready→go 握手可在重启后续接。
- local_task_ledger 保存 pid、process_start_time_ns、os_boot_id、stdout/stderr 路径及字节偏移、exit_status_path、Job 名称。执行器 PID 是受管身份；Linux 也是进程组组长，用户命令继承该组。
- stdout/stderr 直接追加写本地文件；tail 的事件写入与偏移推进同事务。task.log 提供 stream、offset、length、text 与原始字节 data（JSON base64）；有效 UTF-8 不会在分块处被拆坏。结束或取消先清理进程树并补齐积压日志，再发终态。
- Linux 取消先验证启动时间和 boot ID，向 -pgid 发 SIGTERM，默认 500ms 后必要时 SIGKILL；执行器做 subreaper，收集已退出后代。V1 不支持命令自行 setsid 脱离受管组的 daemon。
- Windows 用户进程挂起创建，加入命名 Job 后才恢复；Job 启用 KILL_ON_JOB_CLOSE，由独立执行器持有，Agent 取消时调用 TerminateJobObject。Agent 重启不关闭执行器持有的最后 Job 句柄。
- Agent 重启复用已保存的 worktree、执行器和日志偏移；不再检查原目录是否被后来改动，也不重跑已接受命令。已保存的取消会恢复并继续清理。身份不匹配且无可信退出记录时为 interrupted_by_restart，不向复用 PID 发信号。
- Controller/网络断线不取消任务；Coordinator 重连对 accepted/running 只做一次 reconcile，常规轮询不会反复 submit。outbox 按 sent 游标分页，业务 ACK 与连接帧 ACK 分离。

## 排队原因

TaskSnapshot.pending_reason 在 queued 时显示阻塞条件：waiting_dependencies、scheduling_paused、concurrency_limit、device_offline、capability_unavailable、device_busy；非 queued 清空。原因变化写 plan.updated，客户端可以据此刷新任务卡。

## 验证与当前范围

```bash
go run ./packages/protocol/cmd/schema > schemas/protocol.schema.json
bash scripts/check.sh
go test -race ./tests ./apps/coordinator ./apps/edge-agent ./packages/edge-link
bash scripts/build.sh
```

完整证据见 [第二轮验收报告](../reports/PHASE2_VALIDATION.md)。本轮本机为 Linux amd64；Windows amd64、Linux arm64 以交叉编译验证，原生运行结果不能据此推定。DSH Go 手机卡片/Cordis 集成、Artifact 传输与真实公网弱网验收仍为后续工作。
