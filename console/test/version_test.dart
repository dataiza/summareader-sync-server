import 'dart:io';

import 'package:summareader_sync_console/src/version.dart';
import 'package:flutter_test/flutter_test.dart';

/// The number the window shows, against the one the release is named after.
///
/// Two copies of a version is how a window ends up confidently reporting the
/// one before the one it is. The release workflow reads pubspec.yaml; this is
/// what stops the constant beside it drifting, on the commit that moves one
/// and forgets the other rather than on the tag.
void main() {
  test('the window reports the version pubspec.yaml declares', () {
    final pubspec = File('pubspec.yaml').readAsStringSync();
    final declared = RegExp(
      r'^version:\s*(\S+)',
      multiLine: true,
    ).firstMatch(pubspec);
    expect(declared, isNotNull, reason: 'pubspec.yaml has no version');
    expect(consoleVersion, declared!.group(1));
  });
}
