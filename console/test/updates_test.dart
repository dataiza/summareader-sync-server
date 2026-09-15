import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:summareader_sync_console/src/desktop_entry.dart';
import 'package:summareader_sync_console/src/updates.dart';
import 'package:summareader_sync_console/src/version.dart';

String _release(String tag, {List<String> assets = const ['x.AppImage']}) =>
    jsonEncode({
      'tag_name': tag,
      'assets': [
        for (final name in assets)
          {
            'name': name,
            'browser_download_url': 'https://example.invalid/$name',
          },
      ],
    });

void main() {
  group('which version is newer', () {
    test('by components, not by how the string sorts', () {
      // The whole reason this is not a string comparison: "0.10.0" sorts
      // before "0.9.0" as text, so the tenth release would never be offered.
      expect(isNewer('0.10.0', '0.9.0'), isTrue);
      expect(isNewer('0.9.0', '0.10.0'), isFalse);
    });

    test('the same version is not an update', () {
      expect(isNewer('1.2.3', '1.2.3'), isFalse);
    });

    test('and neither is an older one', () {
      expect(isNewer('0.1.0', '0.2.0'), isFalse);
      expect(isNewer('1.0.0', '1.0.1'), isFalse);
    });

    test('a missing component counts as zero', () {
      expect(isNewer('1.1', '1.0.9'), isTrue);
      expect(isNewer('1.0', '1.0.0'), isFalse);
    });

    test('something unparseable is never offered', () {
      // A release named for a branch, or a tag somebody typed by hand. Worse
      // than not updating is updating to something nobody meant.
      expect(isNewer('nightly', '0.1.0'), isFalse);
      expect(isNewer('v-two', '0.1.0'), isFalse);
    });
  });

  group('asking GitHub', () {
    Future<Release?> ask(String body, {int status = 200}) => GitHubUpdates(
      client: MockClient((_) async => http.Response(body, status)),
    ).newer();

    test('a newer release is offered, with its image', () async {
      final found = await ask(_release('v99.0.0'));
      expect(found?.version, '99.0.0');
      expect(found?.image.toString(), endsWith('.AppImage'));
    });

    test('the version this console already is, is not', () async {
      expect(await ask(_release('v$consoleVersion')), isNull);
    });

    test('a tag without its v is read the same way', () async {
      expect((await ask(_release('99.0.0')))?.version, '99.0.0');
    });

    test('a release carrying no AppImage is refused, not guessed at', () async {
      // Every release also carries the headless bundles and the archives
      // GitHub attaches itself. Replacing this program with a tarball would be
      // worse than doing nothing.
      expect(
        () => ask(_release('v99.0.0', assets: ['headless.tar.gz'])),
        throwsA(isA<FormatException>()),
      );
    });

    test('an answer that is not a release says so', () async {
      expect(() => ask('{}'), throwsA(isA<FormatException>()));
      expect(() => ask('[]'), throwsA(isA<FormatException>()));
    });

    test('and so does a refusal from the other end', () async {
      expect(() => ask('{}', status: 403), throwsA(isA<HttpException>()));
    });
  });

  group('where the running image is', () {
    test('APPIMAGE names it', () {
      expect(
        runningImage(const {'APPIMAGE': '/home/you/X.AppImage'}),
        '/home/you/X.AppImage',
      );
    });

    test('and without it there is nothing to replace', () async {
      // A tarball, or `flutter run`. Guessing from resolvedExecutable would
      // point into a read-only mount that vanishes when the process ends.
      expect(runningImage(const {}), isNull);
      expect(runningImage(const {'APPIMAGE': ''}), isNull);

      final refusal = await replaceRunningImage((
        version: '9.9.9',
        image: Uri.parse('https://example.invalid/x'),
      ), environment: const {});
      expect(refusal, contains('not running as an AppImage'));
    });
  });

  group('replacing the image', () {
    late Directory dir;

    setUp(() => dir = Directory.systemTemp.createTempSync('appimage'));
    tearDown(() => dir.deleteSync(recursive: true));

    test('the new bytes end up at the path that was running', () async {
      final image = File('${dir.path}/SummaReader.AppImage')
        ..writeAsStringSync('the old one');

      final refusal = await replaceRunningImage(
        (version: '9.9.9', image: Uri.parse('https://example.invalid/new')),
        client: MockClient((_) async => http.Response('the new one', 200)),
        imagePath: image.path,
      );

      expect(refusal, isNull);
      expect(image.readAsStringSync(), 'the new one');
      expect(
        File('${image.path}.new').existsSync(),
        isFalse,
        reason: 'the partial file is renamed, not left beside it',
      );
    });

    test('a download that fails leaves the old one alone', () async {
      final image = File('${dir.path}/SummaReader.AppImage')
        ..writeAsStringSync('the old one');

      final refusal = await replaceRunningImage(
        (version: '9.9.9', image: Uri.parse('https://example.invalid/new')),
        client: MockClient((_) async => http.Response('nope', 404)),
        imagePath: image.path,
      );

      expect(refusal, contains('404'));
      expect(image.readAsStringSync(), 'the old one');
      expect(File('${image.path}.new').existsSync(), isFalse);
    });

    test('an empty download is refused rather than written', () async {
      final image = File('${dir.path}/SummaReader.AppImage')
        ..writeAsStringSync('the old one');

      final refusal = await replaceRunningImage(
        (version: '9.9.9', image: Uri.parse('https://example.invalid/new')),
        client: MockClient((_) async => http.Response('', 200)),
        imagePath: image.path,
      );

      expect(refusal, contains('empty'));
      expect(image.readAsStringSync(), 'the old one');
    });
  });

  group('the applications menu', () {
    late Directory home;
    Map<String, String> env() => {'HOME': home.path, 'XDG_DATA_HOME': ''};

    setUp(() => home = Directory.systemTemp.createTempSync('menu'));
    tearDown(() => home.deleteSync(recursive: true));

    test('it is not there until it is added', () async {
      expect(isInMenu(env()), isFalse);

      expect(
        await addToMenu('/home/you/X.AppImage', environment: env()),
        isNull,
      );
      expect(isInMenu(env()), isTrue);

      expect(await removeFromMenu(environment: env()), isNull);
      expect(isInMenu(env()), isFalse);
    });

    test('the entry names the image that is running', () async {
      await addToMenu('/opt/apps/SummaReader MCP.AppImage', environment: env());
      final written = File(desktopFilePath(env())).readAsStringSync();

      // Escaped, because a path with a space in it is one somebody chose and
      // an unescaped Exec silently becomes two arguments.
      expect(written, contains(r'Exec=/opt/apps/SummaReader\ MCP.AppImage'));
      // The line the window is matched by. Without it the launcher shows the
      // program twice, once as its icon and once as an unnamed window.
      expect(written, contains('StartupWMClass=$desktopId'));
    });

    test('the icons travel with it, and go when it goes', () async {
      final icons = Directory('${home.path}/image/48x48/apps')
        ..createSync(recursive: true);
      File(
        '${icons.path}/$desktopId.png',
      ).writeAsStringSync('not really a png');

      await addToMenu(
        '/home/you/X.AppImage',
        environment: env(),
        iconsFrom: '${home.path}/image',
      );
      final installed = File(
        '${home.path}/.local/share/icons/hicolor/48x48/apps/$desktopId.png',
      );
      expect(installed.existsSync(), isTrue);

      await removeFromMenu(environment: env());
      expect(installed.existsSync(), isFalse);
    });

    test('XDG_DATA_HOME is followed when absolute, ignored when not', () {
      expect(
        desktopFilePath({'HOME': '/home/you', 'XDG_DATA_HOME': '/mnt/share'}),
        '/mnt/share/applications/$desktopId.desktop',
      );
      expect(
        desktopFilePath({'HOME': '/home/you', 'XDG_DATA_HOME': 'relative'}),
        '/home/you/.local/share/applications/$desktopId.desktop',
      );
    });
  });

  group('keeping the image somewhere the menu can point', () {
    late Directory home;
    Map<String, String> env() => {'HOME': home.path};

    setUp(() => home = Directory.systemTemp.createTempSync('keep'));
    tearDown(() => home.deleteSync(recursive: true));

    test('it moves out of downloads, and does not stay behind', () async {
      // Moved and not copied: two copies of a program that each replace
      // themselves from GitHub are two programs a month later.
      final downloads = Directory('${home.path}/Downloads')..createSync();
      final image = File('${downloads.path}/X.AppImage')
        ..writeAsStringSync('an image');

      final kept = await keepImage(image.path, environment: env());

      expect(kept, '${home.path}/Applications/X.AppImage');
      expect(File(kept).readAsStringSync(), 'an image');
      expect(image.existsSync(), isFalse, reason: 'one file, not two');
    });

    test('one already in place is left exactly where it is', () async {
      final apps = Directory('${home.path}/Applications')..createSync();
      final image = File('${apps.path}/X.AppImage')..writeAsStringSync('x');

      expect(await keepImage(image.path, environment: env()), image.path);
      expect(image.existsSync(), isTrue);
    });

    test('the entry names where it ended up, not where it was', () async {
      // The whole point: the path in the entry has to survive somebody
      // emptying their downloads folder.
      final downloads = Directory('${home.path}/Downloads')..createSync();
      final image = File('${downloads.path}/X.AppImage')
        ..writeAsStringSync('an image');

      final kept = await keepImage(image.path, environment: env());
      await addToMenu(kept, environment: env());

      expect(menuTarget(env()), kept);
      expect(menuTarget(env()), isNot(contains('Downloads')));
    });

    test('a stale entry is noticed, and a matching one is not', () {
      final apps = Directory('${home.path}/Applications')..createSync();
      File('${apps.path}/X.AppImage').writeAsStringSync('x');

      // Nothing added yet: nothing can be stale.
      expect(
        menuIsStale({...env(), 'APPIMAGE': '${apps.path}/X.AppImage'}),
        isFalse,
      );
    });
  });

  group('the two spellings of the entry', () {
    late Directory home;

    setUp(() => home = Directory.systemTemp.createTempSync('spelling'));
    tearDown(() => home.deleteSync(recursive: true));

    test('what is installed says the same as what is in the image', () async {
      // There are two copies of this entry: the one packed into the AppImage
      // (scripts/appimage/sk.dataiza.summareader_sync_console.desktop) and the one
      // addToMenu writes, which differs only in Exec naming an absolute path.
      //
      // Nothing enforced that until this test, and the comment warning about
      // it was already in the file when the MCP console shipped a menu entry
      // reading "SummaReader Sync Server" — ported from its sibling with the
      // application id replaced and the four display strings left alone.
      await addToMenu(
        '/home/you/X.AppImage',
        environment: {'HOME': home.path, 'XDG_DATA_HOME': ''},
      );
      final written = File(
        '${home.path}/.local/share/applications/sk.dataiza.summareader_sync_console.desktop',
      ).readAsLinesSync();
      final packed = File(
        '../scripts/appimage/sk.dataiza.summareader_sync_console.desktop',
      ).readAsLinesSync();

      for (final key in const [
        'Name',
        'GenericName',
        'Comment',
        'Icon',
        'Categories',
        'Keywords',
        'StartupWMClass',
      ]) {
        String? valueIn(List<String> lines) => lines
            .where((line) => line.startsWith('$key='))
            .map((line) => line.substring(key.length + 1))
            .firstOrNull;

        expect(
          valueIn(written),
          valueIn(packed),
          reason: '$key differs between the installed entry and the packed one',
        );
      }
    });
  });
}
