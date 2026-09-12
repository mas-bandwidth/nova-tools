package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The runtime witness for findings F1 and F2 of audit packet 1 (#164), against
// THE REAL nova-bus on a bare fixture bus with two clones -- the same fixture
// the advancing tests use, for the reason stated there: the recovery is a
// property of what nova-bus LISTS, and arithmetic over the source is not a
// witness of it.
//
// F2, the completeness measure. The recovery compared `carrying=<n>` -- which
// SPEC.md defines as "the size of the OPEN LIST -- every entry on it, the heard
// and the unreadable included" -- against the greater of two PARTIAL counts:
// the items the classifier relayed (which drops a note this tool has already
// printed, and drops a line it has seen standing) and the INBOX NOTE lines
// (which drops every receipt, heard and unreadable entry). With one already
// printed note AND one receipt on the carried list, both partial counts are
// short of `carrying=`, so the call could never call the recovery complete and
// the marker was kept for ever.
//
// F1, the marker. `Recover` keeps the marker on that path and says so, and the
// spec's step 4 says "the marker stays, NOTHING ADVANCES" -- but the advance
// gated only on an empty bus queue, so the next bus poll of the same call wrote
// a fresh `bus:advance` over the unresolved one and moved the cursor. The notes
// the recovery could not reach were then behind the cursor with nothing naming
// them: the residual the marker exists to close, re-opened.
//
// The two are one witness because F2 is how an ordinary reader REACHES the
// state F1 then loses.

// inflightMarker is the kill of The races, written into the state by hand: a
// test cannot SIGKILL a function it is calling, and this is the state such a
// kill leaves -- the marker, and no record of what the advance listed.
const inflightMarker = "bus:advance|inflight%7C2026-09-11T12:00:00Z%7C-\n"

// appendLine adds one line to a state file this tool wrote itself, so that the
// marker sits beside the printed marks of a reader that has been running. A
// fresh state file with nothing but the marker is the case the advancing tests
// already cover; the case here is a reader with a history.
func appendLine(t *testing.T, path, line string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(line); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// carriedPast puts the cursor past everything on the checkout by hand, which is
// where a kill in the advance's gap leaves it: carried, unprinted, and on no
// listing a plain inbox makes. Two advances, because one moves the cursor to
// the head it read at its start and the notes its own push fetched are still
// listed by the next plain inbox (the two-poll property).
func carriedPast(t *testing.T, rowan string) string {
	t.Helper()
	for i := 0; i < 2; i++ {
		runBus(t, "inbox", "--bus", rowan, "--as", "Rowan", "--receipt-max-words", "40",
			"--advance", "--remote", "origin", "--branch", "main")
	}
	return runBus(t, "inbox", "--bus", rowan, "--as", "Rowan", "--receipt-max-words", "40")
}

// F2: a carried list that mixes an already printed note, an unprinted note and a
// bare receipt is exactly what a reader who has been shown notes it has not
// answered and has receipted anything carries. Every entry on it is one the
// recovery reached, so the recovery is COMPLETE, the marker is cleared, and the
// count the call displays is the count the bus carries.
func TestTheRecoveryCountsEveryCarriedEntryAndNotAMaxOfPartialSums(t *testing.T) {
	rowan, stella := synthBus(t)
	push(t, stella, "stella-ffffffffff01", "the printed note")
	gitAt(t, rowan, "pull", "-q", "--ff-only")
	state := filepath.Join(t.TempDir(), "wake.state")

	// One note, printed and marked printed: S, the half the INBOX NOTE count
	// sees and the classifier does not.
	first := wakeRun(t, advanceArgs(state, rowan, "--max-lines", "0")...)
	if first.exit != 0 {
		t.Fatalf("exit = %d; %s", first.exit, first.all())
	}
	if !strings.Contains(first.stdout, "WAKE BUS id=stella-ffffffffff01") {
		t.Fatalf("the note the checkout held was not relayed:\n%s", first.all())
	}

	// An unprinted note (U, the half both counts see) and a bare receipt (O,
	// the half only the classifier sees), neither ever shown to this tool, with
	// the cursor put past both.
	push(t, stella, "stella-ffffffffff02", "the unprinted note")
	pushKind(t, stella, "stella-ffffffffff03", "heard, thank you", "receipt")
	plain := carriedPast(t, rowan)
	if !strings.Contains(plain, "carrying=3") {
		t.Fatalf("the fixture does not carry the printed note, the unprinted one and the receipt:\n%s", plain)
	}
	if strings.Contains(plain, "INBOX NOTE id=stella-ffffffffff02") {
		t.Fatalf("the cursor is not past the unprinted note, so there is nothing only the recovery can reach:\n%s", plain)
	}
	appendLine(t, state, inflightMarker)

	r := wakeRun(t, advanceArgs(state, rowan, "--max-lines", "0")...)
	if strings.Contains(r.stdout, "bus recovery incomplete") {
		t.Errorf("the recovery reached every carried entry and called itself incomplete; carrying= counts the notes, the bare receipts, the heard and the unreadable, and the measure must count all of them:\n%s", r.all())
	}
	if want := "WAKE NOTE bus advance was interrupted; recovered 3 notes from OPEN"; !strings.Contains(r.stdout, want) {
		t.Errorf("the call does not display the count the bus carries (want %q):\n%s", want, r.all())
	}
	// Visibility, undiminished: the unprinted note reaches the window, the
	// receipt entry reaches it as a line, and the note this tool has already
	// printed is not printed a second time.
	if n := countLines(r.stdout, "WAKE BUS id=stella-ffffffffff02"); n != 1 {
		t.Errorf("the unprinted carried note was printed %d times, want once:\n%s", n, r.all())
	}
	if !strings.Contains(r.stdout, "stella-ffffffffff03") {
		t.Errorf("the carried receipt was not shown to the window at all:\n%s", r.all())
	}
	if n := countLines(r.stdout, "WAKE BUS id=stella-ffffffffff01"); n != 0 {
		t.Errorf("a note this tool had already printed was printed again %d times:\n%s", n, r.all())
	}
	if strings.Contains(read(t, state), "inflight") {
		t.Errorf("the marker was not cleared by a complete recovery:\n%s", read(t, state))
	}
	if r.exit != 0 {
		t.Errorf("exit = %d; %s", r.exit, r.all())
	}
}

// F1: an incomplete recovery holds the cursor. The incompleteness here is the
// one step 4 names -- "the checkout moved between the two reads" -- injected by
// dropping one entry from the reader's own OPEN list before each `--open` read,
// so it does not depend on the arithmetic F2 repairs and the assertions below
// cannot go vacuous when that repair lands.
func TestAnUnresolvedRecoveryHoldsTheCursorAndKeepsItsMarker(t *testing.T) {
	rowan, stella := synthBus(t)
	for i := 0; i < 3; i++ {
		push(t, stella, fmt.Sprintf("stella-99999999990%d", i), fmt.Sprintf("carried %d", i))
	}
	gitAt(t, rowan, "pull", "-q", "--ff-only")
	state := filepath.Join(t.TempDir(), "wake.state")

	// Every carried note is one this tool has printed, so the recovery has no
	// news to end the call with and the call runs to its deadline -- which is
	// what gives the advance a second bus poll to run in, and is the shape the
	// residual actually has: a reader that has been shown its mail and was
	// killed mid-advance.
	first := wakeRun(t, advanceArgs(state, rowan, "--max-lines", "0")...)
	if first.exit != 0 {
		t.Fatalf("exit = %d; %s", first.exit, first.all())
	}
	if n := countLines(first.stdout, "WAKE BUS id=stella-9999999999"); n != 3 {
		t.Fatalf("the first call printed %d of the three notes:\n%s", n, first.all())
	}
	plain := carriedPast(t, rowan)
	if !strings.Contains(plain, "carrying=3") {
		t.Fatalf("the fixture does not carry the three printed notes:\n%s", plain)
	}
	appendLine(t, state, inflightMarker)

	// The checkout moves between the recovery's two reads: one entry leaves the
	// reader's OPEN list before each `--open`, the way an answer written in
	// another session removes one. The real nova-bus still produces every line.
	t.Setenv("NOVA_WAKE_SHRINK_OPEN", filepath.Join(rowan, "from-rowan", "OPEN"))
	head := strings.TrimSpace(gitAt(t, rowan, "rev-parse", "HEAD"))
	before := len(busCalls(t))
	r := wakeRun(t, advanceArgs(state, rowan, "--max-lines", "0")...)
	calls := busCalls(t)[before:]

	// The precondition, asserted rather than assumed: without it every check
	// below passes for the wrong reason.
	if !strings.Contains(r.stdout, "WAKE NOTE bus recovery incomplete") {
		t.Fatalf("the fixture did not leave a recovery unresolved, so this test proves nothing:\n%s", r.all())
	}
	for _, c := range calls {
		if advanced(c) {
			t.Errorf("an advance ran while a recovery was unresolved; step 4 is \"the marker stays, nothing advances\": %q", c)
		}
	}
	if !strings.Contains(read(t, state), "inflight") {
		t.Errorf("the advance wrote over the unresolved recovery's marker, which is the only thing naming what it could not reach:\n%s", read(t, state))
	}
	if after := strings.TrimSpace(gitAt(t, rowan, "rev-parse", "HEAD")); after != head {
		t.Errorf("the cursor moved while a recovery was unresolved")
	}
	// Said once, with the remedy in it, and not once per poll.
	if n := countLines(r.stdout, "WAKE NOTE bus advance deferred: a recovery is unresolved"); n != 1 {
		t.Errorf("the call says the advance is held %d times, want once:\n%s", n, r.stdout)
	}
	// The refusal and counting guarantees are untouched: the call still ends
	// with a verdict, and rule 7's counts still add up over the reads it made.
	if r.exit != 0 {
		t.Errorf("exit = %d; a held advance is not a failed source; %s", r.exit, r.all())
	}
	if !strings.Contains(r.stdout, "WAKE SOURCE bus read=") {
		t.Errorf("the call printed no counts line:\n%s", r.stdout)
	}
}
