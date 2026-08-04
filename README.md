# summareader-sync-server

One implementation, two deployments: self-host it for free, or use the hosted
instance. Same binary, different URL — there is no "cloud edition".

That is only sustainable because the server is deliberately incapable. It
stores opaque ciphertext against sequence numbers and never learns what any of
it means. It cannot search, cannot count unread, and cannot render a web
reader, because it holds no keys.

## Running it

```sh
go build -o summareader-sync .

# Create an account and issue a token for your first device. This has to happen
# from a shell, because until one device has a token there is nobody to
# authorise the request.
./summareader-sync pair "My library" "Desktop" --dir=./pb_data

./summareader-sync serve --http=127.0.0.1:8099 --dir=./pb_data
```

`pair` prints the token once. Paste it into SummaReader on that device. Add
`--json` for scripting.

**Every device after the first is enrolled from one that is already paired** —
no shell access needed:

```sh
curl -X POST http://127.0.0.1:8099/enroll \
  -H "Authorization: Bearer <existing-token>" \
  -d '{"label":"Phone"}'
```

The QR a user scans during pairing carries the *master key*, which is what makes
the library readable. The new device still needs its own *server* token, and
that is what enrol issues. Separate tokens are what make revoking one device
possible at all.

## Running it in Docker

```sh
docker compose up -d
docker compose exec sync summareader-sync pair "My library" "Desktop" --dir=/data
```

The second command prints a token once, as it does outside Docker, and every
device after the first enrols from one already paired.

Compose binds the port to `127.0.0.1` on purpose. This server speaks plain
HTTP and holds everyone's ciphertext, so putting it directly on a public
interface means device tokens crossing the network in the clear. Put a TLS
terminator in front of it and expose that instead.

Everything worth keeping is in the `sync-data` volume. Losing it does not lose
anybody's library — those live on the devices — but it does lose every
device's token and the account they share, so each device would have to be
paired again.

### Checking a deployment

```sh
./scripts/smoke.sh          # build, start, exercise the contract
./scripts/smoke.sh --clean  # and remove the container and its volume
```

Twelve checks: that the image builds and starts, that all five operations
answer, that an unauthenticated request is refused, that a second device can
enrol without a shell, that listed devices do not carry their tokens, and that
the log and the blobs survive a restart. The unit tests cover the handlers;
this covers everything around them that can be broken while every test passes.

## Finding it on a local network

The server advertises itself over mDNS as `_summareader-sync._tcp`, so a client
on the same network can offer it rather than asking somebody to read an IP
address off a router. `serve --no-announce` turns it off, for networks that
would rather nothing multicast and for hosted instances that have no reason to
shout on whatever network they sit in.

**Two things it does not survive, both worth knowing before relying on it.**

Docker's default bridge network does not carry multicast to the LAN, so a
container started by the compose file here is not discoverable. That needs
`network_mode: host`, which also gives up the port mapping — a deliberate
trade, not an oversight.

And on a host already running a responder of its own, discovery is uneven:
verified working between processes with avahi-daemon running, and `avahi-browse`
on that same host still does not list it. A client on another machine is the
case that matters and is the case least affected, but this is a convenience
that can fail quietly, which is why the address always works and nothing
depends on this.

## Administration

PocketBase's own dashboard is at `/_/`, and it is where backups, restores and
raw inspection live. Create a superuser to reach it:

```sh
docker compose exec sync summareader-sync superuser create you@example.com
```

**What an admin interface here can and cannot do is decided by the encryption,
not by effort.** The server holds opaque ciphertext and no keys. So it can:

- list, enrol and revoke devices, and see when each last synced
- back up and restore the whole store
- delete an account's data outright, which is what `/wipe` is
- report how much space an account is using

And it cannot, ever:

- delete a feed, or show you one — it does not know what a feed is
- apply retention by content, age of an article, or read state
- search anything

Those belong in the app, which has the keys. An admin screen offering them
would either be lying or would mean giving the server the keys, which is the
one thing this design exists to avoid.

A backup taken here is safe to keep anywhere, and **useless without the
recovery code** — it is ciphertext. That is a feature, and it is also the
thing to remember before relying on the backup as your only copy.

## Ceilings, if you are hosting for somebody else

Both of these are **off**, and stay off unless you set a number. If you self-host,
the disk is yours and you can already see it — there is nothing here for you.
They exist for the case where somebody else's growth is your bill.

**Storage.** Each account has a `quota_bytes` field, editable in the dashboard.
Zero means no ceiling. Set it and the account is refused an append or a new
blob once its stored payload would exceed it — answered as `507` with a
sentence the app shows the person, not a status code they can do nothing with.

Usage is summed at the check rather than kept in a counter column, so it cannot
drift out of step with wipes and deletions. Payload bytes only: row overhead
and indexes are real disk too, but a ceiling somebody can reason about is worth
more than one that is exactly right.

**Request rate.** PocketBase ships its own rate limiter, disabled by default,
under Settings → Rate limits in the dashboard. Rules match by path prefix, so
`/append` and `/blob` can be limited without any code here. It is per-client,
not per-account, which is the right shape for abuse and the wrong shape for
billing — if you ever need a ceiling per paying account, that is a different
mechanism and this is not it.

**Neither of these prunes anything.** The log is append-only: read state and
highlights append to it forever, so an account grows with use, not with the
size of the library. A quota over a log nothing prunes is a ceiling an active
account eventually reaches whatever it stores, and today the only remedy the
server can offer is a wipe.

## The five operations, frozen

```
POST /append          -> {"seq": n}
GET  /from/{seq}      -> {"entries": [...]}   entries strictly after seq
PUT  /blob/{name}     content-addressed, idempotent
GET  /blob/{name}
GET  /subscribe       -> {"head": n}          a change hint, no payload
```

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
GET  /devices    list them — never with their tokens
POST /revoke     stop one syncing
```

Listing deliberately omits tokens: a settings screen has no use for them, and a
token that appears in a list is a token that ends up in a screenshot. Revoking
the device you are currently holding is refused, because it would lock you out
of the account with no way back except the CLI.

## Revocation

Dropping a device's token stops it syncing. Whatever it already downloaded
stays readable, because it still holds its own copy of the master key. There is
no key rotation and no re-encryption pass — those would mean re-encrypting the
whole library. The client copy says exactly this and no more.

## Version pinning

PocketBase is pre-1.0 with no compatibility guarantee, so the version in
`go.mod` is exact. Upgrade deliberately and re-run the tests; the collection
API in particular changes between minor versions.
