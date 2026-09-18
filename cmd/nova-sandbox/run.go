// The run verb gives one command a DISPOSABLE place to work and then takes the place
// away. On darwin that place is an APFS volume of its own in the boot container, quota'd
// by --size, mounted at /Volumes/nova-<name>: it is the only directory the wall lets the
// command write to, the command runs in a process group of its own, and on exit — normal,
// error, signal or --timeout — the group is killed and the volume is UNMOUNTED AND
// DELETED. Nothing of the run survives on the boot volume, so there is no cleanup step to
// forget, no half-cleaned job directory to inherit, and no quota that leaks into the next
// card's disk.
//
// Glenn, 2026-09-18: "build our own minimal isolation and hygiene sandboxes on Mac" — the
// Mac equivalent of a container, built out of the two things macOS already has: the
// seatbelt wall this binary already applies, and an APFS volume that costs nothing to make
// and nothing to throw away.
//
// A failure to delete is the one thing this verb may not do quietly: it prints
// SANDBOX LEAK with the disk and the one command that removes it, and exits 3. A leak that
// nobody is told about is exactly the cleanup debt the verb exists to abolish.
//
// Everything the verb reaches the machine through is a package-level var — the volume
// manager (diskutil), the executor (a contained child in a process group of its own) and
// the clock — so run_test.go replaces all three and the logic is tested without touching a
// disk: create, run, ALWAYS delete, report a leak, refuse a duplicate name, kill the group
// on a timeout.
package main

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sandbox"
)

// runRemedy is the one remedy line every refusal of this verb carries.
const runRemedy = "run: nova-sandbox run --name <n> --size <8g> [--timeout <30m>] [--read <dir>]... -- <command> <args...>"

// volumePrefix is the one name shape this verb makes and the one it deletes. A volume
// whose name does not begin with it was not made here and is never touched.
const volumePrefix = "nova-"

// exitTimeout is timeout(1)'s status, and it is what --timeout costs: the command did not
// finish inside the deadline and its group was killed.
const exitTimeout = 124

// exitLeak is the status of the one failure this verb cannot repair: the volume is still
// on the disk after the run. It overrides the command's own status, because "nothing
// survives" is the whole contract and a caller that reads 0 would believe it was kept.
const exitLeak = 3

// killGrace is how long a killed group is given to die on SIGTERM before SIGKILL. It is
// production code's own wait, never a test's.
const killGrace = 2 * time.Second

// diskVolume is one disposable volume: the name it carries, the disk it is, and where it
// is mounted. Every field comes from diskutil, never from the caller.
type diskVolume struct {
	Name  string // nova-<n>, the volume's own name
	Disk  string // disk3s7, the device the delete names
	Mount string // /Volumes/nova-<n>, where the work happens
}

// volumeManager is the whole of this verb's contact with the disk. The production body is
// diskutil (volumes_darwin.go); run_test.go puts a fake here and asserts the ORDER of the
// calls, which is where the contract lives: a create is always followed by a delete.
type volumeManager interface {
	// Container names the APFS container the boot volume lives in.
	Container() (string, error)
	// Exists reports whether a volume of this name is already on the machine.
	Exists(name string) (bool, error)
	// Create exports a new volume with a quota and returns it mounted.
	Create(container, name, size string) (diskVolume, error)
	// Used is the bytes the volume holds, asked BEFORE the delete, because after it
	// there is nothing to ask.
	Used(mount string) (int64, error)
	// Delete unmounts and removes the volume. An error here is a LEAK, not a retry.
	Delete(disk string) error
}

// The three seams. Each is a var so a test can replace it, and none of them is reachable
// from caller input.
var (
	runVolumes volumeManager = newPlatformVolumes()
	runExec                  = startInOwnGroup
	runNow                   = time.Now
	runSignals               = notifyTerminating
)

// runFlags is the run verb's own argv, parsed by hand like the bare form's.
type runFlags struct {
	name, size, container, timeout string
	reads                          []string
	argv                           []string
	sawDashDash                    bool
	bad                            []sandbox.Refusal
}

