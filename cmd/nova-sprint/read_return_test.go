package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// read --return hands a read its reader holds back, with the reason: the
// next tick asks it of another reader up at the same attempt, the inbox holds
// one happened note naming the reader, the card and the reason, and no
// judgment is owed (docs/SPEC-SPRINT.md section 6; tla/DirtyTick.tla,
// ReadReturn).
func TestReadReturnIsAskedOfAnotherReaderByTheNextTick(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b,reader-c --members m1")
	ta.ok("reader away reader-c")
	ta.inReview(1)
	ta.ok("ask")
	ta.ok("reader up reader-c")
	ta.ok("read --as reader-a --begin s1-1.r1.reader-a")
	code, _, errs := ta.do("read --as reader-a --return s1-1.r1.reader-a")
	assert.NotEqual(t, 0, code, "a return wants its reason")
	assert.Contains(t, errs, "--reason")
	code, _, _ = ta.do("read --as reader-c --return s1-1.r1.reader-a --reason 'not mine'")
	assert.Equal(t, 1, code, "a return of a read the caller does not hold is refused")
	ta.ok("read --as reader-a --return s1-1.r1.reader-a --reason 'STAGE FAIL no bench mirror' --actor coordinator")
	story := ta.ok("card s1-1")
	assert.Contains(t, story, "taken back from reader-a by reader-a", "the returner is the reader, whoever ran the verb")
	assert.NotContains(t, story, "reason by", "the reason is the inbox note's, not the card's")
	assert.Empty(t, ta.askedOf("reader-a"), "the reader holds nothing")
	ta.ok("start")
	ta.ok("tick")
	assert.Equal(t, []string{"s1-1.r1.reader-c"}, ta.askedOf("reader-c"), "asked of another reader up at the same attempt")
	assert.Equal(t, []string{"s1-1.r1.reader-b"}, ta.askedOf("reader-b"))
	var returned []sprint.Group
	for _, g := range ta.inboxGroups() {
		assert.False(t, g.Kind == sprint.Judgment, "no judgment is owed: %+v", g)
		if g.Type == sprint.NReadReturned {
			returned = append(returned, g)
		}
	}
	require.Len(t, returned, 1)
	assert.Equal(t, sprint.Happened, returned[0].Kind)
	assert.Contains(t, returned[0].What, "reader-a returned s1-1.r1.reader-a: STAGE FAIL no bench mirror")
}
