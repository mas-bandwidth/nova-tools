package sprint_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/buildinfo"
	"github.com/mas-bandwidth/nova-tools/pkg/hostload"
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

// advance moves the clock d and runs nothing.
func (r *transitionRig) advance(d time.Duration) {
	r.mu.Lock()
	r.now = r.now.Add(d)
	r.mu.Unlock()
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

// statusNotes is every status note open (a judgment, or one answered and kept).
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

// pushes is what the push loop delivered for the row: every status judgment written, and
// every note to the coordinator that one changed.
func (r *transitionRig) pushes(row string) (judgments, changes int) {
	r.t.Helper()
	notes, _, err := r.m.NotesSince(r.ctx, "", 100000)
	require.NoError(r.t, err)
	for _, n := range notes {
		if len(n.Primaries) != 1 || n.Primaries[0] != row {
			continue
		}
		switch {
		case n.Kind == sprint.Judgment && n.Type == sprint.NStatus:
			judgments++
		case n.Kind == sprint.Happened && n.Type == sprint.NStatusChanged && n.To != "":
			changes++
		}
	}
	return judgments, changes
}

// up brings amy up past the dwell: her daemon beats, her session answers, and the change
// holds for the dwell. The clock ends at the tick that counts it.
func (r *transitionRig) up(build string, start time.Time) {
	r.t.Helper()
	r.daemon(build, start, start)
	r.answered()
	r.tick(time.Second)
	assert.Empty(r.t, r.statusNotes(sprint.FriendRow("amy")), "inside the dwell: nothing raised")
	r.advance(sprint.StatusDwell)
	r.daemon(build, start, start)
	r.answered()
	r.tick(0)
}

func TestAFriendComingUpPushesTheFourStepsToTheSeat(t *testing.T) {
	t.Parallel()
	r := newTransitionRig(t)
	row := sprint.FriendRow("amy")
	r.tick(time.Second)
	assert.Empty(t, r.statusNotes(row), "the first sight of her records her status and raises nothing")

	// her daemon starts, sends her the present, and her session answers; it holds the dwell
	current := buildinfo.Version("")
	start := r.st.Now()
	r.up(current, start)
	notes := r.statusNotes(row)
	require.Len(t, notes, 1, "her coming up raises one judgment")
	n := notes[0]
	assert.Equal(t, sprint.Judgment, n.Kind, "a judgment, pushed to the seat inbox as every judgment is")
	at := r.st.Now().UTC().Format(time.RFC3339)
	assert.Equal(t, "friend amy is up (was down) at "+at+", transition 1, on session pong 0s ago; bring her up in four steps: "+
		"1 update: her daemon runs "+current+", the current build is "+current+": an unstamped build cannot be compared; run: nova-update, then launchctl kickstart -k gui/$(id -u)/com.nova.friend-amy (to do); "+
		"2 check: daemon beating (0s ago), her daemon started at "+start.Format(time.RFC3339)+" and has beat since; no harness check reported, presence session pong 0s ago; run: nova-friend check amy and read its CHECK DAEMON, CHECK HARNESS and presence lines (to do); "+
		"3 snap to present: her daemon sent the present on its start at "+start.Format(time.RFC3339)+" (done); "+
		"4 into the sprint: not held, evidence session pong 0s ago, no take within 10m0s; run: nova-sprint where, and nova-friend ping --as coordinator --to amy --wake (to do)",
		n.What)
	assert.Equal(t, []string{"ack", "wait"}, n.Decisions, "the verbs are in the text; ack or wait answers it")
	assert.Equal(t, []string{row}, n.Primaries)
	assert.NotEmpty(t, n.Alias, "aliased j<n> as every judgment")

	// a daemon on another build that sent no present: the steps say so, and what to run
	r2 := newTransitionRig(t)
	r2.tick(time.Second)
	r2.daemon("2030-01-01T00:00:00Z-0123456789ab", r2.st.Now().Add(-time.Hour), time.Time{})
	r2.answered()
	r2.tick(time.Second)
	r2.advance(sprint.StatusDwell)
	r2.answered()
	r2.tick(0)
	notes = r2.statusNotes(row)
	require.Len(t, notes, 1)
	assert.Contains(t, notes[0].What, "1 update: her daemon runs 2030-01-01T00:00:00Z-0123456789ab, the current build is "+current)
	assert.Contains(t, notes[0].What, "3 snap to present: her daemon sent no present since its start; run: nova-bus send --to amy")
}

// Two builds compare by their revision; an unstamped devel build is never the same as any.
func TestTwoDevelBuildsAreNeverTheSame(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ a, b, want string }{
		{"devel", "devel", sprint.BuildUnknown},
		{"", "", sprint.BuildUnknown},
		{"devel", "2030-01-01T00:00:00Z-0123456789ab", sprint.BuildUnknown},
		{"2030-01-01T00:00:00Z-0123456789ab", "v1.2.0 2030-01-01T00:00:00Z-0123456789ab", sprint.BuildSame},
		{"2030-01-01T00:00:00Z-0123456789ab", "2030-01-02T00:00:00Z-ba9876543210", sprint.BuildDifferent},
		{"v1.2.0", "v1.2.0", sprint.BuildSame},
		{"v1.2.0", "v1.3.0", sprint.BuildDifferent},
	} {
		assert.Equal(t, c.want, sprint.BuildMatch(c.a, c.b), "%q %q", c.a, c.b)
	}
}

