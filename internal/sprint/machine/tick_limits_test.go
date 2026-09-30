package machine

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/stepbuild"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// TestTickBudgetBoundary: the tick budget at its bounds, each to the one
// (1.4.2): 10,000 changes, 2,500 entries and notes, 2 MiB of requests fit
// and one more does not; 32 steps fit and a 33rd does not; the quarantine
// step's reserve is counted; R16's changes count against its own 2,000 cards
// and not the others' 10,000.
func TestTickBudgetBoundary(t *testing.T) {
	t.Parallel()
	b := DefaultBudget()
	if b.Changes != 10000 || b.EntriesNotes != 2500 || b.RequestBytes != 2<<20 || b.Steps != 32 || b.HeldCards != 2000 {
		t.Fatalf("the budget %+v", b)
	}
	dealt := func(reserve spend, ps ...*planned) int {
		order, _, _ := deal(ps, b, reserve)
		return len(order)
	}
	one := func(rule string, c stepCost) *planned {
		return &planned{batch: Batch{Rule: rule}, reqs: []*sprintfn.Request{{}}, costs: []stepCost{c}}
	}
	for _, c := range []struct {
		c    stepCost
		want int
	}{
		{stepCost{entriesNotes: 2500}, 1}, {stepCost{entriesNotes: 2501}, 0},
		{stepCost{bytes: 2 << 20}, 1}, {stepCost{bytes: 2<<20 + 1}, 0},
		{stepCost{changes: 10000}, 1}, {stepCost{changes: 10001}, 0},
	} {
		if got := dealt(spend{}, one("deal", c.c)); got != c.want {
			t.Errorf("cost %+v: dealt %d, want %d", c.c, got, c.want)
		}
	}
	if got := dealt(spend{entriesNotes: 2400, steps: 1}, one("deal", stepCost{entriesNotes: 101})); got != 0 {
		t.Errorf("the reserve is not counted: dealt %d", got)
	}
	many := &planned{batch: Batch{Rule: "resolve"}}
	for i := 0; i < 33; i++ {
		many.reqs, many.costs = append(many.reqs, &sprintfn.Request{}), append(many.costs, stepCost{changes: 1})
	}
	if got := dealt(spend{}, many); got != 32 {
		t.Errorf("dealt %d steps of 33; want 32", got)
	}
	if got := dealt(spend{}, one("resolve", stepCost{changes: 10000}), one(ruleHeld, stepCost{changes: 2000})); got != 2 {
		t.Errorf("R16's 2,000 cards beside 10,000 changes: dealt %d, want 2", got)
	}
	if got := dealt(spend{}, one(ruleHeld, stepCost{changes: 2001})); got != 0 {
		t.Errorf("R16's 2,001 cards: dealt %d, want 0", got)
	}
}

// TestTickParksAtAChunkOfOne: a LIMIT halves a key's chunk each time, and
// parks it only at a chunk of one: 2,000 halves to one at the eleventh
// halving (rounding up), so a key at ten halvings (a chunk of two) is halved
// again and one at eleven is parked (1.3.5; SprintEvents.tla, chunk1).
func TestTickParksAtAChunkOfOne(t *testing.T) {
	t.Parallel()
	if sprint.Halved(stepbuild.LimitCandidates, 10) != 2 || sprint.Halved(stepbuild.LimitCandidates, 11) != 1 {
		t.Fatal("the chunk's halvings")
	}
	w := newWorld(t)
	l := w.loop("a", []sprint.Rule{dealRule(64)}, Budget{})
	key := []sprint.AgendaKey{{Key: "deal", Seq: 1}}
	rep := Report{}
	if notes := l.halve(Batch{Rule: "deal", Keys: key, Halvings: 10}, sprintfn.CodeLimit, "", "its step", &rep); len(notes) != 1 || l.halvings["deal"] != 11 || len(rep.Parked) != 0 {
		t.Fatalf("at ten halvings: notes %d, halvings %d, parked %v", len(notes), l.halvings["deal"], rep.Parked)
	}
	if notes := l.halve(Batch{Rule: "deal", Keys: key, Halvings: 11}, sprintfn.CodeLimit, "", "its step", &rep); len(notes) != 0 || strings.Join(rep.Parked, ",") != "deal" || !l.owed.owes("deal") {
		t.Fatalf("at eleven halvings: notes %d, parked %v", len(notes), rep.Parked)
	}
	two := append(key, sprint.AgendaKey{Key: "deal:x", Seq: 2})
	if notes := l.halve(Batch{Rule: "deal", Keys: two, Halvings: 11}, sprintfn.CodeLimit, "", "its step", &Report{}); len(notes) != 1 {
		t.Fatal("a batch of two keys at a chunk of one is parked; want halved (its read is cut)")
	}
}

