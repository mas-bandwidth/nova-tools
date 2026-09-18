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
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
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
	// List is every volume on this machine whose name begins with the run verb's own
	// prefix, with the disk and the mount point of each. It is what the reap verb reads,
	// and it is on this interface rather than beside it because one seam means one fake
	// and one place where a volume can be named.
	List() ([]diskVolume, error)
	// Create exports a new volume with a quota and returns it mounted.
	Create(container, name, size string) (diskVolume, error)
	// Used is the bytes the volume holds, asked BEFORE the delete, because after it
	// there is nothing to ask.
	Used(mount string) (int64, error)
	// Delete unmounts and removes the volume. An error here is a LEAK, not a retry.
	Delete(disk string) error
}

// startedRun is what the executor hands back: the channel the status arrives on, the
// function that signals the WHOLE group, and the leader's pid — which is the floor the
// denial reader filters a shared machine's log by.
type startedRun struct {
	done <-chan int
	kill func(syscall.Signal)
	pid  int
}

// The seams. Each is a var so a test can replace it, and none of them is reachable from
// caller input.
var (
	runVolumes volumeManager = newPlatformVolumes()
	runExec                  = startInOwnGroup
	runNow                   = time.Now
	runSignals               = notifyTerminating
	// runGOOS is the platform this verb believes it is on. It is a var for the same reason
	// internal/sandbox's winDir is a function of the platform rather than of runtime.GOOS:
	// the windows half of this verb cannot be run on a Mac, and a test that only ever walks
	// the darwin path calls a windows bug green. The estate has no Windows bench
	// (2026-09-18), so this seam is the only thing standing between "the windows rules are
	// written down" and "the windows rules are exercised".
	runGOOS = runtime.GOOS
)

// runFlags is the run verb's own argv, parsed by hand like the bare form's.
type runFlags struct {
	name, size, container, timeout string
	// scratch, memory, cpu and place are the windows half's, docs/SPEC-SANDBOX.md W4, W5
	// and W9. They are PARSED on every platform, because a caller writes one argv for
	// three platforms and reads one grammar back; what each platform does with them is
	// validateRun's business.
	scratch, memory, cpu string
	place                string
	reads                []string
	argv                 []string
	useGo                bool
	help                 bool
	sawDashDash          bool
	bad                  []sandbox.Refusal
}

// The two --place values. `job` is W1's Job Object plus scratch and is the default on
// windows; `wsb` is W8's Windows Sandbox, one instance per machine, for the one-off.
const (
	placeJob = "job"
	placeWSB = "wsb"
)

// limits is W4's caps in the job's own units, read off the flags after validateRun has
// already accepted their shapes. A flag that was not given is a zero, and a zero limit is
// NOT SET: an unset limit is the machine's, and a limit set to "everything" is a number
// this tool would have had to invent.
func (f runFlags) limits() winLimits {
	var l winLimits
	if f.memory != "" {
		l.MemoryBytes, _ = parseBytes(f.memory)
	}
	if f.cpu != "" {
		l.CPUPercent, _ = strconv.Atoi(f.cpu)
	}
	return l
}

