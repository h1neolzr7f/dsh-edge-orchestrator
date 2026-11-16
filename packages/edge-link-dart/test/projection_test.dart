import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:edge_link_dart/edge_link_dart.dart';
import 'package:test/test.dart';

Map<String, dynamic> plan(String id, int barrier, String status) => {
      'plan': {'plan_id': id},
      'status': status,
      'snapshot_coord_seq': barrier,
      'tasks': [
        {
          'task': {'task_id': '${id}_task', 'target_device_id': 'worker'},
          'status': status,
          'artifacts': [],
        },
      ],
    };
Map<String, dynamic> ev(
  int n,
  String id, {
  String type = 'plan.updated',
  Map<String, dynamic>? payload,
}) =>
    {
      'coord_seq': n,
      'plan_id': id,
      'task_id': '${id}_task',
      'type': type,
      'payload': payload ?? {},
    };

class Source implements FleetSource {
  @override
  Future<List<dynamic>> devices() async =>
      jsonDecode(jsonEncode(fleet['devices'])) as List<dynamic>;
  Map<String, dynamic> fleet = {
    'snapshot_coord_seq': 10,
    'plans': [plan('a', 10, 'queued'), plan('b', 10, 'queued')],
    'devices': [],
  };
  final requests = <List<int>>[];
  final cards = <String, Map<String, dynamic>>{
    'a': plan('a', 30, 'succeeded'),
    'b': plan('b', 12, 'running'),
  };
  final pages = <Map<String, dynamic>>[];
  bool expired = false;
  Future<void>? gate;
  @override
  Future<Map<String, dynamic>> snapshot() async =>
      jsonDecode(jsonEncode(fleet));
  @override
  Future<Map<String, dynamic>> planSnapshot(String id) async {
    await gate;
    if (!cards.containsKey(id))
      throw FleetException('PLAN_NOT_FOUND', 'missing', 404);
    return jsonDecode(jsonEncode(cards[id]));
  }

  @override
  Future<Map<String, dynamic>> events(
    int after,
    int through, {
    int limit = 500,
  }) async {
    requests.add([after, through]);
    if (expired) {
      expired = false;
      throw FleetException('CURSOR_EXPIRED', 'gone', 409);
    }
    return pages.removeAt(0);
  }
}

