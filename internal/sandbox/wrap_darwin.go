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

	"golang.org/x/sys/unix"

	"github.com/mas-bandwidth/nova-tools/internal/subproc"
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
	// THE CAPS (docs/SPEC-SANDBOX.md "wall-caps-processes.w1"): macOS has no limit that
	// binds a tree, so a command whose stdin is not a terminal leads a process group of
	// its own, which the tool counts every second and kills by its group id, never by a
	// pattern. A terminal keeps the caller's group: a child in a background group is
	// stopped by SIGTTIN the moment it reads the keyboard.
	capped := !isTerminal(stdin)
	if capped {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}

	// SANDBOX OK is printed and FLUSHED before the command starts, so a log that ends in
	// a crash still says what the wall was.
	if okLine != nil {
		okLine()
	}
	if err := cmd.Start(); err != nil {
		return ExitNotExecuted, refuse("sandbox_failed", "%s could not be started: %v", backend, err)
	}

	pgid := cmd.Process.Pid
	killGroup := func(sig syscall.Signal) {
		if capped {
			// ignored: the group may already be gone; the wait on the child is the check
			_ = syscall.Kill(-pgid, sig)
		} else {
			// ignored: a signal passed on to a child that may already have exited; the child's exit is the report
			_ = cmd.Process.Signal(sig)
		}
	}
	stopWatch := func() string { return "" }
	if capped {
		stopWatch = p.Watch(p.Tick, func() (Usage, error) { return GroupUsage(pgid) }, func() { killGroup(syscall.SIGKILL) })
	}

	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case s := <-sigs:
				if sig, ok := s.(syscall.Signal); ok && cmd.Process != nil {
					killGroup(sig)
				}
			case <-done:
				return
			}
		}
	}()
	waitErr := cmd.Wait()
	close(done)
	signal.Stop(sigs)
	if line := stopWatch(); line != "" {
		killGroup(syscall.SIGKILL)
		awaitGroupGone(pgid)
		fmt.Fprintf(stderr, "SANDBOX RUNAWAY %s; the process group was killed\n", line)
		return ExitRunaway, nil
	}
	return statusOf(waitErr, cmd.ProcessState), nil
}

// isTerminal reports whether r is a terminal.
func isTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	_, err := unix.IoctlGetTermios(int(f.Fd()), unix.TIOCGETA)
	return err == nil
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

// awaitGroupGone waits, bounded, for every live process of the group to be gone after a
// SIGKILL: delivery is asynchronous and a caller told "killed" must find nothing running.
func awaitGroupGone(pgid int) {
	for i := 0; i < 100; i++ {
		if u, err := GroupUsage(pgid); err == nil && u.Procs == 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}
