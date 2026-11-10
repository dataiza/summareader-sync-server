/// Keeping the console up to date, which nothing else does.
///
/// A build sold as one file has no package manager behind it: whoever
/// downloaded it is on the version they downloaded until they think to look
/// again, and nobody thinks to look again. The server's routes move and the window
/// that supervises it drifts out of step, which is reported as the server
/// being broken.
///
/// So the console asks GitHub what the newest release is and replaces itself.
/// That is only possible because an AppImage *is* one file: there is nothing to
/// unpack, no directory to merge, and the thing to replace is a path the
/// runtime hands us.
library;

import 'dart:convert';
import 'dart:io';

import 'package:crypto/crypto.dart' show sha256;
import 'package:cryptography/cryptography.dart'
    show Ed25519, KeyPairType, Signature, SimplePublicKey;
import 'package:http/http.dart' as http;

import 'version.dart';

/// The public half of the key every release's `SHA256SUMS` is signed with,
/// as SPKI DER in base64; the raw Ed25519 key is its last 32 bytes. Card 719.
///
/// The private half is the secret `UPDATE_SIGNING_KEY` in the
/// summareader-sync-server repository, with an offline copy kept by its owner.
/// Losing it means every installed console refuses every later update: a new
/// public key can only arrive inside a release, and that release would be
/// refused for not matching the old one. Everybody would then have to
/// download a new console by hand.
const updateSigningKey =
    'MCowBQYDK2VwAyEAuQYebGUsRGmKBIvLbiw435axtf3NHmT1zhDO+ME+AQU=';

/// [updateSigningKey] as the 32 bytes Ed25519 wants.
final List<int> updatePublicKey = base64.decode(updateSigningKey).sublist(12);

/// Where the running AppImage is, or null when this is not one.
///
/// `$APPIMAGE` is set by the AppImage runtime to the image's own path. Absent
/// means a tarball, a `flutter run`, or a build from source — all of which
/// update by whatever put them there, and none of which should be offered a
/// button that would rename a file over something nobody chose. **Do not guess
/// this from `Platform.resolvedExecutable`**: inside an image that points into
/// the mount, which is read-only and disappears when the process ends.
String? runningImage([Map<String, String>? environment]) {
  final named = (environment ?? Platform.environment)['APPIMAGE'];
  return named != null && named.isNotEmpty ? named : null;
}

/// A release, reduced to the things worth acting on: its version, its image,
/// and the signed manifest that vouches for the image (card 719). The manifest
/// and its signature are null for a release made before releases were signed,
/// which [replaceRunningImage] then refuses.
typedef Release = ({String version, Uri image, Uri? sums, Uri? signature});

/// Asks what the newest release is. An interface so a test can answer.
abstract class Updates {
  /// The newest release, or null when this one is already it.
  ///
  /// Throws nothing a caller has to catch beyond the network's own failures —
  /// see [GitHubUpdates.newest] for what is treated as "no answer".
  Future<Release?> newer();
}

/// The real one, against the public releases API.
class GitHubUpdates implements Updates {
  const GitHubUpdates({
    this.repository = 'dataiza/summareader-sync-server',
    this.client,
  });

  final String repository;
  final http.Client? client;

  @override
  Future<Release?> newer() async {
    final web = client ?? http.Client();
    final answer = await web.get(
      Uri.https('api.github.com', '/repos/$repository/releases/latest'),
      // Unauthenticated, which is sixty requests an hour per address. This is
      // pressed by a person, so that is not a budget anybody reaches — and a
      // token in a desktop app is a token in everybody's hands.
      headers: const {'Accept': 'application/vnd.github+json'},
    );
    if (answer.statusCode != 200) {
      throw HttpException('GitHub answered ${answer.statusCode}');
    }

    final json = jsonDecode(answer.body);
    if (json is! Map) throw const FormatException('not a release');
    final tag = json['tag_name'];
    if (tag is! String) throw const FormatException('a release with no tag');

    final offered = tag.startsWith('v') ? tag.substring(1) : tag;
    if (!isNewer(offered, consoleVersion)) return null;

    // The asset that is this program. A release also carries the headless
    // bundles and the source archives GitHub attaches itself, and replacing
    // this file with a tarball would be worse than doing nothing.
    final assets = json['assets'];
    if (assets is! List) throw const FormatException('a release with no files');
    final urls = <String, Uri>{};
    for (final asset in assets) {
      if (asset is! Map) continue;
      final name = asset['name'];
      final url = asset['browser_download_url'];
      if (name is String && url is String) urls[name] = Uri.parse(url);
    }
    final image = [
      for (final MapEntry(:key, :value) in urls.entries)
        if (key.endsWith('.AppImage')) value,
    ];
    if (image.isEmpty) {
      throw const FormatException('that release has no AppImage in it');
    }
    return (
      version: offered,
      image: image.first,
      sums: urls['SHA256SUMS'],
      signature: urls['SHA256SUMS.sig'],
    );
  }
}

