package main

// The red tests for the runner `_diag` prune, written before the machine.
//
// The mistake they remove, measured on hulk on 2026-09-18: 24 GitHub Actions
// runner directories held 3.5 GB of `_diag` between them, 18,296 files, and the
// OLDEST file on the bench was two days old. Nothing in the tree was older than
// that, because a runner rolls its own diagnostics; an age window alone can
// therefore never bound the directory, however short it is. The bench writes
// about 1.8 GB of `_diag` a day and would have kept every byte of it.
//
// So the prune is two rules, not one, and both are tested here: an age window
// (two days, not seven) AND a size cap per runner directory (2 GiB by default,
// oldest file first). The cap is what actually bounds a bench; the window is
// what keeps a quiet bench tidy. A file is never removed by a path built from
// text: every deletion goes through internal/safepath below that runner's own
// `_diag` directory.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// diagFile writes one `_diag` log of n bytes with the given mtime and returns
// its path.
func diagFile(t *testing.T, home, runner, name string, n int, when time.Time) string {
	t.Helper()
	p := filepath.Join(home, runner, "_diag", name)
	hygieneWrite(t, p, strings.Repeat("d", n), when)
	return p
}

// hygiene-prunes-diag-older-than-two-days: with a fake clock, a `_diag` log
// three days old goes and one a day old stays. Seven days would have kept both,
// which is the bug: on the real bench nothing is ever seven days old.
func TestHygienePrunesDiagOlderThanTwoDays(t *testing.T) {
	now := time.Date(2026, 9, 18, 18, 0, 0, 0, time.UTC)
	home := t.TempDir()

	stale := diagFile(t, home, "runner-nova-tools-1", "Worker_old-utc.log", 10, now.Add(-72*time.Hour))
	fresh := diagFile(t, home, "runner-nova-tools-1", "Worker_new-utc.log", 10, now.Add(-24*time.Hour))
	// Older than two days but it is the only file left after the stale one goes
	// in the OTHER runner: a runner whose whole directory is stale still keeps
	// its newest file, because the runner process holds it open.
	onlyOne := diagFile(t, home, "runner-nova-tools-2", "Worker_solo-utc.log", 10, now.Add(-96*time.Hour))

	defer swapHygieneEnv(hygieneFakeProcs{}, hygieneFakeDisk{freeGB: 40, free: "40G", sizeGB: 0})()

	code, out, errb := hygieneRun(t, now, "run", "--home", home, "--hostname", "bench")
	if code != 0 {
		t.Fatalf("hygiene run exit = %d, stderr=%s", code, errb)
	}
	if hygieneExists(stale) {
		t.Errorf("a _diag log three days old survived the two-day window: %s", stale)
	}
	if !hygieneExists(fresh) {
		t.Errorf("a _diag log one day old was removed by the two-day window: %s", fresh)
	}
	if !hygieneExists(onlyOne) {
		t.Errorf("the newest file of a runner is never removed, however old it is (the runner holds it open): %s", onlyOne)
	}
	if !strings.Contains(out, "diag-deleted=1") {
		t.Errorf("the HYGIENE line does not carry diag-deleted=1:\n%s", out)
	}
	if !strings.Contains(out, "diag-freed=10") {
		t.Errorf("the HYGIENE line does not carry the bytes it freed:\n%s", out)
	}
}

