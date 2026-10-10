package config

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// LOOP RUN. `nova-config loop run <name>` is a loop's single-instance wrapper, the
// Go verb in place of the bash nova-loop the coordinator's fleet/loops.yml once
// installed (docs/COORDINATOR-TOOLS.md): it takes <run-dir>/<name>.lock, refuses a
// second copy with exit 3, counts the start and writes the restart metrics
// node_exporter reads, then runs the command and holds the lock until it ends. The
// lock is pkg/filelock's (tla/FileLock.tla): the kernel releases it when its
// holder dies, so a lock whose holder is dead is taken with no stale-pid reading.
// The command is the loop row's argv, or the unit's own command after `--`.

// LoopRunExitHeld is loop run's exit when another copy holds the loop's lock: the
// bash wrapper's 3, so a unit's restart policy reads it the same.
const LoopRunExitHeld = 3

// ErrLoopNotRunnable marks a loop row loop run refuses: disabled, with secrets, or
// with no command.
var ErrLoopNotRunnable = errors.New("loop not runnable")

// LoopLockPath is the loop's lock: <runDir>/<name>.lock.
func LoopLockPath(runDir, name string) string { return filepath.Join(runDir, name+".lock") }

// LoopStartsPath is the loop's start count: <runDir>/<name>.starts, written only
// under the lock.
func LoopStartsPath(runDir, name string) string { return filepath.Join(runDir, name+".starts") }

// LoopMetricsPath is the loop's node_exporter textfile in dir.
func LoopMetricsPath(dir, name string) string {
	return filepath.Join(dir, "nova_loop_"+strings.ReplaceAll(name, "-", "_")+".prom")
}

// LoopRunArgv is the command loop run runs for a loop row, or why it refuses the
// row (ErrLoopNotRunnable). A disabled row is not started, as its unit is not. A
// row with keys opens its secrets through the nova-secrets exec the plays wrap
// around its argv, which the row does not carry: the unit's own command after `--`
// runs it.
func LoopRunArgv(r Row) ([]string, error) {
	switch {
	case r.Fields["enabled"] == "false":
		return nil, fmt.Errorf("%w: loop %s is disabled; run: nova-config loop set %s --enabled true", ErrLoopNotRunnable, r.Name, r.Name)
	case r.Fields["keys"] != "":
		return nil, fmt.Errorf("%w: loop %s opens secrets (%s) through nova-secrets exec, which its row does not carry; run: nova-config loop run %s -- <the unit's command>", ErrLoopNotRunnable, r.Name, r.Fields["keys"], r.Name)
	}
	argv := Argv(r.Fields["argv"])
	if len(argv) == 0 {
		return nil, fmt.Errorf("%w: loop %s has no command; run: nova-config loop show %s", ErrLoopNotRunnable, r.Name, r.Name)
	}
	return argv, nil
}

// NextLoopStarts is the start count after prev, the text of the starts file: one
// more than the number it holds, 1 when it is absent or holds no count.
func NextLoopStarts(prev []byte) int {
	n, err := strconv.Atoi(strings.TrimSpace(string(prev)))
	if err != nil || n < 0 {
		return 1
	}
	return n + 1
}

// LoopMetrics is the node_exporter textfile of one start: the starts counter and
// the time of the last start, each labelled with the loop's name.
func LoopMetrics(name string, starts int, at time.Time) string {
	label := `{loop="` + name + `"}`
	return "# HELP nova_loop_starts_total Starts of the loop by nova-config loop run.\n" +
		"# TYPE nova_loop_starts_total counter\n" +
		"nova_loop_starts_total" + label + " " + strconv.Itoa(starts) + "\n" +
		"# HELP nova_loop_last_start_seconds Unix time of the loop's last start by nova-config loop run.\n" +
		"# TYPE nova_loop_last_start_seconds gauge\n" +
		"nova_loop_last_start_seconds" + label + " " + strconv.FormatInt(at.Unix(), 10) + "\n"
}
