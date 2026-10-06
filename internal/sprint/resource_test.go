package sprint_test

import (
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/hostload"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The resources table on the twin (docs/SPEC-SPRINT.md section 11, Resources; the model
// is tla/Resources.tla). The case it was made for, 2026-10-05: a friend claimed the
// shared bench by a bus message, went down out of credit, and another held eight cards
// behind it for half an hour. The owner: "Dining philosophers."

// resourceRig is the hold rig (friends amy and bob, members m1 and m2) with a bench of
// capacity one, and the friends whose session the next tick finds down.
type resourceRig struct {
	*holdRig
	down map[string]bool
}

func newResourceRig(t *testing.T, capacity int) *resourceRig {
	t.Helper()
	r := &resourceRig{holdRig: newHoldRig(t, 0, 0), down: map[string]bool{}}
	_, err := r.st.ResourceAdd(r.ctx, "bench-1", sprint.ResourceBench, capacity)
	require.NoError(t, err)
	return r
}

// tick moves the clock by d, beats every member and every friend not down (a friend
// down is observed down, out of credit, as her daemon says it), and runs one tick.
func (r *resourceRig) tick(d time.Duration) store.TickResult {
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
		obs := sprint.FriendHealth{State: sprint.Up, Seen: r.st.Now(), Generation: sprint.FirstSeatGeneration}
		if r.down[f] {
			obs = sprint.FriendHealth{State: sprint.Down, Seen: r.st.Now(), Generation: sprint.FirstSeatGeneration, Reason: "out of credit"}
		} else {
			_, err := r.st.FriendBeat(r.ctx, f)
			require.NoError(r.t, err)
		}
		_, _, _, err := r.st.FriendHealth(r.ctx, f, "coordinator", obs, "")
		require.NoError(r.t, err)
	}
	res, err := r.st.Tick(r.ctx)
	require.NoError(r.t, err)
	return res
}

func (r *resourceRig) row(name string) store.ResourceRow {
	r.t.Helper()
	rows, err := r.st.Resources(r.ctx)
	require.NoError(r.t, err)
	for _, row := range rows {
		if row.Name == name {
			return row
		}
	}
	r.t.Fatalf("no resource %s in %v", name, rows)
	return store.ResourceRow{}
}

func holderNames(row store.ResourceRow) []string {
	var out []string
	for _, l := range row.Holders {
		out = append(out, l.Member)
	}
	return out
}

func lineNames(row store.ResourceRow) []string {
	var out []string
	for _, w := range row.Line {
		out = append(out, w.Member)
	}
	return out
}

// notesTo is the happened notes of a type addressed to member.
func (r *resourceRig) notesTo(typ, member string) []sprint.Note {
	r.t.Helper()
	notes, _, err := r.st.B.NotesSince(r.ctx, "", 1000)
	require.NoError(r.t, err)
	var out []sprint.Note
	for _, n := range notes {
		if n.Type == typ && n.To == member {
			out = append(out, n)
		}
	}
	return out
}

// A friend holding the bench goes down out of credit: the next tick releases her lease
// and grants the bench to the first in line in the same write, and tells him so; she
// is told her lease went, and the second waiter keeps his place.
func TestAHoldWhoseHolderGoesDownIsReleasedAndTheNextWaiterGetsIt(t *testing.T) {
	t.Parallel()
	r := newResourceRig(t, 1)
	ev, err := r.st.ResourceClaim(r.ctx, "bench-1", "amy", 8*time.Hour)
	require.NoError(t, err)
	assert.Equal(t, sprint.ResourceGranted, ev.Kind, "the bench is free: amy holds it at once")

	ev, err = r.st.ResourceClaim(r.ctx, "bench-1", "bob", time.Hour)
	require.NoError(t, err)
	assert.Equal(t, sprint.ResourceWaiting, ev.Kind)
	assert.Equal(t, 1, ev.Place, "bob is first in line")
	ev, err = r.st.ResourceClaim(r.ctx, "bench-1", "m1", time.Hour)
	require.NoError(t, err)
	assert.Equal(t, 2, ev.Place, "m1 is second")

	r.tick(time.Second)
	row := r.row("bench-1")
	assert.Equal(t, []string{"amy"}, holderNames(row), "amy is up: she keeps it")
	assert.Equal(t, []string{"bob", "m1"}, lineNames(row))

	r.down["amy"] = true
	r.tick(time.Second)
	row = r.row("bench-1")
	assert.Equal(t, []string{"bob"}, holderNames(row), "the tick that read amy down released her lease and granted bob at once")
	assert.Equal(t, []string{"m1"}, lineNames(row), "m1 keeps his place behind bob")
	assert.Equal(t, r.st.Now().Add(time.Hour), row.Holders[0].Until, "bob's lease is the hour he asked for, from the grant")
	assert.Len(t, r.notesTo(sprint.NResourceGranted, "bob"), 1, "bob is told, by a note to him, never by polling")
	gone := r.notesTo(sprint.NResourceReleased, "amy")
	require.Len(t, gone, 1)
	assert.Contains(t, gone[0].What, "down")

	// a member down holds nothing and cannot claim
	_, err = r.st.ResourceClaim(r.ctx, "bench-1", "amy", time.Hour)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "amy is down")

	// a held member's lease goes too
	r.hold(sprint.HoldReq{Names: []string{"bob"}, Reason: "the bench test"})
	r.tick(time.Second)
	assert.Equal(t, []string{"m1"}, holderNames(r.row("bench-1")), "bob held: his lease released and m1 granted")
}

