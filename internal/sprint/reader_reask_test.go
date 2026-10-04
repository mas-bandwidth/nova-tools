package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAReadTakenBackByTheAwaySweepCanBeAskedAgainAtTheSameAttempt pins that
// when a reader's read card is taken back by sweepReads (retired_by away),
// and the reader comes back up, the tick can ask that reader again at the
// same attempt using a second identity (e.g. generation suffix), while reads
// retired for other reasons are not re-asked.
func TestAReadTakenBackByTheAwaySweepCanBeAskedAgainAtTheSameAttempt(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	// Add extra reader rows so we have reader-a, reader-b, reader-c, reader-d.
	w.s.Readers.SetRows([]string{"reader-a", "reader-b", "reader-c", "reader-d"})
	w.s.ReaderStates = map[string]string{
		"reader-a": ReaderUp,
		"reader-b": ReaderUp,
		"reader-c": ReaderUp,
		"reader-d": ReaderUp,
	}
	toReview(w, "s1-1")
	// s1-1 is a pro card (tier 2): needs 2 readers.
	w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	firstReads := readsAt(w.s, w.s.Work.Card("s1-1"), 1)
	require.Len(t, firstReads, 2)
	r1 := firstReads[0].F("reader")
	r2 := firstReads[1].F("reader")

	// Set r1 away so sweepReads takes its read back.
	w.s.ReaderStates[r1] = ReaderAway
	var sweepPlan Plan
	sweepReads(w.s, &sweepPlan)
	require.NotEmpty(t, sweepPlan.Units, "sweepReads takes back r1's read")
	w.must(sweepPlan)

	oldCard := w.s.Readers.Card(ReadCardID("s1-1", 1, r1))
	require.NotNil(t, oldCard)
	assert.False(t, oldCard.Placed())
	assert.Equal(t, "away", oldCard.F("retired_by"))

	// Also simulate another reader's card retired for a different reason (e.g. RetiredByLevel).
	otherReader := "reader-d"
	for _, rd := range []string{"reader-a", "reader-b", "reader-c", "reader-d"} {
		if rd != r1 && rd != r2 {
			otherReader = rd
			break
		}
	}
	levelCardID := ReadCardID("s1-1", 1, otherReader)
	w.s.Readers.Put(&Card{
		ID:     levelCardID,
		Rev:    1,
		Row:    otherReader,
		Col:    "",
		Fields: map[string]string{"kind": "read", "primary": "s1-1", "attempt": "1", "reader": otherReader, "stream": "s1", "retired": stamp(w.s.Now), "retired_by": RetiredByLevel},
	})

	// Bring r1 back up. Keep only r1 and otherReader up to prove r1 is chosen and otherReader is NOT re-asked.
	for _, rd := range w.s.Readers.Rows() {
		w.s.ReaderStates[rd] = ReaderAway
	}
	w.s.ReaderStates[r1] = ReaderUp
	w.s.ReaderStates[r2] = ReaderUp          // already has live read
	w.s.ReaderStates[otherReader] = ReaderUp // retired by level, must NOT be re-asked

	// TickAsk asks for the missing read of s1-1 at attempt 1.
	plan, due := TickAsk(w.s, TickReq{})
	assert.Zero(t, due)
	require.Len(t, plan.Units, 1, "s1-1 must be asked again of r1")
	w.must(plan)

	// Check that r1 has a new read card placed with the second identity.
	newReads := readsAt(w.s, w.s.Work.Card("s1-1"), 1)
	require.Len(t, newReads, 2)

	var reaskedCard *Card
	for _, c := range newReads {
		if c.F("reader") == r1 {
			reaskedCard = c
			break
		}
	}
	require.NotNil(t, reaskedCard, "r1 must be asked again")
	assert.NotEqual(t, ReadCardID("s1-1", 1, r1), reaskedCard.ID, "must have second identity, not plain identity")
	assert.True(t, reaskedCard.Placed(), "reasked card must be placed")

	// otherReader must NOT have been asked.
	for _, c := range newReads {
		assert.NotEqual(t, otherReader, c.F("reader"), "otherReader retired by level must not be re-asked")
	}
}
