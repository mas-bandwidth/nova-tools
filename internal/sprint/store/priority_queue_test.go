package store

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/require"
)

// The priority unit writes the Work operation queue and its consumer copies
// together. The next admission sees that queued producer update before a pump.
func TestPriorityQueuedProducerAndConsumerAgreeBeforePump(t *testing.T) {
	t.Parallel()
	for _, dealFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "priority then create", true: "create then priority"}[dealFirst], func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.setup(1)
			h.startMachine()
			deal := func() { h.must(DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}})) }
			if dealFirst { deal() }
			meter := h.st.meter()
			res := h.must(PriorityStep(sprint.PriorityReq{IDs: []string{"s1-1"}, Level: sprint.PriorityBlocker, Reason: "next slot"}))
			cost := meter.part(sprint.Work, "priority")
			t.Logf("priority before pump: created=%t logical_loads=%d records=%d work_updates=%d fleet_updates=%d", dealFirst, cost.Reads, cost.Rows, res.Tables[sprint.Work], res.Tables[sprint.Fleet])
			require.EqualValues(t, 1, cost.Reads, "one logical table-set load, never a load per consumer")
			require.Equal(t, 1, res.Tables[sprint.Work])
			if dealFirst { require.Equal(t, 1, res.Tables[sprint.Fleet]) } else { deal() }
			queued, err := h.m.QueueRead(h.ctx)
			require.NoError(t, err)
			require.NotEmpty(t, queued, "RUNNING retains the primary update in the operation queue")
			s := h.snap()
			pr := s.Work.Card("s1-1")
			wc := s.Fleet.Card(pr.F("work"))
			require.NotNil(t, wc)
			require.Equal(t, sprint.PriorityBlocker, pr.F(sprint.FieldPriority))
			require.Equal(t, sprint.PriorityBlocker, sprint.QueuePriority(wc))
			h.must(TakeStep(sprint.TakeReq{As: wc.Row, Sel: sprint.Sel{Limit: 1}}))
			require.Equal(t, sprint.Working, h.snap().Fleet.Card(wc.ID).Col)
		})
	}
}
