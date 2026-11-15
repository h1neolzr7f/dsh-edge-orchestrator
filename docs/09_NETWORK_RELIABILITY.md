# 09. 网络可靠性与连接体验

本项目以实际弱网体验为设计输入。连接不能只在实验室 Wi-Fi 正常；手机切换 Wi-Fi/蜂窝网络、Worker 短暂离线、Coordinator 重启，都不应导致任务丢失或重复执行。

## 9.1 连接拓扑

V1 统一采用主动出站连接：

- Worker → Coordinator：WSS/TLS。
- DSH Go Controller → Coordinator：HTTPS/WSS。
- 同 LAN 时允许直接连接本地 Coordinator 地址。
- 跨公网时连接自托管 Coordinator/VPS。
- 不要求 Worker 开放公网端口，不要求配置路由器端口转发。

Coordinator 是消息路由和状态汇合点，不是所有计算任务的执行点。Worker 收到任务后本地执行。

## 9.2 连接状态机

~~~text
stopped -> connecting -> authenticating -> online
online -> degraded -> reconnecting -> authenticating -> online
reconnecting -> offline
offline -> connecting
~~~

degraded 表示心跳或 RTT 已明显恶化，但连接尚未断开。UI 显示“网络较差”，不把它误报为设备离线。

## 9.3 重连与恢复

- 心跳间隔由 Coordinator 下发，默认 20 秒。
- 心跳超时后标记 degraded，再进入 reconnecting。
- 重连使用指数退避并加入 full jitter，避免大量设备同时冲击服务器。
- 网络接口变化（Wi-Fi/蜂窝切换）时立即触发一次重连尝试。
- 新 WebSocket 的 sequence/ack 从头计数，禁止携带旧连接帧 ACK 恢复业务。
- Controller 的 client.hello / link.resume 携带 last_coord_seq；Coordinator 按 coord_seq 升序恢复所有设备与 Plan 事件，固定 high_watermark 后接实时流。
- Worker 携带 last_worker_seq（本地保存的服务端连续确认水位）；Coordinator 返回自己的 last_worker_seq，Worker 补发更大的 worker_seq。服务端水位回退则 EVENT_HISTORY_LOST，不能静默重置。
- 双方可上报 pending_message_ids；未确认控制消息保留原 message_id，使用新连接 sequence 重封装。
- Agent 本地 task ledger 决定任务是否仍在运行，不能根据连接状态杀死任务。

一个 Link Manager 负责上述策略，不再叠加多层重试框架。

## 9.4 消息交付语义

- 控制消息：at-least-once + message_id 去重。
- 任务提交：idempotency_key 去重，重复请求返回同一任务。
- 三层独立：sequence/ack 属于本次连接；worker_seq 按 device_id 全局持久单调；coord_seq 由 Coordinator 跨所有设备及 Plan 全局生成。
- Worker 在 event_outbox 事务分配 worker_seq，Coordinator 以 UNIQUE(device_id, worker_seq) 去重；乱序事件存 worker_event_inbox，缺口补齐后才按 worker_seq 顺序发布、推进状态及解锁后继；事务提交后 event.ack {last_worker_seq} 只确认最高连续节点序号。帧 ack 不允许清理 outbox。
- Controller 按 coord_seq 去重，投影与 last_coord_seq 原子保存。全局主键允许空洞，不等待不存在的相邻整数。
- 首次进入/游标过期：同一读事务取得快照与 snapshot_coord_seq，再接大于屏障的事件；订阅缓冲或数据库补拉填补快照与订阅间隙。CURSOR_EXPIRED 要求快照，CURSOR_INVALID 明确报错。
- 任务状态、依赖解锁及事件落库原子提交；Controller 断线后 Coordinator 继续驱动 DAG。
- 连接层不承诺 exactly-once；业务层确保重复投递不重复启动任务。

## 9.5 文件传输

Artifact 不通过 WebSocket 传输。

1. Worker 请求 artifact upload session。
2. Coordinator 返回 upload_id、chunk_size 和已确认 offset。
3. Worker 分块上传，默认每块 4 MiB。
4. 断线后从服务端返回的 offset 续传。
5. 完成后校验总大小和 SHA-256。
6. 通过 event 通道发送 Artifact Ref。

下载同样支持 Range/分块续传。大日志采用压缩归档后作为 Artifact，不把无限日志流推入 UI。

## 9.6 网络质量策略

- 控制消息与文件流量分离。
- 进度事件在分配 worker_seq 前合并和限频；已经入 outbox 的序号不丢弃，最终状态必落库。
- 单个 Worker 的慢连接不能阻塞其他 Worker 的任务派发。
- Coordinator 对每个节点独立维护发送队列和 ACK 游标。
- 节点离线时任务进入 queued/offline，不在 UI 上假装已派发。
- 用户可以看到最近连接时间、RTT、重连次数和最后错误。

## 9.7 必测网络场景

| 场景 | 注入条件 | 预期 |
|---|---|---|
| 短时丢包 | 1–3% packet loss | 连接可恢复，任务不重复 |
| 高延迟 | RTT 200–800ms | 控制操作仍可用，UI 不假死 |
| 短断网 | 10–60 秒 | Worker 继续执行，事件补传 |
| 网络切换 | Wi-Fi → 蜂窝 → Wi-Fi | 自动重连并续接原任务 |
| Coordinator 重启 | 服务重启一次 | 从持久化队列恢复 |
| Worker 离线 | 任务提交时节点不在线 | 进入待派发队列或提示离线 |
| 大文件中断 | 上传 50% 时断网 | 从已确认 offset 续传 |
| 多节点并发 | 5–10 个 Worker | 慢节点不拖住其他节点 |

## 9.8 V1 体验目标

这些是测试目标，须用真实网络测量修订：
- 正常 LAN 下设备在线状态变化在 2 秒量级内反映。
- 网络恢复后 10 秒内恢复控制连接（不含极端运营商限制）。
- 任务已 accepted 后，Controller 离线不影响任务继续执行；前置成功后 Coordinator 自动解锁下游。
- 事件最终一致；断线期间事件不丢。
- 大文件上传可以续传，不要求从头开始。
- 一个节点断线不影响其他节点的任务和状态流。