func TestATransitionRaisesExactlyOneJudgment(t *testing.T) {
	t.Parallel()
	r := newTransitionRig(t)
	row := sprint.FriendRow("amy")
	r.tick(time.Second)
	r.answered()
	for range 10 {
		r.tick(15 * time.Second)
		r.answered()
	}
	notes := r.statusNotes(row)
	require.Len(t, notes, 1, "ten ticks over one transition, one judgment")
	first := notes[0]
	assert.Contains(t, first.What, "transition 1")
	assert.Empty(t, r.statusNotes("m1"), "m1 beat every tick: no transition, no judgment")

	// held by the coordinator: the next transition replaces the open judgment's text, the
	// same id and alias; ten ticks, still one
	_, err := r.st.Hold(r.ctx, sprint.HoldReq{Names: []string{"amy"}, Who: "coordinator", Reason: "test"})
	require.NoError(t, err)
	for range 10 {
		r.tick(15 * time.Second)
	}
	notes = r.statusNotes(row)
	require.Len(t, notes, 1, "one open status judgment on the row")
	assert.Equal(t, first.ID, notes[0].ID, "replaced in place")
	assert.Equal(t, first.Alias, notes[0].Alias, "the same alias")
	assert.True(t, strings.HasPrefix(notes[0].What, "friend amy is held (was up) at "), notes[0].What)
	assert.Contains(t, notes[0].What, "transition 2")
	judgments, changes := r.pushes(row)
	assert.Equal(t, 1, judgments, "one judgment written")
	assert.Equal(t, 1, changes, "the replace told the seat once")
}

// A friend flipping every minute for an hour floods nothing: her coming up is one push, the
// flips inside the dwell raise nothing and are counted on the judgment, and the one change
// that outlasts the dwell is one more push, the same judgment replaced.
func TestAFriendFlappingForAnHourIsTwoPushes(t *testing.T) {
	t.Parallel()
	r := newTransitionRig(t)
	row := sprint.FriendRow("amy")
	r.tick(time.Second)
	r.up("", r.st.Now())
	notes := r.statusNotes(row)
	require.Len(t, notes, 1)
	first := notes[0]
	flapsFrom := ""
	for k := range 60 {
		r.advance(time.Minute)
		r.answered()
		_, err := r.st.Hold(r.ctx, sprint.HoldReq{Names: []string{"amy"}, Who: "coordinator", Reason: "flap", Release: k%2 == 1})
		require.NoError(t, err)
		r.tick(0)
		if k == 0 {
			flapsFrom = r.st.Now().UTC().Format(time.RFC3339)
		}
	}
	judgments, changes := r.pushes(row)
	assert.Equal(t, 1, judgments+changes, "an hour of one-minute flips: her coming up alone was pushed")
	require.Len(t, r.statusNotes(row), 1)
	assert.Contains(t, r.statusNotes(row)[0].What, "; flapped 30 times since "+flapsFrom+" (each back inside 2m0s)")

	// held for good: the change outlasts the dwell, one more push, the same judgment
	r.advance(time.Minute)
	r.answered()
	_, err := r.st.Hold(r.ctx, sprint.HoldReq{Names: []string{"amy"}, Who: "coordinator", Reason: "for good"})
	require.NoError(t, err)
	for range 3 {
		r.tick(time.Minute)
	}
	judgments, changes = r.pushes(row)
	assert.Equal(t, 1, judgments, "one judgment in all")
	assert.Equal(t, 1, changes, "one change past the dwell, one push")
	notes = r.statusNotes(row)
	require.Len(t, notes, 1)
	assert.Equal(t, first.ID, notes[0].ID)
	assert.True(t, strings.HasPrefix(notes[0].What, "friend amy is held (was up) at "), notes[0].What)
	assert.Contains(t, notes[0].What, "transition 2")
	assert.Contains(t, notes[0].What, "; flapped 30 times since "+flapsFrom+" (each back inside 2m0s)")
}

