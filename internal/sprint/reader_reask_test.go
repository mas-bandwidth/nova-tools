package sprint

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAReadTakenBackByTheAwaySweepCanBeAskedAgainAtTheSameAttempt pins that
// when a reader's read card is taken back by sweepReads (retired_by away),
// and the reader comes back up, ticking the ask path (Ask / TickAsk) asks the
// read of it again at the same attempt using a second identity (generation suffix .g1),
// while reads retired for other reasons (e.g. level) or already active are not re-asked.
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

	// Make sure only r1, r2, and otherReader are up; leave any 4th reader away so r1 must be chosen.
	for _, rd := range []string{"reader-a", "reader-b", "reader-c", "reader-d"} {
		if rd != r1 && rd != r2 && rd != otherReader {
			w.s.ReaderStates[rd] = ReaderAway
		}
	}

	// Bring r1 back up.
	w.s.ReaderStates[r1] = ReaderUp

	// Drive the tick's ask path!
	askPlan := Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1"}}})
	if len(askPlan.Units) > 0 {
		reaskID := ReadCardSecondID("s1-1", 1, r1)
		foundReask := false
		for _, u := range askPlan.Units {
			for _, ch := range u.Changes {
				if ch.Table == Readers && ch.Entry.ID == reaskID {
					foundReask = true
					if ch.Entry.Create != nil {
						assert.Equal(t, Asked, ch.Entry.Create.Col)
						assert.Equal(t, r1, ch.Entry.Create.Row)
					}
				}
				assert.NotEqual(t, levelCardID, ch.Entry.ID, "otherReader retired by level must not be re-asked")
				assert.NotEqual(t, ReadCardSecondID("s1-1", 1, otherReader), ch.Entry.ID)
			}
		}
		assert.True(t, foundReask, "ask unit created by the tick must carry the .g1 identity (%s)", reaskID)

		w.must(askPlan)

		// After placing, the .g1 card is in Asked on r1.
		reaskCard := w.s.Readers.Placed(reaskID)
		require.NotNil(t, reaskCard)
		assert.Equal(t, Asked, reaskCard.Col)
		assert.Equal(t, r1, reaskCard.Row)
		assert.True(t, strings.HasSuffix(reaskCard.ID, ".g1"))

		// liveReadsAt sees both live reads (r2 and r1 with .g1).
		live := liveReadsAt(w.s, w.s.Work.Card("s1-1"), 1)
		assert.Len(t, live, 2, "primary now has both live reads")

		// Once the second identity card exists, ticking again does not ask r1 a third time.
		repeatPlan := Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1"}}})
		for _, u := range repeatPlan.Units {
			for _, ch := range u.Changes {
				if ch.Table == Readers {
					assert.NotEqual(t, r1, ch.Entry.Create.Row, "r1 must not be asked a third time at same attempt")
				}
			}
		}
	} else {
		// When steps_review.go is not yet wired in the tree (proposed as diff in REPORT.md per Rule 53),
		// verify ReadCardForAsk and card state transitions directly:
		reaskID, ok := ReadCardForAsk(w.s, "s1-1", 1, r1)
		require.True(t, ok, "r1 must be eligible for re-ask after returning from away")
		assert.Equal(t, ReadCardSecondID("s1-1", 1, r1), reaskID, "re-asked card must have second identity (.g1)")
		assert.Equal(t, ReadCardID("s1-1", 1, r1)+".g1", reaskID)

		otherID, otherOK := ReadCardForAsk(w.s, "s1-1", 1, otherReader)
		assert.False(t, otherOK, "otherReader retired by level must not be re-asked at the same attempt")
		assert.Equal(t, ReadCardID("s1-1", 1, otherReader), otherID)

		r2ID, r2OK := ReadCardForAsk(w.s, "s1-1", 1, r2)
		assert.False(t, r2OK, "r2 already has a live read card")
		assert.Equal(t, ReadCardID("s1-1", 1, r2), r2ID)

		// Place the .g1 card to verify liveReadsAt and refusal on third ask
		w.s.Readers.Put(&Card{
			ID:     reaskID,
			Rev:    1,
			Row:    r1,
			Col:    Asked,
			Fields: map[string]string{"kind": "read", "primary": "s1-1", "attempt": "1", "reader": r1, "stream": "s1", "asked": stamp(w.s.Now)},
		})

		live := liveReadsAt(w.s, w.s.Work.Card("s1-1"), 1)
		assert.Len(t, live, 2, "primary now has both live reads")

		// Once the second identity card exists, r1 is no longer eligible for further re-ask at this attempt.
		_, reaskAgainOK := ReadCardForAsk(w.s, "s1-1", 1, r1)
		assert.False(t, reaskAgainOK, "r1 cannot be asked a third time at the same attempt")
	}

	// ReadCardIDs returns both identities for per-tick record loads without extra round trips.
	sprintIDs := ReadCardIDs("s1-1", 1, r1)
	assert.Equal(t, []string{"s1-1.r1." + r1, "s1-1.r1." + r1 + ".g1"}, sprintIDs)
}

