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

/// Writes the entry and the icons.
///
/// [image] is the AppImage's own path — `Exec` has to name the file somebody
/// downloaded, wherever they put it, because there is nothing else to run.
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
