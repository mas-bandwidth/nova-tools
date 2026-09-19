package swarm

// WHAT THE CARD IS SAYING, READ WHILE IT IS STILL SAYING IT.
//
// Until this file every wall question was asked of a file AFTER the child was gone:
// cmd/nova-swarm/native.go re-opened <job>/harness-output.log once the process had exited,
// and internal/swarm/supervise.go did the same with <job>/harness.log. A run that stopped
// making progress therefore cost its WHOLE deadline before anybody looked, and the
// coordinator watching it saw nothing at all in the meantime.
//
// MEASURED, 2026-09-19, on hulk (landlock abi 4) and on the Studio's own dead card:
//
//   - A wall refusal does NOT stop a card. Three probe cards inside the real wall
//     (`wallprobewrite`, `wallprobeexec`, `wallprobehang`) each took a refusal -- rc=2 and
//     `Permission denied` on a write to /etc, rc=1 on `sudo`, rc=127 on a missing binary,
//     rc=2 and `Permission denied` on a write to /tmp -- and each read the tool error, went
//     on, and published a correct RESULT.md in 13-35 seconds. The model routes around the
//     wall; killing a card at its first refusal would kill working cards.
//   - The card that DID die (`js-under-20-bytes`, 2026-09-19, rc=-1 wall=1200.04s, no
//     RESULT.md) was not stopped by the wall at all. Its ONE `Operation not permitted` is
//     line 5 of its log -- the harness's own startup banner, before STEP 1 -- and the card
//     then worked for SIXTEEN more model steps past it. Its last three log lines are a
//     provider stream opening at 14:53:36.958Z and then nothing: it died waiting on a model
//     turn that never answered, and the post-mortem scan named the banner line as the cause.
//
// So two things are wrong and both are here. A refusal must be NAMED THE MOMENT IT HAPPENS,
// on one typed line the coordinator reads live instead of at the reap; and a refusal the
// card MOVED PAST is not what killed it, whatever order the lines are in.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// PermissionDeniedMark is the kernel's refusal on linux. landlock denies a write outside the
// write set with EACCES, and the C library spells that `Permission denied` -- so the marks
// this package had (`SANDBOX REFUSED`, `Operation not permitted`) named NO linux refusal at
// all. Measured inside the swarm's own wall on hulk: `sh: 1: cannot create
// /tmp/nova-wall-probe-swarm: Permission denied`. Like OperationNotPermittedMark it counts
// only ON A PATH: `Permission denied` with no path in the line is some other permission, and
// a card's own test output is full of sentences.
const PermissionDeniedMark = "Permission denied"

// wallToolMarks are the harness's own marks for a tool call, measured in the logs of the
// four cards above: `$ <command>` for a shell call, `→ <Tool> ...` for a read and
// `← <Tool> ...` for a write, plus the card's own `STEP <n>`. A line carrying one of these
// AFTER a refusal is the card making another tool call, which is a card that moved past it.
var wallToolMarks = []string{"$ ", "→ ", "← ", WallStepMark}

// WallStopped is the refusal that STOPPED a card, which is not the same question WallRefused
// answers. WallRefused reports the FIRST refusal in a log whether or not the card survived
// it; this reports one only when the card made no further tool call after it.
//
// The difference is a whole card. `js-under-20-bytes` took a refusal in the harness's
// startup banner, ran sixteen more model steps, and died in a provider stall twenty minutes
// later; WallRefused named the banner and the shift went looking at the wall.
func WallStopped(log []byte) (WallRefusal, bool) {
	w, ok := WallRefused(log)
	if !ok {
		return WallRefusal{}, false
	}
	if wallMovedOn(log, w.Path) {
		return WallRefusal{}, false
	}
	return w, true
}

