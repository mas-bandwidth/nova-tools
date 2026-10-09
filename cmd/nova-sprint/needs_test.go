package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
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

// Drop detaches a need from the waiting card in the same step (docs/SPEC-SPRINT.md
// section 3): one DETACHED line names the waiter and the id, and the card stays waiting.
func TestCardShowsTheWaivedNeeds(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 1 --one")
	ta.ok("add --stream s2 b --one --needs s1-1")
	before := ta.applies()
	out := ta.ok("drop s1-1 --reason obsolete")
	require.Contains(t, out, "DETACHED", "drop of a needed card: %s", out)
	require.Contains(t, out, "b", "drop of a needed card: %s", out)
	require.Contains(t, out, "s1-1", "drop of a needed card: %s", out)
	require.Greater(t, ta.applies(), before, "the drop wrote")
	require.Contains(t, ta.ok("card --fields s1-1"), "outcome=dropped")
	require.Contains(t, ta.ok("card --fields b"), "place=s2:waiting")
	require.NotContains(t, ta.ok("card --fields b"), "NEEDS s1-1")
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

// Drop detaches every need of one waiter in one line (docs/SPEC-SPRINT.md
// section 3) and opens no blocked judgment.
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
	before := ta.applies()
	out := ta.ok("drop " + strings.Join(ids, " ") + " --reason obsolete")
	require.Equal(t, 1, strings.Count(out, "DETACHED"), "one line for the twelve needs: %s", out)
	assert.Contains(t, out, "DETACHED b need "+strings.Join(ids, ",")+" dropped", "the line: %s", out)
	require.Greater(t, ta.applies(), before, "the drop wrote")
	for _, g := range ta.inboxGroups() {
		assert.False(t, g.Kind == sprint.Judgment && g.Type == sprint.NBlocked, "the drop opened a blocked judgment: %+v", g)
	}
	ta.clean()
}

// needsMaxFixture admits real dependency chains: the first waiter needs two
// ready cards; every later waiter needs its predecessor (SPEC-SPRINT section 11).
func needsMaxFixture(t *testing.T, streams, depth int) *testApp {
	t.Helper()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --one --stream seeds root-a")
	ta.ok("add --one --stream seeds root-b")
	st, err := ta.a.store(common{redis: "mem:0", actor: "coordinator"})
	require.NoError(t, err)
	requests := make([]sprint.AddReq, streams)
	for i := 0; i < streams; i++ {
		stream := fmt.Sprintf("s%d", i)
		cards := make([]sprint.CardAdd, depth)
		for j := range cards {
			needs := []string{"root-a", "root-b"}
			if j > 0 {
				needs = []string{fmt.Sprintf("%s-%d", stream, j)}
			}
			cards[j] = sprint.CardAdd{ID: fmt.Sprintf("%s-%d", stream, j+1), Needs: needs}
		}
		requests[i] = sprint.AddReq{Stream: stream, Cards: cards}
	}
	result, err := st.Run(context.Background(), store.AddEachStep(requests))
	require.NoError(t, err)
	require.Empty(t, result.Refused)
	return ta
}