// TestTickCutPlanCarriesNoDone: a plan the builder cuts into several requests
// removes none of its keys (every request's Done is dropped, 1.3.6;
// SprintEvents.tla Apply, W27), and a plan of one request keeps its Done.
func TestTickCutPlanCarriesNoDone(t *testing.T) {
	t.Parallel()
	for _, n := range []int{1, 2} {
		build := func(rp sprint.RulePlan, m sprintfn.Meta, b stepbuild.Bounds) ([]sprintfn.Body, error) {
			var out []sprintfn.Body
			for i := 0; i < n; i++ {
				out = append(out, sprintfn.Body{Entries: []tset.Entry{}, Done: []string{"deal"}})
			}
			return out, nil
		}
		l, err := NewLoop(Config{Names: testNames, Owner: "token-a", Name: "a", Rules: []sprint.Rule{dealRule(64)}, Build: build})
		if err != nil {
			t.Fatal(err)
		}
		p, err := l.cut(dealRule(64), Batch{Rule: "deal", Keys: []sprint.AgendaKey{{Key: "deal", Seq: 1}}}, sprint.RulePlan{}, &Report{})
		if err != nil || p == nil || len(p.reqs) != n || p.whole != (n == 1) {
			t.Fatalf("%d bodies: %v %+v", n, err, p)
		}
		for i, req := range p.reqs {
			if (len(req.Body.Done) == 1) != (n == 1) {
				t.Fatalf("%d bodies: request %d carries Done %v", n, i, req.Body.Done)
			}
		}
	}
}

// TestTickIngestStaleGen: an ingest refused STALEGEN ends the tick after RT2:
// another loop took the lease, so nothing this tick planned would apply, and
// the loop holds no generation until its next lease step (1.4.2, T1).
func TestTickIngestStaleGen(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.rows("s1")
	ic := &intercept{c: w.tw}
	k := &counting{c: ic}
	l := w.loop("a", []sprint.Rule{dealRule(64)}, Budget{})
	w.tick(l, k)
	ic.answer = func(it sprintfn.Item) *sprintfn.Result {
		if it.Step != nil && it.Step.Ingest != nil {
			return &sprintfn.Result{Refusal: &sprintfn.Refusal{Code: sprintfn.CodeStaleGen}}
		}
		return nil
	}
	w.verb(create("s1:ready", fresh(), "p1"))
	w.clk.add(TickEvery)
	rep := w.tick(l, k)
	if rep.RoundTrips != 2 || len(rep.Dealt) != 0 || rep.Refused[sprintfn.CodeStaleGen] != 1 || l.gen != 0 || w.place("p1") != "s1:ready" {
		t.Fatalf("an ingest refused STALEGEN: %+v, gen %d", rep, l.gen)
	}
}

