#!/usr/bin/env bash
# Local TLS demo: one Coordinator, two real Worker processes, one three-node DAG.
set -euo pipefail
cd "$(dirname "$0")/.."
umask 077
mkdir -p .local/demo
if [[ ! -f .local/demo/cert.pem ]]; then
  openssl req -x509 -newkey rsa:2048 -nodes -days 30 -keyout .local/demo/key.pem -out .local/demo/cert.pem -subj '/CN=localhost' -addext 'subjectAltName=DNS:localhost,IP:127.0.0.1' 2>/dev/null
fi
if [[ ! -f .local/demo/credentials.json ]]; then
  python3 - <<'PY'
import json,secrets
from pathlib import Path
Path('.local/demo/credentials.json').write_text(json.dumps({'controller':secrets.token_urlsafe(32),'workers':{k:secrets.token_urlsafe(32) for k in ['worker-win-sim','worker-linux-sim']}}))
PY
fi
export DSH_CONTROLLER_TOKEN
DSH_CONTROLLER_TOKEN=$(python3 -c "import json;print(json.load(open('.local/demo/credentials.json'))['controller'])")
export DSH_WORKER_TOKENS
DSH_WORKER_TOKENS=$(python3 -c "import json;print(json.dumps(json.load(open('.local/demo/credentials.json'))['workers']))")
for app in coordinator edge-agent edge-cli; do go build -o ".local/demo/$app" "./apps/$app/cmd/$app"; done
pids=()
cleanup(){ for pid in "${pids[@]}"; do kill "$pid" 2>/dev/null || true; done; for pid in "${pids[@]}"; do wait "$pid" 2>/dev/null || true; done; }
trap cleanup EXIT INT TERM
.local/demo/coordinator --listen 127.0.0.1:8740 --db .local/demo/coordinator.db --tls-cert .local/demo/cert.pem --tls-key .local/demo/key.pem >.local/demo/coordinator.log 2>&1 &
pids+=("$!")
for attempt in {1..100}; do
  if curl -fsS --cacert .local/demo/cert.pem https://localhost:8740/healthz >/dev/null 2>&1; then break; fi
  sleep 0.1
done
for device in worker-win-sim worker-linux-sim; do
  token=$(python3 -c "import json,sys;print(json.load(open('.local/demo/credentials.json'))['workers'][sys.argv[1]])" "$device")
  DSH_WORKER_TOKEN="$token" .local/demo/edge-agent --id "$device" --db ".local/demo/$device.db" --coordinator https://localhost:8740 --ca .local/demo/cert.pem >".local/demo/$device.log" 2>&1 &
  pids+=("$!")
done
.local/demo/edge-cli --ca .local/demo/cert.pem submit examples/phase1-plan.json
.local/demo/edge-cli --ca .local/demo/cert.pem watch plan_phase1_demo