func parseRun(args []string) runFlags {
	var f runFlags
	add := func(reason, text string) {
		f.bad = append(f.bad, sandbox.Refusal{Reason: reason, Text: text})
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			f.sawDashDash = true
			f.argv = append(f.argv, args[i+1:]...)
			return f
		}
		want := func(flag string) string {
			if i+1 >= len(args) {
				add("no_command", flag+" wants a value: "+flag+" <value>")
				return ""
			}
			i++
			return args[i]
		}
		switch a {
		case "--name":
			f.name = want("--name")
		case "--size":
			f.size = want("--size")
		case "--container":
			f.container = want("--container")
		case "--timeout":
			f.timeout = want("--timeout")
		case "--read":
			if v := want("--read"); v != "" {
				f.reads = append(f.reads, v)
			}
		default:
			add("no_command", oneline.Escape(a)+" is not a flag of the run verb; run: nova-sandbox help")
		}
	}
	return f
}

// okName is the shape a --name may take. It is narrow on purpose: the value becomes an
// APFS volume name, a path element under /Volumes and an argument to diskutil, and a name
// that is safe in all three is a short one out of a small alphabet.
func okName(s string) bool {
	if s == "" || len(s) > 32 {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case (r == '-' || r == '_' || r == '.') && i > 0:
		default:
			return false
		}
	}
	return true
}

// okSize is the shape a --size may take: a whole number of bytes, or a number with one of
// diskutil's unit letters. A percentage is refused — the disposable volume's ceiling is a
// number the caller chose, not a share of whatever the machine happens to have free.
func okSize(s string) bool {
	if s == "" {
		return false
	}
	digits := 0
	for i, r := range s {
		switch {
		case r >= '0' && r <= '9':
			digits++
		case r == '.' && digits > 0 && i < len(s)-1:
		case (r == 'k' || r == 'K' || r == 'm' || r == 'M' || r == 'g' || r == 'G' || r == 't' || r == 'T') && digits > 0:
			// A unit letter ends the number: what follows may only be a b or B.
			rest := s[i+1:]
			return rest == "" || rest == "b" || rest == "B"
		default:
			return false
		}
	}
	return digits > 0
}

