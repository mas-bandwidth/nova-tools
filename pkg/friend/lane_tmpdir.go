package friend

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
)

// The lane's own temporary directory. Every harness run in a friend lane gets a
// private, writable temp directory inside its own lane, so no harness ever
// touches the shared /tmp (docs/SPEC-FRIEND.md, one-shot lanes). On 2026-10-11
// every claude run in a friend lane on the seat's machine died in about a second with
// EEXIST mkdir '/tmp/claude-501': the lane wall left /tmp/claude-<uid> visible
// but not usable, and the harness writes its scratch there.

// LaneTmpName is the temporary directory inside a lane's own job directory.
const LaneTmpName = "tmp"

// LaneTmpDir is the private temporary directory of the lane whose job directory
// is dir: <lane dir>/tmp. Wall.Exec makes it 0700 before the run and removes it
// after; RunWall names it at TMPDIR, TMP, TEMP and CLAUDE_CODE_TMPDIR, so two
// lanes get two directories and no harness run reaches the shared /tmp.
func LaneTmpDir(dir string) string { return filepath.Join(dir, LaneTmpName) }

// HarnessFaultEnvironment is the fault of a lane run the friend's environment
// broke, never the card's: it ended within InstantFail with no REPORT.md and no
// RESULT.md and its output names a filesystem error on a path outside its lane.
const HarnessFaultEnvironment = "harness-fault: environment"

// laneFSError is a filesystem error name a run's output carries.
var laneFSError = regexp.MustCompile(`\b(EEXIST|EACCES|EPERM|ENOENT)\b`)

// LaneFilesystemError is the first line of a lane run's output that names a
// filesystem error (EEXIST, EACCES, EPERM, ENOENT) and names a path outside the
// lane dir; "" when none does. A run that trips on the shared /tmp names its
// path there, outside every lane.
func LaneFilesystemError(out, laneDir string) string {
	for _, line := range strings.Split(stripANSI(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !laneFSError.MatchString(line) {
			continue
		}
		if outsidePath(line, laneDir) != "" {
			return oneLine(line, 200)
		}
	}
	return ""
}

// outsidePath is the first absolute path token of line that is not laneDir or
// under it; "" when every path in the line is inside the lane.
func outsidePath(line, laneDir string) string {
	for _, f := range strings.FieldsFunc(line, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '"' || r == '\'' || r == '(' || r == ')' || r == ',' || r == ':'
	}) {
		if !strings.HasPrefix(f, "/") {
			continue
		}
		f = strings.TrimRight(f, ".,;")
		if laneDir != "" && (f == laneDir || strings.HasPrefix(f, laneDir+"/")) {
			continue
		}
		return f
	}
	return ""
}

// LaneEnvironmentFault says a lane run is the friend's environment fault: it
// ended within InstantFail with no report file and its output names a filesystem
// error on a path outside the lane. It answers the harness's error line and true.
func LaneEnvironmentFault(wall time.Duration, out, report, result, laneDir string) (string, bool) {
	if wall >= InstantFail {
		return "", false
	}
	if exists(report) || exists(result) {
		return "", false
	}
	line := LaneFilesystemError(out, laneDir)
	if line == "" {
		return "", false
	}
	return line, true
}

// environmentFault is a lane's run the friend's environment broke: said on the
// record with the harness's error line, the card kept in the lane's hand with its
// attempt not counted and no finish sent, the lanes paused now, and one judgment
// to the seat naming the error line. Unlike a harness fault, an environment fault
// pauses at once: the harness cannot run until the machine is fixed.
func (l *loop) environmentFault(ln *lane, line, first string, now time.Time) {
	d, s := l.d, l.lanes
	reason := FaultWords(HarnessFaultEnvironment, first)
	d.Record(line + fmt.Sprintf(" card=kept turn=%d/%d reason=%q", ln.attempts, CardTurns, reason))
	until := now.Add(FaultDownFor)
	s.gov.PauseUntil(until, reason, now)
	d.Record(d.sayf("%s harness environment fault: {their} lanes pause until %s: %s", now.UTC().Format(time.RFC3339), until.UTC().Format(time.RFC3339), oneLine(reason, 300)))
	if d.FaultDown != nil {
		d.FaultDown(until, reason)
	}
	subject, body := FaultDownText(d.Friend, d.Pro.Get(), reason, until)
	l.tellKind(bus.KindBlocker, subject, body, now)
}
