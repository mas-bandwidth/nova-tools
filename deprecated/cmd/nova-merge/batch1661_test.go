package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The control dir holds one FILE per control id (SPEC-TOOLWORK §6 rule 7): a directory,
// or a symlink to one, named with the id is not a passing selftest on file, so the
// ACCEPT OK that names it is refused like one whose control is absent (stella's hold
// on #3296: acceptOK took any os.Stat success).
func TestAcceptOKRequiresARegularControlFile(t *testing.T) {
	t.Parallel()
	control := t.TempDir()
	const file, dir, link = "0123456789ab", "cafecafecafe", "beefbeefbeef"
	if err := os.WriteFile(filepath.Join(control, file), []byte("ACCEPT SELFTEST PASS\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(control, dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(control, link)); err != nil {
		t.Fatal(err)
	}
	const head = "abcdef0123456789abcdef0123456789abcdef01"
	line := func(c string) string {
		return "ACCEPT OK label=c kind=fix head=" + head[:12] + " control=" + c + " took=1s"
	}
	if why := acceptOK(line(file), head, control); why != "" {
		t.Fatalf("a regular control file must admit, got %q", why)
	}
	for _, c := range []string{dir, link} {
		why := acceptOK(line(c), head, control)
		if !strings.Contains(why, "control="+c+" is not on file") {
			t.Errorf("control=%s (a directory, not a file) must be refused as not on file, got %q", c, why)
		}
	}
}
