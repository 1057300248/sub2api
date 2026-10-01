package cline

import (
	"strings"
	"testing"
)

func TestClineSubjectHashAlphabetAndLength(t *testing.T) {
	for _, value := range []string{strings.Repeat("0123456789abcdef", 4), strings.Repeat("0", 64), strings.Repeat("f", 64)} {
		if !ValidSubjectHash(value) {
			t.Fatalf("valid lowercase SHA-256 encoding rejected: %q", value)
		}
	}
	for _, value := range []string{"", strings.Repeat("0", 63), strings.Repeat("0", 65), strings.Repeat("0", 62) + "é"} {
		if ValidSubjectHash(value) {
			t.Fatalf("invalid hash shape accepted: %q", value)
		}
	}
	for value := 0; value < 256; value++ {
		c := byte(value)
		hash := strings.Repeat("0", 63) + string([]byte{c})
		want := c >= '0' && c <= '9' || c >= 'a' && c <= 'f'
		if got := ValidSubjectHash(hash); got != want {
			t.Fatalf("byte %02x: got %t, want %t", c, got, want)
		}
	}
}