// TestTickAgendaHead: RT1 reads the agenda's head in one range of 2,000 keys,
// and with keys held back in bands cut at their orders, each of 2,000, at
// most nine; the head it gives is the bands up to the first that was cut
// (1.3.5: "2,000 + h keys of the agenda's head").
func TestTickAgendaHead(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	l := w.loop("a", []sprint.Rule{dealRule(64)}, Budget{})
	shape := func() string {
		var out []string
		for _, q := range l.agendaRanges() {
			out = append(out, fmt.Sprintf("[%s %s %d]", q.Min, q.Max, q.Limit))
		}
		return strings.Join(out, "")
	}
	if got := shape(); got != "[-inf +inf 2000]" {
		t.Fatalf("no key held back: %s", got)
	}
	l.heldSeq = map[string]uint64{"deal": 7, "pullback:s1": 3, "resolve:s1": 7}
	if got := shape(); got != "[-inf 3 2000][(3 7 2000][(7 +inf 2000]" {
		t.Fatalf("keys held back at 3 and 7: %s", got)
	}
	for i := 0; i < 20; i++ {
		l.heldSeq[fmt.Sprintf("k%d", i)] = uint64(100 + i)
	}
	if n := len(l.agendaRanges()); n != agendaBandsMax {
		t.Fatalf("%d bands; want %d", n, agendaBandsMax)
	}
	band := func(more bool, ks ...string) tset.ReadAnswer {
		a := tset.ReadAnswer{HasMore: more}
		for i, k := range ks {
			a.IDs, a.Scores = append(a.IDs, k), append(a.Scores, fmt.Sprint(i+1))
		}
		return a
	}
	head, more := parseHead(t, []tset.ReadAnswer{band(false, "a"), band(true, "b", "c"), band(false, "d")})
	if head != "a,b,c" || !more {
		t.Fatalf("the head %s (more %v); want a,b,c cut at the second band", head, more)
	}
	head, more = parseHead(t, []tset.ReadAnswer{band(false, "a"), band(false, "b")})
	if head != "a,b" || more {
		t.Fatalf("the head %s (more %v)", head, more)
	}

	// On the twin, the bands' exclusive bounds are read as Layer 1 reads them.
	w.rows("s1")
	k := &counting{c: w.tw}
	w.tick(l, k)
	w.verb(create("s1:ready", fresh(), "p1"))
	w.clk.add(TickEvery)
	w.tick(l, k)
	w.clk.add(TickEvery)
	if rep := w.tick(l, k); rep.Err != nil {
		t.Fatal(rep.Err)
	}
}

// parseHead is the agenda head parseRT1 gives from bands of answers.
func parseHead(t *testing.T, bands []tset.ReadAnswer) (string, bool) {
	t.Helper()
	w := newWorld(t)
	l := w.loop("a", []sprint.Rule{dealRule(64)}, Budget{})
	k := &counting{c: w.tw}
	w.tick(l, k)
	rt := k.sent[0]
	var read *sprintfn.ReadRequest
	for _, it := range rt {
		if it.Read != nil {
			read = it.Read
		}
	}
	res, err := sprintfn.Read(t.Context(), w.tw, read)
	if err != nil || res.Read == nil {
		t.Fatal(err)
	}
	reply := *res.Read
	reply.Tset = append(append([]tset.ReadAnswer{}, bands...), reply.Tset[1:]...)
	rd, err := parseRT1(&reply, len(bands))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(keysOfOrdered(rd.agenda), ","), rd.agendaMore
}

// keysOfOrdered is the keys of a list in its order.
func keysOfOrdered(ks []sprint.AgendaKey) []string {
	out := make([]string, len(ks))
	for i, k := range ks {
		out[i] = k.Key
	}
	return out
}

// TestHeartbeatRulesFieldCleared: the rules field is the tick's rules as
// JSON, and empty when that is over 8 KiB, never the last tick's (1.4.1).
func TestHeartbeatRulesFieldCleared(t *testing.T) {
	t.Parallel()
	small := map[string]*RuleStat{"deal": {Keys: 1}}
	if got := rulesField(small); got != `{"deal":{"keys":1}}` {
		t.Fatalf("the field %s", got)
	}
	big := map[string]*RuleStat{}
	for i := 0; len(rulesField(big)) != 0; i++ {
		big[fmt.Sprintf("rule-%04d", i)] = &RuleStat{Keys: i, Refused: map[string]int{"NOCOL": i}}
	}
	if got := rulesField(big); got != "" {
		t.Fatalf("over 8 KiB: %d bytes", len(got))
	}
}
