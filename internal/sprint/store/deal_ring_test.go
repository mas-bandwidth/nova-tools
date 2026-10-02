package store

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/require"
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
		if heldBy(s, m) < s.Width(m) || slices.Contains(out.deals, m) {
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
	require.GreaterOrEqual(t, len(deals), 16, "the first 16 deals %v, want %v: eight members up are dealt round the fleet", deals, want)
	require.True(t, slices.Equal(deals[:16], want), "the first 16 deals %v, want %v: eight members up are dealt round the fleet", deals, want)
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
	require.True(t, slices.Equal(deals, want), "deals across ticks %v, want %v: each tick's deal starts past the member the last one dealt to", deals, want)
	readers := []string{"reader-a", "reader-b", "reader-c"}
	for i, a := range asks {
		w := []string{readers[(2*i)%3], readers[(2*i+1)%3]}
		require.True(t, slices.Equal(a, w), "ask %d of %v asked %v, want %v: each tick's ask starts past the reader the last one asked", i+1, asks, a, w)
	}
	require.GreaterOrEqual(t, len(asks), 2*len(ringMembers)-1, "asks %v: want one a tick", asks)
}

func TestTheDealGoesRoundTheFleetThroughTheTick(t *testing.T) {
	t.Parallel()
	dealRingOwnersRun(t, newHarness(t))
}

func TestTheIndexesGoOnFromTickToTick(t *testing.T) {
	t.Parallel()
	dealRingAcrossTicks(t, newHarness(t))
}

// workFailing plays a member taking and finishing every ready card it holds,
// the ones fail names failed.
func workFailing(h *harness, member string, fail func(id string) bool) {
	h.t.Helper()
	h.run(TakeStep(sprint.TakeReq{As: member, Sel: sprint.Sel{Limit: 100}, Who: member}))
	var ok, failed []string
	gens := map[string]int{}
	for _, c := range h.snap().Fleet.Cell(member, sprint.Working) {
		gens[c.ID] = c.Int("gen")
		if fail(c.ID) {
			failed = append(failed, c.ID)
		} else {
			ok = append(ok, c.ID)
		}
	}
	if len(ok) > 0 {
		h.must(FinishStep(sprint.FinishReq{As: member, Sel: sprint.Sel{IDs: ok}, Gens: gens, Who: member}))
	}
	if len(failed) > 0 {
		h.must(FinishStep(sprint.FinishReq{As: member, Sel: sprint.Sel{IDs: failed}, Gens: gens, Failed: true, Report: "tests red", Who: member}))
	}
}

// attemptsBy is the members each attempt's work cards were placed on: the
// count of first attempts (.w1) and of redeals (every later attempt) by member.
func attemptsBy(h *harness) (first, again map[string]int) {
	first, again = map[string]int{}, map[string]int{}
	for _, c := range h.snap().Fleet.Cards() {
		p, attempt, ok := sprint.ParseWorkCard(c.ID)
		if !ok || p == "" {
			continue
		}
		m := c.F("member")
		if m == "" {
			m = c.Row
		}
		if attempt == 1 {
			first[m]++
		} else {
			again[m]++
		}
	}
	return first, again
}

// within fails unless every member's count is within most of the others.
func within(t *testing.T, what string, counts map[string]int, most int) {
	t.Helper()
	lo, hi := -1, 0
	for _, m := range ringMembers {
		if lo < 0 || counts[m] < lo {
			lo = counts[m]
		}
		hi = max(hi, counts[m])
	}
	require.LessOrEqual(t, hi-lo, most, "%s by member %v: want every member within %d of the others", what, counts, most)
}

