package filelock

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// State is the observation of a lock file path.
type State string

const (
	// StateAbsent means the lock file does not exist.
	StateAbsent State = "absent"
	// StateFree means the lock file exists and is not held by any process.
	StateFree State = "free"
	// StateHeld means the lock file exists and is held by a live process.
	StateHeld State = "held"
	// StateStale means the lock file exists and names a holder whose process is dead.
	StateStale State = "stale"
)

func (s State) String() string {
	return string(s)
}

var (
	// ErrHeld indicates the lock is currently held by another process.
	ErrHeld = errors.New("lock held")
	// ErrStale indicates the lock is stale because its recorded holder is dead.
	// It is never stolen silently.
	ErrStale = errors.New("lock stale")
	// ErrNotSupported indicates that filelock is not supported on the current platform.
	ErrNotSupported = errors.New("filelock: locking is not supported on windows")
)

// HeldError describes a refusal because another live process holds the lock.
type HeldError struct {
	Path   string
	Holder Stamp
	Wait   time.Duration
}

func (e *HeldError) Error() string {
	if e.Holder.PID > 0 {
		return fmt.Sprintf("filelock %s is held by %s; waited %s: %v", e.Path, e.Holder, e.Wait, ErrHeld)
	}
	return fmt.Sprintf("filelock %s is held; waited %s: %v", e.Path, e.Wait, ErrHeld)
}

func (e *HeldError) Unwrap() error {
	return ErrHeld
}

// AsHeldError reports whether err is a *HeldError.
func AsHeldError(err error) (*HeldError, bool) {
	var h *HeldError
	ok := errors.As(err, &h)
	return h, ok
}

// StaleError describes a refusal because the lock is stale (holder is dead)
// and cannot be stolen silently.
type StaleError struct {
	Path   string
	Holder Stamp
}

func (e *StaleError) Error() string {
	if e.Holder.PID > 0 {
		return fmt.Sprintf("filelock %s is stale (holder %s is dead): %v", e.Path, e.Holder, ErrStale)
	}
	return fmt.Sprintf("filelock %s is stale: %v", e.Path, ErrStale)
}

func (e *StaleError) Unwrap() error {
	return ErrStale
}

// AsStaleError reports whether err is a *StaleError.
func AsStaleError(err error) (*StaleError, bool) {
	var s *StaleError
	ok := errors.As(err, &s)
	return s, ok
}

// Stamp records who holds the lock: process ID, host name, start time, and label.
type Stamp struct {
	PID     int
	Host    string
	Started time.Time
	Label   string
}

// IsZero reports whether the stamp is empty.
func (s Stamp) IsZero() bool {
	return s.PID == 0 && s.Host == "" && s.Started.IsZero() && s.Label == ""
}

// Format returns the canonical multi-line representation of the stamp.
func (s Stamp) Format() string {
	var b strings.Builder
	if s.PID > 0 {
		fmt.Fprintf(&b, "pid=%d\n", s.PID)
	}
	if s.Host != "" {
		fmt.Fprintf(&b, "host=%s\n", s.Host)
	}
	if !s.Started.IsZero() {
		fmt.Fprintf(&b, "started=%s\n", s.Started.UTC().Format(time.RFC3339Nano))
	}
	if s.Label != "" {
		clean := strings.ReplaceAll(strings.ReplaceAll(s.Label, "\r", " "), "\n", " ")
		fmt.Fprintf(&b, "label=%s\n", clean)
	}
	return b.String()
}

// String returns a single-line summary of the stamp.
func (s Stamp) String() string {
	if s.IsZero() {
		return "-"
	}
	var parts []string
	if s.PID > 0 {
		parts = append(parts, fmt.Sprintf("pid=%d", s.PID))
	}
	if s.Host != "" {
		parts = append(parts, fmt.Sprintf("host=%s", s.Host))
	}
	if !s.Started.IsZero() {
		parts = append(parts, fmt.Sprintf("started=%s", s.Started.UTC().Format(time.RFC3339)))
	}
	if s.Label != "" {
		parts = append(parts, fmt.Sprintf("label=%s", s.Label))
	}
	return strings.Join(parts, " ")
}

