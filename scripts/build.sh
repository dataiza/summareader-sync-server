#!/usr/bin/env bash
# Builds every version there is, into dist/.
#
#   scripts/build.sh              # all of them
#   scripts/build.sh --headless   # only the server, for every platform
#   scripts/build.sh --console    # only the desktop console, for this machine
#
# There are two programs here and they are not the same thing. The server is
# the Go binary: no cgo, no build tags, so one machine builds it for five
# platforms with no C compiler anywhere, and it is what the service and the
# container run. The console is a Flutter app that supervises it — a separate
# process, talking to the server over the same argv the systemd unit uses and
# the same two HTTP endpoints. A window needs a toolchain per platform, so the
# console is built for the machine it is built on, and the server no longer
# pays anything for its existence.
set -euo pipefail

cd "$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

what="${1:-all}"
out="dist"
mkdir -p "$out"

if [ "$what" = all ] || [ "$what" = --headless ]; then
  command -v go >/dev/null || {
    echo "No Go toolchain. Docker builds the server: scripts/run.sh --docker" >&2
    exit 1
  }

  # Stripped, as the container builds it. The difference is a third of the size
  # and nothing else — debug symbols in something nobody attaches a debugger to.
  flags=(-trimpath -ldflags=-s\ -w)

  echo "The server, for every platform:"
  for target in linux/amd64 linux/arm64 windows/amd64 darwin/arm64 darwin/amd64; do
    os="${target%%/*}"
    arch="${target##*/}"
    name="summareader-sync-$os-$arch"
    [ "$os" = windows ] && name="$name.exe"
    CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build "${flags[@]}" -o "$out/$name" .
    printf '  %-34s %s\n' "$name" "$(du -h "$out/$name" | cut -f1)"
  done
fi

if [ "$what" = all ] || [ "$what" = --console ]; then
  echo
  if ! command -v flutter >/dev/null; then
    echo "No Flutter toolchain, so no console. The server above is complete"
    echo "on its own — the console is a window in front of it, not part of it."
    exit 0
  fi

  # Reported rather than assumed: someone reading the output should be able to
  # tell which machine's build this is without knowing how the script works.
  # Flutter's name for the platform, not the kernel's: `uname -s` says darwin
  # and `flutter build darwin` is not a command.
  case "$(uname -s)" in
    Darwin) host=macos ;;
    Linux) host=linux ;;
    *) host=windows ;;
  esac
  echo "The desktop console, for this machine ($host):"
  (cd console && flutter build "$host" --release)
  echo "  console/build/$host/x64/release/bundle"
  echo
  echo "  Only this machine's. A window needs a toolchain per platform, and"
  echo "  the other two need either their own runner or a cross setup —"
  echo "  neither is set up yet."
fi

echo
echo "In $out/. The server is what the service and the container run; the"
echo "console is a separate program that starts, stops and watches one."
