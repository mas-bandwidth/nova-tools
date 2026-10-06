package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The resources table (sprint/resource.go; docs/SPEC-SPRINT.md section 11, Resources;
// the model is tla/Resources.tla). It is one record of the deployment's, beside the
// friends record and outside the sprint's tables, so a clear keeps it: a bench is held
// across epochs. Every change is one read-modify-write of the record under the sprint's
// fence, taken as a lock (lock.go): a claim, a release and the tick's reap never
// interleave, which is the model's one action at a time. A grant, and a release the
// tick made, is a happened note addressed to the member; a line waiting with no room
// past sprint.ResourceStarveAfter is the coordinator's one judgment of it.

// keyResources is the resources record.
const keyResources = "resources"

// ResourceRow is one resource as resource list shows it: its holders, each lease's
// expiry and whether it is past it (the next tick releases it), and its line in order.
type ResourceRow struct {
	sprint.Resource
	Expired []string `json:"expired,omitempty"`
}

// resourceTable reads the record; an empty table when there is none.
func resourceTable(ctx context.Context, kv KV) (*sprint.ResourceTable, error) {
	t := &sprint.ResourceTable{Rows: map[string]*sprint.Resource{}}
	raw, ok, err := kv.GetKey(ctx, keyResources)
	if err != nil || !ok || raw == "" {
		return t, err
	}
	if err := json.Unmarshal([]byte(raw), t); err != nil {
		return nil, fmt.Errorf("the resources record cannot be read (%v); nothing was changed", err)
	}
	if t.Rows == nil {
		t.Rows = map[string]*sprint.Resource{}
	}
	return t, nil
}

// resourcesKV is the store's records, or the refusal of a store that keeps none.
func (st *Store) resourcesKV() (KV, error) {
	kv, err := st.rootKV()
	if err != nil {
		return nil, errors.New("this store keeps no records, so it keeps no resources")
	}
	return kv, nil
}

// resourceLocked takes the sprint's fence as a lock at the generation it reads (a fence
// held by another operation is waited on and read again), reads the resources record,
// runs fn on it, writes it when fn says it changed, and releases the lock unwritten.
func (st *Store) resourceLocked(ctx context.Context, verb string, fn func(t *sprint.ResourceTable) (bool, error)) error {
	kv, err := st.resourcesKV()
	if err != nil {
		return err
	}
	pinned, err := st.pin(ctx)
	if err != nil {
		return err
	}
	r := pinned.retry(withBudget(ctx))
	for r.next(pinned.attempts()) {
		f, err := pinned.B.ReadFence(ctx)
		if err != nil {
			return err
		}
		if f.Pending != nil {
			continue // another operation holds the fence: wait, and read again
		}
		lock := OpRecord{ID: "resource-" + pinned.newID() + "-lock", Verb: verb + " lock", At: pinned.now(), Lock: true}
		ok, err := pinned.B.Acquire(ctx, f.Gen, lock)
		if err != nil {
			return err
		}
		if !ok {
			continue // another writer moved the generation: read again
		}
		ferr := func() error {
			t, err := resourceTable(ctx, kv)
			if err != nil {
				return err
			}
			changed, err := fn(t)
			if err != nil || !changed {
				return err
			}
			b, err := json.Marshal(t)
			if err != nil {
				return err
			}
			return kv.SetKey(ctx, keyResources, string(b))
		}()
		return errors.Join(ferr, pinned.B.Release(ctx, lock, false))
	}
	return fmt.Errorf("%s: the sprint's fence stayed held (%d tries); nothing was changed; run it again", verb, r.tries)
}

// ResourceStatuses is every member that may hold a resource and its status now: each
// friend of the roster (up, held, or down: no session, or out of credit) and each fleet
// member (up, held, or down by its beat). A store with no sprint has no fleet member.
func (st *Store) ResourceStatuses(ctx context.Context) (map[string]string, error) {
	out := map[string]string{}
	now := st.now()
	friends, err := st.FriendRows(ctx, now)
	if err != nil {
		return nil, err
	}
	for _, f := range friends {
		out[f.Name] = f.Status
	}
	pinned, err := st.pin(ctx)
	if refusalCode(err) == "NOTABLE" {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	shape, beats, err := pinned.fleetBeats(ctx, nil)
	if refusalCode(err) == "NOTABLE" {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	for _, row := range shape.Rows {
		if sprint.IsFriendRow(row.Key) {
			continue
		}
		out[row.Key] = sprint.PresenceStatus(row.Texts[sprint.Status] == sprint.Held, beats[row.Key], now)
	}
	return out, nil
}

// goneOf is the members whose status takes their leases, with why.
func goneOf(statuses map[string]string) map[string]string {
	gone := map[string]string{}
	for m, s := range statuses {
		if why := sprint.ResourceGone(s); why != "" {
			gone[m] = why
		}
	}
	return gone
}

// ResourceAdd adds a resource or sets its capacity (the coordinator's resource add).
func (st *Store) ResourceAdd(ctx context.Context, name, kind string, capacity int) ([]sprint.ResourceEvent, error) {
	var evs []sprint.ResourceEvent
	err := st.resourceLocked(ctx, "resource add", func(t *sprint.ResourceTable) (bool, error) {
		var err error
		evs, err = t.ResourceAdd(name, kind, capacity, st.now())
		return err == nil, err
	})
	if err != nil {
		return nil, err
	}
	return evs, st.tellResources(ctx, "resource add", evs, nil)
}

// ResourceClaim is member's claim of a resource for d (resource claim): refused for a
// name that is not a friend, a fleet member or the coordinator, and for a member down or
// held; the table reaped first, as the tick reaps it, so the claim never queues behind
// a lease the tick would release.
func (st *Store) ResourceClaim(ctx context.Context, name, member string, d time.Duration) (sprint.ResourceEvent, error) {
	statuses, err := st.ResourceStatuses(ctx)
	if err != nil {
		return sprint.ResourceEvent{}, err
	}
	if err := st.mayHold(ctx, member, statuses); err != nil {
		return sprint.ResourceEvent{}, err
	}
	var ev sprint.ResourceEvent
	var reaped []sprint.ResourceEvent
	var refused error
	err = st.resourceLocked(ctx, "resource claim", func(t *sprint.ResourceTable) (bool, error) {
		reaped = t.ResourceReap(st.now(), goneOf(statuses))
		ev, refused = t.ResourceClaim(name, member, d, st.now())
		// a refused claim still writes the reap it made
		return refused == nil || len(reaped) > 0, nil
	})
	if err != nil {
		return ev, err
	}
	if ev.Kind == sprint.ResourceGranted {
		// granted at once: the claimant is told by the verb, the record says so
		ev.Why = "claimed"
	}
	return ev, errors.Join(refused, st.tellResources(ctx, "resource claim", reaped, nil))
}

// ResourceRenew moves member's live lease of a resource to now+d (resource renew).
func (st *Store) ResourceRenew(ctx context.Context, name, member string, d time.Duration) (sprint.ResourceEvent, error) {
	var ev sprint.ResourceEvent
	err := st.resourceLocked(ctx, "resource renew", func(t *sprint.ResourceTable) (bool, error) {
		var err error
		ev, err = t.ResourceRenew(name, member, d, st.now())
		return err == nil, err
	})
	return ev, err
}

// ResourceRelease gives member's lease of a resource back, or takes it out of the line,
// and grants the freed room in the same write (resource release).
func (st *Store) ResourceRelease(ctx context.Context, name, member string) ([]sprint.ResourceEvent, error) {
	var evs []sprint.ResourceEvent
	err := st.resourceLocked(ctx, "resource release", func(t *sprint.ResourceTable) (bool, error) {
		var err error
		evs, err = t.ResourceRelease(name, member, st.now())
		return err == nil, err
	})
	if err != nil {
		return nil, err
	}
	return evs, st.tellResources(ctx, "resource release", evs, nil)
}

// Resources is the resources table as resource list shows it, by name: it reads and
// writes nothing else.
func (st *Store) Resources(ctx context.Context) ([]ResourceRow, error) {
	kv, err := st.resourcesKV()
	if err != nil {
		return nil, err
	}
	t, err := resourceTable(ctx, kv)
	if err != nil {
		return nil, err
	}
	now := st.now()
	out := make([]ResourceRow, 0, len(t.Rows))
	for _, n := range t.Names() {
		row := ResourceRow{Resource: *t.Rows[n]}
		for _, l := range row.Holders {
			if !now.Before(l.Until) {
				row.Expired = append(row.Expired, l.Member)
			}
		}
		out = append(out, row)
	}
	return out, nil
}

// ResourceTick is the tick's resources part (tla/Resources.tla Expire, MemberDown and
// Grant): every lease past its expiry and every lease and place in line of a member down
// or held released, the room granted to the lines in the same write, and the
// coordinator's one judgment of each line waiting with no room past
// sprint.ResourceStarveAfter. A store with no resources record reads that one key and
// nothing else.
func (st *Store) ResourceTick(ctx context.Context) (Result, error) {
	res := Result{Verb: "tick resources"}
	kv, err := st.rootKV()
	if err != nil {
		return res, nil // a store that keeps no records keeps no resources
	}
	if raw, ok, err := kv.GetKey(ctx, keyResources); err != nil || !ok || raw == "" {
		return res, err
	}
	statuses, err := st.ResourceStatuses(ctx)
	if err != nil {
		return res, err
	}
	var evs []sprint.ResourceEvent
	var starved []sprint.Resource
	err = st.resourceLocked(ctx, "tick resources", func(t *sprint.ResourceTable) (bool, error) {
		now := st.now()
		evs = t.ResourceReap(now, goneOf(statuses))
		for _, r := range t.ResourceStarved(now, sprint.ResourceStarveAfter) {
			starved = append(starved, *r)
		}
		return len(evs) > 0 || len(starved) > 0, nil
	})
	if err != nil {
		return res, err
	}
	if len(evs) == 0 && len(starved) == 0 {
		return res, nil
	}
	return st.tellResourcesResult(ctx, "tick resources", evs, starved)
}

// mayHold refuses a claimant that is not a friend, a fleet member or the coordinator,
// and one whose status takes its leases.
func (st *Store) mayHold(ctx context.Context, member string, statuses map[string]string) error {
	if !sprint.ValidID(member) {
		return fmt.Errorf("--as %q: a member's name wants letters, digits, _ and -", member)
	}
	if s, ok := statuses[member]; ok {
		if why := sprint.ResourceGone(s); why != "" {
			return fmt.Errorf("%s is %s and holds nothing; nothing was changed", member, why)
		}
		return nil
	}
	if coord, err := st.B.Coordinator(ctx); err == nil && coord != "" && coord == member {
		return nil
	}
	return fmt.Errorf("%s is not a friend, a fleet member or the coordinator, so it cannot hold a resource (%v); nothing was changed", member, slices.Sorted(maps.Keys(statuses)))
}

// tellResources writes the notes of what a resource change did (tellResourcesResult).
func (st *Store) tellResources(ctx context.Context, verb string, evs []sprint.ResourceEvent, starved []sprint.Resource) error {
	_, err := st.tellResourcesResult(ctx, verb, evs, starved)
	return err
}

// tellResourcesResult writes, in one step, a happened note to each member granted a
// lease and to each member whose lease or place in line the tick released, and a
// judgment of each line starved. A store with no sprint writes no note: the record
// holds what happened.
func (st *Store) tellResourcesResult(ctx context.Context, verb string, evs []sprint.ResourceEvent, starved []sprint.Resource) (Result, error) {
	var notes []sprint.Note
	for _, e := range evs {
		switch {
		case e.Kind == sprint.ResourceGranted:
			notes = append(notes, sprint.Note{Kind: sprint.Happened, Type: sprint.NResourceGranted, Who: sprint.MachineActor, To: e.Member, Count: 1,
				What: fmt.Sprintf("%s holds %s until %s", e.Member, e.Resource, stampOf(e.Until)),
				Hint: fmt.Sprintf("use it; renew before %s (nova-sprint resource renew %s --as %s --for <duration>); release it when done (nova-sprint resource release %s --as %s)", stampOf(e.Until), e.Resource, e.Member, e.Resource, e.Member)})
		case e.Why != "released":
			notes = append(notes, sprint.Note{Kind: sprint.Happened, Type: sprint.NResourceReleased, Who: sprint.MachineActor, To: e.Member, Count: 1,
				What: fmt.Sprintf("%s's %s of %s released: %s", e.Member, map[string]string{sprint.ResourceReleased: "lease", sprint.ResourceLeft: "place in line"}[e.Kind], e.Resource, e.Why),
				Hint: fmt.Sprintf("claim it again when you need it: nova-sprint resource claim %s --as %s --for <duration>", e.Resource, e.Member)})
		}
	}
	for _, r := range starved {
		var holders, line []string
		for _, l := range r.Holders {
			holders = append(holders, l.Member+" until "+stampOf(l.Until))
		}
		for _, w := range r.Line {
			line = append(line, w.Member)
		}
		notes = append(notes, sprint.Note{Kind: sprint.Judgment, Type: sprint.NResourceStarved, Who: sprint.MachineActor, Count: 1,
			Primaries: []string{sprint.ResourceSubject(r.Name)}, Decisions: sprint.ResourceStarvedDecisions,
			What: fmt.Sprintf("%s (%s, capacity %d) has had %d waiting with no room since %s: held by %v; waiting %v", r.Name, r.Kind, r.Capacity, len(r.Line), stampOf(r.Full), holders, line)})
	}
	if len(notes) == 0 {
		return Result{Verb: verb}, nil
	}
	res, err := st.Run(ctx, Step{Verb: verb, Actor: sprint.MachineActor, Plan: func(s *sprint.Snapshot) sprint.Plan {
		for i := range notes {
			notes[i].At = s.Now
		}
		return sprint.Plan{Notes: notes}
	}})
	if refusalCode(err) == "NOTABLE" {
		return Result{Verb: verb}, nil
	}
	if err != nil {
		return res, fmt.Errorf("%s: the resources record is written and its notes were not (%w)", verb, err)
	}
	return res, nil
}
