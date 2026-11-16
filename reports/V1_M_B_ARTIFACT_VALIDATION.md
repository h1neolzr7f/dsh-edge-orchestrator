# Task2 implementation report (recovered historical evidence)
Baseline implementation remote775054b (original localebfd894) implements M-B1..4. Both named real TLS regressions observed HTTP404 RED then GREEN. Full scripts/check.sh passed root13.739s and7submodules test/vet. Artifact focused race passed agent1.514s/coordinator1.781s/root11.473s. Nine production cross-builds passed using GOFLAGS=-buildvcs=false for isolated-worktree stamping. Windows Agent/root tests cross-compiled only, later native CI found CRLF fixture failure. Draft2020-12 schema validated by controller, includes ArtifactBinding/Ref/UploadRequest/UploadSession/InputArtifact/OutputArtifact.
Canonical Go APIs in packages/artifact: Client{URL,Token,HTTP}, NewClient, CreateUpload(ctx,in), UploadChunk(ctx,id,offset,b), CompleteUpload(ctx,id), UploadFile(ctx,in,path), DownloadFile(ctx,id,path,expectedSHA). Edge-link wrappers CreateArtifactUpload, UploadArtifactChunk, CompleteArtifactUpload, UploadArtifactFile, DownloadArtifactFile. Protocol OutputArtifact, ArtifactUploadRequest/Session included.
Lifecycle: durable Accept, status check before preflight, accepted preflight with download staging outside checkout, claim+started after preflight, explicit persistent-process resume restores worktree/result for interrupted output upload, output upload before success, cleanup after durable finish. Late terminal/running duplicates skip workspace changes. Scratch without workspace_context is task-owned, no host overlay. Tests cover partial download restart,10MiB interrupted output upload restart/one execution, missing/hash/conflict permissions, invalid patches, bindings while paused, standalone upload.
Artifact auth currently maps ControllerToken and static WorkerTokens in artifactsHTTP; assigned(device,plan,task), required(device,id) run under s.mu. M-C must add dynamic credentials to artifact HTTP and workerLink together.
Independent review found seven blockers in task-2-review-report.md. Fix round1 not yet implemented at environment reset; historical evidence is not verification of forthcoming fixes. Append actual RED/GREEN fix evidence here. Report stays ignored; do not force-add.

## Fix round 1 — recovered baseline 775054b, 2026-11-16

Scope: seven review blockers plus native CRLF fixture. No M-A rebuild or later milestone. Product/test checkpoints (controller owns publication and scoped review):
- `2ea07fa` — plan-scoped snapshot/symbolic provenance, alternate-media content idempotency, nested execution-cwd input mounts, shutdown/deadline claim guards.
- `758820c` — artifact disk I/O/hash isolated from scheduler/cancellation under per-upload locks, with reference-counted lock removal.
- `3c1f03a` — durable initial staging/ready name publication, POSIX directory/ancestor fsync, Windows native write-through moves and file flush, recoverable known-name replay after rename.
- `692d188` — securely open once and transfer checked descriptors; Linux nonblocking FIFO rejection/openat no-follow directory walk; Windows final-handle-path confinement; extended real Git worktree mount/process regression and no-started claim check. Invalid local HTTPS endpoint errors now return directly instead of retrying until context expires.
- `c88e131` — normalize only CRLF in the artifact-report child text fixture, with real LF/CRLF/wrong-text subprocess regression. Production Git EOL policy and byte-exact binary checks untouched.

### Focused RED → GREEN evidence

All commands below use `PATH=/workspace/scratch/toolchains/go/bin:$PATH` and execute from repository root.

1. `go test ./apps/coordinator ./apps/edge-agent -run 'TestArtifact(RetryAlternateMedia|ProvenanceDoesNot|NestedProcessMount|ClaimBoundary)' -count=1`
   - RED: alternate media returned IDEMPOTENCY_CONFLICT; foreign staged artifact leaked into snapshot and resolved downstream binding (queued instead of rejected); nested cwd input missing; deadline and shutdown both claimed execution, count=1/status=running.
   - GREEN: coordinator0.027s, agent0.035s. Extended nested regression then covers scratch and a real Git detached worktree, runs `git hash-object input` in nested cwd, verifies host/root remain unmounted. Claim checks additionally assert no task.started event.
