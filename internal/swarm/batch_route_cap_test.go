package swarm

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A FREE TIER QUEUES FOREVER, SO THE LAUNCHER HOLDS CARDS BACK (nova-tools#917).
//
// Measured 2026-09-17 03:30-03:55Z: ~100 Muse cards on one contributor-free key across five
// machines. hulk and vision produced ZERO results in thirteen minutes at load 0.5-2.0, their
// logs frozen mid-tool-call at an output age of 783 s. The tier does not refuse; it QUEUES,
// and every card launched past its queue's depth burns its whole deadline for nothing and is
// paid for.
//
// This is the end-to-end shape: four cards, one route, a cap of two. Two run; the other two
// wait at the gate holding no process and no spend, and start only as the first two end.
func TestABatchNeverExceedsItsRouteCap(t *testing.T) {
	windowsIsNotABench(t)
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	tsv := writeCards(t, dir, [][2]string{
		{"a", "RESULT: a\ndone a"},
		{"b", "RESULT: b\ndone b"},
		{"c", "RESULT: c\ndone c"},
		{"d", "RESULT: d\ndone d"},
	})
	// Each card announces itself, waits for the test's go-ahead, and publishes. The test
	// therefore decides exactly when a card ends, and so exactly when a gate slot frees.
	runner := runnerDoing(t, dir, "capped",
		runnerStep{Op: "mkdir", Path: "{job}"},
		runnerStep{Op: "write", Path: "{root}/started-{label}"},
		runnerStep{Op: "waitfile", Path: "{root}/release-{label}"},
		publishCard("{job}"),
	)
	release := func(label string) {
		if err := os.WriteFile(filepath.Join(root, "release-"+label), []byte("go"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	started := func(label string) bool {
		_, err := os.Stat(filepath.Join(root, "started-"+label))
		return err == nil
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		// Two cards start. The third must NOT, however long we look, until one ends.
		waitFor(t, func() bool { return started("a") && started("b") })
		for i := 0; i < 40; i++ {
			if started("c") || started("d") {
				t.Errorf("a route capped at 2 launched a third card while two were in flight")
				break
			}
			time.Sleep(5 * time.Millisecond) // wall-ok: watching for something that must not happen
		}
		release("a")
		// Now, and only now, a third may start.
		waitFor(t, func() bool { return started("c") || started("d") })
		release("b")
		release("c")
		release("d")
	}()
	code, out, errs := runBatchInput(BatchInput{
		ID: "B1", Deadline: 60 * time.Second, Cards: tsv, Root: root, Runner: runner,
		MaxInflight: 2, Auth: "muse-contributor-free",
	})
	<-done
	if code != 0 {
		t.Fatalf("every card finished, so the batch exits 0, got %d:\n%s%s", code, out, errs)
	}
	if !strings.Contains(out, "BATCH B1 n=4 done=4") {
		t.Fatalf("a cap delays cards, it never loses them:\n%s", out)
	}
	// The cap says what it did: never more than two at once, and two launches waited.
	if !strings.Contains(out, "cap=2 peak=2 held-back=2") {
		t.Fatalf("the batch says what the cap did, per route:\n%s", out)
	}
	if !strings.Contains(out, "BATCH ROUTE model@muse-contributor-free") {
		t.Fatalf("the route line names the provider/model and the key's PROFILE:\n%s", out)
	}
}

// THE NEGATIVE CONTROL: NO CAP IS TODAY'S BEHAVIOUR. Four cards, no `MaxInflight`, and all
// four are in flight at once -- which is exactly what the measurement says is wrong on a
// free tier and exactly what must not change for anyone who did not ask for a cap. No ROUTE
// line is printed at all.
func TestWithNoCapEveryCardLaunchesAtOnce(t *testing.T) {
	windowsIsNotABench(t)
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	tsv := writeCards(t, dir, [][2]string{
		{"a", "RESULT: a\ndone a"},
		{"b", "RESULT: b\ndone b"},
		{"c", "RESULT: c\ndone c"},
		{"d", "RESULT: d\ndone d"},
	})
	runner := runnerDoing(t, dir, "uncapped",
		runnerStep{Op: "mkdir", Path: "{job}"},
		runnerStep{Op: "write", Path: "{root}/started-{label}"},
		runnerStep{Op: "waitfile", Path: "{root}/release-all"},
		publishCard("{job}"),
	)
	done := make(chan struct{})
	go func() {
		defer close(done)
		// All four are running before any is released: no cap held anything.
		waitFor(t, func() bool {
			for _, l := range []string{"a", "b", "c", "d"} {
				if _, err := os.Stat(filepath.Join(root, "started-"+l)); err != nil {
					return false
				}
			}
			return true
		})
		if err := os.WriteFile(filepath.Join(root, "release-all"), []byte("go"), 0o644); err != nil {
			t.Error(err)
		}
	}()
	code, out, errs := runBatchInput(BatchInput{
		ID: "B1", Deadline: 60 * time.Second, Cards: tsv, Root: root, Runner: runner,
	})
	<-done
	if code != 0 {
		t.Fatalf("every card finished, so the batch exits 0, got %d:\n%s%s", code, out, errs)
	}
	if strings.Contains(out, "BATCH ROUTE") {
		t.Fatalf("a batch that asked for no cap prints no route line:\n%s", out)
	}
}

// TWO ROUTES ARE TWO QUEUES. A cap of one on each lets one card of EACH model run together:
// the tier queues per key and per model, so a cap that were global would halve a batch that
// spreads across providers for no reason at all.
func TestTheCapIsPerRouteEndToEnd(t *testing.T) {
	windowsIsNotABench(t)
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	tsv := filepath.Join(dir, "cards.tsv")
	ca := writeCard(t, dir, "a.card", "RESULT: a\ndone a")
	cb := writeCard(t, dir, "b.card", "RESULT: b\ndone b")
	if err := os.WriteFile(tsv, []byte("a\t1\tmodel-muse\t"+ca+"\nb\t2\tmodel-flash\t"+cb+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := runnerDoing(t, dir, "tworoutes",
		runnerStep{Op: "mkdir", Path: "{job}"},
		runnerStep{Op: "write", Path: "{root}/started-{label}"},
		runnerStep{Op: "waitfile", Path: "{root}/release-all"},
		publishCard("{job}"),
	)
	done := make(chan struct{})
	go func() {
		defer close(done)
		// Both run at once under a cap of ONE, because they are not the same queue. If the
		// cap were global this never holds and the test gives up.
		waitFor(t, func() bool {
			_, ea := os.Stat(filepath.Join(root, "started-a"))
			_, eb := os.Stat(filepath.Join(root, "started-b"))
			return ea == nil && eb == nil
		})
		if err := os.WriteFile(filepath.Join(root, "release-all"), []byte("go"), 0o644); err != nil {
			t.Error(err)
		}
	}()
	code, out, errs := runBatchInput(BatchInput{
		ID: "B1", Deadline: 60 * time.Second, Cards: tsv, Root: root, Runner: runner,
		MaxInflight: 1, Auth: "k",
	})
	<-done
	if code != 0 {
		t.Fatalf("both cards finished, so the batch exits 0, got %d:\n%s%s", code, out, errs)
	}
	if !strings.Contains(out, "model-muse@k cap=1 peak=1 held-back=0") || !strings.Contains(out, "model-flash@k cap=1 peak=1 held-back=0") {
		t.Fatalf("two routes under a cap of one hold nothing back:\n%s", out)
	}
}

// runBatchInput drives Batch with a whole BatchInput, for the tests whose subject is a field
// the five-argument helper does not carry.
func runBatchInput(in BatchInput) (int, string, string) {
	var out, errb bytes.Buffer
	in.Stdout, in.Stderr = &out, &errb
	code := Batch(in)
	return code, out.String(), errb.String()
}

// A CARD THAT NEVER PRODUCES A FIRST TOKEN IS `stalled` (nova-tools#917).
//
// Measured 2026-09-17: a fresh known-answer card on hulk hung for its WHOLE 150 s deadline
// having produced nothing at all, while deepseek-flash on the same bench in the same second
// answered in 11 s. The tier had queued it and was never going to answer.
//
// The idle window cannot see this. Every signal it has -- a log that grew, a process tree
// whose CPU advanced -- needs a FIRST sample to compare against, and a card that never spoke
// once provides none of them: it is invisible to the idle monitor and runs to the deadline,
// paid for, every time.
//
// So a card that has produced nothing since it launched is ended at `StallAfter`, and its
// reason says what happened rather than borrowing the idle token.
func TestACardWithNoFirstTokenIsStalled(t *testing.T) {
	windowsIsNotABench(t)
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	tsv := writeCards(t, dir, [][2]string{
		{"quiet", "RESULT: quiet\nMISSING"},
		{"speaks", "RESULT: speaks\ndone speaking"},
	})
	// `quiet` writes NOTHING and sits there: the queued card. `speaks` writes one line to
	// its log -- one token -- and then waits exactly as long, which is the control: it is
	// silent for the same window and must NOT be stalled, because it spoke once.
	runner := runnerDoing(t, dir, "firsttoken",
		runnerStep{Op: "mkdir", Path: "{job}"},
		runnerStep{Op: "stdout", Body: "the first token", When: "label==speaks"},
		runnerStep{Op: "write", Path: "{root}/started-{label}"},
		runnerStep{Op: "waitfile", Path: "{root}/release", N: 25000},
		func() runnerStep { s := publishCard("{job}"); s.When = "label==speaks"; return s }(),
	)
	sampler := &fakeTreeSampler{}
	sampler.cpuForCard = func(cardIndex, pid int) (uint64, bool) { return 50_000_000, true }
	clk := newManualClock()
	code, out, errs := runBatchClock(BatchInput{
		// Idle is left at its zero value: this test is the FIRST-TOKEN window and nothing else.
		ID: "B1", Deadline: 60 * time.Second, StallAfter: testIdleBudget, // wall-ok: the injected clock advances this first-token window; it is never real time
		Cards: tsv, Root: root, Runner: runner,
		snapshot: func() activitySnapshot { return sampler },
	}, clk, func() {
		waitForFile(t, filepath.Join(root, "started-quiet"))
		waitForFile(t, filepath.Join(root, "started-speaks"))
		clk.waitTick()
		clk.tick() // the first reading of both
		clk.advance(testIdleBudget)
		clk.tick() // a whole first-token window later
		if err := os.WriteFile(filepath.Join(root, "release"), []byte("go"), 0o644); err != nil {
			t.Error(err)
		}
	})
	if code != 1 {
		t.Fatalf("a batch with a stalled card exits 1, got %d:\n%s%s", code, out, errs)
	}
	if !strings.Contains(out, "quiet slot=1: ABSTAIN reason=stalled") {
		t.Fatalf("a card that produced no first token inside StallAfter is stalled:\n%s", out)
	}
	if strings.Contains(out, "speaks slot=2: ABSTAIN reason=stalled") {
		t.Fatalf("THE CONTROL: a card that spoke once is never stalled, however quiet it is after:\n%s", out)
	}
}

// AND THE STALL CHECK IS OFF BY DEFAULT. A batch that named no `StallAfter` keeps today's
// behaviour exactly: a silent card runs to its deadline and is scored by what it left, and
// no card is ever `stalled` for a window nobody asked for.
func TestWithNoStallAfterASilentCardIsNotStalled(t *testing.T) {
	windowsIsNotABench(t)
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	card := writeCard(t, dir, "a.card", "RESULT: a\nall green")
	tsv := filepath.Join(dir, "cards.tsv")
	if err := os.WriteFile(tsv, []byte("a\t1\tmodel\t"+card+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := runnerDoing(t, dir, "silentnostall",
		runnerStep{Op: "mkdir", Path: "{job}"},
		runnerStep{Op: "write", Path: "{root}/started"},
		publishCard("{job}"),
	)
	code, out, _ := runBatch2(BatchInput{
		ID: "B1", Deadline: 60 * time.Second, Cards: tsv, Root: root, Runner: runner,
	})
	if code != 0 {
		t.Fatalf("a card that finished is done, got %d:\n%s", code, out)
	}
	if strings.Contains(out, "reason=stalled") {
		t.Fatalf("no StallAfter, no stall kill:\n%s", out)
	}
}

// runBatch2 is runBatchInput under a second name for the tests below it; see runBatchInput.
func runBatch2(in BatchInput) (int, string, string) { return runBatchInput(in) }
