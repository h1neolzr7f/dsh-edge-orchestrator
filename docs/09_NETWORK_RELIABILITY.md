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
- 重新连接时发送 last_ack、last_event_seq 和未确认 message_id。
- Coordinator 返回 resume cursor；双方补发未确认消息。
- Agent 本地 task ledger 决定任务是否仍在运行，不能根据连接状态杀死任务。

一个 Link Manager 负责上述策略，不再叠加多层重试框架。

## 9.4 消息交付语义

- 控制消息：at-least-once + message_id 去重。
- 任务提交：idempotency_key 去重，重复请求返回同一任务。
- 事件：本地持久化后发送，Coordinator ACK 后推进游标。
- 最终状态：Task Snapshot 可覆盖事件丢失后的状态视图。
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
- 进度事件做合并和限频；最终状态不丢。
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
- 任务已 accepted 后，Controller 离线不影响任务继续执行。
- 事件最终一致；断线期间事件不丢。
- 大文件上传可以续传，不要求从头开始。
- 一个节点断线不影响其他节点的任务和状态流。
