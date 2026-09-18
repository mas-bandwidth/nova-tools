package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
)

// The fixtures are COPIES of the real files this lane works on: the work set of
// 2026-09-18 (~/rowan-working/work/pitstop-2026-09-18-units.lisp) and the fleet's
// own lanes table. A verb tested against a fixture shaped to suit it has been
// tested against nothing.
const (
	realSetFixture = "testdata/pitstop-2026-09-18-units.lisp"
	lanesFixture   = "testdata/lanes.tsv"
)

// workSet copies the fixture into the test's own directory, because every verb
// under test EDITS the file it is given.
func workSet(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(realSetFixture)
	if err != nil {
		t.Fatalf("read %s: %v", realSetFixture, err)
	}
	path := filepath.Join(t.TempDir(), "units.lisp")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

// fakeRouter replaces the seam onto nova-decide for the length of one test. No
// unit test dials the provider, needs a key on disk, or is coupled to the
// ladder's arithmetic: what `next` owes is the four gates and the one line.
func fakeRouter(t *testing.T, answer func(decide.Unit) (decide.RouteResult, error)) {
	t.Helper()
	was := router
	router = func(_ context.Context, _ *decide.Registry, u decide.Unit, floor float64, _ bool, _, _ string) (decide.RouteResult, error) {
		res, err := answer(u)
		res.Unit, res.Floor = u.ID, floor
		return res, err
	}
	t.Cleanup(func() { router = was })
}

// rung is a routed answer at one confidence: the ladder's own shape, minus the
// ladder.
func rung(name string, conf float64) decide.RouteResult {
	return decide.RouteResult{
		Rung: decide.Mind{Name: name, Ask: "child"}, Confidence: conf,
		Wait: decide.WaitNone, Reason: "the fake ladder",
	}
}

func nextLine(t *testing.T, args ...string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if code := run(append([]string{"next"}, args...), &stdout, &stderr); code != 0 {
		t.Fatalf("next exit = %d, stderr = %s", code, stderr.String())
	}
	line := strings.TrimSpace(stdout.String())
	if strings.Count(line, "\n") != 0 {
		t.Fatalf("next printed more than one line:\n%s", line)
	}
	return line
}

// The verb's headline: one mind, one file, ONE unit. The two answers pinned
// here are the real ones for the real set -- the coordinator's child gets the
// first ready unit of a free lane, and Stella gets her own.
func TestNextAnswersOneUnitForOneMind(t *testing.T) {
	fakeRouter(t, func(u decide.Unit) (decide.RouteResult, error) { return rung("opus", 0.90), nil })
	path := workSet(t)
	for mind, want := range map[string]string{
		"rowan-child": "NEXT unit=certify:verb lane=pulse rung=opus conf=0.90 take=- reason=\"the fake ladder\"",
		"Stella":      "NEXT unit=lisp:collision lane=work rung=opus conf=0.90 take=- reason=\"the fake ladder\"",
	} {
		got := nextLine(t, "--file", path, "--for", mind, "--lanes", lanesFixture, "--no-jev")
		if got != want {
			t.Errorf("next --for %s\n got %s\nwant %s", mind, got, want)
		}
	}
}

// A13: a friend's unit is never handed to a child, and a child's is never
// handed to a friend. The machinery routes to friends.
func TestNextNeverHandsAFriendsUnitToAnotherMind(t *testing.T) {
	fakeRouter(t, func(u decide.Unit) (decide.RouteResult, error) { return rung("opus", 0.90), nil })
	path := workSet(t)
	for _, mind := range []string{"rowan-child", "Stella"} {
		line := nextLine(t, "--file", path, "--for", mind, "--lanes", lanesFixture, "--no-jev")
		// wall:toolchain-roots is Johnny's, and it is ready and in a free lane.
		if strings.Contains(line, "wall:toolchain-roots") {
			t.Errorf("next --for %s offered Johnny's unit: %s", mind, line)
		}
	}
	line := nextLine(t, "--file", path, "--for", "Johnny", "--lanes", lanesFixture, "--no-jev")
	if !strings.Contains(line, "unit=wall:toolchain-roots") {
		t.Errorf("next --for Johnny did not offer his own unit: %s", line)
	}
}

// A6 and A4 together: taking a unit charges its lane, and the lane is not free
// again while the unit is live. The clock does not free it; only an outcome
// does. This is the bug the verb exists to prevent -- two cards in one lane.
func TestTakingAUnitHoldsItsLaneUntilTheAttemptIsRecorded(t *testing.T) {
	fakeRouter(t, func(u decide.Unit) (decide.RouteResult, error) { return rung("opus", 0.90), nil })
	path := workSet(t)
	taken := nextLine(t, "--file", path, "--for", "rowan-child", "--lanes", lanesFixture,
		"--no-jev", "--take", "--started", "2026-09-18T12:00:00Z")
	if !strings.Contains(taken, "unit=certify:verb") || !strings.Contains(taken, "take=1") {
		t.Fatalf("take did not open attempt 1 on certify:verb: %s", taken)
	}
	again := nextLine(t, "--file", path, "--for", "rowan-child", "--lanes", lanesFixture, "--no-jev")
	if strings.Contains(again, "lane=pulse") {
		t.Errorf("the pulse lane was offered twice; a lane is capacity 1 (A6): %s", again)
	}
	if !strings.Contains(again, "unit=darwin:measured-shards") {
		t.Errorf("the next answer is not the next free lane: %s", again)
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"attempt", "record", "--file", path, "--unit", "certify:verb",
		"--by", "rowan-child", "--outcome", "ok", "--proof", "8a132e77", "--pr", "1369"}, &stdout, &stderr); code != 0 {
		t.Fatalf("attempt record exit = %d, stderr = %s", code, stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); !strings.Contains(got, "ATTEMPT closed") ||
		!strings.Contains(got, "n=1") || !strings.Contains(got, "state=closed") ||
		!strings.Contains(got, "started=2026-09-18T12:00:00Z") {
		t.Errorf("record did not close the attempt take opened: %s", got)
	}
	// The lane is free again, and the unit that held it is done rather than
	// offered a second time.
	freed := nextLine(t, "--file", path, "--for", "rowan-child", "--lanes", lanesFixture, "--no-jev")
	if strings.Contains(freed, "unit=certify:verb") {
		t.Errorf("a closed unit was offered again: %s", freed)
	}
	// And closing it opened its dependant: certify:fix-loop needs certify:verb,
	// so the same answer carries the graph's half of the question too.
	if !strings.Contains(freed, "unit=certify:fix-loop") || !strings.Contains(freed, "lane=pulse") {
		t.Errorf("the freed pulse lane was not offered to the unit its close unblocked: %s", freed)
	}
}

// Stella's lease rule through the seam: a route that is not dispatchable is a
// unit the verb does not hand out, whatever its lane says. A rung that may
// still be running is not a rung to step off.
func TestNextNeverDispatchesAUnitTheLadderIsWaitingOn(t *testing.T) {
	fakeRouter(t, func(u decide.Unit) (decide.RouteResult, error) {
		res := rung("opus", 0.90)
		if u.ID == "certify:verb" {
			res.Wait = decide.WaitAwaitingTermination
			res.Reason = "attempt 1 on opus timed out and is not known to have terminated"
		}
		return res, nil
	})
	line := nextLine(t, "--file", workSet(t), "--for", "rowan-child", "--lanes", lanesFixture, "--no-jev")
	if strings.Contains(line, "certify:verb") {
		t.Errorf("a unit awaiting termination was dispatched: %s", line)
	}
}

// NEXT NONE is never "nothing to do": it names the gate that emptied the set,
// so the mind reading it knows whether to wait, to ask, or to look at a lane
// somebody else is holding.
func TestNextNoneNamesTheGateThatEmptiedTheSet(t *testing.T) {
	fakeRouter(t, func(u decide.Unit) (decide.RouteResult, error) { return rung("opus", 0.90), nil })
	path := workSet(t)
	line := nextLine(t, "--file", path, "--for", "Emma", "--lanes", lanesFixture, "--no-jev")
	if !strings.HasPrefix(line, "NEXT NONE reason=") {
		t.Fatalf("a mind who owns nothing got an answer: %s", line)
	}
	if !strings.Contains(line, "ready") || !strings.Contains(line, "Emma") {
		t.Errorf("the reason names neither the count nor the mind: %s", line)
	}
}

// Accounting is not optional: a jev call that nobody can account for is refused
// before it is made, exactly as nova-decide route refuses it.
func TestNextRefusesAJevCallItCannotAccountFor(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"next", "--file", workSet(t), "--for", "rowan-child", "--lanes", lanesFixture}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit = %d, want 2; stdout = %s", code, stdout.String())
	}
	for _, want := range []string{"--usage", "--log", "--no-jev"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("the refusal does not name %s: %s", want, stderr.String())
		}
	}
}

