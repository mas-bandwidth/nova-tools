package sprintfn

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// IT08's extensions of the sprint's queries, carried end to end (the cold
// read of PR 4788, M2): the cursor and the made needs of `waiters`, a line's
// more_ids, the counts of `streams`, and the sprint keys a query also reads
// (sprint.SprintQ.WaiterAfter, Missing, Counts, Keys; rules_position_read.go).
// The twin answers each, the wire carries each, and the answers load through
// IT08's alignment (LoadPartial).

// extBounds are the bounds a rule's read is sized to here: Layer 1's.
var extBounds = sprint.ReadBounds{Queries: 1024, Records: 10000, RangeIDs: 20000, Bytes: 8 << 20}

// extWorld is the standard world with the dropping marks of both streams and
// {p}next@e.streams, and a line that created ghost and n2 (its seq is line).
func extWorld(t *testing.T) (w *qworld, line uint64) {
	t.Helper()
	w = standard(t)
	w.cards(card{sprint.Work, "s2", "waiting", "ghost", "20", fields("kind", "work", "open", "0")},
		card{sprint.Work, "s2", "waiting", "n2", "21", fields("kind", "work", "open", "0")})
	line = uint64(len(w.log.Lines(testPrefix, "0")))
	w.seed(w.hset("dropping", "s1", "op-drop-s1"), w.hset("next", "streams", "2"), w.zadd("wait:n2", "0", "w2"))
	return w, line
}

// ruleRead is the read a registered rule plans for the keys.
func ruleRead(t *testing.T, name string, keys ...sprint.AgendaKey) sprint.ReadPlan {
	t.Helper()
	for _, r := range sprint.RuleTable() {
		if r.Name == name {
			rp, left := r.Read(keys, extBounds, 0)
			if len(left) > 0 {
				t.Fatalf("%s left keys: %v", name, left)
			}
			return rp
		}
	}
	t.Fatalf("no rule %s", name)
	return sprint.ReadPlan{}
}

// answerPlan answers every sprint query of a plan from the twin and loads the
// answer; it returns the answers.
func answerPlan(t *testing.T, w *qworld, rp sprint.ReadPlan) []sprint.Answer {
	t.Helper()
	var as []sprint.Answer
	for _, q := range rp.Sprint {
		a, err := w.tw.Query(q)
		if err != nil {
			t.Fatalf("%s %+v: %v", q.Kind, q, err)
		}
		as = append(as, a)
	}
	if _, err := sprint.LoadPartial(sprint.ReadPlan{Sprint: rp.Sprint}, sprint.ReadAnswer{Epoch: "0", ActiveEpoch: "0",
		TimeMS: "1790000000123", Sprint: as}); err != nil {
		t.Fatalf("the twin's answers to the read do not load: %v", err)
	}
	return as
}

// TestRuleReadsLoadFromTheTwin: R4's own reads (needs:n, made:n+w with its
// cursor and the made filter, a line of needs, each with the dropping marks)
// and R5's (streams with the stuck ids and the marks) are answered by the twin
// and load; so do R3's (front with jopen:G, the one field its rule tests) and
// R15's (streams with the stream set's version).
func TestRuleReadsLoadFromTheTwin(t *testing.T) {
	t.Parallel()
	w, line := extWorld(t)
	as := answerPlan(t, w, ruleRead(t, "needs", sprint.AgendaKey{Key: "needs:ghost", Seq: 1}))
	if want := []sprint.KeyAnswer{{Key: sprint.KeyDropping, Streams: []string{"s1"}}}; !reflect.DeepEqual(as[0].Keys, want) {
		t.Fatalf("needs:ghost reads the marks of its waiters' stream: %+v", as[0].Keys)
	}
	as = answerPlan(t, w, ruleRead(t, "needs", sprint.AgendaKey{Key: "made:ghost+w1", Seq: 1}))
	if n := as[0].Needs; len(n) != 1 || !reflect.DeepEqual(n[0].Waiters, []string{"w2"}) || n[0].Last != "w2" {
		t.Fatalf("made:ghost+w1: %+v", n)
	}
	rp := ruleRead(t, "needs", sprint.AgendaKey{Key: "needs@" + uintText(line), Seq: 1})
	as = answerPlan(t, w, rp)
	if !reflect.DeepEqual(as[0].IDs, []string{"ghost", "n2"}) || as[0].MoreIDs || len(as[0].Keys) != 1 {
		t.Fatalf("a line of needs: %+v", as[0])
	}
	as = answerPlan(t, w, ruleRead(t, "cross", sprint.AgendaKey{Key: "cross", Seq: 1}))
	if want := []sprint.KeyAnswer{{Key: sprint.KeyDropping, Streams: []string{"s1", "s2"}}}; !reflect.DeepEqual(as[0].Keys, want) {
		t.Fatalf("cross reads the marks of every stream: %+v", as[0].Keys)
	}
	// R15 reads no jopen:sprint since errata 3 amendment 6 (it opens no
	// judgment): the stream set's version alone
	as = answerPlan(t, w, ruleRead(t, "done", sprint.AgendaKey{Key: "done", Seq: 1}))
	if len(as[0].Keys) != 1 || as[0].Keys[0].Key != sprint.KeyNextStreams {
		t.Fatalf("done reads the stream set's version only: %+v", as[0].Keys)
	}
	as = answerPlan(t, w, ruleRead(t, "resolve", sprint.AgendaKey{Key: "resolve:s1", Seq: 1}))
	if want := (sprint.KeyAnswer{Key: sprint.KeyJOpenG, Subject: "g1"}); !reflect.DeepEqual(as[0].Keys[0], want) {
		t.Fatalf("resolve reads jopen of g1: %+v", as[0].Keys)
	}
}

