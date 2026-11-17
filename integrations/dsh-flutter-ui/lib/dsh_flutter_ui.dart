library;

import 'dart:convert';
// Keep the SDK Material entry point for upstream DSH versions before the
// material_ui extraction; Flutter3.50 still exports the same widgets here.
// ignore: deprecated_member_use
import 'package:flutter/material.dart';
import 'package:edge_link_dart/edge_link_dart.dart';

/// Mount beside existing conversation rows, keyed by model.planId.
class FleetPlanOverviewCard extends StatelessWidget {
  final PlanCardModel model;
  final VoidCallback onPause, onResume, onCancel;
  const FleetPlanOverviewCard(
      {super.key,
      required this.model,
      required this.onPause,
      required this.onResume,
      required this.onCancel});
  @override
  Widget build(BuildContext context) => Card(
          child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          mainAxisSize: MainAxisSize.min,
          children: [
            Text(model.goal, style: Theme.of(context).textTheme.titleMedium),
            const SizedBox(height: 8),
            Text('${model.planId} · ${model.status}'),
            if (model.schedulingPaused) const Text('后续调度已暂停'),
            const SizedBox(height: 8),
            const Text('暂停后续步骤，已运行进程继续'),
            const SizedBox(height: 8),
            Wrap(spacing: 8, runSpacing: 4, children: [
              TextButton(
                  onPressed: model.terminal ? null : onPause,
                  child: const Text('暂停后续步骤')),
              TextButton(
                  onPressed: model.terminal ? null : onResume,
                  child: const Text('恢复调度')),
              TextButton(
                  onPressed: model.terminal ? null : onCancel,
                  child: const Text('取消任务')),
            ]),
          ],
        ),
      ));
}

class FleetWorkerExecutionCard extends StatelessWidget {
  final WorkerStepCardModel model;

  /// Only supply after authenticated download and SHA256/size verification.
  final Widget Function(Map<String, dynamic> ref)? previewBuilder;
  const FleetWorkerExecutionCard(
      {super.key, required this.model, this.previewBuilder});
  @override
  Widget build(BuildContext context) => Card(
          child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          mainAxisSize: MainAxisSize.min,
          children: [
            Text(model.deviceId,
                style: Theme.of(context).textTheme.titleMedium),
            Text('${model.taskId} · ${model.status}'),
            if (model.pendingReason.isNotEmpty) Text(model.pendingReason),
            if (model.errorCode != null) Text('执行失败：${model.errorCode}'),
            if (model.logTail.isNotEmpty)
              Padding(
                  padding: const EdgeInsets.only(top: 8),
                  child: SelectableText(model.logTail)),
            for (final artifact in model.artifacts)
              Padding(
                  padding: const EdgeInsets.only(top: 8),
                  child: Text(
                      '产物：${artifact['name']} · ${artifact['size_bytes']} bytes')),
            if (model.canPreviewScreen && previewBuilder != null)
              Padding(
                  key: const ValueKey('fleet-screen-preview'),
                  padding: const EdgeInsets.only(top: 12),
                  child: previewBuilder!(model.previewRef!)),
          ],
        ),
      ));
}

class FleetApprovalBanner extends StatelessWidget {
  final String reason, status;
  final VoidCallback onApprove, onDeny;
  const FleetApprovalBanner(
      {super.key,
      required this.reason,
      this.status = 'pending',
      required this.onApprove,
      required this.onDeny});
  @override
  Widget build(BuildContext context) => Card(
          child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          mainAxisSize: MainAxisSize.min,
          children: [
            Text('操作审批 · $status',
                style: Theme.of(context).textTheme.titleMedium),
            const SizedBox(height: 8),
            Text(reason),
            Wrap(spacing: 8, children: [
              TextButton(
                  onPressed: status == 'pending' ? onApprove : null,
                  child: const Text('同意')),
              TextButton(
                  onPressed: status == 'pending' ? onDeny : null,
                  child: const Text('拒绝')),
            ]),
          ],
        ),
      ));
}

/// Displays a Coordinator-issued payload. Host supplies refreshed state.
class FleetPairingSheet extends StatefulWidget {
  final Map<String, dynamic>? pairing;
  final Future<void> Function() onCreatePairing;
  const FleetPairingSheet(
      {super.key, this.pairing, required this.onCreatePairing});
  @override
  State<FleetPairingSheet> createState() => _FleetPairingSheetState();
}

class _FleetPairingSheetState extends State<FleetPairingSheet> {
  bool _busy = false;
  String? _error;
  Future<void> _create() async {
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      await widget.onCreatePairing();
    } catch (error) {
      if (mounted)
        setState(() {
          _error = error.toString();
        });
    } finally {
      if (mounted)
        setState(() {
          _busy = false;
        });
    }
  }

  @override
  Widget build(BuildContext context) => Card(
          child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          mainAxisSize: MainAxisSize.min,
          children: [
            Text('添加执行设备', style: Theme.of(context).textTheme.titleMedium),
            if (widget.pairing case final pairing?) ...[
              const SizedBox(height: 8),
              SelectableText('配对码：${pairing['pairing_code']}'),
              Text('有效期至：${pairing['expires_at']}'),
              Text('协调器：${pairing['coordinator_url']}'),
              const SizedBox(height: 8),
              SelectableText(jsonEncode(pairing)),
              const Text('在目标设备输入配对码；成功兑换后该码立即失效。'),
            ],
            if (_error != null) Text(_error!),
            TextButton(
                onPressed: _busy ? null : _create,
                child: Text(_busy ? '正在生成…' : '生成新配对码')),
          ],
        ),
      ));
}
