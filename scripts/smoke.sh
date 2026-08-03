#!/usr/bin/env bash
# Stands the server up in Docker and drives the whole contract against it.
#
# Unit tests already cover the handlers. What they cannot cover is the thing
# most likely to be wrong in a deployment: that the image builds, that the
# server starts inside it, that the volume keeps data across a restart, and
# that a device paired through `docker compose exec` can then actually sync.
# Every one of those has a way to be broken while every test passes.
#
#   ./scripts/smoke.sh            # build, test, and leave the stack running
#   ./scripts/smoke.sh --clean    # and tear it down, volume included
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

base="http://127.0.0.1:8099"
pass=0

say() { printf '  %s\n' "$*"; }
ok()  { pass=$((pass + 1)); printf '  ✓ %s\n' "$*"; }
die() { printf '  ✗ %s\n' "$*" >&2; exit 1; }

# Every check is "did we get the answer we expected", not "did curl exit 0".
# A server that answers 401 to everything would pass the second and fail the
# users.
expect() {
  local what="$1" got="$2" want="$3"
  case "$got" in
    *"$want"*) ok "$what" ;;
    *) die "$what — wanted '$want', got: $got" ;;
  esac
}

echo "Building and starting…"
docker compose up -d --build >/dev/null

echo "Waiting for it to answer…"
for _ in $(seq 1 30); do
  if curl -fsS --max-time 2 "$base/instance" >/dev/null 2>&1; then break; fi
  sleep 1
done

echo
echo "The server:"
expect "answers /instance without a token" \
  "$(curl -fsS "$base/instance")" "allreader-sync-server"

# Pairing is a shell command by necessity: until one device has a token there
# is nobody who could authorise issuing one.
paired="$(docker compose exec -T sync \
  allreader-sync pair "Smoke test" "First device" --dir=/data --json)"
token="$(echo "$paired" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')"
[ -n "$token" ] || die "pairing produced no token: $paired"
ok "pairs a first device from the shell"

auth=(-H "Authorization: Bearer $token" -H 'content-type: application/json')

echo
echo "The five operations:"
expect "append returns a sequence number" \
  "$(curl -fsS -X POST "$base/append" "${auth[@]}" -d '{"payload":"one"}')" \
  '"seq":1'
expect "and the next one moves it on" \
  "$(curl -fsS -X POST "$base/append" "${auth[@]}" -d '{"payload":"two"}')" \
  '"seq":2'
expect "readFrom returns what was appended" \
  "$(curl -fsS "$base/from/0" "${auth[@]}")" '"payload":"one"'
expect "putBlob accepts a blob" \
  "$(curl -fsS -X PUT "$base/blob/smoke" "${auth[@]}" -d '{"payload":"bytes"}')" \
  '"name":"smoke"'
expect "getBlob returns it" \
  "$(curl -fsS "$base/blob/smoke" "${auth[@]}")" '"payload":"bytes"'

echo
echo "Who may sync:"
expect "an unauthenticated append is refused" \
  "$(curl -sS -X POST "$base/append" -H 'content-type: application/json' \
      -d '{"payload":"nope"}')" "unknown or revoked device"
expect "a second device enrols without a shell" \
  "$(curl -fsS -X POST "$base/enroll" "${auth[@]}" -d '{"label":"Second"}')" \
  '"label":"Second"'
expect "devices are listed without their tokens" \
  "$(curl -fsS "$base/devices" "${auth[@]}")" '"token":""'

echo
echo "Across a restart:"
docker compose restart >/dev/null
for _ in $(seq 1 30); do
  if curl -fsS --max-time 2 "$base/instance" >/dev/null 2>&1; then break; fi
  sleep 1
done
expect "the log survived" "$(curl -fsS "$base/from/0" "${auth[@]}")" \
  '"payload":"one"'
expect "and so did the blob" "$(curl -fsS "$base/blob/smoke" "${auth[@]}")" \
  '"payload":"bytes"'

echo
echo "$pass checks passed."

if [ "${1:-}" = "--clean" ]; then
  say "tearing down, volume included"
  docker compose down -v >/dev/null
else
  say "left running — ./scripts/smoke.sh --clean to remove it and its data"
fi
