#!/bin/sh
# Seeds the system TCC.db so the guest agent can capture the screen without an
# interactive grant. Run ONCE during golden-image prep, AFTER SIP is disabled
# (boot recoveryOS, run `csrutil disable`, reboot), as root:
#
#   sudo /Volumes/MirageTools/seed-tcc.sh
#
# Then reboot; clones inherit the grant. macOS 26 TCC.db schema is not fully
# documented — this is a spike; if it fails, the printed sqlite error guides the
# next iteration.
set -u

AGENT="/usr/local/bin/mirage-agent"
DB="/Library/Application Support/com.apple.TCC/TCC.db"
SERVICE="kTCCServiceScreenCapture"

[ "$(id -u)" -eq 0 ] || { echo "run as root: sudo $0" >&2; exit 1; }
[ -f "$AGENT" ] || { echo "agent not installed at $AGENT — run install.sh first" >&2; exit 1; }

# Refuse to run with SIP on: the system TCC.db is SIP-protected and the write
# would silently fail.
if csrutil status 2>/dev/null | grep -qi enabled; then
	echo "SIP is enabled. Boot recoveryOS (mirage start <img> --recovery), run" >&2
	echo "  csrutil disable" >&2
	echo "then reboot and re-run this script." >&2
	exit 1
fi

# Derive the agent's designated code requirement → csreq blob, so the row keys
# on the signature (survives path moves; with a stable signing cert, rebuilds).
REQ=$(codesign -d -r- "$AGENT" 2>&1 | sed -n 's/^designated => //p')
CSREQ_HEX=""
if [ -n "$REQ" ]; then
	if printf '%s' "$REQ" | csreq -r- -b /tmp/.agent.csreq 2>/dev/null; then
		CSREQ_HEX=$(xxd -p /tmp/.agent.csreq | tr -d '\n')
		rm -f /tmp/.agent.csreq
	fi
fi

echo "agent requirement: ${REQ:-<none>}"
echo "csreq blob: ${CSREQ_HEX:+present}"

# Explicit-column INSERT; omitted columns must accept defaults/NULL on this
# macOS version. client_type=1 (absolute path), auth_value=2 (allowed),
# auth_reason=2 (user set), auth_version=1.
if [ -n "$CSREQ_HEX" ]; then
	CSREQ_SQL="X'$CSREQ_HEX'"
else
	CSREQ_SQL="NULL"
fi

SQL="INSERT OR REPLACE INTO access
  (service, client, client_type, auth_value, auth_reason, auth_version, csreq, flags, last_modified)
  VALUES ('$SERVICE', '$AGENT', 1, 2, 2, 1, $CSREQ_SQL, 0, strftime('%s','now'));"

echo "seeding $SERVICE for $AGENT …"
if sqlite3 "$DB" "$SQL"; then
	echo "OK. Verifying:"
	sqlite3 "$DB" "SELECT service, client, auth_value FROM access WHERE service='$SERVICE';"
	echo "Reboot the guest, then re-seal. Screenshot should now work."
else
	echo "sqlite insert failed — schema may differ on this macOS. Columns are:" >&2
	sqlite3 "$DB" "PRAGMA table_info(access);" >&2
fi
