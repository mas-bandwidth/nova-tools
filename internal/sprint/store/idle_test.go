package store

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The idle alarm (docs/SPEC-SPRINT.md section 14, "The fleet is idle"; Rowan, 2026-10-04
// at 1:30 PM: the fleet ran 4 of 68 slots with 561 cards held, and nothing said why). When
// the fleet works under half its width for 5 minutes while cards wait, the tick traces each
// waiting card to the root of its chain and pushes one note to the coordinator naming the
// roots by the cards behind them; once an episode, and a clear note when it recovers. On
// the mem twin, injected clock, no socket; the model is tla/SprintRules.tla
// (AlarmOncePerEpisode, ClearFollowsAlarm).

func (h *harness) notesTo(typ string) []sprint.Note {
	h.t.Helper()
	all, _, err := h.m.NotesSince(h.ctx, "", 100000)
	require.NoError(h.t, err)
	var out []sprint.Note
	for _, n := range all {
		if n.Type == typ && n.Kind == sprint.Happened && n.To != "" {
			out = append(out, n)
		}
	}
	return out
}

func TestTheIdleAlarmNamesTheRootsOnceAnEpisode(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.st.IdleAlarm = true
	h.setup(0)
	// root is dropped: ten cards wait on it, the first three directly
	h.must(AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"root", "held-one"}}))
	h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"a", "b", "c"}, Needs: []string{"root"}}))
	h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"a1", "a2", "a3", "b1", "b2"}, Needs: []string{"a"}}))
	h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"c1", "c2"}, Needs: []string{"c"}}))
	// two more behind a sentinel never released
	h.must(AddStep(sprint.AddReq{Stream: "s3", IDs: []string{"gate"}, Sentinel: true, Held: true}))
	h.must(AddStep(sprint.AddReq{Stream: "s3", IDs: []string{"g1", "g2"}}))
	h.must(DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"root", "held-one"}}, Reason: "re-cut"}))
	h.startMachine()
	h.machine()
	assert.Empty(t, h.notesTo(sprint.NIdle), "under the window: nothing yet")
	h.tick(3 * time.Minute)
	h.machine()
	assert.Empty(t, h.notesTo(sprint.NIdle))
	h.tick(3 * time.Minute)
	h.machine()
	notes := h.notesTo(sprint.NIdle)
	require.Len(t, notes, 1, "one note past the window")
	n := notes[0]
	assert.Equal(t, h.st.Actor, n.To, "addressed to the coordinator")
	assert.True(t, strings.HasPrefix(n.What, "fleet 0/"), n.What)
	assert.Contains(t, n.What, "10 behind 3 drop-blocked judgments (oldest 6m0s)")
	assert.Contains(t, n.What, "2 behind sentinel gate (held)")
	for i := 0; i < 3; i++ {
		h.tick(time.Minute)
		h.machine()
	}
	assert.Len(t, h.notesTo(sprint.NIdle), 1, "once an episode")
	// the cards behind go: no card waits, the episode ends with a clear note
	h.must(DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"a", "b", "c", "a1", "a2", "a3", "b1", "b2", "c1", "c2", "g1", "g2", "gate"}}, Reason: "done with them"}))
	h.machine()
	clears := h.notesTo(sprint.NIdleCleared)
	require.Len(t, clears, 1, "a clear note when it recovers")
	assert.Contains(t, clears[0].What, "after 9m0s")
	h.machine()
	assert.Len(t, h.notesTo(sprint.NIdleCleared), 1)
}

// A chain whose root is in flight and held by a judgment names the card and the judgment.
func TestTheIdleTraceNamesACardAtItsBound(t *testing.T) {
	t.Parallel()
	h := flashAndPro(t)
	h.st.IdleAlarm = true
	h.addReady("s1", 1, briefOf("flash", ""))
	h.must(AddStep(sprint.AddReq{Stream: "s2", IDs: []string{"x", "y"}, Needs: []string{"s1-1"}}))
	h.startMachine()
	h.machine()
	h.boundOut("s1-1")
	require.Len(t, h.openOf(sprint.NBound), 1)
	s := h.snap()
	trace := sprint.TraceIdle(s, sprint.TickReq{})
	assert.Contains(t, trace, "2 behind s1-1 (a card reached its bound")
}
