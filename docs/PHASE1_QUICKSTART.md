# Phase 0/1 最小运行闭环

本轮实现为 Go Coordinator、Edge Agent、Edge CLI、共享协议/Schema 与自动化验收。DSH Go 手机应用、Cordis 插件和 Flutter 卡片仍属后续产品集成；当前 Controller 使用 CLI/HTTPS。

## 本机一键演示

需要 Go 1.22+（本轮实测 1.24.2）、Bash、Python 3、OpenSSL 和 curl。在仓库根目录执行：

```bash
bash scripts/demo.sh
```

脚本生成本地测试证书和每设备独立随机令牌，真实启动一个 Coordinator、两个 Worker **进程**，提交 examples/phase1-plan.json：两前置 device.inspect 并行后，由 Coordinator 自动派发 report.aggregate。退出时停止演示进程，数据库保留在 .local/demo；再次执行使用相同 Plan 和 Receipt，不重跑已完成任务。要开始全新演示，可在确认不需历史记录后更换数据目录或创建不同的 plan_id、task_id、idempotency_key。

两个 sim 名称仅标记节点，资源报告使用当前宿主 OS；在 Linux 同机运行不构成 Windows/ARM 原生验收。自动化并行验收用执行屏障证明确有重叠，普通盘点过快时肉眼未必能看到中间状态。

## 构建与运行

```bash
bash scripts/check.sh
bash scripts/build.sh
```

输出 dist/linux-amd64、dist/linux-arm64、dist/windows-amd64，每目录含 coordinator、edge-agent、edge-cli；Windows 文件带 .exe。全部使用 CGO_ENABLED=0 构建，无 Python/Node/SQLite 系统运行库依赖。

Coordinator 读取环境变量 DSH_CONTROLLER_TOKEN 和 DSH_WORKER_TOKENS（JSON 格式的 device_id → token 映射）；每个 Worker 只读取自己的 DSH_WORKER_TOKEN。要求 Controller 与各 Worker 的令牌互不相同。

```bash
# 先在环境中设置以上令牌；证书应由客户端信任，并包含实际主机名/IP。
./dist/linux-amd64/coordinator --listen 0.0.0.0:8740 --db coordinator.db --tls-cert cert.pem --tls-key key.pem
./dist/linux-amd64/edge-agent --id worker-linux-sim --db linux-worker.db --coordinator https://YOUR_HOST:8740 --ca cert.pem
./dist/linux-amd64/edge-cli --url https://YOUR_HOST:8740 --ca cert.pem devices
./dist/linux-amd64/edge-cli --url https://YOUR_HOST:8740 --ca cert.pem submit examples/phase1-plan.json
./dist/linux-amd64/edge-cli --url https://YOUR_HOST:8740 --ca cert.pem watch plan_phase1_demo
```

Windows 使用相同参数和 .exe；PowerShell 中用 `$env:DSH_WORKER_TOKEN` 设置环境变量。每个设备必须使用独立数据库和匹配令牌，不能复制运行中数据库、混用身份或重置旧身份的事件序号。当前用显式预配置令牌接入，扫码配对/设备密钥轮换尚未实现。

完整测试证据见 [Phase 0/1 验收记录](../reports/PHASE0_PHASE1_VALIDATION.md)。

## 当前 API

| 接口 | 功能 |
|---|---|
| POST /v1/plans | Plan JSON 提交，返回 PlanAccepted；幂等冲突为 HTTP 409 |
| GET /v1/plans/{plan_id} | 单 Plan 快照，不推进全局恢复游标 |
| POST /v1/plans/{plan_id}/pause 或 resume | 持久化暂停/继续后续派发，已接受任务继续 |
| POST /v1/plans/{plan_id}/cancel | 取消计划并等待已接受任务终结 |
| GET /v1/devices | 清单、授权能力和连接状态 |
| GET /v1/snapshot | 完整 Fleet 快照及 snapshot_coord_seq |
| GET /v1/events?after_coord_seq=N&high_watermark=H&limit=500 | 单游标全设备恢复；首次 H=0，后续页固定首响应水位 |
| WSS /v1/link | Worker hello/welcome、register、ready、任务、Receipt、事件 ACK 与心跳 |
| GET /healthz | 无身份数据的运行状态；存储/调度错误返回错误状态 |

所有业务 HTTPS 接口使用 Controller Bearer；WSS 使用与 device_id 绑定的 Worker Bearer。控制语义对应 04_PROTOCOL_CONTRACT.md；此阶段 Controller 通过 HTTPS 动作和分页拉取事件，尚无手机持续订阅界面。

## 恢复规则

- Coordinator：SQLite 持久化 Plan、依赖、任务投递、乱序 inbox 及全局事件；重启重新检查未终态任务。断线不影响 Worker 已接受的执行。
- Worker：accepted 前落本地 Receipt 与幂等映射；claim 与 task.started 事件同事务保存。重复投递返回原 Receipt，执行计数不增加。
- Worker 重启时，已 claim 但无终态的内置任务标记 failed / interrupted_by_restart，不擅自重做；仅已接收但尚未 claim 的任务可继续。
- Worker 事件按 device_id 全局 worker_seq 生成；Coordinator 缓冲缺口，只按连续序号推进状态与依赖，提交后才 event.ack。Controller 用 coord_seq 跨设备补齐。
- 全局快照与事件水位在一致状态下取得；单 Plan 快照只能替换该卡片。当前不裁剪事件历史，因此尚无 CURSOR_EXPIRED 分支，未来加入保留策略时必须实现。
- 取消先持久化 Worker tombstone，再以 task.reconcile / task.snapshot 对账。未收到原投递的 Worker 明确返回 TASK_NOT_FOUND 后，Coordinator 可结束取消，且迟到投递不执行。
- 不自动重放已接受任务到另一台 Worker；离线设备保留原任务待对账。网络重连只重投递相同身份与内容。

## 本轮明确边界

支持 device.inspect（真实平台、CPU、物理内存、磁盘信息）及 report.aggregate（合并结构化上游结果），均为进程内只读/汇总能力。没有 process.exec、远程 Shell、进程树控制、Git 工作区补丁应用或 Artifact HTTP 传输。协议已定义 workspace_context、input_artifacts、ArtifactRef，但本轮拒绝带这些未实现预检的执行请求，避免静默忽略。小型结构化结果随事件持久化，不声称已完成文件传输。

Linux/Windows 原生进程组、Job Object、磁盘日志 tail、DSH Go UI/插件、二维码配对、100MB/1GB 断点续传及公网/蜂窝真机验收，按 03_EXECUTION_PLAN.md 与 07_VALIDATION.md 后续推进。本轮没有自动合并第一阶段 PR，也没有修改上游 DSH Go APK。

仓库尚未选定本项目源码 LICENSE；本 PR 是实现审阅，不是已授权的独立开源发行包。上游引用与品牌素材边界继续遵循 08_REUSE_AND_RESEARCH.md。
