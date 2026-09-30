//go:build functional

package tset

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The Go side of Layer 2 against the store's Lua (work items J5, J9; J7's
// LineBytes stays owed as an export): the twin's lines equal the stored lines
// byte for byte on random steps, and a replay of an epoch's log into a Mem
// twin equals the store's tables.

// logWorld is the composed store and a Mem and MemLog kept in step with it.
type logWorld struct {
	t     *testing.T
	fx    *tsetFixture
	store *RedisStore
	mem   *Mem
	log   *MemLog
	rng   *rand.Rand
	// the generator's own view: each table's rows, and each member's cell
	rows   map[string][]string
	cells  map[string]map[string][]string // table, cell: ids
	placed map[string]string              // table\x00id: cell
	all    map[string]bool                // table\x00id, placed or removed
	nextID int
	lines  int
	ops    int
	// reads counts the reads compared by kind and mode, refusals by code.
	reads map[string]int
}

var logWorldColumns = map[string][]string{"work": {"a", "b"}, "aux": {"c"}}

func newLogWorld(t *testing.T, seed int64) *logWorld {
	t.Helper()
	fx := newComposedTSetFixture(t)
	w := &logWorld{t: t, fx: fx, mem: NewMem(), log: NewMemLog(), rng: rand.New(rand.NewSource(seed)),
		rows: map[string][]string{}, cells: map[string]map[string][]string{}, placed: map[string]string{}, all: map[string]bool{},
		reads: map[string]int{}}
	for _, table := range []string{"work", "aux"} {
		fx.Define(t, table, logWorldColumns[table]...)
		if err := w.mem.DefineTable(fx.Space, table, TableDefinition{Columns: logWorldColumns[table],
			MemberPrefix: fx.Space + "member:" + table + ":", EpochKey: fx.Space + "sprint:epoch", EpochField: "n"}); err != nil {
			t.Fatal(err)
		}
		w.cells[table] = map[string][]string{}
	}
	// The line-at probe registers L.read_line_at as a read kind, so the
	// twin's LineAt is compared with the store's.
	fx.ActivateWithLua(t, logDraftLineAtProbeLua)
	w.store = newFixtureRedis(t, fx.Client)
	return w
}

// apply runs one step on the store and on the twin, and compares every line
// the store wrote with the twin's, byte for byte, with its seq.
func (w *logWorld) apply(step Step) {
	w.t.Helper()
	step.Space = w.fx.Space
	w.ops++
	op := fmt.Sprintf("op-%d", w.ops)
	intent := "log-world/" + op
	step.Op, step.Intent = &op, &intent
	// The composed profile's static rules come before Mem's guards (L1 8).
	if ref := CheckLogStep(step.Entries, step.Notes); ref != nil {
		w.t.Fatalf("the twin's static rules refused a generated step %s: %+v\n%s", ref.Code, ref.Detail, stepJSON(step))
	}
	mp, ref := w.mem.Plan(step)
	if ref != nil {
		w.t.Fatalf("the twin refused a generated step %s: %+v\n%s", ref.Code, ref.Detail, stepJSON(step))
	}
	reply, err := w.store.Step(context.Background(), step)
	if err != nil {
		w.t.Fatalf("the store refused a generated step: %v\n%s", err, stepJSON(step))
	}
	if _, ref := w.mem.Commit(mp); ref != nil {
		w.t.Fatalf("the twin's commit: %+v", ref)
	}
	write := step.Epoch
	for _, e := range step.Entries {
		if e.Kind == "advance" {
			write, _ = NextDecimal(step.Epoch)
		}
	}
	var stored []LogLine
	if reply.Lines > 0 {
		msgs, err := w.fx.Client.XRange(context.Background(), fixtureLogKey(w.fx.Space, string(write)),
			string(reply.FirstSeq)+"-0", string(reply.LastSeq)+"-0").Result()
		if err != nil {
			w.t.Fatal(err)
		}
		for _, m := range msgs {
			stored = append(stored, LogLine{Seq: Decimal(strings.TrimSuffix(m.ID, "-0")),
				N: m.Values["n"].(string), D: m.Values["d"].(string)})
		}
	}
	nowMS := Decimal("0")
	if len(stored) > 0 {
		b, err := ParseLogBody(stored[0].D)
		if err != nil {
			w.t.Fatal(err)
		}
		nowMS = Decimal(b.MS)
	}
	lp, apply, ref := w.log.Plan(w.fx.Space, LogLinesInput{NowMS: nowMS, RequestEpoch: step.Epoch, WriteEpoch: write,
		Entries: mp.Entries, Request: step.Entries, Notes: step.Notes})
	if ref != nil {
		w.t.Fatalf("the twin's log refused %s: %+v\n%s", ref.Code, ref.Detail, stepJSON(step))
	}
	apply()
	if lp.FirstSeq != reply.FirstSeq || lp.LastSeq != reply.LastSeq || lp.LineCount != reply.Lines {
		w.t.Fatalf("seqs: twin %s..%s (%d), store %s..%s (%d)\n%s", lp.FirstSeq, lp.LastSeq, lp.LineCount,
			reply.FirstSeq, reply.LastSeq, reply.Lines, stepJSON(step))
	}
	if len(stored) != len(lp.Lines) {
		w.t.Fatalf("the store wrote %d lines, the twin %d\n%s", len(stored), len(lp.Lines), stepJSON(step))
	}
	for i := range stored {
		if stored[i] != lp.Lines[i] {
			w.t.Fatalf("line %s differs\nstore n=%s d=%s\ntwin  n=%s d=%s\nstep %s", stored[i].Seq,
				stored[i].N, stored[i].D, lp.Lines[i].N, lp.Lines[i].D, stepJSON(step))
		}
	}
	w.lines += len(stored)
}

