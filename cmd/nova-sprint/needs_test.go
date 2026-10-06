package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// H11: card shows each need with its state and what needs the card; queue
// --stream <s> --col waiting shows what each waiting card still waits for.
func TestTheReadsShowTheNeeds(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 2")
	ta.ok("add --stream s2 b --one --needs s1-1,s1-2")
	out := ta.ok("card --fields b")
	require.Contains(t, out, "NEEDS s1-1 ready\n", "card b")
	require.Contains(t, out, "NEEDS s1-2 ready\n", "card b")
	require.Contains(t, ta.ok("card --fields s1-1"), "NEEDED-BY b\n", "card s1-1")
	require.Contains(t, ta.ok("queue --stream s2 --col waiting"), "b work:s2:waiting waits for: s1-1,s1-2", "queue")
	code, _, errs := ta.do("queue --as m1 --col waiting")
	require.Equal(t, 2, code, "--col without --stream: %d %s", code, errs)
	require.Contains(t, errs, "--col takes waiting, with --stream", "--col without --stream: %d %s", code, errs)
	ta.clean()
}

// card shows each waived need, by whom and when it was waived.
func TestCardShowsTheWaivedNeeds(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 1 --one")
	ta.ok("add --stream s2 b --one --needs s1-1")
	ta.ok("drop s1-1 --reason obsolete")
	g := ta.group(sprint.NBlocked, "s2")
	ta.ok("ack " + g.Notes[0] + " --reason fine")
	out := ta.ok("card --fields b")
	require.Contains(t, out, "NEEDS s1-1 off the table (dropped) waived by ", "card b")
	require.Contains(t, out, " at 20", "card b")
	ta.clean()
}

func TestAddDoesNotAdmitAWaiterOfAnInvalidID(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	code, out, errs := ta.do("add --stream s1 --needs bad.id bad.id waiter")
	require.Equal(t, 1, code, "refusal: code=%d out=%s errors=%s", code, out, errs)
	require.Contains(t, out+errs, "bad.id", "refusal: code=%d out=%s errors=%s", code, out, errs)
	require.Contains(t, out+errs, "waiter", "refusal: code=%d out=%s errors=%s", code, out, errs)
	require.NotContains(t, ta.ok("queue --stream s1 --col waiting"), "waiter work:", "admitted waiter")
	ta.clean()
}

// card prints what holds the primary now (check rule 12): with the machine
// STOPPED, the next tick's deal, and behind it the need that tick moves.
func TestTheCardSaysWhatHoldsIt(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 1 --one")
	ta.ok("add --stream s2 b --one --needs s1-1")
	require.Contains(t, ta.ok("card --fields s1-1"), "HELD (e) the machine is STOPPED; the next tick: s1-1", "card s1-1")
	require.Contains(t, ta.ok("card --fields b"), "HELD (d) needs s1-1 (e)", "card b")
	require.Contains(t, ta.ok("card b --json"), `"held":{"id":"b","by":"d"`, "card b --json")
}

// inbox --open lists the whole needs of a blocked judgment, one per line, as
// card --fields does; the judgment's own line previews them.
func TestInboxOpenListsEveryDroppedNeed(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 12")
	var ids []string
	for i := 1; i <= 12; i++ {
		ids = append(ids, "s1-"+strconv.Itoa(i))
	}
	ta.ok("add --stream s2 b --one --needs " + strings.Join(ids, ","))
	ta.ok("drop " + strings.Join(ids, " ") + " --reason obsolete")
	g := ta.group(sprint.NBlocked, "s2")
	list := ta.ok("inbox --open " + g.ID)
	for _, id := range ids {
		assert.Contains(t, list, "\n  NEEDS "+id+"\n", "inbox --open %s does not list the need %s", g.ID, id)
	}
	assert.Contains(t, list, "... and 4 more", "the judgment's line no longer previews the needs")
	assert.NotContains(t, ta.ok("inbox"), "NEEDS ", "inbox without --open lists needs")
	var open struct {
		Needs []string `json:"needs"`
	}
	ta.json("inbox --open "+g.ID, &open)
	assert.Len(t, open.Needs, 12, "inbox --open --json needs: %v", open.Needs)
	card := ta.ok("card --fields b")
	for _, id := range ids {
		assert.Contains(t, card, "NEEDS "+id+" ", "card --fields b lacks %s", id)
	}
	ta.clean()
}

