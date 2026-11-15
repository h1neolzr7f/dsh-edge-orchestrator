# ADR-004: 任务执行生命周期归 Edge Agent，进程身份与日志持久化

- 状态：Accepted
- 日期：2026-11-15

## 决策

Worker 返回 accepted 前事务写 local_task_ledger、幂等映射和原始 Receipt。任务与 Controller、Coordinator 和连接生命周期分离。workspace_context 预检及 input_artifacts 下载校验完成后才 running。

启动受管进程必须保存 `(pid, process_start_time_ns, os_boot_id, stdout_path, stderr_path)`，另存 pgid/job_name、日志字节偏移、exit_status_path、启动阶段。process_start_time_ns 是 OS 创建时间归一化值，os_boot_id 跨 Agent 重启稳定、跨 OS 重启变化，不用 Agent 启动时间代替。

stdout/stderr 直接追加写 `<agent_data_dir>/runs/<sha256(task_id)>/stdout.log` 与 stderr.log，由 Agent tail 协程按字节偏移生成 task.log / task.progress。偏移与 event_outbox 同事务提交。独立 task runner 负责启动握手、维持受管进程和原子写退出状态，不依赖 Agent 匿名管道和父进程存活。

## 恢复语义

Agent 重启先比对 (pid, process_start_time_ns, os_boot_id) 和受管进程组/Job 身份。匹配且存活则继续 tail；进程已退则校验退出状态文件并补尾部日志。身份不匹配、启动结果无法确认或没有可信退出状态时，记录 failed / interrupted_by_restart，不误判成功、不杀复用 PID，也不自动再执行命令。

启动前先保存 launching，再由独立 runner 保存身份和启动结果；崩溃窗口先与 runner 对账。目录与日志保留到事件及退出状态确认后。

## 取消与超时

- Linux x86_64 / ARM64：SysProcAttr{Setpgid: true} 创建独立进程组，记录 pgid；受管 runner 保持组长存活直至用户进程树收敛；核对身份后向 -pgid 发 SIGTERM，默认 500ms（可配置）后对仍属本任务的组发 SIGKILL。V1 不支持自行脱离受管组的 daemon。
- Windows：CreateJobObject + AssignProcessToJobObject，用户进程挂起创建、入 Job 后恢复，不允许 breakaway；取消/超时调用 TerminateJobObject。独立 runner 持有命名 Job 句柄，Agent 重启可重开；避免 Agent 退出触发最后句柄关闭杀任务。无法入 Job 明确失败，不降级为仅终止父进程。
- 取消请求先持久化，进程树退出及句柄释放后才 cancelled；超时清理后 failed / DEADLINE_EXCEEDED。终态不被迟到取消覆盖。

## 交付语义与边界

控制消息 at-least-once，任务按 idempotency_key 去重。Worker 事件按 device_id 全局 worker_seq 进 outbox；Coordinator UNIQUE(device_id, worker_seq) 去重并生成 coord_seq，落库后 event.ack {last_worker_seq}。Controller 用 last_coord_seq 恢复，不承诺 exactly-once。

Agent 进程崩溃不等于 OS 重启；OS 重启后原任务无法继续，以可信退出状态或 interrupted_by_restart 明确收敛。Coordinator 驱动 Plan 调度，Worker 管单任务执行，职责不混合。

实现依据：[Linux proc](https://docs.kernel.org/filesystems/proc.html)、[Go SysProcAttr](https://pkg.go.dev/syscall#SysProcAttr)、[Windows Job Objects](https://learn.microsoft.com/en-us/windows/win32/procthread/job-objects)。进程树退出、日志恢复及 PID 复用必须按 07_VALIDATION.md 在相应平台验收。
