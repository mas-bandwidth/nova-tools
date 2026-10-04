package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A friend's card (docs/SPEC-SPRINT.md section 1; the owner, 2026-10-03: "doing parts on
// friends where we would normally do friend work"): a brief whose header says WHO: friend
// is dealt by the tick to a friend up below her ready reserve width, on her own fleet row,
// in ready (ready reserve); her workers claim cards with friend take up to her active width;
// a card with no WHO line is the machines' as before.

// friendBrief is a card brief whose header carries the WHO line who.
func friendBrief(who string) string {
	return "c: a friend's card\nREPO: mas-bandwidth/nova-tools\nWHO: " + who + "\n\nThe task."
}

// tierBrief is a card brief of the tier whose header carries the WHO line who.
func tierBrief(who, tier string) string {
	return "c: a friend's card tier: " + tier + "\nREPO: mas-bandwidth/nova-tools\nWHO: " + who + "\n\nThe task."
}

// friendTakes takes each card named on the friend's row, as friend take does.
func friendTakes(w *world, friend string, cards ...string) Plan {
	w.t.Helper()
	return w.must(Take(w.s, TakeReq{Sel: Sel{IDs: cards}, As: FriendRow(friend), Gens: gensOf(w.s, cards...), Who: FriendRow(friend)}))
}

// friendWorld is a world with two machines up and the cards of each brief given, one
// stream, ids s1-1, s1-2, ... in order.
func friendWorld(t *testing.T, briefs ...string) *world {
	w := newWorld(t, "reader-a", "reader-b")
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m1"}))
	w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "m2"}))
	var cards []CardAdd
	for i, b := range briefs {
		cards = append(cards, CardAdd{ID: "s1-" + itoa(i+1), Brief: b})
	}
	w.must(Add(w.s, AddReq{Stream: "s1", Cards: cards}))
	return w
}

// dealWith is the tick's deal part with these friends, applied.
func dealWith(w *world, seats ...FriendSeat) Plan {
	w.t.Helper()
	w.s.Friends = seats
	p, _ := TickDeal(w.s, TickReq{Friends: seats})
	return w.must(p)
}

func TestAFriendsCardIsDealtToTheFriendItNamesOnHerRowInWorking(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend amy"))
	pr := w.s.Primary("s1-1")
	require.Equal(t, FriendRow("amy"), pr.F(FieldWho), "add writes the brief's WHO line on the card")

	// not up, or no width: it waits ready, and no machine is dealt it
	dealWith(w, FriendSeat{Name: "amy", Width: 2, Status: Down, Tiers: []string{"flash"}}, FriendSeat{Name: "bob", Width: 2, Status: Up, Tiers: []string{"flash"}})
	require.Equal(t, Ready, w.s.StateOf("s1-1"))
	require.Nil(t, w.s.Fleet.Card("s1-1.w1"))

	dealWith(w, FriendSeat{Name: "amy", Width: 2, Status: Up, Tiers: []string{"flash"}})
	wc := w.s.Fleet.Card("s1-1.w1")
	require.NotNil(t, wc)
	assert.Equal(t, FriendRow("amy"), wc.Row)
	assert.Equal(t, FriendRow("amy"), wc.F("member"))
	assert.Equal(t, Ready, wc.Col, "staged in ready reserve: child takes it")
	assert.Equal(t, "1", wc.F("gen"))
	assert.Equal(t, Working, w.s.StateOf("s1-1"))
	assert.Equal(t, "s1-1.w1", w.s.Primary("s1-1").F("work"))
	assert.NotContains(t, w.s.Members(), FriendRow("amy"), "her row is no machine of the fleet")
	assert.Empty(t, Check(w.s, nil), "what is always true holds with her card dealt")

	// child claims card: ready -> working
	friendTakes(w, "amy", "s1-1.w1")
	wc = w.s.Fleet.Card("s1-1.w1")
	assert.Equal(t, Working, wc.Col, "now working after take")
	assert.NotEmpty(t, wc.F("taken"), "its deadline is the working one, from its take")

	// the machines' deal verb refuses a friend's card by name
	w2 := friendWorld(t, friendBrief("friend"))
	p := Deal(w2.s, DealReq{Sel: Sel{IDs: []string{"s1-1"}}})
	require.Len(t, p.Refused, 1)
	assert.Contains(t, p.Refused[0].Why, "a friend's card")
}

