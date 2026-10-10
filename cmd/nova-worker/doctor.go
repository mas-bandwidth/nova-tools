// nova-worker doctor: refuse to launch under a shadowed or unreadable binary.
//
// A card that a rebuilt swarm starts must run the build that was rebuilt. When PATH puts an
// older nova-worker before the one at ~/.local/bin, every card runs the older one and nothing
// downstream can tell which build ran. The doctor reads the one `version` line the nova-worker
// first on PATH prints and the one the literal ~/.local/bin/nova-worker prints, and if the two
// differ it prints both in full and names the fix. `native` runs the same check
// before they start anything, so a launch refuses before it spends rather than after
// somebody notices. It invents no version of its own: internal/buildinfo produces the line
// and this file only reads what a binary said.
//
// `DOCTOR OK stamp=<line>` says the two agree, or that there is one binary to read. A
// mismatch is exit 2 with both stamps and one remedy, because a refusal that does not say
// which line is stale sends the reader back to run the check by hand.
//
// A binary that cannot be read is a refusal, not a shrug. A `version` that hangs past the
// deadline, exits non-zero, prints nothing, prints a first line past the limit, or names a
// file that is not there is exit 2 with one DOCTOR UNREADABLE line: the binary's path, the
// cause, what the other binary came to, and the next action. A stamp printed before the
// failure is still compared. The one tolerated absence is the ~/.local/bin copy: with none
// installed there is nothing to shadow with.
//
// Only the first line of a binary's output is kept, up to a fixed limit, and a stamp is
// printed as a bounded, escaped excerpt, so a binary that streams forever costs the doctor
// neither memory nor an unbounded line. The two binaries are read at the same time under one
// deadline.
//
// The machine is a doctorEnv (the PATH lookup, the home directory and the version reader),
// so the whole check is driven with fixed binaries and short deadlines; production uses the
// real LookPath, the real home, and `<path> version`.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/harness"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/seatcred"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// doctorLookPath resolves a bare binary name on PATH. A package var so a test answers the
// PATH question with a fixed path rather than the machine's.
var doctorLookPath = exec.LookPath

// doctorHomeDir is where the literal ~/.local/bin lives. A package var so a test's home is
// its own temp directory.
var doctorHomeDir = os.UserHomeDir

// doctorReadVersion reads the one `version` line a binary prints. A package var so a test
// never runs a binary it went looking for.
var doctorReadVersion = readVersionLine

// doctorVersionDeadline bounds one `<binary> version` run. A version line is instant, so a
// binary that has not answered by then is hung, and the doctor refuses naming it rather than
// hanging the launch it guards. doctorVersionGrace is how long the wait for the output
// pipe continues once the binary has exited or been killed, for a child that still holds it.
const (
	doctorVersionDeadline = 5 * time.Second
	doctorVersionGrace    = time.Second
)

// errDoctorNotFound is the read error for a binary that is not there.
var errDoctorNotFound = errors.New("not found")

// doctorVersionLineMax is the most a `version` line may hold. A stamp is one short line, so
// a first line past this is the binary's fault and its own cause, and nothing past it is kept.
const doctorVersionLineMax = 4096

// doctorStampExcerpt is the most of a stamp a DOCTOR line prints: the comparison sees the
// whole line, the output a bounded, escaped excerpt of it.
const doctorStampExcerpt = 200

// firstLineWriter keeps the first line written to it and discards the rest: it is the
// binary's stdout, so a binary that streams forever costs the doctor at most limit bytes.
// It is safe for the copy goroutine to write while the reader looks at it.
type firstLineWriter struct {
	mu         sync.Mutex
	line       []byte
	limit      int
	done       bool // the first line ended, or overflowed: later bytes are discarded
	overflowed bool
	onOverflow func()
}

