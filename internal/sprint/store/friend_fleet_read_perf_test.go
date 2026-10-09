//go:build perf

package store

import (
	"fmt"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Historical fleet records scale by 100 while the visible friend cells remain
// fixed. This bounds the fetched record count, not a 100-times fleet/network
// assertion (SPEC-SPRINT-DASHBOARD, one-second serving).
func TestFriendUsageProjectionReadsOnlySelectedRecordsAt100TimesHistory(t *testing.T) {
	t.Parallel()
	for _, history := range []int{2824, 282400} {
		t.Run(fmt.Sprint(history), func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			rows := fillFriendUsage(t, h, 0)
			// Seed unrelated historical records directly: fixture construction is not
			// part of the measured read or a test of the store batch-write contract.
			h.m.mu.Lock()
			table := h.m.tables["t-fleet"]
			for i := 0; i < history; i++ {
				table.members[fmt.Sprintf("history-%d", i)] = &memMember{epoch: 0, placed: true, row: "m1", col: sprint.DoneOK, rev: 1, fields: map[string]string{sprint.FieldUsage: "input=11 output=7 actual_usd=0.125 cost=actual"}}
			}
			table.rev++
			h.m.mu.Unlock()
			obs := &friendFleetObservation{}
			h.st.B = &friendFleetProbe{Mem: h.m, observe: obs}
			started := time.Now()
			selected, err := h.st.friendFleet(h.ctx, rows)
			elapsed := time.Since(started)
			require.NoError(t, err)
			assert.Len(t, selected.Cards(), 8)
			assert.Equal(t, 1, obs.reads, "one bounded set read regardless of machine history")
			assert.Len(t, obs.ids, 8)
			var cards []*sprint.Card
			for _, col := range []string{sprint.Ready, sprint.Working, sprint.DoneOK, sprint.DoneFailed} {
				cards = append(cards, selected.Cell("friend.alpha", col)...)
			}
			assert.EqualValues(t, 72, sprint.FriendTokensFromCards(cards))
			t.Logf("history=%d selected=%d readsets=%d elapsed=%s; live Redis latency is gated separately", history, len(obs.ids), obs.reads, elapsed)
		})
	}
}
