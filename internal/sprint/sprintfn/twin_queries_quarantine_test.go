package sprintfn

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The second cold read of IT30 (PR 4777): `streams` and the listings leave out
// quarantined ids, `streams` finds a cross need where the writer puts it, a
// mark over its reserve is DRIFT, and leaving ids out costs a step for each id.
// Every test holds the Go twin and the Lua kinds (under gopher-lua, on stubs of
// Layer 1's helpers) to one answer.

// crossWorld is the standard world with two more streams stopped on a cross
// need, s3 on the card s2 waits for and s4 on another, so that a stream is
// stopped on each of the need cards and two share one.
func crossWorld(t *testing.T, ctl map[string]string) *qworld {
	t.Helper()
	w := standardWith(t, ctl)
	w.rows(sprint.Work, "s3", "s4")
	w.rows(sprint.Merge, "s3", "s4")
	w.cards(
		card{sprint.Merge, "s3", "ctl", "ctl-s3", "1", fields("state", "stopped", "cause", "cross", "other", "p2")},
		card{sprint.Merge, "s4", "ctl", "ctl-s4", "1", fields("state", "stopped", "cause", "cross", "other", "p3")},
		card{sprint.Merge, "s3", "stuck", "st7", "1", nil},
		card{sprint.Merge, "s4", "stuck", "st8", "1", nil},
	)
	return w
}

// needs is each stream's need card and whether it has a stuck list, by stream.
func needs(t *testing.T, r StreamsResult) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, it := range r.Items {
		switch {
		case it.Need != nil:
			out[it.Stream] = it.Need.ID
		case it.Stuck != nil:
			out[it.Stream] = "(left out)"
		}
	}
	return out
}

// TestStreamsFindsTheCrossNeedWhereTheWriterPutsIt: the card a stream stopped
// on a cross need waits for is on its control card as `other` (steps_merge.go
// writes ctlSet["other"]), and `need_card`, which the writer puts on the stuck
// merge card, is read when `other` is empty, as IT11's held rule reads the two
// (rules_held.go). When both are set `other` is the writer's and wins. A stream
// that is not stopped, or not on a cross, or names no card, has no need. The
// Lua kind finds the same.
func TestStreamsFindsTheCrossNeedWhereTheWriterPutsIt(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		ctl  map[string]string
		want map[string]string
	}{
		{"other, as the writer records it", fields("state", "stopped", "cause", "cross", "other", "p2"), map[string]string{"s2": "p2", "s3": "p2", "s4": "p3"}},
		{"need_card alone", fields("state", "stopped", "cause", "cross", "need_card", "p2"), map[string]string{"s2": "p2", "s3": "p2", "s4": "p3"}},
		{"both, other wins", fields("state", "stopped", "cause", "cross", "other", "p2", "need_card", "p1"), map[string]string{"s2": "p2", "s3": "p2", "s4": "p3"}},
		{"neither", fields("state", "stopped", "cause", "cross"), map[string]string{"s3": "p2", "s4": "p3"}},
		{"a stop that is not a cross", fields("state", "stopped", "cause", "conflict", "other", "p2"), map[string]string{"s3": "p2", "s4": "p3"}},
		{"a stream that is not stopped", fields("state", "merging", "cause", "cross", "other", "p2"), map[string]string{"s3": "p2", "s4": "p3"}},
	} {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			w := crossWorld(t, c.ctl)
			q := sprint.SprintQ{Kind: sprint.QueryStreams, Fields: []string{}, Limit: 2}
			res, _ := w.query(q)
			r := res.(StreamsResult)
			if got := needs(t, r); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("needs %v, want %v", got, c.want)
			}
			// A stream stopped on p2 has p2's record, where the record exists.
			for _, it := range r.Items {
				if it.Need != nil && (!it.Need.Exists || it.Need.Place.Col != "waiting") {
					t.Fatalf("%s's need: %+v", it.Stream, it.Need)
				}
			}
			h := newLuaHarness(t, w)
			h.agree("streams "+c.name, mustEncode(t, q))
			if h.answered[sprint.QueryStreams] != 1 {
				t.Fatalf("the Lua did not answer: %v %v", h.answered, h.refused)
			}
		})
	}
}

