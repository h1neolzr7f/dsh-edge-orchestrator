# 10. Grok Bot 式交互体验设计（Chat-first UX）

- 状态：Accepted product direction / Planned implementation
- 日期：2026-11-15
- 参考：Grok Bot 的消息驱动、执行过程可见、持续任务、人工接管、多 Bot 分工模式
- 核心原则：**保留 DSH Go 现有聊天、模型、Cordis、手机原生操作与语音交互；给它增加无感跨设备协作，而不是换成设备管理后台。**

## 1. 产品意图

用户与主 DSH Agent 对话，像委派工作给同事一样表达目标。主 Agent 自主发现授权设备、理解各自能力、生成 Plan 并委派工作。plan.submit 后，Go Coordinator 内嵌 packages/orchestrator 独立执行拓扑校验、依赖解锁、能力匹配、并发门控和重试状态机，手机离线也能推进。用户不需要亲自切换 Remote Host、复制提示词、创建多个会话或搬运文件。

与 Grok Bot 的区别：Grok Bot 的持续云电脑和 Bot 协作是现有产品形态；DSH Edge Orchestrator 允许 Android 手机、用户自己的 Windows/Linux/ARM、服务器和边缘硬件组成可调度网络。我们借鉴**交互语法与体验原则**，不模仿专有 UI 或声称已实现其全部特性。

## 2. 一级交互：聊天先于设备管理

主页面保留 DSH Go 对话列表与输入框。一个自然语言任务可直接触发 Fleet，原有本机对话功能不变。

推荐主路径：
1. 用户输入目标或语音委派：“把 A 机器的代码修复，B 机器运行测试，完成后告诉我。”
2. DSH Agent 读取授权节点和能力，自动生成任务计划；必要时只确认关键权限或缺失信息。
3. 聊天中插入一张**任务概览卡片**：任务名、状态、节点数、进度、暂停后续步骤/继续、审批/取消、展开详情。
4. 每个已派发节点呈现**执行卡片**：设备名称、当前工作、最近事件、状态和产物；允许展开日志。
5. 用户在任务运行中继续发送指令：“把测试交给 Linux”“取消部署”“先让我看一眼截图”。Agent 在同一对话关联工作：V1 已接受 DAG 不原地编辑，追加工作创建关联的新 Plan；取消走原 task_id/plan_id，已接受任务不静默迁移。
6. Coordinator 自动派发显式下游汇总任务并保存报告；Agent 在线时在同一聊天中呈现带证据的总结，手机离线不阻止后继汇总执行。
7. 关闭手机、换网络、从 PC 打开同一任务时保持 plan_id、步骤状态和事件；不会重新执行已接受操作。

系统内部的 Task DAG、Device Registry、Scheduler、Ledger 必须隐身于常规交互之后，仅高级用户进入详情页查看。

## 3. 页面信息架构

### A. 对话（默认入口）
- 原 DSH Go 对话、输入、模型和插件控件仍可使用。
- 用户消息和 Agent 回复之间可出现任务卡、节点执行卡、审批卡、文件卡。
- 允许在任务运行期间追加消息、引用文件和暂停后续步骤、恢复调度、处理审批或取消任务。暂停使用 plan.pause，继续使用 plan.resume；已接受进程继续运行，卡片明确展示此语义。
- 任务卡绑定 plan_id，设备执行卡绑定 task_id + device_id。
- 事件先合并成易读的阶段描述，调试展开后才显示原始日志，避免刷屏。

### B. 任务详情（由对话卡片进入）
- 顶部显示目标、总体状态、已完成/总步骤、当前阻塞原因。
- 中部显示 DAG/步骤列表和设备分工；默认按用户可理解的任务阶段，而不是技术依赖图。
- 每步显示启动/完成时间、执行设备、工具、结果验证、日志摘要及 Artifact。
- 下方展示完整事件记录、错误信息和可导出的诊断包。
- 支持取消指定步骤、重试允许重试的步骤、查看节点选择依据、审批、打开文件结果。

### C. 设备（次级入口）
- 设备列表、在线/退化/离线、网络质量、CPU/内存、可用能力、授权状态、Agent 模式。
- 一键配对、撤销、重新扫描能力和手动诊断。
- 选择设备不改变现有 DSH Go 的活动聊天主机；无需切换连接才能使用 Fleet。

### D. 工作台（可选高级视图）
- PC 使用双栏：左侧对话与任务、右侧执行预览或节点信息。
- 手机使用卡片和底部抽屉，保证在狭窄屏幕仍以对话为主。
- 不把“设备树/日志表格/终端”设计成首次打开后的默认首页。

## 4. 操作可见性分三个等级，严禁混淆

### L1 — 实时事件和日志（V1 必须）
- 适用于所有标准 Worker。
- 展示步骤/工具名称、状态、stdout/stderr 摘要、文件、时间线、错误与进度。
- 网络中断时保留最后状态，明确标注连接断开，不显示虚假实时画面。

