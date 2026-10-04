package store

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The backlog alarms (docs/SPEC-SPRINT.md section 14, "The backlog alarms"; the card
// verb-seat-install-pushb: the review and merging backlogs were view items only, so
// inbox --wait --push seat never carried them). When the oldest result in review, or
// merging, has waited past its alarm (the sprint's setting, else 30 minutes), the tick
// pushes the coordinator one note; once an episode, and one clear note when it ends.
// On the mem twin, injected clock, no socket; the episode is the idle alarm's
// (tla/SprintRules.tla, AlarmOncePerEpisode, ClearFollowsAlarm).
func TestTheBacklogAlarmsTellTheCoordinatorOnceAnEpisode(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.st.BacklogAlarm = true
	h.setup(1)
	h.toReview()
	h.startMachine()
	h.machine()
	assert.Empty(t, h.notesTo(sprint.NBacklogReview), "a result just in: nothing")
	h.tick(29 * time.Minute)
	h.machine()
	assert.Empty(t, h.notesTo(sprint.NBacklogReview), "under the alarm: nothing yet")
	h.tick(2 * time.Minute)
	h.machine()
	notes := h.notesTo(sprint.NBacklogReview)
	require.Len(t, notes, 1, "one note past the alarm")
	n := notes[0]
	assert.Equal(t, h.st.Actor, n.To, "addressed to the coordinator")
	assert.Equal(t, "1 in review, the oldest result waiting 31m0s (stream s1), past the alarm of 30m0s", n.What)
	assert.Equal(t, "run: nova-sprint ask --stream s1", n.Hint)
	for i := 0; i < 3; i++ {
		h.tick(time.Minute)
		h.machine()
	}
	assert.Len(t, h.notesTo(sprint.NBacklogReview), 1, "once an episode")

	// accepted: review is empty, its episode ends with one clear; the same result now
	// waits in merging, past the alarm at once
	for _, rc := range h.snap().Readers.Of("s1-1") {
		h.must(ReadStep(sprint.ReadReq{As: rc.F("reader"), Verdict: "ok", Sel: sprint.Sel{IDs: []string{rc.ID}}}))
	}
	h.must(AcceptStep(sprint.AcceptReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}}))
	h.machine()
	clears := h.notesTo(sprint.NAlarmCleared)
	require.Len(t, clears, 1, "a clear note when the review backlog ends")
	assert.Equal(t, sprint.NBacklogReview+": 0 in review, after 3m0s", clears[0].What)
	assert.Equal(t, h.st.Actor, clears[0].To)
	merging := h.notesTo(sprint.NBacklogMerging)
	require.Len(t, merging, 1, "the merging backlog's own episode")
	assert.True(t, strings.HasPrefix(merging[0].What, "1 in merging, the oldest result waiting 34m0s (stream s1)"), merging[0].What)
	assert.Equal(t, "run: nova-sprint land --stream s1", merging[0].Hint)
	h.machine()
	assert.Len(t, h.notesTo(sprint.NAlarmCleared), 1, "a clear once")

	// the alarm's threshold is the sprint's setting: off ends the episode with a clear
	h.must(SetStep(sprint.SetReq{AlarmMergingAge: sprint.AlarmOff, Who: h.st.Actor}))
	h.machine()
	clears = h.notesTo(sprint.NAlarmCleared)
	require.Len(t, clears, 2)
	assert.Equal(t, sprint.NBacklogMerging+": its alarm is off", clears[1].What)
	// a longer alarm: the result is under it, so nothing; past it, one note again
	h.must(SetStep(sprint.SetReq{AlarmMergingAge: "1h", Who: h.st.Actor}))
	h.machine()
	assert.Len(t, h.notesTo(sprint.NBacklogMerging), 1, "under the hour: nothing")
	h.tick(30 * time.Minute)
	h.machine()
	merging = h.notesTo(sprint.NBacklogMerging)
	require.Len(t, merging, 2, "a second episode past the new alarm")
	assert.True(t, strings.HasSuffix(merging[1].What, "past the alarm of 1h0m0s"), merging[1].What)
	assert.Len(t, h.notesTo(sprint.NBacklogReview), 1)
}

// The alarm's setting is refused whole for a value it does not take.
func TestTheBacklogAlarmSettingIsRefusedForANonDuration(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(1)
	res := h.run(SetStep(sprint.SetReq{AlarmReviewAge: "soon", Who: h.st.Actor}))
	require.NotEmpty(t, res.Refused)
	assert.Contains(t, res.Refused[0].Why, "--alarm-review-age wants a duration above zero")
	h.must(SetStep(sprint.SetReq{AlarmReviewAge: sprint.ReadTierDefault, Who: h.st.Actor}))
	age, on := h.snap().BacklogAge(sprint.Review)
	assert.True(t, on)
	assert.Equal(t, sprint.BacklogAgeDefault, age)
}
