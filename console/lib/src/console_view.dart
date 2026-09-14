import 'package:flutter/material.dart';
import 'package:summareader_ui/summareader_ui.dart';

import 'addresses.dart';
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
  });

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

  ConsoleState _copy({bool? docker, String? error, bool keepError = true}) =>
      ConsoleState(
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
      );

  String get countsLine =>
      '${plural(devices, 'device')} · ${plural(entries, 'entry')} · '
      '${megabytes(bytes)}';
}

/// The console's body.
///
/// Built from [ConsoleState] and a set of callbacks, with nothing in it that
/// starts a process or reads a socket — see the class comment there.
class ConsoleView extends StatelessWidget {
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
  // because this widget has no state to hold a confirmation in.
  final ValueChanged<PairedDevice>? onRename;
  final ValueChanged<PairedDevice>? onRevoke;
  final ValueChanged<PairedDevice>? onResume;
  final ValueChanged<PairedDevice>? onRemove;
  final ValueChanged<String>? onName;

  @override
  Widget build(BuildContext context) {
    const title = 'SummaReader Sync Server';
    return SingleChildScrollView(
      child: Center(
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 760),
          child: Padding(
            padding: const EdgeInsets.fromLTRB(26, 30, 26, 60),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(title, style: Ar.headingStyle(32, forText: title)),
                const SizedBox(height: 6),
                Text(
                  'Your own library, on your own machine. Nothing here can '
                  'read what it holds.',
                  style: Ar.bodyStyle(13.5, color: Ar.dim(0.6)),
                ),
                const SizedBox(height: 26),
                // Four sections by subject rather than one card of
                // everything: what is true right now, what you press to
                // change it, who is paired with it, and how it is reached.
                // Start, the dashboard and pairing were all inside "Status",
                // which is a heading about none of them.
                _status(context),
                _running(),
                _devices(),
                _address(),
                if (state.linux) _atLogin(),
              ],
            ),
          ),
        ),
      ),
    );
  }

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

  Widget _status(BuildContext context) {
    final missing = state.serverBinary == null;
    return _section(
      'Status',
      'Whether the server is up, where it is, and who is running it.',
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
    );
  }

  /// The controls, which are not the status.
  ///
  /// They shared a card with the running dot and the counts, under a heading
  /// that describes only those — so "Start" and "Pair a device" lived under
  /// "Status", which is a word about neither.
  Widget _running() {
    final missing = state.serverBinary == null;
    return _section(
      'Running it',
      'Starting and stopping, the dashboard, and letting a device in.',
      _card([
        Wrap(
          spacing: 10,
          runSpacing: 10,
          children: [
            PrimaryButton(
              label: state.running ? 'Stop' : 'Start',
              icon: state.running
                  ? Icons.stop_circle_outlined
                  : Icons.play_arrow_rounded,
              onTap: missing ? null : onToggle,
            ),
            // PocketBase's admin interface is the real one, and it is served
            // by the server — so there is nothing to open until it is up.
            PillButton(
              label: 'Open dashboard',
              icon: Icons.open_in_new,
              height: 40,
              onTap: state.running ? onDashboard : null,
            ),
            PillButton(
              label: pairButtonText(state.devices),
              icon: Icons.qr_code_2,
              height: 40,
              onTap: onPair,
            ),
          ],
        ),
      ]),
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
            onTap: onResume == null ? null : () => onResume!(device),
          ),
        ] else ...[
          PillButton(
            label: 'Rename',
            height: 32,
            onTap: onRename == null ? null : () => onRename!(device),
          ),
          const SizedBox(width: 8),
          PillButton(
            label: 'Stop',
            height: 32,
            onTap: onRevoke == null ? null : () => onRevoke!(device),
          ),
        ],
        const SizedBox(width: 8),
        // Available whichever state it is in: a device worth forgetting is
        // usually one that was stopped first.
        PillButton(
          label: 'Remove',
          height: 32,
          onTap: onRemove == null ? null : () => onRemove!(device),
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
            child: _NameField(name: state.name, onSubmitted: onName),
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
                  onTap: onBind == null ? null : () => onBind!(candidate.ip),
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
            child: _PortField(port: port, onSubmitted: onPort),
          ),
        ),
        _row(
          'Data directory',
          Text(
            state.dir,
            textAlign: TextAlign.end,
            style: Ar.bodyStyle(12.5, color: Ar.dim(0.6)),
          ),
          hint: 'Set with --dir at launch; not editable here.',
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
          onChanged: onAtLogin,
        ),
        hint: 'Writes ~/.config/systemd/user/summareader-sync.service.',
      ),
      if (state.compose) ...[
        RadioRow(
          label: 'This binary',
          hint: 'The unit runs summareader-sync serve directly.',
          selected: !state.docker,
          onTap: onRunAs == null ? null : () => onRunAs!(false),
        ),
        RadioRow(
          label: 'docker compose',
          hint:
              'The unit brings the container up instead, with the bind '
              'address passed in as SYNC_BIND and SYNC_PORT.',
          selected: state.docker,
          onTap: onRunAs == null ? null : () => onRunAs!(true),
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