/// Starts the image again and leaves.
///
/// The new version is on disk and the old one is the process you are looking
/// at — the kernel is holding it open by its inode, which is precisely why
/// replacing it was safe. Nothing changes until it is started again, so the
/// honest end of an update is a button that does it.
///
/// Detached, so the new process is not a child that dies with this one, and
/// then this process exits. What is not done here is anything clever about
/// saving: this console writes as it goes, so there is no document to flush.
///
/// Not started directly, though (card 1007). The console is unique on Linux,
/// so a new process that finds this one still holding the application id
/// hands itself over to it and ends, and then this one ends too, leaving
/// nothing running. A detached shell waits for this process id to disappear
/// and only then runs the image; see [startAfterExit].
///
/// [args] are passed to the image, which is how a `--install` given to a
/// copy that was switched to survives the switch.
///
/// Returns a sentence when it could not start — no shell, a permission — and
/// never returns at all when it could. A moved or missing image is found by
/// the shell after this process has gone, and is not reported.
Future<String?> restartInto(
  String image, [
  List<String> args = const [],
]) async {
  try {
    await Process.start(
      '/bin/sh',
      startAfterExit(pid, image, args),
      mode: ProcessStartMode.detached,
    );
  } on ProcessException catch (error) {
    return 'Could not start $image (${error.message}). Start it yourself.';
  }
  exit(0);
}

/// The arguments for `/bin/sh` that start [image] once process [waitFor] has
/// gone.
///
/// The wait is bounded at thirty seconds, because a parent that never reaps
/// the process would keep its id visible for ever, and the half second after
/// it covers the session bus noticing that the name is free. The same script
/// as the app's (card 900).
List<String> startAfterExit(
  int waitFor,
  String image, [
  List<String> args = const [],
]) => [
  '-c',
  r'i=0; while kill -0 "$1" 2>/dev/null && [ $i -lt 300 ]; do '
      r'sleep 0.1; i=$((i+1)); done; sleep 0.5; shift; exec "$@"',
  'sh',
  '$waitFor',
  image,
  ...args,
];

/// Whether [offered] is a later version than [mine].
///
/// By components and not by string order: `0.10.0` is later than `0.9.0` and
/// sorts before it as text, which is the bug this exists to not have. Anything
/// unparseable compares as not newer — a release named something surprising
/// should be ignored rather than offered.
bool isNewer(String offered, String mine) {
  List<int>? parts(String raw) {
    final numbers = <int>[];
    for (final piece in raw.split('-').first.split('.')) {
      final value = int.tryParse(piece);
      if (value == null) return null;
      numbers.add(value);
    }
    return numbers.isEmpty ? null : numbers;
  }

  final theirs = parts(offered), ours = parts(mine);
  if (theirs == null || ours == null) return false;
  for (var i = 0; i < theirs.length || i < ours.length; i++) {
    final a = i < theirs.length ? theirs[i] : 0;
    final b = i < ours.length ? ours[i] : 0;
    if (a != b) return a > b;
  }
  return false;
}

