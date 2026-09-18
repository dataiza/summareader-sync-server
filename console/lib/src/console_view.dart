import 'package:flutter/material.dart';
import 'package:summareader_ui/summareader_ui.dart';

import 'addresses.dart';
import 'updates.dart';
import 'version.dart';
import 'format.dart';
import 'pairing.dart';
import 'server.dart';

/// Everything the console is drawn from: a handful of strings, a handful of
/// flags, and the device list.
///
/// A value rather than a live server, so the same layout can be rendered with
/// no server, no systemd and no display — which is how the picture in the
/// documentation is taken. A window whose screenshot can only be produced by
/// hand is a window whose screenshot is quietly out of date.
class ConsoleState {
  const ConsoleState({
    required this.dir,
    required this.addr,
    this.running = false,
    this.managed = false,
    this.devices = 0,
    this.entries = 0,
    this.bytes = 0,
    this.paired = const [],
    this.hosts = const [],
    this.atLogin = false,
    this.docker = false,
    this.compose = false,
    this.linux = true,
    this.name = '',
    this.serverBinary,
    this.error,
    this.updatable = false,
    this.updateOffer,
    this.updateSaid,
    this.updateInstalled,
  });

  /// A newer release, found and not yet accepted. Replacing the program
  /// somebody is running is not something to do because they pressed "check".
  final Release? updateOffer;

  /// Where the new version is, once it is in place. The old one is still the
  /// process on screen, so the honest end of an update is a button that
  /// starts the new one.
  final String? updateInstalled;

  /// What the check or the download is doing, or what it did. A line rather
  /// than a message that fades: a download is a minute long, and the sentence
  /// about restarting is worth still being there afterwards.
  final String? updateSaid;

  /// Whether this console can update and register itself — true only when it
  /// is running as an AppImage, which is the one form that is a single file it
  /// owns. A tarball or a `flutter run` shows neither control, because both
  /// would act on something nobody chose.
  final bool updatable;

  final String dir;
  final String addr;
  final bool running;

  /// Whether a systemd unit exists, which makes systemd the owner of the
  /// server and this window a remote control for it.
  final bool managed;

  final int devices;
  final int entries;
  final int bytes;
  final List<PairedDevice> paired;

  /// The addresses the bind chooser offers, already in the order it offers
  /// them: the two decisions, then what this machine answers on.
  final List<LanAddr> hosts;

  final bool atLogin;
  final bool docker;
  final bool compose;

  /// Whether "Start at login" exists at all. A control that cannot work is
  /// worse than an absent one: it invites the question of why it did nothing.
  final bool linux;

  /// What the server calls itself. Empty means it has never been named, and
  /// answers with PocketBase's own default.
  final String name;

  /// The server binary this console would run, or null when there is none.
  final String? serverBinary;

  /// The last thing that went wrong, shown where it happened rather than in a
  /// box that has to be dismissed before the window can be read again.
  final String? error;

  String get statusLine {
    if (serverBinary == null) return 'No server binary found';
    if (!running) return 'Stopped';
    // Who is running it is the badge beside this line, not more words in it.
    return 'Running on http://$addr';
  }

  /// The same state with a different message, or none. Two one-line copiers
  /// rather than a general copyWith: these are the only two fields anything
  /// changes without rebuilding the whole thing from a poll.
  ConsoleState withError(String? message) =>
      _copy(error: message, keepError: false);

  ConsoleState withDocker(bool value) => _copy(docker: value);

  /// The two halves of the update flow, which move together: an offer with no
  /// line under it, a line with no offer, or neither.
  ConsoleState withUpdate({
    required Release? offer,
    required String? said,
    String? installed,
  }) => _copy(
    updateOffer: offer,
    updateSaid: said,
    updateInstalled: installed,
    keepUpdate: false,
  );

