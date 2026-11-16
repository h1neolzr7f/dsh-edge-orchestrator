# V1 完整验证报告

日期：2026-11-16。仓库：h1neolzr7f/dsh-edge-orchestrator。执行范围：Round3 V1 M-A→M-E，保留 DSH 原聊天/模型/手机工具边界。本报告描述已实现并实测的运行时与接入包；没有将独立组件冒充已发布手机应用。

## 里程碑与文件

| 里程碑 | 交付 | 主要文件/证据 |
|---|---|---|
| M-A | worktree 清理、毒事件隔离推进水位、Windows 进程树退出、调度负载一次扫描 | apps/edge-agent/{workspace_cleanup,proc_windows}.go；apps/coordinator/{store,scheduler}.go；V1_M_A_RUNTIME_VALIDATION.md |
| M-B |4MiB可续传 HTTPS 产物、哈希与原子发布、Controller预上传补丁、输入绑定与输出收集、隔离 Git 工作区 | packages/artifact；apps/coordinator/artifacts*.go；apps/edge-agent/{artifacts,workspace}*.go；V1_M_B_ARTIFACT_VALIDATION.md |
| M-C | 审批门禁、CLI、一次性配对与持久凭据 | approvals.go、pairings.go、identity.go、packages/protocol/authority.go；V1_M_C_AUTH_VALIDATION.md |
| M-D |8个原生 Cordis fleet 工具、自定义CA、超时与中止、真实跨语言联调 | integrations/dsh-cordis-plugin；tests/fixtures/tls-cert；V1_M_D_PLUGIN_VALIDATION.md |
| M-E | Dart 快照/游标投影、Flutter4组件、完整真实演示、CI与运行文档 | packages/edge-link-dart；integrations/dsh-flutter-ui；scripts/check-ui.sh、demo-v1-full.*；docs/V1_QUICKSTART.md |

## 实测结果

运行环境：Go1.24.2、Node24.22.1（另用真实Node18运行插件测试）、Flutter3.50.0/Dart3.14.0。Flutter使用官方SDK，无补丁；CI=true避免实例元数据探测，FLUTTER_SUPPRESS_ANALYTICS=true使官方实现返回NoOpAnalytics，不发送遥测。

| 检查 | 结果 |
|---|---|
| go schema生成逐字比较 + jsonschema Draft2020-12 check_schema | PASS，新增审批/配对/输出/上传定义齐全 |
| bash scripts/check.sh | PASS，8个Go模块test/vet + Node真实进程联调；最终Node1.991s |
| go test -race ./tests ./apps/coordinator ./apps/edge-agent ./packages/edge-link | PASS，无race；最终root97.414s/coordinator4.177s/agent10.959s/edge-link有效缓存PASS |
| bash scripts/build.sh（本地工作树GOFLAGS=-buildvcs=false） | PASS，CGO_ENABLED=0；Linux amd64/arm64、Windows amd64，共9程序 |
| bash scripts/check-ui.sh | PASS，Dart analyze无问题、7测试；Flutter analyze无问题、2 widget测试 |
| Node18 插件真实联调 | PASS，全部8工具，2.202s |
| 固定DSH SDK0.2.2-rc.1实际schema验证器 | PASS，8个工具定义，非伪造defineTool兼容层 |
| GitHub原生Linux/Windows CI（M-D，run43873573475） | 两平台Go+Node check/race PASS；Linux9构建PASS |
| bash scripts/demo-v1-full.sh | succeeded，2台动态配对Worker，真实补丁+JUnit+Gitdiff+下游挂载汇总 |

演示证据见 [V1_DEMO_EVIDENCE.json](V1_DEMO_EVIDENCE.json)：补丁163字节，首次演示两个真实 node:test 任务执行窗口重叠826ms，修复后的最终演示重叠823ms，汇总产物63字节且SHA256核验通过。报告内容由实际测试程序产生；不是预先伪造成功文本。示例命令的临时凭据与证书仅在本地测试生命周期内使用。

## API 清单

