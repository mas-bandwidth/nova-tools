package sprint

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// The resources (docs/SPEC-SPRINT.md section 19; the model is tla/Resources.tla):
// a shared resource (a bench machine or directory, a branch, a port, a provider
// account) is a row of the resources table, which the coordinator's verbs alone
// manage. No friend holds a resource by agreement with another friend: on
// 2026-10-05 a friend held the shared bench by a bus message, went down out of
// credit, and the bench stayed held for the morning while eight cards waited (the
// owner: "Dining philosophers."; "If we have any sort of resources being managed
// in future, they should be managed by the coordinator here, as formal verbs").
//
// Every hold is a lease with an expiry (resource claim --for <duration>), renewed
// by resource renew and given back by resource release. A claim is granted while
// the resource has room and nobody waits ahead; otherwise the claimant joins the
// back of the line and keeps its place without asking again (it never polls):
// the grant is the coordinator's, made at a release, an expiry or a member going
// down, and the tick makes each of them. A lease past its expiry, or whose
// holder is down (held, out of credit, no session evidence), is released by the
// tick at once and the head of the line is granted; a waiter down leaves the
// line. A resource with waiters and no room for longer than ResourceStarveBound
// raises one judgment to the coordinator (NResourceStarved), once while it stands.
//
// The invariants the model holds, and resource_test.go checks on the code: never
// more holders than the capacity (CapacityKept), a member down holds nothing
// (DownHoldsNothing), nobody waits while a place is free (NoRoomWasted), a grant
// never overtakes an earlier waiter (NoOvertaking), and every waiter is served
// while holders keep releasing or expiring (EveryWaiterIsServed).

const (
	// ResourceStarveBound is how long a resource may have waiters and no room
	// before the tick raises the judgment: longer than one gate run on a bench,
	// shorter than the morning the shared bench was lost to.
	ResourceStarveBound = 30 * time.Minute
	// ResourceLeaseMax bounds a lease: a hold past it is a mistake, not a job.
	ResourceLeaseMax = 24 * time.Hour
	// ResourceWatchEvery is how often resource claim --wait reads the table for
	// its grant: a read, never a claim again (the place is kept without asking).
	ResourceWatchEvery = 5 * time.Second
	// NResourceStarved is the tick's judgment that a resource has had waiters and
	// no room for ResourceStarveBound: the holders are named, and the coordinator
	// decides (release a holder, add capacity, or acknowledge the wait).
	NResourceStarved = "a resource has waiters and no room"
	// NResourceReleased is the tick's note that it released a lease: expired, or
	// its holder went down; the next waiter granted is named.
	NResourceReleased = "a resource lease was released by the tick"
)

// ResourceDecisions are the decisions open on NResourceStarved.
var ResourceDecisions = []string{"resource release <name> --as <holder>", "resource add <name> --capacity <n>", "ack"}

// ResourceKinds are the kinds a resource is one of.
var ResourceKinds = []string{"bench", "branch", "port", "account"}

// ResourceSubject is the open subject of a resource's judgment: no card or stream
// id has a colon, so it is never read as one.
func ResourceSubject(name string) string { return "resource:" + name }

// ResourceHold is one lease: who holds it, since when, and until when.
type ResourceHold struct {
	Who   string    `json:"who"`
	Since time.Time `json:"since"`
	Until time.Time `json:"until"`
}

// ResourceWait is one place in the line: who waits, since when, and the lease
// it asked for, granted from the moment of the grant.
type ResourceWait struct {
	Who   string        `json:"who"`
	Since time.Time     `json:"since"`
	For   time.Duration `json:"for"`
}

