package control

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const defaultLockTimeout = 10 * time.Second

// Handle is an open control directory store.
type Handle struct {
	controlDir  string
	maxRUN      time.Duration
	lockTimeout time.Duration
}

// Open opens the control store at the named directory with a required positive
// maxRUN bound. Empty controlDir is refused. The package reads and writes
// controlDir/state.json, controlDir/acks/<owner>.json, and
// controlDir/coordinator.lock. It does not join "control/" itself.
func Open(controlDir string, maxRUN time.Duration) (*Handle, error) {
	cleanDir := filepath.Clean(strings.TrimSpace(controlDir))
	if cleanDir == "" || cleanDir == "." && strings.TrimSpace(controlDir) == "" {
		return nil, errors.New("controlDir cannot be empty")
	}
	if maxRUN <= 0 {
		return nil, errors.New("maxRUN must be a positive duration")
	}

	if err := ensurePlainDir(cleanDir); err != nil {
		return nil, fmt.Errorf("creating control directory %s: %w", cleanDir, err)
	}
	acks := filepath.Join(cleanDir, "acks")
	if err := ensurePlainDir(acks); err != nil {
		return nil, fmt.Errorf("creating acks directory %s: %w", acks, err)
	}

	return &Handle{
		controlDir:  cleanDir,
		maxRUN:      maxRUN,
		lockTimeout: defaultLockTimeout,
	}, nil
}

func (h *Handle) statePath() string {
	return filepath.Join(h.controlDir, "state.json")
}

func (h *Handle) lockPath() string {
	return filepath.Join(h.controlDir, "coordinator.lock")
}

func (h *Handle) acksDir() string {
	return filepath.Join(h.controlDir, "acks")
}

func (h *Handle) ackPath(owner string) string {
	return filepath.Join(h.acksDir(), owner+".json")
}

// Load reads and parses control/state.json without acquiring coordinator.lock.
// It fails closed if the file does not exist, is unreadable, or is malformed.
// An absent state is not implicit RUN.
func (h *Handle) Load(now time.Time) (State, error) {
	return h.loadUnlocked(now)
}

func (h *Handle) loadUnlocked(now time.Time) (State, error) {
	_ = now
	data, err := os.ReadFile(h.statePath())
	if err != nil {
		if os.IsNotExist(err) {
			return State{}, fmt.Errorf("%w: %s", ErrStateNotFound, h.statePath())
		}
		return State{}, fmt.Errorf("reading control state: %w", err)
	}

	var st State
	if err := decodeJSON(data, &st); err != nil {
		return State{}, fmt.Errorf("malformed control state in %s: %w", h.statePath(), err)
	}
	if err := ValidateState(st); err != nil {
		return State{}, fmt.Errorf("invalid control state in %s: %w", h.statePath(), err)
	}
	return st, nil
}

// Update atomically and durably updates control/state.json under coordinator.lock
// after validating CAS against expectedGeneration.
// Initialization from absent state requires expectedGeneration == 0 and creates generation 1.
// Renewal requires generation n+1 and never extends in-place.
// PAUSE, DRAIN, and STOP updates work even after an earlier RUN has expired.
func (h *Handle) Update(now time.Time, expectedGeneration int64, nextState State) (State, error) {
	unlock, err := takeCoordinatorLock(context.Background(), h.lockPath(), h.lockTimeout)
	if err != nil {
		return State{}, fmt.Errorf("acquiring coordinator lock for update: %w", err)
	}
	defer unlock()

	current, err := h.loadUnlocked(now)
	var currentGen int64
	if err != nil {
		if !errors.Is(err, ErrStateNotFound) {
			return State{}, fmt.Errorf("reading current state under coordinator lock: %w", err)
		}
		currentGen = 0
	} else {
		currentGen = current.Generation
	}

	if nextState.Generation == 0 {
		nextState.Generation = expectedGeneration + 1
	}
	if strings.TrimSpace(nextState.Scope) == "" {
		nextState.Scope = ScopeFleet
	}
	if nextState.At.IsZero() {
		nextState.At = now
	}

	if err := ValidateUpdate(currentGen, expectedGeneration, nextState, now, h.maxRUN); err != nil {
		return State{}, err
	}

	if err := writeDurableJSON(h.statePath(), nextState); err != nil {
		return State{}, fmt.Errorf("durable write of control state: %w", err)
	}
	return nextState, nil
}

// WriteAck durably records a bench acknowledgement in control/acks/<owner>.json.
// The unique owner binds exactly one bench and never grants admission.
// WriteAck is called after STARTING; missing/stale ack fails closed for new starts,
// but does not roll back STARTING.
func (h *Handle) WriteAck(now time.Time, ack Ack) error {
	if ack.Observed.IsZero() {
		ack.Observed = now
	}
	if err := ValidateAck(ack); err != nil {
		return err
	}
	unlock, err := takeCoordinatorLock(context.Background(), h.lockPath(), h.lockTimeout)
	if err != nil {
		return fmt.Errorf("acquiring coordinator lock for ack: %w", err)
	}
	defer unlock()

	if err := h.refuseOwnerRebind(ack); err != nil {
		return err
	}
	if err := writeDurableJSON(h.ackPath(ack.Owner), ack); err != nil {
		return fmt.Errorf("durable write of ack for %s: %w", ack.Owner, err)
	}
	return nil
}

