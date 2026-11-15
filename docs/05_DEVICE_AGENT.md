# 05. Edge Agent 设计

## 5.1 产品定义

Edge Agent 是安装在 PC、服务器、Linux SBC 或后续 Android 设备上的常驻执行节点。它不是另一个聊天 Agent，而是 DSH Agent 可调用的“设备身体”：负责发现本机能力、接收任务、调用工具、保存状态并返回结果。

## 5.2 自主探索

首次启动执行 Device Introspection：

- OS、架构、内核、系统版本和运行时；
- CPU、内存、磁盘、GPU、电池和网络接口；
- Git、Python、Node、Dart、Java、ADB、Docker、浏览器等工具；
- 本机服务、开放的本地 API、SSH/ADB 可达设备；
- USB、串口、蓝牙、摄像头、音频、GPIO、MQTT 等可识别接口。

每个探测器返回 id、version、detected、available、metadata、permission_scope。Agent 将结果组合成 Capability Manifest。

探测分三层：
1. 静态探测：读取系统信息和 PATH 中的可执行程序。
2. 只读探测：执行版本查询、设备枚举和接口状态读取。
3. 适配器探测：加载已安装的 Capability Pack。

设备可以自主发现“我能做什么”；授权由用户掌握。未授权能力可以展示，但不能被调度执行。

## 5.3 运行时结构

~~~mermaid
flowchart TB
  BOOT[Agent Bootstrap] --> INSPECT[Device Inspector]
  BOOT --> ID[Device Identity]
  INSPECT --> MAN[Device Manifest]
  MAN --> CAP[Capability Registry]
  ID --> LINK[Edge Link Manager]
  LINK --> INBOX[Task Inbox]
  INBOX --> LEDGER[Local Task Ledger]
  LEDGER --> PRE[Workspace / Artifact Preflight]
  PRE --> ROUTER[Capability Router]
  ROUTER --> PROC[Process Adapter]
  ROUTER --> FS[Filesystem Adapter]
  ROUTER --> GIT[Git Adapter]
  ROUTER --> HW[Hardware / Android Adapter]
  PROC --> OUT[Event Outbox]
  FS --> OUT
  GIT --> OUT
  HW --> OUT
  OUT --> LINK
~~~

## 5.4 V1 首批能力

| Capability | 功能 | 优先级 |
|---|---|---:|
| device.inspect | 系统与资源信息 | P0 |
| process.exec | 授权程序与参数执行 | P0 |
| filesystem.read | 授权目录读取 | P0 |
| filesystem.write | 授权目录写入 | P0 |
| git.inspect | status / branch / diff / log | P0 |
| process.cancel | 取消受管进程 | P0 |
| artifact.upload | 结果产物上传 | P0 |
| browser.control | 浏览器自动化 | P1 |
| android.control | ADB / Accessibility | P1 |
| docker.control | 容器操作 | P1 |
| serial.uart / mqtt | 外设通信 | P2 |
| gpio.read/write | GPIO 控制 | P2 |

## 5.5 任务生命周期

1. 收到 task.submit。
2. 校验 capability、权限和 schema。
3. 检查持久化 idempotency_key；相同内容返回原 Receipt，内容不同返回 IDEMPOTENCY_CONFLICT。
4. 以事务将 Task、幂等映射与原始 Receipt 写入 local_task_ledger。
5. 返回 accepted。
6. 预检 workspace_context：要求匹配完整 Git HEAD；无补丁时要求干净，有补丁时在干净任务隔离 worktree 下载校验并应用指定 patch_artifact_id。V1 不自动同步或覆盖用户工作区，不匹配返回 failed / WORKSPACE_MISMATCH。
7. 经鉴权 HTTPS 下载 input_artifacts 到任务隔离目录，按 SHA-256 校验后挂载到相对工作目录的 mount_path。拒绝路径逃逸与覆盖冲突；中断续传保持 accepted。此步骤成功后才允许 running。
8. 启动受管执行器，持久化进程身份与日志路径，再记录 task.started、task.progress、task.log。
9. 产物写本地临时区，上传并经 Coordinator 校验 ready，才允许任务 succeeded；错误记录 failed。
10. 事件按 device_id 全局 worker_seq 事务写 event_outbox；Link 恢复后按 event.ack 的 last_worker_seq 补传。

