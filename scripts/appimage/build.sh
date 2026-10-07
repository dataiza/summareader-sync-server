#!/usr/bin/env bash
# Packs the built console into a single AppImage.
#
#   scripts/appimage/build.sh <bundle-dir> <server-binary> <version> <out.AppImage>
#
# An AppImage and not a package, because the person installing this should need
# no repository, no package manager and no root: one file, chmod +x, run. It is
# also what makes updating possible at all — the console replaces this very
# file from a GitHub release, which it can only do because the whole program is
# one file it owns.
#
# Everything else the project ships on Linux stays as it is. The headless
# bundle is a systemd service and a container, and scripts/install.sh is the
# right answer there.
set -euo pipefail

bundle="${1:?the Flutter bundle directory}"
server="${2:?the summareader-sync binary}"
version="${3:?the version}"
out="${4:?where to write the AppImage}"

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo="$(cd "$here/../.." && pwd)"
id=sk.dataiza.summareader_sync_console

# appimagetool is downloaded rather than vendored — fifteen megabytes of
# someone else's binary does not belong in this history — and pinned by hash
# rather than by tag, because upstream publishes only a moving `continuous`
# release. A rebuild there changes the hash and this stops, loudly, which is
# the point: the alternative is silently building with a tool nobody chose.
#
# When that happens, check what changed upstream and put the new hash here.
# Last moved 2026-10-07, to the build the app's script has pinned since
# 2026-10-04: upstream's CI rebuilt `continuous` from 854e19e, whose only new
# change adds an APPIMAGETOOL_RUNTIME_FILE variable. The 1.0.0 release stopped
# here.
tool_url=https://github.com/AppImage/appimagetool/releases/download/continuous/appimagetool-x86_64.AppImage
tool_sha=95cbe7cce9717fce90c484e34052ee7c7f1d7635b33c12525b4776826a7d29b6
tool="${APPIMAGETOOL:-${XDG_CACHE_HOME:-$HOME/.cache}/summareader/appimagetool}"

if [ ! -x "$tool" ]; then
  mkdir -p "$(dirname "$tool")"
  echo "Fetching appimagetool…"
  curl -fsSL -o "$tool" "$tool_url"
  chmod +x "$tool"
fi
got="$(sha256sum "$tool" | cut -d' ' -f1)"
if [ "$got" != "$tool_sha" ]; then
  echo "appimagetool is not the pinned build." >&2
  echo "  expected $tool_sha" >&2
  echo "  got      $got" >&2
  echo "Upstream rebuilt its rolling release. Check what changed, then update" >&2
  echo "tool_sha in $0 — or set APPIMAGETOOL to a build you trust." >&2
  exit 1
fi

# The runtime too, by hash and from a dated release. Left to itself the pinned
# appimagetool fetches the type2 runtime from upstream's moving `continuous`
# release on every build, and that runtime is the ELF header of the AppImage —
# the first code that runs when the console starts, and what its self-update
# swaps in. The tool's pin did not cover it (card 711, as in the app).
runtime_url=https://github.com/AppImage/type2-runtime/releases/download/20251108/runtime-x86_64
runtime_sha=2fca8b443c92510f1483a883f60061ad09b46b978b2631c807cd873a47ec260d
runtime="${APPIMAGE_RUNTIME:-${XDG_CACHE_HOME:-$HOME/.cache}/summareader/runtime-x86_64-20251108}"

if [ ! -f "$runtime" ]; then
  mkdir -p "$(dirname "$runtime")"
  echo "Fetching the AppImage runtime…"
  curl -fsSL -o "$runtime" "$runtime_url"
fi
got="$(sha256sum "$runtime" | cut -d' ' -f1)"
if [ "$got" != "$runtime_sha" ]; then
  echo "The AppImage runtime is not the pinned build." >&2
  echo "  expected $runtime_sha" >&2
  echo "  got      $got" >&2
  echo "Delete $runtime to fetch it again, or set APPIMAGE_RUNTIME to a build" >&2
  echo "you trust." >&2
  exit 1
fi

app="$(mktemp -d)"
trap 'rm -rf "$app"' EXIT

