package up

import (
	"fmt"
	"os"
	"path/filepath"
)

// The ssh step is the one-machine part of the ssh dependency
// (docs/SETUP.md, dep-ssh-b.w8): a fleet reaches its benches with
// `ssh <bench>`, so this machine needs ssh on PATH, `~/.ssh` at mode 0700, and
// a `known_hosts` file to carry the benches' host keys. The step makes the
// directory and an empty file; it never runs `ssh-keyscan` and never copies a
// key, because a host key taken without asking is the tool trusting a bench for
// the person. A person fills the file and puts this machine's key on each bench.
func init() {
	Register(Step{Name: "ssh", Order: 55, Plan: planRemote, Apply: applyRemote})
}

// planRemote reports ssh missing when its binary is not on PATH, create until
// `~/.ssh/known_hosts` exists, and ok once it does. The child is not started
// here; Env.Exec's real seam is the one place a host is reached, so this
// function is not itself a host seam.
func planRemote(e *Env) Finding {
	if _, err := e.Exec.LookPath("ssh"); err != nil {
		return Finding{State: Missing, Detail: "ssh not found; install openssh-client (linux) or openssh (darwin)"}
	}
	knownHosts := filepath.Join(e.Machine.Home, ".ssh", "known_hosts")
	if _, err := os.Stat(knownHosts); os.IsNotExist(err) {
		return Finding{State: Create, Detail: "known_hosts missing; run `ssh-keyscan <bench> >> ~/.ssh/known_hosts` for each bench"}
	}
	return Finding{State: OK, Detail: "ssh configured"}
}

// applyRemote makes `~/.ssh` at mode 0700 and an empty `known_hosts` at mode
// 0600, each only when it is absent, and changes nothing else.
func applyRemote(e *Env) error {
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
