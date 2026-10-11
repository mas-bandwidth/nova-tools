// Package filelock is the one lock on a file across processes: the kernel's lock
// (flock on unix, LockFileEx on Windows), released by the kernel when its holder
// dies, with the holder's stamp written inside the file for a refusal to name. Its
// design and invariants are tla/FileLock.tla. Lock acquisition is exclusive, with
// waiting or non-blocking forms; release clears the holder note, which names a holder
// in a refusal but never affects lock decisions.
//
// Its callers, on unix, each the same flock on the same file its earlier binary took, so
// an old and a new binary exclude each other across an upgrade (each package's
// *compat*_test.go or TestAnOldBinarysCreateLockKeepsTheNewOneOut): cmd/nova-sandbox
// (the volume-creation lock, darwin), pkg/swarm (the slot store's lock, behind an
// in-process turn), internal/tokens (the fold lock) and internal/update (the snapshot
// lock). Each creates a fresh lock file itself with the mode it always had. On Windows
// those three packages keep their own locks (an O_EXCL .held file, or byte 0) until a
// migration in which old and new still exclude each other: this lock is LockFileEx at
// OffsetHigh 0x80000000. pkg/bus keeps its own lock.
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

	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
)

var (
	// ErrHeld indicates the lock is currently held by an exclusive holder.
	ErrHeld = errors.New("lock held")
	// ErrBusy indicates the lock could not be acquired because only shared askers were present.
	// It is distinct from ErrHeld and does not wrap it.
	ErrBusy = errors.New("lock busy")
	// ErrTimeout indicates waiting for the lock timed out.
	ErrTimeout = errors.New("lock timeout")
	// ErrNotSupported indicates that filelock is not supported on the current platform.
	ErrNotSupported = errors.New("filelock: locking is not supported on this platform")
)

// HeldError describes a refusal because another process holds the lock,
// or a timeout occurred while waiting for the lock.
type HeldError struct {
	Path     string
	Holder   Stamp
	Wait     time.Duration
	TimedOut bool
}

func (e *HeldError) Error() string {
	cleanPath := oneline.Escape(oneline.Cap(e.Path, 1024))
	if !e.Holder.IsZero() {
		if e.Wait > 0 {
			return fmt.Sprintf("filelock %q is held by %s; waited %s: %v", cleanPath, e.Holder, e.Wait, ErrHeld)
		}
		return fmt.Sprintf("filelock %q is held by %s: %v", cleanPath, e.Holder, ErrHeld)
	}
	if e.Wait > 0 {
		return fmt.Sprintf("filelock %q is held; waited %s: %v", cleanPath, e.Wait, ErrHeld)
	}
	return fmt.Sprintf("filelock %q is held: %v", cleanPath, ErrHeld)
}

// Unwrap returns []error so that errors.Is(err, ErrHeld) and
// errors.Is(err, ErrTimeout) (if TimedOut) both match.
func (e *HeldError) Unwrap() []error {
	if e.TimedOut {
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
		fmt.Fprintf(&b, "host=%s\n", oneline.Field(oneline.Cap(s.Host, 256)))
	}
	if !s.Started.IsZero() {
		fmt.Fprintf(&b, "started=%s\n", s.Started.UTC().Format(time.RFC3339Nano))
	}
	if s.Label != "" {
		clean := strings.ReplaceAll(strings.ReplaceAll(oneline.Cap(s.Label, 1024), "\r", " "), "\n", " ")
		fmt.Fprintf(&b, "label=%s\n", oneline.Escape(clean))
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
		parts = append(parts, fmt.Sprintf("host=%s", oneline.Field(oneline.Cap(s.Host, 256))))
	}
	if !s.Started.IsZero() {
		parts = append(parts, fmt.Sprintf("started=%s", s.Started.UTC().Format(time.RFC3339)))
	}
	if s.Label != "" {
		parts = append(parts, fmt.Sprintf("label=%q", oneline.Escape(oneline.Cap(s.Label, 1024))))
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
				stamp.Host = oneline.Cap(strings.TrimSpace(v), 256)
			case "started", "start", "at":
				val := strings.TrimSpace(v)
				if t, err := time.Parse(time.RFC3339Nano, val); err == nil {
					stamp.Started = t.UTC()
				} else if t, err := time.Parse(time.RFC3339, val); err == nil {
					stamp.Started = t.UTC()
				}
			case "label":
				stamp.Label = oneline.Cap(v, 1024)
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
			stamp.Host = oneline.Cap(v, 256)
		case "started", "start", "at":
			if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
				stamp.Started = t.UTC()
			} else if t, err := time.Parse(time.RFC3339, v); err == nil {
				stamp.Started = t.UTC()
			}
		case "label":
			stamp.Label = oneline.Cap(strings.Trim(v, `"`), 1024)
		}
	}

	return stamp, nil
}