// SPEC-SPRINT section 11: a bounded needs answer preserves full graph facts,
// stable identities and the dependency boundary while naming every hidden list.
func TestNeedsMaxBoundsTheOutputAndNamesTheRest(t *testing.T) {
	t.Parallel()
	ta := needsMaxFixture(t, 10, 100)
	type more struct {
		Shown       int    `json:"shown"`
		Total       int    `json:"total"`
		Omitted     int    `json:"omitted"`
		FirstHidden string `json:"first_hidden"`
		Command     string `json:"command"`
	}
	type card struct {
		Column string                        `json:"column"`
		ID     string                        `json:"id"`
		Depth  int                           `json:"depth"`
		Root   bool                          `json:"root"`
		Needs  []struct{ ID, Column string } `json:"needs"`
		More   *more                         `json:"more"`
	}
	type stream struct {
		Stream    string                       `json:"stream"`
		Cards     []card                       `json:"cards"`
		Widths    []struct{ Depth, Width int } `json:"width"`
		Total     int                          `json:"total"`
		More      *more                        `json:"more"`
		WidthMore *more                        `json:"width_more"`
	}
	type view struct {
		Streams []stream `json:"streams"`
		Cards   int      `json:"cards"`
		Orphans int      `json:"dropped_or_absent"`
		More    *more    `json:"more"`
	}
	var full view
	ta.json("needs --max 0", &full)
	require.Len(t, full.Streams, 10)
	require.Equal(t, 1000, full.Cards)
	for _, st := range full.Streams {
		assert.Len(t, st.Cards, 100)
		require.Len(t, st.Widths, 100, "the fixture is a chain, not a flat fan-out")
		assert.Nil(t, st.More)
		assert.Nil(t, st.WidthMore)
		assert.Len(t, st.Cards[0].Needs, 2)
		require.Equal(t, 99, st.Cards[99].Depth, "the fixture really reaches depth 99")
	}
	for _, max := range []int{1} {
		t.Run(fmt.Sprintf("max %d", max), func(t *testing.T) {
			line := fmt.Sprintf("needs --max %d", max)
			out := ta.ok(line + " --json")
			assert.Less(t, len(out), 4096, "1000 cards still produce a bounded answer")
			var got view
			require.NoError(t, json.Unmarshal([]byte(out), &got))
			assert.Len(t, got.Streams, max)
			assert.Equal(t, full.Cards, got.Cards)
			assert.Equal(t, full.Orphans, got.Orphans)
			require.NotNil(t, got.More, "stream truncation is explicit")
			assert.Equal(t, 10-max, got.More.Omitted)
			assert.Equal(t, full.Streams[max].Cards[0].ID, got.More.FirstHidden)
			cards, needs, widths := 0, 0, 0
			for _, st := range got.Streams {
				cards += len(st.Cards)
				widths += len(st.Widths)
				assert.Equal(t, 100, st.Total)
				require.NotNil(t, st.More)
				assert.Equal(t, 100-len(st.Cards), st.More.Omitted)
				assert.NotEmpty(t, st.More.FirstHidden)
				assert.Contains(t, st.More.Command, "--stream")
				assert.Contains(t, st.More.Command, "--max 0")
				assert.Contains(t, st.More.Command, "--json")
				require.NotNil(t, st.WidthMore)
				for _, c := range st.Cards {
					needs += len(c.Needs)
					if c.More != nil {
						assert.Greater(t, c.More.Omitted, 0)
						assert.NotEmpty(t, c.More.FirstHidden)
						assert.Contains(t, c.More.Command, "--max 0")
					}
				}
			}
			assert.LessOrEqual(t, cards, max)
			assert.LessOrEqual(t, needs, max)
			assert.LessOrEqual(t, widths, max)
			assert.Equal(t, full.Streams[0].Cards[0].ID, got.Streams[0].Cards[0].ID)
			assert.Equal(t, sprint.Waiting, got.Streams[0].Cards[0].Column)
			assert.Equal(t, sprint.Ready, got.Streams[0].Cards[0].Needs[0].Column)
			assert.True(t, got.Streams[0].Cards[0].Root)
			assert.Equal(t, 0, got.Streams[0].Cards[0].Depth)
			text := ta.ok(line)
			t.Logf("bound max=%d cards=1000 json_bytes=%d text_bytes=%d limit=4096", max, len(out), len(text))
			assert.Less(t, len(text), 4096)
			assert.Contains(t, text, "MORE kind=streams")
			assert.Contains(t, text, "MORE kind=cards")
			assert.Contains(t, text, "MORE kind=width")
			assert.Contains(t, text, "NEEDS OK cards=1000")
			assert.NotContains(t, text, "needs none", "a hidden dependency is not absent")
			if max == 1 {
				assert.Contains(t, text, "MORE kind=needs")
				assert.Contains(t, text, "first_hidden=root-b")
			}
		})
	}
	ta.clean()
}

// Cycles can be present in a stored graph even though admission refuses a new
// cycle. Bounding its evidence must not make the result look acyclic.
func TestNeedsMaxKeepsACycleVisibleWhenItsIDsAreHidden(t *testing.T) {
	t.Parallel()
	s := &sprint.Snapshot{Work: sprint.NewTable(sprint.Work)}
	s.Work.SetRows([]string{"a", "b"})
	for _, row := range []struct {
		name   string
		cycles int
	}{{"a", 3}, {"b", 1}} {
		for cycle := 0; cycle < row.cycles; cycle++ {
			for i := 0; i < 3; i++ {
				id := fmt.Sprintf("%s-%d-%d", row.name, cycle, i)
				s.Work.Put(&sprint.Card{ID: id, Row: row.name, Col: sprint.Waiting, Score: float64(cycle*3 + i),
					Fields: map[string]string{"needs": fmt.Sprintf("%s-%d-%d", row.name, cycle, (i+1)%3)}})
			}
		}
	}
	full := sprintNeeds(s, "", false)
	require.Len(t, full.Streams, 2, "the fixture declares both streams")
	limited := limitNeeds(full, 2, false, false)
	assert.Len(t, full.Streams[0].Cards, 9, "limiting leaves the full graph intact")
	assert.Len(t, full.Streams[0].Cycle, 3)
	assert.Len(t, full.Streams[1].Cycle, 1)
	assert.Len(t, limited.Streams[0].Cycle, 2)
	assert.Empty(t, limited.Streams[1].Cycle)
	require.NotNil(t, limited.Streams[1].CycleMore)
	assert.Equal(t, 1, limited.Streams[1].CycleMore.Omitted)
	assert.Equal(t, "b-0-0", limited.Streams[1].CycleMore.FirstHidden)
	text := needsText(limited, false)
	assert.Contains(t, text, "CYCLE stream=b its needs make a cycle\nMORE kind=cycle")
	assert.Contains(t, text, "shown=0 total=1 omitted=1 first_hidden=b-0-0")
}

