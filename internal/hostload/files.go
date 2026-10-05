package hostload

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// A machine's open file descriptors, measured with its load (docs/SPEC-SPRINT.md section
// 5, the fleet). A sprint once ran its machine out of them, and only a workshop tool
// outside nova-sprint watched the count; the beat carries it now. The count is one cheap
// read (darwin: the sysctl kern.num_files; Linux: /proc/sys/fs/file-nr); the processes
// holding the most (darwin: lsof; Linux: /proc/<pid>/fd) cost a walk of every process,
// so they are read only over the warn bound, and at most once every HoldersEvery.

// The default bounds, the workshop tool's (fd-watch --warn and --alarm, 2026-10-03):
// a count of open descriptors above which the reading says warn, and alarm.
const (
	FilesWarnDefault  = 50000
	FilesAlarmDefault = 150000
)

// FilesTop is how many holders a reading lists.
const FilesTop = 10

// HoldersEvery is the least time between two reads of the holders: between them the
// last list stands.
const HoldersEvery = 30 * time.Second

// HoldersTimeout bounds one read of the holders, under a beat window: a read that runs
// over says so and the count stands with no list.
const HoldersTimeout = 10 * time.Second

// The levels of a reading against its bounds.
const (
	LevelOK    = "ok"
	LevelWarn  = "warn"
	LevelAlarm = "alarm"
)

// Holder is one process and how many file descriptors it has open.
type Holder struct {
	PID     int    `json:"pid"`
	Command string `json:"cmd"`
	User    string `json:"user,omitempty"`
	Open    int    `json:"open"`
}

// String is the holder as the beat and the judgment say it, the workshop tool's words:
// "node pid=300 user=rowan fds=40000".
func (h Holder) String() string {
	s := h.Command + " pid=" + strconv.Itoa(h.PID)
	if h.User != "" {
		s += " user=" + h.User
	}
	return s + " fds=" + strconv.Itoa(h.Open)
}

// Files is one reading of the machine's open file descriptors: when it was taken, the
// count and the system's limit (0 when not known), the bounds it was read against, and,
// over the warn bound, the processes holding the most, most first, when they were read,
// or why they could not be.
type Files struct {
	At     time.Time `json:"at"`
	Open   int       `json:"open"`
	Max    int       `json:"max,omitempty"`
	Warn   int       `json:"warn"`
	Alarm  int       `json:"alarm"`
	Top    []Holder  `json:"top,omitempty"`
	TopAt  time.Time `json:"top_at,omitzero"`
	TopErr string    `json:"top_err,omitempty"`
}

// Level is the reading against its bounds: alarm above the alarm bound, warn above the
// warn bound, else ok.
func (f Files) Level() string {
	switch {
	case f.Open > f.Alarm:
		return LevelAlarm
	case f.Open > f.Warn:
		return LevelWarn
	}
	return LevelOK
}

// TopText is the holders as one line, most first, or why they are not listed.
func (f Files) TopText() string {
	if len(f.Top) == 0 {
		if f.TopErr != "" {
			return "not listed: " + f.TopErr
		}
		return "none listed"
	}
	words := make([]string, len(f.Top))
	for i, h := range f.Top {
		words[i] = h.String()
	}
	return strings.Join(words, ", ")
}

// FilesBounds is a source's bounds, each zero or less its default.
func FilesBounds(src Source) (warn, alarm int) {
	warn, alarm = src.FilesWarn, src.FilesAlarm
	if warn <= 0 {
		warn = FilesWarnDefault
	}
	if alarm <= 0 {
		alarm = FilesAlarmDefault
	}
	return warn, alarm
}

// MeasureFiles is the reading at now: nil when the source cannot count the machine's
// descriptors. Over the warn bound it lists the top holders, read again once
// HoldersEvery has passed since the last read (the last reading's, prev), else kept.
func MeasureFiles(src Source, prev *Files, now time.Time) *Files {
	if src.OpenFiles == nil {
		return nil
	}
	open, limit, err := src.OpenFiles()
	if err != nil || open < 0 {
		return nil
	}
	f := &Files{At: now, Open: open, Max: max(limit, 0)}
	f.Warn, f.Alarm = FilesBounds(src)
	if f.Level() == LevelOK || src.Holders == nil {
		return f
	}
	if prev != nil && !prev.TopAt.IsZero() && !now.Before(prev.TopAt) && now.Sub(prev.TopAt) < HoldersEvery {
		f.Top, f.TopAt, f.TopErr = prev.Top, prev.TopAt, prev.TopErr
		return f
	}
	f.TopAt = now
	hs, err := src.Holders()
	if err != nil {
		f.TopErr = err.Error()
		return f
	}
	f.Top = TopHolders(hs, FilesTop)
	return f
}

// TopHolders is the n holders with the most open, most first, the lower pid first of a
// tie.
func TopHolders(hs []Holder, n int) []Holder {
	out := slices.Clone(hs)
	slices.SortFunc(out, func(a, b Holder) int {
		return cmp.Or(cmp.Compare(b.Open, a.Open), cmp.Compare(a.PID, b.PID))
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// ParseFileNr is Linux's /proc/sys/fs/file-nr: the handles allocated, the unused (0
// since 2.6) and the maximum.
func ParseFileNr(s string) (open, limit int, ok bool) {
	f := strings.Fields(s)
	if len(f) < 3 {
		return 0, 0, false
	}
	a, err1 := strconv.Atoi(f[0])
	m, err2 := strconv.Atoi(f[2])
	if err1 != nil || err2 != nil || a < 0 || m < 0 {
		return 0, 0, false
	}
	return a, m, true
}

// ParseLsof is the holders in lsof's field output (`lsof -n -P -F pcLf`): for each
// process a p line (its pid), c (its command) and L (its user), then an f line per open
// file; only numbered descriptors count (cwd, txt, mem and the like are none). A process
// with no numbered descriptor is no holder.
func ParseLsof(s string) []Holder {
	var out []Holder
	var cur *Holder
	flush := func() {
		if cur != nil && cur.Open > 0 {
			out = append(out, *cur)
		}
		cur = nil
	}
	for _, line := range strings.Split(s, "\n") {
		if line == "" {
			continue
		}
		v := line[1:]
		switch line[0] {
		case 'p':
			flush()
			if pid, err := strconv.Atoi(v); err == nil {
				cur = &Holder{PID: pid}
			}
		case 'c':
			if cur != nil {
				cur.Command = v
			}
		case 'L':
			if cur != nil {
				cur.User = v
			}
		case 'f':
			if _, err := strconv.Atoi(v); err == nil && cur != nil {
				cur.Open++
			}
		}
	}
	flush()
	return out
}

// HoldersTimedOut is the reason a holders' read that ran past HoldersTimeout gives.
func HoldersTimedOut(tool string) error {
	return fmt.Errorf("%s timed out after %s", tool, HoldersTimeout)
}
