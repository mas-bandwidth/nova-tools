package store

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A notes-only stop must not hold the twin away from its nested queue drain
// (docs/SPEC-SPRINT.md, the work pump). The functional drive can hit this at
// its final tick; a queued edit reproduces it with no scheduler or sockets.
func TestANotesOnlyStopDrainsWithoutReadingTheTablesAgain(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.st.ShareTwin(NewTwin())
	h.must(Step{Verb: "prime twin", Load: All, Plan: func(*sprint.Snapshot) sprint.Plan { return sprint.Plan{} }})
	h.startMachine()
	score := 7.0
	h.must(RankStep(sprint.RankReq{IDs: []string{"s1-1"}, Score: &score}))
	q, err := h.m.QueueRead(h.ctx)
	require.NoError(t, err)
	require.NotEmpty(t, q)
	before := h.stats().reads.Load()
	h.stopMachine()
	assert.Equal(t, before, h.stats().reads.Load(), "the notes-only step must leave the twin available for its nested drain")
	q, err = h.m.QueueRead(h.ctx)
	require.NoError(t, err)
	assert.Empty(t, q)
	assert.Equal(t, score, h.snap().Primary("s1-1").Score)
}
