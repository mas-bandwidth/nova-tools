package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// log --card prints the card's own lines: a set move the card is in (an add of
// three, a deal of many) is its line with the set's size, never the first card's
// words with the others listed (the comfort list of 2026-10-03, item 9); the full
// log and --json keep the set line whole.
func TestLogCardPrintsASetMoveAsTheCardsOwnLine(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 3")

	out := ta.ok("log --card s1-2")
	assert.Contains(t, out, "  s1-2 added to s1 by coordinator (in a set of 3)")
	assert.NotContains(t, out, "with s1-2")
	assert.NotContains(t, out, "s1-3")

	out = ta.ok("log")
	assert.Contains(t, out, "3 cards: s1-1 added to s1 by coordinator (with s1-2, s1-3)")

	var j struct {
		Lines []struct{ Cards []string } `json:"lines"`
	}
	ta.json("log --card s1-2", &j)
	if assert.Len(t, j.Lines, 1) {
		assert.Equal(t, []string{"s1-1", "s1-2", "s1-3"}, j.Lines[0].Cards)
	}
}
