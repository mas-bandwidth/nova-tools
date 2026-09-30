package machine

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// dealRule is the tests' deal (R6's key): the head of fresh:s1, each card to
// working at attempt 1.
func dealRule(limit int, follow ...string) sprint.Rule {
	return testRule("deal", headRead(sprint.IndexFresh, limit, []string{"kind", "attempt", sprint.PrimaryField}, follow),
		moveAll("ready", "working", map[string]string{"attempt": "1"}))
}

// releaseRule is the tests' release (R3's key resolve:s): the head of elig:s1,
// each card to ready, never dealt.
func releaseRule(limit int) sprint.Rule {
	return testRule("resolve", headRead(sprint.IndexElig, limit, []string{"kind", "open"}, nil),
		moveAll("waiting", "ready", map[string]string{"attempt": "0"}))
}

// TestTickIdleOneRoundTrip: a tick with no new line and nothing due is one
// round trip, and writes only its lease renewal and its heartbeat (1.4.2, T5;
// E8 counts the same on the store).
func TestTickIdleOneRoundTrip(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.rows("s1")
	w.verb(create("s1:ready", fresh(), "p1", "p2"))
	k := &counting{c: w.tw}
	l := w.loop("a", []sprint.Rule{dealRule(64)}, Budget{})
	// The first tick takes the lease and learns the cursor; the next ingests
	// and deals; the one after ingests the deal's own line; then the machine
	// is idle.
	w.tick(l, k)
	if rep := w.tick(l, k); rep.Lines == 0 || rep.Applied == 0 {
		t.Fatalf("the busy tick: %+v", rep)
	}
	if rep := w.tick(l, k); rep.Lines != 1 || rep.RoundTrips != 2 {
		t.Fatalf("the tick after: %+v", rep)
	}
	for i := 0; i < 3; i++ {
		w.clk.add(TickEvery)
		lines := len(w.log.Lines(testNames.Prefix, "0"))
		agenda := w.zset("agenda@0")
		rep := w.tick(l, k)
		if rep.RoundTrips != 1 || !rep.Held {
			t.Fatalf("idle tick %d: %d round trips, held %v, %+v", i, rep.RoundTrips, rep.Held, rep)
		}
		if n := len(w.log.Lines(testNames.Prefix, "0")); n != lines {
			t.Fatalf("an idle tick wrote %d lines", n-lines)
		}
		if got := w.zset("agenda@0"); len(got) != len(agenda) {
			t.Fatalf("an idle tick changed the agenda: %v -> %v", agenda, got)
		}
		items := k.last()
		if len(items) != 3 || items[0].Step == nil || items[0].Step.Lease == nil || items[1].Page == nil || items[2].Read == nil {
			t.Fatalf("an idle tick's round trip is not the lease step, the page and the read: %+v", items)
		}
	}
}

// TestTickBusyAtMostThree: a busy tick (new lines, keys, a rule's read, its
// steps) is at most three round trips (1.4.2): RT1, RT2 (the ingest and the
// reads) and RT3 (the steps, in one flush).
func TestTickBusyAtMostThree(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.rows("s1")
	k := &counting{c: w.tw}
	l := w.loop("a", []sprint.Rule{dealRule(64), releaseRule(64)}, Budget{})
	w.tick(l, k)
	w.verb(create("s1:ready", fresh(), "p1", "p2"), create("s1:waiting", waiting(), "q1", "q2", "q3"))
	rep := w.tick(l, k)
	if rep.RoundTrips != 3 {
		t.Fatalf("a busy tick took %d round trips: %+v", rep.RoundTrips, rep)
	}
	if len(rep.Dealt) != 2 || rep.Applied != 2 || rep.Read["deal"] != 1 || rep.Read["resolve"] != 1 {
		t.Fatalf("the busy tick did not deal and release: %+v", rep)
	}
	rt2, rt3 := k.sent[len(k.sent)-2], k.sent[len(k.sent)-1]
	if rt2[0].Step == nil || rt2[0].Step.Ingest == nil || len(rt2) != 3 {
		t.Fatalf("RT2 is not the ingest and one read a rule: %+v", rt2)
	}
	for _, it := range rt3 {
		if it.Step == nil || it.Step.Meta.Gen != rep.Gen || !it.Step.Meta.Tick {
			t.Fatalf("an RT3 step without the lease generation (T1): %+v", it)
		}
	}
	for _, id := range []string{"p1", "p2"} {
		if p := w.place(id); p != "s1:working" {
			t.Fatalf("%s is at %q, not dealt", id, p)
		}
	}
	for _, id := range []string{"q1", "q2", "q3"} {
		if p := w.place(id); p != "s1:ready" {
			t.Fatalf("%s is at %q, not released", id, p)
		}
	}
	// What RT3 made due (the released cards' deal) is ingested and dealt the
	// next tick, again in at most three.
	w.clk.add(TickEvery)
	rep = w.tick(l, k)
	if rep.RoundTrips > 3 || w.place("q1") != "s1:working" {
		t.Fatalf("the next tick: %d round trips, q1 at %q", rep.RoundTrips, w.place("q1"))
	}
	// The busy tick's rules are in the heartbeat, by field (1.4.1).
	if rules := w.hash("heartbeat")["rules"]; !strings.Contains(rules, `"deal":{"keys":1,"steps":1,"applied":1,"changes":2}`) {
		t.Fatalf("the heartbeat's rules: %s", rules)
	}
}

