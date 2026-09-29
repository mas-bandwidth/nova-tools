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

// lockedBuffer is a write-safe buffer: the output copy can still be running when the wait
// gives up on a pipe a child holds open, and the first line is read after that.
type lockedBuffer struct {
	mu   sync.Mutex
	data []byte
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = append(b.data, p...)
	return len(p), nil
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data)
}

// readVersionLine runs `<path> version` under the production deadline and returns its first
// line. It is production's reader only: every unit test injects the reader or the deadline.
func readVersionLine(path string) (string, error) {
	return readVersionLineWithin(path, doctorVersionDeadline, doctorVersionGrace)
}

// readVersionLineWithin is readVersionLine with the deadline and the pipe grace named. The
// first line printed is returned even when the run then fails, so a stamp read before a hang
// or a non-zero exit is still compared; the error names the cause in the words the refusal
// prints: "timed out after <deadline>", "exited <n>", "printed nothing", or "not found". A
// run that outlives the deadline is killed, and a child still holding the output pipe is
// given up on after the grace.
func readVersionLineWithin(path string, deadline, grace time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "version")
	var out lockedBuffer
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	cmd.WaitDelay = grace
	err := cmd.Run()
	line, _, _ := strings.Cut(out.String(), "\n")
	line = strings.TrimRight(line, "\r")
	switch {
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
// report; a stamp it printed first is still compared.
func (e doctorEnv) compareBinaries(pathBinary, localBinary string) doctorReport {
	r := doctorReport{pathBinary: pathBinary, localBinary: localBinary}
	var pathRead, localRead doctorRead
	if pathBinary != "" {
		pathRead = e.readOne(pathBinary)
		r.pathLine = pathRead.line
		if pathRead.cause != "" {
			r.unreadable = append(r.unreadable, pathRead)
		}
	}
	if doctorSameFile(pathBinary, localBinary) || localBinary == "" {
		r.stamp = r.pathLine
		return r
	}
	localRead = e.readOne(localBinary)
	r.localLine = localRead.line
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
		fmt.Fprintf(stdout, "DOCTOR OK stamp=%s\n", oneline.Escape(doctorStamp(r.stamp)))
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
			oneline.Field(r.pathBinary), oneline.Escape(doctorStamp(r.pathLine)))
		fmt.Fprintf(stderr, "DOCTOR DRIFT local=%s stamp=%s\n",
			oneline.Field(r.localBinary), oneline.Escape(doctorStamp(r.localLine)))
		fmt.Fprintf(stderr, "DOCTOR REFUSED %s shadows %s; copy the ~/.local/bin binary over the PATH one, or fix PATH so ~/.local/bin comes first\n",
			oneline.Field(r.pathBinary), oneline.Field(r.localBinary))
	}
	for _, u := range r.unreadable {
		role, other, otherLine := "path", r.localBinary, r.localLine
		if u.binary == r.localBinary && u.binary != r.pathBinary {
			role, other, otherLine = "local", r.pathBinary, r.pathLine
		}
		fmt.Fprintf(stderr, "DOCTOR UNREADABLE reading the version of %s=%s: %s; %s reported %s; run `%s version` by hand and rebuild or remove the binary that does not answer, then launch again\n",
			oneline.Field(role), oneline.Field(u.binary), oneline.Escape(u.cause),
			doctorOther(other), doctorReported(otherLine), oneline.Field(u.binary))
	}
}

// doctorOther names the other binary in an unreadable line, "no other binary" when the
// comparison had none. What it returns is already one escaped token.
func doctorOther(binary string) string {
	if binary == "" {
		return "no other binary"
	}
	return oneline.Field(binary)
}

// doctorReported is what the other binary said: its stamp, escaped, or that it said nothing.
func doctorReported(line string) string {
	if strings.TrimSpace(line) == "" {
		return "nothing"
	}
	return "stamp=" + oneline.Escape(line)
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
