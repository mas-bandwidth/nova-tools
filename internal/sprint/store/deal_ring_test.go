package store

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The deal's and the ask's rolling indexes through the machine's tick (errata
// 3, amendment 5; tla/SprintEvents.tla, dcur and acur): the owner's run on a
// real store, eight members beaten up before start and three streams of ten
// ready, dealt m1 m1 m2 m1 m2 m1 m2 m3 ...; the index must not restart at the
// first member each tick, it goes on where the last deal left off. The same
// drivers run on the Mem backend here and on a real Redis in the functional
// tier (deal_ring_functional_test.go).

var ringMembers = []string{"m1", "m2", "m3", "m4", "m5", "m6", "m7", "m8"}

// ringTick is what one tick of the machine did to the rolling indexes.
type ringTick struct {
	deals    []string   // the members dealt to, in the deal's order
	asks     [][]string // the readers asked, per primary, in the ask's order
	dealAt   string     // the fleet table's deal_index after the tick
	askAt    string     // the readers table's ask_index after the tick
	eligible []string   // the members up with room as the tick's deal saw them
}

func (r ringTick) String() string {
	asks := make([]string, len(r.asks))
	for i, a := range r.asks {
		asks[i] = strings.Join(a, "+")
	}
	return fmt.Sprintf("deals=%v deal_index=%s eligible=%v asks=%v ask_index=%s", r.deals, r.dealAt, r.eligible, asks, r.askAt)
}

// ringFleet adds the members as the owner's fleet has them (rows, down until
// they beat), then has every one of them beat.
func ringFleet(h *harness) {
	h.t.Helper()
	for _, m := range ringMembers {
		h.must(FleetStep(sprint.FleetReq{Op: "release", Member: m, Who: "tester"}))
	}
	h.mu.Lock()
	h.live = append([]string(nil), ringMembers...)
	h.mu.Unlock()
	h.beat()
}

// ringMachineTick is one tick of the machine and what it did to the indexes.
func ringMachineTick(h *harness) ringTick {
	h.t.Helper()
	var out ringTick
	res := h.machine()
	for _, p := range res.Parts {
		for _, line := range p.Moved {
			switch p.Name {
			case "deal":
				if _, m, ok := strings.Cut(line, " member="); ok {
					m, _, _ = strings.Cut(m, " ")
					out.deals = append(out.deals, m)
				}
			case "ask":
				if _, rs, ok := strings.Cut(line, " asked of "); ok {
					out.asks = append(out.asks, strings.Split(rs, ", "))
				}
			}
		}
	}
	s := h.snap()
	out.dealAt, _ = s.Fleet.Prop(sprint.PropDealIndex)
	out.askAt, _ = s.Readers.Prop(sprint.PropAskIndex)
	for _, m := range s.UpMembers() {
		if s.Fleet.Count(m, sprint.Ready) < sprint.MaxReadyPerMember || slices.Contains(out.deals, m) {
			out.eligible = append(out.eligible, m)
		}
	}
	return out
}

// takeOne plays one member taking and finishing one of its ready cards, the
// simulation's take: the members in turn from next, the first that has one.
func takeOne(h *harness, next int) int {
	h.t.Helper()
	s := h.snap()
	for i := range ringMembers {
		m := ringMembers[(next+i)%len(ringMembers)]
		ready := s.Fleet.Cell(m, sprint.Ready)
		if len(ready) == 0 {
			continue
		}
		c := ready[0]
		gens := map[string]int{c.ID: c.Int("gen")}
		h.must(TakeStep(sprint.TakeReq{As: m, Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: gens, Who: m}))
		h.must(FinishStep(sprint.FinishReq{As: m, Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: gens, Who: m}))
		return (next + i + 1) % len(ringMembers)
	}
	return next
}

// dealRingOwnersRun is the owner's run: eight members beaten up before start,
// three streams of ten ready, the machine ticking every second and a member
// finishing a card each tick. The first sixteen deals go m1..m8, m1..m8.
func dealRingOwnersRun(t *testing.T, h *harness) {
	ringFleet(h)
	for _, st := range []string{"s1", "s2", "s3"} {
		h.must(AddStep(sprint.AddReq{Stream: st, Count: 10}))
	}
	h.startMachine()
	var deals []string
	next := 0
	for i := 0; i < 12 && len(deals) < 16; i++ {
		r := ringMachineTick(h)
		t.Logf("tick %d: %s", i+1, r)
		deals = append(deals, r.deals...)
		next = takeOne(h, next)
		h.readAll()
		h.tick(time.Second)
	}
	want := append(append([]string(nil), ringMembers...), ringMembers...)
	if len(deals) < 16 || !slices.Equal(deals[:16], want) {
		t.Fatalf("the first 16 deals %v, want %v: eight members up are dealt round the fleet", deals, want)
	}
}

// dealRingAcrossTicks deals one card a tick, and asks one primary a tick: the
// index goes on from tick to tick, and across a stop and a start of the
// machine and a new loop on the same store, never back to the first name.
func dealRingAcrossTicks(t *testing.T, h *harness) {
	ringFleet(h)
	h.startMachine()
	// every member up before the first card, the presence changes applied
	for i := 0; i < len(ringMembers)+2; i++ {
		h.machine()
		h.tick(time.Second)
	}
	var deals []string
	var asks [][]string
	for i := 0; i < 2*len(ringMembers); i++ {
		if i == len(ringMembers)+3 {
			// a stop, a start and a new loop: a store of its own on the backend
			h.stopMachine()
			h.startMachine()
			o := h.st
			h.st = &Store{B: o.B, Names: o.Names, Actor: o.Actor, Now: o.Now, NewID: o.NewID, Sleep: o.Sleep}
		}
		h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{fmt.Sprintf("c%02d", i)}}))
		r := ringMachineTick(h)
		t.Logf("tick %d: %s", i+1, r)
		deals = append(deals, r.deals...)
		asks = append(asks, r.asks...)
		for _, m := range r.deals {
			h.work(m)
		}
		h.readAll()
		h.tick(time.Second)
	}
	want := append(append([]string(nil), ringMembers...), ringMembers...)
	if !slices.Equal(deals, want) {
		t.Fatalf("deals across ticks %v, want %v: each tick's deal starts past the member the last one dealt to", deals, want)
	}
	readers := []string{"reader-a", "reader-b", "reader-c"}
	for i, a := range asks {
		w := []string{readers[(2*i)%3], readers[(2*i+1)%3]}
		if !slices.Equal(a, w) {
			t.Fatalf("ask %d of %v asked %v, want %v: each tick's ask starts past the reader the last one asked", i+1, asks, a, w)
		}
	}
	if len(asks) < 2*len(ringMembers)-1 {
		t.Fatalf("asks %v: want one a tick", asks)
	}
}

func TestTheDealGoesRoundTheFleetThroughTheTick(t *testing.T) {
	t.Parallel()
	dealRingOwnersRun(t, newHarness(t))
}

func TestTheIndexesGoOnFromTickToTick(t *testing.T) {
	t.Parallel()
	dealRingAcrossTicks(t, newHarness(t))
}
