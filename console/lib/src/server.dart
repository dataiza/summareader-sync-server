import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:math';

import 'addresses.dart';
import 'service.dart';

/// One paired device, as /overview reports it.
class PairedDevice {
  const PairedDevice({
    required this.id,
    this.label = '',
    this.revoked = false,
    this.lastSeen = '',
    this.entries = 0,
  });

  factory PairedDevice.fromJson(Map<String, dynamic> json) => PairedDevice(
    id: json['id'] as String? ?? '',
    label: json['label'] as String? ?? '',
    revoked: json['revoked'] as bool? ?? false,
    lastSeen: json['last_seen'] as String? ?? '',
    entries: (json['entries'] as num?)?.toInt() ?? 0,
  );

  final String id;
  final String label;
  final bool revoked;
  final String lastSeen;
  final int entries;
}

/// What `first-device --json` prints: an account, a device, and the one token
/// that device will ever be shown.
class FirstDevice {
  const FirstDevice({
    required this.accountId,
    required this.deviceId,
    required this.token,
  });

  factory FirstDevice.fromJson(Map<String, dynamic> json) => FirstDevice(
    accountId: json['account_id'] as String? ?? '',
    deviceId: json['device_id'] as String? ?? '',
    token: json['token'] as String? ?? '',
  );

  final String accountId;
  final String deviceId;
  final String token;
}

/// The server binary this console supervises.
///
/// Not this executable: the console is a Flutter window and the server is the
/// portable Go binary, which is the one thing that must not need a display or
/// a C toolchain to run. Looked for beside the console first, because that is
/// how the two are shipped together, and then on PATH.
///
/// Null when there is none, which the status line says out loud. A console
/// whose Start button does nothing, with no explanation, is worse than one
/// that admits it cannot find the server.
String? serverBinary() {
  final named = Platform.environment['SUMMAREADER_SYNC_BIN'];
  if (named != null && named.isNotEmpty && File(named).existsSync()) {
    return File(named).absolute.path;
  }

  final exe = Platform.isWindows ? 'summareader-sync.exe' : 'summareader-sync';
  final beside = File(Platform.resolvedExecutable).parent.path;
  for (final candidate in [
    '$beside/$exe',
    '$beside/../$exe',
    './$exe',
    './dist/$exe',
  ]) {
    final file = File(candidate);
    if (file.existsSync()) return file.absolute.path;
  }

  final which = Platform.isWindows ? 'where' : 'which';
  final found = Process.runSync(which, [exe]);
  if (found.exitCode == 0) {
    final path = (found.stdout as String).trim().split('\n').first.trim();
    if (path.isNotEmpty) return path;
  }
  return null;
}

/// An opaque bearer token, from the platform's own source of randomness.
String newToken() {
  final random = Random.secure();
  return base64Url
      .encode(List<int>.generate(32, (_) => random.nextInt(256)))
      .replaceAll('=', '');
}

/// The server the console supervises, as a child process — or, when a unit is
/// installed, as something systemd owns and this only asks about.
///
/// A child process rather than something embedded, so the console's own crash
/// cannot take the server down with it, and so the desktop path runs exactly
/// the argv the service path runs. The counts come from that process's own
/// HTTP endpoints rather than from a second connection to the SQLite file it
/// is writing.
class SyncServer {
  SyncServer({
    required this.exe,
    required this.dir,
    required this.token,
    required this.addr,
    bool Function()? managedBy,
  }) : _managedBy = managedBy ?? serviceInstalled;

  /// Null when no server binary could be found on this machine.
  final String? exe;
  final String dir;
  final String token;

  /// The address the server binds to. It moves while the console is open —
  /// changing it is a stop, a rebind and a start, because a listening socket
  /// cannot be moved.
  String addr;

  Process? _child;
  bool _childRunning = false;

  /// How the question "is a unit installed" is asked. Injectable only so a
  /// test can put a unit somewhere other than the developer's own systemd
  /// directory; nothing in the app passes it.
  final bool Function() _managedBy;

  /// True when a systemd unit exists, which makes that unit the owner of the
  /// server and this console a remote control for it. Never both.
  bool get managed => _managedBy();

  Future<bool> get running async =>
      managed ? await serviceActive() : _childRunning;

