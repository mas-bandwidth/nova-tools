package sprint_test

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/hostload"
)

// fdMeter is a fake machine whose open file descriptors the test sets: a load average
// of 1 on 4 cores, the workshop's bounds (warn 50000, alarm 150000), three holders.
type fdMeter struct {
	mu   sync.Mutex
	open int
}

func (f *fdMeter) set(n int) { f.mu.Lock(); f.open = n; f.mu.Unlock() }

func (f *fdMeter) source() hostload.Source {
	return hostload.Source{NCPU: 4, Load1: func() (float64, bool) { return 1, true },
		OpenFiles: func() (int, int, error) { f.mu.Lock(); defer f.mu.Unlock(); return f.open, 491520, nil },
		Holders: func() ([]hostload.Holder, error) {
			return []hostload.Holder{
				{PID: 200, Command: "redis-server", User: "build", Open: 9000},
				{PID: 300, Command: "node", User: "build", Open: 140000},
			}, nil
		},
		FilesWarn: 50000, FilesAlarm: 150000}
}

// fdTicks runs n ticks, the clock a second on before each, m1 beating with its load
// given as the member's beat gives it (fleet beat --load) and its descriptors measured
// by the meter.
func (r *alarmRig) fdTicks(meter *fdMeter, n int) {
	r.t.Helper()
	src := meter.source()
	src.Given = func() (float64, bool) { return 10, true }
	for range n {
		r.mu.Lock()
		r.now = r.now.Add(time.Second)
		r.mu.Unlock()
		require.NoError(r.t, r.st.BeatReaders(r.ctx))
		_, err := r.st.Beat(r.ctx, "m1", nil, src)
		require.NoError(r.t, err)
		_, err = r.st.Tick(r.ctx)
		require.NoError(r.t, err)
	}
}

// fdEpisodes is how many open-files judgments were written and how many cleared notes
// named them, and every judgment written.
func (r *alarmRig) fdEpisodes() (raised, cleared int, judgments []sprint.Note) {
	r.t.Helper()
	notes, _, err := r.m.NotesSince(r.ctx, "", 100000)
	require.NoError(r.t, err)
	for _, n := range notes {
		switch {
		case n.Kind == sprint.Judgment && n.Type == sprint.NFilesAlarm:
			raised++
			judgments = append(judgments, n)
		case n.Kind == sprint.Happened && n.Type == sprint.NAlarmCleared && strings.HasPrefix(n.What, sprint.NFilesAlarm+":"):
			assert.Equal(r.t, "coordinator", n.To, "a cleared note is the coordinator's: %+v", n)
			cleared++
		}
	}
	return raised, cleared, judgments
}

func (r *alarmRig) fdText() string {
	r.t.Helper()
	beats, err := r.st.Beats(r.ctx, []string{"m1"})
	require.NoError(r.t, err)
	r.mu.Lock()
	defer r.mu.Unlock()
	return sprint.FilesText(beats["m1"], r.now)
}

