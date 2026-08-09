#!/usr/bin/env bash
# Builds and serves in the foreground. For working on it — scripts/install.sh
# is for leaving it running.
#
# Bound to localhost, like the compose file and for the same reason: this
# server speaks plain HTTP and holds everyone's ciphertext, so a public
# interface means device tokens crossing the network in the clear.
set -euo pipefail

cd "$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

go build -o summareader-sync .
exec ./summareader-sync serve \
  --http="${SYNC_ADDR:-127.0.0.1:8099}" \
  --dir="${SYNC_DIR:-./pb_data}" "$@"
