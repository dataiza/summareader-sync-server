import 'dart:convert';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:summareader_sync_console/src/addresses.dart';
import 'package:summareader_sync_console/src/config.dart';
import 'package:summareader_sync_console/src/console_view.dart';
import 'package:summareader_sync_console/src/format.dart';
import 'package:summareader_sync_console/src/pairing.dart';
import 'package:summareader_sync_console/src/server.dart';
import 'package:summareader_sync_console/src/service.dart';

void main() {
  _theTwoPages();
  _deviceControls();
  _serverIssuedCode();

  // The unit file is written by the console and read by systemd, and nothing
  // in between ever looks at it. A wrong ExecStart is a service that fails at
  // the next login, silently, in a log nobody has open — so the bytes are
  // asserted here rather than by installing one and hoping.
  test('the unit runs what the Start button runs', () {
    const config = ServiceConfig(
      exe: '/home/you/.local/bin/summareader-sync',
      addr: '10.10.20.1:8099',
      dir: '/home/you/pb_data',
      token: 'sesame',
    );
    final unit = renderUnit(config);

    // The point of the assertion: the line in the unit is the argv the child
    // process is started with, built by the same function. Two spellings of it
    // is how the desktop path and the service path drift into two servers.
    expect(
      unit,
      contains(
        'ExecStart=${serveArgv(config.exe, config.addr, config.dir).join(' ')}',
      ),
    );
    for (final want in [
      'ExecStart=/home/you/.local/bin/summareader-sync serve '
          '--http=10.10.20.1:8099 --dir=/home/you/pb_data',
      'Environment="SUMMAREADER_METRICS_TOKEN=sesame"',
      'ReadWritePaths=/home/you/pb_data',
      'WantedBy=default.target',
    ]) {
      expect(unit, contains(want));
    }
  });

  test('the container unit passes the bind in as two variables', () {
    final unit = renderUnit(
      const ServiceConfig(
        exe: '/home/you/.local/bin/summareader-sync',
        addr: '10.10.20.1:9000',
        dir: '/home/you/pb_data',
        token: 'sesame',
        compose: '/home/you/src/sync/docker-compose.yml',
        uid: 1000,
        gid: 1000,
      ),
    );

    // A container's own port is fixed by the image and only the mapping moves,
    // so the bind chosen in the console reaches compose as variables.
    for (final want in [
      'ExecStart=docker compose -f /home/you/src/sync/docker-compose.yml up',
      'ExecStop=docker compose -f /home/you/src/sync/docker-compose.yml down',
      'Environment="SYNC_BIND=10.10.20.1"',
      'Environment="SYNC_PORT=9000"',
      'Environment="SYNC_UID=1000"',
      'WorkingDirectory=/home/you/src/sync',
    ]) {
      expect(unit, contains(want));
    }

    // ProtectSystem=strict on the Docker path is a unit that cannot reach the
    // socket it needs. The image confines itself; this would only break it.
    expect(unit, isNot(contains('ProtectSystem')));

    // What the console reads back on the next launch, so it reopens on the
    // server that is actually running rather than on its own defaults.
    expect(serviceBindIn(unit), '10.10.20.1:9000');
  });

  // A console that mints a fresh token per window reports zero devices against
  // a server full of them, which reads exactly like a server nobody has paired
  // with. So the token is read back out of the unit that is already installed.
  test('the token is read out of an installed unit, not minted again', () {
    final unit = renderUnit(
      const ServiceConfig(
        exe: '/bin/summareader-sync',
        addr: '127.0.0.1:8099',
        dir: '/data',
        token: 'the-one-the-server-has',
      ),
    );

    expect(
      unitEnv(unit, 'SUMMAREADER_METRICS_TOKEN'),
      'the-one-the-server-has',
    );
    expect(serviceBindIn(unit), '127.0.0.1:8099');
    // Two calls to the minter never agree, which is why reading matters.
    expect(newToken(), isNot(newToken()));
  });

  // The exact bytes the QR carries, because they are a contract with the app.
  //
  // A renamed field or a stray one shows up nowhere on this side: the code
  // still draws, still scans, and the phone quietly refuses it. And the
  // missing key is the point — the app's own pairing code carries the
  // library key, this one must not, because the server has never had it.
  test('the pairing code carries the address and token and no key', () {
    final payload = pairingPayload('http://192.168.1.24:8099', 'tok-43-chars');

    expect(
      payload,
      '{"version":2,"server":"http://192.168.1.24:8099",'
      '"device_token":"tok-43-chars"}',
    );
    expect(payload, isNot(contains('"k"')));
    expect(payload, isNot(contains('key')));
  });

  // A machine with Docker on it holds several private addresses and only some
  // of them lead anywhere a phone can follow. Ordered against a written-down
  // list rather than against whatever this machine happens to have plugged in,
  // which is a different list on every machine that runs this.
  test('the virtual interfaces are offered last', () {
    final ordered = orderLanAddrs(const [
      LanAddr('172.18.0.1', 'br-27ee9d724738'),
      LanAddr('172.17.0.1', 'docker0'),
      LanAddr('10.10.20.1', 'enp7s0'),
      LanAddr('192.168.122.1', 'virbr0'),
      LanAddr('10.30.0.100', 'enp5s0f0'),
      LanAddr('192.168.1.24', 'wlan0'),
      LanAddr('172.20.0.2', 'veth7f21a3c'),
    ]);

    expect(ordered.map((a) => a.iface), [
      'enp7s0',
      'enp5s0f0',
      'wlan0',
      'br-27ee9d724738',
      'docker0',
      'virbr0',
      'veth7f21a3c',
    ]);

    // The address is what goes in the code; the interface is only how the
    // person tells them apart, so both have to reach the dialog.
    expect(ordered.first.toString(), '10.10.20.1 (enp7s0)');
  });

  // A code is only worth drawing at an address the scanning phone can open,
  // and the console's own default is the one address it cannot.
  test('the code address is never loopback', () {
    const lan = [LanAddr('192.168.1.24', 'wlan0')];

    for (final addr in ['127.0.0.1:8099', '0.0.0.0:8099', '[::]:8099']) {
      expect(reachableUrl(addr, lan), 'http://192.168.1.24:8099');
    }
    // Nothing to reach it by is said out loud rather than guessed at.
    expect(reachableUrl('127.0.0.1:8099', const []), '');

    // An address someone chose is left alone: they know where they put it.
    expect(reachableUrl('192.168.1.24:8099', lan), 'http://192.168.1.24:8099');
    expect(reachableUrl('sync.example:8099', lan), 'http://sync.example:8099');
  });

  // An address someone typed is a decision, and the dialog opens on it rather
  // than on whatever this machine's first interface happens to be.
  test('the chosen bind address is offered first', () {
    const lan = [LanAddr('192.168.1.24', 'wlan0'), LanAddr('10.0.0.5', 'eth0')];

    expect(pairingHosts('10.30.0.100:8099', lan).first.ip, '10.30.0.100');
    expect(pairingHosts('sync.example:8099', lan).first.ip, 'sync.example');
    for (final addr in ['127.0.0.1:8099', '0.0.0.0:8099', '[::]:8099']) {
      expect(pairingHosts(addr, lan), lan);
    }
  });

  // Loopback and everything are decisions rather than addresses, so they are
  // offered even though no interface answers to them — and an address already
  // in use survives the list not containing it.
  test('the address menu offers both decisions and keeps an unknown bind', () {
    const lan = [LanAddr('192.168.1.24', 'wlan0')];

    expect(bindHosts('127.0.0.1', lan).map((h) => h.ip).take(2), [
      '127.0.0.1',
      '0.0.0.0',
    ]);
    expect(bindHosts('sync.example', lan).first.ip, 'sync.example');

    expect(splitBind('10.10.20.1:8099'), ('10.10.20.1', '8099'));
    expect(splitBind('[::1]:8099'), ('::1', '8099'));
  });

  // The two ways a bridge address can be spelled, and the one that is not
  // private at all: 172.16-31 is the block, 172.32 is somebody's server.
  test('only private addresses are offered to a phone', () {
    expect(privateV4(InternetAddress('10.1.2.3')), isTrue);
    expect(privateV4(InternetAddress('172.16.0.1')), isTrue);
    expect(privateV4(InternetAddress('172.31.255.1')), isTrue);
    expect(privateV4(InternetAddress('172.32.0.1')), isFalse);
    expect(privateV4(InternetAddress('8.8.8.8')), isFalse);
  });

  // The third button asks two different questions and used to ask the wrong
  // one twice. With a library already on the server, offering to create a
  // first device again creates a second, unrelated one — which syncs nothing
  // and looks like it worked.
  test('the pairing button knows whether there is already a library', () {
    expect(pairButtonText(0), 'Create first device');
    expect(pairButtonText(3), 'Add a device');
  });

  // Who owns the server, said in the one place that decides it. With a unit
  // installed the console drives systemctl and never starts a child of its
  // own: two processes writing one SQLite file is how a sync server corrupts
  // itself, and neither of them would notice.
  test('a unit on disk makes systemd the owner', () async {
    final home = Directory.systemTemp.createTempSync('console-unit');
    addTearDown(() => home.deleteSync(recursive: true));

    expect(serviceInstalled(home.path), isFalse);
    File(unitPath(home.path))
      ..createSync(recursive: true)
      ..writeAsStringSync('[Unit]\n');
    expect(serviceInstalled(home.path), isTrue);
    // Where systemd looks, and nowhere else.
    expect(unitPath(home.path), '${home.path}/systemd/user/$unitName');

    final server = SyncServer(
      // Deliberately without a binary: with a unit installed, Start must go
      // to systemctl and must never reach for one. Two processes writing one
      // SQLite file is how a sync server corrupts itself.
      exe: null,
      dir: '/data',
      token: 'sesame',
      addr: '127.0.0.1:8099',
      managedBy: () => serviceInstalled(home.path),
    );
    expect(server.managed, isTrue);
    await expectLater(
      server.start(),
      throwsA(
        predicate<Object>((e) => !'$e'.contains('No summareader-sync binary')),
      ),
    );
  });

  test('the counts come out of the Prometheus text', () {
    const text =
        '# HELP summareader_server_devices Devices\n'
        'summareader_server_devices 3\n'
        'summareader_server_log_entries 1284\n';

    expect(gauge(text, 'summareader_server_devices'), 3);
    expect(gauge(text, 'summareader_server_log_entries'), 1284);
    expect(gauge(text, 'summareader_server_nothing'), 0);
  });

  test('the counts read as English', () {
    expect(plural(1, 'device'), '1 device');
    expect(plural(3, 'device'), '3 devices');
    expect(plural(1, 'entry'), '1 entry');
    expect(plural(0, 'entry'), '0 entries');

    final now = DateTime.parse('2026-08-19T12:00:00Z');
    expect(ago('', now: now), 'never synced');
    expect(ago('2026-08-19T11:59:00Z', now: now), 'just now');
    expect(ago('2026-08-19T11:30:00Z', now: now), '30 minutes ago');
    expect(ago('2026-08-19T09:00:00Z', now: now), '3 hours ago');
    expect(ago('2026-08-10T12:00:00Z', now: now), '9 days ago');
  });

  // A device with no label is still a device, and a revoked one is still
  // somebody's — leaving either off the list hides the two cases worth seeing.
  test('the device list shows the unlabelled and the revoked', () {
    const state = ConsoleState(
      dir: '/data',
      addr: '127.0.0.1:8099',
      running: true,
      serverBinary: '/bin/summareader-sync',
      paired: [
        PairedDevice(id: 'k7d', entries: 0),
        PairedDevice(id: 'x1', label: 'Old laptop', revoked: true, entries: 4),
      ],
    );

    expect(state.statusLine, 'Running on http://127.0.0.1:8099');
    expect(state.paired.first.label, isEmpty);
    expect(state.countsLine, '0 devices · 0 entries · 0.0 MB');
  });

  // The complaint this file exists for: the address is changed in the window,
  // the window is closed, and it is back to the default. It has to survive.
  group('the config file', () {
    late Directory dir;
    setUp(() => dir = Directory.systemTemp.createTempSync('console-config'));
    tearDown(() => dir.deleteSync(recursive: true));

    test('a rebind written here comes back on the next open', () {
      saveConfig(dir.path, {'http': '10.10.20.1:9000'});
      expect(configBind(dir.path), '10.10.20.1:9000');
    });

    test('nothing configured is not an error, just nothing', () {
      expect(configBind(dir.path), isEmpty);
    });

    test('keys this version does not know are kept', () {
      File(configPath(dir.path)).writeAsStringSync(
        '{"_comment":"hands off","http":"old:1","future":42}',
      );
      saveConfig(dir.path, {'http': 'new:2'});

      final values = readConfig(dir.path);
      expect(values['_comment'], 'hands off');
      expect(values['future'], 42);
      expect(values['http'], 'new:2');
    });

    test('a malformed file is refused, not overwritten', () {
      const broken = '{"http": "1.2.3.4:1",}';
      final file = File(configPath(dir.path))..writeAsStringSync(broken);

      expect(() => saveConfig(dir.path, {'http': 'new:2'}), throwsException);
      expect(file.readAsStringSync(), broken);
    });

    test('the metrics token is never written by a rebind', () {
      saveConfig(dir.path, {'http': 'a:1'});
      expect(readConfig(dir.path).containsKey('metrics_token'), isFalse);
    });
  });
}