// capMore is the truncation marker needs prints after a list it cut.
type capMore struct {
	Omitted  int    `json:"omitted"`
	Command  string `json:"command"`
	Boundary string `json:"boundary"`
	Needs    *int   `json:"needs"`
}

// needsCapCard is one card of the needs graph, as --json prints it.
type needsCapCard struct {
	ID    string `json:"id"`
	Needs []struct {
		ID    string `json:"id"`
		State string `json:"state"`
	} `json:"needs"`
	More *capMore `json:"more"`
}

// needsCapStream is one stream of the needs graph, as --json prints it.
type needsCapStream struct {
	Stream    string         `json:"stream"`
	Cards     []needsCapCard `json:"cards"`
	CardsMore *capMore       `json:"cards_more"`
	Widths    []struct {
		Depth int `json:"depth"`
		Width int `json:"width"`
	} `json:"width"`
	WidthsMore *capMore `json:"width_more"`
	Total      int      `json:"total"`
	Cycle      []string `json:"cycle"`
	CycleMore  *capMore `json:"cycle_more"`
}

// needsCapView is the needs --json object the cap test reads.
type needsCapView struct {
	Streams []needsCapStream `json:"streams"`
	More    *capMore         `json:"more"`
	Cards   int              `json:"cards"`
}

// needsDepthLines counts the card lines (each names a depth). The summary
// lines name cards= and are not card lines.
func needsDepthLines(out string) int {
	n := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, " depth=") {
			n++
		}
	}
	return n
}

