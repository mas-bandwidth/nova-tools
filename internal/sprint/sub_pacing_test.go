package sprint_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/hostload"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A subscription friend is paced to her plan's reset (docs/SPEC-SPRINT.md section 1,
// sub-pacingb.w1; the owner, 2026-10-04: "PACING and offloading", "always make sure that
// subs are 100% utilized before spending on fleet"), on the twin store with a fake clock:
// no wall-clock sleep, no socket. amy and bob are subscription friends (windows 5h, width
// 2, tier pro), cat is a friend with no windows (tier flash), and m1 is a machine of the
// fleet (the paid width).

// paceRig is the running sprint on the twin.
type paceRig struct {
	t   *testing.T
	st  *store.Store
	mem *store.Mem
	ctx context.Context
	mu  sync.Mutex
	now time.Time
}

var paceT0 = time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)

func paceBrief(who string) string {
	tier := "pro"
	if who == "friend cat" {
		tier = "flash" // cat's tier: she takes no pro card, so the subscription friends' cards are theirs or the fleet's
	}
	return "c: a friend's card tier: " + tier + "\nREPO: mas-bandwidth/nova-tools\nWHO: " + who + "\n\nThe task."
}

func newPaceRig(t *testing.T) *paceRig {
	t.Helper()
	m := store.NewMem()
	r := &paceRig{t: t, mem: m, ctx: context.Background(), now: paceT0}
	n := 0
	r.st = &store.Store{B: m, Names: sprint.Names{Prefix: "t-"}, Actor: "coordinator",
		Now:   func() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return r.now },
		NewID: func() string { r.mu.Lock(); defer r.mu.Unlock(); n++; return fmt.Sprint(n) },
		Sleep: func(time.Duration) {}}
	require.NoError(t, r.st.Init(r.ctx))
	require.NoError(t, m.SetCoordinator(r.ctx, "coordinator"))
	_, _, _, err := r.st.SyncFriends(r.ctx, []store.FriendSpec{
		{Name: "amy", Width: 2, Class: "pro", Windows: []string{"5h"}, Usage: "limit-messages"},
		{Name: "bob", Width: 2, Class: "pro", Windows: []string{"5h"}, Usage: "limit-messages"},
		{Name: "cat", Width: 1, Class: "flash"},
	})
	require.NoError(t, err)
	r.must(store.FleetStep(sprint.FleetReq{Op: "up", Member: "m1", Width: 2}))
	_, _, _, err = r.st.SetMachine(r.ctx, true)
	require.NoError(t, err)
	return r
}

func (r *paceRig) advance(d time.Duration) {
	r.mu.Lock()
	r.now = r.now.Add(d)
	r.mu.Unlock()
}

// tick moves the clock a second, beats every friend and runs one tick of the machine.
func (r *paceRig) tick() {
	r.t.Helper()
	r.advance(time.Second)
	for _, f := range []string{"amy", "bob", "cat"} {
		_, err := r.st.FriendBeat(r.ctx, f)
		require.NoError(r.t, err)
	}
	zero := 0.0
	_, err := r.st.Beat(r.ctx, "m1", &zero, hostload.Source{})
	require.NoError(r.t, err)
	_, err = r.st.Tick(r.ctx)
	require.NoError(r.t, err)
}

func (r *paceRig) must(step store.Step) store.Result {
	r.t.Helper()
	res, err := r.st.Run(r.ctx, step)
	require.NoError(r.t, err, step.Verb)
	require.Empty(r.t, res.Refused, "%s refused", step.Verb)
	return res
}

func (r *paceRig) add(stream string, ids map[string]string) {
	r.t.Helper()
	var cards []sprint.CardAdd
	for _, id := range sortedIDs(ids) {
		cards = append(cards, sprint.CardAdd{ID: id, Brief: paceBrief(ids[id])})
	}
	r.must(store.AddStep(sprint.AddReq{Stream: stream, Cards: cards}))
}