// bigRule is a rule whose plan, whatever it reads, moves n cards of s1 (ids
// the tests make up: the steps are refused, which these tests do not look
// at), one unit each, with a field of pad bytes; each unit its own entry when
// apart.
func bigRule(name string, n, pad int) sprint.Rule {
	return testRule(name, headRead(sprint.IndexElig, 1, []string{"kind"}, nil),
		func(s *sprint.Snapshot, keys []sprint.AgendaKey, now sprint.Now) sprint.RulePlan {
			var rp sprint.RulePlan
			set := map[string]string{"attempt": "0"}
			if pad > 0 {
				set["pad"] = strings.Repeat("x", pad)
			}
			for i := 0; i < n; i++ {
				rp.Plan.Units = append(rp.Plan.Units, moveUnit(sprint.Work, fmt.Sprintf("r%d", i), "s1", "waiting", "ready", set))
			}
			rp.Done = keys
			return rp
		})
}

// busyWorld is a world with a loop that has learned the cursor, and a fresh
// card in s1: its line queues resolve:s1 and deal (2.1).
func busyWorld(t *testing.T, rules []sprint.Rule, b Budget, o builderOpts) (*world, *Loop, *counting) {
	t.Helper()
	w := newWorld(t)
	w.rows("s1")
	k := &counting{c: w.tw}
	l, err := NewLoop(Config{Names: testNames, Owner: "token-a", Name: "a", Rules: rules, Build: testBuild(o), Budget: b})
	if err != nil {
		t.Fatal(err)
	}
	w.tick(l, k)
	w.verb(create("s1:ready", fresh(), "p1"))
	return w, l, k
}

// TestTickRoundRobin: every rule with keys gets a step before any gets a
// second, so a release of 100,000 does not starve a deal (1.4.2;
// SprintEvents.tla Rounds): the release's first step, then the deal's, then
// the release's next steps until the budget of 10,000 changes is spent.
func TestTickRoundRobin(t *testing.T) {
	t.Parallel()
	w, l, k := busyWorld(t, []sprint.Rule{bigRule("resolve", 100000, 0), dealRule(64)}, Budget{}, builderOpts{})
	rep := w.tick(l, k)
	want := []string{"resolve", "deal", "resolve", "resolve", "resolve"}
	if strings.Join(rep.Dealt, ",") != strings.Join(want, ",") {
		t.Fatalf("dealt %v, want %v", rep.Dealt, want)
	}
	if rep.Changes > DefaultBudget().Changes || rep.Changes != 4*2000+1 {
		t.Fatalf("changes %d: the release's four steps of 2,000 and the deal's one", rep.Changes)
	}
	if w.place("p1") != "s1:working" {
		t.Fatalf("the deal was starved: p1 is at %s", w.place("p1"))
	}
	if rep.RoundTrips != 3 {
		t.Fatalf("%d round trips", rep.RoundTrips)
	}
}

