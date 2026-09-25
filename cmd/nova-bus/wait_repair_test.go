package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// #2627: a killed git leaves index.lock, and the beat it was writing stays dirty. The next
// nova-bus wait used to fail on that lock, and every tick after it failed the same way.
// These tests are the keeper's controls, run against the tool itself. A repair is claimed
// only by a WAIT REPAIR line, and only for what the run actually changed.

func waitRepairLines(r result) []string {
	var lines []string
	for _, stream := range []string{r.stdout, r.stderr} {
		for _, line := range strings.Split(stream, "\n") {
			if strings.HasPrefix(line, "WAIT REPAIR ") {
				lines = append(lines, line)
			}
		}
	}
	return lines
}

func headOf(t *testing.T, dir string) string {
	t.Helper()
	return strings.TrimSpace(gitIn(t, dir, "rev-parse", "HEAD"))
}

func gitAncestor(t *testing.T, dir, ancestor, desc string) bool {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "merge-base", "--is-ancestor", ancestor, desc)
	err := cmd.Run()
	if err == nil {
		return true
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return false
	}
	t.Fatalf("merge-base --is-ancestor: %v", err)
	return false
}

func gitIndexLock(t *testing.T, checkout string) string {
	t.Helper()
	gd := strings.TrimSpace(gitIn(t, checkout, "rev-parse", "--absolute-git-dir"))
	return filepath.Join(gd, "index.lock")
}

// plantLock creates the checkout's index.lock. age > 0 backdates it; a zero age leaves
// the mtime at the moment of creation, which is a fresh lock.
func plantLock(t *testing.T, checkout string, age time.Duration) string {
	t.Helper()
	lock := gitIndexLock(t, checkout)
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if age > 0 {
		when := time.Now().Add(-age)
		if err := os.Chtimes(lock, when, when); err != nil {
			t.Fatal(err)
		}
	}
	return lock
}

// pushAhead commits content on a second clone and pushes it, so checkout is behind
// origin/main by that commit. The returned hash is the new tip.
func pushAhead(t *testing.T, bare, rel, content string) string {
	t.Helper()
	other := bench(t, bare)
	writeFile(t, other, rel, content)
	gitIn(t, other, "add", "-A")
	gitIn(t, other, "-c", "user.name=Bo", "-c", "user.email=bo@example.com", "commit", "-q", "-m", "advance "+rel)
	gitIn(t, other, "push", "-q", "origin", "HEAD:main")
	return strings.TrimSpace(gitIn(t, bare, "rev-parse", "main"))
}

func commitAndPush(t *testing.T, dir, rel, content, msg string) {
	t.Helper()
	writeFile(t, dir, rel, content)
	gitIn(t, dir, "add", "--", rel)
	gitIn(t, dir, "-c", "user.name=Ada", "-c", "user.email=ada@example.com", "commit", "-q", "-m", msg)
	gitIn(t, dir, "push", "-q", "origin", "HEAD:main")
}

// The done-when: a checkout with a 60s-old index.lock and a dirty BEAT ticks, and prints
// one WAIT REPAIR line naming both.
func TestWaitRepairStaleLockAndDirtyBeat(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, bare := busDir(t)
	tip := pushAhead(t, bare, "from-bo/arrived.md", "From: Bo\nTo: Ada\nDate: Mon Sep  7 00:03:00 UTC 2026\nId: bo-222222222222\nSubject: Arrived\n\nA note on the bus.\n")
	const garbage = "STALE GARBAGE beat from a killed commit\n"
	writeFile(t, checkout, "from-ada/BEAT", garbage)
	lock := plantLock(t, checkout, 2*time.Minute)

	r := invoke(t, "", waitFlags(checkout, "Ada", "2s")...).mustCode(t, 0)
	lines := waitRepairLines(r)
	if len(lines) != 1 {
		t.Fatalf("want one WAIT REPAIR line, got %d\nstdout:\n%s\nstderr:\n%s", len(lines), r.stdout, r.stderr)
	}
	if !strings.Contains(lines[0], "index.lock") || !strings.Contains(lines[0], "from-ada/BEAT") {
		t.Fatalf("the repair line does not name both repairs: %s", lines[0])
	}
	if _, err := os.Lstat(lock); !os.IsNotExist(err) {
		t.Fatalf("stale index.lock still present: %v", err)
	}
	// Discarded, not regenerated (#3144: no wait writes a BEAT): the fixture holds none.
	if beat, err := os.ReadFile(filepath.Join(checkout, "from-ada", "BEAT")); !os.IsNotExist(err) {
		t.Fatalf("BEAT was not discarded (%v):\n%s", err, beat)
	}
	if !gitAncestor(t, checkout, tip, "HEAD") {
		t.Fatalf("the fast-forward did not land %s; HEAD is %s", tip, headOf(t, checkout))
	}
	if !strings.Contains(r.stdout, "WAIT OK") {
		t.Fatalf("the wait did not tick:\n%s", r.stdout)
	}
}

