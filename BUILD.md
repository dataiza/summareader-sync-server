# Building it

Two kinds of binary, and they are not interchangeable.

| | Headless | Desktop |
|---|---|---|
| what it is | the server | the same server, plus a window |
| needs | nothing but Go | Go **and a C toolchain** |
| builds for | every platform, from one machine | only the machine that builds it |
| size | ~33 MB, ~24 MB stripped | ~52 MB |
| built by | `go build .` | `go build -tags gui .` |

That asymmetry is the whole reason the window sits behind a build tag. Ask for
it in one binary and the server would need a C compiler everywhere too.

## Everything at once

```sh
./scripts/build.sh              # every version there is, into dist/
./scripts/build.sh --headless   # only the portable ones
./scripts/build.sh --desktop    # only the window, for this machine
```

Build into `dist/` and run it from there. A binary built without the tag
answers `gui` by printing how to build the other one rather than opening
anything — which is correct, and also means a stale binary lying around in the
repository root answers the same way.

## Headless — by hand

```sh
go build -o summareader-sync .
```

**`CGO_ENABLED=0` is what makes it easy to run anywhere.** One machine
cross-compiles every download, and whoever runs it installs nothing first:

```sh
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w" \
  -o summareader-sync-darwin-arm64 .
```

`linux/amd64`, `linux/arm64`, `windows/amd64`, `darwin/arm64` and
`darwin/amd64` all work from any of them. `-s -w` drops the debug information,
which is a third of the size and nothing else.

## Desktop — by hand

```sh
go build -tags gui -o summareader-sync-gui .
```

Needs cgo because the toolkit does, so this one is built on the platform it
runs on. On Debian and Ubuntu that means `libgl1-mesa-dev xorg-dev`; on Fedora,
`mesa-libGL-devel libXcursor-devel libXrandr-devel libXinerama-devel libXi-devel`.

Honest about the state: **the desktop build has been run on Linux only.**
Nothing produces the Windows and macOS builds yet — no workflow, no release.
The code has no platform-specific parts and the toolkit supports all three, but
neither of those is the same as having run it. The one thing that is
deliberately Linux-only is the window's "Start at login" switch, which writes a
systemd user unit; it is hidden on the other two.

## No Go toolchain at all

Docker builds the headless one for you — the image compiles it:

```sh
docker compose build
./scripts/run.sh --docker
```

## Tests

```sh
go test ./...              # the server
go test -tags gui ./...    # and the window
```

`docs/desktop-window.png` — the screenshot in [RUNNING.md](RUNNING.md) — is
drawn by `go test -tags gui`, not photographed. Fyne's software painter renders
the same widget tree without a display, so the picture is regenerated whenever
the window changes rather than aging quietly, and `git status` after a test run
is the review of that change.

`./scripts/smoke.sh` is the other half: twelve checks against a real container,
covering everything around the handlers that can be broken while every unit
test passes. See [RUNNING.md](RUNNING.md#checking-a-deployment).

## Version pinning

PocketBase is pre-1.0 with no compatibility guarantee, so the version in
`go.mod` is exact. Upgrade deliberately and re-run the tests; the collection
API in particular changes between minor versions.
