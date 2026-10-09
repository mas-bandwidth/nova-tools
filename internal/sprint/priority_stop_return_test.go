package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A priority change during an active lease is visible when that consumer
// returns to its queue, without changing the lease being cancelled. This is
// the producer/returned-copy receipt replayed by tla/PriorityStopReturn.tla.
func TestStopReturnRefreshesPriorityFromTheProducer(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, table, kind, from, to, initial, origin, change, role, urgency string
	}{
		{"work promotion", Fleet, "work", Working, Ready, PriorityNormal, "", PriorityBlocker, PriorityBlocker, PriorityBlocker},
		{"work demotion", Fleet, "work", Working, Ready, PriorityHigh, "", PriorityLow, PriorityLow, PriorityLow},
		{"fix urgency", Fleet, "work", Working, Ready, PriorityFix, PriorityHigh, PriorityBlocker, PriorityFix, PriorityBlocker},
		{"friend read", Fleet, "read", Working, Ready, PriorityNormal, "", PriorityBlocker, PriorityReader, PriorityBlocker},
		{"reader table", Readers, "read", Reading, Asked, PriorityHigh, "", PriorityLow, PriorityReader, PriorityLow},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t)
			pr := &Card{ID: "producer", Row: "stream", Col: Review, Rev: 1, Fields: map[string]string{
				"kind": "primary", FieldPriority: tc.initial,
			}}
			if tc.origin != "" {
				pr.Fields[FieldProducerPriority] = tc.origin
			}
			w.s.Work.Put(pr)
			fields := map[string]string{"kind": tc.kind, PrimaryField: pr.ID, "stream": pr.Row, "gen": "1", "attempt": "2", "branch": "retained", FieldStarted: "1"}
			for k, v := range consumerPriorityFields(&Card{Fields: fields}, pr) {
				fields[k] = v
			}
			child := &Card{ID: "consumer", Row: "owner", Col: tc.from, Rev: 1, Fields: fields}
			w.s.T(tc.table).SetRows([]string{child.Row})
			w.s.T(tc.table).Put(child)
			beforeRole, beforeUrgency := QueuePriority(child), ProducerPriority(child)
			w.must(SetPriority(w.s, PriorityReq{IDs: []string{pr.ID}, Level: tc.change, Reason: "next free slot"}))
			assert.Equal(t, beforeRole, QueuePriority(w.s.T(tc.table).Card(child.ID)), "no preemption or rewrite of the active lease")
			assert.Equal(t, beforeUrgency, ProducerPriority(w.s.T(tc.table).Card(child.ID)))
			req := StopReturnReq{As: child.Row, IDs: []string{child.ID}, Gens: map[string]int{child.ID: 1}, Reason: "owned child stopped"}
			w.must(StopReturn(w.s, req))
			back := w.s.T(tc.table).Card(child.ID)
			assert.Equal(t, tc.to, back.Col)
			assert.Equal(t, tc.role, QueuePriority(back))
			assert.Equal(t, tc.urgency, ProducerPriority(back))
			assert.Equal(t, 2, back.Int("gen"))
			assert.Equal(t, "2", back.F("attempt"))
			assert.Equal(t, "retained", back.F("branch"))
			assert.Empty(t, back.F(FieldStarted))
			require.Empty(t, StopReturn(w.s, req).Units, "an ACK retry doesn't return a second generation")
			stale := req
			stale.Gens = map[string]int{child.ID: 9}
			require.NotEmpty(t, StopReturn(w.s, stale).Refused, "unrelated generation remains fenced")
		})
	}
}
