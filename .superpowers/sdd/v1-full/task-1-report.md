# Task 1 — M-A runtime boundary repairs

Base: `3aa427577500f5c4253a533f5d4df16e623e6b21`. Branch: `feat/v1-full-closure`.

## Delivered scope and interfaces

All four M-A repairs are implemented; no later milestone functionality is installed.

- **P2-B1:** `apps/edge-agent/workspace_cleanup.go` adds the private reusable method `func (a *Agent) cleanupTaskWorkspace(t protocol.TaskEnvelope) error`. `launch` invokes it only after `finish` has durably committed terminal state. Persistent-process success/cancellation already return through the final log-drain path; the cleanup helper additionally refuses cleanup for a still-live runner/tree, missing process exit record, or undrained logs. It uses an independent bounded context because the task context is commonly cancelled at this point. It executes `git worktree remove --force`, removes only the task's `workspace` directory, and executes `git worktree prune`. Logs, exit/ready/spec records, result summaries, and the local ledger remain intact. Repeated removal is safe; a missing worktree registration is handled by disk removal and pruning.
- **P2-B2:** `AppendEvent(device string, ev protocol.TaskEvent) (int64, error)` keeps its existing exported signature. Business-invalid buffered events roll back their event savepoint, compact their inbox payload while retaining the original conflict hash, and advance the worker watermark past the quarantined sequence. Nonterminal offending tasks become `failed` with error code `PROTOCOL_VIOLATION`; `refreshPlan` updates the plan. Infrastructure failures do not discard pending events. A duplicate of an unapplied buffered event retries draining, so a buffered valid successor can proceed after the initial quarantine disconnect. Historical applied/quarantined duplicates remain idempotent and conflicting hashes remain rejected. The first quarantine may return `INVALID_EVENT`/one `link.error`; subsequent reconnects do not reencounter that historical poison.
- **P2-B3:** Windows `terminateOwned` reports an `openJob` failure if ownership/liveness still exists; after successful job termination it caps the runner grace at 500 ms and opens/terminates the surviving independent runner. The termination handle is checked against the stored creation time, with a final full identity check, to protect against PID reuse. Real Windows process tests are added for a missing live job and a runner outside the payload job.
- **P2-B4:** `schedule` loads global active-task payloads once before the per-plan loop, builds shared `deviceLoad`/`capLoad` maps, and retains the existing in-place increments after dispatch. A real SQLite view/scalar-function counter regression measures actual payload reads across four plans: eight total reads (four global and four plan snapshot reads), versus the old twenty.

No HTTP, Node, Dart, schema, or exported Go API additions. The new diagnostic error code is carried through the existing `protocol.Error` contract.

**Task 2 ordering requirement:** output collection/upload must finish before calling `cleanupTaskWorkspace`; keep the helper reusable at the final launch boundary. Do not remove runner logs/state after upload. There is no artifact upload implementation in this milestone.

## RED evidence

All Go commands used `PATH=/workspace/scratch/toolchains/go/bin:$PATH`; the unrelated `/usr/bin/go` was not used for verification.

1. `go test ./tests -run '^TestPhase2WorktreeCleanedUpAfterCompletion$' -count=1` before the fix: exit 1.

   ```text
   --- FAIL: TestPhase2WorktreeCleanedUpAfterCompletion (2.49s)
       e2e_phase2_exec_test.go:549: task worktree remains after completion: .../runs/96bb90d6c7142f439591074afe8baa65dea3e0dabcebe7e8eb4593f442e2e8aa/workspace (<nil>)
   FAIL .../tests 2.491s
   ```

2. `go test ./apps/coordinator -run '^TestPoisonBufferedEventDoesNotWedgeSubsequentWorkerReconnect$' -count=1` before the fix: exit 1.

   ```text
   --- FAIL: TestPoisonBufferedEventDoesNotWedgeSubsequentWorkerReconnect (0.00s)
       round2_test.go:93: reconnect wedged by historical poison: ack=1 err=INVALID_EVENT: non-contiguous task_seq
   FAIL .../apps/coordinator 0.006s
   ```

