package sprint_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/hostload"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every status transition of a friend or a fleet member raises one judgment, pushed to the
// seat inbox (docs/SPEC-SPRINT.md section 8, "Status transitions"; the owner, 2026-10-06
// 2:55 PM ET: "make the machine prompt you when a friend changes their status"). On the twin
// store with a fake clock and no sockets.

// transitionRig is a running sprint on the twin: the coordinator holds the seat, member m1
// (width 2) beats, and the friend amy is on the roster.
type transitionRig struct {
	t   *testing.T
	m   *store.Mem
	st  *store.Store
	ctx context.Context
	mu  sync.Mutex
	now time.Time
	// member says m1 beats with every tick
	member bool
}

var transitionT0 = time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)

func newTransitionRig(t *testing.T) *transitionRig {
	t.Helper()
	r := &transitionRig{t: t, m: store.NewMem(), ctx: context.Background(), now: transitionT0, member: true}
	r.st = r.open()
	require.NoError(t, r.st.Init(r.ctx))
	require.NoError(t, r.m.SetCoordinator(r.ctx, "coordinator"))
	_, _, _, err := r.st.SyncFriends(r.ctx, []store.FriendSpec{{Name: "amy", Width: 2, Class: "pro"}})
	require.NoError(t, err)
	r.beat()
	res, err := r.st.Run(r.ctx, store.FleetStep(sprint.FleetReq{Op: "up", Member: "m1", Width: 2}))
	require.NoError(t, err)
	require.Empty(t, res.Refused)
	_, _, _, err = r.st.SetMachine(r.ctx, true)
	require.NoError(t, err)
	return r
}

// open is a store over the rig's twin: a restart of the server is a new one.
func (r *transitionRig) open() *store.Store {
	n := 0
	return &store.Store{B: r.m, Names: sprint.Names{Prefix: "t-"}, Actor: "coordinator",
		Now:   func() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return r.now },
		NewID: func() string { r.mu.Lock(); defer r.mu.Unlock(); n++; return fmt.Sprintf("%d-%d", r.now.Unix(), n) },
		Sleep: func(time.Duration) {}}
}

func (r *transitionRig) beat() {
	r.t.Helper()
	if !r.member {
		return
	}
	zero := 0.0
	_, err := r.st.Beat(r.ctx, "m1", &zero, hostload.Source{})
	require.NoError(r.t, err)
}

// tick moves the clock d, beats m1 (while it beats) and runs one tick.
func (r *transitionRig) tick(d time.Duration) {
	r.t.Helper()
	r.mu.Lock()
	r.now = r.now.Add(d)
	r.mu.Unlock()
	r.beat()
	_, err := r.st.Tick(r.ctx)
	require.NoError(r.t, err)
}

// daemon is amy's daemon's beat: the build it runs, its start, and the present it sent.
func (r *transitionRig) daemon(build string, started, present time.Time) {
	r.t.Helper()
	_, err := r.st.FriendBeatReport(r.ctx, "amy", sprint.FriendReport{Build: build, Started: started, Present: present}, nil)
	require.NoError(r.t, err)
}

// answered is a wake ping amy's session answered, now: she is up on it.
func (r *transitionRig) answered() {
	r.t.Helper()
	_, _, _, err := r.st.FriendHealth(r.ctx, "amy", "coordinator", sprint.FriendHealth{State: sprint.Up, Seen: r.st.Now(), Generation: sprint.FirstSeatGeneration}, "")
	require.NoError(r.t, err)
}

// statusNotes is every status note open (a judgment, or one answered by rule and kept).
func (r *transitionRig) statusNotes(row string) []sprint.Note {
	r.t.Helper()
	open, err := r.m.OpenNotes(r.ctx)
	require.NoError(r.t, err)
	seen := map[string]bool{}
	var out []sprint.Note
	for _, o := range open {
		if o.Note.Type == sprint.NStatus && o.Subject() == row && !seen[o.Note.ID] {
			seen[o.Note.ID] = true
			out = append(out, o.Note)
		}
	}
	return out
}