// TestTickReadsWithinBounds: each rule's keys are cut to what its read fits in
// Layer 1's read bounds by the queries' declared costs, and what does not fit
// stays queued: the read never costs a fourth round trip (1.4.2).
func TestTickReadsWithinBounds(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	streams := ids("s", 200)
	w.rows(streams...)
	var es []tset.Entry
	for i, s := range streams {
		es = append(es, create(s+":waiting", waiting(), fmt.Sprintf("q%d", i+1)))
	}
	w.verb(es...)
	perKey := func(k sprint.AgendaKey) sprint.SprintQ {
		return sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Work, Fields: []string{"kind", "open"},
			Source: sprint.IDSource{Kind: sprint.SourceHead, Key: sprint.IndexElig + ":" + strings.TrimPrefix(k.Key, "resolve:"), Limit: 60}}
	}
	release := testRule("resolve", func(keys []sprint.AgendaKey, b sprint.ReadBounds, h int) (sprint.ReadPlan, []sprint.AgendaKey) {
		take, rest := sprint.FitKeys(keys, func(k sprint.AgendaKey) sprint.Cost { return sprint.QueryCost(perKey(k)) }, sprint.Cost{}, b, h)
		var rp sprint.ReadPlan
		for _, k := range take {
			rp.Sprint = append(rp.Sprint, perKey(k))
		}
		return rp, rest
	}, func(s *sprint.Snapshot, keys []sprint.AgendaKey, now sprint.Now) sprint.RulePlan {
		var rp sprint.RulePlan
		for _, c := range s.Work.LoadedCards() {
			rp.Plan.Units = append(rp.Plan.Units, moveUnit(sprint.Work, c.ID, c.Row, "waiting", "ready", map[string]string{"attempt": "0"}))
		}
		rp.Done = keys
		return rp
	})
	k := &counting{c: w.tw}
	l := w.loop("a", []sprint.Rule{release}, Budget{})
	w.tick(l, k)
	released := 0
	for i := 0; i < 4 && released < 200; i++ {
		w.clk.add(TickEvery)
		rep := w.tick(l, k)
		if rep.RoundTrips > 3 {
			t.Fatalf("tick %d took %d round trips", i, rep.RoundTrips)
		}
		if i == 0 && (rep.Read["resolve"] != 10000/60 || rep.Left["resolve"] != 200-10000/60) {
			t.Fatalf("the first read took %d keys and left %d; want %d and %d", rep.Read["resolve"], rep.Left["resolve"], 10000/60, 200-10000/60)
		}
		for _, round := range k.sent[len(k.sent)-rep.RoundTrips:] {
			for _, it := range round {
				if it.Read == nil || len(it.Read.Sprint) == 0 {
					continue
				}
				var c sprint.Cost
				for _, q := range it.Read.Sprint {
					if q.Kind != sprint.QueryRelated {
						continue // RT1's sprint-key reads
					}
					sq, ref := sprintfn.DecodeSprintQ(q)
					if ref != nil {
						t.Fatal(ref)
					}
					c = c.Add(sprint.QueryCost(sq))
				}
				if b := sprint.L1ReadBounds(); len(it.Read.Sprint) > b.Queries || c.Records > b.Records || c.RangeIDs > b.RangeIDs {
					t.Fatalf("a read of %d queries declares %+v, over %+v", len(it.Read.Sprint), c, b)
				}
			}
		}
		released = 0
		for _, s := range streams {
			if len(w.zset("elig:"+s+"@0")) == 0 {
				released++
			}
		}
	}
	if released != 200 {
		t.Fatalf("%d of 200 streams released in four ticks", released)
	}
}