// TestQueryJOpenKeys: jopen:G reads the one field R3 tests on the first
// sentinel of a `front` (none when the stream has none), jopen:sprint the one
// R15 tests on the sprint, each one HMGET of one field and one probe: absent is
// no judgment, a note's id is open, h and a note's id is held, and anything
// else is DRIFT; jopen:G of a kind other than `front` is REQUEST (2.3 R3, R15).
func TestQueryJOpenKeys(t *testing.T) {
	t.Parallel()
	w := standard(t)
	front := sprint.SprintQ{Kind: sprint.QueryFront, Stream: "s1", Fields: []string{}, Heads: []sprint.HeadQ{}, Keys: []string{sprint.KeyJOpenG}}
	streams := sprint.SprintQ{Kind: sprint.QueryStreams, Fields: []string{}, Keys: []string{sprint.KeyJOpenSprint}}
	keyOf := func(q sprint.SprintQ) sprint.KeyAnswer {
		t.Helper()
		a, err := w.tw.Query(q)
		if err != nil || len(a.Keys) != 1 {
			t.Fatalf("%s: %v %+v", q.Kind, err, a.Keys)
		}
		return a.Keys[0]
	}
	if k := keyOf(front); k.Subject != "g1" || len(k.Open)+len(k.Held) != 0 {
		t.Fatalf("no judgment on g1: %+v", k)
	}
	if k := keyOf(streams); k.Subject != sprint.SprintSubject || len(k.Open)+len(k.Held) != 0 {
		t.Fatalf("no judgment on the sprint: %+v", k)
	}
	w.note(sprint.NSentinelReached, sprint.ReachedCause, "g1")
	w.note(sprint.NSprintDone, sprint.SprintDoneCause, sprint.SprintSubject)
	if k := keyOf(front); !reflect.DeepEqual(k.Open, []string{sprint.NSentinelReached}) || len(k.Held) != 0 {
		t.Fatalf("reached open on g1: %+v", k)
	}
	if k := keyOf(streams); !reflect.DeepEqual(k.Open, []string{sprint.NSprintDone}) {
		t.Fatalf("done open on the sprint: %+v", k)
	}
	w.seed(w.hset("jopen:g1", sprint.NSentinelReached+"|"+sprint.ReachedCause, "hn1"))
	if k := keyOf(front); !reflect.DeepEqual(k.Held, []string{sprint.NSentinelReached}) || len(k.Open) != 0 {
		t.Fatalf("reached held on g1: %+v", k)
	}
	if _, c := w.query(front); c.Probes > QueryProbes(front) {
		t.Fatalf("charged %d probes, declared %d", c.Probes, QueryProbes(front))
	}
	w.seed(w.hset("jopen:g1", sprint.NSentinelReached+"|"+sprint.ReachedCause, "x"))
	if ref := w.refused(front); ref.Code != codeDrift {
		t.Fatalf("a field that is no note: %+v", ref)
	}
	if k := keyOf(sprint.SprintQ{Kind: sprint.QueryFront, Stream: "s9", Fields: []string{}, Heads: []sprint.HeadQ{}, Keys: []string{sprint.KeyJOpenG}}); k.Subject != "" {
		t.Fatalf("a stream with no sentinel reads no jopen: %+v", k)
	}
	if ref := ValidateSprintQ(sprint.SprintQ{Kind: sprint.QueryStreams, Fields: []string{}, Keys: []string{sprint.KeyJOpenG}}); ref == nil || ref.Code != CodeRequest {
		t.Fatalf("jopen:G of streams: %v", ref)
	}
}

