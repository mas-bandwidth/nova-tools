package machine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/stepbuild"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
	"github.com/redis/go-redis/v9"
)

// The tests' world: the composed twin (sprintfn.Twin over tset.Mem and the log
// stub, with every phase and part of the stack), a clock the test moves, a
// client that counts round trips, test rules named after the real rule keys
// so that real lines queue them (2.1), and a step builder that turns their
// plans into bodies. The real rules and the real builder (8.0's step.Build)
// are other items'; what these tests pin is the loop.

var testNames = sprint.Names{Prefix: "t:"}

var testColumns = map[string][]string{
	sprint.Work:    {"waiting", "ready", "working", "review", "merging", "landed"},
	sprint.Readers: {"asked", "reading", "ok", "broken"},
	sprint.Merge:   {"queued", "merged", "stuck", "returned", "ctl"},
	sprint.Fleet:   {"ready", "working", "withdrawn", "ok", "failed", "ctl"},
}

// clock is the test's time: the twin's calls and the loops read it.
type clock struct {
	mu sync.Mutex
	at time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.at }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(d)
}

// world is one test's store and clock. c is the store the steps and reads go
// to: the twin, or a real store (tick_functional_test.go's store world). tw,
// mem and log are the twin's parts, nil on a store world; raw is the store
// world's own connection, for the sprint's raw keys, nil on the twin.
type world struct {
	t   *testing.T
	c   sprintfn.Client
	tw  *sprintfn.Twin
	mem *tset.Mem
	log *sprintfn.LogStub
	raw *redis.Client
	clk *clock
	gen uint64 // the generation a test's own tick steps carry
}

func newWorld(t *testing.T) *world {
	t.Helper()
	m := tset.NewMem()
	for _, table := range []string{sprint.Work, sprint.Readers, sprint.Merge, sprint.Fleet} {
		if err := m.DefineTable(testNames.Prefix, table, tset.TableDefinition{Columns: testColumns[table],
			MemberPrefix: testNames.TSetMemberPrefix(table), EpochKey: testNames.EpochKey(), EpochField: "n"}); err != nil {
			t.Fatalf("define %s: %v", table, err)
		}
	}
	log := sprintfn.NewLogStub()
	tw := sprintfn.NewTwin(m, log, testNames)
	tw.UseQueries()
	clk := &clock{at: time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)}
	tw.SetClock(clk.now)
	w := &world{t: t, c: tw, tw: tw, mem: m, log: log, clk: clk}
	w.seed()
	return w
}

// seed sets the sprint's counters, as the coordinator does before the first
// card: the same first step on the twin and on a store.
func (w *world) seed() {
	w.t.Helper()
	w.step(&sprintfn.Request{Epoch: "0", Meta: sprintfn.Meta{Verb: "seed", Actor: "coordinator"},
		Sprint: &sprintfn.SprintPart{Counter: &sprintfn.CounterChange{Read: map[string]string{"score": "", "streams": ""},
			Set: map[string]string{"score": "100000000", "streams": "1"}}}})
}

// step applies a request or fails the test.
func (w *world) step(req *sprintfn.Request) *sprintfn.StepReply {
	w.t.Helper()
	res, err := sprintfn.Step(context.Background(), w.c, req)
	if err != nil {
		w.t.Fatalf("step: %v", err)
	}
	if res.Step == nil {
		w.t.Fatalf("step refused: %+v, %v", res.Refusal, res.Err)
	}
	return res.Step
}

// verb is a coordinator's step of entries.
func (w *world) verb(entries ...tset.Entry) {
	w.t.Helper()
	w.step(&sprintfn.Request{Epoch: "0", Meta: sprintfn.Meta{Verb: "seed", Actor: "coordinator"}, Body: sprintfn.Body{Entries: entries}})
}

// rows adds work rows, a hundred a step (L1 6).
func (w *world) rows(names ...string) {
	w.t.Helper()
	for i := 0; i < len(names); i += 100 {
		w.verb(tset.Entry{Kind: "rows", Table: sprint.Work, Add: names[i:min(i+100, len(names))]})
	}
}

