#!/bin/sh
# Grants the guest agent Screen Recording by seeding the system TCC.db, so
# `mirage screenshot` works unattended. Run ONCE during golden-image prep,
# AFTER SIP is disabled (boot recoveryOS, `csrutil disable`, reboot), as root:
#
#   sudo /Volumes/MirageTools/seed-tcc.sh
#
# The capture runs via the root daemon (which calls screencapture through
# `launchctl asuser`); TCC attributes it to mirage-agent, so mirage-agent is the
# client granted here. NOTE: the agent is ad-hoc signed, so its cdhash changes
# on every rebuild — re-run this after reinstalling the agent. (A stable signing
# identity would remove that; tracked as a follow-up.)
set -eu

AGENT="/usr/local/bin/mirage-agent"
DB="/Library/Application Support/com.apple.TCC/TCC.db"
SERVICE="kTCCServiceScreenCapture"

[ "$(id -u)" -eq 0 ] || { echo "run as root: sudo $0" >&2; exit 1; }
[ -f "$AGENT" ] || { echo "agent not installed at $AGENT — run install.sh first" >&2; exit 1; }

if csrutil status 2>/dev/null | grep -qi enabled; then
	echo "SIP is enabled. Boot recoveryOS (mirage start <img> --recovery), run" >&2
	echo "  csrutil disable" >&2
	echo "then reboot and re-run this script." >&2
	exit 1
fi

# Build a csreq pinned to the agent's 20-byte cdhash (ad-hoc has no usable
# designated requirement, so we pin the hash directly).
CDH=$(codesign -dvvv "$AGENT" 2>&1 | sed -n 's/^CDHash=//p')
[ -n "$CDH" ] || { echo "could not read agent cdhash" >&2; exit 1; }
printf 'cdhash H"%s"' "$CDH" | csreq -r- -b /tmp/.agent.csreq
HEX=$(xxd -p /tmp/.agent.csreq | tr -d '\n')
rm -f /tmp/.agent.csreq

sqlite3 "$DB" "INSERT OR REPLACE INTO access
  (service, client, client_type, auth_value, auth_reason, auth_version, csreq, flags, last_modified)
  VALUES ('$SERVICE', '$AGENT', 1, 2, 2, 1, X'$HEX', 0, strftime('%s','now'));"

echo "granted $SERVICE to $AGENT (cdhash $CDH):"
sqlite3 "$DB" "SELECT service, client, auth_value FROM access WHERE service='$SERVICE' AND client='$AGENT';"
killall tccd 2>/dev/null || true
echo "done — screenshot should work after a reboot."
