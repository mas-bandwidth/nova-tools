package sprint_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
)

// Stats tidy on the twin store (the owner, 2026-10-06: "can you please clear the sets of
// done consumer cards for all friends and fleet", "I would like a semi-fresh start to stats
// now"; the verb renamed tidy the same day, "reset sounds too aggressive"). The done cells
// lose their history, the archive keeps it, and the work stays where it was.

// places is every card of every table at its place and generation, by table and id.
func places(s *sprint.Snapshot) map[string]map[string]string {
	out := map[string]map[string]string{}
	for name, t := range map[string]*sprint.Table{sprint.Work: s.Work, sprint.Merge: s.Merge, sprint.Readers: s.Readers, sprint.Fleet: s.Fleet} {
		out[name] = map[string]string{}
		for _, c := range t.Cards() {
			if c.Placed() {
				out[name][c.ID] = c.Row + ":" + c.Col + "@" + c.F("gen")
			}
		}
	}
	return out
}

// doneCounts is each fleet row's ok and failed counts, as the table's done and ok% count them.
func doneCounts(s *sprint.Snapshot) map[string][2]int {
	out := map[string][2]int{}
	for _, row := range s.Fleet.Rows() {
		out[row] = [2]int{s.Fleet.Count(row, sprint.DoneOK), s.Fleet.Count(row, sprint.DoneFailed)}
	}
	return out
}

// landOnM1 lands s1-1..s1-n on m1 alone (m2 down), one a minute, priced: n finished work
// cards on one row, newest last, so the row holds more than the rest rule's sample.
func (r *conflictRig) landOnM1(n int) {
	r.t.Helper()
	r.must(store.FleetStep(sprint.FleetReq{Op: "down", Member: "m2"}))
	if n > 3 {
		r.must(store.AddStep(sprint.AddReq{Stream: "s1", Count: n - 3, Brief: "c: the work (s1) tier: flash\nREPO: mas-bandwidth/nova-tools\n\nThe task.\n"}))
	}
	for i := 1; i <= n; i++ {
		r.mu.Lock()
		r.now = r.now.Add(time.Minute)
		r.mu.Unlock()
		r.beat()
		id := fmt.Sprintf("s1-%d", i)
		r.landWithCost("s1", id)
		// RUNNING landing queues its Work move; drain it before the next
		// card so tidy sees every completed primary on its landed cell.
		r.must(store.DrainStep())
		require.Equal(r.t, sprint.Landed, r.snap().StateOf(id), "%s landed", id)
	}
}

// archiveOf is the archive record at key.
func (r *conflictRig) archiveOf(key string) store.StatsArchive {
	r.t.Helper()
	var arch store.StatsArchive
	raw, ok, err := r.m.GetKey(r.ctx, key)
	require.NoError(r.t, err)
	require.True(r.t, ok, "the archive record %s is written", key)
	require.NoError(r.t, json.Unmarshal([]byte(raw), &arch))
	return arch
}

