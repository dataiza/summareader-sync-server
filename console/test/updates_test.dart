import 'dart:convert';
import 'dart:io';

import 'package:crypto/crypto.dart' show sha256;
import 'package:cryptography/cryptography.dart' show Ed25519, SimpleKeyPair;
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:summareader_sync_console/src/config.dart';
import 'package:summareader_sync_console/src/console_screen.dart';
import 'package:summareader_sync_console/src/console_view.dart';
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

const _base =
    'https://github.com/dataiza/summareader-sync-server/releases/download/v9.9.9';

/// A release laid out the way the release workflow lays one out: the image,
/// and beside it the checksum list and its signature. Card 719.
final Release _signedRelease = (
  version: '9.9.9',
  image: Uri.parse('$_base/SummaReaderSync-9.9.9-x86_64.AppImage'),
  sums: Uri.parse('$_base/SHA256SUMS'),
  signature: Uri.parse('$_base/SHA256SUMS.sig'),
);

/// A throwaway key pair, so the tests sign as the release workflow does
/// without the real key, and the public half they pass as the trusted one.
late SimpleKeyPair _signer;
late List<int> _trusted;

/// A fake GitHub serving [_signedRelease] with [body] as the image.
///
/// The checksum list is right for [body] unless [manifest] says otherwise,
/// and signed by [_signer] unless [signer] says otherwise. [length] is the
/// length the image's answer claims, which is the body's unless given.
Future<http.Client> _serving(
  String body, {
  String? manifest,
  SimpleKeyPair? signer,
  int? length,
}) async {
  final name = _signedRelease.image.pathSegments.last;
  final sums = manifest ?? '${sha256.convert(utf8.encode(body))}  $name\n';
  final signature = await Ed25519().sign(
    utf8.encode(sums),
    keyPair: signer ?? _signer,
  );
  return MockClient.streaming((request, _) async {
    http.StreamedResponse answer(List<int> bytes, {int? claimed}) =>
        http.StreamedResponse(
          Stream.value(bytes),
          200,
          contentLength: claimed ?? bytes.length,
          request: request,
        );
    final url = request.url;
    if (url == _signedRelease.sums) return answer(utf8.encode(sums));
    if (url == _signedRelease.signature) return answer(signature.bytes);
    if (url == _signedRelease.image) {
      return answer(utf8.encode(body), claimed: length);
    }
    return http.StreamedResponse(const Stream.empty(), 404, request: request);
  });
}

