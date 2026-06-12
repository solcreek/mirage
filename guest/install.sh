#!/bin/sh
# Installs the Mirage guest agent from the mounted tools image into the guest as
# a LaunchDaemon so it starts on every boot. Run once during golden-image prep:
#
#   sudo /Volumes/MirageTools/install.sh
#
# After this, seal the image; every clone boots with the agent listening on
# vsock so headless `mirage exec` works with no manual step.
set -eu

HERE="$(cd "$(dirname "$0")" && pwd)"
LABEL="com.solcreek.mirage-agent"
PLIST="/Library/LaunchDaemons/${LABEL}.plist"

if [ "$(id -u)" -ne 0 ]; then
	echo "install.sh must run as root: sudo $0" >&2
	exit 1
fi

echo "installing mirage-agent → /usr/local/bin"
install -d /usr/local/bin
install -m 0755 "${HERE}/mirage-agent" /usr/local/bin/mirage-agent

# Root LaunchDaemon (:4444 — exec, ping, screenshot via launchctl asuser).
echo "installing LaunchDaemon → ${PLIST}"
install -m 0644 "${HERE}/${LABEL}.plist" "${PLIST}"
launchctl bootout system "${PLIST}" 2>/dev/null || true
launchctl bootstrap system "${PLIST}"
launchctl enable "system/${LABEL}"

echo "mirage-agent installed (daemon :4444)."
echo "For screenshot, also run seed-tcc.sh once (SIP off) to grant Screen Recording."