### L2 — 截图或画面预览（在相应 adapter 可用时）
- Android：复用已有 PhoneControl 的屏幕捕获能力和权限。
- Windows/Linux：使用专用桌面捕获 adapter；无 GUI 的服务器不提供画面入口。
- 允许按需截图，后续可扩展低频 JPEG/WebP 预览；图片传输与控制信道分离。
- 无 adapter/权限时隐藏“查看屏幕”，而不是显示假画面。

### L3 — 人工接管与交还（高级扩展，不作为最小 V1 门槛）
- 当某一步需要用户参与时弹出“需要你接管”的任务卡片。
- 控制权属于明确的当前操作者；接管期间 Agent 对对应设备的输入操作暂停。
- 完成后点击“交还 Agent”，任务从已保存状态继续。
- 桌面远控需要明确的输入 adapter，不能把截图预览误称作远程控制。
- V1 提供计划调度暂停/继续（plan.pause / plan.resume）、审批及取消；暂停只阻止后续步骤派发。挂起已运行进程与全功能桌面接管需扩展适配器并单独验收。

## 5. 对话与任务的关系

- 对话中的一段任务请求建立唯一 plan_id。
- 多个设备子任务通过同一 plan_id 组织；用户在同一对话持续追加意图。
- 任务不随聊天窗口退出而中断；Coordinator 和 Worker 保留真实执行状态。
- 任务完成通知通过 DSH Go 的原有通知入口呈现，支持直接跳转对应会话。
- 用户可以在高级详情查看每个步骤的独立 agent/session，但不必为运行任务手动进入那些会话。
- 不为 Fleet 开发另一套模型、聊天记录、长期记忆或 Agent Loop。

## 6. 必须实现的 UI 事件契约

控制报文与展示事件分层，规范来源为 04_PROTOCOL_CONTRACT.md 第 4.8 节。plan.submit / accepted / rejected / cancel / snapshot / pause / resume 是 Envelope.kind；以下为 Event.type 或由持久化事实派生的 UI 投影名：
- plan.created / plan.updated / plan.completed
- step.assigned / step.started / step.progress / step.finished
- node.connection.degraded / node.connection.restored
- approval.requested / approval.resolved
- artifact.ready
- preview.available（只在确有截屏时）
- handoff.requested / handoff.released（仅在接管适配器存在时）

plan.submit 首次成功与 plan.created 同事务；调度/任务变化生成 plan.updated；首次终态生成一次 plan.completed，payload.status 明确 succeeded/failed/cancelled。plan.rejected 只显示请求错误；plan.snapshot 替换任务卡，不重复生成创建/完成通知。step.* 映射后端任务事实，不作为另一套执行状态机。

UI 由 integrations/dsh-flutter-ui 通过 packages/edge-link-dart 订阅。进入页面时获取全 Fleet 一致快照与 snapshot_coord_seq，随后接大于该屏障的事件；正常恢复携带 last_coord_seq，按 coord_seq 去重并原子保存投影和游标。单 plan.snapshot 只更新卡片及该卡屏障，不能推进全局 last_coord_seq；Worker 的 worker_seq/task_seq 不用作 UI 全局游标。卡片不自行推测运行结果。模型总结必须引用后端已记录的验证状态和产物，而不是把“Agent 说完成了”视作成功。

## 7. V1 交互验收

1. 一段自然语言或语音输入即可创建并执行跨设备任务；不要求用户先进入设备管理页面。
2. 聊天中自动出现任务总览卡和至少两个 Worker 的执行卡。
3. 两个 Worker 的任务时间窗口实际重叠，卡片与真实事件相符。
4. 用户可在任务运行中追加指令、审批、取消、查看证据与产物。
5. 手机关闭页面、失去网络 30–60 秒后恢复；同一对话/plan_id 继续显示已记录的状态，不重新运行任务。
6. 无 GUI 或无截图权限的节点绝不显示虚假画面；有能力的 Android/桌面节点可按需预览截图。
7. 失败消息包含发生在哪台设备、哪个步骤、当前可采取的操作，不要求用户手动找日志。
8. Fleet 开关关闭后，DSH Go 原有本机和远程聊天、Phone Control、模型选择、语音和 Cordis 插件表现不变。

## 8. 开发落地

- Phase 0–3：先打通 Coordinator/Worker 真实执行、持久化和恢复；同时建立 task/step UI 事件契约。
- Phase 4：优先交付聊天内任务卡、节点执行卡、进度、审批、取消、任务详情；不要用一个独立 dashboard 替代聊天入口。
- Phase 5：弱网状态可见、离线快照/恢复、通知、日志限流。
- Phase 6：按需截图预览和 Android 自身执行节点。
- Phase 7 / V1.5：人机接管与多设备可视化工作台。

**禁止的产品偏离：**只做设备列表、远程命令面板、拖拽工作流、必须先选择主机才能对话、把原 DSH Go 核心聊天/模型/手机工具替换成自研简单聊天页。

## 9. 参考依据

- Grok Bot documentation — Message and collaborate: https://docs.x.ai/grok-bot/chat-and-collaboration
- Grok Bot documentation — Use the computer and apps: https://docs.x.ai/grok-bot/computer-and-apps
- Grok Bot documentation — Get started: https://docs.x.ai/grok-bot/get-started
- DSH Go upstream: https://github.com/ccch1mneyyy/dsh-Go