// Capacity is never exceeded: two holders of a capacity-two bench, the third claimant
// waits; a release grants the line's head, never a newcomer ahead of it; a raised
// capacity grants the line at once.
func TestAResourceNeverHasMoreHoldersThanItsCapacity(t *testing.T) {
	t.Parallel()
	r := newResourceRig(t, 2)
	for _, m := range []string{"amy", "bob", "m1", "m2"} {
		_, err := r.st.ResourceClaim(r.ctx, "bench-1", m, time.Hour)
		require.NoError(t, err)
		assert.LessOrEqual(t, len(r.row("bench-1").Holders), 2)
	}
	row := r.row("bench-1")
	assert.Equal(t, []string{"amy", "bob"}, holderNames(row))
	assert.Equal(t, []string{"m1", "m2"}, lineNames(row))

	_, err := r.st.ResourceClaim(r.ctx, "bench-1", "amy", time.Hour)
	require.Error(t, err, "a holder claims again: refused, renew instead")
	assert.Contains(t, err.Error(), "resource renew")

	evs, err := r.st.ResourceRelease(r.ctx, "bench-1", "amy")
	require.NoError(t, err)
	require.Len(t, evs, 2)
	assert.Equal(t, sprint.ResourceGranted, evs[1].Kind)
	assert.Equal(t, "m1", evs[1].Member, "the room freed goes to the line's head in the same write")
	row = r.row("bench-1")
	assert.Equal(t, []string{"bob", "m1"}, holderNames(row))
	assert.Equal(t, []string{"m2"}, lineNames(row))

	// amy comes back: she queues behind m2, never ahead
	ev, err := r.st.ResourceClaim(r.ctx, "bench-1", "amy", time.Hour)
	require.NoError(t, err)
	assert.Equal(t, 2, ev.Place)

	_, err = r.st.ResourceAdd(r.ctx, "bench-1", sprint.ResourceBench, 4)
	require.NoError(t, err)
	row = r.row("bench-1")
	assert.Equal(t, []string{"bob", "m1", "m2", "amy"}, holderNames(row), "a raised capacity grants the line at once")
	assert.Empty(t, row.Line)

	_, err = r.st.ResourceAdd(r.ctx, "bench-1", sprint.ResourcePort, 1)
	require.Error(t, err, "a name keeps its kind")
	_, err = r.st.ResourceClaim(r.ctx, "bench-1", "stranger", time.Hour)
	require.Error(t, err, "a name that is no friend, member or coordinator holds nothing")
}

// An expired lease is released by the tick, and its room granted; a lease is renewed
// only while it is live; renewing keeps it past the old expiry.
func TestAnExpiredLeaseIsReleasedByTheTick(t *testing.T) {
	t.Parallel()
	r := newResourceRig(t, 1)
	_, err := r.st.ResourceClaim(r.ctx, "bench-1", "amy", 10*time.Minute)
	require.NoError(t, err)
	_, err = r.st.ResourceClaim(r.ctx, "bench-1", "bob", 10*time.Minute)
	require.NoError(t, err)

	r.tick(5 * time.Minute)
	ev, err := r.st.ResourceRenew(r.ctx, "bench-1", "amy", 10*time.Minute)
	require.NoError(t, err)
	assert.Equal(t, r.st.Now().Add(10*time.Minute), ev.Until)
	r.tick(6 * time.Minute)
	assert.Equal(t, []string{"amy"}, holderNames(r.row("bench-1")), "renewed: past the old expiry she still holds it")

	r.mu.Lock()
	r.now = r.now.Add(4 * time.Minute) // past the renewed expiry, no tick yet
	r.mu.Unlock()
	row := r.row("bench-1")
	assert.Equal(t, []string{"amy"}, row.Expired, "past its expiry, list says so before a tick")
	_, err = r.st.ResourceRenew(r.ctx, "bench-1", "amy", time.Hour)
	require.Error(t, err, "an expired lease is not renewed, even before the tick releases it")
	assert.Contains(t, err.Error(), "expired")

	r.tick(time.Second)
	row = r.row("bench-1")
	assert.Equal(t, []string{"bob"}, holderNames(row), "the tick released amy's expired lease and granted bob")
	assert.Empty(t, row.Expired)
	gone := r.notesTo(sprint.NResourceReleased, "amy")
	require.Len(t, gone, 1)
	assert.Contains(t, gone[0].What, "expired")
}