// ReadStamp reads and parses the Stamp from path without acquiring the lock.
// It opens with O_RDONLY and does not block on FIFOs.
func ReadStamp(path string) (Stamp, error) {
	f, err := openFileSafe(path, os.O_RDONLY, 0)
	if err != nil {
		return Stamp{}, err
	}
	defer func() { _ = f.Close() }() // ignored: the open is read-only, a close error loses nothing
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

type safePathError struct {
	err *os.PathError
}

func (e *safePathError) Error() string {
	if e.err == nil {
		return ""
	}
	return fmt.Sprintf("%s %s: %v", e.err.Op, oneline.Escape(oneline.Cap(e.err.Path, 1024)), e.err.Err)
}

func (e *safePathError) Unwrap() error {
	if e.err == nil {
		return nil
	}
	return e.err
}

func wrapPathError(err error) error {
	if err == nil {
		return nil
	}
	var pe *os.PathError
	if errors.As(err, &pe) {
		return &safePathError{err: pe}
	}
	return err
}

// clock abstracts time measurement and sleeping for fast, deterministic unit tests.
type clock interface {
	Now() time.Time
	Sleep(time.Duration)
}

// realClock uses the host operating system clock and time.Sleep.
type realClock struct{}

func (realClock) Now() time.Time        { return time.Now() }
func (realClock) Sleep(d time.Duration) { time.Sleep(d) }

// options configures internal lock, tryLock, and probe behavior.
type options struct {
	clock        clock
	pollInterval time.Duration
	jitter       func(time.Duration) time.Duration
	host         string
	pid          int
	sync         func(*os.File) error
	verifyInode  func(*os.File, string) (bool, error)
}

const defaultPollInterval = 25 * time.Millisecond

func defaultJitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	return d + time.Duration(rand.Int63n(int64(d/2)+1))
}

func (o options) getClock() clock {
	if o.clock != nil {
		return o.clock
	}
	return realClock{}
}

func (o options) getPollInterval() time.Duration {
	if o.pollInterval > 0 {
		return o.pollInterval
	}
	return defaultPollInterval
}

func (o options) getJitter(d time.Duration) time.Duration {
	if o.jitter != nil {
		return o.jitter(d)
	}
	return defaultJitter(d)
}

func (o options) getHost() string {
	if o.host != "" {
		return o.host
	}
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return h
}

func (o options) getPID() int {
	if o.pid > 0 {
		return o.pid
	}
	return os.Getpid()
}

func defaultSync(f *os.File) error {
	if f == nil {
		return errors.New("nil file")
	}
	return f.Sync()
}

func (o options) getSync() func(*os.File) error {
	if o.sync != nil {
		return o.sync
	}
	return defaultSync
}

// FileLock represents an acquired, open file lock.
type FileLock struct {
	file *os.File
	mu   sync.Mutex
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
		errs = append(errs, wrapPathError(err))
	}
	if _, err := l.file.Seek(0, 0); err != nil {
		errs = append(errs, wrapPathError(err))
	}
	if err := l.file.Sync(); err != nil {
		errs = append(errs, wrapPathError(err))
	}
	unlockFile(l.file)
	if err := l.file.Close(); err != nil {
		errs = append(errs, wrapPathError(err))
	}
	l.file = nil
	return errors.Join(errs...)
}

func lockLoop(path string, label string, timeout time.Duration, opts options, tryLockFn func(string, string, options) (*FileLock, error)) (*FileLock, error) {
	clk := opts.getClock()
	poll := opts.getPollInterval()
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

		sleepDur := opts.getJitter(poll)
		if sleepDur > remaining {
			sleepDur = remaining
		}
		clk.Sleep(sleepDur)
	}
}

// ranOut is the answer when the bound ran out: the last refusal, carrying the
// bound as the wait. A holder's refusal stays the *HeldError naming the note
// read on that last refusal, and answers ErrTimeout as well as ErrHeld, because
// the callers name the holder on a run-out (pkg/bus ErrLockHeld printing
// the pid, internal/tokens HolderPID). A
// refusal by askers alone (ErrBusy) names nobody and stays ErrBusy.
func ranOut(path string, timeout time.Duration, last error) error {
	if he, ok := AsHeldError(last); ok {
		he.Wait = timeout
		he.TimedOut = true
		return he
	}
	cleanPath := oneline.Escape(oneline.Cap(path, 1024))
	return fmt.Errorf("filelock %q: timeout after %v: %w: %w", cleanPath, timeout, ErrTimeout, wrapPathError(last))
}

// TryLock attempts to acquire the exclusive file lock on path without waiting.
//
// The lockfile path must be dedicated to filelock: taking an exclusive lock on an existing
// regular file truncates it when writing the holder stamp. The file is created if absent
// and is never removed.
func TryLock(path string, label string) (*FileLock, error) {
	return tryLockWithOptions(path, label, options{})
}

// Lock acquires the exclusive file lock on path, waiting up to timeout with jittered backoff.
//
// The lockfile path must be dedicated to filelock: taking an exclusive lock on an existing
// regular file truncates it when writing the holder stamp. The file is created if absent
// and is never removed.
func Lock(path string, label string, timeout time.Duration) (*FileLock, error) {
	return lockWithOptions(path, label, timeout, options{})
}
