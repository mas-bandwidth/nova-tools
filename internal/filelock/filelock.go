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
)

func (s State) String() string {
	return string(s)
}

var (
	// ErrHeld indicates the lock is currently held by another process.
	ErrHeld = errors.New("lock held")
	// ErrTimeout indicates waiting for the lock timed out.
	ErrTimeout = errors.New("lock timeout")
	// ErrBusy indicates the take was refused by askers alone (a Probe, or
	// another refused taker asking the shared lock) maxAsks times in a row:
	// nobody holds the lock, so this is never ErrHeld. Try again.
	ErrBusy = errors.New("lock busy")
	// ErrNotSupported indicates that filelock is not supported on the current platform.
	ErrNotSupported = errors.New("filelock: locking is not supported on this platform")
)

// HeldError describes a refusal because another process holds the lock. From
// a bounded Lock that ran out, Holder is the note read on the last refusal,
// Wait is the bound, and the error answers ErrTimeout as well as ErrHeld.
type HeldError struct {
	Path   string
	Holder Stamp
	Wait   time.Duration

	timedOut bool // set by lockLoop when the bound ran out
}

func (e *HeldError) Error() string {
	if !e.Holder.IsZero() {
		if e.Wait > 0 {
			return fmt.Sprintf("filelock %q is held by %s; waited %s: %v", e.Path, e.Holder, e.Wait, ErrHeld)
		}
		return fmt.Sprintf("filelock %q is held by %s: %v", e.Path, e.Holder, ErrHeld)
	}
	if e.Wait > 0 {
		return fmt.Sprintf("filelock %q is held; waited %s: %v", e.Path, e.Wait, ErrHeld)
	}
	return fmt.Sprintf("filelock %q is held: %v", e.Path, ErrHeld)
}

func (e *HeldError) Unwrap() []error {
	if e.timedOut {
		return []error{ErrHeld, ErrTimeout}
	}
	return []error{ErrHeld}
}

// AsHeldError reports whether err is a *HeldError.
func AsHeldError(err error) (*HeldError, bool) {
	var h *HeldError
	ok := errors.As(err, &h)
	return h, ok
}

// Stamp records diagnostic details of who holds the lock: PID, host, start time, and label.
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
		parts = append(parts, fmt.Sprintf("label=%q", s.Label))
	}
	return strings.Join(parts, " ")
}

// ParseStamp parses a Stamp from raw text.
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
			stamp.Label = strings.Trim(v, `"`)
		}
	}

	return stamp, nil
}

// ReadStamp reads and parses the Stamp from path without acquiring the lock.
func ReadStamp(path string) (Stamp, error) {
	f, err := os.Open(path)
	if err != nil {
		return Stamp{}, err
	}
	defer f.Close()
	return readExistingStamp(f), nil
}

func readExistingStamp(f *os.File) Stamp {
	if f == nil {
		return Stamp{}
	}
	buf := make([]byte, 4096)
	n, err := f.ReadAt(buf, 0)
	if err != nil && n == 0 {
		return Stamp{}
	}
	st, err := ParseStamp(string(buf[:n]))
	if err != nil {
		return Stamp{}
	}
	return st
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
	PollInterval time.Duration
	Jitter       func(time.Duration) time.Duration
	Host         string
	PID          int
}

const defaultPollInterval = 25 * time.Millisecond

func defaultJitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	return d + time.Duration(rand.Int63n(int64(d/2)+1))
}

func (o Options) clock() Clock {
	if o.Clock != nil {
		return o.Clock
	}
	return RealClock{}
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

func (o Options) pid() int {
	if o.PID > 0 {
		return o.PID
	}
	return os.Getpid()
}

// FileLock represents an acquired, open file lock.
type FileLock struct {
	path     string
	file     *os.File
	stamp    Stamp
	previous *Stamp
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

// Previous returns the stamp of the previous unreleased holder (e.g. killed/crashed), or nil.
func (l *FileLock) Previous() *Stamp {
	return l.previous
}

// String returns a description of the held lock.
func (l *FileLock) String() string {
	return fmt.Sprintf("filelock %q held by %s", l.path, l.stamp)
}

// Unlock releases the file lock by truncating the file to zero bytes, syncing,
// releasing the OS lock, and closing the descriptor. The file is NEVER deleted.
// Calling Unlock multiple times is safe and returns nil on subsequent calls.
func (l *FileLock) Unlock() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}

	var errs []error
	if err := l.file.Truncate(0); err != nil {
		errs = append(errs, err)
	}
	if _, err := l.file.Seek(0, 0); err != nil {
		errs = append(errs, err)
	}
	if err := l.file.Sync(); err != nil {
		errs = append(errs, err)
	}
	unlockFile(l.file)
	if err := l.file.Close(); err != nil {
		errs = append(errs, err)
	}
	l.file = nil
	return errors.Join(errs...)
}

func lockLoop(path string, label string, timeout time.Duration, opts Options, tryLockFn func(string, string, Options) (*FileLock, error)) (*FileLock, error) {
	clk := opts.clock()
	poll := opts.pollInterval()
	deadline := clk.Now().Add(timeout)

	for {
		lock, err := tryLockFn(path, label, opts)
		if err == nil {
			return lock, nil
		}
		if !errors.Is(err, ErrHeld) && !errors.Is(err, ErrBusy) {
			return nil, err
		}

		now := clk.Now()
		remaining := deadline.Sub(now)
		if remaining <= 0 {
			// One final non-blocking attempt right at deadline before giving up
			lock, lastErr := tryLockFn(path, label, opts)
			if lastErr == nil {
				return lock, nil
			}
			return nil, ranOut(path, timeout, lastErr)
		}

		sleepDur := opts.jitter(poll)
		if sleepDur > remaining {
			sleepDur = remaining
		}
		clk.Sleep(sleepDur)
	}
}

// ranOut is the answer when the bound ran out: the last refusal, carrying the
// bound as the wait. A holder's refusal stays the *HeldError naming the note
// read on that last refusal, and answers ErrTimeout as well as ErrHeld, because
// the callers name the holder on a run-out (internal/merge AsHeldError for exit
// 2, internal/bus ErrLockHeld printing the pid, internal/tokens HolderPID). A
// refusal by askers alone (ErrBusy) names nobody and stays ErrBusy.
func ranOut(path string, timeout time.Duration, last error) error {
	if he, ok := AsHeldError(last); ok {
		he.Wait = timeout
		he.timedOut = true
		return he
	}
	return fmt.Errorf("filelock %q: timeout after %v: %w: %w", path, timeout, ErrTimeout, last)
}

// TryLock attempts to acquire the exclusive file lock on path without waiting.
func TryLock(path string, label string) (*FileLock, error) {
	return TryLockWithOptions(path, label, Options{})
}

// Lock acquires the exclusive file lock on path, waiting up to timeout with jittered backoff.
func Lock(path string, label string, timeout time.Duration) (*FileLock, error) {
	return LockWithOptions(path, label, timeout, Options{})
}

// Probe inspects path without taking an exclusive lock and without creating the file if absent.
func Probe(path string) (State, Stamp, error) {
	return ProbeWithOptions(path, Options{})
}
