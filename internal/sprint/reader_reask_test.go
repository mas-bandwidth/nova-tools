package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
