package machine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// R14's phase 2: the push, outside the store (2.3, R14). Phase 1 is a step of
// the rule (IT10): guarded by the entry's score as read, it moves
// remind:<person> to R + 5 min and writes claimed_gen and claimed_r on the
// goal record. Phase 2 runs in the loop after RT3: it builds the packet from
// its read (counts only) and pushes it along the person's route with a time
// limit of 2 s, holding no store call while it waits. Phase 3 records the
// outcome in the next tick's RT1 step. Only the lease holder's phase 1
// applies, so only one loop pushes; at most one push a period.

// PushLimit is the time a push may take (2.3, R14).
const PushLimit = 2 * time.Second

// Goal is one person's goal as phase 1 claimed it: the person, the goal text,
// the route, and the claim, the lease generation and the running time R of
// the step that claimed this period's push.
type Goal struct {
	Person, Text, Route string
	ClaimedGen          uint64
	ClaimedR            int64
}

// Packet is what a push carries: counts only, from phase 1's read (the size
// of jnotes, which is the open judgments), and which sprint and epoch it is of.
type Packet struct {
	N      int // the push's number for the person
	Open   int // the open judgments (jnotes' size)
	Sprint string
	Epoch  uint64
	At     time.Time
}

// Outcome is what a push did, for phase 3: delivered, or failed with its
// error, or timed out. Claim names the period it was for.
type Outcome struct {
	Person     string
	ClaimedGen uint64
	ClaimedR   int64
	Delivered  bool
	TimedOut   bool
	Err        string
}

// Deliver is how a push goes down a route: the present build's deliverers
// (store.NewDeliverer, a file route), or a test's.
type Deliver func(route string) (store.Deliverer, error)

// deliverer is the route's deliverer the loop uses unless told another.
var deliverer Deliver = store.NewDeliverer

// Push pushes one packet along the goal's route, within PushLimit or the
// context's own deadline, whichever is sooner. It holds no store call. A
// deliverer that has not returned by then is left to finish on its own, and
// the push is timed out.
func Push(ctx context.Context, g Goal, packet Packet) Outcome {
	return push(ctx, deliverer, g, packet)
}

func push(ctx context.Context, d Deliver, g Goal, packet Packet) Outcome {
	out := Outcome{Person: g.Person, ClaimedGen: g.ClaimedGen, ClaimedR: g.ClaimedR}
	dv, err := d(g.Route)
	if err != nil {
		out.Err = err.Error()
		return out
	}
	ctx, cancel := context.WithTimeout(ctx, PushLimit)
	defer cancel()
	text := g.Text
	if packet.Open > 0 {
		text += fmt.Sprintf("\n%d open judgments wait in the inbox.", packet.Open)
	}
	done := make(chan error, 1)
	go func() {
		done <- dv.Deliver(store.Reminder{N: packet.N, To: g.Person, At: packet.At, Sprint: packet.Sprint, Epoch: packet.Epoch, Text: text})
	}()
	select {
	case err := <-done:
		if err != nil {
			out.Err = err.Error()
			return out
		}
		out.Delivered = true
	case <-ctx.Done():
		out.TimedOut = true
		out.Err = ctx.Err().Error()
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			out.Err = "the push did not arrive within its time limit"
		}
	}
	return out
}

// pusher pushes each claim once: a claim is a person's period, named by the
// generation and the running time of the step that claimed it, so a second
// sight of the same claim (the same tick's plan seen again, a key delivered
// twice) pushes nothing, and at most one push goes out a period (2.3, R14).
type pusher struct {
	deliver Deliver
	pushed  map[string]int64 // person -> the ClaimedR of the last claim pushed
	gen     map[string]uint64
}

func newPusher(d Deliver) *pusher {
	if d == nil {
		d = deliverer
	}
	return &pusher{deliver: d, pushed: map[string]int64{}, gen: map[string]uint64{}}
}

// run pushes the claims not yet pushed, in order, and returns their outcomes.
func (p *pusher) run(ctx context.Context, claims []Claim) []Outcome {
	var out []Outcome
	for _, c := range claims {
		if r, ok := p.pushed[c.Goal.Person]; ok && r == c.Goal.ClaimedR && p.gen[c.Goal.Person] == c.Goal.ClaimedGen {
			continue
		}
		p.pushed[c.Goal.Person], p.gen[c.Goal.Person] = c.Goal.ClaimedR, c.Goal.ClaimedGen
		out = append(out, push(ctx, p.deliver, c.Goal, c.Packet))
	}
	return out
}

// Claim is one push phase 1 claimed this tick: the goal and its packet.
type Claim struct {
	Goal   Goal
	Packet Packet
}
