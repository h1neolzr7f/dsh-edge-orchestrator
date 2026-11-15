#!/usr/bin/env bash
# TLS demo: Phase 1 built-ins, or Phase 2 real unittest + Git workflow.
set -euo pipefail
cd "$(dirname "$0")/.."
umask 077
export DSH_DEMO_DIR=.local/demo
plan_file=examples/phase1-plan.json
plan_id=plan_phase1_demo
if [[ "${DSH_DEMO_MODE:-phase1}" == phase2 ]]; then
  export DSH_DEMO_DIR=.local/demo-phase2
  plan_file="$DSH_DEMO_DIR/plan.json"
  plan_id=plan_phase2_demo
fi
mkdir -p "$DSH_DEMO_DIR"
if [[ ! -f "$DSH_DEMO_DIR/cert.pem" ]]; then
  openssl req -x509 -newkey rsa:2048 -nodes -days 30 -keyout "$DSH_DEMO_DIR/key.pem" -out "$DSH_DEMO_DIR/cert.pem" -subj '/CN=localhost' -addext 'subjectAltName=DNS:localhost,IP:127.0.0.1' 2>/dev/null
fi
python3 - <<'PY'
import json,secrets,os,subprocess
from pathlib import Path
d=Path(os.environ['DSH_DEMO_DIR']);credentials=d/'credentials.json'
if not credentials.exists():credentials.write_text(json.dumps({'controller':secrets.token_urlsafe(32),'workers':{k:secrets.token_urlsafe(32) for k in ['worker-win-sim','worker-linux-sim']}}))
if os.environ.get('DSH_DEMO_MODE')=='phase2':
 root=d/'workspace';root.mkdir(exist_ok=True)
 def git(*args):return subprocess.check_output(['git','-C',str(root),*args],stderr=subprocess.STDOUT).decode().strip()
 if not (root/'.git').exists():
  git('init','-q');(root/'test_example.py').write_text('import unittest\nclass Example(unittest.TestCase):\n def test_add(self): self.assertEqual(2+2,4)\n');git('add','.');git('-c','user.name=Demo','-c','user.email=demo@example.invalid','commit','-qm','demo fixture')
 head=git('rev-parse','HEAD');context={'root_dir':'workspace/project','expected_git_head':head,'policy':'require_clean_or_match'}
 tasks=[{'task_id':'phase2_test','target_device_id':'worker-win-sim','capability':{'id':'process.exec','version':'1.x'},'input':{'program':'python3','args':['-m','unittest','-v'],'cwd':'workspace/project','timeout_ms':60000},'depends_on':[]},
 {'task_id':'phase2_diff','target_device_id':'worker-linux-sim','capability':{'id':'git.inspect','version':'1.x'},'input':{'workspace':'workspace/project','mode':'summary'},'depends_on':[]},
 {'task_id':'phase2_summary','target_device_id':'worker-linux-sim','capability':{'id':'report.aggregate','version':'1.x'},'input':{},'depends_on':['phase2_test','phase2_diff']}]
 for task in tasks:task['workspace_context']=context
 (d/'plan.json').write_text(json.dumps({'plan_id':'plan_phase2_demo','idempotency_key':'phase2:demo','goal':'真实单元测试与 Git 检查并行汇总','concurrency_limit':2,'created_at':'2026-11-15T12:00:00Z','tasks':tasks},ensure_ascii=False))
PY
export DSH_CONTROLLER_TOKEN
DSH_CONTROLLER_TOKEN=$(python3 -c "import json,os;print(json.load(open(os.environ['DSH_DEMO_DIR']+'/credentials.json'))['controller'])")
export DSH_WORKER_TOKENS
DSH_WORKER_TOKENS=$(python3 -c "import json,os;print(json.dumps(json.load(open(os.environ['DSH_DEMO_DIR']+'/credentials.json'))['workers']))")
for app in coordinator edge-agent edge-cli; do CGO_ENABLED=0 go build -o "$DSH_DEMO_DIR/$app" "./apps/$app/cmd/$app"; done
pids=()
cleanup(){ for pid in "${pids[@]}"; do kill "$pid" 2>/dev/null || true; done; for pid in "${pids[@]}"; do wait "$pid" 2>/dev/null || true; done; }
trap cleanup EXIT INT TERM
"$DSH_DEMO_DIR/coordinator" --listen 127.0.0.1:8740 --db "$DSH_DEMO_DIR/coordinator.db" --tls-cert "$DSH_DEMO_DIR/cert.pem" --tls-key "$DSH_DEMO_DIR/key.pem" >"$DSH_DEMO_DIR/coordinator.log" 2>&1 &
pids+=("$!")
for attempt in {1..100}; do
  if curl -fsS --cacert "$DSH_DEMO_DIR/cert.pem" https://localhost:8740/healthz >/dev/null 2>&1; then break; fi
  sleep 0.1
done
workspaces=()
if [[ "${DSH_DEMO_MODE:-phase1}" == phase2 ]]; then workspaces=(--workspace "workspace/project=$PWD/$DSH_DEMO_DIR/workspace"); fi
for device in worker-win-sim worker-linux-sim; do
  token=$(python3 -c "import json,os,sys;print(json.load(open(os.environ['DSH_DEMO_DIR']+'/credentials.json'))['workers'][sys.argv[1]])" "$device")
  DSH_WORKER_TOKEN="$token" "$DSH_DEMO_DIR/edge-agent" --id "$device" --db "$DSH_DEMO_DIR/$device.db" --coordinator https://localhost:8740 --ca "$DSH_DEMO_DIR/cert.pem" "${workspaces[@]}" >"$DSH_DEMO_DIR/$device.log" 2>&1 &
  pids+=("$!")
done
"$DSH_DEMO_DIR/edge-cli" --ca "$DSH_DEMO_DIR/cert.pem" submit "$plan_file"
"$DSH_DEMO_DIR/edge-cli" --ca "$DSH_DEMO_DIR/cert.pem" watch "$plan_id"