func TestStatsTidyZeroesCountersAndKeepsTheWork(t *testing.T) {
	t.Parallel()
	r := newConflictRig(t)
	_, _, _, err := r.st.SetMachine(r.ctx, true)
	require.NoError(t, err)
	// thirteen s1 cards landed on m1, priced: their work cards are history
	r.landOnM1(13)
	// s2: one card merging, one in review, one working, one dealt, one ready: the work
	r.must(store.AddStep(sprint.AddReq{Stream: "s2", Count: 5, Brief: "c: the work (s2) tier: flash\nREPO: mas-bandwidth/nova-tools\n\nThe task.\n"}))
	r.toMerging("s2-1")
	r.must(dealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s2-2", "s2-3", "s2-4"}}}))
	for _, id := range []string{"s2-2", "s2-3"} {
		wc := r.snap().Fleet.Card(r.snap().Work.Card(id).F("work"))
		r.must(store.TakeStep(sprint.TakeReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: wc.Int("gen")}}))
	}
	wc := r.snap().Fleet.Card(r.snap().Work.Card("s2-2").F("work"))
	r.must(store.FinishStep(sprint.FinishReq{Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: wc.Int("gen")}}))
	r.must(store.DrainStep())
	// an hour of RUNNING on, and half a second: no finish is within the keep window, which
	// is running time (a stopped machine's hour would keep every finish)
	_, _, _, err = r.st.SetMachine(r.ctx, true)
	require.NoError(t, err)
	r.mu.Lock()
	r.now = r.now.Add(time.Hour + 500*time.Millisecond)
	r.mu.Unlock()

	before := r.snap()
	cols := map[string]string{}
	for _, id := range []string{"s1-1", "s1-13", "s2-1", "s2-2", "s2-3", "s2-4", "s2-5"} {
		cols[id] = before.Work.Card(id).Col
	}
	assert.Equal(t, map[string]string{"s1-1": sprint.Landed, "s1-13": sprint.Landed, "s2-1": sprint.Merging,
		"s2-2": sprint.Review, "s2-3": sprint.Working, "s2-4": sprint.Working, "s2-5": sprint.Ready}, cols, "cards in every column")
	assert.Equal(t, sprint.Ready, before.Fleet.Card(before.Work.Card("s2-4").F("work")).Col, "s2-4 dealt, not taken")
	counts := doneCounts(before)
	require.Equal(t, [2]int{15, 0}, counts["m1"], "thirteen landed, one merging, one in review: fifteen done on m1")
	open, err := r.m.OpenNotes(r.ctx)
	require.NoError(t, err)
	where := places(before)
	costBefore := before.StreamCtl("s1").F(sprint.FieldCost)
	require.NotEmpty(t, costBefore)

	// --dry-run says what would move and writes nothing
	dry, err := r.st.TidyStats(r.ctx, store.TidyReq{Kinds: sprint.TidyKinds, Reason: "a fresh start", DryRun: true})
	require.NoError(t, err)
	assert.Equal(t, 5, dry.Moved)
	assert.Equal(t, 10, dry.Kept)
	assert.Equal(t, where, places(r.snap()), "a dry run moves nothing")
	_, ok, err := r.m.GetKey(r.ctx, dry.Archive)
	require.NoError(t, err)
	assert.False(t, ok, "a dry run writes no archive")
	rec, err := r.st.StatsTidied(r.ctx)
	require.NoError(t, err)
	assert.Zero(t, rec.Since, "a dry run records nothing")

	res, err := r.st.TidyStats(r.ctx, store.TidyReq{Kinds: sprint.TidyKinds, Reason: "a fresh start"})
	require.NoError(t, err)
	require.Empty(t, res.Refused)
	assert.Equal(t, 5, res.Moved, "the five oldest: the row's newest ten stay (the rest rule's sample)")
	assert.Equal(t, 10, res.Kept)
	at := r.st.Now().UTC()
	assert.True(t, strings.HasPrefix(res.Archive, "stats:archive:"+at.Format(time.RFC3339Nano)+"-"), res.Archive)

	after := r.snap()
	var kept []string
	for _, c := range append(after.Fleet.Cell("m1", sprint.DoneOK), after.Fleet.Cell("m1", sprint.DoneFailed)...) {
		kept = append(kept, c.F(sprint.PrimaryField))
	}
	assert.ElementsMatch(t, []string{"s2-1", "s2-2", "s1-6", "s1-7", "s1-8", "s1-9", "s1-10", "s1-11", "s1-12", "s1-13"}, kept,
		"the live work's cards and the newest ten stay; the history left the done cells")
	for i := 1; i <= 5; i++ {
		assert.Nil(t, after.Fleet.Placed(sprint.WorkCardID(fmt.Sprintf("s1-%d", i), 1)), "s1-%d's work card is off the done cell", i)
	}
	// every record kept: read by id, as stats reads it
	s, err := r.st.Load(r.ctx, []string{sprint.Work, sprint.Fleet, sprint.Readers}, sprint.StatsRecords)
	require.NoError(t, err)
	for i := 1; i <= 5; i++ {
		w := s.Fleet.Card(sprint.WorkCardID(fmt.Sprintf("s1-%d", i), 1))
		require.NotNil(t, w, "s1-%d's work card record is kept", i)
		assert.Equal(t, "yes", w.F("ok"))
	}
	// every other card where it was: work, merge, readers, and the fleet's open cells
	got := places(after)
	for _, name := range []string{sprint.Work, sprint.Merge, sprint.Readers} {
		assert.Equal(t, where[name], got[name], "the %s table is untouched", name)
	}
	for id, place := range where[sprint.Fleet] {
		if c := before.Fleet.Card(id); c.Col == sprint.DoneOK || c.Col == sprint.DoneFailed {
			continue
		}
		assert.Equal(t, place, got[sprint.Fleet][id], "%s stays where it was", id)
	}
	for id := range cols {
		assert.Equal(t, cols[id], after.Work.Card(id).Col, id)
	}
	openAfter, err := r.m.OpenNotes(r.ctx)
	require.NoError(t, err)
	assert.Equal(t, open, openAfter, "the judgments are unchanged")
	r.clean("tidied")

	// the archive holds the old values, done, each card with the cell it came from
	arch := r.archiveOf(res.Archive)
	assert.Equal(t, store.ArchiveDone, arch.State)
	assert.Equal(t, "a fresh start", arch.Reason)
	assert.Equal(t, sprint.TidyKinds, arch.Kinds)
	require.Len(t, arch.Rows, 1)
	assert.Equal(t, "m1", arch.Rows[0].Row)
	assert.Equal(t, 15, arch.Rows[0].OK)
	var moved []sprint.TidyCard
	for _, c := range arch.Rows[0].Moved {
		assert.False(t, c.Finished.IsZero(), "%s carries its finish stamp", c.ID)
		moved = append(moved, sprint.TidyCard{ID: c.ID, Cell: c.Cell})
	}
	want := []sprint.TidyCard{}
	for i := 1; i <= 5; i++ {
		want = append(want, sprint.TidyCard{ID: sprint.WorkCardID(fmt.Sprintf("s1-%d", i), 1), Cell: "ok"})
	}
	assert.ElementsMatch(t, want, moved)
	assert.Equal(t, sprint.StreamBase{Cost: costBefore, Landed: 13}, arch.Streams["s1"])
	assert.NotEmpty(t, arch.Routes, "the route counters are archived")

	// the record names the tidy, to the nanosecond; the stream's cost counts from it
	rec, err = r.st.StatsTidied(r.ctx)
	require.NoError(t, err)
	assert.Equal(t, at, rec.Since)
	assert.Equal(t, "a fresh start", rec.Reason)
	assert.Equal(t, []string{res.Archive}, rec.Archives)
	since, err := r.st.StatsSince(r.ctx, sprint.TidyStreams)
	require.NoError(t, err)
	assert.Equal(t, at, since)
	pinned, err := r.st.Pinned(r.ctx)
	require.NoError(t, err)
	shapes, err := r.m.Shapes(r.ctx, []string{pinned.Names.Table(sprint.Work)})
	require.NoError(t, err)
	row := shapes[0].Row("s1")
	require.GreaterOrEqual(t, row, 0)
	assert.Equal(t, "-", shapes[0].Rows[row].Texts[sprint.Cost], "nothing landed in s1 since the tidy")
	assert.Equal(t, costBefore, after.StreamCtl("s1").F(sprint.FieldCost), "the control card keeps the epoch's cost")

	// stats counts from the tidy: nothing has happened since; the whole epoch is still there
	ps := sprint.StatsSince(s, since)
	assert.Equal(t, since, ps.Since)
	assert.Zero(t, ps.Primaries)
	assert.Empty(t, ps.Work)
	assert.Empty(t, ps.Routes)
	assert.Equal(t, 18, sprint.Stats(s).Primaries)

	// an unreadable stats record is no tidy: the tick and the mirrors run on
	require.NoError(t, r.m.SetKey(r.ctx, "stats", "{not json"))
	_, err = r.st.Tick(r.ctx)
	require.NoError(t, err, "an unreadable stats record never fails a tick")
	require.NoError(t, pinned.SyncMirrors(r.ctx), "nor a mirror sync")
	rec, err = r.st.StatsTidied(r.ctx)
	require.NoError(t, err)
	assert.Zero(t, rec.Since)
}