// create creates work cards in a cell, with the fields given, scored in order.
func create(cell string, fields map[string]string, ids ...string) tset.Entry {
	scores := make([]string, len(ids))
	for i := range ids {
		scores[i] = strconv.Itoa(i + 1)
	}
	return tset.Entry{Kind: "create", Table: sprint.Work, To: cell, IDs: ids, Scores: scores, Set: fields, About: ids}
}

// fresh are primaries ready and never dealt: in fresh:<s> by derivation.
func fresh() map[string]string { return map[string]string{"kind": "primary", "attempt": "0"} }

// waiting are primaries waiting with no open need: in elig:<s>.
func waiting() map[string]string { return map[string]string{"kind": "primary", "open": "0"} }

// ids are n ids with a prefix.
func ids(prefix string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("%s%d", prefix, i+1)
	}
	return out
}

// zset is one of the sprint's sorted sets, on the twin or on the store.
func (w *world) zset(name string) map[string]float64 {
	w.t.Helper()
	if w.raw == nil {
		return w.tw.SprintKeys()[testNames.Key(name)].ZSet
	}
	zs, err := w.raw.ZRangeWithScores(context.Background(), testNames.Key(name), 0, -1).Result()
	if err != nil {
		w.t.Fatalf("read the sorted set %s: %v", name, err)
	}
	out := make(map[string]float64, len(zs))
	for _, z := range zs {
		out[z.Member.(string)] = z.Score
	}
	return out
}

// hash is one of the sprint's hashes, on the twin or on the store.
func (w *world) hash(name string) map[string]string {
	w.t.Helper()
	if w.raw == nil {
		return w.tw.SprintKeys()[testNames.Key(name)].Hash
	}
	h, err := w.raw.HGetAll(context.Background(), testNames.Key(name)).Result()
	if err != nil {
		w.t.Fatalf("read the hash %s: %v", name, err)
	}
	return h
}

// place is where a work card is, "" when it has none.
func (w *world) place(id string) string {
	w.t.Helper()
	return w.placeIn(sprint.Work, id)
}

// placeIn is where a card of a table is, "" when it has none.
func (w *world) placeIn(table, id string) string {
	w.t.Helper()
	res, err := sprintfn.Read(context.Background(), w.c, &sprintfn.ReadRequest{Epoch: "0",
		Tset: []tset.ReadQuery{{Kind: "ids", Table: table, IDs: []string{id}}}})
	if err != nil || res.Read == nil {
		w.t.Fatalf("read %s: %v %+v", id, err, res.Refusal)
	}
	r := res.Read.Tset[0].Records[0]
	if !r.Exists || r.Place == nil {
		return ""
	}
	return r.Place.Row + ":" + r.Place.Col
}

// counting counts the round trips a client flushes, and keeps each one's
// items (E8 counts them on the store; on the twin, a pipeline is a call).
type counting struct {
	c     sprintfn.Client
	mu    sync.Mutex
	calls int
	sent  [][]sprintfn.Item
	fail  func(call int, items []sprintfn.Item) error
}

func (k *counting) Pipeline(ctx context.Context, items []sprintfn.Item) ([]sprintfn.Result, error) {
	k.mu.Lock()
	k.calls++
	call := k.calls
	k.sent = append(k.sent, items)
	fail := k.fail
	k.mu.Unlock()
	if fail != nil {
		if err := fail(call, items); err != nil {
			return nil, err
		}
	}
	return k.c.Pipeline(ctx, items)
}

// last is the items of the last round trip.
func (k *counting) last() []sprintfn.Item {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.sent[len(k.sent)-1]
}

// loop is a loop over the world with the test rules and builder.
func (w *world) loop(name string, rules []sprint.Rule, b Budget) *Loop {
	w.t.Helper()
	l, err := NewLoop(Config{Names: testNames, Owner: "token-" + name, Name: name, Rules: rules, Build: testBuild(builderOpts{}), Budget: b})
	if err != nil {
		w.t.Fatal(err)
	}
	return l
}