// wallMovedOn reports whether the card made another tool call after the line that named
// path. A path it cannot find again is treated as not moved on: the caller already has a
// refusal, and inventing a recovery it did not see would throw the classification away.
func wallMovedOn(log []byte, path string) bool {
	lines := strings.Split(string(log), "\n")
	at := -1
	for i, raw := range lines {
		line := strings.TrimSpace(stripPaint(raw))
		if line == "" {
			continue
		}
		if wallRefusalLine(line) && (path == "" || strings.Contains(line, path)) {
			at = i
			break
		}
	}
	if at < 0 {
		return false
	}
	for _, raw := range lines[at+1:] {
		line := strings.TrimSpace(stripPaint(raw))
		if line == "" {
			continue
		}
		for _, mark := range wallToolMarks {
			if strings.HasPrefix(line, mark) {
				return true
			}
		}
	}
	return false
}

// wallRefusalLine reports whether one already-trimmed, already-stripped line is a refusal in
// the three grammars this package knows: the sandbox's own word, and the kernel's two, each
// of the last on a path.
func wallRefusalLine(line string) bool {
	switch {
	case strings.Contains(line, SandboxRefusedMark):
		return true
	case strings.Contains(line, OperationNotPermittedMark), strings.Contains(line, PermissionDeniedMark):
		return wallPathToken(line) != ""
	}
	return false
}

// WallKind is the ONE WORD a refusal is reported by -- the `<what>` of the typed line -- and
// it is read out of the refusal's own sentence rather than guessed: `home` for the wall's
// own home_outside refusal, `write` and `read` for a path the kernel refused in one
// direction, `exec` for a refused execution, `fence` for the harness's own auto-reject, and
// `denied` when the sentence names no direction at all.
func WallKind(line string) string {
	l := strings.ToLower(stripPaint(line))
	switch {
	case strings.Contains(l, "reason=home_outside"):
		return "home"
	case strings.Contains(l, "auto-reject"), strings.Contains(l, "permission requested"):
		return "fence"
	case strings.Contains(l, "cannot execute"), strings.Contains(l, "exec format"), strings.Contains(l, "permission denied") && strings.Contains(l, "./"):
		return "exec"
	case strings.Contains(l, "cannot create"), strings.Contains(l, "cannot write"), strings.Contains(l, "cannot touch"),
		strings.Contains(l, "cannot make"), strings.Contains(l, "cannot create directory"), strings.Contains(l, "cannot remove"),
		strings.Contains(l, "read-only file system"), strings.Contains(l, "mkdir"), strings.Contains(l, "mkdtemp"):
		return "write"
	case strings.Contains(l, "unable to read"), strings.Contains(l, "cannot open"), strings.Contains(l, "cannot read"),
		strings.Contains(l, "cannot access"), strings.Contains(l, "no such file or directory"):
		return "read"
	}
	return "denied"
}

// WallRefusedLine is the typed line a refusal is announced on THE MOMENT IT IS SEEN, before
// anything is decided about it and while the card is still running:
//
//	WALL REFUSED <what> <path> task=<id> step=<n>
//
// It is a note, never a verdict: a card that goes on to publish is done, and this line
// having been printed takes nothing away from it.
func WallRefusedLine(task, kind, path, step string) string {
	return fmt.Sprintf("WALL REFUSED %s %s task=%s step=%s",
		oneline.Field(dashOr(kind)), oneline.Field(dashOr(path)), oneline.Field(task), oneline.Field(dashOr(step)))
}

// WallReader is an io.Writer over the child's own output, put in the capture chain beside
// the log files and the timeline so every byte is seen AS IT ARRIVES rather than re-read
// from a file once the child is gone. It holds a partial line until its newline, the way
// Timeline does, and it is safe for the child's concurrent stdout and stderr copies.
//
// It decides nothing. It remembers the first refusal, the last STEP the card reached, and
// whether the card made another tool call after the refusal -- and it calls announce ONCE,
// with the typed line, the first time a refusal appears.
type WallReader struct {
	mu       sync.Mutex
	buf      []byte
	refusal  WallRefusal
	kind     string
	refused  bool
	movedOn  bool
	step     string
	announce func(string)
	task     string
}

// NewWallReader returns a reader for one card. announce is called once, from the writing
// goroutine, with the typed line of the first refusal; a nil announce says nothing.
func NewWallReader(task string, announce func(string)) *WallReader {
	return &WallReader{task: task, announce: announce}
}

