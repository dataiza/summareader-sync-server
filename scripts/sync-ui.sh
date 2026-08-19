#!/usr/bin/env bash
# Re-copies the look — the summareader_ui package and the two font files it
# names — out of a checkout of the app.
#
#   scripts/sync-ui.sh              # from ../summareader
#   scripts/sync-ui.sh ~/src/app    # from somewhere else
#
# A copy rather than a pubspec git dependency because pub has to read that
# repository's metadata to resolve one, and the credential on this machine
# cannot: a CI box would need a token nobody has issued yet. The cost of the
# copy is that it can drift, which is what console/test/vendored_ui_test.dart
# is for — it fails, on a machine that has the app checked out beside this
# one, the moment the two differ.
#
# The fonts come along because the package names Caprasimo and Figtree and
# ships neither. Without them the console draws in whatever the platform
# reaches for, which is the exact failure this whole exercise exists to avoid.
set -euo pipefail

cd "$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

source="${1:-../summareader}"

if [ ! -d "$source/packages/summareader_ui" ]; then
  echo "No checkout of summareader at $source." >&2
  echo "It is not fetched automatically — clone it beside this repository," >&2
  echo "or pass where it is: scripts/sync-ui.sh /path/to/summareader" >&2
  exit 1
fi

# Deleted first, so a file removed upstream is removed here too. A sync that
# only ever adds leaves the widget somebody deleted still compiling.
rm -rf console/packages/summareader_ui/lib
mkdir -p console/packages/summareader_ui console/assets/fonts

cp -R "$source/packages/summareader_ui/lib" console/packages/summareader_ui/lib
cp "$source/packages/summareader_ui/pubspec.yaml" console/packages/summareader_ui/pubspec.yaml
cp "$source"/assets/fonts/Caprasimo-Regular.ttf console/assets/fonts/
cp "$source"/assets/fonts/Figtree-Variable.ttf console/assets/fonts/

echo "Copied from $source. Run 'cd console && flutter test' to check nothing"
echo "the console draws was relying on what changed."
