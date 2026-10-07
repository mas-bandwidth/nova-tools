package up

import (
	"fmt"
	"os"
	"path/filepath"
)

func init() {
	Register(Step{Name: "ssh", Order: 55, Plan: sshPlan, Apply: sshApply})
}

func sshPlan(e *Env) Finding {
	if _, err := e.Exec.LookPath("ssh"); err != nil {
		return Finding{State: Missing, Detail: "ssh not found; install openssh-client (linux) or openssh (darwin)"}
	}
	knownHosts := filepath.Join(e.Machine.Home, ".ssh", "known_hosts")
	if _, err := os.Stat(knownHosts); os.IsNotExist(err) {
		return Finding{State: Create, Detail: "known_hosts missing; run `ssh-keyscan <bench>` for each bench"}
	}
	return Finding{State: OK, Detail: "ssh configured"}
}

func sshApply(e *Env) error {
	sshDir := filepath.Join(e.Machine.Home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", sshDir, err)
	}
	knownHosts := filepath.Join(sshDir, "known_hosts")
	if _, err := os.Stat(knownHosts); os.IsNotExist(err) {
		f, err := os.OpenFile(knownHosts, os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return fmt.Errorf("create %s: %w", knownHosts, err)
		}
		if err := f.Close(); err != nil {
			return fmt.Errorf("close %s: %w", knownHosts, err)
		}
	}
	return nil
}
