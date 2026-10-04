package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// A read taken back with no verdict is asked again of the same reader on a new
// read card (sprint.ReadCardIDAt), through the store: the create is at an id the
// store has never held, and the tick reads the retired cards of every ask
// (tickExtras) so each ask is the next one. docs/SPEC-SPRINT.md,
// read-asked-again-after-takebackc.w1.
func TestReadTakenBackIsAskedAgainOnANewCardThroughTheStore(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.startMachine()
	h.machine()
	h.work("m1")
	h.machine()
	readers := []string{"reader-a", "reader-b", "reader-c"}
	held := func() []*sprint.Card {
		t.Helper()
		var held []*sprint.Card
		for _, c := range h.snap().Readers.Of("s1-1") {
			if c.Placed() {
				held = append(held, c)
			}
		}
		require.Len(t, held, 2, "a pro card has two reads")
		sprint.SortCards(held)
		return held
	}
	seen := map[string]bool{}
	for round := 0; round < 5; round++ {
		rc := held()[0]
		for _, c := range held() {
			seen[c.ID] = true
		}
		// the reader holding the read goes away, the others are up: the read is
		// taken back and asked of a reader up (never asked first); the reader is
		// up again after
		for _, r := range readers {
			require.NoError(t, h.st.SetReaderAway(h.ctx, r, r == rc.Row, "coordinator"))
		}
		h.machine()
		require.NoError(t, h.st.SetReaderAway(h.ctx, rc.Row, false, "coordinator"))
		after, err := h.st.Load(h.ctx, All, tickExtras)
		require.NoError(t, err)
		old := after.Readers.Card(rc.ID)
		require.NotNil(t, old, "the retired card is read with the tick's extras")
		assert.False(t, old.Placed(), "round %d: the read taken back stays retired", round)
		assert.Equal(t, "away", old.F("retired_by"))
	}
	for _, c := range held() {
		seen[c.ID] = true
	}
	// five reads taken back among three readers: some reader was asked a second
	// time, on the card of its second ask, and none a fourth
	second, third := 0, 0
	for id := range seen {
		primary, attempt, reader, ok := sprint.ParseReadCard(id)
		require.True(t, ok, id)
		assert.NotEqual(t, sprint.ReadCardIDAt(primary, attempt, reader, 4), id)
		if id == sprint.ReadCardIDAt(primary, attempt, reader, 2) {
			second++
		}
		if id == sprint.ReadCardIDAt(primary, attempt, reader, 3) {
			third++
		}
	}
	assert.Positive(t, third, "a read taken back twice is asked a third time, on the third card: %v", seen)
	assert.Positive(t, second, "a reader whose read was taken back is asked again: %v", seen)
}