// tick ticks a loop through a counting client and fails on a failed tick.
func (w *world) tick(l *Loop, k *counting) Report {
	w.t.Helper()
	before := k.calls
	rep, err := Tick(context.Background(), k, l)
	if err != nil {
		w.t.Fatalf("tick: %v", err)
	}
	if rep.RoundTrips != k.calls-before {
		w.t.Fatalf("the report says %d round trips, the client flushed %d", rep.RoundTrips, k.calls-before)
	}
	return rep
}

// ---- the test rules

// testRule is a rule of the tests', with the real rule's name and priority so
// that the keys real lines queue are its own (2.1).
func testRule(name string, read func([]sprint.AgendaKey, sprint.ReadBounds, int) (sprint.ReadPlan, []sprint.AgendaKey),
	plan func(*sprint.Snapshot, []sprint.AgendaKey, sprint.Now) sprint.RulePlan) sprint.Rule {
	p, ok := sprint.PriorityOf(name)
	if !ok {
		panic("no priority for " + name)
	}
	return sprint.Rule{Name: name, Priority: p, Read: read, Plan: plan}
}

// headRead reads the head of an index of s1 through `related` (1.0), with the
// fields and follows given, limit halved by the halvings.
func headRead(index string, limit int, fields, follow []string) func([]sprint.AgendaKey, sprint.ReadBounds, int) (sprint.ReadPlan, []sprint.AgendaKey) {
	return func(keys []sprint.AgendaKey, b sprint.ReadBounds, h int) (sprint.ReadPlan, []sprint.AgendaKey) {
		q := sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Work, Fields: fields, Follow: follow,
			Source: sprint.IDSource{Kind: sprint.SourceHead, Key: index + ":s1", Limit: sprint.Halved(limit, h)}}
		return sprint.ReadPlan{Sprint: []sprint.SprintQ{q}}, nil
	}
}

// moveAll is a plan that moves every card the read returned from one column
// of its row to another, setting fields, and finishes its keys when it moved
// every card its read could return.
func moveAll(from, to string, set map[string]string) func(*sprint.Snapshot, []sprint.AgendaKey, sprint.Now) sprint.RulePlan {
	return func(s *sprint.Snapshot, keys []sprint.AgendaKey, now sprint.Now) sprint.RulePlan {
		var rp sprint.RulePlan
		for _, c := range s.Work.LoadedCards() {
			if c.Col != from {
				continue
			}
			rp.Plan.Units = append(rp.Plan.Units, moveUnit(sprint.Work, c.ID, c.Row, from, to, set))
		}
		rp.Done = keys
		return rp
	}
}

// moveUnit is one card's move as a unit of a plan.
func moveUnit(table, id, row, from, to string, set map[string]string) sprint.Unit {
	return sprint.Unit{Key: id, Stream: row, Changes: []sprint.Change{{Table: table, Entry: ntable.BatchMemberEntry{
		ID: id, Expect: &ntable.MemberExpect{Place: &ntable.PlaceExpect{Row: row, Col: from}},
		Move: &ntable.MemberMoveOp{Row: row, Col: to}, Set: set}}}}
}

// ---- the tests' step builder

type builderOpts struct {
	maxUnits  int  // units a body holds at most; 0 is no bound but the bounds'
	unitEntry bool // each unit its own entry (no grouping)
}

