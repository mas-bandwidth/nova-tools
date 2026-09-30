package machine

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// R14's phase 2: the push, outside the store (2.3, R14). Phase 1 is a step of
// the rule (IT10): guarded by the entry's score as read, it moves
// remind:<person> to R + 5 min and writes claimed_gen and claimed_r on the
// goal record. Phase 2 runs in the loop after RT3: it builds the packet from
// its read (counts only) and pushes it along the person's route with a time
// limit of 2 s, holding no store call while it waits; the pushes run off the
// tick (pusher). Phase 3 records the outcome in the next tick's RT1 step, the
// error step, as the judgment "a reminder could not be delivered" opened or
// closed (outcomeNote). Only the lease holder's phase 1 applies, so only one
// loop pushes; at most one push a period.

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
// of jnotes, which is the open judgments), and the epoch it is of (a store
// holds one sprint).
type Packet struct {
	N     int // the push's number for the person
	Open  int // the open judgments (jnotes' size)
	Epoch uint64
	At    time.Time
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
		done <- dv.Deliver(store.Reminder{N: packet.N, To: g.Person, At: packet.At, Epoch: packet.Epoch, Text: text})
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

// The bounds of R14's phases 2 and 3 in the loop.
const (
	// PushesInFlightMax is the most pushes that run at once. A claim past it
	// is not pushed this period (phase 1 moved its entry: the next period
	// claims again, 2.3 R14), and the report counts it.
	PushesInFlightMax = 64
	// OutcomesMax is the most outcomes the loop keeps for phase 3. Past it the
	// oldest goes: an outcome is recorded by the next RT1 step, and a person's
	// next push brings a newer one.
	OutcomesMax = 256
)

// pusher runs phase 2 off the tick (2.3, R14: "outside the store, in the loop
// after RT3 ... holds no store call while it waits"; "the push is outside
// every round trip's budget"). The tick starts each claim's push and goes on:
// the pushes run at once, each within PushLimit, and their outcomes wait for
// the next tick's RT1, whose error step records them (phase 3). So a tick
// never waits on a route, whatever the number of claims.
//
// Each claim is pushed once: a claim is a person's period, named by the
// generation and the running time of the step that claimed it, so a second
// sight of the same claim (the same tick's plan seen again, a key delivered
// twice) pushes nothing, and at most one push goes out a period. A push that
// fails is not retried by the loop: the next period's claim pushes again.
type pusher struct {
	deliver Deliver
	mu      sync.Mutex
	pushed  map[string]int64 // person -> the ClaimedR of the last claim pushed
	gen     map[string]uint64
	running int
	done    []Outcome // finished and not yet taken, at most OutcomesMax
	lost    int       // outcomes dropped past OutcomesMax
	wg      sync.WaitGroup
}

func newPusher(d Deliver) *pusher {
	if d == nil {
		d = deliverer
	}
	return &pusher{deliver: d, pushed: map[string]int64{}, gen: map[string]uint64{}}
}

// start starts the push of each claim not yet pushed and returns at once:
// how many it started and how many it could not (PushesInFlightMax).
func (p *pusher) start(ctx context.Context, claims []Claim) (started, skipped int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range claims {
		if r, ok := p.pushed[c.Goal.Person]; ok && r == c.Goal.ClaimedR && p.gen[c.Goal.Person] == c.Goal.ClaimedGen {
			continue
		}
		if p.running >= PushesInFlightMax {
			skipped++
			continue
		}
		p.pushed[c.Goal.Person], p.gen[c.Goal.Person] = c.Goal.ClaimedR, c.Goal.ClaimedGen
		p.running++
		started++
		p.wg.Add(1)
		go func(c Claim) {
			defer p.wg.Done()
			out := push(ctx, p.deliver, c.Goal, c.Packet)
			p.mu.Lock()
			defer p.mu.Unlock()
			p.running--
			p.done = append(p.done, out)
			if over := len(p.done) - OutcomesMax; over > 0 {
				p.done = append([]Outcome(nil), p.done[over:]...)
				p.lost += over
			}
		}(c)
	}
	return started, skipped
}

// take hands over the outcomes finished so far, oldest first.
func (p *pusher) take() []Outcome {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := p.done
	p.done = nil
	return out
}

// wait waits for every push started to finish (each within PushLimit).
func (p *pusher) wait() { p.wg.Wait() }

// run pushes the claims and waits for them: start, wait, take.
func (p *pusher) run(ctx context.Context, claims []Claim) []Outcome {
	p.start(ctx, claims)
	p.wait()
	return p.take()
}

// outcomeNote is phase 3's note of an outcome (2.3, R14; 2.2): delivered,
// "a reminder could not be delivered" is closed if it is open; failed or
// timed out, it is opened, once (J keeps one judgment a cause).
func outcomeNote(o Outcome) sprint.NoteReq {
	if o.Delivered {
		return sprint.NoteReq{Op: "close", Type: sprint.NRemindFailed, Cause: remindCause, Subjects: []string{o.Person},
			Text: "the reminder of " + o.Person + " was delivered"}
	}
	return sprint.NoteReq{Op: "open", Type: sprint.NRemindFailed, Cause: remindCause, Subjects: []string{o.Person},
		Text:      fmt.Sprintf("the reminder of %s could not be delivered: %s", o.Person, o.Err),
		Decisions: []string{"goal set " + o.Person + " --to <route>", "goal drop " + o.Person, "ack"}}
}

// remindCause is the cause of R14's judgment: its owner key's kind (2.2,
// remind:<person>).
const remindCause = "remind"

// Claim is one push phase 1 claimed this tick: the goal and its packet.
type Claim struct {
	Goal   Goal
	Packet Packet
}
