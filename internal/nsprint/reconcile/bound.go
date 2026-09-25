package reconcile

// Bench sessions bounded by the lease (nova-tools #3322).
//
// THE HURT (2026-09-23 live smoke, outcome 3). A bench whose sshd accepts TCP
// and never sends a banner held its deal session for the ssh connect timeout
// (5 s) plus the reserve round trips. The deal pass waits for every bench
// session before it writes any row, and the pass renews lease:reconciler only
// at its start, so the pass ran past the 6 s TTL: the row write was refused
// FENCED, the reconciler exited 3, neither bench's ssh row was written, and
// 200 reservations were left in `starting` on benches that ran nothing.
//
// THE RULE. Every bench session a pass opens is bounded by the lease: its
// budget is the lease time left (Lease.Remaining, on the lease clock) less a
// write margin kept for the pass's fenced writes (the ssh rows, the undeal of
// a returned batch, the pass record). A session still waiting for its sshd at
// the budget is cut. A session that never reached the remote command (the
// banner or key exchange did not complete) is `timeout` with a why that leads
// WEDGED, so the deal pass writes the row and returns the bench's
// reservations to the pool well inside the lease, and the next pass runs. A
// session cut after it may have reached the remote command is `error`: its
// batch stays dealt for the start-ack rule (#2756 3.2), and its row is still
// written in time.

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/deal"
)

// DefaultWriteMargin is the lease time a pass keeps back from its bench
// sessions for its own fenced writes (rows, undeals, the pass record): each
// is one Redis Function call, milliseconds on the fleet Redis.
const DefaultWriteMargin = time.Second

// WedgedPrefix leads the why of a bench whose session was bounded by the lease
// before its sshd completed the banner or key exchange.
const WedgedPrefix = "WEDGED"

// LeaseBound wraps a deal.Dialer so every session it opens ends inside the
// lease's deadline less Margin, timed on the lease's clock.
type LeaseBound struct {
	Dialer deal.Dialer
	Lease  *Lease
	Margin time.Duration // DefaultWriteMargin when zero
}

// Dial implements deal.Dialer.
func (d LeaseBound) Dial(b deal.Bench) deal.Session {
	return &boundSession{d: d, bench: b}
}

type boundSession struct {
	d     LeaseBound
	bench deal.Bench
}

func (d LeaseBound) margin() time.Duration {
	if d.Margin > 0 {
		return d.Margin
	}
	return DefaultWriteMargin
}

// Run runs the bench's one session inside the lease budget.
func (s *boundSession) Run(ctx context.Context, stdin []byte) error {
	budget := s.d.Lease.Remaining() - s.d.margin()
	inner := s.clamp(budget)
	if inner == nil {
		// Not enough lease left to open a session: nothing is sent, so
		// nothing ran, and the batch goes back to the pool this pass.
		return s.wedged(fmt.Sprintf("no session opened: %s of lease left after the %s write margin", budget.Round(time.Millisecond), s.d.margin()))
	}
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	timer := s.d.Lease.Clock().After(budget)
	var cut atomic.Bool
	stop := make(chan struct{})
	watched := make(chan struct{})
	go func() {
		defer close(watched)
		select {
		case <-timer:
			cut.Store(true)
			cancel()
		case <-stop:
		}
	}()
	err := inner.Run(cctx, stdin)
	close(stop)
	<-watched
	if err == nil {
		return nil
	}
	var se *deal.SessionError
	preExec := errors.As(err, &se) && (se.State == deal.SSHTimeout || se.State == deal.SSHRefused)
	switch {
	case preExec && se.State == deal.SSHTimeout:
		// The sshd accepted and never finished the banner or key exchange,
		// or the ssh child was killed at the budget with no start line back
		// from the remote verb (ssh.go, #3322).
		why := fmt.Sprintf("no start line inside the lease budget %s (lease deadline less %s): %s",
			budget.Round(time.Millisecond), s.d.margin(), firstLine(se.Stderr))
		out := s.wedged(why)
		out.Exit = se.Exit
		return out
	case preExec:
		return err
	case cut.Load():
		return &deal.SessionError{Bench: s.bench.Name, State: deal.SSHError, Exit: -1,
			Stderr: fmt.Sprintf("cut at the lease budget %s after the session may have reached the remote command: %v",
				budget.Round(time.Millisecond), err)}
	}
	return err
}

