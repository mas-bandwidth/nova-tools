package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

// Demanded test 22, the fold's half: A RECORD FILE THAT DOES NOT DECODE IS NEVER SKIPPED.
// The unreadable file may be the hold or the newer red, so its entry is blocked for the
// pass whatever its other records say, and a file whose path names no entry stops the
// pass before any entry is read.

// putRecord writes a file straight into the lane branch at the remote, the way another
// machine's lane would, and returns nothing: what it proves is what the coordinator's
// next pull folds.
func (l *lab) putRecord(path, body string) {
	l.t.Helper()
	side := filepath.Join(l.dir, "side")
	if _, err := os.Stat(side); os.IsNotExist(err) {
		l.git(l.dir, "clone", "-q", "--branch", "nova-merge/lane", l.remote, side)
	}
	l.git(side, "fetch", "-q", "origin", "nova-merge/lane")
	l.git(side, "reset", "-q", "--hard", "FETCH_HEAD")
	full := filepath.Join(side, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		l.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		l.t.Fatal(err)
	}
	l.git(side, "add", "--", path)
	l.git(side, "-c", "user.name=other", "-c", "user.email=other@localhost", "commit", "-q", "-m", "a record from another machine")
	l.git(side, "push", "-q", "origin", "HEAD:refs/heads/nova-merge/lane")
}

func TestAMalformedRecordBlocksItsEntryAndIsNeverSkipped(t *testing.T) {
	l := newLab(t)
	oid := setupPR(t, l, 951, "feature-a", "a.txt", true)
	if exit, _, errb := l.run("run", "--lane", l.lane, "--once"); exit != 0 {
		t.Fatalf("run: %s", errb)
	}
	if exit, _, errb := l.run("read", "--lane", l.lane, "--pr", "951", "--who", "emma",
		"--head", oid, "--verdict", "approve"); exit != 0 {
		t.Fatalf("read: %s", errb)
	}
	base := l.baseSHA()
	_, stdout, _ := l.run("run", "--lane", l.lane, "--once")
	m := mergeSHAOf(t, stdout, "951")
	if exit, _, errb := l.run("gate", "--lane", l.lane, "--pr", "951", "--head", oid,
		"--base-sha", base, "--merge", m, "--verdict", "green", "--summary", l.summary("g")); exit != 0 {
		t.Fatalf("gate: %s", errb)
	}
	// Everything is in place for a merge. Now a truncated record arrives from another
	// machine, in this entry's own directory.
	truncated := `{"who":"stella","verdict":"ho`
	l.putRecord("reads/951/stella-"+oid[:12]+"-20260911T131500Z-abcdef.json", truncated)

	exit, stdout, stderr := l.run("run", "--lane", l.lane, "--once")
	if exit != 1 {
		t.Fatalf("a malformed record blocks its entry: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stderr, "FOLD REFUSED file=reads/951/stella-")
	contains(t, stderr, "MERGE BLOCKED entry=951 reason=malformed_record")
	absent(t, stdout, "MERGE OK")
	for _, p := range l.pushes() {
		if strings.HasPrefix(p, "refs/heads/main") {
			t.Errorf("nothing may be published while a record cannot be read: %s", p)
		}
	}
	// The file is preserved untouched: the fold never repairs anything.
	raw, err := os.ReadFile(filepath.Join(l.lane, "reads", "951", "stella-"+oid[:12]+"-20260911T131500Z-abcdef.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != truncated {
		t.Errorf("the file is byte-identical afterwards, got %q", raw)
	}
}

func TestARecordWhosePathNamesNoEntryStopsThePass(t *testing.T) {
	l := newLab(t)
	setupPR(t, l, 951, "feature-a", "a.txt", false)
	if exit, _, errb := l.run("run", "--lane", l.lane, "--once"); exit != 0 {
		t.Fatalf("run: %s", errb)
	}
	l.putRecord("reads/README.json", "not a record at all\n")
	exit, stdout, stderr := l.run("run", "--lane", l.lane, "--once")
	if exit != 1 {
		t.Fatalf("exit %d, want 1\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stderr, "RUN STOPPED reason=malformed_record file=reads/README.json")
	absent(t, stdout, "RUN ENTRY")
	absent(t, stdout, "RUN BASE")
}

// Demanded test 19 and 22: a read recorded on ANOTHER MACHINE -- a second lane on the
// same branch -- is one file in the branch, and the coordinator's next pass folds it.
func TestAReadFromAnotherMachineReachesTheCoordinatorsNextPass(t *testing.T) {
	l := newLab(t)
	oid := setupPR(t, l, 951, "feature-a", "a.txt", true)
	if exit, _, errb := l.run("run", "--lane", l.lane, "--once"); exit != 0 {
		t.Fatalf("run: %s", errb)
	}
	// A reader's own lane, on the same branch: joined=true, and it creates nothing.
	readerLane := filepath.Join(l.dir, "emma-lane")
	exit, stdout, stderr := l.run("init", "--lane", readerLane, "--repo", "o/n", "--base", "main", "--lane-branch", "nova-merge/lane")
	if exit != 0 {
		t.Fatalf("a second lane on the same branch: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stdout, "joined=true")
	exit, stdout, stderr = l.run("read", "--lane", readerLane, "--pr", "951", "--who", "emma",
		"--head", oid, "--verdict", "approve")
	if exit != 0 {
		t.Fatalf("read: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stdout, "READ OK entry=951")
	contains(t, stdout, "pushed=true")
	// The coordinator's next pass says what it pulled, so a reader of the log can see
	// that a read recorded on another machine reached this pass.
	_, stdout, _ = l.run("run", "--lane", l.lane, "--once")
	contains(t, stdout, "pulled=1")
	contains(t, stdout, "read=1a/0h")
	// And status shows it as a COUNT, never as a name.
	_, stdout, _ = l.run("status", "--lane", l.lane)
	contains(t, stdout, "reads=1a/0h")
	absent(t, stdout, "emma")
	// The list is behind --reads.
	_, stdout, _ = l.run("status", "--lane", l.lane, "--reads", "951")
	contains(t, stdout, "STATUS READ entry=951 who=emma")
	contains(t, stdout, "head="+merge.Short(oid))
	// Every lane is clean afterwards: a git status in a lane that is not mid-verb is
	// clean, on both machines.
	for _, lane := range []string{l.lane, readerLane} {
		if out := l.git(lane, "status", "--porcelain"); out != "" {
			t.Errorf("%s is not clean:\n%s", lane, out)
		}
	}
}
