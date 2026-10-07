package sprint

import (
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Unit coverage for the log (internal/sprint/log.go): who a line names, what
// it is about, the replay of its move lines, the grouping of a step's set
// moves, the place and stream checks, and a notification's line. All pure:
// built from lines and hand-made tables, with no store, clock, sleep or
// subprocess.

var logCoverAt = time.Date(2026, 10, 4, 3, 0, 0, 0, time.UTC)

// logCoverTable is an observed table with the rows and the cards named.
func logCoverTable(name string, rows []string, cards ...*Card) *Table {
	t := NewTable(name)
	t.SetRows(rows)
	for _, c := range cards {
		t.Put(c)
	}
	return t
}

// logCoverCard is a placed card with a generation.
func logCoverCard(id, row, col string, gen int) *Card {
	return &Card{ID: id, Row: row, Col: col, Fields: map[string]string{"gen": strconv.Itoa(gen)}}
}

// logCoverMove is a work-table move line of one card, to To and at Gen.
func logCoverMove(id, to string, gen int) Line {
	return Line{Kind: LineMove, At: logCoverAt, Table: Work, Card: id, To: to, Gen: gen}
}

func TestLogCoverNamesReadsTheCardsALineIsAbout(t *testing.T) {
	t.Parallel()
	note := Note{ID: "n~1", Primaries: []string{"p1", "p2"}, Card: "p1", Other: "q9"}
	cases := []struct {
		name string
		line Line
		want []string
	}{
		{"a set move names every card of the set", Line{Kind: LineMove, Cards: []string{"a", "b"}, Card: "a"}, []string{"a", "b"}},
		{"a move names its card", logCoverMove("p1", "s1:ready", 1), []string{"p1"}},
		{"a note names its subjects and its named cards, none twice", Line{Note: &note}, []string{"p1", "p2", "q9"}},
		{"a stream-level note names its stream subject and its card", Line{Note: &Note{StreamLevel: true, Stream: "s1", Card: "stop-1"}}, []string{"stream:s1", "stop-1"}},
		// The refusal: a note naming no cards at all is about nothing.
		{"a note with no subjects and no named cards names none", Line{Note: &Note{ID: "n~1"}}, nil},
	}
	for _, c := range cases {
		require.Equal(t, c.want, c.line.Names(), c.name)
	}
}

func TestLogCoverAboutMatchesCardPrimaryPrefixAndRefuses(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		line Line
		id   string
		want bool
	}{
		{"the card itself", logCoverMove("p1", "s1:ready", 1), "p1", true},
		{"a primary is about its work card", Line{Kind: LineMove, Card: "p1.w2"}, "p1", true},
		{"a primary is about its read card", Line{Kind: LineMove, Card: "p1.r1.reader-a"}, "p1", true},
		{"a set move is about any card of the set", Line{Kind: LineMove, Card: "a", Cards: []string{"a", "b"}}, "b", true},
		{"the line's primary field names it when its card is not the primary", Line{Kind: LineMove, Card: "s1-merge", Primary: "p1"}, "p1", true},
		// The refusals.
		{"another card is not about the card", logCoverMove("p2", "s1:ready", 1), "p1", false},
		{"a prefix without the dot is not the card", Line{Kind: LineMove, Card: "p12"}, "p1", false},
		{"a note's line never matches by the primary field", Line{Note: &Note{ID: "n~1", Primaries: []string{"other"}}, Primary: "p1"}, "p1", false},
	}
	for _, c := range cases {
		require.Equal(t, c.want, c.line.About(c.id), c.name)
	}
}

func TestLogCoverReplayAppliesMoveLinesInOrder(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		lines []Line
		want  map[string]Place
	}{
		{
			"the last move wins, a set moves together, a removal takes the card off",
			[]Line{
				logCoverMove("p1", "s1:ready", 1),
				logCoverMove("p1", "s1:working", 2),
				{Kind: LineMove, At: logCoverAt, Table: Work, Card: "a", Cards: []string{"a", "b"}, To: "s1:review", Gen: 3},
				{Kind: LineMove, At: logCoverAt, Table: Work, Card: "p1", From: "s1:working", Removed: true},
			},
			map[string]Place{"work/a": {Row: "s1", Col: "review", Gen: 3}, "work/b": {Row: "s1", Col: "review", Gen: 3}},
		},
		{
			// The refusal: a queued line and a move naming no card pass by.
			"only move lines of a card are replayed",
			[]Line{
				{Kind: LineQueued, Table: Work, Card: "q1", To: "s1:ready"},
				{Kind: LineMove, Table: Work, To: "s1:ready"},
			},
			map[string]Place{},
		},
		{
			// The refusal: a move whose To names no place places nothing.
			"a move with no place in To places nothing",
			[]Line{logCoverMove("p1", "ready", 1)},
			map[string]Place{},
		},
	}
	for _, c := range cases {
		require.Equal(t, c.want, Replay(c.lines), c.name)
	}
}

