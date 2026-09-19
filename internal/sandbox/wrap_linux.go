//go:build linux

// The linux body: Landlock, the LSM an unprivileged process can apply to itself, which
// the kernel inherits across fork(2) and execve(2) so that the command cannot lift it.
// docs/SPEC-SANDBOX.md, "Linux — Landlock, no root" is normative.
//
// THE SHAPE IS RESTRICT-THEN-FORK, AND IT IS NOT THE SHAPE THE SPEC'S PROPOSAL NAMED.
// The proposal was restrict-then-exec IN PLACE: the tool applies the ruleset to itself
// and then syscall.Exec's the command, becoming it, so Run never returns. That cannot be
// this body, and the reason is `probe`: probeVerb runs FOUR walled steps in ONE process
// and reads the status of each (cmd/nova-sandbox/main.go, walled()), so a Run that never
// returns turns the probe into its own first step and the other three never happen. The
// spec's own rule 10 requires those four, so the proposal and rule 10 could not both be
// kept and rule 10 is the one with tests.
//
// So: the tool restricts ITSELF, then starts the command as a child and waits, exactly
// as the darwin body waits on sandbox-exec's child. The process count is the same as
// darwin's -- tool plus command, no helper -- so this is NOT the "re-exec helper with a
// hidden flag" the spec considered and rejected; there is no second process and no
// hidden verb. What it costs is that the tool's own process is walled too from the
// moment Run restricts, which is why Run is documented below as one-way.
package sandbox

import (
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"syscall"
)

// Backend is what the SANDBOX OK line names on this platform.
const Backend = "landlock"

// linuxReadRoots is the spec's linux roots table: read-only, and SKIPPED IF ABSENT (no
// /lib64 on a pure-arm64 image, no /opt on a minimal one). Rule 5 refuses a CALLER's
// missing path; a missing root is the machine's shape, not the caller's mistake.
//
// /run/systemd/resolve is here because the resolver and TLS need it (issue #893): a
// harness that cannot resolve a name inside the sandbox is a sandbox bug, not a network
// one. It is part of this one table, enforced by addRules, and not switchable.
//
// /proc, NOT /proc/self: a /proc/self opened O_PATH resolves at open time to the pid
// that opened it -- the tool's -- so a rule built on it would grant the wrapped process
// its own /proc entry and grant every child it spawns nothing.
var linuxReadRoots = []string{"/usr", "/bin", "/sbin", "/lib", "/lib64", "/etc", "/run/systemd/resolve", "/opt", "/dev", "/proc"}

// linuxWriteFiles is the rest of that table: /dev is READ-only above, and these two are
// the writable exceptions in it. Measured, not assumed -- with /dev read-only and these
// two missing, `sh -c 'echo x > /dev/null'` inside the wall is "cannot create /dev/null:
// Permission denied", which is a wall that denies the work.
var linuxWriteFiles = []string{"/dev/null", "/dev/tty"}

// available is the seam the ABI decisions are tested through, as on darwin: a test replaces
// it and the tool must then behave as the reported number demands. What it carries here is
// the DISCOVERED ABI rather than a yes-or-no, because two of the three answers depend on
// the number -- an ABI above maxKnownABI is CLAMPED to the table and said so
// (TestNewerLandlockABIIsClampedToTheTableOnLinux), an ABI below minKnownABI is refused
// (TestLandlockABIBelowTheTableRefusesOnLinux), and no landlock at all is rule 1's
// no_sandbox refusal (TestNoLandlockRefusesOnLinux). No kernel on the fleet reports any of
// the three, so without the seam none of them has a test on the platform whose body is built.
var available = landlockABI

// Available answers rule 1's question for this machine. The string is what the check
// verb appends to its note, so it names the kernel rather than a path: there is no
// binary to point at, the backend is in the running kernel or it is nowhere.
func Available() (string, bool) {
	abi, ok := available()
	if !ok {
		return "", false
	}
	return "the running kernel's landlock LSM, abi " + strconv.Itoa(abi), true
}

// ABI is the abi= field. On linux it is the discovered Landlock version, and "-" when
// there is no Landlock to have one. It is a function and not a const because this is the
// one platform whose answer is the machine's rather than the build's.
func ABI() string {
	abi, ok := available()
	if !ok {
		return "-"
	}
	return strconv.Itoa(abi)
}

// ClampedABI is the abi= field's companion: the ABI the wall is actually BUILT at, and
// whether that is below the one the kernel reports. The tool prints `used=<n>` only when
// the two differ, so the line on an ordinary machine is the line it has always been.
func ClampedABI() (int, bool) {
	abi, ok := available()
	if !ok {
		return 0, false
	}
	return wallABI(abi)
}