2. `go test ./tests -run TestArtifactSlowCompletionDoesNotBlockLiveProcessCancellation -count=1`
   - RED2.120s: real HTTPS Control exceeded its strict2s context while separate completion was stalled in hashing.
   - GREEN0.365s: actual process tree terminated and no live child remained while hashing was still blocked. Controlled Linux FIFO in coordinator's private test artifact directory supplies the slow read; no production hash test hook. Separate120s bulk log-integrity budget unchanged.
3. `go test ./apps/edge-agent -run TestStandaloneArtifactUploadRejectsFIFOWithoutBlocking -count=1`
   - RED0.512s: standalone open blocked beyond500ms; test unblocked baseline FIFO safely.
   - GREEN included agent0.275s focused Artifact/Upload run and later agent0.018s focused regression run.
4. `go test ./apps/coordinator -run TestArtifactPublicationFailureDoesNotAdvanceSQLite -count=1`
   - RED0.026s: injected staging/ready namespace durability failures were ignored, upload/artifact/completed SQLite rows advanced.
   - GREEN: `go test ./apps/coordinator -run 'TestArtifact(PublicationFailure|CrashAfter)' -count=1` coordinator0.030s. Publication seam faults occur before DB acknowledgment; existing real crash-after-rename/one-event idempotence regression exercises native publication/recovery.
5. `go test ./apps/edge-agent ./packages/artifact -run 'TestConfinedArtifactOpenRejectsReplacedParent|TestUploadOpenFileKeepsCheckedDescriptor' -count=1`
   - Regression sensitivity RED verified by temporarily restoring prior path-following/reopen behavior: replaced parent escaped confinement; TLS upload used replacement pathname bytes instead of checked descriptor. Secure implementations restored before continuing.
   - GREEN combined command `go test ./apps/edge-agent ./packages/artifact ./tests -run 'TestStandaloneArtifactUploadRejectsFIFO|TestConfinedArtifactOpenRejectsReplacedParent|TestUploadOpenFileKeepsCheckedDescriptor|TestArtifactReportFixtureAcceptsNativeCRLF' -count=1`: agent0.018s/artifact0.024s/root0.031s. Descriptor remains caller-owned and hashes from offset0 even if caller seek offset is nonzero. Full coordinator TLS upload/download proves exact original bytes survive pathname replacement.
6. `go test ./tests -run TestArtifactReportFixtureAcceptsNativeCRLF -count=1`
   - RED0.018s: CRLF child exited9.
   - GREEN included combined root0.031s above. LF and CRLF accepted; incorrect text still rejected.
7. `go test ./packages/artifact -run TestUploadInvalidEndpointReturnsConfigurationError -count=1`
   - RED0.124s: invalid local endpoint retried until context deadline.
   - GREEN included artifact0.025s command below.

Final focused coverage before full suite:
- `go test ./apps/edge-agent ./apps/coordinator ./packages/artifact ./tests -run 'Artifact|TestDAGArtifact' -count=1` — agent0.298s/coordinator0.639s/root2.455s; artifact package had no matching tests for this filter (descriptor tests run explicitly above/below).
- `go test ./apps/edge-agent ./packages/artifact ./tests -run 'TestUpload|TestStandaloneArtifact|TestConfinedArtifact|TestArtifact(NestedProcess|ClaimBoundary|ReportFixture)' -count=1` — agent0.088s/artifact0.019s/root0.030s, all pass.

### Full applicable verification after all fixes