// okContainer is the shape diskutil spells a container reference in. The value is only
// ever a machine's own answer or a caller override, and both go into a command argument.
func okContainer(s string) bool {
	rest, ok := strings.CutPrefix(s, "disk")
	if !ok || rest == "" {
		return false
	}
	for _, r := range rest {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// runVerb is the verb. It returns the status the tool exits with: the command's own,
// 124 for a --timeout, 125 for a refusal of the tool's own, and 3 for a leak.
func runVerb(args []string, stdin io.Reader, stdout, stderr io.Writer, env []string) int {
	f := parseRun(args)
	if !okName(f.name) {
		f.bad = append(f.bad, sandbox.Refusal{Reason: "no_name",
			Text: "--name wants one short name out of letters, digits, - _ and . : it becomes the volume nova-<n> and the directory under /Volumes"})
	}
	if !okSize(f.size) {
		f.bad = append(f.bad, sandbox.Refusal{Reason: "bad_size",
			Text: "--size wants the volume's quota, a number with an optional k, m, g or t: --size 8g"})
	}
	var deadline time.Duration
	if f.timeout != "" {
		d, err := time.ParseDuration(f.timeout)
		if err != nil || d <= 0 {
			f.bad = append(f.bad, sandbox.Refusal{Reason: "bad_timeout",
				Text: "--timeout wants a positive Go duration: --timeout 30m"})
		} else {
			deadline = d
		}
	}
	if f.container != "" && !okContainer(f.container) {
		f.bad = append(f.bad, sandbox.Refusal{Reason: "no_container",
			Text: "--container wants an APFS container reference: --container disk3"})
	}
	if !f.sawDashDash {
		f.bad = append(f.bad, sandbox.Refusal{Reason: "no_command",
			Text: "no --; the command comes after it: nova-sandbox run --name j1 --size 8g -- <command> <args...>"})
	} else if len(f.argv) == 0 {
		f.bad = append(f.bad, sandbox.Refusal{Reason: "no_command",
			Text: "nothing after --; the run verb wraps one command"})
	}
	if len(f.bad) > 0 {
		code := refuseAll(stderr, f.bad)
		fmt.Fprintln(stderr, runRemedy)
		return code
	}
	// Rule 1's shape for this verb: a platform whose disposable place is not built REFUSES,
	// and the refusal says where the disposable place is on that platform instead. A run
	// that quietly worked in an ordinary directory would leave exactly the debt the verb
	// abolishes.
	if line, remedy, refused := noDisposableBody(runtime.GOOS); refused {
		fmt.Fprintln(stderr, line)
		fmt.Fprintln(stderr, remedy)
		return sandbox.ExitRefused
	}
	return runDisposable(f, deadline, stdin, stdout, stderr, env)
}

// noDisposableBody is rule 1's shape for this verb, with the platform NAMED so that a
// test on a Mac can ask what the tool says on linux. A platform whose disposable place is
// not built REFUSES and the refusal says where the disposable place is there instead: a
// run that quietly worked in an ordinary directory would leave exactly the cleanup debt
// the verb abolishes, and would leave it on the platform nobody was watching.
func noDisposableBody(goos string) (line, remedy string, refused bool) {
	if goos == "darwin" {
		return "", "", false
	}
	return fmt.Sprintf("SANDBOX REFUSED reason=no_sandbox: the disposable volume is darwin's, an APFS volume in the boot container made per run and deleted on exit; %s has no body here and this tool does not pretend an ordinary directory is one",
			oneline.Field(goos)),
		"run: nova-sandbox --write <dir> -- <command> <args...>, naming the card's own image root as <dir>: on linux a card is already disposable because it runs INSIDE its image, and the image is the container",
		true
}

// runDisposable is everything from the container lookup onwards. It is one function on
// purpose: from the moment the volume exists there is exactly ONE path to the exit, and
// that path deletes it.
func runDisposable(f runFlags, deadline time.Duration, stdin io.Reader, stdout, stderr io.Writer, env []string) int {
	started := runNow()
	refuse := func(reason, format string, a ...any) int {
		fmt.Fprintf(stderr, "SANDBOX REFUSED reason=%s: %s\n%s\n", oneline.Field(reason), oneline.Escape(fmt.Sprintf(format, a...)), runRemedy)
		return sandbox.ExitRefused
	}

	container := f.container
	if container == "" {
		got, err := step(stderr, "container", func() (string, error) { return runVolumes.Container() })
		if err != nil {
			return refuse("no_container", "the APFS container of the boot volume could not be read: %s; name it with --container disk3", oneline.Err(err))
		}
		container = got
	}
	if !okContainer(container) {
		return refuse("no_container", "%s is not an APFS container reference; name it with --container disk3", oneline.Escape(container))
	}

	name := volumePrefix + f.name
	exists, err := step(stderr, "look", func() (bool, error) { return runVolumes.Exists(name) })
	if err != nil {
		return refuse("volume_failed", "the volumes on this machine could not be listed: %s", oneline.Err(err))
	}
	if exists {
		return refuse("volume_exists", "a volume named %s is already on this machine; a run never joins a place it did not make. Pick another --name, or remove it: diskutil apfs deleteVolume %s", oneline.Escape(name), oneline.Escape(name))
	}

	vol, err := step(stderr, "create", func() (diskVolume, error) { return runVolumes.Create(container, name, f.size) })
	if err != nil {
		return refuse("volume_failed", "the disposable volume could not be created in %s: %s", oneline.Escape(container), oneline.Err(err))
	}

	// ONE exit from here. Whatever the run does, the volume goes.
	code := runInVolume(f, vol, deadline, stdin, stdout, stderr, env)
	return finish(stderr, f.name, vol, code, started)
}

// runInVolume builds the wall around the volume and runs the command inside it. Its
// answer is the status the run earned; the volume's fate is not its business.
func runInVolume(f runFlags, vol diskVolume, deadline time.Duration, stdin io.Reader, stdout, stderr io.Writer, env []string) int {
	refuse := func(reason, format string, a ...any) int {
		fmt.Fprintf(stderr, "SANDBOX REFUSED reason=%s: %s\n%s\n", oneline.Field(reason), oneline.Escape(fmt.Sprintf(format, a...)), runRemedy)
		return sandbox.ExitRefused
	}
	// The two directories the volume is born with. They are the TOOL's, not the caller's,
	// so making them is not a breach of "every path is yours and none is guessed": the
	// whole volume is thrown away, so nothing made here outlives the run. work/ is the
	// working directory and home/ is rule 9's data home, kept apart so a command that
	// writes its tree does not write into its own dotfiles.
	work := filepath.Join(vol.Mount, "work")
	home := filepath.Join(vol.Mount, "home")
	for _, d := range []string{work, home} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return refuse("volume_failed", "%s could not be made on the disposable volume: %s", oneline.Escape(d), oneline.Err(err))
		}
	}

	// The wall: the volume is the ONE --write, so the only place on this machine the
	// command may write is the place that is about to be deleted. Rule 8's temp directory
	// defaults inside it, which is what puts TMPDIR on the volume too.
	p, bad := sandbox.Build(sandbox.Input{
		Reads:  f.reads,
		Writes: []string{vol.Mount},
		Cwd:    work,
		Argv:   f.argv,
		Home:   home,
	})
	if len(bad) > 0 {
		code := refuseAll(stderr, bad)
		fmt.Fprintln(stderr, runRemedy)
		return code
	}
	childEnv := withHome(sandbox.ChildEnv(env, p.Tmp), home)

	fmt.Fprintf(stderr, "SANDBOX OK backend=%s abi=%s read=%d write=%d net=%s cwd=%s cwdb64=%s ancestors=%d cmd=%s gpu=%s\n",
		oneline.Field(sandbox.Backend), oneline.Field(sandbox.ABI()), len(p.Reads), len(p.Writes),
		oneline.Field(p.Net()), oneline.Field(p.Cwd), base64Cwd(p.Cwd), p.AncestorCount(),
		oneline.Field(p.CmdName()), oneline.Field(string(p.GPUMode)))

	done, killGroup, err := runExec(p, childEnv, stdin, stdout, stderr)
	if err != nil {
		var r sandbox.Refusal
		if asRefusal(err, &r) {
			code := refuseAll(stderr, []sandbox.Refusal{r})
			fmt.Fprintln(stderr, runRemedy)
			return code
		}
		return refuse("sandbox_failed", "the contained command could not be started: %s", oneline.Err(err))
	}

	var deadlineC <-chan time.Time
	if deadline > 0 {
		timer := time.NewTimer(deadline)
		defer timer.Stop()
		deadlineC = timer.C
	}
	grace := time.NewTimer(killGrace)
	defer grace.Stop()
	sigs, stop := runSignals()
	defer stop()

	code, timedOut := supervise(done, deadlineC, grace.C, sigs, killGroup)
	if timedOut {
		fmt.Fprintf(stderr, "SANDBOX NOTE the command did not finish inside --timeout %s; its whole process group was killed and the volume goes with it\n", oneline.Field(f.timeout))
	}
	return code
}