  /// Whether something is already answering on this address.
  ///
  /// [running] only knows about servers this console has a handle on — its own
  /// child, or a unit. A second console left open on the same machine is
  /// neither, and starting beside it is two writers on one SQLite file.
  ///
  /// `/instance` and not `/overview`, because it is the one route that answers
  /// without a credential: a server somebody else started holds a different
  /// metrics token, and asking an authenticated route would read "nothing is
  /// there" for a server that plainly is.
  Future<bool> healthy() async => await _text('/instance') != null;

  Future<void> start() async {
    if (managed) {
      await systemctl(['start', unitName]);
      return;
    }
    final binary = exe;
    if (binary == null) {
      throw Exception(
        'No summareader-sync binary beside this console or on PATH. '
        'Build one with scripts/build.sh, or point SUMMAREADER_SYNC_BIN at it.',
      );
    }

    // The console's own pid goes with it, so the child can end itself if this
    // window is killed outright rather than closed. See [supervisedArgv].
    final argv = supervisedArgv(binary, addr, dir, pid);
    // A token minted per window and written nowhere is what switches the
    // metrics endpoints on. The status pane reads those rather than opening
    // the database beside the server: the counts already exist over HTTP, and
    // a second writer on one SQLite file does not.
    final child = await Process.start(
      argv.first,
      argv.skip(1).toList(),
      environment: {
        'SUMMAREADER_METRICS_TOKEN': token,
        // The same value under both names. They are separate credentials so
        // that a *scraper* can be given the read-only one — the console is
        // the operator and holds both by definition, and a second random
        // string here would be one more thing to keep in step for no boundary
        // that anybody stands on.
        'SUMMAREADER_OPERATOR_TOKEN': token,
      },
      mode: ProcessStartMode.inheritStdio,
    );
    _child = child;
    _childRunning = true;
    unawaited(child.exitCode.then((_) => _childRunning = false));
  }

  Future<void> stop() async {
    if (managed) {
      await systemctl(['stop', unitName]);
      return;
    }
    final child = _child;
    if (child == null || !_childRunning) return;

    // Interrupt first, so PocketBase closes the database instead of being cut
    // off mid-write. Windows has no interrupt to deliver and a server that
    // ignores one has to be killed regardless, so both roads end at kill.
    child.kill(ProcessSignal.sigint);
    await child.exitCode.timeout(
      const Duration(seconds: 5),
      onTimeout: () {
        child.kill(ProcessSignal.sigkill);
        return child.exitCode;
      },
    );
    _child = null;
    _childRunning = false;
  }

  /// What the running server holds, in one request: the totals and a row per
  /// paired device.
  ///
  /// Null when it is not up or will not answer. Errors are silence rather than
  /// a dialog: this runs every couple of seconds, and a server that is down is
  /// already saying so on the line above.
  Future<Map<String, dynamic>?> overview() async => _get('/overview');

  /// The Prometheus text, for the two numbers /overview does not carry in the
  /// same shape. One request, parsed for what it needs.
  Future<String?> metrics() async => _text('/metrics');

  Future<String?> _text(String path) async {
    final client = HttpClient()..connectionTimeout = const Duration(seconds: 2);
    try {
      final request = await client.getUrl(Uri.parse('http://$addr$path'));
      request.headers.set('Authorization', 'Bearer $token');
      final response = await request.close().timeout(
        const Duration(seconds: 2),
      );
      if (response.statusCode != HttpStatus.ok) {
        await response.drain<void>();
        return null;
      }
      // The body has its own deadline. request.close() only gets as far as the
      // headers, so an address that answers and then stops talking left this
      // waiting for as long as it cared to — on the poll that is a tick that
      // never finishes, and on a button it is the button.
      return await response
          .transform(utf8.decoder)
          .join()
          .timeout(const Duration(seconds: 2));
    } on Exception {
      return null;
    } finally {
      client.close(force: true);
    }
  }

  /// Renames a device. True when the server took it.
  ///
  /// Not silent like the polling reads: somebody pressed a button and is
  /// waiting to see whether it worked, and "the name is already used" is the
  /// answer they most need.
  Future<String?> rename(String deviceId, String label) =>
      _post('/operator/rename', {'device': deviceId, 'label': label});

  /// Stops a device syncing. Null on success, otherwise what went wrong.
  ///
  /// Reversible — see [resume]. The device keeps its own token, so nothing has
  /// to be carried back to it.
  Future<String?> revoke(String deviceId) =>
      _post('/operator/revoke', {'device': deviceId});

