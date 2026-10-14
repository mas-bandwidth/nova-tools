package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
)

// PollEvery is how often the wait reads the log for the answer. The read is
// the log's (bus.LogForward, as nova-bus log reads it): it touches no consumer
// group, so nothing meant for the caller's own receiver is taken or acked.
const PollEvery = 2 * time.Second

// WakeBudget bounds one wake attempt at a window; with two deadlines it is the
// whole run's bound: at most 2 x deadline + WakeBudget (+ one poll's call).
const WakeBudget = 8 * time.Second

// Exit codes.
const (
	ExitAnswered = 0
	ExitNoAnswer = 1 // the caller's signal to notify Glenn
	ExitRefused  = 2 // could not run, or the wake needs a one-time grant
)

// Clock is the time the waits read: the real one in main, a fake in tests.
type Clock interface {
	Now() time.Time
	Sleep(d time.Duration)
}

// Waker is one harness adapter: it puts a line in front of a friend whose
// session is down. A *GrantNeeded error means the OS refused the UI
// scripting and names the process that needs the one-time grant.
type Waker interface {
	Via() string
	Wake(ctx context.Context, line string) error
}

// GrantNeeded is the refusal of UI scripting for want of the Accessibility
// grant: Process is who needs it.
type GrantNeeded struct {
	Process string
	Detail  string
}

func (g *GrantNeeded) Error() string {
	return fmt.Sprintf("%s needs the one-time Accessibility grant (System Settings > Privacy & Security > Accessibility): %s", g.Process, g.Detail)
}

// Ask is one ask-or-wake: send, wait, wake at the deadline, wait again.
type Ask struct {
	Bus      *bus.Bus
	Clock    Clock
	Waker    Waker // nil: the friend has no wake adapter
	As, To   string
	Subject  string
	Body     string
	Deadline time.Duration
	Out      io.Writer
}

// Run is the whole ask; it answers the exit code.
func (a *Ask) Run(ctx context.Context) int {
	if a.Deadline <= 0 {
		fmt.Fprintln(a.Out, "REFUSED --deadline must be above zero: never wait forever")
		return ExitRefused
	}
	// arm before sending, so an answer that lands at once is still past the cursor
	cursor, err := a.Bus.LogCursor(ctx)
	if err != nil {
		fmt.Fprintf(a.Out, "REFUSED the store did not answer: %v\n", err)
		return ExitRefused
	}
	m, err := a.Bus.Send(ctx, bus.Message{From: a.As, To: []string{a.To}, Subject: a.Subject, Body: a.Body, Kind: bus.KindRequest})
	if err != nil {
		fmt.Fprintf(a.Out, "REFUSED send: %v\n", err)
		return ExitRefused
	}
	fmt.Fprintf(a.Out, "SENT %s to=%s at=%s\n", m.ID, a.To, m.At.UTC().Format(time.RFC3339))
	start := a.Clock.Now()

	got, cursor, err := a.wait(ctx, m.ID, cursor, start.Add(a.Deadline))
	if err != nil {
		fmt.Fprintf(a.Out, "REFUSED the store did not answer: %v\n", err)
		return ExitRefused
	}
	if got {
		return a.answered(m.ID, start)
	}

	if a.Waker == nil {
		fmt.Fprintf(a.Out, "NO ANSWER %s after %s and no wake adapter for %s\n", a.To, a.Deadline, a.To)
		return ExitNoAnswer
	}
	line := fmt.Sprintf("Rowan: please read your psychic link (bus message %s) and reply with --re.", m.ID)
	wctx, cancel := context.WithTimeout(ctx, WakeBudget)
	err = a.Waker.Wake(wctx, line)
	cancel()
	var grant *GrantNeeded
	switch {
	case errors.As(err, &grant):
		fmt.Fprintf(a.Out, "WAKE REFUSED %s via %s: %s\n", a.To, a.Waker.Via(), grant.Error())
		return ExitRefused
	case err != nil:
		fmt.Fprintf(a.Out, "WAKE-FAILED %s via %s: %v\n", a.To, a.Waker.Via(), err)
	default:
		fmt.Fprintf(a.Out, "WOKE-ATTEMPT %s via %s\n", a.To, a.Waker.Via())
	}

	got, _, err = a.wait(ctx, m.ID, cursor, a.Clock.Now().Add(a.Deadline))
	if err != nil {
		fmt.Fprintf(a.Out, "REFUSED the store did not answer: %v\n", err)
		return ExitRefused
	}
	if got {
		return a.answered(m.ID, start)
	}
	fmt.Fprintf(a.Out, "NO ANSWER %s after wake\n", a.To)
	return ExitNoAnswer
}

func (a *Ask) answered(id string, start time.Time) int {
	fmt.Fprintf(a.Out, "ANSWERED %s %s after %ds\n", a.To, id, int(a.Clock.Now().Sub(start).Round(time.Second)/time.Second))
	return ExitAnswered
}

// wait reads the log forward from cursor until an answer to id arrives or
// until is reached; every wait is bounded by until.
func (a *Ask) wait(ctx context.Context, id, cursor string, until time.Time) (bool, string, error) {
	for {
		for {
			es, next, more, err := a.Bus.LogForward(ctx, cursor)
			if err != nil {
				return false, cursor, err
			}
			cursor = next
			for _, e := range es {
				if a.isAnswer(e.Message(), id) {
					return true, cursor, nil
				}
			}
			if !more {
				break
			}
		}
		left := until.Sub(a.Clock.Now())
		if left <= 0 {
			return false, cursor, nil
		}
		a.Clock.Sleep(min(PollEvery, left))
	}
}

// isAnswer is a message from the friend to the caller naming id with --re; a
// reply without --re does not count.
func (a *Ask) isAnswer(m bus.Message, id string) bool {
	return m.From == a.To && m.Re == id && (slices.Contains(m.To, a.As) || slices.Contains(m.CC, a.As))
}