func (w *firstLineWriter) Write(p []byte) (int, error) {
	n := len(p)
	w.mu.Lock()
	overflow := false
	if !w.done {
		take := p
		if i := bytes.IndexByte(p, '\n'); i >= 0 {
			take, w.done = p[:i], true
		}
		if room := w.limit - len(w.line); len(take) > room {
			w.line = append(w.line, take[:room]...)
			w.done, w.overflowed, overflow = true, true, true
		} else {
			w.line = append(w.line, take...)
		}
	}
	notify := w.onOverflow
	w.mu.Unlock()
	if overflow && notify != nil {
		notify()
	}
	return n, nil
}

// result is the first line kept, without a trailing carriage return, and whether it
// overflowed the limit.
func (w *firstLineWriter) result() (line string, overflowed bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return strings.TrimRight(string(w.line), "\r"), w.overflowed
}

// readVersionLine runs `<path> version` under the production deadline and returns its first
// line. It is production's reader only: every unit test injects the reader or the deadline.
func readVersionLine(path string) (string, error) {
	return readVersionLineWithin(path, doctorVersionDeadline, doctorVersionGrace, doctorVersionLineMax)
}

// runVersion runs `<path> version` under the deadline (and the parent context) with its stdout going to a first-line
// writer, and returns the writer (what was kept, and whether the line overflowed), whether the
// deadline or the overflow ended the run, and the run's own error.
func runVersion(parent context.Context, path string, deadline, grace time.Duration, limit int) (out *firstLineWriter, ended bool, err error) {
	ctx, cancel := context.WithTimeout(parent, deadline)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "version")
	out = &firstLineWriter{limit: limit, onOverflow: cancel}
	cmd.Stdout = out
	cmd.Stderr = io.Discard
	cmd.WaitDelay = grace
	err = cmd.Run()
	return out, ctx.Err() != nil, err
}

// readVersionLineWithin is readVersionLine with the deadline, the pipe grace and the line
// limit named. The first line printed is returned even when the run then fails, so a stamp
// read before a hang or a non-zero exit is still compared; the error names the cause in the
// words the refusal prints: "timed out after <deadline>", "exited <n>", "printed nothing",
// "printed a line longer than <limit> bytes", or "not found". A run that outlives the
// deadline is killed, a run whose first line passes the limit is killed at once, and a child
// still holding the output pipe is given up on after the grace. Output after the first line
// is discarded as it arrives, so memory is bounded by the limit whatever the binary prints.
func readVersionLineWithin(path string, deadline, grace time.Duration, limit int) (string, error) {
	return readVersionLineUnder(context.Background(), path, deadline, grace, limit)
}

// readVersionLineUnder is readVersionLineWithin under a parent context: cancelling the parent
// ends the run as the deadline does, and the refusal names the deadline. A test that has seen
// the binary print cancels the parent instead of waiting the deadline out.
func readVersionLineUnder(parent context.Context, path string, deadline, grace time.Duration, limit int) (string, error) {
	out, timedOut, err := runVersion(parent, path, deadline, grace, limit)
	line, overflowed := out.result()
	switch {
	case overflowed:
		return "", fmt.Errorf("printed a line longer than %d bytes", limit)
	case timedOut:
		return line, fmt.Errorf("timed out after %s", deadline)
	case err == nil || errors.Is(err, exec.ErrWaitDelay):
		// A binary that exited 0 and left a child holding the pipe has answered.
	case errors.Is(err, fs.ErrNotExist) || errors.Is(err, exec.ErrNotFound):
		return "", errDoctorNotFound
	default:
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			if code := exit.ExitCode(); code >= 0 {
				return line, fmt.Errorf("exited %d", code)
			}
			return line, fmt.Errorf("was killed (%s)", exit.String())
		}
		return line, err
	}
	if strings.TrimSpace(line) == "" {
		return "", errors.New("printed nothing")
	}
	return line, nil
}

