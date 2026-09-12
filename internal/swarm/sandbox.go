package swarm

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// THE LAUNCH SEAM: every job runs inside nova-sandbox.
//
// docs/SPEC-SANDBOX.md is normative for what the wall is; this file is the DISPATCHER
// CALLER of its "two callers" section, and nothing more. Its shape is that section's, rule
// by rule:
//
//	the write set   the job directory FIRST and the per-job data home, and nothing else.
//	                Derived per job by the dispatcher and NEVER configurable by the task
//	                text: a task file that names a directory buys nothing, because the argv
//	                is built here from the job this tool created (rule 4's order is
//	                load-bearing -- the first --write is what the cwd and the temp
//	                directory default to).
//	the read set    the worker home -- the slot directory, which holds the harness config
//	                this tool writes and the worker's own AGENTS.md -- and any toolchain
//	                root the worker description named in `read_roots`. A toolchain in a
//	                user directory is exactly a caller-supplied read-only root, and it is
//	                named once per worker rather than per job, so N workers read one copy.
//	the cwd         the job directory (rule 13). A cwd outside every named path denies
//	                getcwd(3) and every git command dies before it reads anything.
//	HOME            the per-job data home, which is inside the write set (rule 9). A HOME
//	                outside every --write is SANDBOX REFUSED reason=home_outside, and the
//	                wall that let the job start and killed its first git command is the
//	                silent sandbox rule 1 exists to prevent.
//	the key         read as DATA by this tool before the wrap and passed by environment
//	                (SPEC-SWARM rule 6, SPEC-SANDBOX rule 6). The key FILE is in neither
//	                list, so the job cannot read it even if it is told to.
//	the network     --net-deny is NEVER passed: the provider's API is the work, and the
//	                line says net=nopromise.
//
// WHY THE BINARY AND NOT THE PACKAGE. internal/sandbox is in this repository and
// sandbox.Run would link straight in, and the spec's own caller section is written in
// ARGV: "every worker's argv carries that checkout as --read and the worker home as
// --read", "a task text naming a directory does not change the worker's --write argv --
// the test plants one and compares the argv" (test 24), `nova-sandbox probe` run once by
// `run` (test 23), and `nova-sandbox grant`/`release` run by the dispatcher on windows.
// Three further reasons, each of them about this tool rather than about taste:
//
//  1. sandbox.Run BLOCKS until the command ends and hands back only a status. The launch
//     transaction needs the child's pid, its process group and its start stamp at the one
//     moment they are not in doubt (supervise.go), and it needs to reap that group at a
//     deadline. An exec'd binary gives the supervisor the same *exec.Cmd it has always
//     had, and rule 18's transaction is untouched.
//  2. The wall is then the SAME BINARY a person runs by hand, with the same argv in `ps`,
//     which is what makes a failed job diagnosable: a reader copies the line out of the
//     process table and runs it.
//  3. A linked-in wall would make this tool refuse to BUILD its own launch on a platform
//     whose body is not built; an exec'd one refuses at the probe, once, with a line that
//     names the remedy.
//
// The cost is one process per job and one PATH lookup per run, and it is paid once at a
// launch that already costs a fork and a handshake.

// SandboxBinary is the name this tool looks for on PATH when --sandbox names no path. It is
// the tool's OWN name and not a path: a default PATH lookup of a named command is what this
// tool already does for the harness, and no directory is guessed.
const SandboxBinary = "nova-sandbox"

// SandboxJob is one job's wrap, and every field of it is the dispatcher's own.
type SandboxJob struct {
	Sandbox   string   // the resolved nova-sandbox binary
	PoolName  string   // the container name on windows; accepted and ignored elsewhere
	SlotDir   string   // the worker home for this job: the slot copy
	JobDir    string   // the job directory: the first --write, and the cwd
	DataHome  string   // the per-job data home, which is also the child's HOME
	ReadRoots []string // toolchain roots the worker description named
	Command   string   // the harness, resolved on PATH by the caller
	Args      []string // the harness's own arguments
}

