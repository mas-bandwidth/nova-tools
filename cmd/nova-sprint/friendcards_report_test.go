package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A friend's report whose Verdict: and Head: lines are not the first two is
// read all the same (the reader stays lenient), and the card gains a NOTE
// naming the line numbers, so the shape is learned rather than a card lost.
func TestAFriendReportWhoseFirstTwoLinesAreNotPinnedIsReadAllTheSame(t *testing.T) {
	t.Parallel()
	sha := strings.Repeat("a", 40)
	pinned := "Verdict: LAND\nHead: " + sha + "\n\nthe body says what changed\n"
	later := "# Notes\n\nVerdict: LAND\nHead: " + sha + "\n\nthe body says what changed\n"

	v1, h1, p1 := friendReportOf(pinned)
	v2, h2, p2 := friendReportOf(later)
	require.Equal(t, VerdictLand, v1, "the pinned report's verdict")
	assert.Equal(t, v1, v2, "the verdict is read the same wherever the line sits")
	assert.Equal(t, h1, h2, "the head is read the same")
	assert.Equal(t, p1, p2, "the paragraph is read the same")
	require.NotEmpty(t, p1)

	assert.Empty(t, friendReportPinNote(pinned), "a pinned report needs no note")
	note := friendReportPinNote(later)
	assert.Contains(t, note, "Verdict: is on line 3", "the note names the Verdict line: %s", note)
	assert.Contains(t, note, "Head: is on line 4", "the note names the Head line: %s", note)

	// a HOLD may leave Head: out; line 2 blank is pinned
	hold := "Verdict: HOLD\n\n\nthe body says why\n"
	assert.Empty(t, friendReportPinNote(hold), "a HOLD with a blank line 2 is pinned")
}
