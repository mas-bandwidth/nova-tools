//go:build darwin

// The darwin body: sandbox-exec with a profile generated from
// profiles/darwin.sb.tmpl for this one run. The profile is passed INLINE with -p, so no
// file is written anywhere at any point and there is nothing to clean up. sandbox-exec
// applies the profile and execs the command IN PLACE, so no second process sits between
// the tool and the command — but the tool WAITS (rule 12), because it forwards SIGINT and
// SIGTERM to THE CHILD (cmd.Process.Signal, not the group: there is no Setpgid here, so
// the child stays in the caller's process group and the caller owns the group and the
// reaping) and returns the command's status.
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
	// The descriptors above stderr, in order from fd 3. sandbox-exec execs the command in
	// place, so what is fd 3 here is fd 3 in the command: the probe's nonce pipe survives
	// the backend without the backend knowing about it.
	cmd.ExtraFiles = p.Extra
	// NO Setpgid: the wrapped tree stays in the CALLER's process group, and the caller
	// owns pgid and reaping. A group of the tool's own looked tidier and was wrong: a
	// swarm supervisor puts each job in a group of its making and reaps that group at the
	// deadline (SPEC-SWARM rule 11), and a command that forked a background child left
	// that child in the tool's group, outside the one the supervisor kills -- measured by
	// the seam read, survivors=0 reported while a process was still alive, which is the
	// silent failure that rule exists to prevent. Signals are forwarded to the CHILD
	// (below), not to a group, for the same reason: the group is not the tool's to signal.

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
					// The CHILD, not -pid: with no group of its own, -pid would name a
					// process group this tool never created and does not own.
					_ = cmd.Process.Signal(sig)
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