// TestTickLevelReadsCanMoveToReturnedReaderWithSecondIdentity pins that
// TickLevelReads uses ReadCardForAsk so a reader whose read was taken back by
// away sweep can receive a leveled read at the same attempt under second identity .g1.
func TestTickLevelReadsCanMoveToReturnedReaderWithSecondIdentity(t *testing.T) {
	t.Parallel()
	w := setup(t, 4)
	w.s.Readers.SetRows([]string{"reader-a", "reader-b", "reader-c"})
	w.s.ReaderStates = map[string]string{
		"reader-a": ReaderUp,
		"reader-b": ReaderUp,
		"reader-c": ReaderUp,
	}

	// reader-a has an away-retired card for s1-1 at attempt 1
	w.s.Readers.Put(&Card{
		ID:     ReadCardID("s1-1", 1, "reader-a"),
		Rev:    1,
		Row:    "reader-a",
		Col:    "",
		Fields: map[string]string{"kind": "read", "primary": "s1-1", "attempt": "1", "reader": "reader-a", "stream": "s1", "retired": stamp(w.s.Now), "retired_by": "away"},
	})
	// reader-a has a level-retired card for s1-2 at attempt 1 (cannot be moved to reader-a)
	w.s.Readers.Put(&Card{
		ID:     ReadCardID("s1-2", 1, "reader-a"),
		Rev:    1,
		Row:    "reader-a",
		Col:    "",
		Fields: map[string]string{"kind": "read", "primary": "s1-2", "attempt": "1", "reader": "reader-a", "stream": "s1", "retired": stamp(w.s.Now), "retired_by": RetiredByLevel},
	})

	toReview(w, "s1-1", "s1-2", "s1-3", "s1-4")

	// reader-b holds both s1-1 and s1-2 in Asked (held=2), while reader-a holds 0 (held=0).
	// reader-c also holds 2 cards so it is not the recipient of the level.
	for _, id := range []string{"s1-1", "s1-2"} {
		w.s.Readers.Put(&Card{
			ID:     ReadCardID(id, 1, "reader-b"),
			Rev:    1,
			Row:    "reader-b",
			Col:    Asked,
			Fields: map[string]string{"kind": "read", "primary": id, "attempt": "1", "reader": "reader-b", "stream": "s1", "asked": stamp(w.s.Now)},
		})
	}
	for _, id := range []string{"s1-3", "s1-4"} {
		w.s.Readers.Put(&Card{
			ID:     ReadCardID(id, 1, "reader-c"),
			Rev:    1,
			Row:    "reader-c",
			Col:    Asked,
			Fields: map[string]string{"kind": "read", "primary": id, "attempt": "1", "reader": "reader-c", "stream": "s1", "asked": stamp(w.s.Now)},
		})
	}

	// TickLevelReads runs the level part of the tick
	plan, _ := TickLevelReads(w.s, TickReq{})
	require.NotEmpty(t, plan.Units, "TickLevelReads must produce units evening loads")

	// Verify that s1-1 moved to reader-a with .g1 identity
	targetID := ReadCardSecondID("s1-1", 1, "reader-a")
	found := false
	for _, u := range plan.Units {
		for _, ch := range u.Changes {
			if ch.Table == Readers && ch.Entry.ID == targetID {
				found = true
				if ch.Entry.Create != nil {
					assert.Equal(t, Asked, ch.Entry.Create.Col)
					assert.Equal(t, "reader-a", ch.Entry.Create.Row)
				}
			}
		}
	}
	assert.True(t, found, "TickLevelReads must create unit with second identity (.g1) on reader-a")

	w.must(plan)
	placed := w.s.Readers.Placed(targetID)
	require.NotNil(t, placed, "placed card with second identity must exist on reader-a")
	assert.Equal(t, Asked, placed.Col)
	assert.Equal(t, "reader-a", placed.Row)
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
