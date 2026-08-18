#!/usr/bin/env bash
# Builds every version there is, into dist/.
#
#   scripts/build.sh              # all of them
#   scripts/build.sh --headless   # only the portable ones
#   scripts/build.sh --desktop    # only the window, for this machine
#
# There are two kinds and they are not interchangeable. The headless binary is
# the server: it needs no C toolchain, so one machine builds it for every
# platform, and it is what the service and the container run. The desktop one
# is that same server plus a window, which needs cgo — so it can only be built
# on the platform it runs on, and this script builds it for this machine only.
#
# That asymmetry is the whole reason the window sits behind a build tag. Ask
# for it in one binary and the server would need a toolchain everywhere too.
set -euo pipefail

cd "$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

command -v go >/dev/null || {
  echo "No Go toolchain. Docker builds the headless one: scripts/run.sh --docker" >&2
  exit 1
}

what="${1:-all}"
out="dist"
mkdir -p "$out"

# Stripped, as the container builds it. The difference is a third of the size
# and nothing else — debug symbols in something nobody attaches a debugger to.
flags=(-trimpath -ldflags=-s\ -w)

if [ "$what" = all ] || [ "$what" = --headless ]; then
  echo "Headless — the server, for every platform:"
  for target in linux/amd64 linux/arm64 windows/amd64 darwin/arm64 darwin/amd64; do
    os="${target%%/*}"
    arch="${target##*/}"
    name="summareader-sync-$os-$arch"
    [ "$os" = windows ] && name="$name.exe"
    CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build "${flags[@]}" -o "$out/$name" .
    printf '  %-34s %s\n' "$name" "$(du -h "$out/$name" | cut -f1)"
  done
fi

if [ "$what" = all ] || [ "$what" = --desktop ]; then
  echo
  # Reported rather than assumed: someone reading the output should be able to
  # tell which machine's binary this is without knowing how the script works.
  host="$(go env GOOS)-$(go env GOARCH)"
  echo "Desktop — the window, for this machine ($host):"
  name="summareader-sync-gui-$host"
  [ "$(go env GOOS)" = windows ] && name="$name.exe"
  go build -tags gui "${flags[@]}" -o "$out/$name" .
  printf '  %-34s %s\n' "$name" "$(du -h "$out/$name" | cut -f1)"
  echo
  echo "  Only this machine's. A window needs cgo, so the other platforms need"
  echo "  either their own runner or a cross toolchain — neither is set up yet."
fi

echo
echo "In $out/. The headless one is what the service and the container run;"
echo "the desktop one is the same server with a window in front of it."
