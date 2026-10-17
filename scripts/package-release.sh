#!/usr/bin/env bash
# Everything a release repository is given, built into out/.
#
#   scripts/package-release.sh headless          # five platforms, one machine
#   scripts/package-release.sh console linux
#   scripts/package-release.sh console macos         # the .dmg
#   scripts/package-release.sh console macos-store   # the App Store .pkg
#
# On a Mac, MACOS_SIGN_IDENTITY signs the darwin headless binaries and the
# .dmg's app with that Developer ID identity; unset, both are left unsigned
# (the app ad-hoc), which is enough to try a build on a desk. The App Store
# build cannot be made unsigned and says what it needs. Notarizing is the
# release workflow's, because it needs the App Store Connect key.
#
# BUILD_NUMBER is the console's CFBundleVersion; see release.yml for why it is
# the run number.
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

# Developer ID signing with the hardened runtime and a secure timestamp: the
# three things notarization checks for. No entitlements — the download is not
# sandboxed and a Go binary needs nothing from the runtime it refuses.
devid_sign() {
  codesign --force --options runtime --timestamp \
    --sign "$MACOS_SIGN_IDENTITY" "$@"
}

# The server for the console's bundle: both architectures in one file,
# because the Flutter app beside it is universal and a helper that is not
# would leave an Intel Mac with a window and no server.
universal_server() {
  local dest="$1"
  build_server darwin arm64 "$dest.arm64"
  build_server darwin amd64 "$dest.amd64"
  lipo -create -output "$dest" "$dest.arm64" "$dest.amd64"
  rm -f "$dest.arm64" "$dest.amd64"
}

