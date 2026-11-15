# Repository Setup

仓库已创建： https://github.com/h1neolzr7f/dsh-edge-orchestrator

## 本地开发

~~~bash
git clone https://github.com/h1neolzr7f/dsh-edge-orchestrator.git
cd dsh-edge-orchestrator
~~~

项目初期先实现 Go Coordinator / Edge Agent 与协议 Schema。引擎工具放在 integrations/dsh-cordis-plugin（TypeScript / Node.js，PRoot Ubuntu 内的 Cordis）；界面放在 integrations/dsh-flutter-ui（Flutter / Dart，聊天卡片、审批、扫码配对和设备状态）。Flutter 通过 packages/edge-link-dart 订阅 Coordinator，不承载 DAG 解锁。

## 本地最小开发环境

- Go 1.22+，按纯 Go SQLite 驱动支持范围锁定实际工具链及依赖版本；CGO_ENABLED=0
- Node.js / TypeScript（开发 Cordis 插件时需要，跟随 DSH 引擎版本）
- Flutter/Dart（仅开发 UI 时需要，跟随 DSH Go 锁定版本）
- Git
- Docker（可选，用于启动 Coordinator 依赖或演示环境）

## 第一轮代码目录

~~~text
apps/coordinator
apps/edge-agent
apps/edge-cli
packages/protocol
packages/edge-link
packages/edge-link-dart
packages/orchestrator
packages/capability-runtime
packages/artifact
integrations/dsh-cordis-plugin
integrations/dsh-flutter-ui
schemas
tests
~~~

## 开发入口

先按 docs/03_EXECUTION_PLAN.md 的 Phase 0 开发，不接入 DS Harness Remote。根 go.work 管理 apps/coordinator、apps/edge-agent、apps/edge-cli、packages/protocol、packages/edge-link，其他 Go package 建模块时加入。Coordinator 内嵌 packages/orchestrator 驱动 DAG；Dart package 不加入 Go Workspace。

SQLite 必须使用 modernc.org/sqlite 等纯 Go 驱动，不依赖 CGO；代码阶段验证 CGO_ENABLED=0 下 Windows amd64、Linux amd64、Linux arm64 交叉编译。当前仅文档，无 Go 模块或可执行程序，不能宣称已经编译通过。

集成前按 08_REUSE_AND_RESEARCH.md 核对 AGPL-3.0、SDK MIT 与品牌/素材边界，衍生发行使用自己的名称和素材；本仓库代码分发前落定 LICENSE。
