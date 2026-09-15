//go:build linux

// The linux body: user, mount and (on --net-deny) network namespaces via
// unshare(2), with the caller's --read directories bind-mounted read-only and its
// --write directories bind-mounted read-write into a fresh root the command is
// chroot(2)ed into. Everything else on disk is not present inside the new root, so
// the wall is the absence of a path, not a rule about one.
//
// The command's own status is the tool's (env(1)'s convention, as on darwin). The
// one departure from the darwin body is the shape of the seam: unshare(CLONE_NEWUSER)
// must run single-threaded and before the uid_map write, so the tool RE-EXECS itself
// with the hidden `linux-child` verb (the plan travels on the environment), and that
// child does the namespace work and then syscall.Exec's the command in place. A
// caller who runs the hidden verb directly is refused by the parent-process guard,
// which needs no secret value because nothing the verb does grants a capability the
// caller lacks: the wall it builds is one the caller could build with unshare(1).
package sandbox

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

// Backend is what the SANDBOX OK line names on this platform.
const Backend = "unshare"

// ABI is the abi= field; unshare has no numbered ABI, so the line carries the one
// "-" the grammar already means by "or no ABI".
const ABI = "-"

// LinuxChildVerb is the hidden verb this body's child runs, shared with every
// platform's stub so cmd/nova-sandbox dispatches one name on all three.
const LinuxChildVerb = "linux-child"

// linuxPlanVar carries the re-exec plan from the parent to the child. It holds
// directory paths and argv, never a credential.
const linuxPlanVar = "NOVA_SANDBOX_LINUX_PLAN"

// linuxPlan is what the parent sends the child: the resolved, already-refused
// policy, serialised so the child needs no access to the caller's filesystem before
// the wall is up.
type linuxPlan struct {
	Reads   []string
	Writes  []string
	Cwd     string
	NetDeny bool
	Argv    []string
}

// Available answers rule 1's question for this machine: the unshare(2) syscall is
// present on every linux, and an unprivileged user namespace is the wall itself. If
// the kernel forbids it, the first Run refuses at the syscall, loudly, rather than
// advertising a wall in `check` that a later run cannot build.
func Available() (string, bool) { return "unshare", true }

// NetEnforceable is rule 7 for this platform: --net-deny becomes a network namespace
// with no interface, which is a stronger promise than any policy rule.
func NetEnforceable() bool { return true }

// Note is the one clause the check verb prints about this backend.
func Note() string {
	return "user, mount and network namespaces via unshare(2); --read are read-only bind mounts at their own paths and --write are read-write, in a fresh root the command is confined to"
}