// runUsage is the verb's own banner. It exists because `nova-sandbox run --help` printed
// FOUR REFUSALS and exit 125 — one for the missing --name, one for the missing --size, one
// for --help not being a flag of the verb, one for the missing -- (measured 2026-09-18 by
// a non-author dogfooding the verb). ONBOARDING.md point 2 puts the banner behind `help`
// rather than in front of every mistake; asking how to use a verb is not a mistake, and a
// tool that answers the question with four complaints teaches the reader to stop asking.
const runUsage = `nova-sandbox run: one command, in a DISPOSABLE place that is deleted on exit (darwin)

usage:
  nova-sandbox run --name <n> --size <8g> [--timeout <30m>] [--go] [--read <dir>]...
                   [--container <disk>] -- <command> <args...>

  --name <n>      the volume is nova-<n>, mounted at /Volumes/nova-<n>. Letters,
                  digits, - _ and . REQUIRED.
  --size <s>      the volume's quota, e.g. 8g or 64m. REQUIRED: a disposable place
                  with no ceiling can fill the boot disk.
  --timeout <d>   a Go duration after which the whole process group is killed and
                  the volume deleted anyway. Exit 124.
  --go            add the Go toolchain's own roots as --read: GOROOT and GOMODCACHE,
                  as ` + "`go env`" + ` reports them. A toolchain outside the roots the
                  profile already grants is unreadable inside the wall, and a module
                  cache lives under the caller's home, which the wall denies -- so a
                  card that builds Go wants this flag, and the alternative is naming
                  both by hand in every argv.
  --read <dir>    readable, recursively, and NOT writable. Repeatable.
  --container <d> the APFS container to make the volume in. Default: the container
                  the boot volume is in.

windows (docs/SPEC-SANDBOX.md, "Windows -- the disposable place", W1..W12):
  --scratch <d>   REQUIRED on windows, refused elsewhere: an existing absolute path
                  the per-run directory <scratch>\nova-<n> is made under. There is
                  no default: not the TEMP variable, not the user profile.
  --size          REFUSED on windows: NTFS has no per-directory ceiling this tool
                  can enforce without administrator rights, and a ceiling the tool
                  only measures is not a ceiling. Use --place wsb, or name a
                  --scratch on a volume you have already sized.
  --memory <s>    the Job Object's memory cap, e.g. 4g. Accepted and IGNORED on
                  darwin and linux, so one caller builds one argv for three
                  platforms.
  --cpu <n>       the Job Object's hard CPU cap, 1..100, as a percentage of one
                  machine's total cycles. Accepted and ignored off windows.
  --place job|wsb job (the default) is a Job Object plus the scratch. wsb is
                  Windows Sandbox: full disposability, Pro and Enterprise only, ONE
                  INSTANCE PER MACHINE -- the review place, never the swarm's -- and
                  it requires --timeout, because the guest's status comes back
                  through a file or not at all.

The volume is the run's ONLY writable directory: the working directory is
<volume>/work, HOME is <volume>/home and TMPDIR is on it too. On exit -- normal,
error, signal or --timeout -- the whole process group is killed and the volume is
unmounted and DELETED, so there is no cleanup step. A delete that fails prints
SANDBOX LEAK with the one command that removes it and exits 3.

When a contained command exits non-zero, the tool asks the operating system what it
refused and prints one SANDBOX DENIED line per path, with the flag that would have
allowed it. docs/SPEC-SANDBOX.md says what that can and cannot see on this macOS.

example:
  nova-sandbox run --name card1 --size 8g --timeout 30m --go \
                   -- /bin/sh -c 'cd repo && go build ./...'
`

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
		case "--scratch":
			f.scratch = want("--scratch")
		case "--memory":
			f.memory = want("--memory")
		case "--cpu":
			f.cpu = want("--cpu")
		case "--place":
			f.place = want("--place")
		case "--read":
			if v := want("--read"); v != "" {
				f.reads = append(f.reads, v)
			}
		case "--go":
			f.useGo = true
		case "help", "--help", "-h":
			f.help = true
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

// goDirs is what `go env` answers about the toolchain: the root it was installed at and
// the module cache it downloads into. Neither is a path the caller typed, and neither is
// guessed — both come from the toolchain itself.
type goDirs struct{ Root, ModCache string }

// runGoEnv is the seam --go reaches the toolchain through, so a test asks a function and
// never a machine's real Go.
var runGoEnv = readGoEnv

// readGoEnv asks the go on the caller's PATH where it lives. The child's environment is
// goenv.Clean's, because this reads a go command's OUTPUT: a GOFLAGS=-json inherited from
// a Makefile would turn these two lines into a JSON document and the paths below into
// nonsense (internal/goenv, and the class test that enforces it).
func readGoEnv() (goDirs, error) {
	bin, err := exec.LookPath("go")
	if err != nil {
		return goDirs{}, err
	}
	cmd := exec.Command(bin, "env", "GOROOT", "GOMODCACHE")
	cmd.Env = goenv.Clean(os.Environ())
	out, err := cmd.Output()
	if err != nil {
		return goDirs{}, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return goDirs{}, fmt.Errorf("go env answered %d lines, want GOROOT and GOMODCACHE", len(lines))
	}
	return goDirs{Root: strings.TrimSpace(lines[0]), ModCache: strings.TrimSpace(lines[1])}, nil
}

