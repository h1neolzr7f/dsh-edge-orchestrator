# edge_link_dart

Pure Dart3 HTTPS client and chat-card projection, with no runtime package dependencies. Flutter/mobile/desktop use dart:io. FleetClient checks HTTPS, system/custom CA, Bearer authentication, request/stream deadlines and response size. Redirects are not followed. snapshot/devices/planSnapshot/events/submit/control/approvals/resolveApproval/createPairing/downloadArtifact cover the read/control surfaces. downloadArtifact returns raw authenticated bytes; consumers must verify against the ArtifactRef before decoding/displaying them.

FleetProjectionStore serializes sync and refreshPlan. Initial or CURSOR_EXPIRED recovery installs the entire Fleet baseline; incremental pages fix one high watermark, sort/deduplicate coord_seq and publish projection+cursor together after the persist callback succeeds. Single-card refresh only changes its cardBarrierSeq; a newer card may be ahead of the global cursor without losing unrelated Plan events. Empty/duplicate event pages are checked for progress. Controller-staged artifact.ready before a future Plan is accepted without inventing a card or getting stuck on404. Current device authorization is refreshed per sync, including screenshot permission revocation. Task logs hydrate from the barrier-consistent bounded TaskSnapshot.log_tail (with committed result-summary fallback for old servers), then append newer log events once. Preview refs come from actual events, never inferred execution.

```dart
final cache = FileProjectionCache('/private/fleet/projection.json');
final client = FleetClient('https://coordinator.example:8740', token: token, caBytes: pem);
final store = FleetProjectionStore(client, initial: await cache.load(), persist: cache.save);
await store.sync();
// On reconnect / polling:
await store.sync();
// Does not advance global lastCoordSeq:
await store.refreshPlan(planId);
```

FileProjectionCache atomically renames a single record containing cards and cursor, under a private single-writer directory. It does not promise directory fsync/physical power-loss durability. ProjectionState/models return copies rather than writable references to internal state. The host refreshes its own UI after store operations complete; a failed request/save leaves the previous projection intact.

Preview gating requires an authorized screen.capture/desktop.capture/android.screenshot capability AND a preview.available event containing a same-task image ArtifactRef with ID, size and SHA256. No capture adapter is implemented by this package. L1 facts/logs work without screenshots; canHandoff is false until a real handoff adapter exists.

Validation: `dart analyze && dart test`. Tests include actual Go Coordinator TLS/custom-CA/controller/pairing access, fixed pages, two unrelated Plans, ahead card barrier, duplicates/out-of-order, expired cursor, future staged artifact, failed atomic save, serialized operations, cache roundtrip and permission revocation.
