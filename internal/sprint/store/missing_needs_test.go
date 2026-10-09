package store

import (
	"fmt"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/require"
)

func TestAddRefusesDependentsOfAnUnadmittedID(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{"bad.id", "ctl-absent"} {
		t.Run(bad, func(t *testing.T) {
			h := newHarness(t)
			h.setup(0)
			r := h.run(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{bad, "waiter"}, Needs: []string{bad}}))
			require.Len(t, r.Refused, 2, "dependent admitted: %+v", r)
			require.Nil(t, h.snap().Work.Card("waiter"), "dependent admitted: %+v", r)
			require.Nil(t, h.snap().Work.Card(bad), "dependent admitted: %+v", r)
			h.clean("refused missing dependency")
		})
	}
	// All or nothing: an add naming several writes none of them when any is
	// refused, naming every one; an existing id is refused as well.
	h := newHarness(t)
	h.setup(1)
	r := h.run(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"bad.id", "good"}}))
	require.Len(t, r.Refused, 2, "partial acceptance: %+v", r)
	require.Nil(t, h.snap().Work.Card("good"), "partial acceptance: %+v", r)
	require.Contains(t, fmt.Sprint(r.Refused), "all or none", "partial acceptance: %+v", r)
	r = h.run(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"s1-1", "waiter"}, Needs: []string{"s1-1"}}))
	require.Len(t, r.Refused, 2, "an existing id with a new one: %+v", r)
	require.Nil(t, h.snap().Work.Card("waiter"), "an existing id with a new one: %+v", r)
	h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"waiter"}, Needs: []string{"s1-1"}}))
	require.Equal(t, sprint.Waiting, h.state("waiter"), "waiter: %s", h.state("waiter"))
	h.clean("unrelated refusals")
}

// Recreate already-persisted data from the old admission bug. Production
// verbs no longer create it. A resolve detaches a name that is no card.
func seedMissingNeeds(h *harness, id, needs string) {
	h.t.Helper()
	h.m.mu.Lock()
	defer h.m.mu.Unlock()
	table := h.m.tables["t-work"]
	c := table.members[id]
	c.fields["needs"] = needs
	c.rev++
	table.rev++
}

// seedDroppedNeed marks a primary's record dropped off the table without a
// drop step. Add still refuses a need that names a dropped card. A resolve
// after it detaches the id from every waiting card that names it
// (docs/SPEC-SPRINT.md section 11). The log gets the removed line the engine
// would have written, so the store's replay stays true.
func seedDroppedNeed(h *harness, id string) { seedDroppedNeedWhy(h, id, "") }

// seedDroppedNeedWhy is seedDroppedNeed with the reason the record keeps.
func seedDroppedNeedWhy(h *harness, id, why string) {
	h.t.Helper()
	s := h.snap()
	c := s.Work.Card(id)
	if c == nil {
		return
	}
	from := c.Row + ":" + c.Col
	h.m.mu.Lock()
	t := h.m.tables["t-work"]
	mm := t.members[id]
	if mm == nil {
		mm = t.members[fmt.Sprintf("%s~%d", id, s.Epoch)]
	}
	if mm != nil {
		mm.placed, mm.row, mm.col = false, "", ""
		mm.fields["outcome"] = "dropped"
		if why != "" {
			mm.fields["reason"] = why
		}
		mm.rev++
		t.rev++
	}
	h.m.mu.Unlock()
	h.m.AtEpoch(s.Epoch, false).(*Mem).appendLine(sprint.Line{Kind: sprint.LineMove, At: s.Now, Epoch: s.Epoch, Card: id, Table: sprint.Work, From: from, Removed: true, Verb: "drop", Actor: "tester"})
}

// detachedStories is the happened lines of a gone need, for one card when id is set.
func detachedStories(h *harness, id string) []sprint.Note {
	h.t.Helper()
	all, _, err := h.m.NotesSince(h.ctx, "", 100000)
	require.NoError(h.t, err)
	var out []sprint.Note
	for _, n := range all {
		if n.Kind != sprint.Happened || n.Type != "need detached" {
			continue
		}
		if id != "" && !noteNames(n, id) {
			continue
		}
		out = append(out, n)
	}
	return out
}

func noteNames(n sprint.Note, id string) bool {
	for _, p := range n.Primaries {
		if p == id {
			return true
		}
	}
	return false
}

