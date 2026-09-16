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

func TestAwakeReadsMaxAndAsFromConfig(t *testing.T) {
	dir := awakeBus(t)
	for _, name := range []string{"a", "b", "c"} {
		if err := os.MkdirAll(filepath.Join(dir, "from-"+name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfgPath := filepath.Join(t.TempDir(), "config")
	write(t, cfgPath, "bus="+dir+"\nas=rowan\nmax=2\n")
	t.Setenv("NOVA_WAKE_CONFIG", cfgPath)

	r := wakeRun(t, "awake")
	if r.exit != 0 {
		t.Fatalf("awake exit %d, want 0; stderr=%s", r.exit, r.stderr)
	}
	lines := strings.Split(strings.TrimRight(r.stdout, "\n"), "\n")
	if len(lines) != 4 { // 2 FRIEND, 1 "... and N more", 1 AWAKE OK
		t.Fatalf("config max not read: awake printed %d lines, want 4\nstdout=%s", len(lines), r.stdout)
	}
	if !strings.Contains(r.stdout, "... and 1 more\n") {
		t.Fatalf("bounds line missing\nstdout=%s", r.stdout)
	}
	if !strings.Contains(r.stdout, "AWAKE OK friends=3 awake=0 asleep=0 unknown=3 window=300\n") {
		t.Fatalf("summary line missing\nstdout=%s", r.stdout)
	}
}

func TestAwakeReadsBusAndWindowFromConfig(t *testing.T) {
	dir := awakeBus(t)
	cursorCommit(t, dir, "alice", at.Add(-10*time.Second))
	cfgPath := filepath.Join(t.TempDir(), "config")
	write(t, cfgPath, "bus="+dir+"\nwindow=600\n")
	t.Setenv("NOVA_WAKE_CONFIG", cfgPath)

	r := wakeRun(t, "awake")
	if r.exit != 0 {
		t.Fatalf("awake exit %d, want 0; stderr=%s", r.exit, r.stderr)
	}
	if !strings.Contains(r.stdout, "AWAKE OK friends=1 awake=1 asleep=0 unknown=0 window=600\n") {
		t.Fatalf("config bus and window not read\nstdout=%s", r.stdout)
	}
}

func TestFlagBeatsConfig(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config")
	write(t, cfgPath, "bus=/nonexistent\nwindow=999\n")
	t.Setenv("NOVA_WAKE_CONFIG", cfgPath)

	dir := awakeBus(t)
	cursorCommit(t, dir, "alice", at.Add(-400*time.Second))
	r := wakeRun(t, "awake", "--bus", dir, "--window", "100")
	if r.exit != 0 {
		t.Fatalf("awake exit %d, want 0; stderr=%s", r.exit, r.stderr)
	}
	if !strings.Contains(r.stdout, "FRIEND alice asleep age=400 source=bus-cursor\n") {
		t.Fatalf("flag window did not beat config\nstdout=%s", r.stdout)
	}
	if !strings.Contains(r.stdout, "window=100\n") {
		t.Fatalf("summary missing flag window\nstdout=%s", r.stdout)
	}
}

func TestAwakeRefusesUnknownConfigKey(t *testing.T) {
	dir := awakeBus(t)
	cfgPath := filepath.Join(t.TempDir(), "config")
	write(t, cfgPath, "bus="+dir+"\nbuss="+dir+"\n")
	t.Setenv("NOVA_WAKE_CONFIG", cfgPath)

	r := wakeRun(t, "awake")
	if r.exit != 2 {
		t.Fatalf("unknown config key: exit %d, want 2\nstderr=%s", r.exit, r.stderr)
	}
	if !strings.Contains(r.stderr, "AWAKE REFUSED") {
		t.Fatalf("missing AWAKE REFUSED\nstderr=%s", r.stderr)
	}
	if !strings.Contains(r.stderr, `unknown config key "buss"`) {
		t.Fatalf("refusal should name the unknown key\nstderr=%s", r.stderr)
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

// THE BEAT CARRIES A LEASE, and it outruns both the cursor and the window. A line whose
// manager process is alive but between two waits stops writing a beat while it works the note
// it was just handed, and over a slow note that gap outruns --window, so by cursor age
// alone it reads asleep. The exit beat wrote until=now+--beat-lease, so the friend stays
// awake inside the lease even though both its cursor and its beat stamp are old.
func TestAwakeHonoursBeatLease(t *testing.T) {
	dir := awakeBus(t)
	cursorCommit(t, dir, "rowan", at.Add(-472*time.Second)) // asleep by cursor alone
	write(t, filepath.Join(dir, "from-rowan", "BEAT"),
		at.Add(-500*time.Second).Format(time.RFC3339Nano)+" rowansha until="+at.Add(5*time.Minute).Format(time.RFC3339Nano)+"\n")

	r := wakeRun(t, "awake", "--bus", dir)
	if r.exit != 0 {
		t.Fatalf("awake exit %d, want 0; stderr=%s", r.exit, r.stderr)
	}
	for _, want := range []string{
		"FRIEND rowan awake age=500 source=bus-beat\n",
		"AWAKE OK friends=1 awake=1 asleep=0 unknown=0 window=300\n",
	} {
		if !strings.Contains(r.stdout, want) {
			t.Fatalf("awake output missing %q\nstdout=%s", want, r.stdout)
		}
	}

	// A lease that has run out keeps nobody awake: the beat is old AND its until is past,
	// so the line falls back to its cursor, which is also asleep.
	dir = awakeBus(t)
	cursorCommit(t, dir, "eve", at.Add(-472*time.Second))
	write(t, filepath.Join(dir, "from-eve", "BEAT"),
		at.Add(-500*time.Second).Format(time.RFC3339Nano)+" evesha until="+at.Add(-1*time.Minute).Format(time.RFC3339Nano)+"\n")
	r = wakeRun(t, "awake", "--bus", dir)
	for _, want := range []string{
		"FRIEND eve asleep age=472 source=bus-cursor\n",
		"AWAKE OK friends=1 awake=0 asleep=1 unknown=0 window=300\n",
	} {
		if !strings.Contains(r.stdout, want) {
			t.Fatalf("expired-lease output missing %q\nstdout=%s", want, r.stdout)
		}
	}
}

// THE BEAT WINS OVER THE CURSOR. A waiting line's cursor does not move, so by the cursor
// commit alone it reads asleep -- but its BEAT file keeps advancing every poll, and where
// the beat is newer than the cursor commit the friend reads awake, source=bus-beat. The
// beat is read from the file's own content, not from any commit, so it is written straight
// into the working tree here and nothing is committed.
func TestAwakeReadsBeatOverCursor(t *testing.T) {
	dir := awakeBus(t)
	cursorCommit(t, dir, "dave", at.Add(-600*time.Second)) // old: past --window, asleep by cursor
	write(t, filepath.Join(dir, "from-dave", "BEAT"),
		at.Add(-5*time.Second).Format(time.RFC3339Nano)+" davesha\n")

	r := wakeRun(t, "awake", "--bus", dir)
	if r.exit != 0 {
		t.Fatalf("awake exit %d, want 0; stderr=%s", r.exit, r.stderr)
	}
	for _, want := range []string{
		"FRIEND dave awake age=5 source=bus-beat\n",
		"AWAKE OK friends=1 awake=1 asleep=0 unknown=0 window=300\n",
	} {
		if !strings.Contains(r.stdout, want) {
			t.Fatalf("awake output missing %q\nstdout=%s", want, r.stdout)
		}
	}
}