// TestTickBudgetEntriesAndBytes: RT3 carries at most 2,500 entries and notes,
// 2 MiB of requests, 10,000 changes and 32 steps a tick (1.0, the tick budget
// and "Bytes"), whatever the plans hold: what is not dealt stays for a later
// tick.
func TestTickBudgetEntriesAndBytes(t *testing.T) {
	t.Parallel()
	sentBytes := func(t *testing.T, k *counting) (bytes, entries, steps int) {
		for _, it := range k.last() {
			n, ref := sprintfn.EncodedSize(testNames.Prefix, it.Step)
			if ref != nil {
				t.Fatal(ref)
			}
			bytes, entries, steps = bytes+n, entries+len(it.Step.Body.Entries)+len(it.Step.Body.Notes), steps+1
		}
		return
	}
	t.Run("entries", func(t *testing.T) {
		t.Parallel()
		w, l, k := busyWorld(t, []sprint.Rule{bigRule("resolve", 3000, 0)}, Budget{}, builderOpts{maxUnits: 100, unitEntry: true})
		rep := w.tick(l, k)
		_, entries, steps := sentBytes(t, k)
		if rep.EntriesNotes != 2500 || entries != 2500 || steps != 25 {
			t.Fatalf("dealt %d entries and notes in %d steps (report %d); want 2,500 in 25", entries, steps, rep.EntriesNotes)
		}
	})
	t.Run("bytes", func(t *testing.T) {
		t.Parallel()
		w, l, k := busyWorld(t, []sprint.Rule{bigRule("resolve", 1000, 4000)}, Budget{}, builderOpts{maxUnits: 100, unitEntry: true})
		rep := w.tick(l, k)
		bytes, _, steps := sentBytes(t, k)
		if bytes > 2<<20 || rep.RequestBytes != bytes || steps < 4 || steps > 5 {
			t.Fatalf("dealt %d bytes in %d steps (report %d); want at most 2 MiB", bytes, steps, rep.RequestBytes)
		}
	})
	t.Run("steps", func(t *testing.T) {
		t.Parallel()
		w, l, k := busyWorld(t, []sprint.Rule{bigRule("resolve", 40, 0)}, Budget{}, builderOpts{maxUnits: 1})
		w.tick(l, k)
		if _, _, steps := sentBytes(t, k); steps != 32 {
			t.Fatalf("dealt %d steps; want 32", steps)
		}
	})
}

// TestTickPageLimitFromBytes: the ingest's page limit is set each tick from
// the last page's bytes a line, to expect at most 2 MiB, and at least one
// line (1.0, "Bytes"; lines takes no byte limit).
func TestTickPageLimitFromBytes(t *testing.T) {
	t.Parallel()
	for _, pageBytes := range []int{2 << 20, 1000} {
		w := newWorld(t)
		w.rows("s1")
		long := make([]string, 1000)
		for i := range long {
			long[i] = fmt.Sprintf("%0200d", i)
		}
		w.verb(create("s1:waiting", waiting(), long...))
		b := DefaultBudget()
		b.PageBytes = pageBytes
		k := &counting{c: w.tw}
		l := w.loop("a", nil, b)
		w.tick(l, k)
		want := min(5000, pageBytes/assumedLineBytes) // before any page: the assumed bytes a line
		sawOne := false
		for i := 0; i < 3; i++ {
			before := seqOf(l.cur)
			w.clk.add(TickEvery)
			rep := w.tick(l, k)
			if rep.PageLimit != want || pageOf(k.sent[len(k.sent)-rep.RoundTrips]).Limit != want {
				t.Fatalf("page bytes %d, tick %d: the page's limit is %d; want %d", pageBytes, i, rep.PageLimit, want)
			}
			sawOne = sawOne || want == 1
			after := seqOf(l.cur)
			if after == before {
				break
			}
			total := 0
			lines := w.log.Stored(testNames.Prefix, "0") // the tick's own steps log lines too
			for _, line := range lines[before:after] {
				total += len(line.Item())
			}
			want = max(1, min(5000, pageBytes/(total/int(after-before))))
		}
		if pageBytes == 1000 && !sawOne {
			t.Fatal("a line over the page's bytes did not bring the limit to one line")
		}
	}
}

