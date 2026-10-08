package sprint

import (
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A named WHO pin waits for its preferred friend before the bound, then leaves
// a durable preference and waiver when another same-tier friend takes it.
func TestPinPreferenceHasAClock(t *testing.T) {
	t.Parallel()
	seats := []FriendSeat{
		{Name: "amy", Width: 1, Status: Down, Class: "pro"},
		{Name: "bob", Width: 1, Status: Up, Class: "pro"},
	}
	w := friendWorld(t, "c: work tier: pro\nWHO: friend amy\n\nWork.")
	dealWith(w, seats...)
	assert.Equal(t, Ready, w.s.StateOf("s1-1"))
	require.Nil(t, w.s.Fleet.Card("s1-1.w1"))
	w.tick(PinWaitDefault + time.Second)
	p := dealWith(w, seats...)
	wc := w.s.Fleet.Card("s1-1.w1")
	require.NotNil(t, wc)
	assert.Equal(t, FriendRow("bob"), wc.Row)
	pr := w.s.Primary("s1-1")
	assert.Equal(t, "amy", pr.F(FieldPreferred))
	assert.NotEmpty(t, pr.F(FieldPinWaivedAt))
	assert.Equal(t, FriendRow("amy"), pr.F(FieldWho))
	var story string
	for _, u := range p.Units {
		story += u.Moved
	}
	assert.True(t, strings.Contains(story, "pin to amy waived after"), story)
}

func TestOnlyPinNeverWaives(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, "c: work tier: pro\nWHO: friend amy only\n\nWork.")
	w.tick(time.Hour)
	dealWith(w, FriendSeat{Name: "amy", Width: 1, Status: Down, Class: "pro"}, FriendSeat{Name: "bob", Width: 1, Status: Up, Class: "pro"})
	assert.Equal(t, Ready, w.s.StateOf("s1-1"))
	assert.Nil(t, w.s.Fleet.Card("s1-1.w1"))
}

func TestWaivedPinPrefersReturnedFriendOnNextAttempt(t *testing.T) {
	t.Parallel()
	w := friendWorld(t, "c: work tier: pro\nWHO: friend amy\n\nWork.")
	amy := FriendSeat{Name: "amy", Width: 1, Status: Down, Class: "pro"}
	bob := FriendSeat{Name: "bob", Width: 1, Status: Up, Class: "pro"}
	w.tick(PinWaitDefault + time.Second)
	dealWith(w, amy, bob)
	startLanes(w, bob)
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{"s1-1.w1"}}, As: FriendRow("bob"), Gens: gensOf(w.s, "s1-1.w1"), Failed: true, Report: "HOLD: retry"}))
	w.must(Rework(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-1"}}, Fix: "retry with preferred friend"}))
	amy.Status = Up
	dealWith(w, amy, bob)
	wc := w.s.Fleet.Card("s1-1.w2")
	require.NotNil(t, wc)
	assert.Equal(t, FriendRow("amy"), wc.Row)
	assert.Empty(t, w.s.Primary("s1-1").F(FieldPinWaivedAt))
}

func TestPinWaitSetting(t *testing.T) {
	t.Parallel()
	w := friendWorld(t)
	assert.Equal(t, PinWaitDefault, w.s.PinWait())
	w.must(Set(w.s, SetReq{Who: "coordinator", PinWait: "5m"}))
	assert.Equal(t, 5*time.Minute, w.s.PinWait())
}

func TestWaivedPinReturnsOnlyBeforeStart(t *testing.T) {
	t.Parallel()
	seats := []FriendSeat{
		{Name: "amy", Width: 1, Status: Up, Tiers: []string{cardhdr.RouteFlash}},
		{Name: "bob", Width: 1, Status: Up, Tiers: []string{cardhdr.RouteFlash}},
	}
	for _, tc := range []struct {
		name, col, want string
	}{
		{"queued", Ready, FriendRow("amy")},
		{"started", Working, FriendRow("bob")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := rbWorld(t, "m1")
			rbPlace(w, "s1-1", cardhdr.RouteFlash, FriendRow("bob"), tc.col,
				"pr."+FieldWho, FriendRow("amy"), "pr."+FieldPreferred, "amy", "pr."+FieldPinWaivedAt, stamp(w.s.Now))
			w.must(Rebalance(w.s, seats, "machine"))
			assert.Equal(t, tc.want, w.s.Fleet.Card("s1-1.w1").Row)
			if tc.col == Ready {
				assert.Empty(t, w.s.Primary("s1-1").F(FieldPinWaivedAt))
			}
		})
	}
}
