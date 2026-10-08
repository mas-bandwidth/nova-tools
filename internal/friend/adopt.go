package friend

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
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
func WithProcessStarted(ctx context.Context, started func(pid int) error) context.Context {
	return context.WithValue(ctx, processKey{}, started)
}

// processStarted is the sink in ctx, nil when none.
func processStarted(ctx context.Context) func(pid int) error {
	f, _ := ctx.Value(processKey{}).(func(pid int) error)
	return f
}

// runReceipt is written before the launch gate lets the child execute the
// harness. The run ID binds it to one Started record even if a job is reused.
type runReceipt struct {
	RunID    string `json:"run_id"`
	PID      int    `json:"pid"`
	Identity string `json:"identity"`
}

func runReceiptPath(dir, job string) string { return filepath.Join(dir, "jobs", job, "RUN") }

func writeRunReceipt(dir, job string, r runReceipt) error {
	if r.RunID == "" || r.PID <= 0 || r.Identity == "" {
		return errors.New("incomplete run identity")
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(runReceiptPath(dir, job), append(raw, '\n'), 0o644)
}

func readRunReceipt(dir, job string) (runReceipt, error) {
	raw, err := os.ReadFile(runReceiptPath(dir, job))
	if err != nil {
		return runReceipt{}, err
	}
	var r runReceipt
	err = json.Unmarshal(raw, &r)
	return r, err
}

// runStillAlive rejects PID reuse: a different live leader is never evidence
// for the old run. Only an absent leader leaves open the possibility that
// members of its original group are still working.
func runStillAlive(pid int, identity string) bool {
	if identity == "" {
		return false
	}
	current := ProcessIdentity(pid)
	return current == identity || current == "" && ProcessGroupAlive(pid)
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
		pause(ctx, AdoptPoll)
	}
	return false
}
