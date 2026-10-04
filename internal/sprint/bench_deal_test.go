package sprint_test

import (
	"maps"
	"slices"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A card's BENCH line is honoured by the deal (docs/SPEC-SPRINT.md section 5, the deal):
// a card whose brief names a bench is dealt only to that member, or to the members its
// comma-separated line names, and while they are down or held it waits in ready with the
// no-stall rule saying why; a BENCH naming no fleet member is refused at add. A card that
// needs a tool one member alone has is dealt to that member or waits: it is never dealt
// to a member that fails it on the missing tool.

// benchBrief is a card's brief whose BENCH line names its bench.
func benchBrief(bench string) string {
	return "c: a card for one bench\nREPO: mas-bandwidth/nova-tools\nBENCH: " + bench + "\n\nThe task."
}

// addBenched admits cards of a stream, in id order: each brief is the bench it names, ""
// for a card with no BENCH line.
func addBenched(r *holdRig, stream string, briefs map[string]string) {
	r.t.Helper()
	var cards []sprint.CardAdd
	for _, id := range slices.Sorted(maps.Keys(briefs)) {
		c := sprint.CardAdd{ID: id}
		if b := briefs[id]; b != "" {
			c.Brief = benchBrief(b)
		}
		cards = append(cards, c)
	}
	r.must(store.AddStep(sprint.AddReq{Stream: stream, Cards: cards}))
}

// benchRow is the fleet row the primary's first attempt's work card is placed on, "-" when
// no member holds it (not dealt, or withdrawn).
func benchRow(s *sprint.Snapshot, primary string) string {
	if wc := s.Fleet.Placed(sprint.WorkCardID(primary, 1)); wc != nil && (wc.Col == sprint.Ready || wc.Col == sprint.Working) {
		return wc.Row
	}
	return "-"
}

func TestDealHonoursACardsBenchLine(t *testing.T) {
	t.Parallel()
	t.Run("dealt only to the member its bench names", func(t *testing.T) {
		t.Parallel()
		r := newHoldRig(t, 0, 0)
		addBenched(r, "s1", map[string]string{"s1-1": "m1", "s1-2": "m1", "s1-3": "m1", "s1-4": ""})
		r.tick()
		r.tick() // the second tick's level would even the queues: a bench card stays on its bench
		s := r.snap()
		for _, id := range []string{"s1-1", "s1-2", "s1-3"} {
			assert.Equal(t, sprint.Working, s.StateOf(id), "%s is dealt", id)
			assert.Equal(t, "m1", benchRow(s, id), "%s is on its bench m1, and on no member its BENCH line does not name", id)
		}
		assert.Equal(t, sprint.Working, s.StateOf("s1-4"), "a card with no BENCH line is dealt round the fleet as before")
		assert.Equal(t, "m2", benchRow(s, "s1-4"), "the deal still goes round the fleet for a card with no bench")
	})
	t.Run("a bench of two is dealt to the one that is up", func(t *testing.T) {
		t.Parallel()
		r := newHoldRig(t, 0, 0)
		r.hold(sprint.HoldReq{Names: []string{"m1"}, Reason: "its tool is being installed"})
		addBenched(r, "s1", map[string]string{"s1-1": "m1, m2"})
		r.tick()
		s := r.snap()
		assert.Equal(t, sprint.Working, s.StateOf("s1-1"))
		assert.Equal(t, "m2", benchRow(s, "s1-1"), "of the two its line names, the one up takes it")
	})
	t.Run("it waits while its bench is down and says why", func(t *testing.T) {
		t.Parallel()
		r := newHoldRig(t, 0, 0)
		r.hold(sprint.HoldReq{Names: []string{"m1"}, Reason: "its tool is being installed"})
		addBenched(r, "s1", map[string]string{"s1-1": "m1"})
		r.tick()
		s := r.snap()
		assert.Equal(t, sprint.Ready, s.StateOf("s1-1"), "it waits in ready while its bench is held")
		assert.Equal(t, "-", benchRow(s, "s1-1"), "no other member is dealt it")
		h, err := r.st.Held(r.ctx, "s1-1")
		require.NoError(t, err)
		assert.Contains(t, h.Why, "waits for its bench m1", "where and the dashboard say why it waits")

		r.hold(sprint.HoldReq{Names: []string{"m1"}, Release: true, Reason: "installed"})
		r.tick()
		assert.Equal(t, "m1", benchRow(r.snap(), "s1-1"), "its bench up, the tick deals it there")

		// its bench going down takes the card back: withdrawn, its primary ready again for
		// its bench, and never dealt to a member its line does not name
		res, err := r.st.Run(r.ctx, store.FleetStep(sprint.FleetReq{Op: "down", Member: "m1", Who: "coordinator"}))
		require.NoError(t, err)
		require.Len(t, res.Moved, 1)
		assert.Contains(t, res.Moved[0], "s1-1.w1 withdrawn", "its bench down, its card is taken back")
		assert.Contains(t, res.Moved[0], "s1-1 working -> ready", "its primary waits ready for its bench")
		assert.Equal(t, "-", benchRow(r.snap(), "s1-1"), "no member its BENCH line does not name holds it")
	})
	t.Run("a bench naming no member is refused at add", func(t *testing.T) {
		t.Parallel()
		r := newHoldRig(t, 0, 0)
		res, err := r.st.Run(r.ctx, store.AddStep(sprint.AddReq{Stream: "s1", Cards: []sprint.CardAdd{{ID: "s1-1", Brief: benchBrief("nobody")}}}))
		require.NoError(t, err)
		require.Len(t, res.Refused, 1, "a BENCH naming no fleet member is refused")
		assert.Equal(t, "s1-1", res.Refused[0].Key)
		assert.Contains(t, res.Refused[0].Why, "BENCH: nobody names no fleet member")
		assert.Contains(t, res.Refused[0].Why, "nova-sprint fleet", "the refusal carries its remedy")
		assert.Equal(t, sprint.State(""), r.snap().StateOf("s1-1"), "nothing was written")
	})
}