// NetEnforceable is rule 7 for this platform: TCP bind/connect arrived at ABI 4, so a
// kernel below it cannot enforce --net-deny and must refuse rather than pretend.
func NetEnforceable() bool {
	abi, ok := available()
	if !ok {
		return false
	}
	// The wall's ABI, not the kernel's: the clamp is what the ruleset is built at, and
	// answering from a number the ruleset will not use is how a promise gets made that
	// the wall does not keep. Clamping never crosses 4 downward -- the table's maximum
	// is 6 -- so this is the same answer either way today, and it stays true when it is not.
	used, _ := wallABI(abi)
	return used >= 4
}

// Note is the one clause the check verb prints about this backend.
func Note() string {
	abi, ok := available()
	if !ok {
		return "no landlock in this kernel: below 5.13, not compiled in, or not in the boot-time lsm= list"
	}
	if used, clamped := wallABI(abi); clamped {
		return "landlock abi " + strconv.Itoa(abi) + " is above this tool's table: the wall is built at abi " +
			strconv.Itoa(used) + " (clamped), which this kernel enforces as asked; the rights this abi added are not handled until the table grows"
	}
	switch {
	case abi < 4:
		return "landlock abi " + strconv.Itoa(abi) + ": filesystem only, and --net-deny is refused below abi 4"
	case abi < 6:
		return "landlock abi " + strconv.Itoa(abi) + ": net is TCP bind/connect only, and abstract unix sockets and signals are unscoped below abi 6"
	default:
		return "landlock abi " + strconv.Itoa(abi) + ": net is TCP bind/connect only; udp is unrestricted at every abi"
	}
}

