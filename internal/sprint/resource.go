package sprint

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// Resources: the coordinator's table of shared things (docs/SPEC-SPRINT.md section 11,
// Resources; card coordinator-managed-resources.w1). The owner, 2026-10-05 9:25 AM ET,
// after a friend held eight cards for half an hour behind a bench another friend had
// claimed by a bus message and never released (she went down out of credit): "Dining
// philosophers." and "If we have any sort of resources being managed in future, they
// should be managed by the coordinator here, as formal verbs." A bench machine or
// directory, a branch, a port or a provider account is a row with a capacity; a member
// holds it only by a lease the verbs grant, every lease has an expiry, and no member
// holds a resource by agreement with another.
//
// The model is tla/Resources.tla: Claim, Renew, Release, Expire (Reap, a lease past
// its expiry), MemberDown (Reap, a holder down, held or out of credit) and Grant
// (fill, the line's head granted in the same write as the room freed); its invariants
// CapacityHeld, DownHoldsNothing, NoWaitWithRoom and LineClean, and WaiterGranted under
// fairness, are what the functions below keep. Each function is one read-modify-write
// of the record, which the store makes under the sprint's fence
// (store/resource.go).

// The kinds of resource.
const (
	ResourceBench   = "bench"
	ResourceBranch  = "branch"
	ResourcePort    = "port"
	ResourceAccount = "account"
)

// ResourceKinds is every kind, in the order help names them.
var ResourceKinds = []string{ResourceBench, ResourceBranch, ResourcePort, ResourceAccount}

const (
	// ResourceMaxLease is the longest one claim or renew may hold for: a holder that
	// needs longer renews, so a holder that stops renewing frees the room within it.
	ResourceMaxLease = 24 * time.Hour
	// ResourceStarveAfter is how long a resource's line may wait with no room before
	// the coordinator gets its one judgment of it (NResourceStarved).
	ResourceStarveAfter = 30 * time.Minute
)

// The resource notes: a grant and a release the tick made, addressed to the member,
// and the coordinator's one judgment of a line waiting with no room.
const (
	NResourceGranted  = "resource granted"
	NResourceReleased = "resource released"
	NResourceStarved  = "a resource has waiters and no room"
)

// ResourceStarvedDecisions is the resource judgment's decisions: the coordinator
// acknowledges it (and acts on the holders: hold one, or raise the capacity). It is not a
// condition the tick keeps: a wait is judged once, and the tick closes nothing.
var ResourceStarvedDecisions = []string{"ack"}

// ResourceSubject is the judgment's subject for a resource: one open judgment a
// resource.
func ResourceSubject(name string) string { return "resource:" + name }

// Lease is one member's hold of a resource, from Since until Until.
type Lease struct {
	Member string    `json:"member"`
	Since  time.Time `json:"since"`
	Until  time.Time `json:"until"`
}

// ResourceWait is one member in a resource's line: since when, and how long the lease
// it asked for is (granted from the moment it is granted).
type ResourceWait struct {
	Member string        `json:"member"`
	Since  time.Time     `json:"since"`
	For    time.Duration `json:"for"`
}

// Resource is one row of the resources table.
type Resource struct {
	Name     string         `json:"name"`
	Kind     string         `json:"kind"`
	Capacity int            `json:"capacity"`
	Holders  []Lease        `json:"holders,omitempty"`
	Line     []ResourceWait `json:"line,omitempty"`
	// Full is since when the line has waited with no room (zero while no one waits
	// or there is room), and Judged says this wait's judgment was raised: one
	// judgment a wait, raised again only after the line empties or gets room.
	Full   time.Time `json:"full,omitzero"`
	Judged bool      `json:"judged,omitempty"`
}

// Room says the resource has room for another holder.
func (r *Resource) Room() bool { return len(r.Holders) < r.Capacity }

// Holds is the member's lease of the resource, if it holds one.
func (r *Resource) Holds(member string) (Lease, bool) {
	for _, l := range r.Holders {
		if l.Member == member {
			return l, true
		}
	}
	return Lease{}, false
}

// Place is the member's place in the line, 1 first; 0 when it does not wait.
func (r *Resource) Place(member string) int {
	for i, w := range r.Line {
		if w.Member == member {
			return i + 1
		}
	}
	return 0
}

// ResourceEvent is what a resource function did to one member: granted a lease,
// released one (Why: released, expired, down, held, ...), or put it in line.
type ResourceEvent struct {
	Kind     string    `json:"kind"` // granted, released, waiting, left
	Resource string    `json:"resource"`
	Member   string    `json:"member"`
	Until    time.Time `json:"until,omitzero"`
	Place    int       `json:"place,omitempty"`
	Why      string    `json:"why,omitempty"`
}

