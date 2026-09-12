//go:build darwin

// The darwin body: sandbox-exec with a profile generated from
// profiles/darwin.sb.tmpl for this one run. sandbox-exec applies the profile and execs
// the command IN PLACE, so no second process sits between the tool and the command — but
// the tool WAITS (rule 12), because it must remove the generated profile file when the
// command ends.
package sandbox

import (
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

// Backend is what the SANDBOX OK line names on this platform.
const Backend = "sandbox-exec"

// ABI is the abi= field, which only linux fills.
const ABI = "-"

// sandboxExecPath is where the OS ships the backend. It is looked up on the PATH first,
// so a machine that moved it is not a refusal. A backend that is not there at all is
// reason=no_sandbox (rule 1).
const sandboxExecPath = "/usr/bin/sandbox-exec"

// Available answers rule 1's question for this machine: is the backend there at all?
// available is the seam rule 1's refusal is tested through: a test sets it to a function
// that says no, and the tool must then REFUSE rather than run. It is a variable and not a
// build tag because the refusal is the behaviour under test, not the platform.
var available = lookupSandboxExec

// Available answers rule 1's question for this machine: is the backend there at all?
func Available() (string, bool) { return available() }

func lookupSandboxExec() (string, bool) {
	if p, err := exec.LookPath("sandbox-exec"); err == nil {
		return p, true
	}
	if fi, err := os.Stat(sandboxExecPath); err == nil && !fi.IsDir() && fi.Mode().Perm()&0o111 != 0 {
		return sandboxExecPath, true
	}
	return "", false
}

// NetEnforceable is rule 7 for this platform: the grant is simply withheld from the
// generated profile, so an enforced denial is always available here.
func NetEnforceable() bool { return true }

// Note is the one clause the check verb prints about this backend.
func Note() string {
	return "sandbox-exec is deprecated by Apple and works on macOS 26; the wall is the profile it applies"
}

// Run applies the policy and runs the command, and returns the status the tool must exit
// with. A Refusal returned here is the tool saying NO before the command ran; it is
// fatal either way, because there is no fallback and no degraded mode (rule 1).
func Run(p *Policy, env []string, stdin io.Reader, stdout, stderr io.Writer, okLine func()) (int, error) {
	// Rule 2: no root. The wall is a wall for an ordinary user, and a policy applied by
	// a root process is a different thing than the one this spec describes.
	if os.Geteuid() == 0 {
		return ExitRefused, refuse("sandbox_failed", "this tool does not run as root: rule 2 is that the wall holds for an ordinary unprivileged user, and a root child is outside what this policy was measured against")
	}
	backend, ok := Available()
	if !ok {
		return ExitRefused, refuse("no_sandbox", "sandbox-exec is on no PATH entry and is not at %s; this tool does not run a command it cannot contain", sandboxExecPath)
	}
	text, params, err := DarwinProfile(p)
	if err != nil {
		return ExitRefused, refuse("sandbox_failed", "the profile could not be generated: %v", err)
	}

	// Rule 12 / revision 7: the profile is passed INLINE. No file, so nothing in the
	// write set to race with and nothing to unlink on a signal death.
	argv := []string{"-p", text}
	for _, kv := range params {
		argv = append(argv, "-D", kv)
	}
	argv = append(argv, "--")
	argv = append(argv, p.Argv...)

	cmd := exec.Command(backend, argv...)
	cmd.Dir = p.Cwd
	cmd.Env = env
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	// THE WRAPPED TREE STAYS IN THE CALLER'S PROCESS GROUP, and this is a change the
	// nova-swarm seam made to this body rather than a preference. A `Setpgid: true` here
	// puts sandbox-exec and everything it execs into a NEW group whose id no caller can
	// learn: os/exec hands back the tool's pid and nothing below it, and a launcher that
	// must reap a job -- nova-swarm's supervisor, which puts the whole job in one group
	// and counts what is left in it after the leader has gone (SPEC-SWARM rule 11) -- then
	// holds a group that contains the wrapper and not the work. Measured on this Mac
	// 2026-09-12: with the new group, a wrapped harness that forks a background child left
	// that child alive after the run and the supervisor's survivor count was 0, and the
	// job was recorded `done`; without it, the same child is counted and killed, which is
	// what rule 11 demands. Inheriting the caller's group costs nothing here: a launcher
	// that wants the tree in a group of its own puts THIS process in one (the swarm does),
	// and then the whole tree is in it by inheritance.
	//
	// The signal forwarding below therefore targets the CHILD'S PID rather than a group:
	// the child's group is now the tool's own, and `kill(-pgid)` from inside it would
	// deliver the signal back to this process, over and over, through its own handler.

	// SANDBOX OK is printed and FLUSHED before the command starts, so a log that ends in
	// a crash still says what the wall was.
	if okLine != nil {
		okLine()
	}
	if err := cmd.Start(); err != nil {
		return ExitNotExecuted, refuse("sandbox_failed", "%s could not be started: %v", backend, err)
	}

	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case s := <-sigs:
				if sig, ok := s.(syscall.Signal); ok && cmd.Process != nil {
					// The child, not the group: the group is this process's own, and
					// signalling it would signal this process again (see above).
					_ = syscall.Kill(cmd.Process.Pid, sig)
				}
			case <-done:
				return
			}
		}
	}()
	waitErr := cmd.Wait()
	close(done)
	signal.Stop(sigs)
	return statusOf(waitErr, cmd.ProcessState), nil
}

// statusOf is the exit-status mapping of rule 12: the child's status is the tool's, and
// a death by signal N is 128+N.
func statusOf(waitErr error, st *os.ProcessState) int {
	if st != nil {
		if ws, ok := st.Sys().(syscall.WaitStatus); ok {
			if ws.Signaled() {
				return 128 + int(ws.Signal())
			}
			return ws.ExitStatus()
		}
		return st.ExitCode()
	}
	if waitErr != nil {
		return ExitNotExecuted
	}
	return 0
}