// A machine's open file descriptors are measured by its beat beside its load, so no
// workshop tool watches them (docs/SPEC-SPRINT.md section 5, the fleet; section 8,
// "Open files"): under the warn bound nothing is said; over it the member's files word
// says warn and no judgment is written; over the alarm bound one judgment is written,
// naming the member, its count, the top holders and its cards that ended on a timeout,
// and it stays one while the count moves; under the alarm again it closes with one
// cleared note to the coordinator, and a second episode is raised again.
func TestOpenFilesOverTheAlarmRaiseOneJudgmentNamingTheTopHolders(t *testing.T) {
	t.Parallel()
	r := newAlarmRig(t)
	meter := &fdMeter{open: 1000}
	r.must(store.FleetStep(sprint.FleetReq{Op: "up", Member: "m1", Width: 4}))
	r.must(store.AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"a"}}))
	_, _, _, err := r.st.SetMachine(r.ctx, true)
	require.NoError(t, err)
	r.fdTicks(meter, 3)
	// the effect on cards: a's child ran out its deadline on m1
	r.must(store.TakeStep(sprint.TakeReq{As: "m1", Sel: sprint.Sel{Limit: 1}, Who: "m1"}))
	s := r.snap()
	wc := s.Fleet.Card(s.Work.Card("a").F("work"))
	require.NotNil(t, wc, "the fixture: a dealt and taken")
	r.must(store.FinishStep(sprint.FinishReq{As: "m1", Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: wc.Int("gen")},
		Failed: true, Report: "deadline: the child ran past its deadline", Who: "m1"}))

	r.fdTicks(meter, 3)
	raised, cleared, _ := r.fdEpisodes()
	require.Equal(t, [2]int{0, 0}, [2]int{raised, cleared}, "under the warn bound")
	require.Empty(t, r.fdText(), "under the warn bound the files word is empty")

	meter.set(60000)
	r.fdTicks(meter, 3)
	raised, cleared, _ = r.fdEpisodes()
	require.Equal(t, [2]int{0, 0}, [2]int{raised, cleared}, "over the warn bound only: no judgment")
	require.Equal(t, "60000 warn", r.fdText(), "over the warn bound the files word says warn")

	meter.set(160000)
	r.fdTicks(meter, 3)
	raised, cleared, js := r.fdEpisodes()
	require.Equal(t, [2]int{1, 0}, [2]int{raised, cleared}, "over the alarm bound: one judgment")
	require.Equal(t, "160000 alarm", r.fdText())
	j := js[0]
	assert.Equal(t, sprint.MemberSubject("m1"), j.Stream, "the judgment is the member's")
	for _, want := range []string{"m1 has 160000 open files, above its alarm of 150000", "node pid=300 user=build fds=140000", "redis-server pid=200 user=build fds=9000", wc.ID + " (deadline)"} {
		assert.Contains(t, j.What, want, "the judgment names %q", want)
	}
	assert.Equal(t, []string{"fleet up m1 --width 2", "fleet down m1", "ack", "wait 15m"}, j.Decisions)

	// the count moves and stays over the alarm: the same episode, no second judgment
	meter.set(170000)
	r.fdTicks(meter, 3)
	raised, cleared, _ = r.fdEpisodes()
	require.Equal(t, [2]int{1, 0}, [2]int{raised, cleared}, "one judgment an episode, whatever the count does")
	require.Equal(t, 1, r.openOf(sprint.NFilesAlarm), "the episode's judgment is open")
	assert.Contains(t, r.fdJudgment().What, "m1 has 170000 open files", "the open judgment says the latest count")

	// under the alarm: closed, one cleared note
	meter.set(1000)
	r.fdTicks(meter, 3)
	raised, cleared, _ = r.fdEpisodes()
	require.Equal(t, [2]int{1, 1}, [2]int{raised, cleared}, "under the alarm: cleared once")
	require.Zero(t, r.openOf(sprint.NFilesAlarm))

	// a second episode is raised again
	meter.set(160000)
	r.fdTicks(meter, 3)
	raised, cleared, _ = r.fdEpisodes()
	require.Equal(t, [2]int{2, 1}, [2]int{raised, cleared}, "a second episode")

	// a beat gone stale is no reading: the episode clears
	r.mu.Lock()
	r.now = r.now.Add(sprint.BeatDeadline + time.Second)
	r.mu.Unlock()
	_, err = r.st.Tick(r.ctx)
	require.NoError(t, err)
	raised, cleared, _ = r.fdEpisodes()
	require.Equal(t, [2]int{2, 2}, [2]int{raised, cleared}, "a stale beat clears the episode")
	require.Empty(t, r.fdText())
}

// fdJudgment is the open open-files judgment.
func (r *alarmRig) fdJudgment() sprint.Note {
	r.t.Helper()
	for _, o := range r.snap().Open {
		if o.Note.Type == sprint.NFilesAlarm && o.Note.Kind == sprint.Judgment {
			return o.Note
		}
	}
	require.FailNow(r.t, "no open judgment of type "+sprint.NFilesAlarm)
	return sprint.Note{}
}

