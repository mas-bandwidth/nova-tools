package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

// hygieneProcs and hygieneDisk are the two doors tests replace so no test reads
// /proc or df; cmdHygiene reads them the way nova-swarm reads PATH.
type hygieneFakeProcs struct{ busy map[string]bool }

func (f hygieneFakeProcs) Busy(path string) bool { return f.busy[path] }

type hygieneFakeDisk struct {
	freeGB int
	sizeGB int
	free   string
}

func (f hygieneFakeDisk) FreeGB() int       { return f.freeGB }
func (f hygieneFakeDisk) Free() string      { return f.free }
func (f hygieneFakeDisk) SizeGB(string) int { return f.sizeGB }

func swapHygieneEnv(procs pulse.HygieneProcs, disk pulse.HygieneDisk) func() {
	oldProcs, oldDisk := hygieneProcs, hygieneDisk
	hygieneProcs = procs
	hygieneDisk = func(string) pulse.HygieneDisk { return disk }
	return func() { hygieneProcs, hygieneDisk = oldProcs, oldDisk }
}

func hygieneMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func hygieneWrite(t *testing.T, p, body string, when time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if !when.IsZero() {
		if err := os.Chtimes(p, when, when); err != nil {
			t.Fatal(err)
		}
	}
}

func hygieneExists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// hygieneRun calls the verb the way a bench would and returns code, stdout, stderr.
func hygieneRun(t *testing.T, now time.Time, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(append([]string{"hygiene"}, args...), &out, &errb, now)
	return code, out.String(), errb.String()
}

