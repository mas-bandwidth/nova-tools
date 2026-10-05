package friend

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
)

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

// DeliveryCells is a friend's two cells of the friends table from what she
// is owed receipts for: undelivered, the count, and oldest, the oldest
// undelivered message's age at now (Age), "-" when nothing is owed.
func DeliveryCells(bl bus.Backlog, now time.Time) (undelivered, oldest string) {
	if bl.Count == 0 {
		return "0", "-"
	}
	return strconv.Itoa(bl.Count), Age(bl.Age(now))
}

// Age is a wait as the table says it: seconds under a minute, minutes under
// an hour, hours and minutes under two days, else days.
func Age(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", max(0, int(d/time.Second)))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d/time.Hour), int(d%time.Hour/time.Minute))
	}
	return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
}

// ReceiptLine is the line a turn asks the session to run once it has read
// the messages of text (each RECV OK line's id, as Text prints it): the
// session's receipt, nova-bus ack by id as the friend (bin is nova-bus by
// path). "" when text carries no message.
func ReceiptLine(bin, friend, text string) string {
	var ids []string
	for _, line := range strings.Split(text, "\n") {
		rest, ok := strings.CutPrefix(line, "RECV OK id=")
		if !ok {
			continue
		}
		if id, _, ok := strings.Cut(rest, " "); ok && id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return ""
	}
	return fmt.Sprintf("When you have read the message(s) above, say so, exactly as written: %s ack --as %s --id %s", bin, friend, strings.Join(ids, ","))
}