// ParseStamp parses a Stamp from raw text. It accepts multi-line key=value pairs,
// single-line space-separated key=value pairs, legacy "pid=N at=RFC3339", or bare PID.
func ParseStamp(s string) (Stamp, error) {
	if strings.TrimSpace(s) == "" || strings.TrimSpace(s) == "-" {
		return Stamp{}, nil
	}
	trimmed := strings.Trim(s, "\r\n")
	if n, err := strconv.Atoi(strings.TrimSpace(trimmed)); err == nil && n > 0 {
		return Stamp{PID: n}, nil
	}

	var stamp Stamp
	lines := strings.Split(trimmed, "\n")
	if len(lines) > 1 {
		for _, line := range lines {
			line = strings.TrimRight(line, "\r")
			if strings.TrimSpace(line) == "" {
				continue
			}
			k, v, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			k = strings.TrimSpace(k)
			switch k {
			case "pid":
				if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
					stamp.PID = n
				}
			case "host":
				stamp.Host = strings.TrimSpace(v)
			case "started", "start", "at":
				val := strings.TrimSpace(v)
				if t, err := time.Parse(time.RFC3339Nano, val); err == nil {
					stamp.Started = t.UTC()
				} else if t, err := time.Parse(time.RFC3339, val); err == nil {
					stamp.Started = t.UTC()
				}
			case "label":
				stamp.Label = v
			}
		}
		if !stamp.IsZero() {
			return stamp, nil
		}
	}

	for _, tok := range strings.Fields(trimmed) {
		k, v, ok := strings.Cut(tok, "=")
		if !ok {
			continue
		}
		switch k {
		case "pid":
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				stamp.PID = n
			}
		case "host":
			stamp.Host = v
		case "started", "start", "at":
			if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
				stamp.Started = t.UTC()
			} else if t, err := time.Parse(time.RFC3339, v); err == nil {
				stamp.Started = t.UTC()
			}
		case "label":
			stamp.Label = v
		}
	}

	return stamp, nil
}

// ReadStamp reads and parses the Stamp from path.
func ReadStamp(path string) (Stamp, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Stamp{}, err
	}
	return ParseStamp(string(raw))
}

// Clock abstracts time measurement and sleeping for fast, deterministic unit tests.
type Clock interface {
	Now() time.Time
	Sleep(time.Duration)
}

// RealClock uses the host operating system clock and time.Sleep.
type RealClock struct{}

func (RealClock) Now() time.Time        { return time.Now() }
func (RealClock) Sleep(d time.Duration) { time.Sleep(d) }

// LockStepClock is an in-memory virtual clock for testing bounded waits without sleeping.
type LockStepClock struct {
	mu    sync.Mutex
	start time.Time
	now   time.Time
}

// NewLockStepClock returns a LockStepClock initialized to start (or a default fixed time if zero).
func NewLockStepClock(start time.Time) *LockStepClock {
	if start.IsZero() {
		start = time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	}
	return &LockStepClock{start: start, now: start}
}

func (c *LockStepClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *LockStepClock) Sleep(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// Waited returns the elapsed virtual duration since clock creation.
func (c *LockStepClock) Waited() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now.Sub(c.start)
}

// Options configures Lock, TryLock, and Probe behavior.
type Options struct {
	Clock        Clock
	ProcessAlive func(pid int) bool
	PollInterval time.Duration
	Jitter       func(time.Duration) time.Duration
	Host         string
}

const defaultPollInterval = 25 * time.Millisecond

func defaultJitter(d time.Duration) time.Duration {
	return d + time.Duration(rand.Int63n(int64(d/2)+1))
}

func (o Options) clock() Clock {
	if o.Clock != nil {
		return o.Clock
	}
	return RealClock{}
}

func (o Options) isAlive(pid int) bool {
	if o.ProcessAlive != nil {
		return o.ProcessAlive(pid)
	}
	return ProcessAlive(pid)
}

func (o Options) pollInterval() time.Duration {
	if o.PollInterval > 0 {
		return o.PollInterval
	}
	return defaultPollInterval
}

func (o Options) jitter(d time.Duration) time.Duration {
	if o.Jitter != nil {
		return o.Jitter(d)
	}
	return defaultJitter(d)
}

func (o Options) host() string {
	if o.Host != "" {
		return o.Host
	}
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return h
}

// FileLock represents an acquired, open file lock.
type FileLock struct {
	path     string
	file     *os.File
	stamp    Stamp
	released bool
	mu       sync.Mutex
}

// Path returns the path of the locked file.
func (l *FileLock) Path() string {
	return l.path
}

// Stamp returns the stamp written by this holder when taking the lock.
func (l *FileLock) Stamp() Stamp {
	return l.stamp
}

// String returns a description of the held lock.
func (l *FileLock) String() string {
	return fmt.Sprintf("lock %s held by %s", l.path, l.stamp)
}

func kindOfMode(mode os.FileMode) string {
	switch {
	case mode.IsDir():
		return "directory"
	case mode&os.ModeNamedPipe != 0:
		return "fifo"
	case mode&os.ModeSocket != 0:
		return "socket"
	case mode&os.ModeDevice != 0:
		return "device"
	case mode&os.ModeSymlink != 0:
		return "symlink"
	}
	return "something that is not a regular file"
}