3. `go test ./apps/coordinator -run '^TestScheduleLoadsActiveTasksOnceAcrossPlans$' -count=1` with the baseline `scheduler.go` temporarily restored, then the repaired file restored: exit 1 against baseline.

   ```text
   --- FAIL: TestScheduleLoadsActiveTasksOnceAcrossPlans (0.00s)
       scheduler_load_test.go:50: active payload reads=20 want=8 (one global scan plus each plan)
   FAIL .../apps/coordinator 0.008s
   ```

Windows behavioral RED/GREEN execution is not available on this Linux host. Its tests are real native-process tests, cross-compiled only; no native Windows success is claimed.

## GREEN and full verification

1. `go test ./apps/coordinator ./tests -run 'Test(PoisonBufferedEventDoesNotWedgeSubsequentWorkerReconnect|OutOfOrderPoisonEventDoesNotRollbackPriorValidEvent|Phase2WorktreeCleanedUpAfterCompletion)$' -count=1`: exit 0.

   ```text
   ok .../apps/coordinator 0.011s
   ok .../tests 0.490s
   ```

2. `go test ./apps/coordinator -run '^TestScheduleLoadsActiveTasksOnceAcrossPlans$' -count=1`: exit 0, `ok .../apps/coordinator 0.011s`.

3. Full applicable suite: `bash scripts/check.sh`: exit 0. This ran tests and vet in the root plus all six Go modules.

   ```text
   ok .../tests 11.038s
   ok .../packages/protocol (cached)
   ?  .../packages/protocol/cmd/schema [no test files]
   ok .../packages/edge-link (cached)
   ok .../packages/orchestrator (cached)
   ok .../apps/coordinator 0.027s
   ?  .../apps/coordinator/cmd/coordinator [no test files]
   ok .../apps/edge-agent 0.200s
   ?  .../apps/edge-agent/cmd/edge-agent [no test files]
   ?  .../apps/edge-cli/cmd/edge-cli [no test files]
   ```

   The full suite preceded the final additional SQLite counter test and Windows-only tests. A subsequent fresh `go test ./apps/coordinator ./apps/edge-agent -count=1` passed: coordinator 0.034s; edge-agent 0.190s.

4. `GOFLAGS=-buildvcs=false bash scripts/build.sh`: exit 0; all nine production binaries built with `CGO_ENABLED=0` for linux/amd64, linux/arm64, windows/amd64. Repeated after the temporary baseline-scheduler RED probe so the delivered build uses the repaired source.

   Environment-only workaround: the unmodified `scripts/build.sh` initially exited 1 with `error obtaining VCS status: exit status 128`. `go build -x` revealed Go invoking `cd /workspace; git status --porcelain` in this isolated checkout layout. `GOFLAGS=-buildvcs=false` disables stamping, not compilation. The controller approved this verification workaround; source/build scripts are unchanged.

5. `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -c -o /tmp/dsh-v1-agent-windows.test.exe ./apps/edge-agent`: exit 0. Compile-only native Windows regressions; not executed here.

6. `git diff --check`: exit 0.

## Existing regression ruling

`TestOutOfOrderPoisonEventDoesNotRollbackPriorValidEvent` previously required watermark/ACK 1 and task `running` after poisoned seq 2. That assertion conflicts with the required quarantine advancement and would leave the offending task running indefinitely. The controller explicitly ruled to change watermark/ACK to 2, require `failed`/`PROTOCOL_VIOLATION`, and separately prove the legitimate seq-1 `task.started` event remains durably committed. Its first-call error assertion remains. All existing test functions are retained.

## Changed files

- `apps/edge-agent/workspace_cleanup.go`: bounded reusable disposable-checkout cleanup.
- `apps/edge-agent/agent.go`: post-durable-finish cleanup call.
- `apps/edge-agent/proc_windows.go`: missing-job errors and independently owned runner fallback.
- `apps/edge-agent/proc_windows_test.go`: real native Windows lifecycle regressions (compile-only here).
- `apps/coordinator/store.go`: quarantine, durable watermark, failed task/plan refresh, pending-duplicate retry.
- `apps/coordinator/round2_test.go`: named reconnect regression, hash retention, adjusted valid-prefix regression.
- `apps/coordinator/scheduler.go`: one shared active-load scan per scheduling pass.
- `apps/coordinator/scheduler_load_test.go`: measured real SQLite payload-read regression.
- `tests/e2e_phase2_exec_test.go`: real TLS/child-process worktree cleanup regression, metadata removal and diagnostic preservation.
- This report.