void main() {
  test(
      'single-card barrier preserves unrelated events; fixed pages sorted/deduplicated and reload',
      () async {
    final api = Source();
    final commits = <ProjectionState>[];
    final store = FleetProjectionStore(
      api,
      persist: (s) async {
        commits.add(ProjectionState.fromJson(s.toJson()));
      },
    );
    await store.sync();
    expect(store.lastCoordSeq, 10);
    await store.refreshPlan('a');
    expect(store.lastCoordSeq, 10);
    expect(store.plans['a']!.cardBarrierSeq, 30);
    api.pages.addAll([
      {
        'events': [ev(12, 'b'), ev(11, 'a'), ev(12, 'b')],
        'high_watermark': 20,
        'next_coord_seq': 12,
        'has_more': true,
      },
      {
        'events': [ev(20, 'b')],
        'high_watermark': 20,
        'next_coord_seq': 20,
        'has_more': false,
      },
    ]);
    api.cards['b'] = plan('b', 20, 'succeeded');
    await store.sync();
    expect(api.requests, [
      [10, 0],
      [12, 20],
    ]);
    expect(store.lastCoordSeq, 20);
    expect(store.plans['b']!.status, 'succeeded');
    expect(store.plans['a']!.cardBarrierSeq, 30);
    final reloaded = FleetProjectionStore(api, initial: commits.last);
    expect(reloaded.lastCoordSeq, 20);
    expect(reloaded.plans['b']!.status, 'succeeded');
  });
  test(
      'expired cursor resets baseline; staged artifact before future Plan creates no fake card',
      () async {
    final api = Source();
    final store = FleetProjectionStore(api);
    await store.sync();
    api.pages.add({
      'events': [ev(11, 'future', type: 'artifact.ready')],
      'high_watermark': 11,
      'next_coord_seq': 11,
      'has_more': false,
    });
    await store.sync();
    expect(store.lastCoordSeq, 11);
    expect(store.plans.containsKey('future'), false);
    api.expired = true;
    api.fleet = {
      'snapshot_coord_seq': 40,
      'plans': [plan('new', 40, 'running')],
      'devices': [],
    };
    await store.sync();
    expect(store.lastCoordSeq, 40);
    expect(store.plans.keys, ['new']);
  });
  test(
      'failed atomic save leaves cursor and projection unchanged; refresh and sync serialize',
      () async {
    final api = Source();
    bool fail = false;
    final store = FleetProjectionStore(
      api,
      persist: (s) async {
        if (fail) throw FileSystemException('disk full');
      },
    );
    await store.sync();
    fail = true;
    api.pages.add({
      'events': [ev(11, 'b')],
      'high_watermark': 11,
      'next_coord_seq': 11,
      'has_more': false,
    });
    await expectLater(store.sync(), throwsA(isA<FileSystemException>()));
    expect(store.lastCoordSeq, 10);
    expect(store.plans['b']!.status, 'queued');
    fail = false;
    final gate = Completer<void>();
    api.gate = gate.future;
    final refresh = store.refreshPlan('a');
    api.pages.add({
      'events': [ev(12, 'b')],
      'high_watermark': 12,
      'next_coord_seq': 12,
      'has_more': false,
    });
    final sync = store.sync();
    await Future<void>.delayed(Duration.zero);
    expect(store.lastCoordSeq, 10);
    gate.complete();
    await Future.wait([refresh, sync]);
    expect(store.lastCoordSeq, 12);
    expect(store.plans['a']!.cardBarrierSeq, 30);
  });
  test(
      'screen preview needs authorized capture AND actual preview ref; L3 never fabricated',
      () async {
    final api = Source();
    api.fleet['devices'] = [
      {
        'device_id': 'worker',
        'capabilities': [
          {'capability_id': 'screen.capture', 'authorized': true},
        ],
      },
    ];
    final store = FleetProjectionStore(api);
    await store.sync();
    expect(store.worker('a', 'a_task').canPreviewScreen, false);
    api.pages.add({
      'events': [
        ev(
          11,
          'a',
          type: 'preview.available',
          payload: {
            'artifact_ref': {
              'artifact_id': 'shot',
              'task_id': 'a_task',
              'media_type': 'image/png',
              'sha256': 'a' * 64,
              'size_bytes': 123,
            },
          },
        ),
      ],
      'high_watermark': 11,
      'next_coord_seq': 11,
      'has_more': false,
    });
    await store.sync();
    expect(store.worker('a', 'a_task').canPreviewScreen, true);
    expect(store.worker('a', 'a_task').canHandoff, false);
    api.fleet['devices'][0]['capabilities'][0]['authorized'] = false;
    api.pages.add({
      'events': [],
      'high_watermark': 12,
      'next_coord_seq': 12,
      'has_more': false
    });
    await store.sync();
    expect(store.worker('a', 'a_task').canPreviewScreen, false);
    final noPermission = WorkerStepCardModel.fromSnapshot(
      plan('x', 1, 'running')['tasks'][0],
      captureAuthorized: false,
      previewRef: {'artifact_id': 'shot', 'media_type': 'image/png'},
    );
    expect(noPermission.canPreviewScreen, false);
  });
  test('file cache saves projection and cursor in one record', () async {
    final dir = await Directory.systemTemp.createTemp('fleet-projection');
    addTearDown(() => dir.delete(recursive: true));
    final cache = FileProjectionCache('${dir.path}/projection.json');
    final s = FleetProjectionStore(Source(), persist: cache.save);
    await s.sync();
    expect((await cache.load())!.lastCoordSeq, 10);
    expect((await cache.load())!.plans.keys, containsAll(['a', 'b']));
  });
}