func uintText(n uint64) string { b, _ := json.Marshal(n); return string(b) }

// TestQueryKeysAnswerTheMarks: the dropping marks of the streams a query
// reached, in the order reached (front: its stream; waiters: its waiters';
// streams: every stream it lists), and {p}next@e.streams; a query that reached
// no stream reads no mark and charges no probe for it; a counter that is no
// exact decimal is DRIFT.
func TestQueryKeysAnswerTheMarks(t *testing.T) {
	t.Parallel()
	// the standard world marks s2 alone; the counter is set here
	w := standard(t)
	w.seed(w.hset("next", "streams", "2"))
	both := []string{sprint.KeyDropping, sprint.KeyNextStreams}
	for _, c := range []struct {
		name    string
		q       sprint.SprintQ
		want    []string
		reached int
	}{
		{"waiters of s1", sprint.SprintQ{Kind: sprint.QueryWaiters, Source: ids("ghost"), Limit: 5, Fields: []string{}, Keys: both}, nil, 1},
		{"front of s2", sprint.SprintQ{Kind: sprint.QueryFront, Stream: "s2", Fields: []string{}, Keys: both}, []string{"s2"}, 1},
		{"front of s1", sprint.SprintQ{Kind: sprint.QueryFront, Stream: "s1", Fields: []string{}, Keys: both}, nil, 1},
		{"streams", sprint.SprintQ{Kind: sprint.QueryStreams, Fields: []string{}, Limit: 0, Keys: both}, []string{"s2"}, 2},
		{"no waiters", sprint.SprintQ{Kind: sprint.QueryWaiters, Source: ids("nothing"), Limit: 5, Fields: []string{}, Keys: both}, nil, 0},
	} {
		a := loadAnswered(t, w, c.q)
		want := []sprint.KeyAnswer{{Key: sprint.KeyDropping, Streams: c.want}, {Key: sprint.KeyNextStreams, N: 2}}
		if !reflect.DeepEqual(a.Keys, want) {
			t.Errorf("%s: keys %+v, want %+v", c.name, a.Keys, want)
		}
		// each stream reached is named once (w1 and w2 are both of s1), and
		// next.streams is one probe
		_, with := w.query(c.q)
		plain := c.q
		plain.Keys = nil
		_, without := w.query(plain)
		if got := with.Probes - without.Probes; got != c.reached+1 {
			t.Errorf("%s: the keys charged %d probes, want %d", c.name, got, c.reached+1)
		}
		if with.Probes > QueryProbes(c.q) {
			t.Errorf("%s: charged %d probes, declared %d", c.name, with.Probes, QueryProbes(c.q))
		}
	}
	bad := standard(t)
	bad.seed(bad.hset("next", "streams", "02"))
	if ref := bad.refused(sprint.SprintQ{Kind: sprint.QueryStreams, Fields: []string{}, Keys: []string{sprint.KeyNextStreams}}); ref.Code != codeDrift {
		t.Fatalf("a counter of 02: %v", ref)
	}
}

