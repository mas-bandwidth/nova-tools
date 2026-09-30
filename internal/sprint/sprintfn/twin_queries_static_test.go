package sprintfn

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// The static phase and the budget of a read (the second cold read of IT30,
// findings 3, 4 and 6): a read checks every sprint query before it samples the
// time or reads a thing, in the order Layer 1's S.validate does, and the
// queries of one read share one budget.

// malformed is a related query the validation refuses: a name with a blank in it.
var malformed = SprintQuery{Kind: sprint.QueryRelated,
	Query: json.RawMessage(`{"kind":"related","t":"work","fields":[],"follow":[],"src":{"kind":"ids","ids":["a b"]}}`)}

func relatedOver(t *testing.T, id string) SprintQuery {
	t.Helper()
	return mustEncode(t, sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Work, Source: ids(id), Fields: []string{"open"}})
}

// readRefusal reads and returns the refusal, failing when there is none.
func readRefusal(t *testing.T, w *qworld, rr *ReadRequest) *Refusal {
	t.Helper()
	res, err := Read(context.Background(), w.tw, rr)
	if err != nil || res.Refusal == nil || res.Refusal.Detail.QueryIndex == nil {
		t.Fatalf("%+v %v, want a refusal at a query", res, err)
	}
	return res.Refusal
}

