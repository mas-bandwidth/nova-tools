package friend

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
)

// cappedHarness is a lane harness whose card turn prints lines and then works on, printing
// nothing more, until its turn is ended: a lane that runs past its cap.
type cappedHarness struct {
	mu    sync.Mutex
	turns int
	lines int
}

func (h *cappedHarness) Deliver(context.Context, string) (int, error) { return 0, nil }

func (h *cappedHarness) OpenSession(context.Context, string) (string, error) { return "ses_1", nil }

func (h *cappedHarness) DeliverTo(ctx context.Context, _, _ string) (LaneTurn, error) {
	h.mu.Lock()
	h.turns++
	n := h.lines
	h.mu.Unlock()
	for i := range n {
		printed(ctx, fmt.Appendf(nil, "line %d of the lane\n", i))
	}
	<-ctx.Done()
	return LaneTurn{Exit: -1}, errors.New("the delivery was stopped with its process group")
}

func (h *cappedHarness) got() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.turns
}

// A lane's wall time is capped by its card's tier (a-lane-is-capped-by-its-tier.w1): a
// flash card's lane that runs past the flash cap is ended by the daemon, the card's
// REPORT.md is a HOLD naming `capped at <cap>` with the last 40 lines of the lane's
// output, the failed finish carries the cap, the card is not handed again, and the sprint
// that reads the finish deals it once more at the next tier up, before the cap counts as
// a failure; the cap and the overrun are on the card's cost record.
func TestALaneIsCappedByItsTier(t *testing.T) {
	t.Parallel()

	var report string
	var sent []string
	synctest.Test(t, func(t *testing.T) {
		dir := laneEndFixture(t, "c1~15")
		out := filepath.Join(dir, "outbox", "c1~15")
		h := &cappedHarness{lines: 50}
		r, state := laneRig(t, &lanesHarness{dir: dir, active: map[string]int{}}, 1)
		r.d.Deliver = h
		r.d.heldCards = []HeldCard{{Card: "c1", Job: "c1~15", Col: "working", Kind: "work", Tier: "flash", Epoch: 15}}
		r.d.LaneCaps = func() map[string]time.Duration { return map[string]time.Duration{"flash": 2 * time.Minute} }
		f := &finishes{}
		r.d.Finish = f.finish
		r.run(t, 400)

		raw, err := os.ReadFile(filepath.Join(out, "REPORT.md"))
		require.NoError(t, err, strings.Join(r.records, "\n"))
		report = string(raw)
		assert.True(t, strings.HasPrefix(report, "Verdict: HOLD\n\nnova-friend lane 1 of bob finished card c1: capped at 2m0s (tier flash, overrun "), report)
		assert.Contains(t, report, "the card's wall reached its tier's cap and the daemon ended the lane")
		assert.Contains(t, report, "no pushed head found.")
		assert.Contains(t, report, "The last 40 lines of the lane's output:\n\n    line 10 of the lane\n")
		assert.Contains(t, report, "    line 49 of the lane\n")
		assert.NotContains(t, report, "line 9 of the lane", "only the last 40 lines")
		assert.Equal(t, 1, h.got(), "the capped card is ended, not handed again")
		assert.Empty(t, state.Started)
		assert.Equal(t, []string{"c1~15"}, state.GivenUp)
		records := strings.Join(r.records, "\n")
		assert.Contains(t, records, "lane 1: card c1 capped at 2m0s (tier flash): its wall since ")
		assert.Contains(t, records, "card=capped turn=1/2")
		assert.Contains(t, records, "finish=failed sent=server")
		got := f.got()
		require.Len(t, got, 1, "one finish")
		sent = got[0]
	})

	require.Equal(t, []string{"finish", "--as", "friend.bob", "c1@1", "--epoch", "15", "--failed", "--branch", "sprint/c1.g1.e15", "--report"}, sent[:10])
	assert.True(t, strings.HasPrefix(sent[10], "friend bob HOLD: nova-friend lane 1 of bob finished card c1: capped at 2m0s (tier flash, overrun "), sent[10])

	// the sprint reads the finish: the first cap re-deals the card one tier up, its failed
	// count untouched; the cap and the overrun are on the take's cost record
	s, finish := cappedSprint(t, "", sent[10])
	p := sprint.Finish(s, finish)
	require.Empty(t, p.Refused, "%+v", p.Refused)
	require.Len(t, p.Units, 1)
	wc, pr := entryOf(p.Units[0], "c1"), entryOf(p.Units[0], "p1")
	require.NotNil(t, wc.Move)
	assert.Equal(t, sprint.DoneFailed, wc.Move.Col)
	assert.Equal(t, "2m0s", wc.Set[sprint.FieldLaneCap])
	require.NotNil(t, pr.Move)
	assert.Equal(t, sprint.Ready, pr.Move.Col, "re-dealt: the primary goes back to ready for the next deal")
	assert.Equal(t, "pro", pr.Set[sprint.FieldTierNow], "at the next tier up")
	assert.Equal(t, "flash -> pro", pr.Set[sprint.FieldCapRedealt])
	assert.Empty(t, pr.Set["failed"], "a cap re-dealt is no failure")
	rec := pr.Set[sprint.FieldCostRecord+"c1#g1"]
	assert.Contains(t, rec, "end=capped")
	assert.Contains(t, rec, "lane_cap=2m0s lane_overrun=")
	assert.Contains(t, p.Units[0].Moved, "re-dealt once at the next tier up, pro")

	// a card the cap re-dealt once already: the second cap is a failure
	s, finish = cappedSprint(t, "flash -> pro", sent[10])
	p = sprint.Finish(s, finish)
	require.Empty(t, p.Refused, "%+v", p.Refused)
	require.Len(t, p.Units, 1)
	pr = entryOf(p.Units[0], "p1")
	require.NotNil(t, pr.Move)
	assert.Equal(t, sprint.Review, pr.Move.Col)
	assert.Equal(t, "1", pr.Set["failed"], "the second cap counts as a failure")
	assert.Contains(t, pr.Set[sprint.FieldCostRecord+"c1#g1"], "lane_cap=2m0s")
	assert.Equal(t, "2m0s", entryOf(p.Units[0], "c1").Set[sprint.FieldLaneCap])
}