- `PATH=/workspace/scratch/toolchains/go/bin:$PATH bash scripts/check.sh` — PASS, exit0, run once after fixes. Root18.104s; protocol0.005s; artifact0.020s; edge-link0.114s; orchestrator0.003s; coordinator0.650s; agent0.588s; CLI compiled. All corresponding `go vet` invocations passed. Full output: `task-2-fix-check.log` beside this report.
- `PATH=/workspace/scratch/toolchains/go/bin:$PATH go test -race ./apps/coordinator ./apps/edge-agent ./packages/artifact ./tests -run 'Artifact|DAGArtifact|UploadOpen|UploadInvalid' -count=1` — PASS, exit0: coordinator2.207s/agent2.069s/artifact1.123s/root15.580s. Full output: `task-2-fix-race.log`.
- `git diff --check` — PASS; product tree clean after checkpoints (this report/logs remain ignored).

### Public APIs for downstream consumers

Added, no existing methods removed:
- `(*artifact.Client).UploadOpenFile(ctx context.Context, in protocol.ArtifactUploadRequest, f *os.File) (protocol.ArtifactRef, error)` — accepts an already confined, regular, seekable descriptor; hashes from offset0; resumes using server-confirmed offset; never reopens f.Name(); caller owns/closes descriptor.
- `(*edgelink.Client).UploadArtifactOpenFile(ctx context.Context, in protocol.ArtifactUploadRequest, f *os.File) (protocol.ArtifactRef, error)` — same ownership and behavior wrapper.

`UploadFile` and `UploadArtifactFile` remain compatible path-based convenience methods. No wire/schema changes.

### Durability guarantee and native validation boundary

POSIX flushes initial empty file before publishing `.part`, then fsyncs artifact directory; creating a directory fsyncs its parent. Chunk bytes remain file-synced before confirmed offsets. Verified ready publication fsyncs containing directory before SQLite artifact/completion transaction. Rename-before-DB recovery republishes via the same two known names, so interruption during retry remains recoverable.

Windows uses `FlushFileBuffers` through Go File.Sync and documented `MoveFileExW(..., MOVEFILE_WRITE_THROUGH)` for initial `.part` and final `.ready` publication; initial artifact directory also uses native write-through directory rename. No COPY_ALLOWED, no no-op directory-sync fallback. Official contract: https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-movefileexw describes on-disk move completion, while explicit flush guarantee is phrased for copy/delete moves. https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-flushfilebuffers describes flushing a specified file's buffered information. This implements the agreed documented API direction; it does not prove complete parent/ancestor durability under physical power loss, filter drivers, or hardware caches. Controller was notified promptly and accepted this stated boundary. Windows runtime/recovery/CRLF execution remains controller-owned native CI; cross-build success is not native runtime evidence.

Attempted Linux syscall trace was unavailable: ptrace denied with PTRACE_TRACEME Operation not permitted. That exploratory trace test was removed; final durability regression uses per-server publication fault injection plus real native filesystem/recovery calls. Initial cross-build without GOFLAGS hit isolated-checkout VCS-stamping error, then rerun with the explicitly authorized `GOFLAGS=-buildvcs=false`; no toolchain/source policy workaround was added.

### Final cross-build result

PASS, exit0, all nine `CGO_ENABLED=0` builds (linux/amd64, linux/arm64, windows/amd64 × coordinator, edge-agent, edge-cli). Exact shell:
```bash
export PATH=/workspace/scratch/toolchains/go/bin:$PATH
export CGO_ENABLED=0
export GOFLAGS=-buildvcs=false
task_build_root=$(mktemp -d /tmp/mb-cross-builds-XXXXXX)
for target in linux/amd64 linux/arm64 windows/amd64; do
  export GOOS=${target%/*}
  export GOARCH=${target#*/}
  for component in coordinator edge-agent edge-cli; do
    go build -o "$task_build_root/$component-$GOOS-$GOARCH" "./apps/$component/cmd/$component" || exit 1
    printf 'PASS %s %s\n' "$target" "$component"
  done
done
GOOS=windows GOARCH=amd64 go test -c -o "$task_build_root/coordinator-windows-test.exe" ./apps/coordinator || exit 1
GOOS=windows GOARCH=amd64 go test -c -o "$task_build_root/agent-windows-test.exe" ./apps/edge-agent || exit 1
GOOS=windows GOARCH=amd64 go test -c -o "$task_build_root/root-windows-test.exe" ./tests || exit 1
```
Windows coordinator, agent and root test cross-compilation: PASS, exit0. Binaries are disposable verification outputs in /tmp, not committed.

