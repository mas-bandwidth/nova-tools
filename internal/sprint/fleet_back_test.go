package sprint_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/hostload"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeAdopter is the release adopt path for one machine, by hand: a test
// starts nothing on a machine and finishes each adoption when it says so.
type fakeAdopter struct {
	mu     sync.Mutex
	target string
	starts map[string]int
	runs   map[string]sprint.MachineAdoption // by machine, of the episode in eps
	eps    map[string]string
}

func newFakeAdopter(target string) *fakeAdopter {
	return &fakeAdopter{target: target, starts: map[string]int{}, runs: map[string]sprint.MachineAdoption{}, eps: map[string]string{}}
}

func (f *fakeAdopter) Target() string { return f.target }

func (f *fakeAdopter) Start(machine, version, episode string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.runs[machine].State == sprint.AdoptRunning {
		return false
	}
	f.starts[machine]++
	f.eps[machine] = episode
	f.runs[machine] = sprint.MachineAdoption{State: sprint.AdoptRunning}
	return true
}

func (f *fakeAdopter) Adoption(machine, episode string) sprint.MachineAdoption {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.eps[machine] != episode {
		return sprint.MachineAdoption{}
	}
	return f.runs[machine]
}

func (f *fakeAdopter) finish(machine string, a sprint.MachineAdoption) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runs[machine] = a
}

func (f *fakeAdopter) started(machine string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.starts[machine]
}

// backRig is a running sprint on the twin whose tick holds a member back from
// down to adopt through the fake: members m1 and m2 at width 2, stream s1.
type backRig struct {
	t      *testing.T
	st     *store.Store
	mem    *store.Mem
	ctx    context.Context
	mu     sync.Mutex
	now    time.Time
	beats  map[string]bool // the members that beat
	adopts *fakeAdopter
}

func newBackRig(t *testing.T, cards int) *backRig {
	t.Helper()
	m := store.NewMem()
	r := &backRig{t: t, mem: m, ctx: context.Background(), now: holdT0, beats: map[string]bool{"m1": true, "m2": true}, adopts: newFakeAdopter("v1.2.0")}
	n := 0
	r.st = &store.Store{B: m, Names: sprint.Names{Prefix: "t-"}, Actor: "coordinator",
		Now:     func() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return r.now },
		NewID:   func() string { r.mu.Lock(); defer r.mu.Unlock(); n++; return fmt.Sprint(n) },
		Sleep:   func(time.Duration) {},
		Updates: sprint.FleetBackTables(r.adopts)}
	require.NoError(t, r.st.Init(r.ctx))
	require.NoError(t, m.RowsAdd(r.ctx, "t-readers", []string{"reader-a", "reader-b", "reader-c"}))
	require.NoError(t, m.SetCoordinator(r.ctx, "coordinator"))
	r.beat()
	for _, member := range []string{"m1", "m2"} {
		_, err := r.st.Run(r.ctx, store.FleetStep(sprint.FleetReq{Op: "up", Member: member, Width: 2}))
		require.NoError(t, err)
	}
	_, err := r.st.Run(r.ctx, store.AddStep(sprint.AddReq{Stream: "s1", Count: cards}))
	require.NoError(t, err)
	_, _, _, err = r.st.SetMachine(r.ctx, true)
	require.NoError(t, err)
	return r
}

func (r *backRig) beat() {
	r.t.Helper()
	require.NoError(r.t, r.st.BeatReaders(r.ctx))
	zero := 0.0
	for _, m := range []string{"m1", "m2"} {
		if r.beats[m] {
			_, err := r.st.Beat(r.ctx, m, &zero, hostload.Source{})
			require.NoError(r.t, err)
		}
	}
}

// tick moves the clock by d, beats the members that beat and runs one tick.
func (r *backRig) tick(d time.Duration) {
	r.t.Helper()
	r.mu.Lock()
	r.now = r.now.Add(d)
	r.mu.Unlock()
	r.beat()
	_, err := r.st.Tick(r.ctx)
	require.NoError(r.t, err)
}

