package swarm

import (
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// ISSUE #640: A BATCH LINE MEANT NOTHING OF THE BATCH WAS STILL RUNNING, AND IT DID NOT.
//
// Dogfood, tool-pulse probe TP1, 2026-09-16 01:30Z: six cards, all abstained
// `reason=deadline`, `BATCH TP1 n=6 done=0 abstain=6` printed -- and five to twelve minutes
// later `ps` still showed, under the batch root, two `nova-sandbox` wrappers at 12:30
// elapsed, five `nova-*.test` binaries at 5-17% CPU each and a go-build cache process. They
// were killed by hand.
//
// THE CAUSE, at dev 31e35195: the batch killed ONE PID.
//
//	internal/swarm/batch.go:493  _ = procs[i].cmd.Process.Kill()   // the idle kill
//	internal/swarm/batch.go:513  _ = procs[i].cmd.Process.Kill()   // the deadline kill
//
// `Process.Kill` is SIGKILL to one process. The card's own children -- the wall, the
// harness, whatever the harness started -- are not that process. `native` has had the right
// shape since its deadline landed (`ownChildGroup` at launch, `swarm.KillGroup(pgid,
// started)` at the deadline, `cmd/nova-swarm/native.go:486`); `batch` had neither half, and
// `grep -n 'KillGroup\|Setpgid\|ownGroup' internal/swarm/batch.go` found only the `ownGroup`
// inside `selfNative`, whose group nothing ever signalled.
//
// WHAT THE SECOND ASSERTION IS FOR. The issue asks for `killed=<n>` on the ABSTAIN row,
// because a coordinator reading the batch line has no other way to know the tree went with
// it -- and the whole fault is that the printed line and the machine disagreed.

// TestBatchDeadlineEndsTheCardsWholeTree is the issue as a test. A card leaves an ordinary
// child running and then blocks; the deadline fires; the child must be gone when the BATCH
// line has been printed.
//
// RED AT dev 31e35195: "the card's child outlived the BATCH line" -- the pid was still alive
// after Batch returned, exactly as ps showed on the bench.
func TestBatchDeadlineEndsTheCardsWholeTree(t *testing.T) {
	dir := t.TempDir()
	root := dir + "/root"
	card := writeCard(t, dir, "a.card", "RESULT: a\nall green")
	tsv := dir + "/cards.tsv"
	if err := os.WriteFile(tsv, []byte("a\t1\tmodel\t"+card+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The card: start a child, publish its pid, then block past the deadline. The child
	// carries its own 20 s bound, so a RED run of this test leaves nothing behind.
	pidPath := dir + "/linger.pid"
	runner := runnerDoing(t, dir, "lingerer",
		runnerStep{Op: "linger", Path: pidPath, N: 20000, Ms: 30000},
	)
	clk := newManualClock()
	code, out, _ := runBatchClock(BatchInput{
		ID: "B1", Deadline: 30 * time.Second, Cards: tsv, Root: root, Runner: runner,
	}, clk, func() {
		// The child exists before the deadline is fired, so the assertion below is about
		// the kill and never about a process that had not started yet.
		waitForFile(t, pidPath)
		clk.waitDeadline()
		clk.advance(30 * time.Second)
	})
	if code != 1 {
		t.Fatalf("a batch whose only card is killed at the deadline exits 1, got %d:\n%s", code, out)
	}
	pid := readPidFile(t, pidPath)
	// THE ASSERTION OF THE ISSUE. `Alive` is signal 0: it asks the kernel and sends nothing.
	if Alive(pid, "") {
		t.Errorf("the card's child %d outlived the BATCH line; a BATCH line means nothing of the batch is still running:\n%s", pid, out)
	}
}

// TestBatchAbstainRowSaysHowManyItKilled is the reporting half. The count is the card's own
// process group, read before the kill; a platform that cannot enumerate a group counts the
// group it is about to end as one, which is a floor and never an invention
// (internal/swarm/proc_darwin.go, proc_bsd.go).
//
// RED AT dev 31e35195: the row read `a slot=1: ABSTAIN reason=deadline log=0` and carried no
// count at all.
func TestBatchAbstainRowSaysHowManyItKilled(t *testing.T) {
	dir := t.TempDir()
	root := dir + "/root"
	card := writeCard(t, dir, "a.card", "RESULT: a\nall green")
	tsv := dir + "/cards.tsv"
	if err := os.WriteFile(tsv, []byte("a\t1\tmodel\t"+card+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pidPath := dir + "/linger.pid"
	runner := runnerDoing(t, dir, "lingerer2",
		runnerStep{Op: "linger", Path: pidPath, N: 20000, Ms: 30000},
	)
	clk := newManualClock()
	_, out, _ := runBatchClock(BatchInput{
		ID: "B1", Deadline: 30 * time.Second, Cards: tsv, Root: root, Runner: runner,
	}, clk, func() {
		waitForFile(t, pidPath)
		clk.waitDeadline()
		clk.advance(30 * time.Second)
	})
	n, found := killedCount(out)
	if !found {
		t.Fatalf("a killed card's ABSTAIN row names how many processes the kill ended; it reads:\n%s", out)
	}
	if n < 1 {
		t.Errorf("the card and its child were running when the deadline fired, so the count is at least 1, got %d:\n%s", n, out)
	}
}

// TestBatchRowSaysNothingAboutKillsItDidNotMake is the other direction, so `killed=` is a
// fact about this card and not a field that appears on everything. A card that finished on
// its own was never killed, and its row says nothing about a kill.
func TestBatchRowSaysNothingAboutKillsItDidNotMake(t *testing.T) {
	dir := t.TempDir()
	root := dir + "/root"
	tsv := writeCards(t, dir, [][2]string{{"a", "RESULT: a\nall green"}})
	runner := fakeRunner(t, dir)
	code, out, errs := runBatch(t, tsv, root, runner, 30*time.Second)
	if code != 0 {
		t.Fatalf("a clean batch exits 0, got %d; stderr: %s", code, errs)
	}
	if strings.Contains(out, "killed=") {
		t.Errorf("a card nobody killed carries no kill count:\n%s", out)
	}
}

// readPidFile reads the one pid the linger step recorded.
func readPidFile(t *testing.T, path string) int {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the card recorded its child's pid at %s: %v", path, err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 {
		t.Fatalf("the pid file %s holds %q", path, raw)
	}
	return pid
}

// killedCount reads the `killed=<n>` field off whichever line carries one.
func killedCount(out string) (int, bool) {
	for _, line := range strings.Split(out, "\n") {
		for _, f := range strings.Fields(line) {
			if rest, ok := strings.CutPrefix(f, "killed="); ok {
				n, err := strconv.Atoi(rest)
				return n, err == nil
			}
		}
	}
	return 0, false
}
