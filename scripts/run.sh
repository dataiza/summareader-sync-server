#!/usr/bin/env bash
# Builds and serves in the foreground. For working on it — scripts/install.sh
# is for leaving it running.
#
#   scripts/run.sh              # needs a Go toolchain
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

command -v go >/dev/null || {
  echo "No Go toolchain. scripts/run.sh --docker needs only Docker." >&2
  exit 1
}

go build -o summareader-sync .
exec ./summareader-sync serve \
  --http="${SYNC_ADDR:-127.0.0.1:8099}" \
  --dir="${SYNC_DIR:-./pb_data}" "$@"
