package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// reachTimeout bounds the one inventory read and one ssh probe, so a bench that
// does not answer leaves the check judged, never hung.
const reachTimeout = 20 * time.Second

// sshOptions are on every probe: BatchMode so a missing key is a refusal rather
// than a prompt nobody answers, and ConnectTimeout so a sleeping bench costs
// seconds.
var sshOptions = []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=5"}

func init() {
	Default.Register(Check{Name: "ssh", Dependency: "ssh between the coordinator and the benches",
		Fleet: true, Run: checkReach})
}

// checkReach covers the coordinator's reach to the fleet: the check reads the
// benches the inventory names (`nova-config inventory --list`), skips this
// machine, and runs `ssh -o BatchMode=yes -o ConnectTimeout=5 <bench> true`
// through the exec seam for each. A bench that does not answer is named with
// the reason ssh gave (unknown host key, no key, timeout), and the fix is the
// documented step, never a key the tool copies (docs/SETUP.md, dep-ssh-b.w3).
// It is a fleet check: `--local` skips it, because one machine reaches no other.
func checkReach(ctx context.Context, env Env) Result {
	benches, err := inventoryBenches(ctx, env)
	if err != nil {
		return Result{Status: Fail,
			Evidence: "the fleet inventory could not be read: " + oneLine(err.Error()),
			Fix:      "nova-config inventory --list (docs/SETUP.md, dep-ssh-b.w3)"}
	}
	if len(benches) == 0 {
		return Result{Status: OK, Evidence: "the inventory names no bench but this machine; nothing to reach"}
	}
	var problems []string
	var first *reachFailure
	for _, bench := range benches {
		f := reachBench(ctx, env, bench)
		if f == nil {
			continue
		}
		problems = append(problems, f.bench+": "+f.kind+" ("+f.detail+")")
		if first == nil {
			first = f
		}
	}
	if len(problems) == 0 {
		return Result{Status: OK,
			Evidence: fmt.Sprintf("%d bench(es) reachable by ssh, non-interactively, host keys known", len(benches))}
	}
	return Result{Status: Fail, Evidence: strings.Join(problems, "; "), Fix: reachFix(*first)}
}

// reachFailure is one bench the check could not reach: its name, the reason as
// one of unknown host key, no key, timeout or ssh failed, and the line ssh
// printed.
type reachFailure struct {
	bench  string
	kind   string
	detail string
}

// inventoryBenches reads the benches the inventory names through
// `nova-config inventory --list`, the Ansible inventory of the applied state
// (docs/FLEET.md). This machine is skipped: the inventory marks it
// ansible_connection=local, and a machine does not reach itself by ssh.
func inventoryBenches(ctx context.Context, env Env) ([]string, error) {
	cctx, cancel := context.WithTimeout(ctx, reachTimeout)
	defer cancel()
	out, err := env.Exec(cctx, "nova-config", "inventory", "--list")
	if err != nil {
		return nil, err
	}
	var inv struct {
		Meta struct {
			Hostvars map[string]struct {
				Connection string `json:"ansible_connection"`
			} `json:"hostvars"`
		} `json:"_meta"`
		Benches struct {
			Hosts []string `json:"hosts"`
		} `json:"benches"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &inv); err != nil {
		return nil, fmt.Errorf("nova-config inventory --list answered no inventory JSON: %w", err)
	}
	benches := make([]string, 0, len(inv.Benches.Hosts))
	for _, name := range inv.Benches.Hosts {
		if inv.Meta.Hostvars[name].Connection == "local" {
			continue
		}
		benches = append(benches, name)
	}
	return benches, nil
}

// reachBench runs the one probe against bench, or nil when it answers.
func reachBench(ctx context.Context, env Env, bench string) *reachFailure {
	args := append(append([]string{}, sshOptions...), bench, "true")
	cctx, cancel := context.WithTimeout(ctx, reachTimeout)
	defer cancel()
	_, err := env.Exec(cctx, "ssh", args...)
	if err == nil {
		return nil
	}
	return &reachFailure{bench: bench, kind: reachReason(cctx, err), detail: reachDetail(err)}
}

// reachReason names why ssh failed: the timeout when this check's own deadline
// ended it, else the line ssh printed (an exec seam keeps stderr on the error),
// else the error's own words.
func reachReason(ctx context.Context, err error) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "timeout"
	}
	text := ""
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		text = string(exit.Stderr)
	}
	if strings.TrimSpace(text) == "" {
		text = err.Error()
	}
	low := strings.ToLower(text)
	switch {
	case strings.Contains(low, "host key verification failed"),
		strings.Contains(low, "remote host identification has changed"),
		strings.Contains(low, "no hostkey alg"):
		return "unknown host key"
	case strings.Contains(low, "permission denied"),
		strings.Contains(low, "no supported authentication methods"),
		strings.Contains(low, "publickey"):
		return "no key"
	case strings.Contains(low, "timed out"),
		strings.Contains(low, "connection refused"),
		strings.Contains(low, "no route to host"),
		strings.Contains(low, "network is unreachable"),
		strings.Contains(low, "could not resolve hostname"):
		return "timeout"
	}
	return "ssh failed"
}

// reachDetail is the one line ssh printed, or the error's own words.
func reachDetail(err error) string {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		if s := strings.TrimSpace(string(exit.Stderr)); s != "" {
			if i := strings.IndexByte(s, '\n'); i >= 0 {
				s = s[:i]
			}
			return oneLine(s)
		}
	}
	return oneLine(err.Error())
}

// reachFix is the documented step for the first bench the check could not
// reach: a real verb or the step in docs/SETUP.md, never a key the tool copies.
func reachFix(f reachFailure) string {
	const doc = "docs/SETUP.md, dep-ssh-b.w3"
	switch f.kind {
	case "unknown host key":
		return "record " + f.bench + "'s host key once: ssh-keyscan " + f.bench + " >> ~/.ssh/known_hosts, then run nova-doctor again (" + doc + ")"
	case "no key":
		return "authorize this machine's key on " + f.bench + ": append this machine's ~/.ssh/id_ed25519.pub to " + f.bench + ":~/.ssh/authorized_keys (" + doc + ")"
	case "timeout":
		return "check that " + f.bench + " answers on the tailnet and its sshd is running (" + doc + ")"
	default:
		return "ssh -o BatchMode=yes -o ConnectTimeout=5 " + f.bench + " true, and fix what it prints (" + doc + ")"
	}
}