void _deviceControls() {
  group('what an operator can do to a device', () {
    // Not running, so the server's own Start/Stop button says "Start" and
    // cannot be confused with the Stop on a device row.
    ConsoleState stateWith(List<PairedDevice> paired) =>
        ConsoleState(dir: '/tmp', addr: '127.0.0.1:8099', paired: paired);

    testWidgets('a stopped device offers Resume, not Stop', (tester) async {
      // Stopping was one-way in the window though never in the data: the
      // column has always been a boolean, and a device stopped by mistake
      // needed pairing again from scratch to come back.
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: ConsoleView(
              state: stateWith([
                const PairedDevice(id: 'd1', label: 'Phone', revoked: true),
              ]),
              onResume: (_) {},
              onRemove: (_) {},
            ),
          ),
        ),
      );

      expect(find.text('Resume'), findsOneWidget);
      expect(find.text('Stop'), findsNothing);
      expect(find.text('Remove'), findsOneWidget);
    });

    testWidgets('a working device offers Stop and Rename', (tester) async {
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: ConsoleView(
              state: stateWith([const PairedDevice(id: 'd1', label: 'Phone')]),
              onRename: (_) {},
              onRevoke: (_) {},
              onRemove: (_) {},
            ),
          ),
        ),
      );

      expect(find.text('Stop'), findsOneWidget);
      expect(find.text('Rename'), findsOneWidget);
      expect(find.text('Resume'), findsNothing);
      // Whichever state it is in: a device worth forgetting is usually one
      // that was stopped first.
      expect(find.text('Remove'), findsOneWidget);
    });
  });
}