func (h *Handle) refuseOwnerRebind(ack Ack) error {
	existing, err := h.readAckFile(ack.Owner)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if existing.Owner != ack.Owner {
		return fmt.Errorf("%w: existing ack owner %q does not match filename %q", ErrInvalidAck, existing.Owner, ack.Owner)
	}
	if existing.Bench != ack.Bench {
		return fmt.Errorf("%w: owner %q is bound to bench %q", ErrInvalidAck, ack.Owner, existing.Bench)
	}
	return nil
}

// LoadAck reads and validates a single bench acknowledgement from control/acks/<owner>.json.
func (h *Handle) LoadAck(owner string) (Ack, error) {
	owner = strings.TrimSpace(owner)
	if owner == "" || strings.ContainsAny(owner, "/\\:*?\"<>|\x00") || strings.Contains(owner, "..") {
		return Ack{}, fmt.Errorf("%w: invalid owner name %q", ErrInvalidAck, owner)
	}
	ack, err := h.readAckFile(owner)
	if err != nil {
		return Ack{}, err
	}
	if ack.Owner != owner {
		return Ack{}, fmt.Errorf("%w: ack owner %q does not match filename owner %q", ErrInvalidAck, ack.Owner, owner)
	}
	return ack, nil
}

func (h *Handle) readAckFile(owner string) (Ack, error) {
	targetFile := h.ackPath(owner)
	data, err := os.ReadFile(targetFile)
	if err != nil {
		return Ack{}, err
	}
	var ack Ack
	if err := decodeJSON(data, &ack); err != nil {
		return Ack{}, fmt.Errorf("malformed ack in %s: %w", targetFile, err)
	}
	if err := ValidateAck(ack); err != nil {
		return Ack{}, fmt.Errorf("invalid ack in %s: %w", targetFile, err)
	}
	return ack, nil
}

// WithCoordinator provides the one admission linearization.
// It takes coordinator.lock, loads and validates current fleet RUN authority
// (desired=RUN, scope=fleet, unexpired at now), and runs callback under the lock.
// The callback is never called on invalid or expired authority.
// If callback returns an error, the result is typed ambiguous: no rollback is
// performed and no new token is consumed; the caller reconciles possible STARTING.
func (h *Handle) WithCoordinator(ctx context.Context, now time.Time, callback func(State) error) error {
	unlock, err := takeCoordinatorLock(ctx, h.lockPath(), h.lockTimeout)
	if err != nil {
		return fmt.Errorf("taking coordinator lock: %w", err)
	}
	defer unlock()

	st, err := h.loadUnlocked(now)
	if err != nil {
		return fmt.Errorf("loading control state under coordinator lock: %w", err)
	}
	if err := st.IsValidAuthority(now); err != nil {
		return fmt.Errorf("control admission refused: %w", err)
	}
	if err := callback(st); err != nil {
		return &AmbiguousError{Op: "callback", Cause: err}
	}
	return nil
}

var (
	durableWrite    = func(f *os.File, p []byte) (int, error) { return f.Write(p) }
	durableSyncFile = func(f *os.File) error { return f.Sync() }
	durableClose    = func(f *os.File) error { return f.Close() }
	durableRename   = os.Rename
	durableSyncDir  = fsyncDir
)

func fsyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func makeNonce() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d-%d", time.Now().UnixNano(), os.Getpid())
	}
	return hex.EncodeToString(b[:])
}

func checkDurableMatch(targetPath string, data []byte) bool {
	existing, err := os.ReadFile(targetPath)
	if err != nil {
		return false
	}
	return bytes.Equal(existing, data)
}

// writeDurableJSON replaces targetPath atomically:
// 1. Writes to unique nonce tmp file with O_EXCL in the same directory.
// 2. fsyncs file and closes it.
// 3. Renames tmp file over targetPath.
// 4. fsyncs the parent directory.
// Pre-rename failures return the cause and leave old bytes. A rename or
// directory-sync error after the new bytes are visible is AmbiguousError.
func writeDurableJSON(targetPath string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling json: %w", err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(targetPath)
	if err := ensurePlainDir(dir); err != nil {
		return fmt.Errorf("creating directory %s: %w", dir, err)
	}

	nonce := makeNonce()
	baseName := filepath.Base(targetPath)
	tmpPath := filepath.Join(dir, fmt.Sprintf(".%s.tmp.%s", baseName, nonce))

	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("creating temp file %s: %w", tmpPath, err)
	}

	writeErr := func() error {
		if _, err := durableWrite(f, data); err != nil {
			return err
		}
		if err := durableSyncFile(f); err != nil {
			return err
		}
		return durableClose(f)
	}()
	if writeErr != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("writing temp file %s: %w", tmpPath, writeErr)
	}

	if err := durableRename(tmpPath, targetPath); err != nil {
		_ = os.Remove(tmpPath)
		if checkDurableMatch(targetPath, data) {
			return &AmbiguousError{Op: "rename", Path: targetPath, Cause: err}
		}
		return fmt.Errorf("renaming %s to %s: %w", tmpPath, targetPath, err)
	}

	if err := durableSyncDir(dir); err != nil {
		return &AmbiguousError{Op: "dirsync", Path: dir, Cause: err}
	}
	return nil
}
