package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAReadTakenBackByTheAwaySweepCanBeAskedAgainAtTheSameAttempt pins that
// when a reader's read card is taken back by sweepReads (retired_by away),
// and the reader comes back up, the reader is eligible to be asked again at the
// same attempt using a second identity (generation suffix .g1), while reads
// retired for other reasons (e.g. level) or already active are not re-asked.
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

	// Bring r1 back up.
	w.s.ReaderStates[r1] = ReaderUp

	// ReadCardForAsk determines that r1 is eligible to be re-asked at the same attempt
	// and returns its second identity (.g1) because its previous read was retired by away.
	reaskID, ok := ReadCardForAsk(w.s, "s1-1", 1, r1)
	require.True(t, ok, "r1 must be eligible for re-ask after returning from away")
	assert.Equal(t, ReadCardSecondID("s1-1", 1, r1), reaskID, "re-asked card must have second identity (.g1)")
	assert.Equal(t, ReadCardID("s1-1", 1, r1)+".g1", reaskID)

	// otherReader retired by level must NOT be eligible to be re-asked at the same attempt.
	otherID, otherOK := ReadCardForAsk(w.s, "s1-1", 1, otherReader)
	assert.False(t, otherOK, "otherReader retired by level must not be re-asked at the same attempt")
	assert.Equal(t, ReadCardID("s1-1", 1, otherReader), otherID)

	// r2 already has a live read at this attempt and must not be asked again.
	r2ID, r2OK := ReadCardForAsk(w.s, "s1-1", 1, r2)
	assert.False(t, r2OK, "r2 already has a live read card")
	assert.Equal(t, ReadCardID("s1-1", 1, r2), r2ID)

	// Place the second card for r1, as Ask would when wired.
	w.s.Readers.Put(&Card{
		ID:     reaskID,
		Rev:    1,
		Row:    r1,
		Col:    Asked,
		Fields: map[string]string{"kind": "read", "primary": "s1-1", "stream": "s1", "reader": r1, "attempt": "1", "asked": stamp(w.s.Now)},
	})

	// Once the second identity card exists, r1 is no longer eligible for further re-ask at this attempt.
	_, reaskAgainOK := ReadCardForAsk(w.s, "s1-1", 1, r1)
	assert.False(t, reaskAgainOK, "r1 cannot be asked a third time at the same attempt")
}

// TestReadCardSecondIDAndGenerationSuffix pins that ReadCardSecondID and ReadCardIDs
// generate the correct second identity (.g1) and identity pairs.
func TestReadCardSecondIDAndGenerationSuffix(t *testing.T) {
	t.Parallel()
	plain := ReadCardID("s1-1", 1, "reader-a")
	assert.Equal(t, "s1-1.r1.reader-a", plain)

	second := ReadCardSecondID("s1-1", 1, "reader-a")
	assert.Equal(t, "s1-1.r1.reader-a.g1", second)

	ids := ReadCardIDs("s1-1", 1, "reader-a")
	require.Equal(t, []string{"s1-1.r1.reader-a", "s1-1.r1.reader-a.g1"}, ids)
}

// TestValidCardIDWithGenerationSuffix pins that ValidCardID accepts read cards
// with valid generation suffixes (.g1, .g2) and rejects malformed or non-read cards.
func TestValidCardIDWithGenerationSuffix(t *testing.T) {
	t.Parallel()
	// Valid read cards with generation suffixes
	assert.True(t, ValidCardID("s1-1.r1.reader-a.g1"))
	assert.True(t, ValidCardID("s1-1.r1.reader-a.g2"))
	assert.True(t, ValidCardID("s1-1.r2.reader-b.g10"))

	// Valid 3-part cards
	assert.True(t, ValidCardID("s1-1.r1.reader-a"))
	assert.True(t, ValidCardID("s1-1.w1"))
	assert.True(t, ValidCardID("s1-1"))

	// Invalid generation suffixes or card structures
	assert.False(t, ValidCardID("s1-1.r1.reader-a.g0"), "g0 is invalid (generation starts at 1)")
	assert.False(t, ValidCardID("s1-1.r1.reader-a.g"), "bare g is invalid")
	assert.False(t, ValidCardID("s1-1.w1.reader-a.g1"), "non-read card cannot have 4 parts with generation suffix")
	assert.False(t, ValidCardID("a.b.c.d"), "arbitrary 4 parts is invalid")
	assert.False(t, ValidCardID("s1-1.r1.reader-a.g1.extra"), "5 parts is invalid")
	assert.False(t, ValidCardID(""))
}

// TestParseReadCardWithGenerationSuffix pins that ParseReadCard extracts primary,
// attempt, and reader from both plain and generation-suffixed read card identities.
func TestParseReadCardWithGenerationSuffix(t *testing.T) {
	t.Parallel()
	// Plain read card
	p, a, r, ok := ParseReadCard("s1-1.r1.reader-a")
	assert.True(t, ok)
	assert.Equal(t, "s1-1", p)
	assert.Equal(t, 1, a)
	assert.Equal(t, "reader-a", r)

	// Second identity read card with .g1
	p, a, r, ok = ParseReadCard("s1-1.r1.reader-a.g1")
	assert.True(t, ok)
	assert.Equal(t, "s1-1", p)
	assert.Equal(t, 1, a)
	assert.Equal(t, "reader-a", r)

	// Second identity read card with higher generation
	p, a, r, ok = ParseReadCard("mycard.r3.reader-z.g2")
	assert.True(t, ok)
	assert.Equal(t, "mycard", p)
	assert.Equal(t, 3, a)
	assert.Equal(t, "reader-z", r)

	// Invalid read cards
	_, _, _, ok = ParseReadCard("s1-1.r1.reader-a.g0")
	assert.False(t, ok)
	_, _, _, ok = ParseReadCard("s1-1.r1.reader-a.g")
	assert.False(t, ok)
	_, _, _, ok = ParseReadCard("s1-1.w1.g1")
	assert.False(t, ok)
	_, _, _, ok = ParseReadCard("s1-1.r0.reader-a")
	assert.False(t, ok)
}