mkdir -p "$app/usr/bin"
cp -r "$bundle/." "$app/usr/bin/"

# Beside the console's own executable, which is the first place it looks —
# see serverBinary() in console/lib/src/server.dart. Inside an AppImage that
# resolves into the mount, so the window finds its server without knowing it is
# running from an image at all.
install -m 755 "$server" "$app/usr/bin/summareader-sync"

# The licence travels with the binary, which the AGPL is not shy about. An
# image is a single file somebody downloads with no directory beside it, so
# there is nowhere else for it to be — usr/share/doc is where a package would
# put it and is where anyone looking will look.
mkdir -p "$app/usr/share/doc/$id"
install -m 644 "$repo/LICENSE" "$app/usr/share/doc/$id/LICENSE"

install -m 755 "$here/AppRun" "$app/AppRun"
install -m 644 "$here/$id.desktop" "$app/$id.desktop"

# The icon ladder, out of the macOS bundle, which is the only artwork in this
# repository. 48 is the one size freedesktop actually asks for and the one the
# ladder is missing, so it is scaled here rather than committed — a generated
# PNG in the tree is a file nobody can regenerate when the source changes.
icons="$repo/console/macos/Runner/Assets.xcassets/AppIcon.appiconset"
for size in 16 32 64 128 256 512; do
  dir="$app/usr/share/icons/hicolor/${size}x${size}/apps"
  mkdir -p "$dir"
  install -m 644 "$icons/app_icon_$size.png" "$dir/$id.png"
done
# 48 is the one size the ladder lacks and the one the freedesktop spec names,
# so it is scaled here. Skipped rather than fatal when there is no ImageMagick:
# hicolor lookup falls back to the nearest size, so the icon is a little softer
# on a panel that wanted 48 and nothing else is wrong. CI installs the tool, so
# a released image always has it; a contributor building locally without it
# gets a working AppImage and a line saying what is missing.
# `magick` or `convert`, and the difference is not cosmetic: ImageMagick 7
# renamed the tool, and Debian and Ubuntu still ship version 6, whose package
# provides `convert` and no `magick` at all. Looking only for `magick` is how
# the first release came out with no 48 — the build did not fail, it took the
# branch below and said so in a log nobody was reading.
#
# `candidate` and not `tool`: that name already holds the path to
# appimagetool, and reusing it here silently turned the packaging step into
# `convert --no-appstream AppDir`, which fails complaining about a decode
# delegate for a command-line flag.
resize=""
for candidate in magick convert; do
  if command -v "$candidate" >/dev/null 2>&1; then resize="$candidate"; break; fi
done

if [ -n "$resize" ]; then
  mkdir -p "$app/usr/share/icons/hicolor/48x48/apps"
  "$resize" "$icons/app_icon_512.png" -resize 48x48 \
    "$app/usr/share/icons/hicolor/48x48/apps/$id.png"
else
  echo "  no ImageMagick, so no 48x48 icon — the desktop will scale one" >&2
fi

# appimagetool wants the icon at the root as well, named after the desktop
# file, or it refuses the AppDir.
install -m 644 "$app/usr/share/icons/hicolor/256x256/apps/$id.png" "$app/$id.png"

# No update information embedded. That field is for zsync, which would mean
# publishing a .zsync beside every release and carrying a client; the console
# asks GitHub for the newest release and replaces this file itself, which is
# one moving part instead of three.
# APPIMAGE_EXTRACT_AND_RUN, because appimagetool is itself an AppImage and a
# GitHub runner has no libfuse2 to mount one with. Unset, the build fails there
# with "dlopen(): error loading libfuse.so.2", which reads as a problem with
# this project rather than with the tool. Set, it unpacks itself to a temporary
# directory and runs from there — slower by a second, works everywhere.
rm -f "$out"
APPIMAGE_EXTRACT_AND_RUN=1 ARCH=x86_64 VERSION="$version" \
  "$tool" --no-appstream --runtime-file "$runtime" "$app" "$out" >/dev/null
chmod 755 "$out"
echo "  $(basename "$out")  $(du -h "$out" | cut -f1)"
