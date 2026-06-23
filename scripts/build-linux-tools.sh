#!/bin/sh
# Build the Linux guest tools image: an ISO9660 disk carrying the Linux
# mirage-agent (arm64) + installer. Attach it to an installed Linux guest with:
#
#   mirage start <name> --gui --tools bin/mirage-linux-tools.iso
#
# then in the guest:
#
#   sudo mount -o ro /dev/vdb /mnt && sudo /mnt/install.sh
#
# ISO9660 is read-only and mounts natively on Linux (no extra filesystem tools),
# and hdiutil makehybrid builds it on macOS without privileges.
set -eu

repo="$(cd "$(dirname "$0")/.." && pwd)"
stage="$(mktemp -d)"
trap 'rm -rf "$stage"' EXIT

echo "building mirage-agent (linux/arm64)…"
GOOS=linux GOARCH=arm64 go build -C "$repo" -o "$stage/mirage-agent" ./cmd/mirage-agent
cp "$repo/guest/linux/install.sh" "$stage/install.sh"
cp "$repo/guest/linux/mirage-agent.service" "$stage/mirage-agent.service"
chmod +x "$stage/install.sh"

out="$repo/bin/mirage-linux-tools.iso"
mkdir -p "$repo/bin"
rm -f "$out"
hdiutil makehybrid -iso -joliet -default-volume-name MIRAGETOOLS -o "$out" "$stage" >/dev/null

echo "built $out"
echo "attach with: mirage start <name> --gui --tools $out"