// SPEC-SPRINT section 11: trimming display lists preserves complete orphan
// counts and depth/root facts, and does not rewrite the full graph's cards.
func TestNeedsMaxPreservesOrphanTotalsAndTheFullGraph(t *testing.T) {
	t.Parallel()
	s := &sprint.Snapshot{Work: sprint.NewTable(sprint.Work)}
	s.Work.SetRows([]string{"a", "b"})
	for _, c := range []*sprint.Card{
		{ID: "a-1", Row: "a", Col: sprint.Waiting, Score: 1, Fields: map[string]string{"needs": "missing-a,missing-b"}},
		{ID: "a-2", Row: "a", Col: sprint.Waiting, Score: 2, Fields: map[string]string{"needs": "a-1,missing-c"}},
		{ID: "b-1", Row: "b", Col: sprint.Waiting, Score: 1, Fields: map[string]string{"needs": "missing-d"}},
	} {
		s.Work.Put(c)
	}
	full := sprintNeeds(s, "", false)
	require.Len(t, full.Streams, 2, "the fixture declares both streams")
	require.Equal(t, 3, full.Orphans)
	require.Len(t, full.Streams[0].Cards[0].Needs, 2)
	limited := limitNeeds(full, 1, false, false)
	assert.Equal(t, full.Cards, limited.Cards)
	assert.Equal(t, full.Orphans, limited.Orphans)
	assert.Equal(t, full.Streams[0].Orphans, limited.Streams[0].Orphans)
	assert.Equal(t, full.Streams[0].Cards[0].Root, limited.Streams[0].Cards[0].Root)
	assert.Equal(t, full.Streams[0].Cards[0].Depth, limited.Streams[0].Cards[0].Depth)
	assert.Equal(t, full.Streams[0].Widths[0], limited.Streams[0].Widths[0])
	require.NotNil(t, limited.Streams[0].Cards[0].More)
	assert.Equal(t, 1, limited.Streams[0].Cards[0].More.Omitted)
	assert.Equal(t, "missing-b", limited.Streams[0].Cards[0].More.FirstHidden)
	assert.Equal(t, absentWord, limited.Streams[0].Cards[0].Needs[0].Column)
	assert.Len(t, full.Streams[0].Cards[0].Needs, 2, "the original needs are intact")
	assert.Nil(t, full.Streams[0].Cards[0].More, "the original has no truncation")
	assert.Equal(t, full, limitNeeds(full, 0, false, false), "--max 0 returns the complete graph")
}

// The smaller fixture checks budgets shared across streams, entirely hidden
// needs, drill-down selectors and roots without repeatedly walking 1000 cards.
func TestNeedsMaxSharesBudgetsAndPreservesDrillDownSelectors(t *testing.T) {
	t.Parallel()
	ta := needsMaxFixture(t, 3, 4)
	var full, got needsView
	ta.json("needs --max 0", &full)
	ta.json("needs --max 2", &got)
	require.Len(t, got.Streams, 2)
	cards, needs, widths := 0, 0, 0
	for _, st := range got.Streams {
		cards += len(st.Cards)
		widths += len(st.Widths)
		for _, c := range st.Cards {
			needs += len(c.Needs)
		}
	}
	assert.Equal(t, 2, cards)
	assert.Equal(t, 2, needs)
	assert.Equal(t, 2, widths)
	require.Len(t, got.Streams[0].Cards, 2)
	second := got.Streams[0].Cards[1]
	assert.Empty(t, second.Needs)
	require.NotNil(t, second.More, "an empty displayed list does not mean no needs")
	assert.Equal(t, "s0-1", second.More.FirstHidden)
	assert.Equal(t, 1, second.More.Omitted)
	assert.Contains(t, ta.ok("needs --max 2"), "needs shown=0")
	require.NotNil(t, got.More)
	drill := strings.TrimPrefix(got.More.Command, "nova-sprint ")
	got = needsView{}
	ta.json(drill, &got)
	assert.Equal(t, full, got, "the drill-down reproduces the complete graph")
	for _, line := range []string{"needs --stream s2 --max 1", "needs --roots --max 1"} {
		got = needsView{}
		ta.json(line, &got)
		require.Len(t, got.Streams, 1)
		assert.Len(t, got.Streams[0].Cards, 1)
		assert.Equal(t, 4, got.Streams[0].Total)
		assert.NotNil(t, got.Streams[0].WidthMore)
		if strings.Contains(line, "--roots") {
			require.NotNil(t, got.More)
			assert.Contains(t, got.More.Command, "--roots --json")
		}
	}
	assert.NotContains(t, ta.ok("needs --max 0"), "MORE kind=")
	ta.clean()
}