func TestAFriendIsDealtNoMoreThanHerWidth(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend amy"), friendBrief("friend amy"), friendBrief("friend amy"), friendBrief("friend"))
	seats := []FriendSeat{
		{Name: "amy", Width: 2, Status: Up, Tiers: []string{"flash"}},
		{Name: "bob", Width: 1, Status: Up, Tiers: []string{"flash"}},
	}
	dealWith(w, seats...)
	assert.Equal(t, 2, w.s.Fleet.Count(FriendRow("amy"), Ready), "amy reserve holds her width")
	assert.Equal(t, 1, w.s.Fleet.Count(FriendRow("bob"), Ready), "WHO: friend goes to the friend with reserve room")
	assert.Equal(t, Ready, w.s.StateOf("s1-3"), "the third card for amy waits in backlog: reserve is full")

	// a tick later nothing has freed: nothing more is dealt
	dealWith(w, seats...)
	assert.Equal(t, 2, w.s.Fleet.Count(FriendRow("amy"), Ready))
	assert.Equal(t, Ready, w.s.StateOf("s1-3"))

	// amy takes one, then finishes it: the next tick deals her the third
	friendTakes(w, "amy", "s1-1.w1")
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, As: FriendRow("amy"), Gens: gensOf(w.s, "s1-1.w1"), Head: "abc"}))
	dealWith(w, seats...)
	assert.Equal(t, Working, w.s.StateOf("s1-3"))
	assert.Equal(t, 2, w.s.Fleet.Count(FriendRow("amy"), Ready))
}

func TestWhoFriendGoesToTheUpFriendWithTheMostFreeWidth(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend"), friendBrief("friend"), friendBrief("friend"))
	dealWith(w,
		FriendSeat{Name: "amy", Width: 1, Status: Up, Tiers: []string{"flash"}},
		FriendSeat{Name: "bob", Width: 3, Status: Up, Tiers: []string{"flash"}},
		FriendSeat{Name: "cat", Width: 8, Status: Held, Tiers: []string{"flash"}},
	)
	assert.Equal(t, FriendRow("bob"), w.s.Fleet.Card("s1-1.w1").Row, "bob has three free, amy one")
	assert.Equal(t, FriendRow("bob"), w.s.Fleet.Card("s1-2.w1").Row, "bob still has two free")
	assert.Equal(t, FriendRow("amy"), w.s.Fleet.Card("s1-3.w1").Row, "amy and bob have one each: amy is first by name")
	assert.Equal(t, 0, w.s.Fleet.Count(FriendRow("cat"), Ready), "a held friend is dealt nothing")
}

func TestACardWithNoWhoIsDealtToAMachine(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, "c: a machine's card\nREPO: mas-bandwidth/nova-tools\n\nThe task.")
	require.Empty(t, w.s.Primary("s1-1").F(FieldWho))
	dealWith(w, FriendSeat{Name: "amy", Width: 8, Status: Up, Tiers: []string{"flash"}})
	wc := w.s.Fleet.Card("s1-1.w1")
	require.NotNil(t, wc)
	assert.Contains(t, []string{"m1", "m2"}, wc.Row)
	assert.Equal(t, Ready, wc.Col, "a machine takes its card")
}