func TestAFleetMemberGoingDownNamesItsLastBeat(t *testing.T) {
	t.Parallel()
	r := newTransitionRig(t)
	r.tick(time.Second)
	assert.Empty(t, r.statusNotes("m1"))
	last := r.st.Now().UTC().Format(time.RFC3339)
	r.member = false // its beat stops
	r.tick(sprint.MissedBeatsDown*sprint.BeatDeadline + time.Second)
	assert.Empty(t, r.statusNotes("m1"), "down, inside the dwell")
	r.tick(sprint.StatusDwell)
	notes := r.statusNotes("m1")
	require.Len(t, notes, 1)
	at := r.st.Now().UTC().Format(time.RFC3339)
	assert.Equal(t, "fleet member m1 is down (was up) at "+at+", transition 1: its last beat "+last+" (2m46s ago, 11 beat windows of 15s missed), width 2; "+
		"its unfinished cards were dealt round the members up; bring it back: its beat on m1 (nova-sprint fleet beat m1) has stopped, start its member loop again; "+
		"or hold it: nova-sprint hold m1 --reason <text>", notes[0].What)
	assert.Equal(t, sprint.Judgment, notes[0].Kind)
	for range 10 {
		r.tick(time.Second)
	}
	assert.Len(t, r.statusNotes("m1"), 1, "down for ten more ticks: still the one judgment")

	// it beats again past the dwell: the same judgment replaced with its beat and width
	r.member = true
	r.tick(time.Second)
	r.tick(sprint.StatusDwell)
	notes2 := r.statusNotes("m1")
	require.Len(t, notes2, 1)
	assert.Equal(t, notes[0].ID, notes2[0].ID)
	assert.True(t, strings.HasPrefix(notes2[0].What, "fleet member m1 is up (was down) at "+r.st.Now().UTC().Format(time.RFC3339)+", transition 2: its last beat "), notes2[0].What)
	assert.Contains(t, notes2[0].What, "width 2; the tick deals it cards from now")
}

func TestARestartDoesNotReplayTransitions(t *testing.T) {
	t.Parallel()
	r := newTransitionRig(t)
	row := sprint.FriendRow("amy")
	r.tick(time.Second)
	start := r.st.Now()
	r.up("", start)
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
	judgments, changes := r.pushes(row)
	assert.Equal(t, 1, judgments+changes)

	// her daemon restarts, and the server restarts inside the dwell: one change, counted once
	again := r.st.Now()
	r.daemon("", again, again)
	r.answered()
	r.tick(time.Second)
	r.st = r.open()
	r.advance(sprint.StatusDwell)
	r.daemon("", again, again)
	r.answered()
	r.tick(0)
	r.st = r.open()
	r.tick(time.Second)
	notes = r.statusNotes(row)
	require.Len(t, notes, 1)
	assert.Equal(t, first, notes[0].ID, "the open judgment replaced")
	assert.Contains(t, notes[0].What, "friend amy's daemon started again at "+again.UTC().Format(time.RFC3339))
	assert.Contains(t, notes[0].What, "transition 2")
	judgments, changes = r.pushes(row)
	assert.Equal(t, 1, judgments)
	assert.Equal(t, 1, changes)
}