// Resource is one row of the resources table.
type Resource struct {
	Kind     string         `json:"kind"`
	Capacity int            `json:"capacity"`
	Holders  []ResourceHold `json:"holders,omitempty"`
	Waiters  []ResourceWait `json:"waiters,omitempty"`
	// Starved is since when the line has been non-empty with no room (a waiter
	// joined and was not granted); zero while the line is empty. The judgment is
	// raised once it is ResourceStarveBound old.
	Starved time.Time `json:"starved,omitzero"`
	// Judged is when the starved judgment was raised and stands; zero while none
	// is open: the tick closes it once the line has room.
	Judged time.Time `json:"judged,omitzero"`
}

// Resources is the table by name: the record the store keeps.
type Resources map[string]Resource

// ResourceAnswer is a claim's, a renew's or a release's answer.
type ResourceAnswer struct {
	Name     string    `json:"name"`
	Granted  bool      `json:"granted"`         // the asker holds the lease
	Place    int       `json:"place,omitempty"` // in the line (1 is next) when it does not
	Until    time.Time `json:"until,omitzero"`  // the lease's expiry when it holds
	Held     int       `json:"held"`
	Capacity int       `json:"capacity"`
	Gave     bool      `json:"gave,omitempty"` // release: the asker held or waited, and does not now
	// Released is who the step released (expired leases); GrantedNow is who it
	// granted from the line, in order.
	Released   []string `json:"released,omitempty"`
	GrantedNow []string `json:"granted_now,omitempty"`
}

// ResourceChange is what a tick's pass over the table did to one resource.
type ResourceChange struct {
	Name     string
	Expired  []string // holders released at their expiry
	Down     []string // holders released because they are down
	Left     []string // waiters that left the line because they are down
	Granted  []string // waiters granted, in order
	Starving bool     // the line has had no room for ResourceStarveBound
}

// ResourceWhy is why a name, a kind, a capacity or a member is refused; "" when
// none is.
func ResourceWhy(name, kind string, capacity int, who string) string {
	var why []string
	if !ValidID(name) {
		why = append(why, "a resource name wants letters, digits, _ and -: "+name)
	}
	if kind != "" && !slices.Contains(ResourceKinds, kind) {
		why = append(why, "--kind is one of "+strings.Join(ResourceKinds, ", ")+", found "+kind)
	}
	if capacity < 0 {
		why = append(why, fmt.Sprintf("--capacity wants a count from 1, found %d", capacity))
	}
	if who != "" && !ValidID(who) {
		why = append(why, "--as wants the member claiming (letters, digits, _ and -): "+who)
	}
	return strings.Join(why, "; ")
}

// LeaseWhy is why a lease length is refused: "" when it is not.
func LeaseWhy(d time.Duration) string {
	switch {
	case d <= 0:
		return "--for wants the lease's length, a duration above 0 (2h)"
	case d > ResourceLeaseMax:
		return "--for wants a lease of at most " + ResourceLeaseMax.String() + ", found " + d.String()
	}
	return ""
}

// Add is the resources with name added at kind and capacity: refused when it is
// there (resource add <name> --capacity <n> on a row sets its capacity instead).
func (rs Resources) Add(name, kind string, capacity int) (Resources, error) {
	if why := ResourceWhy(name, kind, capacity, ""); why != "" {
		return rs, errors.New(why)
	}
	if kind == "" {
		return rs, errors.New("--kind wants one of " + strings.Join(ResourceKinds, ", "))
	}
	if capacity < 1 {
		return rs, errors.New("--capacity wants a count from 1")
	}
	if _, ok := rs[name]; ok {
		return rs, fmt.Errorf("resource %s is on the table; resource add %s --capacity <n> sets its capacity", name, name)
	}
	return rs.with(name, Resource{Kind: kind, Capacity: capacity}), nil
}

// SetCapacity is the resources with name's capacity set: a wider one grants the
// line into the room; a narrower one takes no lease back and grants none until
// the holders are under it.
func (rs Resources) SetCapacity(name string, capacity int, now time.Time) (Resources, ResourceChange, error) {
	r, ok := rs[name]
	if !ok {
		return rs, ResourceChange{}, noResource(name)
	}
	if capacity < 1 {
		return rs, ResourceChange{}, errors.New("--capacity wants a count from 1")
	}
	r.Capacity = capacity
	var ch ResourceChange
	r, ch.Granted = r.grant(now)
	r = r.clock(now)
	ch.Name = name
	return rs.with(name, r), ch, nil
}