Final status: DONE_WITH_CONCERNS only for the documented Windows native execution/power-loss guarantee boundary. All seven code blockers addressed, CRLF fixture fixed, minor invalid-endpoint retry issue also addressed. Product HEAD `c88e131`; baseline-to-HEAD review range `775054b..c88e131`. No source changes after the full suite/race/build verification.

## Fix round 2 — base c88e131, 2026-11-16

Read `task-2-fix1-review-report.md` before changing source. Scoped to its two remaining Important findings plus controller-reported native Windows restart failure (remote37436e96/run43811651063). Round1 fixes retained. No later milestone or new public API.

Coherent GREEN checkpoints:
- `8c8a56a` — Linux directory visibility is no longer treated as publication durability. Existing and newly created directory paths recursively re-establish ancestor/parent fsync barriers on each use, including retry after failed fsync. Filesystem root terminates recursion because it has no parent name.
- `9f60d0d` — `processPaths` selects authorized base and nested execution cwd from the same longest workspace mapping used by `processDir`. Output-only unprepared processes no longer guess workspace/project. Secure open-from-authorized-base and descriptor transfer remain intact.
- `0969240` — persistent-process shutdown classification at cancellation-marker and terminal-commit boundaries. `Run` deferred/AfterFunc root stops now use `stopRoot` under a.mu; Close already cancels under that mutex. Marker publication and terminal commit use the same lock and authoritative durable user-cancellation map. Parent shutdown detaches verified persistent runners, including completed exit records awaiting durable finish. A genuine user cancellation still retains its marker/tree cleanup path. Resumable completed results are preserved for recovery instead of becoming interrupted failures. Slow-log I/O seam is a small monitor helper accepting the actual log-reader callback; no public/test-hook config field.

### Covering RED → GREEN evidence

Commands execute from repository root with `PATH=/workspace/scratch/toolchains/go/bin:$PATH`.

1. `go test ./apps/coordinator -run 'TestArtifactDirectory' -count=1`
   - RED coordinator0.007s: failed parent fsync left the directory visible; retry returned nil without repeating barrier; second directory create during a deliberately stalled first parent's fsync returned nil despite both configured barriers failing.
   - GREEN coordinator0.012s in combined command below. Tests inject at the actual platform directory-fsync helper, including concurrent/stalled creation. Every HTTP upload's directory prerequisite still propagates this helper's error before staging-file or SQLite creation.
2. `go test ./apps/edge-agent -run TestArtifactUnpreparedProcessOutputsUseAuthorizedCWDMapping -count=1`
   - RED agent0.024s: only workspace/other failed default mapping lookup; distinct workspace/project and workspace/other failed checked-anchor confinement.
   - GREEN agent0.133s in combined command: `go test ./apps/coordinator ./apps/edge-agent -run 'TestArtifactDirectory|TestArtifactUnpreparedProcessOutputsUseAuthorizedCWDMapping|TestArtifactNestedProcessMount' -count=1`.
   - New regression leaves workspace_context/input_artifacts absent, verifies no prepared context was accidentally installed, runs real `git --version` from nested other/sub/deep to generate output, uploads through real coordinator TLS and downloads exact matching bytes. Covers only-other and distinct-default/other configurations. Existing scratch/worktree nested mounts also remain GREEN.
3. `go test ./apps/edge-agent -run 'TestProcessShutdownDuringLogDrain|TestCompletedProcessShutdownBeforeFinish' -count=1`
   - RED agent0.094s: a verified real runner's monitor was paused after its initial root-stop check during log draining; parent shutdown wrote a user-cancel marker, then recovery became failed. Separately, completed exit record before finish became terminal interrupted failure rather than remaining recoverable.
   - GREEN agent0.204s before expanding stop-source coverage. The final deterministic test covers both Close and the same context.AfterFunc/stopRoot path used by Run; pause/release channels control the boundary (no probabilistic timing retry). Restart preserves execution_count1 and succeeds with restart-finish output. Completed result is similarly recovered from running state and succeeds.
   - Final focused command `go test ./apps/edge-agent ./tests -run 'TestProcessShutdownDuringLogDrain|TestCompletedProcessShutdownBeforeFinish|TestPhase2AgentRestartResumesProcessLogs|TestPhase2CancelKillsChildProcessTree|TestPhase2CancelledProcessDrainsLogBacklog|TestPhase2NonzeroTimeoutAndAllowlist' -count=1` — PASS agent0.283s/root9.574s. Existing live process-tree cancellation retains strict2s; bulk-integrity backlog retains separate120s budget.

