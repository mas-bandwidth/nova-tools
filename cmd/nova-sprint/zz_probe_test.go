package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestZZProbe(t *testing.T) {
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	p := filepath.Join(t.TempDir(), "b.md")
	os.WriteFile(p, []byte(passingBrief(strings.Repeat("x", 9000))), 0o600)
	code, out, errs := ta.do("add --stream s1 --count 1 --brief-file " + p)
	t.Logf("code=%d\nOUT=%s\nERR=%s", code, out, errs)
	code, out, errs = ta.do("add --stream s1 --count 1 --json --brief-file " + p)
	t.Logf("code=%d\nOUT=%s\nERR=%s", code, out, errs)
}
