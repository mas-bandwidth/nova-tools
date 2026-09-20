package pulse

// Bounded observation of remote hosts (#2009). A DOWN bench must never hang the
// fill loop, the dealer, the sampler or a harvest: every remote call has a wall-
// clock budget, hosts are probed in isolation, and a host that misses its budget
// is MISSED for that tick while the others proceed.
//
// Monitoring ssh never reuses a multiplexed connection. A stale ControlMaster
// socket to a dead host hangs new sessions even with ConnectTimeout; ServerAlive
// kills an established session that goes silent; ConnectTimeout kills a host
// that never accepts.

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ObservationWaitDelay bounds how long Wait may sit on leftover copy pipes
// after the observation child is gone. Zero (Go's default) waits forever for a
// descendant that inherited stdout -- a 1s probe then blocked past its deadline
// (#2009 HOLD on 5914cd01).
const ObservationWaitDelay = 2 * time.Second

// IsolationOptions is the argv every observation remote call carries.
// ControlMaster=no so a stale multiplexed socket to a dead host cannot hang a
// new session even with ConnectTimeout. ServerAlive so an established session
// that goes silent dies. ConnectTimeout so a host that never accepts dies.
// BatchMode so a missing key is a refusal now rather than a password prompt.
var IsolationOptions = []string{
	"-o", "BatchMode=yes",
	"-o", "ConnectTimeout=5",
	"-o", "ControlMaster=no",
	"-o", "ControlPath=none",
	"-o", "ServerAliveInterval=2",
	"-o", "ServerAliveCountMax=2",
}

// IsolationArgv builds one observation remote's arguments. stdin is true when
// the remote script is on the child's stdin (`bash -s`); then there is no -n,
// because -n disconnects stdin. Otherwise -n so a leftover stdin cannot feed
// the remote (the 2026-09-17 harvest edge).
func IsolationArgv(stdin bool, target string, rest ...string) []string {
	n := len(IsolationOptions) + 1 + len(rest)
	if !stdin {
		n++
	}
	args := make([]string, 0, n)
	if !stdin {
		args = append(args, "-n")
	}
	args = append(args, IsolationOptions...)
	args = append(args, target)
	return append(args, rest...)
}

// HostObservation is one host's result in a bounded tick. Missed means the host
// did not answer inside the budget; Value is then the zero value and must not
// be read as a measurement (never timeout=0 capacity).
type HostObservation[T any] struct {
	Host   string
	Value  T
	Err    error
	Missed bool
}

// ObserveHosts runs fn once per host, in parallel, each bounded by ctx. A host
// that has not returned when ctx is done is Missed; a host that returns a value
// is kept even if a neighbour missed. fn must return when ctx is done. The
// returned slice is in hosts order.
func ObserveHosts[T any](ctx context.Context, hosts []string, fn func(context.Context, string) (T, error)) []HostObservation[T] {
	out := make([]HostObservation[T], len(hosts))
	for i, h := range hosts {
		out[i] = HostObservation[T]{Host: h, Missed: true}
	}
	if len(hosts) == 0 {
		return out
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i, h := range hosts {
		wg.Add(1)
		go func(i int, h string) {
			defer wg.Done()
			v, err := fn(ctx, h)
			mu.Lock()
			defer mu.Unlock()
			out[i].Err = err
			if err == nil {
				out[i].Missed = false
				out[i].Value = v
				return
			}
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				out[i].Missed = true
				return
			}
			out[i].Missed = false
		}(i, h)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		<-done
	}
	return out
}