/// Downloads [release] and puts it where the running image is.
///
/// Returns null when it worked, and a sentence when it did not — the same
/// shape as `libraryRefusal`, because these are ordinary conditions rather
/// than exceptions: an image somebody put in /opt, a machine with no network,
/// a disk with no room.
///
/// **Nothing is installed that the release's signed manifest does not vouch
/// for** (card 719). The manifest is checked against [publicKey], which is
/// [updatePublicKey] everywhere but a test, before the image is fetched; the
/// image is then checked against its line in the manifest before it is made
/// executable. A missing manifest is refused as firmly as a wrong one, because
/// a hostile release would simply leave it out.
///
/// **Replacing the file a running program was started from is safe, and looks
/// as though it should not be.** The kernel holds the old inode open for as
/// long as this process lives, so the running console carries on out of a file
/// that no longer has a name. The rename is atomic, so there is no moment at
/// which the path holds half an image. The new version is what starts next
/// time.
Future<String?> replaceRunningImage(
  Release release, {
  http.Client? client,
  String? imagePath,
  Map<String, String>? environment,
  void Function(int received, int? total)? onProgress,
  List<int>? publicKey,
}) async {
  final path = imagePath ?? runningImage(environment);
  if (path == null) {
    return 'This is not running as an AppImage, so there is nothing to '
        'replace. Nothing has been changed.';
  }

  final sums = release.sums, signature = release.signature;
  if (sums == null || signature == null) {
    return 'That release carries no signed checksum list, so there is no way '
        'to tell it came from the people who make this console. Nothing has '
        'been changed.';
  }
  for (final url in [release.image, sums, signature]) {
    if (!fromGitHub(url)) return _offSite(url);
  }

  final image = File(path);
  final beside = image.parent;
  // Beside the image rather than in /tmp, because the rename has to be on one
  // filesystem to be atomic — across a mount point it is a copy, and a copy
  // interrupted leaves a broken program where a working one was.
  final partial = File('${image.path}.new');

  try {
    final web = client ?? http.Client();

    // The manifest first: it is a few hundred bytes, and a release that fails
    // here has not cost anybody fifty megabytes.
    final manifest = await web.get(sums);
    final signed = await web.get(signature);
    for (final answer in [manifest, signed]) {
      if (answer.statusCode != 200) {
        return 'The checksum list answered ${answer.statusCode}. Nothing has '
            'been changed.';
      }
      if (answer case http.BaseResponseWithUrl(:final url)
          when !fromGitHub(url)) {
        return _offSite(url);
      }
    }
    final genuine = await Ed25519().verify(
      manifest.bodyBytes,
      signature: Signature(
        signed.bodyBytes,
        publicKey: SimplePublicKey(
          publicKey ?? updatePublicKey,
          type: KeyPairType.ed25519,
        ),
      ),
    );
    if (!genuine) {
      return 'The checksum list is not signed by the key this console trusts. '
          'Nothing has been changed.';
    }
    final expected = expectedSum(
      utf8.decode(manifest.bodyBytes, allowMalformed: true),
      release.image.pathSegments.last,
    );
    if (expected == null) {
      return 'The signed checksum list does not name '
          '${release.image.pathSegments.last}. Nothing has been changed.';
    }

    // Streamed rather than fetched whole, for two reasons: fifty megabytes of
    // image does not need to be in memory at once, and somebody watching a
    // progress line is the difference between "this is working" and "this has
    // hung". [onProgress] is called with what has arrived and the total when
    // the server gave a length, which it usually does.
    final answer = await web.send(http.Request('GET', release.image));
    if (answer.statusCode != 200) {
      return 'The download answered ${answer.statusCode}. Nothing has been '
          'changed.';
    }
    // GitHub sends the asset from another host, and the client follows that
    // on its own. Where it ended up is checked as well as where it started.
    if (answer case http.BaseResponseWithUrl(:final url)
        when !fromGitHub(url)) {
      return _offSite(url);
    }

    final sink = partial.openWrite();
    var received = 0;
    try {
      await for (final chunk in answer.stream) {
        sink.add(chunk);
        received += chunk.length;
        onProgress?.call(received, answer.contentLength);
      }
    } finally {
      await sink.close();
    }
    if (received == 0) {
      if (partial.existsSync()) partial.deleteSync();
      return 'The download was empty. Nothing has been changed.';
    }
    // A connection that closed early ends the stream as quietly as one that
    // finished. Card 1028.
    final total = answer.contentLength;
    if (total != null && received != total) {
      partial.deleteSync();
      return 'The download stopped at $received of $total bytes. Nothing has '
          'been changed.';
    }
    final actual = (await sha256.bind(partial.openRead()).first).toString();
    if (actual != expected) {
      partial.deleteSync();
      return 'The download does not match its signed checksum. Nothing has '
          'been changed.';
    }

    await Process.run('chmod', ['755', partial.path]);
    // Last, and only now: until this rename the old image is untouched, so
    // an interrupted download costs a file beside it and nothing else.
    await partial.rename(image.path);
    return null;
  } on FileSystemException catch (error) {
    // The common one by a distance: an image in /opt or /usr/local, owned by
    // root, opened by somebody who is not.
    if (partial.existsSync()) partial.deleteSync();
    return 'Could not write to ${beside.path} (${error.osError?.message ?? error.message}). '
        'Move the AppImage somewhere you own, or download the new one '
        'yourself. Nothing has been changed.';
  } on Object catch (error) {
    if (partial.existsSync()) partial.deleteSync();
    return 'The download failed ($error). Nothing has been changed.';
  }
}

/// Whether [url] is https on GitHub, which is the only place a release of
/// this console lives. The asset URL is on github.com and redirects to
/// objects.githubusercontent.com, so both are needed. Card 1028.
bool fromGitHub(Uri url) =>
    url.scheme == 'https' &&
    (url.host == 'github.com' || url.host == 'objects.githubusercontent.com');

String _offSite(Uri url) =>
    'The update would come from ${url.scheme}://${url.host}, which is not '
    'GitHub over https. Nothing has been changed.';

/// The lowercase hex SHA-256 that [manifest] gives for [name], or null when
/// it names no such file. The manifest is what `sha256sum` writes: a hash, a
/// space, a space or a star, and the name.
String? expectedSum(String manifest, String name) {
  for (final line in const LineSplitter().convert(manifest)) {
    final match = RegExp(r'^([0-9a-fA-F]{64}) [ *](.+)$').firstMatch(line);
    if (match != null && match[2] == name) return match[1]!.toLowerCase();
  }
  return null;
}