func (r *backRig) ctl(m string) *sprint.Card {
	r.t.Helper()
	s, err := r.st.Load(r.ctx, store.All, nil)
	require.NoError(r.t, err)
	c := s.MemberCtl(m)
	require.NotNil(r.t, c, m)
	return c
}

// row is the member's status cell in the fleet table.
func (r *backRig) row(m string) string {
	r.t.Helper()
	shapes, err := r.mem.Shapes(r.ctx, []string{r.st.Names.Table(sprint.Fleet)})
	require.NoError(r.t, err)
	require.Len(r.t, shapes, 1)
	for _, row := range shapes[0].Rows {
		if row.Key == m {
			return row.Texts[sprint.Status]
		}
	}
	require.FailNow(r.t, "no fleet row "+m)
	return ""
}

// dealt is the work cards placed on the member's row, ready and working.
func (r *backRig) dealt(m string) []string {
	r.t.Helper()
	s, err := r.st.Load(r.ctx, store.All, nil)
	require.NoError(r.t, err)
	return onRow(s, m, "s1", sprint.Ready, sprint.Working)
}

// notes is the What of every note of the type the log holds.
func (r *backRig) notes(typ string) []string {
	r.t.Helper()
	lines, err := r.st.Log(r.ctx)
	require.NoError(r.t, err)
	var out []string
	for _, l := range lines {
		if l.Note != nil && l.Note.Type == typ {
			out = append(out, l.Note.What)
		}
	}
	return out
}

// downAndBack takes m down by its missing beats, then has it beat again and
// ticks once: the tick that sees it back.
func (r *backRig) downAndBack(m string) {
	r.t.Helper()
	r.beats[m] = false
	for range sprint.MissedBeatsDown + 1 {
		r.tick(sprint.BeatDeadline)
	}
	require.Equal(r.t, sprint.Down, r.ctl(m).F("status"), "%s is down by its missing beats", m)
	require.Empty(r.t, r.dealt(m), "%s's cards went round the fleet", m)
	r.beats[m] = true
	r.tick(time.Second)
}