| 角色 | 接口 |
|---|---|
| Controller | GET /v1/devices、/v1/snapshot、/v1/events；POST /v1/plans；GET /v1/plans/{id}；POST /v1/plans/{id}/{pause,resume,cancel} |
| 审批 | GET /v1/approvals；POST /v1/approvals/{id}/resolve（approved/denied） |
| 配对 | Controller POST /v1/pairings；公开一次性兑换 POST /v1/pairings/claim |
| 产物 | POST /v1/artifacts/uploads；PATCH /v1/artifacts/uploads/{id}?offset=N；POST /v1/artifacts/uploads/{id}/complete；GET /v1/artifacts/{id}（Range/ETag） |
| Worker | WSS /v1/link；静态或动态设备绑定令牌；上传仅分配任务，下载仅明确所需产物 |
| Cordis | fleet.list_devices、inspect_device、list_capabilities、plan_tasks、dispatch_task、get_task、cancel_task、get_artifacts |
| Flutter | FleetPlanOverviewCard、FleetWorkerExecutionCard、FleetApprovalBanner、FleetPairingSheet |

Dart FleetProjectionStore 对全局 lastCoordSeq 与单卡 cardBarrierSeq 分别建模，分页固定 high_watermark、排序去重，保存失败时不提交投影和游标。并发刷新串行化。单卡屏障领先不漏另一Plan事件。Controller先上传再创建Plan的artifact.ready不会产生假卡或404死循环。权限撤销后真实截图预览立即关闭。

## RED→GREEN 与复核

M-A/M-B的历史发现、每轮修复和真实Windows证据保留在分里程碑报告中。M-C原始缺失接口RED由中断的实现者记录，恢复后全量和真实TLS链路重新验证。M-D补充测试实际发现首分块后的chunk_size丢失和DSH不支持minimum schema关键词，均经RED→GREEN修复。

M-E最初Dart依赖解析晚于代码创建，首次测试不是RED，已如实记录并丢弃那份生产实现；在现有测试下观察缺失client/models/store的RED后，重新实现最终版本，6测试GREEN。另有截图权限撤销用例先观察Expected false/Actual true，再以每次sync刷新设备授权修复。Flutter先观察4组件缺失的真实编译RED，再实现后通过窄屏/回调/预览门禁测试。演示修正了真实Node JUnit格式和task.succeeded事件名的断言后成功，未替换测试结果。

## 明确边界与实施决定

- 上游DSH固定提交f5fa9256db4c82d601078b8064056b93dc3560ec、SDK0.2.2-rc.1。已核对真实Cordis注册与Flutter聊天行入口；交付可嵌入包，不宣称完成上游APK移植。
- 无实际截图适配器时只显示L1事实/日志。L2需已授权capture能力、真实preview.available ArtifactRef及宿主验证图像；L3接管未实现，不渲染接管控制。
- POSIX identity与WAL/SHM0600；Windows由部署者限制专用数据目录ACL。V1不自动配置ACL，也无令牌撤销/轮换API。若目录权限错误，本地凭据可能暴露；这项部署要求已写入快速指南。
- 文件系统发布验证包括同步屏障、故障注入与真实FS测试；未宣称物理断电试验。Dart缓存提供投影+游标原子替换，不宣称目录fsync级断电保证。
- Cordis产物下载使用私有workspaceRoot，拒绝遍历/符号链接/覆盖，文件名取artifact_id。目录不能由不可信本地用户并发改写。
- 因实现子任务额度中断，M-C起改为本会话继续，使用最终整分支独立复核补足逐任务复核席位；风险是逐任务新上下文复核减少。
- Flutter3.50将SDK Material入口标记弃用；为兼容现有DSH版本保留官方仍导出的入口，只对该import定点抑制弃用提示，不屏蔽其他分析诊断。
- 自动审批曾拒绝Flutter实例元数据/Google遥测请求。后续使用已核对源码的标准CI/遥测关闭配置，去除了被拒绝的网络行为；没有修改SDK或访问受保护端点。