// supervise waits for whichever of three things happens first — the command finished, the
// deadline passed, this tool was asked to stop — and in EVERY case kills the whole process
// group before it returns. The group is the unit because a command that forked a
// background child leaves that child holding the volume open, and a volume that is held
// open cannot be unmounted: the survivor would turn a clean exit into a leak.
//
// It is a function over channels rather than over a process so that the ordering — term,
// then grace, then kill, then always the sweep — is testable without a real child, a real
// signal or a real clock.
func supervise(done <-chan int, deadline, grace <-chan time.Time, sigs <-chan os.Signal, killGroup func(syscall.Signal)) (int, bool) {
	// sweep is the last act of every path below: whatever finished the run, nothing of it
	// is left running. On a group whose members have all gone this costs one ESRCH.
	sweep := func() { killGroup(syscall.SIGKILL) }
	// reap sends the group a signal and waits for the leader's status, escalating to
	// SIGKILL once the grace has passed.
	reap := func(sig syscall.Signal) int {
		killGroup(sig)
		select {
		case code := <-done:
			return code
		case <-grace:
			killGroup(syscall.SIGKILL)
			return <-done
		}
	}
	select {
	case code := <-done:
		sweep()
		return code, false
	case <-deadline:
		reap(syscall.SIGTERM)
		sweep()
		// The command's own status after a kill says only which signal killed it, and the
		// thing the caller needs to know is that the deadline is what ended it.
		return exitTimeout, true
	case s := <-sigs:
		sig, ok := s.(syscall.Signal)
		if !ok {
			sig = syscall.SIGTERM
		}
		reap(sig)
		sweep()
		return 128 + int(sig), false
	}
}

