# ADR-001: DS Harness Remote 不作为 V1 核心传输依赖

- 状态：Accepted
- 日期：2026-11-15

## 背景

DSH Go 已有 DS Harness Remote 能力，但项目发起者的实际使用反馈是 Remote 经常卡退，弱网连接体验不佳。源码审阅也发现当前 Gateway 的 Control Peer 连接模型对同一 Client 身份存在连接替换限制。

## 决策

V1 自研轻量 Edge Link + Coordinator。DS Harness Remote 仅保留为可选兼容 Adapter 和技术参考，不放在核心通信链路。

## 复用边界

保留 DSH Go 的 Agent、Cordis、Android Phone Control、模型和 UI。Remote 的配对、加密、P2P/Relay、Host/Session 工具可继续研究，但不要求 Edge Agent 依赖 Remote Host Runtime。

## 后果

- 项目可独立控制连接状态、重连、消息 ACK、任务队列和文件续传。
- 需要自己维护一套精简通信协议和 Coordinator。
- 不自创密码算法；身份与加密采用成熟 TLS/密码库。
- 未来 Remote Adapter 必须通过同一弱网验收标准，不能仅凭“能连上”接入。
