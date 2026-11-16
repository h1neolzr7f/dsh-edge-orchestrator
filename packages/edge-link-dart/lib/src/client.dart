import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

class FleetException implements Exception {
  final String code, message;
  final int? status;
  FleetException(this.code, this.message, [this.status]);
  @override
  String toString() => '$code: $message';
}

abstract interface class FleetSource {
  Future<Map<String, dynamic>> snapshot();
  Future<List<dynamic>> devices();
  Future<Map<String, dynamic>> planSnapshot(String id);
  Future<Map<String, dynamic>> events(int after, int through,
      {int limit = 500});
}

class FleetClient implements FleetSource {
  final Uri endpoint;
  final String token;
  final Duration timeout;
  final HttpClient _http;
  FleetClient(String url,
      {required this.token,
      List<int>? caBytes,
      this.timeout = const Duration(seconds: 30)})
      : endpoint = Uri.parse(url),
        _http = HttpClient(context: _security(caBytes)) {
    if (endpoint.scheme != 'https' ||
        endpoint.host.isEmpty ||
        endpoint.userInfo.isNotEmpty ||
        endpoint.hasQuery ||
        endpoint.hasFragment) {
      _http.close(force: true);
      throw ArgumentError('HTTPS Coordinator URL required');
    }
    _http.connectionTimeout = timeout;
  }
  static SecurityContext _security(List<int>? ca) {
    final context = SecurityContext(withTrustedRoots: true);
    if (ca != null) context.setTrustedCertificatesBytes(ca);
    return context;
  }

  String get _base => endpoint.toString().replaceFirst(RegExp(r'/$'), '');
  Future<Uint8List> _send(String method, String route, [Object? body]) async {
    final request =
        await _http.openUrl(method, Uri.parse('$_base$route')).timeout(timeout);
    request.followRedirects = false;
    request.headers.set(HttpHeaders.authorizationHeader, 'Bearer $token');
    request.headers.contentType = ContentType.json;
    if (body != null) request.write(jsonEncode(body));
    try {
      final response = await request.close().timeout(timeout);
      final bytes = BytesBuilder(copy: false);
      await for (final chunk in response.timeout(timeout)) {
        bytes.add(chunk);
        if (bytes.length > 64 * 1024 * 1024)
          throw FleetException('RESPONSE_TOO_LARGE', '64 MiB limit');
      }
      final result = bytes.takeBytes();
      if (response.statusCode >= 300) {
        Map<String, dynamic> error = {};
        try {
          error = jsonDecode(utf8.decode(result)) as Map<String, dynamic>;
        } on FormatException {/* non-JSON gateway error */}
        throw FleetException(
            error['code'] as String? ?? 'HTTP_ERROR',
            error['message'] as String? ?? 'HTTP ${response.statusCode}',
            response.statusCode);
      }
      return result;
    } catch (_) {
      request.abort();
      rethrow;
    }
  }

  Future<dynamic> request(String method, String route, [Object? body]) async =>
      jsonDecode(utf8.decode(await _send(method, route, body)));
  @override
  Future<Map<String, dynamic>> snapshot() async =>
      await request('GET', '/v1/snapshot') as Map<String, dynamic>;
  @override
  Future<List<dynamic>> devices() async =>
      await request('GET', '/v1/devices') as List<dynamic>;
  @override
  Future<Map<String, dynamic>> planSnapshot(String id) async =>
      await request('GET', '/v1/plans/${Uri.encodeComponent(id)}')
          as Map<String, dynamic>;
  @override
  Future<Map<String, dynamic>> events(int after, int through,
          {int limit = 500}) async =>
      await request('GET',
              '/v1/events?after_coord_seq=$after&high_watermark=$through&limit=$limit')
          as Map<String, dynamic>;
  Future<Map<String, dynamic>> submit(Map<String, dynamic> plan) async =>
      await request('POST', '/v1/plans', plan) as Map<String, dynamic>;
  Future<Map<String, dynamic>> control(String id, String action) async {
    if (!['pause', 'resume', 'cancel'].contains(action))
      throw ArgumentError('invalid plan action');
    return await request('POST', '/v1/plans/${Uri.encodeComponent(id)}/$action')
        as Map<String, dynamic>;
  }

  Future<List<dynamic>> approvals() async =>
      await request('GET', '/v1/approvals') as List<dynamic>;
  Future<Map<String, dynamic>> resolveApproval(String id, String decision,
          {String reason = ''}) async =>
      await request('POST', '/v1/approvals/${Uri.encodeComponent(id)}/resolve',
          {'decision': decision, 'reason': reason}) as Map<String, dynamic>;
  Future<Map<String, dynamic>> createPairing({String deviceHint = ''}) async =>
      await request('POST', '/v1/pairings', {
        'device_hint': deviceHint,
        'coordinator_url': endpoint.toString()
      }) as Map<String, dynamic>;
  Future<Uint8List> downloadArtifact(String artifactId) =>
      _send('GET', '/v1/artifacts/${Uri.encodeComponent(artifactId)}');
  void close() => _http.close(force: true);
}