// testBuild turns a rule's plan into bodies (8.0's step.Build, as far as the
// tests' plans go): each unit's changes are entries, grouped by table, kind,
// cells and fields into one entry unless unitEntry, packed into bodies inside
// the bounds' candidates (and maxUnits), then each body halved until it is
// inside the request bytes (measured by sprintfn.EncodedSize), each unit
// whole. The notes, guards, intents and requeues ride the first body; Done
// rides a plan of one body.
func testBuild(o builderOpts) Builder {
	return func(rp sprint.RulePlan, m sprintfn.Meta, b stepbuild.Bounds) ([]sprintfn.Body, error) {
		type unit struct {
			es []tset.Entry
			n  int
		}
		var us []unit
		for _, u := range rp.Plan.Units {
			es, n, err := unitEntries(u)
			if err != nil {
				return nil, err
			}
			us = append(us, unit{es, n})
		}
		var groups [][]unit
		var cur []unit
		cands := 0
		for _, u := range us {
			if len(cur) > 0 && ((o.maxUnits > 0 && len(cur) >= o.maxUnits) || cands+u.n > b.Candidates) {
				groups, cur, cands = append(groups, cur), nil, 0
			}
			cur, cands = append(cur, u), cands+u.n
		}
		if len(cur) > 0 {
			groups = append(groups, cur)
		}
		body := func(g []unit) sprintfn.Body {
			var es []tset.Entry
			for _, u := range g {
				es = appendEntries(es, u.es, o.unitEntry)
			}
			return sprintfn.Body{Entries: es}
		}
		size := func(body sprintfn.Body) int {
			n, ref := sprintfn.EncodedSize(testNames.Prefix, &sprintfn.Request{Epoch: "0", Body: body, Meta: m})
			if ref != nil {
				return int(^uint(0) >> 1)
			}
			return n
		}
		var bodies []sprintfn.Body
		var fit func(g []unit) error
		fit = func(g []unit) error {
			bd := body(g)
			if size(bd) <= b.RequestBytes {
				bodies = append(bodies, bd)
				return nil
			}
			if len(g) == 1 {
				return fmt.Errorf("a unit is over %d bytes alone", b.RequestBytes)
			}
			if err := fit(g[:len(g)/2]); err != nil {
				return err
			}
			return fit(g[len(g)/2:])
		}
		for _, g := range groups {
			if err := fit(g); err != nil {
				return nil, err
			}
		}
		if len(bodies) == 0 && (len(rp.Notes)+len(rp.Done)+len(rp.Requeue)+len(rp.Guards)+len(rp.Intents)) != 0 {
			bodies = []sprintfn.Body{{}}
		}
		if len(bodies) == 0 {
			return nil, nil
		}
		bodies[0].Notes, bodies[0].Guards, bodies[0].Intents = rp.Notes, rp.Guards, rp.Intents
		for _, k := range rp.Requeue {
			bodies[0].Requeue = append(bodies[0].Requeue, k.Key)
		}
		if len(bodies) == 1 {
			for _, k := range rp.Done {
				bodies[0].Done = append(bodies[0].Done, k.Key)
			}
		}
		return bodies, nil
	}
}

// unitEntries is a unit's changes as Layer 1 entries, and its candidates.
func unitEntries(u sprint.Unit) ([]tset.Entry, int, error) {
	var out []tset.Entry
	for _, c := range u.Changes {
		e := c.Entry
		cell := func(row, col string) string { return row + ":" + col }
		switch {
		case e.Create != nil:
			out = append(out, tset.Entry{Kind: "create", Table: c.Table, To: cell(e.Create.Row, e.Create.Col), IDs: []string{e.ID},
				Scores: []string{strconv.FormatFloat(e.Create.Score, 'f', -1, 64)}, Set: e.Set, About: []string{e.ID}})
		case e.Move != nil && e.Expect != nil && e.Expect.Place != nil:
			out = append(out, tset.Entry{Kind: "move", Table: c.Table, From: cell(e.Expect.Place.Row, e.Expect.Place.Col),
				To: cell(e.Move.Row, e.Move.Col), IDs: []string{e.ID}, Set: e.Set, About: []string{e.ID}})
		default:
			return nil, 0, errors.New("the tests' builder takes creates, and moves with their place")
		}
	}
	return out, len(out), nil
}

// appendEntries adds a unit's entries, each into an entry of the same kind,
// table, cells and fields when grouping (T4: batch always).
func appendEntries(into, es []tset.Entry, apart bool) []tset.Entry {
	for _, e := range es {
		merged := false
		if !apart {
			for i := range into {
				if sameEntry(into[i], e) {
					into[i].IDs = append(into[i].IDs, e.IDs...)
					into[i].Scores = append(into[i].Scores, e.Scores...)
					into[i].About = append(into[i].About, e.About...)
					merged = true
					break
				}
			}
		}
		if !merged {
			e.IDs = append([]string(nil), e.IDs...)
			e.About = append([]string(nil), e.About...)
			e.Scores = append([]string(nil), e.Scores...)
			into = append(into, e)
		}
	}
	return into
}