### Full applicable checks after final round2 source

- `PATH=/workspace/scratch/toolchains/go/bin:$PATH bash scripts/check.sh` — PASS exit0, run once after source commit0969240. Root17.703s; protocol cached; artifact0.021s; edge-link/orchestrator cached; coordinator0.674s; agent0.892s; CLI compiled. All corresponding go vet commands passed. Full output `task-2-fix2-check.log` beside this report.

Round2 introduces only internal helpers; the two round1 public descriptor-upload methods remain unchanged. Native Windows runtime execution remains controller-owned. Cross-compiling does not establish native execution or physical power-cut durability, and round1's explicitly documented Windows API guarantee boundary remains unchanged.

- `PATH=/workspace/scratch/toolchains/go/bin:$PATH go test -race ./apps/coordinator ./apps/edge-agent ./packages/artifact ./tests -run 'Artifact|DAGArtifact|UploadOpen|UploadInvalid|ProcessShutdown|CompletedProcessShutdown|AgentRestartResumesProcessLogs|CancelKillsChildProcessTree|CancelledProcessDrainsLogBacklog|NonzeroTimeoutAndAllowlist' -count=1` — PASS exit0: coordinator2.223s/agent9.181s/artifact1.096s/root89.770s. The root filter includes the separate120s bulk-integrity test and the separately asserted strict2s live process-tree cancel; both passed. Full output `task-2-fix2-race.log`.

### Final round2 cross-build/check result

PASS exit0: nine `CGO_ENABLED=0 GOFLAGS=-buildvcs=false` production builds, linux/amd64 + linux/arm64 + windows/amd64 × coordinator + edge-agent + edge-cli, using the exact nine-build shell recorded in round1 with `task_build_root=$(mktemp -d /tmp/mb-fix2-cross-builds-XXXXXX)`. Each command was `go build -o "$task_build_root/$component-$GOOS-$GOARCH" "./apps/$component/cmd/$component"` under the exported target GOOS/GOARCH. The isolated-checkout VCS-stamp exception is the sole GOFLAGS change. Windows coordinator, agent (including new deterministic boundary tests), and root tests cross-compiled via the three `GOOS=windows GOARCH=amd64 go test -c` commands recorded above; all exit0. No native execution claimed.

`git diff --check` PASS. Product tree clean at final `0969240150196ff1304857e985525eb08a2e0af9`; reports/logs remain ignored and were not force-added. No source changes after full-suite/race/build verification.

Round2 final status: DONE_WITH_CONCERNS only for controller-owned native Windows execution and the unchanged documented durability API/physical power-loss boundary. All round2 requested source findings are addressed. Review range `c88e131..0969240`, coherent commits `8c8a56a`, `9f60d0d`, `0969240`.

## Fix round 3 — base 0969240, 2026-11-16

Read `task-2-fix2-review-report.md`; all round2 targets were accepted, with one new scoped Important Linux permissions regression. Small helper/test-only correction, commit `9ee69aa`: artifact storage owns its directory name directly beneath the already established SQLite DB directory. `durableArtifactDirWithSync` now creates/checks that child and always fsyncs its immediate parent, including existing names, failed-sync retries, and concurrent creation. It does not recursively open/read/fsync unrelated pre-existing ancestors. Ready/staging file directory fsync, all prior fixes, and public interfaces remain unchanged.

