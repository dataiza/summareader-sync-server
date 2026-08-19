import 'dart:io';

/// One address a server could bind to or a pairing code could name, and the
/// interface it sits on.
class LanAddr {
  const LanAddr(this.ip, [this.iface = '']);

  final String ip;
  final String iface;

  /// The interface name is what makes the choice obvious to somebody who has
  /// never thought about networking: 10.10.20.1 and 172.17.0.1 look equally
  /// plausible until one of them says docker0 beside it.
  @override
  String toString() => iface.isEmpty ? ip : '$ip ($iface)';

  @override
  bool operator ==(Object other) =>
      other is LanAddr && other.ip == ip && other.iface == iface;

  @override
  int get hashCode => Object.hash(ip, iface);
}

/// Host and port, forgiving about being handed one without the other — the
/// port field in the window can be empty for a keystroke.
(String host, String port) splitBind(String addr) {
  final colon = addr.lastIndexOf(':');
  if (colon < 0) return (addr, '8099');
  final host = addr.substring(0, colon).replaceAll('[', '').replaceAll(']', '');
  return (host, addr.substring(colon + 1));
}

/// Whether this is one of the address blocks reserved for private networks —
/// the ones a phone in the same house can reach and the wider internet cannot.
bool privateV4(InternetAddress address) {
  if (address.type != InternetAddressType.IPv4) return false;
  final parts = address.address.split('.').map(int.parse).toList();
  if (parts.length != 4) return false;
  return parts[0] == 10 ||
      (parts[0] == 172 && parts[1] >= 16 && parts[1] < 32) ||
      (parts[0] == 192 && parts[1] == 168);
}

/// Every private IPv4 this machine answers on, likeliest first.
///
/// A desktop with Docker or a VM manager installed has several, and only some
/// of them lead anywhere a phone can follow. The virtual ones are sorted to
/// the back rather than dropped, because somebody running this inside a
/// container may well need the one on the bridge — but nobody should be handed
/// it first.
Future<List<LanAddr>> lanAddrs() async {
  final List<LanAddr> found = [];
  try {
    final interfaces = await NetworkInterface.list(
      includeLoopback: false,
      type: InternetAddressType.IPv4,
    );
    for (final iface in interfaces) {
      for (final address in iface.addresses) {
        if (privateV4(address)) found.add(LanAddr(address.address, iface.name));
      }
    }
  } on OSError {
    // A machine with no networking at all still has a console worth opening;
    // it just has no address to offer a phone.
    return const [];
  }
  return orderLanAddrs(found);
}

/// Moves the virtual interfaces to the back, leaving the order the system gave
/// everything else — that order is the machine's own opinion about which
/// network matters, and this has no better one.
List<LanAddr> orderLanAddrs(List<LanAddr> addrs) => [
  ...addrs.where((a) => !virtualIface(a.iface)),
  ...addrs.where((a) => virtualIface(a.iface)),
];

/// Recognises the interfaces that exist for software on this machine to talk
/// to itself. Named by prefix because that is how the tools that create them
/// name them, and a phone can reach none of them.
bool virtualIface(String name) => const [
  'docker',
  'br-',
  'bridge',
  'veth',
  'virbr',
  'vboxnet',
  'vmnet',
  'tun',
  'tap',
].any(name.startsWith);

/// What the Address menu offers.
///
/// The two non-addresses first, because they are the two decisions: only this
/// machine, or every interface on it. Then every address the machine actually
/// answers on, which is the list the pairing dialog draws from — a bind chosen
/// here is usually chosen so that a phone can reach it.
List<LanAddr> bindHosts(String current, List<LanAddr> lan) {
  final hosts = [const LanAddr('127.0.0.1'), const LanAddr('0.0.0.0'), ...lan];
  if (hosts.any((h) => h.ip == current) || current.isEmpty) return hosts;
  // A hostname, or an address on an interface that is down: keep it rather
  // than silently rebinding a running server to something else.
  return [LanAddr(current), ...hosts];
}

/// What the pairing dialog offers, in the order it offers them.
///
/// An address someone chose goes first: that is a stated intention, not a
/// guess, and the rest of the list is guesswork by comparison. Everything the
/// machine holds still follows it, because the bind address and the address a
/// phone should dial are not always the same thing.
List<LanAddr> pairingHosts(String addr, List<LanAddr> lan) {
  final (host, _) = splitBind(addr);
  if (host.isEmpty) return lan;

  final ip = InternetAddress.tryParse(host);
  if (ip != null && (ip.isLoopback || _unspecified(ip))) return lan;

  return [LanAddr(host), ...lan.where((h) => h.ip != host)];
}

/// Turns the address the console serves on into one the phone scanning the
/// code can actually open.
///
/// The default is loopback, and a QR saying 127.0.0.1 works on every device
/// except the one it was drawn for. So when the server is bound to loopback or
/// to everything, this reaches for the machine's own address on the local
/// network — the same address the mDNS announcement leads clients to, and what
/// SYNC_BIND names for the container. An address someone chose explicitly is
/// left alone: they know where they put it.
///
/// Empty when there is nothing a phone could reach, which the dialog says out
/// loud. A code that silently cannot work is worse than no code.
String reachableUrl(String addr, List<LanAddr> lan) {
  final (host, port) = splitBind(addr);
  if (host.isEmpty || port.isEmpty) return '';

  final ip = InternetAddress.tryParse(host);
  if (ip == null) return 'http://$host:$port';
  if (!ip.isLoopback && !_unspecified(ip)) return 'http://$host:$port';

  if (lan.isEmpty) return '';
  return 'http://${lan.first.ip}:$port';
}

/// 0.0.0.0 and ::, which mean "every interface" and name none of them.
bool _unspecified(InternetAddress ip) =>
    ip.address == '0.0.0.0' || ip.address == '::';
