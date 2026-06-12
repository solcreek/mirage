// Package kcpassword encodes /etc/kcpassword, the obfuscated auto-login
// credential macOS reads at boot. The format is a simple XOR against a fixed
// cycling key with a trailing pad block — not encryption; it only hides the
// password from a casual glance, which is all macOS itself does.
package kcpassword

// key is the fixed obfuscation key macOS uses for /etc/kcpassword.
var key = []byte{0x7D, 0x89, 0x52, 0x23, 0xD2, 0xBC, 0xDD, 0xEA, 0xA3, 0xB9, 0x1F}

// Encode XORs the password with the cycling key and pads to the next multiple
// of 12. A full padding block is appended even when already aligned, which
// macOS requires to read the file correctly.
func Encode(pw []byte) []byte {
	pad := 12 - (len(pw) % 12)
	out := make([]byte, len(pw)+pad)
	copy(out, pw)
	for i := range out {
		out[i] ^= key[i%len(key)]
	}
	return out
}