# Everything in Contents/Frameworks, signed one by one before the app.
# Inside out, and not with --deep: --deep signs nested code with the app's
# own options and entitlements, which is wrong for both kinds of build here.
sign_frameworks() {
  local app="$1"
  shift
  local nested
  for nested in "$app"/Contents/Frameworks/*.framework "$app"/Contents/Frameworks/*.dylib; do
    [ -e "$nested" ] || continue
    codesign --force "$@" "$nested"
  done
}

# Builds the console and puts the server inside it, at a name that reads as
# the product rather than as the Flutter package. Prints the bundle's path.
macos_app() {
  (cd console && flutter pub get >&2 &&
    flutter build macos --release ${BUILD_NUMBER:+--build-number="$BUILD_NUMBER"} >&2)
  local built
  built="$(find console/build/macos/Build/Products/Release -maxdepth 1 -name '*.app' | head -1)"
  [ -n "$built" ] || { echo "no .app was built" >&2; exit 1; }
  local work=build-macos
  rm -rf "$work"
  mkdir -p "$work"
  local app="$work/SummaReader Sync.app"
  # ditto keeps the bundle's symlinks (the frameworks' Versions/Current)
  # where cp -r would turn them into copies and break their signatures.
  ditto "$built" "$app"
  # Contents/MacOS, beside the console's own executable, which is the first
  # place serverBinary() looks — and inside the bundle, which is the only
  # place a sandboxed app may start a program from.
  universal_server "$app/Contents/MacOS/$name"
  echo "$app"
}

case "$what" in
headless)
  # HEADLESS_TARGETS narrows the list, for the Mac job that rebuilds only the
  # two darwin bundles in order to sign them.
  for target in ${HEADLESS_TARGETS:-linux/amd64 linux/arm64 darwin/arm64 darwin/amd64 windows/amd64}; do
    goos="${target%%/*}"
    goarch="${target##*/}"
    dir="$name-headless-$goos-$goarch"
    rm -rf "$dir"
    mkdir "$dir"

    exe="$name"
    [ "$goos" = windows ] && exe="$name.exe"
    build_server "$goos" "$goarch" "$dir/$exe"
    if [ "$goos" = darwin ] && [ -n "${MACOS_SIGN_IDENTITY:-}" ]; then
      devid_sign "$dir/$exe"
    fi

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
  [ -n "$host" ] || { echo "which platform: linux, macos or macos-store" >&2; exit 1; }
  command -v flutter >/dev/null || { echo "No Flutter toolchain." >&2; exit 1; }

  case "$host" in
  linux)
    (cd console && flutter pub get && flutter build linux --release)
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
    app="$(macos_app)"
    if [ -n "${MACOS_SIGN_IDENTITY:-}" ]; then
      # The helper before the app: sealing the app records the helper's
      # signature, so signing the helper afterwards breaks the app's.
      sign_frameworks "$app" --options runtime --timestamp --sign "$MACOS_SIGN_IDENTITY"
      devid_sign "$app/Contents/MacOS/$name"
      # No --entitlements, so the sandbox flutter build signed in is dropped.
      # Not sandboxed, this console finds the library a headless server on
      # the same Mac keeps in ~/Library/Application Support, and the
      # directory box can name any folder.
      devid_sign "$app"
    else
      codesign --force --deep --sign - "$app"
    fi

    # A disk image with the app and a link to /Applications, the layout
    # somebody expects to drag across.
    stage=build-macos/dmg
    mkdir -p "$stage"
    ditto "$app" "$stage/$(basename "$app")"
    ln -sfn /Applications "$stage/Applications"
    dmg="$out/SummaReaderSync-$version.dmg"
    rm -f "$dmg"
    hdiutil create -volname "SummaReader Sync" -srcfolder "$stage" \
      -fs HFS+ -format UDZO -ov "$dmg" >/dev/null
    if [ -n "${MACOS_SIGN_IDENTITY:-}" ]; then
      codesign --force --timestamp --sign "$MACOS_SIGN_IDENTITY" "$dmg"
    fi
    ;;
  macos-store)
    # Apple Distribution for the app, Mac Installer Distribution for the
    # package, the profile that ties the bundle id to the team.
    for need in MACOS_STORE_IDENTITY MACOS_INSTALLER_IDENTITY MACOS_PROFILE APPLE_TEAM_ID; do
      [ -n "${!need:-}" ] || { echo "$need is not set; an App Store build has to be signed" >&2; exit 1; }
    done
    app="$(macos_app)"
    cp "$MACOS_PROFILE" "$app/Contents/embedded.provisionprofile"

    # Release.entitlements plus the two keys a profile is matched on. Signed
    # by hand rather than through an Xcode archive, so Xcode never adds them,
    # and TestFlight refuses a Mac build whose application identifier does
    # not match its profile's.
    bundle_id="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' "$app/Contents/Info.plist")"
    entitlements=build-macos/store.entitlements
    cp console/macos/Runner/Release.entitlements "$entitlements"
    # PlistBuddy, not plutil: plutil reads the dots in these key names as a
    # path through nested dictionaries and stops at "com". The first TestFlight
    # upload, 1.1.0, failed exactly there.
    /usr/libexec/PlistBuddy -c "Add :com.apple.application-identifier string $APPLE_TEAM_ID.$bundle_id" "$entitlements"
    /usr/libexec/PlistBuddy -c "Add :com.apple.developer.team-identifier string $APPLE_TEAM_ID" "$entitlements"

    sign_frameworks "$app" --sign "$MACOS_STORE_IDENTITY"
    # Sandbox and inherit only, or the sandbox kills the server at launch.
    codesign --force --sign "$MACOS_STORE_IDENTITY" \
      --entitlements console/macos/Runner/Helper.entitlements "$app/Contents/MacOS/$name"
    codesign --force --sign "$MACOS_STORE_IDENTITY" \
      --entitlements "$entitlements" "$app"
    codesign --verify --strict --deep "$app"

    pkg="$out/SummaReaderSync-$version.pkg"
    productbuild --component "$app" /Applications \
      --sign "$MACOS_INSTALLER_IDENTITY" "$pkg"
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
