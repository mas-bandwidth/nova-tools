package friend

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// A friend's status, from evidence (docs/SPEC-FRIEND.md, "A friend's status,
// from evidence"). The owner, 2026-10-04: "are friends actually doing work?"
// The daemon's beat says only that the daemon's loop runs; the status is
// decided by FriendStatus from what the friend did, in order, and the
// evidence is shown beside it.

// AnswerBound is how old the session's last answer may be while the friend
// is up: two windows, a ping each window and a challenge open for less than
// one, so a session answering every ping is never older than this.
const AnswerBound = 2 * Window

// LimitFile is the friend's limit, in the state directory: the provider's
// limit the session is at and when it resets. The daemon is its one writer;
// no file is no limit.
const LimitFile = "limit.json"

// The harness evidence: whether the harness's process was seen in the
// process table. Advisory, shown and never deciding: a session run from its
// command line has no app to see, and an app that runs answers nothing.
// HarnessNotSeen is defined in alive.go ("not-seen").
const (
	HarnessUnknown = ""
	HarnessRunning = "running"
)

// Evidence is what a friend's status is decided from.
type Evidence struct {
	Harness     string    // HarnessRunning, HarnessNotSeen or HarnessUnknown; shown, never deciding
	DaemonUp    bool      // the daemon's status file is fresh; shown, never deciding up
	LastAnswer  time.Time // the session's last pong; zero is never
	Limit       string    // the limit's name, when the provider said one
	LimitUntil  time.Time // the limit's reset; zero, or past, is no limit
	Undelivered int       // messages waiting on the friend's stream; negative is not counted
	BusBlocked  string    // why the bus cannot deliver to the friend; empty when it can
	LastResult  time.Time // the end of the last turn; zero is none yet
	LastExit    int       // that turn's exit
}

// Verdict is a friend's status, the one reason that decided it, and every
// piece of evidence as a person reads it.
type Verdict struct {
	Status   string   `json:"status"` // up or down
	Reason   string   `json:"reason"`
	Evidence []string `json:"evidence"`
}

// FriendStatus decides a friend's status from evidence, in order: at a limit
// is down until the reset; no session answer within bound is down; a bus that
// cannot deliver to her is down; otherwise up. The harness's process is shown
// and decides nothing (the finding of 2026-10-05). Times are shown in loc.
func FriendStatus(e Evidence, now time.Time, bound time.Duration, loc *time.Location) Verdict {
	answer := "no session answer ever"
	answered := !e.LastAnswer.IsZero() && now.Sub(e.LastAnswer) < bound
	if !e.LastAnswer.IsZero() {
		answer = "session answer " + Ago(now.Sub(e.LastAnswer))
		if !answered {
			answer = "no session answer " + Ago(now.Sub(e.LastAnswer))
		}
	}
	limit := "no limit"
	limited := e.LimitUntil.After(now)
	if limited {
		limit = strings.TrimSpace(e.Limit + " limit until " + e.LimitUntil.In(loc).Format("Mon 3:04 PM"))
	}
	harness := "harness unknown"
	switch e.Harness {
	case HarnessRunning:
		harness = "harness running"
	case HarnessNotSeen, "not running":
		harness = "harness not seen"
	case HarnessUnknown:
		harness = "harness unknown"
	default:
		harness = "harness " + e.Harness
	}
	result := "no result yet"
	if !e.LastResult.IsZero() {
		result = fmt.Sprintf("last result %s exit=%d", Ago(now.Sub(e.LastResult)), e.LastExit)
	}
	undelivered := fmt.Sprintf("%d undelivered", e.Undelivered)
	if e.Undelivered < 0 {
		undelivered = "undelivered not counted"
	}
	v := Verdict{Status: "down", Evidence: []string{harness, answer, limit, undelivered, result}}
	switch {
	case limited:
		v.Reason = limit
	case !answered:
		v.Reason = answer
	case e.BusBlocked != "":
		v.Reason = fmt.Sprintf("bus cannot deliver: %s (%s)", e.BusBlocked, undelivered)
	default:
		v.Status, v.Reason = "up", answer
	}
	return v
}

// Ago is a duration as a person reads it on the table: 40s, 12m, 3h5m, 2d.
func Ago(d time.Duration) string {
	switch {
	case d < 0:
		return "0s"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d/time.Second))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d < 24*time.Hour:
		h, m := int(d/time.Hour), int(d%time.Hour/time.Minute)
		if m == 0 {
			return fmt.Sprintf("%dh", h)
		}
		return fmt.Sprintf("%dh%dm", h, m)
	}
	return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
}

// ReadLimitFile reads the limit file; found is false when there is none.
func ReadLimitFile(stateDir string) (l Limit, found bool, err error) {
	found, err = read(filepath.Join(stateDir, LimitFile), &l)
	return l, found, err
}

// resultTail is how much of the end of the log LastResult reads.
const resultTail = 64 << 10

// LastResult is the newest turn's line of the daemon's log (the line a turn's
// end writes, "<RFC3339> subject=... exit=<n>"): when it ended and its exit.
// No log, or no turn in it, is the zero time.
func LastResult(stateDir string) (at time.Time, exit int, err error) {
	f, err := os.Open(LogPath(stateDir))
	if os.IsNotExist(err) {
		return time.Time{}, 0, nil
	}
	if err != nil {
		return time.Time{}, 0, err
	}
	defer f.Close() // ignored: read-only
	fi, err := f.Stat()
	if err != nil {
		return time.Time{}, 0, err
	}
	off := max(fi.Size()-resultTail, 0)
	buf := make([]byte, fi.Size()-off)
	if _, err := f.ReadAt(buf, off); err != nil {
		return time.Time{}, 0, err
	}
	sc := bufio.NewScanner(bytes.NewReader(buf))
	sc.Buffer(make([]byte, 0, 64<<10), resultTail)
	for sc.Scan() {
		stamp, rest, ok := strings.Cut(sc.Text(), " ")
		if !ok || !strings.HasPrefix(rest, "subject=") {
			continue
		}
		t, terr := time.Parse(time.RFC3339, stamp)
		if terr != nil {
			continue
		}
		for _, field := range strings.Fields(rest) {
			if v, ok := strings.CutPrefix(field, "exit="); ok {
				if n, nerr := strconv.Atoi(v); nerr == nil {
					at, exit = t, n
				}
			}
		}
	}
	return at, exit, sc.Err()
}