// hygiene-diag-size-cap-deletes-oldest-first: every file is inside the window,
// so only the cap can bound the directory. It deletes oldest first and stops the
// moment the directory is at or below the cap.
func TestHygieneDiagSizeCapDeletesOldestFirst(t *testing.T) {
	now := time.Date(2026, 9, 18, 18, 0, 0, 0, time.UTC)
	home := t.TempDir()

	// Four 100-byte logs, all written today, 250 bytes of cap: the two oldest go
	// and the directory lands at 200.
	a := diagFile(t, home, "runner-nova-tools-1", "Worker_a-utc.log", 100, now.Add(-4*time.Hour))
	b := diagFile(t, home, "runner-nova-tools-1", "Worker_b-utc.log", 100, now.Add(-3*time.Hour))
	c := diagFile(t, home, "runner-nova-tools-1", "Worker_c-utc.log", 100, now.Add(-2*time.Hour))
	d := diagFile(t, home, "runner-nova-tools-1", "Worker_d-utc.log", 100, now.Add(-1*time.Hour))

	defer swapHygieneEnv(hygieneFakeProcs{}, hygieneFakeDisk{freeGB: 40, free: "40G", sizeGB: 0})()

	code, out, errb := hygieneRun(t, now, "run", "--home", home, "--hostname", "bench", "--diag-max-bytes", "250")
	if code != 0 {
		t.Fatalf("hygiene run exit = %d, stderr=%s", code, errb)
	}
	if hygieneExists(a) || hygieneExists(b) {
		t.Errorf("the cap did not take the two oldest logs first: a=%v b=%v", hygieneExists(a), hygieneExists(b))
	}
	if !hygieneExists(c) || !hygieneExists(d) {
		t.Errorf("the cap took more than it had to: it stops at or below the cap, and 200 <= 250")
	}
	if !strings.Contains(out, "diag-deleted=2") || !strings.Contains(out, "diag-freed=200") {
		t.Errorf("the HYGIENE line does not carry diag-deleted=2 diag-freed=200:\n%s", out)
	}
}

// hygiene-diag-cap-is-per-runner-directory: two runner directories each under
// the cap, together over it. Nothing goes. The cap bounds a runner, not a bench,
// because a runner is what writes into its own `_diag`.
func TestHygieneDiagCapIsPerRunnerDirectory(t *testing.T) {
	now := time.Date(2026, 9, 18, 18, 0, 0, 0, time.UTC)
	home := t.TempDir()

	var kept []string
	for _, runner := range []string{"runner-nova-tools-1", "runner-nova-tools-2"} {
		kept = append(kept,
			diagFile(t, home, runner, "Worker_a-utc.log", 60, now.Add(-2*time.Hour)),
			diagFile(t, home, runner, "Worker_b-utc.log", 60, now.Add(-time.Hour)),
		)
	}

	defer swapHygieneEnv(hygieneFakeProcs{}, hygieneFakeDisk{freeGB: 40, free: "40G", sizeGB: 0})()

	code, out, errb := hygieneRun(t, now, "run", "--home", home, "--hostname", "bench", "--diag-max-bytes", "200")
	if code != 0 {
		t.Fatalf("hygiene run exit = %d, stderr=%s", code, errb)
	}
	for _, p := range kept {
		if !hygieneExists(p) {
			t.Errorf("a 120-byte runner directory was pruned against a 200-byte per-runner cap: %s", p)
		}
	}
	if !strings.Contains(out, "diag-deleted=0") {
		t.Errorf("nothing should have gone:\n%s", out)
	}
}

// hygiene-diag-dry-run-prints-would-and-deletes-nothing: the same tree under
// --dry-run prints one WOULD line per file it would take and every byte
// survives, so a bench's first run is read before it is felt.
func TestHygieneDiagDryRunDeletesNothing(t *testing.T) {
	now := time.Date(2026, 9, 18, 18, 0, 0, 0, time.UTC)
	home := t.TempDir()

	stale := diagFile(t, home, "runner-nova-tools-1", "Worker_old-utc.log", 10, now.Add(-72*time.Hour))
	fresh := diagFile(t, home, "runner-nova-tools-1", "Worker_new-utc.log", 10, now.Add(-time.Hour))

	defer swapHygieneEnv(hygieneFakeProcs{}, hygieneFakeDisk{freeGB: 40, free: "40G", sizeGB: 0})()

	code, out, errb := hygieneRun(t, now, "run", "--home", home, "--hostname", "bench", "--dry-run")
	if code != 0 {
		t.Fatalf("hygiene run exit = %d, stderr=%s", code, errb)
	}
	if !strings.Contains(out, "WOULD delete-diag "+stale) {
		t.Errorf("--dry-run did not name the file it would take:\n%s", out)
	}
	if !hygieneExists(stale) || !hygieneExists(fresh) {
		t.Errorf("--dry-run deleted a file")
	}
	if _, err := os.Stat(filepath.Join(home, "hygiene.log")); err == nil {
		t.Errorf("--dry-run wrote the action log")
	}
}

