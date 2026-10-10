package store

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dieOnStatus is a backend whose fleet manifest that moves the status record to the
// transition named never answers: a tick cut between its record and its judgment.
type dieOnStatus struct {
	*Mem
	entry string
}

func (d *dieOnStatus) Apply(ctx context.Context, m ntable.BatchManifest) (ntable.Receipt, error) {
	if m.Table == "t-fleet" && strings.Contains(m.Props[sprint.PropStatusSeen], d.entry) {
		return ntable.Receipt{}, fmt.Errorf("%w: the tick was cut", ntable.ErrUnknownOutcome)
	}
	return d.Mem.Apply(ctx, m)
}

// statusSeen is the fleet table's status record now.
func (h *harness) statusSeen() string {
	h.t.Helper()
	v, _ := h.snap().Fleet.Prop(sprint.PropStatusSeen)
	return v
}

// A tick cut between the status record and the judgment leaves no half state: the record
// and the judgment are one operation, pending until it is finished whole, and once it is,
// the transition is recorded and judged once, never again (docs/SPEC-SPRINT.md section 8,
// "Status transitions").
func TestATickCutBetweenTheStatusRecordAndItsJudgmentFinishesWhole(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.startMachine()
	h.tick(time.Second)
	h.machine()
	require.Contains(t, h.statusSeen(), "m2=up,0,", "the first sight of m2")
	h.mu.Lock()
	h.live = []string{"m1"} // m2's beat stops
	h.mu.Unlock()
	h.tick(sprint.MissedBeatsDown*sprint.BeatDeadline + time.Second)
	h.machine()
	require.Empty(t, h.openOf(sprint.NStatus), "m2 down, inside the dwell")

	// the tick that counts m2's transition is cut on the wire
	h.tick(sprint.StatusDwell)
	st := *h.st
	st.B = &dieOnStatus{Mem: h.m, entry: "m2=down,1,"}
	_, err := st.Tick(h.ctx)
	require.Error(t, err, "the cut tick")
	require.NotNil(t, h.m.Pending(), "the cut tick leaves its operation pending")
	assert.Equal(t, 0, h.written(sprint.NStatus), "no judgment before the operation finishes")
	assert.NotContains(t, h.statusSeen(), "m2=down,1,", "no record before the operation finishes")

	// the repair finishes the operation whole: the record and the judgment together
	h.tick(2 * time.Minute)
	rr, err := h.st.Repair(h.ctx)
	require.NoError(t, err, "repair: %+v", rr)
	require.Len(t, rr, 1, "repair: %+v", rr)
	assert.Nil(t, h.m.Pending())
	assert.Contains(t, h.statusSeen(), "m2=down,1,", "the record moved with the operation")
	assert.Equal(t, 1, h.written(sprint.NStatus), "the judgment written with it, once: %s", rr[0].Done)
	for range 3 {
		h.tick(time.Second)
		h.machine()
	}
	assert.Equal(t, 1, h.written(sprint.NStatus), "never judged again")
	assert.Len(t, h.openOf(sprint.NStatus), 1)
}
