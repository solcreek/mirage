package kcpassword

import "testing"

// Encode is its own inverse under the same key (XOR), so decoding the output
// up to the original length must recover the password. This pins the byte
// format macOS depends on.
func TestEncodeRoundTrip(t *testing.T) {
	for _, pw := range []string{"", "mirage", "12345678", "a", "twelvecharss", "thirteenchars!"} {
		enc := Encode([]byte(pw))
		if len(enc)%12 != 0 {
			t.Fatalf("%q: encoded length %d not a multiple of 12", pw, len(enc))
		}
		if len(enc) <= len(pw) {
			t.Fatalf("%q: expected a trailing pad block, got len %d for input %d", pw, len(enc), len(pw))
		}
		dec := make([]byte, len(enc))
		for i := range enc {
			dec[i] = enc[i] ^ key[i%len(key)]
		}
		if string(dec[:len(pw)]) != pw {
			t.Fatalf("%q: round-trip got %q", pw, string(dec[:len(pw)]))
		}
	}
}
