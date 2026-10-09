package friend

import (
	"context"
	"time"
)

// A lane's run is its own process group (RealExec, Setsid), so a daemon that is killed,
// crashes or is restarted leaves its runs working on. Until 2026-10-08 the daemon that
// started up finished every card still marked started FAILED at once ("the run is gone",
// lane_end.go endStarted), over a run that was still working, and the stopgap runners had
// an `adopt` for exactly this. Now each card turn's process id is recorded on its started
// mark (Started.Pid, through the context the exec answers, WithProcessStarted), and a daemon
// starting up adopts a run whose process is alive (Daemon.ProcessAlive): the lane holds the
// card and waits on the process, polling every AdoptPoll on the daemon's clock, and the
// card ends as every run's does when it exits; only a run that is gone is ended as before
// (internal/friend/tla/LaneEnd.tla, Adopts, AdoptedNotFailed; docs/SPEC-FRIEND.md, "a
// lane's end is a finish").

// processKey carries, in a delivery's context, what to call with the process id once the
// command has started (WithProcessStarted); the exec calls it before it waits.
type processKey struct{}

// WithProcessStarted marks ctx so the exec hands the started process's id to started.
func WithProcessStarted(ctx context.Context, started func(pid int)) context.Context {
	return context.WithValue(ctx, processKey{}, started)
}

// processStarted is the sink in ctx, nil when none.
func processStarted(ctx context.Context) func(pid int) {
	f, _ := ctx.Value(processKey{}).(func(pid int))
	return f
}

// AdoptPoll is how often an adopted run's process is looked at while the lane waits on it.
const AdoptPoll = 5 * time.Second

// waitAdopted waits on an adopted run: until its process is no longer alive, or ctx ends.
// It answers whether the process ended (false: the daemon stopped first).
func waitAdopted(ctx context.Context, pid int, alive func(int) bool, pause func(context.Context, time.Duration)) bool {
	for ctx.Err() == nil {
		if !alive(pid) {
			return true
		}
		if pause != nil {
			pause(ctx, AdoptPoll)
		} else {
			select {
			case <-ctx.Done():
				return false
			case <-time.After(AdoptPoll):
			}
		}
	}
	return false
}
