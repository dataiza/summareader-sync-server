import 'dart:io';

import 'package:flutter/material.dart';
import 'package:summareader_ui/summareader_ui.dart';

import 'src/config.dart';
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

  // The same precedence the server resolves, for the same two options: flag,
  // then environment, then the config file beside the data directory, then the
  // default. The console has to agree with the server about where the server
  // is, or it opens reporting nothing against a server that is running fine.
  final env = Platform.environment;
  final where = resolveDirs(args, env);
  final configured = configBind(where.config);

  runApp(
    ConsoleApp(
      dir: where.data,
      configDir: where.config,
      chosen: where.chosen,
      addr:
          flagValue(args, '--http') ??
          env['SUMMAREADER_HTTP'] ??
          (configured.isEmpty ? '127.0.0.1:8099' : configured),
    ),
  );
}

class ConsoleApp extends StatelessWidget {
  const ConsoleApp({
    super.key,
    required this.dir,
    required this.configDir,
    required this.addr,
    required this.chosen,
  });

  /// Where the database is.
  final String dir;

  /// Where the config file is, which is the *default* data directory unless a
  /// flag or the environment moved both. A `dir` key in that file moves only
  /// the database, so after somebody chooses a directory in this window the
  /// two are different and the settings must keep being read and written where
  /// the server looks for them.
  final String configDir;

  final String addr;

  /// Whether anything said where the library goes.
  final bool chosen;

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
        body: ConsoleScreen(
          dir: dir,
          configDir: configDir,
          addr: addr,
          chosen: chosen,
        ),
      ),
    );
  }
}