// doctorEnv is what the doctor asks of the machine: where nova-worker is on PATH, where home
// is, and what a binary reports for `version`. A test builds one with its own reader (and
// its own deadline), so the whole preflight is driven without a package var.
type doctorEnv struct {
	lookPath func(string) (string, error)
	homeDir  func() (string, error)
	read     func(string) (string, error)
	// run runs a headless harness's verb (`<binary> --version`, its login verb) under the
	// doctor's deadline: its first line and its exit code; nil runs it (runVerbLine).
	run func(binary string, args ...string) (line string, rc int)
}

// liveDoctorEnv is the machine's environment through the package seams.
func liveDoctorEnv() doctorEnv {
	return doctorEnv{lookPath: doctorLookPath, homeDir: doctorHomeDir, read: doctorReadVersion, run: runVerbLine}
}

// runVerbLine runs `<binary> <args...>` under the version deadline, stdin closed, and
// returns the first line of what it printed (stdout, else stderr) and its exit code; -1
// when it could not be started or was killed.
func runVerbLine(binary string, args ...string) (string, int) {
	ctx, cancel := context.WithTimeout(context.Background(), doctorVersionDeadline)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	out, errb := &firstLineWriter{limit: doctorVersionLineMax}, &firstLineWriter{limit: doctorVersionLineMax}
	cmd.Stdout, cmd.Stderr = out, errb
	cmd.WaitDelay = doctorVersionGrace
	err := cmd.Run()
	line, _ := out.result()
	if strings.TrimSpace(line) == "" {
		line, _ = errb.result()
	}
	rc := 0
	if err != nil {
		rc = -1
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() >= 0 {
			rc = exit.ExitCode()
		}
	}
	return strings.TrimSpace(line), rc
}

// headlessReport is one headless harness as the doctor found it: the binary on PATH (""
// when none), its version line and whether its login verb said it is logged in.
type headlessReport struct {
	kind, binary, version, said string
	loggedIn                    bool
}

// headlessReports asks each headless harness (internal/harness; docs/SPEC-WORKER.md, the
// headless harnesses) whether it is on PATH, its version, and its login, in the order of
// harness.Headless. A machine without one has no report to refuse on: the heavy tier's
// members have them and the others do not.
func (e doctorEnv) headlessReports() []headlessReport {
	var out []headlessReport
	if e.run == nil {
		return nil // an environment with no runner (a test's fake) has no harness to ask
	}
	for _, kind := range harness.Headless {
		r := headlessReport{kind: kind}
		if bin, err := e.lookPath(kind); err == nil && bin != "" {
			r.binary = bin
			r.version, _ = e.run(bin, "--version")
			line, rc := e.run(bin, swarm.HeadlessLoginArgv(kind)...)
			r.loggedIn, r.said = swarm.HeadlessLogin(kind, []byte(line), rc)
		}
		out = append(out, r)
	}
	return out
}

// writeHeadlessReports prints one DOCTOR HARNESS line per headless harness: where it is,
// what it says it is, and whether it is logged in; `-` for one that is not on PATH. The
// lines inform and never refuse: a launch under a logged-out harness ends as a provider
// failure of class auth (nativeprovider.go, swarm.HeadlessFailure), named on its card.
func writeHeadlessReports(w io.Writer, reports []headlessReport) {
	for _, r := range reports {
		if r.binary == "" {
			fmt.Fprintf(w, "DOCTOR HARNESS kind=%s binary=- version=- login=-\n", oneline.Field(r.kind))
			continue
		}
		login := "no"
		if r.loggedIn {
			login = "yes"
		}
		fmt.Fprintf(w, "DOCTOR HARNESS kind=%s binary=%s version=%s login=%s said=%s\n",
			oneline.Field(r.kind), oneline.Field(r.binary), doctorExcerpt(doctorStamp(r.version)), oneline.Field(login), doctorExcerpt(doctorStamp(r.said)))
	}
}

