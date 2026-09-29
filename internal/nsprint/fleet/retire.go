package fleet

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// ErrRetireRefused marks a fleet retire refusal.
var ErrRetireRefused = errors.New("fleet retire refused")

func retireRefused(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrRetireRefused, fmt.Sprintf(format, a...))
}

// RetireKey returns the bench's retirement receipt hash key in Redis.
func RetireKey(bench string) string { return "bench:" + bench + ":retire" }

// RetiredUnitsKey returns the bench's retired units set key in Redis.
func RetiredUnitsKey(bench string) string { return "bench:" + bench + ":retired_units" }

// UnitRetireScript generates a cross-platform shell script to stop and remove a nova unit.
func UnitRetireScript(unit string) string {
	clean := strings.TrimSuffix(strings.TrimSuffix(unit, ".plist"), ".service")
	plistName := clean + ".plist"
	serviceName := clean + ".service"
	return fmt.Sprintf(`if [ "$(uname)" = Darwin ]; then `+
		`launchctl bootout gui/$(id -u)/%s 2>/dev/null || launchctl bootout system/%s 2>/dev/null || launchctl unload "$HOME/Library/LaunchAgents/%s" 2>/dev/null || true; `+
		`rm -f "$HOME/Library/LaunchAgents/%s" "/Library/LaunchDaemons/%s"; `+
		`else `+
		`systemctl --user stop %s 2>/dev/null || sudo systemctl stop %s 2>/dev/null || true; `+
		`systemctl --user disable %s 2>/dev/null || sudo systemctl disable %s 2>/dev/null || true; `+
		`rm -f "$HOME/.config/systemd/user/%s" "/etc/systemd/system/%s"; `+
		`systemctl --user daemon-reload 2>/dev/null || true; `+
		`fi`,
		clean, clean, plistName,
		plistName, plistName,
		serviceName, serviceName,
		serviceName, serviceName,
		serviceName, serviceName,
	)
}

// RetireArgv builds the ansible ad-hoc argv to retire a unit on a bench.
func RetireArgv(bench, unit string) []string {
	return []string{
		"ansible", bench, "-i", PlayInventory, "--forks", fmt.Sprint(PlayForks),
		"-m", "ansible.builtin.shell", "-a", UnitRetireScript(unit),
	}
}

// RetireResult is what one unit retirement did.
type RetireResult struct {
	Bench  string
	Unit   string
	Status string // "OK" or "FAILED"
	MS     int64
	Detail string
	Err    string
	DryRun bool
}

// OK reports whether retirement succeeded.
func (r RetireResult) OK() bool {
	return r.Status == "OK" && r.Err == ""
}

// Line returns the standard receipt line for fleet retire.
func (r RetireResult) Line() string {
	ms := strconv.FormatInt(r.MS, 10)
	if !r.OK() {
		errStr := r.Err
		if errStr == "" {
			errStr = "failed"
		}
		line := fmt.Sprintf("FLEET RETIRE bench=%s unit=%s status=%s err=%s ms=%s", r.Bench, r.Unit, r.Status, errStr, ms)
		if r.DryRun {
			line += " check=yes"
		}
		return line
	}
	line := fmt.Sprintf("FLEET RETIRE bench=%s unit=%s status=%s ms=%s", r.Bench, r.Unit, r.Status, ms)
	if r.DryRun {
		line += " check=yes"
	}
	return line
}

// Retire coordinates stopping and removing a stray unit on a bench.
type Retire struct {
	Runner   ExecRunner
	Client   *redis.Client
	Bench    string
	Unit     string
	PlayDir  string
	Registry string
	Benches  []string
	DryRun   bool
	Now      func() time.Time
	Out      io.Writer
}

func (r *Retire) printf(format string, a ...any) {
	if r.Out != nil {
		fmt.Fprintf(r.Out, format, a...)
	}
}

func (r *Retire) check() error {
	if r.Bench == "" {
		return retireRefused("bench is required: fleet retire <bench> <unit>")
	}
	if r.Unit == "" {
		return retireRefused("unit is required: fleet retire <bench> <unit>")
	}
	if r.Runner == nil {
		return retireRefused("fleet retire has no runner")
	}
	if len(r.Benches) > 0 {
		known := false
		for _, b := range r.Benches {
			if b == r.Bench {
				known = true
				break
			}
		}
		if !known {
			return retireRefused("bench %s is not in registry %s", r.Bench, r.Registry)
		}
	}
	if r.PlayDir != "" {
		inv := filepath.Join(r.PlayDir, PlayInventory)
		if _, err := os.Stat(inv); err != nil {
			return retireRefused("play directory %s has no %s", r.PlayDir, PlayInventory)
		}
	}
	if r.Client == nil && !r.DryRun {
		return retireRefused("fleet retire writes receipt to fleet store: --redis <addr>, or --dry-run")
	}
	return nil
}

// Run checks pre-conditions, runs unit retirement through the runner, writes the receipt, and outputs the result line.
func (r *Retire) Run(ctx context.Context) (RetireResult, error) {
	res := RetireResult{
		Bench:  r.Bench,
		Unit:   r.Unit,
		Status: "FAILED",
		DryRun: r.DryRun,
	}
	if err := r.check(); err != nil {
		return res, err
	}

	argv := RetireArgv(r.Bench, r.Unit)
	var env []string
	if r.Registry != "" {
		env = PlayEnv(r.Registry)
	}

	now := time.Now
	if r.Now != nil {
		now = r.Now
	}
	start := now()

	var out string
	var runErr error
	if !r.DryRun {
		pctx, cancel := context.WithTimeout(ctx, PlayTimeout)
		out, runErr = r.Runner.Run(pctx, r.PlayDir, env, argv)
		cancel()
	}

	finish := now()
	res.MS = finish.Sub(start).Milliseconds()
	if res.MS < 0 {
		res.MS = 0
	}

	if r.DryRun {
		res.Status = "OK"
		r.printf("%s\n", res.Line())
		return res, nil
	}

	res.Detail = strings.TrimSpace(out)
	if runErr != nil || strings.Contains(out, "FAILED!") || strings.Contains(out, "UNREACHABLE!") {
		res.Status = "FAILED"
		if runErr != nil {
			res.Err = strings.Join(strings.Fields(runErr.Error()), "_")
		} else {
			res.Err = "command_failed"
		}
	} else {
		res.Status = "OK"
	}

	if r.Client != nil {
		at := strconv.FormatInt(finish.UnixMilli(), 10)
		pipe := r.Client.Pipeline()
		pipe.HSet(ctx, RetireKey(r.Bench),
			"at", at,
			"bench", r.Bench,
			"unit", r.Unit,
			"status", res.Status,
			"result", out,
		)
		if res.OK() {
			pipe.SAdd(ctx, RetiredUnitsKey(r.Bench), r.Unit)
		}
		if _, err := pipe.Exec(ctx); err != nil {
			return res, fmt.Errorf("write retire receipt: %w", err)
		}
	}

	r.printf("%s\n", res.Line())
	return res, nil
}