Non-root fixture uses real filesystem permissions, SQLite and HTTPS uploads. Current process identity confirmed `uid=1000(agent) gid=1000(agent)`. It creates a pre-existing shared0111 ancestor and private0700 state child; ReadDir(shared) must fail with permission denied while opening and reading the actual Coordinator SQLite DB succeeds. Empty artifact create+complete over a real TLS server must succeed without directory-read privileges on the unrelated ancestor. The test explicitly skips as root rather than reporting root's DAC bypass as coverage; this run executed as uid1000.

Exact RED/GREEN commands, from repository root with `PATH=/workspace/scratch/toolchains/go/bin:$PATH`:
- RED `go test ./apps/coordinator -run 'TestArtifactDirectory' -count=1` — exit1, coordinator0.019s. New traversal-only test failed its real HTTPS upload with INTERNAL_ERROR, while its permission-enforcement and readable/writable SQLite prerequisites passed. Existing failed-parent and stalled-concurrent-parent tests remained in the same run.
- GREEN `go test ./apps/coordinator -run 'TestArtifactDirectory|TestArtifactPublicationFailure|TestArtifactCrashAfterRename' -count=1` — exit0, coordinator0.037s. Includes the real non-root regression, failed-parent retry, concurrent second create during stalled/failed first sync, both publication-failure ledger guards, and crash-after-rename idempotence.
- GREEN `go test -race ./apps/coordinator -run 'TestArtifactDirectory|TestArtifactPublicationFailure|TestArtifactCrashAfterRename' -count=1` — exit0, coordinator1.225s.
- GREEN relevant Linux coordinator cross-builds, exit0 for both:
```bash
export PATH=/workspace/scratch/toolchains/go/bin:$PATH
for arch in amd64 arm64; do
 CGO_ENABLED=0 GOOS=linux GOARCH=$arch GOFLAGS=-buildvcs=false go build -o "/tmp/mb-fix3-coordinator-linux-$arch" ./apps/coordinator/cmd/coordinator || exit 1
 printf 'PASS linux/%s coordinator\n' "$arch"
done
```
- `git diff --check` — PASS. No broad suites repeated, per scoped round3 instruction. No lifecycle/platform/public API changes. The existing Windows native API/physical power-loss documentation is unchanged; this Linux-only correction makes no new Windows or power-cut claim.

Round3 status DONE, product HEAD `9ee69aa`; report remains ignored, not force-added. Only the two scoped Linux helper/test files committed. No product source changes after focused test/race/build verification.

## Native CI delayed-ACK fixture supplement — 2026-11-16

Controller supplied concrete native evidence remotee167dedb/run43813308105: Windows regular root tests PASS44.622s including restart/CRLF/artifacts; Windows race packages pass except `TestPhase2DelayedACKDrainsOver100Events`, which expired its generic15s fixture context at phase2_log_transport_test.go:89 after15.17s. It relays1.2MiB/~300 durable SQLite log events under race. The separate strict2s live-cancellation test passed in that CI run.

Fixture-only commit `a1f7ea2`: pass explicit bounded45s to `newPhase2` for this delayed-ACK integrity test alone. Preserve `timeout_ms:5000`, cumulative ACK>=250, held-ACK assertion, succeeded state, eventually empty local outbox, and Coordinator log count>=200. No production/process-stop/deadline tests or global fixture defaults changed.

Failure-only cleanup diagnostics now query the still-open local ledger before agent/database cleanup, using an independent bounded2s context so diagnostics work after the fixture context expires: `local_status`, generated worker sequence, persisted ACK, pending outbox count, maximum relayed ACK, plus query error. The database reader was moved before the terminal-state wait so the same diagnostics also cover failure inside e.run rather than only later assertions.

Exact verification (only focused race once, as requested):
`PATH=/workspace/scratch/toolchains/go/bin:$PATH go test -race ./tests -run '^TestPhase2DelayedACKDrainsOver100Events$' -count=1` — PASS exit0, root13.084s. No full suite repeated. The RED evidence is the actual supplied native CI timeout, not an invented/repeated local failure. `git diff --check` PASS; only `tests/phase2_log_transport_test.go` committed. Native rerun remains controller-owned.
