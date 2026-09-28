package decide

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/events"
)

// The log has two sinks. The JSONL file is what it has always been; the fleet's
// record is one decide event on cards:done, folded into the decisions table
// (#2623). Everything below runs on a file, the fake sink or the in-memory
// stream: a unit test never opens a socket.

func sampleEntry() Entry {
	in := 937
	return Entry{
		Time:       "2026-09-18T10:00:00Z",
		Unit:       "u1",
		Kind:       KindRebase,
		Evidence:   Unit{ID: "u1", Kind: KindRebase, Files: 2, Packages: 1, Lanes: 1, LaneOwner: "decide"},
		RungTried:  "flash",
		Height:     0,
		Confidence: measured(0.94),
		Floor:      measured(DefaultFloor),
		Source:     SourceRules,
		RowanPick:  "flash",
		Wait:       WaitNone,
		Outcome:    OutcomeOK,
		Calls:      1,
		TokensIn:   &in,
	}
}

// --log is a path. An empty one is a refusal, never a guess at one.
func TestOpenLogSinkIsAPath(t *testing.T) {
	t.Parallel()

	if _, err := OpenLogSink(""); err == nil {
		t.Fatal("an empty --log must refuse rather than guess a path")
	}
	sink, err := OpenLogSink(filepath.Join(t.TempDir(), "decide.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	if _, ok := sink.(*FileSink); !ok {
		t.Fatalf("a path is the JSONL sink, got %T", sink)
	}
}

// The file sink is the log as it has always been: the same rows AppendEntry
// wrote and ReadEntries read.
func TestFileSinkIsTheJSONLLogItAlwaysWas(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "decide.jsonl")
	sink, err := OpenLogSink(path)
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	first := sampleEntry()
	second := sampleEntry()
	second.Unit = "u2"
	for _, e := range []Entry{first, second} {
		if err := sink.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	direct, err := ReadEntries(path)
	if err != nil {
		t.Fatal(err)
	}
	through, err := sink.Entries()
	if err != nil {
		t.Fatal(err)
	}
	if len(direct) != 2 || len(through) != 2 {
		t.Fatalf("two decisions are two rows: direct=%d sink=%d", len(direct), len(through))
	}
	if through[1].Unit != "u2" || through[0].Evidence.Files != 2 {
		t.Errorf("the sink lost the row: %+v", through)
	}
}

// The decide event is the whole decide_log row under decide_log's names: every
// column the calibration set reads survives the mapping.
func TestDecisionEventCarriesTheWholeRow(t *testing.T) {
	t.Parallel()

	e := sampleEntry()
	e.SteppedUp, e.Escalated, e.Designated = true, true, true
	e.Reason = "the lane's owner is up"
	e.Refusal = "no-rung"
	e.RungSucceeded = "pro"
	e.Wait = WaitAwaitingTermination
	e.AwaitingTermination = true
	e.UsageFailed = true
	ev, err := DecisionEvent(e)
	if err != nil {
		t.Fatal(err)
	}
	if err := ev.Validate(); err != nil {
		t.Fatalf("the stream refuses the decision: %v", err)
	}
	if ev.Kind != events.Decide || ev.Label != "u1" || !ev.At.Equal(time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("event=%s label=%s at=%s; want decide, the unit, the row's stamp", ev.Kind, ev.Label, ev.At)
	}
	f := ev.Fields()
	for name, want := range map[string]string{
		"unit_id": "u1", "kind": KindRebase, "files": "2", "packages": "1", "lanes": "1", "lane": "decide",
		"rung_tried": "flash", "height": "0", "confidence": "0.94", "floor": "0.65",
		"stepped_up": "true", "escalated": "true", "designated": "true", "source": SourceRules,
		"rowan_pick": "flash", "reason": "the lane's owner is up", "wait": WaitAwaitingTermination,
		"awaiting_termination": "true", "refusal": "no-rung", "outcome": OutcomeOK, "rung_succeeded": "pro",
		"calls": "1", "tokens_in": "937", "usage_failed": "true",
	} {
		if f[name] != want {
			t.Errorf("%s = %q, want %q", name, f[name], want)
		}
	}
}

// Stella's presence rule: a counter the provider did not report is ABSENT,
// not zero, and a reported zero is a zero.
func TestDecisionEventKeepsAnUnreportedCounterAbsent(t *testing.T) {
	t.Parallel()

	e := sampleEntry()
	ev, err := DecisionEvent(e)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ev.Fields()["tokens_out"]; ok {
		t.Fatal("an unreported counter is absent from the entry, never a zero")
	}
	zero := 0
	e.TokensOut = &zero
	if ev, err = DecisionEvent(e); err != nil {
		t.Fatal(err)
	}
	if got, ok := ev.Fields()["tokens_out"]; !ok || got != "0" {
		t.Fatalf("a reported zero is a measurement: tokens_out=%q present=%v", got, ok)
	}
}

// A stamp that is not a time is a refusal naming the unit, never a silent zero
// stamp in the record.
func TestDecisionEventRefusesATimeThatIsNotOne(t *testing.T) {
	t.Parallel()

	e := sampleEntry()
	e.Time = "yesterday"
	if _, err := DecisionEvent(e); err == nil || !strings.Contains(err.Error(), "u1") {
		t.Fatalf("a stamp that is not a time must refuse naming the unit: %v", err)
	}
}

// A reason longer than the stream's field ceiling is cut with the byte mark,
// not refused: a long reason must not cost the whole decision.
func TestDecisionEventCapsALongReason(t *testing.T) {
	t.Parallel()

	e := sampleEntry()
	e.Reason = strings.Repeat("step 1: flash answered below the floor; ", 12)
	ev, err := DecisionEvent(e)
	if err != nil {
		t.Fatal(err)
	}
	if err := ev.Validate(); err != nil {
		t.Fatalf("a long reason cost the decision: %v", err)
	}
	if !strings.Contains(ev.Decision.Reason, "...+") {
		t.Fatalf("the reason was cut without the mark: %q", ev.Decision.Reason)
	}
}

// The round trip at the decide end: the sink writes through the Emitter every
// card transition uses, onto cards:done, and the fold reads it into decisions
// with the unreported counter NULL (a dash in the dump), never 0.
func TestEventSinkWritesADecisionTheFoldReads(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	stream := events.NewFakeStream()
	sink := &EventSink{Emitter: stream, Bench: "hulk"}
	if err := sink.Append(sampleEntry()); err != nil {
		t.Fatalf("writing the decision: %v", err)
	}
	if _, err := sink.Entries(); err == nil || !strings.Contains(err.Error(), "fold") {
		t.Fatalf("the writer must send a reader to the fold: %v", err)
	}
	db, err := events.OpenDB(ctx, filepath.Join(t.TempDir(), "ev.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	folder := &events.Folder{Reader: stream, DB: db}
	if err := folder.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := folder.Once(ctx); err != nil {
		t.Fatal(err)
	}
	if n, err := db.CountKind(ctx, events.Decide); err != nil || n != 1 {
		t.Fatalf("the fold holds %d decisions (err %v), want 1", n, err)
	}
	var dump bytes.Buffer
	if err := db.Dump(ctx, &dump); err != nil {
		t.Fatal(err)
	}
	// ... unit_id kind files packages lanes lane rung_tried height confidence floor ...
	// calls tokens_in tokens_out usd usage_failed
	if !strings.Contains(dump.String(), "\tu1\trebase\t2\t1\t1\tdecide\tflash\t0\t0.94\t0.65\t") ||
		!strings.Contains(dump.String(), "\t1\t937\t-\t-\t0\n") {
		t.Fatalf("the fold's decision row is not the row that was written:\n%s", dump.String())
	}
	if !strings.Contains(dump.String(), "decisions\t1-0\tu1\thulk\t") {
		t.Fatalf("the decision lost its bench:\n%s", dump.String())
	}
}

// Tee writes every sink and reports every failure, so the file and the stream
// are written together and neither failure hides the other.
func TestTeeWritesEverySinkAndReportsEveryFailure(t *testing.T) {
	t.Parallel()

	a, b := NewFakeLogSink(), NewFakeLogSink()
	both := Tee(a, nil, b)
	if err := both.Append(sampleEntry()); err != nil {
		t.Fatal(err)
	}
	for _, s := range []*FakeLogSink{a, b} {
		if rows, _ := s.Entries(); len(rows) != 1 {
			t.Fatalf("a sink behind the tee holds %d rows, want 1", len(rows))
		}
	}
	b.AppendErr = errors.New("the stream is down")
	err := both.Append(sampleEntry())
	if err == nil || !strings.Contains(err.Error(), "the stream is down") {
		t.Fatalf("the tee hid a failed sink: %v", err)
	}
	if rows, _ := a.Entries(); len(rows) != 2 {
		t.Fatalf("one failed sink stopped the others: the file holds %d rows, want 2", len(rows))
	}
}

// The summary is a projection of the rows, so it reads the same off the file
// and off the fake.
func TestSummaryIsTheSameOffEitherSink(t *testing.T) {
	t.Parallel()

	reg, err := LoadRegistry("")
	if err != nil {
		t.Fatal(err)
	}
	entries := []Entry{sampleEntry(), sampleEntry()}
	entries[1].Unit = "u2"
	entries[1].Outcome = OutcomeFailed
	entries[1].RungSucceeded = ""

	file, err := OpenLogSink(filepath.Join(t.TempDir(), "decide.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	fake := NewFakeLogSink()
	for _, e := range entries {
		if err := file.Append(e); err != nil {
			t.Fatal(err)
		}
		if err := fake.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	fileRows, err := file.Entries()
	if err != nil {
		t.Fatal(err)
	}
	fakeRows, err := fake.Entries()
	if err != nil {
		t.Fatal(err)
	}
	fileSum, err := Summarize(reg, fileRows)
	if err != nil {
		t.Fatal(err)
	}
	fakeSum, err := Summarize(reg, fakeRows)
	if err != nil {
		t.Fatal(err)
	}
	if fileSum.Render() != fakeSum.Render() {
		t.Errorf("the same rows are the same summary:\nfile: %s\nfake: %s", fileSum.Render(), fakeSum.Render())
	}
	if !strings.Contains(fileSum.Render(), "LOG OK rows=2") {
		t.Errorf("two rows are two rows: %s", fileSum.Render())
	}
}

// The fake keeps the order the rows were appended in.
func TestFakeLogSinkKeepsTheOrder(t *testing.T) {
	t.Parallel()

	sink := NewFakeLogSink()
	for _, id := range []string{"a", "b", "c"} {
		e := sampleEntry()
		e.Unit = id
		if err := sink.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := sink.Entries()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[0].Unit != "a" || rows[2].Unit != "c" {
		t.Fatalf("the rows are the record, in order: %+v", rows)
	}
}

// Stella HOLD 7 at d4482049: the absent->NULL invariant on the LIVE writer
// path. A JSON lines row that omitted confidence and floor goes through
// ReadEntries, EventSink.Append and so DecisionEvent, onto cards:done, and the
// fold stores NULL for both (a dash in the dump) -- never a present 0. The
// control beside it: a row that CARRIED a zero confidence folds a 0.
func TestAnOmittedConfidenceAndFloorFoldAsNullThroughDecisionEvent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "decide.jsonl")
	lines := `{"time":"2026-09-18T10:00:00Z","unit":"absent","kind":"rebase","evidence":{"id":"absent","kind":"rebase"},"rung_tried":"flash","height":0,"stepped_up":false,"escalated":false,"source":"rules","rowan_pick":"flash"}
{"time":"2026-09-18T10:00:01Z","unit":"zero","kind":"rebase","evidence":{"id":"zero","kind":"rebase"},"rung_tried":"flash","height":0,"confidence":0,"floor":0,"stepped_up":false,"escalated":false,"source":"rules","rowan_pick":"flash"}
`
	if err := os.WriteFile(path, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	rows, err := ReadEntries(path)
	if err != nil || len(rows) != 2 {
		t.Fatalf("read %d rows (err %v), want 2", len(rows), err)
	}

	ev, err := DecisionEvent(rows[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"confidence", "floor"} {
		if v, ok := ev.Fields()[name]; ok {
			t.Errorf("the source row omitted %s but the event carries %s=%q; absent is not zero", name, name, v)
		}
	}
	ev, err = DecisionEvent(rows[1])
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"confidence", "floor"} {
		if v, ok := ev.Fields()[name]; !ok || v != "0" {
			t.Errorf("the source row carried %s=0 but the event has %s=%q present=%v; a written zero is a zero", name, name, v, ok)
		}
	}

	stream := events.NewFakeStream()
	sink := &EventSink{Emitter: stream, Bench: "hulk"}
	for _, e := range rows {
		if err := sink.Append(e); err != nil {
			t.Fatalf("writing the decision: %v", err)
		}
	}
	db, err := events.OpenDB(ctx, filepath.Join(t.TempDir(), "ev.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	folder := &events.Folder{Reader: stream, DB: db}
	if err := folder.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := folder.Once(ctx); err != nil {
		t.Fatal(err)
	}
	var dump bytes.Buffer
	if err := db.Dump(ctx, &dump); err != nil {
		t.Fatal(err)
	}
	// ... unit_id kind files packages lanes lane rung_tried height confidence floor ...
	if !strings.Contains(dump.String(), "\tabsent\trebase\t0\t0\t0\t-\tflash\t0\t-\t-\t") {
		t.Errorf("the omitted confidence and floor did not fold as NULL:\n%s", dump.String())
	}
	if !strings.Contains(dump.String(), "\tzero\trebase\t0\t0\t0\t-\tflash\t0\t0\t0\t") {
		t.Errorf("the written zero confidence and floor did not fold as 0:\n%s", dump.String())
	}
}
