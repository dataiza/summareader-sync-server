import 'dart:io';

import 'package:flutter_test/flutter_test.dart';

/// The vendored look, checked against the one it was copied from.
///
/// `console/packages/summareader_ui` is a copy of the app's package — see
/// VENDORED.md there for why it cannot be a pubspec dependency. A copy drifts,
/// and drift in this one is a console that stops looking like the app one
/// widget at a time, with nothing to say so.
///
/// Skipped where the app is not checked out beside this repository, which is
/// what a CI box holding only this repository looks like. It has nothing to
/// drift from, and failing there would mean a red build for a file nobody on
/// that machine can see.
void main() {
  final source = Directory(
    Platform.environment['SUMMAREADER_REPO'] ?? '../../summareader',
  );

  test('the vendored look is the app\'s look', () {
    final upstream = Directory('${source.path}/packages/summareader_ui');
    if (!upstream.existsSync()) {
      markTestSkipped('no checkout of summareader at ${source.path}');
      return;
    }

    final differences = <String>[];
    for (final entry in upstream.listSync(recursive: true)) {
      if (entry is! File) continue;
      final relative = entry.path.substring(upstream.path.length + 1);
      final here = File('packages/summareader_ui/$relative');
      if (!here.existsSync()) {
        differences.add('$relative is missing here');
      } else if (here.readAsStringSync() != entry.readAsStringSync()) {
        differences.add('$relative differs');
      }
    }

    // The fonts too: the package names two families and ships neither, so a
    // console without them draws in whatever the platform reaches for.
    for (final font in ['Caprasimo-Regular.ttf', 'Figtree-Variable.ttf']) {
      final upstreamFont = File('${source.path}/assets/fonts/$font');
      final here = File('assets/fonts/$font');
      if (!upstreamFont.existsSync()) continue;
      if (!here.existsSync()) {
        differences.add('$font is missing here');
      } else if (here.lengthSync() != upstreamFont.lengthSync()) {
        differences.add('$font differs');
      }
    }

    expect(
      differences,
      isEmpty,
      reason:
          'The copy has drifted from ${source.path}. Run scripts/sync-ui.sh '
          'to bring it back, and check what changed.',
    );
  });
}