  ConsoleState _copy({
    bool? docker,
    String? error,
    bool keepError = true,
    Release? updateOffer,
    String? updateSaid,
    String? updateInstalled,
    bool keepUpdate = true,
  }) => ConsoleState(
    dir: dir,
    addr: addr,
    running: running,
    managed: managed,
    devices: devices,
    entries: entries,
    bytes: bytes,
    paired: paired,
    hosts: hosts,
    atLogin: atLogin,
    docker: docker ?? this.docker,
    compose: compose,
    linux: linux,
    serverBinary: serverBinary,
    error: keepError ? this.error : error,
    // Carried, not defaulted. Left out of this list they would reset to
    // false on every withError — the section would vanish the first time
    // anything failed, which is exactly when somebody is looking at it.
    updatable: updatable,
    // Carried through every other copy, for the same reason `updatable`
    // is: left out, a download's progress line would vanish the moment
    // anything else touched the state — and the poll touches it twice a
    // second.
    updateOffer: keepUpdate ? this.updateOffer : updateOffer,
    updateSaid: keepUpdate ? this.updateSaid : updateSaid,
  );

  String get countsLine =>
      '${plural(devices, 'device')} · ${plural(entries, 'entry')} · '
      '${megabytes(bytes)}';
}

/// The console's body: Status and Devices, with everything that is set rather
/// than watched behind the menu in the top bar.
///
/// Built from [ConsoleState] and a set of callbacks, with nothing in it that
/// starts a process or reads a socket — see the class comment there.
class ConsoleView extends StatefulWidget {
  const ConsoleView({
    super.key,
    required this.state,
    this.onToggle,
    this.onDashboard,
    this.onPair,
    this.onBind,
    this.onPort,
    this.onAtLogin,
    this.onRunAs,
    this.onRename,
    this.onRevoke,
    this.onResume,
    this.onRemove,
    this.onName,
    this.onDir,
    this.onCheckUpdates,
    this.onDownloadUpdate,
    this.onDismissUpdate,
    this.onRestart,
  });

  final ConsoleState state;
  final VoidCallback? onToggle;
  final VoidCallback? onDashboard;
  final VoidCallback? onPair;
  final ValueChanged<String>? onBind;
  final ValueChanged<String>? onPort;
  final ValueChanged<bool>? onAtLogin;
  final ValueChanged<bool>? onRunAs;
  // The list was read-only: an operator could see every device and could not
  // rename or stop one, on a server they run. The dialogs live in the screen
  // because each of them talks to the server; the only thing this widget
  // remembers is which of its two pages is up.
  final ValueChanged<PairedDevice>? onRename;
  final ValueChanged<PairedDevice>? onRevoke;
  final ValueChanged<PairedDevice>? onResume;
  final ValueChanged<PairedDevice>? onRemove;

  /// Where the database goes. Editable now: it was a line of text saying "set
  /// with --dir at launch", on a window whose whole job is to be the way you
  /// do that without a launch.
  final ValueChanged<String>? onDir;

  /// Asked for by a press. Absent unless this is an AppImage — see
  /// [ConsoleState.updatable].
  final VoidCallback? onCheckUpdates;

  /// Accept the offered release, and put the offer away again.
  final ValueChanged<Release>? onDownloadUpdate;
  final VoidCallback? onDismissUpdate;

  /// Start the new version and leave. Only offered once there is one.
  final VoidCallback? onRestart;
  final ValueChanged<String>? onName;

  @override
  State<ConsoleView> createState() => _ConsoleViewState();
}

/// The one thing the console's body remembers: which of its two pages is up.
///
/// A pushed route would have been less code, but the page it pushes is built
/// from the poll that pushed it — two seconds later the switch, the address
/// list and the last error on it are a snapshot of a window that has moved on.
/// Swapping the body in place keeps Settings on the same heartbeat as
/// everything else here.
class _ConsoleViewState extends State<ConsoleView> {
  bool _settings = false;

  ConsoleState get state => widget.state;

  @override
  Widget build(BuildContext context) =>
      _settings ? _settingsPage() : _mainPage();

