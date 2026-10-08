package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// A reader's standalone begin and beat renewal read the current read_lease,
// even though neither operation draws coordinator routes (SPEC-SPRINT section 11).
func TestReadLeaseSettingControlsBeginAndRenewal(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.asked1(1)
	rc := h.snap().Readers.Of("s1-1")[0]
	h.m.SetPolicy("read_lease", "30m")
	h.must(ReadStep(sprint.ReadReq{As: rc.Row, Begin: true, Sel: sprint.Sel{IDs: []string{rc.ID}}, Who: rc.Row}))
	lease := func() time.Time {
		c := h.snap().Readers.Placed(rc.ID)
		require.NotNil(t, c)
		at, err := time.Parse(time.RFC3339, c.F(sprint.FieldLease))
		require.NoError(t, err)
		return at
	}
	assert.Equal(t, h.st.Now().Add(30*time.Minute), lease())
	h.m.SetPolicy("read_lease", "1h")
	h.tick(time.Minute)
	h.must(Step{Verb: "lease", Load: []string{sprint.Readers}, Plan: func(s *sprint.Snapshot) sprint.Plan {
		return sprint.RenewReaderLeases(s, rc.Row)
	}})
	assert.Equal(t, h.st.Now().Add(time.Hour), lease())
}

// A timeout edit reaches the tick's presence move, so an already running member
// goes down on the next pass without rebuilding or restarting the machine.
func TestMemberDownSettingChangesTheNextTick(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.startMachine()
	h.machine()
	h.m.SetPolicy("member_down_after", "2m")
	h.live = nil
	h.tick(time.Minute)
	h.machine()
	assert.Equal(t, sprint.Up, h.snap().MemberCtl("m1").F("status"))
	h.m.SetPolicy("member_down_after", "15s")
	h.machine()
	assert.Equal(t, sprint.Down, h.snap().MemberCtl("m1").F("status"))
}
