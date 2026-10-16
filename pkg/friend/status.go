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

	"github.com/mas-bandwidth/nova-tools/pkg/atomicfile"
)

// A friend's status, from evidence (docs/SPEC-FRIEND.md, "A friend's status,
// from evidence"). Whether she is there is the nova-hook record's alone (its
// per-minute challenge, which only her model answers); the daemon's status file
// says only that the daemon's loop runs. The status is decided by FriendStatus
// in order, and the evidence is shown beside it.

// LimitFile is the friend's limit, in the state directory: the provider's
// limit the session is at and when it resets. The daemon is its one writer;
// no file is no limit.
const LimitFile = "limit.json"

// The hook record's state a friend is up in (nova-tools hook.StateProven).
const HookProven = "proven"

// HookRecord is a friend's nova-hook record as status and check read it
// (nova-tools hook.Status): its state, since when, the last answer to a
// challenge, the challenges missed in a row, and why it reads down.
type HookRecord struct {
	State      string    // none, armed, proven, unhooked, ...; "" when not read
	Since      time.Time // when it was hooked, or unhooked; zero when never
	LastAnswer time.Time // when her model last answered a challenge; zero is never
	Misses     int       // challenges in a row left unanswered
	Down       string    // why it reads down whatever its state (unhooked, no server, no answer), "" when it stands
	ReadError  string    // why the record could not be read; "" when it was
}

// Up says the record proves her there: proven, and nothing reads it down.
func (h HookRecord) Up() bool { return h.State == HookProven && h.Down == "" && h.ReadError == "" }

// Words is the record as a person reads it on one line.
func (h HookRecord) Words(now time.Time) string {
	if h.ReadError != "" {
		return "hook record not read: " + h.ReadError
	}
	state := h.State
	if state == "" {
		state = "none"
	}
	out := "hook " + state
	if !h.Since.IsZero() {
		out += " since " + Ago(now.Sub(h.Since)) + " ago"
	}
	if h.LastAnswer.IsZero() {
		out += ", never answered"
	} else {
		out += ", answer " + Ago(now.Sub(h.LastAnswer)) + " ago"
	}
	if h.Misses > 0 {
		out += fmt.Sprintf(", %d missed", h.Misses)
	}
	if h.Down != "" {
		out += ": " + h.Down
	}
	return out
}

// Evidence is what a friend's status is decided from.
type Evidence struct {
	DaemonUp    bool       // the daemon's status file is fresh; shown, never deciding up
	Hook        HookRecord // her nova-hook record: the one truth of whether she is there
	Limit       string     // the limit's name, when the provider said one
	LimitUntil  time.Time  // the limit's reset; zero, or past, is no limit
	Undelivered int        // messages waiting on the friend's stream; negative is not counted
	BusBlocked  string     // why the bus cannot deliver to the friend; empty when it can
	LastResult  time.Time  // the end of the last turn; zero is none yet
	LastExit    int        // that turn's exit
}

// Verdict is a friend's status, the one reason that decided it, and every
// piece of evidence as a person reads it.
type Verdict struct {
	Status   string   `json:"status"` // up or down
	Reason   string   `json:"reason"`
	Evidence []string `json:"evidence"`
}

// FriendStatus decides a friend's status from evidence, in order: at a limit
// is down until the reset; a hook record that does not prove her there is
// down; a bus that cannot deliver to her is down; otherwise up. Times are shown
// in loc.
func FriendStatus(e Evidence, now time.Time, loc *time.Location) Verdict {
	hook := e.Hook.Words(now)
	limit := "no limit"
	limited := e.LimitUntil.After(now)
	if limited {
		limit = strings.TrimSpace(e.Limit + " limit until " + e.LimitUntil.In(loc).Format("Mon 3:04 PM"))
	}
	result := "no result yet"
	if !e.LastResult.IsZero() {
		result = fmt.Sprintf("last result %s exit=%d", Ago(now.Sub(e.LastResult)), e.LastExit)
	}
	undelivered := fmt.Sprintf("%d undelivered", e.Undelivered)
	if e.Undelivered < 0 {
		undelivered = "undelivered not counted"
	}
	v := Verdict{Status: "down", Evidence: []string{hook, limit, undelivered, result}}
	switch {
	case limited:
		v.Reason = limit
	case !e.Hook.Up():
		v.Reason = hook
	case e.BusBlocked != "":
		v.Reason = fmt.Sprintf("bus cannot deliver: %s (%s)", e.BusBlocked, undelivered)
	default:
		v.Status, v.Reason = "up", hook
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

// LaneFaultFile is the lane's provider-refusal fault report in the state
// directory (lane-fault=<n> refusal="<first line>"), written by the daemon
// when a lane faults and removed when the fault is cleared; absent is no fault
// (docs/SPEC-FRIEND.md, the lanes).
const LaneFaultFile = "lane-fault"

// WriteLaneFault records a lane's provider-refusal fault report; an empty
// report removes the file (a clear, or a stale file a restart cleared).
func WriteLaneFault(stateDir, report string) error {
	path := filepath.Join(stateDir, LaneFaultFile)
	if report == "" {
		err := os.Remove(path)
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return err
	}
	return atomicfile.WriteFile(path, []byte(report+"\n"), 0o644)
}

// ReadLaneFault is the lane fault report, or "" when there is none.
func ReadLaneFault(stateDir string) string {
	raw, err := os.ReadFile(filepath.Join(stateDir, LaneFaultFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// ClearLaneFault removes the lane fault file, the person's clear; whether one
// was there.
func ClearLaneFault(stateDir string) (bool, error) {
	err := os.Remove(filepath.Join(stateDir, LaneFaultFile))
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}

// resultTail is how much of the end of the log LastResult reads.
const resultTail = 64 << 10

// LastResult is the newest turn's line of the daemon's log (the line a turn's
// end writes, "<RFC3339> lane=<n> ... exit=<n>"): when it ended and its exit.
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
		if !ok || !strings.HasPrefix(rest, "subject=") && !strings.HasPrefix(rest, "lane=") { // a turn's line: a lane's, or one from before the lanes
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