// finish is the one exit: it measures what the volume holds, deletes it, and prints the
// receipt. A delete that fails prints SANDBOX LEAK with the disk and the one command that
// removes it, and costs exit 3 whatever the command's own status was.
func finish(stderr io.Writer, name string, vol diskVolume, code int, started time.Time) int {
	freed, err := runVolumes.Used(vol.Mount)
	if err != nil {
		freed = 0
	}
	delErr := stepErr(stderr, "delete", func() error { return runVolumes.Delete(vol.Disk) })
	wall := runNow().Sub(started).Seconds()
	if delErr != nil {
		fmt.Fprintf(stderr, "SANDBOX DONE name=%s exit=%d wall=%.3f freed=%d\n", oneline.Field(name), code, wall, 0)
		fmt.Fprintf(stderr, "SANDBOX LEAK name=%s volume=%s remedy=\"diskutil apfs deleteVolume %s\"\n",
			oneline.Field(name), oneline.Field(vol.Disk), oneline.Field(vol.Disk))
		fmt.Fprintf(stderr, "SANDBOX NOTE the volume could not be deleted: %s; it is still on this machine and the run did not end clean\n", oneline.Err(delErr))
		return exitLeak
	}
	fmt.Fprintf(stderr, "SANDBOX DONE name=%s exit=%d wall=%.3f freed=%d\n", oneline.Field(name), code, wall, freed)
	return code
}

// step runs one thing that takes real time and says so on stderr when it does. The line
// is printed BEFORE the step, because a step over 0.1s is exactly the one a reader is
// waiting on and a line printed after it arrives too late to be progress.
func step[T any](stderr io.Writer, name string, do func() (T, error)) (T, error) {
	fmt.Fprintf(stderr, "SANDBOX STEP name=%s state=start\n", oneline.Field(name))
	at := runNow()
	got, err := do()
	fmt.Fprintf(stderr, "SANDBOX STEP name=%s state=done ms=%d\n", oneline.Field(name), runNow().Sub(at).Milliseconds())
	return got, err
}

// stepErr is step for the one step whose answer is only whether it worked.
func stepErr(stderr io.Writer, name string, do func() error) error {
	_, err := step(stderr, name, func() (struct{}, error) { return struct{}{}, do() })
	return err
}

// withHome puts the job's data home in the child's environment. ChildEnv leaves HOME
// alone — it is the caller's to point — and here the tool points it, because the only
// writable directory is one the tool just made.
func withHome(env []string, home string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if n, _, _ := strings.Cut(kv, "="); n == "HOME" {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "HOME="+home)
}

// base64Cwd is the cwdb64= receipt of SPEC-SANDBOX's output grammar: the raw path bytes
// the wall applied, strict base64url and no padding, because oneline's escape is not
// injective and the readable field cannot be reversed to the bytes.
func base64Cwd(cwd string) string { return base64.RawURLEncoding.EncodeToString([]byte(cwd)) }

// notifyTerminating is the production signal seam: SIGINT and SIGTERM, and a stop that
// puts the handlers back.
func notifyTerminating() (<-chan os.Signal, func()) {
	c := make(chan os.Signal, 4)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
	return c, func() { signal.Stop(c) }
}