// applyGoReads is --go: the toolchain's own roots, added to the read set.
//
// Measured 2026-09-18, dogfooding the verb on a real card step: a `go build` inside the
// wall died with `go: cannot find GOROOT directory: 'go' binary is trimmed and GOROOT is
// not set`, which names nothing about a sandbox. The root cause of THAT one is fixed in
// the optional roots' ancestors, but the class remains — a toolchain installed anywhere
// the profile's root table does not already cover is unreadable inside the wall, and the
// module cache lives under the caller's home, which the wall denies by design.
//
// A path that is not there is SKIPPED with a note, not refused: rule 5's
// refusal-for-absence is about the paths the CALLER named, and an empty module cache on a
// machine that has never downloaded a module is not a misconfiguration.
func applyGoReads(f *runFlags, stderr io.Writer) *sandbox.Refusal {
	if !f.useGo {
		return nil
	}
	dirs, err := runGoEnv()
	if err != nil {
		return &sandbox.Refusal{Reason: "bad_read",
			Text: "--go asks the go on this PATH where its roots are, and there is no go to ask: " + oneline.Err(err) + ". Install go, or name the roots yourself with --read"}
	}
	var added, skipped []string
	for _, dir := range []string{dirs.Root, dirs.ModCache} {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			skipped = append(skipped, dir)
			continue
		}
		f.reads = append(f.reads, dir)
		added = append(added, dir)
	}
	if len(added) > 0 {
		fmt.Fprintf(stderr, "SANDBOX NOTE --go added %d read root(s) from go env: %s\n",
			len(added), oneline.Escape(strings.Join(added, " ")))
	}
	if len(skipped) > 0 {
		fmt.Fprintf(stderr, "SANDBOX NOTE --go skipped %s: go env names it and it is not there, so there is nothing to grant\n",
			oneline.Escape(strings.Join(skipped, " ")))
	}
	return nil
}

// runVerb is the verb. It returns the status the tool exits with: the command's own,
// 124 for a --timeout, 125 for a refusal of the tool's own, and 3 for a leak.
func runVerb(args []string, stdin io.Reader, stdout, stderr io.Writer, env []string) int {
	f := parseRun(args)
	// The question, before every complaint about the argv that did not ask it.
	if f.help {
		fmt.Fprint(stdout, runUsage)
		return 0
	}
	goos := runGOOS
	deadline, bad := validateRun(&f, goos)
	f.bad = append(f.bad, bad...)
	if len(f.bad) > 0 {
		code := refuseAll(stderr, f.bad)
		fmt.Fprintln(stderr, remedyFor(goos))
		return code
	}
	// Rule 1's shape for this verb: a platform whose disposable place is not built REFUSES,
	// and the refusal says where the disposable place is on that platform instead. A run
	// that quietly worked in an ordinary directory would leave exactly the debt the verb
	// abolishes.
	if line, remedy, refused := noDisposableBody(goos); refused {
		fmt.Fprintln(stderr, line)
		fmt.Fprintln(stderr, remedy)
		return sandbox.ExitRefused
	}
	// --go is resolved AFTER the platform is known and BEFORE anything is made: its two
	// roots are ordinary reads by the time the policy is built, so nothing below this line
	// knows the flag exists.
	if r := applyGoReads(&f, stderr); r != nil {
		code := refuseAll(stderr, []sandbox.Refusal{*r})
		fmt.Fprintln(stderr, remedyFor(goos))
		return code
	}
	if goos == "windows" {
		return runDisposableWindows(f, deadline, stdin, stdout, stderr, env)
	}
	return runDisposable(f, deadline, stdin, stdout, stderr, env)
}

