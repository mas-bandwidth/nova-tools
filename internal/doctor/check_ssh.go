package doctor

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// sshTimeout bounds one ssh probe the check runs.
const sshTimeout = 10 * time.Second

func init() {
	Default.Register(Check{
		Name:       "ssh",
		Dependency: "ssh between the coordinator and the benches",
		Fleet:      true,
		Run:        checkSSH,
	})
}

// checkSSH verifies that the machine can reach each bench in the inventory via ssh.
// It runs `ssh -o BatchMode=yes -o ConnectTimeout=5 <bench> true` through a fakeable exec
// and names a bench that fails with the reason (unknown host key, no key, timeout).
func checkSSH(ctx context.Context, env Env) Result {
	store, seat := env.Getenv("NOVA_SECRETS_STORE"), env.Getenv("NOVA_SECRETS_SEAT")
	if store == "" || seat == "" {
		return Result{Status: Fail, Evidence: "no seat store; cannot read inventory",
			Fix: "nova-up --local writes the seat's store and inventory"}
	}

	// Read inventory from seat's secrets store
	invPath := strings.TrimSuffix(store, "/") + "/" + seat + "_inv.yaml"
	invBytes, err := env.ReadFile(invPath)
	if err != nil {
		// Try alternate location
		invPath = strings.TrimSuffix(store, "/") + "/" + seat + ".inv"
		invBytes, err = env.ReadFile(invPath)
		if err != nil {
			return Result{Status: Fail, Evidence: "cannot read inventory: " + err.Error(),
				Fix: "nova-up --local writes the seat's inventory"}
		}
	}

	// Parse bench names from inventory (simple YAML parsing)
	var benches []string
	for _, line := range strings.Split(string(invBytes), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "secrets:") {
			continue
		}
		if strings.HasPrefix(line, "benches:") {
			continue
		}
		if strings.HasPrefix(line, "  - ") {
			benches = append(benches, strings.TrimPrefix(line, "  - "))
		} else if strings.Contains(line, ":") {
			// Key-value line, skip
			continue
		} else if line != "" {
			benches = append(benches, line)
		}
	}

	if len(benches) == 0 {
		return Result{Status: OK, Evidence: "no benches in inventory"}
	}

	var failures []string
	for _, bench := range benches {
		out, err := env.Exec(ctx, "ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5", bench, "true")
		if err != nil {
			reason := "connection failed"
			if strings.Contains(err.Error(), "host key") || strings.Contains(out, "host key") {
				reason = "unknown host key"
			} else if strings.Contains(err.Error(), "timeout") || strings.Contains(out, "timeout") {
				reason = "timeout"
			} else if strings.Contains(err.Error(), "permission") || strings.Contains(out, "permission") {
				reason = "no key or permission denied"
			}
			failures = append(failures, fmt.Sprintf("%s: %s", bench, reason))
		}
	}

	if len(failures) == 0 {
		return Result{Status: OK, Evidence: fmt.Sprintf("all %d bench(es) reachable", len(benches))}
	}

	return Result{
		Status:   Fail,
		Evidence: strings.Join(failures, "; "),
		Fix:      "add host key to ~/.ssh/known_hosts or run `ssh-keyscan <bench>`; see docs/SETUP.md, dep-ssh-b.w6",
	}
}