// Claim is who's claim of name for a lease of d at now: a holder renews (its
// lease runs d from now); a waiter keeps its place (its lease length is d); a
// newcomer joins the back of the line, and the head is granted while there is
// room, so a claim is granted only at the head.
func (rs Resources) Claim(name, who string, d time.Duration, now time.Time) (Resources, ResourceAnswer, error) {
	if why := ResourceWhy(name, "", 0, who); why != "" {
		return rs, ResourceAnswer{}, errors.New(why)
	}
	if why := LeaseWhy(d); why != "" {
		return rs, ResourceAnswer{}, errors.New(why)
	}
	r, ok := rs[name]
	if !ok {
		return rs, ResourceAnswer{}, noResource(name)
	}
	if i := r.hold(who); i >= 0 {
		r.Holders[i].Until = now.Add(d)
	} else if i := r.wait(who); i >= 0 {
		r.Waiters[i].For = d
	} else {
		r.Waiters = append(slices.Clone(r.Waiters), ResourceWait{Who: who, Since: now, For: d})
	}
	var ans ResourceAnswer
	r, ans.GrantedNow = r.grant(now)
	r = r.clock(now)
	ans.fill(name, who, r)
	return rs.with(name, r), ans, nil
}

// Renew is who's renewal of its lease of name: the lease runs d from now. A
// member that holds nothing is refused: a renewal never joins the line.
func (rs Resources) Renew(name, who string, d time.Duration, now time.Time) (Resources, ResourceAnswer, error) {
	if why := ResourceWhy(name, "", 0, who); why != "" {
		return rs, ResourceAnswer{}, errors.New(why)
	}
	if why := LeaseWhy(d); why != "" {
		return rs, ResourceAnswer{}, errors.New(why)
	}
	r, ok := rs[name]
	if !ok {
		return rs, ResourceAnswer{}, noResource(name)
	}
	i := r.hold(who)
	if i < 0 {
		if p := r.wait(who); p >= 0 {
			return rs, ResourceAnswer{}, fmt.Errorf("%s holds no lease of %s: it is %s in the line; a renewal never grants", who, name, ordinal(p+1))
		}
		return rs, ResourceAnswer{}, fmt.Errorf("%s holds no lease of %s; claim one: resource claim %s --as %s --for %s", who, name, name, who, d)
	}
	r.Holders = slices.Clone(r.Holders)
	r.Holders[i].Until = now.Add(d)
	var ans ResourceAnswer
	ans.fill(name, who, r)
	return rs.with(name, r), ans, nil
}

// Release is who's release of its lease of name, or of its place in the line,
// at now: the head is granted into the room made.
func (rs Resources) Release(name, who string, now time.Time) (Resources, ResourceAnswer, error) {
	if why := ResourceWhy(name, "", 0, who); why != "" {
		return rs, ResourceAnswer{}, errors.New(why)
	}
	r, ok := rs[name]
	if !ok {
		return rs, ResourceAnswer{}, noResource(name)
	}
	n := len(r.Holders) + len(r.Waiters)
	r.Holders = slices.DeleteFunc(slices.Clone(r.Holders), func(h ResourceHold) bool { return h.Who == who })
	r.Waiters = slices.DeleteFunc(slices.Clone(r.Waiters), func(w ResourceWait) bool { return w.Who == who })
	gave := len(r.Holders)+len(r.Waiters) < n
	var ans ResourceAnswer
	r, ans.GrantedNow = r.grant(now)
	r = r.clock(now)
	ans.fill(name, who, r)
	ans.Gave = gave
	return rs.with(name, r), ans, nil
}

