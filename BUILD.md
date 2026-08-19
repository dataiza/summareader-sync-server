# Building it

Two programs, and only one of them is the server.

| | The server | The console |
|---|---|---|
| what it is | the whole thing — the service and the container run this | a window that starts, stops and watches one |
| needs | nothing but Go | Flutter 3.47, and that platform's own toolchain |
| builds for | five platforms, from one machine | only the machine that builds it |
| size | ~24 MB stripped | ~23 MB, as a bundle |
| built by | `go build .` | `cd console && flutter build linux` |

**The server has no build tags and needs no C compiler.** It used to: the
window was a Fyne widget tree inside the same binary, behind a `gui` tag,
because a toolkit needs cgo and the server must not. Moving the window out to
Flutter took the last reason for cgo with it. `scripts/build.sh` now produces
five `CGO_ENABLED=0` targets with no tags and no conditions — one machine
builds every download, and whoever runs it installs nothing first. That is
less to explain and less to get wrong, which is the point.

## Everything at once

```sh
./scripts/build.sh              # both, into dist/
./scripts/build.sh --headless   # only the server, for every platform
./scripts/build.sh --console    # only the console, for this machine
```

A machine with no Flutter still builds the server; the script says so and
stops rather than failing.

## The server — by hand

```sh
go build -o summareader-sync .
```

`CGO_ENABLED=0` is what makes it easy to run anywhere:

```sh
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w" \
  -o summareader-sync-darwin-arm64 .
```

`linux/amd64`, `linux/arm64`, `windows/amd64`, `darwin/arm64` and
`darwin/amd64` all work from any of them. `-s -w` drops the debug information,
which is a third of the size and nothing else.

## The console — by hand

```sh
cd console
flutter run -d linux            # from source, while working on it
flutter build linux --release   # into build/linux/x64/release/bundle
```

Honest about the state: **the console has been built and run on Linux only.**
The `linux`, `windows` and `macos` scaffolding is committed and the code has no
platform-specific parts, but neither of those is the same as having run it.
Nothing produces the other two yet — a window needs a toolchain per platform,
and that means either a runner each or a cross setup, and there is neither.
The one deliberately Linux-only thing is "Start at login", which writes a
systemd user unit; the row is absent elsewhere.

**The console finds the server binary rather than containing one**:
`SUMMAREADER_SYNC_BIN` if it is set, else a `summareader-sync` beside the
console itself, else one on `PATH`. Nothing packages the two together yet, so
on a machine built from this repository, point the variable at `dist/`. When
there is no server binary anywhere, the console says so in its status line
instead of offering a Start button that quietly does nothing.

## The look, copied from the app

`console/packages/summareader_ui/` is a copy of the app repository's package —
the same fonts, colours and widgets, so the console looks like the thing it
serves. `scripts/sync-ui.sh` refreshes it from a checkout of the app, `../summareader`
unless you pass another path.

A copy rather than a pubspec git dependency because resolving one means pub
reading that repository's metadata, and the token on the build machine cannot:
CI would need a credential nobody has issued. The cost of a copy is drift,
which `console/test/vendored_ui_test.dart` catches — it fails on a machine that
has the app checked out beside this one, and skips where there is nothing to
compare against.

## No Go toolchain at all

Docker builds the server for you — the image compiles it:

```sh
docker compose build
./scripts/run.sh --docker
```

## Tests

```sh
go test ./...                   # the server
cd console && flutter test      # the console
```

`docs/desktop-window.png` — the screenshot in [RUNNING.md](RUNNING.md) — is a
Flutter golden, drawn by a widget test with no display involved, not
photographed. It is regenerated whenever the window changes rather than ageing
quietly:

```sh
cd console && flutter test --update-goldens --tags golden
```

`git status` after that is the review of the change. CI runs
`flutter test --exclude-tags golden`, because a golden compares pixels and
font rendering differs between the machine that drew the picture and the one
checking it. The `console` job in `.github/workflows/ci.yml` runs the analyzer,
the formatter and the rest of the suite.

`./scripts/smoke.sh` is the other half: twelve checks against a real container,
covering everything around the handlers that can be broken while every unit
test passes. See [RUNNING.md](RUNNING.md#checking-a-deployment).

## Version pinning

PocketBase is pre-1.0 with no compatibility guarantee, so the version in
`go.mod` is exact. Upgrade deliberately and re-run the tests; the collection
API in particular changes between minor versions.
