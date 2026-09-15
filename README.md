# summareader-sync-server

One implementation, two deployments: self-host it for free, or use the hosted
instance. Same binary, different URL — there is no "cloud edition".

That is only sustainable because the server is deliberately incapable. It
stores opaque ciphertext against sequence numbers and never learns what any of
it means. It cannot search, cannot count unread, and cannot render a web
reader, because it holds no keys.

## Downloads

Built releases are on this repository's [Releases](../../releases/latest) page —
three per platform, and they are not variations of one another.

| | For | File |
|---|---|---|
| **Headless** | a server reached over the network | `summareader-sync-headless-<os>-<arch>.tar.gz` |
| **Console, Linux** | a desktop, with a window | `summareader-sync-console-linux-x64.tar.gz` |
| **Console, macOS** | a Mac, with a window | `summareader-sync-console-macos.zip` |

A headless bundle is everything a server needs and no window: the binary, an
installer that writes a systemd user unit, the unit, and a compose file whose
Dockerfile copies the binary rather than compiling it. A console bundle is the
window carrying its own copy of the server. Neither needs this repository.

GitHub attaches the tagged source to every release, which is what the licence
below asks for.

## Start here

```sh
./scripts/run.sh                                    # build and serve, foreground
./summareader-sync first-device "My library" "Desktop" --dir=./pb_data
```

The second command prints a token once. Paste it into SummaReader on that
device; **every device after the first joins from one that is already paired**,
by scanning that app's pairing code — not from here. That code carries the key
that makes the library readable, and this server has never held it.

Prefer a window, or a container?

```sh
./scripts/run.sh gui                  # the desktop console, in front of the server
./scripts/run.sh --docker
```

## Documentation

| | |
|---|---|
| [BUILD.md](BUILD.md) | building it — the server, the console, cross-compiling, tests |
| [RUNNING.md](RUNNING.md) | running it — command line, the console, Docker, leaving it running, administration |
| below | why it is shaped like this |

## The five operations, frozen

```
POST /append          -> {"seq": n}
POST /append-batch    -> {"seq": n, "count": k}   # up to 500, one transaction
GET  /from/{seq}      -> {"entries": [...]}   entries strictly after seq
PUT  /blob/{name}     content-addressed, idempotent
GET  /blob/{name}
GET  /subscribe       -> {"head": n}          a change hint, no payload
```

`/append-batch` is the same operation many at a time, not a sixth: optional,
all-or-nothing, and answered with 404 by a server that predates it, which the
client takes as "send them one at a time". It exists because the work per
record is 0.35 ms and the round-trip to reach it is twenty or more, so a first
sync of a real library was almost entirely waiting.

Plus `GET /instance`, which is not part of the contract. It exists so a client
can tell "different server" from "same server, no data" — without it, pointing
at a fresh instance is indistinguishable from everything having been deleted,
and those want opposite responses.

The contract is frozen on purpose. Adapters stay a few hundred lines each only
while it never grows; adding "query by tag" or "count unread" would double the
work permanently and mean building a database once per backend.

## Why `seq` is a counter row and not an identity column

This is the one non-obvious thing in the server, and it is load-bearing.

The requirement is that `seq` be strictly monotonic per account, gap-free, and
visible to a reader only after commit.

An auto-increment column cannot provide that. Identity values are assigned at
`INSERT`, not at `COMMIT`. With two devices appending at once, a reader can
observe seq 5 while seq 4 is still in flight, advance its cursor past 4, and
never see entry 4 again. Nothing errors. The entry is simply gone from that
device's view, permanently, and only under concurrency — which is to say, only
in production.

The fix is a counter row bumped inside the same transaction as the insert, so
both become visible at the same commit. It serializes appends per account,
which costs nothing when an account has three devices.

`append_test.go` asserts the invariant under concurrent writers.
`naive_check_test.go` implements the wrong version and shows it produces
duplicates — roughly 75 of them per 96 appends here — so the test is known to
be capable of catching the bug rather than merely passing.

## What the server knows

| Knows | Does not know |
|---|---|
| account ids | who the account belongs to |
| device tokens | what any device is called by its owner |
| sequence numbers | what order means to the client |
| ciphertext, by size | anything inside it |
| blob names (HMACs) | whether two accounts hold the same file |

Billing identity maps to `account_id` in a separate table, outside the sync
path.

## Devices

```
POST /enroll     issue a token for another device on this account
POST /recovery   record what a typed recovery code must later prove
POST /join       trade proof of that code for a token — no token required
GET  /devices    list them — never with their tokens
POST /revoke     stop one syncing
POST /rename     the caller says what it is called
```

## The operator's own routes

```
POST /operator/rename   {device, label}   rename any device on this server
POST /operator/revoke   {device}          stop any device syncing, reversibly
POST /operator/resume   {device}          let a stopped device sync again
POST /operator/remove   {device}          forget a device entirely
POST /operator/enroll   {label}           issue a token into the one library
```