// One writer at a time over one document, and a lock already held is a REFUSAL
// naming the path rather than a wait -- a waiter here would be the barrier A9
// removes.
func TestAWriteVerbRefusesWhenTheSetIsLocked(t *testing.T) {
	path := workSet(t)
	if err := os.Mkdir(path+".lock", 0o755); err != nil {
		t.Fatalf("take the lock: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"attempt", "record", "--file", path, "--unit", "certify:verb",
		"--by", "rowan-child", "--outcome", "uncertain"}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), path+".lock") {
		t.Errorf("the refusal does not name the lock: %s", stderr.String())
	}
}

// A3's refusal, at the CLI: the word the author must write instead is IN the
// message, so nobody has to go and read the spec to get past it.
func TestAttemptRecordRefusesAnOutcomeWithNoProof(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"attempt", "record", "--file", workSet(t), "--unit", "certify:verb",
		"--by", "rowan-child", "--outcome", "failed"}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit = %d, want 2; stdout = %s", code, stdout.String())
	}
	if !strings.Contains(stderr.String(), "uncertain") || !strings.Contains(stderr.String(), "--proof") {
		t.Errorf("the refusal names neither the remedy nor the word: %s", stderr.String())
	}
}

// attempt list is the reader beside the writer, and it reads what the writer
// wrote: two records, in order, with their proofs.
func TestAttemptListReadsBackWhatRecordWrote(t *testing.T) {
	path := workSet(t)
	record := func(args ...string) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		base := []string{"attempt", "record", "--file", path, "--unit", "harvest:bench", "--by", "rowan-child"}
		if code := run(append(base, args...), &stdout, &stderr); code != 0 {
			t.Fatalf("attempt record %v: exit %d, stderr %s", args, code, stderr.String())
		}
	}
	record("--outcome", "failed", "--rung", "flash", "--proof", "exit-1", "--started", "2026-09-18T09:10:00Z")
	record("--outcome", "ok", "--rung", "opus", "--proof",
		"https://forge.invalid/nova-tools/pull/1367", "--started", "2026-09-18T10:40:00Z")

	var stdout, stderr bytes.Buffer
	if code := run([]string{"attempt", "list", "--file", path, "--unit", "harvest:bench"}, &stdout, &stderr); code != 0 {
		t.Fatalf("attempt list exit = %d, stderr = %s", code, stderr.String())
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("attempt list printed %d lines, want 2 records and the OK line:\n%s", len(lines), stdout.String())
	}
	if !strings.Contains(lines[0], "n=1") || !strings.Contains(lines[0], "outcome=red") ||
		!strings.Contains(lines[0], "proof=path:exit-1") {
		t.Errorf("record 1: %s", lines[0])
	}
	if !strings.Contains(lines[1], "n=2") || !strings.Contains(lines[1], "rung=opus") ||
		!strings.Contains(lines[1], "proof=url:https") {
		t.Errorf("record 2: %s", lines[1])
	}
	if !strings.Contains(lines[2], "attempts=2") || !strings.Contains(lines[2], "state=closed") {
		t.Errorf("the OK line: %s", lines[2])
	}
}

