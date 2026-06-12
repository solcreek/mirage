//go:build darwin

package main

import "testing"

// TestEncodeKCPassword checks the /etc/kcpassword obfuscation: XOR with the
// cycling key, padded up to the next multiple of 12 (a full padding block is
// added even when already aligned). Decoding XORs again and strips trailing
// key bytes back to the original password.
func TestEncodeKCPassword(t *testing.T) {
	for _, pw := range []string{"", "admin", "twelvecharpw", "0123456789ab", "pédagogie🔐"} {
		enc := encodeKCPassword([]byte(pw))
		if len(enc)%12 != 0 {
			t.Errorf("%q: encoded length %d not a multiple of 12", pw, len(enc))
		}
		if len(enc) < len([]byte(pw)) {
			t.Errorf("%q: encoded shorter than input", pw)
		}
		// Decode: XOR back with the key, then drop trailing decoded-zero bytes.
		dec := make([]byte, len(enc))
		for i := range enc {
			dec[i] = enc[i] ^ kcKey[i%len(kcKey)]
		}
		got := string(trimTrailingZeros(dec))
		if got != pw {
			t.Errorf("round-trip: got %q, want %q", got, pw)
		}
	}
}

func trimTrailingZeros(b []byte) []byte {
	n := len(b)
	for n > 0 && b[n-1] == 0 {
		n--
	}
	return b[:n]
}
