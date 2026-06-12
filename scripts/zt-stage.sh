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

echo "staged to $STAGE (account: admin / mirage)"
