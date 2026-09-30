package machine

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
)

// The tests of the tick-end note (errata 3 amendment 8; SprintEvents.tla,
// TickEnd and TickEndOnce): a tick that addressed the coordinator ends with
// exactly one tick-end line, after every line it covers; a tick that did not,
// with none; the next ingest reads the line and makes no key of it.

// splitTickEnd is RT3's items less a last tick-end step, and that step's count
// (0 when RT3 has none).
func splitTickEnd(t *testing.T, items []sprintfn.Item) ([]sprintfn.Item, int) {
	t.Helper()
	if len(items) == 0 {
		return items, 0
	}
	last := items[len(items)-1].Step
	if last == nil || last.Meta.Rule != TickEndRule {
		return items, 0
	}
	if len(last.Body.Notes) != 1 || last.Body.Notes[0].Op != sprint.NoteOpTickEnd {
		t.Fatalf("the tick-end step carries %+v", last.Body.Notes)
	}
	n, ok := sprint.TickEndCount(last.Body.Notes[0].Text)
	if !ok {
		t.Fatalf("the tick-end note's text is %q", last.Body.Notes[0].Text)
	}
	return items[:len(items)-1], n
}

// logLine is a line of the log as the tests read it (L2 4's semantic words).
type logLine struct {
	Seq   json.RawMessage   `json:"seq"`
	Kind  string            `json:"kind"`
	About []string          `json:"about"`
	Meta  map[string]string `json:"meta"`
}

// lines are the epoch's lines from the index from (0-based), decoded; a meta
// value that is not a string (a list of decisions) is left out.
func (w *world) linesFrom(from int) []logLine {
	w.t.Helper()
	var out []logLine
	for _, raw := range w.log.Lines(testNames.Prefix, "0")[from:] {
		var l struct {
			Seq   json.RawMessage            `json:"seq"`
			Kind  string                     `json:"kind"`
			About []string                   `json:"about"`
			Meta  map[string]json.RawMessage `json:"meta"`
		}
		if err := json.Unmarshal(raw, &l); err != nil {
			w.t.Fatal(err)
		}
		ll := logLine{Seq: l.Seq, Kind: l.Kind, About: l.About, Meta: map[string]string{}}
		for k, v := range l.Meta {
			var s string
			if json.Unmarshal(v, &s) == nil {
				ll.Meta[k] = s
			}
		}
		out = append(out, ll)
	}
	return out
}

// tickEnds are the tick-end lines among lines.
func tickEnds(ls []logLine) []logLine {
	var out []logLine
	for _, l := range ls {
		if l.Kind == "note" && l.Meta["kind"] == sprint.TickEnd {
			out = append(out, l)
		}
	}
	return out
}

// judgeRule is the tests' deal (a key "work: cards enter ready" queues): its
// plan opens n judgments of distinct causes and finishes its keys; n is read
// when it plans, so a test sets each tick's count.
func judgeRule(n *int, typ string) sprint.Rule {
	calls := 0
	return testRule("deal", headRead(sprint.IndexFresh, 4, []string{"kind"}, nil),
		func(s *sprint.Snapshot, keys []sprint.AgendaKey, now sprint.Now) sprint.RulePlan {
			calls++
			var rp sprint.RulePlan
			for i := 0; i < *n; i++ {
				rp.Notes = append(rp.Notes, sprint.NoteReq{Op: "open", Type: typ, Cause: fmt.Sprintf("c%d-%d", calls, i),
					Subjects: []string{fmt.Sprintf("x%d", i)}, Text: "raised by the test"})
			}
			rp.Done = keys
			return rp
		})
}

// TestTickEndOneNoteForThreeJudgments: a tick whose rule opens three judgments
// writes one tick-end line, judgments=3, to the coordinator, its last line
// (after the three it covers), in a notes-only step of its own at the end of
// RT3 and no round trip more. The next tick ingests the four lines, makes no key
// of the tick-end line, and writes no tick-end: the three were covered.
func TestTickEndOneNoteForThreeJudgments(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.rows("s1")
	n := 3
	k := &counting{c: w.tw}
	l := w.loop("a", []sprint.Rule{judgeRule(&n, TypeStepRefused)}, Budget{})
	w.tick(l, k)
	w.verb(create("s1:ready", fresh(), "p1"))
	before := len(w.log.Lines(testNames.Prefix, "0"))
	rep := w.tick(l, k)
	if rep.RoundTrips != 3 || rep.Wake != 3 || rep.Applied != 1 {
		t.Fatalf("the tick with three judgments: %+v", rep)
	}
	rt3, wake := splitTickEnd(t, k.last())
	if wake != 3 || len(rt3) != 1 {
		t.Fatalf("RT3 is the rule's step and the tick-end of 3: tick-end %d after %d steps", wake, len(rt3))
	}
	ls := w.linesFrom(before)
	ends := tickEnds(ls)
	if len(ends) != 1 || ls[len(ls)-1].Meta["kind"] != sprint.TickEnd {
		t.Fatalf("the tick wrote %d tick-end lines, the last line %+v; want one, last", len(ends), ls[len(ls)-1])
	}
	e := ends[0]
	if e.Meta["text"] != "judgments=3" || e.Meta["to"] != sprint.TickEndTo || len(e.About) != 1 || e.About[0] != sprint.TickEndTo {
		t.Fatalf("the tick-end line: %+v", e)
	}
	opened := 0
	for _, x := range ls {
		if x.Meta["kind"] == sprint.Judgment {
			opened++
		}
	}
	if opened != 3 {
		t.Fatalf("the tick opened %d judgments, want 3", opened)
	}

	n = 0
	w.clk.add(TickEvery)
	before = len(w.log.Lines(testNames.Prefix, "0"))
	rep = w.tick(l, k)
	if rep.Lines == 0 || rep.Wake != 0 || rep.WakeLate != 0 || len(tickEnds(w.linesFrom(before))) != 0 {
		t.Fatalf("the tick after, ingesting the covered lines: %+v", rep)
	}
	ev, err := sprint.ParseEvent(strings.Trim(string(e.Seq), `"`)+"-0", w.log.Lines(testNames.Prefix, "0")[before-1])
	if err != nil || ev.Kind != sprint.TickEnd || len(sprint.Ingest([]sprint.Event{ev}).Keys) != 0 {
		t.Fatalf("the tick-end line reads as %+v (%v), keys %v; want the tick-end kind and no key", ev, err, sprint.Ingest([]sprint.Event{ev}).Keys)
	}
}

