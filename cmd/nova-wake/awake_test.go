package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The awake verb reads presence over bus cursors: for every lane from-<name>/,
// the newest commit touching from-<name>/CURSOR is that friend's last beat
// (docs/SPEC-WORK.md, Presence, source bus-cursor). These tests run against a
// real git repo inside t.TempDir(), because the reading is a git log.

func awakeBus(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	return dir
}

// cursorCommit writes a from-<name>/CURSOR file and commits it at the given
// instant, so the lane's last beat is exactly `when`.
func cursorCommit(t *testing.T, dir, name string, when time.Time) {
	t.Helper()
	write(t, filepath.Join(dir, "from-"+name, "CURSOR"), "the cursor\n")
	git(t, dir, "add", "-A")
	stamp := when.UTC().Format(time.RFC3339)
	cmd := exec.Command("git", "-C", dir,
		"-c", "user.name="+name, "-c", "user.email="+name+"@mas-bandwidth.com",
		"commit", "-q", "-m", "cursor beat from "+name)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_DATE="+stamp, "GIT_COMMITTER_DATE="+stamp,
		"GIT_CONFIG_GLOBAL="+filepath.Join(dir, ".gitconfig-none"),
		"GIT_CONFIG_SYSTEM="+filepath.Join(dir, ".gitconfig-none"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("committing cursor %s: %v\n%s", name, err, out)
	}
}

func TestAwakeClassifiesByCursorAge(t *testing.T) {
	dir := awakeBus(t)
	cursorCommit(t, dir, "alice", at.Add(-10*time.Second)) // fresh -> awake
	cursorCommit(t, dir, "bob", at.Add(-600*time.Second))  // old -> asleep
	if err := os.MkdirAll(filepath.Join(dir, "from-carol"), 0o755); err != nil {
		t.Fatal(err)
	} // no CURSOR commit -> unknown

	r := wakeRun(t, "awake", "--bus", dir)
	if r.exit != 0 {
		t.Fatalf("awake exit %d, want 0; stderr=%s", r.exit, r.stderr)
	}
	for _, want := range []string{
		"FRIEND alice awake age=10 source=bus-cursor\n",
		"FRIEND bob asleep age=600 source=bus-cursor\n",
		"FRIEND carol unknown age=- source=bus-cursor\n",
		"AWAKE OK friends=3 awake=1 asleep=1 unknown=1 window=300\n",
	} {
		if !strings.Contains(r.stdout, want) {
			t.Fatalf("awake output missing %q\nstdout=%s", want, r.stdout)
		}
	}
}

func TestAwakeRefusesNonBus(t *testing.T) {
	r := wakeRun(t, "awake")
	if r.exit != 2 || !strings.Contains(r.stderr, "AWAKE REFUSED") {
		t.Fatalf("missing --bus: exit=%d stderr=%s, want 2 and AWAKE REFUSED", r.exit, r.stderr)
	}

	dir := t.TempDir()
	r = wakeRun(t, "awake", "--bus", dir)
	if r.exit != 2 || !strings.Contains(r.stderr, "AWAKE REFUSED") {
		t.Fatalf("non-git --bus: exit=%d stderr=%s, want 2 and AWAKE REFUSED", r.exit, r.stderr)
	}
}

func TestAwakeBoundsLines(t *testing.T) {
	dir := awakeBus(t)
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		if err := os.MkdirAll(filepath.Join(dir, "from-"+name), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	r := wakeRun(t, "awake", "--bus", dir, "--max", "2")
	if r.exit != 0 {
		t.Fatalf("awake exit %d, want 0; stderr=%s", r.exit, r.stderr)
	}
	lines := strings.Split(strings.TrimRight(r.stdout, "\n"), "\n")
	if len(lines) != 4 { // 2 FRIEND, 1 "... and N more", 1 AWAKE OK
		t.Fatalf("awake printed %d lines, want 4\nstdout=%s", len(lines), r.stdout)
	}
	if !strings.Contains(r.stdout, "FRIEND a unknown age=- source=bus-cursor\n") {
		t.Fatalf("first line missing\na=%s", r.stdout)
	}
	if !strings.Contains(r.stdout, "FRIEND b unknown age=- source=bus-cursor\n") {
		t.Fatalf("second line missing\n%s", r.stdout)
	}
	if !strings.Contains(r.stdout, "... and 3 more\n") {
		t.Fatalf("bounds line missing\n%s", r.stdout)
	}
	if !strings.Contains(r.stdout, "AWAKE OK friends=5 awake=0 asleep=0 unknown=5 window=300\n") {
		t.Fatalf("summary line missing\n%s", r.stdout)
	}
}
