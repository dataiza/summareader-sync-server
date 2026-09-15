# summareader-sync-server

A self-hosted server that keeps a SummaReader library in step across your own
devices.

It stores opaque ciphertext against sequence numbers and holds no keys, so it
cannot read a word of what it carries. No search, no unread counts, no web
reader — it has nothing to build them from.

There is no hosted service to sign up for. The only sync server is one you
run yourself — this one.

---

## Install

### Downloads

Built releases are on the [Releases](../../releases/latest) page.

| | For | File |
|---|---|---|
| **Headless** | a server reached over the network | `summareader-sync-headless-<os>-<arch>.tar.gz` (`.zip` on Windows) |
| **Console, Linux** | a desktop, with a window | `summareader-sync-console-linux-x64.tar.gz` |
| **Console, macOS** | a Mac, with a window | `summareader-sync-console-macos.zip` |

Headless bundles are built for `linux-amd64`, `linux-arm64`, `darwin-amd64`,
`darwin-arm64` and `windows-amd64`. Neither kind needs this repository.

### Headless bundle — systemd user service (Linux)

```sh
tar -xzf summareader-sync-headless-linux-amd64.tar.gz
cd summareader-sync-headless-linux-amd64
./install.sh
```

Defaults: `BIN_DIR=~/.local/bin`, `DATA_DIR=~/.local/share/summareader-sync`,
`ADDR=127.0.0.1:8099`. Set any of them to override.

```sh
BIN_DIR=/opt/bin DATA_DIR=/srv/sync ADDR=0.0.0.0:8099 ./install.sh
sudo loginctl enable-linger "$USER"   # keep it running after logout
./install.sh --uninstall              # keeps the database
```

`install.sh` ships only in the Linux headless bundles — systemd is a Linux
answer. Elsewhere, use the binary directly or Docker.

### Headless bundle — just the binary

```sh
tar -xzf summareader-sync-headless-<os>-<arch>.tar.gz
cd summareader-sync-headless-<os>-<arch>
./summareader-sync serve --http=127.0.0.1:8099 --dir=./pb_data
```

### Console bundle

```sh
# Linux
tar -xzf summareader-sync-console-linux-x64.tar.gz
./summareader-sync-console/summareader_sync_console
```

```sh
# macOS — unzip summareader-sync-console-macos.zip, then open the .app
```

The console carries its own copy of the server binary beside it. Nothing else
to install.

### Docker

```sh
docker compose up -d
```

Every headless bundle ships a `Dockerfile` and a `docker-compose.yml`; that
Dockerfile copies the binary rather than compiling it, so it needs no Go and no
source. From a checkout, the root `Dockerfile` compiles instead.

```sh
SYNC_BIND=10.10.20.1 SYNC_PORT=8099 docker compose up -d   # reachable from a phone
SYNC_UID=$(id -u) SYNC_GID=$(id -g) docker compose up -d   # if ./pb_data is not uid 1000
./scripts/install.sh --docker                              # from a checkout, left running
```

Data lives in `./pb_data`, bind-mounted to `/data`.

### From source (needs Go 1.26+)

```sh
git clone https://github.com/dataiza/summareader-sync-server
cd summareader-sync-server
go build -o summareader-sync .
```

```sh
./scripts/run.sh              # build and serve in the foreground
./scripts/run.sh gui          # the desktop console in front of it
./scripts/run.sh --docker     # in a container, no Go needed
./scripts/build.sh            # every platform, into dist/
```

Building the console needs Flutter — see [BUILD.md](BUILD.md).

---

## Use

### First device, and every one after it

**These are two different operations.** Once, from a shell:

```sh
./summareader-sync first-device "My library" "Desktop" --dir=./pb_data
./summareader-sync first-device "My library" "Desktop" --dir=./pb_data --json
```

It prints a token **once** and it is not recoverable. Paste it into SummaReader
on that device.

**Every device after the first joins by scanning the pairing code of an app
that is already paired.** That code carries the key that makes the library
readable. This server has never held that key, cannot hold it, and so cannot
issue such a code.

Running `first-device` again creates a second, unrelated library — it does not
add a device.

### Serve

```sh
./summareader-sync serve --http=127.0.0.1:8099 --dir=./pb_data
./summareader-sync serve --name="Study" --no-announce
./summareader-sync version
```

`--http` is the bind address, `--dir` where the database lives. Localhost by
default: this is plain HTTP carrying device tokens.

Every flag is also an environment variable (`SUMMAREADER_HTTP`,
`SUMMAREADER_DIR`, `SUMMAREADER_NAME`, `SUMMAREADER_NO_ANNOUNCE`,
`SUMMAREADER_METRICS_TOKEN`, `SUMMAREADER_OPERATOR_TOKEN`) and a key in
`summareader-sync.json` beside the data directory. Flag beats environment beats
file. See [RUNNING.md](RUNNING.md#configuration).

### Manage devices

```sh
./summareader-sync devices list
./summareader-sync devices rename <device-id> <name>
./summareader-sync devices revoke <device-id>    # stop it, reversibly
./summareader-sync devices resume <device-id>    # let it sync again
./summareader-sync devices remove <device-id>    # forget it; cannot be resumed
```

Flags on all of them: `--addr` (the running server), `--token` (the operator
token), `--json`.

These speak HTTP to the running server, so `SUMMAREADER_OPERATOR_TOKEN` must be
set on both. Without it the routes are off and the commands say so.

What a revoked or removed device already downloaded stays on it — the key is on
the device.

### The desktop console

```sh
./scripts/run.sh gui                        # from a checkout
./summareader-sync-console/summareader_sync_console --dir=./pb_data
```

![The desktop console](docs/desktop-window.png)

It starts, stops and watches the server as a child process, shows device counts
and the device list, edits the bind address, can install the systemd user unit,
and draws a QR code for the first device. Full walkthrough:
[RUNNING.md](RUNNING.md#the-desktop-console).

### Docker

```sh
docker compose up -d
docker compose exec sync summareader-sync first-device "My library" "Desktop" --dir=/data
docker compose logs -f
```

---

More: [BUILD.md](BUILD.md) (building) · [RUNNING.md](RUNNING.md) (running,
configuration, metrics, administration) · [docs/DESIGN.md](docs/DESIGN.md) (the
frozen five-operation protocol, and why `seq` is a counter row).

## Licence

GNU Affero General Public License, version 3 — the full text is in `LICENSE`,
and it ships inside every download.

Section 13 is the one that matters for a server: run a **modified** version
where other people can reach it over a network, and those people are entitled
to its source. Running an unmodified build puts no obligation on you.

This is not the licence SummaReader itself carries. The app is a separate,
proprietary program; it speaks to this over a network protocol and links none
of its code.