func TestStoredMissingNeedsDetach(t *testing.T) {
	t.Parallel()
	for _, sentinel := range []bool{false, true} {
		for _, live := range []bool{false, true} {
			t.Run(fmt.Sprintf("sentinel=%v/live=%v", sentinel, live), func(t *testing.T) {
				h := newHarness(t)
				h.setup(1)
				h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"waiter"}, Needs: []string{"s1-1"}, Sentinel: sentinel}))
				needs := "bad.id"
				if live {
					needs += ",s1-1"
				}
				seedMissingNeeds(h, "waiter", needs)
				h.must(ResolveStep(sprint.ResolveReq{}))
				require.Empty(t, h.nOpenOf(sprint.NMissingNeed, "waiter"))
				story := detachedStories(h, "waiter")
				require.Len(t, story, 1, "story: %+v", story)
				require.Equal(t, "need bad.id names no card; detached", story[0].What)
				c := h.snap().Work.Card("waiter")
				switch {
				case live:
					require.Equal(t, sprint.Waiting, c.Col, "live dependency bypassed: %+v", c)
					require.Equal(t, "s1-1", c.F("needs"), "live dependency bypassed: %+v", c)
					require.Empty(t, c.F("reached"), "live dependency bypassed: %+v", c)
				case sentinel:
					require.Equal(t, sprint.Waiting, c.Col, "sentinel not reached: %+v", c)
					require.NotEmpty(t, c.F("reached"), "sentinel not reached: %+v", c)
					require.Empty(t, c.F("needs"), "sentinel not reached: %+v", c)
				default:
					require.Equal(t, sprint.Ready, c.Col, "primary not ready: %+v", c)
					require.Empty(t, c.F("needs"), "primary not ready: %+v", c)
				}
				h.must(ResolveStep(sprint.ResolveReq{}))
				require.Len(t, detachedStories(h, "waiter"), 1, "told again")
				h.clean("missing need detached")
			})
		}
	}
}

func TestTwoMissingNeedsDetachInOneResolve(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"waiter"}, Needs: []string{"s1-1"}}))
	seedMissingNeeds(h, "waiter", "bad.id,later.bad")
	h.must(ResolveStep(sprint.ResolveReq{}))
	require.Equal(t, sprint.Ready, h.state("waiter"))
	require.Empty(t, h.snap().Work.Card("waiter").F("needs"))
	require.Empty(t, h.nOpenOf(sprint.NMissingNeed, "waiter"))
	story := detachedStories(h, "waiter")
	require.Len(t, story, 2, "story: %+v", story)
	h.clean("two missing needs detached")
}

func TestAGoneNeedDetachesAndALiveOneStays(t *testing.T) {
	t.Parallel()
	for _, action := range []string{"missing-live", "missing-only", "dropped"} {
		t.Run(action, func(t *testing.T) {
			h := newHarness(t)
			h.setup(1)
			h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"waiter"}, Needs: []string{"s1-1"}}))
			switch action {
			case "missing-live":
				seedMissingNeeds(h, "waiter", "later,s1-1")
				h.must(ResolveStep(sprint.ResolveReq{}))
				c := h.snap().Work.Card("waiter")
				require.Equal(t, sprint.Waiting, c.Col)
				require.Equal(t, "s1-1", c.F("needs"))
				require.Empty(t, h.nOpenOf(sprint.NMissingNeed, "waiter"))
				story := detachedStories(h, "waiter")
				require.Len(t, story, 1, "story: %+v", story)
				require.Contains(t, story[0].What, "later")
			case "missing-only":
				seedMissingNeeds(h, "waiter", "later")
				h.must(ResolveStep(sprint.ResolveReq{}))
				require.Equal(t, sprint.Ready, h.state("waiter"))
				require.Empty(t, h.snap().Work.Card("waiter").F("needs"))
				require.Empty(t, h.nOpenOf(sprint.NMissingNeed, "waiter"))
			case "dropped":
				h.must(AddStep(sprint.AddReq{Stream: "s3", IDs: []string{"later"}}))
				seedMissingNeeds(h, "waiter", "later")
				seedDroppedNeed(h, "later")
				h.must(ResolveStep(sprint.ResolveReq{}))
				require.Equal(t, sprint.Ready, h.state("waiter"))
				require.Empty(t, h.nOpenOf(sprint.NBlocked, "waiter"))
				story := detachedStories(h, "waiter")
				require.Len(t, story, 1, "story: %+v", story)
				require.Contains(t, story[0].What, "dropped")
			}
			h.clean("gone need detached")
		})
	}
}

// Every gone need of a waiting card is detached in the one resolve that finds
// it, and a second resolve does not tell it again.
func TestMissingNeedsDetachAllInOneResolve(t *testing.T) {
	t.Parallel()
	const n = 51
	h := newHarness(t)
	h.setup(1)
	h.must(AddStep(sprint.AddReq{Stream: "s2", Count: n, Needs: []string{"s1-1"}}))
	for i := 1; i <= n; i++ {
		seedMissingNeeds(h, fmt.Sprintf("s2-%d", i), "bad.id")
	}
	h.must(ResolveStep(sprint.ResolveReq{}))
	require.Empty(t, h.nOpenOf(sprint.NMissingNeed, ""))
	require.Len(t, detachedStories(h, ""), n)
	for i := 1; i <= n; i++ {
		id := fmt.Sprintf("s2-%d", i)
		require.Equal(t, sprint.Ready, h.state(id), "%s", id)
	}
	h.must(ResolveStep(sprint.ResolveReq{}))
	require.Len(t, detachedStories(h, ""), n, "told again")
	h.clean("missing needs detached")
}