// The kinds of ResourceEvent.
const (
	ResourceGranted  = "granted"
	ResourceReleased = "released"
	ResourceWaiting  = "waiting"
	ResourceLeft     = "left" // left the line without a grant
)

// ResourceTable is the resources table: every row by name.
type ResourceTable struct {
	Rows map[string]*Resource `json:"rows"`
}

// Names is every resource's name, sorted.
func (t *ResourceTable) Names() []string {
	out := make([]string, 0, len(t.Rows))
	for n := range t.Rows {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

func (t *ResourceTable) row(name string) (*Resource, error) {
	r, ok := t.Rows[name]
	if !ok {
		return nil, fmt.Errorf("no resource %s; run: nova-sprint resource list (or the coordinator's resource add %s --kind <kind> --capacity <n>)", name, name)
	}
	return r, nil
}

// ResourceAdd adds a resource, or sets the capacity of one of the same kind (a larger
// capacity grants its line at once; a smaller one takes no lease back: the holders
// over it keep theirs until they release or expire, and no one is granted meanwhile).
func (t *ResourceTable) ResourceAdd(name, kind string, capacity int, now time.Time) ([]ResourceEvent, error) {
	switch {
	case !ValidID(name):
		return nil, fmt.Errorf("a resource's name wants letters, digits, _ and -: %q", name)
	case !slices.Contains(ResourceKinds, kind):
		return nil, fmt.Errorf("--kind %q is not a kind; use one of %s", kind, strings.Join(ResourceKinds, ", "))
	case capacity < 1:
		return nil, fmt.Errorf("--capacity %d: a resource holds at least one", capacity)
	}
	if t.Rows == nil {
		t.Rows = map[string]*Resource{}
	}
	r, ok := t.Rows[name]
	if !ok {
		t.Rows[name] = &Resource{Name: name, Kind: kind, Capacity: capacity}
		return nil, nil
	}
	if r.Kind != kind {
		return nil, fmt.Errorf("resource %s is a %s, not a %s; a name keeps its kind", name, r.Kind, kind)
	}
	r.Capacity = capacity
	return r.fill(now), nil
}

// ResourceClaim is a member's claim of a resource for d: a lease at once while the
// resource has room and no one waits (first in, first out), else the member's place in
// the line, granted by the release or the tick that frees the room; it never polls. A
// member already in line is told its place again and nothing changes.
func (t *ResourceTable) ResourceClaim(name, member string, d time.Duration, now time.Time) (ResourceEvent, error) {
	r, err := t.row(name)
	if err != nil {
		return ResourceEvent{}, err
	}
	if err := leaseFor(d); err != nil {
		return ResourceEvent{}, err
	}
	if l, ok := r.Holds(member); ok {
		return ResourceEvent{}, fmt.Errorf("%s already holds %s until %s; run: nova-sprint resource renew %s --as %s --for <duration>", member, name, stamp(l.Until), name, member)
	}
	if p := r.Place(member); p > 0 {
		return ResourceEvent{Kind: ResourceWaiting, Resource: name, Member: member, Place: p}, nil
	}
	if r.Room() && len(r.Line) == 0 {
		l := Lease{Member: member, Since: now, Until: now.Add(d)}
		r.Holders = append(r.Holders, l)
		return ResourceEvent{Kind: ResourceGranted, Resource: name, Member: member, Until: l.Until}, nil
	}
	r.Line = append(r.Line, ResourceWait{Member: member, Since: now, For: d})
	r.settle(now)
	return ResourceEvent{Kind: ResourceWaiting, Resource: name, Member: member, Place: len(r.Line)}, nil
}

// ResourceRenew moves the member's live lease to now+d. A lease past its expiry is not
// renewed, even before the tick has released it: its room is owed to the line.
func (t *ResourceTable) ResourceRenew(name, member string, d time.Duration, now time.Time) (ResourceEvent, error) {
	r, err := t.row(name)
	if err != nil {
		return ResourceEvent{}, err
	}
	if err := leaseFor(d); err != nil {
		return ResourceEvent{}, err
	}
	for i, l := range r.Holders {
		if l.Member != member {
			continue
		}
		if !now.Before(l.Until) {
			return ResourceEvent{}, fmt.Errorf("%s's lease of %s expired at %s and is not renewed; run: nova-sprint resource claim %s --as %s --for <duration>", member, name, stamp(l.Until), name, member)
		}
		r.Holders[i].Until = now.Add(d)
		return ResourceEvent{Kind: ResourceGranted, Resource: name, Member: member, Until: r.Holders[i].Until}, nil
	}
	return ResourceEvent{}, fmt.Errorf("%s holds no lease of %s; run: nova-sprint resource claim %s --as %s --for <duration>", member, name, name, member)
}

// ResourceRelease gives the member's lease back, or takes it out of the line, and
// grants the room freed to the line's head in the same write. It is the grants it made
// after the release itself.
func (t *ResourceTable) ResourceRelease(name, member string, now time.Time) ([]ResourceEvent, error) {
	r, err := t.row(name)
	if err != nil {
		return nil, err
	}
	if _, ok := r.Holds(member); ok {
		r.Holders = slices.DeleteFunc(r.Holders, func(l Lease) bool { return l.Member == member })
		out := []ResourceEvent{{Kind: ResourceReleased, Resource: name, Member: member, Why: "released"}}
		return append(out, r.fill(now)...), nil
	}
	if r.Place(member) > 0 {
		r.Line = slices.DeleteFunc(r.Line, func(w ResourceWait) bool { return w.Member == member })
		r.settle(now)
		return []ResourceEvent{{Kind: ResourceLeft, Resource: name, Member: member, Why: "released"}}, nil
	}
	return nil, fmt.Errorf("%s neither holds %s nor waits for it; nothing was changed", member, name)
}

// ResourceReap is the tick's part (tla/Resources.tla Expire and MemberDown, then
// Grant): every lease at or past its expiry released, every lease and place in line of
// a member gone (gone names it with why: down, held, out of credit) released, and each
// resource's freed room granted to its line in the same write. It is what it did.
func (t *ResourceTable) ResourceReap(now time.Time, gone map[string]string) []ResourceEvent {
	var out []ResourceEvent
	for _, name := range t.Names() {
		r := t.Rows[name]
		r.Holders = slices.DeleteFunc(r.Holders, func(l Lease) bool {
			why, down := gone[l.Member]
			switch {
			case down:
				out = append(out, ResourceEvent{Kind: ResourceReleased, Resource: name, Member: l.Member, Why: why})
			case !now.Before(l.Until):
				out = append(out, ResourceEvent{Kind: ResourceReleased, Resource: name, Member: l.Member, Why: "expired at " + stamp(l.Until)})
			default:
				return false
			}
			return true
		})
		r.Line = slices.DeleteFunc(r.Line, func(w ResourceWait) bool {
			why, down := gone[w.Member]
			if down {
				out = append(out, ResourceEvent{Kind: ResourceLeft, Resource: name, Member: w.Member, Why: why})
			}
			return down
		})
		out = append(out, r.fill(now)...)
	}
	return out
}

// ResourceStarved is the resources whose line has waited with no room for at least
// bound and whose wait was not judged yet; each is marked judged, so the coordinator
// gets one judgment a wait.
func (t *ResourceTable) ResourceStarved(now time.Time, bound time.Duration) []*Resource {
	var out []*Resource
	for _, name := range t.Names() {
		r := t.Rows[name]
		if !r.Judged && !r.Full.IsZero() && now.Sub(r.Full) >= bound {
			r.Judged = true
			out = append(out, r)
		}
	}
	return out
}

// fill grants the line's head while there is room, in order, and settles the wait.
func (r *Resource) fill(now time.Time) []ResourceEvent {
	var out []ResourceEvent
	for r.Room() && len(r.Line) > 0 {
		w := r.Line[0]
		r.Line = r.Line[1:]
		l := Lease{Member: w.Member, Since: now, Until: now.Add(w.For)}
		r.Holders = append(r.Holders, l)
		out = append(out, ResourceEvent{Kind: ResourceGranted, Resource: r.Name, Member: w.Member, Until: l.Until})
	}
	if len(r.Line) == 0 {
		r.Line = nil
	}
	r.settle(now)
	return out
}

// settle keeps Full and Judged: a wait begins when someone waits with no room, and
// ends when the line empties or there is room.
func (r *Resource) settle(now time.Time) {
	if len(r.Line) == 0 || r.Room() {
		r.Full, r.Judged = time.Time{}, false
		return
	}
	if r.Full.IsZero() {
		r.Full = now
	}
}

// leaseFor refuses a lease of no length or longer than ResourceMaxLease.
func leaseFor(d time.Duration) error {
	if d <= 0 || d > ResourceMaxLease {
		return fmt.Errorf("--for %s: a lease is longer than nothing and at most %s; renew to hold longer", d, ResourceMaxLease)
	}
	return nil
}

// ResourceGone is why a member's status takes its leases: down (no beat, or a friend
// out of credit or past her allowance) or held; "" while it may hold.
func ResourceGone(status string) string {
	switch status {
	case Down, Held:
		return status
	}
	return ""
}