func TestAFriendComingUpPushesTheFourStepsToTheSeat(t *testing.T) {
	t.Parallel()
	r := newTransitionRig(t)
	row := sprint.FriendRow("amy")
	r.tick(time.Second)
	assert.Empty(t, r.statusNotes(row), "the first sight of her records her status and raises nothing")

	// her daemon starts on the current build, sends her the present, and her session answers
	current := buildinfo.Version("")
	start := r.st.Now()
	r.daemon(current, start, start)
	r.answered()
	r.tick(time.Second)
	notes := r.statusNotes(row)
	require.Len(t, notes, 1, "her coming up raises one judgment")
	n := notes[0]
	assert.Equal(t, sprint.Judgment, n.Kind, "a judgment, pushed to the seat inbox as every judgment is")
	at := r.st.Now().UTC().Format(time.RFC3339)
	assert.Equal(t, "friend amy is up (was down) at "+at+", transition 1, on session pong 1s ago; bring her up in four steps: "+
		"1 update: her daemon runs "+current+", the current build is "+current+" (done); "+
		"2 check: daemon beating (1s ago), harness checked by her daemon's start at "+start.Format(time.RFC3339)+", presence session pong 1s ago (done); "+
		"3 snap to present: her daemon sent the present on its start at "+start.Format(time.RFC3339)+" (done); "+
		"4 into the sprint: not held, evidence session pong 1s ago, no take within 10m0s; run: nova-sprint where, and nova-friend ping --as coordinator --to amy --wake (to do)",
		n.What)
	assert.Equal(t, []string{"ack", "wait"}, n.Decisions, "the verbs are in the text; ack or wait answers it")
	assert.Equal(t, []string{row}, n.Primaries)
	assert.NotEmpty(t, n.Alias, "aliased j<n> as every judgment")

	// a daemon on an old build that sent no present: the steps say so, and what to run
	r2 := newTransitionRig(t)
	r2.tick(time.Second)
	r2.daemon("2030-01-01T00:00:00Z-0123456789ab", r2.st.Now().Add(-time.Hour), time.Time{})
	r2.answered()
	r2.tick(time.Second)
	notes = r2.statusNotes(row)
	require.Len(t, notes, 1)
	assert.Contains(t, notes[0].What, "1 update: her daemon runs 2030-01-01T00:00:00Z-0123456789ab, the current build is "+current+"; run: nova-update, then launchctl kickstart -k gui/$(id -u)/com.nova.friend-amy (to do)")
	assert.Contains(t, notes[0].What, "3 snap to present: her daemon sent no present since its start; run: nova-bus send --to amy")
}

func TestATransitionRaisesExactlyOneJudgment(t *testing.T) {
	t.Parallel()
	r := newTransitionRig(t)
	row := sprint.FriendRow("amy")
	r.tick(time.Second)
	r.answered()
	for range 10 {
		r.tick(time.Second)
	}
	notes := r.statusNotes(row)
	require.Len(t, notes, 1, "ten ticks over one transition, one judgment")
	assert.Contains(t, notes[0].What, "transition 1")
	assert.Empty(t, r.statusNotes("m1"), "m1 beat every tick: no transition, no judgment")

	// held by the coordinator: the next transition closes the first and raises one more,
	// answered by rule only when the tick answers by rule; ten ticks, still one
	_, err := r.st.Hold(r.ctx, sprint.HoldReq{Names: []string{"amy"}, Who: "coordinator", Reason: "test"})
	require.NoError(t, err)
	for range 10 {
		r.tick(time.Second)
	}
	notes = r.statusNotes(row)
	require.Len(t, notes, 1, "the hold's judgment alone: the one before is closed")
	assert.True(t, strings.HasPrefix(notes[0].What, "friend amy is held (was up) at "), notes[0].What)
	assert.Contains(t, notes[0].What, "transition 2")
}

func TestAFleetMemberGoingDownNamesItsLastBeat(t *testing.T) {
	t.Parallel()
	r := newTransitionRig(t)
	r.tick(time.Second)
	assert.Empty(t, r.statusNotes("m1"))
	last := r.st.Now().UTC().Format(time.RFC3339)
	r.member = false // its beat stops
	r.tick(sprint.MissedBeatsDown*sprint.BeatDeadline + time.Second)
	notes := r.statusNotes("m1")
	require.Len(t, notes, 1)
	at := r.st.Now().UTC().Format(time.RFC3339)
	assert.Equal(t, "fleet member m1 is down (was up) at "+at+", transition 1: its last beat "+last+" (46s ago, 3 beat windows of 15s missed), width 2; "+
		"its unfinished cards were dealt round the members up; bring it back: its beat on m1 (nova-sprint fleet beat m1) has stopped, start its member loop again; "+
		"or hold it: nova-sprint hold m1 --reason <text>", notes[0].What)
	assert.Equal(t, sprint.Judgment, notes[0].Kind)
	for range 10 {
		r.tick(time.Second)
	}
	assert.Len(t, r.statusNotes("m1"), 1, "down for ten more ticks: still the one judgment")

	// it beats again: up, the down's judgment closed, the up's raised with its beat and width
	r.member = true
	r.tick(time.Second)
	notes = r.statusNotes("m1")
	require.Len(t, notes, 1)
	assert.True(t, strings.HasPrefix(notes[0].What, "fleet member m1 is up (was down) at "+r.st.Now().UTC().Format(time.RFC3339)+", transition 2: its last beat "), notes[0].What)
	assert.Contains(t, notes[0].What, "width 2")
}

