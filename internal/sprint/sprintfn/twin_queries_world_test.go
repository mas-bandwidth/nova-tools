package sprintfn

import (
	"encoding/json"
	"math/rand"
	"sort"
	"strconv"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// A world for the query tests: a twin whose tables are written through its
// own steps, whose derived indexes are kept by a stand-in for X (IT13) that
// computes them from the tables by IT02's own definition after every step, and
// whose notes are turned into lines and into jopen by a stand-in for J (IT15).
// Keys that no step of the sprint's own writes yet (missing, wait:n of a need
// with no record, askwait, the quarantine, the clock) are seeded, as a part
// would write them.

// card is one card of a fixture: where it is, its score and its fields.
type card struct {
	table, row, col, id, score string
	fields                     map[string]string
}

type qworld struct {
	t       *testing.T
	tw      *Twin
	m       *tset.Mem
	log     *MemLog
	derived map[string]bool // the index keys the stand-in X has written
	epoch   string
}

func fields(kv ...string) map[string]string {
	out := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		out[kv[i]] = kv[i+1]
	}
	return out
}

func newQWorld(t *testing.T) *qworld {
	t.Helper()
	w := &qworld{t: t, derived: map[string]bool{}, epoch: "0"}
	phases := Phases{
		XPre: func(*State, *Request, *Before) *Refusal { return nil },
		XCmds: func(st *State, tp TablePlan, lp LogPlan) []Cmd {
			return w.syncIndexes(st, tp)
		},
		JDecide: func(st *State, in []NoteReq, obs *Before) ([]tset.Note, JPlan, *Refusal) {
			var notes []tset.Note
			var jp JPlan
			for i, n := range in {
				meta, _ := json.Marshal(map[string]string{noteMetaType: n.Type, noteMetaCause: n.Cause})
				notes = append(notes, tset.Note{Line: tset.NoteLine{Kind: noteKind, Meta: meta}, About: n.Subjects})
				jp.Notes = append(jp.Notes, JNote{Index: i, Req: n})
			}
			return notes, jp, nil
		},
		JCmds: func(st *State, jp JPlan, lp LogPlan) []Cmd {
			var cmds []Cmd
			for _, n := range jp.Notes {
				id := noteIDPrefix + string(lp.NoteSeqs[n.Index])
				for _, s := range n.Req.Subjects {
					cmds = append(cmds, Command("HSET", w.k("jopen:"+s), kindHash, n.Req.Type+"|"+n.Req.Cause, id))
				}
				cmds = append(cmds, Command("ZADD", w.k("jnotes"), kindZSet, "1", id))
			}
			return cmds
		},
	}
	w.tw, w.m, w.log = newTestTwin(t, phases)
	w.tw.UseQueries()
	return w
}

// k is a per-epoch sprint key of the world.
func (w *qworld) k(name string) string { return testPrefix + "sprint:" + name + "@" + w.epoch }

func (w *qworld) step(req *Request) *StepReply {
	w.t.Helper()
	req.Epoch = tset.Decimal(w.epoch)
	req.Meta = Meta{Verb: "test"}
	return mustStep(w.t, w.tw, req)
}

// rows adds rows to a table.
func (w *qworld) rows(table string, names ...string) {
	w.t.Helper()
	w.step(&Request{Body: Body{Entries: []tset.Entry{{Kind: "rows", Table: table, Add: names}}}})
}

// cards creates the cards, one entry a cell.
func (w *qworld) cards(cs ...card) {
	w.t.Helper()
	type cell struct{ table, to string }
	var order []cell
	by := map[cell][]card{}
	for _, c := range cs {
		k := cell{c.table, c.row + ":" + c.col}
		if _, ok := by[k]; !ok {
			order = append(order, k)
		}
		by[k] = append(by[k], c)
	}
	var entries []tset.Entry
	for _, k := range order {
		e := tset.Entry{Kind: "create", Table: k.table, To: k.to}
		for _, c := range by[k] {
			e.IDs, e.Scores, e.About = append(e.IDs, c.id), append(e.Scores, c.score), append(e.About, c.id)
			f := c.fields
			if f == nil {
				f = map[string]string{}
			}
			e.Each = append(e.Each, f)
		}
		entries = append(entries, e)
	}
	w.step(&Request{Body: Body{Entries: entries}})
}

