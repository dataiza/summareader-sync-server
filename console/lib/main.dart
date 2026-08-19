import 'dart:io';

import 'package:flutter/material.dart';
import 'package:summareader_ui/summareader_ui.dart';

import 'src/console_screen.dart';

/// The desktop console for the sync server.
///
/// It is a separate program from the server, not a window bolted onto it. The
/// server has to cross-compile to five targets from one machine with no C
/// toolchain anywhere, and a window needs one — so the window became this, and
/// the server went back to being a plain `CGO_ENABLED=0` build with no tags.
/// What connects them is a child process and two HTTP endpoints.
void main(List<String> args) {
  WidgetsFlutterBinding.ensureInitialized();

  runApp(
    ConsoleApp(
      dir: _flag(args, '--dir') ?? defaultDataDir(),
      addr: _flag(args, '--http') ?? '127.0.0.1:8099',
    ),
  );
}

/// The same two flags the server's own `serve` takes, spelled the same way, so
/// somebody who knows one command already knows this one.
String? _flag(List<String> args, String name) {
  for (final arg in args) {
    if (arg.startsWith('$name=')) return arg.substring(name.length + 1);
  }
  final at = args.indexOf(name);
  return at >= 0 && at + 1 < args.length ? args[at + 1] : null;
}

/// Where the database goes when nothing says otherwise — the same directory
/// the server picks for itself, because a console that opened a different one
/// would report an empty server and be right about the wrong database.
String defaultDataDir() {
  final env = Platform.environment;
  final base = switch (Platform.operatingSystem) {
    'windows' => env['APPDATA'] ?? '',
    'macos' => '${env['HOME']}/Library/Application Support',
    _ => env['XDG_CONFIG_HOME'] ?? '${env['HOME']}/.config',
  };
  return '$base/summareader-sync';
}

class ConsoleApp extends StatelessWidget {
  const ConsoleApp({super.key, required this.dir, required this.addr});

  final String dir;
  final String addr;

  @override
  Widget build(BuildContext context) {
    // The palette follows the desktop's own light or dark setting. One line
    // rather than a preference: this window is opened to answer a question and
    // closed again, and a theme switch in it would be a setting nobody came
    // here for.
    final brightness =
        WidgetsBinding.instance.platformDispatcher.platformBrightness;
    Ar.use(brightness);

    return MaterialApp(
      title: 'SummaReader Sync Server',
      debugShowCheckedModeBanner: false,
      theme: Ar.themeData(brightness),
      home: Scaffold(
        backgroundColor: Ar.bg,
        body: ConsoleScreen(dir: dir, addr: addr),
      ),
    );
  }
}
