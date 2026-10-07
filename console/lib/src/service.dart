import 'dart:convert';
import 'dart:io';
import 'dart:math';

import 'addresses.dart';

/// Leaving the server running when the console is closed, as a systemd *user*
/// service — the same thing scripts/install.sh does, written from the console
/// so it works for somebody who has the binaries and not the repository.
///
/// A user service and not a system one, for the reason the script gives: this
/// holds one person's ciphertext on one person's machine, and a unit under
/// ~/.config needs no root to install, inspect or remove.
///
/// The unit runs either the server binary or `docker compose`, because "run it
/// as a container" and "keep it running" are different questions and the
/// answer to the second is systemd either way.
const unitName = 'summareader-sync.service';

/// The argv that starts the server, spelled once.
///
/// Once, because it is written into the unit here *and* handed to
/// Process.start when the console supervises a child of its own. Two spellings
/// would mean the desktop path and the service path drifting into two
/// different servers, with nothing to notice the difference until one of them
/// is serving a database the other never saw.
List<String> serveArgv(String exe, String addr, String dir) => [
  exe,
  'serve',
  '--http=$addr',
  '--dir=$dir',
];

/// The same argv, plus the one flag a child this console started gets: the pid
/// it should stop when it sees it go.
///
/// Deliberately not inside [serveArgv], because that list is also the unit's
/// ExecStart and a unit's server has no console to outlive — being left running
/// is the entire point of having installed one. A supervisor pid in there would
/// be a server that stops itself the first time somebody closes a window, or,
/// worse, when a later process happens to be given that number.
List<String> supervisedArgv(String exe, String addr, String dir, int pid) => [
  ...serveArgv(exe, addr, dir),
  '--supervisor-pid=$pid',
];

/// An opaque bearer token, from the platform's own source of randomness.
String newToken() {
  final random = Random.secure();
  return base64Url
      .encode(List<int>.generate(32, (_) => random.nextInt(256)))
      .replaceAll('=', '');
}

const metricsEnv = 'SUMMAREADER_METRICS_TOKEN';
const operatorEnv = 'SUMMAREADER_OPERATOR_TOKEN';

/// The two credentials the console holds for the server it runs. Card 716.
///
/// Two, and independent. The metrics one opens /metrics and /overview, which
/// are read-only, and is the one a scraper may be handed; the operator one
/// enrols, stops and removes devices. Until 716 the console gave both names one
/// value and wrote it into a unit anybody on the machine could read, so a
/// scraper set up from that file could also enrol devices.
class Tokens {
  const Tokens({
    required this.metrics,
    required this.operator,
    this.fromConfig = const {},
  });

  final String metrics;
  final String operator;

  /// The environment names whose value the config file already gives.
  final Set<String> fromConfig;

  /// What the server is started with: each token the config file does not
  /// already name.
  ///
  /// The server reads the environment before the file, so passing one the file
  /// also names would quietly replace the operator's own setting with the
  /// console's.
  Map<String, String> get environment => {
    if (metrics.isNotEmpty && !fromConfig.contains(metricsEnv))
      metricsEnv: metrics,
    if (operator.isNotEmpty && !fromConfig.contains(operatorEnv))
      operatorEnv: operator,
  };
}

/// Where each token comes from: the config file the server will read, then the
/// installed unit, then a fresh one.
///
/// The file first, because a `metrics_token` an operator wrote there is the one
/// their scraper already holds. The unit next, because a console opened beside
/// a running service has to hold what that server holds — minting afresh would
/// leave the pane reporting zero devices against a server full of them.
Tokens resolveTokens(Map<String, dynamic> config, String unit) {
  final fromConfig = <String>{};
  String pick(String key, String env) {
    final set = config[key];
    if (set is String && set.trim().isNotEmpty) {
      fromConfig.add(env);
      return set.trim();
    }
    final installed = unitEnv(unit, env);
    return installed.isNotEmpty ? installed : newToken();
  }

  final metrics = pick('metrics_token', metricsEnv);
  final operator = pick('operator_token', operatorEnv);
  return Tokens(metrics: metrics, operator: operator, fromConfig: fromConfig);
}