// remedyFor is the one remedy line a refusal carries, and it is the PLATFORM'S. A windows
// reader handed the darwin argv would type `--size 8g`, which W6 refuses, and would not type
// the `--scratch` W5 requires: a remedy that names the wrong flags is worse than none,
// because a reader trusts it.
func remedyFor(goos string) string {
	if goos == "windows" {
		return winRemedy
	}
	return runRemedy
}

// validateRun is every check on the argv that does not touch the machine, WITH THE PLATFORM
// NAMED. The platform is a parameter and not runtime.GOOS because the windows rules cannot
// be run on a Mac and the estate has no Windows bench: this is how a darwin `go test` asks
// what the tool says on windows, the same way internal/sandbox's winpath_test.go does.
//
// The contract does not change across the three (docs/SPEC-SANDBOX.md, W-preamble): the same
// verb, the same receipt, the same exit codes. What differs is here and nowhere else.
func validateRun(f *runFlags, goos string) (time.Duration, []sandbox.Refusal) {
	var bad []sandbox.Refusal
	add := func(reason, text string) { bad = append(bad, sandbox.Refusal{Reason: reason, Text: text}) }
	win := goos == "windows"

	if !okName(f.name) {
		add("no_name", "--name wants one short name out of letters, digits, - _ and . : it becomes the volume nova-<n> and the directory under /Volumes")
	}

	// W6. --size is REFUSED on windows, because NTFS quotas are per user per volume, a
	// directory quota is FSRM (a server role) and a per-run quota is a VHDX, which needs
	// administrator rights that rule 2 forbids this tool from requiring. A ceiling the tool
	// only MEASURES is not a ceiling, and the precedent is rule 7's net_unenforceable: a
	// promise this tool cannot enforce is a refusal, never a note.
	switch {
	case win && f.size != "":
		add("size_unenforceable", "--size cannot be enforced on windows: NTFS quotas are per user per volume, a directory quota is FSRM (a server role) and a per-run quota is a VHDX that needs administrator rights this tool does not take. Two remedies: run under --place wsb, whose whole disk is discarded, or name a --scratch on a volume you have already sized")
	case !win && !okSize(f.size):
		add("bad_size", "--size wants the volume's quota, a number with an optional k, m, g or t: --size 8g")
	}

	// W5. --scratch is REQUIRED on windows and must be an absolute path: there is no
	// default, no %TEMP% and no %USERPROFILE%. A disposable place the tool chose the
	// location of is a place the caller cannot put on the volume they meant.
	switch {
	case win && f.scratch == "":
		add("bad_scratch", "--scratch is required on windows: it is the existing absolute path the per-run directory <scratch>\\nova-<n> is made under, and there is no default -- not %TEMP%, not %USERPROFILE%. Name one: --scratch C:\\nova")
	case win && !absolutePathFor(goos, f.scratch):
		add("bad_scratch", "--scratch wants an EXISTING ABSOLUTE path: --scratch C:\\nova. A relative one is resolved against a working directory this verb is about to replace")
	case !win && f.scratch != "":
		add("bad_scratch", "--scratch is the windows half's flag: on darwin the disposable place is an APFS volume made by the tool and there is nothing for it to be made under. Drop it, or run this on windows")
	}

	// W4. --memory and --cpu are the JOB's caps and they are accepted and IGNORED off
	// windows, the way --name already is, so one caller builds one argv for three
	// platforms. Their SHAPES are checked everywhere: a typo that is silently ignored on a
	// Mac and refused on a bench is a bug found on the wrong machine.
	if f.memory != "" {
		if n, ok := parseBytes(f.memory); !ok || n <= 0 {
			add("bad_memory", "--memory wants a positive quantity, a number with an optional k, m, g or t: --memory 4g. It is the Job Object's ProcessMemoryLimit and JobMemoryLimit on windows, and is accepted and ignored elsewhere")
		}
	}
	if f.cpu != "" {
		n, err := strconv.Atoi(f.cpu)
		if err != nil || n < 1 || n > 100 {
			add("bad_cpu", "--cpu wants a whole percentage of one machine's cycles, 1..100: --cpu 50. It is the Job Object's hard CPU rate cap on windows, and is accepted and ignored elsewhere")
		}
	}

	// W9. The default on windows is --place job, because Windows Sandbox permits ONE
	// running instance per machine and a pool of workers each wanting one is a queue of one.
	if f.place == "" {
		f.place = placeJob
	}
	switch f.place {
	case placeJob:
	case placeWSB:
		if !win {
			add("no_wsb", "--place wsb is Windows Sandbox and there is none on "+goos+": the disposable place here is the platform's own. Drop --place, or run this on windows")
		}
	default:
		add("bad_place", "--place wants job or wsb: job is a Job Object plus the per-run scratch and is the default, wsb is Windows Sandbox -- full disposability, one instance per machine, the review place and never the swarm's")
	}

	var deadline time.Duration
	if f.timeout != "" {
		d, err := time.ParseDuration(f.timeout)
		if err != nil || d <= 0 {
			add("bad_timeout", "--timeout wants a positive Go duration: --timeout 30m")
		} else {
			deadline = d
		}
	}
	// W10. --timeout is REQUIRED under wsb. WindowsSandbox.exe returns as soon as the VM is
	// up and carries no guest status, so the command's exit comes back through a file in the
	// mapped folder; without a deadline a guest that never writes the file is a wait with no
	// end, and this verb never waits without one.
	if win && f.place == placeWSB && deadline == 0 && f.timeout == "" {
		add("bad_timeout", "--place wsb requires --timeout: WindowsSandbox.exe returns as soon as the VM is up and carries no guest status, so the command's exit comes back through a file in the mapped folder -- and a guest that never writes it is a wait with no end. Name the deadline: --timeout 30m")
	}

	if f.container != "" {
		if win {
			add("no_container", "--container is darwin's APFS container reference; windows makes no volume, so there is no container to name. The place is --scratch")
		} else if !okContainer(f.container) {
			add("no_container", "--container wants an APFS container reference: --container disk3")
		}
	}

	if !f.sawDashDash {
		add("no_command", "no --; the command comes after it: "+remedyFor(goos))
	} else if len(f.argv) == 0 {
		add("no_command", "nothing after --; the run verb wraps one command")
	}
	return deadline, bad
}