`/operator/enroll` was deliberately absent, on the reasoning that a token
minted here is useless because this server has never held the key. That is
true of a device which has never held the key either, and false of one that
has — and the second case is the common one: a server rebuilt, a device
stopped and coming back. The app has always accepted a pairing code carrying
an address and a token and no key, so the window draws one, and says plainly
which of the two cases it is for.

It enrols into the account already on the server and never creates a second
one, which is the mistake the pair button used to make.

Stopping and removing are different things and the difference is worth stating:
a stopped device keeps its row, its name and its token, and `resume` puts it
back — which is the honest answer for a phone somebody still owns, or a device
stopped while its owner was away. Removing takes the token with the row, so it
cannot be resumed; it is for a device that is gone. What either one has already
downloaded stays on it, because the key is on the device and nothing here can
reach that.

`/rename` and `/revoke` above answer a **device** token and act on the caller.
That is right for a phone renaming itself and useless to whoever runs the
server: the terminal and the console window are not paired with anything, so
until now an operator could see every device and change none of them, on a box
they own.

These two take `SUMMAREADER_OPERATOR_TOKEN` instead, and read the account off
the device record rather than off the caller — then hand it to the same
`renameDevice` and `revokeDevice` an ordinary device uses, so the account-scope
checks inside those stay meaningful instead of bypassed.

**It is deliberately not the metrics token.** That one is read-only — counts,
ages and who is paired — and is the sort of thing handed to a monitoring system
and forgotten about. These rename and revoke. Whoever runs the server can
already stop the process and read the file, so this is not new power; it is the
same power under a name that says what it does, so a scraper's credential does
not quietly acquire it. Unset, both routes 404 rather than 401, for the reason
`/overview` does: a route that answers "unauthorized" has told you it exists.

From a terminal, against a server that is running:

```
summareader-sync devices list                       [--json]
summareader-sync devices rename <device-id> <name>  [--json]
summareader-sync devices revoke <device-id>         [--json]
summareader-sync devices resume <device-id>         [--json]
summareader-sync devices remove <device-id>         [--json]
```

## What the server calls itself

`--name`, `SUMMAREADER_NAME`, or `"name"` in the config file. It is what the
dashboard shows and what the local-network advertisement carries, and it
defaults to `Acme` because that is PocketBase's own default and this does not
override one that has not been set.

**It is not the server's identity.** That is generated when the database is
made and stored beside it, and it is what `/instance` answers with — so two
servers may be called the same thing without a device mistaking one for the
other. They could not, until recently: the name *was* the identity, nobody
ever set it, and every server in existence therefore claimed to be "Acme".
A client pointed at a recreated database saw an identity it recognised,
concluded nothing had changed, and collected refusals it had no way to
explain.

HTTP rather than opening the database, unlike `first-device`: that one has to
write before a server exists and the console stops the server to run it, which
is the right trade exactly once. Renaming a device should not mean a restart.

`/join` is the one route with no `Authorization` header. `/enroll` needs a
paired device to ask on the new one's behalf, which leaves nothing for a fresh
install or for somebody who has lost every device — that gap is what this
closes. It matches a hash of the proof against `accounts.join_verifier`, which
is empty on every account that has never made a recovery code and is refused
rather than matched, since a filter on the verifier alone would match all of
them at once. Nothing is created on a failure and the device is not touched, so
the column never becomes a record of who has been guessing.

`/rename` exists because a label is otherwise written once, at enrolment, by
whichever device minted the token. That suits a phone somebody is holding and
not a headless one — an MCP mirror in a container has a name in its config and,
without this, no way to say it, so every such device is listed under whatever
was typed at that moment. In practice, "A new device", for all of them.

It renames **the caller**, always. There is no device id in the body, so a
stolen token can rename the device it was stolen from and nothing else.

Listing deliberately omits tokens: a settings screen has no use for them, and a
token that appears in a list is a token that ends up in a screenshot. Revoking
the device you are currently holding is refused, because it would lock you out
of the account with no way back except the CLI.

## Revocation

Dropping a device's token stops it syncing. Whatever it already downloaded
stays readable, because it still holds its own copy of the master key. There is
no key rotation and no re-encryption pass — those would mean re-encrypting the
whole library. The client copy says exactly this and no more.

## Licence

GNU Affero General Public License, version 3 — the full text is in `LICENSE`,
and it ships inside every download.

Section 13 is the one that matters for a server: run a **modified** version
where other people can reach it over a network, and those people are entitled
to its source. Running an unmodified build puts no obligation on you.

This is not the licence SummaReader itself carries. The app is a separate,
proprietary program; it speaks to this over a network protocol and links none
of its code.
