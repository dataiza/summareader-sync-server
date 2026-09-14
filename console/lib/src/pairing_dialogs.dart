import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:qr_flutter/qr_flutter.dart';
import 'package:summareader_ui/summareader_ui.dart';

import 'addresses.dart';
import 'format.dart';
import 'pairing.dart';
import 'server.dart';

/// The shell both pairing dialogs sit in, so they read as pages of this
/// console rather than as whatever the platform's dialog looks like.
Future<void> showConsoleDialog(
  BuildContext context,
  String title,
  Widget body,
) => showDialog<void>(
  context: context,
  builder: (context) => Dialog(
    backgroundColor: Ar.bg,
    shape: RoundedRectangleBorder(
      borderRadius: BorderRadius.circular(Ar.radiusLg),
    ),
    child: ConstrainedBox(
      constraints: const BoxConstraints(maxWidth: 560),
      child: SingleChildScrollView(
        padding: const EdgeInsets.fromLTRB(26, 26, 26, 20),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          mainAxisSize: MainAxisSize.min,
          children: [
            Text(title, style: Ar.headingStyle(22, forText: title)),
            const SizedBox(height: 16),
            body,
            const SizedBox(height: 20),
            Align(
              alignment: Alignment.centerRight,
              child: PrimaryButton(
                label: 'Done',
                onTap: () => Navigator.of(context).pop(),
              ),
            ),
          ],
        ),
      ),
    ),
  ),
);

/// Puts the token somewhere it can be selected and copied, and beside it the
/// same token as a QR code.
///
/// It is shown once and is not recoverable, which is the whole reason this
/// exists: printing it to a terminal nobody has open helps nobody. Two ways
/// into one library — a desktop copies the text, a phone scans the code rather
/// than typing forty-three characters off another screen.
Future<void> showFirstDeviceToken(
  BuildContext context,
  FirstDevice device,
  String addr,
  List<LanAddr> lan,
) {
  final (_, port) = splitBind(addr);
  final hosts = pairingHosts(addr, lan);
  return showConsoleDialog(
    context,
    'Paste this into SummaReader',
    _TokenBody(device: device, hosts: hosts, port: port),
  );
}

class _TokenBody extends StatefulWidget {
  const _TokenBody({
    required this.device,
    required this.hosts,
    required this.port,
  });

  final FirstDevice device;
  final List<LanAddr> hosts;
  final String port;

  @override
  State<_TokenBody> createState() => _TokenBodyState();
}

class _TokenBodyState extends State<_TokenBody> {
  // Which address the code names is a question only the person in front of the
  // screen can answer — the machine cannot tell which network the phone is on.
  // So the code is redrawn on every change of the choice rather than drawn
  // once from a guess.
  late var _chosen = widget.hosts.isEmpty ? null : widget.hosts.first;

