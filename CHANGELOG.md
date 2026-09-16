# Changelog

The version a release is tagged with is the one in `version.go`, and the
release workflow refuses to publish without a section here that names it.

## 0.3.5

### Checking for an update no longer installs one

Finding a newer release now says which version it found and waits: **Download**
or **Cancel**. Replacing the program somebody is running is the one control on
that page that changes this program, and it was happening because they pressed
"check".

The download says how far along it is, as a percentage under the version, and
the line stays afterwards to say the new version starts next time — forty
megabytes over a slow connection is a minute of a window that otherwise looks
as though it has stopped. The file is written beside the old one and swapped
only at the end, so an interrupted download costs a stray file and nothing
else.

## 0.3.4

### Start at login is not offered inside an AppImage

The unit it writes names the server binary *inside the image's mount*: a path
that exists only while the window is open, and a different one at every launch.
So the switch wrote a service that could not start, on the one build where it
looked most like the obvious thing to press.

It is absent there. **This program** says where a service does come from
instead — `scripts/install.sh` from the release, which writes the unit around a
binary that stays put — and the section keeps *Check for updates*.

### Fixed

- The version and **Check for updates** no longer paint past the edge of their
  card in a narrow window; they wrap.

## 0.3.3

### Check for updates was there for two seconds

**This program** — the version and *Check for updates* — appeared at launch and
vanished at the first poll. The window rebuilds its whole state every two
seconds and that rebuild left the field out, so the section drew once and was
gone before anybody could read it. It is carried now, like everything else the
poll does not compute.

### The dialog about the menu entry says what it is about to do

Downloading a new release by hand and running it out of `~/Downloads` is
installing it, and the dialog that noticed treated it as a discrepancy: two
paths and a warning about what could break. It now says what pressing the
button does — moves this copy to `~/Applications` and starts it from the menu
from now on — and the button says **Use this one**.

**And the copy it replaces is deleted**, when there is one: an AppImage in
`~/Applications` that the entry named until now. Left alone it is a second
program a version behind, checking GitHub for itself and startable from a file
manager. Nothing outside `~/Applications` is ever removed, and never the copy
the entry now names. The dialog names the file before it happens.

### Fixed

- Dialogs that ask a question no longer carry a third **Done** button under
  their own two. It belongs to the shell, and was right for the dialogs that
  only show something — a token, a QR code — and wrong beside Leave it and Use
  this one, where it meant "whichever of these is the quiet one".

## 0.3.2

### Configuration no longer asks a question it has already asked

**In the applications menu** is gone from the settings. The console offers the
menu once on a first run, and offers to repoint the entry when it names
somewhere stale — between them there is nothing left for a switch to say, and a
third place for one answer to live is a third place for it to disagree.

**Check for updates** is where it was, and is now the whole of *This program*.

## 0.3.1

### The menu entry survives tidying your downloads

Adding the console to the applications menu wrote the path the AppImage had at
that moment — which is the downloads folder, because that is where a file you
just downloaded is. Empty that folder, as people do, and the icon in the
launcher starts nothing at all.

It moves the image to `~/Applications` first now, which is where AppImages
conventionally live, and the entry names it there. **Moved, not copied**: two
copies of a program that each replace themselves from GitHub are two programs a
month later, and which one runs depends on which icon was clicked. The dialog
says so before it does it.

And if the entry already names somewhere else — the file was moved by hand, or
a second copy is being run — the console says so on launch and offers to point
it here. That is the only moment anything is in a position to notice, because
the program that would have complained is the one that is not there.

## 0.3.0

### An AppImage for Linux, that updates itself

One file: `chmod +x`, run. No repository, no package manager, no root. The
unpacked tarball stays for anyone packaging this themselves.

**It updates itself.** Configuration → This program → *Check for updates* asks
GitHub for the newest release and replaces the running image. Nothing is
checked until it is pressed. Replacing the file a running program started from
is safe — the kernel holds the old inode until the process ends, and the rename
is atomic.

**It offers, once, to join the applications menu**, writing a launcher entry
and icons into `~/.local/share`. Asked rather than done, and a no is remembered
as firmly as a yes. Both controls are absent unless it *is* an AppImage.

The AGPL text now travels inside the image, in `usr/share/doc`. An image is a
single file with no directory beside it, so there was nowhere else for it to be
— and it was not in there at all.

## 0.2.0

### The library goes in the data directory

`~/.local/share/summareader-sync` on Linux. It was `os.UserConfigDir()` —
`~/.config` — for a SQLite database holding an account and every device token,
while `scripts/install.sh` had always used `~/.local/share` and `run.sh` and
both compose files used `./pb_data`. The systemd install, the window and a bare
binary each opened a different database and nothing said so.

**macOS and Windows are unchanged.** There is one sensible place for both there
and it is the one already in use, so on those platforms this is a migration
with nothing to migrate.

`XDG_DATA_HOME` is honoured when absolute and ignored when not: resolving a
relative one against the working directory is two databases waiting to happen.

`serve` now prints which directory it opened and who decided — the config file,
the environment or the default.

### The window asks where, once, and lets you change it

On a first run, and only when nothing else said. The **Data directory** row in
Configuration was a line of text reading "set with --dir at launch"; it is a
field now. Nothing is copied and nothing is deleted when it changes: an empty
directory is a new library no paired device knows about, and moving an existing
one is `mv` and a decision.

A database already in the chosen directory is **kept** unless you say
otherwise. Starting again deletes the account and every device token, which
nothing here can restore, and it says so in those words.

### Configuration is a button

It was a menu holding exactly one item, so reaching the settings meant opening
a menu to choose the only thing in it. A pill now, labelled Configuration, the
way the app spells the same control.

## 0.1.0

The first published build. What is in it, rather than what changed — there is
nothing before this to have changed from.

### The server

- Five frozen operations over an append-only log of ciphertext, plus
  `/append-batch`, which is the same operation up to 500 at a time in one
  transaction. A first sync of a real library was almost entirely round-trip.
- `/instance`, so a client can tell "a different server" from "the same server
  with nothing in it". Those want opposite responses, and until this existed
  they were indistinguishable.
- Per-device tokens, which is what makes revoking one device possible at all.
  Sequence numbers are gap-free because a counter moves in the same transaction
  as the insert.
- Announces itself on the local network over mDNS, so the app can offer an
  address rather than ask for one. `--no-announce` turns it off.
- Optional storage ceilings, off by default, for anyone hosting somebody
  else's growth.
- No cgo anywhere: one machine cross-compiles it for five platforms, and the
  container image carries no libc to match.

### Running it

- A systemd **user** service, installed by `install.sh` with no root involved,
  or a container from the compose file. Both serve the same database, so the
  choice is about how you like to start things.
- A desktop console that starts, stops and watches a server, draws the pairing
  code for a new device, and can install the service itself — for someone who
  has the binaries and not the repository.

### Administration

- A name of its own, `ACME` by default, shown in the dashboard and carried in
  the network announcement. It is display only; identity is the value
  `/instance` answers with, which nobody can edit.
- `devices list`, `devices rename`, `devices revoke`, `devices resume` and
  `devices remove`, over HTTP against the running server, so there is never a
  second writer on the database.
- Two devices cannot be given the same name. An unnamed one is numbered rather
  than refused, because the app sends the same default label every time and
  refusing would fail the second pairing in a dialog with no name field in it.
- `/overview` reports what the server holds without reading any of it.
