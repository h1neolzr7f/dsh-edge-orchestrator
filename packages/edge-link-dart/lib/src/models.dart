import 'dart:convert';

Map<String, dynamic> cloneJson(Map<String, dynamic> value) =>
    jsonDecode(jsonEncode(value)) as Map<String, dynamic>;

class PlanCardModel {
  final Map<String, dynamic> _value;
  PlanCardModel.fromSnapshot(Map<String, dynamic> value)
      : _value = cloneJson(value);
  String get planId => _value['plan']['plan_id'] as String;
  String get goal => _value['plan']['goal'] as String? ?? planId;
  String get status => _value['status'] as String;
  int get cardBarrierSeq => (_value['snapshot_coord_seq'] as num).toInt();
  bool get schedulingPaused => _value['scheduling_paused'] == true;
  bool get terminal =>
      const ['succeeded', 'failed', 'cancelled'].contains(status);
  List<Map<String, dynamic>> get tasks =>
      (toJson()['tasks'] as List).cast<Map<String, dynamic>>();
  Map<String, dynamic> toJson() => cloneJson(_value);
}

class WorkerStepCardModel {
  final Map<String, dynamic> _task;
  final Map<String, dynamic>? _preview;
  final bool captureAuthorized;
  final String logTail;
  WorkerStepCardModel.fromSnapshot(Map<String, dynamic> task,
      {this.captureAuthorized = false,
      Map<String, dynamic>? previewRef,
      this.logTail = ''})
      : _task = cloneJson(task),
        _preview = previewRef == null ? null : cloneJson(previewRef);
  String get taskId => _task['task']['task_id'] as String;
  String get deviceId => _task['task']['target_device_id'] as String? ?? '';
  String get status => _task['status'] as String;
  String get pendingReason => _task['pending_reason'] as String? ?? '';
  String? get errorCode => _task['error']?['code'] as String?;
  List<Map<String, dynamic>> get artifacts =>
      ((_task['artifacts'] as List?) ?? []).map((v) => cloneJson(v)).toList();
  bool get canHandoff => false;
  bool get canPreviewScreen {
    final ref = _preview;
    return captureAuthorized &&
        ref != null &&
        ref['task_id'] == taskId &&
        ref['artifact_id'] is String &&
        (ref['artifact_id'] as String).isNotEmpty &&
        ref['media_type'] is String &&
        (ref['media_type'] as String).startsWith('image/') &&
        ref['sha256'] is String &&
        RegExp(r'^[a-f0-9]{64}$').hasMatch(ref['sha256']) &&
        ref['size_bytes'] is num &&
        ref['size_bytes'] > 0;
  }

  Map<String, dynamic>? get previewRef =>
      canPreviewScreen ? cloneJson(_preview!) : null;
}

class ProjectionState {
  final Map<String, dynamic> _value;
  ProjectionState.fromJson(Map<String, dynamic> value)
      : _value = cloneJson(value);
  factory ProjectionState.empty() => ProjectionState.fromJson({
        'last_coord_seq': null,
        'plans': <String, dynamic>{},
        'devices': [],
        'logs': <String, dynamic>{},
        'previews': <String, dynamic>{},
        'approvals': <String, dynamic>{},
      });
  int? get lastCoordSeq => (_value['last_coord_seq'] as num?)?.toInt();
  Map<String, PlanCardModel> get plans =>
      Map.unmodifiable((_value['plans'] as Map<String, dynamic>)
          .map((k, v) => MapEntry(k, PlanCardModel.fromSnapshot(v))));
  Map<String, dynamic> toJson() => cloneJson(_value);
}
