module github.com/h1neolzr7f/dsh-edge-orchestrator/packages/edge-link

go 1.22.0

require (
	github.com/coder/websocket v1.8.13
	github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol v0.0.0
)

require github.com/Masterminds/semver/v3 v3.3.1 // indirect

replace github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol => ./../protocol

require github.com/h1neolzr7f/dsh-edge-orchestrator/packages/artifact v0.0.0

replace github.com/h1neolzr7f/dsh-edge-orchestrator/packages/artifact => ../artifact