// TestTickEndNoneWhenNothingAddressed: a tick that addresses the coordinator
// nothing (a rule's step of no judgment) writes no tick-end, and reserves no
// step for one.
func TestTickEndNoneWhenNothingAddressed(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.rows("s1")
	n := 0
	k := &counting{c: w.tw}
	l := w.loop("a", []sprint.Rule{judgeRule(&n, TypeStepRefused)}, Budget{})
	w.tick(l, k)
	w.verb(create("s1:ready", fresh(), "p1"))
	before := len(w.log.Lines(testNames.Prefix, "0"))
	rep := w.tick(l, k)
	if _, wake := splitTickEnd(t, k.last()); rep.Wake != 0 || wake != 0 || rep.Applied != 1 {
		t.Fatalf("a tick of no judgment: %+v", rep)
	}
	if ends := tickEnds(w.linesFrom(before)); len(ends) != 0 {
		t.Fatalf("a tick of no judgment wrote %+v", ends)
	}
}

// TestTickEndCountsAVerbsJudgment: a judgment a verb opens between ticks is
// counted by the tick that ingests it, and with nothing to read or send in
// RT3 the ingest step is the tick's last and carries the tick-end.
func TestTickEndCountsAVerbsJudgment(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.rows("s1")
	n := 0
	k := &counting{c: w.tw}
	l := w.loop("a", []sprint.Rule{judgeRule(&n, TypeStepRefused)}, Budget{})
	w.tick(l, k)
	w.step(&sprintfn.Request{Epoch: "0", Meta: sprintfn.Meta{Verb: "merge", Actor: "merger"}, Body: sprintfn.Body{Notes: []sprintfn.NoteReq{
		{Op: sprintfn.JOpOpen, Type: sprint.NRed, Cause: "RED", Subjects: []string{sprint.StreamSubject("s1")}, Text: "red"}}}})
	before := len(w.log.Lines(testNames.Prefix, "0"))
	rep := w.tick(l, k)
	if rep.RoundTrips != 2 || rep.Wake != 1 {
		t.Fatalf("the tick that ingests a verb's judgment: %+v", rep)
	}
	rt2 := k.last()
	if st := rt2[0].Step; st == nil || st.Ingest == nil || len(st.Body.Notes) != 1 || st.Body.Notes[0].Text != "judgments=1" {
		t.Fatalf("the ingest step does not carry the tick-end: %+v", rt2)
	}
	if ends := tickEnds(w.linesFrom(before)); len(ends) != 1 || ends[0].Meta["text"] != "judgments=1" {
		t.Fatalf("the tick-end lines: %+v", ends)
	}
	w.clk.add(TickEvery)
	before = len(w.log.Lines(testNames.Prefix, "0"))
	if rep := w.tick(l, k); rep.Wake != 0 || len(tickEnds(w.linesFrom(before))) != 0 {
		t.Fatalf("the tick after wrote a tick-end: %+v", rep)
	}
}

// TestTickEndOwedWhenItsStepIsNotWritten: a tick-end step that does not apply
// is owed to the next RT1's error step, which writes it with the tick's count
// (Report.WakeLate); the tick that owed it and the one that writes it write
// one tick-end between them.
func TestTickEndOwedWhenItsStepIsNotWritten(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.rows("s1")
	n := 2
	ic := &intercept{c: w.tw}
	k := &counting{c: ic}
	l := w.loop("a", []sprint.Rule{judgeRule(&n, TypeStepRefused)}, Budget{})
	w.tick(l, k)
	w.verb(create("s1:ready", fresh(), "p1"))
	before := len(w.log.Lines(testNames.Prefix, "0"))
	ic.answer = func(it sprintfn.Item) *sprintfn.Result {
		if it.Step != nil && it.Step.Meta.Rule == TickEndRule {
			return &sprintfn.Result{Err: fmt.Errorf("the connection dropped")}
		}
		return nil
	}
	if rep := w.tick(l, k); rep.Wake != 0 || l.owed.wake != 2 {
		t.Fatalf("the tick whose tick-end was lost: %+v, owed %d", rep, l.owed.wake)
	}
	ic.answer = nil
	n = 0
	w.clk.add(TickEvery)
	rep := w.tick(l, k)
	if rep.WakeLate != 2 || rep.Wake != 0 || l.owed.wake != 0 {
		t.Fatalf("the next tick: %+v, owed %d; want the owed tick-end of 2 written late", rep, l.owed.wake)
	}
	if ends := tickEnds(w.linesFrom(before)); len(ends) != 1 || ends[0].Meta["text"] != "judgments=2" {
		t.Fatalf("the two ticks wrote the tick-end lines %+v; want one of 2", ends)
	}
}
