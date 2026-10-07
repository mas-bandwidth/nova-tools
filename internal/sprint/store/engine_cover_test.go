package store

// The stream coverage card of 2026-10-04: no unit test reached the Error of
// engine.go:252, (*PendingError).Error, which sat at 0.0% in the unit tier's
// per-function table of go tool cover -func. The tests that hold the
// pending-operation refusal only errors.As it, so nothing printed the line the
// caller reads. These two tests are the whole reach.

import (
	"errors"
	"testing"
	"time"

	"github.com/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEngineCoverPendingErrorLines pins the line (*PendingError).Error prints:
// the operation's id, why it cannot be finished now, and the one remedy in the
// same line. The last row is the zero value, the shape a caller meets when
// neither operation nor cause was named: the line still ends in the repair.
func TestEngineCoverPendingErrorLines(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		pe   *PendingError
		want string
	}{
		{
			"a table that will not take its entries names itself",
			&PendingError{Op: "deal-1", Why: "table t-work cannot finish: cut"},
			"operation deal-1 is pending: table t-work cannot finish: cut; run: nova-sprint repair",
		},
		{
			"a store that did not answer keeps its cause",
			&PendingError{Op: "finish-3", Why: "the store did not answer: " + ErrUnknown.Error()},
			"operation finish-3 is pending: the store did not answer: the store did not confirm the write (changed=unknown); run: nova-sprint repair",
		},
		{
			"an operation with no cause named still ends in the remedy",
			&PendingError{},
			"operation  is pending: ; run: nova-sprint repair",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, c.want, c.pe.Error(), "the refusal line as the caller reads it")
			var as *PendingError
			assert.ErrorAs(t, error(c.pe), &as, "a verb returns it as an error")
		})
	}
}

// TestEngineCoverAVerbPastTheGraceRefusesWithThePendingLine pins the refusal as
// a caller meets it: a step cut between two of its tables leaves its operation
// in the fence, and past the grace a mutating verb cannot read the sprint, so
// it refuses with a *PendingError whose line names the fenced operation and
// ends in nova-sprint repair. The store is the in-memory one with its own seam
// failing one apply point, and the clock is the harness's: no sleep, no live
// store, no subprocess.
func TestEngineCoverAVerbPastTheGraceRefusesWithThePendingLine(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(2)
	h.m.Fail = func(point string) error {
		if point == "apply t-work before" {
			return errors.New("cut")
		}
		return nil
	}
	step := DealStep(sprint.DealReq{Sel: ids("s1-1")})
	step.CallerOp = "caller-cut"
	_, err := h.st.Run(h.ctx, step)
	require.Error(t, err, "the deal cut between its tables")
	pending := h.m.Pending()
	require.NotNil(t, pending, "the cut operation stays in the fence")

	h.tick(2 * time.Minute) // past the grace: its writer is taken as gone
	_, err = h.st.Run(h.ctx, FleetStep(sprint.FleetReq{Op: "up", Member: "m9"}))
	var pe *PendingError
	require.ErrorAs(t, err, &pe, "a verb past the grace over an operation that cannot finish: %v", err)
	assert.Equal(t, pending.ID, pe.Op, "the line names the operation held in the fence")
	assert.ErrorContains(t, err, "operation "+pending.ID+" is pending: ")
	assert.ErrorContains(t, err, "; run: nova-sprint repair")
}
