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
  LEDGER --> ROUTER[Capability Router]
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
3. 检查 idempotency_key；已存在则返回原 Receipt。
4. 将 Task 写入本地 ledger。
5. 返回 accepted。
6. 启动执行器，写入 started/progress/log 事件。
7. 产物写入本地临时区并加入上传队列。
8. 完成后写入 succeeded/failed。
9. Link 恢复后补传未确认事件和产物元数据。

Worker 接受任务后，即使 Coordinator 或手机暂时离线，也继续执行。重启后根据本地 task ledger 和进程信息恢复任务状态。

## 5.6 部署形态

- Windows：单文件 Agent + Windows Service，可选用户态前台运行。
- Linux：单文件 Agent + systemd service。
- ARM64 Linux：同一 Go 代码交叉编译。
- Android：Kotlin Foreground Service + DSH Go 现有 Phone Control/PRoot 能力；不把 Android 后台限制假装成普通 Linux daemon。

## 5.7 语言与集成

- 通用 Agent 与 Coordinator：Go。
- DSH Go Controller：Dart client + Cordis plugin。
- Android API：Kotlin adapter。
- 能力插件通过 JSON Schema 和稳定 Adapter interface 接入，不让 Orchestrator 依赖具体设备型号。
