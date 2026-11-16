import 'dart:io';
import 'package:edge_link_dart/edge_link_dart.dart';
import 'package:test/test.dart';

void main() {
  test('real Go HTTPS client verifies CA, controller auth and pairing routes',
      () async {
    final root = Directory.current.parent.parent.path;
    final temp = await Directory.systemTemp.createTemp('fleet-dart-tls');
    Process? server;
    addTearDown(() async {
      server?.kill();
      if (server != null) await server.exitCode;
      await temp.delete(recursive: true);
    });
    Future<void> run(List<String> args) async {
      final r = await Process.run('go', args, workingDirectory: root);
      if (r.exitCode != 0) fail('${r.stderr}');
    }

    await run(['run', './tests/fixtures/tls-cert', temp.path]);
    final bin = '${temp.path}/coordinator${Platform.isWindows ? '.exe' : ''}';
    await run([
      'build',
      '-buildvcs=false',
      '-o',
      bin,
      './apps/coordinator/cmd/coordinator'
    ]);
    final socket = await ServerSocket.bind(InternetAddress.loopbackIPv4, 0);
    final port = socket.port;
    await socket.close();
    final url = 'https://127.0.0.1:$port';
    server = await Process.start(bin, [
      '--listen',
      '127.0.0.1:$port',
      '--db',
      '${temp.path}/c.db',
      '--tls-cert',
      '${temp.path}/cert.pem',
      '--tls-key',
      '${temp.path}/key.pem'
    ], environment: {
      'DSH_CONTROLLER_TOKEN': 'controller',
      'DSH_WORKER_TOKENS': ''
    });
    server.stdout.drain<void>();
    server.stderr.drain<void>();
    final client = FleetClient(url,
        token: 'controller',
        caBytes: await File('${temp.path}/cert.pem').readAsBytes());
    addTearDown(client.close);
    Map<String, dynamic>? snapshot;
    for (int i = 0; i < 100; i++) {
      try {
        snapshot = await client.snapshot();
        break;
      } on SocketException {
        await Future<void>.delayed(const Duration(milliseconds: 50));
      }
    }
    expect(snapshot!['snapshot_coord_seq'], 0);
    expect(await client.devices(), isEmpty);
    expect(await client.approvals(), isEmpty);
    final pairing = await client.createPairing(deviceHint: 'dart-worker');
    expect(pairing['pairing_code'], hasLength(16));
    expect(pairing['coordinator_url'], url);
    final untrusted = FleetClient(url, token: 'controller');
    addTearDown(untrusted.close);
    await expectLater(untrusted.snapshot(), throwsA(isA<HandshakeException>()));
    final bad = FleetClient(url,
        token: 'bad',
        caBytes: await File('${temp.path}/cert.pem').readAsBytes());
    addTearDown(bad.close);
    await expectLater(bad.snapshot(),
        throwsA(isA<FleetException>().having((e) => e.status, 'status', 401)));
    expect(
        () => FleetClient('http://localhost', token: 'x'), throwsArgumentError);
  }, timeout: const Timeout(Duration(seconds: 45)));
}