func stepJSON(step Step) string {
	b, _ := json.Marshal(step)
	return string(b)
}

// The random words: every byte class cjson escapes, UTF-8, and plain text.
var logWorldWords = []string{"plain", "a/b", `quote"d`, `back\slash`, "tab\there", "new\nline", "\x01ctl\x1f",
	"del\x7f", "é and ü", " sep", "<tag>&amp;", "", "brief text with spaces", "日本"}

func (w *logWorld) word() string {
	s := logWorldWords[w.rng.Intn(len(logWorldWords))]
	if s == "" {
		return "e"
	}
	return s
}

var logWorldScores = []string{"1", "2.5", "2.50", "-7", "0.1", "1e3", "12345", "0", "3.25"}

// meta is a random meta value. A body d holds no JSON number (L2 1.1), so
// meta holds strings, booleans, nulls, arrays and objects; digits are strings.
func (w *logWorld) meta() json.RawMessage {
	switch w.rng.Intn(6) {
	case 0:
		return nil
	case 1:
		return json.RawMessage(`{}`)
	case 2:
		return json.RawMessage(fmt.Sprintf(`{"why":%s,"n":"%d"}`, strJSON(w.word()), w.rng.Intn(1000)))
	case 3:
		return json.RawMessage(`{"z":["1","0.1","-2.5e-7","12345678901234567890",true,null,"x/y"],"a":{"b":{},"c":[]}}`)
	case 4:
		return json.RawMessage(fmt.Sprintf(`{"kind":"judgment","type":%s,"list":[%s,"b"]}`, strJSON(w.word()), strJSON(w.word())))
	}
	return json.RawMessage(`{"f":"1e20","g":false,"h":null}`)
}

func strJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

var logWorldAbouts = []string{"p1", "p2", "p3", "p4", "p5", "p6", "p7", "p8"}

func (w *logWorld) about() string { return logWorldAbouts[w.rng.Intn(len(logWorldAbouts))] }

// fields draws n names from names, each with a random word.
func (w *logWorld) fields(n int, names ...string) map[string]string {
	out := map[string]string{}
	for i := 0; i < n; i++ {
		out[names[w.rng.Intn(len(names))]] = w.word()
	}
	return out
}

// The shared names and the per-id names never overlap (FIELDOVERLAP).
var logWorldShared = []string{"brief", "state"}
var logWorldOwn = []string{"fix", "finding", "own"}

func (w *logWorld) cell(table string) string {
	rows := w.rows[table]
	cols := logWorldColumns[table]
	return rows[w.rng.Intn(len(rows))] + ":" + cols[w.rng.Intn(len(cols))]
}

