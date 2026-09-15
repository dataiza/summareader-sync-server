/// Putting the console in the applications menu, and taking it out again.
///
/// An AppImage is one file and nothing knows about it. Downloaded, it sits in
/// ~/Downloads and is started by finding it there — no icon, no menu entry, and
/// a task switcher that shows an unnamed window. That is the part of "just
/// download and run" people actually dislike, and it is fixed by writing two
/// kinds of file into ~/.local/share, which needs no root and no tool.
///
/// Offered once rather than done silently. Writing into somebody's home the
/// first time they open a program, without asking, is exactly what makes people
/// distrust this format.
library;

import 'dart:io';

import 'updates.dart';

/// The GTK application id, which is also what the files are named after.
///
/// It has to match `APPLICATION_ID` in linux/CMakeLists.txt or the window is
/// never matched to the entry — the launcher then shows the program twice, once
/// as the icon that started it and once as an unnamed window beside it.
const desktopId = 'sk.dataiza.summareader_sync_console';

/// Where the entry and the icons go, per the XDG base directory spec.
String _dataHome([Map<String, String>? environment]) {
  final env = environment ?? Platform.environment;
  final named = env['XDG_DATA_HOME'] ?? '';
  // Absolute or ignored, as the specification requires — the same rule the
  // config and the library follow in config.dart.
  if (named.startsWith('/')) return named;
  return '${env['HOME'] ?? ''}/.local/share';
}

String desktopFilePath([Map<String, String>? environment]) =>
    '${_dataHome(environment)}/applications/$desktopId.desktop';

/// Whether the console is in the applications menu.
bool isInMenu([Map<String, String>? environment]) =>
    File(desktopFilePath(environment)).existsSync();

/// The sizes written, which are the ones the AppImage carries.
const _sizes = [16, 32, 48, 64, 128, 256, 512];

/// Where an AppImage lives once somebody has decided to keep it.
///
/// `~/Applications` is the convention — AppImageLauncher puts them there and
/// some desktops already scan it — and it is not `~/.local/bin`, which is for
/// things on PATH rather than for whole programs.
String applicationsDir([Map<String, String>? environment]) =>
    '${(environment ?? Platform.environment)['HOME'] ?? ''}/Applications';

/// Moves the image somewhere the menu entry can keep pointing at.
///
/// The entry names an absolute path, and until this existed that path was
/// wherever the file happened to be sitting when the question was answered —
/// usually the downloads folder. Tidying that folder, which is a thing people
/// do, left a launcher entry pointing at nothing.
///
/// **Moved and not copied.** Two copies of a program that can each replace
/// itself from GitHub are two programs: a month later they are different
/// versions, and which one runs depends on which icon was clicked. One file
/// also means no second 18–40 MB.
///
/// A rename first, because within one filesystem it is atomic and instant. It
/// fails across a mount point — a download on another disk — so that falls
/// back to copy-then-delete, and if the delete fails the copy is still good
/// and the original is merely still there.
///
/// Returns the path it now lives at, which is the old one when it was already
/// in the right place or could not be moved at all.
Future<String> keepImage(
  String image, {
  Map<String, String>? environment,
}) async {
  final home = Directory(applicationsDir(environment));
  final target = '${home.path}/${image.split('/').last}';
  if (image == target) return image;

  try {
    await home.create(recursive: true);
    final source = File(image);
    try {
      return (await source.rename(target)).path;
    } on FileSystemException {
      final copied = await source.copy(target);
      try {
        await source.delete();
      } on FileSystemException {
        // Copied but not removed: a read-only download directory, or a file
        // somebody else owns. The menu entry points at the copy, which is the
        // part that matters, and the original is where they left it.
      }
      return copied.path;
    }
  } on FileSystemException {
    // No ~/Applications and none can be made. Point at the image where it is,
    // which is what happened before this existed.
    return image;
  }
}

/// Deletes the image the menu used to name, now that this one has taken its
/// place.
///
/// The case this is for: a newer release downloaded by hand and run out of
/// ~/Downloads, while the menu still names the copy in ~/Applications. Moving
/// the new one in and repointing the entry leaves the old one sitting there —
/// a whole second program that checks GitHub for itself and can be started
/// from a file manager, one version behind for ever.
///
/// Deliberately narrow. Only a file in ~/Applications, only an AppImage, never
/// the one now named, and never something that is not there any more: this is
/// clearing away a copy that has been replaced, not deleting whatever a menu
/// entry happened to point at.
Future<bool> removeSuperseded(
  String named,
  String kept, {
  Map<String, String>? environment,
}) async {
  if (!supersedes(named, kept, environment: environment)) return false;
  try {
    await File(named).delete();
    return true;
  } on FileSystemException {
    // Somebody else's file, or a read-only directory. The menu points at the
    // right one either way, which is the part that matters.
    return false;
  }
}