// TestStreamsLeavesOutQuarantinedCards: `streams` leaves out the ids in
// {p}quarantine@e as every sprint query does (1.0, 1.3.5), through the same
// leave as `related`: a quarantined need card is no record (and is named), and
// neither is a quarantined control card, whose stream is listed with none and
// is not read as stopped. Two streams on one need card are left without it
// together. The Lua kind answers alike, charge for charge.
func TestStreamsLeavesOutQuarantinedCards(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name       string
		quarantine []string
		control    map[string]bool // the streams whose control card is read
		need       map[string]bool // the streams whose need card is read
		left       []string
	}{
		{"nothing quarantined", nil,
			map[string]bool{"s1": true, "s2": true, "s3": true, "s4": true},
			map[string]bool{"s2": true, "s3": true, "s4": true}, []string{}},
		{"a need card", []string{"p2"},
			map[string]bool{"s1": true, "s2": true, "s3": true, "s4": true},
			map[string]bool{"s4": true}, []string{"p2"}},
		{"a control card of a stopped stream", []string{"ctl-s3"},
			map[string]bool{"s1": true, "s2": true, "s4": true},
			map[string]bool{"s2": true, "s4": true}, []string{"ctl-s3"}},
		{"a control card and its need card", []string{"ctl-s4", "p3"},
			map[string]bool{"s1": true, "s2": true, "s3": true},
			map[string]bool{"s2": true, "s3": true}, []string{"ctl-s4"}},
		{"everything", []string{"ctl-s1", "ctl-s2", "ctl-s3", "ctl-s4", "p2", "p3"},
			map[string]bool{}, map[string]bool{}, []string{"ctl-s1", "ctl-s2", "ctl-s3", "ctl-s4"}},
	} {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			w := crossWorld(t, fields("state", "stopped", "cause", "cross", "other", "p2"))
			if len(c.quarantine) != 0 {
				var kv []string
				for _, id := range c.quarantine {
					kv = append(kv, id, "DRIFT")
				}
				w.seed(w.hset("quarantine", kv...))
			}
			q := sprint.SprintQ{Kind: sprint.QueryStreams, Fields: []string{"state"}, Limit: 3}
			res, charge := w.query(q)
			r := res.(StreamsResult)
			if !reflect.DeepEqual(r.Rows, []string{"s1", "s2", "s3", "s4"}) {
				t.Fatalf("a quarantined card does not take a stream off the list: %v", r.Rows)
			}
			for _, it := range r.Items {
				if (it.Control != nil) != c.control[it.Stream] {
					t.Errorf("%s: control card read is %v, want %v", it.Stream, it.Control != nil, c.control[it.Stream])
				}
				if (it.Need != nil) != c.need[it.Stream] {
					t.Errorf("%s: need card read is %v, want %v", it.Stream, it.Need != nil, c.need[it.Stream])
				}
			}
			// What is left out is named, once each, in the order it was asked: the control
			// cards in the order of the rows, then the need cards in the order of the streams.
			if !reflect.DeepEqual(r.LeftOut, c.left) {
				t.Fatalf("left out %v, want %v", r.LeftOut, c.left)
			}
			// Nothing quarantined is read: one record for each card that is not out.
			wantRecords := len(c.control) + len(c.need)
			if charge.Records != wantRecords {
				t.Errorf("read %d records, want %d", charge.Records, wantRecords)
			}
			if p := QueryProbes(q); charge.Probes > p {
				t.Errorf("made %d probes, declared %d", charge.Probes, p)
			}
			h := newLuaHarness(t, w)
			h.agree("streams "+c.name, mustEncode(t, q))
			if h.answered[sprint.QueryStreams] != 1 {
				t.Fatalf("the Lua did not answer: %v %v", h.answered, h.refused)
			}
		})
	}
}

