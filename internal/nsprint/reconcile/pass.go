package reconcile

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// DefaultInterval is the pass cadence (#2726: a 1 s pass loop).
const DefaultInterval = time.Second

// Counts is what one duty did in one pass; the pass sums them into
// proc:reconciler (spec 5.1.2: dealt, routed, expired; #2930 rev 5: retried
// and ambiguous from the expire duty). Each counts a distinct transition.
type Counts struct {
	Dealt     int
	Routed    int
	Expired   int
	Retried   int // ended cards fed back once (ns_card_retry)
	Ambiguous int // pending idem keys flipped past open_ms (ns_idem_ambiguous)
	// The route duty's moves (#3323), each also counted in Routed.
	Reads   int // read tasks pushed to a reader's queue
	Fixes   int // fix tasks pushed to an author's queue
	Merging int // tasks moved to merging
	Carried int // read tasks carried to a new head with an identical diff (#3580)
	// No ghost cards (#3925): leases the expire duty reaped (ns_lease_reap)
	// and drift the fsck duty repaired plus closed-sprint cards it retired.
	Reaped   int
	Repaired int
}

func (c *Counts) add(o Counts) {
	c.Dealt += o.Dealt
	c.Routed += o.Routed
	c.Expired += o.Expired
	c.Retried += o.Retried
	c.Ambiguous += o.Ambiguous
	c.Reads += o.Reads
	c.Fixes += o.Fixes
	c.Merging += o.Merging
	c.Carried += o.Carried
	c.Reaped += o.Reaped
	c.Repaired += o.Repaired
}

// Zero is true when the pass moved nothing.
func (c Counts) Zero() bool { return c == Counts{} }

// Line is the counts as receipt words, in a fixed order.
func (c Counts) Line() string {
	return fmt.Sprintf("dealt=%d routed=%d expired=%d retried=%d ambiguous=%d reads=%d fixes=%d merging=%d carried=%d reaped=%d repaired=%d",
		c.Dealt, c.Routed, c.Expired, c.Retried, c.Ambiguous, c.Reads, c.Fixes, c.Merging, c.Carried, c.Reaped, c.Repaired)
}

// Duty is one reconciler duty (spec 5.2): deal (#2743), refill (#2935),
// expiry, routing. It runs only after the pass has renewed the lease, and it
// passes l.Token() into every Redis Function it calls so a write after the
// lease is lost refuses FENCED. A duty that sees ErrFenced returns it; the
// pass then stops and the loop exits.
type Duty func(ctx context.Context, l *Lease) (Counts, error)

// PassResult is one recorded pass.
type PassResult struct {
	PassAt int64 // Redis TIME ms, as stored in proc:reconciler pass_at
	Took   time.Duration
	Counts Counts
	Err    string // duty errors, recorded in proc:reconciler err
}

// Loop runs passes under one lease.
type Loop struct {
	Lease  *Lease
	Duties []Duty
	// Names are the duties' names in Duties order, for the duties a pass
	// did not start (#3805); a missing name is "duty <i>".
	Names []string
	// Margin is the lease time a duty must have left to start
	// (Lease.WriteMargin when zero, #3805).
	Margin   time.Duration
	Interval time.Duration // DefaultInterval when zero; must be below the lease TTL
	Passes   int           // stop after this many recorded passes; 0 runs until ctx ends
	// AfterPass, when set, is called after every recorded pass.
	AfterPass func(PassResult)
	// OnError, when set, is called with a pass error that is not a fence
	// (Redis unreachable); the loop retries on the next tick.
	OnError func(error)
}

