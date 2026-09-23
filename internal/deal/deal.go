// Package deal is THE DEALER'S RULE, in one place, for every consumer of work.
//
// A consumer is a bench, a bench leg-set or a friend: something with a queue, a width and
// a set of capabilities. A task is a card, a read, a fix, a decision or a script: something
// with requirements. The dealer matches requirements to capabilities, scores the ready set,
// and tops each consumer's queue to its width the moment it drops below the low water mark.
// That is the whole rule, and a friend gets it unchanged from a bench, because a friend is
// a consumer with width four and a human harness (Glenn, 2026-09-22: "ultimately we want to
// treat friend work the same as swarm work").
//
// THERE IS NO FIXED SET OF QUEUES. `q:flash` and `q:pro` are not in this package and must
// never be: there is no evidence that a model tier is the meaningful split (nova-tools
// #2564), and this week alone the things that actually decided where a card could run were
// the toolchain leg, network locality (18 of 55 cards hung on the house uplink), a warm
// mirror of the repo and base (55 cold clones took three minutes), the wall budget (a five
// minute read must not share an idle bound with a two hour evaluation), the isolation level,
// how many other tasks a task unblocks, whether the task calls a model at all, and a cost
// ceiling. So every task carries REQUIREMENT fields and every consumer carries CAPABILITY
// fields, the match is a field-by-field comparison, and the stream a task lands on is
// `q:<consumer>` where the consumer is the match's OUTPUT. Adding an axis is adding a field
// to both sides; it is never a new stream, and renaming a stream is a table edit.
//
// The model tier is a FIELD on the task (Routes: the routes a task may run on, chosen from
// measured cost per useful card) that the launcher reads, not a queue.
//
// Two callers, one rule: `nova-pulse sprint refill` (the sprint's open list) and the card
// dealer in `nova-pulse fill` (the ready queue). The fill loop reads directories today, so
// it is not yet wired here; when it moves onto the store it calls Plan with its benches as
// consumers and its ready cards as items, and the rule stays one function. A second copy of
// this rule for friends is exactly what this package exists to prevent.
package deal

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// LowWater is the depth at which a consumer's queue is refilled, and Width the depth it is
// topped to when no table says otherwise. Four is Glenn's number for a friend (2026-09-22,
// "a queue if you will. as above, so below"); a bench's width comes from its capability row.
const (
	LowWater     = 2
	FriendWidth  = 4
	DefaultWidth = 4
)

// The localities a task may demand and a consumer may offer. House is the uplink behind
// Glenn's line; datacenter is a rented box on the tailnet; Any matches either.
const (
	LocalityHouse      = "house"
	LocalityDatacenter = "datacenter"
	LocalityAny        = "any"
)

// The isolation levels. Net says the task may reach the network; NoNet says it must not.
const (
	IsolationNet   = "net"
	IsolationNoNet = "nonet"
)

// The consumer kinds. A bench is a machine with slots; a friend is a person's window with
// one slot at a time and a width of four queued.
const (
	KindBench  = "bench"
	KindFriend = "friend"
)

// Requirements is what a task NEEDS. Every field is a requirement a consumer either meets
// or does not; an empty field means "no requirement" and matches anything.
type Requirements struct {
	// Leg is the toolchain leg the task's paths need (go, c+cpp, python, lua, ...).
	Leg string
	// Locality is house, datacenter or any: where the task's network may come from.
	Locality string
	// Repo and Base name the warm mirror the task wants (repo@base), for affinity only:
	// a consumer without the mirror still matches, it just scores lower.
	Repo string
	Base string
	// WallMinutes is the estimate. A consumer whose MaxWallMinutes is smaller does not match:
	// a two hour evaluation does not belong in a pool whose idle bound is five minutes.
	WallMinutes int
	// Isolation is net or nonet.
	Isolation string
	// Kind is card|read|fix|decision|repair|review|ruling|script|evaluation. A consumer
	// declares the kinds it supports; a script or CI task needs no model at all.
	Kind string
	// CostCeilingUSD is the most this task may spend; 0 means no ceiling.
	CostCeilingUSD float64
	// Routes are the model routes this task may run on (its TIER, as a field). Empty means
	// any route the consumer offers; a task that calls no model leaves it empty.
	Routes []string
	// Owner is the name the task already belongs to, if any. An owned task matches ITS
	// OWNER AND NOTHING ELSE: ownership does not move because a queue was short. It moves
	// when the owner hands it over, and a hand-over clears this field and leaves them the
	// required reader, so the verdict stays theirs while the typing moves.
	Owner string
	// ConsumerKind is the rule table's one output that is about the consumer rather than
	// the work: a live or security path, a read, a decision or a ruling wants a FRIEND,
	// and no queue depth makes that untrue.
	ConsumerKind string
	// Unblocks is how many other ready tasks this one unblocks; Age is how long it has been
	// open. Both are scoring inputs, not match inputs.
	Unblocks int
	Age      time.Duration
	// Priority is the front tier: a fix or recut found in the current batch re-enters at the
	// FRONT (ruling 2026-09-20). It picks the stream suffix, not the stream.
	Priority bool
}

