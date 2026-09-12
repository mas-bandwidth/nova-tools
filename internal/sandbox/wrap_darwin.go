//go:build darwin

// The darwin body: sandbox-exec with a profile generated from
// profiles/darwin.sb.tmpl for this one run. sandbox-exec applies the profile and execs
// the command IN PLACE, so no second process sits between the tool and the command — but
// the tool WAITS (rule 12), because it must remove the generated profile file when the
// command ends.
package sandbox

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
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
func Available() (string, bool) {
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
	backend, ok := Available()
	if !ok {
		return ExitRefused, refuse("no_sandbox", "sandbox-exec is on no PATH entry and is not at %s; this tool does not run a command it cannot contain", sandboxExecPath)
	}
	text, params, err := DarwinProfile(p)
	if err != nil {
		return ExitRefused, refuse("sandbox_failed", "the profile could not be generated: %v", err)
	}

	// The filled profile lives inside the first --write, at 0600, under a name the tool
	// chooses, and is removed when the command ends — which is why this body waits.
	profile := filepath.Join(p.Writes[0], fmt.Sprintf("%s%d.sb", profileFilePrefx, os.Getpid()))
	if err := os.WriteFile(profile, []byte(text), profileFilePerm); err != nil {
		return ExitRefused, refuse("sandbox_failed", "the generated profile could not be written to %s: %v", profile, err)
	}
	defer os.Remove(profile)

	argv := []string{"-f", profile}
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
	// Its own process group, so that a forwarded signal reaches the whole wrapped tree.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

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
					_ = syscall.Kill(-cmd.Process.Pid, sig) // the child's process GROUP
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
