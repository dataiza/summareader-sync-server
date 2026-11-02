import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:summareader_sync_console/src/config.dart';
import 'package:summareader_sync_console/src/console_screen.dart';
import 'package:summareader_sync_console/src/desktop_entry.dart';
import 'package:summareader_sync_console/src/second_launch.dart';
import 'package:summareader_sync_console/src/updates.dart';

/// `--install`, the running console that meets another one, and the restart
/// that waits for this process to go (card 1007).
///
/// The menu half runs the real moves and writes against a home made for the
/// test, so the only thing pretended is where home is. The switch itself —
/// one process ending and the other starting — needs two processes and a
/// session bus, and is tried by hand.
void main() {
  group('a second launch', () {
    const path = '/home/me/Applications/SummaReaderSync-1.3.1-x86_64.AppImage';

    test('the same file at the same version is only brought forward', () {
      expect(
        offersSwitch(
          path: path,
          version: '1.3.1',
          ownPath: path,
          ownVersion: '1.3.1',
        ),
        isFalse,
      );
    });

    test('another version offers the switch', () {
      expect(
        offersSwitch(
          path: path,
          version: '1.4.0',
          ownPath: path,
          ownVersion: '1.3.1',
        ),
        isTrue,
      );
    });

    test('another file offers the switch', () {
      expect(
        offersSwitch(
          path: '/home/me/Downloads/SummaReaderSync-1.3.1-x86_64.AppImage',
          version: '1.3.1',
          ownPath: path,
          ownVersion: '1.3.1',
        ),
        isTrue,
      );
    });
  });

  group('starting the next copy after this one has gone', () {
    test('the shell is handed the pid, the file and its arguments', () {
      final argv = startAfterExit(4242, '/a/b.AppImage', ['--install']);
      expect(argv.first, '-c');
      expect(argv.sublist(2), ['sh', '4242', '/a/b.AppImage', '--install']);
    });

    test('and it waits for that pid before it starts anything', () async {
      // A real process to wait for, and a real shell doing the waiting: the
      // script is the part that can be wrong, and reading it proves nothing.
      final waited = await Process.start('sleep', ['0.4']);
      final clock = Stopwatch()..start();
      final run = await Process.run(
        '/bin/sh',
        startAfterExit(waited.pid, '/bin/echo', ['started', '--install']),
      );
      clock.stop();
      await waited.exitCode;

      expect((run.stdout as String).trim(), 'started --install');
      expect(
        clock.elapsedMilliseconds,
        greaterThanOrEqualTo(800),
        reason: 'it started before the other process had gone',
      );
    });
  });

  group('--install', () {
    late Directory home;
    Map<String, String> env([String? image]) => {
      'HOME': home.path,
      'APPIMAGE': ?image,
    };

    File download(String name) => File('${home.path}/Downloads/$name')
      ..createSync(recursive: true)
      ..writeAsStringSync('an image');

    setUp(() => home = Directory.systemTemp.createTempSync('install'));
    tearDown(() => home.deleteSync(recursive: true));

    /// Opens the console as a launch with the given environment would, and
    /// waits until the menu entry names [expected] or a second has passed.
    Future<void> open(
      WidgetTester tester,
      Map<String, String> environment, {
      required bool install,
      String? expected,
    }) async {
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: ConsoleScreen(
              dir: home.path,
              configDir: home.path,
              addr: '127.0.0.1:8099',
              environment: environment,
              install: install,
            ),
          ),
        ),
      );
      await settle(
        tester,
        () =>
            expected != null &&
            menuTarget(env()) == expected &&
            readConfig(home.path)['in_menu'] == true,
      );
    }

    testWidgets('a first start installs without asking', (tester) async {
      final image = download('SummaReaderSync-1.3.1-x86_64.AppImage');
      final kept =
          '${home.path}/Applications/SummaReaderSync-1.3.1-x86_64.AppImage';

      await open(tester, env(image.path), install: true, expected: kept);

      expect(find.text('Add this to your applications?'), findsNothing);
      expect(image.existsSync(), isFalse);
      expect(File(kept).existsSync(), isTrue);
      expect(menuTarget(env()), kept);
      expect(readConfig(home.path)['in_menu'], isTrue);
    });

    testWidgets('an earlier no does not stop it', (tester) async {
      saveConfig(home.path, {'in_menu': false});
      final image = download('SummaReaderSync-1.3.1-x86_64.AppImage');
      final kept =
          '${home.path}/Applications/SummaReaderSync-1.3.1-x86_64.AppImage';

      await open(tester, env(image.path), install: true, expected: kept);

      expect(menuTarget(env()), kept);
    });

    testWidgets('a stale entry is repointed and the old copy removed', (
      tester,
    ) async {
      final old = File(
        '${home.path}/Applications/SummaReaderSync-1.3.0-x86_64.AppImage',
      )..createSync(recursive: true);
      await tester.runAsync(() => addToMenu(old.path, environment: env()));
      final image = download('SummaReaderSync-1.3.1-x86_64.AppImage');
      final kept =
          '${home.path}/Applications/SummaReaderSync-1.3.1-x86_64.AppImage';

      await open(tester, env(image.path), install: true, expected: kept);
      // The old copy goes after the entry is rewritten.
      await settle(tester, () => !old.existsSync());

      expect(menuTarget(env()), kept);
      expect(old.existsSync(), isFalse, reason: 'the superseded copy goes');
    });

    testWidgets('without the option, a first start still asks', (tester) async {
      final image = download('SummaReaderSync-1.3.1-x86_64.AppImage');

      await open(tester, env(image.path), install: false);

      expect(find.text('Add this to your applications?'), findsOneWidget);
      expect(image.existsSync(), isTrue);
      expect(isInMenu(env()), isFalse);
    });

    testWidgets('not an AppImage, nothing happens', (tester) async {
      await open(tester, env(), install: true);

      expect(isInMenu(env()), isFalse);
      expect(runningImage(env()), isNull);
    });
  });
}

/// Lets real disk work and the screen's own futures take turns until [done]
/// holds or a second has passed. The move and the entry are real files, which
/// finish outside the fake clock, and what the screen does with each answer
/// runs inside it, on a pump.
Future<void> settle(WidgetTester tester, bool Function() done) async {
  for (var i = 0; i < 100 && !done(); i++) {
    await tester.runAsync(
      () => Future<void>.delayed(const Duration(milliseconds: 10)),
    );
    // Fake time moves too, because the screen's own refresh after the
    // entry is written waits on timers.
    await tester.pump(const Duration(milliseconds: 100));
  }
}