// Write consumes the child's output and never fails: a reader that could fail a write would
// break the MultiWriter it sits in and take the card's own log down with it.
func (r *WallReader) Write(p []byte) (int, error) {
	if r == nil {
		return len(p), nil
	}
	r.mu.Lock()
	r.buf = append(r.buf, p...)
	var say string
	for {
		i := strings.IndexByte(string(r.buf), '\n')
		if i < 0 {
			break
		}
		line := strings.TrimSpace(stripPaint(string(r.buf[:i])))
		r.buf = r.buf[i+1:]
		if line == "" {
			continue
		}
		if rest, ok := strings.CutPrefix(line, WallStepMark); ok {
			fields := strings.Fields(rest)
			if len(fields) > 0 {
				if n := strings.TrimRight(fields[0], ".:)("); isAllDigits(n) {
					r.step = n
				}
			}
		}
		if r.refused {
			for _, mark := range wallToolMarks {
				if strings.HasPrefix(line, mark) {
					r.movedOn = true
					break
				}
			}
			continue
		}
		if p, ok := FenceRejection([]byte(line)); ok {
			r.refused, r.refusal, r.kind = true, WallRefusal{Path: p, Step: r.step}, "fence"
		} else if wallRefusalLine(line) {
			r.refused, r.refusal, r.kind = true, WallRefusal{Path: wallPathToken(line), Step: r.step}, WallKind(line)
		}
		if r.refused && say == "" {
			say = WallRefusedLine(r.task, r.kind, r.refusal.Path, r.step)
		}
	}
	announce := r.announce
	r.mu.Unlock()
	// OUTSIDE THE LOCK. announce is the caller's writer, and a writer that blocked while
	// this lock was held would stop the child's own output with it.
	if say != "" && announce != nil {
		announce(say)
	}
	return len(p), nil
}

// Stopped is the refusal that stopped this card -- a refusal with no tool call after it --
// and false when there was no refusal or the card moved past the one there was.
func (r *WallReader) Stopped() (WallRefusal, string, bool) {
	if r == nil {
		return WallRefusal{}, "", false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.refused || r.movedOn {
		return WallRefusal{}, "", false
	}
	return r.refusal, r.kind, true
}

// Step is the last `STEP <n>` the card printed, or "" when it printed none.
func (r *WallReader) Step() string {
	if r == nil {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.step
}

// BlockedResultName is the report a run writes FOR a card that never published one, so the
// coordinator's harvest reads a named end instead of an absence. It is `RESULT.md` because
// that is the one file every gather already looks for, and its first line carries no
// findings head, so it can be scored `plan-only` and NEVER `ok` or `clean`: a report the
// machinery wrote can never be counted as work a worker did.
const BlockedResultName = "RESULT.md"

// WriteBlockedResult writes the card's report when the run ended it and it published none.
// It REFUSES to overwrite a result that exists -- a card that published owns its report --
// and it says who wrote it on its own line, because a reader must never have to guess
// whether a worker or the machinery wrote what they are reading.
func WriteBlockedResult(jobDir, task, kind, path, step, reason string) (string, bool, error) {
	if _, found := FindCardResult(jobDir); found {
		return "", false, nil
	}
	dest := filepath.Join(jobDir, BlockedResultName)
	body := strings.Join([]string{
		"RESULT: BLOCKED " + oneline.Field(task),
		WallRefusedLine(task, kind, path, step),
		"blocked: " + oneline.Escape(reason),
		"written-by: nova-swarm native (the card published no report of its own)",
		"",
	}, "\n")
	if err := os.WriteFile(dest, []byte(body), 0o644); err != nil {
		return "", false, err
	}
	return dest, true, nil
}

// wallStoppedInLog reads one log file and reports the refusal that STOPPED the card in it,
// or false when the file cannot be read, holds no refusal, or holds one the card moved past.
func wallStoppedInLog(path string) (WallRefusal, bool) {
	raw, err := readRegular(path)
	if err != nil {
		return WallRefusal{}, false
	}
	return WallStopped(raw)
}