## Self-review and remaining limits

- Checked cleanup placement against durable finish, persistent process/log-drain paths, agent detachment, and cancellation. A detached persistent process returns before cleanup. A live tree or undrained process logs preserve its workspace and report an error rather than deleting an active checkout.
- Reviewed inbox savepoint rollback ordering: only business violations compact/advance; hashes remain; earlier events stay committed; a valid successor in the inbox can retry consumption.
- Reviewed scheduler shared capacity counters: incremental dispatch updates remain within the same scheduling pass; no query is nested inside the per-plan loop.
- Reviewed Windows ownership checks around handle acquisition and bounded grace; both production cross-build and test cross-compilation passed. Native Windows execution remains required in the normal Windows environment.
- No outputs/logs/state directories were swept, no artifact behavior was added, and no runtime core dependency was introduced.
- No subagents or external reviewer were used for this task. The only verification constraints are native Windows execution and isolated-checkout VCS stamping described above.

## Review fix round 1 — locked task worktrees

Baseline: `0e3734bd0e4a4256108bd732598e9d2828a15736`.

The review found that a task-created worktree lock makes `worktree remove --force` fail while disk removal and `worktree prune` succeed. Prune preserves locked metadata, so the original helper incorrectly reported success. This finding is confirmed and fixed.

`cleanupTaskWorkspace` now deliberately removes its agent-owned disposable worktree using Git's double-force option, which is required for locked worktrees. After disk removal/pruning it verifies absence of the exact checkout registration using NUL-delimited porcelain output, handling whitespace in paths. Removal errors are ignored only when Git has positively confirmed registration absence. Listing failures, a remaining registration, disk/prune errors, or a capped/truncated-size listing return an error. The run directory and diagnostics remain unchanged; idempotent repeated cleanup is retained.

Added focused real-Git test `TestCleanupTaskWorkspaceRemovesOwnedLockedRegistration` in `apps/edge-agent/workspace_cleanup_test.go`. It creates a committed repository, prepares the actual task checkout, locks it with `git worktree lock --reason ...`, calls production cleanup, verifies checkout and metadata absence plus diagnostic preservation, and repeats cleanup to verify idempotency.

RED before the repair:

```text
PATH=/workspace/scratch/toolchains/go/bin:$PATH go test ./apps/edge-agent -run '^TestCleanupTaskWorkspaceRemovesOwnedLockedRegistration$' -count=1
--- FAIL: TestCleanupTaskWorkspaceRemovesOwnedLockedRegistration (0.03s)
    workspace_cleanup_test.go:50: locked registration survived cleanup: ...
        worktree .../runs/28763da7684e2e28693b7d37876c18d7d6f2054cbf0556ff1a751ba9a014142c/workspace
        detached
        locked task-created lock
FAIL .../apps/edge-agent 0.037s
```

GREEN after repair:

```text
PATH=/workspace/scratch/toolchains/go/bin:$PATH go test ./apps/edge-agent -run '^TestCleanupTaskWorkspaceRemovesOwnedLockedRegistration$' -count=1
ok .../apps/edge-agent 0.042s
PATH=/workspace/scratch/toolchains/go/bin:$PATH go test ./apps/edge-agent ./tests -run 'Test(CleanupTaskWorkspaceRemovesOwnedLockedRegistration|Phase2WorktreeCleanedUpAfterCompletion)$' -count=1
ok .../apps/edge-agent 0.066s
ok .../tests 0.501s
```

Additional covering verification: `go vet ./apps/edge-agent` and `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -c -o /tmp/dsh-v1-agent-windows.test.exe ./apps/edge-agent` both exited 0. `git diff --check` passed. The full suite was not repeated for this focused checkout-cleanup fix. Native Windows execution remains delegated to the existing CI checkpoint.

Self-review: force is restricted to the derived per-task checkout path, never the authorized source root; NUL framing avoids quoted/space-containing path ambiguity; exact `filepath.Rel == "."` matching avoids prefix collisions; the capped listing fails closed instead of falsely proving absence. No later-milestone scope changed.