func sortedIDs(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func (r *paceRig) snap() *sprint.Snapshot {
	r.t.Helper()
	s, err := r.st.Load(r.ctx, store.All, nil)
	require.NoError(r.t, err)
	return s
}

// finish ends the friend's work card ok, with what it spent (tokens of output, "" for no
// usage) and a limit until when the report named one.
func (r *paceRig) finish(friend, card string, tokens int64, until time.Time) {
	r.t.Helper()
	row := sprint.FriendRow(friend)
	s := r.snap()
	wc := s.Fleet.Card(card)
	require.NotNil(r.t, wc, "no work card %s", card)
	req := sprint.FinishReq{Sel: sprint.Sel{IDs: []string{card}}, As: row, Gens: map[string]int{card: wc.Int("gen")}, Head: "abc", Who: row}
	if tokens > 0 {
		req.Usage = fmt.Sprintf("input=0 cache_read=0 cache_write=0 output=%d reasoning=0 requests=1", tokens)
	}
	if !until.IsZero() {
		req.LimitUntil, req.LimitReason = until, "usage limit reached"
	}
	r.must(store.FinishStep(req))
}

func (r *paceRig) pace(friend string) sprint.FriendPace {
	r.t.Helper()
	p, ok := sprint.SubPaceOf(r.snap().Fleet, friend)
	require.True(r.t, ok, "no pace record of %s", friend)
	return p
}

func (r *paceRig) rowOf(card string) (row, col string) {
	wc := r.snap().Fleet.Card(card)
	if wc == nil {
		return "", ""
	}
	return wc.Row, wc.Col
}

func TestASubscriptionFriendIsPacedToItsReset(t *testing.T) {
	t.Parallel()
	r := newPaceRig(t)
	amy, bob, cat := sprint.FriendRow("amy"), sprint.FriendRow("bob"), sprint.FriendRow("cat")

	// four cards each: two working, two ready behind them, within DealAhead x width
	r.add("f1", map[string]string{"a1": "friend amy", "a2": "friend amy", "a3": "friend amy", "a4": "friend amy",
		"b1": "friend bob", "b2": "friend bob", "b3": "friend bob", "b4": "friend bob", "c1": "friend cat"})
	r.tick()
	s := r.snap()
	require.Equal(t, 2, s.Fleet.Count(amy, sprint.Working))
	require.Equal(t, 2, s.Fleet.Count(amy, sprint.Ready))
	require.Equal(t, 2, s.Fleet.Count(bob, sprint.Working))
	require.Equal(t, 1, s.Fleet.Count(cat, sprint.Working), "a friend with no windows is dealt as before")
	require.Equal(t, 0, s.Fleet.Count("m1", sprint.Working)+s.Fleet.Count("m1", sprint.Ready), "paid_width off: a card a subscription friend covers goes to no machine")
	_, hasCat := sprint.SubPaceOf(s.Fleet, "cat")
	assert.False(t, hasCat, "a friend with no windows has no pace record")
	p := r.pace("amy")
	assert.Equal(t, 2, p.Width, "unpaced until an allowance is known: her paced width is her configured width")
	require.Len(t, p.Windows, 1)
	assert.True(t, p.Windows[0].Start.IsZero(), "a window starts at her first record")

	// her first window: 1000 tokens over four cards, bob's cost nothing
	r.finish("amy", "a1.w1", 500, time.Time{})
	r.finish("amy", "a2.w1", 500, time.Time{})
	p = r.pace("amy")
	assert.Equal(t, int64(1000), p.Windows[0].Burn, "the burn is the tokens of her usage records")
	assert.Equal(t, 2, p.Windows[0].Cards)
	assert.False(t, p.Windows[0].Known, "no allowance before a full window or a limit")
	assert.False(t, p.Windows[0].Start.IsZero(), "the window started at her first record")
	require.Equal(t, 2, r.snap().Fleet.Count(amy, sprint.Working), "a3 and a4 took her lanes at her finishes")
	r.drain("amy")
	r.drain("bob")
	r.drain("cat")

	// the window ends: its burn is the next window's allowance
	r.advance(5 * time.Hour)
	r.tick()
	p = r.pace("amy")
	assert.True(t, p.Windows[0].Known)
	assert.Equal(t, int64(1000), p.Windows[0].Allowance, "the allowance is the burn of the last full window")
	assert.Equal(t, int64(0), p.Windows[0].Burn, "the new window starts empty")
	assert.Equal(t, 2, p.Width, "behind pace: her width is her configured width")

	// four more cards for her, two ready behind her working ones; bob has headroom
	r.add("f1", map[string]string{"a5": "friend amy", "a6": "friend amy", "a7": "friend amy", "a8": "friend amy", "b5": "friend bob", "b6": "friend bob"})
	r.tick()
	s = r.snap()
	require.Equal(t, 2, s.Fleet.Count(amy, sprint.Working))
	require.Equal(t, 2, s.Fleet.Count(amy, sprint.Ready))
	require.Equal(t, 2, s.Fleet.Count(bob, sprint.Working))
	require.Equal(t, 0, s.Fleet.Count(bob, sprint.Ready))

	// she burns her whole allowance in the first tenth of the window: 2x her pace and more
	r.advance(30 * time.Minute)
	r.finish("amy", "a5.w1", 1000, time.Time{})
	p = r.pace("amy")
	assert.Equal(t, int64(1000), p.Windows[0].Burn)
	assert.Greater(t, p.Windows[0].Burn, p.Windows[0].Paced(r.now), "ahead of pace")
	r.tick()
	assert.Equal(t, 1, r.pace("amy").Width, "ahead: one less per tick")
	r.tick()
	assert.Equal(t, 0, r.pace("amy").Width, "ahead: down to nothing, never below")
	r.tick()
	assert.Equal(t, 0, r.pace("amy").Width)
	s = r.snap()
	row, col := r.rowOf("a8.w1")
	assert.Equal(t, bob, row, "her unstarted ready card is offloaded to the subscription friend with headroom")
	assert.Equal(t, sprint.Ready, col)
	assert.Equal(t, 2, s.Fleet.Count(amy, sprint.Working), "lowering the width takes back nothing she holds in working (a6, a7)")
	assert.Equal(t, 0, s.Fleet.Count(amy, sprint.Ready))
	assert.Equal(t, 0, s.Fleet.Count("m1", sprint.Working)+s.Fleet.Count("m1", sprint.Ready), "paid_width off: nothing goes to the fleet")

	// paid width: off, a card no subscription friend has room for waits; on, the fleet takes
	// it, and only while no subscription friend has headroom
	r.add("f1", map[string]string{"d1": "friend", "d2": "friend"})
	r.tick()
	r.tick()
	s = r.snap()
	row, _ = r.rowOf("d1.w1")
	assert.Equal(t, bob, row, "subscription friends first: bob's last room")
	assert.Nil(t, s.Fleet.Card("d2.w1"), "paid_width off: d2 waits ready for a subscription friend")
	assert.Equal(t, sprint.Ready, s.StateOf("d2"))
	r.mem.SetPaidWidth(true)
	r.tick()
	row, _ = r.rowOf("d2.w1")
	assert.Equal(t, "m1", row, "paid_width on and no subscription friend with headroom: the fleet takes it")
	r.finish("bob", "b5.w1", 0, time.Time{})
	r.add("f1", map[string]string{"d3": "friend"})
	r.tick()
	row, _ = r.rowOf("d3.w1")
	assert.Equal(t, bob, row, "paid_width on, a subscription friend with headroom: she takes it, never the fleet")
	r.mem.SetPaidWidth(false)
	require.Equal(t, 4, r.snap().Fleet.Count(bob, sprint.Working)+r.snap().Fleet.Count(bob, sprint.Ready), "bob is full")

	// a limit message: she is down until the reset, her unstarted card taken back and dealt
	// again; a6 ends with the limit, a7 is unstarted
	until := r.now.Add(2 * time.Hour)
	r.finish("amy", "a6.w1", 100, until)
	p = r.pace("amy")
	assert.True(t, p.Limited(r.now))
	assert.Equal(t, until.UTC(), p.Until)
	require.Len(t, p.Limits, 1)
	assert.Equal(t, "usage limit reached", p.Limits[0].Reason)
	r.add("f1", map[string]string{"a9": "friend amy"})
	r.tick()
	s = r.snap()
	assert.Equal(t, sprint.Withdrawn, s.Fleet.Card("a7.w1").Col, "her unstarted card is taken back at once")
	assert.Equal(t, sprint.Ready, s.StateOf("a7"))
	assert.Equal(t, 0, s.Fleet.Count(amy, sprint.Working)+s.Fleet.Count(amy, sprint.Ready))
	assert.Nil(t, s.Fleet.Card("a9.w1"), "a limited friend is never dealt")
	r.tick()
	s = r.snap()
	assert.Equal(t, 0, s.Fleet.Count(amy, sprint.Working)+s.Fleet.Count(amy, sprint.Ready), "still limited: dealt nothing")
	assert.Equal(t, sprint.Ready, s.StateOf("a7"), "no subscription friend has room and paid_width is off: it waits")
	r.drain("bob")
	r.tick()
	s = r.snap()
	assert.Equal(t, bob, s.Fleet.Card("a7.w1").Row, "a subscription friend with room again: her taken-back card is dealt to him")
	assert.Equal(t, "3", s.Fleet.Card("a7.w1").F("gen"), "at its next generation, its own branch and job")
	r.drain("bob")

	// the reset: she comes back, her window restarts, and she is dealt again
	r.advance(2*time.Hour + time.Minute)
	r.tick()
	p = r.pace("amy")
	assert.False(t, p.Limited(r.now))
	assert.Equal(t, until.UTC(), p.Windows[0].Start, "the window restarts at the reset")
	assert.Equal(t, int64(0), p.Windows[0].Burn)
	assert.Equal(t, 1, p.Width, "behind pace: one more per tick")
	r.add("f1", map[string]string{"a10": "friend amy"})
	r.tick()
	s = r.snap()
	assert.Equal(t, amy, s.Fleet.Card("a10.w1").Row, "back at the reset, she is dealt again")
	assert.Equal(t, sprint.Working, s.Fleet.Card("a10.w1").Col)
	assert.Equal(t, 2, r.pace("amy").Width)
	assert.Equal(t, sprint.Up, friendStatusOf(t, r, "amy"))

	// the view: one line per subscription friend
	rows, err := r.st.FriendRows(r.ctx, r.now)
	require.NoError(t, err)
	seen := 0
	for _, fr := range rows {
		if fr.Name != "amy" {
			continue
		}
		seen++
		v := sprint.PaceView(r.snap().Fleet, fr.Name, fr.Windows, fr.Width, r.now)
		assert.Equal(t, 2, v.Width)
		assert.Equal(t, 2, v.Configured)
		require.Len(t, v.Windows, 1)
		assert.Equal(t, "5h", v.Windows[0].Window)
		assert.Contains(t, v.Line(r.now), "PACE amy 5h burn=0/")
		assert.Contains(t, v.Line(r.now), "width=2/2 reset=")
	}
	assert.Equal(t, 1, seen)
	_ = cat
}

// drain finishes every card on the friend's row ok with no usage, her next taken at each
// finish, until she holds none: nothing of hers is working across a long clock advance.
func (r *paceRig) drain(friend string) {
	r.t.Helper()
	row := sprint.FriendRow(friend)
	for i := 0; i < 32; i++ {
		s := r.snap()
		ws := s.Fleet.Cell(row, sprint.Working)
		if len(ws) == 0 {
			require.Equal(r.t, 0, s.Fleet.Count(row, sprint.Ready), "%s holds ready cards and no working one", friend)
			return
		}
		r.finish(friend, ws[0].ID, 0, time.Time{})
	}
	r.t.Fatalf("%s is not drained after 32 finishes", friend)
}

func friendStatusOf(t *testing.T, r *paceRig, name string) string {
	t.Helper()
	rows, err := r.st.FriendRows(r.ctx, r.now)
	require.NoError(t, err)
	for _, fr := range rows {
		if fr.Name == name {
			return fr.Status
		}
	}
	return ""
}

func TestWindowsAreOneOrTwoDurationWordsOrWeekly(t *testing.T) {
	t.Parallel()
	w := sprint.PaceWindow{Word: "weekly", Start: paceT0, Allowance: 7000, Known: true}
	assert.Equal(t, 7*24*time.Hour, w.Length())
	assert.Equal(t, int64(1000), w.Paced(paceT0.Add(24*time.Hour)), "paced burn is allowance x elapsed / length")
	assert.Equal(t, int64(7000), w.Paced(paceT0.Add(10*24*time.Hour)), "never past the allowance")
	w.Burn = 1001
	assert.True(t, w.Ahead(paceT0.Add(24*time.Hour)))
	w.Burn = 1000
	assert.False(t, w.Ahead(paceT0.Add(24*time.Hour)))
}

func TestTheUsageReadersReadALimitAndItsReset(t *testing.T) {
	t.Parallel()
	now := paceT0
	read, ok := sprint.ReadUsage("limit-messages", "Verdict: HOLD\n\nThe harness said: usage limit reached, resets at 2030-01-02T08:00:00Z.\n", now)
	require.True(t, ok)
	assert.True(t, read.Limited)
	assert.Equal(t, time.Date(2030, 1, 2, 8, 0, 0, 0, time.UTC), read.Until)
	read, ok = sprint.ReadUsage("codex-limit", "You've hit your usage limit. Try again in 2h 10m.", now)
	require.True(t, ok)
	assert.Equal(t, now.Add(2*time.Hour+10*time.Minute), read.Until)
	read, ok = sprint.ReadUsage("claude-status", "Current session: 100% used, resets in 45 minutes\nWeekly: 40% used", now)
	require.True(t, ok)
	assert.Equal(t, now.Add(45*time.Minute), read.Until)
	_, ok = sprint.ReadUsage("limit-messages", "Verdict: LAND\nHead: abc\n\nDone.\n", now)
	assert.False(t, ok, "a report with no limit word reads as none")
}
