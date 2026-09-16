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

import 'package:http/http.dart' as http;

import 'version.dart';

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

/// A release, reduced to the two things worth acting on.
typedef Release = ({String version, Uri image});

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
    for (final asset in assets) {
      if (asset is! Map) continue;
      final name = asset['name'];
      final url = asset['browser_download_url'];
      if (name is String && name.endsWith('.AppImage') && url is String) {
        return (version: offered, image: Uri.parse(url));
      }
    }
    throw const FormatException('that release has no AppImage in it');
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
/// Returns a sentence when it could not start — a moved file, a permission —
/// and never returns at all when it could.
Future<String?> restartInto(String image) async {
  try {
    await Process.start(image, const [], mode: ProcessStartMode.detached);
  } on ProcessException catch (error) {
    return 'Could not start $image (${error.message}). Start it yourself.';
  }
  // A moment for the new process to be on its way before this one goes; the
  // AppImage runtime mounts before it execs, and a mount is not instant.
  await Future<void>.delayed(const Duration(milliseconds: 200));
  exit(0);
}

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
}) async {
  final path = imagePath ?? runningImage(environment);
  if (path == null) {
    return 'This is not running as an AppImage, so there is nothing to '
        'replace. Nothing has been changed.';
  }

  final image = File(path);
  final beside = image.parent;
  // Beside the image rather than in /tmp, because the rename has to be on one
  // filesystem to be atomic — across a mount point it is a copy, and a copy
  // interrupted leaves a broken program where a working one was.
  final partial = File('${image.path}.new');

  try {
    final web = client ?? http.Client();
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
