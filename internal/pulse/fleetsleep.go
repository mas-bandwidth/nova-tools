package pulse

// `fleet sleep` is the retired fleet-sleep.sh as a verb: put one bench to sleep, and refuse
// while it is working.
//
// It is deliberately NOT a second implementation of that decision. `fleet suspend` already
// holds the one busy rule the fleet has -- a lease or a job directory with a live pid under
// either swarm root, or a Runner.Worker process, is BUSY and is never suspended (SPEC-PULSE
// ## Fleet) -- and the shell script had a weaker one of its own: it asked GitHub whether the
// bench's runners were busy and knew nothing about the cards in flight on it, so a bench
// running a card through nova-swarm and no workflow looked idle and went to sleep under its
// own work. So `fleet sleep` is the retiring NAME, over one bench, and the machinery is
// `fleet suspend`'s: one place decides busy, and there is nothing to keep in step.

import (
	"io"
	"time"
)

// FleetSleepInput is everything `fleet sleep` needs apart from flag parsing.
type FleetSleepInput struct {
	Benches  string // the fleet file
	Machines string // the machines registry; a machine whose roles lack `bench` is refused
	Name     string // the one bench to put to sleep
	SSH      string // the ssh program; empty is "ssh"
	Force    bool   // sleep even a busy bench
	IfIdle   bool   // skip a busy bench instead of refusing it
	Timeout  time.Duration
	Max      int
	Stdout   io.Writer
	Stderr   io.Writer
}

// FleetSleep suspends one idle bench over ssh and prints one line:
// `FLEET <bench> SUSPENDED`, `FLEET <bench> BUSY <what>` (exit 2, and nothing is
// suspended), `FLEET <bench> SKIP <what>` under --if-idle, or
// `FLEET <bench> UNREACHABLE <reason>` (exit 3).
func FleetSleep(in FleetSleepInput) int {
	return FleetSuspend(FleetSuspendInput{
		Benches:  in.Benches,
		Machines: in.Machines,
		Names:    fleetSleepNames(in.Name),
		SSH:      in.SSH,
		Force:    in.Force,
		IfIdle:   in.IfIdle,
		Timeout:  in.Timeout,
		Max:      in.Max,
		Stdout:   in.Stdout,
		Stderr:   in.Stderr,
	})
}

// fleetSleepNames is the one name as the list FleetSuspend takes; an empty name stays empty
// so the missing-flag refusal is the one FleetSuspend already prints.
func fleetSleepNames(name string) []string {
	if name == "" {
		return nil
	}
	return []string{name}
}
