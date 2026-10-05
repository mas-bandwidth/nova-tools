package friend

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ReachStep is one increasingly direct way to get a friend's attention. The
// machine has no transport: callers inject the bus, daemon and window effects
// (SPEC-FRIEND.md, Reach).
type ReachStep string

const (
	ReachBus    ReachStep = "bus"
	ReachPush   ReachStep = "push"
	ReachWindow ReachStep = "window"
)

var ReachSteps = []ReachStep{ReachBus, ReachPush, ReachWindow}

// Reach is the escalation ladder over an injected clock and effects. Proof
// must report the session's nonce-bearing pong or a real message; it is the
// only transition to OK, so no later side effect follows proof.
type Reach struct {
	Now   func() time.Time
	Wait  func(context.Context, time.Duration)
	Do    func(context.Context, ReachStep, string) (sent, skip string, err error)
	Proof func(context.Context, string) (by string, ok bool, err error)
	Line  func(string)
}

// Run performs the requested suffix of the ladder. An effect skip is recorded
// as NONE and does not pretend it ran; a failed effect is a could-not-run error.
func (r Reach) Run(ctx context.Context, from ReachStep, nonce string, timeout time.Duration) (ReachStep, bool, error) {
	start := 0
	for i, step := range ReachSteps {
		if step == from {
			start = i
			break
		}
	}
	for _, step := range ReachSteps[start:] {
		if err := ctx.Err(); err != nil { return step, false, err }
		// A proof that arrived while the prior rung closed wins before this
		// rung can produce another side effect.
		// One clock budget covers the pre-rung proof read, delivery and every
		// subsequent proof read; none may buy a second full timeout.
		began := r.Now()
		remaining := timeout - r.Now().Sub(began)
		stepCtx, cancel := context.WithTimeout(ctx, remaining)
		if by, ok, err := r.Proof(stepCtx, nonce); err != nil { cancel(); if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil { if r.Line != nil { r.Line(fmt.Sprintf("REACH NONE step=%s waited=%s", step, timeout)) }; continue }; return step, false, err
		} else if ok { cancel(); if r.Line != nil { r.Line(fmt.Sprintf("REACH PROOF step=%s after=0s by=%s", step, by)) }; return step, true, nil }
		cancel()
		remaining = timeout - r.Now().Sub(began)
		if remaining <= 0 { if r.Line != nil { r.Line(fmt.Sprintf("REACH NONE step=%s waited=%s", step, timeout)) }; continue }
		stepCtx, cancel = context.WithTimeout(ctx, remaining)
		sent, skip, err := r.Do(stepCtx, step, nonce)
		if err != nil {
			cancel()
			if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil { if r.Line != nil { r.Line(fmt.Sprintf("REACH NONE step=%s waited=%s", step, timeout)) }; continue }
			return step, false, err
		}
		if r.Line != nil {
			line := fmt.Sprintf("REACH STEP step=%s", step)
			if sent != "" {
				line += " sent=" + sent
			}
			r.Line(line + " nonce=" + nonce)
		}
		if skip != "" {
			if r.Line != nil {
				r.Line(fmt.Sprintf("REACH NONE step=%s waited=0s reason=%s", step, skip))
			}
			cancel(); continue
		}
		for {
			if err := ctx.Err(); err != nil { return step, false, err }
			left := timeout - r.Now().Sub(began)
			if left <= 0 { if r.Line != nil { r.Line(fmt.Sprintf("REACH NONE step=%s waited=%s", step, timeout)) }; cancel(); break }
			cancel(); stepCtx, cancel = context.WithTimeout(ctx, left)
			by, ok, err := r.Proof(stepCtx, nonce)
			if err != nil {
				cancel(); if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil { break }
				return step, false, err
			}
			if ok {
				if r.Line != nil {
					r.Line(fmt.Sprintf("REACH PROOF step=%s after=%s by=%s", step, r.Now().Sub(began).Round(time.Millisecond), by))
				}
				cancel(); return step, true, nil
			}
			if r.Now().Sub(began) >= timeout {
				if r.Line != nil {
					r.Line(fmt.Sprintf("REACH NONE step=%s waited=%s", step, timeout))
				}
				break
			}
			left = timeout - r.Now().Sub(began)
			wait := min(time.Second, left)
			before := r.Now()
			r.Wait(ctx, wait)
			if err := ctx.Err(); err != nil { return step, false, err }
			if !r.Now().After(before) { return step, false, fmt.Errorf("reach clock did not advance while waiting") }
		}
	}
	return ReachWindow, false, nil
}
