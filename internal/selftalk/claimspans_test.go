package selftalk

import (
	"math/rand/v2"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// claimWhole is the claim shape as one expression, the form Scan compiled until
// v1.2.2. It is kept here as the reference: claimSpans must return exactly its
// spans, without its cost (a hundred and twenty live states per input byte).
var claimWhole = regexp.MustCompile(`(?i)[^.!?]{0,` + "120" + `}\b(` + claimMarkers + `)\b[^.!?]{0,160}[.!?]`)

// The linear scan returns the whole-sentence shape's spans exactly, on texts
// built to press every edge: markers near and far from the terminator, the
// 120-rune lead cut short by a terminator or the start of the text, words that
// glue onto a marker, multi-byte runes inside the lead, and sentences longer
// than both bounds.
func TestClaimSpansAreTheWholeSentenceShapes(t *testing.T) {
	t.Parallel()

	require.Equal(t, 120, claimLead, "the reference pattern and claimLead name the same bound")
	pieces := []string{
		"I am", "I'm", "I have never", "I always", "I never", "I cannot", "I can't", "I can not",
		"I do not", "I don't", "my work is", "my is", "makes me", "I tend", "I struggle", "I fail",
		"reliably", "every time", "in one direction", "i AM", "MY X IS",
		"xI am", "I amx", "Iam", "word", "words", "fallible", "broken", "naïve", "résumé", "日本",
		"—", ",", ":", "'", "\"", "x", "_", "9", ".", "!", "?", ". ", " ", " ", " ", " ",
	}
	rng := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < 400; i++ {
		var b strings.Builder
		for n := rng.IntN(100); n > 0; n-- {
			b.WriteString(pieces[rng.IntN(len(pieces))])
			if rng.IntN(3) > 0 {
				b.WriteByte(' ')
			}
		}
		text := b.String()
		assert.Equal(t, claimWhole.FindAllStringIndex(text, -1), claimSpans(text), "spans differ on %q", text)
	}
}

// One megabyte of a single sentence, the shape that ran the whole-sentence
// pattern past CI's 75 s deadline under -race, is one claim whose span reaches
// back no further than its lead. The deadline itself is held by
// cmd/nova-self-talk's TestOneGiantTraitSentenceCannotMakeAFindingLineOrJSONItemGiant.
func TestClaimSpansOnOneGiantSentenceReachBackOnlyTheirLead(t *testing.T) {
	t.Parallel()

	giant := "I hoard " + strings.Repeat("word ", 200000) + "and I am fallible, said once."
	spans := claimSpans(giant)
	require.Len(t, spans, 1, "one sentence, one claim")
	assert.Equal(t, len(giant), spans[0][1], "the claim ends at the sentence's terminator")
	assert.LessOrEqual(t, len([]rune(giant[spans[0][0]:])), 120+len("I am")+160+1, "the claim reaches no further back than its lead")
}
