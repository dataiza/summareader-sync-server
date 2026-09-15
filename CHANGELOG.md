# Changelog

The version a release is tagged with is the one in `version.go`, and the
release workflow refuses to publish without a section here that names it.

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
