import 'dart:async';
import 'dart:io';
import 'dart:ui' show AppExitResponse;

import 'package:flutter/material.dart';
import 'package:http/http.dart' as http;
import 'package:summareader_ui/summareader_ui.dart';
import 'package:url_launcher/url_launcher.dart';

import 'addresses.dart';
import 'config.dart';
import 'desktop_entry.dart';
import 'first_run.dart';
import 'updates.dart';
import 'console_view.dart';
import 'pairing_dialogs.dart';
import 'server.dart';
import 'service.dart';

/// The console: everything that has to talk to a process, a socket or systemd,
/// wrapped around the view that draws it.
class ConsoleScreen extends StatefulWidget {
  const ConsoleScreen({
    super.key,
    required this.dir,
    required this.configDir,
    required this.addr,
    this.chosen = true,
    this.client,
    this.environment,
  });

  /// The two things an update needs from outside this process: somewhere to
  /// fetch the image from, and an APPIMAGE to replace. Null everywhere but a
  /// test, which has neither a network nor a running AppImage and so supplies
  /// a stand-in for both.
  final http.Client? client;
  final Map<String, String>? environment;

  /// Where the database is when the window opens. Mutable afterwards — see
  /// `_dir` in the state, which is what everything reads once somebody has
  /// chosen one.
  final String dir;

  /// Where the config file is. Never moves while the window is open: the
  /// server finds its settings from the flag, the environment or the default
  /// and never from the `dir` key it is about to read.
  final String configDir;

  /// False when nothing said where the library goes, which is the one
  /// condition the first-run question is asked on.
  final bool chosen;

  final String addr;

  @override
  State<ConsoleScreen> createState() => _ConsoleScreenState();
}