// occupied is a random cell of the table with members, or "".
func (w *logWorld) occupied(table string) string {
	var cells []string
	for c, ids := range w.cells[table] {
		if len(ids) != 0 {
			cells = append(cells, c)
		}
	}
	sort.Strings(cells)
	if len(cells) == 0 {
		return ""
	}
	return cells[w.rng.Intn(len(cells))]
}

func (w *logWorld) take(table, cell string, n int) []string {
	ids := append([]string(nil), w.cells[table][cell]...)
	w.rng.Shuffle(len(ids), func(i, j int) { ids[i], ids[j] = ids[j], ids[i] })
	if n > len(ids) {
		n = len(ids)
	}
	return ids[:n]
}

func (w *logWorld) move(table, id, to string) {
	key := table + "\x00" + id
	if from, ok := w.placed[key]; ok {
		list := w.cells[table][from]
		for i, x := range list {
			if x == id {
				w.cells[table][from] = append(list[:i:i], list[i+1:]...)
				break
			}
		}
		delete(w.placed, key)
	}
	if to != "" {
		w.cells[table][to] = append(w.cells[table][to], id)
		w.placed[key] = to
	}
	w.all[key] = true
}

// randomStep is one step of up to three entries of different tables and
// kinds, and maybe notes; the generator's view is updated as the step says.
func (w *logWorld) randomStep() Step {
	step := Step{Epoch: "0"}
	used := map[string]bool{}
	for k := 0; k < 1+w.rng.Intn(3); k++ {
		table := []string{"work", "aux"}[w.rng.Intn(2)]
		if used[table] {
			continue
		}
		used[table] = true
		if len(w.rows[table]) == 0 || w.rng.Intn(12) == 0 {
			row := fmt.Sprintf("r%d", len(w.rows[table]))
			step.Entries = append(step.Entries, Entry{Kind: "rows", Table: table, Add: []string{row}})
			w.rows[table] = append(w.rows[table], row)
			continue
		}
		from := w.occupied(table)
		switch choice := w.rng.Intn(10); {
		case choice < 4 || from == "":
			n := 1 + w.rng.Intn(40)
			e := Entry{Kind: "create", Table: table, To: w.cell(table), Meta: w.meta()}
			for i := 0; i < n; i++ {
				w.nextID++
				id := fmt.Sprintf("m%d", w.nextID)
				e.IDs = append(e.IDs, id)
				e.Scores = append(e.Scores, logWorldScores[w.rng.Intn(len(logWorldScores))])
				e.About = append(e.About, w.about())
			}
			if w.rng.Intn(2) == 0 {
				e.Set = w.fields(1+w.rng.Intn(2), logWorldShared...)
			}
			if w.rng.Intn(2) == 0 {
				for range e.IDs {
					e.Each = append(e.Each, w.fields(w.rng.Intn(3), logWorldOwn...))
				}
			}
			for _, id := range e.IDs {
				w.move(table, id, e.To)
			}
			step.Entries = append(step.Entries, e)
		case choice < 8:
			ids := w.take(table, from, 1+w.rng.Intn(20))
			e := Entry{Kind: "move", Table: table, From: from, IDs: ids, Meta: w.meta()}
			if w.rng.Intn(4) != 0 {
				e.To = w.cell(table)
			}
			for range ids {
				e.About = append(e.About, w.about())
				if w.rng.Intn(2) == 0 {
					e.Scores = append(e.Scores, logWorldScores[w.rng.Intn(len(logWorldScores))])
				}
			}
			if len(e.Scores) != len(ids) {
				e.Scores = nil
			}
			if w.rng.Intn(2) == 0 {
				e.Set = w.fields(1+w.rng.Intn(2), logWorldShared...)
			}
			if w.rng.Intn(3) == 0 {
				e.Unset = []string{[]string{"fix", "own", "finding", "absent"}[w.rng.Intn(4)]}
			}
			to := e.To
			if to == "" {
				to = from
			}
			for _, id := range ids {
				w.move(table, id, to)
			}
			step.Entries = append(step.Entries, e)
		default:
			ids := w.take(table, from, 1+w.rng.Intn(10))
			e := Entry{Kind: "remove", Table: table, From: from, IDs: ids}
			for range ids {
				e.About = append(e.About, w.about())
			}
			if w.rng.Intn(2) == 0 {
				e.Set = w.fields(1, logWorldShared...)
			}
			if w.rng.Intn(2) == 0 {
				e.Unset = []string{"fix", "own"}
			}
			for _, id := range ids {
				w.move(table, id, "")
			}
			step.Entries = append(step.Entries, e)
		}
	}
	for k := w.rng.Intn(4) - 1; k > 0; k-- {
		n := Note{Line: NoteLine{Kind: "note", Meta: w.meta()}, About: []string{}}
		if n.Line.Meta == nil {
			n.Line.Meta = json.RawMessage(`{}`)
		}
		for i := w.rng.Intn(5); i > 0; i-- {
			n.About = append(n.About, w.about())
		}
		step.Notes = append(step.Notes, n)
	}
	if step.Entries == nil {
		step.Entries = []Entry{}
	}
	return step
}

