#!/bin/sh
# Install the Mirage guest agent (Linux). Run once inside the guest as root,
# after attaching the tools image (mirage start <name> --gui --tools <iso>):
#
#   sudo mount -o ro /dev/vdb /mnt      # the tools image (iso9660)
#   sudo /mnt/install.sh
#
# Installs the agent as a systemd service listening on vsock :4444, so the host
# can exec/configure the guest. Persists across reboots and is inherited by
# clones of this image.
set -eu

here="$(cd "$(dirname "$0")" && pwd)"

if [ "$(id -u)" != "0" ]; then
	echo "must run as root: sudo $0" >&2
	exit 1
fi

install -m 0755 "$here/mirage-agent" /usr/local/bin/mirage-agent
install -m 0644 "$here/mirage-agent.service" /etc/systemd/system/mirage-agent.service
systemctl daemon-reload
systemctl enable --now mirage-agent.service

echo "mirage-agent installed and started (vsock :4444)"
systemctl --no-pager --full status mirage-agent.service 2>/dev/null | head -4 || true
