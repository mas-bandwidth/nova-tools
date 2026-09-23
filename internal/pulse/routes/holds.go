// Package routes holds the in-process route hold set: the per-route TTL holds
// that stop a launch before it dispatches a card, and a tiny bag of helpers
// for the rest of the routing state the launch path will want without
// reaching for MUSE-HELD or providers-*.txt.
//
// A route is a string: the identifier the launch path uses to pick the
// worker it dispatches a card with. The identifier is owned by the routes
// table (swarm.ParseRoutes in internal/swarm/route.go) and is whatever
// shape that table picks, so this package never parses one. The only
// normalisation is trimming surrounding whitespace, applied identically in
// Set, IsHeld and Lift, so a key stored by Set is the key IsHeld and Lift
// look up. It answers the only question it ever has to answer: is this
// route currently held.
//
// Stage: this is the primitive stage only. No production caller consults the
// hold set yet; wiring it into internal/pulse/launch.go (Launch/LaunchInput)
// so held routes are not dispatched is a follow-up card outside this
// package's PATHS.
//
// The hold set is the in-process equivalent of the STOP sentinel for routes:
// one Set places a hold and one IsHeld reads it back, both in O(1) and
// without a goroutine. A Set takes a wall-clock instant and a duration; an
// IsHeld takes a wall-clock instant and answers true when the route is
// held at that instant with the hold's expiry still in the future. The
// hold lifts itself at expiry: any IsHeld call at or after the expiry
// instant returns false and removes the hold. There is no scheduling, no
// timer, no goroutine, and no filesystem sentinel: the package's
// correctness is a single map and a single time comparison.
//
// This package is also the one reason the launcher path will not, in this
// change or the next, read MUSE-HELD or providers-*.txt. The route hold set
// is the state the launcher reads; the named files were a half-built
// substitute, and the hold set replaces them. A second substitute would
// reintroduce the same problem the hold set was built to solve.
package routes

import (
	"sort"
	"strings"
	"sync"
	"time"
)

// Route is the identifier the launch path uses for one dispatch lane
// (provider + tier + key, the same triple the swarm's --max-inflight
// counts on). It is a value, not a pointer, so a Route travels in slices
// and tables by value and the wall-clock cost of comparing two Routes is
// the cost of comparing two strings.
type Route struct {
	// Name is the route identifier the launcher passes to HoldSet.IsHeld,
	// exactly as it appears in the routes table. Empty is a typo callers
	// catch themselves.
	Name string
}

// String returns the route's Name. It exists so Route prints cleanly in
// tests and logs without a fmt.Sprintf per occurrence.
func (r Route) String() string { return r.Name }

// Hold is the TTL record the hold set keeps per route: the route itself
// and the wall-clock instant at which the hold lifts. A Hold is a value,
// not a pointer, so the expiry instant is fixed when the hold is inserted
// and is shared by every reader in the same instant.
type Hold struct {
	Route     string
	ExpiresAt time.Time
}

// HoldSet is a concurrent set of per-route TTL holds. The empty value is
// ready to use: Set, IsHeld, Lift and Snapshot all work on the zero
// HoldSet. Two goroutines may call Set/IsHeld/Lift/Snapshot concurrently
// without racing; the mutex covers the map only.
//
// A HoldSet holds up to a few dozen entries: the routes a cluster runs
// lanes over, not a cardinality that needs a tree. The cost of an IsHeld
// is one map lookup, one time comparison and, on expiry, one map delete.
// The bounded cost is why this is a map and not a sorted set or a trie.
type HoldSet struct {
	mu    sync.Mutex
	holds map[string]time.Time
	now   func() time.Time
}

// NewHoldSet returns a HoldSet that reads the wall clock through now. A
// nil now is time.Now, the production clock; a test passes a clock it
// controls so a TTL test costs no wall time. The returned HoldSet has no
// holds: nothing is held until Set returns.
func NewHoldSet(now func() time.Time) *HoldSet {
	return &HoldSet{now: now}
}

// Set places a hold on the route for ttl starting at now. The expiry
// instant is now.Add(ttl), recorded once; a second Set on the same route
// REFRESHES the expiry, not stacks: the latest Set wins. The operation is
// in-process, so the hold is visible to a same-process IsHeld on the
// next instruction -- well under the 1 s the dispatcher budget allows.
//
// A ttl of zero is a Lift: Set(route, 0, now) is exactly equal in effect
// to Lift(route). A ttl of less than zero is a typo and is dropped on the
// floor: a negative hold is not a thing this package keeps.
//
// A blank route is a typo: Set is a no-op when the route's name trims to
// empty, the same way an IsHeld on a blank route answers false.
func (h *HoldSet) Set(route string, ttl time.Duration, now time.Time) {
	route = strings.TrimSpace(route)
	if route == "" {
		return
	}
	if ttl < 0 {
		return
	}
	if ttl == 0 {
		h.Lift(route)
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.holds == nil {
		h.holds = make(map[string]time.Time)
	}
	h.holds[route] = now.Add(ttl)
}

// IsHeld reports whether route is held at now. An expired hold -- expiry
// at or before now -- returns false AND is removed from the set, so the
// next IsHeld sees no remnant. A hold whose expiry is strictly after now
// is held, regardless of how many Set calls preceded it.
//
// The check uses the supplied now, never the wall clock: a caller drives
// the clock explicitly so a TTL test is reproducible and a dispatcher
// running at the wall clock's resolution gets exactly the answer the wall
// clock gives.
func (h *HoldSet) IsHeld(route string, now time.Time) bool {
	route = strings.TrimSpace(route)
	if route == "" {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	expiry, ok := h.holds[route]
	if !ok {
		return false
	}
	if !expiry.After(now) {
		delete(h.holds, route)
		return false
	}
	return true
}

// Lift removes the hold on route, if any. Lift of a route that is not
// held is a no-op, the same way Set of an already-held route is a refresh.
// The return value reports whether a hold was lifted, so a caller can
// count its own lifts without re-reading the set.
func (h *HoldSet) Lift(route string) bool {
	route = strings.TrimSpace(route)
	if route == "" {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.holds[route]; !ok {
		return false
	}
	delete(h.holds, route)
	return true
}

// Snapshot returns one Hold per currently-held route, sorted by route
// name. The slice is a fresh copy on every call: callers may sort, slice
// or send it through any channel without racing with a concurrent Set.
// Expired holds (expiry at or before now) are pruned: omitted from the
// returned slice AND deleted from the set, the same way IsHeld prunes; the
// returned slice is the set as the supplied now sees it.
func (h *HoldSet) Snapshot(now time.Time) []Hold {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]Hold, 0, len(h.holds))
	for route, expiry := range h.holds {
		if !expiry.After(now) {
			delete(h.holds, route)
			continue
		}
		out = append(out, Hold{Route: route, ExpiresAt: expiry})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Route < out[j].Route })
	if len(out) == 0 {
		return nil
	}
	return out
}

// Clock returns the wall clock the HoldSet was built with, or time.Now
// when no clock was supplied. The clock is the answer the production
// HoldSet gives when callers do not inject one.
func (h *HoldSet) Clock() func() time.Time {
	if h.now == nil {
		return time.Now
	}
	return h.now
}