func TestAFriendWhoGoesQuietKeepsHerCardAndTheDeadlineHoldsIt(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend amy"))
	dealWith(w, FriendSeat{Name: "amy", Width: 2, Status: Up, Tiers: []string{"flash"}})
	friendTakes(w, "amy", "s1-1.w1")
	require.Equal(t, Working, w.s.Fleet.Card("s1-1.w1").Col)

	// she goes quiet: the presence, the rebalance and the level move nothing of hers
	presenceWith(w, "m1", "m2")
	p, _ := TickLevel(w.s, TickReq{})
	w.must(p)
	wc := w.s.Fleet.Card("s1-1.w1")
	assert.Equal(t, FriendRow("amy"), wc.Row, "no take-back from a friend")
	assert.Equal(t, Working, wc.Col)
	assert.Equal(t, "1", wc.F("gen"))
	// she holds it before its deadline, whatever her status
	mustHold(t, running(w), "s1-1", HeldByActor)

	// past the working deadline the tick's late judgment names it, as any work card's
	w.tick(DeadlineUnfinished + time.Minute)
	p, _ = TickDeadlines(w.s, TickReq{})
	var late []Note
	for _, n := range p.Notes {
		if n.Type == NWorkLate {
			late = append(late, n)
		}
	}
	require.Len(t, late, 1)
	assert.Contains(t, late[0].What, "s1-1.w1")
}