// move moves cards of a table from one cell to another.
func (w *qworld) move(table, from, to string, ids ...string) {
	w.t.Helper()
	w.step(&Request{Body: Body{Entries: []tset.Entry{{Kind: "move", Table: table, From: from, To: to, IDs: ids, About: ids}}}})
}

// note opens a note on the subjects through J.
func (w *qworld) note(typ, cause string, subjects ...string) {
	w.t.Helper()
	w.step(&Request{Body: Body{Notes: []NoteReq{{Op: "open", Type: typ, Cause: cause, Subjects: subjects}}}})
}

// seed writes keys no derivation writes, as a part would.
func (w *qworld) seed(cmds ...Cmd) {
	w.t.Helper()
	if _, ref := w.tw.keys.check(testPrefix, cmds); ref != nil {
		w.t.Fatalf("seed: %v", ref)
	}
	w.tw.keys.apply(cmds)
}

func (w *qworld) zadd(name string, pairs ...string) Cmd {
	return Command("ZADD", w.k(name), kindZSet, pairs...)
}

func (w *qworld) hset(name string, kv ...string) Cmd {
	return Command("HSET", w.k(name), kindHash, kv...)
}

func (w *qworld) hsetBare(name string, kv ...string) Cmd {
	return Command("HSET", testPrefix+"sprint:"+name, kindHash, kv...)
}

// syncIndexes is the stand-in for X: the derived indexes and the due set, from
// the tables as they will be after the step, by IT02's definition from scratch,
// as commands that move the keyspace to them. The twin plans the tables without
// writing them (Mem.Plan, S15) and asks X for its commands before it commits,
// so the tables after the step are the snapshot with the plan's entries laid
// over it: each id the plan creates or moves has the place, score and fields
// the plan gives it, and each it removes is gone.
func (w *qworld) syncIndexes(st *State, tp TablePlan) []Cmd {
	snap, err := w.m.Snapshot(testPrefix)
	if err != nil {
		w.t.Fatal(err)
	}
	type key struct{ table, id string }
	by := map[key]*sprint.IndexCard{}
	for table, tbl := range snap.Epochs[tset.Decimal(w.epoch)].Tables {
		for id, r := range tbl.Records {
			if r.Column == "" {
				continue
			}
			score, _ := strconv.ParseFloat(r.Score, 64)
			fields := make(map[string]string, len(r.Fields))
			for f, v := range r.Fields {
				fields[f] = v
			}
			by[key{table, id}] = &sprint.IndexCard{Table: table, ID: id, Row: r.Row, Col: r.Column, Score: score, Fields: fields}
		}
	}
	for _, pe := range tp.Entries {
		switch pe.Entry.Kind {
		case "create", "move", "remove":
		default:
			continue
		}
		for i, id := range pe.Entry.IDs {
			k := key{pe.Entry.Table, id}
			if pe.Entry.Kind == "remove" {
				delete(by, k)
				continue
			}
			c := by[k]
			if c == nil {
				c = &sprint.IndexCard{Table: pe.Entry.Table, ID: id, Fields: map[string]string{}}
				by[k] = c
			}
			a := pe.After[i]
			if a.Place != nil {
				c.Row, c.Col = a.Place.Row, a.Place.Col
			}
			if a.Score != "" {
				c.Score, _ = strconv.ParseFloat(a.Score, 64)
			}
			for f, v := range a.Fields {
				if v.Present {
					c.Fields[f] = v.Value
				} else {
					delete(c.Fields, f)
				}
			}
		}
	}
	var cards []*sprint.IndexCard
	for _, c := range by {
		cards = append(cards, c)
	}
	want, err := sprint.IndexDefinition(cards)
	if err != nil {
		w.t.Fatal(err)
	}
	wantKeys := map[string]map[string]float64{}
	for k, members := range want {
		name := k.String()
		wantKeys[w.k(name)] = members
	}
	var cmds []Cmd
	var keys []string
	for key := range w.derived {
		keys = append(keys, key)
	}
	for key := range wantKeys {
		if !w.derived[key] {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		have := map[string]float64{}
		if v := st.Keys.ks.vals[key]; v != nil {
			for m, s := range v.zset {
				have[m] = s
			}
		}
		var rem, add []string
		members := wantKeys[key]
		for m := range have {
			if _, ok := members[m]; !ok {
				rem = append(rem, m)
			}
		}
		for m, s := range members {
			if h, ok := have[m]; !ok || h != s {
				add = append(add, formatScore(s), m)
			}
		}
		if len(rem) > 0 {
			sort.Strings(rem)
			cmds = append(cmds, Command("ZREM", key, kindZSet, rem...))
		}
		if len(add) > 0 {
			cmds = append(cmds, Command("ZADD", key, kindZSet, sortPairs(add)...))
		}
		w.derived[key] = len(members) > 0 || w.derived[key]
	}
	return cmds
}

// sortPairs orders a flat score, member list by member.
func sortPairs(flat []string) []string {
	type pair struct{ s, m string }
	var ps []pair
	for i := 0; i+1 < len(flat); i += 2 {
		ps = append(ps, pair{flat[i], flat[i+1]})
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i].m < ps[j].m })
	out := make([]string, 0, len(flat))
	for _, p := range ps {
		out = append(out, p.s, p.m)
	}
	return out
}