// TestStreamsCountsLoad: a streams query with counts gives, for every stream
// it lists, the count of each cell named, in the query's order, and loads
// (IT08's R15 reads them); a column the work table does not have is NOCOL.
func TestStreamsCountsLoad(t *testing.T) {
	t.Parallel()
	w := standard(t)
	q := sprint.SprintQ{Kind: sprint.QueryStreams, Fields: []string{"state"}, Limit: 1, Counts: []string{"waiting", "ready", "landed"},
		Keys: []string{sprint.KeyNextStreams}}
	a := loadAnswered(t, w, q)
	want := []sprint.CellCount{{Row: "s1", Col: "waiting", N: 6}, {Row: "s1", Col: "ready", N: 3}, {Row: "s1", Col: "landed", N: 1},
		{Row: "s2", Col: "waiting", N: 1}, {Row: "s2", Col: "ready", N: 1}, {Row: "s2", Col: "landed", N: 0}}
	if !reflect.DeepEqual(a.Counts, want) {
		t.Fatalf("counts %+v, want %+v", a.Counts, want)
	}
	if !reflect.DeepEqual(a.Keys, []sprint.KeyAnswer{{Key: sprint.KeyNextStreams}}) {
		t.Fatalf("an unset counter reads 0: %+v", a.Keys)
	}
	_, c := w.query(q)
	if c.Probes > QueryProbes(q) {
		t.Fatalf("charged %d probes, declared %d", c.Probes, QueryProbes(q))
	}
	q.Counts = []string{"waiting", "nocol"}
	if ref := w.refused(q); ref.Code != "NOCOL" || ref.Detail.Table != sprint.Work || !reflect.DeepEqual(ref.Detail.Cells, []string{"s1:nocol"}) {
		t.Fatalf("a column the table lacks: %+v", ref)
	}
}

// TestQueryExtensionsRefusedBeforeAnyRead: an extension on a kind that has no
// use for it (jopen:G of a query that has no first sentinel), a cursor of a
// source that is not one id, and a malformed name are REQUEST, before any read
// (ValidateSprintQ).
func TestQueryExtensionsRefusedBeforeAnyRead(t *testing.T) {
	t.Parallel()
	one := ids("n")
	for name, q := range map[string]sprint.SprintQ{
		"jopen:G of streams":  {Kind: sprint.QueryStreams, Fields: []string{}, Keys: []string{sprint.KeyJOpenG}},
		"jopen:G of waiters":  {Kind: sprint.QueryWaiters, Source: one, Limit: 1, Fields: []string{}, Keys: []string{sprint.KeyJOpenG}},
		"a key twice":         {Kind: sprint.QueryStreams, Fields: []string{}, Keys: []string{sprint.KeyDropping, sprint.KeyDropping}},
		"keys of related":     {Kind: sprint.QueryRelated, Table: sprint.Work, Source: one, Fields: []string{}, Keys: []string{sprint.KeyDropping}},
		"counts of waiters":   {Kind: sprint.QueryWaiters, Source: one, Limit: 1, Fields: []string{}, Counts: []string{"waiting"}},
		"a column twice":      {Kind: sprint.QueryStreams, Fields: []string{}, Counts: []string{"waiting", "waiting"}},
		"a cursor of two ids": {Kind: sprint.QueryWaiters, Source: ids("n", "m"), Limit: 1, Fields: []string{}, WaiterAfter: "w1"},
		"a cursor of a head":  {Kind: sprint.QueryWaiters, Source: head("missing", 2), Limit: 1, Fields: []string{}, WaiterAfter: "w1"},
		"a cursor not an id":  {Kind: sprint.QueryWaiters, Source: one, Limit: 1, Fields: []string{}, WaiterAfter: "a b"},
		"missing of streams":  {Kind: sprint.QueryStreams, Fields: []string{}, Missing: true},
	} {
		if ref := ValidateSprintQ(q); ref == nil || ref.Code != CodeRequest {
			t.Errorf("%s: %v, want REQUEST", name, ref)
		}
	}
}

