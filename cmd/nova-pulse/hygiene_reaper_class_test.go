package main

// hygiene_reaper_class_test.go is issue #1512: the Go verb's port of the class
// test scripts/bench-hygiene_test.sh wrote for the shell reaper in #1511.
//
// THE CLASS IS: WHAT MAY THIS TOOL DELETE. Two benches lost a certify tree,
// corpus and all, and a 43-card swarm root, to a reaper that deleted on SHAPE
// (any directory under a swarm root that was not a slot) and judged a live card
// dead by SILENCE (a quiet fifteen minutes took its HOME and TMPDIR out from
// under it). The shell script stopped doing both in #1511. The Go verb, which is
// what replaces that script on every bench, still did. So:
//
//	(a) a bare directory under either swarm root survives a run, whatever its age;
//	(b) a job with a live lease is never touched, even when it has been quiet for
//	    hours, and neither are its slot's data/ (the card's HOME) and tmp/ (its TMPDIR);
//	(c) an unleased job newer than 6 h survives; one older than 6 h goes;
//	(d) a freshly emptied slot is not deleted; an empty slot quiet for 6 h is;
//	(e) the go build cache is never dropped while any lease is live.
//
// The second pass proves the lease is not a permanent shield: with its pid gone
// and its heartbeat stale, the same job and the same cache go.
//
// Liveness is read with internal/swarm's own JobLease, which is what
// `nova-swarm native` WRITES. One definition, not two: a reaper that re-spells
// the launcher's rule is a reaper that can disagree with it.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// hygieneAge sets a path's mtime, which is what every age rule reads.
func hygieneAge(t *testing.T, when time.Time, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if err := os.Chtimes(p, when, when); err != nil {
			t.Fatalf("chtimes %s: %v", p, err)
		}
	}
}

// hygieneLiveChild starts a real process and returns its pid and a stopper, so a
// lease can name a pid this kernel will answer for. The shell class test does
// the same with `sleep 600`.
func hygieneLiveChild(t *testing.T) (int, func()) {
	t.Helper()
	cmd := exec.Command("sleep", "600")
	if err := cmd.Start(); err != nil {
		t.Skipf("no sleep on this host: %v", err)
	}
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}
	t.Cleanup(stop)
	return cmd.Process.Pid, stop
}

