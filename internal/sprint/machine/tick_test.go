package machine

import (
	"context"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
)

// dealRule is the tests' deal (R6's key): the head of fresh:s1, each card to
// working at attempt 1.
func dealRule(limit int, follow ...string) sprint.Rule {
	return testRule("deal", headRead(sprint.IndexFresh, limit, []string{"kind", "attempt"}, follow),
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
	_ = context.Background
	_ = sprintfn.PopMax
}
