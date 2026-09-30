package sprint

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"strings"
	"testing"
)

// streamID is the stream id of the line at seq.
func streamID(seq uint64) string { return fmt.Sprintf("%d-0", seq) }

func TestEventOfReadsThePresentLineByItsOwnNames(t *testing.T) {
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
			Event{Seq: 14, Kind: Judgment, Cards: []string{"p1", "p2"}, Stream: "s1", NoteType: NCannotAsk, Subjects: []string{"p1", "p2"}, Opens: true, Count: 2}},
		{"a decided line closes",
			15, NoteLine(Note{ID: "n1.d1", Kind: Decided, Type: NBlocked, Stream: "s1", Primaries: []string{"w1"}, Count: 1, Who: "u1"}, "op2"),
			Event{Seq: 15, Kind: Decided, Cards: []string{"w1"}, Stream: "s1", Actor: "u1", NoteType: NBlocked, Subjects: []string{"w1"}, Closes: true, Count: 1}},
		{"an acknowledged line closes",
			16, NoteLine(Note{ID: "n2", Kind: Acknowledged, Type: NConflict, Stream: "s2", StreamLevel: true, Who: "u1"}, "op3"),
			Event{Seq: 16, Kind: Acknowledged, Cards: []string{"stream:s2"}, Stream: "s2", Actor: "u1", NoteType: NConflict, Subjects: []string{"stream:s2"}, Closes: true, Count: 1}},
		{"a happened line neither opens nor closes",
			17, NoteLine(Note{ID: "n3", Kind: Happened, Type: NMachineStarted, Who: "u1"}, "op4"),
			Event{Seq: 17, Kind: Happened, Actor: "u1", NoteType: NMachineStarted}},
		{"an update of an open judgment neither opens nor closes it",
			18, func() Line {
				l := NoteLine(Note{ID: "n4", Kind: Judgment, Type: NBound, Stream: "s1", Primaries: []string{"p1"}, Count: 1}, "op5")
				l.Verb = "updated"
				return l
			}(),
			Event{Seq: 18, Kind: Judgment, Cards: []string{"p1"}, Stream: "s1", Verb: "updated", NoteType: NBound, Subjects: []string{"p1"}, Count: 1}},
		{"a judgment of the whole sprint has the sprint as its subject",
			19, NoteLine(Note{ID: "n5", Kind: Judgment, Type: NSprintDone, SprintLevel: true}, "op6"),
			Event{Seq: 19, Kind: Judgment, Cards: []string{SprintSubject}, NoteType: NSprintDone, Subjects: []string{SprintSubject}, Opens: true, Count: 1}},
	}
	for _, tt := range tests {
		if got := EventOf(tt.seq, tt.line); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s:\n got %+v\nwant %+v", tt.name, got, tt.want)
		}
	}
}

// The count of a note is how many subjects it is on: a list the writer cut
// says so, and the one subject of a stream or of the sprint is never a cut list.
func TestEventOfKeepsTheCountOfTheSubjects(t *testing.T) {
	t.Parallel()
	ids := cardIDs(60)
	cut := EventOf(3, NoteLine(Note{ID: "n1", Kind: Decided, Type: NBlocked, Stream: "s1", Primaries: ids, Count: len(ids)}.Bound(), "op"))
	if len(cut.Subjects) != MaxListed || cut.Count != 60 {
		t.Fatalf("a note of 60 primaries, written as the Redis store writes it: %d subjects, count %d", len(cut.Subjects), cut.Count)
	}
	whole := EventOf(4, NoteLine(Note{ID: "n1", Kind: Decided, Type: NBlocked, Stream: "s1", Primaries: ids[:5], Count: 5}.Bound(), "op"))
	if len(whole.Subjects) != 5 || whole.Count != 5 {
		t.Fatalf("a note of 5: %d subjects, count %d", len(whole.Subjects), whole.Count)
	}
	// The tick writes a stream-level judgment with the primaries it is about,
	// and its count is theirs; its one subject is the stream.
	stream := EventOf(5, NoteLine(Note{ID: "n1", Kind: Judgment, Type: NStalled, Stream: "s1", Primaries: ids[:3], Count: 3, StreamLevel: true}, "op"))
	if !reflect.DeepEqual(stream.Subjects, []string{"stream:s1"}) || stream.Count != 1 {
		t.Fatalf("a stream-level note: %+v", stream)
	}
	// A move line has no count.
	if e := EventOf(6, workLine("p1", "s1:ready", "s1:working", nil)); e.Count != 0 {
		t.Fatalf("a move line has count %d", e.Count)
	}
}