// SandboxArgv is the wrap: nova-sandbox's flags, then --, then the harness verbatim.
// Everything after -- is the harness's own argv unchanged, so no argument is re-parsed and
// no quote re-interpreted (rule 12).
func (j SandboxJob) SandboxArgv() []string {
	jobDir := absPath(j.JobDir)
	argv := []string{"--read", absPath(j.SlotDir)}
	for _, root := range j.ReadRoots {
		argv = append(argv, "--read", absPath(root))
	}
	// Rule 4: the job directory is the FIRST --write, because the cwd and the temp
	// directory default to it. The data home is named beside it because the spec names it
	// -- it is where the harness keeps its database, and it is the child's HOME.
	argv = append(argv, "--write", jobDir, "--write", absPath(j.DataHome), "--cwd", jobDir)
	if j.PoolName != "" {
		// On windows the container name is required; on darwin and linux it is accepted
		// and ignored, so one caller builds one argv for three platforms.
		argv = append(argv, "--name", j.PoolName)
	}
	argv = append(argv, "--", j.Command)
	return append(argv, j.Args...)
}

// SandboxCommand is the whole argv, the binary first, which is what exec.Command is handed.
func (j SandboxJob) SandboxCommand() []string {
	return append([]string{j.Sandbox}, j.SandboxArgv()...)
}

// LookSandbox resolves the wall's binary. A path the caller typed is used as typed; an
// empty one is the tool's own name on PATH. A miss is a REFUSAL naming the remedy, never a
// silent run without a wall: rule 11's --no-sandbox is the one workaround and it is typed
// by a person, never inferred by this tool.
func LookSandbox(path string) (string, error) {
	name := path
	if strings.TrimSpace(name) == "" {
		name = SandboxBinary
	}
	found, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%s is on no PATH entry and --sandbox names no file: every job runs inside nova-sandbox (docs/SPEC-SANDBOX.md), so build it (`go build ./cmd/nova-sandbox`) and put it on PATH, name it with --sandbox <path>, or run with --no-sandbox and own every read and write the jobs make",
			oneline.Field(name))
	}
	return found, nil
}

// SandboxProbeDir is the directory the probe of rule 10 writes in, and it is the
// dispatcher's own: the probe needs a --write whose PARENT is outside every named path, so
// that the write it expects to be denied has somewhere to be attempted.
//
// It is ABSOLUTE, because `--pool` is used as typed and the README's own invocation types it
// relative (`--pool ./pool`). Rule 5 of the wall is "paths are resolved, absolute and
// existing", so a relative pool sent the probe `--write pool/sandbox-probe`, the wall
// refused it as relative, and `run` refused the whole pass and started NO WORKER for the
// documented line (DeepSeek's read of #88 at d0c1841, HIGH 1).
func SandboxProbeDir(poolDir string) string {
	return filepath.Join(absPath(poolDir), "sandbox-probe")
}

// absPath is every path this seam hands the wall, made absolute at the point the argv is
// built. The seam is the LAST place a path is the tool's own: after this it is a flag the
// wall resolves under rule 5, and a relative one there is a refusal, never a path relative
// to the child. It is resolved against THIS process's directory, which is the directory the
// same process would have opened the path from anyway; an empty path stays empty, because an
// empty field is the caller's absence and not a path to the current directory.
func absPath(path string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}

