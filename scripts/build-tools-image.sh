#!/bin/sh
# Builds the Mirage guest tools image: a raw, read-only-friendly disk image that
# carries the guest agent + installer + LaunchDaemon plist. Attached to a VM as a
# second block device, it auto-mounts in the guest under /Volumes/MirageTools.
#
#   scripts/build-tools-image.sh [output.img]
#
# Default output: ./bin/mirage-tools.img
set -eu

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="${1:-${ROOT}/bin/mirage-tools.img}"
VOLNAME="MirageTools"
STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT

echo "building guest agent (darwin/arm64)…"
GOOS=darwin GOARCH=arm64 go build -o "${STAGE}/mirage-agent" "${ROOT}/cmd/mirage-agent"
codesign -s - --force "${STAGE}/mirage-agent"

cp "${ROOT}/guest/install.sh" "${STAGE}/install.sh"
cp "${ROOT}/guest/seed-tcc.sh" "${STAGE}/seed-tcc.sh"
chmod +x "${STAGE}/install.sh" "${STAGE}/seed-tcc.sh"
cp "${ROOT}"/guest/launchd/*.plist "${STAGE}/"

echo "creating raw image ${OUT}…"
mkdir -p "$(dirname "$OUT")"
rm -f "$OUT"
# 128 MiB raw image, GPT + HFS+, populated from the staging dir.
hdiutil create -size 128m -layout GPTSPUD -fs HFS+ -volname "$VOLNAME" \
	-srcfolder "$STAGE" -format UDRW -ov "${OUT%.img}.dmg" >/dev/null
# Convert the UDIF .dmg to a raw image VZ can attach as a block device.
hdiutil convert "${OUT%.img}.dmg" -format UDTO -o "${OUT%.img}" >/dev/null
mv "${OUT%.img}.cdr" "$OUT"
rm -f "${OUT%.img}.dmg"

echo "done: $OUT"
echo "attach with: mirage start <name> --gui --tools $OUT"