## 最终整分支审查与修复

独立整分支审查覆盖全部97文件，记录于 [V1_FINAL_REVIEW.md](V1_FINAL_REVIEW.md)。原始意见为1项Critical与7项Important；它们均进入同一修复轮，未以第二次审查代替复现测试。

| 发现 | 最终行为与RED→GREEN证据 |
|---|---|
| 审批前/终态产物授权 | Worker仅dispatched/accepted/running可传输；发布事务再次核验，跨取消的哈希不发布；完成会话同所有者重复查询/完成仍幂等。HTTP测试实际复现待批泄漏和终态写入后转绿 |
| 非法事件改坏未派发任务 | failed/cancelled与隔离失败只影响实际执行任务；queued/waiting/rejected原状态保持，隔离推进worker水位，后续其他任务正常，历史重复仍ACK；waiting不再发Worker reconcile |
| 完整补丁文件的EOF续传 | 真实ServeContent剥离416 ETag的配置下复现失败；以新的已认证成功响应获取摘要并验证，绝不信任本地自算哈希 |
| 崩溃后的终态工作区 | 终态账本作为持久清理队列，重启重试，非续跑任务恢复失败后也清理；真实Git注册/目录消失且诊断保留 |
| Go重定向外送 | HTTPS API、上传/下载、公开兑换及WSS一律拒绝跳转，复制调用者Transport但不放行重定向；真实HTTPS→HTTP目标收到0次请求，重定向为永久语义错误 |
| Cordis幂等重试 | 稳定ID/Key和存储创建时间恢复；丢失成功响应后重放、并发/普通重试、单任务再次派发均复用Plan；修改语义仍IDEMPOTENCY_CONFLICT |
| 冷开/重建丢日志 | Go一致快照增加有索引的16KiB真实日志尾；Dart恢复该尾部/已提交摘要，再追加屏障后的日志，冷开与游标过期测试转绿 |
| 首次配对响应丢失 | Controller绑定设备ID签发一次性恢复码；实际丢弃HTTPS响应后同Agent成功恢复并持久化，独立二次兑换失败；已登记身份及登记竞态拒绝替换，保护历史事件序号 |

全量回归还复现了共享Git仓库的并发建工作区失败：全局git worktree prune会删除另一Worker尚未写完gitdir/commondir的全新元数据，即使设置较老expire仍然如此。已改为只对当前checkout执行带双force的targeted remove并验证自身注册消失，不再全局prune；确定性“他人正在创建的元数据目录必须保留”用例先失败再通过，原跨Worker并行流程连续3次通过。此处修订了早期M-A计划中的全局prune步骤，保留其他陈旧工作区由仓库所有者处理，避免删除不属于本任务的资产。

### 保留的小问题

- 未完成上传若最终哈希失败，当前会话不能清零修复；稳定源文件后换一个产物名称/新会话重传。不会将错误内容发布为ready。
- Cordis下载保存时磁盘写满，可能残留本次新建的部分文件；清除该文件后重试。不会覆盖既有文件；目录保持私有。

这些是资源/损坏后的恢复便利性问题，已在运行说明披露；没有阻塞正确执行、授权或完整性验证。日志尾部初实现曾放在内部调度/事件刷新的loadPlan热路径，使8MiB回放race压测在120s窗口失败（2051条生成、1630条已确认）。已把补齐限制为对外Plan/Fleet/控制响应的一致快照读取，内部调度不读日志；相同8MiB race回放随后50.778s通过，未放宽任何时限。最终完整Go/Node回归及竞争检测已通过；Dart7测试、Flutter2测试与分析、9个构建、schema和演示均通过。最终原生CI见 [PR#4 checks](https://github.com/h1neolzr7f/dsh-edge-orchestrator/pull/4/checks)，合并时核验。

实施决定补充：首次配对恢复只允许从未登记过的ID，已有事件历史不能用新账本覆盖；这保留worker_seq契约，代价是已登记设备仍需恢复原数据库或换新ID。
