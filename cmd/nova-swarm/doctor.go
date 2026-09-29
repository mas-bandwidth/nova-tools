// nova-swarm doctor: refuse to launch under a SHADOWED binary.
//
// THE FAILURE THIS VERB IS (cairn b9395d11, 2026-09-17). PATH put ~/go/bin before
// ~/.local/bin, ~/go/bin held a nova-swarm from an earlier build, and for 25 minutes every
// card that a rebuilt swarm started silently ran the STALE binary -- with a private build
// cache rather than the shared one, so nothing downstream could tell which build had run.
// The mismatch was caught by hand, and the operator-run bench-standard.sh grew a check for
// it. This verb folds that same comparison into `nova-swarm` itself so a launch refuses
// before it starts rather than after somebody notices.
//
// The comparison is deliberately narrow: read the one `version` line the nova-swarm found
// first on PATH prints, read the one the literal ~/.local/bin/nova-swarm prints, and if the
// two differ, print both in full and name the fix. It is not a general drift daemon, and it
// invents no version of its own: internal/buildinfo produces the line and this file only
// reads what a binary said.
//
// ONE LINE ON SUCCESS. `DOCTOR OK stamp=<line>` says the two agree (or that there is only
// one of them to read). A mismatch is exit 2 with both stamps and one remedy, because a
// refusal that does not say which line is stale sends the reader back to run the check by
// hand -- which is the thing this verb removes.
//
// A BINARY THAT CANNOT BE READ IS A REFUSAL, NOT A SHRUG. A `version` that hangs past the
// deadline, exits non-zero, prints nothing, or names a file that is not there is exit 2 with
// one DOCTOR UNREADABLE line: the binary's path, the cause, what the other binary reported
// and the next action. A stamp printed before the failure is still compared, so a stale
// binary that then hangs is refused as shadowing and as unreadable. The one tolerated absence
// is the ~/.local/bin copy: with none installed there is nothing to shadow with.
//
// THE TWO SEAMS ARE PACKAGE VARS. No unit test may execute a path it discovered: the PATH
// resolver (doctorLookPath), the home directory (doctorHomeDir) and the version reader
// (doctorReadVersion) are replaced by a test that hands over two fixed stamps. Production
// uses the real LookPath, the real home, and `<path> version`.
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
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/seatcred"
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

// readVersionLineWithin is readVersionLine with the deadline, the pipe grace and the line
// limit named. The first line printed is returned even when the run then fails, so a stamp
// read before a hang or a non-zero exit is still compared; the error names the cause in the
// words the refusal prints: "timed out after <deadline>", "exited <n>", "printed nothing",
// "printed a line longer than <limit> bytes", or "not found". A run that outlives the
// deadline is killed, a run whose first line passes the limit is killed at once, and a child
// still holding the output pipe is given up on after the grace. Output after the first line
// is discarded as it arrives, so memory is bounded by the limit whatever the binary prints.
func readVersionLineWithin(path string, deadline, grace time.Duration, limit int) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "version")
	out := &firstLineWriter{limit: limit, onOverflow: cancel}
	cmd.Stdout = out
	cmd.Stderr = io.Discard
	cmd.WaitDelay = grace
	err := cmd.Run()
	line, overflowed := out.result()
	switch {
	case overflowed:
		return "", fmt.Errorf("printed a line longer than %d bytes", limit)
	case ctx.Err() != nil:
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

// doctorEnv is what the doctor asks of the machine: where nova-swarm is on PATH, where home
// is, and what a binary reports for `version`. A test builds one with its own reader (and
// its own deadline), so the whole preflight is driven without a package var.
type doctorEnv struct {
	lookPath func(string) (string, error)
	homeDir  func() (string, error)
	read     func(string) (string, error)
}

// liveDoctorEnv is the machine's environment through the package seams.
func liveDoctorEnv() doctorEnv {
	return doctorEnv{lookPath: doctorLookPath, homeDir: doctorHomeDir, read: doctorReadVersion}
}

// resolveBinaries answers the two questions in one place: which nova-swarm comes
// first on PATH, and where the literal ~/.local/bin copy is. The overrides are the verb's
// --path and --local; empty means resolve. A name that cannot be resolved is the empty
// string, which the comparison reads as "nothing to compare" rather than as a mismatch.
func (e doctorEnv) resolveBinaries(pathOverride, localOverride string) (onPath, local string) {
	onPath = strings.TrimSpace(pathOverride)
	if onPath == "" {
		if found, err := e.lookPath("nova-swarm"); err == nil {
			onPath = found
		}
	}
	local = strings.TrimSpace(localOverride)
	if local == "" {
		if home, err := e.homeDir(); err == nil && strings.TrimSpace(home) != "" {
			local = filepath.Join(home, ".local", "bin", "nova-swarm")
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
	pathBinary  string       // the nova-swarm first on PATH
	pathLine    string       // the full `version` line it printed, "" when it printed none
	localBinary string       // the literal ~/.local/bin/nova-swarm
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
// and it answers, when no nova-swarm is on PATH, or when ~/.local/bin has no copy to shadow
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
		wg.Add(1)
		go func() {
			defer wg.Done()
			pathRead = e.readOne(pathBinary)
		}()
	}
	if readLocal {
		wg.Add(1)
		go func() {
			defer wg.Done()
			localRead = e.readOne(localBinary)
		}()
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
	pathFlag := f.fs.String("path", "", "")
	localFlag := f.fs.String("local", "", "")
	if !f.parse(args, stderr) {
		return 2
	}
	r := e.compareBinaries(e.resolveBinaries(*pathFlag, *localFlag))
	if !r.refused() {
		if r.stamp == "" {
			// No binary was read: there is nothing to compare, and the line says so rather
			// than reporting a stamp nobody read.
			fmt.Fprintln(stdout, "DOCTOR OK nothing to compare: no nova-swarm on PATH and none under the local directory")
			return 0
		}
		fmt.Fprintf(stdout, "DOCTOR OK stamp=%s\n", doctorExcerpt(r.stamp))
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
// preflight belongs: `batch` starts workers and `native` starts one child, and neither may
// spend anything under a shadowed binary.
func doctorLaunchVerb(v string) bool { return v == "batch" || v == "native" }

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
	// preflight reads them the same way: `--seat s batch ...` is a launch, and `batch --seat
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

// doctorLaunchHelp reports whether args asking for a launch verb (batch or native)
// are actually a request for help, rather than a launch carrying a flag value
// spelled "-h" (like `batch --id -h`).
func doctorLaunchHelp(verb string, args []string) bool {
	var fs *flag.FlagSet
	switch verb {
	case "batch":
		f, _ := batchFlagSet()
		fs = f.fs
	case "native":
		f, _ := nativeFlagSet()
		fs = f.fs
	default:
		return false
	}
	return fs.Parse(args) == flag.ErrHelp
}