// The boundary of the second tidy, to the nanosecond: 59 seconds and a half after the
// last is refused, nothing written; 61 seconds after runs.
func TestASecondStatsTidyWithinAMinuteIsRefused(t *testing.T) {
	t.Parallel()
	r := newConflictRig(t)
	_, _, _, err := r.st.SetMachine(r.ctx, false)
	require.NoError(t, err)
	step := func(d time.Duration) {
		r.mu.Lock()
		r.now = r.now.Add(d)
		r.mu.Unlock()
	}
	step(900 * time.Millisecond) // a whole-second compare would read the next tidy as 60.4 s on
	first, err := r.st.TidyStats(r.ctx, store.TidyReq{Kinds: []string{sprint.TidyFleet}, Reason: "first"})
	require.NoError(t, err)
	require.Empty(t, first.Refused)
	step(59*time.Second + 500*time.Millisecond)
	second, err := r.st.TidyStats(r.ctx, store.TidyReq{Kinds: []string{sprint.TidyFleet}, Reason: "second"})
	require.NoError(t, err)
	assert.Contains(t, second.Refused, "a second within 1m0s is refused, nothing written", "59.5 s on: refused")
	rec, err := r.st.StatsTidied(r.ctx)
	require.NoError(t, err)
	assert.Equal(t, "first", rec.Reason, "the refused tidy wrote nothing")
	step(1500 * time.Millisecond) // 61 s after the first
	third, err := r.st.TidyStats(r.ctx, store.TidyReq{Kinds: []string{sprint.TidyFleet}, Reason: "third"})
	require.NoError(t, err)
	assert.Empty(t, third.Refused, "61 s on: it runs")
	rec, err = r.st.StatsTidied(r.ctx)
	require.NoError(t, err)
	assert.Equal(t, "third", rec.Reason)
	assert.Len(t, rec.Archives, 2)
	assert.NotEqual(t, rec.Archives[0], rec.Archives[1])
}

