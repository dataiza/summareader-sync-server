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