// TestTickDriftReadQuarantines: a read refused DRIFT naming a card under the
// head of fresh:s quarantines it in RT3 of the same tick, and the rule reads
// again next tick without it and deals the rest (1.3.5, 1.4.2).
func TestTickDriftReadQuarantines(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.rows("s1")
	// p2 names sixteen read cards where a primary keeps fifteen (1.3.1): the
	// read that follows them is refused DRIFT naming p2.
	bad := fresh()
	bad["rcards"] = strings.Join(ids("x", 16), ",")
	w.verb(create("s1:ready", fresh(), "p1"), create("s1:ready", bad, "p2"), create("s1:ready", fresh(), "p3"))
	k := &counting{c: w.tw}
	l := w.loop("a", []sprint.Rule{dealRule(64, sprint.FollowRCards)}, Budget{})
	w.tick(l, k)
	rep := w.tick(l, k)
	if rep.Refused["DRIFT"] != 1 || strings.Join(rep.Quarantined, ",") != "p2" || rep.RoundTrips != 3 {
		t.Fatalf("the refused read: %+v", rep)
	}
	rt3, wake := splitTickEnd(t, k.last())
	if wake != 1 || rep.Wake != 1 {
		t.Fatalf("the tick opened the invariant judgment on p2 and wrote the tick-end of %d (report %d), want 1", wake, rep.Wake)
	}
	if len(rt3) != 1 || rt3[0].Step.Sprint == nil || len(rt3[0].Step.Sprint.Quarantine) != 1 || rt3[0].Step.Sprint.Quarantine[0].Stream != "s1" {
		t.Fatalf("RT3 is not the quarantine of p2 in s1: %+v", rt3)
	}
	if _, ok := w.hash("quarantine@0")["p2"]; !ok {
		t.Fatalf("p2 is not quarantined: %v", w.hash("quarantine@0"))
	}
	if _, ok := w.zset("fresh:s1@0")["p2"]; ok {
		t.Fatal("p2 is still in fresh:s1")
	}
	if len(w.hash("jopen:p2@0")) == 0 {
		t.Fatal("no judgment is open on p2")
	}
	w.clk.add(TickEvery)
	rep = w.tick(l, k)
	if w.place("p1") != "s1:working" || w.place("p3") != "s1:working" || w.place("p2") != "s1:ready" {
		t.Fatalf("the next tick: p1 %s, p2 %s, p3 %s; %+v", w.place("p1"), w.place("p2"), w.place("p3"), rep)
	}
}

// bigCards are n fresh cards of s1 with a field of 64,000 bytes each: a read of
// their whole records past 8 MiB of reply is refused BUDGET (L1 7).
func bigCards(w *world, n int) {
	pad := strings.Repeat("y", 64000)
	for i := 0; i < n; i += 50 {
		var cards []string
		for j := i; j < min(i+50, n); j++ {
			cards = append(cards, fmt.Sprintf("b%d", j+1))
		}
		f := fresh()
		f["brief"] = pad
		w.verb(create("s1:ready", f, cards...))
	}
}

