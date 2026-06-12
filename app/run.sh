#!/bin/sh
# Build, sign, and launch the Mirage GUI.
#
# Creating a macOS VZVirtualMachine requires the com.apple.security.virtualization
# entitlement, so the built executable must be code-signed before it can boot a
# VM — `swift run` alone produces an unsigned binary that fails at VM creation.
set -eu

here="$(cd "$(dirname "$0")" && pwd)"
repo="$(cd "$here/.." && pwd)"
config="${1:-debug}"

cd "$here"
swift build -c "$config"
bin="$(swift build -c "$config" --show-bin-path)/MirageApp"

codesign --entitlements "$repo/entitlements.plist" -s - --force "$bin"

# Point the GUI's CLI client at the repo's freshly built mirage binary.
export MIRAGE_BIN="${MIRAGE_BIN:-$repo/bin/mirage}"
exec "$bin"
