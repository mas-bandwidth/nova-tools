package tset

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// The twin of Layer 2 composed with Mem, as sprintfn composes them: Mem plans
// the tables, MemLog plans the lines from that plan, and both commit or
// neither (L1 1.1; L2 0). The model is tla/SetTableLog.tla.

type logTwin struct {
	t     *testing.T
	m     *Mem
	l     *MemLog
	space string
	now   int
}

func newLogTwin(t *testing.T, tables ...string) *logTwin {
	t.Helper()
	w := &logTwin{t: t, m: NewMem(), l: NewMemLog(), space: "twin:", now: 1790000000000}
	for _, table := range tables {
		if err := w.m.DefineTable(w.space, table, TableDefinition{Columns: []string{"a", "b"},
			MemberPrefix: w.space + "member:" + table + ":", EpochKey: w.space + "sprint:epoch", EpochField: "n"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.m.SetActiveEpoch(w.space, "0"); err != nil {
		t.Fatal(err)
	}
	return w
}

// try runs one composed step and returns its log plan or its refusal; a
// refusal writes nothing to either.
func (w *logTwin) try(step Step) (LogPlan, *Refusal) {
	w.t.Helper()
	step.Space = w.space
	mp, ref := w.m.Plan(step)
	if ref != nil {
		return LogPlan{}, ref
	}
	if mp.Replay {
		return LogPlan{}, nil
	}
	write := step.Epoch
	for _, e := range step.Entries {
		if e.Kind == "advance" {
			next, _ := NextDecimal(step.Epoch)
			write = next
		}
	}
	w.now++
	lp, apply, ref := w.l.Plan(w.space, LogLinesInput{NowMS: Decimal(fmt.Sprint(w.now)), RequestEpoch: step.Epoch,
		WriteEpoch: write, Entries: mp.Entries, Request: step.Entries, Notes: step.Notes})
	if ref != nil {
		return LogPlan{}, ref
	}
	if _, ref := w.m.Commit(mp); ref != nil {
		return LogPlan{}, ref
	}
	apply()
	return lp, nil
}

func (w *logTwin) step(step Step) LogPlan {
	w.t.Helper()
	lp, ref := w.try(step)
	if ref != nil {
		w.t.Fatalf("composed step refused %s: %+v", ref.Code, ref.Detail)
	}
	return lp
}

func (w *logTwin) read(plan ReadPlan) (ReadReply, *Refusal) {
	plan.Space = w.space
	return w.l.Read(w.space, plan)
}

func (w *logTwin) page(plan ReadPlan) ReadReply {
	w.t.Helper()
	plan.Mode = "page"
	r, ref := w.read(plan)
	if ref != nil {
		w.t.Fatalf("page refused %s: %+v", ref.Code, ref.Detail)
	}
	return r
}

// image is the whole twin: its tables and its log.
func (w *logTwin) image() string {
	w.t.Helper()
	snap, err := w.m.Snapshot(w.space)
	if err != nil {
		w.t.Fatal(err)
	}
	b, _ := json.Marshal(snap)
	var logs []string
	for _, epoch := range []Decimal{"0", "1", "2"} {
		lines, _ := json.Marshal(w.l.Stored(w.space, epoch))
		hist, _ := json.Marshal(w.l.Histories(w.space, epoch))
		logs = append(logs, string(lines), string(hist))
	}
	return string(b) + strings.Join(logs, "|")
}

func twinNote(meta string, about ...string) Note {
	return Note{Line: NoteLine{Kind: "note", Meta: json.RawMessage(meta)}, About: append([]string{}, about...)}
}

func twinNamed(step Step, op string) Step {
	intent := "twin/" + op
	step.Op, step.Intent = &op, &intent
	return step
}

// TestLogLinesCanonicalBodies: the twin builds the stored bodies the store's
// Lua builds, byte for byte, for the functional tier's own fixture
// (TestLogOneLinePerEmittingEntry): rows, a create with shared and own fields,
// a move, a no-op stay and a guard (no line), and two notes.
func TestLogLinesCanonicalBodies(t *testing.T) {
	t.Parallel()
	w := newLogTwin(t, "work")
	w.step(Step{Epoch: "0", Entries: []Entry{{Kind: "rows", Table: "work", Add: []string{"r"}}}})
	w.step(Step{Epoch: "0", Entries: []Entry{{Kind: "create",
		Table: "work", To: "r:a", IDs: []string{"x", "y", "z"}, Scores: []string{"1", "2", "3"},
		About: []string{"p", "q", "z"}, Set: map[string]string{"brief": "shared-brief"},
		Each: []map[string]string{{"own": "1"}, {"own": "2"}, {"own": "3"}}}}})
	lp := w.step(twinNamed(Step{Epoch: "0", Entries: []Entry{
		{Kind: "rows", Table: "work", Add: []string{"s"}},
		{Kind: "move", Table: "work", From: "r:a", To: "s:b", IDs: []string{"x"},
			Set: map[string]string{"state": "moved"}, About: []string{"p"}},
		{Kind: "move", Table: "work", From: "r:a", IDs: []string{"y"}, About: []string{"q"}},
		{Kind: "guard", Table: "work", From: "r:a", IDs: []string{"z"}},
	}, Notes: []Note{twinNote(`{"why":"x"}`, "p", "p", "q"), twinNote(`{}`)}}, "emit"))
	if lp.FirstSeq != "3" || lp.LastSeq != "6" || lp.LineCount != 4 ||
		!reflect.DeepEqual(lp.NoteSeqs, []Decimal{"5", "6"}) || lp.AboutAppends != 3 {
		t.Fatalf("emitting step's plan=%+v", lp)
	}
	want := []struct{ n, d string }{
		{"0", `{"k":"w","ms":"MS","tbl":"work","add":[{"rank":"0","row":"r"}]}`},
		{"3", `{"k":"c","ms":"MS","tbl":"work","to":"r:a","ids":["x","y","z"],"about":["p","q","z"],` +
			`"score":[[null,"1"],[null,"2"],[null,"3"]],"rev":[[null,"1"],[null,"1"],[null,"1"]],` +
			`"shared":{"brief":"shared-brief"},"set":[{"own":"1"},{"own":"2"},{"own":"3"}]}`},
		{"0", `{"k":"w","ms":"MS","tbl":"work","add":[{"rank":"1","row":"s"}]}`},
		{"1", `{"k":"m","ms":"MS","tbl":"work","from":"r:a","to":"s:b","ids":["x"],"about":["p"],` +
			`"score":[["1","1"]],"rev":[["1","2"]],"shared":{"state":"moved"}}`},
		{"2", `{"k":"n","ms":"MS","about":["p","q"],"meta":{"why":"x"}}`},
		{"0", `{"k":"n","ms":"MS"}`},
	}
	lines := w.l.Stored(w.space, "0")
	if len(lines) != len(want) {
		t.Fatalf("log has %d lines, want %d", len(lines), len(want))
	}
	for i, line := range lines {
		b, err := ParseLogBody(line.D)
		if err != nil {
			t.Fatal(err)
		}
		canonical := strings.Replace(line.D, `"ms":"`+b.MS+`"`, `"ms":"MS"`, 1)
		if line.N != want[i].n || canonical != want[i].d || string(line.Seq) != fmt.Sprint(i+1) {
			t.Errorf("line %s: n=%s d=%s\nwant n=%s d=%s", line.Seq, line.N, canonical, want[i].n, want[i].d)
		}
	}
	for about, seqs := range map[string][]Decimal{"p": {"2", "4", "5"}, "q": {"2", "5"}, "z": {"2"}} {
		if got := w.l.History(w.space, "0", about); !reflect.DeepEqual(got, seqs) {
			t.Errorf("history %s=%v, want %v", about, got, seqs)
		}
	}
	before := w.image()
	noop := w.step(Step{Epoch: "0", Entries: []Entry{
		{Kind: "move", Table: "work", From: "r:a", IDs: []string{"y"}, About: []string{"q"}},
		{Kind: "guard", Table: "work", From: "r:a", IDs: []string{"z"}},
		{Kind: "rows", Table: "work", Add: []string{"r"}},
	}})
	if noop.LineCount != 0 || noop.FirstSeq != "0" || noop.LastSeq != "0" || w.image() != before {
		t.Fatalf("a no-op step planned %+v or changed the twin", noop)
	}
}

// cardChain reads one cardlines query's whole page chain and returns each
// about's projected items as "seq/id" (or "seq/note"), failing on a page that
// returns nothing and is not exhausted.
func (w *logTwin) cardChain(q ReadQuery) map[string][]string {
	w.t.Helper()
	seen := map[string][]string{}
	for turn := 0; ; turn++ {
		if turn > 10000 {
			w.t.Fatal("cardlines page chain does not end")
		}
		page := w.page(ReadPlan{Epoch: "0", Queries: []ReadQuery{q}})
		n := 0
		for i, raw := range page.Items {
			var slot struct {
				About string            `json:"about"`
				Lines []json.RawMessage `json:"lines"`
			}
			if err := json.Unmarshal(raw, &slot); err != nil || slot.About != q.Abouts[i] {
				w.t.Fatalf("slot %d=%s err=%v", i, raw, err)
			}
			for _, line := range slot.Lines {
				var item struct {
					Seq  string `json:"seq"`
					Kind string `json:"kind"`
					ID   string `json:"id"`
				}
				if err := json.Unmarshal(line, &item); err != nil {
					w.t.Fatal(err)
				}
				label := item.ID
				if item.Kind == "note" {
					label = "note"
				}
				seen[slot.About] = append(seen[slot.About], item.Seq+"/"+label)
				n++
			}
		}
		if page.Exhausted {
			return seen
		}
		if n == 0 {
			w.t.Fatal("a nonexhausted page returned no item")
		}
		var cursor CardCursor
		if err := json.Unmarshal(page.Next, &cursor); err != nil {
			w.t.Fatalf("page cursor %s: %v", page.Next, err)
		}
		q.Cursor = &cursor
	}
}

// TestHistoryCursorCoverage (L2 4; L1 7; the model's
// CursorNeitherSkipsNorRepeats): on the composed twin, every page limit's
// cardlines chain returns each primary's projection of each of its history
// lines exactly once, in order, pages ending inside a line resume at the
// next item (the pair cursor), appends past the first page's high-water stay
// out of the chain, and no foreign id or field is returned.
func TestHistoryCursorCoverage(t *testing.T) {
	t.Parallel()
	w := newLogTwin(t, "work")
	w.step(Step{Epoch: "0", Entries: []Entry{{Kind: "rows", Table: "work", Add: []string{"r"}}}})
	w.step(Step{Epoch: "0", Entries: []Entry{{Kind: "create", Table: "work", To: "r:a",
		IDs: []string{"m1", "m2", "m3", "q1"}, Scores: []string{"1", "2", "3", "4"},
		About: []string{"p", "p", "p", "q"},
		Each:  []map[string]string{{"brief": "p1"}, {"brief": "p2"}, {"brief": "p3"}, {"brief": "q-only"}}}}})
	w.step(twinNamed(Step{Epoch: "0", Entries: []Entry{}, Notes: []Note{twinNote(`{"k":"v"}`, "p", "q"), twinNote(`{}`, "q")}}, "notes"))
	w.step(Step{Epoch: "0", Entries: []Entry{{Kind: "move", Table: "work", From: "r:a", To: "r:b",
		IDs: []string{"m2", "q1"}, About: []string{"p", "q"}, Set: map[string]string{"state": "moved"}}}})
	want := map[string][]string{
		"p": {"2/m1", "2/m2", "2/m3", "3/note", "5/m2"},
		"q": {"2/q1", "3/note", "4/note", "5/q1"},
	}
	for limit := 1; limit <= 6; limit++ {
		got := w.cardChain(ReadQuery{Kind: "cardlines", Abouts: []string{"p", "q"}, Fields: []string{"brief"}, Limit: limit})
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("limit %d: the chain gave %v, want %v", limit, got, want)
		}
	}
	// A page ending inside the three-item create line carries the pair.
	first := w.page(ReadPlan{Epoch: "0", Queries: []ReadQuery{{Kind: "cardlines", Abouts: []string{"p"},
		Fields: []string{"brief"}, Limit: 2}}})
	var cursor CardCursor
	if err := json.Unmarshal(first.Next, &cursor); err != nil || cursor.Positions[0].NextIndex != 0 ||
		cursor.Positions[0].NextItem != 2 || cursor.Positions[0].ThroughIndex != 2 {
		t.Fatalf("first page's cursor %s err=%v, want the pair (0, 2) and high-water 2", first.Next, err)
	}
	if strings.Contains(string(first.Items[0]), "q-only") || strings.Contains(string(first.Items[0]), `"q1"`) {
		t.Fatalf("a foreign id or field in p's projection: %s", first.Items[0])
	}
	// Appends after the first page stay out of its chain.
	w.step(twinNamed(Step{Epoch: "0", Entries: []Entry{}, Notes: []Note{twinNote(`{}`, "p")}}, "late"))
	q := ReadQuery{Kind: "cardlines", Abouts: []string{"p"}, Fields: []string{"brief"}, Limit: 2, Cursor: &cursor}
	var rest []string
	for {
		page := w.page(ReadPlan{Epoch: "0", Queries: []ReadQuery{q}})
		var slot struct {
			Lines []struct {
				Seq string `json:"seq"`
			} `json:"lines"`
		}
		if err := json.Unmarshal(page.Items[0], &slot); err != nil {
			t.Fatal(err)
		}
		for _, l := range slot.Lines {
			rest = append(rest, l.Seq)
		}
		if page.Exhausted {
			break
		}
		var next CardCursor
		if err := json.Unmarshal(page.Next, &next); err != nil {
			t.Fatal(err)
		}
		q.Cursor = &next
	}
	if !reflect.DeepEqual(rest, []string{"2", "3", "5"}) {
		t.Fatalf("the rest of the chain after the pair: %v, want m3's line 2, then 3 and 5, and not the late 6", rest)
	}
}

// TestRefuseCURSOR (L2 8; L1 7): a cardlines cursor whose epoch, projection,
// metadata switch, abouts or order differ, a position past its high-water or
// the list, an item index past its line, and a lines through_seq above the
// tail or an after_seq above the high-water: each is CURSOR with the query's
// index, and nothing changes.
func TestRefuseCURSOR(t *testing.T) {
	t.Parallel()
	w := newLogTwin(t, "work")
	w.step(Step{Epoch: "0", Entries: []Entry{{Kind: "rows", Table: "work", Add: []string{"r"}}}})
	w.step(Step{Epoch: "0", Entries: []Entry{{Kind: "create", Table: "work", To: "r:a",
		IDs: []string{"m1", "m2"}, Scores: []string{"1", "2"}, About: []string{"p", "p"}}}})
	w.step(twinNamed(Step{Epoch: "0", Entries: []Entry{}, Notes: []Note{twinNote(`{}`, "p"), twinNote(`{}`, "q")}}, "notes"))
	query := ReadQuery{Kind: "cardlines", Abouts: []string{"p", "q"}, Fields: []string{}, Limit: 1}
	first := w.page(ReadPlan{Epoch: "0", Queries: []ReadQuery{query}})
	var good CardCursor
	if err := json.Unmarshal(first.Next, &good); err != nil || len(good.Positions) != 2 {
		t.Fatalf("first cursor %s: %v", first.Next, err)
	}
	clone := func() CardCursor {
		c := good
		c.Fields = append([]string{}, good.Fields...)
		c.Positions = append([]CardCursorPosition(nil), good.Positions...)
		return c
	}
	before := w.image()
	cases := []struct {
		name  string
		plan  func() ReadPlan
		query func(*ReadQuery)
	}{
		{"wrong epoch", nil, func(q *ReadQuery) { c := clone(); c.Epoch = "1"; q.Cursor = &c }},
		{"projection mismatch", nil, func(q *ReadQuery) { c := clone(); q.Fields = []string{"brief"}; q.Cursor = &c }},
		{"metadata mismatch", nil, func(q *ReadQuery) { c := clone(); q.IncludeMeta = true; q.Cursor = &c }},
		{"about order mismatch", nil, func(q *ReadQuery) { c := clone(); q.Abouts = []string{"q", "p"}; q.Cursor = &c }},
		{"duplicate position", nil, func(q *ReadQuery) { c := clone(); c.Positions[1].About = "p"; q.Cursor = &c }},
		{"future position", nil, func(q *ReadQuery) {
			c := clone()
			c.Positions[0].NextIndex = c.Positions[0].ThroughIndex + 2
			q.Cursor = &c
		}},
		{"future high-water", nil, func(q *ReadQuery) { c := clone(); c.Positions[0].ThroughIndex += 100; q.Cursor = &c }},
		{"item past its line", nil, func(q *ReadQuery) { c := clone(); c.Positions[0].NextItem = 5; q.Cursor = &c }},
		{"item on an exhausted position", nil, func(q *ReadQuery) {
			c := clone()
			c.Positions[1].NextIndex = c.Positions[1].ThroughIndex + 1
			c.Positions[1].NextItem = 1
			q.Cursor = &c
		}},
		{"lines through above the tail", func() ReadPlan {
			through := Decimal("99")
			return ReadPlan{Epoch: "0", Queries: []ReadQuery{{Kind: "lines", AfterSeq: "0", ThroughSeq: &through, Limit: 5}}}
		}, nil},
		{"lines after above the high-water", func() ReadPlan {
			through := Decimal("2")
			return ReadPlan{Epoch: "0", Queries: []ReadQuery{{Kind: "lines", AfterSeq: "3", ThroughSeq: &through, Limit: 5}}}
		}, nil},
	}
	for _, tc := range cases {
		plan := ReadPlan{Epoch: "0", Mode: "page"}
		if tc.plan != nil {
			plan = tc.plan()
			plan.Mode = "page"
		} else {
			q := query
			tc.query(&q)
			plan.Queries = []ReadQuery{q}
		}
		reply, ref := w.read(plan)
		if ref == nil || ref.Code != "CURSOR" || ref.Detail.QueryIndex == nil || *ref.Detail.QueryIndex != 0 ||
			len(reply.Items) != 0 || len(reply.Answers) != 0 {
			t.Errorf("%s: reply=%+v refusal=%+v, want CURSOR at query 0 with no answer", tc.name, reply, ref)
		}
		if w.image() != before {
			t.Fatalf("%s changed the twin", tc.name)
		}
	}
	// The good cursor still reads.
	q := query
	q.Cursor = &good
	w.page(ReadPlan{Epoch: "0", Queries: []ReadQuery{q}})
}

// TestRefuseLOGIDAcrossComposition (L2 2, 8; the model's LogIdGuard,
// SeqGapless and SeqCeiling): on Mem composed with the log twin, a head a raw
// XADD * left, an entry a raw XDEL took, and a stored head past the live
// ceiling are LOGID for the next write, which changes neither the tables nor
// the log, and for the reads; a history seq whose line is gone is LOGID for
// cardlines; a log deleted under its histories is DRIFT for the next write;
// and a step that would pass the ceiling is OVERFLOW.
func TestRefuseLOGIDAcrossComposition(t *testing.T) {
	t.Parallel()
	seed := func(t *testing.T) *logTwin {
		w := newLogTwin(t, "work")
		w.step(Step{Epoch: "0", Entries: []Entry{{Kind: "rows", Table: "work", Add: []string{"r"}}}})
		w.step(Step{Epoch: "0", Entries: []Entry{{Kind: "create", Table: "work", To: "r:a",
			IDs: []string{"p"}, Scores: []string{"1"}, About: []string{"p"}}}})
		return w
	}
	write := Step{Epoch: "0", Entries: []Entry{{Kind: "create", Table: "work", To: "r:a",
		IDs: []string{"must-not-exist"}, Scores: []string{"2"}, About: []string{"must-not-exist"}}}}
	refused := func(t *testing.T, w *logTwin, step Step, code, budget string) {
		t.Helper()
		before := w.image()
		_, ref := w.try(step)
		if ref == nil || ref.Code != code || (budget != "" && ref.Detail.Budget != budget) {
			t.Fatalf("write refusal=%+v, want %s %s", ref, code, budget)
		}
		if w.image() != before {
			t.Fatalf("a %s write changed the tables or the log", code)
		}
	}
	reads := func(t *testing.T, w *logTwin, code string) {
		t.Helper()
		for _, q := range []ReadQuery{{Kind: "last"}, {Kind: "lines", AfterSeq: "0", Limit: 10}} {
			reply, ref := w.read(ReadPlan{Epoch: "0", Queries: []ReadQuery{q}})
			if ref == nil || ref.Code != code || len(reply.Answers) != 0 {
				t.Fatalf("%s read: reply=%+v refusal=%+v, want %s", q.Kind, reply, ref, code)
			}
		}
	}
	t.Run("auto id", func(t *testing.T) {
		t.Parallel()
		w := seed(t)
		w.l.PlantAutoID(w.space, "0")
		refused(t, w, write, "LOGID", "last_generated_id")
		reads(t, w, "LOGID")
	})
	t.Run("deleted entry", func(t *testing.T) {
		t.Parallel()
		w := seed(t)
		w.l.DeleteLine(w.space, "0", "1")
		refused(t, w, write, "LOGID", "length")
		reads(t, w, "LOGID")
		// p's history names seq 2, whose line is still there; delete it too
		// and cardlines finds a history seq with no line.
		w.l.DeleteLine(w.space, "0", "2")
		_, ref := w.read(ReadPlan{Epoch: "0", Queries: []ReadQuery{{Kind: "cardlines", Abouts: []string{"p"}, Fields: []string{}, Limit: 5}}})
		if ref == nil || ref.Code != "LOGID" {
			t.Fatalf("cardlines over a deleted line: %+v, want LOGID", ref)
		}
	})
	t.Run("head past the ceiling", func(t *testing.T) {
		t.Parallel()
		w := seed(t)
		w.l.SetHead(w.space, "0", logSeqCeiling+1)
		refused(t, w, write, "LOGID", "entries_added")
	})
	t.Run("step past the ceiling", func(t *testing.T) {
		t.Parallel()
		w := seed(t)
		w.l.SetHead(w.space, "0", logSeqCeiling-1)
		two := twinNamed(Step{Epoch: "0", Entries: []Entry{}, Notes: []Note{twinNote(`{}`, "p"), twinNote(`{}`, "p")}}, "two")
		refused(t, w, two, "OVERFLOW", "log_seq")
		one := twinNamed(Step{Epoch: "0", Entries: []Entry{}, Notes: []Note{twinNote(`{}`, "p")}}, "one")
		if lp := w.step(one); lp.FirstSeq != "9007199254740991" {
			t.Fatalf("the last seq under the ceiling: %+v", lp)
		}
	})
	t.Run("log deleted under its histories", func(t *testing.T) {
		t.Parallel()
		w := seed(t)
		w.l.DeleteLog(w.space, "0")
		touch := Step{Epoch: "0", Entries: []Entry{{Kind: "move", Table: "work", From: "r:a", To: "r:b",
			IDs: []string{"p"}, About: []string{"p"}}}}
		refused(t, w, touch, "DRIFT", "")
	})
}