// TestTickBudgetReadHalvesThenParks: a read refused BUDGET is a bug, named in
// "the machine's step was refused", and the rule reads next tick at half its
// size; only a read of one key at limits of one parks the key (1.4.2, 1.3.5;
// SprintEvents.tla Apply's LIMIT branch; IT10's OnBug).
func TestTickBudgetReadHalvesThenParks(t *testing.T) {
	t.Parallel()
	whole := func(limit int, halve bool) sprint.Rule {
		return testRule("deal", func(keys []sprint.AgendaKey, b sprint.ReadBounds, h int) (sprint.ReadPlan, []sprint.AgendaKey) {
			if !halve {
				h = 0 // a rule that reads the same whatever it is told
			}
			return sprint.ReadPlan{Sprint: []sprint.SprintQ{{Kind: sprint.QueryRelated, Table: sprint.Work, Fields: []string{"kind", "attempt", "brief"},
				Source: sprint.IDSource{Kind: sprint.SourceHead, Key: "fresh:s1", Limit: sprint.Halved(limit, h)}}}}, nil
		}, moveAll("ready", "working", map[string]string{"attempt": "1"}))
	}
	t.Run("halves", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.rows("s1")
		bigCards(w, 150)
		k := &counting{c: w.tw}
		l := w.loop("a", []sprint.Rule{whole(150, true)}, Budget{})
		w.tick(l, k)
		rep := w.tick(l, k)
		if rep.Refused["BUDGET"] != 1 || rep.Halved["deal"] != 1 || len(rep.Parked) != 0 {
			t.Fatalf("the refused read: %+v", rep)
		}
		if rt3, wake := splitTickEnd(t, k.last()); wake != 1 || len(rt3) != 1 || len(rt3[0].Step.Body.Notes) != 1 || rt3[0].Step.Body.Notes[0].Type != TypeStepRefused {
			t.Fatalf("the refusal is not named in RT3: %+v", rt3)
		}
		if len(w.hash("jopen:deal@0")) == 0 {
			t.Fatal(`"the machine's step was refused" is not open on deal`)
		}
		w.clk.add(TickEvery)
		rep = w.tick(l, k)
		if rep.Refused["BUDGET"] != 0 || rep.Applied == 0 || w.place("b1") != "s1:working" || w.place("b150") != "s1:ready" {
			t.Fatalf("the read at half: %+v; b1 %s, b150 %s", rep, w.place("b1"), w.place("b150"))
		}
		if _, ok := rep.Halved["deal"]; ok {
			t.Fatal("the halving outlived a plan that applied whole (1.3.6)")
		}
	})
	t.Run("parks at one", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		w.rows("s1")
		bigCards(w, 150)
		k := &counting{c: w.tw}
		l := w.loop("a", []sprint.Rule{whole(150, false)}, Budget{})
		w.tick(l, k)
		// The key has been halved to limits of one already (eleven halvings
		// take 2,000 to one): one more BUDGET parks it.
		l.halvings["deal"] = 11
		rep := w.tick(l, k)
		if rep.Refused["BUDGET"] != 1 || strings.Join(rep.Parked, ",") != "deal" {
			t.Fatalf("the read at one: %+v", rep)
		}
		w.clk.add(TickEvery)
		rep = w.tick(l, k)
		if _, ok := w.hash("parked@0")["deal"]; !ok {
			t.Fatalf("deal is not parked: %v", w.hash("parked@0"))
		}
		if _, ok := w.zset("agenda@0")["deal"]; ok {
			t.Fatal("a parked key is still in the agenda")
		}
		// A new line that would queue it does not: it waits for its judgment.
		w.verb(create("s1:ready", fresh(), "p1"))
		w.clk.add(TickEvery)
		if rep = w.tick(l, k); rep.Read["deal"] != 0 {
			t.Fatalf("a parked key was planned: %+v", rep)
		}
	})
}

// TestTickHoldsBackDroppingKeys: a key whose only work was in dropping streams
// is held back: it stays in the agenda and is not planned again until a new
// line queues it or a dropping mark changes (1.3.5).
func TestTickHoldsBackDroppingKeys(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.rows("s1", "s2")
	hold := true
	deal := testRule("deal", headRead(sprint.IndexFresh, 64, []string{"kind", "attempt"}, nil),
		func(s *sprint.Snapshot, keys []sprint.AgendaKey, now sprint.Now) sprint.RulePlan {
			if hold {
				return sprint.RulePlan{HeldBack: keys}
			}
			return moveAll("ready", "working", map[string]string{"attempt": "1"})(s, keys, now)
		})
	k := &counting{c: w.tw}
	l := w.loop("a", []sprint.Rule{deal}, Budget{})
	w.tick(l, k)
	w.verb(create("s1:ready", fresh(), "p1"))
	if rep := w.tick(l, k); rep.Read["deal"] != 1 || strings.Join(rep.HeldBack, ",") != "deal" {
		t.Fatalf("the plan that held deal back: %+v", rep)
	}
	if _, ok := w.zset("agenda@0")["deal"]; !ok {
		t.Fatal("a held back key left the agenda")
	}
	w.clk.add(TickEvery)
	if rep := w.tick(l, k); rep.Read["deal"] != 0 || rep.RoundTrips != 1 {
		t.Fatalf("a held back key was planned: %+v", rep)
	}
	// A new line that queues deal plans it again.
	w.verb(create("s1:ready", fresh(), "p2"))
	w.clk.add(TickEvery)
	if rep := w.tick(l, k); rep.Read["deal"] != 1 || strings.Join(rep.HeldBack, ",") != "deal" {
		t.Fatalf("a new line of deal did not plan it: %+v", rep)
	}
	// A dropping mark that changes plans it again.
	w.step(&sprintfn.Request{Epoch: "0", Meta: sprintfn.Meta{Verb: "drop", Actor: "coordinator"},
		Sprint: &sprintfn.SprintPart{Dropping: map[string]string{"s2": "op-drop-1"}}})
	hold = false
	w.clk.add(TickEvery)
	if rep := w.tick(l, k); rep.Read["deal"] != 1 || len(rep.HeldBack) != 0 || w.place("p2") != "s1:working" {
		t.Fatalf("a changed mark did not plan deal: %+v", rep)
	}
}