// hygiene-diag-never-follows-a-symlink-out: a symlink in `_diag` pointing at a
// file outside the runner directory is never followed and its target is never
// removed, however old the link is. safepath decides every deletion; the path is
// never built from what the tree says.
func TestHygieneDiagNeverFollowsASymlinkOut(t *testing.T) {
	now := time.Date(2026, 9, 18, 18, 0, 0, 0, time.UTC)
	home := t.TempDir()

	outside := filepath.Join(home, "precious.txt")
	hygieneWrite(t, outside, "keep me\n", now.Add(-200*time.Hour))

	diag := filepath.Join(home, "runner-nova-tools-1", "_diag")
	hygieneMkdir(t, diag)
	link := filepath.Join(diag, "Worker_link-utc.log")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("this filesystem has no symlinks: %v", err)
	}
	// Two real files, and a cap of one byte, so the prune certainly deletes:
	// the older real file goes, and the link and its target are still there.
	old := diagFile(t, home, "runner-nova-tools-1", "Worker_real-a-utc.log", 10, now.Add(-2*time.Hour))
	newest := diagFile(t, home, "runner-nova-tools-1", "Worker_real-b-utc.log", 10, now.Add(-time.Hour))

	defer swapHygieneEnv(hygieneFakeProcs{}, hygieneFakeDisk{freeGB: 40, free: "40G", sizeGB: 0})()

	code, _, errb := hygieneRun(t, now, "run", "--home", home, "--hostname", "bench", "--diag-max-bytes", "1")
	if code != 0 {
		t.Fatalf("hygiene run exit = %d, stderr=%s", code, errb)
	}
	if hygieneExists(old) {
		t.Errorf("the cap did not delete at all, so this test proves nothing: %s", old)
	}
	if !hygieneExists(newest) {
		t.Errorf("the newest real file must survive: %s", newest)
	}
	if !hygieneExists(link) {
		t.Errorf("the prune removed the symlink itself: %s", link)
	}
	if !hygieneExists(outside) {
		t.Fatalf("the prune followed a symlink out of the runner directory and deleted %s", outside)
	}
}

// hygiene-diag-flags-refuse-nonsense: --diag-max-bytes and --diag-days each take
// a number, and a bad one is exit 2 with a remedy line, never a silent default
// that deletes on a guess.
func TestHygieneDiagFlagsRefuseNonsense(t *testing.T) {
	now := time.Date(2026, 9, 18, 18, 0, 0, 0, time.UTC)
	home := t.TempDir()
	defer swapHygieneEnv(hygieneFakeProcs{}, hygieneFakeDisk{freeGB: 40, free: "40G", sizeGB: 0})()

	for _, bad := range [][]string{
		{"--diag-max-bytes", "lots"},
		{"--diag-max-bytes", "-1"},
		{"--diag-days", "never"},
		{"--diag-days", "-3"},
	} {
		args := append([]string{"run", "--home", home}, bad...)
		code, _, errb := hygieneRun(t, now, args...)
		if code != 2 {
			t.Errorf("%v: exit = %d, want 2", bad, code)
		}
		if !strings.Contains(errb, bad[0]) {
			t.Errorf("%v: the refusal does not name the flag: %s", bad, errb)
		}
	}
}

// hygiene-diag-default-cap-is-two-gibibytes: the default the bench runs with,
// pinned so a change to it is a change to this line. 2 GiB per runner over 24
// runners is 48 GiB, against the 3.5 GB hulk held unbounded.
func TestHygieneDiagDefaultCapIsTwoGibibytes(t *testing.T) {
	if diagMaxBytesDefault != 2*1024*1024*1024 {
		t.Fatalf("the default --diag-max-bytes is %d, want 2 GiB", diagMaxBytesDefault)
	}
	if diagDaysDefault != 2 {
		t.Fatalf("the default --diag-days is %d, want 2", diagDaysDefault)
	}
}
