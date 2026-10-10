package sprint_test

import (
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/hostload"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// quietLines is the QUIET lines a worker's view carries now: the fleet table's properties
// as view worker reads them, from the table's shape.
func (r *holdRig) quietLines() []string {
	r.t.Helper()
	shapes, err := r.st.B.Shapes(r.ctx, []string{r.st.Names.Table(sprint.Fleet)})
	require.NoError(r.t, err)
	return sprint.QuietLines(shapes[0].Props, r.st.Now())
}

// quietLog is the log's lines of quiets begun and ended, as written.
func (r *holdRig) quietLog() []string {
	r.t.Helper()
	lines, err := r.st.Log(r.ctx)
	require.NoError(r.t, err)
	var out []string
	for _, l := range lines {
		if l.Note != nil && (l.Note.Type == sprint.NQuiet || l.Note.Type == sprint.NQuietEnded) {
			out = append(out, l.Note.What)
		}
	}
	return out
}

// TestFleetQuietDealsNothingAndTellsWorkersUntilItEnds: fleet quiet holds a member out of
// the deal until its time while its dealt work finishes, every worker's view carries the
// QUIET line, and at the time the deal resumes by itself and the log says the quiet ended;
// --end ends one early (docs/SPEC-SPRINT.md section 5, fleet-quiet-machine-b.w7).
func TestFleetQuietDealsNothingAndTellsWorkersUntilItEnds(t *testing.T) {
	t.Parallel()
	t.Run("until its time", func(t *testing.T) {
		t.Parallel()
		r := newHoldRig(t, 12, 0)
		r.tick()
		working := r.takeOne("m1")
		until := r.st.Now().Add(11 * time.Minute)
		r.must(store.FleetStep(sprint.FleetReq{Op: "quiet", Member: "m1", Until: until, Reason: "load 64: the macOS CI legs time out", Who: "coordinator"}))
		line := "QUIET m1 until " + until.UTC().Format(time.RFC3339) + ": load 64: the macOS CI legs time out; run no go build or test there"
		assert.Equal(t, []string{line}, r.quietLines(), "every worker's view carries the QUIET line")

		// its dealt work finishes; nothing new is dealt or levelled to it
		s := r.snap()
		ready := onRow(s, "m1", "s1", sprint.Ready)
		gen := s.Fleet.Card(working).Int("gen")
		r.must(store.FinishStep(sprint.FinishReq{As: "m1", Sel: sprint.Sel{IDs: []string{working}}, Gens: map[string]int{working: gen}, Who: "m1"}))
		for range ready {
			id := r.takeOne("m1")
			s = r.snap()
			r.must(store.FinishStep(sprint.FinishReq{As: "m1", Sel: sprint.Sel{IDs: []string{id}}, Gens: map[string]int{id: s.Fleet.Card(id).Int("gen")}, Who: "m1"}))
		}
		for range 5 {
			r.tick()
			assert.Empty(t, onRow(r.snap(), "m1", "s1", sprint.Ready, sprint.Working), "a quiet member is dealt nothing")
		}
		assert.NotEmpty(t, onRow(r.snap(), "m2", "s1", sprint.Ready, sprint.Working), "the other member is dealt as before")
		assert.Equal(t, sprint.Ready, r.snap().StateOf("s1-12"), "the deal holds cards back rather than give them to the quiet member")
		for _, f := range sprint.Unheld(sprint.HeldState{Snap: r.snap(), Running: true}, r.st.Now()) {
			assert.NotContains(t, f.Subject, "s1-", "a card waiting behind the quiet is held, not stalled: %s", f)
		}
		assert.Equal(t, []string{line}, r.quietLines(), "the line stands until the time")

		// at its time the deal resumes by itself, and the log says so once
		r.mu.Lock()
		r.now = until
		r.mu.Unlock()
		assert.Empty(t, r.quietLines(), "the line is gone at the time")
		r.tick()
		r.tick()
		assert.NotEmpty(t, onRow(r.snap(), "m1", "s1", sprint.Ready, sprint.Working), "the deal resumes by itself")
		assert.Equal(t, []string{
			"m1 quiet until " + until.UTC().Format(time.RFC3339) + ": load 64: the macOS CI legs time out; no card is dealt to it, its dealt work finishes; run no go build or test there",
			"m1 quiet ended at its time " + until.UTC().Format(time.RFC3339) + ": load 64: the macOS CI legs time out; the deal gives it cards again",
		}, r.quietLog(), "the log holds the quiet and its end, once")
	})
	t.Run("ended early", func(t *testing.T) {
		t.Parallel()
		r := newHoldRig(t, 12, 0)
		until := r.st.Now().Add(time.Hour)
		r.must(store.FleetStep(sprint.FleetReq{Op: "quiet", Member: "m1", Until: until, Reason: "the gocache trim", Who: "coordinator"}))
		r.tick()
		assert.Empty(t, onRow(r.snap(), "m1", "s1", sprint.Ready, sprint.Working), "a quiet member is dealt nothing")
		r.must(store.FleetStep(sprint.FleetReq{Op: "quiet", Member: "m1", End: true, Who: "coordinator"}))
		assert.Empty(t, r.quietLines(), "--end takes the line off at once")
		r.tick()
		assert.NotEmpty(t, onRow(r.snap(), "m1", "s1", sprint.Ready, sprint.Working), "--end resumes the deal")
		assert.Len(t, r.quietLog(), 2)
		assert.Contains(t, r.quietLog()[1], "m1 quiet ended early by coordinator")
	})
	t.Run("refusals", func(t *testing.T) {
		t.Parallel()
		r := newHoldRig(t, 1, 0)
		for _, c := range []struct {
			name string
			req  sprint.FleetReq
			why  string
		}{
			{"no member", sprint.FleetReq{Op: "quiet", Member: "nobody", Until: r.st.Now().Add(time.Hour), Reason: "x"}, "names no fleet member"},
			{"no reason", sprint.FleetReq{Op: "quiet", Member: "m1", Until: r.st.Now().Add(time.Hour)}, "--reason wants"},
			{"past", sprint.FleetReq{Op: "quiet", Member: "m1", Until: r.st.Now(), Reason: "x"}, "a time after now"},
			{"end not quiet", sprint.FleetReq{Op: "quiet", Member: "m1", End: true}, "m1 is not quiet"},
		} {
			t.Run(c.name, func(t *testing.T) {
				res, err := r.st.Run(r.ctx, store.FleetStep(c.req))
				require.NoError(t, err)
				require.Len(t, res.Refused, 1)
				assert.Contains(t, res.Refused[0].Why, c.why)
			})
		}
		assert.Empty(t, r.quietLog(), "a refusal writes nothing")
	})
}

// TestAMemberWhoseBeatSaysNoRoomIsDealtNothing: a member whose fresh beat says it starts no
// card (fleet beat --no-room: its free disk under its floor) is dealt nothing and levelled
// nothing, the deal's refusal names it with its word, the snapshot carries the word, and the
// first beat without it puts the member back in the deal (fault 2 of 2026-10-10: hetzner at
// 0.0 GiB was dealt 872 cards it handed straight back refused at staging).
func TestAMemberWhoseBeatSaysNoRoomIsDealtNothing(t *testing.T) {
	t.Parallel()
	r := newHoldRig(t, 12, 0)
	why := "free disk on the volume of /slots is 0.0 GiB, under the floor of 10 GiB"
	full := func() {
		r.t.Helper()
		r.mu.Lock()
		r.now = r.now.Add(time.Second)
		r.mu.Unlock()
		r.beat()
		zero := 0.0
		_, err := r.st.BeatOwing(r.ctx, "m1", &zero, hostload.Source{}, nil, why)
		require.NoError(t, err)
		_, err = r.st.Tick(r.ctx)
		require.NoError(t, err)
	}
	for range 4 {
		full()
		assert.Empty(t, onRow(r.snap(), "m1", "s1", sprint.Ready, sprint.Working), "a member that starts no card is dealt nothing")
	}
	s := r.snap()
	assert.Equal(t, why, s.NoRoom["m1"], "the snapshot carries the member's word")
	assert.NotContains(t, s.NoRoom, "m2")
	assert.NotEmpty(t, onRow(s, "m2", "s1", sprint.Ready, sprint.Working), "the other member is dealt as before")

	// a beat without the word clears it: the member is dealt again
	r.tick()
	r.tick()
	s = r.snap()
	assert.Empty(t, s.NoRoom["m1"])
	assert.NotEmpty(t, onRow(s, "m1", "s1", sprint.Ready, sprint.Working), "the deal gives it cards again")
}

// TestAReaderWhoseBeatSaysNoRoomHasNoRoom: a reader whose queue carries --no-room has its
// word on the snapshot while its beat is fresh, and none once a beat without it lands or the
// beat goes stale; the ask's room for it is none (readerRooms), so it is asked nothing.
func TestAReaderWhoseBeatSaysNoRoomHasNoRoom(t *testing.T) {
	t.Parallel()
	r := newHoldRig(t, 1, 0)
	why := "free disk on the volume of /slots is 2.0 GiB, under the floor of 10 GiB"
	wrote, err := r.st.ReaderBeat(r.ctx, "reader-a", why)
	require.NoError(t, err)
	require.True(t, wrote)
	s := r.snap()
	assert.Equal(t, why, s.NoRoom["reader-a"])
	assert.NotContains(t, s.NoRoom, "reader-b")
	assert.Equal(t, sprint.ReaderUp, s.ReaderStates["reader-a"], "a reader under its floor is up: it still returns and finishes its reads")

	// stale: its word no longer counts
	r.mu.Lock()
	r.now = r.now.Add(2 * sprint.BeatDeadline)
	r.mu.Unlock()
	assert.NotContains(t, r.snap().NoRoom, "reader-a")

	_, err = r.st.ReaderBeat(r.ctx, "reader-a", why)
	require.NoError(t, err)
	_, err = r.st.ReaderBeat(r.ctx, "reader-a", "")
	require.NoError(t, err)
	assert.NotContains(t, r.snap().NoRoom, "reader-a", "a beat without the word clears it")
}