// TestQueryExtensionsOnTheWire: the wire carries each extension (the equality
// vector: the exact object), a query decodes to the query encoded, and a query
// with none encodes as it did before them.
func TestQueryExtensionsOnTheWire(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		q    sprint.SprintQ
		wire string
	}{
		{sprint.SprintQ{Kind: sprint.QueryWaiters, Source: ids("n"), Limit: 20, Fields: []string{"open"}, WaiterAfter: "w150", Missing: true,
			Keys: []string{sprint.KeyDropping}},
			`{"after":"w150","fields":["open"],"keys":["dropping"],"kind":"waiters","limit":20,"missing":true,"src":{"ids":["n"],"kind":"ids"}}`},
		{sprint.SprintQ{Kind: sprint.QueryStreams, Fields: []string{"dropped"}, Counts: []string{"waiting", "landed"},
			Keys: []string{sprint.KeyNextStreams, sprint.KeyDropping}},
			`{"counts":["waiting","landed"],"fields":["dropped"],"keys":["next.streams","dropping"],"kind":"streams","limit":0}`},
		{sprint.SprintQ{Kind: sprint.QueryFront, Stream: "s1", Fields: []string{}, Keys: []string{sprint.KeyDropping}},
			`{"fields":[],"heads":[],"keys":["dropping"],"kind":"front","stream":"s1"}`},
		{sprint.SprintQ{Kind: sprint.QueryWaiters, Source: ids("n"), Limit: 1, Fields: []string{}},
			`{"fields":[],"kind":"waiters","limit":1,"src":{"ids":["n"],"kind":"ids"}}`},
	} {
		enc := mustEncode(t, c.q)
		if string(enc.Query) != c.wire {
			t.Errorf("%s: wire %s, want %s", c.q.Kind, enc.Query, c.wire)
		}
		dec, ref := DecodeSprintQ(enc)
		if ref != nil {
			t.Fatalf("%s: decode: %v", c.q.Kind, ref)
		}
		if !reflect.DeepEqual(dec, c.q) {
			t.Errorf("%s: decodes to %+v, want %+v", c.q.Kind, dec, c.q)
		}
	}
}

// TestWaitersMoreIDsOnTheWire: a line's more_ids reaches the answer the store
// sends (its JSON), and the decoded answer projects it (sprint.Answer.MoreIDs):
// R4 moves a line key on only past its last window.
func TestWaitersMoreIDsOnTheWire(t *testing.T) {
	t.Parallel()
	w, line := extWorld(t)
	for limit, want := range map[int]bool{1: true, 2: false} {
		q := sprint.SprintQ{Kind: sprint.QueryWaiters, Source: sprint.IDSource{Kind: sprint.SourceLine, Seq: line, Limit: limit}, Limit: 5, Fields: []string{}}
		raw, ref, _ := w.goRead(mustEncode(t, q))
		if ref != nil {
			t.Fatal(ref)
		}
		res, err := DecodeResult(q.Kind, raw)
		if err != nil {
			t.Fatal(err)
		}
		if a := res.Project(q); a.MoreIDs != want {
			t.Errorf("a window of %d of a line of two: more_ids %v, want %v (%s)", limit, a.MoreIDs, want, raw)
		}
	}
}

// TestLuaWaitersCursorAwaitsAnOrderedHead: the Lua refuses CONFIG, before it reads
// anything, a waiters query with a cursor. wait:n's members are all scored 0,
// so the head after a member is a lexicographic range, and Layer 1's checked
// reads give a sorted set's head by score only (S.read_range_head): the twin
// answers the cursor, and the store cannot until Layer 1 has such a head. This
// pins the gap until it closes.
func TestLuaWaitersCursorAwaitsAnOrderedHead(t *testing.T) {
	t.Parallel()
	w, _ := extWorld(t)
	h := newLuaHarness(t, w)
	q := mustEncode(t, sprint.SprintQ{Kind: sprint.QueryWaiters, Source: ids("ghost"), Limit: 1, Fields: []string{}, WaiterAfter: "w1"})
	if _, ref := h.read(q); ref == nil || ref.Code != CodeConfig {
		t.Fatalf("the Lua's cursor: %+v, want CONFIG", ref)
	}
	if h.charge != (QueryCharge{}) {
		t.Fatalf("the refusal read something: %+v", h.charge)
	}
	if _, ref, _ := w.goRead(q); ref != nil {
		t.Fatalf("the twin refused the cursor: %v", ref)
	}
}