func sameEntry(a, b tset.Entry) bool {
	if a.Kind != b.Kind || a.Table != b.Table || a.From != b.From || a.To != b.To || len(a.Set) != len(b.Set) || len(a.IDs) >= stepbuild.LimitEntryIDs {
		return false
	}
	ja, _ := json.Marshal(a.Set)
	jb, _ := json.Marshal(b.Set)
	return string(ja) == string(jb)
}

// keysOf is the keys of a list, sorted.
func keysOf(ks []sprint.AgendaKey) []string {
	out := make([]string, len(ks))
	for i, k := range ks {
		out[i] = k.Key
	}
	sort.Strings(out)
	return out
}

// pageOf is the page's query in RT1's items: after the lease step, and after
// the error step when one is owed. A round trip with no page gives the zero
// query.
func pageOf(items []sprintfn.Item) tset.ReadQuery {
	for _, it := range items {
		if it.Page != nil {
			return it.Page.Queries[0]
		}
	}
	return tset.ReadQuery{}
}

// intercept forwards to a client, but answers the items a test names itself:
// answer gives the result of an item (nil forwards it), and agenda, when set,
// replaces RT1's agenda head with its keys. The error steps' results are kept,
// in order (an error step is RT1's second item, a tick step of no lease).
type intercept struct {
	c      sprintfn.Client
	mu     sync.Mutex
	answer func(it sprintfn.Item) *sprintfn.Result
	agenda []sprint.AgendaKey
	errRes []sprintfn.Result
}

// refuseRule answers every step of a rule with a refusal of a code.
func refuseRule(rule, code string) func(it sprintfn.Item) *sprintfn.Result {
	return func(it sprintfn.Item) *sprintfn.Result {
		if it.Step != nil && it.Step.Meta.Rule == rule {
			return &sprintfn.Result{Refusal: &sprintfn.Refusal{Code: code}}
		}
		return nil
	}
}

// isErrorStep says an item is RT1's error step.
func isErrorStep(it sprintfn.Item) bool {
	return it.Step != nil && it.Step.Meta.Rule == "tick" && it.Step.Lease == nil
}

func (r *intercept) Pipeline(ctx context.Context, items []sprintfn.Item) ([]sprintfn.Result, error) {
	var fwd []sprintfn.Item
	var idx []int
	out := make([]sprintfn.Result, len(items))
	for i, it := range items {
		if r.answer != nil {
			if res := r.answer(it); res != nil {
				out[i] = *res
				continue
			}
		}
		fwd = append(fwd, it)
		idx = append(idx, i)
	}
	res, err := r.c.Pipeline(ctx, fwd)
	if err != nil {
		return nil, err
	}
	for j, i := range idx {
		out[i] = res[j]
		it := items[i]
		if it.Read != nil && len(it.Read.Tset) >= 3 && it.Read.Tset[0].Kind == "range" && strings.Contains(it.Read.Tset[0].Key, "agenda") && r.agenda != nil && res[j].Read != nil {
			a := res[j].Read.Tset[0]
			a.IDs, a.Scores, a.HasMore = nil, nil, false
			for _, k := range r.agenda {
				a.IDs = append(a.IDs, k.Key)
				a.Scores = append(a.Scores, strconv.FormatUint(k.Seq, 10))
			}
			res[j].Read.Tset[0] = a
			out[i] = res[j]
		}
	}
	r.mu.Lock()
	for i, it := range items {
		if isErrorStep(it) {
			r.errRes = append(r.errRes, out[i])
		}
	}
	r.mu.Unlock()
	return out, nil
}

// codes are the results' codes, "applied" for a step that applied.
func codes(rs []sprintfn.Result) []string {
	var out []string
	for _, r := range rs {
		switch {
		case r.Step != nil:
			out = append(out, "applied")
		case r.Refusal != nil:
			out = append(out, r.Refusal.Code)
		default:
			out = append(out, "unknown")
		}
	}
	return out
}