// TestListingsLeaveOutQuarantinedControlCards: `fleet` and `readers` leave a
// quarantined control card out and name it, as `streams` and `related` do
// (1.0: every sprint query leaves the id out, front(s) alone keeps a
// quarantined sentinel). The member is still listed with its counts. The Lua
// kinds answer alike.
func TestListingsLeaveOutQuarantinedControlCards(t *testing.T) {
	t.Parallel()
	w := standard(t)
	w.seed(w.hset("quarantine", "ctl-m1", "DRIFT", "ctl-r2", "DRIFT"))
	h := newLuaHarness(t, w)
	for _, q := range []sprint.SprintQ{
		{Kind: sprint.QueryFleet, Fields: []string{"status"}},
		{Kind: sprint.QueryFleet, Fields: []string{}, Units: 1},
		{Kind: sprint.QueryReaders, Fields: []string{}},
	} {
		res, c := w.query(q)
		l := res.(ListingResult)
		for _, it := range l.Items {
			if it.Row == "m1" && it.Control != nil {
				t.Errorf("%s: the quarantined control card of %s is returned", q.Kind, it.Row)
			}
			if len(it.Counts) == 0 {
				t.Errorf("%s: %s is listed without its counts", q.Kind, it.Row)
			}
		}
		if q.Kind == sprint.QueryFleet && q.Units == 0 {
			if l.Items[0].Control != nil || l.Items[1].Control == nil || !reflect.DeepEqual(l.LeftOut, []string{"ctl-m1"}) {
				t.Errorf("fleet: %+v", l)
			}
			if c.Records != 1 {
				t.Errorf("fleet read %d control cards, want the one not quarantined", c.Records)
			}
		}
		h.agree(fmt.Sprintf("%s units %d", q.Kind, q.Units), mustEncode(t, q))
	}
}

// TestAMarkOverItsReserveIsDrift: Layer 1's checked probe refuses DRIFT
// (read_reservation) a reply larger than the reserve the probe asked for, and
// the quarantine's probe asks for 1 KiB a mark and 64 bytes over (1.3.1, the
// cold read's finding 7). The twin answers as the store does, and so does the
// Lua on a stub that checks the reply against the reserve: a mark over its share
// makes every query that names its card DRIFT, and a long mark among ids that
// share the reserve passes while the sum is under it.
func TestAMarkOverItsReserveIsDrift(t *testing.T) {
	t.Parallel()
	long := func(n int) string { return strings.Repeat("x", n) }
	for _, c := range []struct {
		name  string
		marks map[string]string
		ids   []string
		drift bool
	}{
		{"a mark at its reserve", map[string]string{"p2": long(1024 + 64)}, []string{"p2"}, false},
		{"a mark one byte over its reserve", map[string]string{"p2": long(1024 + 65)}, []string{"p2"}, true},
		{"a long mark, two ids share the reserve", map[string]string{"p2": long(2000)}, []string{"p1", "p2"}, false},
		{"marks that together pass their reserve", map[string]string{"p1": long(1500), "p2": long(1500)}, []string{"p1", "p2"}, true},
		{"a long mark of a card not asked", map[string]string{"p3": long(5000)}, []string{"p1", "p2"}, false},
	} {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			w := standard(t)
			var kv []string
			for id, m := range c.marks {
				kv = append(kv, id, m)
			}
			w.seed(w.hset("quarantine", kv...))
			q := sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Work, Source: ids(c.ids...), Fields: []string{}}
			_, _, err := w.tw.QueryFull(q)
			ref, refused := err.(*Refusal)
			if refused != c.drift || (refused && (ref.Code != codeDrift || ref.Detail.Budget != "read_reservation")) {
				t.Fatalf("the twin: %v, want drift %v", err, c.drift)
			}
			h := newLuaHarness(t, w)
			h.agree("related "+c.name, mustEncode(t, q))
		})
	}

	// The fields of a hash key are read under the same rule: one HMGET of the
	// clock's five fields reserves 5 KiB and 64 bytes, and jnote's own field 1 KiB.
	w := standard(t)
	w.seed(w.hsetBare("clock", "stopped_ms", "1000", "stophold_ms", strings.Repeat("9", 6000)))
	h := newLuaHarness(t, w)
	h.agree("clock with a field over the reserve", mustEncodeKey(t, KeyQ{Kind: KeyClock}))
	if h.refused[KeyClock] != 1 {
		t.Fatalf("a clock field over its reserve was answered: %v", h.answered)
	}
	w.note("blocked", "c1", "p1")
	note := lastNote(w)
	w.seed(w.hset("jopen:p1", "blocked|c1", strings.Repeat("h", 1100)))
	q := sprint.SprintQ{Kind: sprint.QueryJnote, Source: ids(note), Fields: []string{}, Subjects: 4}
	if ref := w.refused(q); ref.Code != codeDrift || ref.Detail.Budget != "read_reservation" {
		t.Fatalf("jnote's own field over its reserve: %v", ref)
	}
	h.agree("jnote with a hold over the reserve", mustEncode(t, q))
}

