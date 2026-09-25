//go:build unix

package update

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `nova-version snapshot` INVENTORIES A WINDOWS BENCH'S BIN DIRECTORY, and
// every file in it is called nova-<something>.exe. The verb selects by the
// `nova-` prefix and asks each file its own `version`, so the suffix is
// carried through rather than stripped: the name column is the file's real
// name, which is what makes a snapshot comparable with what `install` put
// there and with what a person sees in the directory.
//
// The stubs are shell scripts with a windows-shaped NAME -- which is exactly
// the point, and the reason this can be asserted on a unix runner: nothing in
// the verb may key off the host's platform when deciding which files are
// tools. (Unix-only because the stubs need a shebang; the windows leg of CI
// runs the rest of this package.)
func TestSnapshotReadsExeNamesAndKeepsTheSuffix(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	stub := func(name, line string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nprintf '%s\\n' '"+line+"'\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	stub("nova-bus.exe", "nova-bus v0.16.0 windows/amd64 go1.26.5")
	stub("nova-update.exe", "nova-update v0.16.0 windows/amd64 go1.26.5")
	// A file `install` moved aside because windows would not replace a
	// running image. It is dot-prefixed for exactly this reason: it is not a
	// tool and must not become a row.
	stub(".nova-update.exe.old", "nova-update v0.15.0 windows/amd64 go1.26.5")

	out := filepath.Join(dir, "snapshot.tsv")
	var o, e bytes.Buffer
	if code := Main("nova-version", []string{"snapshot", "--bin", bin, "--out", out}, "", &o, &e); code != 0 {
		t.Fatalf("snapshot refused a windows bin directory: %d\n%s", code, e.String())
	}
	if !strings.Contains(o.String(), "tools=2") {
		t.Fatalf("want tools=2, got:\n%s", o.String())
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	if lines[0] != snapshotHeader {
		t.Fatalf("header is %q", lines[0])
	}
	var names []string
	for _, line := range lines[1:] {
		fields := strings.Split(line, "\t")
		if len(fields) != 4 {
			t.Fatalf("row %q is not four columns", line)
		}
		if fields[3] != "windows/amd64" {
			t.Fatalf("row %q does not carry the platform the binary reported", line)
		}
		names = append(names, fields[0])
	}
	if got := strings.Join(names, " "); got != "nova-bus.exe nova-update.exe" {
		t.Fatalf("snapshot rows are %q; the name column is the file's real name, suffix and all", got)
	}
}
