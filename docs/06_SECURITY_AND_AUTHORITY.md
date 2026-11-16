# 06. 权限与执行权模型

本项目面向真实设备执行。权限设计要清晰、可理解、可审计，不做庞大的策略框架，也不能把“设备发现”误当成“用户授权”。

## 6.1 设备身份与配对

1. Controller 使用 `POST /v1/pairings` 创建一次性配对码，默认十分钟有效，可绑定 device_hint。
2. Edge Agent 使用 `--pair CODE --id DEVICE_ID` 经 HTTPS 原子兑换；同一码仅可成功一次。
3. Coordinator 生成高熵随机 bearer worker_token，仅持久化 SHA-256 token_hash，不保存明文设备令牌。
4. Edge Agent 在本地 SQLite identity 中保存设备 ID、Coordinator endpoint 与令牌；POSIX 数据库及 WAL/SHM 为 0600；Windows 使用专属用户目录并由部署者限制该目录 ACL（chmod 在 Windows 上不提供 POSIX 的用户隔离）。后续启动自动加载，拒绝跨 endpoint 使用保存凭据。
5. 静态 DSH_WORKER_TOKENS/DSH_WORKER_TOKEN 继续兼容；Coordinator 支持空静态映射的纯配对部署。V1 不包含令牌轮换、撤销 API 或设备密钥对生命周期。

V1 不复用 Remote Runtime 的身份生命周期；采用成熟 TLS/密码库，不自行设计密码算法。

## 6.2 权限等级

| 等级 | 能力 | 默认行为 |
|---|---|---|
| L0 观察 | 系统信息、资源、能力清单 | 配对后可用 |
| L1 只读 | 文件读取、Git 状态、日志读取 | 用户授权后可用 |
| L2 普通写入 | 指定工作区写文件、创建构建产物 | 按目录授权 |
| L3 执行 | 启动程序、运行测试、脚本和构建 | 按 capability 授权 |
| L4 高影响 | 系统设置、服务管理、外设动作、真实设备控制 | 单独授权，必要时逐次审批 |

## 6.3 授权作用域

授权绑定：
- device_id；
- capability_id 与版本范围；
- 工作目录或资源范围；
- 有效期；
- 是否允许后台执行；
- 最大并发数。

授权可撤销。撤销后阻止新任务；已完成任务不受影响。高影响任务按策略停止。

## 6.4 审批策略

TaskEnvelope.requires_approval 为 true 且依赖就绪时，Coordinator 持久化 pending 审批与 approval.requested 事件；waiting_approval 不占执行并发槽。Controller 使用 GET /v1/approvals 和 POST /v1/approvals/{id}/resolve（decision: approved/denied，reason 可选）决定。批准仅恢复此不可变任务的 queued 状态，拒绝产生 APPROVAL_DENIED 并使下游收敛。重复同一决定幂等，反向决定冲突；取消和 deadline 使待批记录失效，不能通过迟到批准复活任务。


常用开发流程可以配置长期授权，例如在指定仓库运行测试。以下动作默认逐次审批：
- 删除大量文件或覆盖关键目录；
- 修改系统级配置、服务或网络；
- 对外发送敏感文件或凭据；
- 控制真实硬件运动部件；
- 访问未授权工作区。

## 6.5 凭据

- API Key、SSH 私钥、Cookie 和访问令牌不进入 Task Envelope。
- 凭据保存在本机 Secret Store，由本地 Adapter 执行时读取。
- 任务日志只记录凭据引用和操作结果，不记录明文。
- 设备撤销后清理短期令牌和会话授权。

## 6.6 保持简单

- 不做配对即 root。
- 不允许模型生成任意 Shell 后无条件执行。
- V1 不引入复杂 RBAC/ABAC。
- Adapter 直接调用 Executor，不堆多层代理。