// Case 1: a stale lock and a clean tree. One WAIT REPAIR line, naming the lock and not a
// beat the run did not have to repair.
func TestWaitRepairStaleIndexLock(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, bare := busDir(t)
	tip := pushAhead(t, bare, "from-bo/arrived.md", "From: Bo\nTo: Ada\nDate: Mon Sep  7 00:03:00 UTC 2026\nId: bo-222222222222\nSubject: Arrived\n\nA note on the bus.\n")
	lock := plantLock(t, checkout, 2*time.Minute)

	r := invoke(t, "", waitFlags(checkout, "Ada", "2s")...).mustCode(t, 0)
	lines := waitRepairLines(r)
	if len(lines) != 1 {
		t.Fatalf("want one WAIT REPAIR line, got %d\nstdout:\n%s\nstderr:\n%s", len(lines), r.stdout, r.stderr)
	}
	if !strings.Contains(lines[0], "index.lock") {
		t.Fatalf("the repair line does not name the lock: %s", lines[0])
	}
	if strings.Contains(lines[0], "BEAT") {
		t.Fatalf("claimed a BEAT repair this run did not do: %s", lines[0])
	}
	if _, err := os.Lstat(lock); !os.IsNotExist(err) {
		t.Fatalf("stale index.lock still present: %v", err)
	}
	if !gitAncestor(t, checkout, tip, "HEAD") {
		t.Fatalf("fast-forward did not land after the lock was removed; HEAD %s", headOf(t, checkout))
	}
}

// Case 2: a lock just created is left in place, and no repair is claimed.
func TestWaitRepairFreshLockLeftAlone(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, bare := busDir(t)
	pushAhead(t, bare, "from-bo/arrived.md", "From: Bo\nTo: Ada\nDate: Mon Sep  7 00:03:00 UTC 2026\nId: bo-222222222222\nSubject: Arrived\n\nA note on the bus.\n")
	lock := plantLock(t, checkout, 0)
	before := headOf(t, checkout)

	r := invoke(t, "", waitFlags(checkout, "Ada", "2s")...)
	if r.code == 0 {
		t.Fatalf("a fresh lock did not stop the fast-forward\nstdout:\n%s\nstderr:\n%s", r.stdout, r.stderr)
	}
	if lines := waitRepairLines(r); len(lines) != 0 {
		t.Fatalf("claimed a repair of a fresh lock: %q", lines)
	}
	if _, err := os.Lstat(lock); err != nil {
		t.Fatalf("fresh index.lock was removed: %v", err)
	}
	if headOf(t, checkout) != before {
		t.Fatalf("HEAD moved under a fresh lock: %s -> %s", before, headOf(t, checkout))
	}
}

// Case 3: a dirty BEAT an older wait left is discarded, and not regenerated (#3144: no wait
// writes a BEAT). One WAIT REPAIR line, naming it.
func TestWaitRepairDirtyBeat(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	const garbage = "STALE GARBAGE beat from a killed commit\n"
	writeFile(t, checkout, "from-ada/BEAT", garbage)

	r := invoke(t, "", waitFlags(checkout, "Ada", "2s")...).mustCode(t, 0)
	lines := waitRepairLines(r)
	if len(lines) != 1 {
		t.Fatalf("want one WAIT REPAIR line, got %d\nstdout:\n%s\nstderr:\n%s", len(lines), r.stdout, r.stderr)
	}
	if !strings.Contains(lines[0], "from-ada/BEAT") {
		t.Fatalf("the repair line does not name the beat: %s", lines[0])
	}
	if strings.Contains(lines[0], "index.lock") {
		t.Fatalf("claimed a lock repair this run did not do: %s", lines[0])
	}
	if beat, err := os.ReadFile(filepath.Join(checkout, "from-ada", "BEAT")); !os.IsNotExist(err) {
		t.Fatalf("BEAT was not discarded, or was regenerated (%v):\n%s", err, beat)
	}
	if out := strings.TrimSpace(gitIn(t, checkout, "log", "--format=%H", "--", "from-ada/BEAT")); out != "" {
		t.Fatalf("the repair committed a BEAT: %s", out)
	}
}

