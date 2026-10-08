package friend

import (
	"context"
	"fmt"
	"testing"
	"testing/synctest"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A one-shot restart compacts status, not a request no explicit native session has read.
func TestOneShotPresentRetainsRequestUntilExactNativeSessionAcceptsIt(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		from    string
		lostACK bool
	}{{"ada", false}, {"bob", false}, {"ada", true}} {
		t.Run(fmt.Sprintf("%s/lostACK=%v", row.from, row.lostACK), func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h := &lanesHarness{dir: cardDirFixture(t, nil, nil, nil), finish: map[string]bool{}, active: map[string]int{}}
				r, _ := laneRig(t, h, 1)
				r.d.noPresent = false
				request, err := r.bus.Send(context.Background(), bus.Message{From: row.from, To: []string{"bob"}, Kind: bus.KindRequest, Subject: "review this", Body: "an ordinary request waiting for its own native session"})
				require.NoError(t, err)
				r.d.m = Start(t0)
				for range 2 { // the first present and another daemon restart before any OpenSession
					l := &loop{d: r.d, b: r.bus, ctx: context.Background(), presentDue: true,
						inHand: map[string]bool{}, answered: map[string]bool{}, failed: map[string]int{}}
					l.startPresent(t0, false)
					pending, fresh := r.pending(t)
					require.Len(t, pending, 1, "unread request must stay pending across restart")
					assert.Equal(t, request.ID, pending[0].Message().ID)
					assert.Empty(t, fresh)
					stages, _, err := r.bus.Stages(context.Background(), "bob", request.ID)
					require.NoError(t, err)
					require.Len(t, stages, 1)
					assert.Equal(t, bus.Delivered, stages[0].State, "present never stamps an unread request read/acted")
					require.Len(t, l.hand, 1, "the current daemon can deliver without waiting for claim expiry")
					assert.True(t, l.inHand[pending[0].Entry])
				}
				turns, _, seeds := h.got()
				assert.Empty(t, turns)
				assert.Empty(t, seeds, "present has opened no native session")
				lost := &lostAck{Fake: r.store}
				if row.lostACK {
					lost.lose = 100
					r.d.Store = lost
				}
				r.run(t, 8)
				turns, texts, seeds := h.got()
				require.Equal(t, []string{"ses_1: -"}, turns)
				require.Len(t, seeds, 1, "OpenSession creates the exact target of DeliverTo")
				require.Len(t, texts, 1)
				assert.Contains(t, texts[0], request.ID)
				assert.Contains(t, texts[0], request.Body)
				pending, fresh := r.pending(t)
				if row.lostACK {
					require.Len(t, pending, 1, "native acceptance precedes the lost ACK")
					assert.Equal(t, request.ID, pending[0].Message().ID)
				} else {
					assert.Empty(t, pending, "ACK follows accepted native turn")
				}
				assert.Empty(t, fresh)
				stages, _, err := r.bus.Stages(context.Background(), "bob", request.ID)
				require.NoError(t, err)
				require.Len(t, stages, 1)
				assert.Equal(t, bus.Acted, stages[0].State)
				lost.lose = 0
				r.run(t, r.beats+8) // even a lost ACK must not accept the request twice
				turns, _, _ = h.got()
				assert.Equal(t, []string{"ses_1: -"}, turns)
				pending, fresh = r.pending(t)
				assert.Empty(t, pending)
				assert.Empty(t, fresh)
			})
		})
	}
}

func TestOneShotPresentKeepsRequestCompactionExceptions(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name, subject, body string
		acted               bool
	}{
		{"acted request", "review this", "already accepted", true},
		{"deal", "card old.w1 dealt: old", "obsolete assignment", false},
		{"expired ping", "PING old", PingText("ada", t0.Add(-StaleAfter), "old"), false},
		{"session check", SessionCheckPrefix + " old", "obsolete check", false},
		{"present control", PresentSubject, "present", false},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			r := presentRig(t)
			m, err := r.bus.Send(context.Background(), bus.Message{From: "bob", To: []string{"bob"}, Kind: bus.KindRequest, Subject: row.subject, Body: row.body})
			require.NoError(t, err)
			if row.acted {
				_, ok, err := r.bus.Recv(context.Background(), "bob", 0)
				require.NoError(t, err)
				require.True(t, ok)
				_, err = r.bus.Stamp(context.Background(), "bob", bus.Acted, m.ID)
				require.NoError(t, err)
			}
			if row.name == "expired ping" {
				r.store.Advance(StaleAfter)
			}
			r.d.m = Start(t0)
			l := &loop{d: r.d, b: r.bus, ctx: context.Background(), presentDue: true,
				inHand: map[string]bool{}, answered: map[string]bool{}, failed: map[string]int{}}
			l.startPresent(t0, false)
			pending, fresh := r.pending(t)
			assert.Empty(t, pending)
			assert.Empty(t, fresh)
			assert.Empty(t, l.hand)
			assert.Empty(t, r.delivered)
		})
	}
}

