package doctor

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const sshProbeTimeout = 10 * time.Second

func init() {
	Default.Register(Check{
		Name:       "ssh",
		Dependency: "ssh access between the coordinator and the benches",
		Run:        checkSSH,
	})
}

func checkSSH(ctx context.Context, env Env) Result {
	// Check if this is a bench (coordinator checks happen elsewhere)
	if coordinatorSeat(env) {
		// On the coordinator, we don't run ssh checks; benches are reached remotely
		return Result{
			Status:   OK,
			Evidence: "coordinator machine; ssh checks run on benches",
		}
	}

	// On a bench, verify ssh is available using `which ssh`
	ctx, cancel := context.WithTimeout(ctx, sshProbeTimeout)
	defer cancel()
	whichOut, err := env.Exec(ctx, "sh", "-c", "which ssh")
	if err != nil {
		return Result{
			Status:   Fail,
			Evidence: "ssh not found on PATH",
			Fix:      "install openssh-client on this bench (docs/SETUP.md, dep-ssh-b.w4)",
		}
	}
	sshPath := strings.TrimSpace(whichOut)
	if sshPath == "" {
		return Result{
			Status:   Fail,
			Evidence: "ssh not found on PATH",
			Fix:      "install openssh-client on this bench (docs/SETUP.md, dep-ssh-b.w4)",
		}
	}

	// Verify ssh binary works
	sshOut, err := env.Exec(ctx, sshPath, "-V")
	if err != nil {
		return Result{
			Status:   Fail,
			Evidence: fmt.Sprintf("ssh did not answer: %v", err),
			Fix:      "reinstall openssh-client on this bench (docs/SETUP.md, dep-ssh-b.w4)",
		}
	}
	if !strings.Contains(sshOut, "OpenSSH") {
		return Result{
			Status:   Warn,
			Evidence: fmt.Sprintf("ssh binary found but not OpenSSH: %s", strings.TrimSpace(sshOut)),
			Fix:      "install openssh-client on this bench (docs/SETUP.md, dep-ssh-b.w4)",
		}
	}

	return Result{
		Status:   OK,
		Evidence: fmt.Sprintf("ssh available at %s", sshPath),
	}
}