// TestHygieneReaperClass is the whole class in one fixture, because the rules
// are about a tree and not about a path: a rule that keeps one directory by
// deleting its neighbour is not the rule anybody wanted.
func TestHygieneReaperClass(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	old := now.Add(-8 * time.Hour)   // past the 6 h any deletion needs
	young := now.Add(-2 * time.Hour) // merely quiet
	home := t.TempDir()
	root1 := filepath.Join(home, "rowan-swarm-root")
	root2 := filepath.Join(home, "rowan-working", "tmp")

	// (a) two bare directories, one under each root: somebody's work, not a slot.
	bare1 := filepath.Join(root1, "certify-tree")
	bare2 := filepath.Join(root2, "toolchains")
	hygieneWrite(t, filepath.Join(bare1, "corpus", "data.txt"), "the corpus\n", old)
	hygieneWrite(t, filepath.Join(bare2, "REPORT.md"), "the report\n", old)

	// (b) a leased slot: one job quiet for 8 hours, holding a lease with a live
	// pid and a fresh heartbeat, beside the card's HOME and TMPDIR.
	leased := filepath.Join(root1, "slot-leased")
	ljob := filepath.Join(leased, "jobs", "job-quiet")
	hygieneWrite(t, filepath.Join(ljob, "harness-output.log"), "a long model call\n", old)
	hygieneMkdir(t, filepath.Join(leased, "data"))
	hygieneMkdir(t, filepath.Join(leased, "tmp", "job-quiet"))

	// (c) an unleased young job and an unleased old one, in their own slots.
	youngSlot := filepath.Join(root1, "slot-young")
	youngJob := filepath.Join(youngSlot, "jobs", "job-young")
	hygieneWrite(t, filepath.Join(youngJob, "harness-output.log"), "working\n", young)
	oldSlot := filepath.Join(root1, "slot-old")
	oldJob := filepath.Join(oldSlot, "jobs", "job-old")
	hygieneWrite(t, filepath.Join(oldJob, "harness-output.log"), "crashed\n", old)

	// a harvested job goes whatever its age: it has been read.
	harv := filepath.Join(root2, "slot-harvested")
	harvJob := filepath.Join(harv, "jobs", "job-read")
	hygieneWrite(t, filepath.Join(harvJob, "harness-output.log"), "done\n", old)
	hygieneWrite(t, filepath.Join(harvJob, "RESULT.md"), "", old)
	hygieneWrite(t, filepath.Join(harvJob, ".harvested"), "", old)

	// (d) an empty slot quiet for 8 hours.
	stale := filepath.Join(root1, "slot-stale")
	hygieneMkdir(t, filepath.Join(stale, "jobs"))

	// (e) the build cache.
	cache := filepath.Join(home, ".cache", "go-build")
	hygieneWrite(t, filepath.Join(cache, "trim.txt.d", "x"), "x\n", time.Time{})

	// Age from the leaves up: writing a child touches its parent.
	hygieneAge(t, old,
		filepath.Join(bare1, "corpus"), bare1,
		filepath.Join(bare2), // REPORT.md's parent
		ljob, filepath.Join(leased, "jobs"), filepath.Join(leased, "data"),
		filepath.Join(leased, "tmp", "job-quiet"), filepath.Join(leased, "tmp"), leased,
		oldJob, filepath.Join(oldSlot, "jobs"), oldSlot,
		harvJob, filepath.Join(harv, "jobs"), harv,
		filepath.Join(stale, "jobs"), stale,
	)
	hygieneAge(t, young, youngJob, filepath.Join(youngSlot, "jobs"), youngSlot)

	// THE LEASE. A real live process stands in for the card's launcher; the file
	// carries its pid and its mtime is the heartbeat, written just now. This is
	// the shape `nova-swarm native` writes.
	pid, stopChild := hygieneLiveChild(t)
	host, _ := os.Hostname()
	hygieneWrite(t, filepath.Join(ljob, ".lease"),
		"pid="+itoa(pid)+"\nhost="+host+"\nlabel=job-quiet\nstarted=2026-09-19T04:00:00Z\n", now)

	// An empty slot made LAST, so nothing has aged it: fresh, and not to be deleted.
	fresh := filepath.Join(root1, "slot-fresh")
	hygieneMkdir(t, filepath.Join(fresh, "jobs"))

	// freeGB under the 25 GB floor forces the cache branch, so (e) is exercised
	// on a bench with room to spare: with a lease live, the cache stays.
	defer swapHygieneEnv(hygieneFakeProcs{busy: map[string]bool{}}, hygieneFakeDisk{freeGB: 1, free: "1G", sizeGB: 0})()

	mustSurvive := []string{
		filepath.Join(bare1, "corpus", "data.txt"),
		filepath.Join(bare2, "REPORT.md"),
		filepath.Join(ljob, "harness-output.log"),
		filepath.Join(leased, "data"),
		filepath.Join(leased, "tmp", "job-quiet"),
		leased,
		filepath.Join(youngJob, "harness-output.log"),
		fresh,
		filepath.Join(cache, "trim.txt.d", "x"),
	}

	// THE DRY RUN proposes the 8 h unleased job and nothing that must survive.
	code, dry, errb := hygieneRun(t, now, "run", "--home", home, "--hostname", "bench", "--dry-run")
	if code != 0 {
		t.Fatalf("hygiene run --dry-run exit = %d, stderr=%s", code, errb)
	}
	if !strings.Contains(dry, " "+oldJob+"\n") {
		t.Errorf("the dry run does not propose %s, an unleased job quiet 8 h:\n%s", oldJob, dry)
	}
	for _, p := range mustSurvive {
		if strings.Contains(dry, " "+p) {
			t.Errorf("the dry run proposes %s, which must survive:\n%s", p, dry)
		}
	}
	for _, p := range mustSurvive {
		if !hygieneExists(p) {
			t.Errorf("the dry run deleted %s; a dry run changes nothing on disk", p)
		}
	}
	if hygieneExists(filepath.Join(home, "hygiene.log")) {
		t.Error("the dry run wrote an action log")
	}

	// THE REAL PASS.
	code, out, errb := hygieneRun(t, now, "run", "--home", home, "--hostname", "bench")
	if code != 0 {
		t.Fatalf("hygiene run exit = %d, stderr=%s", code, errb)
	}
	for _, p := range mustSurvive {
		if !hygieneExists(p) {
			t.Errorf("run deleted %s\nHYGIENE: %s", p, strings.TrimSpace(out))
		}
	}
	for _, p := range []string{oldJob, harvJob, stale} {
		if hygieneExists(p) {
			t.Errorf("run left %s\nHYGIENE: %s", p, strings.TrimSpace(out))
		}
	}
	// (a) again, stated as the rule rather than as a path: a directory with no
	// jobs/ is not a slot and is not even counted as one.
	if !strings.Contains(out, "cache=kept-lease") {
		t.Errorf("the build cache was not kept for a live lease; the line says: %s", strings.TrimSpace(out))
	}

	// Every deletion is one dated line: <utc> <verb> <path>.
	raw, err := os.ReadFile(filepath.Join(home, "hygiene.log"))
	if err != nil {
		t.Fatalf("run wrote no hygiene.log: %v", err)
	}
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		if !hygieneLogLine.MatchString(line) {
			t.Errorf("hygiene.log line does not match <utc> <verb> <path>: %q", line)
		}
	}
	stamp := now.Format(time.RFC3339)
	if !strings.Contains(string(raw), stamp+" DEAD "+oldJob+"\n") {
		t.Errorf("no dated line for the 8 h unleased job that left no RESULT.md:\n%s", raw)
	}
	if !strings.Contains(string(raw), stamp+" delete-job "+harvJob+"\n") {
		t.Errorf("no dated line for the harvested job:\n%s", raw)
	}

	// THE LEASE IS NOT A PERMANENT SHIELD. The launcher is gone and the heartbeat
	// is stale: the same job and the same cache go on the next pass.
	stopChild()
	hygieneAge(t, old, filepath.Join(ljob, ".lease"), filepath.Join(ljob, "harness-output.log"),
		ljob, filepath.Join(leased, "jobs"), leased)
	code, out, errb = hygieneRun(t, now, "run", "--home", home, "--hostname", "bench")
	if code != 0 {
		t.Fatalf("second pass exit = %d, stderr=%s", code, errb)
	}
	if hygieneExists(ljob) {
		t.Errorf("a job whose lease is dead and stale was kept\nHYGIENE: %s", strings.TrimSpace(out))
	}
	if hygieneExists(cache) {
		t.Errorf("the build cache was kept with no live lease\nHYGIENE: %s", strings.TrimSpace(out))
	}
	if !hygieneExists(filepath.Join(bare1, "corpus", "data.txt")) {
		t.Error("the bare directory did not survive the second pass")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
