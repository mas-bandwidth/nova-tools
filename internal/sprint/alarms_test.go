package sprint_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store/storetest"
	"github.com/mas-bandwidth/nova-tools/pkg/hostload"
)

// alarmRig is a sprint on the in-memory store, ticked by the machine on its twin
// (every part's twin checked against a fresh read), with a fake clock a tick moves by
// a second.
type alarmRig struct {
	t   *testing.T
	ctx context.Context
	m   *store.Mem
	st  *store.Store
	mu  sync.Mutex
	now time.Time
}

func newAlarmRig(t *testing.T) *alarmRig {
	t.Helper()
	r := &alarmRig{t: t, ctx: context.Background(), m: store.NewMem(), now: time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)}
	n := 0
	r.st = &store.Store{B: r.m, Names: sprint.Names{Prefix: "a-"}, Actor: "coordinator",
		Now:   func() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return r.now },
		NewID: func() string { r.mu.Lock(); defer r.mu.Unlock(); n++; return fmt.Sprint(n) },
		Sleep: func(time.Duration) {}}
	r.st.CheckTwin = func(twin, fresh *sprint.Snapshot) error {
		if d := storetest.TwinDiff(twin, fresh); d != "" {
			return errors.New(d)
		}
		return nil
	}
	require.NoError(t, r.st.Init(r.ctx))
	require.NoError(t, r.m.RowsAdd(r.ctx, "a-readers", []string{"reader-a", "reader-b"}))
	require.NoError(t, r.m.SetCoordinator(r.ctx, "coordinator"))
	r.beat()
	return r
}

// beat is a beat of the member and of every reader, at the clock's time.
func (r *alarmRig) beat() {
	r.t.Helper()
	require.NoError(r.t, r.st.BeatReaders(r.ctx))
	zero := 0.0
	_, err := r.st.Beat(r.ctx, "m1", &zero, hostload.Source{})
	require.NoError(r.t, err)
}

func (r *alarmRig) must(step store.Step) {
	r.t.Helper()
	res, err := r.st.Run(r.ctx, step)
	require.NoError(r.t, err, "%s", step.Verb)
	require.Empty(r.t, res.Refused, "%s refused", step.Verb)
}

// ticks runs n ticks of the machine, the clock a second on before each.
func (r *alarmRig) ticks(n int) {
	r.t.Helper()
	for range n {
		r.mu.Lock()
		r.now = r.now.Add(time.Second)
		r.mu.Unlock()
		r.beat()
		_, err := r.st.Tick(r.ctx)
		require.NoError(r.t, err)
	}
}

func (r *alarmRig) snap() *sprint.Snapshot {
	r.t.Helper()
	s, err := r.st.Load(r.ctx, store.All, nil)
	require.NoError(r.t, err)
	return s
}

// episodes is, for each alarm, how many times it was raised (a judgment of its type
// written) and how many times it cleared (a cleared note naming it), from every note
// the sprint wrote.
func (r *alarmRig) episodes() map[string][2]int {
	r.t.Helper()
	notes, _, err := r.m.NotesSince(r.ctx, "", 100000)
	require.NoError(r.t, err)
	out := map[string][2]int{}
	for _, typ := range sprint.AlarmTypes {
		var e [2]int
		for _, n := range notes {
			switch {
			case n.Kind == sprint.Judgment && n.Type == typ:
				e[0]++
			case n.Kind == sprint.Happened && n.Type == sprint.NAlarmCleared && strings.HasPrefix(n.What, typ+":"):
				assert.Equal(r.t, "coordinator", n.To, "a cleared note is the coordinator's: %+v", n)
				e[1]++
			}
		}
		out[typ] = e
	}
	return out
}

