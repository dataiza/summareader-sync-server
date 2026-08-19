#!/usr/bin/env bash
# Installs the sync server as a systemd *user* service, so it comes back after
# a reboot without anything being run as root. `--uninstall` reverses it.
#
# A user service and not a system one: this holds one person's ciphertext on
# one person's machine, and a unit under ~/.config needs no sudo to install,
# inspect or remove. For a shared box, copy the unit to /etc/systemd/system,
# give it a User= and drop the --user flags.
#
# `--docker` runs it as a container instead, which is the answer when there is
# no Go on the machine — the image builds the binary itself.
#
#   BIN_DIR=…   where the binary goes          (default ~/.local/bin)
#   DATA_DIR=…  where the database lives       (default: see below)
#   ADDR=…      what it listens on             (default 127.0.0.1:8099)
set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo"

# mise puts the toolchains on PATH from a login shell only, and this is a
# script somebody may well run from a hook or over ssh.
export PATH="$HOME/.local/share/mise/shims:$PATH"

name="summareader-sync"
bin_dir="${BIN_DIR:-$HOME/.local/bin}"
unit_dir="$HOME/.config/systemd/user"
addr="${ADDR:-127.0.0.1:8099}"

# Whatever is already here wins. The account and every device token live in
# this database — pointing a fresh service at an empty directory instead would
# look like the server had forgotten every device that is paired with it.
if [ -n "${DATA_DIR:-}" ]; then
  data_dir="$DATA_DIR"
elif [ -f "$repo/pb_data/data.db" ]; then
  data_dir="$repo/pb_data"
else
  data_dir="$HOME/.local/share/$name"
fi

# In a container instead. No Go, no unit, no binary: the image builds it, and
# `restart: unless-stopped` plus an enabled docker.service is what brings it
# back after a reboot — which is why there is no systemd unit here to keep in
# step with compose.
if [ "${1:-}" = "--docker" ]; then
  command -v docker >/dev/null || { echo "Docker is not installed." >&2; exit 1; }
  if [ "${2:-}" = "--uninstall" ]; then
    docker compose down
    echo "stopped. ./pb_data is kept — every device token is in it."
    exit 0
  fi

  docker compose up -d --build

  # Its own healthcheck already asks this; asking once here is the difference
  # between a script that started something and a script that saw it answer.
  for _ in $(seq 30); do
    curl -fsS "http://$addr/instance" >/dev/null 2>&1 && break
    sleep 1
  done
  curl -fsS "http://$addr/instance" >/dev/null 2>&1 \
    && echo "answering on $addr" \
    || { echo "did not answer on $addr — docker compose logs" >&2; exit 1; }

  echo
  echo "  docker compose logs -f"
  echo "  docker compose exec sync summareader-sync first-device \"My library\" \"Desktop\" --dir=/data"
  systemctl is-enabled docker >/dev/null 2>&1 \
    || echo "  sudo systemctl enable --now docker   # or it will not come back after a reboot"
  exit 0
fi

if [ "${1:-}" = "--uninstall" ]; then
  systemctl --user disable --now "$name.service" 2>/dev/null || true
  rm -f "$unit_dir/$name.service" "$bin_dir/$name"
  systemctl --user daemon-reload
  echo "removed: the service and the binary."
  echo "kept:    $data_dir — the account and every device token are in there."
  exit 0
fi

mkdir -p "$bin_dir" "$unit_dir" "$data_dir"

# Built here when there is a Go toolchain, and otherwise taken from whatever
# the last build left in the repo — a machine that runs this server does not
# have to be a machine that compiles it, and the Docker path builds elsewhere
# on purpose.
if command -v go >/dev/null 2>&1; then
  go build -o "$bin_dir/$name" .
elif [ -x "$repo/$name" ]; then
  echo "No Go toolchain — installing the binary already in $repo."
  install -m 755 "$repo/$name" "$bin_dir/$name"
else
  echo "No Go toolchain and no built binary in $repo." >&2
  echo "Install Go, or build it once: docker compose build" >&2
  exit 1
fi

sed -e "s|@BIN@|$bin_dir/$name|g" \
    -e "s|@DATA@|$data_dir|g" \
    -e "s|@ADDR@|$addr|g" \
    scripts/$name.service >"$unit_dir/$name.service"

systemctl --user daemon-reload
systemctl --user enable --now "$name.service"

echo "listening on $addr, data in $data_dir"
echo
echo "  systemctl --user status $name     # is it up"
echo "  journalctl --user -u $name -f     # what it is saying"
echo
# A user service stops at logout unless the user lingers. On a desktop that is
# rarely noticed; on a box reached over SSH it is the whole difference between
# a server and a program that ran once.
if ! loginctl show-user "$USER" --property=Linger 2>/dev/null | grep -q 'yes'; then
  echo "To keep it running when you are not logged in:"
  echo "  sudo loginctl enable-linger $USER"
fi
# Pairing needs the server stopped or the same directory — it writes the
# account the server serves.
if [ ! -s "$data_dir/data.db" ]; then
  echo
  echo "Nothing paired yet. The first device is issued from a shell:"
  echo "  $bin_dir/$name first-device \"My library\" \"Desktop\" --dir=$data_dir"
fi
