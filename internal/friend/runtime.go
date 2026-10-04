// Package friend delivers addressed messages to registered native harnesses.
// Redis owns pending deliveries, session routes, failures and durable receipts;
// the runtime keeps no message journal. Libraries considered: go-redis streams
// through friendbus, context and time for bounded waits, and subproc for adapters.
package friend

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/friendbus"
)

// Bus is the addressed stream and its receipts. Accept atomically records the
// receipt and acknowledges the pending entry only at the current session route.
type Bus interface {
	GetSession(context.Context, string) (friendbus.Session, bool, error)
	Register(context.Context, string, friendbus.Session) error
	Claim(context.Context, string, string, int, time.Duration) ([]friendbus.Delivery, error)
	ReclaimPage(context.Context, string, string, time.Duration, int, string) ([]friendbus.Delivery, string, error)
	Accept(context.Context, string, friendbus.Delivery, friendbus.Acceptance) error
	RecordFailure(context.Context, string, friendbus.Delivery, string) error
}

// Adapter restores an actual native session at startup and injects a message
// into it. The native harness durably deduplicates Delivery.Key across process
// restarts and returns the same durable receipt when a pending entry is replayed.
// A process start, successful HTTP response or notification is not this receipt.
type Adapter interface {
	Register(context.Context, string, friendbus.Session) (friendbus.Session, error)
	Wake(context.Context, friendbus.Delivery, friendbus.Session) (friendbus.Acceptance, error)
}

// Runtime is one consumer of a friend's addressed stream, outside the sprint.
// Every native call is bounded; messages remain pending until durable acceptance.
type Runtime struct {
	Bus      Bus
	Name     string
	Consumer string
	Adapter  Adapter
	Lease    time.Duration
	Block    time.Duration
	Retry    time.Duration
	Timeout  time.Duration
	OnError  func(error)
}

func (r Runtime) valid() error {
	if r.Bus == nil || r.Adapter == nil {
		return errors.New("the friend runtime needs its message bus and native harness adapter")
	}
	if strings.TrimSpace(r.Name) == "" {
		return errors.New("the friend runtime needs the addressed friend name")
	}
	if r.Timeout <= 0 {
		return errors.New("the native harness call timeout must be positive")
	}
	return nil
}

// Register restores the previous route through the adapter's real startup
// handshake before storing its replacement. Declaring a session ID alone does
// not establish a wakeable harness. Both durable capabilities are required.
func (r Runtime) Register(ctx context.Context) (friendbus.Session, error) {
	if err := r.valid(); err != nil {
		return friendbus.Session{}, err
	}
	bounded, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()
	previous, _, err := r.Bus.GetSession(bounded, r.Name)
	if err != nil {
		return friendbus.Session{}, fmt.Errorf("read the previous native session: %w", err)
	}
	session, err := r.Adapter.Register(bounded, r.Name, previous)
	if err != nil {
		return friendbus.Session{}, fmt.Errorf("restore the native harness session: %w", err)
	}
	if err := validateSession(session); err != nil {
		return friendbus.Session{}, err
	}
	if err := r.Bus.Register(bounded, r.Name, session); err != nil {
		return friendbus.Session{}, fmt.Errorf("record the restored native session: %w", err)
	}
	return session, nil
}

func validateSession(s friendbus.Session) error {
	if s.ID == "" || s.Adapter == "" || s.Revision == "" {
		return errors.New("the native handshake needs a session ID, adapter and route revision")
	}
	for _, capability := range []string{"durable-delivery-id", "durable-receipt"} {
		if !slices.Contains(s.Capabilities, capability) {
			return fmt.Errorf("the native harness does not confirm %s; deliveries remain pending", capability)
		}
	}
	return nil
}

// Run recovers pending deliveries before blocking for new messages. A failed
// injection is stored for inspection and left pending for reclamation. A store
// failure ends the loop so its supervisor can restart it without losing the PEL.
func (r Runtime) Run(ctx context.Context) error {
	if err := r.valid(); err != nil {
		return err
	}
	if r.Consumer == "" || r.Lease <= 0 || r.Block <= 0 || r.Retry <= 0 {
		return errors.New("the listener needs a consumer and positive lease, block and retry durations")
	}
	session, err := r.Register(ctx)
	if err != nil {
		return err
	}
	cursor := "0-0"
	for ctx.Err() == nil {
		storeCtx, cancel := context.WithTimeout(ctx, r.Timeout)
		pending, next, err := r.Bus.ReclaimPage(storeCtx, r.Name, r.Consumer, r.Lease, 32, cursor)
		cancel()
		if err != nil {
			return fmt.Errorf("recover pending addressed messages: %w", err)
		}
		for _, d := range pending {
			if err := r.deliver(ctx, d, session); err != nil {
				return err
			}
		}
		cursor = next
		// Bound the block by the retry cadence: a failed delivery cannot be
		// stranded behind an indefinitely quiet stream.
		block := min(r.Block, r.Retry)
		if cursor != "" && cursor != "0-0" {
			block = 0 // continue the recovery scan without a quiet-stream wait
		}
		storeCtx, cancel = context.WithTimeout(ctx, block+r.Timeout)
		fresh, err := r.Bus.Claim(storeCtx, r.Name, r.Consumer, 32, block)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("wait for addressed messages: %w", err)
		}
		for _, d := range fresh {
			if err := r.deliver(ctx, d, session); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}

func (r Runtime) deliver(ctx context.Context, d friendbus.Delivery, s friendbus.Session) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	bounded, cancel := context.WithTimeout(ctx, r.Timeout)
	a, err := r.Adapter.Wake(bounded, d, s)
	cancel()
	if err == nil && (!a.Durable || a.ReceiptID == "" || a.Session != s.ID || a.Adapter != s.Adapter || a.Revision != s.Revision) {
		err = errors.New("the native harness returned no durable receipt for this registered session and revision")
	}
	if err == nil {
		// A lost store reply can replay this message; the adapter's stable
		// delivery ID makes that replay return its previous native receipt.
		storeCtx, cancel := context.WithTimeout(ctx, r.Timeout)
		defer cancel()
		if err := r.Bus.Accept(storeCtx, r.Name, d, a); err != nil {
			return fmt.Errorf("record native acceptance of delivery %s: %w", d.Key, err)
		}
		return nil
	}
	reason := fmt.Sprintf("deliver %s to native session %s: %v", d.Key, s.ID, err)
	storeCtx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()
	if failErr := r.Bus.RecordFailure(storeCtx, r.Name, d, reason); failErr != nil {
		return errors.Join(err, fmt.Errorf("record the native delivery failure: %w", failErr))
	}
	if r.OnError != nil {
		r.OnError(errors.New(reason))
	}
	// The failure is inspectable in Redis even without an output callback;
	// leaving it pending permits a later retry at the same delivery key.
	return nil
}
