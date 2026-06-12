#!/bin/sh
# Zero-touch golden-image prep (SPIKE): mounts an installed (userless) image's
# Data volume and writes everything offline — admin user, auto-login, agent,
# Screen Recording TCC grant, and .AppleSetupDone — so the VM boots straight to
# a logged-in desktop with the agent live, no Setup Assistant, no recovery, no
# in-guest commands.
#
#   scripts/zt-stage.sh           # (no sudo) stage artifacts to /tmp/zt-stage
#   sudo scripts/zt-apply.sh zt   # write them into image 'zt'
#
# Account: admin / password "mirage".
set -eu

NAME="${1:?usage: sudo zt-apply.sh <image-name>}"
STAGE="/tmp/zt-stage"
USERHOME=$(eval echo "~${SUDO_USER:-$USER}")
DISK="${USERHOME}/.local/share/mirage/images/${NAME}.mirage/disk.img"
[ -f "$DISK" ] || { echo "no image disk at $DISK" >&2; exit 1; }
[ -f "$STAGE/admin.plist" ] || { echo "run scripts/zt-stage.sh first" >&2; exit 1; }

echo "attaching $DISK …"
ATTACH=$(hdiutil attach -nomount "$DISK")
TOP=$(echo "$ATTACH" | grep GUID_partition | awk '{print $1}' | head -1)
DATA=""
cleanup() { [ -n "$DATA" ] && diskutil unmount force "$DATA" >/dev/null 2>&1 || true; hdiutil detach "$TOP" >/dev/null 2>&1 || true; }
trap cleanup EXIT

# SAFETY: only consider APFS volumes from THIS attached image (never the host's
# own "Data" volume). APFS volumes in the attach output carry GUID 41504653-…;
# pick the one whose volume name is exactly "Data".
for d in $(echo "$ATTACH" | awk '/41504653-0000-11AA/{print $1}'); do
	nm=$(diskutil info "$d" 2>/dev/null | awk -F': *' '/Volume Name/{print $2}')
	if [ "$nm" = "Data" ]; then DATA="$d"; break; fi
done
[ -n "$DATA" ] || { echo "could not find the image's Data volume in: $ATTACH" >&2; exit 1; }
# Extra guard: refuse anything that's already mounted at a system path.
cur=$(diskutil info "$DATA" | awk -F': *' '/Mount Point/{print $2}')
case "$cur" in /|/System/Volumes/Data) echo "REFUSING: $DATA looks like the host volume ($cur)" >&2; DATA=""; exit 1;; esac

diskutil mount "$DATA" >/dev/null
MP=$(diskutil info "$DATA" | awk -F': *' '/Mount Point/{print $2}')
echo "mounted image Data volume ($DATA) at $MP"

DS="$MP/private/var/db/dslocal/nodes/Default"
GUID=$(cat "$STAGE/generateduid")

echo "1. admin user record → dslocal"
install -m 0600 -o 0 -g 0 "$STAGE/admin.plist" "$DS/users/admin.plist"

echo "2. add admin to the admin group"
python3 - "$DS/groups/admin.plist" "$GUID" <<'PY'
import sys, plistlib
path, guid = sys.argv[1], sys.argv[2]
with open(path,'rb') as f: g = plistlib.load(f)
if guid not in g.get('groupmembers',[]): g.setdefault('groupmembers',[]).append(guid)
if 'admin' not in g.get('users',[]): g.setdefault('users',[]).append('admin')
with open(path,'wb') as f: plistlib.dump(g, f, fmt=plistlib.FMT_BINARY)
print("   admin added to groupmembers + users")
PY

echo "3. home dir (macOS fills it on first login)"
mkdir -p "$MP/Users/admin" && chown 501:20 "$MP/Users/admin" && chmod 700 "$MP/Users/admin"

echo "4. auto-login (kcpassword + autoLoginUser)"
install -m 0600 -o 0 -g 0 "$STAGE/kcpassword" "$MP/private/etc/kcpassword"
defaults write "$MP/Library/Preferences/com.apple.loginwindow" autoLoginUser -string admin

echo "5. install agent + LaunchDaemon (root-owned — launchd refuses non-root daemons)"
mkdir -p "$MP/usr/local/bin" "$MP/Library/LaunchDaemons"
install -m 0755 -o 0 -g 0 "$STAGE/mirage-agent" "$MP/usr/local/bin/mirage-agent"
install -m 0644 -o 0 -g 0 "$STAGE/com.solcreek.mirage-agent.plist" "$MP/Library/LaunchDaemons/com.solcreek.mirage-agent.plist"

echo "6. Screen Recording TCC grant"
TCCDST="$MP/Library/Application Support/com.apple.TCC/TCC.db"
if sqlite3 "$TCCDST" "SELECT 1 FROM access LIMIT 1;" >/dev/null 2>&1; then
	# DB already initialized — insert the grant directly.
	HEX=$(xxd -p "$STAGE/agent.csreq" | tr -d '\n'); NOW=$(date +%s)
	sqlite3 "$TCCDST" "INSERT OR REPLACE INTO access (service,client,client_type,auth_value,auth_reason,auth_version,csreq,flags,last_modified,last_reminded) VALUES ('kTCCServiceScreenCapture','/usr/local/bin/mirage-agent',1,2,4,1,X'$HEX',0,$NOW,$NOW);"
	echo "   inserted grant into existing TCC.db"
elif [ -f "$STAGE/TCC.db" ]; then
	# Fresh install: no access table yet. Drop in base's real TCC.db (correct
	# schema + a matching cert-pinned mirage-agent grant).
	install -m 0644 -o 0 -g 0 "$STAGE/TCC.db" "$TCCDST"
	echo "   installed base TCC.db (carries the matching grant)"
else
	echo "   WARNING: no access table and no staged TCC.db — screenshot will need a grant" >&2
fi

echo "7. skip Setup Assistant"
touch "$MP/private/var/db/.AppleSetupDone"

echo "done — detaching. Boot it: mirage start $NAME ; mirage exec $NAME -- whoami"
