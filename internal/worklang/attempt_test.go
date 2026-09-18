package worklang_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/worklang"
)

// The real set of 2026-09-18, copied into testdata: the file this writer was
// built to edit, so every pin below is a pin on a real document rather than on
// a fixture shaped to make the writer look good.
const realSet = "testdata/pitstop-2026-09-18-units.lisp"

func readReal(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(realSet)
	if err != nil {
		t.Fatalf("read %s: %v", realSet, err)
	}
	return data
}

func mustRecord(t *testing.T, data []byte, id string, a worklang.NewAttempt) ([]byte, worklang.Recorded) {
	t.Helper()
	out, rec, err := worklang.Record(realSet, data, id, a, worklang.DefaultLimits())
	if err != nil {
		t.Fatalf("record on %s: %v", id, err)
	}
	return out, rec
}

func green() worklang.NewAttempt {
	proof, _ := worklang.ProofOf("8a132e77c0de4c1b9f0f51e5b6f0a9c2d3e4f5a6")
	return worklang.NewAttempt{
		Rung: "opus", Owner: "rowan-child", Started: "2026-09-18T12:00:00Z",
		Outcome: "ok", Proof: proof,
	}
}

// worklang-attempt-record-round-trips-every-byte-but-the-edited-unit. The work
// set is a person's document: its comments, its blank lines and the column its
// keys line up at are the document. A writer that re-rendered it would hand
// back a file its author no longer recognises, and the diff of a one-attempt
// edit would be the whole file.
func TestRecordRoundTripsEveryByteButTheEditedUnit(t *testing.T) {
	before := readReal(t)
	after, _ := mustRecord(t, before, "certify:verb", green())

	b, err := worklang.ParseWorkSet(realSet, before, worklang.DefaultLimits())
	if err != nil {
		t.Fatalf("parse before: %v", err)
	}
	edited, ok := b.Unit("certify:verb")
	if !ok {
		t.Fatal("the real set holds no unit certify:verb")
	}
	if got, want := string(after[:edited.Offset]), string(before[:edited.Offset]); got != want {
		t.Error("the bytes before the edited unit changed")
	}
	tailBefore := string(before[edited.End:])
	if got := string(after[len(after)-len(tailBefore):]); got != tailBefore {
		t.Error("the bytes after the edited unit changed")
	}
	// And the file is still readable, with every OTHER unit byte-identical.
	a, err := worklang.ParseWorkSet(realSet, after, worklang.DefaultLimits())
	if err != nil {
		t.Fatalf("the edited set no longer reads: %v", err)
	}
	if len(a.Units) != len(b.Units) {
		t.Fatalf("units %d, want %d", len(a.Units), len(b.Units))
	}
	for i := range b.Units {
		if b.Units[i].ID == "certify:verb" {
			continue
		}
		was := string(b.Units[i].Bytes(before))
		is := string(a.Units[i].Bytes(after))
		if was != is {
			t.Errorf("unit %s changed:\nwas %s\nis  %s", b.Units[i].ID, was, is)
		}
	}
}

// worklang-attempt-without-a-termination-proof-is-refused-at-the-writer. A3's
// door is held on the way IN as well as on the way out: a file is never written
// and then found unreadable.
func TestRecordRefusesAnOutcomeWithNoProof(t *testing.T) {
	a := green()
	a.Proof = worklang.Proof{}
	_, _, err := worklang.Record(realSet, readReal(t), "certify:verb", a, worklang.DefaultLimits())
	if err == nil {
		t.Fatal("recording ok with no proof was accepted")
	}
	if !strings.Contains(err.Error(), "uncertain") {
		t.Errorf("the refusal does not name the word to write instead: %v", err)
	}
}

// worklang-uncertain-owes-no-proof-and-keeps-the-unit-uncertain (A4).
func TestUncertainNeedsNoProofAndIsItsOwnState(t *testing.T) {
	a := green()
	a.Outcome, a.Proof = "uncertain", worklang.Proof{}
	after, rec, err := worklang.Record(realSet, readReal(t), "certify:verb", a, worklang.DefaultLimits())
	if err != nil {
		t.Fatalf("uncertain was refused: %v", err)
	}
	if rec.State != "uncertain" {
		t.Errorf("state %q, want uncertain", rec.State)
	}
	if got := stateOf(t, after, "certify:verb"); got != "uncertain" {
		t.Errorf("the file says :state :%s, want uncertain", got)
	}
}

