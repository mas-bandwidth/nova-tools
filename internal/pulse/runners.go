package pulse

// Rule E2: A RUNNER BUSY WITH NOTHING RUNNING IS A STUCK RUNNER.
//
// 2026-09-16 17:35Z (issue #828, class E, second rule): all sixteen self-hosted runners
// showed busy on GitHub with zero runs in progress on the repo for twenty minutes -- the
// jobs of cancelled six-minute shards never finished cancelling -- and fourteen merge-group
// runs and nine PR runs sat queued behind them. Nothing merged for forty minutes, and a
// person freed it by hand, one `systemctl restart` per runner on Space and one
// `svc.sh stop; svc.sh start` per runner on the Studio.
//
// The rule that retires it: the reap step reads the runner list each tick; a runner busy
// with no in-progress run on the repo for more than RunnerIdle is restarted through its
// service and counted on the REAP line. "For more than" is the whole rule, so the busy
// runners are recorded with the moment they were first seen busy-with-nothing-running
// (<queue>/runners.busy.tsv) and a runner that is busy on a live run clears its own row: a
// reaper that restarted on the first sight of a busy runner would kill live jobs, which is
// worse than the jam.
//
// Both halves are interfaces. The real table is two bounded gh calls and the real restarter
// is one ssh per runner, so a test drives fakes and no test of this package restarts
// anything or reaches the network.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// DefaultRunnerIdle is how long a runner may be busy with nothing running before it is
// stuck. Five minutes: longer than the longest gap between a job finishing and the next
// starting, far shorter than the forty minutes the jam cost.
const DefaultRunnerIdle = 5 * time.Minute

// RunnerBusyFile is where the moment each runner was first seen busy-with-nothing-running
// is kept, so the rule survives a tick and a restart of the loop.
const RunnerBusyFile = "runners.busy.tsv"

// RunnerServicesFile maps a runner name to the service that restarts it:
// name<TAB>host<TAB>kind<TAB>target, kind being systemd (target is the unit), svc (target is
// the runner directory holding svc.sh) or runsh (target is the runner directory holding
// run.sh, supervised by a loop). A host of "-" is this machine. `nova-pulse fleet add`
// writes these rows, so a runner it stands up is one the reaper can restart.
const RunnerServicesFile = "runner-services.tsv"

// Runner is one self-hosted runner as the runners API reports it.
type Runner struct {
	Name   string `json:"name"`
	Status string `json:"status"` // online, offline
	Busy   bool   `json:"busy"`
}

// RunnerTable is the two facts the rule needs: which runners are busy, and how many runs
// are actually in progress on the repo they serve.
type RunnerTable interface {
	Runners(repo string) ([]Runner, error)
	InProgress(repo string) (int, error)
}

// RunnerRestarter restarts one runner through its own service.
type RunnerRestarter interface {
	Restart(name string) error
}

// reapStuckRunners is rule E2, one pass. It returns how many runners it restarted --
// under --dry-run, how many it would have restarted, which is the same count, so the
// reaper can be read before it is trusted.
func reapStuckRunners(in ReapInput, now time.Time) int {
	if in.Runners == nil || strings.TrimSpace(in.Repo) == "" {
		return 0
	}
	idle := in.RunnerIdle
	if idle <= 0 {
		idle = DefaultRunnerIdle
	}
	runners, err := in.Runners.Runners(in.Repo)
	if err != nil {
		fmt.Fprintf(in.Stderr, "REAP NOTE the runner list could not be read: %s\n", oneline.Err(err))
		return 0
	}
	running, err := in.Runners.InProgress(in.Repo)
	if err != nil {
		fmt.Fprintf(in.Stderr, "REAP NOTE the in-progress runs could not be read: %s\n", oneline.Err(err))
		return 0
	}

	since := readBusySince(in.Queue)
	restarted := 0
	seen := map[string]bool{}
	for _, r := range runners {
		// A runner that is not busy, and every runner while a run is in progress on the
		// repo, is working or idle: no row, nothing to restart.
		if !r.Busy || running > 0 {
			continue
		}
		seen[r.Name] = true
		first, ok := since[r.Name]
		if !ok {
			since[r.Name] = now // first sight: the clock starts, nothing is restarted
			continue
		}
		if now.Sub(first) <= idle {
			continue
		}
		restarted++
		delete(since, r.Name)
		if in.DryRun {
			continue
		}
		if in.Restarter == nil {
			fmt.Fprintf(in.Stderr, "REAP NOTE runner=%s is stuck and no restarter is wired (pass --repo and the %s map)\n",
				oneline.Field(r.Name), RunnerServicesFile)
			continue
		}
		if err := in.Restarter.Restart(r.Name); err != nil {
			fmt.Fprintf(in.Stderr, "REAP NOTE runner=%s could not be restarted: %s\n", oneline.Field(r.Name), oneline.Err(err))
		}
	}
	// A runner nobody saw busy this pass is a runner that recovered: its row goes, so a
	// runner that jams again is given its own five minutes and not the last jam's.
	for name := range since {
		if !seen[name] {
			delete(since, name)
		}
	}
	if !in.DryRun {
		writeBusySince(in.Queue, since)
	}
	return restarted
}