// resolveBinaries answers the two questions in one place: which nova-worker comes
// first on PATH, and where the literal ~/.local/bin copy is. The overrides are the verb's
// --path and --local; empty means resolve. A name that cannot be resolved is the empty
// string, which the comparison reads as "nothing to compare" rather than as a mismatch.
func (e doctorEnv) resolveBinaries(pathOverride, localOverride string) (onPath, local string) {
	onPath = strings.TrimSpace(pathOverride)
	if onPath == "" {
		if found, err := e.lookPath("nova-worker"); err == nil {
			onPath = found
		}
	}
	local = strings.TrimSpace(localOverride)
	if local == "" {
		if home, err := e.homeDir(); err == nil && strings.TrimSpace(home) != "" {
			local = filepath.Join(home, ".local", "bin", "nova-worker")
		}
	}
	return onPath, local
}

// doctorRead is one binary's answer: the first line it printed (kept even when the run then
// failed) and why the run failed, "" when it did not.
type doctorRead struct {
	binary string
	line   string
	cause  string
}

// doctorReport is what one comparison found, so the printing and the refusing are two thin
// readers of one result rather than two places that each re-run the check.
type doctorReport struct {
	pathBinary  string       // the nova-worker first on PATH
	pathLine    string       // the full `version` line it printed, "" when it printed none
	localBinary string       // the literal ~/.local/bin/nova-worker
	localLine   string       // the full `version` line it printed, "" when it printed none
	shadowed    bool         // both stamps read, and the two lines differ
	unreadable  []doctorRead // every binary compared whose read failed
	pathCause   string       // why PATH's binary could not be read, "" when it was
	localCause  string       // why the local copy could not be read, "" when it was, "not found" when absent
	sameFile    bool         // PATH's binary is the local copy: there is one binary
	stamp       string       // the line to report when the pair is fine
}

// refused reports whether the launch stops: a shadowing pair, or a binary that cannot be read.
func (r doctorReport) refused() bool { return r.shadowed || len(r.unreadable) > 0 }

// readOne asks for one binary's stamp and folds the reader's error into the words the refusal
// prints. A stamp printed before a failure is kept.
func (e doctorEnv) readOne(binary string) doctorRead {
	line, err := e.read(binary)
	r := doctorRead{binary: binary, line: strings.TrimRight(line, "\n")}
	if err != nil {
		r.cause = err.Error()
	} else if strings.TrimSpace(r.line) == "" {
		r.cause = "printed nothing"
	}
	return r
}

// compareBinaries is the whole decision. There is no refusal when the two names are one file
// and it answers, when no nova-worker is on PATH, or when ~/.local/bin has no copy to shadow
// with (the one absence that is tolerated): there is no second stamp to disagree with. Every
// other binary the comparison reads must answer, and one that does not is named in the
// report; a stamp it printed first is still compared. The two are read at the same time, so
// two hung binaries cost one deadline plus the pipe grace, not two deadlines.
func (e doctorEnv) compareBinaries(pathBinary, localBinary string) doctorReport {
	r := doctorReport{pathBinary: pathBinary, localBinary: localBinary}
	readPath := pathBinary != ""
	readLocal := localBinary != "" && !doctorSameFile(pathBinary, localBinary)
	var pathRead, localRead doctorRead
	var wg sync.WaitGroup
	if readPath {
		wg.Go(func() { pathRead = e.readOne(pathBinary) })
	}
	if readLocal {
		wg.Go(func() { localRead = e.readOne(localBinary) })
	}
	wg.Wait()
	if readPath {
		r.pathLine, r.pathCause = pathRead.line, pathRead.cause
		if pathRead.cause != "" {
			r.unreadable = append(r.unreadable, pathRead)
		}
	}
	if !readLocal {
		r.stamp = r.pathLine
		r.sameFile = readPath && localBinary != "" && !readLocal
		return r
	}
	r.localLine, r.localCause = localRead.line, localRead.cause
	if localRead.cause != "" && localRead.cause != errDoctorNotFound.Error() {
		r.unreadable = append(r.unreadable, localRead)
	}
	switch {
	case r.pathLine == "" && r.localLine == "":
	case r.localLine == "":
		// Nothing at ~/.local/bin to shadow with, or it printed nothing to compare.
		r.stamp = r.pathLine
	case r.pathLine == "":
		// PATH has no readable copy, so there is no pair to compare.
		r.stamp = r.localLine
	case r.pathLine == r.localLine:
		r.stamp = r.pathLine
	default:
		r.shadowed = true
	}
	return r
}

