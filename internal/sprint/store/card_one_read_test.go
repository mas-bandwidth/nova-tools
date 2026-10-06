package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// `nova-sprint card` read the work table whole three times (CardOf's load, Held's load
// and the place's load) and the log once: about 100 server-ms at 2,000 cards. CardRead is
// the card's whole view from one load of the tables, and it answers what the three did.
func TestACardsViewReadsTheTablesOnceAndAnswersWhatThreeReadsDid(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.must(AddStep(sprint.AddReq{Brief: briefOf("flash", ""), Stream: "s1", Count: 3}))
	h.must(AddStep(sprint.AddReq{Brief: briefOf("flash", ""), Stream: "s1", IDs: []string{"late"}, Needs: []string{"s1-1"}}))

	// the three reads' answers, taken the old way
	wantInfo, err := h.st.CardOf(h.ctx, "late")
	require.NoError(t, err)
	wantHeld, err := h.st.Held(h.ctx, "late")
	require.NoError(t, err)
	ws, err := h.st.Load(h.ctx, []string{sprint.Work}, nil)
	require.NoError(t, err)
	wantLine := ws.Work.Column(sprint.States...)

	before := h.st.stats().reads.Load()
	got, err := h.st.CardRead(h.ctx, "late", true)
	require.NoError(t, err)
	assert.Equal(t, int64(1), h.st.stats().reads.Load()-before, "one load of the tables for the whole view")

	assert.Equal(t, wantInfo, got.CardInfo)
	require.NotNil(t, got.Held)
	assert.Equal(t, wantHeld, *got.Held)
	assert.Equal(t, wantLine, got.Line, "the work table's cards in the states, for the card's place")

	// not asking for what holds it (an earlier epoch's view) reads the work table only and holds nothing
	got, err = h.st.CardRead(h.ctx, "late", false)
	require.NoError(t, err)
	assert.Nil(t, got.Held)
	assert.Equal(t, wantInfo, got.CardInfo)

	// no such primary: no record, no error, as CardOf says it
	none, err := h.st.CardRead(h.ctx, "s1-99", true)
	require.NoError(t, err)
	assert.Nil(t, none.Primary)
}
