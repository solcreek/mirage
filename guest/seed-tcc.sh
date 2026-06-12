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

# Prefer the agent's designated requirement (stable across rebuilds when signed
# with the dev identity); fall back to a cdhash pin for ad-hoc builds.
REQ=$(codesign -d -r- "$AGENT" 2>&1 | sed -n 's/^designated => //p')
if [ -n "$REQ" ]; then
	printf '%s' "$REQ" | csreq -r- -b /tmp/.agent.csreq
else
	CDH=$(codesign -dvvv "$AGENT" 2>&1 | sed -n 's/^CDHash=//p')
	[ -n "$CDH" ] || { echo "could not read agent signature" >&2; exit 1; }
	printf 'cdhash H"%s"' "$CDH" | csreq -r- -b /tmp/.agent.csreq
fi
HEX=$(xxd -p /tmp/.agent.csreq | tr -d '\n')
rm -f /tmp/.agent.csreq

# auth_reason=4 + last_reminded=now mirror a real user "Allow": without them
# macOS shows its periodic screen-recording reminder ("bypass the private window
# picker / directly access your screen") on top of the grant. NOTE: the
# com.apple.TCC directory is only writable in this prep window (SIP off, before
# tccd re-locks it this boot) — do ALL TCC seeding in one pass here.
NOW="strftime('%s','now')"
sqlite3 "$DB" "INSERT OR REPLACE INTO access
  (service, client, client_type, auth_value, auth_reason, auth_version, csreq, flags, last_modified, last_reminded)
  VALUES ('$SERVICE', '$AGENT', 1, 2, 4, 1, X'$HEX', 0, $NOW, $NOW);"

echo "granted $SERVICE to $AGENT (cdhash $CDH):"
sqlite3 "$DB" "SELECT service, client, auth_value, auth_reason FROM access WHERE service='$SERVICE' AND client='$AGENT';"
killall tccd 2>/dev/null || true
echo "done — screenshot works with no consent dialog after a reboot."