func TestAReworkOfAFriendsCardWaitsReadyForAFriend(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend amy"))
	dealWith(w, FriendSeat{Name: "amy", Width: 2, Status: Up, Tiers: []string{"flash"}})
	friendTakes(w, "amy", "s1-1.w1")
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, As: FriendRow("amy"), Gens: gensOf(w.s, "s1-1.w1"), Failed: true, Report: "friend amy HOLD: the gate is red"}))
	require.Equal(t, Review, w.s.StateOf("s1-1"))
	w.must(Rework(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-1"}}, Fix: "make the gate green"}))
	require.Equal(t, Ready, w.s.StateOf("s1-1"), "a friend's rework is never dealt to a machine")
	require.Nil(t, w.s.Fleet.Card("s1-1.w2"))
	dealWith(w, FriendSeat{Name: "amy", Width: 2, Status: Up, Tiers: []string{"flash"}})
	friendTakes(w, "amy", "s1-1.w2")
	wc := w.s.Fleet.Card("s1-1.w2")
	require.NotNil(t, wc)
	assert.Equal(t, FriendRow("amy"), wc.Row)
	assert.Equal(t, "make the gate green", wc.F("fix"))
}

// A friend's row is no member of the clear's shape: a clear restores the machines, and
// her row, with no control card, comes back only when she is dealt to again.
func TestAFriendsRowIsNoMemberOfTheClearsShape(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend amy"))
	dealWith(w, FriendSeat{Name: "amy", Width: 2, Status: Up, Tiers: []string{"flash"}})
	require.True(t, w.s.Fleet.HasRow(FriendRow("amy")))
	sh := ShapeOf(w.s)
	assert.Equal(t, []string{"m1", "m2"}, sh.Members)
	assert.Equal(t, map[string]string{"m1": Up, "m2": Up}, sh.Status)
}

// Rolling reserve tests: Glenn's explicit requirement: up to width active PLUS width ready reserve.
func TestFriendRollingReserveKeepsUpToWidthActivePlusWidthReserve(t *testing.T) {
	t.Parallel()
	w := friendWorld(t,
		friendBrief("friend amy"),
		friendBrief("friend amy"),
		friendBrief("friend amy"),
		friendBrief("friend amy"),
		friendBrief("friend amy"),
		friendBrief("friend amy"),
	)
	seats := []FriendSeat{{Name: "amy", Width: 2, Status: Up, Tiers: []string{"flash"}}}

	// 1. Initial deal fills the ready-to-pull reserve up to Amy's width (2).
	dealWith(w, seats...)
	assert.Equal(t, 2, w.s.Fleet.Count(FriendRow("amy"), Ready), "amy has 2 cards staged in ready reserve")
	assert.Equal(t, 0, w.s.Fleet.Count(FriendRow("amy"), Working), "amy has 0 active working")
	assert.Equal(t, Ready, w.s.Fleet.Card("s1-1.w1").Col)
	assert.Equal(t, Ready, w.s.Fleet.Card("s1-2.w1").Col)
	assert.Equal(t, Ready, w.s.StateOf("s1-3"), "s1-3 waits in backlog: reserve is full")

	// 2. Child 1 claims s1-1.w1. Moves Ready -> Working.
	friendTakes(w, "amy", "s1-1.w1")
	assert.Equal(t, 1, w.s.Fleet.Count(FriendRow("amy"), Ready), "reserve drops to 1")
	assert.Equal(t, 1, w.s.Fleet.Count(FriendRow("amy"), Working), "1 active working")

	// 3. Rolling replenishment: tick immediately refills the reserve with s1-3 without coordinator nudge!
	dealWith(w, seats...)
	assert.Equal(t, 2, w.s.Fleet.Count(FriendRow("amy"), Ready), "reserve refilled to 2")
	assert.Equal(t, 1, w.s.Fleet.Count(FriendRow("amy"), Working), "still 1 active working")
	assert.Equal(t, Ready, w.s.Fleet.Card("s1-3.w1").Col)
	assert.Equal(t, Ready, w.s.StateOf("s1-4"), "s1-4 waits in backlog")

	// 4. Child 2 claims s1-2.w1. Moves Ready -> Working.
	friendTakes(w, "amy", "s1-2.w1")
	assert.Equal(t, 1, w.s.Fleet.Count(FriendRow("amy"), Ready), "reserve drops to 1")
	assert.Equal(t, 2, w.s.Fleet.Count(FriendRow("amy"), Working), "amy reaches maximum active width (2)")

	// 5. Rolling replenishment: tick immediately refills the reserve with s1-4!
	dealWith(w, seats...)
	assert.Equal(t, 2, w.s.Fleet.Count(FriendRow("amy"), Ready), "reserve refilled to 2 (s1-3.w1, s1-4.w1)")
	assert.Equal(t, 2, w.s.Fleet.Count(FriendRow("amy"), Working), "active working is 2 (s1-1.w1, s1-2.w1)")
	assert.Equal(t, 4, len(w.s.Fleet.Cell(FriendRow("amy"), Ready))+len(w.s.Fleet.Cell(FriendRow("amy"), Working)), "row holds 4 cards: 2 active working + 2 ready reserve")

	// 6. Child 3 attempts to claim s1-3.w1 while amy is already at active width (2).
	// Must be refused to prevent width-overflow!
	p := Take(w.s, TakeReq{Sel: Sel{IDs: []string{"s1-3.w1"}}, As: FriendRow("amy"), Gens: gensOf(w.s, "s1-3.w1"), Who: FriendRow("amy")})
	require.Len(t, p.Refused, 1)
	assert.Contains(t, p.Refused[0].Why, "active width")
	assert.Equal(t, Ready, w.s.Fleet.Card("s1-3.w1").Col, "s1-3 remains staged ready in reserve")

	// 7. Child 1 completes s1-1.w1 (finish). Active working drops to 1.
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, As: FriendRow("amy"), Gens: gensOf(w.s, "s1-1.w1"), Head: "abc"}))
	assert.Equal(t, 1, w.s.Fleet.Count(FriendRow("amy"), Working), "working drops to 1")
	assert.Equal(t, 2, w.s.Fleet.Count(FriendRow("amy"), Ready), "reserve still holds 2")

	// 8. Now Child 3 can claim s1-3.w1!
	friendTakes(w, "amy", "s1-3.w1")
	assert.Equal(t, 2, w.s.Fleet.Count(FriendRow("amy"), Working), "working back to 2")
	assert.Equal(t, 1, w.s.Fleet.Count(FriendRow("amy"), Ready), "reserve drops to 1")

	// 9. Tick replenishes reserve with s1-5!
	dealWith(w, seats...)
	assert.Equal(t, 2, w.s.Fleet.Count(FriendRow("amy"), Ready), "reserve refilled with s1-5.w1")
	assert.Equal(t, Ready, w.s.Fleet.Card("s1-5.w1").Col)
}

