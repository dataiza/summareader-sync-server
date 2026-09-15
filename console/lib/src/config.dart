import 'dart:convert';
import 'dart:io';

/// The server's config file, from the console's side.
///
/// Same file, same keys, same precedence as the Go binary reads: a flag beats
/// an environment variable beats this file beats the default. The console
/// reads it so the window opens on the address that is actually configured,
/// and writes it so an address changed in the window is still there after the
/// window is closed — which it was not, before there was a file to put it in.
const configName = 'summareader-sync.json';

/// Beside the data directory, so one directory is the whole installation.
String configPath(String dir) => '$dir/$configName';

/// What is in the file, or an empty map when there is none.
///
/// A malformed file reads as empty here rather than throwing: this runs on the
/// way to drawing a window, and a console that will not open is a worse way to
/// report a stray comma than a console that opens on the defaults. [saveConfig]
/// is where it matters, and that one refuses.
Map<String, dynamic> readConfig(String dir) {
  try {
    final raw = File(configPath(dir)).readAsStringSync();
    return jsonDecode(raw) as Map<String, dynamic>;
  } on Object {
    return const {};
  }
}

/// Writes back only the keys given, keeping everything else in the file —
/// including keys this version has never heard of, which is how a newer
/// server's settings and the example file's comments survive a port change.
///
/// A file that exists and does not parse is refused, with the parse error and
/// the path. Written to a temporary file and renamed, so a failure leaves the
/// old file rather than half of a new one.
void saveConfig(String dir, Map<String, dynamic> updates) {
  final file = File(configPath(dir));
  final values = <String, dynamic>{};
  if (file.existsSync()) {
    final raw = file.readAsStringSync();
    try {
      values.addAll(jsonDecode(raw) as Map<String, dynamic>);
    } on FormatException catch (error) {
      throw Exception('${file.path}: $error');
    }
  }
  values.addAll(updates);

  file.parent.createSync(recursive: true);
  final tmp = File('${file.path}.tmp');
  tmp.writeAsStringSync(
    '${const JsonEncoder.withIndent('  ').convert(values)}\n',
  );
  tmp.renameSync(file.path);
}

/// The bind address the file names, or empty when it names none.
String configBind(String dir) => (readConfig(dir)['http'] as String?) ?? '';

/// Where the file is, and where the database is — which are not always the
/// same directory.
///
/// The file lives beside the *default* data directory, and a `dir` key inside
/// it moves the database somewhere else. That looks circular and is not: the
/// file is found from the flag, the environment or the default and **never**
/// from the key it is about to read, which is the rule `configPath` follows in
/// Go for exactly this reason. Follow the key to find the file and a `dir`
/// pointing anywhere makes the file that holds it unfindable on the next
/// start.
typedef Dirs = ({String config, String data});

/// The precedence the server resolves, spelled once for this side of it.
///
/// `--dir`, then SUMMAREADER_DIR — either of which moves the file and the
/// database together — then the `dir` key, which moves only the database.
Dirs resolveDirs(List<String> args, Map<String, String> environment) {
  final named = flagValue(args, '--dir') ?? environment['SUMMAREADER_DIR'];
  if (named != null && named.isNotEmpty) {
    return (config: named, data: named);
  }

  final fallback = defaultDataDir(environment);
  final stored = readConfig(fallback)['dir'];
  return (
    config: fallback,
    data: stored is String && stored.isNotEmpty ? stored : fallback,
  );
}

/// Where the database goes when nothing says otherwise — the same directory
/// the server picks for itself, because a console that opened a different one
/// would report an empty server and be right about the wrong database.
///
/// The **data** directory. This said `XDG_CONFIG_HOME` until the server's own
/// default did, and the two have to move together. Only Linux changed: on
/// macOS and Windows there is one sensible place for both, and it is the one
/// already named here.
///
/// [environment] is a parameter so a test can state a machine rather than
/// inherit this one's.
String defaultDataDir([Map<String, String>? environment]) {
  final env = environment ?? Platform.environment;
  final base = switch (Platform.operatingSystem) {
    'windows' => env['APPDATA'] ?? '',
    'macos' => '${env['HOME']}/Library/Application Support',
    // Absolute or ignored, as the specification requires: a relative one
    // resolved against the working directory is two databases waiting to
    // happen.
    _ =>
      (env['XDG_DATA_HOME']?.startsWith('/') ?? false)
          ? env['XDG_DATA_HOME']!
          : '${env['HOME']}/.local/share',
  };
  return '$base/summareader-sync';
}

/// The same two flags the server's own `serve` takes, spelled the same way, so
/// somebody who knows one command already knows this one.
///
/// Public because main() reads `--http` with it: one spelling of "find a flag"
/// rather than two that can disagree about `--dir=x` versus `--dir x`.
String? flagValue(List<String> args, String name) {
  for (final arg in args) {
    if (arg.startsWith('$name=')) return arg.substring(name.length + 1);
  }
  final at = args.indexOf(name);
  return at >= 0 && at + 1 < args.length ? args[at + 1] : null;
}