func TestLogCoverReplayOntoHoldsTheReplayAsTheLogGrows(t *testing.T) {
	t.Parallel()
	start := func() map[string]Place {
		return map[string]Place{"work/p1": {Row: "s1", Col: Ready, Gen: 1}}
	}
	cases := []struct {
		name string
		out  map[string]Place
		line Line
		want map[string]Place
	}{
		{"one more move places the card again", start(), logCoverMove("p1", "s1:working", 2),
			map[string]Place{"work/p1": {Row: "s1", Col: "working", Gen: 2}}},
		{"a removal takes the card off", start(),
			Line{Kind: LineMove, Table: Work, Card: "p1", From: "s1:ready", Removed: true},
			map[string]Place{}},
		{"a set move places every card of the set", start(),
			Line{Kind: LineMove, Table: Readers, Card: "a", Cards: []string{"a", "b"}, To: "reader-a:ok", Gen: 4},
			map[string]Place{"work/p1": {Row: "s1", Col: Ready, Gen: 1}, "readers/a": {Row: "reader-a", Col: "ok", Gen: 4}, "readers/b": {Row: "reader-a", Col: "ok", Gen: 4}}},
		// The refusals: the held places stand as they were.
		{"a notification line changes nothing", start(),
			Line{Kind: Judgment, Note: &Note{ID: "n~1"}}, start()},
		{"a move with no place in To changes nothing", start(), logCoverMove("p1", "working", 2), start()},
	}
	for _, c := range cases {
		require.Equal(t, c.want, ReplayOnto(c.out, c.line), c.name)
	}
}

func TestLogCoverGroupSetsMergesSetMovesAndPassesTheRest(t *testing.T) {
	t.Parallel()
	move := func(id, cause, primary string, set map[string]string) Line {
		return Line{Kind: LineMove, At: logCoverAt, Table: Work, Card: id, From: "s1:ready", To: "s1:working",
			Gen: 1, Verb: "deal", Actor: "machine", Cause: cause, Primary: primary, Set: set}
	}
	grouped := func(first Line, cards []string) Line {
		g := first
		g.Cards = cards
		return g
	}
	cleared := grouped(move("a", "dealt", "p1", nil), []string{"a", "b"})
	cleared.Cause = ""
	cleared.Primary = ""
	scored := grouped(move("a", "dealt", "p1", map[string]string{"score": "1", "head": "h"}), []string{"a", "b"})
	scored.Set = map[string]string{"head": "h"}
	cases := []struct {
		name  string
		lines []Line
		want  []Line
	}{
		{"lines of one shape become one line naming its cards",
			[]Line{move("a", "dealt", "p1", nil), move("b", "dealt", "p1", nil)},
			[]Line{grouped(move("a", "dealt", "p1", nil), []string{"a", "b"})}},
		{"differing causes and primaries are left off the merged line",
			[]Line{move("a", "dealt", "p1", nil), move("b", "dealt again", "p2", nil)},
			[]Line{cleared}},
		{"the score rides aside: lines differing only in score merge and lose it",
			[]Line{move("a", "dealt", "p1", map[string]string{"score": "1", "head": "h"}),
				move("b", "dealt", "p1", map[string]string{"score": "2", "head": "h"})},
			[]Line{scored}},
		// The refusals: a line of no one else's shape, and a line that is
		// no move, are kept whole.
		{"lines of different shapes stay apart",
			[]Line{move("a", "dealt", "p1", nil), move("b", "dealt", "p1", map[string]string{"head": "h"})},
			[]Line{move("a", "dealt", "p1", nil), move("b", "dealt", "p1", map[string]string{"head": "h"})}},
		{"a queued line and a notification's line pass through ungrouped",
			[]Line{{Kind: LineQueued, Table: Work, Card: "a"}, {Kind: LineQueued, Table: Work, Card: "a"},
				Line{Kind: Happened, Note: &Note{ID: "n~1"}}},
			[]Line{{Kind: LineQueued, Table: Work, Card: "a"}, {Kind: LineQueued, Table: Work, Card: "a"},
				Line{Kind: Happened, Note: &Note{ID: "n~1"}}}},
	}
	for _, c := range cases {
		require.Equal(t, c.want, GroupSets(c.lines), c.name)
	}
}

func TestLogCoverLogViolationsReplaysTheLinesThenPlaces(t *testing.T) {
	t.Parallel()
	s := &Snapshot{Work: logCoverTable(Work, []string{"s1"}, logCoverCard("p1", "s1", Ready, 1))}
	// The main path: a log that replays to the table is clean.
	require.Empty(t, LogViolations(s, []Line{logCoverMove("p1", "s1:ready", 1)}))
	// The refusal: a card the log never placed.
	require.Equal(t, []Violation{{13, "work: p1 is at s1:ready and the log never placed it there"}},
		LogViolations(s, nil))
}

