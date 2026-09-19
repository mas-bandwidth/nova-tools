package swarm

import "time"

// THE PER-BENCH STAGGER (nova-tools#1785). Nothing today paces two launches to the SAME
// bench: the fill's round-robin calls its launcher for every ready card a bench has
// capacity for with no gap between two calls naming the same bench, and `nova-swarm
// batch`'s per-card loop is gated only PER ROUTE (the provider/model/key), never per
// bench. A route and a bench are different axes: two benches can share one key (the
// free/contributor tier) while each opens its OWN ssh session, and the per-route cap does
// not bound how many of those sessions open in the same second.
//
// Measured 2026-09-19: twelve simultaneous launches tripped sshd `MaxStartups`; 119 cards
// started within one minute against a single provider key. Staggering is always important.
//
// A BenchStagger enforces a minimum gap between two `Wait` calls that name the same bench.
// It is a plain value with an injectable clock and sleep, no goroutine and no channel, so
// a test drives it deterministically and a caller whose gap is zero gets a no-op -- which
// is `--stagger 0`'s green case, and the behaviour every caller that did not ask for a gap
// has today.
type BenchStagger struct {
	gap   time.Duration
	now   func() time.Time
	sleep func(time.Duration)
	last  map[string]time.Time // bench -> the moment its last Wait ended (or began)
}

// NewBenchStagger makes a stagger of `gap` between two launches naming one bench. now is
// the clock and sleep the pause; a nil now is time.Now and a nil sleep is time.Sleep. gap
// <= 0 makes every Wait a no-op.
func NewBenchStagger(gap time.Duration, now func() time.Time, sleep func(time.Duration)) *BenchStagger {
	if now == nil {
		now = time.Now
	}
	if sleep == nil {
		sleep = time.Sleep
	}
	return &BenchStagger{gap: gap, now: now, sleep: sleep, last: map[string]time.Time{}}
}

// Wait blocks -- via sleep -- only the remainder of the gap since the last Wait call that
// named this bench, then records this call as the new last time. Two Wait calls naming the
// same bench are therefore never closer than gap, and two naming different benches never
// block each other at all.
func (s *BenchStagger) Wait(bench string) {
	if s.gap <= 0 {
		return
	}
	now := s.now()
	if prev, ok := s.last[bench]; ok {
		if remain := s.gap - now.Sub(prev); remain > 0 {
			s.sleep(remain)
			now = s.now()
		}
	}
	s.last[bench] = now
}

// NativeStartJitter is the uniform-random 0-2s pause `nova-swarm native` takes immediately
// before it starts the child (nova-tools#1785): the answer to a wave of launches landing on
// one bench in one instant and tripping sshd MaxStartups. It reuses randBelow, the same
// cryptographic source ProviderRetryDelay already trusts, so cmd/nova-swarm needs no random
// import of its own.
func NativeStartJitter() time.Duration {
	return time.Duration(randBelow(int(2 * time.Second)))
}