/// Everything that differs between one installation and the next. A null
/// [compose] means the unit runs the binary directly.
class ServiceConfig {
  const ServiceConfig({
    required this.exe,
    required this.addr,
    required this.dir,
    required this.tokens,
    this.compose,
    this.uid = 0,
    this.gid = 0,
  });

  final String exe;
  final String addr;
  final String dir;
  final Tokens tokens;
  final String? compose;
  final int uid;
  final int gid;
}

/// Writes the unit out. A pure function of the config so the thing that ends
/// up on disk can be asserted in a test rather than by installing it and
/// reading it back.
///
/// Every value is quoted in Environment= lines: a data directory with a space
/// in it is unremarkable on a desktop and unquoted would silently become two
/// variables, one of them empty.
String renderUnit(ServiceConfig c) {
  final b = StringBuffer();

  b.writeln('[Unit]');
  b.writeln('# Written by the SummaReader sync server console. Turning off');
  b.writeln('# "Start at login" there removes this file again.');
  b.writeln('Description=SummaReader sync server');
  b.writeln('Documentation=https://github.com/dataiza/summareader-sync-server');
  b.writeln('After=network-online.target');
  b.writeln('Wants=network-online.target');
  b.writeln();

  b.writeln('[Service]');
  // The same environment a child of the console is started with — see
  // [Tokens.environment]. Secrets in a unit, which is why [installService]
  // writes it readable by its owner alone.
  c.tokens.environment.forEach((name, value) {
    b.writeln('Environment="$name=$value"');
  });

  final compose = c.compose;
  if (compose == null) {
    b.writeln('ExecStart=${serveArgv(c.exe, c.addr, c.dir).join(' ')}');
  } else {
    // The compose file offers one port on loopback and one on a named
    // address, so the bind chosen in the console reaches the container as
    // those two variables rather than as a flag.
    final (host, port) = splitBind(c.addr);
    b.writeln('Environment="SYNC_BIND=$host"');
    b.writeln('Environment="SYNC_PORT=$port"');
    b.writeln('Environment="SYNC_UID=${c.uid}"');
    b.writeln('Environment="SYNC_GID=${c.gid}"');
    b.writeln('WorkingDirectory=${File(compose).parent.path}');
    b.writeln(
      'ExecStart=docker compose -f $compose up --abort-on-container-exit',
    );
    b.writeln('ExecStop=docker compose -f $compose down');
  }

  b.writeln('Restart=on-failure');
  b.writeln('RestartSec=5');

  // Hardening for the native path only. The database is the one thing the
  // server writes and everything else being read-only costs nothing here. The
  // container path is left alone: it needs the Docker socket and the image
  // does its own confining, so this would only be a way to break it.
  if (compose == null) {
    b.writeln();
    b.writeln('NoNewPrivileges=true');
    b.writeln('PrivateTmp=true');
    b.writeln('ProtectSystem=strict');
    b.writeln('ReadWritePaths=${c.dir}');
  }

  b.writeln();
  b.writeln('[Install]');
  b.writeln('WantedBy=default.target');
  return b.toString();
}

/// Honours XDG_CONFIG_HOME, because systemd does not look anywhere else.
///
/// [configHome] is for tests, which cannot change this process's environment
/// and have no business writing into the developer's real systemd directory.
String unitPath([String? configHome]) {
  final config =
      configHome ??
      Platform.environment['XDG_CONFIG_HOME'] ??
      '${Platform.environment['HOME'] ?? ''}/.config';
  return '$config/systemd/user/$unitName';
}

/// Decides who owns the server: with a unit on disk the console drives
/// systemctl, and without one it supervises a child of its own. Never both —
/// two processes writing one SQLite file is how a sync server corrupts itself,
/// and neither of them would notice it had happened.
bool serviceInstalled([String? configHome]) =>
    (Platform.isLinux || configHome != null) &&
    File(unitPath(configHome)).existsSync();

/// The installed unit's text, or empty when there is none.
String installedUnit() {
  try {
    return File(unitPath()).readAsStringSync();
  } on FileSystemException {
    return '';
  }
}

Future<bool> serviceActive() async =>
    (await Process.run('systemctl', [
      '--user',
      'is-active',
      '--quiet',
      unitName,
    ])).exitCode ==
    0;

