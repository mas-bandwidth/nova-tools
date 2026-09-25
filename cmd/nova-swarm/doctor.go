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
// THE TWO SEAMS ARE PACKAGE VARS. No unit test may execute a path it discovered: the PATH
// resolver (doctorLookPath), the home directory (doctorHomeDir) and the version reader
// (doctorReadVersion) are replaced by a test that hands over two fixed stamps. Production
// uses the real LookPath, the real home, and `<path> version`.
package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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

// readVersionLine runs `<path> version` and returns its first line, without its newline.
// It is production's reader only: every test replaces doctorReadVersion.
func readVersionLine(path string) (string, error) {
	cmd := exec.Command(path, "version")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return "", err
	}
	line, _, _ := strings.Cut(out.String(), "\n")
	return strings.TrimRight(line, "\r"), nil
}

// resolveDoctorBinaries answers the two questions in one place: which nova-swarm comes
// first on PATH, and where the literal ~/.local/bin copy is. The overrides are the verb's
// --path and --local; empty means resolve. A name that cannot be resolved is the empty
// string, which the comparison reads as "nothing to compare" rather than as a mismatch.
func resolveDoctorBinaries(pathOverride, localOverride string) (onPath, local string) {
	onPath = strings.TrimSpace(pathOverride)
	if onPath == "" {
		if found, err := doctorLookPath("nova-swarm"); err == nil {
			onPath = found
		}
	}
	local = strings.TrimSpace(localOverride)
	if local == "" {
		if home, err := doctorHomeDir(); err == nil && strings.TrimSpace(home) != "" {
			local = filepath.Join(home, ".local", "bin", "nova-swarm")
		}
	}
	return onPath, local
}

// doctorReport is what one comparison found, so the printing and the refusing are two thin
// readers of one result rather than two places that each re-run the check.
type doctorReport struct {
	pathBinary  string // the nova-swarm first on PATH
	pathLine    string // the full `version` line it printed, "" when unreadable
	localBinary string // the literal ~/.local/bin/nova-swarm
	localLine   string // the full `version` line it printed, "" when absent/unreadable
	shadowed    bool   // both readable, and the two lines differ
	stamp       string // the line to report when not shadowed
}

// compareDoctorBinaries is the whole decision. The three not-shadowed cases are deliberate:
// the two names are the same file, ~/.local/bin has no copy to shadow with, or PATH's
// binary cannot be read -- in every one there is no second stamp to disagree with, and a
// guard that invented a refusal there would stop launches it cannot justify.
func compareDoctorBinaries(pathBinary, localBinary string) doctorReport {
	r := doctorReport{
		pathBinary:  pathBinary,
		localBinary: localBinary,
	}
	if pathBinary != "" {
		if line, err := doctorReadVersion(pathBinary); err == nil {
			r.pathLine = strings.TrimRight(line, "\n")
		}
	}
	if doctorSameFile(pathBinary, localBinary) || localBinary == "" {
		r.stamp = r.pathLine
		return r
	}
	if line, err := doctorReadVersion(localBinary); err == nil {
		r.localLine = strings.TrimRight(line, "\n")
	}
	switch {
	case strings.TrimSpace(r.localLine) == "":
		// Nothing at ~/.local/bin to shadow with. The reference script skips this case.
		r.stamp = r.pathLine
	case strings.TrimSpace(r.pathLine) == "":
		// PATH's copy cannot be read, so there is no pair to compare.
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
	f := newFlags("doctor")
	pathFlag := f.fs.String("path", "", "")
	localFlag := f.fs.String("local", "", "")
	if !f.parse(args, stderr) {
		return 2
	}
	r := compareDoctorBinaries(resolveDoctorBinaries(*pathFlag, *localFlag))
	if !r.shadowed {
		fmt.Fprintf(stdout, "DOCTOR OK stamp=%s\n", oneline.Escape(doctorStamp(r.stamp)))
		return 0
	}
	writeDoctorRefusal(stderr, r)
	return 2
}

// writeDoctorRefusal prints the two full lines and the one remedy. The paths go through
// Field so they are single tokens; the version lines go through Escape so a caller reading
// the refusal sees the line as the binary wrote it, spaces and all.
func writeDoctorRefusal(stderr io.Writer, r doctorReport) {
	fmt.Fprintf(stderr, "DOCTOR DRIFT path=%s stamp=%s\n",
		oneline.Field(r.pathBinary), oneline.Escape(doctorStamp(r.pathLine)))
	fmt.Fprintf(stderr, "DOCTOR DRIFT local=%s stamp=%s\n",
		oneline.Field(r.localBinary), oneline.Escape(doctorStamp(r.localLine)))
	fmt.Fprintf(stderr, "DOCTOR REFUSED %s shadows %s; copy the ~/.local/bin binary over the PATH one, or fix PATH so ~/.local/bin comes first\n",
		oneline.Field(r.pathBinary), oneline.Field(r.localBinary))
}

// doctorLaunchVerb reports whether v is a verb that starts a card, which is where the
// preflight belongs: `run` starts workers and `native` starts one child, and neither may
// spend anything under a shadowed binary.
func doctorLaunchVerb(v string) bool { return v == "run" || v == "native" }

// preflightDoctor is what main calls before the dispatcher. It is at the process boundary
// rather than inside cmdRun/cmdNative on purpose: the question is about the real PATH and
// the real home, and the dispatcher is the thing tests call with fake ones. A shadowed pair
// stops the launch with exit 2 before run() is reached; every other verb, and an
// unresolvable pair, proceeds untouched.
func preflightDoctor(args []string, stderr io.Writer) (int, bool) {
	if len(args) == 0 || !doctorLaunchVerb(args[0]) {
		return 0, false
	}
	r := compareDoctorBinaries(resolveDoctorBinaries("", ""))
	if !r.shadowed {
		return 0, false
	}
	writeDoctorRefusal(stderr, r)
	return 2, true
}