// standard is the world most tests read: two streams, two members, two
// readers, and cards in every cell the queries look at.
//
//	stream s1 (work):  waiting  g1 (a sentinel, score 50), p1 (10), p2 (20), p3 (60),
//	                            w1 (70, waits for p1 and ghost), w2 (80, waits for ghost)
//	                   ready    f1 (30), f2 (55), a1 (35, attempt 2)
//	                   working  k1 (5, attempt 1, its work card k1.w1 at m1 working)
//	                   review   v1 (8, attempt 1, read cards v1.r1.r1 and v1.r1.r2)
//	                   merging  q1 (9, its merge card q1)
//	                   landed   d1 (1)
//	stream s2 (work):  waiting  h1 (sentinel, 5), ready x1 (3, attempt 1, withdrawn work card x1.w1)
//
// ghost has no record: it is in {p}missing@e, and w1 and w2 are in its wait set.
//
// Stream s2 is stopped on a cross need, p2: its control card names it in `other`,
// where the writer puts it (steps_merge.go), and standardWith puts it elsewhere.
func standard(t *testing.T) *qworld {
	t.Helper()
	return standardWith(t, fields("state", "stopped", "cause", "cross", "other", "p2"))
}

// standardWith is the standard world with the fields of s2's control card given.
func standardWith(t *testing.T, ctlS2 map[string]string) *qworld {
	t.Helper()
	w := newQWorld(t)
	w.rows(sprint.Work, "s1", "s2")
	w.rows(sprint.Merge, "s1", "s2")
	w.rows(sprint.Fleet, "m1", "m2")
	w.rows(sprint.Readers, "r1", "r2")
	wk := func(row, col, id, score string, kv ...string) card {
		return card{sprint.Work, row, col, id, score, fields(kv...)}
	}
	w.cards(
		wk("s1", "waiting", "g1", "50", "kind", "sentinel", "open", "0"),
		wk("s1", "waiting", "p1", "10", "kind", "work", "open", "0"),
		wk("s1", "waiting", "p2", "20", "kind", "work", "open", "0"),
		wk("s1", "waiting", "p3", "60", "kind", "work", "open", "0"),
		wk("s1", "waiting", "w1", "70", "kind", "work", "open", "2", "needs", "p1,ghost"),
		wk("s1", "waiting", "w2", "80", "kind", "work", "open", "1", "needs", "ghost"),
		wk("s1", "ready", "f1", "30", "kind", "work", "attempt", "0"),
		wk("s1", "ready", "f2", "55", "kind", "work", "attempt", "0"),
		wk("s1", "ready", "a1", "35", "kind", "work", "attempt", "2"),
		wk("s1", "working", "k1", "5", "kind", "work", "attempt", "1"),
		wk("s1", "review", "v1", "8", "kind", "work", "attempt", "1", "rcards", "v1.r1.r1,v1.r1.r2"),
		wk("s1", "merging", "q1", "9", "kind", "work"),
		wk("s1", "landed", "d1", "1", "kind", "work"),
		wk("s2", "waiting", "h1", "5", "kind", "sentinel", "open", "0"),
		wk("s2", "ready", "x1", "3", "kind", "work", "attempt", "1"),
		card{sprint.Fleet, "m1", "working", "k1.w1", "1", fields("member", "m1", "primary", "k1", "due_unfinished", "9000")},
		card{sprint.Fleet, "m1", "ready", "a1.w2", "2", fields("member", "m1", "primary", "a1", "due_untaken", "8000")},
		card{sprint.Fleet, "m2", "withdrawn", "x1.w1", "3", fields("member", "m2", "primary", "x1")},
		card{sprint.Fleet, "m1", "ctl", "ctl-m1", "1", fields("status", "up")},
		card{sprint.Fleet, "m2", "ctl", "ctl-m2", "1", fields("status", "down")},
		card{sprint.Merge, "s1", "ctl", "ctl-s1", "1", fields("state", "merging", "due_mergeidle", "7000")},
		card{sprint.Merge, "s2", "ctl", "ctl-s2", "1", ctlS2},
		card{sprint.Merge, "s1", "queued", "q1", "9", fields("ci", "green")},
		card{sprint.Merge, "s2", "stuck", "st1", "1", nil},
		card{sprint.Merge, "s2", "stuck", "st2", "2", nil},
		card{sprint.Merge, "s2", "stuck", "st3", "3", nil},
		card{sprint.Readers, "r1", "asked", "v1.r1.r1", "1", fields("primary", "v1", "due_unbegun", "6000")},
		card{sprint.Readers, "r2", "reading", "v1.r1.r2", "2", fields("primary", "v1", "due_unreported", "6500")},
	)
	w.seed(
		w.zadd("missing", "100", "ghost"),
		w.zadd("wait:ghost", "0", "w1", "0", "w2"),
		w.zadd("askwait", "5", "v1"),
		w.hset("dropping", "s2", "op-drop"),
		w.hset("parked", "agenda-key-1", "note-9"),
		w.hset("tick", "cur", "77", "behind_n", "0"),
		w.hsetBare("clock", "stopped_ms", "1000", "stopped_since_ms", "", "stophold_ms", "0", "due_since_ms", "", "stopraised_ms", "0"),
		w.hsetBare("lease", "owner", "tok", "name", "loop", "until_ms", "9999999999999", "gen", "4"),
		w.hsetBare("heartbeat", "tick_at", "5", "ticks", "9"),
	)
	return w
}