  /// Lets a stopped device sync again.
  Future<String?> resume(String deviceId) =>
      _post('/operator/resume', {'device': deviceId});

  /// Forgets a device entirely. Its token goes with it, so it cannot be
  /// resumed — it would have to pair again.
  Future<String?> remove(String deviceId) =>
      _post('/operator/remove', {'device': deviceId});

  /// Enrols another device and hands back its token.
  ///
  /// The code this produces carries somewhere to sync and something to
  /// authenticate with, and no key — the server has never held one. That is
  /// enough for a device that already holds this library's key and is being
  /// let back in, and not enough for one that has never seen it. The dialog
  /// says which.
  Future<FirstDevice?> enrol(String label) async {
    final body = await _postJson('/operator/enroll', {'label': label});
    if (body == null) return null;
    return FirstDevice.fromJson(body);
  }

  Future<Map<String, dynamic>?> _postJson(
    String path,
    Map<String, String> body,
  ) async {
    final client = HttpClient()..connectionTimeout = const Duration(seconds: 2);
    try {
      final request = await client.postUrl(Uri.parse('http://$addr$path'));
      request.headers.set('Authorization', 'Bearer $token');
      request.headers.contentType = ContentType.json;
      request.write(jsonEncode(body));
      final response = await request.close().timeout(
        const Duration(seconds: 5),
      );
      final text = await response.transform(utf8.decoder).join();
      if (response.statusCode != HttpStatus.ok) return null;
      return jsonDecode(text) as Map<String, dynamic>;
    } on Exception {
      return null;
    } finally {
      client.close(force: true);
    }
  }

  /// Null when it worked, otherwise a sentence to show.
  Future<String?> _post(String path, Map<String, String> body) async {
    final client = HttpClient()..connectionTimeout = const Duration(seconds: 2);
    try {
      final request = await client.postUrl(Uri.parse('http://$addr$path'));
      request.headers.set('Authorization', 'Bearer $token');
      request.headers.contentType = ContentType.json;
      request.write(jsonEncode(body));
      final response = await request.close().timeout(
        const Duration(seconds: 5),
      );
      final text = await response.transform(utf8.decoder).join();
      if (response.statusCode == HttpStatus.ok) return null;
      try {
        final decoded = jsonDecode(text) as Map<String, dynamic>;
        final message = decoded['error'];
        if (message is String && message.isNotEmpty) return message;
      } on FormatException {
        // Fall through to the status line, which is better than nothing.
      }
      return 'the server refused that (${response.statusCode})';
    } on Exception {
      return 'could not reach the server';
    } finally {
      client.close(force: true);
    }
  }

  Future<Map<String, dynamic>?> _get(String path) async {
    final body = await _text(path);
    if (body == null) return null;
    try {
      return jsonDecode(body) as Map<String, dynamic>;
    } on FormatException {
      return null;
    }
  }
}

/// Why the console will not start the server on its own, or null when it may.
///
/// The list is what [SyncServer.start] and `serveArgv` actually reach for: a
/// binary to run, a directory to hand it, and an address to bind. Named rather
/// than counted, the way the refusals in updates.dart name what stopped them —
/// a start that quietly did nothing is indistinguishable from a server that
/// failed to bind, and this is the start nobody was watching.
///
/// The metrics token is deliberately not in the list. It is minted per window
/// rather than read from anywhere, so it cannot be the thing that is missing.
String? autostartRefusal({
  required String? exe,
  required String dir,
  required String addr,
  bool managed = false,
}) {
  // A unit carries its own ExecStart, so the binary, the directory and the
  // address were all settled when it was installed and there is nothing left
  // here to be absent. Starting it is a systemctl call and nothing else.
  if (managed) return null;

  final absent = [
    if (exe == null || exe.isEmpty) 'no summareader-sync binary',
    if (dir.trim().isEmpty) 'nowhere to keep the library',
    if (addr.trim().isEmpty || splitBind(addr).$2.trim().isEmpty)
      'no address to listen on',
  ];
  if (absent.isEmpty) return null;
  return 'Not starting the server on its own: ${absent.join(', ')}. '
      'Start does the same thing by hand once that is sorted out, and says '
      'the same if it is not. Nothing has been started.';
}

