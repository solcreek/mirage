#!/bin/sh
# Creates a stable self-signed code-signing identity for the guest agent, so its
# designated requirement — and the TCC csreq seeded from it — stays constant
# across rebuilds (ad-hoc signing changes the cdhash every build, invalidating
# the seeded Screen Recording grant). Idempotent: reuses an existing identity.
#
# Prints the identity name on stdout for the build to consume.
set -eu

IDENTITY="Mirage Agent Dev"
KEYCHAIN="${HOME}/.local/share/mirage/mirage-codesign.keychain-db"
KPASS="mirage-dev"

# A self-signed cert is untrusted, so find-identity -p codesigning won't list it;
# detect it by certificate instead (avoids creating duplicates).
if security find-certificate -c "$IDENTITY" "$KEYCHAIN" >/dev/null 2>&1; then
	echo "$IDENTITY"
	exit 0
fi

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

openssl req -x509 -newkey rsa:2048 -nodes \
	-keyout "$TMP/key.pem" -out "$TMP/cert.pem" -days 3650 \
	-subj "/CN=${IDENTITY}" \
	-addext "basicConstraints=critical,CA:false" \
	-addext "keyUsage=critical,digitalSignature" \
	-addext "extendedKeyUsage=critical,codeSigning" >/dev/null 2>&1
# Legacy PBE/MAC so macOS `security` can import the PKCS12 (modern defaults fail).
openssl pkcs12 -export -inkey "$TMP/key.pem" -in "$TMP/cert.pem" \
	-out "$TMP/id.p12" -passout "pass:${KPASS}" -name "$IDENTITY" \
	-certpbe PBE-SHA1-3DES -keypbe PBE-SHA1-3DES -macalg sha1 >/dev/null 2>&1

mkdir -p "$(dirname "$KEYCHAIN")"
security create-keychain -p "$KPASS" "$KEYCHAIN" 2>/dev/null || true
security unlock-keychain -p "$KPASS" "$KEYCHAIN"
security import "$TMP/id.p12" -k "$KEYCHAIN" -P "$KPASS" -T /usr/bin/codesign -A >/dev/null
security set-key-partition-list -S apple-tool:,apple: -s -k "$KPASS" "$KEYCHAIN" >/dev/null 2>&1 || true
# Make codesign find this keychain.
existing="$(security list-keychains -d user | sed 's/"//g' | tr -d ' ')"
security list-keychains -d user -s "$KEYCHAIN" $existing >/dev/null 2>&1 || true

echo "$IDENTITY"