class _ConsoleScreenState extends State<ConsoleScreen>
    with WidgetsBindingObserver {
  late SyncServer _server;
  late ConsoleState _state;
  Timer? _poll;
  List<LanAddr> _lan = const [];
  String? _compose;

  /// Which of the two a unit would run. Held here as well as read off the
  /// unit, because the choice can be made before the switch that installs one
  /// is on — and a poll two seconds later would otherwise put it back.
  bool _docker = false;

  /// Whether opening this window starts the server. Held here as well as in
  /// the file so the switch answers at once rather than two seconds later,
  /// when the poll rebuilds the state.
  bool _autostart = false;

  /// What the server calls itself. Empty means it has never been named, and
  /// PocketBase's own default — "Acme" — is what it answers with.
  String _name = '';

  /// Where the database is, now. Not `widget.dir`: choosing a directory in the
  /// window has to take effect without restarting the console, and a final
  /// field cannot.
  late String _dir;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);

    _dir = widget.dir;

    final exe = serverBinary();

    // A service installed on an earlier run owns the server, and its address
    // and metrics token are on disk. Both are read back rather than starting
    // from this console's defaults: a fresh token would leave the pane
    // reporting zero devices against a server full of them, which reads
    // exactly like a server nobody has paired with.
    final token = serviceInstalled() && serviceMetricsToken().isNotEmpty
        ? serviceMetricsToken()
        : newToken();
    final installed = serviceInstalled() ? serviceBind() : '';

    _server = SyncServer(
      exe: exe,
      dir: _dir,
      token: token,
      addr: installed.isEmpty ? widget.addr : installed,
    );
    _compose = exe == null ? null : composeFile(exe, _dir);
    _docker = serviceDocker();
    _name = (readConfig(widget.configDir)['name'] as String?) ?? '';
    _autostart = readConfig(widget.configDir)['autostart'] == true;

    _state = ConsoleState(
      dir: _dir,
      addr: _server.addr,
      hosts: bindHosts(splitBind(_server.addr).$1, const []),
      linux: Platform.isLinux,
      serverBinary: exe,
      managed: serviceInstalled(),
      atLogin: serviceInstalled(),
      autostart: _autostart,
      docker: _docker,
      compose: _compose != null,
      updatable: _updatable,
    );

    // After the first frame, because a dialog needs a Navigator and there is
    // none until this widget is in a tree.
    if (!widget.chosen) {
      WidgetsBinding.instance.addPostFrameCallback((_) => _askWhereToPutIt());
    } else {
      // One question at a time, and where the library goes is the more
      // important of the two: somebody answering that should not be handed a
      // second dialog on top of the first.
      WidgetsBinding.instance.addPostFrameCallback((_) async {
        await _offerTheMenu();
        await _offerToRepoint();
      });
    }

    // Opening the window is a start, when the file says so. Off unless it
    // does, and never a stop — a window opening is no reason to take down a
    // server somebody left running. After the first frame, and after the
    // first-run questions above, so a dialog asking where the library goes is
    // not answered by a server starting on the old directory underneath it.
    if (_autostart) {
      WidgetsBinding.instance.addPostFrameCallback(
        (_) => unawaited(_startUnasked()),
      );
    }

    unawaited(_refresh());
    _poll = Timer.periodic(const Duration(seconds: 2), (_) => _refresh());
  }

  @override
  void dispose() {
    _poll?.cancel();
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }

  /// Closing the console stops the server it started.
  ///
  /// Leaving a child running with nothing supervising it means the next Start
  /// finds the port taken and no way from here to see why. A service is left
  /// alone: being left running is the entire point of having installed one.
  @override
  Future<AppExitResponse> didRequestAppExit() async {
    if (!_server.managed) await _server.stop();
    return AppExitResponse.exit;
  }

  Future<void> _refresh() async {
    final lan = await lanAddrs();
    final running = await _server.running;
    final metrics = running ? await _server.metrics() : null;
    // Who is paired and what each has sent. Null when the server is down,
    // which leaves the last list on screen rather than blanking it — the
    // devices did not stop existing because the server stopped.
    final overview = running ? await _server.overview() : null;
    final devices = overview == null
        ? null
        : [
            for (final row in overview['devices'] as List<dynamic>? ?? const [])
              PairedDevice.fromJson(row as Map<String, dynamic>),
          ];

    if (!mounted) return;
    setState(() {
      _lan = lan;
      _state = ConsoleState(
        dir: _dir,
        addr: _server.addr,
        running: running,
        managed: _server.managed,
        devices: metrics == null
            ? _state.devices
            : gauge(metrics, 'summareader_server_devices').round(),
        entries: metrics == null
            ? _state.entries
            : gauge(metrics, 'summareader_server_log_entries').round(),
        bytes: dirSize(_dir),
        paired: devices ?? _state.paired,
        hosts: bindHosts(splitBind(_server.addr).$1, lan),
        linux: Platform.isLinux,
        name: _name,
        serverBinary: _server.exe,
        atLogin: serviceInstalled(),
        autostart: _autostart,
        // The unit is the truth once there is one; before that, whatever was
        // picked here.
        docker: serviceInstalled() ? serviceDocker() : _docker,
        compose: _compose != null,
        error: _state.error,
        // Rebuilt here every two seconds, so anything left out of this list is
        // a thing that shows at launch and disappears. Check for updates did
        // exactly that, and a download's progress line would do it twice a
        // second.
        updatable: _updatable,
        updateOffer: _state.updateOffer,
        updateSaid: _state.updateSaid,
        // Carried like the other two, and for the reason above: left out, the
        // line saying the update is in place survived the tick and the button
        // that acts on it did not.
        updateInstalled: _state.updateInstalled,
      );
    });
  }

  /// Reports what went wrong in the window rather than in a box that has to be
  /// dismissed: this polls every two seconds and a modal stack of five
  /// identical errors is not a report, it is an obstacle.
  void _fail(Object error) {
    if (!mounted) return;
    setState(() => _state = _state.withError(error.toString()));
  }

  ServiceConfig _serviceConfig({String? addr, bool? docker}) => ServiceConfig(
    exe: _server.exe ?? '',
    addr: addr ?? _server.addr,
    dir: _dir,
    token: _server.token,
    compose: (docker ?? _docker) ? _compose : null,
    uid: _uid(),
    gid: _gid(),
  );

  Future<void> _toggle() async {
    try {
      if (await _server.running) {
        await _server.stop();
      } else {
        await _server.start();
      }
      if (mounted) setState(() => _state = _state.withError(null));
    } on Object catch (error) {
      _fail(error);
    }
    await _refresh();
  }

  /// The start nobody pressed.
  ///
  /// Whether it may start at all is [autostart]'s question, which is why the
  /// prerequisites and the already-running check live there rather than here:
  /// a window is the part of a program a test cannot look at.
  Future<void> _startUnasked() async {
    try {
      final refusal = await autostart(
        exe: _server.exe,
        dir: _dir,
        addr: _server.addr,
        managed: _server.managed,
        // Never a second copy. `running` knows only about a unit or this
        // console's own child, so a server another console left up is found by
        // asking the address itself.
        alreadyUp: () async => await _server.running || await _server.healthy(),
        start: _server.start,
      );
      if (refusal != null) _fail(refusal);
    } on Object catch (error) {
      _fail(error);
    }
    await _refresh();
  }

  /// Whether opening this window is by itself enough to start the server.
  ///
  /// Written and nothing else. What it governs happens at the next launch, and
  /// starting the server because somebody turned the switch on would be one
  /// control doing two things.
  void _setAutostart(bool on) {
    try {
      saveConfig(widget.configDir, {'autostart': on});
      setState(() {
        _autostart = on;
        _state = _state.withAutostart(on);
      });
    } on Object catch (error) {
      _fail(error);
    }
  }

  /// Rebinding is a restart, because a listening socket cannot be moved. The
  /// address is written to the config file, and to the unit as well when there
  /// is one — otherwise the address changes here and comes back the old one at
  /// the next login, with nothing said.
  ///
  /// Both, not one: a unit and a config that disagree are worse than either,
  /// because which of them wins depends on how the server was started that
  /// day. The unit passes --http, which beats the file, so writing the file
  /// alone would leave the service on the old address; writing the unit alone
  /// would lose the address for every launch that is not the service.
  ///
  /// The metrics token is deliberately *not* written here. The console mints
  /// one per window for its own status pane, and a per-window secret in a file
  /// on disk is a credential outliving the reason it existed — an operator who
  /// wants a scraper sets `metrics_token` themselves, and that one this
  /// console reads and never prints beside the address.
  /// Renames the server.
  ///
  /// Display only — the identity `/instance` answers with is generated and
  /// stored, so two servers may be called the same thing without a device
  /// mistaking one for the other. That was not true while the name *was* the
  /// identity and every install answered "Acme".
  ///
  /// Applied at bootstrap, so the server is restarted to pick it up — the
  /// same dance a bind address change already does, and for the same reason.
  Future<void> _rename(String next) async {
    final wanted = next.trim();
    if (wanted == _name) return;
    try {
      final wasRunning = await _server.running;
      await _server.stop();
      setState(() => _name = wanted);
      saveConfig(widget.configDir, {'name': wanted.isEmpty ? null : wanted});
      if (serviceInstalled()) {
        await installService(_serviceConfig());
      } else if (wasRunning) {
        await _server.start();
      }
    } on Object catch (error) {
      _fail(error);
    }
    await _refresh();
  }

  /// Points the server at another directory.
  ///
  /// A restart, because a database cannot be swapped under a running process.
  /// The key is written to the config file at the *default* location — where
  /// the server looks for it — and the database goes wherever the key says.
  /// The file does not move with it; that is the rule that stops a `dir`
  /// pointing anywhere from making the file that holds it unfindable.
  ///
  /// Config first, then the unit, in that order. The unit passes `--dir`,
  /// which beats the file, so writing the file alone would leave the service
  /// on the old directory and writing the unit alone would lose the choice for
  /// every launch that is not the service. `ReadWritePaths` moves with it too,
  /// or systemd's hardening refuses the new path and the server fails at the
  /// next login in a log nobody has open.
  ///
  /// **Nothing is copied and nothing is deleted.** An empty directory is a new
  /// library that no paired device knows about, which is a thing somebody may
  /// genuinely want; moving an existing one is `mv` and a decision, not a side
  /// effect of typing in a field.
  /// Asks GitHub whether there is a newer release, and takes it.
  ///
  /// Both halves report through the error line the rest of this screen uses,
  /// so a forty-megabyte download with no sign of life does not read as a
  /// window that has hung.
  /// Asks GitHub what the newest release is. Finding one does not install it.
  ///
  /// Replacing the program somebody is running is the one control on this page
  /// that changes this program, and it used to happen because they pressed
  /// "check". Now it is a sentence and two buttons.
  Future<void> _checkUpdates() async {
    setState(() {
      _state = _state.withUpdate(
        offer: null,
        said: 'Checking for a newer release…',
      );
    });
    try {
      final found = await const GitHubUpdates().newer();
      if (!mounted) return;
      setState(() {
        _state = _state.withUpdate(
          offer: found,
          said: found == null ? 'This is the newest release.' : null,
        );
      });
    } on Object catch (error) {
      if (mounted) {
        setState(() => _state = _state.withUpdate(offer: null, said: '$error'));
      }
    }
  }

  /// Fetches the accepted release and puts it where this image is.
  ///
  /// The progress line is the point: forty megabytes over a slow connection is
  /// a minute of a window that would otherwise look as though it had stopped.
  Future<void> _downloadUpdate(Release release) async {
    setState(() {
      _state = _state.withUpdate(
        offer: null,
        said: 'Downloading ${release.version}…',
      );
    });
    // Redrawn on a whole percent rather than on every chunk: a setState per
    // eight kilobytes is thousands of frames to move a number that has not
    // changed.
    var shown = -1;
    try {
      final refusal = await replaceRunningImage(
        release,
        client: widget.client,
        environment: widget.environment,
        onProgress: (received, total) {
          if (!mounted || total == null || total <= 0) return;
          final percent = (received * 100 ~/ total).clamp(0, 100);
          if (percent == shown) return;
          shown = percent;
          setState(() {
            _state = _state.withUpdate(
              offer: null,
              said: 'Downloading… $percent%',
            );
          });
        },
      );
      // The filename carries the version and the swap wrote the new program
      // into the old path, so without this last month's number sits in the
      // name of this month's program — and the menu entry names it.
      final now = refusal == null
          ? await nameForVersion(
              runningImage(widget.environment)!,
              release.version,
              environment: widget.environment,
            )
          : null;
      if (!mounted) return;
      setState(() {
        _state = _state.withUpdate(
          offer: null,
          said:
              refusal ?? '${release.version} is in place — restart to use it.',
          installed: now,
        );
      });
    } on Object catch (error) {
      if (mounted) _fail(error);
    }
  }

  /// Whether this is an AppImage: the one form that is a single file this
  /// program owns, so replacing it and registering it are things it can
  /// honestly offer to do. A tarball or a `flutter run` shows neither.
  bool get _updatable => runningImage(widget.environment) != null;

  /// Puts the console in the applications menu, or takes it out.
  ///
  /// The answer is written down either way, which is what makes the question
  /// on a first run a question asked once rather than every launch.
  Future<void> _setInMenu(bool wanted) async {
    final image = runningImage();
    if (image == null) return;
    try {
      // Moved somewhere it can stay before the entry names it. The entry
      // holds an absolute path, and until this it named wherever the file
      // happened to be when the question was answered — usually a downloads
      // folder, which people empty.
      final kept = wanted ? await keepImage(image) : image;
      final refusal = wanted ? await addToMenu(kept) : await removeFromMenu();
      saveConfig(widget.configDir, {'in_menu': wanted});
      if (!mounted) return;
      setState(() {
        if (refusal != null) _state = _state.withError(refusal);
      });
    } on Object catch (error) {
      _fail(error);
    }
    await _refresh();
  }

  /// Offers to repoint a menu entry that names somewhere else.
  ///
  /// Somebody moved the file by hand, or is running a second copy. The entry
  /// still names the old path, so the icon in their launcher starts nothing —
  /// and this is the only moment anything can notice, because the program that
  /// would have complained is the one that is not there.
  Future<void> _offerToRepoint() async {
    if (!mounted || !menuIsStale()) return;

    final image = runningImage();
    if (image == null) return;
    final named = menuTarget() ?? '';
    // Named before the question is asked, because the dialog says what the
    // button will do and deleting somebody's file is the part of that worth
    // saying out loud.
    final replacing =
        supersedes(named, '${applicationsDir()}/${image.split('/').last}')
        ? named.split('/').last
        : null;

    final wanted = await askAboutARepoint(
      context,
      named,
      image,
      replacing: replacing,
    );
    if (!mounted || !wanted) return;
    await _setInMenu(true);
    // The entry now names where the image was kept, which is the one file
    // this must never delete.
    await removeSuperseded(named, menuTarget() ?? '');
  }

  /// Offers the menu once, on a first run that is an AppImage.
  ///
  /// Only when the key is absent: a stored `false` is somebody having said no,
  /// and asking again would make "once" mean "every launch until you give in".
  Future<void> _offerTheMenu() async {
    if (!mounted || runningImage() == null) return;
    if (readConfig(widget.configDir)['in_menu'] is bool || isInMenu()) return;

    final wanted = await askAboutTheMenu(context);
    if (!mounted) return;
    if (wanted) {
      await _setInMenu(true);
    } else {
      saveConfig(widget.configDir, {'in_menu': false});
    }
  }

  /// The first-run question, asked once and then never again.
  ///
  /// Writing the key is what makes it once: `resolveDirs` reports `chosen` on
  /// the next launch and this is not called. Dismissing without answering
  /// leaves the default in place and asks again, which is right — the question
  /// is "where", and no answer is not an answer.
  Future<void> _askWhereToPutIt() async {
    if (!mounted) return;
    final wanted = await askWhereTheLibraryGoes(context, _dir);
    if (wanted == null || !mounted) return;

    // Asked before anything is written, because the answer decides what is
    // written. A directory that already holds a database is almost always the
    // one that was meant — a reinstall, or a service that has been filling it
    // — so this only comes up at all when there is something to lose.
    if (holdsALibrary(wanted)) {
      await askAboutWhatIsAlreadyThere(context, wanted);
      if (!mounted) return;
    }

    if (wanted == _dir) {
      // The default, accepted. Still written down: it is the difference
      // between "nobody has said" and "this was agreed to", and without it the
      // question comes back on every launch.
      saveConfig(widget.configDir, {'dir': wanted});
      await _refresh();
      return;
    }
    // Not asked about: the first-run question *is* the question, and a
    // confirmation behind it would be asking the same thing twice with the
    // second one sounding like a warning.
    await _relocate(wanted, confirm: false);
  }

  /// Points the server at another directory, having asked first.
  ///
  /// The question belongs here rather than to the field, because this is the
  /// one place an attempt to change the directory actually arrives — typed and
  /// submitted, or chosen however a caller chose it. A field that commits when
  /// focus leaves would otherwise raise it for somebody who clicked elsewhere,
  /// and the unchanged-path return above means it is never raised for a path
  /// that is already the one in use.
  Future<void> _relocate(String next, {bool confirm = true}) async {
    final wanted = next.trim();
    if (wanted.isEmpty || wanted == _dir) return;
    if (confirm) {
      final agreed = await askAboutChangingTheDirectory(context, _dir, wanted);
      if (!agreed || !mounted) return;
    }
    try {
      final wasRunning = await _server.running;
      await _server.stop();

      saveConfig(widget.configDir, {'dir': wanted});
      setState(() {
        _dir = wanted;
        _server = SyncServer(
          exe: _server.exe,
          dir: wanted,
          token: _server.token,
          addr: _server.addr,
        );
        _compose = _server.exe == null
            ? null
            : composeFile(_server.exe!, wanted);
      });

      if (serviceInstalled()) {
        await installService(_serviceConfig());
      } else if (wasRunning) {
        await _server.start();
      }
    } on Object catch (error) {
      _fail(error);
    }
    await _refresh();
  }

  Future<void> _rebind(String next) async {
    if (next == _server.addr) return;
    try {
      final wasRunning = await _server.running;
      await _server.stop();
      _server.addr = next;
      saveConfig(widget.configDir, {'http': next});
      if (serviceInstalled()) {
        await installService(_serviceConfig(addr: next));
      } else if (wasRunning) {
        await _server.start();
      }
    } on Object catch (error) {
      _fail(error);
    }
    await _refresh();
  }

  Future<void> _setAtLogin(bool on) async {
    try {
      if (on) {
        // The console's own child lets go first: installing enables and starts
        // the unit, and the port and the database can only have one owner.
        await _server.stop();
        await installService(_serviceConfig());
      } else {
        await uninstallService();
      }
    } on Object catch (error) {
      _fail(error);
    }
    await _refresh();
  }

  /// Switching between binary and container is the same install with a
  /// different ExecStart. Only meaningful once the switch above is on.
  Future<void> _setRunAs(bool docker) async {
    _docker = docker;
    setState(() => _state = _state.withDocker(docker));
    if (!serviceInstalled()) return;
    try {
      await installService(_serviceConfig(docker: docker));
    } on Object catch (error) {
      _fail(error);
    }
    await _refresh();
  }

  Future<void> _pair() async {
    if (_state.devices > 0) {
      // A code rather than a curl line to copy. It carries no key — the
      // server has never held one — so the dialog says which case it is for.
      final device = await _server.enrol('A new device');
      if (!mounted) return;
      if (device == null) {
        await showAddDevice(context, _state.devices, _server.addr, _lan);
        return;
      }
      await showDeviceCode(context, device, _server.addr, _lan);
      await _refresh();
      return;
    }
    try {
      final device = await runFirstDevice(_server);
      if (!mounted) return;
      await showFirstDeviceToken(context, device, _server.addr, _lan);
    } on Object catch (error) {
      _fail(error);
    }
    await _refresh();
  }

  /// Renames a device from the operator's side.
  ///
  /// The server refuses a name another device on the same library already
  /// answers to, and that refusal is the one worth showing rather than
  /// swallowing — it is the whole reason somebody is renaming.
  Future<void> _renameDevice(PairedDevice device) async {
    final controller = TextEditingController(text: device.label);
    String? problem;
    await showConsoleDialog(
      context,
      'Rename this device',
      StatefulBuilder(
        builder: (context, setInner) => Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          mainAxisSize: MainAxisSize.min,
          children: [
            Text(
              'What the other devices will see this one called.',
              style: Ar.bodyStyle(13.5, color: Ar.dim(0.7)),
            ),
            const SizedBox(height: 12),
            ArField(controller: controller, background: Ar.neutral100),
            if (problem != null) ...[
              const SizedBox(height: 10),
              Text(problem!, style: Ar.bodyStyle(13)),
            ],
            const SizedBox(height: 16),
            Row(
              mainAxisAlignment: MainAxisAlignment.end,
              children: [
                PillButton(
                  label: 'Rename',
                  onTap: () async {
                    final failed = await _server.rename(
                      device.id,
                      controller.text.trim(),
                    );
                    if (failed == null) {
                      if (context.mounted) Navigator.of(context).pop();
                      await _refresh();
                      return;
                    }
                    setInner(() => problem = failed);
                  },
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }

  /// Stops a device syncing, after saying what that does and does not do.
  Future<void> _revokeDevice(PairedDevice device) async {
    final name = device.label.isEmpty ? device.id : device.label;
    await showConsoleDialog(
      context,
      'Stop $name syncing?',
      Builder(
        builder: (dialogContext) => Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          mainAxisSize: MainAxisSize.min,
          children: [
            Text(
              'It will not be able to sync again without being paired afresh.\n\n'
              'What it already downloaded stays on it. It holds its own copy of '
              'the key, and nothing here can reach that — stopping a device is '
              'about this server, not about the device.',
              style: Ar.bodyStyle(13.5, color: Ar.dim(0.7), height: 1.55),
            ),
            const SizedBox(height: 16),
            Row(
              mainAxisAlignment: MainAxisAlignment.end,
              children: [
                PillButton(
                  label: 'Stop it',
                  onTap: () async {
                    final navigator = Navigator.of(dialogContext);
                    await _server.revoke(device.id);
                    navigator.pop();
                    await _refresh();
                  },
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }

  /// Lets a stopped device sync again. No question asked: it undoes something
  /// rather than doing something, and it takes one press to undo again.
  Future<void> _resumeDevice(PairedDevice device) async {
    await _server.resume(device.id);
    await _refresh();
  }

  /// Forgets a device. Asked first, because this one cannot be undone.
  Future<void> _removeDevice(PairedDevice device) async {
    final name = device.label.isEmpty ? device.id : device.label;
    await showConsoleDialog(
      context,
      'Forget $name?',
      Builder(
        builder: (dialogContext) => Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          mainAxisSize: MainAxisSize.min,
          children: [
            Text(
              'Its token goes with it, so it cannot be resumed — it would '
              'have to pair again from another device.\n\n'
              'Stopping keeps the row and can be undone, and is the better '
              'answer for a device somebody still has. This is for one that '
              'is gone.\n\n'
              'What it already downloaded stays on it either way. It holds '
              'its own copy of the key, and nothing here can reach that.',
              style: Ar.bodyStyle(13.5, color: Ar.dim(0.7), height: 1.55),
            ),
            const SizedBox(height: 16),
            Row(
              mainAxisAlignment: MainAxisAlignment.end,
              children: [
                PillButton(
                  label: 'Forget it',
                  onTap: () async {
                    final navigator = Navigator.of(dialogContext);
                    await _server.remove(device.id);
                    navigator.pop();
                    await _refresh();
                  },
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) => ConsoleView(
    state: _state,
    onToggle: _toggle,
    onDashboard: () => launchUrl(Uri.parse('http://${_server.addr}/_/')),
    onPair: _pair,
    onBind: (host) => _rebind('$host:${splitBind(_server.addr).$2}'),
    onPort: (port) {
      final trimmed = port.trim();
      unawaited(
        _rebind(
          '${splitBind(_server.addr).$1}:${trimmed.isEmpty ? '8099' : trimmed}',
        ),
      );
    },
    onAtLogin: _setAtLogin,
    onAutostart: _setAutostart,
    onRename: _renameDevice,
    onRevoke: _revokeDevice,
    onResume: _resumeDevice,
    onRemove: _removeDevice,
    onName: _rename,
    onDir: _relocate,
    onCheckUpdates: _checkUpdates,
    onDownloadUpdate: _downloadUpdate,
    onDismissUpdate: () =>
        setState(() => _state = _state.withUpdate(offer: null, said: null)),
    onRunAs: _setRunAs,
  );
}

/// The user and group the unit's container should run as. Not available on
/// Windows, where the container path does not exist either.
int _uid() => Platform.isLinux ? int.parse(_id('-u')) : 0;
int _gid() => Platform.isLinux ? int.parse(_id('-g')) : 0;

String _id(String flag) {
  final result = Process.runSync('id', [flag]);
  final value = (result.stdout as String).trim();
  return value.isEmpty ? '0' : value;
}
