# Changelog

The version a release is tagged with is the one in `version.go`, and the
release workflow refuses to publish without a section here that names it.

## 0.7.0

The console can keep itself up to date. A server runs on a machine nobody
looks at, so an update that waits to be asked for is an update that never
happens. Switched on — it is off until it is — the console looks once when it
opens and daily after that, downloads what it finds and puts it in place.

Nothing running is interrupted. The new image is written over the old one
while the old one is still running, which is safe and looks as though it
should not be: the console and the server carry on, the page says a new
version is in place, and *Restart now* is there when it suits. **The server
itself is not restarted** — it is answering other devices, and a restart on a
machine nobody is watching drops whatever is in flight.

A check that fails says nothing at all. Nobody asked, so nobody is waiting
for an answer, and a machine with no network is the ordinary case rather than
a fault worth reporting. The button still reports everything.

## 0.6.1

- **Restart now works after downloading an update.** The button drew, and
  pressing it did nothing: the window never handed it anything to do. The
  update itself was always installed correctly — only the last step, using it
  without quitting and reopening the window yourself, was missing.

## 0.6.0

### A device hears about another device in seconds

`/subscribe` holds the request open. A device says where it has got to, and the
answer comes when the log passes that point or when the server has held on long
enough — and those are deliberately the same answer, so nothing here is an
event and nothing is an error. The hint still carries no payload and no count.

It is the other half of something that has worked for a long time: a phone has
pushed a change out five seconds after you mark an article read, and the
desktop then found out on its own timer, up to fifteen minutes later.

**Not PocketBase's realtime API**, which was the obvious answer and does not
fit: it gates subscriptions on `@request.auth` and so needs a PocketBase auth
record per subscriber, where this server authenticates a device token of its
own against its own tables.

The device that wrote is never woken by its own append — it knows, and telling
it would make it sync in response to itself for ever.

`SUMMAREADER_HOLD` sets how long, in seconds; 45 by default, under the sixty a
reverse proxy commonly closes an idle request at. A negative number switches
the wait off entirely, for a deployment that cannot hold a connection open. An
older client that never sends `since` is answered immediately, exactly as
before.

## 0.5.0

### The window can start the server itself

Opening the console and pressing **Start** only ever has one answer on a
machine set up for it. **Configuration → When it starts → Start with this
window** makes opening the window the whole of it.

Off unless it is turned on, and the setting is checked afresh at every launch
rather than trusted: a file written where everything was present and read where
something is missing is how a start nobody watched publishes a library. What
must be present is what the server actually reaches for — a binary, a directory
to keep the library in, an address to listen on. Where a unit owns the server
there is nothing to check, because all three were settled when the unit was
written.

Never a stop, and never a second copy: a server already answering — a unit that
came back at login, or another console left open on the same machine — is left
where it is. Two writers on one database is the thing being avoided.

### Configuration is three pages

It was one column of two and a third screens. **How it is reached** carries the
address and is where Configuration opens; **When it starts** carries the two
switches that answer when the server comes up, which are one subject; **This
program** carries the version and updates, and is offered only to a build that
can replace itself.

## 0.4.0

### The way out is where the way in was

Opening Configuration put **Close** on the left of the title while the button
that opened the page sat on the right, so leaving was not where arriving had
been. It is the same slot now — and there is only one slot, because a header
with two places to put one control is a header where the control ends up in
both.

### The device list speaks from the server's side

Each row read *"N entries sent"*, which is true from the device's point of view
in a window that belongs to the server. It says **received** now. The number is
unchanged and means what it always did: the entries carrying that device's id,
which is what it appended. A device that has never appended anything shows
zero, and a fresh time beside a count that does not move is the honest picture
of a device that syncs and has nothing to say.

### The Restart button has somewhere to go

After downloading a new version the window said it was in place and offered no
way to use it. Two things were dropping the path it had written: the state's own
copy carried the offer and the sentence and silently left the third field out,
and the two-second poll would have wiped it again a moment later. Each was
confirmed by reverting it alone.

### Changing the data directory asks first

It stopped the server, opened whatever was at the new path and restarted,
without a word — and it moves nothing. The library it was serving stays where
it is, and **devices live in that database**, so pointed at an empty directory
the server has no entries and no paired devices, and every device paired with
it becomes a stranger. It now names both paths, says what will happen, and says
that typing the old path back brings all of it back. The first-run question is
exempt: that question is already the question.

### A typed value is kept when you leave the box

Only Enter used to commit. Now leaving the field, changing the page or closing
the window keeps it too — but only when the value parses and differs from the
one stored, and a refused value puts the box back to what is really in effect.
Clearing the port box and clicking away no longer sets 8099, which is what an
empty string used to mean.

## 0.3.9

### A broken macOS or Windows build is found on the pull request

The release cross-compiles five targets from the Linux runner, so a break on
either of the two nobody here can run was found at tag time — when the fix is
most expensive and the release is already waiting on it. CI now builds for
`darwin/arm64` and `windows/amd64` alongside the tests.

It is a compile check and nothing more, which is all the code allows: there is
no cgo and there are no build tags, so the compiler sees everything, and the
one platform-specific branch — the data directory in `main.go` — is already
covered by tests that take the platform as a parameter. Deliberately not a
`windows-latest` job: with no Windows machine to run the binary on, that would
cost minutes to re-prove exactly what this proves for nothing.

## 0.3.8

### The install question asks about installing

The dialog that offers to repoint the menu entry opened with what was wrong
rather than with what was about to happen, and put two paths, a move and a
deletion in front of the buttons. It now asks whether to install the version
in hand, and keeps the rest behind a "What this does" disclosure that starts
closed.

## 0.3.7

### Restart now

Beside the line that says the update is in place. The new version is on disk
and the old one is the process on screen — which is exactly why replacing it
was safe — so nothing changes until it is started again, and that was a
sentence asking you to do it.

## 0.3.6

### An updated image renames itself to the version it holds

A self-update writes the new program into the old path — that is what makes
the swap atomic — so `SummaReaderSync-0.3.5-x86_64.AppImage` went on saying
0.3.5 while holding 0.3.6, and the launcher entry named that file. The file is
renamed afterwards now, and the menu entry follows it. Nothing outside the
name changes, and an entry naming another copy is left alone.

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
