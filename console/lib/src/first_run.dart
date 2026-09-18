import 'dart:io';

import 'package:flutter/material.dart';
import 'package:summareader_ui/summareader_ui.dart';

import 'pairing_dialogs.dart';
import 'version.dart';

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
    done: false,
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
    done: false,
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

/// Whether to put the console in the applications menu, asked once.
///
/// An AppImage is a file in a folder and nothing knows about it: no icon, no
/// menu entry, and a task switcher showing an unnamed window. Fixing that means
/// writing into somebody's home, so it is asked rather than done — writing
/// there unbidden the first time a program runs is what makes people distrust
/// this format.
///
/// Returns true to add it. A no is remembered as firmly as a yes, or "once"
/// becomes "every launch until you give in".
Future<bool> askAboutTheMenu(BuildContext context) async {
  var wanted = false;

  await showConsoleDialog(
    context,
    'Add this to your applications?',
    Builder(
      builder: (dialogContext) => Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        mainAxisSize: MainAxisSize.min,
        children: [
          Text(
            'This is a single file you downloaded, so nothing has told your '
            'desktop about it. Adding it moves this file to ~/Applications and '
            'writes a launcher entry and icons into ~/.local/share — no root, '
            'nothing outside your home, and reversible from Configuration.',
            style: Ar.bodyStyle(13.5, color: Ar.dim(0.75), height: 1.6),
          ),
          const SizedBox(height: 12),
          Text(
            'It is moved rather than copied so that there is one of it: two '
            'copies both update themselves, and a month later they are '
            'different versions. Leave it out and this keeps working exactly '
            'as it does now: run the file where it is. You will not be asked '
            'again either way.',
            style: Ar.bodyStyle(13, color: Ar.dim(0.7), height: 1.6),
          ),
          const SizedBox(height: 18),
          Row(
            mainAxisAlignment: MainAxisAlignment.end,
            children: [
              PillButton(
                label: 'Not now',
                onTap: () => Navigator.of(dialogContext).pop(),
              ),
              const SizedBox(width: 9),
              PrimaryButton(
                label: 'Add it',
                onTap: () {
                  wanted = true;
                  Navigator.of(dialogContext).pop();
                },
              ),
            ],
          ),
        ],
      ),
    ),
    done: false,
  );

  return wanted;
}

/// Offers to repoint a menu entry that names a file somewhere else.
///
/// Asked rather than done, on the same grounds as adding it in the first
/// place: this rewrites a file in somebody's home, and the launch it happens
/// on has nothing to do with the menu as far as they are concerned.
///
/// The question is the one being answered, which is whether to install this
/// release — the two paths and the deletion are how that is carried out, and
/// leading with them buried the only sentence anybody needed to read.
Future<bool> askAboutARepoint(
  BuildContext context,
  String named,
  String running, {
  String? replacing,
}) async {
  var wanted = false;
  var showingDetail = false;

  await showConsoleDialog(
    context,
    'Install version $consoleVersion?',
    StatefulBuilder(
      builder: (dialogContext, setInner) => Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        mainAxisSize: MainAxisSize.min,
        children: [
          Text(
            'You are running a copy that your applications menu does not know '
            'about. Installing it makes this the copy the menu starts.',
            style: Ar.bodyStyle(13.5, color: Ar.dim(0.75), height: 1.6),
          ),
          const SizedBox(height: 14),
          // Closed by default: what moves where is the answer to a question
          // that only some people ask, and it is a wall of text in front of
          // everybody else.
          Hoverable(
            onTap: () => setInner(() => showingDetail = !showingDetail),
            builder: (context, hovered) => Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                Icon(
                  showingDetail ? Icons.expand_more : Icons.chevron_right,
                  size: 18,
                  color: Ar.dim(hovered ? 0.85 : 0.6),
                ),
                const SizedBox(width: 4),
                Text(
                  'What this does',
                  style: Ar.bodyStyle(13, color: Ar.dim(hovered ? 0.85 : 0.6)),
                ),
              ],
            ),
          ),
          if (showingDetail) ...[
            const SizedBox(height: 12),
            Text(
              'Your applications menu starts this one:',
              style: Ar.bodyStyle(13.5, color: Ar.dim(0.75), height: 1.6),
            ),
            const SizedBox(height: 6),
            SelectableText(
              named,
              style: Ar.bodyStyle(12.5, color: Ar.dim(0.6)),
            ),
            const SizedBox(height: 12),
            Text(
              'and you are running this one:',
              style: Ar.bodyStyle(13.5, color: Ar.dim(0.75), height: 1.6),
            ),
            const SizedBox(height: 6),
            SelectableText(
              running,
              style: Ar.bodyStyle(12.5, color: Ar.dim(0.6)),
            ),
            const SizedBox(height: 12),
            Text(
              replacing != null
                  ? 'Moving this one to ~/Applications and starting it from the '
                        'menu from now on makes it the copy you have installed. '
                        '$replacing is deleted — it is the same program, one '
                        'download behind, and two copies each update themselves '
                        'separately.'
                  : 'Moving this one to ~/Applications and starting it from the '
                        'menu from now on makes it the copy you have installed. '
                        'If the entry keeps naming a file that is gone, the icon '
                        'in your launcher starts nothing at all.',
              style: Ar.bodyStyle(13, color: Ar.dim(0.7), height: 1.6),
            ),
          ],
          const SizedBox(height: 18),
          Row(
            mainAxisAlignment: MainAxisAlignment.end,
            children: [
              PillButton(
                label: 'Leave it',
                onTap: () => Navigator.of(dialogContext).pop(),
              ),
              const SizedBox(width: 9),
              PrimaryButton(
                label: 'Install',
                onTap: () {
                  wanted = true;
                  Navigator.of(dialogContext).pop();
                },
              ),
            ],
          ),
        ],
      ),
    ),
    done: false,
  );

  return wanted;
}

