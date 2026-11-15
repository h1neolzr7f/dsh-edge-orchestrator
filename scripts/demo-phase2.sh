#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
DSH_DEMO_MODE=phase2 exec bash scripts/demo.sh
