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
GUI_LABEL="com.solcreek.mirage-agent-gui"
PLIST="/Library/LaunchDaemons/${LABEL}.plist"
GUI_PLIST="/Library/LaunchAgents/${GUI_LABEL}.plist"

if [ "$(id -u)" -ne 0 ]; then
	echo "install.sh must run as root: sudo $0" >&2
	exit 1
fi

echo "installing mirage-agent → /usr/local/bin"
install -d /usr/local/bin
install -m 0755 "${HERE}/mirage-agent" /usr/local/bin/mirage-agent

# Root LaunchDaemon (:4444 — exec/ping, no login needed).
echo "installing LaunchDaemon → ${PLIST}"
install -m 0644 "${HERE}/${LABEL}.plist" "${PLIST}"
launchctl bootout system "${PLIST}" 2>/dev/null || true
launchctl bootstrap system "${PLIST}"
launchctl enable "system/${LABEL}"

# User LaunchAgent (:4445 — screenshot, needs the GUI session).
echo "installing LaunchAgent → ${GUI_PLIST}"
install -d /Library/LaunchAgents
install -m 0644 "${HERE}/${GUI_LABEL}.plist" "${GUI_PLIST}"
# Load it into the console user's GUI session if someone is logged in.
gui_uid=$(stat -f%u /dev/console 2>/dev/null || echo "")
if [ -n "$gui_uid" ] && [ "$gui_uid" != "0" ]; then
	launchctl bootout "gui/${gui_uid}" "${GUI_PLIST}" 2>/dev/null || true
	launchctl bootstrap "gui/${gui_uid}" "${GUI_PLIST}" 2>/dev/null || true
fi

echo "mirage-agent installed (daemon :4444 + gui agent :4445)."
echo "Screen Recording (TCC) for screenshot may still need granting — see the S3 spike."
