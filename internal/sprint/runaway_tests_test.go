package sprint_test

import (
	"encoding/json"
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
// count of live processes whose name ends in .test and the oldest parent pid.
// The store's beat has no field for the count, so the test writes it on the
// record the beat just stored (fleet beat --tests, the beat agent's count).
func (r *alarmRig) beatTests(n, parent int) {
	r.t.Helper()
	r.mu.Lock()
	r.now = r.now.Add(time.Second)
	r.mu.Unlock()
	require.NoError(r.t, r.st.BeatReaders(r.ctx))
	zero := 0.0
	b, err := r.st.Beat(r.ctx, "m1", &zero, hostload.Source{})
	require.NoError(r.t, err)
	b.Tests = &n
	b.TestParent = parent
	raw, err := json.Marshal(b)
	require.NoError(r.t, err)
	require.NoError(r.t, r.m.SetKey(r.ctx, "beat:m1", string(raw)))
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

// A beat that counts test processes over the threshold (4 times the member's
// width, unless the sprint set tests_alarm) raises one judgment naming the
// member, the count and the oldest parent pid. A second beat, still over,
// raises none. A beat under half the threshold ends the episode.
// docs/SPEC-SPRINT.md, fleet-test-process-alarm-b.w8.
func TestFleetBeatRunawayTestsRaisesOneAlarm(t *testing.T) {
	t.Parallel()
	r := newAlarmRig(t)
	r.must(store.FleetStep(sprint.FleetReq{Op: "up", Member: "m1", Width: 4}))
	_, _, _, err := r.st.SetMachine(r.ctx, true)
	require.NoError(t, err)

	// width 4, no tests_alarm set: the threshold is 16 and half of it is 8.
	r.beatTests(3, 100)
	raised, cleared := r.runawayEpisodes()
	require.Equal(t, [2]int{0, 0}, [2]int{raised, cleared}, "under the threshold")

	r.beatTests(17, 4242)
	raised, cleared = r.runawayEpisodes()
	require.Equal(t, [2]int{1, 0}, [2]int{raised, cleared}, "over the threshold: one judgment")
	j := r.runawayJudgment()
	assert.Equal(t, sprint.MemberSubject("m1"), j.Stream)
	assert.Contains(t, j.What, "runaway test processes on m1: 17")
	assert.Contains(t, j.What, "oldest parent pid 4242")

	r.beatTests(20, 4242)
	raised, cleared = r.runawayEpisodes()
	require.Equal(t, [2]int{1, 0}, [2]int{raised, cleared}, "a second beat raises none")
	assert.Contains(t, r.runawayJudgment().What, "runaway test processes on m1: 20")
	assert.Contains(t, r.runawayJudgment().What, "oldest parent pid 4242")

	r.beatTests(7, 4242)
	raised, cleared = r.runawayEpisodes()
	require.Equal(t, [2]int{1, 1}, [2]int{raised, cleared}, "a low beat ends the episode")
	require.Zero(t, r.openOf(sprint.NRunawayTests))
}

// The threshold scales by the width of the member or friend the beat is of, not the
// default: a friend's configured seat width first, else the width her beat reported,
// else DefaultWidth.
// docs/SPEC-SPRINT.md, fleet-test-process-alarm-b.w8.
func TestTestsAlarmScalesByFriendWidth(t *testing.T) {
	t.Parallel()
	seat := &sprint.Snapshot{Friends: []sprint.FriendSeat{{Name: "amy", Width: 3}}}
	assert.Equal(t, 12, seat.TestsAlarm("amy"), "a friend's configured width")
	assert.Equal(t, 12, seat.TestsAlarmWith("amy", 7), "the seat's width wins over the reported one")
	reported := &sprint.Snapshot{}
	assert.Equal(t, 8, reported.TestsAlarmWith("amy", 2), "the width her beat reported")
	assert.Equal(t, 4*sprint.DefaultWidth, reported.TestsAlarm("amy"), "the default width")
}