// TestNeedsMaxBoundsTheOutputAndNamesTheRest: --max lists at most that many
// items of each kind, in text and JSON. Totals stay the whole graph. Every
// list that was cut is followed by a more field naming how many were omitted
// and the command that prints the rest. A cut of cards also names the first
// card it hid and how many unmet needs that card has, so the cap cannot be
// read as a blocked card needing nothing. --max 0 is the whole graph. A
// thousand waiting cards at --max 1 stays bounded in both modes.
func TestNeedsMaxBoundsTheOutputAndNamesTheRest(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream gate a b")
	var streams []string
	for i := 0; i < 10; i++ {
		streams = append(streams, fmt.Sprintf("s%02d", i))
	}
	ta.ok("add --stream " + strings.Join(streams, ",") + " --count 100 --needs a,b")
	ta.ok("add --one --stream deep r0 --needs a")
	ta.ok("add --one --stream deep r1 --needs r0")
	ta.ok("add --one --stream deep r2 --needs r1")
	ta.ok("add --one --stream deep r3 --needs r2")
	ta.ok("add --one --stream deep r4 --needs r3")

	const waiting = 1005 // ten streams of 100, plus the chain of five

	textAll := ta.ok("needs --max 0")
	jsonAll := ta.ok("needs --max 0 --json")
	textOne := ta.ok("needs --max 1")
	jsonOne := ta.ok("needs --max 1 --json")
	require.Equal(t, textOne, ta.ok("needs --max 1"), "the capped text is stable")
	require.Equal(t, waiting, needsDepthLines(textAll), " --max 0 lists every waiting card")
	require.Equal(t, 1, needsDepthLines(textOne), "--max 1 lists one card")
	require.Greater(t, len(textAll), 40000, "the full text is the graph, not a summary")
	require.Greater(t, len(jsonAll), 40000, "the full JSON is the graph")
	require.Less(t, len(textOne), 4000, "text --max 1 on a thousand cards stays bounded")
	require.Less(t, len(jsonOne), 4000, "JSON --max 1 on a thousand cards stays bounded")
	require.NotContains(t, textAll, "\nMORE ", "--max 0 cuts nothing")
	require.NotContains(t, textOne, " needs none", "a cap must not say a listed card needs nothing")

	var full, got needsCapView
	require.NoError(t, json.Unmarshal([]byte(jsonAll), &full), "needs --max 0 --json: %s", jsonAll)
	require.NoError(t, json.Unmarshal([]byte(jsonOne), &got), "needs --max 1 --json: %s", jsonOne)
	require.Nil(t, full.More, "--max 0 has no streams more field")
	require.Equal(t, waiting, full.Cards)
	require.Equal(t, full.Cards, got.Cards, "the sprint total stays the whole count")
	require.GreaterOrEqual(t, len(full.Streams), 11)
	require.Len(t, got.Streams, 1, "at most --max streams")
	require.NotNil(t, got.More)
	require.Equal(t, len(full.Streams)-1, got.More.Omitted)
	require.Equal(t, "nova-sprint needs --max 0", got.More.Command)
	require.Equal(t, full.Streams[1].Cards[0].ID, got.More.Boundary)
	require.NotNil(t, got.More.Needs)
	require.Equal(t, len(full.Streams[1].Cards[0].Needs), *got.More.Needs)
	require.Greater(t, *got.More.Needs, 0, "the first hidden stream's first card has unmet needs")
	require.Contains(t, textOne, fmt.Sprintf(
		"MORE kind=streams shown=1 total=%d omitted=%d boundary=%s needs=%d run: nova-sprint needs --max 0",
		len(full.Streams), got.More.Omitted, got.More.Boundary, *got.More.Needs))

	fs, cs := full.Streams[0], got.Streams[0]
	require.Equal(t, fs.Stream, cs.Stream)
	require.Equal(t, fs.Total, cs.Total, "a stream's total stays the whole count")
	require.Len(t, cs.Cards, 1)
	require.Equal(t, fs.Cards[0].ID, cs.Cards[0].ID, "the shown card keeps its id, the first in chain order")
	require.Len(t, cs.Cards[0].Needs, 1)
	require.Equal(t, fs.Cards[0].Needs[0].ID, cs.Cards[0].Needs[0].ID)
	require.Equal(t, fs.Cards[0].Needs[0].State, cs.Cards[0].Needs[0].State)
	require.Greater(t, len(fs.Cards[0].Needs), 1, "the fixture's first card has a need the cap must name as omitted")
	require.NotNil(t, cs.Cards[0].More)
	require.Equal(t, len(fs.Cards[0].Needs)-1, cs.Cards[0].More.Omitted)
	require.Equal(t, "nova-sprint card --fields "+cs.Cards[0].ID, cs.Cards[0].More.Command)
	require.Contains(t, textOne, fmt.Sprintf(
		"MORE kind=needs card=%s shown=1 total=%d omitted=%d run: nova-sprint card --fields %s",
		cs.Cards[0].ID, len(fs.Cards[0].Needs), cs.Cards[0].More.Omitted, cs.Cards[0].ID))
	require.NotNil(t, cs.CardsMore)
	require.Equal(t, len(fs.Cards)-1, cs.CardsMore.Omitted)
	require.Equal(t, "nova-sprint needs --stream "+cs.Stream+" --max 0", cs.CardsMore.Command)
	require.Equal(t, fs.Cards[1].ID, cs.CardsMore.Boundary)
	require.NotNil(t, cs.CardsMore.Needs)
	require.Equal(t, len(fs.Cards[1].Needs), *cs.CardsMore.Needs)
	require.Greater(t, *cs.CardsMore.Needs, 0, "the first hidden card of the stream has unmet needs")
	require.Contains(t, textOne, fmt.Sprintf(
		"MORE kind=cards stream=%s shown=1 total=%d omitted=%d boundary=%s needs=%d run: nova-sprint needs --stream %s --max 0",
		cs.Stream, len(fs.Cards), cs.CardsMore.Omitted, cs.CardsMore.Boundary, *cs.CardsMore.Needs, cs.Stream))
	require.LessOrEqual(t, len(cs.Widths), 1)
	if len(fs.Widths) > 1 {
		require.NotNil(t, cs.WidthsMore)
		require.Equal(t, len(fs.Widths)-1, cs.WidthsMore.Omitted)
	} else if len(cs.Widths) == 1 {
		require.Equal(t, fs.Widths[0].Depth, cs.Widths[0].Depth)
		require.Equal(t, fs.Widths[0].Width, cs.Widths[0].Width, "a shown width is the whole width at that depth")
	}

	// The chain is the dependency boundary: r1 waits on r0, and --max 1
	// shows only r0. The cut must name r1 and that r1 has a need.
	var deepAll, deepOne needsCapView
	deepTextAll := ta.ok("needs --stream deep --max 0")
	deepTextOne := ta.ok("needs --stream deep --max 1")
	require.Equal(t, deepTextAll, ta.ok("needs --stream deep"), "under the default cap the chain is whole")
	require.NoError(t, json.Unmarshal([]byte(ta.ok("needs --stream deep --max 0 --json")), &deepAll))
	require.NoError(t, json.Unmarshal([]byte(ta.ok("needs --stream deep --max 1 --json")), &deepOne))
	require.Equal(t, 5, deepOne.Cards)
	require.Len(t, deepOne.Streams, 1)
	dFull, dCap := deepAll.Streams[0], deepOne.Streams[0]
	require.Equal(t, 5, dCap.Total)
	require.Len(t, dCap.Cards, 1)
	require.Equal(t, "r0", dCap.Cards[0].ID)
	require.NotEmpty(t, dCap.Cards[0].Needs, "the shown root keeps its need")
	require.Equal(t, "r1", dFull.Cards[1].ID)
	require.NotEmpty(t, dFull.Cards[1].Needs, "r1 is blocked")
	require.NotNil(t, dCap.CardsMore)
	require.Equal(t, 4, dCap.CardsMore.Omitted)
	require.Equal(t, "r1", dCap.CardsMore.Boundary)
	require.NotNil(t, dCap.CardsMore.Needs)
	require.Equal(t, len(dFull.Cards[1].Needs), *dCap.CardsMore.Needs)
	require.Equal(t, "nova-sprint needs --stream deep --max 0", dCap.CardsMore.Command)
	require.Len(t, dCap.Widths, 1)
	require.Equal(t, dFull.Widths[0].Width, dCap.Widths[0].Width)
	require.NotNil(t, dCap.WidthsMore)
	require.Equal(t, len(dFull.Widths)-1, dCap.WidthsMore.Omitted)
	require.Equal(t, "nova-sprint needs --stream deep --max 0", dCap.WidthsMore.Command)
	require.Contains(t, deepTextOne, "MORE kind=cards stream=deep shown=1 total=5 omitted=4 boundary=r1 needs=1 run: nova-sprint needs --stream deep --max 0")
	require.Contains(t, deepTextOne, "MORE kind=width stream=deep shown=1 total=5 omitted=4 run: nova-sprint needs --stream deep --max 0")
	require.NotContains(t, deepTextOne, "needs none")
	require.NotContains(t, deepTextOne, "r1 needs r0", "the hidden card is not listed as having no need, and is not listed at all")
	require.Contains(t, deepTextOne, "NEEDS stream=deep cards=5")
	require.Equal(t, 5, needsDepthLines(deepTextAll))
	require.Equal(t, 1, needsDepthLines(deepTextOne))

	rootsText := ta.ok("needs --roots --max 1")
	rootsJSON := ta.ok("needs --roots --max 1 --json")
	require.Less(t, len(rootsText), 4000, "--roots --max 1 stays bounded")
	require.Less(t, len(rootsJSON), 4000, "--roots --max 1 --json stays bounded")
	require.Equal(t, 1, needsDepthLines(rootsText))
	var roots needsCapView
	require.NoError(t, json.Unmarshal([]byte(rootsJSON), &roots))
	require.Equal(t, waiting, roots.Cards, "--roots keeps the sprint total")
	require.Len(t, roots.Streams, 1)
	require.NotNil(t, roots.More)
	require.Contains(t, roots.More.Command, "--roots")
	require.Contains(t, roots.More.Command, "--max 0")
	require.Contains(t, rootsText, "run: "+roots.More.Command)
	if roots.Streams[0].CardsMore != nil {
		require.Contains(t, roots.Streams[0].CardsMore.Command, "--roots")
		require.Greater(t, *roots.Streams[0].CardsMore.Needs, 0)
	}
	ta.clean()
}