/// Asks before the data directory is repointed at somewhere else.
///
/// This restarts the server against a different database, and the word
/// "directory" makes it sound like a preference. It is not one: the account,
/// every paired device and everything they have sent live in the directory
/// being left behind, so a server pointed at an empty one comes up knowing
/// nobody.
///
/// Nothing is moved and nothing is deleted, which is the reassurance that
/// makes the rest readable — setting the old path back here puts it all
/// exactly as it was. Said in the dialog, because somebody who has already
/// pressed the button needs it more than somebody who has not.
///
/// Both paths are behind the detail section rather than in the lead, on the
/// same grounds as `askAboutARepoint`: the sentence that decides the answer
/// should not be read past two absolute paths to get to.
///
/// Returns true to go ahead.
Future<bool> askAboutChangingTheDirectory(
  BuildContext context,
  String from,
  String to,
) async {
  var wanted = false;
  var showingDetail = false;

  await showConsoleDialog(
    context,
    'Change the data directory?',
    StatefulBuilder(
      builder: (dialogContext, setInner) => Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        mainAxisSize: MainAxisSize.min,
        children: [
          Text(
            'The server restarts and opens whatever is in the new directory. '
            'Nothing is moved: the library it is serving now stays where it '
            'is, and stops being served.',
            style: Ar.bodyStyle(13.5, color: Ar.dim(0.75), height: 1.6),
          ),
          const SizedBox(height: 14),
          Hoverable(
            onTap: () => setInner(() => showingDetail = !showingDetail),
            builder: (context, hovered) => Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                Icon(
                  showingDetail ? Icons.expand_more : Icons.chevron_right,
                  size: 18,
                  color: Ar.dim(hovered ? 0.85 : 0.6),
                ),
                const SizedBox(width: 4),
                Text(
                  'What this does',
                  style: Ar.bodyStyle(13, color: Ar.dim(hovered ? 0.85 : 0.6)),
                ),
              ],
            ),
          ),
          if (showingDetail) ...[
            const SizedBox(height: 12),
            Text(
              'It is serving this one:',
              style: Ar.bodyStyle(13.5, color: Ar.dim(0.75), height: 1.6),
            ),
            const SizedBox(height: 6),
            SelectableText(from, style: Ar.bodyStyle(12.5, color: Ar.dim(0.6))),
            const SizedBox(height: 12),
            Text(
              'and it would open this one:',
              style: Ar.bodyStyle(13.5, color: Ar.dim(0.75), height: 1.6),
            ),
            const SizedBox(height: 6),
            SelectableText(to, style: Ar.bodyStyle(12.5, color: Ar.dim(0.6))),
            const SizedBox(height: 12),
            Text(
              'If there is no library in the new directory the server starts '
              'with an empty one — no entries, and no paired devices, so every '
              'device you have paired is a stranger to it until it is pointed '
              'back. Nothing in the old directory is touched or deleted: '
              'typing that path in here again brings all of it back.',
              style: Ar.bodyStyle(13, color: Ar.dim(0.7), height: 1.6),
            ),
          ],
          const SizedBox(height: 18),
          Row(
            mainAxisAlignment: MainAxisAlignment.end,
            children: [
              PillButton(
                label: 'Leave it',
                onTap: () => Navigator.of(dialogContext).pop(),
              ),
              const SizedBox(width: 9),
              PrimaryButton(
                label: 'Change it',
                onTap: () {
                  wanted = true;
                  Navigator.of(dialogContext).pop();
                },
              ),
            ],
          ),
        ],
      ),
    ),
    done: false,
  );

  return wanted;
}
