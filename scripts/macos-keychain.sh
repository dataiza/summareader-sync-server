#!/usr/bin/env bash
# A keychain that lives for one release job on a GitHub-hosted Mac.
#
#   scripts/macos-keychain.sh create MACOS_DEVID_CERT [MAC_INSTALLER_CERT ...]
#   scripts/macos-keychain.sh delete
#
# Each NAME is read as two environment variables: NAME_P12, the base64 of a
# .p12, and NAME_PASSWORD. They are the repository secrets' own names, so the
# workflow passes them through unchanged.
#
# A keychain of its own rather than the runner's login keychain, so the
# identities are gone with `delete` (an `if: always()` step) instead of
# depending on the runner being thrown away. A script rather than a step,
# because two jobs need it and the order of these calls is the whole of what
# makes signing work.
set -euo pipefail

keychain="${RUNNER_TEMP:?RUNNER_TEMP is not set}/signing.keychain-db"

case "${1:-}" in
create)
  shift
  pw="$(uuidgen)"
  security create-keychain -p "$pw" "$keychain"
  # Six hours, longer than any job: the default locks it after five minutes,
  # and a build that outlasts that fails at the signature with
  # errSecInternalComponent, which says nothing about a lock.
  security set-keychain-settings -lut 21600 "$keychain"
  security unlock-keychain -p "$pw" "$keychain"

  for name in "$@"; do
    p12="${name}_P12"
    password="${name}_PASSWORD"
    file="$RUNNER_TEMP/$name.p12"
    printf '%s' "${!p12}" | base64 --decode >"$file"
    if ! security import "$file" -k "$keychain" -P "${!password}" \
      -T /usr/bin/codesign -T /usr/bin/productbuild -T /usr/bin/security; then
      rm -f "$file"
      echo "::error::$p12 did not import. Check it is the base64 of a .p12 and that $password matches it."
      exit 1
    fi
    rm -f "$file"
  done

  # Without this, codesign asks a dialog nobody will see for leave to use the
  # key, and waits until the job times out.
  security set-key-partition-list -S apple-tool:,apple: -k "$pw" "$keychain" >/dev/null

  # The intermediates, so the chain is complete inside this keychain. Without
  # its issuer a leaf is listed as a valid identity and still cannot sign:
  # codesign says "unable to build chain to self-signed root". WWDR G3 issues
  # Apple Distribution and Mac Installer Distribution; Developer ID
  # Application is issued by the Developer ID G2 authority, not WWDR. A hosted
  # image usually has both already, so one that is there is not an error.
  for ca in AppleWWDRCAG3.cer DeveloperIDG2CA.cer; do
    curl -fsSL -o "$RUNNER_TEMP/$ca" "https://www.apple.com/certificateauthority/$ca"
    security import "$RUNNER_TEMP/$ca" -k "$keychain" \
      -T /usr/bin/codesign -T /usr/bin/security >/dev/null 2>&1 || true
    rm -f "$RUNNER_TEMP/$ca"
  done

  # First on the search list, keeping what was there: codesign and
  # productbuild find an identity by name through the list, not by a path.
  # shellcheck disable=SC2046 # the list is one quoted path per line
  security list-keychains -d user -s "$keychain" \
    $(security list-keychains -d user | tr -d '"')
  security find-identity -v -p codesigning "$keychain"
  ;;

delete)
  security delete-keychain "$keychain" 2>/dev/null || true
  ;;

*)
  sed -n '2,9p' "$0" >&2
  exit 1
  ;;
esac