  @override
  Widget build(BuildContext context) {
    final chosen = _chosen;
    final where = chosen == null
        ? ''
        : reachableUrl('${chosen.ip}:${widget.port}', widget.hosts);
    final payload = where.isEmpty
        ? null
        : pairingPayload(where, widget.device.token);

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            if (payload != null) ...[
              Container(
                padding: const EdgeInsets.all(10),
                decoration: BoxDecoration(
                  color: Ar.neutral100,
                  borderRadius: BorderRadius.circular(Ar.radiusSm),
                ),
                child: QrImageView(
                  data: payload,
                  size: 180,
                  // Drawn on the light neutral rather than on the paper
                  // ground: a camera reads a code faster off the highest
                  // contrast it can get, and the ground here is not white.
                  backgroundColor: Ar.neutral100,
                  eyeStyle: QrEyeStyle(
                    eyeShape: QrEyeShape.square,
                    color: Ar.neutral900,
                  ),
                  dataModuleStyle: QrDataModuleStyle(
                    dataModuleShape: QrDataModuleShape.square,
                    color: Ar.neutral900,
                  ),
                ),
              ),
              const SizedBox(width: 18),
            ],
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Eyebrow('Account ${widget.device.accountId}'),
                  const SizedBox(height: 8),
                  SelectableText(
                    widget.device.token,
                    style: Ar.bodyStyle(13, height: 1.5),
                  ),
                  const SizedBox(height: 10),
                  PillButton(
                    label: 'Copy token',
                    icon: Icons.copy_all_outlined,
                    onTap: () => Clipboard.setData(
                      ClipboardData(text: widget.device.token),
                    ),
                  ),
                ],
              ),
            ),
          ],
        ),
        const SizedBox(height: 16),
        if (widget.hosts.length > 1) ...[
          Text(
            'Which address the code names:',
            style: Ar.bodyStyle(12.5, color: Ar.dim(0.6)),
          ),
          const SizedBox(height: 8),
          Wrap(
            spacing: 6,
            runSpacing: 6,
            children: [
              for (final host in widget.hosts)
                Segment(
                  label: host.toString(),
                  selected: host == chosen,
                  onTap: () => setState(() => _chosen = host),
                ),
            ],
          ),
          const SizedBox(height: 14),
        ],
        Text(
          payload == null
              ? 'Shown once. No address on this network to put in a code — '
                    'the server is on loopback, so type the token in by hand.'
              : 'Scan on a phone, or copy the token. Shown once, for $where.',
          style: Ar.bodyStyle(12.5, color: Ar.dim(0.7), height: 1.5),
        ),
        const SizedBox(height: 10),
        Text(
          'This code carries an address and a token, and no key. Anyone who '
          'scans it can append to this library and read what is already '
          'there, so it is worth what a password is worth.',
          style: Ar.bodyStyle(12.5, color: Ar.accent800, height: 1.5),
        ),
      ],
    );
  }
}

/// Answers the question this button used to answer wrongly.
///
/// Pressing it a second time used to mint a second library — a new account, a
/// new token, and no relation to the one the devices are already sharing. It
/// looked like it worked and it synced nothing. What actually adds a device is
/// a pairing code from an app, because that code carries the master key and
/// this server has never held one.
Future<void> showAddDevice(
  BuildContext context,
  int devices,
  String addr,
  List<LanAddr> lan,
) {
  final where = reachableUrl(addr, lan).isEmpty
      ? 'http://$addr'
      : reachableUrl(addr, lan);
  final command =
      'curl -X POST $where/enroll \\\n'
      "  -H 'Authorization: Bearer <a token this account already has>' \\\n"
      '  -d \'{"label":"Phone"}\'';

  return showConsoleDialog(
    context,
    'Add a device',
    Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          'This server already holds a library — ${plural(devices, 'device')}.'
          '\n\nA new device joins it by scanning a pairing code in '
          'SummaReader, on a device that is already set up. That code carries '
          'the master key, and the key is what makes the library readable. '
          'This server has never held it and is not supposed to, so there is '
          'nothing here that can replace it.\n\nThe app also asks the server '
          'for the new device\'s own token while it does that. By hand, that '
          'request is:',
          style: Ar.bodyStyle(13, height: 1.6),
        ),
        const SizedBox(height: 14),
        Container(
          width: double.infinity,
          padding: const EdgeInsets.all(14),
          decoration: BoxDecoration(
            color: Ar.neutral100,
            borderRadius: BorderRadius.circular(Ar.radiusSm),
          ),
          child: SelectableText(command, style: Ar.bodyStyle(12, height: 1.6)),
        ),
        const SizedBox(height: 10),
        PillButton(
          label: 'Copy',
          icon: Icons.copy_all_outlined,
          onTap: () => Clipboard.setData(ClipboardData(text: command)),
        ),
        const SizedBox(height: 14),
        Text(
          'Creating a first device again would make a second, separate '
          'library — which is why this button is no longer offering to.',
          style: Ar.bodyStyle(12.5, color: Ar.dim(0.7), height: 1.5),
        ),
      ],
    ),
  );
}
