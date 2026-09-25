package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestMirrorVerbReceipts: the mirror verb is registered, refuses usage with 2,
// and check reads a missing mirror as one REFUSED line and exit 1 (no fetch, no Redis).
func TestMirrorVerbReceipts(t *testing.T) {
	for _, args := range [][]string{
		{"mirror"},
		{"mirror", "nope"},
		{"mirror", "refresh", "--source", "space"},
		{"mirror", "refresh", "--bench", "hulk", "--source", "space"},
		{"mirror", "refresh", "--bench", "space", "--source", "space", "--repos", "nova-tools"},
		{"mirror", "status", "--repos", "nova-tools:dev"},
	} {
		var out, errOut bytes.Buffer
		if code := run(args, &out, &errOut); code != 2 {
			t.Errorf("%v exit %d, want 2 (%s)", args, code, errOut.String())
		}
	}
	var out, errOut bytes.Buffer
	dir := t.TempDir()
	if code := run([]string{"mirror", "check", "--bench", "hulk", "--dir", dir, "--repos", "nova-tools:dev"}, &out, &errOut); code != 1 {
		t.Fatalf("check exit %d: %s%s", code, out.String(), errOut.String())
	}
	if got := out.String(); !strings.HasPrefix(got, "REFUSED hulk nova-tools reason=missing err=") || strings.Count(got, "\n") != 1 {
		t.Fatalf("check line %q", got)
	}
}