// TestSprintQueriesAreCheckedBeforeAnythingIsRead: the twin validates every
// sprint query of a read before TIME and before any Layer 1 or Layer 2 query
// runs, as the store's S.validate does (errata E6), so a malformed query is
// REQUEST at its index whatever an earlier query would have found by reading
// (MISSING, NOTABLE, EPOCHAHEAD). The refusal of the read names the lowest
// index that fails the static phase; a read that passes it samples the time.
// The Lua's validate, run first for every query in order, refuses the same
// index with the same code.
func TestSprintQueriesAreCheckedBeforeAnythingIsRead(t *testing.T) {
	t.Parallel()
	w := standard(t)
	w.seed(w.zadd("elig:s9", "1", "phantom"))
	missing := mustEncode(t, sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Work, Source: head("elig:s9", 5), Fields: []string{}})
	noTable := tset.ReadQuery{Kind: "count", Table: "nosuch", Cells: []string{"s1:waiting"}}
	keyBad := SprintQuery{Kind: KeyDropping, Query: json.RawMessage(`{"kind":"dropping","fields":[]}`)}
	tooMany := SprintQuery{Kind: sprint.QueryRelated, Query: json.RawMessage(`{"kind":"related","t":"work","fields":[],"follow":[],"src":{"kind":"ids","ids":[` +
		`"` + strings.Join(manyIDs(10001), `","`) + `"]}}`)}
	overCost := SprintQuery{Kind: sprint.QueryRelated, Query: json.RawMessage(`{"kind":"related","t":"work","fields":[],"follow":["work"],"src":{"kind":"ids","ids":[` +
		`"` + strings.Join(manyIDs(5001), `","`) + `"]}}`)}
	unknown := SprintQuery{Kind: "nosuchkind", Query: json.RawMessage(`{"kind":"nosuchkind","fields":[]}`)}

	var clock int
	w.tw.SetClock(func() time.Time { clock++; return testTime })
	var asked int
	inner := w.tw.phases.Query
	w.tw.phases.Query = func(st *State, q SprintQuery) (json.RawMessage, *Refusal) {
		asked++
		return inner(st, q)
	}
	h := newLuaHarness(t, w)

	for _, c := range []struct {
		name  string
		rr    *ReadRequest
		index int
		code  string
	}{
		{"a query that reads MISSING, then a malformed one", &ReadRequest{Epoch: "0", Sprint: []SprintQuery{missing, malformed}}, 1, CodeRequest},
		{"a count on a table that is not there, then a malformed one", &ReadRequest{Epoch: "0",
			Tset: []tset.ReadQuery{noTable}, Sprint: []SprintQuery{malformed}}, 1, CodeRequest},
		{"a count on a table that is not there, a good one, then a malformed one", &ReadRequest{Epoch: "0",
			Tset: []tset.ReadQuery{noTable}, Sprint: []SprintQuery{relatedOver(t, "p1"), malformed}}, 2, CodeRequest},
		{"an epoch ahead, and a malformed query", &ReadRequest{Epoch: "9", Sprint: []SprintQuery{malformed}}, 0, CodeRequest},
		{"the lowest malformed query of several", &ReadRequest{Epoch: "0", Sprint: []SprintQuery{relatedOver(t, "p1"), keyBad, malformed}}, 1, CodeRequest},
		{"a malformed key read first", &ReadRequest{Epoch: "0", Sprint: []SprintQuery{keyBad, relatedOver(t, "p1")}}, 0, CodeRequest},
		{"a query of no known kind", &ReadRequest{Epoch: "0", Sprint: []SprintQuery{relatedOver(t, "p1"), unknown}}, 1, CodeRequest},
		{"a query too large, before a malformed one: the lowest index", &ReadRequest{Epoch: "0", Sprint: []SprintQuery{overCost, malformed}}, 0, CodeLimit},
		{"a malformed query, before one too large", &ReadRequest{Epoch: "0", Sprint: []SprintQuery{malformed, overCost}}, 0, CodeRequest},
		{"a query too large", &ReadRequest{Epoch: "0", Sprint: []SprintQuery{relatedOver(t, "p1"), overCost}}, 1, CodeLimit},
		{"a list one too long, after a count on a table that is not there", &ReadRequest{Epoch: "0",
			Tset: []tset.ReadQuery{noTable}, Sprint: []SprintQuery{tooMany}}, 1, CodeLimit},
		{"a list one too long, malformed in its table", &ReadRequest{Epoch: "0",
			Sprint: []SprintQuery{{Kind: sprint.QueryRelated, Query: json.RawMessage(`{"kind":"related","fields":[],"follow":[],"src":{"kind":"ids","ids":[` +
				`"` + strings.Join(manyIDs(10001), `","`) + `"]}}`)}}}, 0, CodeRequest},
	} {
		clock, asked = 0, 0
		ref := readRefusal(t, w, c.rr)
		if *ref.Detail.QueryIndex != c.index || ref.Code != c.code {
			t.Errorf("%s: %s at query %d, want %s at %d", c.name, ref.Code, *ref.Detail.QueryIndex, c.code, c.index)
		}
		if clock != 0 || asked != 0 {
			t.Errorf("%s: the time was sampled %d times and %d sprint queries were asked before the static refusal", c.name, clock, asked)
		}
		// The Lua's validate, for every query in order, refuses the same one.
		_, lref, lindex := h.readSeq(c.rr.Sprint, QueryCharge{})
		if lref == nil || lref.Code != c.code || lindex+len(c.rr.Tset) != c.index {
			t.Errorf("%s: the Lua refused %+v at sprint query %d", c.name, lref, lindex)
		}
	}

	// A read that passes the static phase reads: it samples the time, and a
	// MISSING it finds is at its own index, after the queries before it.
	clock, asked = 0, 0
	ref := readRefusal(t, w, &ReadRequest{Epoch: "0", Sprint: []SprintQuery{relatedOver(t, "p1"), missing}})
	if ref.Code != codeMissing || *ref.Detail.QueryIndex != 1 || clock != 1 || asked != 2 {
		t.Errorf("a read that passes: %s at %d, time sampled %d times, %d queries asked", ref.Code, *ref.Detail.QueryIndex, clock, asked)
	}
	clock = 0
	res, err := Read(context.Background(), w.tw, &ReadRequest{Epoch: "9", Sprint: []SprintQuery{relatedOver(t, "p1")}})
	if err != nil || res.Refusal == nil || res.Refusal.Code != CodeEpochAhead || clock != 1 {
		t.Errorf("an epoch ahead is found by reading: %+v %v, time sampled %d times", res, err, clock)
	}
}

func manyIDs(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = "c" + strconv.Itoa(i)
	}
	return out
}