  /// The window: what is true right now, and who is paired with it.
  ///
  /// It was five sections deep, and the three that were neither status nor
  /// devices were mostly a form — the name, the port, the bind address, the
  /// service. Those are set once and watched from here afterwards, so they
  /// moved behind the menu and this page answers the question somebody opened
  /// the window with.
  Widget _mainPage() => _page([
    _topBar(
      'SummaReader Sync Server',
      'Your own library, on your own machine. Nothing here can read what it '
          'holds.',
      // A button, not a menu. It was a MenuAnchor holding exactly one item,
      // which is a menu that should not exist — and the app spells this same
      // control as a plain pill: label, Icons.tune, and a Close beside the
      // title to come back (lib/src/ui/shell.dart).
      trailing: PillButton(
        label: 'Configuration',
        icon: Icons.tune,
        height: 36,
        onTap: () => setState(() => _settings = true),
      ),
    ),
    _status(),
    _devices(),
  ]);

  Widget _settingsPage() => _page([
    _topBar(
      'Configuration',
      'Where the library is kept, what the server is called, where it listens, '
          'and whether it comes back after a reboot.',
      // Where Configuration was, because that is the button this one
      // replaces. It sat to the left of the title while the button that
      // opened the page sat to the right, so leaving was not where arriving
      // had been — and there is now only one slot to put it in, which is what
      // stops the two pages drifting apart again.
      trailing: PillButton(
        label: 'Close',
        icon: Icons.arrow_back,
        height: 36,
        onTap: () => setState(() => _settings = false),
      ),
    ),
    // The same failure report the Status card carries, because rebinding and
    // installing a service are both done from this page and both can fail —
    // and a message that only appears on the page this one is covering is a
    // message nobody reads.
    if (state.error case final message?)
      Padding(
        padding: const EdgeInsets.only(bottom: 22),
        child: _card([
          Text(
            message,
            style: Ar.bodyStyle(12.5, color: Ar.accent800, height: 1.5),
          ),
        ]),
      ),
    _address(),
    // Not in an AppImage. The unit's ExecStart would name the server binary
    // inside this image's mount — a path that exists only while this window
    // is open, and a different one every launch — so the switch would write a
    // service that cannot start. The headless install is the answer there,
    // and _thisProgram says so.
    if (state.linux && !state.updatable) _atLogin(),
    if (state.updatable) _thisProgram(),
  ]);

