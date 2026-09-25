package release

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestExecSSHThroughBenchsh (#3350): ExecSSH reaches a machine only through
// internal/benchsh. The fake ssh plays the machine locally (its login shell is
// `bash -c` on the remote command word, which must be `bash -s --`): Run's
// words keep their shell meaning as the joined line did before, Send's tar
// stream rides stdin after benchsh's exec line, and Fetch brings the same
// files back.
func TestExecSSHThroughBenchsh(t *testing.T) {
	if _, err := exec.LookPath("tar"); err != nil {
		t.Skip("tar unavailable")
	}
	dir := t.TempDir()
	ssh := filepath.Join(dir, "ssh")
	fake := `#!/bin/bash
while [ "$1" = "-o" ]; do shift 2; done
shift
[ "$1" = "bash -s --" ] || { echo "remote command $1 is not bash -s --" >&2; exit 97; }
exec bash -c "$1"
`
	if err := os.WriteFile(ssh, []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	// macOS bsdtar would add AppleDouble ._ entries for the temp files' xattrs;
	// the local "machine" is not what this test is about.
	t.Setenv("COPYFILE_DISABLE", "1")
	s := ExecSSH{Path: ssh}
	ctx := context.Background()
	out, err := s.Run(ctx, "hulk", []string{"echo", "a", "&&", "(", "false", "||", "echo", "b", ")"})
	if err != nil || out != "a\nb\n" {
		t.Fatalf("Run = %q, %v; want the line's own && and ( || ) to run", out, err)
	}

	goos, goarch := platformOf(t, "linux-amd64")
	src := ArtifactDir(built(t, "v0.16.0", "linux-amd64", "nova-bus", "nova-update"), "v0.16.0", goos, goarch)
	if err := os.WriteFile(filepath.Join(src, "stray.key"), []byte("not shipped"), 0o600); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "machine", "releases")
	if out, err := s.Send(ctx, "hulk", src, dest); err != nil {
		t.Fatalf("Send: %v: %s", err, out)
	}
	landed := filepath.Join(dest, filepath.Base(src))
	back := filepath.Join(dir, "back")
	if out, err := s.Fetch(ctx, "hulk", landed, back); err != nil {
		t.Fatalf("Fetch: %v: %s", err, out)
	}
	names := func(d string) []string {
		entries, err := os.ReadDir(d)
		if err != nil {
			t.Fatal(err)
		}
		var n []string
		for _, e := range entries {
			n = append(n, e.Name())
		}
		sort.Strings(n)
		return n
	}
	arts, err := ReadSums(src)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{SumsFile}
	for _, a := range arts {
		want = append(want, a.Name)
	}
	sort.Strings(want)
	if got := names(back); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("fetched %v, want the shipped set %v", got, want)
	}
	for _, name := range want {
		a, _ := os.ReadFile(filepath.Join(src, name))
		b, _ := os.ReadFile(filepath.Join(back, name))
		if string(a) != string(b) {
			t.Fatalf("%s came back different", name)
		}
	}
}
