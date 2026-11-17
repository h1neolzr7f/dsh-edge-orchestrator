# V1 完整链路运行指南

Go1.24.2、Git、Node18+（DSH 当前内置引擎要求24.9+）。Flutter组件验证使用 Flutter3.50/Dart3.14。TLS 必须有效，生产部署使用自己的 CA 或可信证书。

## 一键真实演示

```bash
bash scripts/check.sh
bash scripts/demo-v1-full.sh /tmp/fleet-demo-evidence.json
bash scripts/build.sh
```

演示会编译真实程序，启动一个 HTTPS Coordinator 和两台动态配对的 Worker；提交前上传修改代码的补丁，分别在隔离 Git worktree 执行真实 node:test 测试输出 JUnit，平行执行 Git diff，再让下游挂载两份报告完成汇总。脚本断言 succeeded 和两个测试任务的时间重叠，验证产物 SHA256。演示证书和临时进程/目录自动清理；DSH_KEEP_DEMO=1 可保留演示目录。演示不会连接外部模型服务。

## 部署与配对

```bash
export DSH_CONTROLLER_TOKEN='your-controller-secret'
./dist/linux-amd64/coordinator --listen 0.0.0.0:8740 --db /private/fleet/coordinator.db --tls-cert /private/cert.pem --tls-key /private/key.pem
```

另一终端使用相同 Controller token：

```bash
./dist/linux-amd64/edge-cli --url https://coordinator.example:8740 --ca /private/ca.pem pair workstation-a
```

取得 pairing_code 后在目标设备运行：

```bash
./dist/linux-amd64/edge-agent --id workstation-a --pair PAIRING_CODE --coordinator https://coordinator.example:8740 --ca /private/ca.pem --db /private/fleet/worker.db --workspace workspace/project=/absolute/repository --allow-programs '["/absolute/path/to/node"]'
```

后续启动省去 --pair、--id 与 DSH_WORKER_TOKEN，保留同一数据库、Coordinator URL、工作区和允许程序配置。配对码十分钟失效且仅能兑换一次，凭据绑定设备和 endpoint。POSIX 数据库及 sidecar0600；Windows 将数据库放在仅当前用户可访问的专用目录并配置 ACL。静态 DSH_WORKER_TOKENS/DSH_WORKER_TOKEN 方式继续可用。V1 不提供通用令牌撤销/轮换接口，首次配对中断恢复见下文。

## 首次配对中断的恢复

若首次兑换的响应丢失、令牌还没存入 Worker，Controller 可为尚未登记过的同一设备 ID 签发恢复码：

```bash
edge-cli --url https://coordinator.example:8740 --ca /private/ca.pem pair-recover workstation-a
```

然后用新码重新执行 edge-agent --pair，仍使用相同 ID 和本地数据库。该码绑定 ID、一次性原子兑换，替换丢失的首次凭据；旧码及旧令牌不再可用。仅 Controller 能发码。已经登记或产生历史事件的设备禁止这种恢复，防止 worker_seq 重置破坏账本；应恢复原 Worker 数据库，或使用新 device_id。恢复码不提供通用令牌撤销/轮换框架。

## 审批与任务

TaskEnvelope 支持 requires_approval、approval_reason、output_artifacts、input_artifacts、input_artifact_bindings 与 workspace_context。审批仅在依赖就绪后生成，不占执行名额；拒绝使下游以 DEPENDENCY_FAILED 收敛。

```bash
edge-cli --url https://coordinator.example:8740 --ca /private/ca.pem approvals
edge-cli --url https://coordinator.example:8740 --ca /private/ca.pem approve APPROVAL_ID 'checked requested action'
edge-cli --url https://coordinator.example:8740 --ca /private/ca.pem deny APPROVAL_ID 'not authorized'
edge-cli --url https://coordinator.example:8740 --ca /private/ca.pem pause PLAN_ID
edge-cli --url https://coordinator.example:8740 --ca /private/ca.pem resume PLAN_ID
edge-cli --url https://coordinator.example:8740 --ca /private/ca.pem cancel PLAN_ID
```

暂停后续步骤，已运行进程继续。取消是真实进程树终止。重复同一审批决定幂等，反向决定冲突，取消或超时后迟到审批不能复活任务。

## 接入现有 DSH

Node 插件见 [Cordis 插件说明](../integrations/dsh-cordis-plugin/README.md)。8个 fleet 工具注册到现有 ctx.tools，保留模型渠道、原聊天、语音和手机操控。自定义 CA 保持证书校验，所有调用支持中止/超时。

Flutter 组件见 [聊天卡片说明](../integrations/dsh-flutter-ui/README.md)，Dart 同步见 [客户端说明](../packages/edge-link-dart/README.md)。把 PlanCard 放入当前对话消息流，以 plan_id 关联原任务。首次/游标过期读取全局快照；单卡刷新不推进全局游标。组件提供暂停/恢复/取消、审批和配对回调，不替换原聊天应用。

当前交付为经过真实测试的接入包；没有声称已构建修改版上游 APK。截图预览需要真实适配器、授权能力与 preview.available 产物，当前 Worker 无截图能力时没有假屏幕。L3 人工接管适配器不在本轮范围。

## 验证

```bash
bash scripts/check.sh        # 所有 Go 模块 test/vet + Node 真实进程联调
bash scripts/check-ui.sh     # Dart analyze/test + Flutter analyze/widget test
# CI=true 和 FLUTTER_SUPPRESS_ANALYTICS=true 由 check-ui.sh 设置

go test -race ./tests ./apps/coordinator ./apps/edge-agent ./packages/edge-link
bash scripts/build.sh       # Linux amd64/arm64、Windows amd64，共9个无CGO程序
```

[完整验证报告](../reports/V1_FULL_VALIDATION.md)包含里程碑、API、实测证据与边界。分析源码可直接下载仓库ZIP，不需要模型API Key。