// TestTickCurFromAtomicRead: a new lease holder whose cursor is stale pages
// from it, and in the same tick drops the lines at or before the real cursor,
// which RT1's read gives, and ingests the rest from there (1.1).
func TestTickCurFromAtomicRead(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.rows("s1")
	k := &counting{c: w.tw}
	a, b := w.loop("a", nil, Budget{}), w.loop("b", nil, Budget{})
	w.tick(b, k)
	w.verb(create("s1:waiting", waiting(), "q1"))
	w.tick(b, k) // b ingests the first lines
	stale := b.cur
	w.verb(create("s1:waiting", waiting(), "q2"))
	w.clk.add(LeaseHold + TickEvery) // b's lease runs out
	if rep := w.tick(a, k); !rep.Held {
		t.Fatalf("a did not take the lease: %+v", rep)
	}
	w.tick(a, k) // a ingests past b's cursor
	real := a.cur
	w.verb(create("s1:waiting", waiting(), "q3"), create("s1:waiting", waiting(), "q4"))
	w.clk.add(LeaseHold + TickEvery)
	if b.cur != stale || seqOf(stale) >= seqOf(real) {
		t.Fatalf("b's cursor %s is not stale against %s", b.cur, real)
	}
	// the lines before b's tick: its own steps (the real rules' deal, resolve
	// and held) write lines after its page
	last := len(w.log.Lines(testNames.Prefix, "0"))
	rep := w.tick(b, k)
	if !rep.Held || rep.Refused[sprintfn.CodeIngestAt] != 0 {
		t.Fatalf("b's tick: %+v", rep)
	}
	page := pageOf(k.sent[len(k.sent)-rep.RoundTrips])
	ingest := k.sent[len(k.sent)-rep.RoundTrips+1][0].Step.Ingest
	if page.AfterSeq != stale || ingest.From != real || rep.Lines != last-int(seqOf(real)) || b.cur != decimal(uint64(last)) {
		t.Fatalf("paged after %s, ingested from %s, %d lines to %s; want from %s, %d lines to %d", page.AfterSeq, ingest.From, rep.Lines, b.cur, real, last-int(seqOf(real)), last)
	}
}

// TestTwoLoopsOneTicks: two loops over one store: one holds the lease and
// ticks; the other's step writes only its idle fields, and it does nothing
// more (1.4.2, E4; SprintEvents.tla LeaseFree, GenOK).
func TestTwoLoopsOneTicks(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.rows("s1")
	k := &counting{c: w.tw}
	a, b := w.loop("a", []sprint.Rule{dealRule(64)}, Budget{}), w.loop("b", []sprint.Rule{dealRule(64)}, Budget{})
	for i := 0; i < 5; i++ {
		w.verb(create("s1:ready", fresh(), fmt.Sprintf("p%d", i)))
		ra, rb := w.tick(a, k), w.tick(b, k)
		if !ra.Held || rb.Held || rb.RoundTrips != 1 {
			t.Fatalf("round %d: a held %v, b held %v in %d round trips", i, ra.Held, rb.Held, rb.RoundTrips)
		}
		if hb := w.hash("heartbeat"); hb["idle_loop"] != "b" || hb["owner"] != "token-a" {
			t.Fatalf("round %d: the heartbeat %v", i, hb)
		}
		w.clk.add(TickEvery)
	}
	for i := 0; i < 4; i++ {
		if p := w.place(fmt.Sprintf("p%d", i)); p != "s1:working" {
			t.Fatalf("p%d is at %s", i, p)
		}
	}
}

