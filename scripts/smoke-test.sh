#!/bin/sh
# Integration smoke test against a real golden image. Covers the paths unit
# tests can't (vz engine + command orchestration): clone, headless exec, the
# ephemeral run command, the persistent supervisor, and the 2-VM quota.
#
#   make build && ./scripts/smoke-test.sh [image]
#
# Requires a prepared image (default: base) with the guest agent installed.
set -eu

MIRAGE="./bin/mirage"
IMAGE="${1:-base}"
fail() { echo "SMOKE FAIL: $*" >&2; exit 1; }
ok() { echo "  ok: $*"; }

[ -x "$MIRAGE" ] || fail "build first: make build"
"$MIRAGE" ls | grep -q "^${IMAGE} " || fail "image '$IMAGE' not found (mirage create / prep it first)"

cleanup() {
	"$MIRAGE" stop "$IMAGE" 2>/dev/null || true
	"$MIRAGE" stop smoke-q2 2>/dev/null || true
	"$MIRAGE" rm smoke-q2 2>/dev/null || true
	"$MIRAGE" rm smoke-q3 2>/dev/null || true
}
trap cleanup EXIT

echo "1. ephemeral run (clone → exec → destroy)"
out=$("$MIRAGE" --json run "$IMAGE" -- 'echo SMOKE_OK')
echo "$out" | grep -q SMOKE_OK || fail "run did not return command output"
echo "$out" | grep -q '"ephemeral"' || fail "run output missing ephemeral name"
ok "run returned output"

echo "2. persistent start + warm exec"
"$MIRAGE" start "$IMAGE" >/dev/null
"$MIRAGE" ls | grep "^${IMAGE} " | grep -q running || fail "image not running after start"
"$MIRAGE" exec "$IMAGE" -- true || fail "warm exec failed"
ok "supervisor up, warm exec works"

echo "3. 2-VM quota"
"$MIRAGE" clone "$IMAGE" smoke-q2 >/dev/null
"$MIRAGE" start smoke-q2 >/dev/null
"$MIRAGE" clone "$IMAGE" smoke-q3 >/dev/null
code=$("$MIRAGE" --json start smoke-q3 2>/dev/null | grep -o '"code": *"[^"]*"' | head -1)
echo "$code" | grep -q macos_vm_limit || fail "3rd macOS VM not refused with macos_vm_limit (got: $code)"
ok "3rd macOS VM refused with macos_vm_limit"

echo "4. stop is synchronous"
"$MIRAGE" stop "$IMAGE" >/dev/null
"$MIRAGE" stop smoke-q2 >/dev/null
"$MIRAGE" ls | grep "^${IMAGE} " | grep -q stopped || fail "image still running after stop"
ok "stop left VMs stopped"

echo "SMOKE PASS"
