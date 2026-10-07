package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// reader retire --dry-run says which readers it would retire and writes nothing: the
// reader keeps its state, so a verb that writes has the --dry-run the onboarding standard asks.
func TestReaderRetireDryRunWritesNothing(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	before := ta.readerState("reader-a")
	assert.Contains(t, ta.ok("reader retire --dry-run reader-a"), "READER-RETIRE DRY-RUN readers=reader-a; nothing was changed")
	assert.Equal(t, before, ta.readerState("reader-a"), "a dry run retires no one")
	code, _, errs := ta.do("reader retire --dry-run reader-x")
	assert.Equal(t, 1, code, "a dry run still refuses a name with no row")
	assert.Contains(t, errs, "no reader reader-x")
	ta.clean()
}

// readerMembersApp is a sprint of three members, m2 and m3 readers (reader-m2, reader-m3),
// with s1-1, a pro card, worked by m1 and in review: its two read cards are m2's and m3's.
func readerMembersApp(t *testing.T) *testApp {
	t.Helper()
	ta := newTestApp(t)
	ta.live = []string{"m1", "m2", "m3"}
	ta.ok("init --readers reader-m2,reader-m3 --members m1,m2,m3")
	ta.inReview(1)
	return ta
}

// A member reader retired (reader retire) is dealt no read card from then on: the pro
// card's read goes to the other reader, and its second read, which no reader of its tier
// may now give it, is the cannot-ask judgment, never a silent wait.
func TestReaderRetireStopsTheReadCardsDealtToItsMember(t *testing.T) {
	t.Parallel()
	ta := readerMembersApp(t)
	ta.ok("reader retire reader-m3")
	assert.Equal(t, "retired", ta.readerState("reader-m3"))
	ta.cutReads()
	assert.Equal(t, []string{"s1-1.r1.m2"}, ta.askedOf("m2"), "the reader not retired is dealt its read card")
	assert.Empty(t, ta.askedOf("m3"), "the retired reader is dealt none")
	ta.ok("start")
	ta.ok("tick")
	assert.Contains(t, ta.ok("inbox"), "cannot ask", "the second read no reader may give is a judgment")
}

// reader remove refuses a member reader that holds a read card on its fleet row, naming
// the card, and writes nothing; once the read is in, the row comes off.
func TestReaderRemoveRefusesAMemberReaderHoldingAReadCard(t *testing.T) {
	t.Parallel()
	ta := readerMembersApp(t)
	ta.cutReads()
	require.Equal(t, []string{"s1-1.r1.m2"}, ta.askedOf("m2"))
	code, _, errs := ta.do("reader remove reader-m2")
	assert.Equal(t, 1, code, errs)
	assert.Contains(t, errs, "reader-m2 holds s1-1.r1.m2 (ready)")
	assert.Contains(t, errs, "nothing was changed")
	ta.ok("take --as m2 s1-1.r1.m2@1")
	ta.ok("read --as m2 --ok s1-1.r1.m2")
	assert.Contains(t, ta.ok("reader remove reader-m2"), "READER-REMOVE OK")
	ta.clean()
}
