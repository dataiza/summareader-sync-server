# summareader-sync-server

One implementation, two deployments: self-host it for free, or use the hosted
instance. Same binary, different URL — there is no "cloud edition".

That is only sustainable because the server is deliberately incapable. It
stores opaque ciphertext against sequence numbers and never learns what any of
it means. It cannot search, cannot count unread, and cannot render a web
reader, because it holds no keys.

## Running it

Three ways into the same server: the headless binary, a desktop window that
supervises that binary, or a container. All three serve the same `pb_data`, so
the choice is about how you like to start things, not about which server you
end up with.

### Headless — the binary

```sh
./scripts/run.sh                  # builds, then serves ./pb_data in the foreground
```

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

**This build needs no C compiler, and that is what makes it easy to run
anywhere.** `CGO_ENABLED=0` cross-compiles it from one machine to linux/amd64,
windows/amd64 and both macOS architectures — around 24–25 MB each with the
debug information stripped, 33–34 MB as `go build` leaves it. One machine
builds every download, and whoever runs it installs nothing first.

```sh
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w" \
  -o summareader-sync-darwin-arm64 .
```

#### Leaving it running

```sh
./scripts/install.sh              # builds it, installs a systemd user service
./scripts/install.sh --uninstall  # reversed; the database is kept
```

A *user* service, under `~/.config/systemd/user`: this holds one person's
ciphertext on one person's machine, and a unit there needs no root to install,
inspect or remove. `scripts/summareader-sync.service` is the definition the
installer fills in — edit that rather than the installed copy. For a shared
box, copy it to `/etc/systemd/system`, give it a `User=` and drop the `--user`
flags.

```sh
systemctl --user status summareader-sync
journalctl --user -u summareader-sync -f
sudo loginctl enable-linger "$USER"    # or it stops when you log out
```

### Desktop — a window

```sh
go build -tags gui -o summareader-sync .
./summareader-sync gui --http=127.0.0.1:8099   # --dir too, or it uses
                                               # ~/.config/summareader-sync
```

![The desktop window: status, counts, and the three buttons](docs/desktop-window.png)

It shows whether the server is up and what it holds, starts and stops it, opens
PocketBase's dashboard, and pairs a first device — which is the one job that
otherwise needs a terminal. Nothing else: the dashboard is the real admin
interface and this does not reimplement any of it.

**The window supervises the server as a child process rather than embedding
it.** It runs the same argv the systemd unit runs, so the desktop path and the
service path cannot drift into two different servers, and the server's own
`log.Fatal` cannot take the window down with it. The counts come from the
child's `/metrics`, with a token minted per window, rather than from a second
connection to the SQLite file the child is writing.

**This build needs cgo**, because the toolkit does, which means a C toolchain
on each platform you want a window for — the exact thing the headless build
avoids. It is also 52 MB against the headless build's 33 MB. That is the
trade the `gui` tag exists to keep optional: `go build .` is untouched and
still cross-compiles, and a build without the tag answers `gui` with a sentence
saying which download this is.

Honest about the state: **the desktop build has been run on Linux only.**
Nothing produces the Windows and macOS builds yet — no workflow, no release.
The code has no platform-specific parts and the toolkit supports all three, but
neither of those is the same as having run it.

`docs/desktop-window.png` is drawn by `go test -tags gui`, not photographed:
the software painter renders the same widget tree without a display, so the
picture is regenerated whenever the window changes rather than aging quietly.

### Container — Docker

```sh
./scripts/run.sh --docker         # same, in a container — needs no Go toolchain
./scripts/install.sh --docker     # or leaves it running, restarted after a reboot
```

```sh
docker compose up -d
docker compose exec sync summareader-sync pair "My library" "Desktop" --dir=/data
```

The second command prints a token once, as it does outside Docker, and every
device after the first enrols from one already paired.

The Docker path installs no systemd unit: `restart: unless-stopped` and an
enabled `docker.service` already bring the container back after a reboot, and a
unit beside compose is a second thing to keep in step.

It serves the database that is already here. `pb_data` holds the account and
every device token, so `docker-compose.override.yml` bind-mounts it rather than
letting the container start on an empty volume — a server that answers
perfectly well and knows none of your devices. The override also sets
`user: "1000:1000"`, because the image runs as uid 10001 and a bind mount it
cannot write reports `attempt to write a readonly database`, which is a
permission error wearing a misleading sentence. Change that uid to yours, or
delete the file to go back to the named volume.

Compose offers the port on `127.0.0.1` and on one named address — set
`SYNC_BIND` to the one other devices reach this machine by, or delete that
line for a machine that syncs only with itself. This server speaks plain HTTP
and holds everyone's ciphertext, so anywhere less private than a network you
control wants a TLS terminator in front of it exposed instead.

Everything worth keeping is in `./pb_data`, which is the same directory the
server uses when run without Docker, so the two are one deployment rather than
two. Set `SYNC_UID`/`SYNC_GID` if that directory is not owned by you. Losing it does not lose
anybody's library — those live on the devices — but it does lose every
device's token and the account they share, so each device would have to be
paired again.

#### Checking a deployment

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
POST /rename     the caller says what it is called
```

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

## Version pinning

PocketBase is pre-1.0 with no compatibility guarantee, so the version in
`go.mod` is exact. Upgrade deliberately and re-run the tests; the collection
API in particular changes between minor versions.

## Metrics

`GET /metrics` answers in the Prometheus text format when
`SUMMAREADER_METRICS_TOKEN` is set, and 404s when it is not. The scraper sends
it as a bearer token.

```sh
SUMMAREADER_METRICS_TOKEN=$(openssl rand -hex 24) ./summareader-sync serve
```

Accounts, devices, log entries, blobs, bytes held — in total and per account,
because "the server is full" is never the useful form of that question. Plus
memory, goroutines and uptime.

**A monitoring system does not get a device token.** One credential that can
both scrape and read the log is a monitoring system that has the library, so
this is its own secret. Everything reported is a count, a byte total or an age:
the server holds ciphertext and cannot read an entry, and a test asserts that
no payload ever appears in the output.
