package main

import (
	"strconv"
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
	require.Equal(t, []string{"s1-1.r1.reader-a"}, ta.askedOf("reader-a"), "a pro card's reads are asked together")
	require.Equal(t, []string{"s1-1.r1.reader-b"}, ta.askedOf("reader-b"), "the second read, of another reader, in the same ask")
	ta.ok("read --as reader-a --ok s1-1.r1.reader-a --finding 'fine'")

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
	require.NotEmpty(t, reading, "a pro card is asked of two readers together")
	var q struct{ Cards []queueCard }
	ta.json("queue --as "+reading, &q)
	require.Len(t, q.Cards, 1)
	word := q.Cards[0].ID + "@" + strconv.Itoa(max(q.Cards[0].Gen, 1))
	ta.ok("read --as " + reading + " --begin --limit 5")
	ta.ok("stop --reason 'fixture cancellation' --until 1h")
	ta.ok("reader retire " + reading)
	for _, r := range []string{"reader-a", "reader-b", "reader-c"} {
		if r != reading {
			_ = ta.askedOf(r)
		}
	}
	code, _, errs = ta.do("start")
	require.NotEqual(t, 0, code, "an active read needs its owner's cancellation receipt before restart")
	require.Contains(t, errs, reading+":"+word)
	ta.ok("stop-return --as " + reading + " --epoch 0 " + word + " --reason 'reader child exited'")
	ta.ok("start")
	ta.ok("tick")
	assert.Empty(t, ta.askedOf(reading), "the read in reading was taken back")
	// its two reads were asked together of reader-a and reader-b: the one taken back goes to
	// reader-c, the one reader up with no card at that attempt, and the other read stays
	require.Equal(t, []string{"s1-1.r1.reader-c"}, ta.askedOf("reader-c"), "asked of one reader up with no card at that attempt")
	for _, r := range []string{"reader-a", "reader-b"} {
		if r != reading {
			assert.Equal(t, []string{"s1-1.r1." + r}, ta.askedOf(r), "the other read stays with its reader")
		}
	}
}