// A line that waits with no room past the bound is the coordinator's one judgment, not
// one a tick; a wait that ends and begins again is judged again.
func TestAStarvedLineIsOneJudgment(t *testing.T) {
	t.Parallel()
	r := newResourceRig(t, 1)
	_, err := r.st.ResourceClaim(r.ctx, "bench-1", "amy", sprint.ResourceMaxLease)
	require.NoError(t, err)
	_, err = r.st.ResourceClaim(r.ctx, "bench-1", "bob", time.Hour)
	require.NoError(t, err)
	var id string
	judged := func() int {
		open, err := r.st.B.OpenNotes(r.ctx)
		require.NoError(t, err)
		n := 0
		for _, o := range open {
			if o.Note.Type == sprint.NResourceStarved {
				n++
				id = o.Note.ID
				assert.Equal(t, []string{sprint.ResourceSubject("bench-1")}, o.Note.Primaries)
				assert.Contains(t, o.Note.What, "amy until")
			}
		}
		return n
	}
	r.tick(sprint.ResourceStarveAfter - time.Minute)
	assert.Equal(t, 0, judged(), "within the bound: no judgment")
	r.tick(2 * time.Minute)
	assert.Equal(t, 1, judged(), "past the bound: one judgment")
	r.tick(sprint.ResourceStarveAfter)
	assert.Equal(t, 1, judged(), "still one")
	r.must(store.AckStep(sprint.AckReq{Notes: []string{id}, Reason: "the bench is busy for the day", Who: "coordinator"}))
	assert.Equal(t, 0, judged(), "the coordinator acknowledges it")
	r.tick(sprint.ResourceStarveAfter)
	assert.Equal(t, 0, judged(), "one judgment a wait: an acknowledged wait is not raised again")
}

// The pure table keeps the model's invariants over a long random walk of claims,
// renews, releases, expiries and members going down (tla/Resources.tla: CapacityHeld,
// DownHoldsNothing, NoWaitWithRoom, LineClean).
func TestTheResourceTableKeepsTheModelsInvariants(t *testing.T) {
	t.Parallel()
	members := []string{"a", "b", "c", "d"}
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	tab := &sprint.ResourceTable{}
	for _, name := range []string{"r1", "r2"} {
		_, err := tab.ResourceAdd(name, sprint.ResourceBranch, 2, now)
		require.NoError(t, err)
	}
	seed := uint64(1)
	rnd := func(n int) int { seed = seed*6364136223846793005 + 1442695040888963407; return int((seed >> 33) % uint64(n)) }
	gone := map[string]string{}
	for step := range 5000 {
		now = now.Add(time.Duration(rnd(120)) * time.Second)
		m, name := members[rnd(len(members))], []string{"r1", "r2"}[rnd(2)]
		switch rnd(6) {
		case 0, 1:
			if gone[m] == "" {
				_, _ = tab.ResourceClaim(name, m, time.Duration(1+rnd(10))*time.Minute, now)
			}
		case 2:
			_, _ = tab.ResourceRenew(name, m, time.Duration(1+rnd(10))*time.Minute, now)
		case 3:
			_, _ = tab.ResourceRelease(name, m, now)
		case 4:
			if gone[m] == "" {
				gone[m] = sprint.Down
			} else {
				delete(gone, m)
			}
			tab.ResourceReap(now, gone)
		case 5:
			tab.ResourceReap(now, gone)
		}
		for _, n := range tab.Names() {
			r := tab.Rows[n]
			require.LessOrEqual(t, len(r.Holders), r.Capacity, "step %d: CapacityHeld", step)
			require.False(t, len(r.Line) > 0 && r.Room(), "step %d: NoWaitWithRoom", step)
			seen := map[string]bool{}
			for _, l := range r.Holders {
				require.False(t, seen[l.Member], "step %d: a member holds twice", step)
				seen[l.Member] = true
			}
			for _, w := range r.Line {
				require.False(t, seen[w.Member], "step %d: LineClean", step)
				seen[w.Member] = true
			}
			// a member goes down only with a reap, and claims nothing while down
			for m := range gone {
				require.False(t, seen[m], "step %d: DownHoldsNothing", step)
			}
		}
	}
}
