#!/usr/bin/env bash
# Everything a release repository is given, built into out/.
#
#   scripts/package-release.sh headless          # five platforms, one machine
#   scripts/package-release.sh console linux
#   scripts/package-release.sh console macos
#
# A script and not a workflow step, so it can be run — and got wrong — here
# rather than on a tag. A release workflow is the worst place to discover that
# a heredoc was indented.
#
# The three kinds are separate downloads on purpose. A headless bundle is
# everything a server needs and no window; a console bundle is a window that
# carries its own server. Somebody installing on a box over ssh and somebody
# double-clicking on a laptop want different files, not the same file with
# instructions about which half to ignore.
set -euo pipefail

cd "$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

what="${1:-}"
out="out"
mkdir -p "$out"

name=summareader-sync
version="$(sed -n 's/^const version = "\(.*\)"$/\1/p' version.go)"
[ -n "$version" ] || { echo "version.go has no version constant" >&2; exit 1; }

# Stripped, as the container builds it: a third of the size, and nothing else
# different. -trimpath keeps the building machine's paths out of the binary.
build_server() {
  local goos="$1" goarch="$2" dest="$3"
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
    go build -trimpath -ldflags="-s -w" -o "$dest" .
}

case "$what" in
headless)
  for target in linux/amd64 linux/arm64 darwin/arm64 darwin/amd64 windows/amd64; do
    goos="${target%%/*}"
    goarch="${target##*/}"
    dir="$name-headless-$goos-$goarch"
    rm -rf "$dir"
    mkdir "$dir"

    exe="$name"
    [ "$goos" = windows ] && exe="$name.exe"
    build_server "$goos" "$goarch" "$dir/$exe"

    cp LICENSE CHANGELOG.md "$dir/"
    # Docker is an answer on every platform, so these travel with all of them.
    cp scripts/release/Dockerfile scripts/release/docker-compose.yml "$dir/"

    cat scripts/release/README.head.md >"$dir/README.md"
    if [ "$goos" = linux ]; then
      # systemd is a Linux answer. Elsewhere the bundle is the binary and the
      # container; shipping an installer that cannot work would be worse than
      # shipping none, and the README must not describe one either.
      cp scripts/install.sh "$dir/"
      cp "scripts/$name.service" "$dir/"
      cat scripts/release/README.systemd.md >>"$dir/README.md"
    fi
    cat scripts/release/README.tail.md >>"$dir/README.md"

    if [ "$goos" = windows ]; then
      (cd "$dir" && zip -qr "../$out/$dir.zip" .)
    else
      tar -czf "$out/$dir.tar.gz" "$dir"
    fi
    rm -rf "$dir"
    printf '  %-44s %s\n' "$dir" "$(du -h "$out/$dir".* | cut -f1)"
  done
  ;;

console)
  host="${2:-}"
  [ -n "$host" ] || { echo "which platform: linux or macos" >&2; exit 1; }
  command -v flutter >/dev/null || { echo "No Flutter toolchain." >&2; exit 1; }

  (cd console && flutter pub get && flutter build "$host" --release)

  case "$host" in
  linux)
    dir="$name-console"
    rm -rf "$dir"
    # Renamed from Flutter's `bundle`, because a tarball that unpacks into a
    # directory called `bundle` says nothing about what it is.
    cp -r console/build/linux/x64/release/bundle "$dir"
    # Beside the console's own executable, which is the first place it looks.
    # A window that cannot find a server is a window with nothing to show.
    build_server linux amd64 "$dir/$name"
    cp LICENSE CHANGELOG.md "$dir/"
    tar -czf "$out/$name-console-linux-x64.tar.gz" "$dir"

    # And the same thing as one file.
    #
    # The AppImage is what a person downloads: chmod +x, run, no repository and
    # no package manager. The tarball stays for anyone packaging this
    # themselves or unpacking it where a fuse mount will not work.
    #
    # Only the AppImage can update itself — it is one file the console owns, so
    # replacing it is a rename. Unpacked into a directory there is nothing to
    # replace, and the console hides that control accordingly.
    # The raw Flutter bundle, not $dir — that one has the LICENSE and the
    # CHANGELOG copied in beside the binaries for the tarball, and neither
    # belongs in usr/bin. build.sh puts the licence in usr/share/doc itself.
    scripts/appimage/build.sh \
      console/build/linux/x64/release/bundle \
      "$dir/$name" \
      "$version" \
      "$out/SummaReaderSync-$version-x86_64.AppImage"

    rm -rf "$dir"
    ;;
  macos)
    app="$(find console/build/macos/Build/Products/Release -maxdepth 1 -name '*.app' | head -1)"
    [ -n "$app" ] || { echo "no .app was built" >&2; exit 1; }
    # Contents/MacOS is what sits beside the console's executable in a bundle.
    build_server darwin arm64 "$app/Contents/MacOS/$name"
    # ditto rather than zip: it preserves the bundle bit and the resource
    # forks that make a .app openable after somebody unzips it.
    rm -f "$out/$name-console-macos.zip"
    ditto -c -k --sequesterRsrc --keepParent "$app" "$out/$name-console-macos.zip"
    ;;
  *)
    echo "no runner builds a $host console" >&2
    exit 1
    ;;
  esac
  printf '  console for %s\n' "$host"
  ;;

*)
  sed -n '2,8p' "$0" >&2
  exit 1
  ;;
esac

echo
echo "$version, in $out/."