// Remove takes name off the table: refused while it is held or waited for.
func (rs Resources) Remove(name string) (Resources, error) {
	r, ok := rs[name]
	if !ok {
		return rs, noResource(name)
	}
	if len(r.Holders)+len(r.Waiters) > 0 {
		return rs, fmt.Errorf("resource %s is held by %s and waited for by %s; release them first", name, orNone(r.holders()), orNone(r.waiters()))
	}
	out := make(Resources, len(rs))
	for k, v := range rs {
		if k != name {
			out[k] = v
		}
	}
	return out, nil
}

// Tick is the coordinator's pass over the table at now, with down saying whether
// a member is down (held, out of credit, no session evidence): a lease past its
// expiry or whose holder is down is released, a waiter down leaves the line, the
// line is served into the room, and a line with no room is timed against
// ResourceStarveBound. The changes name what it did, by resource, in name order;
// a resource it left as it was has no change.
func (rs Resources) Tick(now time.Time, down func(string) bool) (Resources, []ResourceChange) {
	if down == nil {
		down = func(string) bool { return false }
	}
	out := Resources{}
	var changes []ResourceChange
	for _, name := range rs.Names() {
		r := rs[name]
		ch := ResourceChange{Name: name}
		var holders []ResourceHold
		for _, h := range r.Holders {
			switch {
			case down(h.Who):
				ch.Down = append(ch.Down, h.Who)
			case !h.Until.After(now):
				ch.Expired = append(ch.Expired, h.Who)
			default:
				holders = append(holders, h)
			}
		}
		var waiters []ResourceWait
		for _, w := range r.Waiters {
			if down(w.Who) {
				ch.Left = append(ch.Left, w.Who)
				continue
			}
			waiters = append(waiters, w)
		}
		r.Holders, r.Waiters = holders, waiters
		r, ch.Granted = r.grant(now)
		was := r.Starved
		r = r.clock(now)
		ch.Starving = !r.Starved.IsZero() && now.Sub(r.Starved) >= ResourceStarveBound
		out[name] = r
		if len(ch.Expired)+len(ch.Down)+len(ch.Left)+len(ch.Granted) > 0 || ch.Starving || !was.Equal(r.Starved) {
			changes = append(changes, ch)
		}
	}
	return out, changes
}

