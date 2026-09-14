# summareader-sync-server

One implementation, two deployments: self-host it for free, or use the hosted
instance. Same binary, different URL — there is no "cloud edition".

That is only sustainable because the server is deliberately incapable. It
stores opaque ciphertext against sequence numbers and never learns what any of
it means. It cannot search, cannot count unread, and cannot render a web
reader, because it holds no keys.

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
