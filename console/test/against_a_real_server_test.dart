import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:summareader_sync_console/src/server.dart';

/// The one test that runs a server.
///
/// Everything else here is pure: strings in, strings out. This is the part
/// that cannot be checked that way — the console reads its numbers out of the
/// server's own HTTP endpoints, authorised by a token it minted and passed in
/// through the environment, and that only works while four things agree: the
/// variable's name, the endpoints' paths, the bearer scheme and the metric
/// names. Nothing else would notice if one of them changed. The pane would go
/// on showing zero devices for ever, which reads exactly like a server nobody
/// has paired with.
///
/// Runs only when SUMMAREADER_SYNC_BIN names a server binary, and is skipped
/// otherwise:
///
///     go build -o /tmp/summareader-sync .
///     SUMMAREADER_SYNC_BIN=/tmp/summareader-sync flutter test
///
/// Named rather than searched for, because the binaries lying around a
/// checkout — the one run.sh builds, the ones in dist/ — are whatever age
/// they happen to be, and a suite that goes red because somebody has a
/// three-week-old build in their working tree teaches everyone to ignore it.
void main() {
  test('the console reads the counts out of a server it started', () async {
    final exe = _serverBinary();
    if (exe == null) {
      markTestSkipped('SUMMAREADER_SYNC_BIN names no server binary');
      return;
    }

    final dir = Directory.systemTemp.createTempSync('console-server');
    addTearDown(() => dir.deleteSync(recursive: true));

    final server = SyncServer(
      exe: exe,
      dir: dir.path,
      token: 'a-token-nothing-else-knows',
      // A port unlikely to be the one a developer has a real server on.
      addr: '127.0.0.1:8123',
      // Never systemd here: this test owns the process it started, and a unit
      // installed on the machine running the suite must not be touched by it.
      managedBy: () => false,
    );

    await server.start();
    addTearDown(server.stop);
    final overview = await _eventually(server.overview);

    expect(overview, isNotNull, reason: 'the server never answered /overview');
    expect(overview!['devices'], isEmpty);

    final metrics = await server.metrics();
    expect(metrics, isNotNull, reason: '/metrics refused the minted token');
    expect(gauge(metrics!, 'summareader_server_devices'), 0);

    // Creating the first device stops the server and puts it back, because
    // that command writes into the database the server has open.
    final device = await runFirstDevice(server);
    expect(device.token, isNotEmpty);
    expect(device.accountId, isNotEmpty);

    final after = await _eventually(server.overview);
    expect(after!['devices'], hasLength(1));
    expect(
      (after['devices'] as List<dynamic>).first,
      containsPair('account', device.accountId),
    );
  }, timeout: const Timeout(Duration(minutes: 2)));
}

/// Retries until the server is listening. PocketBase runs its migrations
/// first, which on an empty directory is a second or two — a single request
/// straight after start would be testing how fast this machine is.
Future<T?> _eventually<T>(Future<T?> Function() ask) async {
  for (var attempt = 0; attempt < 30; attempt++) {
    final answer = await ask();
    if (answer != null) return answer;
    await Future<void>.delayed(const Duration(milliseconds: 500));
  }
  return null;
}

String? _serverBinary() {
  final named = Platform.environment['SUMMAREADER_SYNC_BIN'] ?? '';
  if (named.isEmpty || !File(named).existsSync()) return null;
  return File(named).absolute.path;
}
