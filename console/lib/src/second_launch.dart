/// What a running console does when another console is started (card 1007).
///
/// The console is unique on Linux, so a second launch never becomes a second
/// window with a second server beside the first on one address and one
/// library. It used to be exactly that, and a newer download started while an
/// older console ran simply opened next to it. The runner now hands over the
/// second launch's file, its version and its arguments; this decides what to
/// make of them. A copy of the app's own (card 900).
library;

import 'dart:io';

import 'package:flutter/services.dart';

import 'updates.dart';
import 'version.dart';

/// The runner's side is `my_application_command_line` in
/// linux/runner/my_application.cc.
const launchChannel = MethodChannel('sk.dataiza.summareader.sync/launch');

/// The file this copy was started from: the AppImage itself, or the binary
/// when it is not one.
String ownLaunchPath([Map<String, String>? environment]) =>
    runningImage(environment) ?? Platform.resolvedExecutable;

/// This copy's version as the runner spells it, which is pubspec.yaml's
/// version as it stands; the test beside [consoleVersion] keeps the two equal.
String get ownLaunchVersion => consoleVersion;

/// Whether a second launch is another copy worth switching to, rather than
/// this one started again.
///
/// Either half differing is enough. Another file at the same version is a
/// second download somebody chose to start; the same file at another version
/// is this copy updated in place, and the running process is the old one.
bool offersSwitch({
  required String path,
  required String version,
  required String ownPath,
  required String ownVersion,
}) => path != ownPath || version != ownVersion;

/// Listens for second launches and passes each one to [onLaunch].
void listenForLaunches(
  void Function(String path, String version, List<String> args) onLaunch,
) {
  launchChannel.setMethodCallHandler((call) async {
    if (call.method != 'launched') return;
    final args = (call.arguments as Map).cast<String, Object?>();
    onLaunch(args['path'] as String? ?? '', args['version'] as String? ?? '', [
      ...(args['args'] as List? ?? const []).cast<String>(),
    ]);
  });
}
