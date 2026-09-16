package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// boot loads a pin, not the whole directory: a pin naming three memories must
// load exactly those three files, and none of the rest of the corpus. A boot
// that walked the directory would count the fourth file too; this asserts the
// byte total is exactly the three named files' and never the walk's.

func TestBootLoadsExactlyThePinnedFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	a := "alpha memory\n\nfirst paragraph aaa\n"
	b := "beta memory\n\nsecond paragraph bbb\n"
	c := "gamma memory\n\nthird paragraph ccc\n"
	write("a.md", a)
	write("b.md", b)
	write("c.md", c)
	write("d.md", "delta memory\n\nfourth paragraph ddd\n") // unpinned, must not load

	pin := filepath.Join(dir, "pin")
	if err := os.WriteFile(pin, []byte("# the few memories this session loads\na.md\nb.md\nc.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	exit, stdout, stderr := runCLI(t, "", "boot", "--root", dir, "--pin", pin)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stdout: %s stderr: %s", exit, stdout, stderr)
	}
	if !strings.Contains(stdout, "files=3") {
		t.Errorf("stdout = %q, want files=3: boot must load exactly the three pinned files", stdout)
	}
	// The byte total is the three named files' and never d.md's: a walk would
	// add len("delta memory\n\nfourth paragraph ddd\n") to the total.
	want := strconv.Itoa(len(a) + len(b) + len(c))
	if !strings.Contains(stdout, "bytes="+want) {
		t.Errorf("stdout = %q, want bytes=%s (exactly the three pinned files, not the unpinned d.md)", stdout, want)
	}
}
