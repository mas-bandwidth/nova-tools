package up

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func init() {
	Register(Step{
		Name:  "ssh",
		Order: 55,
		Plan:  sshPlan,
		Apply: sshApply,
	})
}

// sshPlan checks if ssh is available and configured for bench access.
func sshPlan(e *Env) Finding {
	// Check if ssh binary exists
	_, err := e.Exec.LookPath("ssh")
	if err != nil {
		return Finding{
			State:  Missing,
			Detail: "ssh not found; install openssh-client (linux) or openssh (darwin)",
		}
	}

	// Check if known_hosts file exists
	knownHosts := filepath.Join(e.Machine.Home, ".ssh", "known_hosts")
	if _, err := os.Stat(knownHosts); os.IsNotExist(err) {
		return Finding{
			State:  Create,
			Detail: "known_hosts missing; run `ssh-keyscan <bench>` for each bench",
		}
	}

	return Finding{State: OK, Detail: "ssh configured"}
}

// sshApply creates the ssh directory if needed.
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
		f.Close()
	}

	return nil
}

// checkSSHHostKey verifies a host key is in known_hosts.
func checkSSHHostKey(home, bench string) bool {
	knownHosts := filepath.Join(home, ".ssh", "known_hosts")
	b, err := os.ReadFile(knownHosts)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}
		if strings.HasPrefix(line, bench+" ") || strings.HasPrefix(line, bench+",") {
			return true
		}
	}
	return false
}
