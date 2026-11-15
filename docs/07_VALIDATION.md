# 07. 验收与测试计划

## 7.1 最小环境

- Controller：Android DSH Go。
- Coordinator：Windows PC 或 Linux 小主机；另测 VPS。
- Worker A：Windows 10/11。
- Worker B：Linux x86_64 或 ARM64。
- 网络：同 LAN、跨网络、弱网模拟。

## 7.2 第一关键验收：真实多设备并行

目标：“A 电脑运行测试，B 电脑审查 diff，最后汇总。”

通过条件：
1. 两台 Worker 均返回 accepted。
2. 两个任务执行时间窗口重叠。
3. 各自生成结果与 Artifact。
4. Coordinator 自动派发显式下游汇总任务，生成带上游 Artifact 引用的报告。
5. 手机在前置执行期间断网，下游仍被解锁；恢复后按 last_coord_seq 显示同一 plan_id/task_id 和最终状态。

## 7.3 连接与弱网验收

测试 1–3% 丢包、200–800ms RTT、10–60 秒断网、Wi-Fi/蜂窝切换、Coordinator 重启、Worker 休眠恢复和多 Worker 同时重连。

检查：
- 连接状态正确区分 online/degraded/offline；
- 连接帧重置 sequence/ack；Worker 按 last_worker_seq、Controller 按 last_coord_seq 恢复；
- 同一任务不会因重复消息启动两次；
- 一个 Worker 慢连接不阻塞其他节点；
- Worker 断线时已接受任务继续执行。

## 7.4 Artifact 续传

使用 100MB 和 1GB 文件：
- 上传 50% 时断网；
- 恢复后从服务端确认 offset 续传；
- 最终 SHA-256 与源文件一致；
- 文件传输期间控制消息仍能低延迟往返。

## 7.5 能力自主探索

- 新 Worker 安装后自动上报 OS、架构、CPU、内存和工具清单。
- 安装/卸载 Git、Python、ADB 后，能力 revision 变化。
- 探测结果区分 detected、available、authorized。
- 用户授权后能力进入可调度状态。
- 调度器只选择满足 capability schema 和权限的节点。

## 7.6 任务幂等与恢复

- Worker 接受任务后，在 Receipt 返回前断网。
- Controller 将任务视为 unknown。
- 恢复后使用原 task_id 查询。
- Worker 返回原任务状态，不创建第二个执行实例。
- Worker 重启后核对进程身份三元组、磁盘日志和退出状态；无法确认则 failed / interrupted_by_restart。

## 7.7 初始性能目标

以下是第一轮工程目标，必须用真实设备和网络数据修订：
- LAN 设备注册到在线：P95 小于 2 秒。
- 正常网络任务 submit 到 accepted：P95 小于 3 秒，不含模型规划。
- 断网恢复后连接重建：目标小于 10 秒。
- 两个任务派发时间间隔：小于 2 秒。
- 正常网络事件更新：P95 小于 2 秒。

## 7.8 发布验收材料

- 环境与版本信息；
- 网络条件记录；
- plan/task IDs；
- 两台 Worker 的开始/结束时间；
- Coordinator 连接与 ACK 日志；
- Artifact SHA-256；
- 断网与恢复日志或录像；
- 弱网测试结果和已知限制。

## 7.9 本轮协议修订的验收矩阵（待代码阶段执行）

下表是必须实现的测试，不代表已测试通过。当前 Phase 2 运行检查见 PHASE2_QUICKSTART.md 与 ../reports/PHASE2_VALIDATION.md；其余矩阵仍待对应阶段实现，文档检查不能替代运行验收。