func TestARestartDoesNotReplayTransitions(t *testing.T) {
	t.Parallel()
	r := newTransitionRig(t)
	row := sprint.FriendRow("amy")
	r.tick(time.Second)
	r.daemon("", r.st.Now(), time.Time{})
	r.answered()
	r.tick(time.Second)
	require.Len(t, r.statusNotes(row), 1)
	first := r.statusNotes(row)[0].ID

	// the server restarts: a new store over the same records reads the transitions back
	r.st = r.open()
	for range 5 {
		r.tick(time.Second)
	}
	notes := r.statusNotes(row)
	require.Len(t, notes, 1, "a restart replays no transition")
	assert.Equal(t, first, notes[0].ID)

	// her daemon restarts: a new generation, one judgment, the one before closed
	r.daemon("", r.st.Now(), time.Time{})
	r.answered()
	r.tick(time.Second)
	r.st = r.open()
	r.tick(time.Second)
	notes = r.statusNotes(row)
	require.Len(t, notes, 1)
	assert.NotEqual(t, first, notes[0].ID)
	assert.Contains(t, notes[0].What, "friend amy's daemon started again at ")
	assert.Contains(t, notes[0].What, "transition 2")
}

// With the machine's own answers (run --answer-rules), a friend come up whose four steps
// are all done is answered by rule at the first tick that finds them done, and a hold, the
// coordinator's own act, is answered by rule when it is raised.
func TestAStatusJudgmentWhoseStepsAreDoneIsAnsweredByRule(t *testing.T) {
	t.Parallel()
	r := newTransitionRig(t)
	r.st.AnswerRules = true
	row := sprint.FriendRow("amy")
	res, err := r.st.Run(r.ctx, store.AddStep(sprint.AddReq{Stream: "f1", Cards: []sprint.CardAdd{{ID: "f1-1", Brief: friendsBrief("only friend amy")}}}))
	require.NoError(t, err)
	require.Empty(t, res.Refused)
	r.tick(time.Second)
	start := r.st.Now()
	r.daemon(buildinfo.Version(""), start, start)
	r.answered()
	r.tick(time.Second)
	notes := r.statusNotes(row)
	require.Len(t, notes, 1)
	assert.Equal(t, sprint.Judgment, notes[0].Kind, "no take yet: the fourth step is to do")
	assert.Contains(t, notes[0].What, "(to do)")

	// her card is on her row: she takes it, and the next tick answers the judgment by rule
	s, err := r.st.Load(r.ctx, store.All, nil)
	require.NoError(t, err)
	ready := s.Fleet.Cell(row, sprint.Ready)
	require.NotEmpty(t, ready, "the deal gave her the card")
	wc := ready[0]
	res, err = r.st.Run(r.ctx, store.TakeStep(sprint.TakeReq{Sel: sprint.Sel{IDs: []string{wc.ID}}, As: row, Gens: map[string]int{wc.ID: wc.Int("gen")}, Who: row}))
	require.NoError(t, err)
	require.Empty(t, res.Refused)
	// her daemon's beat names the card running, as it does for a card she started
	_, err = r.st.FriendBeatReport(r.ctx, "amy", sprint.FriendReport{Running: []string{wc.ID}, Build: buildinfo.Version(""), Started: start, Present: start}, nil)
	require.NoError(t, err)
	r.tick(time.Second)
	assert.Empty(t, r.statusNotes(row), "every step done: answered by rule and closed")

	// held: the coordinator's own act, answered by rule as it is raised
	_, err = r.st.Hold(r.ctx, sprint.HoldReq{Names: []string{"amy"}, Who: "coordinator", Reason: "test"})
	require.NoError(t, err)
	r.tick(time.Second)
	notes = r.statusNotes(row)
	require.Len(t, notes, 1)
	assert.Equal(t, sprint.Acknowledged, notes[0].Kind)
	assert.True(t, strings.HasPrefix(notes[0].What, "answered by rule status: every step is done; friend amy is held (was up) at "), notes[0].What)
}