// TestAReadsQueriesShareOneBudget: the store charges every query of a read,
// Layer 1's and Layer 2's included, against one budget of 10,000 records,
// 20,000 range ids and 20,000 probes (L1 6, 7), so a read whose queries pass a
// bound together is BUDGET at the query that passes it. The twin carries one
// charge from query to query, seeded with what the read's Layer 1 queries
// charged; the Lua on stubs that enforce the bounds as S.charge does, with its
// charges carried, refuses at the same query with the same budget.
func TestAReadsQueriesShareOneBudget(t *testing.T) {
	t.Parallel()
	w := standard(t)
	h := newLuaHarness(t, w)
	listOf := func(first, n int) SprintQuery {
		out := make([]string, n)
		for i := range out {
			out[i] = "r" + strconv.Itoa(first+i)
		}
		return mustEncode(t, sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Work, Source: ids(out...), Fields: []string{}})
	}
	both := func(name string, rr *ReadRequest, seed QueryCharge, index int, code, budget string) {
		t.Helper()
		ref := readRefusal(t, w, rr)
		if *ref.Detail.QueryIndex != index || ref.Code != code || ref.Detail.Budget != budget {
			t.Errorf("%s: the twin refused %s (%s) at query %d, want %s (%s) at %d", name, ref.Code, ref.Detail.Budget, *ref.Detail.QueryIndex, code, budget, index)
		}
		_, lref, lindex := h.readSeq(rr.Sprint, seed)
		if lref == nil || lref.Code != code || lref.Budget != budget || lindex+len(rr.Tset) != index {
			t.Errorf("%s: the Lua refused %+v at sprint query %d", name, lref, lindex)
		}
	}

	// Each of two lists of 6,000 ids is within the bound and both are not.
	alone := &ReadRequest{Epoch: "0", Sprint: []SprintQuery{listOf(0, 6000)}}
	if res, err := Read(context.Background(), w.tw, alone); err != nil || res.Read == nil {
		t.Fatalf("one list of 6,000 ids: %+v %v", res, err)
	}
	both("two lists of 6,000 ids", &ReadRequest{Epoch: "0", Sprint: []SprintQuery{listOf(0, 6000), listOf(6000, 6000)}},
		QueryCharge{}, 1, codeBudget, "record")

	// A read's Layer 1 queries are charged first: 5,000 ids of Layer 1's own, and
	// then 6,000 of the sprint's, pass 10,000 records at the sprint's query.
	l1 := tset.ReadQuery{Kind: "ids", Table: sprint.Work, IDs: manyIDs(5000), Fields: []string{}}
	rep, err := w.m.Read(context.Background(), newTSetReadPlan(testPrefix, "0", "atomic", []tset.ReadQuery{l1}))
	if err != nil {
		t.Fatal(err)
	}
	var seed QueryCharge
	seed.addCounters(rep.Counters)
	if seed.Records != 5000 {
		t.Fatalf("Layer 1's twin counted %+v for 5,000 ids", seed)
	}
	both("Layer 1's ids, then a list of 6,000", &ReadRequest{Epoch: "0", Tset: []tset.ReadQuery{l1}, Sprint: []SprintQuery{listOf(0, 6000)}},
		seed, 1, codeBudget, "record")
	// The same sprint query, without the Layer 1 query before it, is answered.
	if res, err := Read(context.Background(), w.tw, &ReadRequest{Epoch: "0", Sprint: []SprintQuery{listOf(0, 6000)}}); err != nil || res.Read == nil {
		t.Fatalf("%+v %v", res, err)
	}

	// Probes: a read of key queries has no records. Six of 2,000 subjects, each
	// with a name to read, make 4,000 probes each; the fifth reaches 20,000 and
	// the sixth passes it.
	subjects := manyIDs(2000)
	jopen := mustEncodeKey(t, KeyQ{Kind: KeyJOpen, Subjects: subjects, Names: []string{"t|c"}})
	six := &ReadRequest{Epoch: "0", Sprint: []SprintQuery{jopen, jopen, jopen, jopen, jopen, jopen}}
	both("six jopen reads of 2,000 subjects", six, QueryCharge{}, 5, codeBudget, "cell")
	five := &ReadRequest{Epoch: "0", Sprint: six.Sprint[:5]}
	if res, err := Read(context.Background(), w.tw, five); err != nil || res.Read == nil {
		t.Fatalf("five jopen reads are at the bound and within it: %+v %v", res, err)
	}
	if _, lref, _ := h.readSeq(five.Sprint, QueryCharge{}); lref != nil {
		t.Fatalf("the Lua refused five: %+v", lref)
	}
}

