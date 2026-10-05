package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// reader retire takes a reader off the table and keeps its history (the comfort
// list of 2026-10-03, item 6: reader remove refuses a reader that holds read
// history): the retired reader is never asked, its queue writes no beat and
// answers reader false, and its row and its ok read stay, the history, counted
// on the card; reader up brings it back; reader remove stays as it is.
func TestReaderRetireKeepsTheHistoryAndTakesTheRowOff(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b,reader-c --members m1")
	ta.inReview(1)
	ta.ok("ask s1-1")
	require.Equal(t, []string{"s1-1.r1.reader-a"}, ta.askedOf("reader-a"), "a pro card's first read is asked alone")
	ta.ok("read --as reader-a --ok s1-1.r1.reader-a --finding 'fine'")
	ta.ok("ask s1-1") // its second read, once the first came back ok
	require.Equal(t, []string{"s1-1.r1.reader-b"}, ta.askedOf("reader-b"), "the second read, of another reader")

	code, _, errs := ta.do("reader remove reader-a")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "reader-a holds s1-1.r1.reader-a (ok)")

	assert.Contains(t, ta.ok("reader retire reader-a"), "READER-RETIRE OK readers=reader-a")
	assert.Equal(t, sprint.ReaderRetired, ta.readerState("reader-a"))
	assert.Contains(t, ta.readerRows(), "reader-a", "the row stays, with its history (where reads no reader state: its round trips are bounded)")
	var q struct {
		Reader bool
		Cards  []queueCard
	}
	ta.json("queue --as reader-a", &q)
	assert.False(t, q.Reader, "a retired reader's queue answers reader false")
	var card struct {
		Reads []*sprint.Card `json:"read_cards"`
	}
	ta.json("card s1-1", &card)
	assert.Len(t, card.Reads, 2, "the history stays: %+v", card.Reads)
	code, _, errs = ta.do("reader retire reader-x")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "no reader reader-x on the readers table")

	// the second read is asked of a reader not retired: reader-b holds it, and
	// --another goes to reader-c, never to reader-a
	ta.ok("ask s1-1 --another")
	assert.Empty(t, ta.askedOf("reader-a"), "a retired reader is never asked")
	assert.NotEmpty(t, ta.askedOf("reader-c"))

	ta.ok("reader up reader-a")
	assert.Equal(t, sprint.ReaderUp, ta.readerState("reader-a"))
}

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

// reader retire's help says what happens to a read the reader is reading, and
// the next tick does that (internal/sprint/readers.go sweepReads: a reader
// that is not up, and retired is not up, has a read asked or reading taken
// back when a reader up has no card at that attempt).
func TestReaderRetireHelpSaysWhatHappensToAReadInReading(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	code, out, errs := ta.do("reader retire -h")
	require.Equal(t, 0, code, errs)
	const sentence = "a read it is reading is taken back at the next tick and asked of a reader up with no card at that attempt, and it stays when none can take it"
	assert.Contains(t, out, sentence)

	ta.ok("init --readers reader-a,reader-b,reader-c --members m1")
	ta.inReview(1)
	ta.ok("ask")
	var reading string
	for _, r := range []string{"reader-a", "reader-b"} {
		if len(ta.askedOf(r)) == 1 {
			reading = r
			break
		}
	}
	require.NotEmpty(t, reading, "a pro card is asked of a reader")
	ta.ok("read --as " + reading + " --begin --limit 5")
	ta.ok("reader retire " + reading)
	for _, r := range []string{"reader-a", "reader-b", "reader-c"} {
		if r != reading {
			_ = ta.askedOf(r)
		}
	}
	ta.ok("start")
	ta.ok("tick")
	assert.Empty(t, ta.askedOf(reading), "the read in reading was taken back")
	again := append(ta.askedOf("reader-b"), ta.askedOf("reader-c")...)
	require.Len(t, again, 1, "asked of one reader up with no card at that attempt")
	assert.NotContains(t, again[0], reading)
}