func TestLogCoverPlaceViolationsChecksEveryPlaceAndRefusesTheRest(t *testing.T) {
	t.Parallel()
	s := &Snapshot{
		Work: logCoverTable(Work, []string{"s1"}, logCoverCard("p1", "s1", Ready, 1), logCoverCard("ctl1", "s1", Ctl, 0),
			&Card{ID: "kept", Row: "s1", Col: "", Fields: map[string]string{"gen": "1"}}),
	}
	placed := func() map[string]Place {
		return map[string]Place{"work/p1": {Row: "s1", Col: Ready, Gen: 1}}
	}
	cases := []struct {
		name   string
		placed map[string]Place
		want   []Violation
	}{
		{"a table the log replays to is clean; a control card, an unplaced card, a ctl place and a table the snapshot does not hold pass",
			map[string]Place{"work/p1": {Row: "s1", Col: Ready, Gen: 1}, "work/ctl1": {Row: "s1", Col: Ctl, Gen: 0}, "ghost/x": {Row: "g", Col: "h", Gen: 0}},
			nil},
		{"a card the log never placed is refused",
			map[string]Place{},
			[]Violation{{13, "work: p1 is at s1:ready and the log never placed it there"}}},
		{"a card elsewhere than the log last moved it is refused",
			map[string]Place{"work/p1": {Row: "s2", Col: Review, Gen: 1}},
			[]Violation{{13, "work: p1 is at s1:ready and the log last moved it to s2:review"}}},
		{"a card at another generation than the log says is refused",
			map[string]Place{"work/p1": {Row: "s1", Col: Ready, Gen: 2}},
			[]Violation{{13, "work: p1 is at generation 1 and the log says 2"}}},
		{"a place whose card left the table is refused, in name order",
			map[string]Place{"work/p1": {Row: "s1", Col: Ready, Gen: 1}, "work/bb": {Row: "s1", Col: Review, Gen: 1}, "work/aa": {Row: "s1", Col: Working, Gen: 3}},
			[]Violation{{13, "work/aa is not on the table and the log last moved it to s1:working"},
				{13, "work/bb is not on the table and the log last moved it to s1:review"}}},
	}
	for _, c := range cases {
		require.Equal(t, c.want, PlaceViolations(s, c.placed), c.name)
	}
	// The refusal of nil tables: a snapshot with no tables at all is clean.
	require.Empty(t, PlaceViolations(&Snapshot{}, placed()))
}

func TestLogCoverNoteLineIsTheNotificationAsWritten(t *testing.T) {
	t.Parallel()
	n := Note{ID: "judgment-s1~9", Kind: Judgment, Type: NConflict, Stream: "s1",
		Who: "machine", What: "conflict on p1", At: logCoverAt, Primaries: []string{"p1"}}
	// The main path: the whole note, its line beside the step that wrote it.
	require.Equal(t, Line{Kind: Judgment, At: logCoverAt, Epoch: 9, Op: "op-1~9", Stream: "s1", Actor: "machine", Note: &n},
		NoteLine(n, "op-1~9"))
	cases := []struct {
		name string
		id   string
		want uint64
	}{
		{"the epoch after the id's last ~ is the line's epoch", "n~12", 12},
		// The refusal: an id holding no epoch stamps the line at 0.
		{"an id with no ~ is the line of epoch 0", "n", 0},
	}
	for _, c := range cases {
		require.Equal(t, c.want, NoteLine(Note{ID: c.id}, "op").Epoch, c.name)
	}
}

func TestLogCoverStreamViolationsKeepsLogAndInboxParity(t *testing.T) {
	t.Parallel()
	n1 := Note{ID: "n1~3", Kind: Happened, Type: NWorkOK, What: "ok at 3"}
	n2 := Note{ID: "n2~3", Kind: Judgment, Type: NConflict, What: "conflict on p1"}
	key2 := "n2~3 (judgment, stream stopped: conflict on a card): conflict on p1"
	cases := []struct {
		name  string
		lines []Line
		inbox []Note
		want  []Violation
	}{
		{"every note written to both is clean",
			[]Line{NoteLine(n1, "op-1"), NoteLine(n2, "op-1")}, []Note{n1, n2}, nil},
		{"an update line is the log's alone",
			[]Line{{Kind: Judgment, Verb: "updated", Note: &n2}, NoteLine(n1, "op-1")}, []Note{n1}, nil},
		{"a note in the inbox and not in the log is refused",
			[]Line{NoteLine(n1, "op-1")}, []Note{n1, n2},
			[]Violation{{14, "notification " + key2 + " is in the inbox and not in the log"}}},
		{"a note in the log and not in the inbox is refused",
			[]Line{NoteLine(n1, "op-1"), NoteLine(n2, "op-1")}, []Note{n1},
			[]Violation{{14, "notification " + key2 + " is in the log and not in the inbox"}}},
		// The refusal of the count: a note written twice needs both entries.
		{"a note written to the log twice needs the inbox twice",
			[]Line{NoteLine(n2, "op-1"), NoteLine(n2, "op-1")}, []Note{n2},
			[]Violation{{14, "notification " + key2 + " is in the log and not in the inbox"}}},
	}
	for _, c := range cases {
		require.Equal(t, c.want, StreamViolations(c.lines, c.inbox), c.name)
	}
}