// fleetLoad is m1's load cell in the fleet table.
func (r *alarmRig) fleetLoad() string {
	r.t.Helper()
	shapes, err := r.m.Shapes(r.ctx, []string{r.st.Names.Table(sprint.Fleet)})
	require.NoError(r.t, err)
	require.NotEmpty(r.t, shapes)
	for _, row := range shapes[0].Rows {
		if row.Key == "m1" {
			return row.Texts[sprint.Load]
		}
	}
	require.FailNow(r.t, "no fleet row m1")
	return ""
}

// The fleet table shows a member's open files over its warn bound beside its load, and
// the open-files judgment is the coordinator's to answer like every alarm's
// (docs/SPEC-SPRINT.md section 8, "Open files"): ack holds the episode quiet whatever
// the count does until it falls under; wait 15m holds it for that running time and the
// tick raises it again when the count is still over, with no cleared note between.
func TestOpenFilesShowInTheFleetTableAndTheJudgmentTakesAckAndWait(t *testing.T) {
	t.Parallel()
	r := newAlarmRig(t)
	meter := &fdMeter{open: 1000}
	r.must(store.FleetStep(sprint.FleetReq{Op: "up", Member: "m1", Width: 4}))
	_, _, _, err := r.st.SetMachine(r.ctx, true)
	require.NoError(t, err)
	r.fdTicks(meter, 3)
	assert.Equal(t, "10.0%", r.fleetLoad(), "under the warn bound the load cell is the load alone")

	meter.set(60000)
	r.fdTicks(meter, 3)
	assert.Equal(t, "10.0% fds 60000 warn", r.fleetLoad(), "over the warn bound the fleet table says warn")

	meter.set(160000)
	r.fdTicks(meter, 3)
	assert.Equal(t, "10.0% fds 160000 alarm", r.fleetLoad())
	j := r.fdJudgment()
	assert.Equal(t, []string{"fleet up m1 --width 2", "fleet down m1", "ack", "wait 15m"}, j.Decisions)

	// ack: the episode is held quiet while the count moves over the alarm
	r.must(store.AckStep(sprint.AckReq{Notes: []string{j.ID}, Reason: "seen", Who: "coordinator"}))
	meter.set(170000)
	r.fdTicks(meter, 3)
	raised, cleared, _ := r.fdEpisodes()
	require.Equal(t, [2]int{1, 0}, [2]int{raised, cleared}, "acknowledged: no second judgment while it stays over")
	require.Zero(t, r.openOf(sprint.NFilesAlarm))

	// under the alarm: the acknowledged episode clears once; over again is a new episode
	meter.set(1000)
	r.fdTicks(meter, 3)
	raised, cleared, _ = r.fdEpisodes()
	require.Equal(t, [2]int{1, 1}, [2]int{raised, cleared}, "the acknowledged episode clears under the alarm")
	meter.set(160000)
	r.fdTicks(meter, 3)
	raised, cleared, _ = r.fdEpisodes()
	require.Equal(t, [2]int{2, 1}, [2]int{raised, cleared}, "a second episode")

	// wait 15m: quiet for that running time, then raised again while still over, not cleared
	r.mu.Lock()
	until := r.now.Add(15 * time.Minute)
	r.mu.Unlock()
	r.must(store.WaitStep(sprint.WaitReq{Note: r.fdJudgment().ID, Until: until, Who: "coordinator"}))
	r.fdTicks(meter, 3)
	raised, cleared, _ = r.fdEpisodes()
	require.Equal(t, [2]int{2, 1}, [2]int{raised, cleared}, "waiting: quiet")
	require.Zero(t, r.openOf(sprint.NFilesAlarm))
	r.mu.Lock()
	r.now = until
	r.mu.Unlock()
	r.fdTicks(meter, 3)
	raised, cleared, _ = r.fdEpisodes()
	require.Equal(t, [2]int{3, 1}, [2]int{raised, cleared}, "the wait run out on a count still over: raised again, no cleared note")
	require.Equal(t, 1, r.openOf(sprint.NFilesAlarm))
}

// openOf is how many judgments of the type are open.
func (r *alarmRig) openOf(typ string) int {
	r.t.Helper()
	n := 0
	for _, o := range r.snap().Open {
		if o.Note.Type == typ && o.Note.Kind == sprint.Judgment {
			n++
		}
	}
	return n
}