Worker 接受任务后，即使 Coordinator 或手机暂时离线，也继续已具备输入的执行；尚未下载完整输入则保持 accepted。重启按以下约定恢复，不能仅凭 PID 存在判断任务存活。

### 进程身份、落盘日志与重启恢复

local_task_ledger 在启动子进程时必须持久化 `(pid, process_start_time_ns, os_boot_id, stdout_path, stderr_path)`，另存 pgid 或 job_name、stdout_offset、stderr_offset、exit_status_path 与启动阶段。process_start_time_ns 是 OS 原生创建时间归一化到纳秒的身份值，保留原始精度，不使用 Agent 的当前时间猜测。Linux 来自 /proc/<pid>/stat 的 starttime（启动后 ticks 换算），os_boot_id 来自 /proc/sys/kernel/random/boot_id；Windows 使用 GetProcessTimes 创建时间（转为 Unix epoch 纳秒）和 Win32_OperatingSystem.LastBootUpTime 归一化 UTC 值作为 os_boot_id，跨 Agent 重启稳定、跨 OS 重启变化，不能用 Agent 随机 UUID。

stdout/stderr 由子进程直接以追加方式写 `<agent_data_dir>/runs/<task_id>/stdout.log` 与 stderr.log，不依赖 Agent 存活的匿名管道。tail 协程按字节偏移封装 task.log / task.progress；偏移推进与 outbox 写入同一事务，事件携带 stream、offset、length，防止崩溃后漏日志。

启动使用独立 task runner：先持久化 launching 记录，再由 runner 持久化受管进程身份、执行用户程序并原子写退出状态文件。runner 不依附 Agent 连接和退出生命周期，避免 Agent 在启动与 ledger 更新间崩溃后重复执行。无法确认该窗口是否执行时，先对账 runner，禁止自动重启用户命令。

Agent 重启先比对 (pid, process_start_time_ns, os_boot_id)，并验证受管组/Job 身份。匹配且存活则恢复 tail；已退出则读绑定 task_id 和进程身份的退出状态文件，补齐尾部日志并恢复终态。身份不符或无可信退出状态时记录 failed / interrupted_by_restart；不得向复用 PID 发信号，不把缺失退出码推测为成功。所有日志与退出状态确认前保留本地运行目录。

### 跨平台进程树清理

- Linux x86_64 / ARM64：使用 SysProcAttr{Setpgid: true} 创建独立进程组，记录 pgid；受管 runner 保持组长存活到其用户进程树收敛，防止父程序先退而失去取消身份锚点。取消或超时先向 -pgid 发 SIGTERM，默认宽限 5 秒，再向仍属于本任务的进程组发 SIGKILL；清理前重新验证身份，防止 PID/PGID 复用。不支持自行 setsid 逃离受管组的守护化程序作为 V1 process.exec。
- Windows：CreateJobObject 创建每任务命名 Job；用户进程以挂起方式创建，AssignProcessToJobObject 成功后才恢复，禁止 breakaway。取消或超时用 TerminateJobObject 清理整棵受管进程树。独立 runner 保持 Job 句柄并写退出状态；Agent 重启重新打开同名 Job，避免 Agent 关闭最后句柄时触发 KILL_ON_JOB_CLOSE 而破坏任务存续。不能入 Job 的任务明确失败，不退化为只杀父 PID。
- 取消先落持久化请求，再清理；确认组/Job 内进程退出并关闭文件句柄后才记录 cancelled。deadline 超时对应 failed / DEADLINE_EXCEEDED。已成功终态不被迟到取消覆盖。

## 5.6 部署形态

- Windows：单文件 Agent + Windows Service，可选用户态前台运行。
- Linux：单文件 Agent + systemd service。
- ARM64 Linux：同一 Go 代码交叉编译。
- Android：Kotlin Foreground Service + DSH Go 现有 Phone Control/PRoot 能力；不把 Android 后台限制假装成普通 Linux daemon。

## 5.7 语言与集成

- 通用 Agent 与 Coordinator：Go。
- DSH Go 引擎：integrations/dsh-cordis-plugin，TypeScript / Node.js Cordis 插件。
- DSH Go UI：integrations/dsh-flutter-ui，依赖 packages/edge-link-dart。
- Android API：Kotlin adapter。
- 能力插件通过 JSON Schema 和稳定 Adapter interface 接入，不让 Orchestrator 依赖具体设备型号。