// dealRingWithFailures is a run with failures (errata 3 amendment 5: every
// placement, first attempts and redeals and levelling alike, moves the index):
// eight members beaten up before start, 32 cards in four streams, the machine
// ticking and every member working its ready cards at once, the odd-numbered
// cards' first attempts failing; once every first attempt is dealt the
// coordinator reworks each failed primary, one at a time, each worked at once. The first attempts and the redeals
// each go round the fleet: every member's count of each within one of the
// others, where the shortest queue with its ties by name gave the redeals to
// the first members.
func dealRingWithFailures(t *testing.T, h *harness) {
	ringFleet(h)
	for _, st := range []string{"s1", "s2", "s3", "s4"} {
		h.must(AddStep(sprint.AddReq{Stream: st, Count: 8}))
	}
	h.startMachine()
	failFirst := func(id string) bool {
		p, attempt, ok := sprint.ParseWorkCard(id)
		if !ok || attempt != 1 {
			return false
		}
		_, n, _ := strings.Cut(p, "-")
		k, err := strconv.Atoi(n)
		return err == nil && k%2 == 1
	}
	for i := 0; i < 20; i++ {
		h.machine()
		for _, m := range ringMembers {
			workFailing(h, m, failFirst)
		}
		h.readAll()
		h.tick(time.Second)
		if first, _ := attemptsBy(h); sum(first) == 32 {
			break
		}
	}
	var failed []string
	for _, c := range h.snap().Work.Column(sprint.Review) {
		if c.F("result") == "failed" {
			failed = append(failed, c.ID)
		}
	}
	require.Len(t, failed, 16, "%d primaries came back failed, want 16", len(failed))
	// one rework at a time, each worked at once: every queue is empty at every
	// rework, where the shortest queue with its ties by name gives each to m1
	skips := 0
	for _, id := range failed {
		// every member is idle: the next member round the fleet is the one past
		// the index, and a rework whose failed attempt was on it skips it
		at, _ := h.snap().Fleet.Prop(sprint.PropDealIndex)
		past := indexPast(ringMembers, at)
		next := ringMembers[(slices.Index(ringMembers, past)+1)%len(ringMembers)]
		failedOn := h.snap().Fleet.Card(sprint.WorkCardID(id, 1)).Row
		h.must(ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{id}}, Fix: "make the test pass", Who: "tester"}))
		to := h.snap().Fleet.Card(sprint.WorkCardID(id, 2)).Row
		want := next
		if next == failedOn {
			want = ringMembers[(slices.Index(ringMembers, next)+1)%len(ringMembers)]
			skips++
		}
		require.Equal(t, want, to, "%s (failed on %s) was redealt to %s, want %s: the next member round the fleet past %s, the member that failed it skipped", id, failedOn, to, want, past)
		for _, m := range ringMembers {
			workFailing(h, m, func(string) bool { return false })
		}
	}
	first, again := attemptsBy(h)
	t.Logf("first attempts by member: %v", first)
	t.Logf("redeals by member:        %v (the member that failed a card skipped %d times)", again, skips)
	require.Equal(t, 32, sum(first), "%d first attempts and %d redeals, want 32 and 16", sum(first), sum(again))
	require.Equal(t, 16, sum(again), "%d first attempts and %d redeals, want 32 and 16", sum(first), sum(again))
	within(t, "first attempts", first, 1)
	// a skip of the member that failed the card gives its turn to the next, and
	// the index moves past that one: each skip can put one member a card behind
	within(t, "redeals", again, 1+skips)
	for _, id := range failed {
		was := h.snap().Fleet.Card(sprint.WorkCardID(id, 1))
		now := h.snap().Fleet.Card(sprint.WorkCardID(id, 2))
		if was == nil || now == nil || now.Row == was.Row {
			t.Fatalf("%s's redeal is on %v, its failed attempt on %v: the member that failed it is avoided while another has room", id, now, was)
		}
	}
}

func sum(counts map[string]int) int {
	n := 0
	for _, v := range counts {
		n += v
	}
	return n
}

func TestTheRedealsGoRoundTheFleetInARunWithFailures(t *testing.T) {
	t.Parallel()
	dealRingWithFailures(t, newHarness(t))
}