// The backlog alarms (docs/SPEC-SPRINT.md section 8, "Backlog alarms"): each of the
// four is off until its threshold is set (`set --alarm-...`, the work table's
// properties), then raised once when its condition starts, held quiet while it stands
// whatever the counts do, and cleared once, with one note to the coordinator, when it
// ends; a second episode is raised again.
func TestBacklogAlarmsPushOncePerEpisodeFromConfigThresholds(t *testing.T) {
	t.Parallel()
	r := newAlarmRig(t)
	r.must(store.FleetStep(sprint.FleetReq{Op: "up", Member: "m1", Width: 4}))
	r.must(store.AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"a", "b", "c"}}))
	r.must(store.AddStep(sprint.AddReq{Stream: "s1", IDs: []string{"d"}, Needs: []string{"a"}}))
	_, _, _, err := r.st.SetMachine(r.ctx, true)
	require.NoError(t, err)
	quiet := [2]int{0, 0}
	want := func(when string, ready, fleet, review, merging [2]int) {
		t.Helper()
		got := r.episodes()
		assert.Equal(t, ready, got[sprint.NAlarmReady], "%s: %s (raised, cleared)", when, sprint.NAlarmReady)
		assert.Equal(t, fleet, got[sprint.NAlarmFleet], "%s: %s (raised, cleared)", when, sprint.NAlarmFleet)
		assert.Equal(t, review, got[sprint.NAlarmReview], "%s: %s (raised, cleared)", when, sprint.NAlarmReview)
		assert.Equal(t, merging, got[sprint.NAlarmMerging], "%s: %s (raised, cleared)", when, sprint.NAlarmMerging)
	}

	// dealt: a, b and c on m1, none taken; d waits on a. With no threshold set the
	// conditions hold and nothing is raised.
	r.ticks(3)
	require.Equal(t, 3, len(r.snap().Fleet.Column(sprint.Ready)), "the fixture: a, b and c dealt and not taken")
	want("no thresholds set", quiet, quiet, quiet, quiet)

	r.must(store.SetStep(sprint.SetReq{AlarmReview: "0", AlarmMerging: "0", AlarmFleet: "50", AlarmReady: "on", Who: "coordinator"}))
	r.ticks(3)
	want("ready 0 with d waiting, m1 working 0 of 4", [2]int{1, 0}, [2]int{1, 0}, quiet, quiet)

	// m1 takes one: working 1 of 4 is below 50 percent still, the same episode whatever
	// its count says; then a second: working 2 of 4, and the fleet's alarm clears once
	r.must(store.TakeStep(sprint.TakeReq{As: "m1", Sel: sprint.Sel{Limit: 1}, Who: "m1"}))
	r.ticks(3)
	want("m1 working 1 of 4", [2]int{1, 0}, [2]int{1, 0}, quiet, quiet)
	r.must(store.TakeStep(sprint.TakeReq{As: "m1", Sel: sprint.Sel{Limit: 1}, Who: "m1"}))
	r.ticks(3)
	want("m1 working 2 of 4", [2]int{1, 0}, [2]int{1, 1}, quiet, quiet)

	// a finished: one in review, above 0, raised; acknowledged, it stays quiet while b and
	// c join it (three in review is the same episode); m1 working 1 of 4 again, with d
	// waiting, is the fleet's second episode, and stays one while it falls to 0
	r.finish("a")
	r.ticks(3)
	want("a in review, m1 working 1 of 4", [2]int{1, 0}, [2]int{2, 1}, [2]int{1, 0}, quiet)
	r.must(store.AckStep(sprint.AckReq{Notes: []string{r.open(sprint.NAlarmReview)}, Reason: "seen", Who: "coordinator"}))
	r.finish("b")
	r.must(store.TakeStep(sprint.TakeReq{As: "m1", Sel: sprint.Sel{Limit: 100}, Who: "m1"}))
	r.ticks(3)
	r.finish("c")
	r.ticks(3)
	require.Equal(t, 3, len(r.snap().Work.Column(sprint.Review)), "the fixture: a, b and c in review")
	want("three in review, m1 idle", [2]int{1, 0}, [2]int{2, 1}, [2]int{1, 0}, quiet)

	// the reads come back ok: the machine accepts all three, merging 3 above 0, review clears
	for _, rd := range []string{"reader-a", "reader-b"} {
		res, err := r.st.Run(r.ctx, store.ReadStep(sprint.ReadReq{Usage: "input=1000 output=100", As: rd, Verdict: "ok", Sel: sprint.Sel{Limit: 100}, Who: rd}))
		require.NoError(t, err, "read as %s: %+v", rd, res)
	}
	r.ticks(3)
	require.Equal(t, 3, len(r.snap().Work.Column(sprint.Merging)), "the fixture: a, b and c merging")
	want("three merging", [2]int{1, 0}, [2]int{2, 1}, [2]int{1, 1}, [2]int{1, 0})

	// landed: merging clears; d's need landed, so it is ready and dealt, and nothing
	// waits: the ready alarm and the fleet's second episode clear
	r.must(store.MergeStep(sprint.MergeReq{Stream: "s1", Batch: 100}))
	r.ticks(3)
	require.Empty(t, r.snap().Work.Column(sprint.Waiting, sprint.Ready), "the fixture: d dealt")
	want("landed, d dealt", [2]int{1, 1}, [2]int{2, 2}, [2]int{1, 1}, [2]int{1, 1})
}

// finish reports the primary's work card, taken by m1, finished ok.
func (r *alarmRig) finish(id string) {
	r.t.Helper()
	s := r.snap()
	wc := s.Fleet.Card(s.Work.Card(id).F("work"))
	require.NotNil(r.t, wc, "%s has a work card", id)
	require.Equal(r.t, sprint.Working, wc.Col, "%s's work card is taken", id)
	r.must(store.FinishStep(sprint.FinishReq{As: "m1", Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: wc.Int("gen")}, Who: "m1"}))
}

// open is the id of the open judgment of the type.
func (r *alarmRig) open(typ string) string {
	r.t.Helper()
	for _, o := range r.snap().Open {
		if o.Note.Type == typ {
			return o.Note.ID
		}
	}
	require.Fail(r.t, "no open judgment of type "+typ)
	return ""
}
