import 'dart:convert';
import 'dart:io';
import 'client.dart';
import 'models.dart';

class FleetProjectionStore {
  final FleetSource source;
  final Future<void> Function(ProjectionState)? persist;
  ProjectionState _state;
  Future<void> _pending = Future.value();
  FleetProjectionStore(this.source, {this.persist, ProjectionState? initial})
      : _state = ProjectionState.fromJson(
            (initial ?? ProjectionState.empty()).toJson());
  int? get lastCoordSeq => _state.lastCoordSeq;
  Map<String, PlanCardModel> get plans => _state.plans;
  ProjectionState get state => ProjectionState.fromJson(_state.toJson());
  Future<void> _serialize(Future<void> Function() action) {
    final result = _pending.then((_) => action());
    _pending = result.then<void>((_) {}, onError: (Object _, StackTrace __) {});
    return result;
  }

  Future<void> _commit(Map<String, dynamic> json) async {
    final candidate = ProjectionState.fromJson(json);
    await persist?.call(ProjectionState.fromJson(json));
    _state = candidate;
  }

  void _merge(Map<String, dynamic> json, Map<String, dynamic> incoming) {
    final id = incoming['plan']['plan_id'];
    final old = json['plans'][id];
    if (old == null ||
        incoming['snapshot_coord_seq'] >= old['snapshot_coord_seq'])
      json['plans'][id] = incoming;
  }

  Future<void> _snapshot() async {
    final incoming = await source.snapshot();
    final json = ProjectionState.empty().toJson();
    json['last_coord_seq'] = incoming['snapshot_coord_seq'];
    json['devices'] = incoming['devices'];
    for (final plan in incoming['plans'] as List) {
      _merge(json, plan);
    }
    await _commit(json);
  }

  Future<void> refreshPlan(String id) => _serialize(() async {
        final card = await source.planSnapshot(id);
        final json = _state.toJson();
        _merge(json, card);
        await _commit(
            json); // Card barrier only; the global cursor is unchanged.
      });
  Future<void> sync() => _serialize(() async {
        if (lastCoordSeq == null) {
          await _snapshot();
          return;
        }
        try {
          final json = _state.toJson();
          json['devices'] = await source
              .devices(); // Permission revocation affects previews immediately.
          int cursor = lastCoordSeq!, high = 0;
          bool more;
          do {
            final page = await source.events(cursor, high);
            final pageHigh = (page['high_watermark'] as num).toInt();
            if (pageHigh < cursor || (high != 0 && pageHigh != high))
              throw StateError('invalid fixed high watermark');
            high = pageHigh;
            final events = (page['events'] as List)
                .cast<Map<String, dynamic>>()
                .toList()
              ..sort((a, b) =>
                  (a['coord_seq'] as num).compareTo(b['coord_seq'] as num));
            int applied = cursor;
            for (final event in events) {
              final seq = (event['coord_seq'] as num).toInt();
              if (seq <= applied) continue;
              if (seq > high) throw StateError('event beyond high watermark');
              final planId = event['plan_id'] as String? ?? '';
              final key = '$planId/${event['task_id'] ?? ''}';
              final payload = event['payload'] as Map<String, dynamic>? ?? {};
              switch (event['type']) {
                case 'task.log':
                  final log =
                      '${json['logs'][key] ?? ''}${payload['text'] ?? ''}';
                  json['logs'][key] = log.length <= 16384
                      ? log
                      : log.substring(log.length - 16384);
                case 'preview.available':
                  if (payload['artifact_ref'] is Map)
                    json['previews'][key] = payload['artifact_ref'];
                case 'approval.requested':
                case 'approval.resolved':
                  if (payload['id'] != null)
                    json['approvals'][payload['id']] = payload;
              }
              final card = json['plans'][planId];
              if (planId.isNotEmpty &&
                  (card == null || seq > card['snapshot_coord_seq'])) {
                try {
                  _merge(json, await source.planSnapshot(planId));
                } on FleetException catch (error) {
                  // A controller may stage a patch before submitting its future Plan.
                  if (event['type'] != 'artifact.ready' ||
                      (error.code != 'PLAN_NOT_FOUND' && error.status != 404))
                    rethrow;
                }
              }
              applied = seq;
            }
            final next = (page['next_coord_seq'] as num).toInt();
            more = page['has_more'] == true;
            if (next < applied ||
                next > high ||
                (more && next <= cursor) ||
                (!more && next != high))
              throw StateError('invalid event page cursor');
            cursor = next;
          } while (more);
          json['last_coord_seq'] = cursor;
          await _commit(json);
        } on FleetException catch (error) {
          if (error.code != 'CURSOR_EXPIRED') rethrow;
          await _snapshot();
        }
      });
  WorkerStepCardModel worker(String planId, String taskId) {
    final task =
        plans[planId]!.tasks.firstWhere((v) => v['task']['task_id'] == taskId);
    final json = _state.toJson();
    bool authorized = false;
    for (final device in json['devices'] as List) {
      if (device['device_id'] == task['task']['target_device_id']) {
        authorized = (device['capabilities'] as List? ?? []).any((c) =>
            c['authorized'] == true &&
            ['screen.capture', 'desktop.capture', 'android.screenshot']
                .contains(c['capability_id']));
      }
    }
    final key = '$planId/$taskId';
    return WorkerStepCardModel.fromSnapshot(task,
        captureAuthorized: authorized,
        previewRef: json['previews'][key] as Map<String, dynamic>?,
        logTail: json['logs'][key] as String? ?? '');
  }

  List<Map<String, dynamic>> get approvals =>
      (_state.toJson()['approvals'] as Map<String, dynamic>)
          .values
          .cast<Map<String, dynamic>>()
          .toList();
}

/// Single-writer private cache. Atomic replacement includes cards and cursor;
/// it does not promise directory fsync or physical power-loss durability.
class FileProjectionCache {
  final String path;
  FileProjectionCache(this.path);
  Future<ProjectionState?> load() async {
    final file = File(path);
    if (!await file.exists()) return null;
    return ProjectionState.fromJson(
        jsonDecode(await file.readAsString()) as Map<String, dynamic>);
  }

  Future<void> save(ProjectionState state) async {
    final file = File('$path.tmp');
    await file.parent.create(recursive: true);
    await file.writeAsString(jsonEncode(state.toJson()), flush: true);
    await file.rename(path);
  }
}
