package sprint

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// lineJSON is a line as the log holds it.
func lineJSON(t *testing.T, l Line) []byte {
	t.Helper()
	b, err := json.Marshal(l)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// streamID is the stream id of the line at seq.
func streamID(seq uint64) string { return fmt.Sprintf("%d-0", seq) }

func TestParseEventReadsALineByItsOwnNames(t *testing.T) {
	t.Parallel()
	moveLine := func(l Line) Line {
		l.Kind = LineMove
		return l
	}
	tests := []struct {
		name string
		seq  uint64
		line Line
		want Event
	}{
		{"one card moved",
			7, moveLine(Line{Table: Work, Card: "p1", Primary: "p1", Stream: "s1", From: "s1:working", To: "s1:review",
				Verb: "finish", Actor: "m1", Set: map[string]string{"result": "ok", "head": "abc", "score": "5"}}),
			Event{Seq: 7, Kind: LineMove, Table: Work, Cards: []string{"p1"}, Stream: "s1", Primary: "p1",
				From: "s1:working", To: "s1:review", Verb: "finish", Actor: "m1", Set: map[string]string{"result": "ok"}}},
		{"a set of cards moved by one line",
			8, moveLine(Line{Table: Work, Card: "p1", Cards: []string{"p1", "p2", "p3"}, Stream: "s1", From: "s1:waiting", To: "s1:ready",
				Verb: "resolve", Actor: "machine"}),
			Event{Seq: 8, Kind: LineMove, Table: Work, Cards: []string{"p1", "p2", "p3"}, Stream: "s1",
				From: "s1:waiting", To: "s1:ready", Verb: "resolve", Actor: "machine"}},
		{"a work card taken off the table has its stream from the row it left, and is its own primary",
			9, moveLine(Line{Table: Work, Card: "p1", From: "s2:ready", Removed: true, Verb: "drop", Actor: "u1"}),
			Event{Seq: 9, Kind: LineMove, Table: Work, Cards: []string{"p1"}, Stream: "s2", Primary: "p1",
				From: "s2:ready", Removed: true, Verb: "drop", Actor: "u1"}},
		{"a work card created has its stream from the row it is placed in",
			10, moveLine(Line{Table: Work, Card: "p9", To: "s3:waiting", Set: map[string]string{"score": "9"}}),
			Event{Seq: 10, Kind: LineMove, Table: Work, Cards: []string{"p9"}, Stream: "s3", Primary: "p9", To: "s3:waiting"}},
		{"a read card keeps the primary the line gives and no stream of its own is made",
			11, moveLine(Line{Table: Readers, Card: "p1.r1.a", Primary: "p1", From: "a:reading", To: "a:ok", Verb: "read", Actor: "a"}),
			Event{Seq: 11, Kind: LineMove, Table: Readers, Cards: []string{"p1.r1.a"}, Primary: "p1",
				From: "a:reading", To: "a:ok", Verb: "read", Actor: "a"}},
		{"a control card created keeps the fields the rules read and drops the rest",
			12, moveLine(Line{Table: Fleet, Card: "ctl-m1", To: "m1:ctl", Verb: "fleet up", Actor: "u1",
				Set: map[string]string{"kind": "member", "status": "up", "since": "t", "score": "0", "load": "1", "held": "t9", "result": "x"}}),
			Event{Seq: 12, Kind: LineMove, Table: Fleet, Cards: []string{"ctl-m1"}, To: "m1:ctl", Verb: "fleet up", Actor: "u1",
				Set: map[string]string{"kind": "member", "status": "up", "held": "t9", "result": "x"}}},
		{"a line that set nothing a rule reads has no fields",
			13, moveLine(Line{Table: Work, Card: "p1", Primary: "p1", Stream: "s1", From: "s1:ready", To: "s1:ready", Set: map[string]string{"score": "2"}}),
			Event{Seq: 13, Kind: LineMove, Table: Work, Cards: []string{"p1"}, Stream: "s1", Primary: "p1", From: "s1:ready", To: "s1:ready"}},
		{"a judgment opens on its subjects",
			14, NoteLine(Note{ID: "n1", Kind: Judgment, Type: NCannotAsk, Stream: "s1", Primaries: []string{"p1", "p2"}, Count: 2}, "op1"),
			Event{Seq: 14, Kind: Judgment, Cards: []string{"p1", "p2"}, Stream: "s1", NoteType: NCannotAsk, Subjects: []string{"p1", "p2"}, Opens: true}},
		{"a decided line closes",
			15, NoteLine(Note{ID: "n1.d1", Kind: Decided, Type: NBlocked, Stream: "s1", Primaries: []string{"w1"}, Count: 1, Who: "u1"}, "op2"),
			Event{Seq: 15, Kind: Decided, Cards: []string{"w1"}, Stream: "s1", Actor: "u1", NoteType: NBlocked, Subjects: []string{"w1"}, Closes: true}},
		{"an acknowledged line closes",
			16, NoteLine(Note{ID: "n2", Kind: Acknowledged, Type: NConflict, Stream: "s2", StreamLevel: true, Who: "u1"}, "op3"),
			Event{Seq: 16, Kind: Acknowledged, Cards: []string{"stream:s2"}, Stream: "s2", Actor: "u1", NoteType: NConflict, Subjects: []string{"stream:s2"}, Closes: true}},
		{"a happened line neither opens nor closes",
			17, NoteLine(Note{ID: "n3", Kind: Happened, Type: NMachineStarted, Who: "u1"}, "op4"),
			Event{Seq: 17, Kind: Happened, Actor: "u1", NoteType: NMachineStarted}},
		{"an update of an open judgment neither opens nor closes it",
			18, func() Line {
				l := NoteLine(Note{ID: "n4", Kind: Judgment, Type: NBound, Stream: "s1", Primaries: []string{"p1"}, Count: 1}, "op5")
				l.Verb = "updated"
				return l
			}(),
			Event{Seq: 18, Kind: Judgment, Cards: []string{"p1"}, Stream: "s1", Verb: "updated", NoteType: NBound, Subjects: []string{"p1"}}},
		{"a judgment of the whole sprint has the sprint as its subject",
			19, NoteLine(Note{ID: "n5", Kind: Judgment, Type: NSprintDone, SprintLevel: true}, "op6"),
			Event{Seq: 19, Kind: Judgment, Cards: []string{SprintSubject}, NoteType: NSprintDone, Subjects: []string{SprintSubject}, Opens: true}},
	}
	for _, tt := range tests {
		got, err := ParseEvent(streamID(tt.seq), lineJSON(t, tt.line))
		if err != nil {
			t.Errorf("%s: %v", tt.name, err)
			continue
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s:\n got %+v\nwant %+v", tt.name, got, tt.want)
		}
		if again := EventOf(tt.seq, tt.line); !reflect.DeepEqual(again, got) {
			t.Errorf("%s: EventOf and ParseEvent disagree:\n%+v\n%+v", tt.name, again, got)
		}
	}
}

func TestParseEventTakesTheSeqFromTheStreamID(t *testing.T) {
	t.Parallel()
	// A line as the layer below is to write it, with a seq and a time of its
	// own and its words grouped: the stream id says which line it is.
	raw := `{"seq":5,"kind":"move","at_ms":1790000000123,"epoch":3,"op":"o~3","table":"work","from":"s2:waiting","to":"s2:ready",` +
		`"cards":["p1","p2"],"set":{"reached":"1"},"each":null,"scores":null,"removed":false,` +
		`"meta":{"verb":"resolve","actor":"machine","stream":"s2"}}`
	e, err := ParseEvent("9-0", []byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if e.Seq != 9 || e.Kind != LineMove || e.Table != Work || !reflect.DeepEqual(e.Cards, []string{"p1", "p2"}) || e.Stream != "s2" {
		t.Fatalf("%+v", e)
	}
	if e.From != "s2:waiting" || e.To != "s2:ready" || e.Set != nil {
		t.Fatalf("%+v", e)
	}
}

func TestParseEventRefusesWhatItCannotReadAndNamesTheSeq(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"", "7", "7-1", "0-0", "-0", "x-0", "-3-0", "+7-0", "007-0", "7-0 ", " 7-0", "7-0-0", "18446744073709551616-0"} {
		e, err := ParseEvent(id, []byte(`{"kind":"move"}`))
		if err == nil {
			t.Errorf("stream id %q accepted as %+v", id, e)
			continue
		}
		if e.Seq != 0 {
			t.Errorf("stream id %q: seq %d for an id with none", id, e.Seq)
		}
	}
	for _, line := range []string{"", "{", "[]", `"move"`, "null", `{"kind":5}`, `{"kind":"move"} x`, `{"kind":"move","at":"yesterday"}`, `{}`, `{"kind":""}`} {
		e, err := ParseEvent("42-0", []byte(line))
		if err == nil {
			t.Errorf("line %q accepted as %+v", line, e)
			continue
		}
		if e.Seq != 42 {
			t.Errorf("line %q: the error left the seq at %d, not 42", line, e.Seq)
		}
		if !strings.Contains(err.Error(), "line 42") {
			t.Errorf("line %q: the error does not name the line: %v", line, err)
		}
	}
	if e, err := ParseEvent("18446744073709551615-0", []byte(`{"kind":"move"}`)); err != nil || e.Seq != 18446744073709551615 {
		t.Fatalf("the largest seq: %+v %v", e, err)
	}
}

func TestEventKeepsOnlyTheFieldsTheRulesRead(t *testing.T) {
	t.Parallel()
	set := map[string]string{"score": "1", "head": "h", "gen": "2", "member": "m1"}
	for _, f := range EventFields {
		set[f] = "v-" + f
	}
	e := EventOf(1, Line{Kind: LineMove, Table: Work, Card: "p1", Set: set})
	if len(e.Set) != len(EventFields) {
		t.Fatalf("%v", e.Set)
	}
	for _, f := range EventFields {
		if e.Set[f] != "v-"+f {
			t.Errorf("field %s: %q", f, e.Set[f])
		}
	}
	// The event holds a copy: what the line's map holds is not shared.
	set["result"] = "changed"
	if e.Set["result"] != "v-result" {
		t.Fatal("the event shares the line's fields")
	}
}

func TestEventOfDoesNotShareTheLinesSlices(t *testing.T) {
	t.Parallel()
	l := NoteLine(Note{Kind: Decided, Type: NBound, Primaries: []string{"p1", "p2"}, Count: 2}, "op")
	e := EventOf(3, l)
	e.Subjects[0] = "changed"
	if l.Note.Primaries[0] != "p1" {
		t.Fatal("the event's subjects are the note's slice")
	}
}
