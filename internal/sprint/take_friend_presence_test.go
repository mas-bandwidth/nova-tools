package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A take by or for a friend reads her presence (docs/SPEC-SPRINT.md section 1, a take
// for a friend; the card take-by-id-reads-the-friends-presence.w1). The night of
// 2026-10-05, a take --as friend.<f> for two friends was refused "member
// friend.<f> is -" while where showed both up: a friend's row has no control card status
// (only a machine's does). Her take is admitted by FriendStatus, as the snapshot's friend
// seats carry it, and refused when she is held, her daemon does not beat, or she has no
// evidence from her session in its window (a beat alone is none: card
// presence-from-session-only), the refusal naming which of the two is missing; a machine's
// take keeps its control card's rule.

// readyForAmy is a world with amy dealt three cards at width 2: two working and s1-3.w1
// ready on her row, which a take at her raised width 3 can take.
func readyForAmy(t *testing.T) *world {
	w := friendWorld(t, friendBrief("only friend amy"), friendBrief("only friend amy"), friendBrief("only friend amy"))
	dealStarted(w, FriendSeat{Name: "amy", Width: 2, Status: Up, Class: "flash,pro"})
	wc := w.s.Fleet.Card("s1-3.w1")
	require.NotNil(t, wc)
	require.Equal(t, Ready, wc.Col)
	require.Empty(t, w.s.MemberCtl(FriendRow("amy")).F("status"), "her row has no control card status")
	return w
}

func takeForAmy(w *world, seat FriendSeat) Plan {
	w.s.Friends = []FriendSeat{seat}
	wc := w.s.Fleet.Card("s1-3.w1")
	return Take(w.s, TakeReq{Sel: Sel{IDs: []string{wc.ID}}, As: FriendRow("amy"), Gens: map[string]int{wc.ID: wc.Int("gen")}, Who: FriendRow("amy")})
}

func TestATakeForAFriendUpOnHerSessionIsAdmittedWithoutAControlStatus(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 5, 23, 0, 0, 0, time.UTC)

	t.Run("a card finished by her session: admitted by FriendStatus, within her width", func(t *testing.T) {
		t.Parallel()
		w := readyForAmy(t)
		up := FriendStatus(FriendPresence{Beat: Beat{At: now.Add(-time.Second)}, Finished: now.Add(-time.Minute)}, now)
		require.Equal(t, Up, up)
		w.must(takeForAmy(w, FriendSeat{Name: "amy", Width: 3, Status: up, Class: "flash,pro"}))
		assert.Equal(t, Working, w.s.Fleet.Card("s1-3.w1").Col)
		assert.Equal(t, FriendRow("amy"), w.s.Fleet.Card("s1-3.w1").Row)
		assert.Empty(t, Check(w.s, nil))
	})

	t.Run("a wake ping her session answered: admitted", func(t *testing.T) {
		t.Parallel()
		w := readyForAmy(t)
		h := FriendHealth{State: Up, Seen: now.Add(-time.Second), Generation: 1}
		f := FriendPresence{Beat: Beat{At: now.Add(-time.Second)}, Health: h, Generation: 1}
		require.Equal(t, Up, FriendStatus(f, now))
		w.must(takeForAmy(w, FriendSeat{Name: "amy", Width: 3, Status: FriendStatus(f, now)}))
		assert.Equal(t, Working, w.s.Fleet.Card("s1-3.w1").Col)
	})

	t.Run("her width is hard: at width 2 the third is refused", func(t *testing.T) {
		t.Parallel()
		w := readyForAmy(t)
		p := takeForAmy(w, FriendSeat{Name: "amy", Width: 2, Status: Up})
		require.Len(t, p.Refused, 1)
		assert.Contains(t, p.Refused[0].Why, "at its width (2 working of 2)")
		assert.Empty(t, p.Units)
	})

	for _, tc := range []struct {
		name string
		f    FriendPresence
		want string
	}{
		{"held", FriendPresence{Held: true, Beat: Beat{At: now.Add(-time.Second)}}, "friend amy is held: held by the coordinator (friend down)"},
		{"beating, no session evidence", FriendPresence{Beat: Beat{At: now.Add(-time.Second)}}, "friend amy is down: daemon up, session deaf (never heard): no wake ping answered by her session within 10m0s, no card finished within 30m0s"},
		{"observed down", FriendPresence{Beat: Beat{At: now.Add(-time.Second)}, Health: FriendHealth{State: Down, Seen: now.Add(-time.Second), Generation: 1}, Generation: 1}, "friend amy is down: daemon up, session deaf (never heard): no wake ping answered by her session within 10m0s, no card finished within 30m0s"},
		{"her session's pong, her daemon not beating", FriendPresence{Beat: Beat{At: now.Add(-time.Minute)}, Health: FriendHealth{State: Up, Seen: now.Add(-time.Second), Generation: 1}, Generation: 1}, "friend amy is down: daemon not beating (last beat 1m0s ago), session pong 1s ago"},
		{"never beaten", FriendPresence{}, "friend amy is down: daemon never beat, session deaf (never heard): no wake ping answered by her session within 10m0s, no card finished within 30m0s"},
	} {
		t.Run(tc.name+": refused, naming it", func(t *testing.T) {
			t.Parallel()
			w := readyForAmy(t)
			st := FriendStatus(tc.f, now)
			require.NotEqual(t, Up, st)
			p := takeForAmy(w, FriendSeat{Name: "amy", Width: 3, Status: st, Why: FriendDownWhy(tc.f, now)})
			require.Len(t, p.Refused, 1)
			assert.Equal(t, tc.want, p.Refused[0].Why)
			assert.Empty(t, p.Units)
			assert.Equal(t, Ready, w.s.Fleet.Card("s1-3.w1").Col)
		})
	}

	t.Run("a friend not on the roster: refused", func(t *testing.T) {
		t.Parallel()
		w := readyForAmy(t)
		w.s.Friends = nil
		wc := w.s.Fleet.Card("s1-3.w1")
		p := Take(w.s, TakeReq{Sel: Sel{IDs: []string{wc.ID}}, As: FriendRow("amy"), Gens: map[string]int{wc.ID: wc.Int("gen")}})
		require.Len(t, p.Refused, 1)
		assert.Equal(t, "no friend amy on the roster", p.Refused[0].Why)
	})

	t.Run("a machine keeps its control card's rule", func(t *testing.T) {
		t.Parallel()
		w := readyForAmy(t)
		w.must(FleetStep(w.s, FleetReq{Op: "down", Member: "m1"}))
		w.s.Friends = []FriendSeat{{Name: "amy", Width: 3, Status: Up}}
		p := Take(w.s, TakeReq{Sel: Sel{IDs: []string{"x"}}, As: "m1"})
		require.Len(t, p.Refused, 1)
		assert.Equal(t, "member m1 is "+w.s.MemberCtl("m1").F("status"), p.Refused[0].Why)
	})
}