// With the machine's own answers (run --answer-rules) a status judgment is pushed first and
// answered by rule no sooner than StatusRuleAfter after its push: a hold, the coordinator's
// own act, is closed then; a friend come up is never, her check being the coordinator's.
func TestAStatusJudgmentWhoseStepsAreDoneIsAnsweredByRule(t *testing.T) {
	t.Parallel()
	r := newTransitionRig(t)
	r.st.AnswerRules = true
	row := sprint.FriendRow("amy")
	r.tick(time.Second)
	_, err := r.st.Hold(r.ctx, sprint.HoldReq{Names: []string{"amy"}, Who: "coordinator", Reason: "test"})
	require.NoError(t, err)
	r.tick(time.Second)
	r.tick(sprint.StatusDwell)
	notes := r.statusNotes(row)
	require.Len(t, notes, 1)
	assert.Equal(t, sprint.Judgment, notes[0].Kind, "pushed as a judgment, never written answered")
	r.tick(sprint.StatusRuleAfter - time.Second)
	assert.Len(t, r.statusNotes(row), 1, "inside a minute of its push: still open")
	r.tick(time.Second)
	assert.Empty(t, r.statusNotes(row), "a minute after its push: answered by rule and closed")

	r2 := newTransitionRig(t)
	r2.st.AnswerRules = true
	r2.tick(time.Second)
	r2.up(buildinfo.Version(""), r2.st.Now())
	for range 3 {
		r2.answered()
		r2.tick(time.Minute)
	}
	assert.Len(t, r2.statusNotes(row), 1, "a friend come up waits on the coordinator's check")
}

// A friend down for 1m59s at a time with a blip up between never makes a transition, and
// with no judgment open nothing else tells the seat: her third flap inside StatusFlapWindow
// raises the one judgment "flapping", once; her later flaps rewrite its count in place, and
// a change that outlasts the dwell replaces it as any transition does.
func TestAFriendFlappingWithNoJudgmentOpenRaisesFlappingOnce(t *testing.T) {
	t.Parallel()
	r := newTransitionRig(t)
	row := sprint.FriendRow("amy")
	r.tick(time.Second)
	r.up("", r.st.Now())
	notes := r.statusNotes(row)
	require.Len(t, notes, 1)
	_, err := r.st.Run(r.ctx, store.AckStep(sprint.AckReq{Notes: []string{notes[0].ID}, Reason: "her steps are run", Who: "coordinator"}))
	require.NoError(t, err)
	before, _ := r.pushes(row)

	down := func() { // her daemon answers the wake ping and her session does not: down
		_, _, _, err := r.st.FriendHealth(r.ctx, "amy", "coordinator", sprint.FriendHealth{State: sprint.DaemonPong, Seen: r.st.Now(), Generation: sprint.FirstSeatGeneration}, "")
		require.NoError(t, err)
	}
	blip := r.answered
	flapsFrom := ""
	for k := range 6 {
		r.advance(time.Second)
		down()
		r.tick(0)
		if k == 0 {
			flapsFrom = r.st.Now().UTC().Format(time.RFC3339)
		}
		r.advance(sprint.StatusDwell - 2*time.Second)
		down()
		r.tick(0) // 1m58s down: inside the dwell
		r.advance(time.Second)
		blip()
		r.tick(0) // back up for a tick: a flap
		judgments, _ := r.pushes(row)
		if k < sprint.StatusFlapsRaise-1 {
			assert.Equal(t, before, judgments, "flap %d: nothing raised yet", k+1)
		} else {
			assert.Equal(t, before+1, judgments, "flap %d: the one flapping judgment", k+1)
		}
	}
	open := r.statusNotes(row)
	var flapping []sprint.Note
	for _, n := range open {
		if n.Kind == sprint.Judgment {
			flapping = append(flapping, n)
		}
	}
	require.Len(t, flapping, 1)
	assert.True(t, strings.HasPrefix(flapping[0].What, "friend amy is flapping at "), flapping[0].What)
	assert.Contains(t, flapping[0].What, "; flapped 6 times since "+flapsFrom+" (each back inside 2m0s)")
	_, changes := r.pushes(row)
	assert.Zero(t, changes, "the count rewritten in place, no push")
}