void _serverIssuedCode() {
  group('the code the server can draw', () {
    test('carries an address and a token, and no key', () {
      // The server has never held a key and cannot put one in. What it can do
      // is save somebody typing an address and a token, which is exactly what
      // the app accepts a keyless code for.
      final decoded =
          jsonDecode(pairingPayload('http://10.0.0.2:8099', 'a-token'))
              as Map<String, dynamic>;

      expect(decoded['server'], 'http://10.0.0.2:8099');
      expect(decoded['device_token'], 'a-token');
      expect(decoded.containsKey('master_key'), isFalse);
      expect(decoded.containsKey('k'), isFalse);
    });

    test('the button says which of the two questions it is answering', () {
      expect(pairButtonText(0), 'Create first device');
      expect(pairButtonText(3), 'Add a device');
    });
  });
}

void _theTwoPages() {
  group('the window and what the menu keeps off it', () {
    // Linux and a binary present, which is the only state in which every
    // control this checks for is drawn at all.
    const state = ConsoleState(
      dir: '/data',
      addr: '127.0.0.1:8099',
      serverBinary: '/bin/summareader-sync',
    );

    Future<void> pumpConsole(WidgetTester tester) => tester.pumpWidget(
      const MaterialApp(
        home: Scaffold(body: ConsoleView(state: state)),
      ),
    );

    testWidgets('the window is Status and Devices', (tester) async {
      await pumpConsole(tester);

      expect(find.text('Status'), findsOneWidget);
      expect(find.text('Devices'), findsOneWidget);
      // The sections that were a form: they are still reachable, just not in
      // the way of the two questions the window is opened to answer.
      expect(find.text('How it is reached'), findsNothing);
      expect(find.text('Start at login'), findsNothing);
      // What is pressed rather than read stays with the card it acts on.
      expect(find.text('Start'), findsOneWidget);
      expect(find.text('Open dashboard'), findsOneWidget);
      expect(find.text('Create first device'), findsOneWidget);
    });

    testWidgets('the menu reaches everything that moved', (tester) async {
      await pumpConsole(tester);

      await tester.tap(find.text('Menu'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('Settings').last);
      await tester.pumpAndSettle();

      // Nothing was cut on the way across: the name, the address, the port,
      // the directory and the service are all on the page the menu opens.
      for (final moved in [
        'How it is reached',
        'Name',
        'Bind address',
        'Port',
        'Data directory',
        '/data',
        'Start at login',
        'Keep it running',
      ]) {
        expect(find.text(moved), findsWidgets, reason: moved);
      }
      expect(find.text('Devices'), findsNothing);

      await tester.tap(find.text('Back'));
      await tester.pumpAndSettle();
      expect(find.text('Devices'), findsOneWidget);
    });
  });
}