  /// The single column both pages are drawn in, at a width a paragraph is
  /// still readable at.
  Widget _page(List<Widget> children) => SingleChildScrollView(
    child: Center(
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 760),
        child: Padding(
          padding: const EdgeInsets.fromLTRB(26, 30, 26, 60),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: children,
          ),
        ),
      ),
    ),
  );

  /// The title, the line under it, and whatever the page hangs either side of
  /// them: the menu on the window, Back on Settings.
  /// The title, the sentence under it, and the one control the page carries.
  ///
  /// One slot rather than two. There was a `leading` as well, used by exactly
  /// one page to put its Close on the far side from the Configuration button
  /// that opened it; a header with two places to put one control is a header
  /// where the control ends up in both.
  Widget _topBar(String title, String blurb, {Widget? trailing}) => Padding(
    padding: const EdgeInsets.only(bottom: 26),
    child: Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Expanded(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Flexible(
                    child: Text(
                      title,
                      overflow: TextOverflow.ellipsis,
                      style: Ar.headingStyle(32, forText: title),
                    ),
                  ),
                  const SizedBox(width: 8),
                  // Small and quiet, but present, exactly as the app does it:
                  // "which one am I running" should not need a menu, and this
                  // window has no About to put it in.
                  Padding(
                    padding: const EdgeInsets.only(top: 12),
                    child: Text(
                      consoleVersion,
                      style: Ar.bodyStyle(12, color: Ar.dim(0.45)),
                    ),
                  ),
                ],
              ),
              const SizedBox(height: 6),
              Text(blurb, style: Ar.bodyStyle(13.5, color: Ar.dim(0.6))),
            ],
          ),
        ),
        if (trailing != null) ...[
          const SizedBox(width: 14),
          Padding(padding: const EdgeInsets.only(top: 6), child: trailing),
        ],
      ],
    ),
  );

  /// The top bar's menu.
  /// The program itself, as opposed to the server it supervises.
  ///
  /// Only drawn when this is an AppImage — see [ConsoleState.updatable]. A
  /// tarball has no single file to replace.
  Widget _thisProgram() => _section(
    'This program',
    'An AppImage is one file you downloaded, with no package manager behind '
        'it, so keeping itself current is something it has to do for itself.',
    _card([
      _row(
        'Without this window',
        Text('install.sh', style: Ar.bodyStyle(13, color: Ar.dim(0.75))),
        hint:
            'An AppImage carries the server inside itself, at a path that '
            'exists only while this window is open — so it cannot be a '
            'service. Keeping the server up after a reboot is the headless '
            'install: scripts/install.sh from the release, which writes the '
            'unit around a binary that stays put.',
      ),
      _row(
        'This console',
        // Wrapped, not a Row: the version and the button together are wider
        // than the right-hand half of a narrow window, and a Row there simply
        // paints past the edge of the card.
        Wrap(
          spacing: 12,
          runSpacing: 8,
          alignment: WrapAlignment.end,
          crossAxisAlignment: WrapCrossAlignment.center,
          children: [
            Text(consoleVersion, style: Ar.bodyStyle(13, color: Ar.dim(0.75))),
            PillButton(
              label: 'Check for updates',
              icon: Icons.download_outlined,
              height: 34,
              onTap: widget.onCheckUpdates,
            ),
          ],
        ),
        hint:
            'Asks GitHub for the newest release. Nothing is checked until you '
            'press it, and nothing is replaced until you say so.',
      ),
      // Found, and waiting to be told to go ahead.
      if (state.updateOffer case final offer?)
        _row(
          '${offer.version} is available',
          Wrap(
            spacing: 8,
            runSpacing: 8,
            alignment: WrapAlignment.end,
            children: [
              PillButton(
                label: 'Download',
                icon: Icons.download_outlined,
                height: 34,
                onTap: () => widget.onDownloadUpdate?.call(offer),
              ),
              PillButton(
                label: 'Cancel',
                height: 34,
                onTap: widget.onDismissUpdate,
              ),
            ],
          ),
          hint:
              'Downloading replaces this AppImage where it sits. The copy you '
              'have open keeps running; the new version starts next time.',
        ),
      // What it is doing, or what it did.
      if (state.updateSaid case final said?)
        _row(
          said,
          state.updateInstalled == null
              ? const SizedBox.shrink()
              : PillButton(
                  label: 'Restart now',
                  icon: Icons.restart_alt,
                  height: 34,
                  onTap: widget.onRestart,
                ),
        ),
    ]),
  );

  // The section shape the app uses everywhere: a heading, a line saying what
  // the group is for, and one card of rows.
  Widget _section(String title, String blurb, Widget child) => Padding(
    padding: const EdgeInsets.only(bottom: 30),
    child: Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(title, style: Ar.headingStyle(19, forText: title)),
        const SizedBox(height: 4),
        Text(blurb, style: Ar.bodyStyle(13.5, color: Ar.dim(0.6))),
        const SizedBox(height: 14),
        child,
      ],
    ),
  );

  Widget _card(List<Widget> rows) => Container(
    padding: const EdgeInsets.all(18),
    decoration: BoxDecoration(
      color: Ar.surface,
      borderRadius: BorderRadius.circular(Ar.radiusMd),
    ),
    child: Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        for (var i = 0; i < rows.length; i++) ...[
          rows[i],
          if (i != rows.length - 1) const SizedBox(height: 16),
        ],
      ],
    ),
  );

  /// A setting: what it is called on the left, what changes it on the right —
  /// or above and below, on a window too narrow for both.
  Widget _row(String label, Widget control, {String? hint}) {
    final words = Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(label, style: Ar.bodyStyle(14)),
        if (hint != null)
          Text(hint, style: Ar.bodyStyle(12, color: Ar.dim(0.6))),
      ],
    );

    return LayoutBuilder(
      builder: (context, row) => row.maxWidth < narrowWindow
          ? Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                words,
                const SizedBox(height: 8),
                Align(alignment: Alignment.centerLeft, child: control),
              ],
            )
          : Row(
              children: [
                Expanded(child: words),
                Flexible(child: control),
              ],
            ),
    );
  }

  Widget _status() {
    final missing = state.serverBinary == null;
    return _section(
      'Status',
      'Whether the server is up, where it is, and who is running it.',
      Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          _card([
            Row(
              children: [
                // A dot rather than the word: the state is read at a glance from
                // across a desk, and the address beside it is what has to be read
                // properly.
                Container(
                  width: 10,
                  height: 10,
                  margin: const EdgeInsets.only(right: 10),
                  decoration: BoxDecoration(
                    shape: BoxShape.circle,
                    color: state.running ? Ar.accent2500 : Ar.neutral400,
                  ),
                ),
                Expanded(
                  child: Text(
                    state.statusLine,
                    style: Ar.bodyStyle(15, weight: FontWeight.w600),
                  ),
                ),
                // Which of the two is running it, said out loud: with a unit
                // installed, Start and Stop drive systemctl, and somebody who does
                // not know that has no way to find out.
                if (state.managed) const Tag(label: 'systemd'),
              ],
            ),
            Text(state.countsLine, style: Ar.bodyStyle(13, color: Ar.dim(0.6))),
            if (missing)
              Text(
                'No summareader-sync beside this console or on PATH. Build one '
                'with scripts/build.sh, or point SUMMAREADER_SYNC_BIN at it.',
                style: Ar.bodyStyle(12.5, color: Ar.accent800, height: 1.5),
              ),
            if (state.error case final message?)
              Text(
                message,
                style: Ar.bodyStyle(12.5, color: Ar.accent800, height: 1.5),
              ),
          ]),
          const SizedBox(height: 14),
          // The buttons sit under the card rather than inside it, and under no
          // heading of their own. They had one — "Running it" — because "Start"
          // and "Pair a device" beneath a heading that describes a dot and two
          // counts is a heading about neither. With the settings gone from this
          // page there is no wrong heading left above them, and what they act on
          // is the card they sit under.
          Wrap(
            spacing: 10,
            runSpacing: 10,
            children: [
              PrimaryButton(
                label: state.running ? 'Stop' : 'Start',
                icon: state.running
                    ? Icons.stop_circle_outlined
                    : Icons.play_arrow_rounded,
                onTap: missing ? null : widget.onToggle,
              ),
              // PocketBase's admin interface is the real one, and it is served
              // by the server — so there is nothing to open until it is up.
              PillButton(
                label: 'Open dashboard',
                icon: Icons.open_in_new,
                height: 40,
                onTap: state.running ? widget.onDashboard : null,
              ),
              PillButton(
                label: pairButtonText(state.devices),
                icon: Icons.qr_code_2,
                height: 40,
                onTap: widget.onPair,
              ),
            ],
          ),
        ],
      ),
    );
  }

  /// Every device is listed, revoked ones included: a device that has been
  /// stopped is something whoever stopped it should still be able to see. So
  /// is one that has never sent anything — that device is either new or not
  /// getting through, and leaving it out hides both.
  Widget _devices() => _section(
    'Devices',
    'Who is paired with this server, and when each was last heard from.',
    _card(
      state.paired.isEmpty
          ? [
              Text(
                'No devices paired yet.',
                style: Ar.bodyStyle(13.5, color: Ar.dim(0.6)),
              ),
            ]
          : [for (final device in state.paired) _deviceRow(device)],
    ),
  );

  Widget _deviceRow(PairedDevice device) {
    // A device whose token was minted without a label. Its id is not a name,
    // but it is what distinguishes it from the others.
    final name = device.label.isEmpty ? device.id : device.label;
    return Row(
      children: [
        KindBadge(
          icon: device.revoked ? Icons.block : Icons.phone_iphone_outlined,
          background: device.revoked ? Ar.neutral300 : Ar.accent2200,
          foreground: device.revoked ? Ar.dim(0.5) : Ar.accent2800,
        ),
        const SizedBox(width: 12),
        Expanded(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(name, style: Ar.bodyStyle(14, weight: FontWeight.w600)),
              Text(
                '${ago(device.lastSeen)} · '
                '${plural(device.entries, 'entry')} sent',
                style: Ar.bodyStyle(12.5, color: Ar.dim(0.6)),
              ),
            ],
          ),
        ),
        // A stopped device keeps its row and its name. Resume is the other
        // half of Stop, which had none: stopping was one-way in the window
        // though never in the data.
        if (device.revoked) ...[
          Tag(
            label: 'stopped',
            background: Ar.neutral300,
            foreground: Ar.dim(0.7),
          ),
          const SizedBox(width: 8),
          PillButton(
            label: 'Resume',
            height: 32,
            onTap: widget.onResume == null
                ? null
                : () => widget.onResume!(device),
          ),
        ] else ...[
          PillButton(
            label: 'Rename',
            height: 32,
            onTap: widget.onRename == null
                ? null
                : () => widget.onRename!(device),
          ),
          const SizedBox(width: 8),
          PillButton(
            label: 'Stop',
            height: 32,
            onTap: widget.onRevoke == null
                ? null
                : () => widget.onRevoke!(device),
          ),
        ],
        const SizedBox(width: 8),
        // Available whichever state it is in: a device worth forgetting is
        // usually one that was stopped first.
        PillButton(
          label: 'Remove',
          height: 32,
          onTap: widget.onRemove == null
              ? null
              : () => widget.onRemove!(device),
        ),
      ],
    );
  }

  Widget _address() {
    final (host, port) = splitBind(state.addr);
    return _section(
      'How it is reached',
      'What this server is called and where it listens. Changing any of them '
          'restarts it — and rewrites the unit when there is one.',
      _card([
        _row(
          'Name',
          SizedBox(
            width: 220,
            child: _NameField(name: state.name, onSubmitted: widget.onName),
          ),
        ),
        // Said here because the obvious reading of a name field is the wrong
        // one: this is a label, and two servers sharing it is fine.
        Text(
          'A label for the dashboard and the network advertisement. Devices '
          'tell servers apart by an identity generated when the database was '
          'made, not by this — so two servers may share a name safely.',
          style: Ar.bodyStyle(12.5, color: Ar.dim(0.6), height: 1.5),
        ),
        _row(
          'Bind address',
          Wrap(
            spacing: 6,
            runSpacing: 6,
            alignment: WrapAlignment.end,
            children: [
              for (final candidate in state.hosts)
                Segment(
                  label: candidate.toString(),
                  selected: candidate.ip == host,
                  onTap: widget.onBind == null
                      ? null
                      : () => widget.onBind!(candidate.ip),
                ),
            ],
          ),
          hint:
              'Loopback and 0.0.0.0 are the two decisions; the rest are '
              'addresses this machine answers on.',
        ),
        _row(
          'Port',
          SizedBox(
            width: 110,
            child: _PortField(port: port, onSubmitted: widget.onPort),
          ),
        ),
        _row(
          'Data directory',
          SizedBox(
            width: 320,
            child: _DirField(dir: state.dir, onSubmitted: widget.onDir),
          ),
          // What is actually at stake, said where the decision is made. The
          // account and every device token are in this directory; pointing
          // the server at an empty one is not a migration, it is a new
          // library that no paired device knows about.
          //
          // The container caveat is real and not worth a mechanism: the unit
          // this window writes for `docker compose` passes the bind address
          // and the port and has never passed a directory, because the image
          // serves /data and the compose file decides what that is.
          hint:
              'Restarts the server. The account and every device token live '
              'here — a different directory is a different library, not a '
              'move. Ignored when the service runs docker compose.',
        ),
      ]),
    );
  }

  Widget _atLogin() => _section(
    'Start at login',
    'A systemd user service, so the server comes back after a reboot without '
        'this window.',
    _card([
      _row(
        'Keep it running',
        ArSwitch(
          value: state.atLogin,
          label: 'Start at login',
          onChanged: widget.onAtLogin,
        ),
        hint: 'Writes ~/.config/systemd/user/summareader-sync.service.',
      ),
      if (state.compose) ...[
        RadioRow(
          label: 'This binary',
          hint: 'The unit runs summareader-sync serve directly.',
          selected: !state.docker,
          onTap: widget.onRunAs == null ? null : () => widget.onRunAs!(false),
        ),
        RadioRow(
          label: 'docker compose',
          hint:
              'The unit brings the container up instead, with the bind '
              'address passed in as SYNC_BIND and SYNC_PORT.',
          selected: state.docker,
          onTap: widget.onRunAs == null ? null : () => widget.onRunAs!(true),
        ),
      ] else
        Text(
          'No docker-compose.yml beside this binary, so only the binary can '
          'be run as a service.',
          style: Ar.bodyStyle(12.5, color: Ar.dim(0.6), height: 1.5),
        ),
    ]),
  );
}

