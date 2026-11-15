# ADR-002: V1 使用主动出站 WSS Edge Link

- 状态：Accepted
- 日期：2026-11-15

## 决策

Worker 主动通过 TLS/WSS 连接 Coordinator；Controller 通过 HTTPS/WSS 连接同一 Coordinator。Coordinator 为每个 Worker 维护独立连接、消息队列和 ACK 游标。

同 LAN 可使用本地 Coordinator；跨公网使用自托管 Coordinator/VPS。V1 不依赖 P2P、TURN 或入站端口映射。

## 原因

- Windows/Linux/Android/Dart/Go 支持成熟。
- 连接模型简单，适合先完成可靠的任务投递和状态恢复。
- 主动出站连接可穿过多数 NAT 和企业网络。
- 单节点连接和队列相互隔离，一个慢节点不拖住其他节点。

## 后果

Coordinator 是 V1 的网络汇合点；需要部署在常开 PC 或服务器。后续可增加 P2P 优化，但必须保留 Relay 回退。