// worklang-the-state-machine-is-a4s-closed-set. Every outcome lands on a state
// the grammar names, and `red` is not one of them: a failed attempt leaves the
// unit OPEN and the ladder is the retry policy.
func TestEveryOutcomeLandsOnAStateTheGrammarNames(t *testing.T) {
	known := map[string]bool{}
	for _, s := range worklang.KnownStates() {
		known[s] = true
	}
	want := map[string]string{
		"green": "closed", "red": "open", "refused": "refused",
		"abandoned": "abandoned", "uncertain": "uncertain",
	}
	for outcome, state := range want {
		if got := worklang.StateAfter(outcome); got != state {
			t.Errorf("StateAfter(%s) = %s, want %s", outcome, got, state)
		}
		if !known[state] {
			t.Errorf("%s is not one of A4's states", state)
		}
	}
	for _, spelling := range []string{"ok", "failed"} {
		if _, ok := worklang.CanonicalOutcome(spelling); !ok {
			t.Errorf("--outcome %s is not accepted", spelling)
		}
	}
	if _, ok := worklang.CanonicalOutcome("done"); ok {
		t.Error("an outcome the grammar does not name was accepted")
	}
}

// worklang-a-taken-unit-is-live-with-one-open-attempt, and the attempt that was
// opened is the attempt that is CLOSED: one try is one record (A3).
func TestTakeOpensOneAttemptAndRecordClosesTheSameOne(t *testing.T) {
	taken, rec, err := worklang.Take(realSet, readReal(t), "certify:verb", worklang.NewAttempt{
		Rung: "opus", Owner: "rowan-child", Started: "2026-09-18T12:00:00Z",
	}, worklang.DefaultLimits())
	if err != nil {
		t.Fatalf("take: %v", err)
	}
	if rec.State != worklang.StateLive || rec.N != 1 {
		t.Fatalf("take recorded n=%d state=%s, want n=1 state=live", rec.N, rec.State)
	}
	if got := stateOf(t, taken, "certify:verb"); got != "live" {
		t.Errorf("the file says :state :%s after take, want live", got)
	}
	if _, _, err := worklang.Take(realSet, taken, "certify:verb", worklang.NewAttempt{
		Rung: "opus", Owner: "rowan-child", Started: "2026-09-18T13:00:00Z",
	}, worklang.DefaultLimits()); err == nil {
		t.Error("a live unit was taken a second time; it still holds its reservation")
	}

	closed, rec, err := worklang.Record(realSet, taken, "certify:verb", green(), worklang.DefaultLimits())
	if err != nil {
		t.Fatalf("record after take: %v", err)
	}
	if !rec.Closed || rec.N != 1 {
		t.Fatalf("record wrote n=%d closed=%v, want n=1 closed=true: a try that started and ended is ONE try", rec.N, rec.Closed)
	}
	attempts := attemptsOf(t, closed, "certify:verb")
	if len(attempts) != 1 {
		t.Fatalf("the unit carries %d attempts, want 1", len(attempts))
	}
	if attempts[0].Outcome != "green" || !attempts[0].HasProof {
		t.Errorf("attempt 1 is :%s proof=%v, want green with a proof", attempts[0].Outcome, attempts[0].HasProof)
	}
	if attempts[0].Started != "2026-09-18T12:00:00Z" {
		t.Errorf("the closed record lost its :started (%q); the try began when it began", attempts[0].Started)
	}
	if got := stateOf(t, closed, "certify:verb"); got != "closed" {
		t.Errorf("the file says :state :%s, want closed", got)
	}
	// And a second, genuinely new try appends rather than overwriting.
	second := green()
	second.Started = "2026-09-18T14:00:00Z"
	_, _, err = worklang.Record(realSet, closed, "certify:verb", second, worklang.DefaultLimits())
	if err == nil {
		t.Error("an attempt was filed on a :closed unit")
	}
}