/// Why the Start button will not start the server, or null when it may.
///
/// Beside [autostartRefusal] and for the same reasons: what a refused start
/// said is the whole of the feature, and a window is the part of a program a
/// test cannot look at.
///
/// [SyncServer.running] knows only about a unit or this console's own child, so
/// a server another console left up is neither — and starting into an address
/// it holds is a child that cannot bind and exits a moment later, leaving the
/// window saying *Stopped* with nothing said about why. That was the report.
///
/// Whatever is there is left where it is. It may be a unit somebody installed
/// on purpose, or another user's server: not this console's to stop, and
/// certainly not something to make room for by force.
Future<String?> startRefusal({
  required String addr,
  required Future<bool> Function() healthy,
}) async {
  // A button handler waits a short while and no longer. [SyncServer.healthy]
  // bounds itself at about four seconds in the worst case, and this is the
  // backstop for the rest: an address that half-answers should refuse the
  // press, not hang the control it was pressed on. Nothing was started either
  // way, and the next press asks again.
  final answering = await healthy().timeout(
    const Duration(seconds: 5),
    onTimeout: () => false,
  );
  if (!answering) return null;

  return 'Something is already serving on $addr, and it is not this window\'s '
      'server. Stop it where it was started, or give this one another address. '
      'Nothing has been started.';
}

/// The start nobody pressed.
///
/// A function rather than three lines inside the window, because whether an
/// unattended start happened, and what it said when it would not, is the whole
/// of this feature — and a window is the part of a program a test cannot look
/// at. [alreadyUp] and [start] are injected for the same reason.
///
/// Returns null when there is nothing to say: either the server was started,
/// or one was already up and was left exactly where it was. Never a stop.
Future<String?> autostart({
  required String? exe,
  required String dir,
  required String addr,
  required bool managed,
  required Future<bool> Function() alreadyUp,
  required Future<void> Function() start,
}) async {
  // Checked here rather than trusted: a setting written on a machine that had
  // a server binary, and read on one that does not, is how an unattended start
  // turns into a window quietly reporting nothing.
  final refused = autostartRefusal(
    exe: exe,
    dir: dir,
    addr: addr,
    managed: managed,
  );
  if (refused != null) return refused;
  if (await alreadyUp()) return null;
  await start();
  return null;
}

/// Picks one unlabelled sample out of the Prometheus text format. Enough for
/// two numbers on a window — a parser for the whole format would be a
/// dependency for something the server prints ten lines of.
double gauge(String text, String name) {
  for (final line in text.split('\n')) {
    if (line.startsWith('$name ')) {
      return double.tryParse(line.substring(name.length + 1).trim()) ?? 0;
    }
  }
  return 0;
}

/// What the database weighs on disk, walked from outside the server. Reading
/// file sizes is not a second writer, so this one is safe to do here.
int dirSize(String dir) {
  var total = 0;
  final root = Directory(dir);
  if (!root.existsSync()) return 0;
  for (final entry in root.listSync(recursive: true, followLinks: false)) {
    if (entry is File) {
      try {
        total += entry.lengthSync();
      } on FileSystemException {
        // A file the server deleted between the listing and the stat. Not
        // worth reporting: the figure is a rounded megabyte count.
      }
    }
  }
  return total;
}

/// Issues a first device by running the server binary's own `first-device`.
///
/// The server steps aside while it does. That command writes the account into
/// the very database the server has open, and rather than reason about two
/// writers on one SQLite file, this stops the child, runs the command that
/// already exists, and puts the server back if it was up.
Future<FirstDevice> runFirstDevice(SyncServer server) async {
  final binary = server.exe;
  if (binary == null) {
    throw Exception('No summareader-sync binary to create a device with.');
  }

  final wasRunning = await server.running;
  await server.stop();
  try {
    // `first-device` and not the `pair` alias it still answers to: the alias
    // prints a deprecation note, PocketBase points cobra's writers at stdout,
    // and so the note lands in front of the JSON and nothing here can parse
    // it. That was this button reporting "pairing produced no token".
    //
    // --json, because the human-readable form prints a wall of migration
    // output around the token and this needs the token itself.
    final result = await Process.run(binary, [
      'first-device',
      '--dir=${server.dir}',
      '--json',
    ]);
    if (result.exitCode != 0) {
      throw Exception('first-device: ${(result.stderr as String).trim()}');
    }
    try {
      return FirstDevice.fromJson(
        jsonDecode((result.stdout as String).trim()) as Map<String, dynamic>,
      );
    } on FormatException {
      throw Exception('pairing produced no token');
    }
  } finally {
    if (wasRunning) await server.start();
  }
}