// Run applies the policy and runs the command by re-executing this binary as the
// linux child, and returns the status the tool must exit with. The parent stays to
// forward SIGINT/SIGTERM and reap, exactly as the darwin body does.
func Run(p *Policy, env []string, stdin io.Reader, stdout, stderr io.Writer, okLine func()) (int, error) {
	if os.Geteuid() == 0 {
		return ExitRefused, refuse("sandbox_failed", "this tool does not run as root: rule 2 is that the wall holds for an ordinary unprivileged user, and a root child is outside what this policy was measured against")
	}
	planBytes, err := json.Marshal(linuxPlan{
		Reads: p.Reads, Writes: p.Writes, Cwd: p.Cwd, NetDeny: p.NetDeny, Argv: p.Argv,
	})
	if err != nil {
		return ExitRefused, refuse("sandbox_failed", "the run plan could not be built: %v", err)
	}
	self, err := os.Executable()
	if err != nil {
		return ExitRefused, refuse("sandbox_failed", "this binary cannot name its own path: %v", err)
	}

	// SANDBOX OK is printed and FLUSHED before the child is launched, so a log that
	// ends in a crash still says what the wall was.
	if okLine != nil {
		okLine()
	}

	cmd := exec.Command(self, LinuxChildVerb)
	cmd.Env = append(env, linuxPlanVar+"="+string(planBytes))
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	// No Setpgid, for the same reason as darwin: the wrapped tree stays in the
	// caller's process group so the supervisor owns pgid and reaping.
	if err := cmd.Start(); err != nil {
		return ExitNotExecuted, refuse("sandbox_failed", "%s could not be started: %v", self, err)
	}

	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case s := <-sigs:
				if sig, ok := s.(syscall.Signal); ok && cmd.Process != nil {
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

// statusOf is the exit-status mapping shared with the darwin body: the child's
// status is the tool's, and a death by signal N is 128+N.
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

// LinuxChild is the hidden verb's body. It is not a caller verb and grants no
// capability a caller lacks, so its guard is the parent process being this same
// binary: a shell that runs `nova-sandbox linux-child` is refused before a namespace
// is entered or a byte of the plan is honoured.
func LinuxChild(args []string, stdout, stderr io.Writer, env []string) int {
	_ = args
	if r := linuxChildGuard(); r != "" {
		fmt.Fprintf(stderr, "SANDBOX REFUSED reason=probe_step_not_a_child: %s\n", r)
		return ExitCannotRun
	}
	var plan linuxPlan
	raw := ""
	for _, kv := range env {
		if n, v, _ := strings.Cut(kv, "="); n == linuxPlanVar {
			raw = v
			break
		}
	}
	if raw == "" {
		fmt.Fprintf(stderr, "SANDBOX REFUSED reason=no_command: %s carries no run plan; it is the child of a wrap this binary started and nothing else runs it\n", LinuxChildVerb)
		return ExitCannotRun
	}
	if err := json.Unmarshal([]byte(raw), &plan); err != nil {
		fmt.Fprintf(stderr, "SANDBOX REFUSED reason=sandbox_failed: the run plan does not parse: %v\n", err)
		return ExitCannotRun
	}
	// The plan variable belongs to the child, never to the command: it is dropped from
	// the environment the command inherits.
	childEnv := make([]string, 0, len(env))
	for _, kv := range env {
		if n, _, _ := strings.Cut(kv, "="); n != linuxPlanVar {
			childEnv = append(childEnv, kv)
		}
	}
	if err := linuxConfine(plan); err != nil {
		fmt.Fprintf(stderr, "SANDBOX REFUSED reason=sandbox_failed: %s\n", err)
		return ExitRefused
	}
	// The command becomes this process; only an exec failure returns.
	if err := syscall.Exec(plan.Argv[0], plan.Argv, childEnv); err != nil {
		fmt.Fprintf(stderr, "SANDBOX REFUSED reason=not_executed: %s could not be executed inside the wall: %v\n", plan.Argv[0], err)
		return ExitNotExecuted
	}
	return ExitNotExecuted
}

// linuxChildGuard is the child's parent-process check: /proc/<ppid>/exe must resolve to
// this same binary.
func linuxChildGuard() string {
	self, err := os.Executable()
	if err != nil {
		return "this process cannot name its own path: " + err.Error()
	}
	if r, err := filepath.EvalSymlinks(self); err == nil {
		self = r
	}
	parent, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", os.Getppid()))
	if err != nil {
		return "the parent process cannot be named: " + err.Error()
	}
	if r, err := filepath.EvalSymlinks(parent); err == nil {
		parent = r
	}
	if self != parent {
		return LinuxChildVerb + " runs only as the child of a wrap this binary started; the parent process is not this binary"
	}
	return ""
}

// linuxConfine enters the namespaces, builds the root and moves the process into it.
// It is the whole of the wall: after the final chroot(2), every path outside the
// read and write sets is not just denied, it is not there.
func linuxConfine(plan linuxPlan) error {
	// unshare(CLONE_NEWUSER) must run while the process is single-threaded, and Go
	// may otherwise move the goroutine onto a thread outside the new namespace.
	runtime.GOMAXPROCS(1)
	runtime.LockOSThread()

	if err := syscall.Unshare(syscall.CLONE_NEWUSER); err != nil {
		return fmt.Errorf("unshare(CLONE_NEWUSER): %w; user namespaces may be disabled on this kernel", err)
	}
	if err := writeIDMaps(); err != nil {
		return err
	}
	// With the uid/gid map written the process holds CAP_SYS_ADMIN in the new user
	// namespace, so the mount and (optional) network namespaces come second.
	flags := syscall.CLONE_NEWNS
	if plan.NetDeny {
		flags |= syscall.CLONE_NEWNET
	}
	if err := syscall.Unshare(flags); err != nil {
		return fmt.Errorf("unshare(mount%s): %w", netWord(plan.NetDeny), err)
	}
	// A fresh mount namespace inherits mount propagation; make the tree private so
	// no bind mount leaks to the host or a sibling.
	if err := syscall.Mount("", "/", "", syscall.MS_REC|syscall.MS_PRIVATE, ""); err != nil {
		return fmt.Errorf("remount / private: %w", err)
	}
	root, err := buildRoot(plan)
	if err != nil {
		return err
	}
	if err := syscall.Chroot(root); err != nil {
		return fmt.Errorf("chroot %s: %w", root, err)
	}
	if err := os.Chdir(plan.Cwd); err != nil {
		return fmt.Errorf("chdir %s inside the wall: %w", plan.Cwd, err)
	}
	return nil
}

func netWord(deny bool) string {
	if deny {
		return "+network"
	}
	return ""
}

// writeIDMaps maps the caller's uid and gid to 0 inside the namespace. setgroups must
// be denied first, or the gid_map write is refused.
func writeIDMaps() error {
	if err := os.WriteFile("/proc/self/setgroups", []byte("deny\n"), 0o600); err != nil {
		return fmt.Errorf("writing /proc/self/setgroups: %w", err)
	}
	if err := os.WriteFile("/proc/self/gid_map", []byte(fmt.Sprintf("0 %d 1\n", os.Getgid())), 0o600); err != nil {
		return fmt.Errorf("writing /proc/self/gid_map: %w", err)
	}
	if err := os.WriteFile("/proc/self/uid_map", []byte(fmt.Sprintf("0 %d 1\n", os.Getuid())), 0o600); err != nil {
		return fmt.Errorf("writing /proc/self/uid_map: %w", err)
	}
	return nil
}

// buildRoot constructs the chroot target: a fresh tmpfs with every system root, every
// --read and every --write bound at its own absolute path, read-only or read-write.
// The command therefore sees its own --write at the path the caller named, and sees
// nothing else.
func buildRoot(plan linuxPlan) (string, error) {
	if len(plan.Writes) == 0 {
		return "", fmt.Errorf("no --write: a command with no writable directory is a misconfiguration, not a tighter wall")
	}
	root := filepath.Join(plan.Writes[0], profileFilePrefx+"linux-root")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("creating the wall's root: %w", err)
	}
	if err := syscall.Mount("tmpfs", root, "tmpfs", 0, "size=64m"); err != nil {
		return "", fmt.Errorf("mounting tmpfs at %s: %w", root, err)
	}

	bind := func(src, dst string, ro bool) error {
		if err := os.MkdirAll(dst, 0o755); err != nil {
			return err
		}
		if err := syscall.Mount(src, dst, "", syscall.MS_BIND|syscall.MS_REC, ""); err != nil {
			return err
		}
		if ro {
			return syscall.Mount(src, dst, "", syscall.MS_BIND|syscall.MS_REMOUNT|syscall.MS_RDONLY|syscall.MS_REC, "")
		}
		return nil
	}

	// System roots are read-only and skip-if-absent, exactly as the spec's linux root
	// table names them; only a --write is refused above for warts, and --write's absence
	// was already a refusal in Build.
	for _, sysroot := range []string{"/usr", "/bin", "/sbin", "/lib", "/lib64", "/etc", "/opt"} {
		if fi, err := os.Stat(sysroot); err == nil && fi.IsDir() {
			if err := bind(sysroot, filepath.Join(root, sysroot), true); err != nil {
				return "", fmt.Errorf("binding %s: %w", sysroot, err)
			}
		}
	}
	// /proc read-only; /dev is a fresh tmpfs carrying bind-mounted device nodes, so a
	// command can write /dev/null without the host's /dev leaking through read-only.
	if err := syscall.Mount("proc", filepath.Join(root, "proc"), "proc", 0, ""); err != nil {
		return "", fmt.Errorf("mounting /proc: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "dev"), 0o755); err == nil {
		if err := syscall.Mount("tmpfs", filepath.Join(root, "dev"), "tmpfs", 0, ""); err == nil {
			for _, node := range []string{"null", "zero", "full", "random", "urandom", "tty"} {
				host := "/dev/" + node
				if _, err := os.Stat(host); err != nil {
					continue
				}
				dst := filepath.Join(root, "dev", node)
				_ = os.WriteFile(dst, nil, 0o666)
				_ = syscall.Mount(host, dst, "", syscall.MS_BIND, "")
			}
		}
	}

	for _, r := range plan.Reads {
		if err := bind(r, filepath.Join(root, r), true); err != nil {
			return "", fmt.Errorf("binding --read %s: %w", r, err)
		}
	}
	for _, w := range plan.Writes {
		if err := bind(w, filepath.Join(root, w), false); err != nil {
			return "", fmt.Errorf("binding --write %s: %w", w, err)
		}
	}
	return root, nil
}
