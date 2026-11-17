#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
node scripts/demo-v1-full.mjs "$@"
