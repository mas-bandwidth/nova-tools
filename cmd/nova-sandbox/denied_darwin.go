//go:build darwin

// The production denial reader: the unified log, asked once, on a run that already failed.
package main

import (
	"context"
	"os/exec"
	"strconv"
	"time"
)

// logPath is where macOS ships the log tool.
const logPath = "/usr/bin/log"

// denialPredicate is the OS's own channel for seatbelt violations: subsystem
// com.apple.sandbox.reporting, category violation. The kernel writes them there attributed
// to itself, with the offending process named in the message text — which is why the
// parser reads `name(pid)` out of the line rather than asking the log for a process.
const denialPredicate = `subsystem == "com.apple.sandbox.reporting" AND category == "violation"`

// denialReadTimeout bounds the one process this costs, and it is SMALL on purpose.
// `log show` opens an on-disk archive before it filters anything: measured on the Studio,
// a query for a three-second window took over ten seconds and found nothing, and a card
// whose test suite fails would have paid that on every run. A diagnostic that slows a
// failing run down more than it explains it is not worth having, so the bound is two
// seconds and going over it is silence, not an error.
const denialReadTimeout = 2 * time.Second

// readOSDenials asks the log what was refused in the last sinceSeconds, and keeps only the
// violations from this run's own process group floor. It answers nil for everything it
// cannot do: no log tool, a query that failed, a query that timed out. This is a
// diagnostic printed after a command has already failed, and a diagnostic may not turn a
// failure into a different failure.
func readOSDenials(sinceSeconds int, pidFloor int) []deniedPath {
	if sinceSeconds <= 0 {
		sinceSeconds = 1
	}
	// The deadline belongs to the CONTEXT, not to a timer racing a goroutine for the Cmd.
	// The earlier shape ran Output in a goroutine and read cmd.Process from this one to
	// kill it, and cmd.Process is written by Start inside that goroutine: a data race, and
	// a `log show` that had not reached Start yet was never killed at all. CommandContext
	// kills the process itself, under the exec package's own lock, which is the only safe
	// reader of a Cmd once it has been started.
	ctx, cancel := context.WithTimeout(context.Background(), denialReadTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, logPath, "show",
		"--last", strconv.Itoa(sinceSeconds)+"s",
		"--style", "syslog",
		"--info", "--debug",
		"--predicate", denialPredicate)
	// Going over the bound is silence, not an error, and so is every other failure: this
	// is a diagnostic printed after a command has already failed.
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	return parseDenials(string(out), pidFloor)
}