// doctorSameFile reports whether the two names are one binary. The string comparison is the
// common case (PATH already resolves to the .local/bin install); os.SameFile catches a
// symlink or a bind mount to the same inode, which a person who linked the two installs
// together would have.
func doctorSameFile(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ai, aerr := os.Stat(a)
	bi, berr := os.Stat(b)
	if aerr != nil || berr != nil {
		return false
	}
	return os.SameFile(ai, bi)
}

// doctorExcerpt is a stamp as a DOCTOR line prints it: at most doctorStampExcerpt bytes,
// escaped to one line. The comparison has already seen the whole line.
func doctorExcerpt(line string) string {
	return oneline.Escape(oneline.Cap(line, doctorStampExcerpt))
}

// doctorStamp is the line the OK form reports, with buildinfo's own floor when a read
// produced nothing -- the same "devel" every version verb prints, never an invented number.
func doctorStamp(line string) string {
	if strings.TrimSpace(line) == "" {
		return buildinfo.Unknown
	}
	return line
}

// cmdDoctor is the verb. `--path` and `--local` override the two resolutions so the check
// is runnable against fixed binaries; with no flags it reads the machine it is on.
func cmdDoctor(args []string, stdout, stderr io.Writer) int {
	return liveDoctorEnv().cmdDoctor(args, stdout, stderr)
}

func (e doctorEnv) cmdDoctor(args []string, stdout, stderr io.Writer) int {
	f := newFlags("doctor")
	pathFlag := f.fs.String("path", "", "the nova-worker binary `file` to read as the one first on PATH (default: PATH's)")
	localFlag := f.fs.String("local", "", "the nova-worker binary `file` to read as the local build (default: ~/.local/bin/nova-worker)")
	if !f.parse(args, stderr) {
		return 2
	}
	r := e.compareBinaries(e.resolveBinaries(*pathFlag, *localFlag))
	if !r.refused() {
		if r.stamp == "" {
			// No binary was read: there is nothing to compare, and the line says so rather
			// than reporting a stamp nobody read.
			fmt.Fprintln(stdout, "DOCTOR OK nothing to compare: no nova-worker on PATH and none under the local directory")
			return 0
		}
		fmt.Fprintf(stdout, "DOCTOR OK stamp=%s\n", doctorExcerpt(r.stamp))
		writeHeadlessReports(stdout, e.headlessReports())
		return 0
	}
	writeDoctorRefusal(stderr, r)
	return 2
}

// writeDoctorRefusal prints what stopped the launch. A shadowing pair is the two full lines
// and the one remedy; the paths go through Field so they are single tokens, and the version
// lines go through Escape so a caller reading the refusal sees the line as the binary wrote
// it, spaces and all. A binary that could not be read is one DOCTOR UNREADABLE line each.
func writeDoctorRefusal(stderr io.Writer, r doctorReport) {
	if r.shadowed {
		fmt.Fprintf(stderr, "DOCTOR DRIFT path=%s stamp=%s\n",
			oneline.Field(r.pathBinary), doctorExcerpt(doctorStamp(r.pathLine)))
		fmt.Fprintf(stderr, "DOCTOR DRIFT local=%s stamp=%s\n",
			oneline.Field(r.localBinary), doctorExcerpt(doctorStamp(r.localLine)))
		fmt.Fprintf(stderr, "DOCTOR REFUSED %s shadows %s; copy the ~/.local/bin binary over the PATH one, or fix PATH so ~/.local/bin comes first\n",
			oneline.Field(r.pathBinary), oneline.Field(r.localBinary))
	}
	for _, u := range r.unreadable {
		role := "path"
		if u.binary == r.localBinary && u.binary != r.pathBinary {
			role = "local"
		}
		fmt.Fprintf(stderr, "DOCTOR UNREADABLE reading the version of %s=%s: %s; %s; run `%s version` by hand and rebuild or remove the binary that does not answer, then launch again\n",
			oneline.Field(role), oneline.Field(u.binary), oneline.Escape(u.cause),
			doctorOtherSentence(r, role), oneline.Field(u.binary))
	}
}