// TestLuaQueryExtensionsEqualTwin: the extensions on a world with marks and a
// counter set, answered by the Lua kinds and the twin alike, and a counter the
// store holds as no exact decimal DRIFT in both.
func TestLuaQueryExtensionsEqualTwin(t *testing.T) {
	t.Parallel()
	w, line := extWorld(t)
	h := newLuaHarness(t, w)
	both := []string{sprint.KeyDropping, sprint.KeyNextStreams}
	for i, q := range []sprint.SprintQ{
		{Kind: sprint.QueryWaiters, Source: ids("ghost", "n2", "p1"), Limit: 5, Fields: []string{"open"}, Missing: true, Keys: both},
		{Kind: sprint.QueryWaiters, Source: ids("ghost"), Limit: 1, Fields: []string{}, Keys: []string{sprint.KeyNextStreams}},
		{Kind: sprint.QueryWaiters, Source: sprint.IDSource{Kind: sprint.SourceLine, Seq: line, Limit: 1}, Limit: 2, Fields: []string{}, Keys: both},
		{Kind: sprint.QueryFront, Stream: "s1", Fields: []string{}, Heads: []sprint.HeadQ{{Index: sprint.HeadAgain, Limit: 2}}, Keys: both},
		{Kind: sprint.QueryStreams, Fields: []string{"state"}, Limit: 3, Counts: []string{"waiting", "ready", "working", "review", "merging", "landed"}, Keys: both},
	} {
		h.agree("extension #"+uintText(uint64(i))+" "+q.Kind, mustEncode(t, q))
	}
	bad := standard(t)
	bad.seed(bad.hset("next", "streams", "18446744073709551616"))
	newLuaHarness(t, bad).agree("a counter past 2^64 - 1", mustEncode(t, sprint.SprintQ{Kind: sprint.QueryStreams, Fields: []string{}, Keys: []string{sprint.KeyNextStreams}}))
}

// TestLuaQueriesJOpenKeys: the Lua reads the jopen keys as the twin does, the
// same answer and charge, for a field absent, open, held and not a note (DRIFT),
// on a `front` with and without a first sentinel and on `streams` (2.3 R3, R15).
func TestLuaQueriesJOpenKeys(t *testing.T) {
	t.Parallel()
	qs := []sprint.SprintQ{
		{Kind: sprint.QueryFront, Stream: "s1", Fields: []string{"kind"}, Heads: []sprint.HeadQ{{Index: sprint.HeadEligBelow, Limit: 2}}, Keys: []string{sprint.KeyJOpenG, sprint.KeyDropping}},
		{Kind: sprint.QueryFront, Stream: "s9", Fields: []string{}, Heads: []sprint.HeadQ{}, Keys: []string{sprint.KeyJOpenG}},
		{Kind: sprint.QueryStreams, Fields: []string{"dropped"}, Counts: []string{"waiting", "landed"}, Keys: []string{sprint.KeyNextStreams, sprint.KeyJOpenSprint}},
		{Kind: sprint.QueryWaiters, Source: ids("ghost"), Limit: 1, Fields: []string{}, Keys: []string{sprint.KeyJOpenSprint}},
	}
	reached, done := sprint.NSentinelReached+"|"+sprint.ReachedCause, sprint.NSprintDone+"|"+sprint.SprintDoneCause
	for name, seed := range map[string][]string{
		"absent": nil,
		"open":   {"n3", "n4"},
		"held":   {"hn3", "hn4"},
		"drift":  {"h", "x9"},
	} {
		w := standard(t)
		if seed != nil {
			w.seed(w.hset("jopen:g1", reached, seed[0]), w.hset("jopen:"+sprint.SprintSubject, done, seed[1]))
		}
		h := newLuaHarness(t, w)
		for i, q := range qs {
			enc := mustEncode(t, q)
			// the Lua declares the probes the twin declares: one for each jopen field
			if _, _, lp := h.declared(enc); lp != QueryProbes(q) {
				t.Errorf("%s #%d: the Lua declared %d probes, the twin %d", name, i, lp, QueryProbes(q))
			}
			h.agree(fmt.Sprintf("%s #%d %s", name, i, q.Kind), enc)
		}
		// jopen:G is front's alone (its first sentinel): a waiters query naming it,
		// sent on the wire as it is, is REQUEST in both halves
		waiters := mustEncode(t, qs[3])
		waiters.Query = json.RawMessage(strings.Replace(string(waiters.Query), `"`+sprint.KeyJOpenSprint+`"`, `"`+sprint.KeyJOpenG+`"`, 1))
		before := h.refused[sprint.QueryWaiters]
		h.agree(name+" jopen:G of waiters", waiters)
		if h.refused[sprint.QueryWaiters] != before+1 {
			t.Fatalf("%s: a waiters query naming jopen:G was not refused: %s", name, waiters.Query)
		}
		if name == "drift" && h.refused[sprint.QueryFront] == 0 {
			t.Fatalf("a jopen field that is no note is not refused")
		}
		if name == "held" && h.answered[sprint.QueryStreams] == 0 {
			t.Fatalf("held: nothing answered")
		}
	}
}