// The document is a person's: a verb that edits one unit leaves every other
// byte of it alone. Pinned at the CLI as well as in the reader, because this is
// the property a coordinator trusts when they let a tool near their work set.
func TestRecordLeavesTheRestOfTheDocumentByteIdentical(t *testing.T) {
	path := workSet(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"attempt", "record", "--file", path, "--unit", "air:bud-setup",
		"--by", "rowan-child", "--outcome", "uncertain", "--started", "2026-09-18T12:00:00Z"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// The unit edited is the last but one; everything up to it is untouched.
	cut := bytes.Index(before, []byte(`(unit "air:bud-setup"`))
	if cut < 0 {
		t.Fatal("the fixture no longer holds air:bud-setup")
	}
	if !bytes.Equal(before[:cut], after[:cut]) {
		t.Error("the bytes before the edited unit changed")
	}
	if n := bytes.Count(after, []byte(":attempts (")); n != 1 {
		t.Errorf("the edit wrote %d :attempts keys, want 1", n)
	}
	if _, err := os.Stat(path + ".lock"); !os.IsNotExist(err) {
		t.Error("the lock was not released")
	}
	if _, err := os.Stat(path + ".nova-work.tmp"); !os.IsNotExist(err) {
		t.Error("the temporary file was left behind")
	}
}
