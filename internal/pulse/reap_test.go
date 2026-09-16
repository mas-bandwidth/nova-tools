package pulse

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeProcs is the process table a test drives: no ps, no kill, no pid of this machine's.
type fakeProcs struct {
	procs  []Proc
	live   map[int]bool
	killed []int
}

func (f *fakeProcs) List() ([]Proc, error) { return f.procs, nil }
func (f *fakeProcs) Alive(pid int) bool    { return f.live[pid] }
func (f *fakeProcs) Kill(pid int) error {
	f.killed = append(f.killed, pid)
	f.live[pid] = false
	return nil
}

func age(t *testing.T, path string, d time.Duration, now time.Time) {
	t.Helper()
	when := now.Add(-d)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
}

func writeCard(t *testing.T, dir, name, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func reapFixture(t *testing.T, now time.Time) (root, queue string, procs *fakeProcs) {
	t.Helper()
	root, queue = t.TempDir(), t.TempDir()

	// a 3-hour-old swarm process under the root, a young one, one on another machine's path,
	// and this very process — the reaper never finds itself (the 19 orphaned shells, 2026-09-10).
	procs = &fakeProcs{
		live: map[int]bool{4001: true, 4002: true, 4003: true, os.Getpid(): true, 9002: true},
		procs: []Proc{
			{PID: 4001, Age: 3 * time.Hour, Args: "nova-swarm-swarmtest supervise " + filepath.Join(root, "12", "jobs", "card-569")},
			{PID: 4002, Age: 5 * time.Minute, Args: "nova-swarm-swarmtest supervise " + filepath.Join(root, "13", "jobs", "card-1400")},
			{PID: 4003, Age: 9 * time.Hour, Args: "sbcl --load /elsewhere/nova-work/run.lisp"},
			{PID: os.Getpid(), Age: 4 * time.Hour, Args: "nova-pulse reap --roots " + root},
		},
	}

	// a slot lock whose pid is dead, and one whose pid is alive.
	for slot, pid := range map[string]int{"3": 9001, "4": 9002} {
		dir := filepath.Join(root, slot)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		writeCard(t, dir, "BATCH", "id=pulse-7 pid="+strconv.Itoa(pid)+" at=2026-09-16T12:00:00Z\n")
	}

	// three launched cards: one orphaned and overdue, one whose job is still on the bench,
	// one launched a minute ago.
	launched := filepath.Join(queue, "launched")
	for _, d := range []string{"pending", "failed", "launched"} { // relative: joining the absolute launched path under queue is garbage on Windows (the dev leg, 2026-09-16)
		if err := os.MkdirAll(filepath.Join(queue, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	age(t, writeCard(t, launched, "card-100.md", "RESULT: CARD-100 orphan\n"), 4*time.Hour, now)
	age(t, writeCard(t, launched, "card-101.md", "RESULT: CARD-101 running\n"), 4*time.Hour, now)
	age(t, writeCard(t, launched, "card-102.md", "RESULT: CARD-102 just launched\n"), time.Minute, now)
	if err := os.MkdirAll(filepath.Join(root, "5", "jobs", "card-101"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root, queue, procs
}

func runReap(t *testing.T, root, queue string, procs ProcessTable, dry bool, now time.Time, tempGlob string) (int, string, string) {
	t.Helper()
	var out, errs bytes.Buffer
	code := Reap(ReapInput{
		Roots: root, Queue: queue, Deadline: 30 * time.Minute, DryRun: dry,
		Procs: procs, TempGlob: tempGlob, TempAge: 30 * time.Minute,
		Now: func() time.Time { return now }, Stdout: &out, Stderr: &errs,
	})
	return code, out.String(), errs.String()
}

// reap-kills-overdue-and-clears-dead-locks: a process under a swarm root older than the
// deadline dies, a young one and one outside the roots live, the reaper never kills itself,
// and a slot lock whose pid is dead is removed — pit stop 3 bug 10 (issue #828, class E).
func TestReapKillsOverdueProcessesAndClearsDeadLocks(t *testing.T) {
	now := time.Date(2026, 9, 16, 18, 0, 0, 0, time.UTC)
	root, queue, procs := reapFixture(t, now)

	code, out, errs := runReap(t, root, queue, procs, false, now, filepath.Join(t.TempDir(), "*swarmtest*"))
	if code != 0 {
		t.Fatalf("exit = %d, stderr=%s", code, errs)
	}
	if want := []int{4001}; !slices.Equal(procs.killed, want) {
		t.Errorf("killed = %v, want %v (old under a root dies; young, foreign and self live)", procs.killed, want)
	}
	if _, err := os.Stat(filepath.Join(root, "3", "BATCH")); !os.IsNotExist(err) {
		t.Errorf("the lock of a dead pid is still there: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "4", "BATCH")); err != nil {
		t.Errorf("the lock of a live pid was removed: %v", err)
	}
	if !strings.Contains(out, "killed=1") || !strings.Contains(out, "locks=1") {
		t.Errorf("REAP line = %q, want killed=1 and locks=1", out)
	}
	if n := len(strings.Split(strings.TrimRight(out, "\n"), "\n")); n != 1 {
		t.Errorf("stdout is %d lines, want one REAP line:\n%s", n, out)
	}
}

// a launched card whose job dir is gone and whose launch is older than the deadline is
// requeued once, under an attempt file, and failed on the second reap. A card whose job is
// on the bench, and one launched a minute ago, are left alone.
func TestReapRequeuesOnceThenFails(t *testing.T) {
	now := time.Date(2026, 9, 16, 18, 0, 0, 0, time.UTC)
	root, queue, procs := reapFixture(t, now)
	tempGlob := filepath.Join(t.TempDir(), "*swarmtest*")

	if _, out, errs := runReap(t, root, queue, procs, false, now, tempGlob); !strings.Contains(out, "requeued=1") {
		t.Fatalf("first reap = %q (stderr %q), want requeued=1", out, errs)
	}
	if _, err := os.Stat(filepath.Join(queue, "pending", "card-100.md")); err != nil {
		t.Errorf("the orphan was not requeued into pending: %v", err)
	}
	if _, err := os.Stat(filepath.Join(queue, "attempts", "card-100.md")); err != nil {
		t.Errorf("the requeue wrote no attempt file: %v", err)
	}
	for _, name := range []string{"card-101.md", "card-102.md"} {
		if _, err := os.Stat(filepath.Join(queue, "launched", name)); err != nil {
			t.Errorf("%s was disposed and should not have been: %v", name, err)
		}
	}

	// the card goes out again and is orphaned again: the second reap fails it, never a third try.
	age(t, writeCard(t, filepath.Join(queue, "launched"), "card-100.md", "RESULT: CARD-100 orphan\n"), 4*time.Hour, now)
	if err := os.Remove(filepath.Join(queue, "pending", "card-100.md")); err != nil {
		t.Fatal(err)
	}
	_, out, _ := runReap(t, root, queue, procs, false, now, tempGlob)
	if !strings.Contains(out, "failed=1") {
		t.Errorf("second reap = %q, want failed=1", out)
	}
	if _, err := os.Stat(filepath.Join(queue, "failed", "card-100.md")); err != nil {
		t.Errorf("the twice-orphaned card is not in failed: %v", err)
	}
}

// the test binaries the swarm's own tests leak die at 30 minutes, and never a young one.
func TestReapRemovesOldSwarmtestTempDirs(t *testing.T) {
	now := time.Date(2026, 9, 16, 18, 0, 0, 0, time.UTC)
	root, queue, procs := reapFixture(t, now)
	tmp := t.TempDir()
	old := filepath.Join(tmp, "nova-swarm-swarmtest-old")
	fresh := filepath.Join(tmp, "nova-swarm-swarmtest-fresh")
	for _, d := range []string{old, fresh} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	age(t, old, 2*time.Hour, now)
	age(t, fresh, time.Minute, now)

	_, out, _ := runReap(t, root, queue, procs, false, now, filepath.Join(tmp, "*swarmtest*"))
	if !strings.Contains(out, "temp=1") {
		t.Errorf("REAP line = %q, want temp=1", out)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("the two-hour-old temp dir is still there: %v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("the one-minute-old temp dir was removed: %v", err)
	}
}

// --dry-run changes nothing and prints the same counts: the reaper is read first, act second.
func TestReapDryRunChangesNothingAndCountsTheSame(t *testing.T) {
	now := time.Date(2026, 9, 16, 18, 0, 0, 0, time.UTC)
	root, queue, procs := reapFixture(t, now)
	tmp := t.TempDir()
	old := filepath.Join(tmp, "swarmtest-old")
	if err := os.MkdirAll(old, 0o755); err != nil {
		t.Fatal(err)
	}
	age(t, old, 2*time.Hour, now)

	_, dry, errs := runReap(t, root, queue, procs, true, now, filepath.Join(tmp, "*swarmtest*"))
	if len(procs.killed) != 0 {
		t.Errorf("--dry-run killed %v", procs.killed)
	}
	for _, path := range []string{filepath.Join(root, "3", "BATCH"), filepath.Join(queue, "launched", "card-100.md"), old} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("--dry-run changed %s: %v", path, err)
		}
	}
	if !strings.Contains(dry, "dry-run=true") {
		t.Errorf("REAP line = %q, want dry-run=true; stderr=%q", dry, errs)
	}

	_, wet, _ := runReap(t, root, queue, procs, false, now, filepath.Join(tmp, "*swarmtest*"))
	strip := func(s string) string {
		return strings.ReplaceAll(strings.TrimSpace(s), "dry-run=true", "dry-run=false")
	}
	if strip(dry) != strip(wet) {
		t.Errorf("the counts differ between the dry run and the real one:\ndry: %s\nwet: %s", strip(dry), strip(wet))
	}
}

// a missing --queue is a refusal with a remedy, never a silent reap.
func TestReapRefusesAMissingQueue(t *testing.T) {
	var out, errs bytes.Buffer
	code := Reap(ReapInput{
		Roots: t.TempDir(), Queue: filepath.Join(t.TempDir(), "nope"), Deadline: time.Minute,
		Procs: &fakeProcs{live: map[int]bool{}}, Now: time.Now, Stdout: &out, Stderr: &errs,
	})
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errs.String(), "REAP REFUSED") || !strings.Contains(errs.String(), "(") {
		t.Errorf("the refusal names no remedy: %q", errs.String())
	}
}