// TestTickKeepsFailing: a tick that fails is counted, and the next tick's RT1
// writes the count and the error in the heartbeat, where "the tick keeps
// failing" is computed at three (1.4.1).
func TestTickKeepsFailing(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.rows("s1")
	k := &counting{c: w.tw}
	l := w.loop("a", []sprint.Rule{dealRule(64)}, Budget{})
	w.tick(l, k)
	k.fail = func(call int, items []sprintfn.Item) error {
		if items[0].Step != nil && items[0].Step.Ingest != nil {
			return errors.New("the store went away")
		}
		return nil
	}
	for i := 1; i <= 4; i++ {
		w.verb(create("s1:ready", fresh(), fmt.Sprintf("p%d", i)))
		w.clk.add(TickEvery)
		if _, err := Tick(context.Background(), k, l); err == nil {
			t.Fatalf("tick %d did not fail", i)
		}
	}
	res, _, err := w.tw.KeyQuery(sprintfn.KeyQ{Kind: sprintfn.KeyHeartbeat})
	if err != nil {
		t.Fatal(err)
	}
	hb := ParseHeartbeat(res.(sprintfn.HeartbeatResult).Fields)
	if hb.Failures != 3 || !strings.Contains(hb.Error, "the store went away") {
		t.Fatalf("the heartbeat says %d failures, %q", hb.Failures, hb.Error)
	}
	groups := InboxGroups(hb, Clock{}, hb.TickAt)
	if len(groups) != 1 || groups[0].ID != GroupFailing || !strings.Contains(groups[0].What, "the store went away") {
		t.Fatalf("groups %+v", groups)
	}
	// A tick that succeeds starts the count again.
	k.fail = nil
	w.clk.add(TickEvery)
	if rep := w.tick(l, k); rep.Err != nil || l.failures != 0 {
		t.Fatalf("the tick after: %+v, failures %d", rep, l.failures)
	}
}

// TestTickShortReadRefusesThePlanByName: a rule whose read comes back short
// (its plan reads what its read plan did not load) never panics the process:
// the tick refuses the plan by name and fails naming what was not loaded, the
// rule's key is parked, and the next tick's error step opens "the machine's
// step was refused" naming it (1.3.5; sprint.UnloadedErr). The case is R6 on
// its registered read, which names no stream's front: a stream on the table
// and no member up is a card R6 must look behind, "front of s1". The tick's
// own table reads R6 with the streams RT1 found (TickShape), and is not short
// (TestTickCurFromAtomicRead ticks the same world whole).
func TestTickShortReadRefusesThePlanByName(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.rows("s1")
	var rules []sprint.Rule
	for _, r := range sprint.RuleTable() {
		if r.Name == "deal" {
			r.ReadFor = nil // the registered read: it names no front
		}
		rules = append(rules, r)
	}
	k := &counting{c: w.tw}
	l := w.loop("a", rules, Budget{})
	w.tick(l, k)
	w.verb(create("s1:waiting", waiting(), "q1"))
	var rep Report
	var err error
	for i := 0; i < 3 && err == nil; i++ {
		rep, err = Tick(context.Background(), k, l)
	}
	if err == nil || !strings.Contains(err.Error(), "rule deal") || !strings.Contains(err.Error(), "front of s1") {
		t.Fatalf("the short read's tick: %v, want it failed naming rule deal and front of s1", err)
	}
	if len(rep.Short) != 1 || rep.Refused[CodeShortRead] != 1 || !slices.Contains(rep.Parked, "deal") {
		t.Fatalf("the report: short %v, refused %v, parked %v", rep.Short, rep.Refused, rep.Parked)
	}
	w.tick(l, k) // the error step parks the key and opens the judgment
	var named bool
	for _, line := range w.log.Lines(testNames.Prefix, "0") {
		s := string(line)
		named = named || strings.Contains(s, TypeStepRefused) && strings.Contains(s, "front of s1") && strings.Contains(s, CodeShortRead)
	}
	if !named {
		t.Fatal("no judgment names the short read")
	}
}