func TestFriendReserveTierMatching(t *testing.T) {
	t.Parallel()
	w := friendWorld(t,
		tierBrief("friend", "flash"),
		tierBrief("friend", "pro"),
		tierBrief("friend", "pro"),
		tierBrief("friend", "flash"),
	)
	seats := []FriendSeat{
		{Name: "amy", Width: 2, Status: Up, Tiers: []string{"flash"}},
		{Name: "bob", Width: 2, Status: Up, Tiers: []string{"flash", "pro"}},
	}
	dealWith(w, seats...)
	// amy only takes flash; bob takes flash and pro
	assert.Equal(t, FriendRow("amy"), w.s.Fleet.Card("s1-1.w1").Row)
	assert.Equal(t, FriendRow("bob"), w.s.Fleet.Card("s1-2.w1").Row)
	assert.Equal(t, FriendRow("bob"), w.s.Fleet.Card("s1-3.w1").Row)
	assert.Equal(t, FriendRow("amy"), w.s.Fleet.Card("s1-4.w1").Row)
	assert.Equal(t, 2, w.s.Fleet.Count(FriendRow("amy"), Ready))
	assert.Equal(t, 2, w.s.Fleet.Count(FriendRow("bob"), Ready))
}

func TestFriendEmptyTiersStageAndTakeNothing(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend amy"), friendBrief("friend bob"))
	// Recovered tier contract: a friend whose row carries no tiers takes no card until sync writes them
	seats := []FriendSeat{
		{Name: "amy", Width: 2, Status: Up, Tiers: nil},
		{Name: "bob", Width: 2, Status: Up, Tiers: []string{}},
	}
	dealWith(w, seats...)
	assert.Equal(t, 0, w.s.Fleet.Count(FriendRow("amy"), Ready), "empty tiers stage nothing")
	assert.Equal(t, 0, w.s.Fleet.Count(FriendRow("bob"), Ready), "empty tiers stage nothing")
	assert.Equal(t, Ready, w.s.StateOf("s1-1"))
	assert.Equal(t, Ready, w.s.StateOf("s1-2"))

	// Staging works once sync writes tiers
	seats[0].Tiers = []string{"flash"}
	dealWith(w, seats...)
	assert.Equal(t, 1, w.s.Fleet.Count(FriendRow("amy"), Ready), "stages once tiers are present")
	assert.Equal(t, 0, w.s.Fleet.Count(FriendRow("bob"), Ready), "bob still has empty tiers")
	assert.Equal(t, Ready, w.s.Fleet.Card("s1-1.w1").Col)
	assert.Equal(t, Ready, w.s.StateOf("s1-2"), "bob card still waits in backlog")

	// Take is refused if friend tiers do not include the card tier
	w.s.Friends = []FriendSeat{{Name: "amy", Width: 2, Status: Up, Tiers: []string{"pro"}}}
	p := Take(w.s, TakeReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, As: FriendRow("amy"), Gens: gensOf(w.s, "s1-1.w1"), Who: FriendRow("amy")})
	require.Len(t, p.Refused, 1)
	assert.Contains(t, p.Refused[0].Why, "cannot do tier flash")
}