// onTidy is the twin store with another writer that acts as the tidy's step first takes
// the fence: act runs once, and an error it returns is the acquire's, after which no
// archive record can be written (the store lost them): an archive there was written first.
type onTidy struct {
	*store.Mem
	act   func(ctx context.Context) error
	fired *bool
	lost  *bool
}

func (o onTidy) AtEpoch(epoch uint64, old bool) store.Backend {
	return onTidy{Mem: o.Mem.AtEpoch(epoch, old).(*store.Mem), act: o.act, fired: o.fired, lost: o.lost}
}

func (o onTidy) Acquire(ctx context.Context, gen uint64, op store.OpRecord) (bool, error) {
	if strings.HasPrefix(op.Verb, "stats tidy") && !*o.fired {
		*o.fired = true
		if err := o.act(ctx); err != nil {
			*o.lost = true
			return false, err
		}
	}
	return o.Mem.Acquire(ctx, gen, op)
}

func (o onTidy) SetKey(ctx context.Context, name, value string) error {
	if *o.lost && strings.Contains(name, "stats:archive:") {
		return errors.New("the store lost its archive writes")
	}
	return o.Mem.SetKey(ctx, name, value)
}

// A card that moves between the tidy's plan and its move is not tidied, and the archive,
// written before anything moved, says what did move; a move that fails keeps its archive,
// failed, with the plan.
func TestATidyWhoseCardMovedKeepsItsArchive(t *testing.T) {
	t.Parallel()
	r := newConflictRig(t)
	_, _, _, err := r.st.SetMachine(r.ctx, true)
	require.NoError(t, err)
	r.landOnM1(12)
	_, _, _, err = r.st.SetMachine(r.ctx, true)
	require.NoError(t, err)
	r.mu.Lock()
	r.now = r.now.Add(time.Hour)
	r.mu.Unlock()
	first := sprint.WorkCardID("s1-1", 1)
	tidier := func(act func(ctx context.Context) error) *store.Store {
		fired, lost := false, false
		return &store.Store{B: onTidy{Mem: r.m, act: act, fired: &fired, lost: &lost}, Names: r.st.Names, Actor: "coordinator", Now: r.st.Now, NewID: r.st.NewID, Sleep: r.st.Sleep}
	}

	// the move fails and no archive can be written after it: the archive written before it
	// stays, planned, with the plan; nothing moved, no tidy recorded
	failing := tidier(func(context.Context) error { return errors.New("the store went away") })
	res, err := failing.TidyStats(r.ctx, store.TidyReq{Kinds: []string{sprint.TidyFleet}, Reason: "fails"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "the store went away")
	assert.Contains(t, err.Error(), "the archive "+res.Archive+" is kept")
	arch := r.archiveOf(res.Archive)
	assert.Equal(t, store.ArchivePlanned, arch.State, "written before the move")
	require.Len(t, arch.Rows, 1)
	assert.Len(t, arch.Rows[0].Moved, 2, "the plan: s1-1 and s1-2")
	assert.Equal(t, sprint.DoneOK, r.snap().Fleet.Card(first).Col, "nothing moved")
	rec, err := r.st.StatsTidied(r.ctx)
	require.NoError(t, err)
	assert.Zero(t, rec.Since)
	assert.Equal(t, []string{res.Archive}, rec.Archives, "teardown still finds the failed archive")

	// another writer takes s1-1's card off its done cell first: it is not tidied
	moving := tidier(func(ctx context.Context) error {
		other := &store.Store{B: r.m, Names: r.st.Names, Actor: "coordinator", Now: r.st.Now, NewID: r.st.NewID, Sleep: r.st.Sleep}
		_, err := other.Run(ctx, store.Step{Verb: "poke", Load: []string{sprint.Fleet}, Plan: func(s *sprint.Snapshot) sprint.Plan {
			c := s.Fleet.Card(first)
			return sprint.Plan{Units: []sprint.Unit{{Key: first, Changes: []sprint.Change{{Table: sprint.Fleet, Entry: ntable.BatchMemberEntry{ID: first,
				Expect: &ntable.MemberExpect{Place: &ntable.PlaceExpect{Row: c.Row, Col: c.Col}}, Move: &ntable.MemberMoveOp{Row: c.Row, Col: sprint.Withdrawn}}}}}}}
		}})
		return err
	})
	res, err = moving.TidyStats(r.ctx, store.TidyReq{Kinds: []string{sprint.TidyFleet}, Reason: "moves"})
	require.NoError(t, err)
	arch = r.archiveOf(res.Archive)
	assert.Equal(t, store.ArchiveDone, arch.State)
	require.Len(t, arch.Rows, 1)
	require.Len(t, arch.Rows[0].Moved, 1, "the moved card is not tidied; the archive says what did move")
	assert.Equal(t, sprint.TidyCard{ID: sprint.WorkCardID("s1-2", 1), Cell: "ok"}, sprint.TidyCard{ID: arch.Rows[0].Moved[0].ID, Cell: arch.Rows[0].Moved[0].Cell})
	assert.Equal(t, sprint.Withdrawn, r.snap().Fleet.Card(first).Col, "it stays where the other writer put it")
	assert.Nil(t, r.snap().Fleet.Placed(sprint.WorkCardID("s1-2", 1)))
}