// TestLogTwinAgreesWithLuaOnRandomEntries (J9; J7's agreement, not its
// export): 10,000 random lines, of every kind the log writes, from creates,
// moves (with stays and score-only changes), removes with field deltas, row
// adds and notes whose words and meta carry every byte class cjson escapes:
// the twin's n and d equal the stored ones byte for byte, with the same seqs,
// and every history list equals the twin's.
func TestLogTwinAgreesWithLuaOnRandomEntries(t *testing.T) {
	t.Parallel()
	w := newLogWorld(t, 20260930)
	for w.lines < 10000 {
		w.apply(w.randomStep())
		// Reads throughout the run, while the histories are short enough for
		// atomic cardlines answers and long enough for pages.
		if w.ops%150 == 0 {
			w.compareReads("0", uint64(w.lines))
		}
	}
	w.compareReads("0", uint64(w.lines))
	for _, about := range logWorldAbouts {
		got := logDraftHistory(t, w.fx.Client, w.fx.Space, "0", about)
		want := w.log.History(w.fx.Space, "0", about)
		if len(got) != len(want) {
			t.Fatalf("history %s: store %d seqs, twin %d", about, len(got), len(want))
		}
		for i := range got {
			if got[i] != string(want[i]) {
				t.Fatalf("history %s at %d: store %s, twin %s", about, i, got[i], want[i])
			}
		}
	}
	w.advance()
	for _, kind := range []string{"last", "lines/atomic", "lines/page", "cardlines/atomic", "cardlines/page", "lineat"} {
		if w.reads[kind] == 0 {
			t.Errorf("no %s answer was compared", kind)
		}
	}
	for _, code := range []string{"CURSOR", "BUDGET", "REQUEST", "LOGID"} {
		if w.reads["refused/"+code] == 0 {
			t.Errorf("no %s refusal was compared", code)
		}
	}
	t.Logf("the twin and the store agree on %d lines over %d steps, and on the reads %v", w.lines, w.ops, w.reads)
}