// A note line names its stream when its line does not: a line built by another
// writer than NoteLine, or one whose stream the line left out.
func TestEventOfTakesTheStreamFromTheNoteWhenTheLineHasNone(t *testing.T) {
	t.Parallel()
	l := Line{Kind: Decided, Note: &Note{ID: "n1", Kind: Decided, Type: NSentinelReached, Stream: "s1", Primaries: []string{"g1"}, Count: 1}}
	if e := EventOf(1, l); e.Stream != "s1" {
		t.Fatalf("the stream of the event is %q", e.Stream)
	}
	if got := ingestAt(t, l); !reflect.DeepEqual(got, []string{"resolve:s1"}) {
		t.Fatalf("a sentinel reached, by a line with no stream of its own: %v", got)
	}
	// The line's own stream is the one it says, whatever the note holds.
	l.Stream = "s2"
	if e := EventOf(1, l); e.Stream != "s2" {
		t.Fatalf("the stream of the event is %q, not the line's", e.Stream)
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

// contractCase is a line of Layer 2's contract and the event it reads as.
type contractCase struct {
	name string
	seq  uint64
	line string
	want Event
}

// contractCases are lines worded as the log contract words them (its section
// 1.1, in the order of its keys, and section 4 for the semantic line), one or
// more for each kind of it: the stored body with k, ms and tbl, and the
// semantic line with kind, at_ms and table. The contract holds no literal line,
// so each is built from its table of keys.
func contractCases() []contractCase {
	q := func(s string) string { return fmt.Sprintf("%q", s) }
	return []contractCase{
		{"a move of one card, stored words", 7,
			`{"k":"m","ms":"1790000000123","tbl":"work","from":"s1:working","to":"s1:review","ids":["p1"],"about":["p1"],` +
				`"score":[["5","5"]],"rev":[["3","4"]],"shared":{"result":"ok","head":"abc"},` +
				`"meta":{"verb":"finish","actor":"m1","stream":"s1"}}`,
			Event{Seq: 7, Kind: LineMove, Table: Work, Cards: []string{"p1"}, Stream: "s1", Primary: "p1",
				From: "s1:working", To: "s1:review", Verb: "finish", Actor: "m1", Set: map[string]string{"result": "ok"}}},
		{"a move of a set of cards, semantic words", 8,
			`{"seq":8,"kind":"move","at_ms":"1790000000123","table":"work","from":"s1:waiting","to":"s1:ready",` +
				`"ids":["p1","p2","p3"],"about":["p1","p2","p3"],"score":[["1","1"],["2","2"],["3","3"]],` +
				`"rev":[["1","2"],["1","2"],["1","2"]],"set":[{"reached":"1"},{"reached":"1"},{"reached":"1"}],` +
				`"meta":{"verb":"resolve","actor":"machine","stream":"s1"}}`,
			Event{Seq: 8, Kind: LineMove, Table: Work, Cards: []string{"p1", "p2", "p3"}, Stream: "s1",
				From: "s1:waiting", To: "s1:ready", Verb: "resolve", Actor: "machine"}},
		{"a result on each id's own set, the same on all", 9,
			`{"kind":"move","at_ms":"1790000000123","table":"work","from":"s1:working","to":"s1:review","ids":["p1","p2"],` +
				`"about":["p1","p2"],"set":[{"result":"failed"},{"result":"failed"}],"meta":{"verb":"finish","actor":"m1","stream":"s1"}}`,
			Event{Seq: 9, Kind: LineMove, Table: Work, Cards: []string{"p1", "p2"}, Stream: "s1",
				From: "s1:working", To: "s1:review", Verb: "finish", Actor: "m1", Set: map[string]string{"result": "failed"}}},
		{"a create of two sentinels, kind shared and needs on each id's own set, stored words", 10,
			`{"k":"c","ms":"1790000000123","tbl":"work","to":"s3:waiting","ids":["g1","g2"],"about":["g1","g2"],` +
				`"score":[[null,"9"],[null,"10"]],"rev":[[null,"1"],[null,"1"]],"shared":{"kind":"sentinel","stream":"s3","attempt":"0"},` +
				`"set":[{"needs":"p1"},{}],"meta":{"verb":"add","actor":"u1","stream":"s3"}}`,
			Event{Seq: 10, Kind: LineMove, Table: Work, Cards: []string{"g1", "g2"}, Stream: "s3", To: "s3:waiting",
				Verb: "add", Actor: "u1", Set: map[string]string{"kind": "sentinel"}}},
		{"a create of one primary, semantic words", 11,
			`{"seq":11,"kind":"create","at_ms":"1790000000123","table":"work","to":"s3:waiting","ids":["p9"],"about":["p9"],` +
				`"score":[[null,"9"]],"rev":[[null,"1"]],"set":[{"kind":"primary","stream":"s3","attempt":"0"}],"meta":{"verb":"add","actor":"u1"}}`,
			Event{Seq: 11, Kind: LineMove, Table: Work, Cards: []string{"p9"}, Stream: "s3", Primary: "p9", To: "s3:waiting",
				Verb: "add", Actor: "u1", Set: map[string]string{"kind": "primary"}}},
		{"a remove, stored words", 12,
			`{"k":"x","ms":"1790000000123","tbl":"work","from":"s2:ready","ids":["p1"],"about":["p1"],"score":[["5",null]],` +
				`"rev":[["2","3"]],"meta":{"verb":"drop","actor":"u1"}}`,
			Event{Seq: 12, Kind: LineMove, Table: Work, Cards: []string{"p1"}, Stream: "s2", Primary: "p1",
				From: "s2:ready", Removed: true, Verb: "drop", Actor: "u1"}},
		{"a remove of two, semantic words", 13,
			`{"kind":"remove","at_ms":"1790000000123","table":"work","from":"s1:working","ids":["p1","p2"],"about":["p1","p2"],` +
				`"score":[["1",null],["2",null]],"rev":[["2","3"],["2","3"]],"meta":{"verb":"drop","actor":"u1","stream":"s1"}}`,
			Event{Seq: 13, Kind: LineMove, Table: Work, Cards: []string{"p1", "p2"}, Stream: "s1",
				From: "s1:working", Removed: true, Verb: "drop", Actor: "u1"}},
		{"a move that gives no place to stays where it was", 14,
			`{"k":"m","ms":"1790000000123","tbl":"work","from":"s1:ready","ids":["p1"],"about":["p1"],"score":[["1","5"]],` +
				`"rev":[["1","2"]],"meta":{"verb":"rescore","stream":"s1"}}`,
			Event{Seq: 14, Kind: LineMove, Table: Work, Cards: []string{"p1"}, Stream: "s1", Primary: "p1",
				From: "s1:ready", To: "s1:ready", Verb: "rescore"}},
		{"a read card, named by the primary in about", 15,
			`{"k":"m","ms":"1790000000123","tbl":"readers","from":"a:reading","to":"a:ok","ids":["p1.r1.a"],"about":["p1"],` +
				`"meta":{"verb":"read","actor":"a"}}`,
			Event{Seq: 15, Kind: LineMove, Table: Readers, Cards: []string{"p1.r1.a"}, Primary: "p1",
				From: "a:reading", To: "a:ok", Verb: "read", Actor: "a"}},
		{"a member's control card created", 16,
			`{"k":"c","ms":"1790000000123","tbl":"fleet","to":"m1:ctl","ids":["ctl-m1"],"about":["ctl-m1"],"score":[[null,"0"]],` +
				`"rev":[[null,"1"]],"shared":{"kind":"member","status":"up","since":"t"},"meta":{"verb":"fleet up","actor":"u1"}}`,
			Event{Seq: 16, Kind: LineMove, Table: Fleet, Cards: []string{"ctl-m1"}, Primary: "ctl-m1", To: "m1:ctl",
				Verb: "fleet up", Actor: "u1", Set: map[string]string{"kind": "member", "status": "up"}}},
		{"a rows line, which names no cards", 17,
			`{"k":"w","ms":"1790000000123","tbl":"work","add":[{"row":"s4","rank":"4"}],"del":["s0"],"meta":{"verb":"streams","actor":"u1"}}`,
			Event{Seq: 17, Kind: kindRows, Table: Work, Verb: "streams", Actor: "u1"}},
		{"an advance, whose places are epochs", 18,
			`{"k":"a","ms":"1790000000123","from":"3","to":"4","meta":{"verb":"clear","actor":"u1"}}`,
			Event{Seq: 18, Kind: kindAdvance, Verb: "clear", Actor: "u1"}},
		{"a note that opens a judgment", 19,
			`{"k":"n","ms":"1790000000123","about":["p1","p2"],"meta":{"kind":"judgment","type":` + q(NCannotAsk) + `,"stream":"s1"}}`,
			Event{Seq: 19, Kind: Judgment, Cards: []string{"p1", "p2"}, Stream: "s1", NoteType: NCannotAsk,
				Subjects: []string{"p1", "p2"}, Opens: true, Count: 2}},
		{"a note that closes one, semantic words", 20,
			`{"seq":20,"kind":"note","at_ms":"1790000000123","about":["w1"],"meta":{"kind":"decided","type":` + q(NBlocked) + `,"stream":"s1","actor":"u1"}}`,
			Event{Seq: 20, Kind: Decided, Cards: []string{"w1"}, Stream: "s1", Actor: "u1", NoteType: NBlocked,
				Subjects: []string{"w1"}, Closes: true, Count: 1}},
		{"a note of a stream that acknowledges", 21,
			`{"k":"n","ms":"1790000000123","about":["stream:s2"],"meta":{"kind":"acknowledged","type":` + q(NConflict) + `,"stream":"s2","actor":"u1"}}`,
			Event{Seq: 21, Kind: Acknowledged, Cards: []string{"stream:s2"}, Stream: "s2", Actor: "u1", NoteType: NConflict,
				Subjects: []string{"stream:s2"}, Closes: true, Count: 1}},
		{"a note that says a thing happened", 22,
			`{"k":"n","ms":"1790000000123","meta":{"kind":"happened","type":` + q(NMachineStarted) + `,"actor":"u1"}}`,
			Event{Seq: 22, Kind: Happened, Actor: "u1", NoteType: NMachineStarted}},
		{"a note that rewrites a judgment in place", 23,
			`{"k":"n","ms":"1790000000123","about":["p1"],"meta":{"kind":"judgment","verb":"updated","type":` + q(NBound) + `,"stream":"s1"}}`,
			Event{Seq: 23, Kind: Judgment, Cards: []string{"p1"}, Stream: "s1", Verb: "updated", NoteType: NBound,
				Subjects: []string{"p1"}, Count: 1}},
		{"a plain note on a card, which says no kind of judgment", 24,
			`{"k":"n","ms":"1790000000123","about":["p1"],"meta":{"text":"seen by the coordinator"}}`,
			Event{Seq: 24, Kind: kindNote, Cards: []string{"p1"}, Subjects: []string{"p1"}, Count: 1}},
	}
}

func TestParseEventReadsTheContractsLine(t *testing.T) {
	t.Parallel()
	for _, c := range contractCases() {
		got, err := ParseEvent(streamID(c.seq), []byte(c.line))
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s:\n got %+v\nwant %+v", c.name, got, c.want)
		}
	}
}

// A line of the contract that names cards never reads as naming none (the
// semantic word for the cards of a card's line is ids, for a note's about).
func TestParseEventNeverReadsALineThatNamesCardsAsNoCards(t *testing.T) {
	t.Parallel()
	for _, c := range contractCases() {
		var raw map[string]json.RawMessage
		if err := json.Unmarshal([]byte(c.line), &raw); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		count := func(word string) int {
			var names []string
			if b, ok := raw[word]; ok {
				if err := json.Unmarshal(b, &names); err != nil {
					t.Fatalf("%s: %v", c.name, err)
				}
			}
			return len(names)
		}
		named := count("ids")
		if named == 0 {
			named = count("about")
		}
		e, err := ParseEvent(streamID(c.seq), []byte(c.line))
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if len(e.Cards) != named {
			t.Errorf("%s: the line names %d cards, the event %d: %+v", c.name, named, len(e.Cards), e.Cards)
		}
	}
}

// The seq of an event is the stream id's. A line as the lines read returns it
// carries a seq of its own (the stream id's, copied), and a time the rules do
// not read: a line with none, or with the stream id's, is read; a line whose own
// seq says another is a wrong pairing of id and line, and is refused.
func TestParseEventTakesTheSeqFromTheStreamID(t *testing.T) {
	t.Parallel()
	line := func(seq string) string {
		if seq != "" {
			seq = `"seq":` + seq + `,`
		}
		return `{` + seq + `"kind":"move","at_ms":"1790000000123","table":"work","from":"s2:waiting","to":"s2:ready",` +
			`"ids":["p1","p2"],"about":["p1","p2"],"set":[{"reached":"1"},{"reached":"1"}],"meta":{"verb":"resolve","actor":"machine","stream":"s2"}}`
	}
	for _, seq := range []string{``, `9`, `"9"`} {
		e, err := ParseEvent("9-0", []byte(line(seq)))
		if err != nil {
			t.Fatalf("seq %s: %v", seq, err)
		}
		if e.Seq != 9 || e.Kind != LineMove || e.Table != Work || !reflect.DeepEqual(e.Cards, []string{"p1", "p2"}) || e.Stream != "s2" {
			t.Fatalf("seq %s: %+v", seq, e)
		}
		if e.From != "s2:waiting" || e.To != "s2:ready" || e.Set != nil {
			t.Fatalf("seq %s: %+v", seq, e)
		}
	}
	for _, seq := range []string{`5`, `"5"`, `10`, `"nine"`, `"09"`, `"9 "`, `""`, `9.0`, `null`, `true`, `[9]`, `{"seq":9}`} {
		e, err := ParseEvent("9-0", []byte(line(seq)))
		if err == nil {
			t.Errorf("a line with its own seq %s at the stream id 9-0 was read as %+v", seq, e)
			continue
		}
		if e.Seq != 9 {
			t.Errorf("seq %s: the error left the seq at %d, not 9", seq, e.Seq)
		}
		for _, want := range []string{"line 9", "its own seq is " + seq, "its stream id says 9"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("seq %s: the error does not say %q: %v", seq, want, err)
			}
		}
	}
}