// TestLuaValidateOrderEqualsTwin: the Lua's validate and the twin's check
// refuse the same queries with the same code when a query is both malformed and
// too large (finding 4 of the cold read): REQUEST for the shape before LIMIT for
// the size, in both halves. The first cases are the cold read's probe, a list of
// 10,001 ids with its fields missing or null, its table missing, or one
// element a number; the rest are queries near every bound of every kind, each
// mutated 40 times below one of its members (a member deleted, retyped, put out
// of range, repeated, or one unknown added).
func TestLuaValidateOrderEqualsTwin(t *testing.T) {
	t.Parallel()
	w := standard(t)
	h := newLuaHarness(t, w)
	list := func(n int) string { return `"` + strings.Join(manyIDs(n), `","`) + `"` }
	compare := func(label string, q SprintQuery) (gcode string) {
		t.Helper()
		_, gref := DecodeSprintQ(q)
		lref := h.validate(q)
		var lcode string
		if lref != nil {
			lcode = lref.Code
		}
		if gref != nil {
			gcode = gref.Code
		}
		if lcode != gcode {
			t.Errorf("%s: the Lua's validate says %q, the twin %q\n%.300s", label, lcode, gcode, q.Query)
		}
		return gcode
	}
	many := list(10001)
	for _, c := range []struct {
		name, kind, raw, want string
	}{
		{"fields missing", "related", `{"kind":"related","t":"work","follow":[],"src":{"kind":"ids","ids":[` + many + `]}}`, CodeRequest},
		{"fields null", "related", `{"kind":"related","t":"work","fields":null,"follow":[],"src":{"kind":"ids","ids":[` + many + `]}}`, CodeRequest},
		{"t missing", "related", `{"kind":"related","fields":[],"follow":[],"src":{"kind":"ids","ids":[` + many + `]}}`, CodeRequest},
		{"an element a number", "related", `{"kind":"related","t":"work","fields":[],"follow":[],"src":{"kind":"ids","ids":[` + many + `,5]}}`, CodeRequest},
		{"an element a bad name", "related", `{"kind":"related","t":"work","fields":[],"follow":[],"src":{"kind":"ids","ids":[` + many + `,"a b"]}}`, CodeRequest},
		{"a repeated element", "related", `{"kind":"related","t":"work","fields":[],"follow":[],"src":{"kind":"ids","ids":[` + many + `,"c7"]}}`, CodeRequest},
		{"an unknown follow", "related", `{"kind":"related","t":"work","fields":[],"follow":["nothing"],"src":{"kind":"ids","ids":[` + many + `]}}`, CodeRequest},
		{"a field that is reserved", "related", `{"kind":"related","t":"work","fields":["revision"],"follow":[],"src":{"kind":"ids","ids":[` + many + `]}}`, CodeRequest},
		{"a key that is not the query's", "related", `{"kind":"related","t":"work","fields":[],"follow":[],"extra":1,"src":{"kind":"ids","ids":[` + many + `]}}`, CodeRequest},
		{"a source key that is not the source's", "related", `{"kind":"related","t":"work","fields":[],"follow":[],"src":{"kind":"ids","ids":[` + many + `],"limit":1}}`, CodeRequest},
		{"well formed and too many", "related", `{"kind":"related","t":"work","fields":[],"follow":[],"src":{"kind":"ids","ids":[` + many + `]}}`, CodeLimit},
		{"waiters, limit 0", "waiters", `{"kind":"waiters","limit":0,"fields":[],"src":{"kind":"ids","ids":[` + many + `]}}`, CodeRequest},
		{"waiters, too many", "waiters", `{"kind":"waiters","limit":1,"fields":[],"src":{"kind":"ids","ids":[` + many + `]}}`, CodeLimit},
		{"needchain, limit 0", "needchain", `{"kind":"needchain","limit":0,"fields":[],"src":{"kind":"ids","ids":[` + many + `]}}`, CodeRequest},
		{"needchain, too many", "needchain", `{"kind":"needchain","limit":5,"fields":[],"src":{"kind":"ids","ids":[` + many + `]}}`, CodeLimit},
		{"jnote, a note id that is not one", "jnote", `{"kind":"jnote","fields":[],"src":{"kind":"ids","ids":["x1"]}}`, CodeRequest},
		{"jnote, too many", "jnote", `{"kind":"jnote","fields":[],"subjects":1,"src":{"kind":"ids","ids":[` + noteList(10001) + `]}}`, CodeLimit},
		{"jnote, too many and one is no note", "jnote", `{"kind":"jnote","fields":[],"subjects":1,"src":{"kind":"ids","ids":[` + noteList(10001) + `,"x1"]}}`, CodeRequest},
		{"front, over cost and a limit of 0 after it", "front", `{"kind":"front","fields":[],"stream":"s","heads":[{"index":"elig","limit":1000,"follow":["rcards"]},{"index":"again","limit":0,"follow":[]}]}`, CodeRequest},
		{"front, over cost", "front", `{"kind":"front","fields":[],"stream":"s","heads":[{"index":"elig","limit":1000,"follow":["rcards"]}]}`, CodeLimit},
		{"streams, over cost and a limit that is a string", "streams", `{"kind":"streams","fields":[],"limit":"100"}`, CodeRequest},
		{"streams, over cost", "streams", `{"kind":"streams","fields":[],"limit":100}`, CodeLimit},
	} {
		got := compare(c.name, SprintQuery{Kind: c.kind, Query: json.RawMessage(c.raw)})
		if got != c.want {
			t.Errorf("%s: refused %q, want %q", c.name, got, c.want)
		}
	}

	// Queries near every bound, mutated below a member chosen first.
	bases := []string{
		`{"kind":"related","t":"work","fields":["open"],"follow":[],"src":{"kind":"ids","ids":[` + list(10001) + `]}}`,
		`{"kind":"related","t":"work","fields":[],"follow":["work"],"src":{"kind":"ids","ids":[` + list(5001) + `]}}`,
		`{"kind":"related","t":"work","fields":[],"follow":["needs"],"src":{"kind":"ids","ids":[` + list(155) + `]}}`,
		`{"kind":"waiters","limit":100,"fields":[],"src":{"kind":"ids","ids":[` + list(101) + `]}}`,
		`{"kind":"needchain","limit":10000,"fields":[],"src":{"kind":"head","key":"missing","limit":2000}}`,
		`{"kind":"needchain","limit":10000,"fields":[],"src":{"kind":"ids","ids":[` + list(10001) + `]}}`,
		`{"kind":"jnote","fields":[],"src":{"kind":"ids","ids":[` + noteList(3) + `]}}`,
		`{"kind":"jnote","fields":[],"subjects":1,"src":{"kind":"ids","ids":[` + noteList(10001) + `]}}`,
		`{"kind":"front","fields":[],"stream":"s","heads":[{"index":"elig","limit":626,"follow":["rcards"]},{"index":"again","limit":5,"follow":[]}]}`,
		`{"kind":"streams","fields":[],"limit":81,"units":250}`,
		`{"kind":"fleet","fields":["a"],"units":251}`,
		`{"kind":"readers","fields":["a"],"units":1024}`,
	}
	rng := rand.New(rand.NewSource(20260930))
	refused := map[string]int{}
	for _, raw := range bases {
		var obj map[string]any
		if err := json.Unmarshal([]byte(raw), &obj); err != nil {
			t.Fatal(err)
		}
		kind := obj["kind"].(string)
		compare("the base of "+kind, SprintQuery{Kind: kind, Query: json.RawMessage(raw)})
		for i := 0; i < 40; i++ {
			b, _ := json.Marshal(mutateBelow(rng, obj))
			q := SprintQuery{Kind: kind, Query: b}
			refused[compare(fmt.Sprintf("%s #%d", kind, i), q)]++
		}
	}
	if refused[CodeRequest] < 100 || refused[CodeLimit] < 20 || refused[""] < 5 {
		t.Fatalf("the mutations were refused REQUEST %d, LIMIT %d times and accepted %d: the test does not test all three", refused[CodeRequest], refused[CodeLimit], refused[""])
	}
}