// SandboxGate is what `run` does ONCE, before the first worker: it asks the machine what it
// can enforce and then proves the wall with the real policy. A failure is a REFUSED RUN
// with no worker started, because a batch that runs unwalled under a green line is exactly
// the silent sandbox this whole tool exists to prevent.
//
// It returns the reason= token for the RUN REFUSED line and the sentence beside it, or an
// empty reason when the wall is there. The two reasons are the spec's own (test 23): a
// machine with no backend is `no_sandbox` and a wall that failed a check is
// `sandbox_probe`.
// `notes` is where the one thing this gate can fail at without refusing the run is said:
// the probe directory it made and could not remove. It is the caller's stderr, never
// stdout, because a pass prints EXACTLY ONE `RUN NOTE` and that line is the remedy at the
// end (SPEC-SWARM's output grammar).
func SandboxGate(sandboxPath, poolDir, secret string, notes io.Writer) (reason, text string) {
	probeDir := SandboxProbeDir(poolDir)
	if err := os.MkdirAll(probeDir, 0o700); err != nil {
		return "sandbox_probe", fmt.Sprintf("the probe directory %s could not be made: %s", probeDir, redactedReason(err))
	}
	// THE PROBE'S DIRECTORY IS THE PROBE'S, and it does not outlive it. It was left in the
	// pool once per run, and the pool is this tool's own layout: a directory nothing in
	// SPEC-SWARM names is a directory a later reader has to account for (Rowan's Fable
	// read of #88 at d0c1841, L4). The removal is BEST EFFORT -- the wall proved itself or
	// it did not, and a directory that will not go away is not a reason to start no worker
	// -- so it is said on stderr and the gate's answer is unchanged.
	defer func() {
		if err := os.RemoveAll(probeDir); err != nil && notes != nil {
			fmt.Fprintf(notes, "nova-swarm run: the probe directory %s could not be removed: %s\n",
				oneline.Field(probeDir), oneline.Escape(redactedReason(err)))
		}
	}()
	// `check` is a question and exits 0 either way, so the ANSWER is read from its line:
	// a machine with no backend says backend=none, and that is a refusal of its own,
	// named apart from a probe that ran and said NO.
	check := exec.Command(sandboxPath, "check")
	checkOut, err := check.CombinedOutput()
	line := oneline.Cap(strings.TrimSpace(string(checkOut)), oneline.TailBytes)
	if err != nil {
		return "no_sandbox", fmt.Sprintf("%s check would not run: %s", sandboxPath, redactedReason(err))
	}
	if strings.Contains(string(checkOut), "backend=none") {
		return "no_sandbox", fmt.Sprintf("this machine has no sandbox backend, and a job this tool cannot contain does not run: %s. The one workaround is `--no-sandbox`, which runs every job with no OS containment and says so once per job", line)
	}
	// The probe itself: five checks under the REAL policy for this platform, run once
	// before the first task. HOME is the probe's own write set, because rule 9 refuses a
	// run whose HOME is outside it -- and the dispatcher's own HOME is.
	probe := exec.Command(sandboxPath, "probe", "--write", probeDir, "--secret", absPath(secret))
	probe.Env = append(environWithout(os.Environ(), "HOME"), "HOME="+probeDir)
	probeOut, err := probe.CombinedOutput()
	if err == nil {
		return "", ""
	}
	return "sandbox_probe", fmt.Sprintf("the wall did not prove itself on this machine, so no worker started: %s",
		oneline.Cap(refusalLine(string(probeOut)), oneline.TailBytes))
}

// environWithout is the environment minus one name, so that the caller can set it and be
// certain there is not a second copy of it behind theirs.
func environWithout(env []string, name string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if n, _, _ := strings.Cut(kv, "="); n == name {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// refusalLine is the ONE line of a failed probe that RUN REFUSED quotes, and it is the line
// that says NO -- not the last one. The probe runs all five checks even when one fails
// (SPEC-SANDBOX test 10), so the refusal is followed by whatever passed after it, and
// `lastLine` quoted a PASSING step under `RUN REFUSED reason=sandbox_probe`: a reader was
// told `read_root expect=allow got=allow` as the reason no worker started (DeepSeek's read
// of #88 at d0c1841, MEDIUM 2).
//
// The order is the probe's own grammar: its `PROBE REFUSED` line first, because that is the
// line the wall wrote to say why; then the first step whose `got=` is not its `expect=`,
// for a probe that failed without one; then the last line, which is all a probe that said
// something else has.
func refusalLine(body string) string {
	var firstFailedStep string
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.Contains(line, "REFUSED") {
			return line
		}
		if firstFailedStep == "" && stepFailed(line) {
			firstFailedStep = line
		}
	}
	if firstFailedStep != "" {
		return firstFailedStep
	}
	return lastLine(body)
}

// stepFailed reads one `PROBE STEP name=<n> expect=<x> got=<y>` line and says whether the
// machine did something other than what the check expected. A line with neither field is
// not a step and is never a reason.
func stepFailed(line string) bool {
	expect, got := "", ""
	for _, field := range strings.Fields(line) {
		switch name, value, _ := strings.Cut(field, "="); name {
		case "expect":
			expect = value
		case "got":
			got = value
		}
	}
	return expect != "" && got != "" && expect != got
}

// lastLine is the one line a refusal is read from when the probe named none: the tool's
// output is bounded by design, and a line is all a refusal gets.
func lastLine(body string) string {
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			return strings.TrimSpace(lines[i])
		}
	}
	return "the probe said nothing"
}

// UnsandboxedLine is rule 11's one loud line, per job, printed BEFORE the job starts. It is
// never a default, is never implied by a missing backend, and no environment variable or
// file can produce it: it exists only where a person typed --no-sandbox.
func UnsandboxedLine(id string, slot int) string {
	return fmt.Sprintf("RUN UNSANDBOXED id=%s slot=%d: no OS containment; every read and write this job makes is yours",
		oneline.Field(id), slot)
}