/// The value of one quoted Environment= line, out of a unit's text.
String unitEnv(String unit, String name) {
  for (final line in unit.split('\n')) {
    final prefix = 'Environment="$name=';
    if (line.startsWith(prefix)) {
      return line.substring(prefix.length).trim().replaceAll('"', '');
    }
  }
  return '';
}

/// The address the installed unit serves on, so the console opens showing
/// where the server actually is rather than its own default.
String serviceBindIn(String unit) {
  for (final line in unit.split('\n')) {
    final flag = line.indexOf('--http=');
    if (flag >= 0) return line.substring(flag + 7).split(' ').first;
  }
  final host = unitEnv(unit, 'SYNC_BIND');
  if (host.isEmpty) return '';
  final port = unitEnv(unit, 'SYNC_PORT');
  return '$host:${port.isEmpty ? '8099' : port}';
}

String serviceBind() => serviceBindIn(installedUnit());

/// Which of the two the installed unit runs, so the console reopens on the
/// choice that was made rather than on the default.
bool serviceDocker() => installedUnit().contains('ExecStart=docker compose');

/// Writes the unit and enables it. Writing over an existing one is the update
/// path: the bind address changed in the console has to reach the unit too, or
/// the service comes back on the old one.
Future<void> installService(ServiceConfig config) async {
  final path = File(unitPath());
  await path.parent.create(recursive: true);
  await writePrivate(path, renderUnit(config));
  await systemctl(['daemon-reload']);
  await systemctl(['enable', '--now', unitName]);
}

/// Writes [text] to [file], readable and writable by its owner alone. Card 716.
///
/// The unit carries both tokens, and ~/.config is readable by everyone on
/// plenty of machines. Dart has no chmod, so the file is created empty,
/// narrowed, and only then given its contents: at no moment is it readable by
/// anybody else with a token in it. Written beside and renamed over, so a unit
/// an older console left at 0644 is replaced rather than rewritten in place.
Future<void> writePrivate(File file, String text) async {
  final tmp = File('${file.path}.tmp');
  await tmp.writeAsString('');
  await narrow(tmp.path);
  await tmp.writeAsString(text);
  await tmp.rename(file.path);
}

/// chmod 600, and a refusal rather than a shrug when it fails: a unit that is
/// still readable by everybody is the thing this exists to prevent.
Future<void> narrow(String path) async {
  final result = await Process.run('chmod', ['600', path]);
  if (result.exitCode != 0) {
    throw Exception('chmod 600 $path: ${'${result.stderr}'.trim()}');
  }
}

Future<void> uninstallService() async {
  // Disabled before removing: the enable symlink outlives the unit file and
  // leaves systemd complaining about it at every login.
  try {
    await systemctl(['disable', '--now', unitName]);
  } on ProcessException {
    // Nothing to disable is not a failure worth stopping the removal for.
  }
  final path = File(unitPath());
  if (path.existsSync()) await path.delete();
  await systemctl(['daemon-reload']);
}

/// The compose file this installation would run, or null when there is none —
/// the console offers the container choice only when there is something to
/// start. Beside the binary first, then beside the data directory, which are
/// the two places a copy of the repository puts it.
String? composeFile(String exe, String dir) {
  final near = File(exe).parent;
  for (final candidate in [
    '${near.path}/docker-compose.yml',
    '${near.parent.path}/docker-compose.yml',
    '${Directory(dir).parent.path}/docker-compose.yml',
    'docker-compose.yml',
  ]) {
    final file = File(candidate);
    if (file.existsSync()) return file.absolute.path;
  }
  return null;
}

/// Runs one command against the user manager, and reports what it said when it
/// fails — "exit status 1" in a message box tells nobody anything.
Future<void> systemctl(List<String> args) async {
  final result = await Process.run('systemctl', ['--user', ...args]);
  if (result.exitCode == 0) return;
  final said = '${result.stderr}${result.stdout}'.trim();
  throw Exception(
    'systemctl --user ${args.join(' ')}: '
    '${said.isEmpty ? 'exit ${result.exitCode}' : said}',
  );
}