// dealRingMemberDownAndLevel verifies on eight members that when a member goes
// down, its cards and any levelled cards distribute round the fleet from the
// deal's rolling index (deal_index) rather than to the shortest queue by name
// (errata 3 amendment 5):
//  1. Eight members (m1..m8) are up. 12 cards are dealt, placing 2 cards on
//     m1..m4 and 1 card on m5..m8, leaving deal_index at m4.
//  2. Members m2..m8 take and finish their cards; m1 keeps its 2 ready cards
//     m2..m8 with 0 ready cards. The deal_index remains m4.
//  3. m1's beat lapses and m1 goes down at the tick. Its 2 cards are redealt:
//     starting past deal_index (m4), the cards go to m5 and m6 (the next up
//     members with room), advancing deal_index to m6. Under shortest queue by
//     name, m2 and m3 (having count 0 and earlier names) would have been picked.
//  4. Queues are made uneven while m1 is down: 9 cards are dealt across m2..m8
//     From deal_index m6, the first round is 7 cards (m7, m8, m2, m3, m4, m5, m6),
//     leaving all 7 up members with 1 card each. Next 2 cards go to m7 and m8!
//     Now m7 and m8 have 2 cards each, m2..m6 have 1 card each. deal_index is m8.
//  5. m1 beats again and comes up. Levelling (R7 / T4) moves an excess card
//     round the fleet past deal_index (m8) to m1 (below the mean), moving
//     deal_index past m1 to m1!
func dealRingMemberDownAndLevel(t *testing.T, h *harness) {
	ringFleet(h)
	h.startMachine()
	// Every member up before cards are added
	for i := 0; i < len(ringMembers)+2; i++ {
		h.machine()
		h.tick(time.Second)
	}
	for _, m := range ringMembers {
		st := h.snap().MemberCtl(m).F("status")
		require.Equal(t, string(sprint.Up), st, "member %s status %q, want up", m, st)
	}

	// 1. Add 12 cards to s1 and tick to deal them
	for i := 0; i < 12; i++ {
		h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{fmt.Sprintf("c%02d", i)}}))
	}
	r := ringMachineTick(h)
	t.Logf("deal tick: %s", r)
	require.Len(t, r.deals, 12, "dealt %d cards, want 12", len(r.deals))
	s := h.snap()
	dealAt, _ := s.Fleet.Prop(sprint.PropDealIndex)
	if dealAt != "12" {
		t.Fatalf("deal_index after 12 deals: %q, want 12 (past %s)", dealAt, indexPast(ringMembers, dealAt))
	}

	// 2. m2..m8 take and finish their cards; m1 keeps its 2 ready cards
	for _, m := range ringMembers[1:] {
		h.work(m)
	}
	s = h.snap()
	n := s.Fleet.Count("m1", sprint.Ready)
	require.Equal(t, 2, n, "m1 ready count: %d, want 2", n)
	for _, m := range ringMembers[1:] {
		n := s.Fleet.Count(m, sprint.Ready)
		require.Equal(t, 0, n, "%s ready count: %d, want 0", m, n)
	}
	dealAt, _ = s.Fleet.Prop(sprint.PropDealIndex)
	require.Equal(t, "12", dealAt, "deal_index before down: %q, want 12", dealAt)

	// 3. m1 goes down: stop its beat and advance past BeatDeadline
	h.setLive("m2", "m3", "m4", "m5", "m6", "m7", "m8")
	h.tick(pastDown)
	h.machine()

	// Verify m1 is down and its cards were redealt past deal_index (counter 12, past m4) to m5 and m6
	s = h.snap()
	st := s.MemberCtl("m1").F("status")
	require.Equal(t, string(sprint.Down), st, "m1 status: %q, want down", st)
	n = s.Fleet.Count("m1", sprint.Ready)
	require.Equal(t, 0, n, "m1 ready count: %d, want 0", n)
	n = s.Fleet.Count("m5", sprint.Ready)
	require.Equal(t, 1, n, "m5 ready count: %d, want 1 (rolling index past m4)", n)
	n = s.Fleet.Count("m6", sprint.Ready)
	require.Equal(t, 1, n, "m6 ready count: %d, want 1 (rolling index past m4)", n)
	for _, m := range []string{"m2", "m3", "m4", "m7", "m8"} {
		n := s.Fleet.Count(m, sprint.Ready)
		require.Equal(t, 0, n, "%s ready count: %d, want 0 (shortest queue by name would have chosen m2/m3)", m, n)
	}
	dealAt, _ = s.Fleet.Prop(sprint.PropDealIndex)
	if dealAt != "14" {
		t.Fatalf("deal_index after member down redeals: %q, want 14 (past %s)", dealAt, indexPast(ringMembers, dealAt))
	}

	// 4. While m1 is down, deal 9 cards across the 7 up members (m2..m8)
	// From deal_index counter 14 (past m6), the first round is 7 cards (m7, m8, m2, m3, m4, m5, m6),
	// leaving all 7 up members with 1 card each. Next 2 cards go to m7 and m8!
	// Now m7 and m8 have 2 cards each, m2..m6 have 1 card each. deal_index is 23 (past m8).
	for i := 0; i < 9; i++ {
		h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{fmt.Sprintf("d%02d", i)}}))
	}
	r = ringMachineTick(h)
	t.Logf("deal while m1 down tick: %s", r)
	s = h.snap()
	dealAt, _ = s.Fleet.Prop(sprint.PropDealIndex)
	if dealAt != "24" {
		t.Fatalf("deal_index after 9 deals: %q, want 24 (past %s)", dealAt, indexPast(ringMembers, dealAt))
	}
	n = s.Fleet.Count("m7", sprint.Ready)
	require.Equal(t, 2, n, "m7 ready count: %d, want 2", n)
	n = s.Fleet.Count("m8", sprint.Ready)
	require.Equal(t, 2, n, "m8 ready count: %d, want 2", n)

	// 5. m1 beats again and comes up. Levelling (T4 / R7) triggers because m7/m8
	// have 2 cards and m1 has 0 (differ by 2 > 1).
	// Mean is 9/8 = 1. Only m1 is below the mean (0 < 1).
	// Starting round the fleet from deal_index (23, past m8), m1 is the next member
	// below the mean. So m1 receives a card from the longest queue, and
	// deal_index advances past m1 to 24!
	h.setLive(ringMembers...)
	h.tick(time.Second)
	h.machine()

	s = h.snap()
	st = s.MemberCtl("m1").F("status")
	require.Equal(t, string(sprint.Up), st, "m1 status: %q, want up", st)
	n = s.Fleet.Count("m1", sprint.Ready)
	require.Equal(t, 1, n, "m1 ready count after levelling: %d, want 1", n)
	dealAt, _ = s.Fleet.Prop(sprint.PropDealIndex)
	if dealAt != "25" {
		t.Fatalf("deal_index after levelling: %q, want 25 (past %s)", dealAt, indexPast(ringMembers, dealAt))
	}
}

func TestMemberDownRedealsAndLevelGoRoundTheFleet(t *testing.T) {
	t.Parallel()
	dealRingMemberDownAndLevel(t, newHarness(t))
}