func noteList(n int) string {
	out := make([]string, n)
	for i := range out {
		out[i] = "n" + strconv.Itoa(i+1)
	}
	return `"` + strings.Join(out, `","`) + `"`
}

// mutateBelow is mutate with the member to change chosen first, uniformly
// among the members of the query, so that a long list of ids is not the only
// place a change lands: it picks one top-level key (or adds an unknown one) and
// changes one place below it.
func mutateBelow(rng *rand.Rand, v map[string]any) map[string]any {
	keys := make([]string, 0, len(v))
	for k := range v {
		if k != "kind" {
			keys = append(keys, k)
		}
	}
	// A stable order, so that a seed gives the same mutations.
	for i := range keys {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	copied := func(x any) any {
		b, _ := json.Marshal(x)
		var out any
		_ = json.Unmarshal(b, &out)
		return out
	}
	root := copied(v).(map[string]any)
	if len(keys) == 0 || rng.Intn(len(keys)+1) == len(keys) {
		root["zz_unknown"] = 1
		return root
	}
	key := keys[rng.Intn(len(keys))]
	sub := map[string]any{key: root[key]}
	changed := mutate(rng, sub)
	root[key] = changed[key]
	if _, added := changed["zz_unknown"]; added {
		root["zz_unknown"] = changed["zz_unknown"]
	}
	if _, gone := changed[key]; !gone {
		delete(root, key)
	}
	return root
}

// TestReadsWriteNothingAndRunTwiceAlike: a read of sprint queries writes
// nothing, and the same read run twice on the same state is the same answer
// (or the same refusal) both times, however many queries it holds and whether
// they refuse: nothing of one read's budget or static phase is left for the
// next. Random reads on the six worlds, one query each and six at a time, run
// twice each; the twin's whole image (tables, sprint keys, log) is the same
// after as before.
func TestReadsWriteNothingAndRunTwiceAlike(t *testing.T) {
	t.Parallel()
	names := make([]string, 0, 6)
	worlds := luaWorlds(t)
	for n := range worlds {
		names = append(names, n)
	}
	sort.Strings(names)
	ran := 0
	for _, name := range names {
		w := worlds[name]
		before := string(image(t, w.tw, w.m, w.log))
		result := func(rr *ReadRequest) string {
			res, err := Read(context.Background(), w.tw, rr)
			if err != nil {
				return "error " + err.Error()
			}
			b, merr := json.Marshal(struct {
				Read    *ReadReply
				Refusal *Refusal
			}{res.Read, res.Refusal})
			if merr != nil {
				t.Fatal(merr)
			}
			return string(b)
		}
		rqs := randomQueries(w, int64(len(name))*31, 90)
		for i := 0; i < len(rqs); i += 6 {
			var batch []SprintQuery
			for _, rq := range rqs[i:min(i+6, len(rqs))] {
				batch = append(batch, rq.Enc)
			}
			for _, rr := range []*ReadRequest{{Epoch: "0", Sprint: batch[:1]}, {Epoch: "0", Sprint: batch}} {
				first, second := result(rr), result(rr)
				if first != second {
					t.Fatalf("%s #%d: the same read answered twice differently\n%.400s\n%.400s", name, i, first, second)
				}
				ran++
			}
		}
		if after := string(image(t, w.tw, w.m, w.log)); after != before {
			t.Fatalf("%s: a read changed the twin", name)
		}
	}
	if ran < 100 {
		t.Fatalf("only %d reads ran", ran)
	}

	// A read that spends its whole budget, run twice: what the first read charged
	// is not left to the second. Five jopen reads make exactly the 20,000 probes
	// a read may; two lists make exactly its 10,000 records.
	w := standard(t)
	list := func(first, n int) SprintQuery {
		out := make([]string, n)
		for i := range out {
			out[i] = "r" + strconv.Itoa(first+i)
		}
		return mustEncode(t, sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Work, Source: ids(out...), Fields: []string{}})
	}
	jopen := mustEncodeKey(t, KeyQ{Kind: KeyJOpen, Subjects: manyIDs(2000), Names: []string{"t|c"}})
	for name, rr := range map[string]*ReadRequest{
		"five jopen reads":   {Epoch: "0", Sprint: []SprintQuery{jopen, jopen, jopen, jopen, jopen}},
		"two lists":          {Epoch: "0", Sprint: []SprintQuery{list(0, 6000), list(6000, 4000)}},
		"a sixth jopen read": {Epoch: "0", Sprint: []SprintQuery{jopen, jopen, jopen, jopen, jopen, jopen}},
	} {
		res1, err1 := Read(context.Background(), w.tw, rr)
		res2, err2 := Read(context.Background(), w.tw, rr)
		if err1 != nil || err2 != nil || (res1.Read == nil) != (res2.Read == nil) ||
			(res1.Refusal == nil) != (res2.Refusal == nil) || (res1.Refusal != nil && *res1.Refusal.Detail.QueryIndex != *res2.Refusal.Detail.QueryIndex) {
			t.Fatalf("%s: the same read at its bound answered differently the second time: %+v %+v", name, res1, res2)
		}
		if (name == "a sixth jopen read") != (res1.Refusal != nil) {
			t.Fatalf("%s: %+v", name, res1)
		}
	}
}