// TestReplayWritesTablesAndCompares (L2 6, J5 at 10,000 cards; 100,000 is
// owed): an epoch of 10,000 cards created, moved and removed with field
// deltas, rows and notes, read back in lines pages, replayed into a Mem twin,
// equals the store's cells, records and rows, and each primary's history
// list; the twin's own log replays to the same tables. The epoch is closed to
// writers while it replays (the model's MCSetTableLogReplayClosed).
func TestReplayWritesTablesAndCompares(t *testing.T) {
	t.Parallel()
	w := newLogWorld(t, 31415926)
	for w.nextID < 10000 {
		w.apply(w.randomStep())
	}
	ctx := context.Background()
	lines, err := ReadEpochLines(ctx, w.store, w.fx.Space, "0", 5000)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := ReplayLines("0", lines)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(lines, w.log.Stored(w.fx.Space, "0")) {
		t.Fatal("the pages of lines differ from the twin's log")
	}
	target := NewMem()
	snap := w.fx.SemanticSnapshot(t)
	for _, table := range []string{"work", "aux"} {
		def := snap.Definitions[table]
		def.Columns = logWorldColumns[table]
		if err := target.DefineTable(w.fx.Space, table, def); err != nil {
			t.Fatal(err)
		}
	}
	if err := target.SetActiveEpoch(w.fx.Space, "0"); err != nil {
		t.Fatal(err)
	}
	if err := replayed.Into(target, w.fx.Space); err != nil {
		t.Fatal(err)
	}
	got, err := target.Snapshot(w.fx.Space)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"work", "aux"} {
		want, have := snap.Epochs["0"].Tables[table], got.Epochs["0"].Tables[table]
		if !reflect.DeepEqual(want.Rows, have.Rows) {
			t.Fatalf("%s rows: store %v, replay %v", table, want.Rows, have.Rows)
		}
		if a, b := nonEmptyCells(want.Cells), nonEmptyCells(have.Cells); !reflect.DeepEqual(a, b) {
			t.Fatalf("%s cells differ: store %d cells, replay %d", table, len(a), len(b))
		}
		if len(want.Records) != len(have.Records) {
			t.Fatalf("%s records: store %d, replay %d", table, len(want.Records), len(have.Records))
		}
		for id, rec := range want.Records {
			if r := have.Records[id]; !reflect.DeepEqual(normalRecord(rec), normalRecord(r)) {
				t.Fatalf("%s record %s: store %+v, replay %+v", table, id, rec, r)
			}
		}
	}
	for _, about := range logWorldAbouts {
		store := logDraftHistory(t, w.fx.Client, w.fx.Space, "0", about)
		var mine []string
		for _, s := range replayed.Histories[about] {
			mine = append(mine, string(s))
		}
		if !reflect.DeepEqual(store, mine) {
			t.Fatalf("history %s: store %d seqs, replay %d", about, len(store), len(mine))
		}
	}
	t.Logf("replayed %d lines of %d cards into the twin; cells, records, rows and histories equal", replayed.Lines, w.nextID)
}

func nonEmptyCells(cells map[string]map[string]map[string]string) map[string]map[string]string {
	out := map[string]map[string]string{}
	for row, cols := range cells {
		for col, ids := range cols {
			if len(ids) != 0 {
				out[row+":"+col] = ids
			}
		}
	}
	return out
}

// normalRecord is a record with an empty field map and a nil one alike.
func normalRecord(r MemRecord) MemRecord {
	if len(r.Fields) == 0 {
		r.Fields = nil
	}
	return r
}

// readBoth runs one read on the store and on the twin and compares the
// refusal code, or the answer: an atomic answer's list, or a page's items,
// next, through and exhausted, each as decoded JSON (the store's cjson and
// the twin order object keys differently). It returns the store's reply.
func (w *logWorld) readBoth(plan ReadPlan) (ReadReply, string) {
	w.t.Helper()
	plan.Space = w.fx.Space
	got, err := w.store.Read(context.Background(), plan)
	want, ref := w.log.Read(w.fx.Space, plan)
	storeCode, twinCode := "", ""
	var refusal *Refusal
	if errors.As(err, &refusal) {
		storeCode = refusal.Code
	} else if err != nil {
		w.t.Fatalf("store read: %v", err)
	}
	if ref != nil {
		twinCode = ref.Code
	}
	q, _ := json.Marshal(plan.Queries[0])
	if storeCode != twinCode {
		w.t.Fatalf("%s read %s: store %q, twin %q", plan.Mode, q, storeCode, twinCode)
	}
	if storeCode != "" {
		w.reads["refused/"+storeCode]++
		return got, storeCode
	}
	if plan.Mode == "page" {
		if a, b := logSemantic(w.t, []any{got.Items, got.Next, got.Through, got.Exhausted}),
			logSemantic(w.t, []any{want.Items, want.Next, want.Through, want.Exhausted}); !reflect.DeepEqual(a, b) {
			w.t.Fatalf("page %s:\nstore %v\ntwin  %v", q, a, b)
		}
	} else if a, b := logSemantic(w.t, got.Answers), logSemantic(w.t, want.Answers); !reflect.DeepEqual(a, b) {
		w.t.Fatalf("atomic %s:\nstore %v\ntwin  %v", q, a, b)
	}
	mode := plan.Mode
	if mode == "" {
		mode = "atomic"
	}
	kind := plan.Queries[0].Kind
	if kind != "last" {
		kind += "/" + mode
	}
	w.reads[kind]++
	return got, ""
}