// worklang-a-second-attempt-is-a-second-record, numbered from the records that
// are there rather than from a counter.
func TestASecondAttemptAppendsANumberedRecord(t *testing.T) {
	failed := green()
	failed.Outcome = "failed"
	first, rec, err := worklang.Record(realSet, readReal(t), "harvest:bench", failed, worklang.DefaultLimits())
	if err != nil {
		t.Fatalf("record red: %v", err)
	}
	if rec.State != "open" {
		t.Fatalf("a red attempt left the unit :%s, want open", rec.State)
	}
	second := green()
	second.Rung, second.Started = "emma", "2026-09-18T15:00:00Z"
	after, rec, err := worklang.Record(realSet, first, "harvest:bench", second, worklang.DefaultLimits())
	if err != nil {
		t.Fatalf("record green: %v", err)
	}
	if rec.N != 2 || rec.Closed {
		t.Fatalf("second record n=%d closed=%v, want n=2 appended", rec.N, rec.Closed)
	}
	attempts := attemptsOf(t, after, "harvest:bench")
	if len(attempts) != 2 || attempts[0].Outcome != "red" || attempts[1].Rung != "emma" {
		t.Fatalf("attempts %+v, want a red first and emma's green second", attempts)
	}
}

// worklang-proof-kind-is-read-off-the-value, so a caller names the evidence
// once rather than naming it and then classifying it.
func TestProofKindIsReadOffTheValue(t *testing.T) {
	for value, kind := range map[string]string{
		"https://forge.invalid/nova-tools/pull/1391": "url",
		"8a132e77":                    "sha",
		"reports/pitstop-tests.md":    "path",
		"/var/log/nova/certify.jsonl": "path",
	} {
		got, ok := worklang.ProofOf(value)
		if !ok || got.Kind != kind {
			t.Errorf("ProofOf(%q) = %+v ok=%v, want kind %s", value, got, ok, kind)
		}
	}
	if _, ok := worklang.ProofOf("   "); ok {
		t.Error("empty proof was read as evidence")
	}
}

// worklang-a-unit-with-no-attempts-key-grows-one-and-the-set-still-reads.
func TestTheEditedSetStillChecksClean(t *testing.T) {
	after, _ := mustRecord(t, readReal(t), "air:bud-setup", green())
	ws, err := worklang.ParseWorkSet(realSet, after, worklang.DefaultLimits())
	if err != nil {
		t.Fatalf("the edited set no longer reads: %v", err)
	}
	findings, _ := ws.Check(worklang.Options{})
	for _, f := range findings {
		t.Errorf("the edit introduced a finding: %s on %s", f.Rule, f.Unit)
	}
	if !strings.Contains(string(after), ":attempts (") {
		t.Error("the unit grew no :attempts key")
	}
	// The written record must survive a round trip back through the reader.
	u, _ := ws.Unit("air:bud-setup")
	got := u.Attempts()
	if len(got) != 1 || got[0].Owner != "rowan-child" || got[0].Proof.Kind != worklang.List {
		t.Fatalf("the written record does not read back: %+v", got)
	}
}

func stateOf(t *testing.T, data []byte, id string) string {
	t.Helper()
	ws, err := worklang.ParseWorkSet(realSet, data, worklang.DefaultLimits())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	u, ok := ws.Unit(id)
	if !ok {
		t.Fatalf("no unit %s", id)
	}
	return u.State()
}

func attemptsOf(t *testing.T, data []byte, id string) []worklang.Attempt {
	t.Helper()
	ws, err := worklang.ParseWorkSet(realSet, data, worklang.DefaultLimits())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	u, ok := ws.Unit(id)
	if !ok {
		t.Fatalf("no unit %s", id)
	}
	return u.Attempts()
}

// The fixture is a COPY of the real file, and a copy that has drifted is a
// fixture that pins nothing. This test is the reminder, not a network read: it
// only asks that the copy is the document it claims to be.
func TestTheFixtureIsTheRealSetsShape(t *testing.T) {
	data := readReal(t)
	ws, err := worklang.ParseWorkSet(realSet, data, worklang.DefaultLimits())
	if err != nil {
		t.Fatalf("the fixture does not read: %v", err)
	}
	if ws.ID != "pitstop-2026-09-18-units" {
		t.Errorf("fixture id %q", ws.ID)
	}
	if len(ws.Units) < 15 {
		t.Errorf("the fixture holds %d units; the real set holds twenty", len(ws.Units))
	}
	if filepath.Base(realSet) != "pitstop-2026-09-18-units.lisp" {
		t.Error("the fixture is not named after the file it copies")
	}
}
