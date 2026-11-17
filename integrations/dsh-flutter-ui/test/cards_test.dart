// Keep the SDK Material entry point for upstream DSH versions before the
// material_ui extraction; Flutter3.50 still exports the same widgets here.
// ignore: deprecated_member_use
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:edge_link_dart/edge_link_dart.dart';
import 'package:dsh_flutter_ui/dsh_flutter_ui.dart';

void main() {
  testWidgets(
      'narrow chat cards never overflow; controls and approvals invoke callbacks',
      (tester) async {
    tester.view.physicalSize = const Size(280, 1400);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    int pause = 0, resume = 0, cancel = 0, approve = 0, deny = 0, pair = 0;
    final plan = PlanCardModel.fromSnapshot({
      'plan': {'plan_id': 'plan-long-id', 'goal': '多台设备执行并行测试，读取生成的报告并总结'},
      'status': 'running',
      'snapshot_coord_seq': 12,
      'tasks': []
    });
    final task = WorkerStepCardModel.fromSnapshot({
      'task': {'task_id': 'long-task-id', 'target_device_id': 'worker-a'},
      'status': 'running',
      'pending_reason': '待可用执行名额',
      'artifacts': []
    }, logTail: 'a' * 300);
    await tester.pumpWidget(MaterialApp(
        home: Scaffold(
            body: SingleChildScrollView(
                child: Column(children: [
      FleetPlanOverviewCard(
          model: plan,
          onPause: () => pause++,
          onResume: () => resume++,
          onCancel: () => cancel++),
      FleetWorkerExecutionCard(model: task),
      FleetApprovalBanner(
          reason: 'Run tests in the authorized workspace',
          onApprove: () => approve++,
          onDeny: () => deny++),
      FleetPairingSheet(
          pairing: {
            'pairing_code': '0123456789abcdef',
            'coordinator_url': 'https://example.test:8740',
            'expires_at': '2026-11-16T23:00:00Z'
          },
          onCreatePairing: () async {
            pair++;
          })
    ])))));
    expect(find.text('暂停后续步骤，已运行进程继续'), findsOneWidget);
    expect(find.byKey(const ValueKey('fleet-screen-preview')), findsNothing);
    expect(tester.takeException(), isNull);
    for (final label in ['暂停后续步骤', '恢复调度', '取消任务', '同意', '拒绝', '生成新配对码']) {
      await tester.ensureVisible(find.text(label));
      await tester.tap(find.text(label));
      await tester.pump();
    }
    expect([pause, resume, cancel, approve, deny, pair], [1, 1, 1, 1, 1, 1]);
    expect(tester.takeException(), isNull);
  });
  testWidgets(
      'preview rendered only for real authorized ref and supplied renderer',
      (tester) async {
    final model = WorkerStepCardModel.fromSnapshot(
        {
          'task': {'task_id': 't', 'target_device_id': 'w'},
          'status': 'running',
          'artifacts': []
        },
        captureAuthorized: true,
        previewRef: {
          'artifact_id': 'a',
          'task_id': 't',
          'media_type': 'image/png',
          'sha256': 'b' * 64,
          'size_bytes': 100
        });
    await tester.pumpWidget(MaterialApp(
        home: FleetWorkerExecutionCard(
            model: model,
            previewBuilder: (_) => const Text('verified preview bytes'))));
    expect(find.text('verified preview bytes'), findsOneWidget);
    expect(find.byKey(const ValueKey('fleet-screen-preview')), findsOneWidget);
  });
}
