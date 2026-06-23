#!/bin/sh
# Repack an Ubuntu ISO into a fully unattended (zero-touch) autoinstall ISO:
# embed a NoCloud seed (autoinstall user-data) + the Mirage guest agent, and set
# the GRUB kernel cmdline to trigger autoinstall — no clicks, no terminal. The
# autoinstall config pins DNS (1.1.1.1) so apt works regardless of the vmnet NAT
# DNS quirk, creates the mirage/mirage user, and installs the agent.
#
# Usage: build-autoinstall-iso.sh <source.iso> [out.iso] [password]
#
# Requires xorriso (brew install xorriso). ISO-repack preserves EFI boot.
set -eu

src="${1:?usage: build-autoinstall-iso.sh <source.iso> [out.iso] [password]}"
repo="$(cd "$(dirname "$0")/.." && pwd)"
out="${2:-$repo/bin/$(basename "${src%.iso}")-autoinstall.iso}"
password="${3:-mirage}"

command -v xorriso >/dev/null || { echo "need xorriso: brew install xorriso" >&2; exit 1; }

stage="$(mktemp -d)"
trap 'rm -rf "$stage"' EXIT
mkdir -p "$stage/nocloud"

echo "generating autoinstall seed (user mirage/$password, DNS 1.1.1.1)…"
pwhash="$(openssl passwd -6 "$password")"
: > "$stage/nocloud/meta-data"
cat > "$stage/nocloud/user-data" <<EOF
#cloud-config
autoinstall:
  version: 1
  locale: en_US.UTF-8
  keyboard: {layout: us}
  network:
    version: 2
    ethernets:
      enp0s1:
        dhcp4: true
        nameservers:
          addresses: [1.1.1.1, 9.9.9.9]
  storage:
    layout: {name: direct}
  identity:
    hostname: mirage
    realname: Mirage
    username: mirage
    password: "$pwhash"
  ssh: {install-server: false}
  late-commands:
    - cp /cdrom/nocloud/mirage-agent /target/usr/local/bin/mirage-agent
    - chmod 0755 /target/usr/local/bin/mirage-agent
    - cp /cdrom/nocloud/mirage-agent.service /target/etc/systemd/system/mirage-agent.service
    - curtin in-target --target=/target -- systemctl enable mirage-agent.service
  shutdown: poweroff
EOF

echo "building mirage-agent (linux/arm64) into the seed…"
GOOS=linux GOARCH=arm64 go build -C "$repo" -o "$stage/nocloud/mirage-agent" ./cmd/mirage-agent
cp "$repo/guest/linux/mirage-agent.service" "$stage/nocloud/mirage-agent.service"

echo "rewriting grub.cfg to auto-trigger autoinstall…"
cat > "$stage/grub.cfg" <<'EOF'
set timeout=1
loadfont unicode
menuentry "Mirage unattended install" {
	set gfxpayload=keep
	linux	/casper/vmlinuz  autoinstall ds=nocloud\;s=/cdrom/nocloud/ --- quiet splash console=tty0
	initrd	/casper/initrd
}
EOF

echo "repacking ISO with xorriso (preserving EFI boot)…"
mkdir -p "$(dirname "$out")"
rm -f "$out"
xorriso -indev "$src" -outdev "$out" \
	-boot_image any replay \
	-map "$stage/nocloud" /nocloud \
	-map "$stage/grub.cfg" /boot/grub/grub.cfg \
	-commit >/dev/null 2>&1

echo "built $out ($(du -h "$out" | cut -f1))"
echo "create with: mirage create <name> --iso $out   (fully unattended)"
