package friend

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
)

// The stages of the delivery check, in the order they are passed: the
// adapter took the text (deliver), the session ran the pong line it carries
// and the pong is on the bus (act; reply is the bus log not read).
const (
	StageDeliver = "deliver"
	StageAct     = "act"
	StageReply   = "reply"
)

// DefaultCheckWithin is how long the check waits for the session's pong by
// default: the session check's own bound (SessionBound). CheckPoll is how
// often it reads the bus and the pong file meanwhile.
const (
	DefaultCheckWithin = SessionBound
	CheckPoll          = time.Second
)

// Conformance is the one promise every harness adapter makes, checked end to
// end (docs/SPEC-FRIEND.md, delivery-conformance-r.w1; the presence model's
// Ask then Answer within the bound, tla/FriendPresence.tla): a session check
// carrying a fresh nonce goes in through the adapter, the session runs the
// exact pong line it carries, and a pong with that nonce from the friend is
// on the bus within Within. The unit tier runs it over a fake Exec and bus's
// Fake; nova-friend check and install run it against the live session.
type Conformance struct {
	Friend, Harness string
	Deliver         Deliverer
	Store           bus.Store
	Within          time.Duration
	Now             func() time.Time
	Wait            func(ctx context.Context) bool // one CheckPoll; false once ctx has ended
	Nonce           func() string
	Text            func(nonce string) string // the session check as the session reads it (SessionCheckText)
}

// CheckResult is one run of the check: Stage empty is a pass.
type CheckResult struct {
	Harness string        `json:"harness"`
	Nonce   string        `json:"nonce"`
	Stage   string        `json:"stage,omitempty"`
	Why     string        `json:"why,omitempty"`
	Took    time.Duration `json:"took"`
}

// Line is the result as nova-friend check prints it.
func (r CheckResult) Line() string {
	if r.Stage == "" {
		return "CHECK OK harness=" + oneline.Field(r.Harness) + " took=" + r.Took.String()
	}
	return "CHECK FAIL harness=" + oneline.Field(r.Harness) + " stage=" + r.Stage + " why=" + oneline.Quote(r.Why)
}

// Run delivers the check and reads for its pong until Within has passed. A
// Stub's refusal, a Deferred and a nonzero exit are stage deliver, each with
// the adapter's own reason.
func (c *Conformance) Run(ctx context.Context) CheckResult {
	start := c.Now()
	nonce := c.Nonce()
	r := CheckResult{Harness: c.Harness, Nonce: nonce}
	fail := func(stage, why string) CheckResult {
		r.Stage, r.Why, r.Took = stage, why, c.Now().Sub(start)
		return r
	}
	_, storeNow, err := c.Store.Roster(ctx)
	if err != nil {
		return fail(StageDeliver, "the store did not answer: "+err.Error())
	}
	cursor := bus.IDAt(storeNow) // a pong from before the check never counts
	dctx, cancel := context.WithTimeout(ctx, c.Within)
	exit, err := c.Deliver.Deliver(dctx, c.Text(nonce))
	cancel()
	var deferred Deferred
	switch {
	case errors.As(err, &deferred):
		return fail(StageDeliver, deferred.Error())
	case err != nil:
		return fail(StageDeliver, err.Error())
	case exit != 0:
		return fail(StageDeliver, fmt.Sprintf("%s's deliver command exited %d", c.Harness, exit))
	}
	// The pong is read as every pong is (PongReader.Message): the table form,
	// only the writer's one framing newline dropped, a malformed pong (or a
	// value in the retired legacy form) refused by name and never taken as
	// the answer.
	pongs := NewPongReader()
	var refused error
	for {
		es, err := c.Store.Range(ctx, bus.LogKey, "("+cursor, "+", LogBatch)
		if err != nil {
			return fail(StageReply, "the bus log: "+err.Error())
		}
		for _, e := range es {
			cursor = e.Entry
			m := e.Message()
			if m.From != c.Friend || !IsPong(m) { // the sender is the message's from, never its body
				continue
			}
			p, _, err := pongs.Message(m)
			switch {
			case err != nil:
				refused = err
			case p.Nonce == nonce:
				r.Took = c.Now().Sub(start)
				return r
			}
		}
		if c.Now().Sub(start) >= c.Within {
			why := fmt.Sprintf("no pong %s from %s within %s: the session did not run the line the check carried", nonce, c.Friend, c.Within)
			if refused != nil {
				why += "; a pong from " + c.Friend + " was refused: " + refused.Error()
			}
			return fail(StageAct, why)
		}
		if !c.Wait(ctx) {
			why := "the wait ended"
			if cause := context.Cause(ctx); cause != nil {
				why = cause.Error()
			}
			return fail(StageAct, "stopped before the pong: "+why)
		}
	}
}
