# Repository Setup

仓库已创建： https://github.com/h1neolzr7f/dsh-edge-orchestrator

## 本地开发

~~~bash
git clone https://github.com/h1neolzr7f/dsh-edge-orchestrator.git
cd dsh-edge-orchestrator
~~~

项目初期先实现 Go Coordinator / Edge Agent 与协议 Schema。DSH Go 集成放在 integrations/dsh-go，保持独立开发和测试。

## 本地最小开发环境

- Go stable
- Flutter/Dart stable（仅在开发 DSH Go 集成时需要）
- Git
- Docker（可选，用于启动 Coordinator 依赖或演示环境）

## 第一轮代码目录

~~~text
apps/coordinator
apps/edge-agent
apps/edge-cli
packages/protocol
packages/edge-link
packages/capability-runtime
packages/artifact
integrations/dsh-go
schemas
tests
~~~

## 开发入口

先按 docs/03_EXECUTION_PLAN.md 的 Phase 0 开发，不要先接入 DS Harness Remote。