func TestFriendTakeFencingPresenceHoldDownTimeoutTier(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend amy"))
	seats := []FriendSeat{{Name: "amy", Width: 2, Status: Up, Tiers: []string{"flash"}}}
	dealWith(w, seats...)
	require.NotNil(t, w.s.Fleet.Card("s1-1.w1"))

	// 1. Refused if friend has no presence record
	w.s.Friends = nil
	p := Take(w.s, TakeReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, As: FriendRow("amy"), Gens: gensOf(w.s, "s1-1.w1"), Who: FriendRow("amy")})
	require.Len(t, p.Refused, 1)
	assert.Contains(t, p.Refused[0].Why, "no presence record")

	// 2. Refused if friend is held
	w.s.Friends = []FriendSeat{{Name: "amy", Width: 2, Status: Held, Tiers: []string{"flash"}}}
	p = Take(w.s, TakeReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, As: FriendRow("amy"), Gens: gensOf(w.s, "s1-1.w1"), Who: FriendRow("amy")})
	require.Len(t, p.Refused, 1)
	assert.Contains(t, p.Refused[0].Why, "held")

	// 3. Refused if friend is down (beat timeout)
	w.s.Friends = []FriendSeat{{Name: "amy", Width: 2, Status: Down, Tiers: []string{"flash"}}}
	p = Take(w.s, TakeReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, As: FriendRow("amy"), Gens: gensOf(w.s, "s1-1.w1"), Who: FriendRow("amy")})
	require.Len(t, p.Refused, 1)
	assert.Contains(t, p.Refused[0].Why, "down")

	// 4. Refused if wrong generation
	w.s.Friends = []FriendSeat{{Name: "amy", Width: 2, Status: Up, Tiers: []string{"flash"}}}
	p = Take(w.s, TakeReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, As: FriendRow("amy"), Gens: map[string]int{"s1-1.w1": 99}, Who: FriendRow("amy")})
	require.Len(t, p.Refused, 1)
	assert.Contains(t, p.Refused[0].Why, "generation")

	// 5. Success when all predicates hold
	friendTakes(w, "amy", "s1-1.w1")
	assert.Equal(t, Working, w.s.Fleet.Card("s1-1.w1").Col)
}

func TestFriendHoldReclaimsTasksToReady(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, friendBrief("friend amy"), friendBrief("friend amy"))
	seats := []FriendSeat{{Name: "amy", Width: 2, Status: Up, Tiers: []string{"flash"}}}
	dealWith(w, seats...)

	// Amy takes the first card; the second stays in ready reserve
	friendTakes(w, "amy", "s1-1.w1")
	assert.Equal(t, Working, w.s.Fleet.Card("s1-1.w1").Col)
	assert.Equal(t, Ready, w.s.Fleet.Card("s1-2.w1").Col)
	assert.Equal(t, Working, w.s.StateOf("s1-1"))
	assert.Equal(t, Working, w.s.StateOf("s1-2"))

	// Coordinator holds Amy: atomically reclaims working and ready tasks back to ready pool
	holdPlan := FriendHold(w.s, "amy", "glenn")
	require.Len(t, holdPlan.Units, 2)
	w.must(holdPlan)

	// Both work cards on fleet row are Withdrawn at next generation (2)
	c1 := w.s.Fleet.Card("s1-1.w1")
	c2 := w.s.Fleet.Card("s1-2.w1")
	assert.Equal(t, Withdrawn, c1.Col)
	assert.Equal(t, Withdrawn, c2.Col)
	assert.Equal(t, "2", c1.F("gen"), "generation bumped to fence stale reports")
	assert.Equal(t, "2", c2.F("gen"))
	assert.Empty(t, c1.F("taken"), "taken cleared")
	assert.Empty(t, c1.F("dealt"), "dealt cleared")
	assert.Empty(t, c1.F(FieldTakeEnded), "no model failure penalty charged")
	assert.Empty(t, c2.F(FieldTakeEnded), "no model failure penalty charged")

	// Both primaries in Work table return to Ready, work cleared
	assert.Equal(t, Ready, w.s.StateOf("s1-1"))
	assert.Equal(t, Ready, w.s.StateOf("s1-2"))
	assert.Empty(t, w.s.Primary("s1-1").F("work"))
	assert.Empty(t, w.s.Primary("s1-2").F("work"))
	assert.Equal(t, 1, w.s.Primary("s1-1").Int("attempt"), "attempt preserved")
	assert.Equal(t, 1, w.s.Primary("s1-2").Int("attempt"), "attempt preserved")

	// Invariant check passes
	assert.Empty(t, Check(w.s, nil))

	// Old stale finish attempt for s1-1.w1 with gen 1 is rejected
	p := Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, As: FriendRow("amy"), Gens: map[string]int{"s1-1.w1": 1}, Head: "abc"})
	require.Len(t, p.Refused, 1)
	assert.Contains(t, p.Refused[0].Why, "generation")

	// Redeal after release: redeals existing withdrawn card s1-1.w1 preserving attempt 1 and bumping gen
	seats[0].Status = Up
	dealWith(w, seats...)
	wc1 := w.s.Fleet.Card("s1-1.w1")
	require.NotNil(t, wc1, "s1-1.w1 redealt cleanly")
	assert.Equal(t, Ready, wc1.Col)
	assert.Equal(t, 1, wc1.Int("attempt"))
	assert.Equal(t, 3, wc1.Int("gen"))
	assert.Nil(t, w.s.Fleet.Card("s1-1.w2"), "did not consume attempt 2")
}