// doctorOtherSentence says, in words, what the binary that was not the one refused came to:
// what it reported, that it could not be read either, that it is not installed, or that
// there is no other binary at all. What it returns is one line, its paths escaped as fields
// and its stamp as a bounded excerpt.
func doctorOtherSentence(r doctorReport, role string) string {
	other, line, cause := r.localBinary, r.localLine, r.localCause
	if role == "local" {
		other, line, cause = r.pathBinary, r.pathLine, r.pathCause
	}
	switch {
	case r.sameFile:
		return "there is no other binary: PATH resolves to the local copy"
	case other == "":
		return "there is no other binary to compare with"
	case cause == errDoctorNotFound.Error():
		return "the other binary, " + oneline.Field(other) + ", is not installed"
	case cause != "" && line == "":
		return "the other binary, " + oneline.Field(other) + ", could not be read either: " + oneline.Escape(cause)
	case cause != "":
		return "the other binary, " + oneline.Field(other) + ", reported stamp=" + doctorExcerpt(line) + " and then failed: " + oneline.Escape(cause)
	}
	return "the other binary, " + oneline.Field(other) + ", reported stamp=" + doctorExcerpt(line)
}

// doctorLaunchVerb reports whether v is a verb that starts a card, which is where the
// preflight belongs: `native` starts one child, and it may not spend anything under a
// shadowed binary.
func doctorLaunchVerb(v string) bool { return v == "native" }

// preflightDoctor is what main calls before the dispatcher. It is at the process boundary
// rather than inside cmdRun/cmdNative on purpose: the question is about the real PATH and
// the real home, and the dispatcher is the thing tests call with fake ones. A shadowed pair
// stops the launch with exit 2 before run() is reached; every other verb, and an
// unresolvable pair, proceeds untouched.
func preflightDoctor(args []string, stderr io.Writer) (int, bool) {
	return liveDoctorEnv().preflight(args, stderr)
}

func (e doctorEnv) preflight(args []string, stderr io.Writer) (int, bool) {
	// The dispatcher takes --seat out of the arguments before it reads the verb, so the
	// preflight reads them the same way: `--seat s native ...` is a launch, and `native --seat
	// s -h` is a help request. A --seat with no name is the dispatcher's own refusal to make.
	args, err := doctorStripSeat(args)
	if err != nil {
		return 0, false
	}
	// -h is a question about the verb, not a launch: the preflight stands aside for it.
	if len(args) == 0 || !doctorLaunchVerb(args[0]) || doctorLaunchHelp(args[0], args[1:]) {
		return 0, false
	}
	r := e.compareBinaries(e.resolveBinaries("", ""))
	if !r.refused() {
		return 0, false
	}
	writeDoctorRefusal(stderr, r)
	return 2, true
}

// doctorStripSeat is the dispatcher's own removal of --seat, on a selection of its own so
// the preflight selects no seat for the process: the same code, the same arguments left.
func doctorStripSeat(args []string) ([]string, error) {
	var scratch seatcred.Selection
	return scratch.FromArgs(args, nil)
}

// doctorLaunchHelp reports whether args asking for a launch verb (native)
// are actually a request for help, rather than a launch carrying a flag value
// spelled "-h" (like `native --label -h`).
func doctorLaunchHelp(verb string, args []string) bool {
	var fs *flag.FlagSet
	switch verb {
	case "native":
		f, _ := nativeFlagSet()
		fs = f.fs
	default:
		return false
	}
	return fs.Parse(args) == flag.ErrHelp
}