// lineAtBoth reads one line by seq through the store's L.read_line_at (the
// probe kind) and the twin's LineAt, and compares the code or the decoded
// line: its kind, ids, about and meta.
func (w *logWorld) lineAtBoth(epoch Decimal, seq string) {
	w.t.Helper()
	wire, err := w.fx.Client.FCall(context.Background(), "ns_tset_lineat_probe", []string{}, Version,
		fmt.Sprintf(`{"epoch":%q,"space":%q,"queries":[{"kind":"lineat","seq":%q}]}`, epoch, w.fx.Space, seq)).Result()
	if err != nil {
		w.t.Fatalf("line-at probe: %v", err)
	}
	var reply map[string]json.RawMessage
	if err := json.Unmarshal([]byte(wire.(string)), &reply); err != nil {
		w.t.Fatal(err)
	}
	storeCode := logDraftCode(w.t, reply)
	line, ref := w.log.LineAt(w.fx.Space, epoch, seq)
	twinCode := ""
	if ref != nil {
		twinCode = ref.Code
	}
	if storeCode != twinCode {
		w.t.Fatalf("line at %s@%s: store %q, twin %q", seq, epoch, storeCode, twinCode)
	}
	if storeCode != "" {
		w.reads["refused/"+storeCode]++
		return
	}
	var answers []struct {
		Seq      string          `json:"seq"`
		LineKind string          `json:"line_kind"`
		IDs      []string        `json:"ids"`
		About    []string        `json:"about"`
		Meta     json.RawMessage `json:"meta"`
	}
	if err := json.Unmarshal(reply["answers"], &answers); err != nil || len(answers) != 1 {
		w.t.Fatalf("line-at answers %s: %v", reply["answers"], err)
	}
	body, err := ParseLogBody(line.D)
	if err != nil {
		w.t.Fatal(err)
	}
	a := answers[0]
	norm := func(s []string) []string {
		if len(s) == 0 {
			return nil
		}
		return s
	}
	meta := json.RawMessage("null")
	if len(body.Meta) != 0 {
		meta = body.Meta
	}
	if a.Seq != string(line.Seq) || a.LineKind != LogWords[body.K] || !reflect.DeepEqual(norm(a.IDs), norm(body.IDs)) ||
		!reflect.DeepEqual(norm(a.About), norm(body.About)) ||
		!reflect.DeepEqual(logSemantic(w.t, a.Meta), logSemantic(w.t, meta)) {
		w.t.Fatalf("line at %s@%s:\nstore %+v meta %s\ntwin  %s", seq, epoch, a, a.Meta, line.D)
	}
	w.reads["lineat"]++
}