// query runs a composite query on the world and returns its result, failing on
// a refusal.
func (w *qworld) query(q sprint.SprintQ) (QueryResult, QueryCharge) {
	w.t.Helper()
	res, c, err := w.tw.QueryFull(q)
	if err != nil {
		w.t.Fatalf("%s: %v", q.Kind, err)
	}
	return res, c
}

// refused runs a query that must be refused and returns the refusal.
func (w *qworld) refused(q sprint.SprintQ) *Refusal {
	w.t.Helper()
	_, _, err := w.tw.QueryFull(q)
	ref, ok := err.(*Refusal)
	if !ok {
		w.t.Fatalf("%s: %v, want a refusal", q.Kind, err)
	}
	return ref
}

func ids(list ...string) sprint.IDSource { return sprint.IDSource{Kind: sprint.SourceIDs, IDs: list} }

func head(key string, limit int) sprint.IDSource {
	return sprint.IDSource{Kind: sprint.SourceHead, Key: key, Limit: limit}
}

func recordIDs(recs []Record) []string {
	out := []string{}
	for _, r := range recs {
		out = append(out, r.ID)
	}
	return out
}

func fieldValue(r Record, name string) string { return recordField(r, name) }

// mustJSONText is a value as JSON text, for a comparison.
func mustJSONText(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// randomQuery is a random query as the twin takes it and as the wire has it.
type randomQuery struct {
	Q   sprint.SprintQ
	Enc SprintQuery
}

// randomQueries are n random queries over the standard world's names that
// pass the static checks: every kind, every source, every follow, projections
// and limits of every size, ids that do not exist and streams that are not
// there.
func randomQueries(w *qworld, seed int64, n int) []randomQuery {
	rng := rand.New(rand.NewSource(seed))
	universe := []string{"g1", "p1", "p2", "p3", "w1", "w2", "f1", "f2", "a1", "k1", "v1", "q1", "d1", "h1", "x1", "ghost", "nobody",
		"k1.w1", "a1.w2", "x1.w1", "ctl-m1", "ctl-m2", "ctl-s1", "ctl-s2", "v1.r1.r1", "v1.r1.r2", "st1", "st2", "st3"}
	tables := []string{sprint.Work, sprint.Fleet, sprint.Merge, sprint.Readers, "nosuch"}
	fieldNames := []string{"attempt", "open", "kind", "needs", "rcards", "primary", "state", "status", "member", "zzz"}
	heads := []string{"elig:s1", "elig:s2", "fresh:s1", "again:s1", "sent:s1", "wait:ghost", "wait:p1", "missing", "askwait", "s1:waiting", "s1:ready",
		"s2:stuck", "m1:ready", "m2:withdrawn", "s9:ready", "elig:s9"}
	pick := func(list []string, n int) []string {
		perm := rng.Perm(len(list))
		out := []string{}
		for _, i := range perm[:min(n, len(list))] {
			out = append(out, list[i])
		}
		return out
	}
	note := lastNote(w)
	lines := len(w.log.Lines(testPrefix, "0"))
	source := func(jnote bool) sprint.IDSource {
		switch k := rng.Intn(4); {
		case jnote && k < 2:
			return ids(note)
		case jnote:
			return head("jnotes", 1+rng.Intn(3))
		case k == 0:
			return head(heads[rng.Intn(len(heads))], 1+rng.Intn(6))
		case k == 1:
			return sprint.IDSource{Kind: sprint.SourceLine, Seq: uint64(1 + rng.Intn(lines+1)), About: rng.Intn(2) == 0, Offset: rng.Intn(2), Limit: rng.Intn(5)}
		}
		return ids(pick(universe, rng.Intn(8))...)
	}
	follow := func() []string {
		out := []string{}
		for _, f := range sprint.Follows {
			if rng.Intn(3) == 0 {
				out = append(out, f)
			}
		}
		return out
	}
	var out []randomQuery
	for len(out) < n {
		q := sprint.SprintQ{Fields: pick(fieldNames, rng.Intn(4))}
		switch rng.Intn(8) {
		case 0:
			q.Kind, q.Table, q.Source, q.Follow = sprint.QueryRelated, tables[rng.Intn(len(tables))], source(false), follow()
		case 1:
			q.Kind, q.Stream = sprint.QueryFront, []string{"s1", "s2", "s9"}[rng.Intn(3)]
			for _, h := range pick([]string{sprint.HeadEligBelow, sprint.HeadFreshBelow, sprint.HeadFreshAbove, sprint.HeadAgain}, rng.Intn(5)) {
				q.Heads = append(q.Heads, sprint.HeadQ{Index: h, Limit: 1 + rng.Intn(5), Follow: follow()})
			}
		case 2:
			q.Kind, q.Source, q.Limit = sprint.QueryWaiters, source(false), 1+rng.Intn(4)
		case 3:
			q.Kind, q.Limit, q.Units = sprint.QueryStreams, rng.Intn(4), rng.Intn(3)
		case 4:
			q.Kind, q.Units = []string{sprint.QueryFleet, sprint.QueryReaders}[rng.Intn(2)], rng.Intn(3)
		case 5:
			q.Kind, q.Source, q.Limit = sprint.QueryNeedchain, source(false), 1+rng.Intn(8)
		default:
			q.Kind, q.Source, q.Subjects = sprint.QueryJnote, source(true), rng.Intn(4)*3
		}
		if q.Fields == nil {
			q.Fields = []string{}
		}
		if ValidateSprintQ(q) != nil {
			continue
		}
		enc, ref := EncodeSprintQ(q)
		if ref != nil {
			w.t.Fatalf("%s: %v", q.Kind, ref)
		}
		out = append(out, randomQuery{q, enc})
	}
	return out
}