// Names is every resource, in order.
func (rs Resources) Names() []string {
	names := make([]string, 0, len(rs))
	for n := range rs {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

func (rs Resources) with(name string, r Resource) Resources {
	out := make(Resources, len(rs)+1)
	for k, v := range rs {
		out[k] = v
	}
	out[name] = r
	return out
}

// grant moves the head of the line to the holders while they are fewer than
// the capacity, each a lease of its own length from now: the grant is the
// coordinator's, never a message between members (the model's Settle).
func (r Resource) grant(now time.Time) (Resource, []string) {
	var granted []string
	for len(r.Holders) < r.Capacity && len(r.Waiters) > 0 {
		w := r.Waiters[0]
		r.Waiters = slices.Clone(r.Waiters[1:])
		r.Holders = append(slices.Clone(r.Holders), ResourceHold{Who: w.Who, Since: now, Until: now.Add(w.For)})
		granted = append(granted, w.Who)
	}
	if len(r.Waiters) == 0 {
		r.Waiters = nil
	}
	return r, granted
}

// clock runs the starvation clock: since when the line has had no room, from
// the moment the line formed (a claim queued, a release that left waiters, a
// tick that found them), zero once the line is empty.
func (r Resource) clock(now time.Time) Resource {
	switch {
	case len(r.Waiters) == 0:
		r.Starved = time.Time{}
	case r.Starved.IsZero():
		r.Starved = now
	}
	return r
}

func (r Resource) hold(who string) int {
	return slices.IndexFunc(r.Holders, func(h ResourceHold) bool { return h.Who == who })
}

func (r Resource) wait(who string) int {
	return slices.IndexFunc(r.Waiters, func(w ResourceWait) bool { return w.Who == who })
}

func (r Resource) holders() []string {
	var out []string
	for _, h := range r.Holders {
		out = append(out, h.Who)
	}
	return out
}

func (r Resource) waiters() []string {
	var out []string
	for _, w := range r.Waiters {
		out = append(out, w.Who)
	}
	return out
}

func (a *ResourceAnswer) fill(name, who string, r Resource) {
	a.Name, a.Held, a.Capacity = name, len(r.Holders), r.Capacity
	if i := r.hold(who); i >= 0 {
		a.Granted, a.Until = true, r.Holders[i].Until
	} else if i := r.wait(who); i >= 0 {
		a.Place = i + 1
	}
}

func noResource(name string) error {
	return fmt.Errorf("no resource %s on the table; the coordinator adds one: resource add %s --kind bench|branch|port|account --capacity <n>", name, name)
}

func orNone(xs []string) string {
	if len(xs) == 0 {
		return "nobody"
	}
	return strings.Join(xs, ", ")
}

// ordinal is 1st, 2nd, 3rd, 4th ... for a place in a line.
func ordinal(n int) string {
	suffix := "th"
	switch {
	case n%100 >= 11 && n%100 <= 13:
	case n%10 == 1:
		suffix = "st"
	case n%10 == 2:
		suffix = "nd"
	case n%10 == 3:
		suffix = "rd"
	}
	return fmt.Sprintf("%d%s", n, suffix)
}

// ResourceRow is one row of the table as a reader sees it (resource list, --json):
// the holders with their expiries and the waiters in order.
type ResourceRow struct {
	Name     string         `json:"name"`
	Kind     string         `json:"kind"`
	Capacity int            `json:"capacity"`
	Holders  []ResourceHold `json:"holders"`
	Waiters  []ResourceWait `json:"waiters"`
	Starved  time.Time      `json:"starved,omitzero"`
}

// Rows is the table at now, by name: what resource list prints. Nothing is
// released here (a reader writes nothing): an expired lease shows with its
// expiry in the past until the tick releases it.
func (rs Resources) Rows() []ResourceRow {
	out := make([]ResourceRow, 0, len(rs))
	for _, name := range rs.Names() {
		r := rs[name]
		row := ResourceRow{Name: name, Kind: r.Kind, Capacity: r.Capacity, Holders: slices.Clone(r.Holders), Waiters: slices.Clone(r.Waiters), Starved: r.Starved}
		if row.Holders == nil {
			row.Holders = []ResourceHold{}
		}
		if row.Waiters == nil {
			row.Waiters = []ResourceWait{}
		}
		out = append(out, row)
	}
	return out
}

// Line is a row as resource list prints it: name, kind, held/capacity, the
// holders with their expiries, and the waiters in order.
func (r ResourceRow) Line(now time.Time) string {
	var hs []string
	for _, h := range r.Holders {
		left := h.Until.Sub(now).Round(time.Second)
		if left < 0 {
			hs = append(hs, h.Who+" (expired "+(-left).String()+" ago)")
			continue
		}
		hs = append(hs, h.Who+" (until "+h.Until.UTC().Format(time.RFC3339)+", "+left.String()+" left)")
	}
	var ws []string
	for i, w := range r.Waiters {
		ws = append(ws, fmt.Sprintf("%d. %s (for %s, since %s)", i+1, w.Who, w.For, w.Since.UTC().Format(time.RFC3339)))
	}
	line := fmt.Sprintf("RESOURCE %s kind=%s held=%d/%d holders: %s; waiting: %s", r.Name, r.Kind, len(r.Holders), r.Capacity, orNone(hs), orNone(ws))
	if !r.Starved.IsZero() {
		line += "; no room since " + r.Starved.UTC().Format(time.RFC3339)
	}
	return line
}
