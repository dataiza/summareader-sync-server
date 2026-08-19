#!/usr/bin/env bash
# Builds and serves in the foreground. For working on it — scripts/install.sh
# is for leaving it running.
#
#   scripts/run.sh              # needs a Go toolchain
#   scripts/run.sh gui          # the desktop console, which is a Flutter app
#   scripts/run.sh --docker     # needs Docker, and nothing else
#
# Bound to localhost, like the compose file and for the same reason: this
# server speaks plain HTTP and holds everyone's ciphertext, so a public
# interface means device tokens crossing the network in the clear.
set -euo pipefail

cd "$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# --docker exists so this can be run on a machine with no Go on it at all.
# `install.sh --docker` is the other half of that: this one is the foreground,
# Ctrl-C, try-it version, and that one leaves a container behind that restarts
# with the machine.
if [ "${1:-}" = "--docker" ]; then
  shift
  command -v docker >/dev/null || { echo "Docker is not installed." >&2; exit 1; }

  # Same ./pb_data as the native path, so switching between the two is not a
  # change of library. The container writes as this user to make that work.
  SYNC_UID="$(id -u)" SYNC_GID="$(id -g)"
  export SYNC_UID SYNC_GID

  # No -d: the point of this script is a process you can watch and stop. The
  # container is removed on the way out rather than left stopped.
  exec docker compose up --build --abort-on-container-exit "$@"
fi

# The console is a Flutter application in console/, not a subcommand of the
# server — the window that used to be one was Fyne inside this binary, and
# taking it out is what gave the server back its cgo-free cross-compile. `gui`
# survives as the word somebody types.
#
# It is handed the same address and directory this script serves on, spelled
# with the same two flags, because a console that opened the default database
# in the home directory would report an empty server and be right about the
# wrong one. SUMMAREADER_SYNC_BIN is how it finds the server to start: it is a
# separate program now and cannot assume it is standing next to one.
if [ "${1:-}" = "gui" ]; then
  shift
  dir="${SYNC_DIR:-$PWD/pb_data}"

  # The address is passed *only* when somebody said one. It used to be passed
  # always, defaulted to loopback — and a flag beats the config file by
  # design, so the address chosen in the window was written to the file,
  # ignored at the next launch, and looked like a console that forgets. The
  # directory is still passed, because that is what selects this checkout's
  # library rather than the one in ~/.config; the address then comes from the
  # config inside it, which is the point of keeping one directory per
  # installation.
  addr=("${SYNC_ADDR:+--http=$SYNC_ADDR}")

  # Built first, and only if Go is here: without a binary the console opens
  # with Start greyed out and a line saying what is missing, which is honest
  # but a poor thing to hand somebody who has a toolchain.
  if command -v go >/dev/null && [ ! -x ./summareader-sync ]; then
    go build -o summareader-sync .
  fi
  [ -x ./summareader-sync ] && export SUMMAREADER_SYNC_BIN="${SUMMAREADER_SYNC_BIN:-$PWD/summareader-sync}"

  case "$(uname -s)" in
    Darwin) bundle="console/build/macos/Build/Products/Release/summareader_sync_console.app/Contents/MacOS/summareader_sync_console"; device=macos ;;
    *)      bundle="console/build/linux/x64/release/bundle/summareader_sync_console"; device=linux ;;
  esac

  # The release build when there is one: it starts at once and needs no
  # toolchain. Otherwise the development run, which needs Flutter.
  if [ -x "$bundle" ]; then
    exec "$bundle" --dir="$dir" "${addr[@]}" "$@"
  fi
  command -v flutter >/dev/null || {
    echo "No Flutter, and no console built yet." >&2
    echo "  ./scripts/build.sh --console" >&2
    echo "…or install Flutter 3.47 and run this again." >&2
    exit 1
  }
  cd console
  args=(--dart-entrypoint-args="--dir=$dir")
  [ -n "${SYNC_ADDR:-}" ] &&
    args+=(--dart-entrypoint-args="--http=$SYNC_ADDR")
  exec flutter run -d "$device" --release "${args[@]}" "$@"
fi

command -v go >/dev/null || {
  echo "No Go toolchain. scripts/run.sh --docker needs only Docker." >&2
  exit 1
}

go build -o summareader-sync .
exec ./summareader-sync serve \
  --http="${SYNC_ADDR:-127.0.0.1:8099}" \
  --dir="${SYNC_DIR:-./pb_data}" "$@"
