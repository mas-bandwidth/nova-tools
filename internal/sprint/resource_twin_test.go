package sprint_test

import (
	"context"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/hostload"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The resources table on the twin store (store.Mem): the coordinator's verbs and
// the tick's pass (docs/SPEC-SPRINT.md section 19; the model is tla/Resources.tla).
// The night of 2026-10-05: a friend held the shared bench by a bus message, went
// down out of credit, and the bench stayed held for the morning. Here the tick
// releases her lease at once and grants the next in line.

// resourceRig is a holdRig with the resources' own tick: it moves the clock by
// d, beats everyone but the friends named down (each of those is observed down,
// out of credit), and runs one tick.
type resourceRig struct {
	*holdRig
	down map[string]bool
}

func newResourceRig(t *testing.T) *resourceRig {
	t.Helper()
	r := &resourceRig{holdRig: newHoldRig(t, 2, 0), down: map[string]bool{}}
	_, err := r.st.ResourceAdd(r.ctx, "bench-a", "bench", 1)
	require.NoError(t, err)
	return r
}

func (r *resourceRig) tickAfter(d time.Duration) store.TickResult {
	r.t.Helper()
	r.mu.Lock()
	r.now = r.now.Add(d)
	r.mu.Unlock()
	require.NoError(r.t, r.st.BeatReaders(r.ctx))
	zero := 0.0
	for _, m := range []string{"m1", "m2"} {
		_, err := r.st.Beat(r.ctx, m, &zero, hostload.Source{})
		require.NoError(r.t, err)
	}
	for _, f := range []string{"amy", "bob"} {
		_, err := r.st.FriendBeat(r.ctx, f)
		require.NoError(r.t, err)
		obs := sprint.FriendHealth{State: sprint.Up, Seen: r.st.Now(), Generation: sprint.FirstSeatGeneration}
		if r.down[f] {
			obs = sprint.FriendHealth{State: sprint.Down, Seen: r.st.Now(), Generation: sprint.FirstSeatGeneration, Reason: "out of credit", Until: r.st.Now().Add(6 * time.Hour)}
		}
		_, _, _, err = r.st.FriendHealth(r.ctx, f, "coordinator", obs, "")
		require.NoError(r.t, err)
	}
	res, err := r.st.Tick(r.ctx)
	require.NoError(r.t, err)
	return res
}

func (r *resourceRig) row(name string) sprint.ResourceRow {
	r.t.Helper()
	rows, err := r.st.ResourceRows(r.ctx)
	require.NoError(r.t, err)
	for _, row := range rows {
		if row.Name == name {
			return row
		}
	}
	r.t.Fatalf("no resource %s", name)
	return sprint.ResourceRow{}
}

func holderNames(row sprint.ResourceRow) []string {
	out := []string{}
	for _, h := range row.Holders {
		out = append(out, h.Who)
	}
	return out
}

func waiterNames(row sprint.ResourceRow) []string {
	out := []string{}
	for _, w := range row.Waiters {
		out = append(out, w.Who)
	}
	return out
}

// notesOf is the log's notes of the type, as written.
func (r *resourceRig) notesOf(typ string) []sprint.Note {
	r.t.Helper()
	lines, err := r.st.Log(r.ctx)
	require.NoError(r.t, err)
	var out []sprint.Note
	for _, l := range lines {
		if l.Note != nil && l.Note.Type == typ {
			out = append(out, *l.Note)
		}
	}
	return out
}

func (r *resourceRig) openOf(typ string) []sprint.Open {
	r.t.Helper()
	var out []sprint.Open
	for _, o := range r.snap().Open {
		if o.Note.Type == typ {
			out = append(out, o)
		}
	}
	return out
}

func TestAHoldWhoseHolderGoesDownIsReleasedAndTheNextWaiterGetsIt(t *testing.T) {
	t.Parallel()
	r := newResourceRig(t)
	ctx := context.Background()
	ans, err := r.st.ResourceClaim(ctx, "bench-a", "amy", 2*time.Hour)
	require.NoError(t, err)
	assert.True(t, ans.Granted)
	ans, err = r.st.ResourceClaim(ctx, "bench-a", "bob", time.Hour)
	require.NoError(t, err)
	assert.False(t, ans.Granted)
	assert.Equal(t, 1, ans.Place)
	// a tick with amy up changes nothing
	r.tickAfter(time.Second)
	assert.Equal(t, []string{"amy"}, holderNames(r.row("bench-a")))
	assert.Equal(t, []string{"bob"}, waiterNames(r.row("bench-a")))
	assert.Equal(t, sprint.Up, r.friendStatus("amy"))
	// amy goes down out of credit: the next tick releases her lease and grants bob
	r.down["amy"] = true
	res := r.tickAfter(time.Second)
	assert.Equal(t, sprint.Down, r.friendStatus("amy"))
	row := r.row("bench-a")
	assert.Equal(t, []string{"bob"}, holderNames(row), "the next waiter holds it")
	assert.Equal(t, []string{}, waiterNames(row))
	require.Len(t, row.Holders, 1)
	assert.Equal(t, r.st.Now().Add(time.Hour), row.Holders[0].Until, "bob's lease runs its own length from the grant")
	found := false
	for _, p := range res.Parts {
		found = found || p.Name == "resources"
	}
	assert.True(t, found, "the tick's parts name the resources' pass: %+v", res.Parts)
	notes := r.notesOf(sprint.NResourceReleased)
	require.Len(t, notes, 1)
	assert.Equal(t, "resource bench-a: holder down: amy; granted: bob", notes[0].What)
	// amy back up holds nothing: she claims again and waits behind bob
	r.down["amy"] = false
	r.tickAfter(time.Second)
	ans, err = r.st.ResourceClaim(ctx, "bench-a", "amy", time.Hour)
	require.NoError(t, err)
	assert.False(t, ans.Granted)
	assert.Equal(t, 1, ans.Place)
}

func TestAnExpiredLeaseIsReleasedByTheTick(t *testing.T) {
	t.Parallel()
	r := newResourceRig(t)
	ctx := context.Background()
	_, err := r.st.ResourceClaim(ctx, "bench-a", "amy", time.Minute)
	require.NoError(t, err)
	_, err = r.st.ResourceClaim(ctx, "bench-a", "m1", 2*time.Hour)
	require.NoError(t, err)
	r.tickAfter(30 * time.Second)
	assert.Equal(t, []string{"amy"}, holderNames(r.row("bench-a")), "the lease has time left")
	r.tickAfter(31 * time.Second)
	row := r.row("bench-a")
	assert.Equal(t, []string{"m1"}, holderNames(row), "past its expiry the lease is released and the head granted")
	assert.Equal(t, []string{}, waiterNames(row))
	notes := r.notesOf(sprint.NResourceReleased)
	require.Len(t, notes, 1)
	assert.Equal(t, "resource bench-a: expired: amy; granted: m1", notes[0].What)
	// a renewal keeps a lease alive past where it would have expired
	_, err = r.st.ResourceRenew(ctx, "bench-a", "m1", 3*time.Hour)
	require.NoError(t, err)
	r.tickAfter(2*time.Hour + time.Second)
	assert.Equal(t, []string{"m1"}, holderNames(r.row("bench-a")))
}

func TestCapacityIsNeverExceededOnTheTwin(t *testing.T) {
	t.Parallel()
	r := newResourceRig(t)
	ctx := context.Background()
	_, err := r.st.ResourceAdd(ctx, "bench-a", "", 2)
	require.NoError(t, err, "capacity set on a row that is there")
	for _, who := range []string{"amy", "bob", "m1", "m2"} {
		_, err := r.st.ResourceClaim(ctx, "bench-a", who, time.Hour)
		require.NoError(t, err)
		assert.LessOrEqual(t, len(r.row("bench-a").Holders), 2)
	}
	assert.Equal(t, []string{"amy", "bob"}, holderNames(r.row("bench-a")))
	assert.Equal(t, []string{"m1", "m2"}, waiterNames(r.row("bench-a")))
	// a release grants the head, and only the head
	ans, err := r.st.ResourceRelease(ctx, "bench-a", "amy")
	require.NoError(t, err)
	assert.Equal(t, []string{"m1"}, ans.GrantedNow)
	assert.Equal(t, []string{"bob", "m1"}, holderNames(r.row("bench-a")))
	// a narrower capacity takes no lease back and grants none
	_, err = r.st.ResourceAdd(ctx, "bench-a", "", 1)
	require.NoError(t, err)
	_, err = r.st.ResourceRelease(ctx, "bench-a", "bob")
	require.NoError(t, err)
	assert.Equal(t, []string{"m1"}, holderNames(r.row("bench-a")))
	assert.Equal(t, []string{"m2"}, waiterNames(r.row("bench-a")))
	for range 3 {
		r.tickAfter(time.Second)
		assert.LessOrEqual(t, len(r.row("bench-a").Holders), 1)
	}
	_, err = r.st.ResourceAdd(ctx, "bench-a", "port", 1)
	assert.ErrorContains(t, err, "its kind does not change")
}

func TestAResourceStarvedPastTheBoundRaisesOneJudgment(t *testing.T) {
	t.Parallel()
	r := newResourceRig(t)
	ctx := context.Background()
	_, err := r.st.ResourceClaim(ctx, "bench-a", "amy", 3*time.Hour)
	require.NoError(t, err)
	_, err = r.st.ResourceClaim(ctx, "bench-a", "bob", time.Hour)
	require.NoError(t, err)
	// the line has no room: under the bound, no judgment, however many ticks
	for range 5 {
		r.tickAfter(5 * time.Minute)
		assert.Empty(t, r.openOf(sprint.NResourceStarved))
	}
	r.tickAfter(5*time.Minute - time.Second)
	assert.Empty(t, r.openOf(sprint.NResourceStarved))
	// at the bound: one judgment, open on the resource, and one only while it stands
	r.tickAfter(time.Second)
	open := r.openOf(sprint.NResourceStarved)
	require.Len(t, open, 1)
	assert.Equal(t, sprint.ResourceSubject("bench-a"), open[0].Subject())
	assert.Contains(t, open[0].Note.What, "resource bench-a (bench, 1/1) has had waiters and no room since")
	assert.Contains(t, open[0].Note.What, "held by amy until")
	assert.Contains(t, open[0].Note.What, "waiting: bob")
	assert.Equal(t, sprint.ResourceDecisions, open[0].Note.Decisions)
	r.tickAfter(time.Minute)
	r.tickAfter(time.Minute)
	assert.Len(t, r.openOf(sprint.NResourceStarved), 1, "raised once while it stands")
	assert.Len(t, r.notesOf(sprint.NResourceStarved), 1)
	// the coordinator releases the holder: bob is granted, the line is empty, and
	// the judgment closes at the next tick
	_, err = r.st.ResourceRelease(ctx, "bench-a", "amy")
	require.NoError(t, err)
	assert.Equal(t, []string{"bob"}, holderNames(r.row("bench-a")))
	r.tickAfter(time.Second)
	assert.Empty(t, r.openOf(sprint.NResourceStarved), "closed once the line has room")
}