func TestBatchPresentStillCompactsOlderRequestsAndCarriesNewestSeatNote(t *testing.T) {
	t.Parallel()
	r := presentRig(t)
	for _, subject := range []string{"older request", "newest request"} {
		_, err := r.bus.Send(context.Background(), bus.Message{From: "ada", To: []string{"bob"}, Kind: bus.KindRequest, Subject: subject, Body: subject})
		require.NoError(t, err)
	}
	r.run(t, 6)
	require.Len(t, r.delivered, 1)
	assert.Contains(t, r.delivered[0], "newest request")
	assert.NotContains(t, r.delivered[0], "older request")
	r.streamEmpty(t)
}

func TestOneShotPresentDoesNotCopyARequestOwnedByALaneTurn(t *testing.T) {
	t.Parallel()
	for _, deferred := range []bool{false, true} {
		t.Run(fmt.Sprint(deferred), func(t *testing.T) {
			t.Parallel()
			r := presentRig(t)
			m, err := r.bus.Send(context.Background(), bus.Message{From: "ada", To: []string{"bob"}, Kind: bus.KindRequest, Subject: "review", Body: "already owned by a native turn"})
			require.NoError(t, err)
			e, ok, err := r.bus.Recv(context.Background(), "bob", 0)
			require.NoError(t, err)
			require.True(t, ok)
			tn := &turn{entries: []string{e.Entry}, msgs: []bus.Message{m}, running: !deferred}
			r.d.m = Start(t0)
			l := &loop{d: r.d, b: r.bus, ctx: context.Background(), presentDue: true,
				inHand: map[string]bool{e.Entry: true}, answered: map[string]bool{}, failed: map[string]int{},
				lanes: &laneSet{lanes: []*lane{{n: 1, session: "ses_1", t: tn}}}}
			if deferred {
				l.lanes.lanes[0].t, l.owedMessage = nil, tn
			}
			l.startPresent(t0, false)
			assert.Empty(t, l.hand, "the retained request must not acquire a second turn owner")
			assert.True(t, l.inHand[e.Entry], "the existing turn retains claim ownership")
			pending, fresh := r.pending(t)
			require.Len(t, pending, 1)
			assert.Equal(t, m.ID, pending[0].Message().ID)
			assert.Empty(t, fresh)
		})
	}
}

func TestOneShotPresentRebuildsOnlyUnreadMixedRequestTurns(t *testing.T) {
	t.Parallel()
	for _, running := range []bool{false, true} {
		t.Run(fmt.Sprint(running), func(t *testing.T) {
			t.Parallel()
			r := presentRig(t)
			request, err := r.bus.Send(context.Background(), bus.Message{From: "ada", To: []string{"bob"}, Kind: bus.KindRequest, Subject: "review", Body: "ordinary request"})
			require.NoError(t, err)
			r.send(t, "ada", "old status", "obsolete status")
			var entries []string
			var msgs []bus.Message
			for range 2 {
				e, ok, err := r.bus.Recv(context.Background(), "bob", 0)
				require.NoError(t, err)
				require.True(t, ok)
				entries, msgs = append(entries, e.Entry), append(msgs, e.Message())
			}
			oldNotice, newNotice := &Push{Text: "old notice"}, &Push{Text: "newer notice"}
			tn := &turn{entries: entries, msgs: msgs, text: "ordinary request and obsolete status", running: running, notice: oldNotice}
			r.d.m = Start(t0)
			l := &loop{d: r.d, b: r.bus, ctx: context.Background(), presentDue: true,
				inHand: map[string]bool{}, answered: map[string]bool{}, failed: map[string]int{},
				notice: newNotice, messageRetry: t0.Add(RecheckEvery), messageSaid: "deferred", messageTries: 1,
				lanes: &laneSet{lanes: []*lane{{n: 1, session: "ses_1"}}}}
			if running {
				l.lanes.lanes[0].t = tn
			} else {
				l.owedMessage = tn
			}
			l.startPresent(t0, false)
			pending, fresh := r.pending(t)
			require.Len(t, pending, 1)
			assert.Equal(t, request.ID, pending[0].Message().ID)
			assert.Empty(t, fresh)
			assert.True(t, l.inHand[entries[0]])
			assert.Same(t, newNotice, l.notice)
			if running {
				assert.Empty(t, l.hand, "already native turn remains sole request owner")
				assert.Same(t, tn, l.lanes.lanes[0].t)
				assert.Equal(t, "ordinary request and obsolete status", tn.text, "active native text is untouched")
			} else {
				assert.Nil(t, l.owedMessage, "superseded text cannot remain as a deferred retry")
				require.Len(t, l.hand, 1)
				assert.Equal(t, request.ID, l.hand[0].Message().ID)
				assert.Zero(t, l.messageRetry)
				assert.Empty(t, l.messageSaid)
				assert.Zero(t, l.messageTries)
				_, rebuilt := l.take()
				text := MessageText("bob", 1, 1, "", "", newNotice.Text, "ada", rebuilt)
				assert.Contains(t, text, request.ID)
				assert.NotContains(t, text, "obsolete status")
			}
		})
	}
}