| 项目 | 注入条件 | 通过条件 |
|---|---|---|
| 双层集成 | 分别加载 Node.js Cordis 插件和 Flutter UI | 引擎恰有 8 个 fleet 工具；Dart 通过 edge-link-dart 订阅卡片；不在 Flutter 执行依赖解锁 |
| 手机离线 DAG | 两前置运行时断开 Controller，保持 Coordinator/Worker 在线 | 两任务实际并行，前置成功及产物 ready 后第 3 个汇总任务自动启动；plan_id 不变 |
| 无效 DAG | 环、缺失依赖、重复 task_id、无效并发数 | plan.rejected，Plan/Task/事件均不部分创建 |
| 暂停调度 | plan.pause 时已有任务运行，再 plan.resume | 当前任务继续，暂停期间不派发后继；恢复仍用原 Plan，状态事件可重放 |
| 步骤取消收敛 | 单独 task.cancel 最后一个未完成步骤 | 所有任务终态后 Plan 按规则进入 cancelled 或 failed，plan.completed 仅一次 |
| Plan 幂等 | 相同 idempotency_key 重复提交，再更改内容 | 相同内容返回原 Plan；不同内容 IDEMPOTENCY_CONFLICT；plan.created 仅一次 |
| 三层序号 | 两 Worker 交错产事件、断线换物理连接 | sequence/ack 重置；worker_seq 跨任务不重置；单 last_coord_seq 补齐两设备及 Plan 变化 |
| 事件确认 | 接收后落库前崩溃、ACK 丢失、先到 90 后到 89 | 未提交不发 event.ack；重复不增 coord_seq、不二次解锁；乱序 90 缓冲到 inbox，补齐 89 后才依次发布与解锁；last_worker_seq 只推进连续值 |
| 快照屏障 | 读取快照与建立订阅之间写入事件 | snapshot_coord_seq 后的事件全部到达，无重复应用；过期明确 CURSOR_EXPIRED |
| 工作区基准 | A/B HEAD 不同、同 HEAD 但脏文件或未跟踪文件 | 无自动同步时 WORKSPACE_MISMATCH；预检失败未启动程序；两节点原工作区不被覆盖 |
| 补丁一致 | 同 HEAD + 同 patch_artifact_id，再改补丁哈希或使其不可应用 | 相同补丁隔离执行；校验/应用失败明确报错，原工作区不变 |
| DAG 产物传递 | A 上传 JUnit、B 上传审查结果，后继用 input_artifact_bindings | 解析为相同 Artifact ID/哈希，HTTPS 下载到 mount_path；两文件验证后才 running |
| 输入损坏 | 截断文件、错误 SHA-256、下载中断、路径逃逸 | 损坏为 ARTIFACT_HASH_MISMATCH，路径拒绝；断网续传保持 accepted，截止后失败；不启动程序 |
| PID 复用 | ledger PID 存在但 start time 或 boot ID 不同 | 不恢复 tail 到错误进程，不对无关进程发信号；failed / interrupted_by_restart |
| Agent 崩溃 | 用户任务运行中强杀 Agent，runner 存活并继续写日志 | 任务不随 Agent 退出；重启验证三元组并从持久偏移补日志，不丢字节、不再执行 |
| 启动窗口崩溃 | launching 后、进程创建后、身份落盘前分别强杀 Agent | 与独立 runner 对账；无法确认则 interrupted_by_restart，不盲目启动第二实例 |
| Linux 取消/超时 | 父子进程均写文件、子进程忽略 SIGTERM | -pgid 收 SIGTERM，5 秒宽限后 SIGKILL；整组退出、文件释放；核对身份防复用 |
| Windows 取消/超时 | Job 内多级子进程持有文件，Agent 重启后取消 | TerminateJobObject 清整树并释放锁；Job 可重开；不因 Agent 句柄关闭误杀运行任务 |
| OS 重启 | 开始任务后系统重启 | os_boot_id 变化，不附着新 PID；可信退出状态或 interrupted_by_restart 明确收敛 |

## 7.10 Phase 0/1 最小代码闭环门槛

用户已确认文档并启动 Phase 0/1 实现。Go 1.22+ Workspace 管理 apps/coordinator、apps/edge-agent、apps/edge-cli、packages/protocol、packages/edge-link；Coordinator 内嵌 packages/orchestrator（建模块时加入 go.work）。SQLite 使用纯 Go 驱动，不依赖 CGO。

`tests/e2e_phase0_phase1_test.go` 必须在临时目录真实启动 Coordinator、SQLite 及两个 Agent（worker-win-sim / worker-linux-sim），验证：

1. 两 Worker 完成握手、注册及 device.inspect 上报。
2. 提交含两个并行前置与一个依赖汇总的 PlanEnvelope；汇总使用确定性测试能力，不依赖手机 LLM。
3. Controller 在前置运行时断线，Coordinator 自主解锁并完成下游。
4. 重复投递相同 idempotency_key，Worker 返回原 Receipt，执行计数严格为 1。
5. Controller 携带 last_coord_seq 恢复，收到全部缺失事件（含 Plan 事件），按 coord_seq 升序且去重后集合与数据库一致；不要求主键数值无空洞。
6. 三平台 CGO_ENABLED=0 交叉编译通过。sim 名称只证明协议流程，Windows Job Object 和 Linux 进程组必须在原生平台单独跑 7.9 对应验收，不能把模拟节点算作 Windows 原生验收。