// cappedSprint is a sprint with the friend bob's work card c1 working for the primary p1,
// on flash, its cap re-deal spent when redealt says so, and bob's finish of it with report.
func cappedSprint(t *testing.T, redealt, report string) (*sprint.Snapshot, sprint.FinishReq) {
	t.Helper()
	s := &sprint.Snapshot{Now: time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC), Work: sprint.NewTable(sprint.Work), Readers: sprint.NewTable(sprint.Readers),
		Merge: sprint.NewTable(sprint.Merge), Fleet: sprint.NewTable(sprint.Fleet), Coordinator: "coordinator", Actor: "coordinator"}
	s.Work.SetRows([]string{"s1"})
	s.Fleet.SetRows([]string{sprint.FriendRow("bob")})
	pr := map[string]string{"kind": "primary", "stream": "s1", "work": "c1", "attempt": "1", "brief": "p1: a card\n\nThe task."}
	if redealt != "" {
		pr[sprint.FieldCapRedealt], pr[sprint.FieldTierNow] = redealt, "pro"
	}
	s.Work.Put(&sprint.Card{ID: "p1", Row: "s1", Col: sprint.Working, Fields: pr})
	s.Fleet.Put(&sprint.Card{ID: "c1", Row: sprint.FriendRow("bob"), Col: sprint.Working, Fields: map[string]string{
		"kind": "work", "primary": "p1", "stream": "s1", "attempt": "1", "gen": "1", "member": sprint.FriendRow("bob")}})
	return s, sprint.FinishReq{Sel: sprint.Sel{IDs: []string{"c1"}}, As: sprint.FriendRow("bob"), Gens: map[string]int{"c1": 1},
		Failed: true, Branch: "sprint/c1.g1.e15", Report: report}
}

// entryOf is the change a unit makes to the card id.
func entryOf(u sprint.Unit, id string) ntable.BatchMemberEntry {
	i := slices.IndexFunc(u.Changes, func(c sprint.Change) bool { return c.Entry.ID == id })
	if i < 0 {
		return ntable.BatchMemberEntry{}
	}
	return u.Changes[i].Entry
}

// The cap of a card's lane is its tier's: the row's when it names one, else the defaults,
// and the longest default for a tier not read; the row's caps ride on the beat's answer.
func TestALanesCapIsItsTiersFromTheRowOrTheDefaults(t *testing.T) {
	t.Parallel()
	row := map[string]time.Duration{"flash": 5 * time.Minute}
	for tier, want := range map[string]time.Duration{"flash": 5 * time.Minute, "pro": 45 * time.Minute, "heavy": 90 * time.Minute, "frontier": 150 * time.Minute, "": 150 * time.Minute, "odd": 150 * time.Minute} {
		assert.Equal(t, want, LaneCap(tier, row), "tier %q", tier)
	}
	assert.Equal(t, 15*time.Minute, LaneCap("flash", nil))

	caps, ok := ParseLaneCaps("BEAT OK row_mode=one-shot row_width=2 row_lane_caps=flash:10m,pro:1h")
	require.True(t, ok)
	assert.Equal(t, map[string]time.Duration{"flash": 10 * time.Minute, "pro": time.Hour}, caps)
	for _, bad := range []string{"BEAT OK row_width=2", "row_lane_caps=flash", "row_lane_caps=flash:0s", "row_lane_caps=:10m", "row_lane_caps=pro:soon"} {
		_, ok := ParseLaneCaps(bad)
		assert.False(t, ok, bad)
	}

	words := CappedWords(15*time.Minute, "flash", 2400*time.Millisecond)
	assert.Equal(t, "capped at 15m0s (tier flash, overrun 2s)", words)
	lc, ok := sprint.ParseLaneCap("friend bob HOLD: nova-friend lane 1 of bob finished card c1: " + words + ": the card's wall ...")
	require.True(t, ok, "the sprint reads the words the lane writes")
	assert.Equal(t, sprint.LaneCap{Cap: 15 * time.Minute, Overrun: 2 * time.Second, Tier: "flash"}, lc)
	assert.Equal(t, "capped at 2h30m0s (tier -, overrun 0s)", CappedWords(150*time.Minute, "", -time.Second))
	assert.Equal(t, "line 2\nline 3", LastLines("line 1\nline 2\nline 3\n", 2))
}

// printed is p, printed by the command a delivery runs, said to ctx's watch and tail: what
// RealExec does with each write, for a test harness that runs no command through it.
func printed(ctx context.Context, p []byte) {
	if len(p) == 0 {
		return
	}
	if seen, _ := ctx.Value(outputKey{}).(func()); seen != nil {
		seen()
	}
	if tail, _ := ctx.Value(tailKey{}).(func([]byte)); tail != nil {
		tail(p)
	}
}
