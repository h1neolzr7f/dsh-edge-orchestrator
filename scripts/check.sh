#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
modules=(. packages/protocol packages/artifact packages/edge-link packages/orchestrator apps/coordinator apps/edge-agent apps/edge-cli)
for module in "${modules[@]}"; do
  go test "./${module}/..."
  go vet "./${module}/..."
done

node --test integrations/dsh-cordis-plugin/test/plugin.test.mjs
