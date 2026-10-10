package friend

import (
	"context"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
)

// outputKey carries, in a delivery's context, what to call when the command
// prints: the daemon's watch on a running turn (WithOutputSeen).
type outputKey struct{}

// Printed records that a turn produced output during testing, so a test
// harness can distinguish a turn that is printing from one that is stuck.
func Printed(ctx context.Context, data []byte) {
	select {
	case <-ctx.Done():
	default:
		// trigger the daemon's output tracking by calling the seen callback
		if seen, ok := ctx.Value(outputKey{}).(func()); ok && seen != nil {
			seen()
		}
	}
}

// Courier is how the sprint server sends its notes to friends: each send on
// the bus, its result watched, so a store that refuses the login or cannot
// be reached is one alarm to the coordinator, raised at the first failure
// and cleared at the next success, and never only a log line (the finding of
// 2026-10-04: 54 failures in 30 minutes, WRONGPASS for the server's bus
// user, and nothing said so; docs/SPEC-FRIEND.md, fr-delivery-receipts.w1).
// Every note is owed its friend's receipt like any message to her
// (docs/SPEC-BUS.md, fr-delivery-receipts.w1).
type Courier struct {
	// Bus is the bus every send goes on; Open, when set, dials one for each
	// send instead (the server's way: one connection per note) and its
	// failure is watched like a send's.
	Bus   *bus.Bus
	Open  func(ctx context.Context) (*bus.Bus, func(), error)
	Watch *bus.Watch
	Now   func() time.Time
}

// Send sends m and hands its result to the Watch.
func (c *Courier) Send(ctx context.Context, m bus.Message) (bus.Message, error) {
	sent, err := c.send(ctx, m)
	if c.Watch != nil {
		now := time.Now
		if c.Now != nil {
			now = c.Now
		}
		c.Watch.Observe(now(), err)
	}
	return sent, err
}

func (c *Courier) send(ctx context.Context, m bus.Message) (bus.Message, error) {
	b := c.Bus
	if c.Open != nil {
		opened, closeBus, err := c.Open(ctx)
		if err != nil {
			return bus.Message{}, err
		}
		if closeBus != nil {
			defer closeBus()
		}
		b = opened
	}
	return b.Send(ctx, m)
}