// TestLeavingIdsOutCostsAStepAnId: leaving out n ids is n steps of the twin's
// own logic and never n times n (a query can leave out every one of the 10,000
// ids it names). QueryCharge.Work counts the steps, and the test holds them to
// a multiple of the input for a source of 10,000 ids all quarantined, and for a
// chain that leaves one out at each step; the store's cost, one HMGET for each
// 2,000 ids, is held with them. The Lua kind answers alike on a smaller list.
func TestLeavingIdsOutCostsAStepAnId(t *testing.T) {
	t.Parallel()
	const n = 10000
	all := make([]string, n)
	w := newQWorld(t)
	w.rows(sprint.Work, "s1")
	var marks []Cmd // a command carries at most maxPieces pairs
	for i := range all {
		all[i] = "q" + strconv.Itoa(i)
		if i%500 == 0 {
			marks = append(marks, w.hset("quarantine", all[i], "DRIFT"))
			continue
		}
		last := &marks[len(marks)-1]
		last.Argv = append(last.Argv, all[i], "DRIFT")
	}
	w.seed(marks...)
	q := sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Work, Source: ids(all...), Fields: []string{}}
	res, c := w.query(q)
	r := asRelated(t, res)
	if len(r.IDs) != 0 || len(r.LeftOut) != n || len(r.Items) != 0 {
		t.Fatalf("ids %d, left out %d, items %d", len(r.IDs), len(r.LeftOut), len(r.Items))
	}
	if c.Probes != ceilDiv(n, probeChunk) || c.Records != 0 {
		t.Fatalf("charged %+v", c)
	}
	if c.Work > 3*n {
		t.Fatalf("leaving out %d ids took %d steps, at most %d are a step or two an id", n, c.Work, 3*n)
	}

	// A list that repeats the ids it names is refused as malformed; the same ids
	// named again by a follow (several cards need one quarantined card) are left
	// out once each, and still in steps an id.
	var cards []card
	for i := 0; i < 60; i++ {
		cards = append(cards, card{sprint.Work, "s1", "waiting", "w" + strconv.Itoa(i), strconv.Itoa(i + 1),
			fields("kind", "work", "open", "1", "needs", "q1,q2,q3,q4,q5,q6,q7,q8")})
	}
	w.cards(cards...)
	var names []string
	for i := 0; i < 60; i++ {
		names = append(names, "w"+strconv.Itoa(i))
	}
	q = sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Work, Source: ids(names...), Fields: []string{}, Follow: []string{sprint.FollowNeeds}}
	res, c = w.query(q)
	r = asRelated(t, res)
	if len(r.LeftOut) != 8 || len(r.IDs) != 60 || c.Work > 3*(60+60*8) {
		t.Fatalf("left out %d, ids %d, work %d", len(r.LeftOut), len(r.IDs), c.Work)
	}

	// The Lua kind leaves out the same ids, a smaller list, on the same stubs.
	h := newLuaHarness(t, w)
	q = sprint.SprintQ{Kind: sprint.QueryRelated, Table: sprint.Work, Source: ids(all[:2500]...), Fields: []string{}}
	h.agree("a list all quarantined", mustEncode(t, q))
	if h.answered[sprint.QueryRelated] != 1 {
		t.Fatalf("%v %v", h.answered, h.refused)
	}
}
