#!/bin/sh
# Stages artifacts for zero-touch golden-image prep (no sudo). Builds + signs the
# guest agent with the stable identity, computes its TCC csreq, and generates an
# offline admin user record (ShadowHashData for password "mirage") + kcpassword.
# Output: /tmp/zt-stage/ — consumed by `sudo scripts/zt-apply.sh <image>`.
set -eu

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
STAGE="/tmp/zt-stage"
KEYCHAIN="${HOME}/.local/share/mirage/mirage-codesign.keychain-db"
rm -rf "$STAGE" && mkdir -p "$STAGE"

GOOS=darwin GOARCH=arm64 go build -o "$STAGE/mirage-agent" "$ROOT/cmd/mirage-agent"
ID="$(sh "$ROOT/scripts/dev-agent-cert.sh")"
security unlock-keychain -p mirage-dev "$KEYCHAIN" 2>/dev/null || true
codesign -s "$ID" --keychain "$KEYCHAIN" --identifier com.solcreek.mirage-agent --force "$STAGE/mirage-agent"
REQ=$(codesign -d -r- "$STAGE/mirage-agent" 2>&1 | sed -n 's/^designated => //p')
printf '%s' "$REQ" | csreq -r- -b "$STAGE/agent.csreq"
cp "$ROOT/guest/launchd/com.solcreek.mirage-agent.plist" "$STAGE/"

python3 - "$STAGE" <<'PY'
import os, sys, hashlib, plistlib, uuid
stage = sys.argv[1]
PW = b"mirage"; salt = os.urandom(32); iters = 50000
entropy = hashlib.pbkdf2_hmac('sha512', PW, salt, iters, dklen=128)
shadow = {"SALTED-SHA512-PBKDF2": {"entropy": entropy, "salt": salt, "iterations": iters}}
blob = plistlib.dumps(shadow, fmt=plistlib.FMT_BINARY)
guid = str(uuid.uuid4()).upper()
user = {"name": ["admin"], "uid": ["501"], "gid": ["20"], "home": ["/Users/admin"],
        "shell": ["/bin/zsh"], "realname": ["admin"], "generateduid": [guid],
        "authentication_authority": [";ShadowHash;HASHLIST:<SALTED-SHA512-PBKDF2>"],
        "ShadowHashData": [blob], "passwd": ["********"]}
with open(f"{stage}/admin.plist","wb") as f: plistlib.dump(user, f, fmt=plistlib.FMT_BINARY)
open(f"{stage}/generateduid","w").write(guid)
key = bytes([0x7D,0x89,0x52,0x23,0xD2,0xBC,0xDD,0xEA,0xA3,0xB9,0x1F])
pad = 12 - (len(PW) % 12); buf = bytearray(PW) + bytearray(pad)
for i in range(len(buf)): buf[i] ^= key[i % len(key)]
open(f"{stage}/kcpassword","wb").write(buf)
PY

# Stage a known-good system TCC.db from the already-prepped 'base' image: a
# fresh install's TCC.db has no `access` table yet (tccd builds it on first run),
# and base's TCC.db already carries the mirage-agent Screen Recording grant whose
# csreq matches this same signing cert. Copying it gives zt the right schema +
# grant in one shot.
BASE_DISK="${HOME}/.local/share/mirage/images/base.mirage/disk.img"
if [ -f "$BASE_DISK" ]; then
	A=$(hdiutil attach -readonly -nomount "$BASE_DISK")
	BT=$(echo "$A" | grep GUID_partition | awk '{print $1}' | head -1)
	BD=""
	for d in $(echo "$A" | awk '/41504653-0000-11AA/{print $1}'); do
		[ "$(diskutil info "$d" 2>/dev/null | awk -F': *' '/Volume Name/{print $2}')" = "Data" ] && BD="$d" && break
	done
	if [ -n "$BD" ]; then
		diskutil mount readOnly "$BD" >/dev/null
		BMP=$(diskutil info "$BD" | awk -F': *' '/Mount Point/{print $2}')
		cp "$BMP/Library/Application Support/com.apple.TCC/TCC.db" "$STAGE/TCC.db" && echo "staged base TCC.db"
		diskutil unmount "$BD" >/dev/null
	fi
	hdiutil detach "$BT" >/dev/null 2>&1 || true
fi

echo "staged to $STAGE (account: admin / mirage)"