// Pass is one reconciler pass: renew the lease (a stale instance stops here
// and no duty runs), run every duty with the token, then write the pass age
// and counts to proc:reconciler in one fenced call that also renews the lease.
// A duty error that is not a fence is recorded and does not stop the loop. A
// lease fenced while a duty ran (the heartbeat, #3737) stops the pass after
// that duty. No duty starts with less than Margin of the lease left (#3805):
// below it the pass renews first (as the deal pass does, #3706), and if the
// renewal did not land it records the duties it did not start in err
// (LEASE-MARGIN) and writes its record inside the lease.
//
// The pass is timed on the lease clock (#3322), the clock its bench sessions
// are bounded by, so took_ms and the session bound are one measurement: a
// pass that recorded itself took under the lease TTL on the clock the lease
// deadline is kept on.
func (lp *Loop) Pass(ctx context.Context) (PassResult, error) {
	start := lp.Lease.now()
	if err := lp.Lease.Renew(ctx); err != nil {
		return PassResult{}, err
	}
	var res PassResult
	var errs []string
	for i, duty := range lp.Duties {
		if lp.Lease.Remaining() < lp.Lease.WriteMargin(lp.Margin) {
			if err := lp.Lease.Renew(ctx); errors.Is(err, ErrFenced) {
				return PassResult{}, err
			}
		}
		if err := lp.Lease.Bounded(lp.Margin); errors.Is(err, ErrFenced) {
			return PassResult{}, err
		} else if err != nil {
			left := make([]string, 0, len(lp.Duties)-i)
			for j := i; j < len(lp.Duties); j++ {
				left = append(left, lp.name(j))
			}
			errs = append(errs, fmt.Sprintf("duties not started %s: %v", strings.Join(left, ","), err))
			break
		}
		c, err := duty(ctx, lp.Lease)
		if errors.Is(err, ErrFenced) {
			// A duty's own fenced write was refused: the lease is lost for
			// every other holder of it too (a harvest worker stops, #3737).
			lp.Lease.Fence(err)
			return PassResult{}, err
		}
		if err := lp.Lease.fencedErr(); err != nil {
			// The heartbeat lost the lease while the duty ran.
			return PassResult{}, err
		}
		if err != nil {
			errs = append(errs, fmt.Sprintf("duty %d: %v", i, err))
		}
		res.Counts.add(c)
	}
	res.Took = lp.Lease.now().Sub(start)
	res.Err = strings.Join(errs, "; ")
	sent := lp.Lease.now()
	reply, err := lp.Lease.call(ctx, fnPass, lp.Lease.token, lp.Lease.ttl.Milliseconds(),
		res.Took.Milliseconds(), res.Counts.Dealt, res.Counts.Routed, res.Counts.Expired, res.Err,
		res.Counts.Retried, res.Counts.Ambiguous)
	if err != nil {
		return PassResult{}, err
	}
	// ns_reconciler_pass renewed the lease to its TTL.
	lp.Lease.renewedAt(sent)
	if len(reply) < 2 {
		return PassResult{}, fmt.Errorf("reconcile pass: unexpected reply %q", reply)
	}
	if res.PassAt, err = strconv.ParseInt(reply[1], 10, 64); err != nil {
		return PassResult{}, fmt.Errorf("reconcile pass: pass_at %q: %w", reply[1], err)
	}
	return res, nil
}

func (lp *Loop) name(i int) string {
	if i < len(lp.Names) && lp.Names[i] != "" {
		return lp.Names[i]
	}
	return fmt.Sprintf("duty %d", i)
}

// Run passes every Interval until ctx ends (nil), the lease is fenced
// (ErrFenced: the caller must exit without releasing; the heartbeat's fence
// ends Run between passes too), or Passes are done. A
// Redis error in one pass is retried on the next tick: within the TTL the
// lease renews; past it the next call is fenced and Run returns. The caller
// releases the lease after a nil return.
func (lp *Loop) Run(ctx context.Context) error {
	if lp.Lease == nil {
		return fmt.Errorf("reconcile run: no lease")
	}
	interval := lp.Interval
	if interval == 0 {
		interval = DefaultInterval
	}
	if interval <= 0 || interval >= lp.Lease.ttl {
		return fmt.Errorf("reconcile run: interval %s must be positive and below the lease TTL %s", interval, lp.Lease.ttl)
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	done := 0
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-lp.Lease.Done():
			// The heartbeat lost the lease between passes (#3737).
			return lp.Lease.fencedErr()
		case <-timer.C:
		}
		started := time.Now()
		res, err := lp.Pass(ctx)
		switch {
		case errors.Is(err, ErrFenced):
			return err
		case ctx.Err() != nil:
			return nil
		case err != nil:
			if lp.OnError != nil {
				lp.OnError(err)
			}
		default:
			done++
			if lp.AfterPass != nil {
				lp.AfterPass(res)
			}
			if lp.Passes > 0 && done >= lp.Passes {
				return nil
			}
		}
		timer.Reset(max(interval-time.Since(started), 0))
	}
}