var hygieneLogLine = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z (reap|delete-job|delete-slot|delete-diag|drop-cache|HYGIENE) .+$`)

// hygiene-run-reaps-dead-and-leaves-live: a fake bench tree with a live slot, a
// slot a process names, a dead slot and an empty slot. `run` reaps the dead,
// deletes the harvested and stale jobs and the emptied slots, leaves the live
// ones alone, and writes one HYGIENE line plus one log line per deletion.
func TestHygieneRunReapsDeadAndLeavesLive(t *testing.T) {
	now := time.Date(2026, 9, 17, 18, 0, 0, 0, time.UTC)
	home := t.TempDir()
	root1 := filepath.Join(home, "rowan-swarm-root")
	root2 := filepath.Join(home, "rowan-working", "tmp")

	live := filepath.Join(root1, "live")
	hygieneWrite(t, filepath.Join(live, "jobs", "card-live", "harness-output.log"), "running\n", now.Add(-time.Minute))

	busy := filepath.Join(root1, "busy")
	hygieneMkdir(t, filepath.Join(busy, "jobs", "card-busy"))

	dead := filepath.Join(root1, "dead")
	hygieneMkdir(t, filepath.Join(dead, "data"))
	hygieneMkdir(t, filepath.Join(dead, "tmp"))
	oldJob := filepath.Join(dead, "jobs", "card-old")
	hygieneMkdir(t, filepath.Join(oldJob, "scratch"))
	hygieneMkdir(t, filepath.Join(oldJob, ".nova-sandbox-tmp"))
	hygieneMkdir(t, filepath.Join(oldJob, "repo", "scratch"))
	hygieneWrite(t, filepath.Join(oldJob, "harness-output.log"), "old\n", now.Add(-7*time.Hour))

	hygieneMkdir(t, filepath.Join(root1, "empty"))

	harvested := filepath.Join(root1, "harvested")
	hygieneMkdir(t, filepath.Join(harvested, "jobs", "card-read"))
	hygieneWrite(t, filepath.Join(harvested, "jobs", "card-read", ".harvested"), "", time.Time{})

	old := filepath.Join(root2, "old")
	hygieneMkdir(t, filepath.Join(old, "data"))
	hygieneMkdir(t, filepath.Join(old, "jobs", "card-tmp"))
	hygieneWrite(t, filepath.Join(old, "jobs", "card-tmp", ".harvested"), "", time.Time{})

	defer swapHygieneEnv(hygieneFakeProcs{busy: map[string]bool{busy: true}}, hygieneFakeDisk{freeGB: 40, free: "40G", sizeGB: 0})()

	code, out, errb := hygieneRun(t, now, "run", "--home", home, "--hostname", "bench")
	if code != 0 {
		t.Fatalf("hygiene run exit = %d, stderr=%s", code, errb)
	}
	// diag-deleted and diag-freed join the line with the runner `_diag` prune:
	// this fixture has no runner directory, so both are zero and every field is
	// still named.
	wantLine := "HYGIENE bench slots=6 reaped=2 jobs-deleted=3 slots-deleted=4 diag-deleted=0 diag-freed=0 cache=kept(0G) free 40G -> 40G"
	if got := strings.TrimSpace(out); got != wantLine {
		t.Fatalf("the one HYGIENE line:\n got: %s\nwant: %s", got, wantLine)
	}

	for _, keep := range []string{
		live, filepath.Join(live, "jobs", "card-live", "harness-output.log"),
		busy, filepath.Join(busy, "jobs", "card-busy"),
	} {
		if !hygieneExists(keep) {
			t.Errorf("run removed %s, a live slot", keep)
		}
	}
	for _, gone := range []string{
		dead, filepath.Join(root1, "empty"), harvested, old,
		filepath.Join(root1, "dead", "data"),
		filepath.Join(oldJob, "scratch"),
		filepath.Join(oldJob, ".nova-sandbox-tmp"),
		filepath.Join(oldJob, "repo", "scratch"),
	} {
		if hygieneExists(gone) {
			t.Errorf("run left %s, a dead or harvested thing", gone)
		}
	}

	raw, err := os.ReadFile(filepath.Join(home, "hygiene.log"))
	if err != nil {
		t.Fatalf("run wrote no hygiene.log: %v", err)
	}
	log := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	for _, line := range log {
		if !hygieneLogLine.MatchString(line) {
			t.Errorf("hygiene.log line does not match <utc> <verb> <path>: %q", line)
		}
	}
	stamp := now.Format(time.RFC3339)
	want := map[string]bool{
		stamp + " reap " + filepath.Join(dead, "data"):                         false,
		stamp + " reap " + filepath.Join(dead, "tmp"):                          false,
		stamp + " reap " + filepath.Join(oldJob, "scratch"):                    false,
		stamp + " reap " + filepath.Join(oldJob, ".nova-sandbox-tmp"):          false,
		stamp + " reap " + filepath.Join(oldJob, "repo", "scratch"):            false,
		stamp + " delete-job " + oldJob:                                        false,
		stamp + " delete-slot " + dead:                                         false,
		stamp + " delete-slot " + filepath.Join(root1, "empty"):                false,
		stamp + " delete-job " + filepath.Join(harvested, "jobs", "card-read"): false,
		stamp + " delete-slot " + harvested:                                    false,
		stamp + " reap " + filepath.Join(old, "data"):                          false,
		stamp + " delete-job " + filepath.Join(old, "jobs", "card-tmp"):        false,
		stamp + " delete-slot " + old:                                          false,
		stamp + " " + wantLine:                                                 false,
	}
	for _, line := range log {
		if _, ok := want[line]; ok {
			want[line] = true
		}
	}
	for line, seen := range want {
		if !seen {
			t.Errorf("hygiene.log is missing %q", line)
		}
	}
	if len(log) != len(want) {
		t.Errorf("hygiene.log has %d lines, want %d:\n%s", len(log), len(want), raw)
	}
}

// hygiene-run-dry-run-changes-nothing: WOULD lines on stdout, the same HYGIENE
// line, and no hygiene.log at all.
func TestHygieneRunDryRunChangesNothing(t *testing.T) {
	now := time.Date(2026, 9, 17, 18, 0, 0, 0, time.UTC)
	home := t.TempDir()
	root := filepath.Join(home, "rowan-swarm-root")
	dead := filepath.Join(root, "dead")
	hygieneMkdir(t, filepath.Join(dead, "data"))
	defer swapHygieneEnv(hygieneFakeProcs{busy: map[string]bool{}}, hygieneFakeDisk{freeGB: 40, free: "40G"})()

	code, out, errb := hygieneRun(t, now, "run", "--home", home, "--hostname", "bench", "--dry-run")
	if code != 0 {
		t.Fatalf("hygiene run --dry-run exit = %d, stderr=%s", code, errb)
	}
	if !strings.Contains(out, "WOULD reap "+filepath.Join(dead, "data")) {
		t.Errorf("--dry-run printed no WOULD line: %q", out)
	}
	if !strings.Contains(out, "HYGIENE bench") {
		t.Errorf("--dry-run printed no HYGIENE line: %q", out)
	}
	if !hygieneExists(filepath.Join(dead, "data")) {
		t.Errorf("--dry-run removed the data dir")
	}
	if hygieneExists(filepath.Join(home, "hygiene.log")) {
		t.Errorf("--dry-run wrote hygiene.log")
	}
}

// each verb's one-line refusal: a missing slot, a ".." job, a symlink slot that
// escapes the root, a ".." slot, and an outside cache all exit 2 and name what
// they refused.
func TestHygieneRefusesPathsOutsideTheRoots(t *testing.T) {
	now := time.Date(2026, 9, 17, 18, 0, 0, 0, time.UTC)
	home := t.TempDir()
	root := filepath.Join(home, "rowan-swarm-root")
	hygieneMkdir(t, root)

	outside := t.TempDir()
	hygieneWrite(t, filepath.Join(outside, "keep"), "x", time.Time{})
	outsideCache := filepath.Join(outside, "cache")
	hygieneMkdir(t, outsideCache)
	if err := os.Symlink(outside, filepath.Join(root, "evil")); err != nil {
		t.Fatal(err)
	}

	slot := filepath.Join(root, "slot")
	hygieneMkdir(t, filepath.Join(slot, "jobs", "card-1"))

	defer swapHygieneEnv(hygieneFakeProcs{busy: map[string]bool{}}, hygieneFakeDisk{freeGB: 40, free: "40G"})()

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"missing slot", []string{"reap", "nosuch"}, "nosuch"},
		{"dotdot slot", []string{"reap", "../escape"}, "../escape"},
		{"symlink slot", []string{"delete-slot", "evil"}, "evil"},
		{"dotdot job", []string{"delete-job", "slot", ".."}, ".."},
		{"missing job", []string{"delete-job", "slot", "nosuch"}, "nosuch"},
		{"non-empty slot", []string{"delete-slot", "slot"}, "slot"},
		{"outside cache", []string{"drop-cache", "--cache", outsideCache}, "cache"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{}, tc.args...)
			args = append(args, "--home", home)
			code, _, errb := hygieneRun(t, now, args...)
			if code != 2 {
				t.Fatalf("exit = %d, want 2; stderr=%q", code, errb)
			}
			if !strings.Contains(errb, tc.want) || !strings.Contains(errb, "(") {
				t.Fatalf("refusal = %q, want it to name %q and one remedy", errb, tc.want)
			}
			if n := len(strings.Split(strings.TrimRight(errb, "\n"), "\n")); n != 1 {
				t.Fatalf("refusal is %d lines, want 1:\n%s", n, errb)
			}
		})
	}
	if !hygieneExists(filepath.Join(outside, "keep")) {
		t.Fatalf("a refusal followed a symlink out of the roots")
	}
}

// the removal verbs do their one job and write the <utc> <verb> <path> line.
func TestHygieneDeleteAndDropVerbs(t *testing.T) {
	now := time.Date(2026, 9, 17, 18, 0, 0, 0, time.UTC)
	home := t.TempDir()
	root := filepath.Join(home, "rowan-swarm-root")
	slot := filepath.Join(root, "slot")
	job := filepath.Join(slot, "jobs", "card-1")
	hygieneMkdir(t, job)
	hygieneMkdir(t, filepath.Join(slot, "data"))
	hygieneMkdir(t, filepath.Join(job, "scratch"))

	cache := filepath.Join(home, ".cache", "go-build")
	hygieneMkdir(t, cache)

	defer swapHygieneEnv(hygieneFakeProcs{busy: map[string]bool{}}, hygieneFakeDisk{freeGB: 40, free: "40G"})()

	if code, _, errb := hygieneRun(t, now, "reap", "slot", "--home", home); code != 0 {
		t.Fatalf("reap exit = %d, stderr=%s", code, errb)
	}
	if hygieneExists(filepath.Join(slot, "data")) || hygieneExists(filepath.Join(job, "scratch")) {
		t.Errorf("reap left data or scratch")
	}
	if !hygieneExists(job) {
		t.Errorf("reap removed the job dir; it may only take the scratch")
	}

	if code, _, errb := hygieneRun(t, now, "delete-job", "slot", "card-1", "--home", home); code != 0 {
		t.Fatalf("delete-job exit = %d, stderr=%s", code, errb)
	}
	if hygieneExists(job) {
		t.Errorf("delete-job left %s", job)
	}

	if code, _, errb := hygieneRun(t, now, "delete-slot", "slot", "--home", home); code != 0 {
		t.Fatalf("delete-slot exit = %d, stderr=%s", code, errb)
	}
	if hygieneExists(slot) {
		t.Errorf("delete-slot left %s", slot)
	}

	if code, _, errb := hygieneRun(t, now, "drop-cache", "--home", home); code != 0 {
		t.Fatalf("drop-cache exit = %d, stderr=%s", code, errb)
	}
	if hygieneExists(cache) {
		t.Errorf("drop-cache left %s", cache)
	}

	raw, err := os.ReadFile(filepath.Join(home, "hygiene.log"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		now.Format(time.RFC3339) + " reap " + filepath.Join(slot, "data"),
		now.Format(time.RFC3339) + " reap " + filepath.Join(job, "scratch"),
		now.Format(time.RFC3339) + " delete-job " + job,
		now.Format(time.RFC3339) + " delete-slot " + slot,
		now.Format(time.RFC3339) + " drop-cache " + cache,
	}
	got := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(got) != len(want) {
		t.Fatalf("log has %d lines, want %d:\n%s", len(got), len(want), raw)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("log[%d] = %q, want %q", i, got[i], w)
		}
	}
}

// hygiene log [n] prints the last n lines of the per-bench log.
func TestHygieneLogPrintsTail(t *testing.T) {
	home := t.TempDir()
	logPath := filepath.Join(home, "hygiene.log")
	hygieneWrite(t, logPath, "one\ntwo\nthree\nfour\n", time.Time{})
	defer swapHygieneEnv(hygieneFakeProcs{busy: map[string]bool{}}, hygieneFakeDisk{freeGB: 40, free: "40G"})()

	code, out, errb := hygieneRun(t, time.Now().UTC(), "log", "2", "--home", home)
	if code != 0 {
		t.Fatalf("log exit = %d, stderr=%s", code, errb)
	}
	if out != "three\nfour\n" {
		t.Fatalf("log 2 = %q, want the last two lines", out)
	}
}
