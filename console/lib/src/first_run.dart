import 'dart:io';

import 'package:flutter/material.dart';
import 'package:summareader_ui/summareader_ui.dart';

import 'pairing_dialogs.dart';

/// The question asked once, before the window is any use.
///
/// The server has to open a database, and until now nobody was ever told which
/// one — the default was a directory under ~/.config that nothing in the
/// documentation named, and install.sh used a different one. A window that is
/// about to create a library somewhere should say where.
///
/// Only when nothing else said. A flag, SUMMAREADER_DIR or a `dir` key in the
/// config file all mean somebody has already answered, and asking again would
/// be a dialog in front of a decision that is made — see `Dirs.chosen`.
///
/// Returns the directory to use, or null when the window was dismissed without
/// an answer, which leaves the default in place and asks again next time.
Future<String?> askWhereTheLibraryGoes(
  BuildContext context,
  String proposed,
) async {
  final controller = TextEditingController(text: proposed);
  String? answer;

  await showConsoleDialog(
    context,
    'Where should the library go?',
    Builder(
      builder: (dialogContext) => Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        mainAxisSize: MainAxisSize.min,
        children: [
          Text(
            'This server keeps an account, every paired device and everything '
            'those devices have sent, in one directory. Nothing else goes in '
            'it, and nothing here can read what it holds.',
            style: Ar.bodyStyle(13.5, color: Ar.dim(0.75), height: 1.6),
          ),
          const SizedBox(height: 14),
          ArField(controller: controller, background: Ar.neutral100),
          const SizedBox(height: 10),
          Text(
            'You can change this later in Configuration. Moving it afterwards '
            'is a matter of copying the directory yourself — the server will '
            'not take it with it.',
            style: Ar.bodyStyle(12.5, color: Ar.dim(0.6), height: 1.5),
          ),
          const SizedBox(height: 18),
          Align(
            alignment: Alignment.centerRight,
            child: PrimaryButton(
              label: 'Keep it here',
              onTap: () {
                answer = controller.text.trim();
                Navigator.of(dialogContext).pop();
              },
            ),
          ),
        ],
      ),
    ),
  );

  controller.dispose();
  return (answer?.isEmpty ?? true) ? null : answer;
}

/// Whether the chosen directory already holds a library, and what to do.
///
/// A directory with a `data.db` in it is somebody's account and every device
/// token they have paired. Opening it is almost always what was meant —
/// reinstalling, or pointing the window at a directory a service has been
/// filling for months — so **Keep** is the answer, and the only one reachable
/// by pressing Return.
///
/// Deleting is offered because the other reason to find a database here is
/// that it is a stale one from an install nobody remembers, and a window that
/// can only refuse is a window somebody works around with `rm` and no warning
/// at all. It says what goes, in the words that matter: the account and every
/// device token, not recoverable.
///
/// Returns true when it was emptied, false when it was kept.
Future<bool> askAboutWhatIsAlreadyThere(
  BuildContext context,
  String dir,
) async {
  var deleted = false;

  await showConsoleDialog(
    context,
    'There is already a library here',
    Builder(
      builder: (dialogContext) => Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        mainAxisSize: MainAxisSize.min,
        children: [
          Text(
            '$dir holds a database already. Keeping it is almost certainly '
            'what you want: it is how a reinstall finds the devices that were '
            'paired before.',
            style: Ar.bodyStyle(13.5, color: Ar.dim(0.75), height: 1.6),
          ),
          const SizedBox(height: 12),
          Text(
            'Starting again deletes the account and every device token in it. '
            'Nothing here can restore them, and every paired device would have '
            'to be paired again from an app that still holds the key.',
            style: Ar.bodyStyle(13, color: Ar.dim(0.7), height: 1.6),
          ),
          const SizedBox(height: 18),
          Align(
            alignment: Alignment.centerRight,
            child: PillButton(
              label: 'Start again, and lose it',
              onTap: () async {
                final navigator = Navigator.of(dialogContext);
                deleted = await emptyLibrary(dir);
                navigator.pop();
              },
            ),
          ),
        ],
      ),
    ),
  );

  return deleted;
}

/// Removes the database and the files that belong to it, and nothing else.
///
/// Named rather than `deleteSync(recursive: true)` on the directory: the config
/// file lives here too on a default install, and it holds the server's name,
/// its address and any tokens an operator set. Losing those to a question
/// about the *library* would be answering something nobody asked.
///
/// The write-ahead log and the shared-memory file go with it, or SQLite opens
/// the next database on top of the last one's uncommitted pages.
Future<bool> emptyLibrary(String dir) async {
  var removed = false;
  for (final name in const [
    'data.db',
    'data.db-wal',
    'data.db-shm',
    'auxiliary.db',
    'auxiliary.db-wal',
    'auxiliary.db-shm',
  ]) {
    final file = File('$dir/$name');
    if (file.existsSync()) {
      file.deleteSync();
      removed = true;
    }
  }
  return removed;
}

/// Whether [dir] already holds a library.
bool holdsALibrary(String dir) => File('$dir/data.db').existsSync();
