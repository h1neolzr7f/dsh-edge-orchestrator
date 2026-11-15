# 08. 开源复用与技术调研

## 8.1 DSH Go：必须保留的产品基础

仓库：https://github.com/ccch1mneyyy/dsh-Go

保留并继续利用：
- DeepSeek Harness Agent Loop 与 Cordis 插件架构；
- Flutter/Dart 移动端 UI；
- Android PRoot 运行环境；
- Phone Control、Accessibility、Shizuku/ADB 等原生能力；
- 模型适配、会话、语音、审批和原有插件生态。

新项目通过 Cordis Fleet 插件和 Dart client 接入，不复制第二套 Agent Loop，不重写 DSH Go 的已有手机控制能力。

## 8.2 DS Harness Remote：可选适配器，不是 V1 核心底座

仓库：https://github.com/liguobao/ds-harness-remote

源码审阅过的部分：
- packages/plugin/src/client-runtime.ts
- packages/plugin/src/client-connection.ts
- packages/plugin/src/remote-session-tools.ts
- packages/plugin/src/connection-controller.ts
- packages/plugin/src/server-connection.ts
- apps/server/src/gateway.ts

可借鉴：
- 设备配对与身份管理思路；
- Noise 加密握手和连接状态建模；
- LAN / P2P / TURN / Relay 的拓扑经验；
- Host / Client / Workspace / Session 的远程工具抽象；
- Agent Remote Tools 的任务委派方式。

不作为 V1 核心依赖的原因：
- 项目发起者实际使用反馈：Remote 经常卡退，弱网下体验不稳定。
- 当前 Gateway 的 Peer/Control 连接模型存在同一 Client 新连接替换旧连接的限制。
- 新编排系统需要自己的任务持久化、离线队列、ACK、事件续传和 Artifact 断点续传；这些不能简单等同于 Remote Session。

处理方式：
1. 不 fork Remote，也不把其 SDK 强行放进核心依赖。
2. 将 Remote 作为可选 Transport Adapter 的候选。
3. 只有未来真实压测达到 Edge Link 的可靠性和体验指标，才考虑启用。
4. 可以继续研究其协议、加密和连接策略，但核心业务协议保持独立。

## 8.3 自研边界

| 能力 | 决策 |
|---|---|
| DSH Agent / Cordis / Android UI | 复用 DSH Go |
| 设备任务模型与 DAG | 自研 |
| Edge Coordinator | 自研 Go 服务 |
| Edge Agent | 自研 Go 常驻进程 |
| Edge Link 消息协议 | 自研薄协议，TLS/WSS |
| 身份配对 | 自研产品流程，采用成熟密码库，不自创密码算法 |
| 断线恢复 / ACK / outbox | 自研，属于核心能力 |
| Artifact 分块续传 | 自研薄层 |
| DS Harness Remote | 可选 adapter / 参考实现 |

## 8.4 为什么 V1 选择 WebSocket/TLS

- Dart、Go、Kotlin 和常见代理均有成熟实现。
- 可使用 HTTPS 端口，减少网络环境配置。
- 连接模型简单，容易实现心跳、ACK、resume cursor。
- 对 V1 的控制消息、任务事件和轻量日志足够。
- 大文件走独立 HTTPS API，避免 WebSocket 被大流量阻塞。

若实测证明 WSS 不足，再按测量结果增加 HTTP/2 或 QUIC，不提前引入 P2P 复杂度。

## 8.5 其他参考方向

后续调研优先看其可复用的具体模块，而不是照搬完整产品：
- Tailscale / Headscale：组网和设备身份体验参考，不作为应用层任务协议。
- RustDesk：远程连接与中继部署经验，不作为 Agent 调度核心。
- Nebula：设备网络身份和 overlay 网络思路。
- NATS：若任务事件量增长，可作为后续消息基础设施；V1 不需要先引入。
- gRPC：服务接口和流式 RPC 参考；V1 WSS 先落地。
