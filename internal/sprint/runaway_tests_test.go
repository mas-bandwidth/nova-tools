package sprint_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/hostload"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// beatTests is one beat of m1 on the twin, the clock a second on, carrying a
// count of live processes whose name ends in ".test" and the parent pid that has
// been alive longest among them. The store's beat has no field for the count, so
// the test stamps it on the record the beat just wrote (fleet beat --tests, or
// the beat agent's own count).
func (r *alarmRig) beatTests(n, parent int) {
	r.t.Helper()
	r.mu.Lock()
	r.now = r.now.Add(time.Second)
	r.mu.Unlock()
	require.NoError(r.t, r.st.BeatReaders(r.ctx))
	zero := 0.0
	_, err := r.st.Beat(r.ctx, "m1", &zero, hostload.Source{})
	require.NoError(r.t, err)
	raw, found, err := r.m.GetKey(r.ctx, sprint.BeatRecordKey("m1"))
	require.NoError(r.t, err)
	require.True(r.t, found, "the beat record the count is stamped on")
	stamped, err := sprint.StampTests(raw, n, parent)
	require.NoError(r.t, err)
	require.NoError(r.t, r.m.SetKey(r.ctx, sprint.BeatRecordKey("m1"), stamped))
	_, err = r.st.Tick(r.ctx)
	require.NoError(r.t, err)
}

// runawayEpisodes is how many runaway-test judgments were written and how many
// cleared notes named them.
func (r *alarmRig) runawayEpisodes() (raised, cleared int) {
	r.t.Helper()
	notes, _, err := r.m.NotesSince(r.ctx, "", 100000)
	require.NoError(r.t, err)
	for _, n := range notes {
		switch {
		case n.Kind == sprint.Judgment && n.Type == sprint.NRunawayTests:
			raised++
		case n.Kind == sprint.Happened && n.Type == sprint.NAlarmCleared && strings.HasPrefix(n.What, sprint.NRunawayTests+":"):
			assert.Equal(r.t, "coordinator", n.To, "a cleared note is the coordinator's: %+v", n)
			cleared++
		}
	}
	return raised, cleared
}

// runawayJudgment is the open runaway-test judgment.
func (r *alarmRig) runawayJudgment() sprint.Note {
	r.t.Helper()
	for _, o := range r.snap().Open {
		if o.Note.Type == sprint.NRunawayTests && o.Note.Kind == sprint.Judgment {
			return o.Note
		}
	}
	require.FailNow(r.t, "no open judgment of type "+sprint.NRunawayTests)
	return sprint.Note{}
}

// A beat that counts live test processes over the threshold raises one judgment
// naming the member, the count and the parent pid that has been alive longest. A
// second beat, still over, raises none and updates the line. A beat under half
// the threshold ends the episode with one cleared note. The threshold is the
// sprint's tests_alarm, else four times the member's width, and a width of 4
// makes it 16 with half at 8. docs/SPEC-SPRINT.md,
// fleet-test-process-alarm-b.w4.
func TestFleetBeatRunawayTestsRaisesOneAlarm(t *testing.T) {
	t.Parallel()
	r := newAlarmRig(t)
	r.must(store.FleetStep(sprint.FleetReq{Op: "up", Member: "m1", Width: 4}))
	_, _, _, err := r.st.SetMachine(r.ctx, true)
	require.NoError(t, err)

	r.beatTests(3, 100)
	raised, cleared := r.runawayEpisodes()
	require.Equal(t, [2]int{0, 0}, [2]int{raised, cleared}, "under the threshold")

	r.beatTests(17, 4242)
	raised, cleared = r.runawayEpisodes()
	require.Equal(t, [2]int{1, 0}, [2]int{raised, cleared}, "over the threshold: one judgment")
	j := r.runawayJudgment()
	assert.Equal(t, sprint.MemberSubject("m1"), j.Stream, "the judgment is the member's")
	assert.Contains(t, j.What, "runaway test processes on m1: 17")
	assert.Contains(t, j.What, "oldest parent pid 4242")

	r.beatTests(20, 4242)
	raised, cleared = r.runawayEpisodes()
	require.Equal(t, [2]int{1, 0}, [2]int{raised, cleared}, "a second beat raises none")
	assert.Contains(t, r.runawayJudgment().What, "runaway test processes on m1: 20", "the open judgment says the latest count")
	assert.Contains(t, r.runawayJudgment().What, "oldest parent pid 4242")

	r.beatTests(7, 4242)
	raised, cleared = r.runawayEpisodes()
	require.Equal(t, [2]int{1, 1}, [2]int{raised, cleared}, "a low beat ends the episode")
	require.Zero(t, r.openOf(sprint.NRunawayTests))

	// a stale beat is no reading: the episode stays as it was
	r.beatTests(17, 4242)
	raised, cleared = r.runawayEpisodes()
	require.Equal(t, [2]int{2, 1}, [2]int{raised, cleared}, "a second episode")
	r.mu.Lock()
	r.now = r.now.Add(sprint.BeatDeadline + time.Second)
	r.mu.Unlock()
	_, err = r.st.Tick(r.ctx)
	require.NoError(t, err)
	raised, cleared = r.runawayEpisodes()
	require.Equal(t, [2]int{2, 2}, [2]int{raised, cleared}, "a stale beat closes the episode")
}
