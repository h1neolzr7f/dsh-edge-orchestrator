# DSH Fleet chat widgets

Embeddable Flutter widgets, not a replacement chat app. Add this package and edge_link_dart via local path dependencies to the existing DSH app. At pinned upstream `ccch1mneyyy/dsh-go` f5fa9256db4c82d601078b8064056b93dc3560ec, chat rendering lives at `app/lib/ui/chat/chat_list.dart` (`_buildRow`) and `chat_rows.dart`. Host-native Cordis tool cards also use the existing presentationMeta dsh-go/view path. These components provide an app-side Fleet row builder to insert alongside existing rows, keyed by the conversation's persisted plan_id. Keep the existing host model/voice/Phone Control/provider services intact.

```dart
FleetPlanOverviewCard(
  model: store.plans[planId]!,
  onPause: () async { await client.control(planId, 'pause'); await store.refreshPlan(planId); },
  onResume: () async { await client.control(planId, 'resume'); await store.refreshPlan(planId); },
  onCancel: () async { await client.control(planId, 'cancel'); await store.refreshPlan(planId); },
);
FleetWorkerExecutionCard(model: store.worker(planId, taskId));
```

Refresh the host widget state after awaited store operations, show request errors in the host's existing error surface, and disable duplicate operator submissions while requests run. On entry/reconnect, fetch client.approvals() as well as the Fleet snapshot so pending decisions retain their approval IDs; bind FleetApprovalBanner to resolveApproval(id,'approved'/'denied'), then sync/refresh the affected Plan. FleetPairingSheet displays the real createPairing response and its QR-compatible JSON payload, expiry and code; onCreatePairing obtains a fresh response and updates the host state. No code/token is fabricated locally.

The plan pause explanation is explicit: 暂停后续步骤，已运行进程继续. Worker cards display recorded state, pending reason, log tail and real artifact metadata. A screen subtree appears only when WorkerStepCardModel.canPreviewScreen is true AND the host supplies a previewBuilder for the authenticated, hash-verified image bytes. With no authorized capture/preview event, there is no screen placeholder. No handoff control is claimed or rendered.

These packages are tested using the real Flutter3.50/Dart3.14 SDK (CI=true) and narrow280px widget tests. They have not been wired into/built as an upstream Android APK; host session routing, image decoding and actual screen/hardware adapters remain host integration work. No upstream brand, pet or protected assets are copied.