// A member down whose beat returns is held adopting and dealt nothing; its
// adoption of the coordinator's release is started once, for it alone; a
// passing adoption brings it up at its width with one note naming the versions
// before and after, and it is dealt again; a failing one keeps it held with the
// failure as its reason and one judgment (docs/SPEC-SPRINT.md section 5, "Back
// from down: adopt the latest").
func TestAMachineBackFromDownAdoptsTheLatestBeforeItIsDealt(t *testing.T) {
	t.Parallel()
	t.Run("passes", func(t *testing.T) {
		t.Parallel()
		r := newBackRig(t, 20)
		r.tick(time.Second)
		require.NotEmpty(t, r.dealt("m1"), "m1 is dealt while up")

		r.downAndBack("m1")
		ctl := r.ctl("m1")
		assert.Equal(t, sprint.Adopting, ctl.F("status"), "back from down: held adopting")
		assert.Equal(t, "v1.2.0", ctl.F(sprint.FieldAdoptTo), "the release the coordinator's machine runs")
		assert.Equal(t, sprint.Adopting, sprint.FleetRowStatus(ctl, sprint.Beat{At: r.st.Now()}, r.st.Now()), "a member adopting is adopting whatever it beats")
		assert.Equal(t, sprint.Adopting, r.row("m1"), "the fleet table shows adopting as its own status")
		assert.Empty(t, r.dealt("m1"), "nothing is dealt to a member adopting")
		for range 3 {
			r.tick(time.Second)
		}
		assert.Equal(t, 1, r.adopts.started("m1"), "one adoption for m1, never a second while it runs")
		assert.Zero(t, r.adopts.started("m2"), "the adoption is m1's alone")
		assert.Equal(t, sprint.Adopting, r.ctl("m1").F("status"))
		assert.Empty(t, r.dealt("m1"), "nothing is dealt while the adoption runs")
		assert.Empty(t, r.notes(sprint.NMemberBack))

		r.adopts.finish("m1", sprint.MachineAdoption{State: sprint.AdoptFinished, From: "v1.1.0", To: "v1.2.0"})
		r.tick(time.Second)
		ctl = r.ctl("m1")
		assert.Equal(t, sprint.Up, ctl.F("status"), "the installed version read back is the release: m1 is up")
		assert.Equal(t, sprint.Up, r.row("m1"), "the fleet table shows it up")
		assert.Empty(t, ctl.F("held"), "the hold is gone")
		assert.Equal(t, "2", ctl.F(sprint.FieldWidth), "at its row's width")
		assert.Equal(t, []string{"m1 is back: v1.1.0 -> v1.2.0"}, r.notes(sprint.NMemberBack), "one note naming the versions before and after")
		r.tick(time.Second)
		r.tick(time.Second)
		assert.NotEmpty(t, r.dealt("m1"), "m1 is dealt again once up")
		assert.Equal(t, []string{"m1 is back: v1.1.0 -> v1.2.0"}, r.notes(sprint.NMemberBack), "still one note")
		assert.Equal(t, 1, r.adopts.started("m1"))
	})
	t.Run("fails", func(t *testing.T) {
		t.Parallel()
		r := newBackRig(t, 20)
		r.tick(time.Second)
		r.downAndBack("m2")
		r.tick(time.Second)
		require.Equal(t, 1, r.adopts.started("m2"))
		r.adopts.finish("m2", sprint.MachineAdoption{State: sprint.AdoptFailed, From: "v1.1.0", Err: "ssh: connect to host m2: Connection refused"})
		for range 4 {
			r.tick(time.Second)
		}
		ctl := r.ctl("m2")
		assert.Equal(t, sprint.Adopting, ctl.F("status"), "a failed adoption keeps it held")
		assert.Equal(t, sprint.Adopting, r.row("m2"), "the fleet table shows adopting while the failure holds it")
		assert.Contains(t, ctl.F(sprint.FieldHeldReason), "Connection refused", "the failure is the hold's reason")
		assert.Empty(t, r.dealt("m2"), "nothing is dealt to it")
		assert.Len(t, r.notes(sprint.NAdoptFailed), 1, "one judgment, never one per tick")
		assert.Empty(t, r.notes(sprint.NMemberBack))
		assert.Equal(t, 1, r.adopts.started("m2"), "a failed adoption is not started again by the tick")
	})
	t.Run("reads back another version", func(t *testing.T) {
		t.Parallel()
		r := newBackRig(t, 20)
		r.tick(time.Second)
		r.downAndBack("m1")
		r.tick(time.Second)
		r.adopts.finish("m1", sprint.MachineAdoption{State: sprint.AdoptFinished, From: "v1.1.0", To: "v1.1.0"})
		r.tick(time.Second) // the failure is written on the member's row
		r.tick(time.Second) // and the next tick's deal raises its judgment
		ctl := r.ctl("m1")
		assert.Equal(t, sprint.Adopting, ctl.F("status"), "the version read back is not the release: held")
		assert.Contains(t, ctl.F(sprint.FieldHeldReason), "reads v1.1.0")
		assert.Len(t, r.notes(sprint.NAdoptFailed), 1)
	})
	t.Run("no adopter", func(t *testing.T) {
		t.Parallel()
		r := newBackRig(t, 20)
		r.adopts.target = "" // no release known: presence as it was
		r.tick(time.Second)
		r.downAndBack("m1")
		assert.Equal(t, sprint.Up, r.ctl("m1").F("status"), "with no release to adopt a member back is up at once")
		assert.Zero(t, r.adopts.started("m1"))
	})
}