// Case 4: behind origin, and the only local difference is the tool's own lane file. The
// dirty BEAT is what blocks the fast-forward (origin changed it too). Discard it, move,
// regenerate. CURSOR is not part of this case.
func TestWaitRepairBehindOwnLaneFileFastForwards(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, bare := busDir(t)
	commitAndPush(t, checkout, "from-ada/BEAT", "2026-09-01T00:00:00Z -\n", "base beat")
	other := bench(t, bare)
	writeFile(t, other, "from-ada/BEAT", "2026-09-02T00:00:00Z -\n")
	gitIn(t, other, "add", "--", "from-ada/BEAT")
	gitIn(t, other, "-c", "user.name=Bo", "-c", "user.email=bo@example.com", "commit", "-q", "-m", "their beat")
	gitIn(t, other, "push", "-q", "origin", "HEAD:main")
	tip := strings.TrimSpace(gitIn(t, bare, "rev-parse", "main"))
	const garbage = "STALE GARBAGE local beat blocks the fast-forward\n"
	writeFile(t, checkout, "from-ada/BEAT", garbage)

	r := invoke(t, "", waitFlags(checkout, "Ada", "2s")...).mustCode(t, 0)
	lines := waitRepairLines(r)
	if len(lines) != 1 || !strings.Contains(lines[0], "from-ada/BEAT") {
		t.Fatalf("want one WAIT REPAIR line naming the beat, got %q\nstdout:\n%s\nstderr:\n%s", lines, r.stdout, r.stderr)
	}
	beat, err := os.ReadFile(filepath.Join(checkout, "from-ada", "BEAT"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(beat), "STALE GARBAGE") {
		t.Fatalf("the blocking BEAT was kept:\n%s", beat)
	}
	if !gitAncestor(t, checkout, tip, "HEAD") {
		t.Fatalf("did not fast-forward onto %s; HEAD is %s", tip, headOf(t, checkout))
	}
}

// Case 5: behind origin, plus a dirty CURSOR. CURSOR is not this tool's to discard.
// Refuse, and leave the file byte for byte as it was. Do not fast-forward. Do not claim
// a repair.
func TestWaitRepairBehindCursorRefusesUntouched(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, bare := busDir(t)
	commitAndPush(t, checkout, "from-ada/CURSOR", "BASE\n", "base cursor")
	other := bench(t, bare)
	writeFile(t, other, "from-ada/CURSOR", "UPSTREAM\n")
	gitIn(t, other, "add", "--", "from-ada/CURSOR")
	gitIn(t, other, "-c", "user.name=Bo", "-c", "user.email=bo@example.com", "commit", "-q", "-m", "their cursor")
	gitIn(t, other, "push", "-q", "origin", "HEAD:main")
	const sentinel = "SENTINEL-LOCAL do not touch\n"
	writeFile(t, checkout, "from-ada/CURSOR", sentinel)
	before := headOf(t, checkout)

	r := invoke(t, "", waitFlags(checkout, "Ada", "2s")...)
	if r.code == 0 {
		t.Fatalf("wait fast-forwarded over a dirty CURSOR\nstdout:\n%s\nstderr:\n%s", r.stdout, r.stderr)
	}
	if lines := waitRepairLines(r); len(lines) != 0 {
		t.Fatalf("claimed a repair while refusing: %q", lines)
	}
	refusal := r.stderr
	if !strings.Contains(refusal, "from-ada/CURSOR") || !strings.Contains(refusal, "not this tool's to discard") {
		t.Fatalf("the refusal does not name the file it left alone:\n%s", refusal)
	}
	got, err := os.ReadFile(filepath.Join(checkout, "from-ada", "CURSOR"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != sentinel {
		t.Fatalf("CURSOR was touched: %q", got)
	}
	if headOf(t, checkout) != before {
		t.Fatalf("HEAD moved: %s -> %s", before, headOf(t, checkout))
	}
}

// Case 6: nothing analogous to a harness check lives in here, and the tool must not lie
// about one. A clean wait claims no repair. --no-beat does not discard a BEAT it does not
// own and does not say it regenerated one. A wait without --advance does not say it advanced.
func TestWaitRepairDoesNotClaimWhatItDidNotDo(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	clean := invoke(t, "", waitFlags(checkout, "Ada", "2s")...).mustCode(t, 0)
	if lines := waitRepairLines(clean); len(lines) != 0 {
		t.Fatalf("a clean wait claimed a repair: %q\nstdout:\n%s", lines, clean.stdout)
	}
	if strings.Contains(clean.stdout, "WAIT ADVANCED") {
		t.Fatalf("a wait without --advance claimed it advanced:\n%s", clean.stdout)
	}
	if !strings.Contains(clean.stdout, "WAIT OK") {
		t.Fatalf("the clean wait did not tick:\n%s", clean.stdout)
	}

	quietCheckout, _ := busDir(t)
	const harness = "1999-01-01T00:00:00Z harness\n"
	writeFile(t, quietCheckout, "from-ada/BEAT", harness)
	quiet := invoke(t, "", waitFlags(quietCheckout, "Ada", "2s", "--no-beat")...).mustCode(t, 0)
	if lines := waitRepairLines(quiet); len(lines) != 0 {
		t.Fatalf("--no-beat claimed a repair: %q\nstdout:\n%s\nstderr:\n%s", lines, quiet.stdout, quiet.stderr)
	}
	if strings.Contains(quiet.stdout, "WAIT ADVANCED") {
		t.Fatalf("--no-beat claimed an advance:\n%s", quiet.stdout)
	}
	got, err := os.ReadFile(filepath.Join(quietCheckout, "from-ada", "BEAT"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != harness {
		t.Fatalf("--no-beat rewrote a BEAT it does not own:\n%q", got)
	}
}
