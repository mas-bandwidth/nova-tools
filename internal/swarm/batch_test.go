package swarm

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func itoa(n int) string { return strconv.Itoa(n) }

// The batch tests drive scatter/wait/gather with a fake runner: a real executable the test
// places in a temp directory (see fakerunner_test.go), one process per card, exactly what a
// real harness stands in for. The runner is handed label, slot, model, card path and root as
// arguments, and it publishes RESULT.md under <root>/<slot>/jobs/<label>/RESULT.md the way a
// worker does: line 1 is the contract (line 1 of the card's text) and line 2 is the
// disposition.

// fakeRunner is a runner that publishes the first two lines of a card as RESULT.md, unless
// the card's second line is the word MISSING -- which stands for a worker that produced no
// result at all. The job directory is made either way, as a worker that started makes it.
func fakeRunner(t *testing.T, dir string) string {
	t.Helper()
	return runnerDoing(t, dir, "runner",
		runnerStep{Op: "mkdir", Path: "{job}"},
		runnerStep{Op: "exit", N: 0, When: "line2==MISSING"},
		publishCard("{job}"),
	)
}

// fakeRunnerLog is fakeRunner plus a run log: it publishes the card's first two lines as
// RESULT.md (or nothing when line 2 is MISSING), and copies a fixed log body into the job's
// native.log for every card it did publish. The log a stalled card would have written is
// absent, exactly like a worker that opened the wall and wrote nothing after it.
func fakeRunnerLog(t *testing.T, dir, name, body string) string {
	t.Helper()
	logSrc := filepath.Join(dir, name)
	if err := os.WriteFile(logSrc, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return runnerDoing(t, dir, "runner-log",
		runnerStep{Op: "mkdir", Path: "{job}"},
		runnerStep{Op: "exit", N: 0, When: "line2==MISSING"},
		publishCard("{job}"),
		runnerStep{Op: "copy", Path: "{root}/{slot}/native.log", Body: logSrc},
	)
}

func writeCard(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeCards(t *testing.T, dir string, cards [][2]string) string {
	t.Helper()
	var b strings.Builder
	for i, c := range cards {
		path := writeCard(t, dir, c[0]+".card", c[1])
		b.WriteString(c[0] + "\t" + itoa(i+1) + "\tmodel\t" + path + "\n")
	}
	path := filepath.Join(dir, "cards.tsv")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func runBatch(t *testing.T, cards, root, runner string, deadline time.Duration) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := Batch(BatchInput{
		ID: "B1", Deadline: deadline, Cards: cards, Root: root, Runner: runner,
		Stdout: &out, Stderr: &errb,
	})
	return code, out.String(), errb.String()
}

// testIdleBudget is the --idle every test in the idle family passes. It is a
// VIRTUAL window: since #916 these tests inject the manualClock seam and the
// test alone advances the clock by this much to fire an idle kill, so no
// assertion here depends on how loaded the machine is. The value is four
// seconds only so the ABSTAIN token reads idle=4 and the four-second activity
// poll is easy to reason about; no test waits it out in real time. The
// wall-clock budget test allows the short literal for exactly this reason.
const testIdleBudget = 4 * time.Second // wall-ok: the injected clock advances this idle window; it is never real time

// idleReason is the ABSTAIN token the batch prints for an idle kill -- batch.go
// formats it as "idle=<seconds>" -- derived from the budget so the two cannot
// drift apart when the budget is retuned.
var idleReason = "ABSTAIN reason=idle=" + itoa(int(testIdleBudget.Seconds()))

func TestBatchGathersLine2(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	tsv := writeCards(t, dir, [][2]string{
		{"a", "RESULT: a\nall green"},
		{"b", "RESULT: b\ndone and clean"},
	})
	runner := fakeRunner(t, dir)
	code, out, errs := runBatch(t, tsv, root, runner, 30*time.Second)
	if code != 0 {
		t.Fatalf("a clean batch exits 0, got %d; stderr: %s", code, errs)
	}
	if !strings.Contains(out, "BATCH B1 n=2 done=2 abstain=0 in=0 out=0 usd=0.0000") {
		t.Fatalf("the packet's first line folds the counts:\n%s", out)
	}
	if !strings.Contains(out, "a slot=1: all green") || !strings.Contains(out, "b slot=2: done and clean") {
		t.Fatalf("line 2 of each card is gathered verbatim:\n%s", out)
	}
}

func TestBatchAbstainsMissingResult(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	tsv := writeCards(t, dir, [][2]string{
		{"a", "RESULT: a\nall green"},
		{"b", "RESULT: b\nMISSING"},
	})
	runner := fakeRunner(t, dir)
	code, out, _ := runBatch(t, tsv, root, runner, 30*time.Second)
	if code != 1 {
		t.Fatalf("a batch with an abstain exits 1, got %d:\n%s", code, out)
	}
	if !strings.Contains(out, "BATCH B1 n=2 done=1 abstain=1") {
		t.Fatalf("the missing result is an abstain row:\n%s", out)
	}
	if !strings.Contains(out, "b slot=2: ABSTAIN reason=no-result log=0") {
		t.Fatalf("the missing result names its one reason token and its empty log, never folded:\n%s", out)
	}
	if !strings.Contains(out, "stalled=1") {
		t.Fatalf("a card that ended with no output after the wall opened is counted stalled:\n%s", out)
	}
}

// TestBatchNamesMissingResultOnCleanExit: a run that ended with exit 0 but published no
// RESULT.md is named "no RESULT.md in <job dir> (rc=0)", not a stall -- the model finished
// its run, so the missing report is named for what it is rather than blamed on a wall that
// opened on nothing. The runner writes output to its log (so it is not a silent stall) and
// exits clean without publishing.
func TestBatchNamesMissingResultOnCleanExit(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	tsv := writeCards(t, dir, [][2]string{
		{"a", "RESULT: a\nMISSING"},
	})
	runner := runnerDoing(t, dir, "clean-no-result",
		runnerStep{Op: "mkdir", Path: "{job}"},
		runnerStep{Op: "write", Path: "{root}/{slot}/native.log", Body: "line one"},
		runnerStep{Op: "exit", N: 0},
	)
	code, out, _ := runBatch(t, tsv, root, runner, 30*time.Second)
	if code != 1 {
		t.Fatalf("a clean exit with no RESULT.md is an abstain, exits 1, got %d:\n%s", code, out)
	}
	if !strings.Contains(out, "BATCH B1 n=1 done=0 abstain=1") || !strings.Contains(out, "stalled=0") {
		t.Fatalf("a clean exit is an abstain, never a stall:\n%s", out)
	}
	if !strings.Contains(out, "a slot=1: ABSTAIN reason=no-result log=1 job="+filepath.Join(resolvedPath(t, root), "1", "jobs", "a")) {
		t.Fatalf("a clean exit with no RESULT.md names reason=no-result and the job that holds none:\n%s", out)
	}
	if strings.Contains(out, "reason=rc=") {
		t.Fatalf("a clean exit names no exit code; it published no result:\n%s", out)
	}
}

func TestBatchAbstainsWrongLine1(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	card := writeCard(t, dir, "a.card", "RESULT: a\nall green")
	tsv := filepath.Join(dir, "cards.tsv")
	if err := os.WriteFile(tsv, []byte("a\t1\tmodel\t"+card+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A runner that writes a result whose line 1 is NOT the contract line: a stranger's card.
	runner := runnerDoing(t, dir, "wrong",
		runnerStep{Op: "mkdir", Path: "{job}"},
		runnerStep{Op: "write", Path: "{job}/RESULT.md", Body: "RESULT: someone-else\nall green\n"},
	)
	code, out, _ := runBatch(t, tsv, root, runner, 30*time.Second)
	if code != 1 {
		t.Fatalf("a wrong line 1 exits 1, got %d:\n%s", code, out)
	}
	if !strings.Contains(out, "BATCH B1 n=1 done=0 abstain=1") {
		t.Fatalf("a differing contract line is refused, not folded:\n%s", out)
	}
}

func TestBatchPrefixLine1WithLongerTailAccepted(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	card := writeCard(t, dir, "a.card", "RESULT: a\nall green")
	tsv := filepath.Join(dir, "cards.tsv")
	if err := os.WriteFile(tsv, []byte("a\t1\tmodel\t"+card+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A worker that printed the full title: the card's contract line 1 is a prefix of the
	// RESULT's line 1, so it is done and the longer tail is named as tail=<n>.
	runner := runnerDoing(t, dir, "prefix",
		runnerStep{Op: "mkdir", Path: "{job}"},
		runnerStep{Op: "write", Path: "{job}/RESULT.md", Body: "{line1}: extra\n{line2}\n"},
	)
	code, out, _ := runBatch(t, tsv, root, runner, 30*time.Second)
	if code != 0 {
		t.Fatalf("a prefix line 1 is done, exits 0, got %d:\n%s", code, out)
	}
	if !strings.Contains(out, "a slot=1: all green log=0 tail=7") {
		t.Fatalf("a longer tail is named tail=<n> on the card line:\n%s", out)
	}
}

func TestBatchChangedWordBeforeEndStillMismatches(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	card := writeCard(t, dir, "a.card", "RESULT: alpha beta\nall green")
	tsv := filepath.Join(dir, "cards.tsv")
	if err := os.WriteFile(tsv, []byte("a\t1\tmodel\t"+card+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A word changed before the end: the card line 1 is NOT a prefix of the RESULT line 1.
	runner := runnerDoing(t, dir, "changed",
		runnerStep{Op: "mkdir", Path: "{job}"},
		runnerStep{Op: "write", Path: "{job}/RESULT.md", Body: "RESULT: alpha gamma\n{line2}\n"},
	)
	code, out, _ := runBatch(t, tsv, root, runner, 30*time.Second)
	if code != 1 {
		t.Fatalf("a changed word before the end is line1-mismatch, exits 1, got %d:\n%s", code, out)
	}
	if !strings.Contains(out, "a slot=1: ABSTAIN reason=line1-mismatch log=0") {
		t.Fatalf("a differing line 1 is refused, not folded:\n%s", out)
	}
}

func TestBatchIdenticalLine1PrintsNoTail(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	card := writeCard(t, dir, "a.card", "RESULT: a\nall green")
	tsv := filepath.Join(dir, "cards.tsv")
	if err := os.WriteFile(tsv, []byte("a\t1\tmodel\t"+card+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := fakeRunner(t, dir)
	code, out, _ := runBatch(t, tsv, root, runner, 30*time.Second)
	if code != 0 {
		t.Fatalf("an identical line 1 is done, exits 0, got %d:\n%s", code, out)
	}
	if !strings.Contains(out, "a slot=1: all green log=0") {
		t.Fatalf("an identical line 1 is done with its line 2 verbatim:\n%s", out)
	}
	if strings.Contains(out, "tail=") {
		t.Fatalf("an identical line 1 prints no tail field:\n%s", out)
	}
}

func TestBatchKillsAtDeadline(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	card := writeCard(t, dir, "a.card", "RESULT: a\nall green")
	tsv := filepath.Join(dir, "cards.tsv")
	if err := os.WriteFile(tsv, []byte("a\t1\tmodel\t"+card+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A runner that sleeps past the deadline and publishes nothing. The deadline is
	// fired by the injected clock, so the kill is the test's own event and never a
	// bet on how loaded the machine is.
	runner := runnerDoing(t, dir, "slow", runnerStep{Op: "sleep", Ms: 30000})
	clk := newManualClock()
	code, out, _ := runBatchClock(BatchInput{
		ID: "B1", Deadline: 30 * time.Second, Cards: tsv, Root: root, Runner: runner,
	}, clk, func() {
		clk.waitDeadline()
		clk.advance(30 * time.Second)
	})
	if code != 1 {
		t.Fatalf("a batch whose only card is killed at the deadline exits 1, got %d:\n%s", code, out)
	}
	if !strings.Contains(out, "BATCH B1 n=1 done=0 abstain=1") {
		t.Fatalf("a killed card is an abstain, never a hang:\n%s", out)
	}
}

func TestBatchKillsIdleCardEarly(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	card := writeCard(t, dir, "a.card", "RESULT: a\nall green")
	tsv := filepath.Join(dir, "cards.tsv")
	if err := os.WriteFile(tsv, []byte("a\t1\tmodel\t"+card+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A runner that writes nothing to its log and never publishes a result: its harness.log
	// stays empty, so the idle monitor kills it. The idle window is advanced by the injected
	// clock, so the kill is the test's own event, not a race with the deadline.
	runner := runnerDoing(t, dir, "idle", runnerStep{Op: "sleep", Ms: 30000})
	clk := newManualClock()
	code, out, _ := runBatchClock(BatchInput{
		ID: "B1", Deadline: 30 * time.Second, Idle: testIdleBudget, Cards: tsv, Root: root, Runner: runner,
	}, clk, func() {
		clk.waitTick()
		clk.advance(testIdleBudget)
		clk.tick()
	})
	if code != 1 {
		t.Fatalf("a batch with an idle-killed card exits 1, got %d:\n%s", code, out)
	}
	if !strings.Contains(out, "BATCH B1 n=1 done=0 abstain=1 in=0 out=0 usd=0.0000 idle=1") {
		t.Fatalf("the idle kill is counted as an abstain and the idle count:\n%s", out)
	}
	if !strings.Contains(out, "a slot=1: "+idleReason+" log=0") {
		t.Fatalf("an idle-killed card names its idle reason token:\n%s", out)
	}
}

func TestBatchIdleDoesNotKillAWritingCard(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	card := writeCard(t, dir, "a.card", "RESULT: a\nall green")
	tsv := filepath.Join(dir, "cards.tsv")
	if err := os.WriteFile(tsv, []byte("a\t1\tmodel\t"+card+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A runner whose log grows the whole time: it writes to stdout every tick, then publishes
	// its result. The idle monitor must leave it alone because its log never sits still. The
	// window is injected: the test proves a tick at the end of a whole --idle sees the growth
	// since the previous tick and does not kill, rather than betting the writes beat a real
	// four-second clock.
	runner := runnerDoing(t, dir, "writing",
		runnerStep{Op: "mkdir", Path: "{job}"},
		runnerStep{Op: "stdout", Body: "working {i}", N: 6, Ms: 150},
		publishCard("{job}"),
	)
	log := filepath.Join(root, "1", "jobs", "a", "harness.log")
	clk := newManualClock()
	code, out, errs := runBatchClock(BatchInput{
		ID: "B1", Deadline: 30 * time.Second, Idle: testIdleBudget, Cards: tsv, Root: root, Runner: runner,
	}, clk, func() {
		clk.waitTick()
		waitForLog(t, log, 1)
		clk.tick()
		waitForLog(t, log, 6)
		clk.advance(testIdleBudget)
		clk.tick()
	})
	if code != 0 {
		t.Fatalf("a batch over a card that keeps writing exits 0, got %d; stderr: %s", code, errs)
	}
	if !strings.Contains(out, "BATCH B1 n=1 done=1 abstain=0 in=0 out=0 usd=0.0000 idle=0") {
		t.Fatalf("a writing card is done, never idle-killed:\n%s", out)
	}
	if !strings.Contains(out, "a slot=1: all green") {
		t.Fatalf("the writing card's line 2 is gathered verbatim:\n%s", out)
	}
}

func TestBatchLineCountsIdle(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	tsv := writeCards(t, dir, [][2]string{
		{"a", "RESULT: a\nall green"},
		{"b", "RESULT: b\ndone and clean"},
	})
	// One runner that idles card a (writes nothing) and finishes card b (writes then publishes).
	runner := runnerDoing(t, dir, "mixed",
		runnerStep{Op: "mkdir", Path: "{job}"},
		runnerStep{Op: "sleep", Ms: 30000, When: "label==a"},
		publishCard("{job}"),
	)
	// Card b's published result is real and its process is real; the test waits for
	// it (the event) before it lets the injected clock say card a has been silent
	// for --idle. No sleep: the readiness wait is the assertion's event.
	clk := newManualClock()
	code, out, _ := runBatchClock(BatchInput{
		ID: "B1", Deadline: 30 * time.Second, Idle: testIdleBudget, Cards: tsv, Root: root, Runner: runner,
	}, clk, func() {
		clk.waitTick()
		waitForFile(t, filepath.Join(root, "2", "jobs", "b", "RESULT.md"))
		clk.tick()
		clk.advance(testIdleBudget)
		clk.tick()
	})
	if code != 1 {
		t.Fatalf("a batch with one idle kill exits 1, got %d:\n%s", code, out)
	}
	if !strings.Contains(out, "BATCH B1 n=2 done=1 abstain=1 in=0 out=0 usd=0.0000 idle=1") {
		t.Fatalf("the BATCH line counts the idle kill in its own idle=<n> field:\n%s", out)
	}
	if !strings.Contains(out, "a slot=1: "+idleReason) || !strings.Contains(out, "b slot=2: done and clean") {
		t.Fatalf("the idle card is named with its reason and the done card is folded:\n%s", out)
	}
}

// TestIdleWatchesNativeLog: a runner appends a line to <slot>/native.log every 200 ms for
// longer than --idle=1s. The idle monitor must watch the child's own log (native.log once it
// exists), not harness.log, so the card is never killed.
func TestIdleWatchesNativeLog(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	card := writeCard(t, dir, "a.card", "RESULT: a\nall green")
	tsv := filepath.Join(dir, "cards.tsv")
	if err := os.WriteFile(tsv, []byte("a\t1\tmodel\t"+card+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := runnerDoing(t, dir, "native-writes",
		runnerStep{Op: "mkdir", Path: "{job}"},
		runnerStep{Op: "appendn", Path: "{root}/{slot}/native.log", Body: "line {i}", N: 12, Ms: 200},
		publishCard("{job}"),
	)
	// The kill window is injected: a tick after a whole --idle sees the growth since
	// the previous tick and leaves the card alone, so the assertion is about the file
	// the monitor watched, not about the runner beating a real clock.
	log := filepath.Join(root, "1", "native.log")
	clk := newManualClock()
	code, out, errs := runBatchClock(BatchInput{
		ID: "B1", Deadline: 30 * time.Second, Idle: testIdleBudget, Cards: tsv, Root: root, Runner: runner,
	}, clk, func() {
		clk.waitTick()
		waitForLog(t, log, 1)
		clk.tick()
		waitForLog(t, log, 6)
		clk.advance(testIdleBudget)
		clk.tick()
	})
	if code != 0 {
		t.Fatalf("a card writing native.log is never idle-killed, exits 0, got %d; stderr: %s", code, errs)
	}
	if !strings.Contains(out, "BATCH B1 n=1 done=1 abstain=0 in=0 out=0 usd=0.0000 idle=0") {
		t.Fatalf("a card whose native.log grows is done, not idle-killed:\n%s", out)
	}
	if !strings.Contains(out, "a slot=1: all green") {
		t.Fatalf("the native-writing card's line 2 is gathered verbatim:\n%s", out)
	}
}

// TestIdleKillsWhenNativeLogStops: a runner appends to native.log for ~600 ms then goes
// quiet. The monitor, watching native.log, kills the card after --idle=1s and names the file
// it watched in the ABSTAIN reason.
func TestIdleKillsWhenNativeLogStops(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	card := writeCard(t, dir, "a.card", "RESULT: a\nall green")
	tsv := filepath.Join(dir, "cards.tsv")
	if err := os.WriteFile(tsv, []byte("a\t1\tmodel\t"+card+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := runnerDoing(t, dir, "native-stops",
		runnerStep{Op: "appendn", Path: "{root}/{slot}/native.log", Body: "line {i}", N: 3, Ms: 200},
		runnerStep{Op: "sleep", Ms: 30000},
	)
	// The monitor watches the child's own native.log, and the kill is driven by the
	// injected clock: the runner is given all the time the machine needs to write its
	// three lines, then the clock alone says the file has sat still for --idle. The
	// old test raced the runner's first write against a real four-second window.
	clk := newManualClock()
	code, out, _ := runBatchClock(BatchInput{
		ID: "B1", Deadline: 30 * time.Second, Idle: testIdleBudget, Cards: tsv, Root: root, Runner: runner,
	}, clk, func() {
		clk.waitTick()
		waitForLog(t, filepath.Join(root, "1", "native.log"), 3)
		clk.tick()
		clk.advance(testIdleBudget)
		clk.tick()
	})
	if code != 1 {
		t.Fatalf("a card whose native.log stops growing is idle-killed, exits 1, got %d:\n%s", code, out)
	}
	if !strings.Contains(out, "BATCH B1 n=1 done=0 abstain=1 in=0 out=0 usd=0.0000 idle=1") {
		t.Fatalf("the stopped native.log card is counted idle:\n%s", out)
	}
	if !strings.Contains(out, "a slot=1: "+idleReason+" log=3 watched="+filepath.Join(resolvedPath(t, root), "1", "native.log")) {
		t.Fatalf("the ABSTAIN reason names the child's log it watched:\n%s", out)
	}
}

func TestBatchOutputBounded(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	cards := make([][2]string, 20)
	for i := range cards {
		label := string(rune('a' + i))
		cards[i] = [2]string{label, "RESULT: " + label + "\nHOLD evidence " + label}
	}
	tsv := writeCards(t, dir, cards)
	runner := fakeRunner(t, dir)
	code, out, _ := runBatch(t, tsv, root, runner, 30*time.Second)
	if code != 1 {
		t.Fatalf("a batch that holds exits 1, got %d", code)
	}
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) > len(cards)+12 {
		t.Fatalf("the packet is bounded at n+12 lines: got %d lines for n=%d:\n%s", len(lines), len(cards), out)
	}
	if !strings.Contains(out, "HOLD:") {
		t.Fatalf("a line 2 holding HOLD produces a HOLD line:\n%s", out)
	}
	holds := 0
	for _, l := range lines {
		if strings.HasPrefix(l, "HOLD:") {
			holds++
		}
	}
	if holds > 11 {
		t.Fatalf("HOLD lines are capped: got %d", holds)
	}
	if !strings.HasPrefix(out, "BATCH B1 n=20 done=20 abstain=0") {
		t.Fatalf("the BATCH line names the counts:\n%s", out)
	}
}

func TestStalledCardIsNamed(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	tsv := writeCards(t, dir, [][2]string{
		{"a", "RESULT: a\nMISSING"},
	})
	runner := fakeRunnerLog(t, dir, "a.log", "SANDBOX OK backend=fake-wall cmd=opencode\n")
	code, out, _ := runBatch(t, tsv, root, runner, 30*time.Second)
	if code != 1 {
		t.Fatalf("a batch with a stalled card exits 1, got %d:\n%s", code, out)
	}
	if !strings.Contains(out, "a slot=1: ABSTAIN reason=no-result log=0") {
		t.Fatalf("a card that ended with no output after the wall opened names its reason and its empty log:\n%s", out)
	}
	if !strings.Contains(out, "stalled=1") {
		t.Fatalf("the stall is counted on the BATCH line, not read out of a RESULT:\n%s", out)
	}
}

func TestWorkingCardCountsLogLines(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	tsv := writeCards(t, dir, [][2]string{
		{"a", "RESULT: a\nall green"},
	})
	runner := fakeRunnerLog(t, dir, "a.log",
		"SANDBOX OK backend=fake-wall cmd=opencode\nline one\nline two\nline three\n")
	code, out, errs := runBatch(t, tsv, root, runner, 30*time.Second)
	if code != 0 {
		t.Fatalf("a clean card exits 0, got %d; stderr: %s", code, errs)
	}
	if !strings.Contains(out, "a slot=1: all green log=3") {
		t.Fatalf("the card line counts its log lines after the sandbox header:\n%s", out)
	}
	if !strings.Contains(out, "BATCH B1 n=1 done=1 abstain=0 in=0 out=0 usd=0.0000 idle=0 stalled=0") {
		t.Fatalf("a working card is not a stall:\n%s", out)
	}
}

func TestBatchLineCountsStalled(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	tsv := writeCards(t, dir, [][2]string{
		{"a", "RESULT: a\nall green"},
		{"b", "RESULT: b\nMISSING"},
	})
	runner := fakeRunnerLog(t, dir, "log", "SANDBOX OK backend=fake-wall cmd=opencode\nline one\nline two\n")
	code, out, _ := runBatch(t, tsv, root, runner, 30*time.Second)
	if code != 1 {
		t.Fatalf("a batch with one stalled card exits 1, got %d:\n%s", code, out)
	}
	if !strings.Contains(out, "BATCH B1 n=2 done=1 abstain=1 in=0 out=0 usd=0.0000 idle=0 stalled=1") {
		t.Fatalf("the BATCH line counts the stalled cards:\n%s", out)
	}
	if !strings.Contains(out, "b slot=2: ABSTAIN reason=no-result log=0") {
		t.Fatalf("the stalled card is named by its reason token and its empty log:\n%s", out)
	}
}

// Slice 10: the batch packet sums tokens_in, tokens_out and usd across cards, from each
// card's usage.tsv, and prints the totals on the BATCH line as in=<n> out=<n> usd=<n>.
func TestBatchSumsUsage(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	tsv := writeCards(t, dir, [][2]string{
		{"a", "RESULT: a\nall green"},
		{"b", "RESULT: b\na second card"},
	})
	// The native run writes one usage.tsv per card; the batch sums them during gather. The
	// job directory is made by scatter, so the usage files are laid down here, in the shape
	// the runner leaves for a real native run.
	for i, spec := range []struct {
		label, in, out, usd string
	}{
		{"a", "100", "50", "0.2500"},
		{"b", "8", "5", "0.7500"},
	} {
		job := filepath.Join(root, itoa(i+1), "jobs", spec.label)
		if err := os.MkdirAll(job, 0o755); err != nil {
			t.Fatal(err)
		}
		row := UsageRow{
			"job": spec.label, "attempt": "1", "started": "2026-09-13T00:00:00Z",
			"ended": "2026-09-13T00:01:00Z", "rc": "0",
			"provider": "deepseek", "model": "deepseek-chat",
			"tokens_in": spec.in, "tokens_out": spec.out,
			"cache_write": "-", "cache_read": "-", "reasoning": "-", "usd": spec.usd,
		}
		if err := WriteCardUsage(filepath.Join(job, "usage.tsv"), row); err != nil {
			t.Fatal(err)
		}
	}
	runner := fakeRunner(t, dir)
	code, out, errs := runBatch(t, tsv, root, runner, 30*time.Second)
	if code != 0 {
		t.Fatalf("a clean batch exits 0, got %d; stderr: %s", code, errs)
	}
	if !strings.Contains(out, "BATCH B1 n=2 done=2 abstain=0 in=108 out=55 usd=1.0000") {
		t.Fatalf("the BATCH line sums tokens and dollars across cards:\n%s", out)
	}
}

// Lesson 24: when usage.tsv is in <root>/<slot>/usage.tsv rather than <root>/<slot>/jobs/<label>/usage.tsv,
// the batch gather still reads the slot fallback and sums in/out/usd onto the BATCH line.
func TestBatchSumsUsageFromSlotFallback(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	tsv := writeCards(t, dir, [][2]string{
		{"a", "RESULT: a\nall green"},
		{"b", "RESULT: b\na second card"},
	})
	for i, spec := range []struct {
		label, in, out, usd string
	}{
		{"a", "100", "50", "0.2500"},
		{"b", "8", "5", "0.7500"},
	} {
		slot := filepath.Join(root, itoa(i+1))
		if err := os.MkdirAll(slot, 0o755); err != nil {
			t.Fatal(err)
		}
		row := UsageRow{
			"job": spec.label, "attempt": "1", "started": "2026-09-13T00:00:00Z",
			"ended": "2026-09-13T00:01:00Z", "rc": "0",
			"provider": "deepseek", "model": "deepseek-chat",
			"tokens_in": spec.in, "tokens_out": spec.out,
			"cache_write": "-", "cache_read": "-", "reasoning": "-", "usd": spec.usd,
		}
		if err := WriteCardUsage(filepath.Join(slot, "usage.tsv"), row); err != nil {
			t.Fatal(err)
		}
	}
	runner := fakeRunner(t, dir)
	code, out, errs := runBatch(t, tsv, root, runner, 30*time.Second)
	if code != 0 {
		t.Fatalf("a clean batch exits 0, got %d; stderr: %s", code, errs)
	}
	if !strings.Contains(out, "BATCH B1 n=2 done=2 abstain=0 in=108 out=55 usd=1.0000") {
		t.Fatalf("the BATCH line sums tokens and dollars from slot fallback usage.tsv:\n%s", out)
	}
}

func TestBatchAllocatesFreeSlots(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	a := writeCard(t, dir, "a.card", "RESULT: a\nall green")
	b := writeCard(t, dir, "b.card", "RESULT: b\ndone and clean")
	tsv := filepath.Join(dir, "cards.tsv")
	if err := os.WriteFile(tsv, []byte("a\t\tmodel\t"+a+"\nb\t-\tmodel\t"+b+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := fakeRunner(t, dir)
	code, out, errs := runBatch(t, tsv, root, runner, 30*time.Second)
	if code != 0 {
		t.Fatalf("a clean auto-allocated batch exits 0, got %d; stderr: %s", code, errs)
	}
	if !strings.Contains(out, "a slot=1: all green") {
		t.Fatalf("the first card takes the lowest free slot (1):\n%s", out)
	}
	if !strings.Contains(out, "b slot=2: done and clean") {
		t.Fatalf("the second card takes the next free slot (2):\n%s", out)
	}
}

func TestBatchRefusesDuplicateSlots(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	card := writeCard(t, dir, "a.card", "RESULT: a\nall green")
	tsv := filepath.Join(dir, "cards.tsv")
	if err := os.WriteFile(tsv, []byte("a\t1\tmodel\t"+card+"\nb\t1\tmodel\t"+card+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(dir, "launched")
	runner := runnerDoing(t, dir, "marker", runnerStep{Op: "touch", Path: sentinel})
	code, out, errs := runBatch(t, tsv, root, runner, 30*time.Second)
	if code != 1 {
		t.Fatalf("a batch that names one slot twice exits 1, got %d; stderr: %s", code, errs)
	}
	if !strings.Contains(errs, "BATCH REFUSED slot 1 named twice (a, b)") {
		t.Fatalf("the refusal names the slot and both labels:\n%s", errs)
	}
	if _, err := os.Stat(sentinel); err == nil {
		t.Fatalf("the batch is refused before any launch; no runner may have run")
	}
	if strings.Contains(out, "BATCH") {
		t.Fatalf("a refused batch emits no packet:\n%s", out)
	}
}

func TestBatchSkipsBusySlot(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	busy := filepath.Join(root, "1", "jobs", "busy")
	if err := os.MkdirAll(busy, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(busy, "lock"), []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		t.Fatal(err)
	}
	card := writeCard(t, dir, "a.card", "RESULT: a\nall green")
	tsv := filepath.Join(dir, "cards.tsv")
	if err := os.WriteFile(tsv, []byte("a\t\tmodel\t"+card+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := fakeRunner(t, dir)
	code, out, errs := runBatch(t, tsv, root, runner, 30*time.Second)
	if code != 0 {
		t.Fatalf("a batch that skips a busy slot exits 0, got %d; stderr: %s", code, errs)
	}
	if !strings.Contains(out, "a slot=2: all green") {
		t.Fatalf("allocation skips the slot whose lock carries a live pid:\n%s", out)
	}
}

// TestGatherResultWinsOverEmptyLog: a valid RESULT.md (line 1 equals the contract) is done
// whatever the log count. fakeRunner publishes RESULT.md and writes no run log, so the card
// has no native.log and an empty harness.log -- the shape run 10's defect mistook for a stall.
func TestGatherResultWinsOverEmptyLog(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	tsv := writeCards(t, dir, [][2]string{
		{"a", "RESULT: a\nall green"},
	})
	runner := fakeRunner(t, dir)
	code, out, errs := runBatch(t, tsv, root, runner, 30*time.Second)
	if code != 0 {
		t.Fatalf("a card with a valid RESULT.md exits 0 even with an empty log, got %d; stderr: %s", code, errs)
	}
	if !strings.Contains(out, "BATCH B1 n=1 done=1 abstain=0 in=0 out=0 usd=0.0000 idle=0 stalled=0") {
		t.Fatalf("a valid RESULT.md is done and never stalled by an empty log:\n%s", out)
	}
	if !strings.Contains(out, "a slot=1: all green") {
		t.Fatalf("line 2 is carried verbatim:\n%s", out)
	}
}

// TestGatherCountsNativeLog: the log the gather counts is the child's own run log,
// <slot>/native.log, not the job's harness.log (which holds only the runner's NATIVE OK line).
// The card line's log=<n> reflects native.log's non-header lines.
func TestGatherCountsNativeLog(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	tsv := writeCards(t, dir, [][2]string{
		{"a", "RESULT: a\nall green"},
	})
	runner := runnerDoing(t, dir, "native-log",
		publishCard("{job}"),
		runnerStep{Op: "write", Path: "{root}/{slot}/native.log",
			Body: "SANDBOX OK backend=fake-wall cmd=opencode\nline one\nline two\n"},
		runnerStep{Op: "stdout", Body: "NATIVE OK label=a rc=0"},
	)
	code, out, errs := runBatch(t, tsv, root, runner, 30*time.Second)
	if code != 0 {
		t.Fatalf("a clean card exits 0, got %d; stderr: %s", code, errs)
	}
	if !strings.Contains(out, "a slot=1: all green log=2") {
		t.Fatalf("the card line counts the child's native.log (2 lines), not harness.log's NATIVE OK line:\n%s", out)
	}
}

// TestGatherStallOnlyWithoutResult: the stall rule fires only for a card without a valid
// RESULT.md. A card whose RESULT.md is present and matches its contract is done whatever its
// log count; a card with no result at all and no output is named stalled.
func TestGatherStallOnlyWithoutResult(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	tsv := writeCards(t, dir, [][2]string{
		{"a", "RESULT: a\nall green"},
		{"b", "RESULT: b\nMISSING"},
	})
	runner := fakeRunner(t, dir)
	code, out, _ := runBatch(t, tsv, root, runner, 30*time.Second)
	if code != 1 {
		t.Fatalf("a batch with one stalled card exits 1, got %d:\n%s", code, out)
	}
	if !strings.Contains(out, "BATCH B1 n=2 done=1 abstain=1 in=0 out=0 usd=0.0000 idle=0 stalled=1") {
		t.Fatalf("the stall is counted only for the card without a valid RESULT.md:\n%s", out)
	}
	if !strings.Contains(out, "a slot=1: all green") || !strings.Contains(out, "b slot=2: ABSTAIN reason=no-result log=0") {
		t.Fatalf("done wins over an empty log; the stall names only the missing-result card:\n%s", out)
	}
	if strings.Contains(out, "a slot=1: ABSTAIN") {
		t.Fatalf("a valid RESULT.md is never stalled by an empty log:\n%s", out)
	}
}

// resolvedPath is a path made absolute and symlink-resolved, which is the form admission
// records. A test that builds its expectation from t.TempDir() must resolve it too: on
// darwin the temp directory is handed out under /var, a symlink to /private/var, so the
// unresolved spelling and the recorded one are two names for one directory and a string
// compare between them fails on every macOS bench (issue #578).
func resolvedPath(t *testing.T, path string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("resolving %s: %v", path, err)
	}
	return real
}

// TestBatchRelativeRootIsAbsolutized: the batch absolutizes --root at admission, so the
// runner's fifth argument ($5) and NOVA_SWARM_ROOT both name the root in absolute and
// symlink-resolved form -- a relative root passed on to the wall was the refusal Emma met
// (the wall refused `--read ./root/1` and `--write root/1/...`). The runner records both and
// the test asserts each is the resolved absolute root, never the relative spelling it was
// handed.
func TestBatchRelativeRootIsAbsolutized(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	tsv := writeCards(t, dir, [][2]string{
		{"a", "RESULT: a\nall green"},
	})
	// The runner records its fifth argument and NOVA_SWARM_ROOT beside the root, then
	// publishes RESULT.md.
	runner := runnerDoing(t, dir, "record",
		runnerStep{Op: "write", Path: "{root}/.arg5", Body: "{arg5}"},
		runnerStep{Op: "write", Path: "{root}/.envroot", Body: "{env:NOVA_SWARM_ROOT}"},
		runnerStep{Op: "mkdir", Path: "{job}"},
		publishCard("{job}"),
	)
	// Run from a foreign working directory so a relative root is meaningful: relRoot is the
	// same directory as root, spelled without its leading path.
	foreign := t.TempDir()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(foreign); err != nil {
		t.Fatalf("chdir to a foreign directory: %v", err)
	}
	defer func() { _ = os.Chdir(orig) }()
	relRoot, err := filepath.Rel(foreign, root)
	if err != nil {
		t.Fatal(err)
	}
	code, out, errs := runBatch(t, tsv, relRoot, runner, 30*time.Second)
	if code != 0 {
		t.Fatalf("a clean batch exits 0, got %d; stderr: %s:\n%s", code, errs, out)
	}
	arg5 := strings.TrimSpace(readTestFile(t, filepath.Join(root, ".arg5")))
	want, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("resolving the root %q: %v", root, err)
	}
	if got, err := filepath.EvalSymlinks(arg5); err != nil {
		t.Errorf("the runner's $5 %q does not resolve: %v", arg5, err)
	} else if got != want {
		t.Errorf("the runner's $5 is %q, want the absolute root %q; a relative root reached the runner", arg5, root)
	}
	envRoot := strings.TrimSpace(readTestFile(t, filepath.Join(root, ".envroot")))
	if got, err := filepath.EvalSymlinks(envRoot); err != nil {
		t.Errorf("NOVA_SWARM_ROOT %q does not resolve: %v", envRoot, err)
	} else if got != want {
		t.Errorf("NOVA_SWARM_ROOT is %q, want the absolute root %q; a relative root reached the environment", envRoot, root)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(raw)
}

// ISSUE #163: a card whose job was refused for size is scored reason=input-limit, read from
// the STRUCTURED signal the supervisor recorded, not from prose over the transcript. The
// runner writes the one line -- `INPUT LIMIT class=token value=12345 limit=8192` -- into the
// card's native.log and no RESULT.md, and the gather names the class instead of a plain
// missing-result abstain.
func TestBatchScoresInputLimit(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	tsv := writeCards(t, dir, [][2]string{
		{"a", "RESULT: a\ndone and green"},
		{"b", "RESULT: b\nMISSING"},
	})
	runner := runnerDoing(t, dir, "runner-inputlimit",
		runnerStep{Op: "mkdir", Path: "{job}"},
		runnerStep{Op: "write", Path: "{root}/{slot}/native.log", When: "line2==MISSING",
			Body: "INPUT LIMIT class=token value=12345 limit=8192"},
		runnerStep{Op: "exit", N: 0, When: "line2==MISSING"},
		publishCard("{job}"),
	)
	code, out, errs := runBatch(t, tsv, root, runner, 30*time.Second)
	if code != 1 {
		t.Fatalf("a batch with an input-limited card is not green, got %d; stderr: %s", code, errs)
	}
	if !strings.Contains(out, "b slot=2: ABSTAIN reason=input-limit") {
		t.Fatalf("an input-limited card scores reason=input-limit:\n%s", out)
	}
	if !strings.Contains(out, "a slot=1: done and green") {
		t.Fatalf("a card that fits still scores its line 2:\n%s", out)
	}
}

// TestBatchThenRunsOnlyWhenAllDone: the --then follow-on runs only when every card is done.
// All done runs the command and records its rc; one abstain prints SKIPPED and exits 3.
func TestBatchThenRunsOnlyWhenAllDone(t *testing.T) {
	// all done: the follow-on runs in the batch's root and its rc is recorded on the line.
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	tsv := writeCards(t, dir, [][2]string{
		{"a", "RESULT: a\nall green"},
		{"b", "RESULT: b\ndone and clean"},
	})
	runner := fakeRunner(t, dir)
	var out, errb bytes.Buffer
	code := Batch(BatchInput{
		ID: "B1", Deadline: 30 * time.Second, Cards: tsv, Root: root, Runner: runner,
		Then:   `[ "$BATCH_ID" = "B1" ] && [ "$BATCH_DONE" = "$BATCH_N" ] && exit 7`,
		Stdout: &out, Stderr: &errb,
	})
	if code != 0 {
		t.Fatalf("a clean batch exits 0, got %d; stderr: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "BATCH THEN rc=7") {
		t.Fatalf("all done runs the follow-on and records its rc:\n%s", out.String())
	}

	// one abstain: the follow-on is skipped and the batch exits 3.
	dir = t.TempDir()
	root = filepath.Join(dir, "root")
	tsv = writeCards(t, dir, [][2]string{
		{"a", "RESULT: a\nall green"},
		{"b", "RESULT: b\nMISSING"},
	})
	runner = fakeRunner(t, dir)
	out.Reset()
	var out2, errb2 bytes.Buffer
	code = Batch(BatchInput{
		ID: "B1", Deadline: 30 * time.Second, Cards: tsv, Root: root, Runner: runner,
		Then:   `exit 0`,
		Stdout: &out2, Stderr: &errb2,
	})
	if code != 3 {
		t.Fatalf("a batch with an abstain skips the follow-on and exits 3, got %d; stderr: %s", code, errb2.String())
	}
	if !strings.Contains(out2.String(), "BATCH THEN SKIPPED done=1 n=2 abstain=1 stalled=1") {
		t.Fatalf("a skipped follow-on prints the counts:\n%s", out2.String())
	}
	if strings.Contains(out2.String(), "BATCH THEN rc=") {
		t.Fatalf("the follow-on must not run on an abstain:\n%s", out2.String())
	}
}

// ISSUE #593: IDLE MEANS NO CHILD ACTIVITY, NOT ONLY NO LOG GROWTH. On 2026-09-15 cards
// 664-670 were killed "idle 300s" while their harness sat in a `go test` that prints nothing
// for minutes: the work was alive, the log was not. A card is active while its process tree
// is alive and its CPU time advanced since the last sample -- a grandchild's CPU counts, the
// way a harness's own child counts -- and idle only when NEITHER the log NOR the tree moved
// for --idle. The fake harness here spins in a grandchild and writes nothing at all; the
// other one sleeps and writes nothing, and only that one is killed.
func TestIdleWatchCountsChildActivity(t *testing.T) {
	windowsIsNotABench(t)
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	tsv := writeCards(t, dir, [][2]string{
		{"spin", "RESULT: spin\nbusy and silent"},
		{"sleeps", "RESULT: sleeps\nMISSING"},
	})
	// One runner, two cards: `spin` starts a silent grandchild that burns CPU for far longer
	// than --idle and then publishes its result; `sleeps` sleeps past the idle window. Neither
	// writes one byte to its log.
	// The spinner burns CPU and writes nothing, and it carries its own deadline so that a red
	// run of this test leaves no process behind: it ends on its own at 20 s whatever happens
	// to its parent. It is a GRANDCHILD of the card, which is the point -- the monitor reads
	// the whole process tree's CPU time.
	// The marker appears once the spin card's grandchild has burned for five real
	// seconds, so the test waits on that event rather than on a clock. Before it takes
	// its first activity sample it waits for that grandchild to appear in the tree with
	// CPU accrued -- a slow fork on a loaded runner must not be read as idle -- and the
	// sample taken after the marker then sees the burner gone: the tree's CPU time did
	// not grow, it fell, and a tree that lost a charged process is working, not still.
	// The sleeping card has no such movement, so the same tick sees it idle and kills it.
	runner := runnerDoing(t, dir, "silent",
		runnerStep{Op: "mkdir", Path: "{job}"},
		runnerStep{Op: "write", Path: "{root}/spin-started", When: "label==spin"},
		runnerStep{Op: "sleep", Ms: 30000, When: "label==sleeps"},
		runnerStep{Op: "exit", N: 0, When: "label==sleeps"},
		runnerStep{Op: "sleep", Ms: 200, When: "label==spin"},
		publishCard("{job}"),
	)
	var round atomic.Int64
	sampler := &fakeTreeSampler{}
	sampler.cpuForCard = func(cardIndex, pid int) (uint64, bool) {
		r := round.Load()
		if cardIndex == 0 { // spin
			// Round 0: 100ms. Round 1+: 300ms (grew by 200ms >= 50ms)
			return uint64(100_000_000 + r*200_000_000), true
		}
		if cardIndex == 1 { // sleeps
			return 50_000_000, true // constant: zero growth
		}
		return 0, false
	}
	clk := newManualClock()
	code, out, errs := runBatchClock(BatchInput{
		ID: "B1", Deadline: 30 * time.Second, Idle: testIdleBudget, Cards: tsv, Root: root, Runner: runner,
		snapshot: func() activitySnapshot {
			round.Add(1)
			return sampler
		},
	}, clk, func() {
		waitForFile(t, filepath.Join(root, "spin-started"))
		clk.waitTick()
		clk.tick() // round 1: initial reading
		clk.advance(testIdleBudget)
		clk.tick() // round 2: spin accrued 200ms CPU, sleeps stayed still
	})
	if code != 1 {
		t.Fatalf("a batch holding one idle card exits 1, got %d; stderr: %s", code, errs)
	}
	if !strings.Contains(out, "spin slot=1: busy and silent") {
		t.Fatalf("a card whose process tree is burning CPU is never idle-killed, however silent its log:\n%s", out)
	}
	if !strings.Contains(out, "sleeps slot=2: "+idleReason) {
		t.Fatalf("a card whose log and process tree both sat still for --idle is idle-killed:\n%s", out)
	}
	if !strings.Contains(out, "BATCH B1 n=2 done=1 abstain=1 in=0 out=0 usd=0.0000 idle=1") {
		t.Fatalf("exactly one of the two silent cards is counted idle:\n%s", out)
	}
}

// TestIdleWatchTopologyChangeKeepsCardAlive: a tree whose CPU shrank lost a process (e.g.
// a compiler or test subprocess finished and was reaped). That process departure is work
// done, so the idle monitor treats it as activity and keeps the silent card alive.
func TestIdleWatchTopologyChangeKeepsCardAlive(t *testing.T) {
	windowsIsNotABench(t)
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	tsv := writeCards(t, dir, [][2]string{
		{"worker", "RESULT: worker\nfinished after subproc"},
	})
	runner := runnerDoing(t, dir, "silent-worker",
		runnerStep{Op: "mkdir", Path: "{job}"},
		runnerStep{Op: "write", Path: "{root}/worker-started"},
		runnerStep{Op: "sleep", Ms: 200},
		publishCard("{job}"),
	)
	var round atomic.Int64
	sampler := &fakeTreeSampler{}
	sampler.cpuForCard = func(cardIndex, pid int) (uint64, bool) {
		r := round.Load()
		if r <= 1 {
			return 200_000_000, true // initial reading: tree has child process
		}
		// Second reading: child process was reaped, so TreeCPU dropped to 100ms.
		// A tree whose CPU fell shrank, which resets lastGrow and saves the card.
		return 100_000_000, true
	}
	clk := newManualClock()
	code, out, errs := runBatchClock(BatchInput{
		ID: "B-TOPO", Deadline: 30 * time.Second, Idle: testIdleBudget, Cards: tsv, Root: root, Runner: runner,
		snapshot: func() activitySnapshot {
			round.Add(1)
			return sampler
		},
	}, clk, func() {
		waitForFile(t, filepath.Join(root, "worker-started"))
		clk.waitTick()
		clk.tick() // round 1
		clk.advance(testIdleBudget)
		clk.tick() // round 2: tree CPU shrank from 200ms to 100ms
	})
	if code != 0 {
		t.Fatalf("batch exit = %d, want 0; stderr: %s", code, errs)
	}
	if !strings.Contains(out, "worker slot=1: finished after subproc") {
		t.Fatalf("a card whose process tree shrank is kept alive and finishes:\n%s", out)
	}
	if strings.Contains(out, "idle=1") {
		t.Fatalf("a topology-changed card was counted idle:\n%s", out)
	}
}

// TestIdleWatchSub1PercentJitterKilled: CPU growth smaller than 1% of the sample interval
// is Darwin scheduler jitter on sleeping threads (issue #916), not real work. The card
// must still be idle-killed once the idle window expires.
func TestIdleWatchSub1PercentJitterKilled(t *testing.T) {
	windowsIsNotABench(t)
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	tsv := writeCards(t, dir, [][2]string{
		{"jittery", "RESULT: jittery\nMISSING"},
	})
	runner := runnerDoing(t, dir, "silent-jitter",
		runnerStep{Op: "mkdir", Path: "{job}"},
		runnerStep{Op: "write", Path: "{root}/jitter-started"},
		runnerStep{Op: "sleep", Ms: 30000},
	)
	var round atomic.Int64
	sampler := &fakeTreeSampler{}
	sampler.cpuForCard = func(cardIndex, pid int) (uint64, bool) {
		r := round.Load()
		// Over a 5-second interval, 1% is 50ms (50,000,000 ns).
		// Growth of only 1,000 ns (1 microsecond) is jitter (< 1%).
		return uint64(100_000_000 + r*1_000), true
	}
	clk := newManualClock()
	code, out, errs := runBatchClock(BatchInput{
		ID: "B-JITTER", Deadline: 30 * time.Second, Idle: testIdleBudget, Cards: tsv, Root: root, Runner: runner,
		snapshot: func() activitySnapshot {
			round.Add(1)
			return sampler
		},
	}, clk, func() {
		waitForFile(t, filepath.Join(root, "jitter-started"))
		clk.waitTick()
		clk.tick() // round 1
		clk.advance(testIdleBudget)
		clk.tick() // round 2: only 1 microsecond growth, card is idle killed
	})
	if code != 1 {
		t.Fatalf("a batch whose only card is idle exits 1, got %d; stderr: %s", code, errs)
	}
	if !strings.Contains(out, "jittery slot=1: "+idleReason) {
		t.Fatalf("a card with sub-1%% CPU jitter is idle-killed:\n%s", out)
	}
}

// TestIdleWatchUnknownSampleReliesOnLog: when TreeCPU returns ok=false (unsupported OS
// or unreadable process table), the monitor falls back to watching the log alone.
func TestIdleWatchUnknownSampleReliesOnLog(t *testing.T) {
	windowsIsNotABench(t)
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	tsv := writeCards(t, dir, [][2]string{
		{"active-log", "RESULT: active-log\nfinished via log"},
		{"silent", "RESULT: silent\nMISSING"},
	})
	runner := runnerDoing(t, dir, "log-watcher",
		runnerStep{Op: "mkdir", Path: "{job}"},
		runnerStep{Op: "stdout", Body: "line 1", When: "label==active-log"},
		runnerStep{Op: "write", Path: "{root}/line1", When: "label==active-log"},
		runnerStep{Op: "sleep", Ms: 30000, When: "label==silent"},
		runnerStep{Op: "sleep", Ms: 50, When: "label==active-log"},
		runnerStep{Op: "stdout", Body: "line 2", When: "label==active-log"},
		runnerStep{Op: "write", Path: "{root}/line2", When: "label==active-log"},
		publishCard("{job}"),
	)
	sampler := &fakeTreeSampler{}
	sampler.cpuForCard = func(cardIndex, pid int) (uint64, bool) {
		return 0, false // TreeCPU returns ok=false (unknown/unsupported)
	}
	clk := newManualClock()
	code, out, errs := runBatchClock(BatchInput{
		ID: "B-UNK", Deadline: 30 * time.Second, Idle: testIdleBudget, Cards: tsv, Root: root, Runner: runner,
		snapshot: func() activitySnapshot { return sampler },
	}, clk, func() {
		waitForFile(t, filepath.Join(root, "line1"))
		clk.waitTick()
		clk.tick() // round 1: active-log has line 1
		waitForFile(t, filepath.Join(root, "line2"))
		clk.advance(testIdleBudget)
		clk.tick() // round 2: log grew to line 2, active-log is saved while silent is idle-killed
	})
	if code != 1 {
		t.Fatalf("batch exit = %d, want 1; stderr: %s", code, errs)
	}
	if !strings.Contains(out, "active-log slot=1: finished via log") {
		t.Fatalf("card with growing log survives unknown CPU sample:\n%s", out)
	}
	if !strings.Contains(out, "silent slot=2: "+idleReason) {
		t.Fatalf("card with silent log and unknown CPU sample is idle-killed:\n%s", out)
	}
}

// ISSUE #594: A RESULT.MD WRITTEN INSIDE repo/ IS THE CARD'S RESULT, NOT A MISSING ONE.
// A model's cwd after STEP 1 is the clone, so it publishes RESULT.md there; gather scored
// the card reason=no-result and the work was lost. Gather reads the job root first, else
// repo/RESULT.md or one directory down, copies it up to the job root and says so once on
// stderr. The RESULT contract is unchanged: line 1 is still the card's contract line.
func TestGatherCopiesResultUpFromRepo(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	tsv := writeCards(t, dir, [][2]string{
		{"a", "RESULT: a\nall green from the clone"},
	})
	runner := runnerDoing(t, dir, "runner-in-repo",
		runnerStep{Op: "mkdir", Path: "{job}/repo"},
		publishCard("{job}/repo"),
	)
	code, out, errs := runBatch(t, tsv, root, runner, 10*time.Second)
	if code != 0 {
		t.Fatalf("a card that published inside repo/ is done, got exit %d; stderr: %s", code, errs)
	}
	if !strings.Contains(out, "a slot=1: all green from the clone") {
		t.Fatalf("the result written inside repo/ is folded, line 2 verbatim:\n%s", out)
	}
	job := filepath.Join(resolvedPath(t, root), "1", "jobs", "a")
	if !strings.Contains(errs, "BATCH NOTE a RESULT.md copied up from "+filepath.Join(job, "repo", "RESULT.md")) {
		t.Fatalf("the copy up is said once on stderr, naming where it came from:\n%s", errs)
	}
	if got := readTestFile(t, filepath.Join(job, "RESULT.md")); got != "RESULT: a\nall green from the clone\n" {
		t.Fatalf("the result is copied to the job root byte for byte, got %q", got)
	}
}

// TestBatchScoresHarnessSilent: a card whose runner said the harness wrote nothing at all --
// `harness=silent` on its own `NATIVE OK` line -- is `ABSTAIN reason=harness-silent`, never
// `no-result` (issue #591). The difference is the whole point of the token: `no-result` is a
// harness that ran and published nothing, which is the model's own doing; `harness-silent` is
// a harness that never ran the card, which is the machinery's, and the two remedies are not
// the same: a harness that SPOKE and published nothing is `harness=ok` on its own line and
// scores `no-result` (card b, 37 bytes in its capture), and a runner that says nothing about
// its harness at all still scores `no-result` (card d), so the pinned semantics of a silent
// RUNNER (as against a silent harness) are untouched.
//
// AND IT COMES BEFORE `rc=<n>`: a harness that never ran the card has an exit code that is
// nothing to go and read -- 0 in the fault that wrote the rule, and any number in the next
// one. The exit code is still on the card's own NATIVE OK line for whoever wants it.
func TestBatchScoresHarnessSilent(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	tsv := writeCards(t, dir, [][2]string{
		{"a", "RESULT: a\nMISSING"},
		{"b", "RESULT: b\nMISSING"},
		{"c", "RESULT: c\nMISSING"},
		{"d", "RESULT: d\nMISSING"},
	})
	var runner string
	// The runner is the native command's stand-in: its stdout is the job's harness.log, and
	// the NATIVE OK line it prints there is where the gather reads the harness's state --
	// the token `native` decided by looking at its own capture, never a file this batch
	// wrote. Card a's harness was silent and the runner exited 0; card b's harness SPOKE
	// (37 bytes of the child's words in the capture) and published nothing; card c's harness
	// was silent and the runner then exited 3; card d's runner prints no NATIVE OK line at
	// all, the way any runner but `native` behaves.
	const nativeOK = "NATIVE OK label={label} job={job} rc=0 wall=0.42s sandbox=none-by-flag " +
		"card_sha256=- binary_sha256=- config=- harness="
	runner = runnerDoing(t, dir, "silent-harness",
		runnerStep{Op: "mkdir", Path: "{job}"},
		// Card b's harness SPOKE: 37 bytes of the child's words in the capture.
		runnerStep{Op: "write", Path: "{job}/harness-output.log", When: "label==b",
			Body: "fake harness: twenty-two-characters!\n"},
		runnerStep{Op: "stdout", Body: nativeOK + "ok", When: "label==b"},
		runnerStep{Op: "stdout", Body: nativeOK + "silent", When: "label==a"},
		runnerStep{Op: "stdout", Body: nativeOK + "silent", When: "label==c"},
		// Card d's runner prints no NATIVE OK line at all, the way any runner but `native` behaves.
		runnerStep{Op: "exit", N: 3, When: "label==c"},
		runnerStep{Op: "exit", N: 0},
	)
	code, out, _ := runBatch(t, tsv, root, runner, 10*time.Second)
	if code != 1 {
		t.Fatalf("four abstaining cards exit 1, got %d:\n%s", code, out)
	}
	if !strings.Contains(out, "a slot=1: ABSTAIN reason=harness-silent log=1 job="+filepath.Join(resolvedPath(t, root), "1", "jobs", "a")) {
		t.Fatalf("a silent harness is reason=harness-silent and names the job:\n%s", out)
	}
	if strings.Contains(out, "a slot=1: ABSTAIN reason=no-result") {
		t.Fatalf("a silent harness is never no-result: it is no run at all:\n%s", out)
	}
	if !strings.Contains(out, "b slot=2: ABSTAIN reason=no-result") || strings.Contains(out, "b slot=2: ABSTAIN reason=harness-silent") {
		t.Fatalf("a harness that SPOKE and published nothing scores no-result, not harness-silent:\n%s", out)
	}
	if got := readTestFile(t, filepath.Join(root, "2", "jobs", "b", "harness-output.log")); len(got) != 37 {
		t.Fatalf("the spoken card's capture holds %d bytes, want the 37 the harness said: %q", len(got), got)
	}
	if !strings.Contains(out, "d slot=4: ABSTAIN reason=no-result log=0") {
		t.Fatalf("a card whose runner said nothing about its harness still scores no-result:\n%s", out)
	}
	if !strings.Contains(out, "c slot=3: ABSTAIN reason=harness-silent log=1") || strings.Contains(out, "reason=rc=3") {
		t.Fatalf("a silent harness is named before the exit code of the run that never happened:\n%s", out)
	}
}

// TestBatchLogAppendsNeverTruncates: the batch pins its runner's stdout to <job>/harness.log
// with O_APPEND, so bytes already in that file survive the run and the runner's own lines
// land after them, in order. Two processes write a card's harness log -- the batch's runner
// here, and the supervisor the runner starts, which pins the harness's own output to the
// same path -- and each holds its own offset. Opened O_TRUNC, this descriptor starts at
// offset 0 and the runner's first line overwrites the head of what the other writer already
// put there: the start of a card's evidence was destroyed by the line announcing the run
// (issue #608). The test writes a line, runs the batch, and demands BOTH, in that order.
func TestBatchLogAppendsNeverTruncates(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	cards := writeCards(t, dir, [][2]string{{"c1", "the item\nDONE\n"}})

	// The bytes another writer put there before the batch opened the file.
	job := filepath.Join(root, "1", "jobs", "c1")
	if err := os.MkdirAll(job, 0o755); err != nil {
		t.Fatal(err)
	}
	const earlier = "the harness said this before the runner started"
	if err := os.WriteFile(filepath.Join(job, "harness.log"), []byte(earlier+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A runner that publishes the card and says one line of its own on stdout.
	const later = "RUNNER SAID THIS"
	runner := runnerDoing(t, dir, "runner-say",
		runnerStep{Op: "mkdir", Path: "{job}"},
		publishCard("{job}"),
		runnerStep{Op: "stdout", Body: later},
	)

	if code, out, errb := runBatch(t, cards, root, runner, 30*time.Second); code != 0 {
		t.Fatalf("the batch exits 0, got %d:\n%s\n%s", code, out, errb)
	}

	raw, err := os.ReadFile(filepath.Join(job, "harness.log"))
	if err != nil {
		t.Fatalf("the card's harness log could not be read: %v", err)
	}
	got := string(raw)
	at, after := strings.Index(got, earlier), strings.Index(got, later)
	if at < 0 {
		t.Errorf("the batch truncated the card's harness log: the bytes written before the run are gone:\n%s", got)
	}
	if after < 0 {
		t.Fatalf("the runner's own line is not in the card's harness log:\n%s", got)
	}
	if at >= 0 && after < at {
		t.Errorf("the runner's line landed before the bytes that were there first; the log is out of order:\n%s", got)
	}
}