// Run applies the policy and runs the command, and returns the status the tool must exit
// with. A Refusal returned here is the tool saying NO before the command ran.
//
// ONE WAY, AND SAY SO: past the restrictSelf below, THIS PROCESS is inside the wall and
// no call can take it back out -- a Landlock domain cannot be lifted, which is the
// property the whole tool rests on. A caller that runs Run twice in one process nests a
// second domain inside the first; that is what probeVerb does, and because every one of
// its walled steps uses the same policy, the nested domain is the same wall again.
// Anything a caller must do UNWALLED it must do before the first Run -- which is exactly
// why rule 10 runs write_outside_control first.
func Run(p *Policy, env []string, stdin io.Reader, stdout, stderr io.Writer, okLine func()) (int, error) {
	// Rule 2: no root. The wall is a wall for an ordinary user, and a policy applied by
	// a root process is a different thing than the one this spec describes.
	if os.Geteuid() == 0 {
		return ExitRefused, refuse("sandbox_failed", "this tool does not run as root: rule 2 is that the wall holds for an ordinary unprivileged user, and a root child is outside what this policy was measured against")
	}
	abi, ok := available()
	if !ok {
		return ExitRefused, refuse("no_sandbox", "this kernel has no landlock: it is below 5.13, or landlock is not compiled in, or it is not in the boot-time lsm= list. This tool does not run a command it cannot contain")
	}
	// BELOW the table's first row there is no ruleset this tool can describe and nothing
	// to clamp to, so this stays the ABI refusal (rule 11: no workaround here; the
	// caller's is nova-swarm run --no-sandbox). No kernel reports it -- landlockABI
	// already answers "no landlock" below 1 -- and it is here because the table has a
	// bottom as well as a top, and a number outside it must be said rather than assumed.
	if abi < minKnownABI {
		return ExitRefused, refuse("landlock_abi_unknown",
			"this kernel reports landlock abi %d and the lowest row of this tool's table is %d: there is no ruleset this build can describe for it. Update the abi table in internal/sandbox/landlock_linux.go and docs/SPEC-SANDBOX.md, or run the job with nova-swarm run --no-sandbox",
			abi, minKnownABI)
	}
	// ABOVE it is a CLAMP, not a refusal: a newer kernel accepts a ruleset built for an
	// older ABI, and the kernel's own documentation tells a program to use the highest
	// ABI it knows that is at or below the kernel's. The wall is built at the table's
	// maximum and the SANDBOX OK line carries used=<n> so the clamp is on the record.
	used, _ := wallABI(abi)
	// Rule 7: an enforced denial the backend cannot give is a refusal, never a weaker
	// wall than the caller asked for. The number that decides is the wall's, not the
	// kernel's, for the reason NetEnforceable gives.
	if p.NetDeny && used < 4 {
		return ExitRefused, refuse("net_unenforceable",
			"--net-deny needs landlock abi 4 (kernel 6.7) for TCP bind/connect and this kernel reports abi %d: this tool will not print net=denied over a network it cannot close", abi)
	}

	rulesetFd, err := createRuleset(used, p.NetDeny)
	if err != nil {
		return ExitRefused, refuse("sandbox_failed", "the landlock ruleset could not be created at abi %d: %v", used, err)
	}
	defer syscall.Close(rulesetFd)
	if err := addRules(rulesetFd, p, used); err != nil {
		return ExitRefused, refuse("sandbox_failed", "%v", err)
	}

	// The command, with its argv VERBATIM: p.Argv[0] is the resolved command and the
	// rest are the caller's, so the struct is built by hand rather than with
	// exec.Command, which would rewrite argv[0] to the path it resolved.
	cmd := &exec.Cmd{
		Path:   p.Command,
		Args:   p.Argv,
		Dir:    p.Cwd,
		Env:    env,
		Stdin:  stdin,
		Stdout: stdout,
		Stderr: stderr,
		// The descriptors above stderr, in order from fd 3: the probe's nonce pipe.
		ExtraFiles: p.Extra,
	}
	// NO Setpgid, for the reason the darwin body gives at length: the wrapped tree stays
	// in the CALLER's process group, because a swarm supervisor puts each job in a group
	// of its making and reaps that group at the deadline (SPEC-SWARM rule 11).

	// SANDBOX OK is printed and FLUSHED before the wall goes up, because past
	// restrictSelf this process is inside it.
	if okLine != nil {
		okLine()
	}

	// The restriction is per-THREAD until it is applied, and Go may move a goroutine
	// between threads at any call. LockOSThread pins this goroutine to the thread that
	// is about to be restricted so that the fork below happens on THAT thread and the
	// child inherits the domain. There is no matching UnlockOSThread on purpose: the
	// thread is restricted for good, and handing it back to the runtime's pool would
	// hand an unrelated goroutine a wall it never asked for. Go destroys a thread whose
	// goroutine exits still locked, which is the disposal wanted.
	runtime.LockOSThread()
	if err := restrictSelf(rulesetFd); err != nil {
		return ExitRefused, refuse("sandbox_failed", "landlock_restrict_self at abi %d: %v", used, err)
	}
	if err := cmd.Start(); err != nil {
		return ExitNotExecuted, refuse("sandbox_failed", "%s could not be started inside the wall: %v", p.Command, err)
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

// addRules is steps 2 and 3 of the spec's linux body: the roots and every --read get the
// read subset, and every --write gets the whole handled set.
func addRules(rulesetFd int, p *Policy, abi int) error {
	read := uint64(fsReadSubset)
	write := writeSubset(abi)

	// The roots, read-only, skipped if absent.
	for _, root := range linuxReadRoots {
		if err := addPathRule(rulesetFd, root, read); err != nil {
			continue // absent on this image, or not a directory: the table is skip-if-absent
		}
	}
	// The two writable device files of the roots table. They are FILES, so they get the
	// file mask: a rule on a non-directory carrying a directory-only right is EINVAL and
	// adds nothing at all (see fsFileSubset). Absent is fine -- a detached service has no
	// /dev/tty -- so an error here is skipped rather than refused, which is exactly why
	// the mask above has to be right: this loop cannot tell a missing file from a
	// malformed rule, and it was a malformed rule that let `> /dev/null` be denied.
	for _, dev := range linuxWriteFiles {
		_ = addPathRule(rulesetFd, dev, fileWriteSubset(abi))
	}
	// The optional roots the policy resolved for this machine, plus the directory of the
	// resolved command, read-only.
	for _, root := range p.OptRoots {
		_ = addPathRule(rulesetFd, root, read)
	}
	// The caller's paths. These are rule 5 paths: Build resolved them and proved they
	// exist, so a failure HERE is real and is a refusal, not a skip.
	for _, dir := range p.Reads {
		if err := addPathRule(rulesetFd, dir, read); err != nil {
			return refuse("sandbox_failed", "--read %s could not be added to the landlock ruleset: %v", dir, err)
		}
	}
	// The no-exec read set: the same rule 5 treatment, minus EXECUTE.
	for _, dir := range p.ReadsNoExec {
		if err := addPathRule(rulesetFd, dir, uint64(fsReadNoExecSubset)); err != nil {
			return refuse("sandbox_failed", "--read-noexec %s could not be added to the landlock ruleset: %v", dir, err)
		}
	}
	for _, dir := range writePaths(p) {
		if err := addPathRule(rulesetFd, dir, write); err != nil {
			return refuse("sandbox_failed", "--write %s could not be added to the landlock ruleset: %v", dir, err)
		}
	}
	return nil
}

// writePaths is every directory the policy grants writing in: the --write set, plus the
// temp directory and the cwd. Those two are normally inside the first --write and the
// duplicate rule is harmless, but --tmp and --cwd can name a directory of their own and
// a wall that denies the job's own temp directory is a wall that denies the work.
func writePaths(p *Policy) []string {
	out := append([]string{}, p.Writes...)
	for _, extra := range []string{p.Tmp, p.Cwd} {
		if extra == "" {
			continue
		}
		seen := false
		for _, w := range out {
			if w == extra {
				seen = true
				break
			}
		}
		if !seen {
			out = append(out, extra)
		}
	}
	return out
}
