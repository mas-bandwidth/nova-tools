//go:build darwin

// The darwin body: sandbox-exec with a profile generated from
// profiles/darwin.sb.tmpl for this one run. The profile is passed INLINE with -p, so no
// file is written anywhere at any point and there is nothing to clean up. sandbox-exec
// applies the profile and execs the command IN PLACE, so no second process sits between
// the tool and the command. The tool waits while forwarding SIGINT and
// SIGTERM to THE CHILD (cmd.Process.Signal, not the group: there is no Setpgid here, so
// the child stays in the caller's process group and the caller owns the group and the
// reaping) and returns the command's status.
package sandbox

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/subproc"
)

// Backend is what the SANDBOX OK line names on this platform.
const Backend = "sandbox-exec"

// ABI is the abi= field, which only linux fills.
func ABI() string { return "-" }

// ClampedABI is linux's alone: only Landlock has a numbered table this tool can be newer
// or older than. Here there is no number, so there is nothing to clamp and no used= field.
func ClampedABI() (int, bool) { return 0, false }

// sandboxExecPath is where the OS ships the backend. It is looked up on the PATH first,
// so a machine that moved it is not a refusal. A backend that is not there at all is
// reason=no_sandbox when the backend is unavailable.
const sandboxExecPath = "/usr/bin/sandbox-exec"

// Available reports whether sandbox-exec exists on this system.
// available is the test seam for backend refusal: a test can replace it with a function
// that says no, and the tool must then REFUSE rather than run. It is a variable and not a
// build tag because the refusal is the behaviour under test, not the platform.
var available = lookupSandboxExec

// Available returns the located backend path and whether it is usable.
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

// NetEnforceable reports that this platform always enforces a network denial: the grant is
// simply withheld from the generated profile.
func NetEnforceable() bool { return true }

// Note is the one clause the check verb prints about this backend.
func Note() string {
	return "sandbox-exec is deprecated by Apple and works on macOS 26; the wall is the profile it applies"
}

// Run applies the policy and runs the command, and returns the status the tool must exit
// with. A Refusal returned here is the tool saying NO before the command ran; it is
// fatal either way because the command cannot run safely without the sandbox.
func Run(p *Policy, env []string, stdin io.Reader, stdout, stderr io.Writer, okLine func()) (int, error) {
	// Do not run as root; this policy applies to ordinary users, and a policy applied by
	// a root process is a different thing than the one this spec describes.
	if os.Geteuid() == 0 {
		return ExitRefused, refuse("sandbox_failed", "this tool does not run as root: rule 2 is that the wall holds for an ordinary unprivileged user, and a root child is outside what this policy was measured against")
	}
	availFn := Available
	if p.Available != nil {
		availFn = p.Available
	}
	backend, ok := availFn()
	if !ok {
		return ExitRefused, refuse("no_sandbox", "sandbox-exec is on no PATH entry and is not at %s; this tool does not run a command it cannot contain", sandboxExecPath)
	}
	text, params, err := DarwinProfile(p)
	if err != nil {
		return ExitRefused, refuse("sandbox_failed", "the profile could not be generated: %v", err)
	}

	// The profile is passed inline, so there is no profile file in the
	// write set to race with and nothing to unlink on a signal death.
	argv := []string{"-p", text}
	for _, kv := range params {
		argv = append(argv, "-D", kv)
	}
	argv = append(argv, "--")
	argv = append(argv, p.Argv...)

	// A long-lived child: the wrapped command runs as long as it runs, under a cancellable
	// context and no deadline; signals reach it through the forwarder below.
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	cmd := subproc.Long(ctx, backend, argv...)
	cmd.Dir = p.Cwd
	cmd.Env = env
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	// The descriptors above stderr, in order from fd 3. sandbox-exec execs the command in
	// place, so what is fd 3 here is fd 3 in the command: the probe's nonce pipe survives
	// the backend without the backend knowing about it.
	cmd.ExtraFiles = p.Extra
	// RULE 12: no process group of the tool's own. The tree stays in the caller's group,
	// so the caller's group kill at its deadline reaches every process of it; a group of
	// the tool's would leave the tree running past the caller's reap, uncounted.
	//
	// THE CAPS (docs/SPEC-SANDBOX.md "wall-caps-processes.w1"): macOS has no limit that
	// binds a tree, so the tool counts the tree every second by parent pid from the
	// command (Tree) and kills it by pid past a cap, never by a pattern or a group.

	// SANDBOX OK is printed and FLUSHED before the command starts, so a log that ends in
	// a crash still says what the wall was.
	if okLine != nil {
		okLine()
	}
	if err := cmd.Start(); err != nil {
		return ExitNotExecuted, refuse("sandbox_failed", "%s could not be started: %v", backend, err)
	}

	// sandbox-exec execs the command in place, so the child's pid is the command's.
	tree := &Tree{top: cmd.Process.Pid, keepTop: true, pgid: syscall.Getpgrp(), list: psRows}
	stopWatch := p.Watch(p.Tick, tree.Usage, func() { tree.KillAndAwait() })

	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case s := <-sigs:
				if sig, ok := s.(syscall.Signal); ok && cmd.Process != nil {
					// ignored: a signal passed on to a child that may already have exited; the child's exit is the report
					_ = cmd.Process.Signal(sig)
				}
			case <-done:
				return
			}
		}
	}()
	waitErr := cmd.Wait()
	tree.Reaped()
	close(done)
	signal.Stop(sigs)
	if line := stopWatch(); line != "" {
		fmt.Fprintf(stderr, "SANDBOX RUNAWAY %s; %s\n", line, RunawayLine(tree.KillAndAwait()))
		return ExitRunaway, nil
	}
	return statusOf(waitErr, cmd.ProcessState), nil
}

// GroupUsage counts the live processes of group pgid and their resident bytes from one
// ps(1). A zombie is dead and is not counted.
func GroupUsage(pgid int) (Usage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := subproc.Context(ctx, "/bin/ps", "-A", "-o", "pgid=,rss=,stat=").Output()
	if err != nil {
		return Usage{}, fmt.Errorf("ps: %w", err)
	}
	var u Usage
	want := strconv.Itoa(pgid)
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 || f[0] != want || strings.HasPrefix(f[2], "Z") {
			continue
		}
		u.Procs++
		kib, _ := strconv.ParseInt(f[1], 10, 64)
		u.RSS += kib << 10
	}
	return u, nil
}

// psRows is the process table from one ps(1), for the tree count. rss is KiB there.
func psRows() ([]procRow, error) {
	// stat's first letter: Z a zombie, T stopped.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := subproc.Context(ctx, "/bin/ps", "-A", "-o", "pid=,ppid=,pgid=,rss=,stat=").Output()
	if err != nil {
		return nil, fmt.Errorf("ps: %w", err)
	}
	var rows []procRow
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 5 {
			continue
		}
		var r procRow
		var e1, e2, e3 error
		r.pid, e1 = strconv.Atoi(f[0])
		r.ppid, e2 = strconv.Atoi(f[1])
		r.pgid, e3 = strconv.Atoi(f[2])
		if e1 != nil || e2 != nil || e3 != nil {
			continue
		}
		kib, _ := strconv.ParseInt(f[3], 10, 64)
		r.rss = kib << 10
		r.zombie = strings.HasPrefix(f[4], "Z")
		r.stopped = strings.HasPrefix(f[4], "T")
		rows = append(rows, r)
	}
	return rows, nil
}
