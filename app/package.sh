#!/bin/sh
# Build a double-clickable Mirage.app, self-contained with the mirage CLI inside.
#
# Both the GUI and the bundled mirage binary are code-signed with the
# virtualization entitlement (each is a separate process that creates VMs).
set -eu

here="$(cd "$(dirname "$0")" && pwd)"
repo="$(cd "$here/.." && pwd)"
config="${1:-release}"

# Build the GUI and the CLI it shells out to.
( cd "$here" && swift build -c "$config" )
gui="$(cd "$here" && swift build -c "$config" --show-bin-path)/MirageApp"
( cd "$repo" && go build -o bin/mirage ./cmd/mirage )

app="$here/build/Mirage.app"
rm -rf "$app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
cp "$gui" "$app/Contents/MacOS/Mirage"
cp "$here/Info.plist" "$app/Contents/Info.plist"
cp "$repo/bin/mirage" "$app/Contents/Resources/mirage"

# Sign inner-out: the bundled CLI first, then the app.
codesign --entitlements "$repo/entitlements.plist" -s - --force "$app/Contents/Resources/mirage"
codesign --entitlements "$repo/entitlements.plist" -s - --force "$app/Contents/MacOS/Mirage"
codesign --entitlements "$repo/entitlements.plist" -s - --force "$app"

echo "built $app"
echo "run: open \"$app\"   (or double-click it in Finder)"