// A cell reference is split at its last colon: the row may hold colons and the
// column may not.
func TestParseEventReadsACellAtItsLastColon(t *testing.T) {
	t.Parallel()
	e, err := ParseEvent("3-0", []byte(`{"k":"m","tbl":"work","from":"a:b:waiting","to":"a:b:ready","ids":["p1"],"about":["p1"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if e.From != "a:b:waiting" || e.To != "a:b:ready" || e.Stream != "a:b" || placeCol(e.To) != "ready" {
		t.Fatalf("%+v", e)
	}
	if got := ingestContractAt(t, `{"k":"m","tbl":"work","from":"a:b:waiting","to":"a:b:ready","ids":["p1"],"about":["p1"]}`); !reflect.DeepEqual(got, []string{"deal", "held:p1", "resolve:a:b"}) {
		t.Fatalf("a card into ready, in a row with a colon: %v", got)
	}
}

func TestParseEventRefusesWhatItCannotReadAndNamesTheSeq(t *testing.T) {
	t.Parallel()
	const good = `{"k":"m","tbl":"work","from":"s1:ready","ids":["p1"]}`
	for _, id := range []string{"", "7", "7-1", "0-0", "-0", "x-0", "-3-0", "+7-0", "007-0", "7-0 ", " 7-0", "7-0-0", "18446744073709551616-0"} {
		e, err := ParseEvent(id, []byte(good))
		if err == nil {
			t.Errorf("stream id %q accepted as %+v", id, e)
			continue
		}
		if e.Seq != 0 {
			t.Errorf("stream id %q: seq %d for an id with none", id, e.Seq)
		}
	}
	for _, line := range []string{
		// not a line at all
		"", "{", "[]", `"move"`, "null", `{"kind":5}`, good + " x", `{}`, `{"kind":""}`, `{"k":""}`,
		// a kind the contract does not name: the kinds of the present Line are not
		// the contract's. (Two kinds that disagree have a test of their own.) Each
		// card's line here names its table, so that it is refused for the reason
		// it is listed for and for no other.
		`{"k":"z","ids":["p1"]}`, `{"kind":"decided","about":["p1"]}`, `{"kind":"judgment","about":["p1"]}`,
		// a card's change that names no cards, empty ones, or a list that does
		// not line up with them; a present Line has card and cards, not ids
		`{"k":"m","tbl":"work","from":"s1:ready","to":"s1:working"}`, `{"k":"m","tbl":"work","from":"s1:ready","ids":[]}`,
		`{"k":"m","tbl":"work","from":"s1:ready","ids":[""]}`,
		`{"kind":"move","table":"work","card":"p1","cards":["p1","p2"],"from":"s1:ready","to":"s1:working"}`,
		`{"k":"m","tbl":"work","from":"s1:ready","ids":["p1","p2"],"about":["p1"]}`,
		`{"k":"m","tbl":"work","from":"s1:ready","ids":["p1","p2"],"set":[{}]}`,
		`{"k":"m","tbl":"work","from":"s1:ready","ids":["p1"],"set":{"reached":"1"}}`,
		// a place a kind needs, or one that is not a string
		`{"k":"c","tbl":"work","ids":["p1"]}`, `{"k":"m","tbl":"work","to":"s1:ready","ids":["p1"]}`, `{"k":"x","tbl":"work","ids":["p1"]}`,
		`{"k":"m","tbl":"work","from":5,"ids":["p1"]}`,
		// a field a rule reads that the ids do not agree on
		`{"k":"m","tbl":"work","from":"s1:working","to":"s1:review","ids":["p1","p2"],"set":[{"result":"ok"},{"result":"failed"}]}`,
		`{"k":"m","tbl":"work","from":"s1:working","to":"s1:review","ids":["p1","p2"],"set":[{"result":"ok"},{}]}`,
		`{"k":"m","tbl":"work","from":"s1:working","to":"s1:review","ids":["p1","p2"],"shared":{"result":"ok"},"set":[{},{"result":"failed"}]}`,
		// a meta word that is not a string, and a note of a kind that is not one
		`{"k":"m","tbl":"work","from":"s1:ready","ids":["p1"],"meta":{"stream":5}}`, `{"k":"n","about":["p1"],"meta":{"kind":"decided","type":5}}`,
		`{"k":"n","about":["p1"],"meta":{"kind":"move"}}`,
	} {
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
	if e, err := ParseEvent("18446744073709551615-0", []byte(good)); err != nil || e.Seq != 18446744073709551615 {
		t.Fatalf("the largest seq: %+v %v", e, err)
	}
}

// A line that says its kind twice, in the semantic word and in the stored tag,
// says one: two that disagree are refused, whichever the reader would have
// taken. Each line here is a good line under either of its two kinds, and read
// on with either word alone it is accepted, so the disagreement is the only
// reason to refuse it.
func TestParseEventRefusesAKindAndATagThatDisagree(t *testing.T) {
	t.Parallel()
	for _, line := range []string{
		// a move that stays where it is, and a remove: both name a place from
		`{"kind":"move","k":"x","tbl":"work","from":"s1:ready","ids":["p1"]}`,
		`{"kind":"remove","k":"m","tbl":"work","from":"s1:ready","ids":["p1"]}`,
		// a create and a move: both name a place to, and the move a place from
		`{"kind":"create","k":"m","tbl":"work","from":"s1:ready","to":"s1:working","ids":["p1"]}`,
		`{"kind":"move","k":"c","tbl":"work","from":"s1:ready","to":"s1:working","ids":["p1"]}`,
		// the kinds that name no cards
		`{"kind":"rows","k":"a"}`, `{"kind":"advance","k":"w"}`, `{"kind":"note","k":"a"}`, `{"kind":"advance","k":"n"}`,
	} {
		var words map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &words); err != nil {
			t.Fatal(err)
		}
		for _, drop := range []string{"kind", "k"} {
			one := maps.Clone(words)
			delete(one, drop)
			b, err := json.Marshal(one)
			if err != nil {
				t.Fatal(err)
			}
			if e, err := ParseEvent("42-0", b); err != nil {
				t.Errorf("%s, with one kind word alone: %v (read as %+v)", b, err, e)
			}
		}
		e, err := ParseEvent("42-0", []byte(line))
		if err == nil {
			t.Errorf("%s: read as %+v", line, e)
			continue
		}
		if e.Seq != 42 || !strings.Contains(err.Error(), "line 42") || !strings.Contains(err.Error(), "it says it is a ") {
			t.Errorf("%s: refused, but not as a kind that disagrees, and naming its seq (%d): %v", line, e.Seq, err)
		}
	}
}

// A card's change on a table the rules do not know is refused, and the error
// names the table: it would queue no key at all, not even its stream's, and
// nothing is lost silently. The stored name of a table, with a deployment's
// prefix, is such a name; so is no name. The four tables of the design are
// read, on each of the kinds of a card's change and in both vocabularies.
func TestParseEventRefusesACardsLineOnATableTheRulesDoNotKnow(t *testing.T) {
	t.Parallel()
	shapes := []string{
		`{"k":"c","tbl":%q,"to":"s1:ready","ids":["p1"]}`,
		`{"k":"m","tbl":%q,"from":"s1:working","to":"s1:review","ids":["p1"],"about":["p1"]}`,
		`{"k":"x","tbl":%q,"from":"s1:ready","ids":["p1"]}`,
		`{"kind":"create","table":%q,"to":"s1:ready","ids":["p1"]}`,
		`{"kind":"move","table":%q,"from":"s1:working","to":"s1:review","ids":["p1"]}`,
		`{"kind":"remove","table":%q,"from":"s1:ready","ids":["p1"]}`,
	}
	for _, table := range []string{Work, Readers, Merge, Fleet} {
		for _, shape := range shapes {
			line := fmt.Sprintf(shape, table)
			if e, err := ParseEvent("42-0", []byte(line)); err != nil || e.Table != table {
				t.Errorf("%s: %+v %v", line, e, err)
			}
		}
	}
	for _, table := range []string{"t-work", "t-readers", "Work", "work ", " work", "works", "cards", "sprint:work", ""} {
		for _, shape := range shapes {
			line := fmt.Sprintf(shape, table)
			e, err := ParseEvent("42-0", []byte(line))
			if err == nil {
				t.Errorf("%s: read as %+v", line, e)
				continue
			}
			if e.Seq != 42 || !strings.Contains(err.Error(), "line 42") || !strings.Contains(err.Error(), fmt.Sprintf("its table %q is none of", table)) {
				t.Errorf("%s: the error does not name the line and the table (seq %d): %v", line, e.Seq, err)
			}
		}
	}
	// A card's line with no table word at all names no table either.
	if e, err := ParseEvent("42-0", []byte(`{"k":"m","from":"s1:ready","ids":["p1"]}`)); err == nil || !strings.Contains(err.Error(), `its table "" is none of`) {
		t.Errorf("a card's line with no table: %+v %v", e, err)
	}
	// A line that names no cards has no table to know: it is read as it is.
	for line, kind := range map[string]string{
		`{"k":"w","tbl":"t-work","add":[{"row":"s4","rank":"4"}]}`: kindRows,
		`{"k":"a","from":"3","to":"4"}`:                            kindAdvance,
		`{"k":"n","about":["p1"],"meta":{"kind":"decided"}}`:       Decided,
	} {
		if e, err := ParseEvent("42-0", []byte(line)); err != nil || e.Kind != kind {
			t.Errorf("%s: %+v %v", line, e, err)
		}
	}
}

// The present Line is not the contract's line: the JSON of a line the sprint
// writes today is refused, not read as a line that names no cards.
func TestParseEventDoesNotReadThePresentLinesJSON(t *testing.T) {
	t.Parallel()
	for _, l := range []Line{
		workLine("p1", "s1:working", "s1:review", map[string]string{"result": "ok"}),
		setOf(Work, "s1:waiting", "s1:ready", nil, "p1", "p2"),
		NoteLine(Note{ID: "n1", Kind: Decided, Type: NBlocked, Stream: "s1", Primaries: []string{"w1"}, Count: 1}, "op"),
	} {
		b, err := json.Marshal(l)
		if err != nil {
			t.Fatal(err)
		}
		if e, err := ParseEvent("7-0", b); err == nil {
			t.Errorf("%s read as %+v", b, e)
		}
	}
}