/// Whether [kept] takes the place of [named] — the question above, asked
/// before the dialog says what pressing the button will do.
bool supersedes(
  String named,
  String kept, {
  Map<String, String>? environment,
}) =>
    named != kept &&
    named.endsWith('.AppImage') &&
    named.startsWith('${applicationsDir(environment)}/') &&
    File(named).existsSync();

/// Writes the entry and the icons.
///
/// [image] is the AppImage's own path. Pass it through [keepImage] first: the
/// entry names an absolute path and has to keep being right after somebody
/// empties their downloads folder.
///
/// Returns null when it worked and a sentence when it did not.
Future<String?> addToMenu(
  String image, {
  Map<String, String>? environment,
  String? iconsFrom,
}) async {
  try {
    final entry = File(desktopFilePath(environment));
    await entry.parent.create(recursive: true);

    // Written here rather than copied out of the image, because exactly one
    // line differs: Exec names an absolute path this time. The rest is kept
    // identical to scripts/appimage/*.desktop on purpose — two spellings of
    // the same entry is how the menu and the window stop matching.
    await entry.writeAsString('''
[Desktop Entry]
Type=Application
Name=SummaReader Sync Server
GenericName=Sync server
Comment=Keeps a SummaReader library in step across your own devices
Exec=${image.replaceAll(' ', r'\ ')}
Icon=$desktopId
Terminal=false
Categories=Utility;Network;
Keywords=summareader;sync;server;reader;
StartupWMClass=$desktopId
''');

    // The icons come out of the running image, where the build put them. A
    // console that is not running as an AppImage has none, and says so rather
    // than writing an entry that points at a missing icon.
    final from = iconsFrom ?? _iconsInThisImage();
    if (from != null) {
      for (final size in _sizes) {
        final source = File('$from/${size}x$size/apps/$desktopId.png');
        if (!source.existsSync()) continue;
        final target = File(
          '${_dataHome(environment)}/icons/hicolor/${size}x$size/apps/$desktopId.png',
        );
        await target.parent.create(recursive: true);
        await source.copy(target.path);
      }
    }

    await _refreshCaches(environment);
    return null;
  } on FileSystemException catch (error) {
    return 'Could not write the menu entry '
        '(${error.osError?.message ?? error.message}). Nothing has been '
        'changed.';
  }
}

/// Removes both, leaving nothing behind.
Future<String?> removeFromMenu({Map<String, String>? environment}) async {
  try {
    final entry = File(desktopFilePath(environment));
    if (entry.existsSync()) await entry.delete();
    for (final size in _sizes) {
      final icon = File(
        '${_dataHome(environment)}/icons/hicolor/${size}x$size/apps/$desktopId.png',
      );
      if (icon.existsSync()) await icon.delete();
    }
    await _refreshCaches(environment);
    return null;
  } on FileSystemException catch (error) {
    return 'Could not remove the menu entry '
        '(${error.osError?.message ?? error.message}).';
  }
}

/// Where this AppImage keeps its icons, or null when this is not one.
String? _iconsInThisImage() {
  // usr/bin/<executable> → usr/share/icons/hicolor. Relative to the resolved
  // executable, which inside an image is the mount and outside it is wherever
  // the bundle was unpacked; in the second case the directory simply is not
  // there and the caller writes no icons.
  final bin = File(Platform.resolvedExecutable).parent;
  final icons = Directory('${bin.parent.path}/share/icons/hicolor');
  return icons.existsSync() ? icons.path : null;
}

/// Tells the desktop the menu changed, where there is something to tell.
///
/// Both tools are optional and both are missing on some desktops. Their absence
/// is not a failure: every environment worth naming rescans on its own, just
/// less promptly, and a program that refuses to add itself to a menu because a
/// cache tool is missing has failed at the thing it was asked to do.
Future<void> _refreshCaches([Map<String, String>? environment]) async {
  final home = _dataHome(environment);
  for (final (tool, args) in [
    ('update-desktop-database', ['$home/applications']),
    ('gtk-update-icon-cache', ['-q', '-t', '-f', '$home/icons/hicolor']),
  ]) {
    try {
      await Process.run(tool, args);
    } on ProcessException {
      // Not installed. See above.
    }
  }
}

/// The path the menu entry currently names, or null when there is no entry.
///
/// Read back rather than remembered, because the thing that goes stale is the
/// file on disk and the file on disk is the truth about it.
String? menuTarget([Map<String, String>? environment]) {
  final entry = File(desktopFilePath(environment));
  if (!entry.existsSync()) return null;
  for (final line in entry.readAsLinesSync()) {
    if (line.startsWith('Exec=')) {
      return line.substring(5).replaceAll(r'\ ', ' ');
    }
  }
  return null;
}

/// Whether the menu entry points somewhere other than the image that is
/// running.
///
/// True after somebody moves the file by hand, or runs a second copy from
/// elsewhere. False when there is no entry at all — nothing is stale if
/// nothing was ever added.
bool menuIsStale([Map<String, String>? environment]) {
  final named = menuTarget(environment);
  final running = runningImage(environment);
  return named != null && running != null && named != running;
}