func TestFriendTakeTierDerivation(t *testing.T) {
	t.Parallel()

	proBrief := "c: work on pro tier: pro\nWHO: friend amy\n\npro task"
	flashBrief := "c: work on flash\nWHO: friend amy\n\nflash task"

	// 1. Pro primary card dealt to friend Amy who can do pro
	w := friendWorld(t, proBrief)
	require.Equal(t, "pro", FriendTier(w.s.Primary("s1-1")))

	seats := []FriendSeat{
		{Name: "amy", Width: 2, Status: Up, Tiers: []string{"flash", "pro"}},
	}
	dealWith(w, seats...)
	wc := w.s.Fleet.Card("s1-1.w1")
	require.NotNil(t, wc)
	assert.Equal(t, "pro", wc.F("tier"))

	// Amy takes it with tiers ["flash", "pro"]: succeeds
	plan := friendTakes(w, "amy", "s1-1.w1")
	require.Empty(t, plan.Refused)
	require.Equal(t, Working, w.s.Fleet.Card("s1-1.w1").Col)

	// 2. Pro primary card dealt, but Amy only allowed ["flash"]: claim is refused
	w2 := friendWorld(t, proBrief)
	dealWith(w2, FriendSeat{Name: "amy", Width: 2, Status: Up, Tiers: []string{"pro"}})
	wc2 := w2.s.Fleet.Card("s1-1.w1")
	require.NotNil(t, wc2)

	// When taking, Amy's current seat tiers are flash-only
	w2.s.Friends = []FriendSeat{
		{Name: "amy", Width: 2, Status: Up, Tiers: []string{"flash"}},
	}
	plan2 := Take(w2.s, TakeReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, As: FriendRow("amy"), Gens: gensOf(w2.s, "s1-1.w1"), Who: FriendRow("amy")})
	require.Len(t, plan2.Refused, 1)
	assert.Contains(t, plan2.Refused[0].Why, "cannot do tier pro (allowed: flash)")

	// 3. Flash primary card dealt, Amy allowed only ["pro"]: claim is refused
	w3 := friendWorld(t, flashBrief)
	require.Equal(t, "flash", FriendTier(w3.s.Primary("s1-1")))
	dealWith(w3, FriendSeat{Name: "amy", Width: 2, Status: Up, Tiers: []string{"flash"}})
	wc3 := w3.s.Fleet.Card("s1-1.w1")
	require.NotNil(t, wc3)
	assert.Equal(t, "flash", wc3.F("tier"))

	// Amy only has pro
	w3.s.Friends = []FriendSeat{
		{Name: "amy", Width: 2, Status: Up, Tiers: []string{"pro"}},
	}
	plan3 := Take(w3.s, TakeReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, As: FriendRow("amy"), Gens: gensOf(w3.s, "s1-1.w1"), Who: FriendRow("amy")})
	require.Len(t, plan3.Refused, 1)
	assert.Contains(t, plan3.Refused[0].Why, "cannot do tier flash (allowed: pro)")

	// 4. Pro-only friend taking pro card succeeds
	w4 := friendWorld(t, proBrief)
	dealWith(w4, FriendSeat{Name: "amy", Width: 2, Status: Up, Tiers: []string{"pro"}})
	plan4 := friendTakes(w4, "amy", "s1-1.w1")
	require.Empty(t, plan4.Refused)
	require.Equal(t, Working, w4.s.Fleet.Card("s1-1.w1").Col)
}
