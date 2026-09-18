//go:build darwin

// The run verb's executor: the same seatbelt wall the bare form applies, around a child
// in a PROCESS GROUP OF ITS OWN.
//
// The bare form deliberately does NOT make a group (internal/sandbox/wrap_darwin.go): the
// caller owns the group there, because a swarm supervisor puts each job in a group of its
// making and reaps that group at the deadline, and a tool that made its own would hide a
// forked background child from the reaper. The run verb is the OTHER side of that same
// rule — here the tool IS the supervisor. It owns the disposable volume, so it owns the
// group that can hold the volume open, and a survivor it did not kill is a volume it
// cannot unmount: a leak, not an untidiness.
package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"

	"github.com/mas-bandwidth/nova-tools/internal/sandbox"
)

// startInOwnGroup applies the policy, starts the command in a new process group, and
// hands back the two things the supervisor needs: the channel the status arrives on, and
// the function that signals the WHOLE group.
func startInOwnGroup(p *sandbox.Policy, env []string, stdin io.Reader, stdout, stderr io.Writer) (<-chan int, func(syscall.Signal), error) {
	// Rule 2: no root, the same as the bare form. A wall measured for an unprivileged
	// user says nothing about a root child, and a root child could delete far more than
	// its own volume.
	if os.Geteuid() == 0 {
		return nil, nil, sandbox.Refusal{Reason: "sandbox_failed",
			Text: "this tool does not run as root: the wall holds for an ordinary unprivileged user, and a disposable volume made and deleted by root is not the thing this verb was measured as"}
	}
	backend, ok := sandbox.Available()
	if !ok {
		return nil, nil, sandbox.Refusal{Reason: "no_sandbox",
			Text: "sandbox-exec is on no PATH entry; this tool does not run a command it cannot contain, disposable volume or not"}
	}
	text, params, err := sandbox.DarwinProfile(p)
	if err != nil {
		return nil, nil, sandbox.Refusal{Reason: "sandbox_failed", Text: fmt.Sprintf("the profile could not be generated: %v", err)}
	}
	// Inline with -p, as the bare form does: no profile file anywhere, so there is nothing
	// on the boot volume to unlink when the run dies.
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
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, nil, sandbox.Refusal{Reason: "sandbox_failed", Text: fmt.Sprintf("%s could not be started: %v", backend, err)}
	}
	// Setpgid makes the child the LEADER of a new group, so the group's id is its pid.
	// The negative pid is the group, and it is the only thing this verb ever signals.
	pgid := cmd.Process.Pid
	killGroup := func(sig syscall.Signal) { _ = syscall.Kill(-pgid, sig) }

	done := make(chan int, 1)
	go func() {
		waitErr := cmd.Wait()
		done <- statusOfRun(waitErr, cmd.ProcessState)
	}()
	return done, killGroup, nil
}

// statusOfRun is the wrapped command's status in env(1)'s terms: its own code, or 128+N
// when a signal ended it, or 126 when there is no state to read at all.
func statusOfRun(waitErr error, st *os.ProcessState) int {
	if st == nil {
		return sandbox.ExitNotExecuted
	}
	if ws, ok := st.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	if code := st.ExitCode(); code >= 0 {
		return code
	}
	if waitErr != nil {
		return sandbox.ExitNotExecuted
	}
	return 0
}