// Capabilities is what a consumer CAN DO. It is one row of the consumers table.
type Capabilities struct {
	Name           string
	Kind           string // bench | friend
	Legs           []string
	Locality       string
	Mirrors        []string // "owner/name@branch" warm mirrors
	MaxWallMinutes int
	Isolation      []string
	Kinds          []string
	Routes         []string
	Width          int
	// Present is the measured heartbeat: a bench:<name> row for a bench, a friend:<name>
	// key for a friend (#2612). A consumer that is not present takes nothing -- never route
	// to an away friend (Glenn, 2026-09-22).
	Present bool
}

// Queue is the stream a consumer reads. It is derived, never hardcoded: rename a consumer
// in the table and the stream name follows.
func (c Capabilities) Queue() string { return "q:" + c.Name }

// FrontQueue is the priority stream beside the bulk one for the same consumer. Priority is
// a separate stream per consumer (or a score), never a separate consumer.
func (c Capabilities) FrontQueue() string { return "q:" + c.Name + ":front" }

// Item is one ready task as the dealer sees it: an id, its requirements, and nothing else.
// The dealer never reads a task's prose.
type Item struct {
	ID   string
	Req  Requirements
	Time time.Time // when it entered the ready set; the age tie-break
}

// Placement is one decision: this item, on this consumer's stream, for this reason.
type Placement struct {
	ItemID   string
	Consumer string
	Queue    string
	Reason   string
}

// Match picks the consumer for one item's requirements, or says why none matched. The
// reason is recorded on the task as route_reason: a route with no reason is a guess.
//
// load is how many minutes of open work each consumer already holds, by name; it is what
// makes the router SHORTEN THE WALL rather than pile a second task on whoever sorted first
// (Glenn, 2026-09-22: "There is no reason not to optimize wall clock, when it costs us
// nothing in the other dimensions"). A caller with no load map passes nil, and the tie
// falls to the name so the answer is the same on every machine.
func Match(req Requirements, cs []Capabilities, load map[string]int) (Capabilities, string, error) {
	var eligible []Capabilities
	var why []string
	for _, c := range cs {
		if r := misfit(req, c); r != "" {
			why = append(why, c.Name+": "+r)
			continue
		}
		eligible = append(eligible, c)
	}
	if len(eligible) == 0 {
		sort.Strings(why)
		return Capabilities{}, "", fmt.Errorf("no consumer matches %s; %s", describe(req), strings.Join(why, "; "))
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		si, sj := fit(req, eligible[i]), fit(req, eligible[j])
		if si != sj {
			return si > sj
		}
		if li, lj := load[eligible[i].Name], load[eligible[j].Name]; li != lj {
			return li < lj
		}
		return eligible[i].Name < eligible[j].Name
	})
	best := eligible[0]
	return best, matchReason(req, best), nil
}

// misfit returns the one sentence that disqualifies c for req, or "" when it fits.
func misfit(req Requirements, c Capabilities) string {
	if !c.Present {
		return "not present"
	}
	if req.Owner != "" && !strings.EqualFold(c.Name, req.Owner) {
		return "owned by " + req.Owner
	}
	if req.ConsumerKind != "" && c.Kind != req.ConsumerKind {
		return "is a " + c.Kind + ", and this wants a " + req.ConsumerKind
	}
	if req.Leg != "" && len(c.Legs) > 0 && !has(c.Legs, req.Leg) {
		return "no " + req.Leg + " leg"
	}
	if req.Locality != "" && req.Locality != LocalityAny && c.Locality != "" && c.Locality != req.Locality {
		return "locality " + c.Locality + ", wants " + req.Locality
	}
	if req.WallMinutes > 0 && c.MaxWallMinutes > 0 && req.WallMinutes > c.MaxWallMinutes {
		return fmt.Sprintf("wall %dm over its %dm bound", req.WallMinutes, c.MaxWallMinutes)
	}
	if req.Isolation != "" && len(c.Isolation) > 0 && !has(c.Isolation, req.Isolation) {
		return "no " + req.Isolation + " isolation"
	}
	if req.Kind != "" && len(c.Kinds) > 0 && !has(c.Kinds, req.Kind) {
		return "does not take " + req.Kind
	}
	if len(req.Routes) > 0 && len(c.Routes) > 0 && !anyOf(c.Routes, req.Routes) {
		return "no route in " + strings.Join(req.Routes, ",")
	}
	return ""
}

// fit scores an eligible consumer: a warm mirror first (a cold clone cost three minutes a
// card), then the friend the task is owned by, then the narrower wall bound, so a five
// minute read does not take the pool a two hour evaluation needs.
func fit(req Requirements, c Capabilities) int {
	score := 0
	if req.Repo != "" && has(c.Mirrors, req.Repo+"@"+req.Base) {
		score += 100
	}
	if req.Owner != "" && strings.EqualFold(c.Name, req.Owner) {
		score += 50
	}
	if c.MaxWallMinutes > 0 && req.WallMinutes > 0 {
		slack := c.MaxWallMinutes - req.WallMinutes
		if slack >= 0 && slack < 60 {
			score += 10
		}
	}
	return score
}

