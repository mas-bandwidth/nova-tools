package doctor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// sshTimeout bounds one ssh probe the check runs, and the inventory read before
// it.
const sshTimeout = 10 * time.Second

// sshDoc is the documented step a fix line names for ssh between the coordinator
// and the benches (docs/SETUP.md, dep-ssh-b.w8).
const sshDoc = "docs/SETUP.md, dep-ssh-b.w8"

func init() {
	Default.Register(Check{
		Name:       "ssh",
		Dependency: "ssh between the coordinator and the benches",
		Fleet:      true,
		Run:        checkBenchReach,
	})
}

// checkBenchReach covers the ssh a fleet machine needs to reach the benches: the
// land, the sandbox worktrees and the bench rule all start `ssh <bench>` on a
// machine that must answer without a prompt (docs/SPEC-DOCTOR.md, the checks).
// It reads the inventory (`nova-config machine list`, with the seat loaded) and
// runs `ssh -o BatchMode=yes -o ConnectTimeout=5 <bench> true` for each machine,
// naming each one that fails and the reason (unknown host key, no key, timeout).
// The fix line is the documented step; the check never copies a key. It is
// fleet-only: a single-machine setup has no other machine to reach. The child
// goes through Env.Exec, whose real seam (OSEnv.Exec) is the one place a host is
// reached, so this function is not itself a host seam.
func checkBenchReach(ctx context.Context, env Env) Result {
	cctx, cancel := context.WithTimeout(ctx, sshTimeout)
	defer cancel()
	benches, err := inventoryNames(cctx, env)
	if err != nil {
		return Result{Status: Fail,
			Evidence: "the bench inventory could not be read: " + err.Error(),
			Fix:      "source the seat file (`set -a; . ~/nova/seat.env; set +a`), then run nova-doctor again (" + sshDoc + ")"}
	}
	if len(benches) == 0 {
		return Result{Status: OK, Evidence: "no benches in the inventory"}
	}

	var failures []string
	for _, bench := range benches {
		pctx, pcancel := context.WithTimeout(ctx, sshTimeout)
		out, err := env.Exec(pctx, "ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5", bench, "true")
		pcancel()
		if err == nil {
			continue
		}
		failures = append(failures, fmt.Sprintf("%s: %s", bench, reachReason(err, out)))
	}
	if len(failures) == 0 {
		return Result{Status: OK, Evidence: fmt.Sprintf("all %d bench(es) answer `ssh -o BatchMode=yes <bench> true`", len(benches))}
	}
	return Result{
		Status:   Fail,
		Evidence: strings.Join(failures, "; "),
		Fix:      "add the bench's host key with `ssh-keyscan <bench> >> ~/.ssh/known_hosts`, and install the key that logs in to it (`ssh-copy-id <bench>`) (" + sshDoc + ")",
	}
}

// reachReason names why one probe failed, from ssh's own words: it timed out
// (including the check's own deadline), the host key is unknown, no key was
// accepted, else the connection failed. ssh's wording is matched without case,
// because the same failure is "Host key verification failed." and "host key".
func reachReason(err error, out string) string {
	text := strings.ToLower(err.Error() + " " + out)
	switch {
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(text, "timeout") || strings.Contains(text, "timed out"):
		return "timeout"
	case strings.Contains(text, "host key"):
		return "unknown host key"
	case strings.Contains(text, "permission denied") || strings.Contains(text, "publickey") || strings.Contains(text, "no such identity"):
		return "no key or permission denied"
	default:
		return "connection failed"
	}
}
