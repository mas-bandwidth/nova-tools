package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// The coordinator summary names the ETA's window and sample and the cost headlines'
// denominators. With no rate and nothing priced the words are the empty ones.
func TestTheCoordinatorSummaryCarriesTheRateWindowAndTheCostDenominators(t *testing.T) {
	t.Parallel()
	v := coordinatorView{Seat: "coordinator", ETA: coordETA{
		Rate: sprint.ETABasis{Window: sprint.RateWindowNone},
		Work: sprint.ETAWork{Left: 2, Queued: 2},
	}, Cost: coordCost{Total: "-", PerLanded: "-", SpendPerLanded: "-"}}
	sum := coordinatorSum(v, true, store.Machine{State: store.Running})
	assert.True(t, strings.HasPrefix(sum, "seat=coordinator machine=running"), sum)
	assert.Contains(t, sum, "rate none")
	assert.Contains(t, sum, "left 2: held 0 executing 0 queued 2")
	assert.Contains(t, sum, "per landed - of 0 priced of 0 landed")
	assert.Contains(t, sum, "spend per landed -")
}