func matchReason(req Requirements, c Capabilities) string {
	var parts []string
	if req.Owner != "" && strings.EqualFold(c.Name, req.Owner) {
		parts = append(parts, "owner")
	}
	if req.ConsumerKind != "" {
		parts = append(parts, req.ConsumerKind+" only")
	}
	if req.Leg != "" {
		parts = append(parts, "leg "+req.Leg)
	}
	if req.Locality != "" && req.Locality != LocalityAny {
		parts = append(parts, "locality "+req.Locality)
	}
	if req.Repo != "" && has(c.Mirrors, req.Repo+"@"+req.Base) {
		parts = append(parts, "warm "+req.Repo+"@"+req.Base)
	}
	if req.Kind != "" {
		parts = append(parts, "kind "+req.Kind)
	}
	if len(parts) == 0 {
		parts = append(parts, "any consumer")
	}
	return strings.Join(parts, ", ")
}

func describe(req Requirements) string {
	var parts []string
	for _, kv := range [][2]string{{"kind", req.Kind}, {"leg", req.Leg}, {"locality", req.Locality}, {"isolation", req.Isolation}, {"owner", req.Owner}} {
		if kv[1] != "" {
			parts = append(parts, kv[0]+"="+kv[1])
		}
	}
	if req.WallMinutes > 0 {
		parts = append(parts, fmt.Sprintf("wall=%dm", req.WallMinutes))
	}
	if len(parts) == 0 {
		return "a task with no requirements"
	}
	return strings.Join(parts, " ")
}

// Plan is the refill: for every consumer whose depth is below the low water mark, take
// items until its queue holds Width, OWNERSHIP FIRST THEN AGE. A consumer that is not
// present takes nothing; an item goes to exactly one consumer; an item nobody matches is
// left in the ready set and reported by the caller, never dropped.
//
// depths is the current queue depth per consumer name. lowWater of 0 means LowWater.
func Plan(cs []Capabilities, depths map[string]int, ready []Item, lowWater int) []Placement {
	if lowWater <= 0 {
		lowWater = LowWater
	}
	items := append([]Item(nil), ready...)
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Req.Priority != items[j].Req.Priority {
			return items[i].Req.Priority
		}
		if items[i].Req.Unblocks != items[j].Req.Unblocks {
			return items[i].Req.Unblocks > items[j].Req.Unblocks
		}
		if !items[i].Time.Equal(items[j].Time) {
			return items[i].Time.Before(items[j].Time)
		}
		return items[i].ID < items[j].ID
	})
	consumers := append([]Capabilities(nil), cs...)
	sort.SliceStable(consumers, func(i, j int) bool { return consumers[i].Name < consumers[j].Name })

	taken := map[string]bool{}
	var out []Placement
	for _, c := range consumers {
		if !c.Present {
			continue
		}
		width := c.Width
		if width <= 0 {
			if c.Kind == KindFriend {
				width = FriendWidth
			} else {
				width = DefaultWidth
			}
		}
		depth := depths[c.Name]
		if depth >= lowWater {
			continue
		}
		// Ownership first, then age: the owned items in age order, then everything else
		// this consumer matches, in the scored order above.
		var owned, rest []Item
		for _, it := range items {
			if taken[it.ID] {
				continue
			}
			if misfit(it.Req, c) != "" {
				continue
			}
			if it.Req.Owner != "" && strings.EqualFold(it.Req.Owner, c.Name) {
				owned = append(owned, it)
				continue
			}
			rest = append(rest, it)
		}
		sort.SliceStable(owned, func(i, j int) bool {
			if !owned[i].Time.Equal(owned[j].Time) {
				return owned[i].Time.Before(owned[j].Time)
			}
			return owned[i].ID < owned[j].ID
		})
		for _, it := range append(owned, rest...) {
			if depth >= width {
				break
			}
			queue := c.Queue()
			if it.Req.Priority {
				queue = c.FrontQueue()
			}
			reason := matchReason(it.Req, c)
			if it.Req.Owner != "" && strings.EqualFold(it.Req.Owner, c.Name) {
				reason = "owner"
			}
			out = append(out, Placement{ItemID: it.ID, Consumer: c.Name, Queue: queue, Reason: reason})
			taken[it.ID] = true
			depth++
		}
	}
	return out
}

func has(set []string, want string) bool {
	for _, s := range set {
		if strings.EqualFold(strings.TrimSpace(s), strings.TrimSpace(want)) {
			return true
		}
	}
	return false
}

func anyOf(set, wants []string) bool {
	for _, w := range wants {
		if has(set, w) {
			return true
		}
	}
	return false
}
