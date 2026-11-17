# M-C: Approval and pairing validation

Implemented durable approval requests/resolution, controller-only HTTPS/CLI operations and one-time cryptographic pairing. Waiting approvals consume no execution slot. Same decisions are idempotent; conflicting decisions, expired/cancelled approvals cannot requeue work. Denial rejects downstream dependencies.

Pairing-only Coordinator startup works without static environment configuration. Atomic claims, device hints, expiry and hash-only server storage are tested. Worker SQLite persists credentials bound to device and endpoint; reload needs no token environment variable. Static tokens remain supported. Pairing redirects are rejected. Dynamic tokens authorize both WebSocket and artifact paths with existing assignment restrictions.

Validation (2026-11-16, Go1.24.2 Linux):
- `bash scripts/check.sh`: all eight modules test/vet PASS, root integration15.209s.
- `go test -race ./apps/coordinator/... ./apps/edge-agent/... ./tests/... -run 'Approval|Pair|Identity|Phase6' -count=1`: PASS (2.069s/1.135s/3.864s).
- Real TLS Coordinator + paired Worker, credential reload, approval blocking with parallel independent work, actual child process, output upload and downstream artifact mount/read: PASS.
- Ten concurrent claims yield exactly one success; hint mismatch, expired code, controller-route auth, hash-only storage and authentication after Coordinator restart: PASS.
- Approval lifecycle approve/deny/cancel/expire and downstream rejection: PASS.

Historical initial core RED was recorded by the interrupted implementer (missing authority APIs); recovered implementation was verified rather than rewritten. A supplemental denial test first exposed its incorrect assumption of task order; it now looks up task IDs, matching the documented sorted snapshot contract.

Public Go APIs: protocol Approval/ApprovalDecision/PairingRequest/Pairing/PairingClaim/PairingCredential; edge-link Approvals, ResolveApproval, CreatePairing, ClaimPairing. Agent Config.PairingCode; CLI approvals/approve/deny/pair. Schema definitions regenerated.

POSIX identity files and WAL/SHM use0600; Windows deployment must restrict its dedicated user data directory ACL because chmod does not implement POSIX user isolation there. V1 has no credential revocation/rotation API. These are explicit deployment limits, not claims of OS secret-store encryption.