// absolutePathFor is "is this an absolute path on THAT platform", with the platform named.
// filepath.IsAbs answers for the host, and the host here is a Mac: `C:\nova` is a relative
// path to it and `/nova` is an absolute one, which is both answers exactly backwards for the
// argv this verb is judging.
func absolutePathFor(goos, p string) bool {
	if p == "" {
		return false
	}
	if goos != "windows" {
		return strings.HasPrefix(p, "/")
	}
	q := strings.ReplaceAll(p, "/", `\`)
	if strings.HasPrefix(q, `\\`) { // a UNC share is absolute
		return true
	}
	// A drive-qualified path is absolute only WITH the separator: `C:nova` is relative to
	// the current directory ON drive C, which is a different directory per drive and not a
	// place this verb can be asked to make anything under.
	return len(q) >= 3 && q[1] == ':' && q[2] == '\\' &&
		((q[0] >= 'a' && q[0] <= 'z') || (q[0] >= 'A' && q[0] <= 'Z'))
}

// parseBytes is --memory's number in bytes. It takes the same shapes okSize takes, because
// a caller who learned --size 8g should not have to learn a second spelling for --memory.
func parseBytes(s string) (int64, bool) {
	if !okSize(s) {
		return 0, false
	}
	body := strings.TrimSuffix(strings.TrimSuffix(s, "b"), "B")
	mult := int64(1)
	if n := len(body); n > 0 {
		switch body[n-1] {
		case 'k', 'K':
			mult, body = 1<<10, body[:n-1]
		case 'm', 'M':
			mult, body = 1<<20, body[:n-1]
		case 'g', 'G':
			mult, body = 1<<30, body[:n-1]
		case 't', 'T':
			mult, body = 1<<40, body[:n-1]
		}
	}
	f, err := strconv.ParseFloat(body, 64)
	if err != nil || f <= 0 {
		return 0, false
	}
	return int64(f * float64(mult)), true
}

// noDisposableBody is rule 1's shape for this verb, with the platform NAMED so that a
// test on a Mac can ask what the tool says on linux. A platform whose disposable place is
// not built REFUSES and the refusal says where the disposable place is there instead: a
// run that quietly worked in an ordinary directory would leave exactly the cleanup debt
// the verb abolishes, and would leave it on the platform nobody was watching.
func noDisposableBody(goos string) (line, remedy string, refused bool) {
	// darwin's place is the APFS volume; windows's is W1's Job Object plus the per-run
	// scratch (runwin.go). Both are built, so neither refuses here.
	if goos == "darwin" || goos == "windows" {
		return "", "", false
	}
	return fmt.Sprintf("SANDBOX REFUSED reason=no_sandbox: the disposable place is darwin's APFS volume, made per run in the boot container and deleted on exit, or windows's Job Object plus per-run scratch; %s has no body here and this tool does not pretend an ordinary directory is one",
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
	// The owner marker, before the command starts. It is what lets `nova-sandbox reap`
	// tell this volume — a run that is working — from the one a SIGKILLed run left
	// mounted with its orphaned children still holding it open. A reaper that cannot make
	// that distinction is one nobody dares to run.
	if err := writeOwnerMarker(vol.Mount, os.Getpid()); err != nil {
		return refuse("volume_failed", "the owner marker could not be written at %s: %s", oneline.Escape(vol.Mount), oneline.Err(err))
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

	startedAt := runNow()
	started, err := runExec(p, childEnv, stdin, stdout, stderr)
	done, killGroup := started.done, started.kill
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
	// A TIMEOUT IS NOT A DENIAL, and this is the difference between the two sentences a
	// failed run can be told. Measured in the 20-run soak (Studio, 2026-09-18): a run that
	// passed its --timeout paid the bounded two-second denials query and was then told
	// "this OS reported no seatbelt denials ... add a --read, or --go" — a remedy for a
	// wall that was never in the way. The command was still working when its deadline
	// passed; nothing refused it. So the probe is skipped and the one true line is printed.
	if timedOut {
		fmt.Fprintf(stderr, "SANDBOX TIMEOUT after=%s name=%s\n", oneline.Field(deadline.String()), oneline.Field(f.name))
		return code
	}
	if code != 0 {
		reportDenials(stderr, p, started.pid, runNow().Sub(startedAt))
	}
	return code
}

// reportDenials is the answer to the silence: a command that failed is told what the wall
// refused, one line per path, with the flag that would have allowed it. It runs ONLY on a
// non-zero exit — it costs a process, and a clean run has no question to ask.
//
// The allowed set the denials are filtered against is the policy's own, so a denial on a
// path the caller already named is not reported: that is some other operation on a granted
// path, and a remedy naming a flag already in the argv sends a reader to fix what is not
// broken.
func reportDenials(stderr io.Writer, p *sandbox.Policy, pid int, ran time.Duration) {
	// The window is the run's, rounded up: the log is asked about the seconds the command
	// was alive and no more, so a neighbour's violation from before it started is not this
	// card's problem.
	window := int(ran.Seconds()) + 2
	denied, _ := step(stderr, "denials", func() ([]deniedPath, error) { return runDenials(window, pid), nil })
	allowed := append(append([]string{}, p.Reads...), p.Writes...)
	allowed = append(allowed, p.OptRoots...)
	denied = outsideTheWall(denied, allowed)
	if len(denied) == 0 {
		// Nothing to report is not the same as nothing denied, and saying so is the whole
		// difference between this run and the one that was measured. See denied.go: this
		// macOS does not report a `sandbox-exec -p` profile's violations at all.
		fmt.Fprintf(stderr, "SANDBOX NOTE the command failed and this OS reported no seatbelt denials for it; if it died on a path, the wall allowed read=%d write=%d and nothing else -- add a --read, or --go for a Go toolchain\n",
			len(p.Reads), len(p.Writes))
		return
	}
	printDenied(stderr, denied, maxDenied)
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
