package main

// --dry-run, PROVED THROUGH THE REAL CLI AGAINST AN ISOLATED QUEUE.
//
// The first version of these guarantees was tested at the library level with the run step
// replaced by a no-op, so nothing ever exercised what the WIRED step does under --dry-run:
// it harvested, swept, reaped, refilled and launched, on a queue a person had pointed it at
// because they were promised nothing would move (Stella's cold read of #1430, defect 1).
//
// So these go through `run(args, ...)` -- the same entry `main` calls -- against a queue
// under t.TempDir(), and the assertion is a BYTE-FOR-BYTE snapshot of the whole tree before
// and after. Not "the card is still there": every file, its mode and its content. A dry run
// that wrote one line to one log is a dry run that lied, and the snapshot says so.
//
// The launcher is a tripwire: a path that does not exist. If a dry run ever reached it, the
// exec would fail and the verb's own FILL line would carry failed=1, so the tool reports the
// breach itself rather than the test having to build a binary to catch it.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// tripwireLauncher is a path no launcher lives at. A dry run must never reach it; a run that
// does gets an exec failure the FILL line counts, which is the breach reporting itself.
const tripwireLauncher = "/nonexistent/nova-pulse-dry-run-tripwire"

// dryQueue is an isolated queue with cards in every directory a verb might touch, plus the
// files the loop's own validation wants.
func dryQueue(t *testing.T) (queue, root, machines, lanes string) {
	t.Helper()
	base := t.TempDir()
	queue, root = filepath.Join(base, "queue"), filepath.Join(base, "swarm-bench-a")
	for _, d := range []string{"pending", "launched", "done", "failed", "ready"} {
		if err := os.MkdirAll(filepath.Join(queue, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	writeMainFile(t, filepath.Join(queue, "ready"), "card-001.md", "RESULT: CARD-1\nLANE: pulse\nSTEP 1. do the thing\n")
	writeMainFile(t, filepath.Join(queue, "ready"), "card-002.md", "RESULT: CARD-2\nLANE: pulse\nSTEP 1. do the other thing\n")
	writeMainFile(t, filepath.Join(queue, "ready"), "card-003.md", "RESULT: CARD-3\nSTEP 1. a third\n")
	writeMainFile(t, filepath.Join(queue, "pending"), "card-004.md", "RESULT: CARD-4\nSTEP 1. waiting\n")
	writeMainFile(t, queue, "NEXT", "9000\n")
	writeMainFile(t, queue, "POLICY", "floor=0\nwait-timeout=1s\n")
	machines = fillMachines(t, base, "bench-a")
	lanes = filepath.Join(base, "lanes.tsv")
	if err := os.WriteFile(lanes, []byte("pulse\tinternal/pulse/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return queue, root, machines, lanes
}

// snapshot is every file under a tree: its relative path, its mode and the hash of its
// content. Two snapshots that differ name the first path that moved.
func snapshot(t *testing.T, tree string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(tree, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(tree, path)
		if rerr != nil {
			return rerr
		}
		if d.IsDir() {
			out[rel+"/"] = "dir"
			return nil
		}
		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		sum := sha256.Sum256(raw)
		out[rel] = fmt.Sprintf("%v %s", info.Mode().Perm(), hex.EncodeToString(sum[:8]))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// sameTree says the tree is untouched, and names every path that is not.
func sameTree(t *testing.T, what string, before, after map[string]string) {
	t.Helper()
	var moved []string
	for path, was := range before {
		if now, ok := after[path]; !ok {
			moved = append(moved, "gone:    "+path)
		} else if now != was {
			moved = append(moved, "changed: "+path)
		}
	}
	for path := range after {
		if _, ok := before[path]; !ok {
			moved = append(moved, "new:     "+path)
		}
	}
	if len(moved) > 0 {
		sort.Strings(moved)
		t.Fatalf("%s changed the queue:\n  %s", what, strings.Join(moved, "\n  "))
	}
}

func dryRun(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, &out, &errb, time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC))
	return out.String(), errb.String(), code
}

// TestFillDryRunTouchesNothingThroughTheCLI: the whole tree, byte for byte, before and
// after -- and the verb still reports what it WOULD have placed.
func TestFillDryRunTouchesNothingThroughTheCLI(t *testing.T) {
	queue, _, machines, lanes := dryQueue(t)
	before := snapshot(t, queue)
	out, errb, code := dryRun(t,
		"fill", "--ready", filepath.Join(queue, "ready"), "--launched", filepath.Join(queue, "launched"),
		"--queue", queue, "--machines", machines, "--lanes", lanes,
		"--bench", "bench-a", "--capacity", "4", "--launcher", tripwireLauncher,
		"--once", "--dry-run")
	if code != 0 {
		t.Fatalf("fill --dry-run exit = %d; stderr=%q", code, errb)
	}
	sameTree(t, "fill --dry-run", before, snapshot(t, queue))
	if !strings.Contains(out, "dry-run=yes") {
		t.Fatalf("the FILL line does not say it changed nothing: %q", out)
	}
	// The tripwire: a launcher it never reached cannot have failed.
	if !strings.Contains(out, "bench-a:launched=2,failed=0") {
		t.Fatalf("the dry run either reached the launcher or lost its count: %q", out)
	}
	if !strings.Contains(out, "FILL HELD card=card-002.md lane=pulse live=card-001.md") {
		t.Fatalf("the dry run lost the lane rule: %q", out)
	}
}

// TestManagerDryRunTouchesNothingThroughTheCLI: no bus advance, no receipt, no card cut or
// moved, and no line in MANAGER.log -- the ledger is a change like any other.
func TestManagerDryRunTouchesNothingThroughTheCLI(t *testing.T) {
	queue, root, _, _ := dryQueue(t)
	before := snapshot(t, queue)
	out, errb, code := dryRun(t,
		"manager", "--policy", filepath.Join(queue, "POLICY"), "--queue", queue,
		"--roots", root, "--bus", filepath.Join(queue, "bus"), "--as", "Rowan",
		"--once", "--dry-run")
	if code != 0 {
		t.Fatalf("manager --dry-run exit = %d; stderr=%q", code, errb)
	}
	sameTree(t, "manager --dry-run", before, snapshot(t, queue))
	if !strings.Contains(out, "dry-run=yes") {
		t.Fatalf("the MANAGER line does not say it changed nothing: %q", out)
	}
}

// TestLoopRefusesDryRunThroughTheCLI: the run step has no read-only mode, so the flag is
// refused rather than half-kept -- and the refusal names the two verbs that do have one.
// Nothing is written, not even the log the loop files every step's lines in.
func TestLoopRefusesDryRunThroughTheCLI(t *testing.T) {
	queue, root, machines, lanes := dryQueue(t)
	before := snapshot(t, queue)
	_, errb, code := dryRun(t,
		"loop", "--queue", queue, "--machines", machines, "--lanes", lanes, "--roots", root,
		"--policy", filepath.Join(queue, "POLICY"), "--launcher", tripwireLauncher,
		"--capacity", "4", "--bench", "bench-a", "--once", "--dry-run")
	if code != 2 {
		t.Fatalf("loop --dry-run exit = %d, want 2", code)
	}
	sameTree(t, "the refused loop --dry-run", before, snapshot(t, queue))
	for _, want := range []string{"fill --dry-run", "manager --dry-run", "#1441"} {
		if !strings.Contains(errb, want) {
			t.Fatalf("the refusal does not name %q: %q", want, errb)
		}
	}
}

// TestLoopValidatesItsTablesThroughTheCLI: a path that is merely non-empty is not a
// registry, and the refusal comes BEFORE the first tick -- nothing is written.
func TestLoopValidatesItsTablesThroughTheCLI(t *testing.T) {
	queue, root, machines, lanes := dryQueue(t)
	before := snapshot(t, queue)
	_, errb, code := dryRun(t,
		"loop", "--queue", queue, "--machines", filepath.Join(queue, "no-such.tsv"),
		"--lanes", lanes, "--roots", root, "--capacity", "0", "--once")
	if code != 2 {
		t.Fatalf("loop with an unreadable --machines exited %d, want 2", code)
	}
	sameTree(t, "the refused loop", before, snapshot(t, queue))
	if !strings.Contains(errb, "--machines") {
		t.Fatalf("the refusal does not name --machines: %q", errb)
	}
	_ = machines
}

// TestTheSnapshotCatchesARealRun is the negative control, and without it every test above is
// vacuous: a snapshot comparison that cannot fail proves nothing. The SAME fill without
// --dry-run must move the tree, and the same helper must say so.
func TestTheSnapshotCatchesARealRun(t *testing.T) {
	queue, _, machines, lanes := dryQueue(t)
	before := snapshot(t, queue)
	// A launcher that is not there: the cards still move out of ready, which is the change
	// the snapshot must see, and the FILL line counts the launcher's failure.
	out, _, _ := dryRun(t,
		"fill", "--ready", filepath.Join(queue, "ready"), "--launched", filepath.Join(queue, "launched"),
		"--queue", queue, "--machines", machines, "--lanes", lanes,
		"--bench", "bench-a", "--capacity", "4", "--launcher", tripwireLauncher,
		"--launch-grace", "0", "--once")
	after := snapshot(t, queue)
	if len(before) == len(after) {
		var same bool = true
		for k, v := range before {
			if after[k] != v {
				same = false
				break
			}
		}
		if same {
			t.Fatalf("a real fill left the tree byte for byte identical, so the dry-run tests above prove nothing: %q", out)
		}
	}
	// And the tripwire reports itself: the launcher WAS reached, and the line says so. All
	// three go out and all three fail, because a launcher that fails releases its card's lane
	// and puts the card back, so the next card of that lane is this tick's to try.
	if !strings.Contains(out, "bench-a:launched=0,failed=3") {
		t.Fatalf("the tripwire launcher was reached and the FILL line did not count it: %q", out)
	}
}
