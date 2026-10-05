package sprint

import (
	"slices"
	"strconv"
	"time"
)

// The lanes (docs/SPEC-SPRINT.md section 18): the one-Go-test-stream-per-machine rule
// (docs/STANDARD.md, "one test stream per machine") enforced by a lock the machine
// grants, not by the coordinator. A lane kind names what it guards (LaneGo, a Go build
// or test run); each machine has its own lanes of each kind, as many as the width. A
// worker takes a lane before its run and gives it back after: a take is granted while
// the machine's holders are fewer than the width and nobody waits ahead of it, and is
// otherwise queued behind the waiters in the order they first asked, so a take never
// passes one that asked before it. A waiter asks again until it is granted; the grant
// is the machine's, made at a take or a give of anyone, never by a message.
//
// Nothing is held forever. A holder that does not take again (a renewal) within
// LaneHoldFor is released; a waiter granted while it was not asking that does not
// claim the grant within LaneWaitFor is released; a waiter that stops asking for
// LaneWaitFor leaves the queue. Each release grants the head of the queue. A narrower
// width takes no lane back: it grants none until the holders are under it.

const (
	// LaneGo is the lane kind of a Go build or test run: one per machine unless the
	// sprint's setting says otherwise (PropGoLanes).
	LaneGo = "go"
	// LaneHoldFor is how long a holder keeps its lane without taking it again: past a
	// whole gate run of `go test -timeout 600s` with room for its build, so a worker
	// that exits without giving it back (killed, lost) frees it.
	LaneHoldFor = 20 * time.Minute
	// LaneWaitFor is how long a waiter keeps its place without asking again, and how
	// long a grant made while it was not asking waits for it to claim it.
	LaneWaitFor = time.Minute
	// LaneAskEvery is how often a waiting take asks again (lane take --wait): well
	// inside LaneWaitFor, so a waiter that is there never loses its place.
	LaneAskEvery = 5 * time.Second
	// LaneWidthDefault is a machine's lanes of a kind when the sprint sets none: the
	// one test stream per machine.
	LaneWidthDefault = 1
	// PropGoLanes is the work table's property: the Go lanes of every machine, a
	// whole number from 1 (`set --go-lanes`).
	PropGoLanes = "go_lanes"
)

// LaneKinds is every lane kind there is.
var LaneKinds = []string{LaneGo}

// LaneHold is one holder of a lane: who, when it was granted, when it last took it,
// and whether it has claimed the grant (a waiter granted at another's give has not,
// until it takes again).
type LaneHold struct {
	Who     string    `json:"who"`
	Since   time.Time `json:"since"`
	Seen    time.Time `json:"seen"`
	Claimed bool      `json:"claimed,omitempty"`
}

// LaneWait is one waiter: who, when it first asked, and when it last asked.
type LaneWait struct {
	Who   string    `json:"who"`
	Since time.Time `json:"since"`
	Seen  time.Time `json:"seen"`
}

// Lane is one machine's lanes of one kind: its holders and its queue, oldest first.
type Lane struct {
	Holders []LaneHold `json:"holders,omitempty"`
	Queue   []LaneWait `json:"queue,omitempty"`
}

// Lanes is a kind's lanes by machine: the record the store keeps.
type Lanes map[string]Lane

// LaneAnswer is a take's or a give's answer: whether the asker holds a lane, its place
// in the queue (1 is next) when it does not, the holders and the width after, and who
// the step released by the timeouts.
type LaneAnswer struct {
	Granted  bool     `json:"granted"`
	Place    int      `json:"place,omitempty"`
	Held     int      `json:"held"`
	Width    int      `json:"width"`
	Gave     bool     `json:"gave,omitempty"` // give: the asker held or waited, and does not now
	Released []string `json:"released,omitempty"`
}

// LaneWidth is the lanes of a kind per machine from the work table's property value:
// a whole number from 1, else LaneWidthDefault (none set, default, or unreadable).
func LaneWidth(v string, set bool) int {
	if n, err := strconv.Atoi(v); set && err == nil && n >= 1 {
		return n
	}
	return LaneWidthDefault
}

// Take is who's take of a lane on machine at now, the width given: the lanes after and
// the answer (section 18). The timeouts are applied first, then a holder renews, a
// waiter keeps its place, a newcomer joins the back of the queue; then the queue's head
// is granted while there is room, so a take is granted only at the head.
func (ls Lanes) Take(machine, who string, width int, now time.Time) (Lanes, LaneAnswer) {
	l, released := ls[machine].expire(now)
	if i := l.hold(who); i >= 0 {
		l.Holders[i].Seen, l.Holders[i].Claimed = now, true
	} else if i := l.wait(who); i >= 0 {
		l.Queue[i].Seen = now
	} else {
		l.Queue = append(l.Queue, LaneWait{Who: who, Since: now, Seen: now})
	}
	l = l.grant(width, now)
	ans := LaneAnswer{Held: len(l.Holders), Width: width, Released: released}
	if i := l.hold(who); i >= 0 {
		l.Holders[i].Seen, l.Holders[i].Claimed = now, true
		ans.Granted = true
	} else {
		ans.Place = l.wait(who) + 1
	}
	return ls.with(machine, l), ans
}