// readBusySince reads <queue>/runners.busy.tsv. A file nobody wrote is nobody busy.
func readBusySince(queue string) map[string]time.Time {
	out := map[string]time.Time{}
	for _, l := range readLines(filepath.Join(queue, RunnerBusyFile)) {
		name, stamp, ok := strings.Cut(strings.TrimSpace(l), "\t")
		if !ok || name == "" {
			continue
		}
		at, err := time.Parse(time.RFC3339, strings.TrimSpace(stamp))
		if err != nil {
			continue
		}
		out[name] = at
	}
	return out
}

// writeBusySince writes the record whole, in a stable order, by rename.
func writeBusySince(queue string, since map[string]time.Time) {
	names := make([]string, 0, len(since))
	for n := range since {
		names = append(names, n)
	}
	slices.Sort(names)
	var b strings.Builder
	for _, n := range names {
		fmt.Fprintf(&b, "%s\t%s\n", n, since[n].UTC().Format(time.RFC3339))
	}
	path := filepath.Join(queue, RunnerBusyFile)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

// GHRunners is the real table: `gh api repos/<o/n>/actions/runners` for the runners and
// `gh run list --status in_progress` for what is actually running, each bounded.
type GHRunners struct{ Timeout time.Duration }

func (g GHRunners) Runners(repo string) ([]Runner, error) {
	out, err := g.sh("gh", "api", "repos/"+repo+"/actions/runners", "--jq", ".runners")
	if err != nil {
		return nil, err
	}
	var runners []Runner
	if strings.TrimSpace(out) == "" {
		return nil, nil
	}
	if err := json.Unmarshal([]byte(out), &runners); err != nil {
		return nil, fmt.Errorf("the runner list did not parse: %w", err)
	}
	return runners, nil
}

func (g GHRunners) InProgress(repo string) (int, error) {
	out, err := g.sh("gh", "run", "list", "-R", repo, "--status", "in_progress", "--limit", "50", "--json", "databaseId", "--jq", "length")
	if err != nil {
		return 0, err
	}
	n := 0
	if _, err := fmt.Sscanf(strings.TrimSpace(out), "%d", &n); err != nil {
		return 0, fmt.Errorf("the in-progress count did not parse: %w", err)
	}
	return n, nil
}

func (g GHRunners) sh(name string, args ...string) (string, error) {
	timeout := g.Timeout
	if timeout <= 0 {
		timeout = childTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	raw, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return string(raw), nil
}

// ServiceRestarter is the real restarter: per the service map, `systemctl restart <unit>`
// over ssh for a systemd runner (Space) and `<dir>/svc.sh stop && <dir>/svc.sh start` for a
// launchd one (the Studio). A runner with no row in the map is a refusal and not a guess:
// restarting the wrong service is worse than the jam.
type ServiceRestarter struct {
	Queue   string
	Timeout time.Duration
}

// RunnerService is one row of the service map.
type RunnerService struct {
	Name   string
	Host   string // "-" or empty is this machine
	Kind   string // systemd, svc, or runsh (fleet.go's normalizeServiceKind)
	Target string // the unit, or the runner directory holding svc.sh and run.sh
}

func (s ServiceRestarter) Restart(name string) error {
	services, err := ReadRunnerServices(s.Queue)
	if err != nil {
		return err
	}
	svc, ok := services[name]
	if !ok {
		return fmt.Errorf("runner %s has no row in %s (add name<TAB>host<TAB>kind<TAB>target; refusing to guess a service)",
			name, filepath.Join(s.Queue, RunnerServicesFile))
	}
	// The three mechanisms live in fleet.go's restartCommand, which `nova-pulse fleet
	// restart` also calls: the reaper and the verb restart a runner the same way or they
	// are two tools, and a runner restarted two ways is a runner nobody understands.
	command, err := restartCommand(svc)
	if err != nil {
		return err
	}
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = childTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var cmd *exec.Cmd
	if svc.Host == "" || svc.Host == "-" {
		cmd = exec.CommandContext(ctx, "sh", "-c", command)
	} else {
		cmd = exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5", svc.Host, command)
	}
	if raw, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %w (%s)", command, err, oneline.Escape(strings.TrimSpace(string(raw))))
	}
	return nil
}

// ReadRunnerServices reads the service map. A map nobody wrote is empty, and every restart
// against it is then a refusal naming the file.
func ReadRunnerServices(queue string) (map[string]RunnerService, error) {
	out := map[string]RunnerService{}
	for _, l := range readLines(filepath.Join(queue, RunnerServicesFile)) {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		f := strings.Split(t, "\t")
		if len(f) != 4 {
			return nil, fmt.Errorf("%s wants name<TAB>host<TAB>kind<TAB>target, got %d fields in %q",
				filepath.Join(queue, RunnerServicesFile), len(f), t)
		}
		out[strings.TrimSpace(f[0])] = RunnerService{
			Name: strings.TrimSpace(f[0]), Host: strings.TrimSpace(f[1]),
			Kind: strings.TrimSpace(f[2]), Target: strings.TrimSpace(f[3]),
		}
	}
	return out, nil
}