void main() {
  setUpAll(() async {
    _signer = await Ed25519().newKeyPair();
    _trusted = (await _signer.extractPublicKey()).bytes;
  });

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

    test('and with the checksum list and its signature beside it', () async {
      final found = await ask(
        _release(
          'v99.0.0',
          assets: ['x.AppImage', 'SHA256SUMS', 'SHA256SUMS.sig'],
        ),
      );
      expect(found?.sums?.pathSegments.last, 'SHA256SUMS');
      expect(found?.signature?.pathSegments.last, 'SHA256SUMS.sig');
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

      final refusal = await replaceRunningImage(
        _signedRelease,
        environment: const {},
      );
      expect(refusal, contains('not running as an AppImage'));
    });
  });

  group('replacing the image', () {
    late Directory dir;
    late File image;

    setUp(() {
      dir = Directory.systemTemp.createTempSync('appimage');
      image = File('${dir.path}/SummaReader.AppImage')
        ..writeAsStringSync('the old one');
    });
    tearDown(() => dir.deleteSync(recursive: true));

    Future<String?> replace(http.Client client, {Release? release}) =>
        replaceRunningImage(
          release ?? _signedRelease,
          client: client,
          imagePath: image.path,
          publicKey: _trusted,
        );

    /// What every refusal must leave: the old image, and nothing beside it.
    void untouched() {
      expect(image.readAsStringSync(), 'the old one');
      expect(File('${image.path}.new').existsSync(), isFalse);
    }

    test('the new bytes end up at the path that was running', () async {
      final refusal = await replace(await _serving('the new one'));

      expect(refusal, isNull);
      expect(image.readAsStringSync(), 'the new one');
      expect(
        File('${image.path}.new').existsSync(),
        isFalse,
        reason: 'the partial file is renamed, not left beside it',
      );
    });

    test('a download that fails leaves the old one alone', () async {
      final refusal = await replace(
        MockClient((_) async => http.Response('nope', 404)),
      );

      expect(refusal, contains('404'));
      untouched();
    });

    test('an empty download is refused rather than written', () async {
      final refusal = await replace(await _serving(''));

      expect(refusal, contains('empty'));
      untouched();
    });

    // Card 719: the signature is what tells a release from somebody else's.
    test('an image that is not the one the list names is refused', () async {
      final name = _signedRelease.image.pathSegments.last;
      final refusal = await replace(
        await _serving(
          'a hostile one',
          manifest: '${sha256.convert(utf8.encode('the new one'))}  $name\n',
        ),
      );

      expect(refusal, contains('does not match its signed checksum'));
      untouched();
    });

    test('a list signed by another key is refused', () async {
      final refusal = await replace(
        await _serving('the new one', signer: await Ed25519().newKeyPair()),
      );

      expect(refusal, contains('not signed by the key'));
      untouched();
    });

    test('a release with no signed list is refused', () async {
      final refusal = await replace(
        await _serving('the new one'),
        release: (
          version: '9.9.9',
          image: _signedRelease.image,
          sums: null,
          signature: null,
        ),
      );

      expect(refusal, contains('no signed checksum list'));
      untouched();
    });

    test('and so is one whose list is named and not there', () async {
      final client = await _serving('the new one');
      final refusal = await replace(
        MockClient.streaming(
          (request, body) async => request.url == _signedRelease.sums
              ? http.StreamedResponse(const Stream.empty(), 404)
              : client.send(http.Request(request.method, request.url)),
        ),
      );

      expect(refusal, contains('404'));
      untouched();
    });

    test('a list that does not name the image is refused', () async {
      final refusal = await replace(
        await _serving('the new one', manifest: '${'0' * 64}  other.tar.gz\n'),
      );

      expect(refusal, contains('does not name'));
      untouched();
    });

    // Card 1028. The claimed length is larger than the body: a short stream
    // with no length claimed would prove nothing.
    test('a download that stops short of its length is refused', () async {
      final refusal = await replace(
        await _serving('the new one', length: 'the new one'.length + 100),
      );

      expect(refusal, contains('stopped at 11 of 111 bytes'));
      untouched();
    });

    test('an image anywhere but GitHub over https is refused', () async {
      for (final where in [
        'https://example.com/x.AppImage',
        'http://github.com/x.AppImage',
      ]) {
        final refusal = await replace(
          await _serving('the new one'),
          release: (
            version: '9.9.9',
            image: Uri.parse(where),
            sums: _signedRelease.sums,
            signature: _signedRelease.signature,
          ),
        );
        expect(refusal, contains('not GitHub over https'), reason: where);
        untouched();
      }
    });

    test('and so is a checksum list', () async {
      final refusal = await replace(
        await _serving('the new one'),
        release: (
          version: '9.9.9',
          image: _signedRelease.image,
          sums: Uri.parse('http://github.com/SHA256SUMS'),
          signature: _signedRelease.signature,
        ),
      );

      expect(refusal, contains('not GitHub over https'));
      untouched();
    });

    test('the release asset host and its redirect target are GitHub', () {
      expect(fromGitHub(_signedRelease.image), isTrue);
      expect(
        fromGitHub(Uri.parse('https://objects.githubusercontent.com/x')),
        isTrue,
      );
      expect(
        fromGitHub(Uri.parse('https://github.com.example.com/x')),
        isFalse,
      );
    });

    test('the built-in key is the 32 bytes Ed25519 wants', () {
      expect(updatePublicKey, hasLength(32));
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

    test('the copy it replaces goes, and only that one', () async {
      // A newer release downloaded by hand and run out of ~/Downloads, while
      // the menu still names the one in ~/Applications. Left alone that is two
      // programs, each checking GitHub for itself, one a version behind.
      final apps = Directory('${home.path}/Applications')..createSync();
      final old = File('${apps.path}/X-0.3.1.AppImage')..writeAsStringSync('o');
      final downloads = Directory('${home.path}/Downloads')..createSync();
      final image = File('${downloads.path}/X-0.3.2.AppImage')
        ..writeAsStringSync('n');

      final kept = await keepImage(image.path, environment: env());
      expect(supersedes(old.path, kept, environment: env()), isTrue);
      expect(
        await removeSuperseded(old.path, kept, environment: env()),
        isTrue,
      );
      expect(old.existsSync(), isFalse);
      expect(File(kept).existsSync(), isTrue, reason: 'never the kept one');
    });

    test('and nothing outside ~/Applications is ever deleted', () async {
      // The entry can name anything at all — somebody edited it, or runs the
      // image from a stick. Repointing is not a licence to delete that.
      final elsewhere = Directory('${home.path}/stick')..createSync();
      final theirs = File('${elsewhere.path}/X.AppImage')
        ..writeAsStringSync('x');
      final kept = '${home.path}/Applications/X-0.3.2.AppImage';

      expect(
        await removeSuperseded(theirs.path, kept, environment: env()),
        isFalse,
      );
      expect(theirs.existsSync(), isTrue);

      // Nor the one the entry now names, whatever it is called.
      final apps = Directory('${home.path}/Applications')..createSync();
      final same = File(kept)..writeAsStringSync('n');
      expect(await removeSuperseded(kept, kept, environment: env()), isFalse);
      expect(same.existsSync(), isTrue);
      expect(apps.existsSync(), isTrue);
    });

    test('the file is renamed to the version it now holds', () async {
      // The swap writes the new program into the old path, which is what
      // makes it atomic — so the name is a version behind until this runs,
      // and the launcher entry names that file.
      final apps = Directory('${home.path}/Applications')..createSync();
      final image = File('${apps.path}/App-0.3.4-x86_64.AppImage')
        ..writeAsStringSync('new program, old name');
      await addToMenu(image.path, environment: env());

      final now = await nameForVersion(image.path, '0.3.5', environment: env());

      expect(now, '${apps.path}/App-0.3.5-x86_64.AppImage');
      expect(File(now).existsSync(), isTrue);
      expect(image.existsSync(), isFalse);
      expect(menuTarget(env()), now, reason: 'the entry follows the file');
    });

    test('and left alone when there is no version in the name', () async {
      final apps = Directory('${home.path}/Applications')..createSync();
      final image = File('${apps.path}/App-x86_64.AppImage')
        ..writeAsStringSync('x');

      expect(
        await nameForVersion(image.path, '0.3.5', environment: env()),
        image.path,
      );
      expect(image.existsSync(), isTrue);
    });

    test('and an entry naming another copy is not hijacked', () async {
      final apps = Directory('${home.path}/Applications')..createSync();
      final theirs = File('${apps.path}/App-0.3.0-x86_64.AppImage')
        ..writeAsStringSync('another copy');
      await addToMenu(theirs.path, environment: env());
      final mine = File('${home.path}/Downloads/App-0.3.4-x86_64.AppImage')
        ..createSync(recursive: true)
        ..writeAsStringSync('mine');

      await nameForVersion(mine.path, '0.3.5', environment: env());

      expect(menuTarget(env()), theirs.path);
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
  group('after a download has finished', () {
    /// Runs a download through the whole screen, and answers with the state
    /// the view is drawing afterwards.
    ///
    /// Pumped whole rather than as a view handed a state, because both of the
    /// things that went wrong here are in the screen: one console never wrote
    /// the field at all, and this one wrote it and rebuilt it away two seconds
    /// later.
    Future<ConsoleState> downloaded(WidgetTester tester) async {
      final dir = Directory.systemTemp.createTempSync('console-update');
      addTearDown(() => dir.deleteSync(recursive: true));
      final image = File('${dir.path}/SummaReader-sync-0.3.8-x86_64.AppImage')
        ..writeAsStringSync('the old one');
      final renamed = File(
        '${dir.path}/SummaReader-sync-9.9.9-x86_64.AppImage',
      );

      await tester.pumpWidget(
        MaterialApp(
          home: ConsoleScreen(
            dir: dir.path,
            configDir: dir.path,
            addr: '127.0.0.1:8099',
            environment: {
              'APPIMAGE': image.path,
              'HOME': dir.path,
              'XDG_DATA_HOME': '',
            },
            client: await tester.runAsync(() => _serving('the new one')),
            updateKey: _trusted,
          ),
        ),
      );
      await tester.pump();

      // Outside the fake clock: the swap writes a file and renames it, and
      // real disk work does not finish on a pump. The rename is the last step
      // before the state is set, so waiting for it is waiting for the update.
      await tester.runAsync(() async {
        view(tester).onDownloadUpdate!(_signedRelease);
        while (!renamed.existsSync()) {
          await Future<void>.delayed(const Duration(milliseconds: 5));
        }
        await Future<void>.delayed(const Duration(milliseconds: 20));
      });
      await tester.pump();
      return view(tester).state;
    }

    testWidgets('there is somewhere to restart into', (tester) async {
      final state = await downloaded(tester);
      expect(state.updateSaid, contains('9.9.9'));
      // Without this the window says an update is in place and offers no way
      // to use it, which is what the other console did.
      expect(state.updateInstalled, isNotNull);
      expect(state.updateInstalled, endsWith('-9.9.9-x86_64.AppImage'));
    });

    testWidgets('and the button is wired to something', (tester) async {
      // The state was tested and the callback was not, so *Restart now* drew,
      // was pressed, and did nothing — for as long as this window has had an
      // updater. `restartInto` was here and correct the whole time; nothing
      // called it.
      await downloaded(tester);

      expect(
        view(tester).onRestart,
        isNotNull,
        reason: 'the button has no callback, so pressing it does nothing',
      );
    });

    testWidgets('and the poll two seconds later keeps it', (tester) async {
      await downloaded(tester);
      // The bug this exists for: the field was set correctly and then left out
      // of the list the poll rebuilds the state from, so the sentence survived
      // and the button under it did not.
      await tester.pump(const Duration(seconds: 3));
      await tester.pump();
      expect(view(tester).state.updateSaid, contains('9.9.9'));
      expect(view(tester).state.updateInstalled, isNotNull);
    });
  });

  group('keeping it up to date without being asked', () {
    test('the setting is written where the server config is', () async {
      // Beside the address and the token, so one directory is the whole
      // installation and the answer survives a restart.
      final dir = await Directory.systemTemp.createTemp('auto-update');
      addTearDown(() => dir.delete(recursive: true));

      saveConfig(dir.path, {'autoUpdate': true});

      expect(readConfig(dir.path)['autoUpdate'], isTrue);
    });

    test('and it is off in a file that has never heard of it', () async {
      // Off unless switched on: a window that asks the network about itself
      // before anybody said so is a window nobody chose.
      final dir = await Directory.systemTemp.createTemp('auto-update');
      addTearDown(() => dir.delete(recursive: true));
      saveConfig(dir.path, {'addr': ':8080'});

      expect(readConfig(dir.path)['autoUpdate'], isNot(true));
    });

    test('turning it on leaves every other key alone', () async {
      // `saveConfig` writes back only the keys given, which is what keeps a
      // newer server's settings through a change made here.
      final dir = await Directory.systemTemp.createTemp('auto-update');
      addTearDown(() => dir.delete(recursive: true));
      saveConfig(dir.path, {'addr': ':9000', 'somethingNewer': 7});

      saveConfig(dir.path, {'autoUpdate': true});

      final after = readConfig(dir.path);
      expect(after['addr'], ':9000');
      expect(after['somethingNewer'], 7);
      expect(after['autoUpdate'], isTrue);
    });
  });
}

/// What the window is drawing right now.
ConsoleView view(WidgetTester tester) =>
    tester.widget<ConsoleView>(find.byType(ConsoleView));
