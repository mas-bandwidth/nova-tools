package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"
)

// TestVerifySumsRefusesUnreadChecksumTail pins the verifySums contract (sums.go:
// check every line of SHA256SUMS) and docs/STANDARD.md section 2, nothing fails
// silently: when the checksum stream cannot be read fully, verification returns
// false and prints a diagnostic naming the read error, and never invents a
// FAILED artifact line for a scanner error. A token longer than
// bufio.MaxScanTokenSize is the smallest stream bufio.Scanner cannot read whole.
func TestVerifySumsRefusesUnreadChecksumTail(t *testing.T) {
	t.Parallel()
	// One byte past bufio.Scanner's default ceiling, so a line this long ends
	// every scan that does not check Scanner.Err.
	oversized := strings.Repeat("a", bufio.MaxScanTokenSize+1)
	sum := func(content string) string {
		h := sha256.Sum256([]byte(content))
		return hex.EncodeToString(h[:])
	}
	bus := sum("bytes of nova-bus_v1.0.0_linux_amd64") + "  nova-bus_v1.0.0_linux_amd64\n"
	tokens := sum("bytes of nova-tokens_v1.0.0_linux_amd64") + "  nova-tokens_v1.0.0_linux_amd64\n"
	for _, c := range []struct {
		name string
		sums string
		want bool
		must string
	}{
		{"an oversized first line", oversized + "\n", false, "token too long"},
		{"a valid prefix then an oversized line", bus + tokens + oversized + "\n", false, "token too long"},
		{"the complete valid set still verifies", bus + tokens, true, ""},
	} {
		h := newHarness(t)
		dist := fillDist(t, h, "v1.0.0")
		h.write("dist/SHA256SUMS", c.sums, 0o644)
		if got := verifySums(h.out, h.errb, dist, filepath.Join(dist, "SHA256SUMS")); got != c.want {
			t.Errorf("%s: verifySums returned %v, want %v; output:\n%s", c.name, got, c.want, h.all())
		}
		if strings.Contains(h.all(), ": FAILED") {
			t.Errorf("%s: a FAILED artifact line was invented for a scanner error:\n%s", c.name, h.all())
		}
		if c.must != "" && !strings.Contains(h.all(), c.must) {
			t.Errorf("%s: the diagnostic does not name the checksum-read error %q:\n%s", c.name, c.must, h.all())
		}
	}
}