// Give is who's give of its lane, or of its place in the queue, on machine at now: the
// lanes after, with the queue's head granted into the room it made.
func (ls Lanes) Give(machine, who string, width int, now time.Time) (Lanes, LaneAnswer) {
	l, released := ls[machine].expire(now)
	n := len(l.Holders) + len(l.Queue)
	l.Holders = slices.DeleteFunc(l.Holders, func(h LaneHold) bool { return h.Who == who })
	l.Queue = slices.DeleteFunc(l.Queue, func(w LaneWait) bool { return w.Who == who })
	gave := len(l.Holders)+len(l.Queue) < n
	l = l.grant(width, now)
	return ls.with(machine, l), LaneAnswer{Held: len(l.Holders), Width: width, Gave: gave, Released: released}
}

// Expire is the lanes at now with the timeouts applied and the queues' heads granted,
// every machine at once: what a reader of the lanes sees.
func (ls Lanes) Expire(width int, now time.Time) Lanes {
	out := Lanes{}
	for m, l := range ls {
		l, _ = l.expire(now)
		out = out.with(m, l.grant(width, now))
	}
	return out
}

// with is the lanes with machine's lane set; an empty lane is no entry.
func (ls Lanes) with(machine string, l Lane) Lanes {
	out := make(Lanes, len(ls)+1)
	for m, x := range ls {
		out[m] = x
	}
	delete(out, machine)
	if len(l.Holders)+len(l.Queue) > 0 {
		out[machine] = l
	}
	return out
}

// expire releases a holder not seen for LaneHoldFor, a grant not claimed for
// LaneWaitFor, and a waiter not seen for LaneWaitFor; released is who it released.
func (l Lane) expire(now time.Time) (Lane, []string) {
	var out Lane
	var released []string
	for _, h := range l.Holders {
		if now.Sub(h.Seen) > LaneHoldFor || !h.Claimed && now.Sub(h.Since) > LaneWaitFor {
			released = append(released, h.Who)
			continue
		}
		out.Holders = append(out.Holders, h)
	}
	for _, w := range l.Queue {
		if now.Sub(w.Seen) > LaneWaitFor {
			released = append(released, w.Who)
			continue
		}
		out.Queue = append(out.Queue, w)
	}
	return out, released
}

// grant moves the queue's head to the holders while they are fewer than width: a
// grant not yet claimed, since now.
func (l Lane) grant(width int, now time.Time) Lane {
	for len(l.Holders) < width && len(l.Queue) > 0 {
		w := l.Queue[0]
		l.Queue = slices.Clone(l.Queue[1:])
		l.Holders = append(slices.Clone(l.Holders), LaneHold{Who: w.Who, Since: now, Seen: w.Seen})
	}
	return l
}

func (l Lane) hold(who string) int {
	return slices.IndexFunc(l.Holders, func(h LaneHold) bool { return h.Who == who })
}

func (l Lane) wait(who string) int {
	return slices.IndexFunc(l.Queue, func(w LaneWait) bool { return w.Who == who })
}

// holders is who holds a lane, in the order granted.
func (l Lane) holders() []string {
	var out []string
	for _, h := range l.Holders {
		out = append(out, h.Who)
	}
	return out
}

// waiters is who waits, in the order they first asked.
func (l Lane) waiters() []string {
	var out []string
	for _, w := range l.Queue {
		out = append(out, w.Who)
	}
	return out
}

// LaneRow is one machine's lanes of a kind as a reader sees them (lane list, where
// --json): the holders and waiters in order, with when the oldest holder was granted.
type LaneRow struct {
	Kind    string    `json:"kind"`
	Machine string    `json:"machine"`
	Width   int       `json:"width"`
	Held    []string  `json:"held"`
	Waiting []string  `json:"waiting"`
	Since   time.Time `json:"since,omitzero"`
}

// Rows is the lanes of kind at now (Expire applied), one row a machine, by machine.
func (ls Lanes) Rows(kind string, width int, now time.Time) []LaneRow {
	cur := ls.Expire(width, now)
	ms := make([]string, 0, len(cur))
	for m := range cur {
		ms = append(ms, m)
	}
	slices.Sort(ms)
	out := make([]LaneRow, 0, len(ms))
	for _, m := range ms {
		l := cur[m]
		r := LaneRow{Kind: kind, Machine: m, Width: width, Held: l.holders(), Waiting: l.waiters()}
		if r.Held == nil {
			r.Held = []string{}
		}
		if r.Waiting == nil {
			r.Waiting = []string{}
		}
		if len(l.Holders) > 0 {
			r.Since = l.Holders[0].Since
		}
		out = append(out, r)
	}
	return out
}
