package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The coordinator's edits of a STOPPED sprint's unstarted primaries (the
// owner, 2026-10-01: "What other things should you be able to do to mutate a
// stopped sprint" / "I don't want you manually hopping in and working around
// it and doing manual stuff."): refused on a RUNNING machine, and for a card
// that has started, one case per state.

// editWorld is stream a with a-1 ready and a-2 waiting on it.
func editWorld(t *testing.T) *world {
	t.Helper()
	w := newWorld(t, "reader-a", "reader-b")
	w.must(Add(w.s, AddReq{Stream: "a", IDs: []string{"a-1"}, Brief: "old one", Who: "coordinator"}))
	w.must(Add(w.s, AddReq{Stream: "a", IDs: []string{"a-2"}, Needs: []string{"a-1"}, Brief: "old two", Who: "coordinator"}))
	require.Equal(t, Ready, w.s.Work.Placed("a-1").Col)
	require.Equal(t, Waiting, w.s.Work.Placed("a-2").Col)
	return w
}

// startedCases is a started primary in each state an edit refuses: dealt once
// and back in ready, working, in review, merging and landed.
var startedCases = []struct {
	name, col string
	attempt   string
}{
	{"ready with an attempt dealt", Ready, "1"},
	{"working", Working, "1"},
	{"review", Review, "1"},
	{"merging", Merging, "1"},
	{"landed", Landed, "1"},
}

func TestBriefReplacesTheBriefOfAnUnstartedPrimary(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"a-1", "a-2"} {
		w := editWorld(t)
		before := *w.s.Work.Placed(id)
		w.must(Brief(w.s, BriefReq{ID: id, Brief: "new brief", Who: "coordinator"}))
		c := w.s.Work.Placed(id)
		assert.Equal(t, "new brief", c.F("brief"), id)
		assert.Equal(t, before.Row, c.Row, id)
		assert.Equal(t, before.Col, c.Col, id)
		assert.Equal(t, before.Score, c.Score, id)
		assert.Equal(t, before.Fields["needs"], c.F("needs"), id)
	}
}

func TestBriefIsRefusedOnARunningMachine(t *testing.T) {
	t.Parallel()
	w := editWorld(t)
	w.s.Running = true
	p := Brief(w.s, BriefReq{ID: "a-1", Brief: "new", Who: "coordinator"})
	require.Len(t, p.Refused, 1)
	assert.Empty(t, p.Units)
	assert.Contains(t, p.Refused[0].Why, "the machine is RUNNING")
	assert.Contains(t, p.Refused[0].Why, "run: nova-sprint stop")
}

func TestBriefIsRefusedForAStartedCard(t *testing.T) {
	t.Parallel()
	for _, tc := range startedCases {
		w := editWorld(t)
		w.place(w.s.Work, "a-1", "a", tc.col)
		w.s.Work.Placed("a-1").Fields["attempt"] = tc.attempt
		p := Brief(w.s, BriefReq{ID: "a-1", Brief: "new", Who: "coordinator"})
		require.Len(t, p.Refused, 1, tc.name)
		assert.Empty(t, p.Units, tc.name)
		assert.Contains(t, p.Refused[0].Why, "a-1 is "+tc.col, tc.name)
		assert.Contains(t, p.Refused[0].Why, "keeps its brief", tc.name)
	}
}

func TestBriefIsRefusedForASentinelAndAnUnknownCard(t *testing.T) {
	t.Parallel()
	w := editWorld(t)
	w.must(Add(w.s, AddReq{Stream: "a", IDs: []string{"a-stop"}, Sentinel: true, Who: "coordinator"}))
	p := Brief(w.s, BriefReq{ID: "a-stop", Brief: "new", Who: "coordinator"})
	require.Len(t, p.Refused, 1)
	assert.Contains(t, p.Refused[0].Why, "a-stop is a sentinel, not a primary")
	p = Brief(w.s, BriefReq{ID: "zz", Brief: "new", Who: "coordinator"})
	require.Len(t, p.Refused, 1)
	assert.Contains(t, p.Refused[0].Why, "no primary zz on the work table")
}