/// The port, in a field that keeps its own text.
///
/// Its own widget because a controller rebuilt on every poll loses the caret
/// twice a second, which is a field nobody can type four digits into.
/// The same shape as [_PortField], and for the same reason: a text field
/// needs a controller, and this view is stateless because everything else in
/// it is drawn from what the screen already knows.
class _NameField extends StatefulWidget {
  const _NameField({required this.name, this.onSubmitted});

  final String name;
  final ValueChanged<String>? onSubmitted;

  @override
  State<_NameField> createState() => _NameFieldState();
}

class _NameFieldState extends State<_NameField> {
  late final _controller = TextEditingController(text: widget.name);

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => ArField(
    controller: _controller,
    // The default it falls back to, shown rather than described.
    hint: 'Acme',
    background: Ar.neutral100,
    onSubmitted: widget.onSubmitted,
  );
}

/// The data directory, in a field that keeps its own text.
///
/// The same shape as [_NameField] and [_PortField], and for the same reason:
/// the poll rebuilds this tree twice a second and a bare controller loses the
/// caret every time.
///
/// No folder browser, deliberately — neither this console nor the app has a
/// picker dependency, and every other path in both is typed.
class _DirField extends StatefulWidget {
  const _DirField({required this.dir, this.onSubmitted});

  final String dir;
  final ValueChanged<String>? onSubmitted;

  @override
  State<_DirField> createState() => _DirFieldState();
}

class _DirFieldState extends State<_DirField> {
  late final _controller = TextEditingController(text: widget.dir);

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => ArField(
    controller: _controller,
    // No hint. [_NameField] shows the default it falls back to, which is
    // useful because a name can be left empty; a directory cannot, so the
    // field always holds one and a placeholder repeating it would be the same
    // string twice. The row's own hint says what changing it costs.
    fontSize: 12.5,
    background: Ar.neutral100,
    onSubmitted: widget.onSubmitted,
  );
}

class _PortField extends StatefulWidget {
  const _PortField({required this.port, this.onSubmitted});

  final String port;
  final ValueChanged<String>? onSubmitted;

  @override
  State<_PortField> createState() => _PortFieldState();
}

class _PortFieldState extends State<_PortField> {
  late final _controller = TextEditingController(text: widget.port);

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => ArField(
    controller: _controller,
    background: Ar.neutral100,
    onSubmitted: widget.onSubmitted,
  );
}