// clamp opens the inner session, or returns nil when the budget is too short
// to open one. The production ssh's own connect timeout is fitted inside the
// budget, so the ssh client itself reports a banner that never came ("timed
// out during banner exchange", pre-exec) before the budget cuts the child;
// the client's timeout is whole seconds, so it needs at least one. Any other
// dialer must end its session when its context does.
func (s *boundSession) clamp(budget time.Duration) deal.Session {
	if budget <= 0 {
		return nil
	}
	var r deal.Remote
	switch d := s.d.Dialer.(type) {
	case deal.Remote:
		r = d
	case *deal.Remote:
		r = *d
	default:
		return s.d.Dialer.Dial(s.bench)
	}
	connect := r.ConnectTimeout
	if connect <= 0 {
		connect = deal.DefaultConnectTimeout
	}
	// Leave the client a second past its connect timeout to report it.
	if fit := (budget - time.Second).Truncate(time.Second); fit < connect {
		connect = fit
	}
	if connect < time.Second {
		return nil
	}
	r.ConnectTimeout = connect
	return r.Dial(s.bench)
}

func (s *boundSession) wedged(why string) *deal.SessionError {
	return &deal.SessionError{Bench: s.bench.Name, State: deal.SSHTimeout, Exit: 255, Stderr: WedgedPrefix + ": " + why}
}

func firstLine(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	return s
}

// Every duty bounded by the lease (nova-tools #3805).
//
// #3737 bounded the harvest and #3322 the deal sessions; dev-red, expire,
// route and ok-to-friend were protected only by the heartbeat, so a slow
// forge read or a long run of moves could carry a pass past the lease
// deadline. The rule is the harvest's: before each unit of new work (a duty
// in the pass, a base in dev-red, a sprint or move in route, a sprint in
// ok-to-friend) the duty asks Bounded; below the write margin it starts
// nothing more and returns an error wrapping ErrLeaseMargin that names what
// it left, which the pass records in proc:reconciler err with the counts of
// what it did. A single slow call is cut at the budget on the lease clock
// (Budget), as a bench session is.

// ErrLeaseMargin is less than the write margin of the reconciler lease left:
// the duty starts no new work this pass.
var ErrLeaseMargin = errors.New("LEASE-MARGIN")

// Bounded is the lease bound before a unit of new work: nil to start it,
// the fence when the lease is lost (wrapping ErrFenced), else an error
// wrapping ErrLeaseMargin when less than margin (WriteMargin when zero) is
// left on the lease clock. It never renews: the heartbeat does, so under a
// held lease it refuses only when renewals are not landing. A nil lease (a
// verb run by hand, with no reconciler lease) is never bounded.
func (l *Lease) Bounded(margin time.Duration) error {
	if l == nil {
		return nil
	}
	if err := l.fencedErr(); err != nil {
		return err
	}
	margin = l.WriteMargin(margin)
	if left := l.Remaining(); left < margin {
		return fmt.Errorf("%w: %s of the lease left, below the %s write margin", ErrLeaseMargin, left.Round(time.Millisecond), margin)
	}
	return nil
}

// Budget is ctx cut when the lease time left less margin runs out, timed on
// the lease clock as a bench session is (LeaseBound): one slow call (a forge
// read) then ends inside the lease. It refuses as Bounded does, before the
// call starts. The cancel must be called. A nil lease returns ctx uncut.
func (l *Lease) Budget(ctx context.Context, margin time.Duration) (context.Context, context.CancelFunc, error) {
	if l == nil {
		return ctx, func() {}, nil
	}
	if err := l.Bounded(margin); err != nil {
		return ctx, func() {}, err
	}
	margin = l.WriteMargin(margin)
	cctx, cancel := context.WithCancel(ctx)
	timer := l.Clock().After(l.Remaining() - margin)
	go func() {
		select {
		case <-timer:
			cancel()
		case <-cctx.Done():
		}
	}()
	return cctx, cancel, nil
}

// WriteMargin is margin, or when it is zero the default write margin for
// this lease: DefaultWriteMargin, capped at TTL/6 so a short test TTL keeps
// room to work (1 s at the 6 s production TTL).
func (l *Lease) WriteMargin(margin time.Duration) time.Duration {
	if margin > 0 {
		return margin
	}
	return min(DefaultWriteMargin, l.ttl/6)
}