// compareReads runs a round of random reads of every kind the log serves on
// both sides, at an epoch whose tail is known: last; lines atomic and paged
// with an optional through_seq, ids_limit and bytes_limit, cursors past the
// tail included; cardlines atomic and paged over a few abouts with a
// projection, meta or not, a repeated about and a changed cursor; and lines by
// seq, past the tail and non-canonical included (read 4812 M2).
func (w *logWorld) compareReads(epoch Decimal, tail uint64) {
	w.t.Helper()
	w.readBoth(ReadPlan{Epoch: epoch, Queries: []ReadQuery{{Kind: "last"}}})
	seq := func(n uint64) Decimal { return Decimal(strconv.FormatUint(n, 10)) }
	pick := func(max uint64) uint64 { return uint64(w.rng.Int63n(int64(max) + 1)) }
	for i := 0; i < 4; i++ {
		q := ReadQuery{Kind: "lines", AfterSeq: seq(pick(tail + 1)), Limit: 1 + w.rng.Intn(40)}
		if w.rng.Intn(3) == 0 {
			through := seq(pick(tail + 1))
			q.ThroughSeq = &through
		}
		if w.rng.Intn(3) == 0 {
			q.IDsLimit = 1 + w.rng.Intn(100)
		}
		if w.rng.Intn(4) == 0 {
			q.BytesLimit = 4096 + w.rng.Intn(16000)
		}
		mode := []string{"", "page"}[i%2]
		reply, code := w.readBoth(ReadPlan{Epoch: epoch, Mode: mode, Queries: []ReadQuery{q}})
		for p := 0; p < 2 && mode == "page" && code == "" && !reply.Exhausted; p++ {
			var next, through string
			if json.Unmarshal(reply.Next, &next) != nil || json.Unmarshal(reply.Through, &through) != nil {
				w.t.Fatalf("a lines page's next %s and through %s", reply.Next, reply.Through)
			}
			n, _ := strconv.ParseUint(next, 10, 64)
			th := Decimal(through)
			q.AfterSeq, q.ThroughSeq = seq(n-1), &th
			reply, code = w.readBoth(ReadPlan{Epoch: epoch, Mode: mode, Queries: []ReadQuery{q}})
		}
	}
	names := append(append([]string{}, logWorldShared...), logWorldOwn...)
	for i := 0; i < 4; i++ {
		abouts := []string{w.about()}
		for k := w.rng.Intn(3); k > 0; k-- {
			if a := w.about(); a != abouts[0] {
				abouts = append(abouts, a)
			}
		}
		if w.rng.Intn(8) == 0 {
			abouts = append(abouts, "p-none")
		}
		if w.rng.Intn(10) == 0 {
			abouts = append(abouts, abouts[0]) // a repeated about is REQUEST
		}
		fields := []string{}
		for _, f := range names {
			if w.rng.Intn(3) == 0 {
				fields = append(fields, f)
			}
		}
		q := ReadQuery{Kind: "cardlines", Abouts: abouts, Fields: fields, IncludeMeta: w.rng.Intn(2) == 0,
			Limit: 1 + w.rng.Intn(60)}
		mode := []string{"", "page"}[i%2]
		reply, code := w.readBoth(ReadPlan{Epoch: epoch, Mode: mode, Queries: []ReadQuery{q}})
		for p := 0; p < 3 && mode == "page" && code == "" && !reply.Exhausted; p++ {
			var cursor CardCursor
			if err := json.Unmarshal(reply.Next, &cursor); err != nil {
				w.t.Fatalf("a cardlines page's cursor %s: %v", reply.Next, err)
			}
			q.Cursor = &cursor
			if p == 2 {
				// A cursor whose identity changed is CURSOR on both.
				bad := cursor
				bad.IncludeMeta = !q.IncludeMeta
				w.readBoth(ReadPlan{Epoch: epoch, Mode: mode, Queries: []ReadQuery{{Kind: "cardlines", Abouts: abouts,
					Fields: fields, IncludeMeta: q.IncludeMeta, Limit: q.Limit, Cursor: &bad}}})
			}
			reply, code = w.readBoth(ReadPlan{Epoch: epoch, Mode: mode, Queries: []ReadQuery{q}})
		}
	}
	for _, s := range []string{string(seq(1 + pick(tail))), string(seq(tail + 1 + pick(5))), "01"} {
		if s == "0" {
			s = "1"
		}
		w.lineAtBoth(epoch, s)
	}
}

// advance ends epoch 0 with an advance on both sides, restores a row of each
// table and writes a card and a note in epoch 1, each line compared as apply
// compares; then the reads of epoch 1 are compared (L2 2: the new epoch's log
// starts at seq 1 with the advance line).
func (w *logWorld) advance() {
	w.t.Helper()
	w.apply(Step{Epoch: "0", Entries: []Entry{{Kind: "advance", AdvanceFrom: "0"},
		{Kind: "rows", Table: "work", Add: []string{"r0"}}, {Kind: "rows", Table: "aux", Add: []string{"r0"}}},
		Notes: []Note{{Line: NoteLine{Kind: "note", Meta: json.RawMessage(`{"why":"advance"}`)}, About: []string{"p1"}}}})
	w.apply(Step{Epoch: "1", Entries: []Entry{{Kind: "create", Table: "work", To: "r0:a", IDs: []string{"after-advance"},
		Scores: []string{"1"}, About: []string{"p1"}, Set: map[string]string{"brief": "b"}}}})
	w.compareReads("1", 5)
	w.lineAtBoth("1", "1")
}
