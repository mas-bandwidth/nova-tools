package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// rank --before <id> puts the cards in line in front of a primary of their own
// stream, placed as add --before places cards, so two cards of one stream are
// ordered in one step (the comfort list of 2026-10-03, item 4); a card of
// another stream, the anchor itself, or no primary refuses the whole step.
func TestRankBeforePlacesCardsInFrontOfAPrimary(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream a --count 4")
	ta.ok("add --one --stream b --count 1")

	out := ta.ok("rank a-4 a-3 --before a-2")
	assert.Contains(t, out, "MOVED a-4 score 4 -> ")
	assert.Contains(t, out, "MOVED a-3 score 3 -> ")
	s1, s2, s3, s4 := ta.primary("a-1").Score, ta.primary("a-2").Score, ta.primary("a-3").Score, ta.primary("a-4").Score
	assert.True(t, s1 < s4 && s4 < s3 && s3 < s2, "the line: a-1 %v, a-4 %v, a-3 %v, a-2 %v", s1, s4, s3, s2)

	for line, want := range map[string]string{
		"rank b-1 --before a-2":         "b-1 is of stream b and a-2 of stream a: --before orders the cards of one stream",
		"rank a-2 --before a-2":         "a-2 is the card --before names",
		"rank a-1 --before a-9":         "--before a-9 is no primary on the table",
		"rank a-1 --before a-2 --first": "wants ids and one of --score <n>, --first, --before <id>",
	} {
		code, _, errs := ta.do(line)
		assert.NotEqual(t, 0, code, line)
		assert.Contains(t, errs, want, line)
	}
	assert.Equal(t, s1, ta.primary("a-1").Score, "a refused rank wrote")
}
