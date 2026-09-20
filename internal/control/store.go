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

// Handle represents an open control directory store.
type Handle struct {
	controlDir     string
	maxRUN         int
	maxRUNDuration time.Duration
	lockTimeout    time.Duration
}

// Store is an alias for Handle for callers who refer to the store type.
type Store = Handle

// Option configures an open Handle.
type Option func(*Handle)

// WithMaxRUNDuration explicitly sets the maximum RUN duration.
func WithMaxRUNDuration(d time.Duration) Option {
	return func(h *Handle) {
		if d > 0 {
			h.maxRUNDuration = d
		}
	}
}

// WithLockTimeout sets the maximum duration to wait when acquiring coordinator.lock.
func WithLockTimeout(d time.Duration) Option {
	return func(h *Handle) {
		if d > 0 {
			h.lockTimeout = d
		}
	}
}

// Open opens a control directory store at controlDir with a required maxRUN bound.
// Empty controlDir is refused; maxRUN must be a positive duration.
// The package reads and writes controlDir/state.json, controlDir/acks/<owner>.json,
// and controlDir/coordinator.lock. It does not join "control/" itself.
func Open(controlDir string, maxRUN int, opts ...Option) (*Handle, error) {
	cleanDir := filepath.Clean(strings.TrimSpace(controlDir))
	if cleanDir == "" || cleanDir == "." && strings.TrimSpace(controlDir) == "" {
		return nil, errors.New("controlDir cannot be empty")
	}
	if maxRUN <= 0 {
		return nil, errors.New("maxRUN must be a positive duration")
	}

	dur := time.Duration(maxRUN) * time.Second
	if maxRUN > 1_000_000 {
		dur = time.Duration(maxRUN)
	}

	h := &Handle{
		controlDir:     cleanDir,
		maxRUN:         maxRUN,
		maxRUNDuration: dur,
		lockTimeout:    10 * time.Second,
	}

	for _, opt := range opts {
		if opt != nil {
			opt(h)
		}
	}

	if h.maxRUNDuration <= 0 {
		return nil, errors.New("maxRUNDuration must be positive")
	}

	if err := os.MkdirAll(h.controlDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating control directory %s: %w", h.controlDir, err)
	}
	if err := os.MkdirAll(h.AcksDir(), 0o755); err != nil {
		return nil, fmt.Errorf("creating acks directory %s: %w", h.AcksDir(), err)
	}

	return h, nil
}

// Dir returns the underlying control directory path.
func (h *Handle) Dir() string {
	return h.controlDir
}

// MaxRUNDuration returns the maximum RUN duration.
func (h *Handle) MaxRUNDuration() time.Duration {
	return h.maxRUNDuration
}

// StatePath returns the path to state.json.
func (h *Handle) StatePath() string {
	return filepath.Join(h.controlDir, "state.json")
}

// LockPath returns the path to coordinator.lock.
func (h *Handle) LockPath() string {
	return filepath.Join(h.controlDir, "coordinator.lock")
}

// AcksDir returns the path to the acks/ subdirectory.
func (h *Handle) AcksDir() string {
	return filepath.Join(h.controlDir, "acks")
}

// AckPath returns the path to a specific owner's ack JSON file.
func (h *Handle) AckPath(owner string) string {
	return filepath.Join(h.AcksDir(), owner+".json")
}

// Load reads and parses control/state.json without acquiring coordinator.lock.
// It fails closed if the file does not exist, is unreadable, or is malformed.
// An absent state is not implicit RUN.
func (h *Handle) Load(now time.Time) (State, error) {
	return h.loadUnlocked(now)
}

func (h *Handle) loadUnlocked(now time.Time) (State, error) {
	data, err := os.ReadFile(h.StatePath())
	if err != nil {
		if os.IsNotExist(err) {
			return State{}, fmt.Errorf("%w: %s", ErrStateNotFound, h.StatePath())
		}
		return State{}, fmt.Errorf("reading control state: %w", err)
	}

	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return State{}, fmt.Errorf("malformed control state in %s: %w", h.StatePath(), err)
	}

	if err := ValidateState(st); err != nil {
		return State{}, fmt.Errorf("invalid control state in %s: %w", h.StatePath(), err)
	}

	return st, nil
}

// Update atomically and durably updates control/state.json under coordinator.lock
// after validating CAS against expectedGeneration.
// Initialization from absent state requires expectedGeneration == 0 and creates generation 1.
// Renewal requires generation n+1 and never extends in-place.
// PAUSE, DRAIN, and STOP updates work even after an earlier RUN has expired.
func (h *Handle) Update(now time.Time, expectedGeneration int64, nextState State) (State, error) {
	unlock, err := takeCoordinatorLock(context.Background(), h.LockPath(), h.lockTimeout)
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

	if err := ValidateUpdate(currentGen, expectedGeneration, nextState, now, h.maxRUNDuration); err != nil {
		return State{}, err
	}

	if err := writeDurableJSON(h.StatePath(), nextState); err != nil {
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

	targetFile := h.AckPath(ack.Owner)
	if err := writeDurableJSON(targetFile, ack); err != nil {
		return fmt.Errorf("durable write of ack for %s: %w", ack.Owner, err)
	}
	return nil
}

// LoadAck reads and validates a single bench acknowledgement from control/acks/<owner>.json.
func (h *Handle) LoadAck(owner string) (Ack, error) {
	owner = strings.TrimSpace(owner)
	if owner == "" || strings.ContainsAny(owner, "/\\:*?\"<>|\x00") || strings.Contains(owner, "..") {
		return Ack{}, fmt.Errorf("%w: invalid owner name %q", ErrInvalidAck, owner)
	}

	targetFile := h.AckPath(owner)
	data, err := os.ReadFile(targetFile)
	if err != nil {
		return Ack{}, err
	}

	var ack Ack
	if err := json.Unmarshal(data, &ack); err != nil {
		return Ack{}, fmt.Errorf("malformed ack in %s: %w", targetFile, err)
	}

	if err := ValidateAck(ack); err != nil {
		return Ack{}, fmt.Errorf("invalid ack in %s: %w", targetFile, err)
	}

	return ack, nil
}

// LoadAcks reads all valid acknowledgement records from control/acks/*.json.
func (h *Handle) LoadAcks() ([]Ack, error) {
	entries, err := os.ReadDir(h.AcksDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading acks directory: %w", err)
	}

	var acks []Ack
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		owner := strings.TrimSuffix(entry.Name(), ".json")
		ack, err := h.LoadAck(owner)
		if err != nil {
			return nil, fmt.Errorf("loading ack %s: %w", entry.Name(), err)
		}
		acks = append(acks, ack)
	}
	return acks, nil
}

// WithCoordinator provides the one admission linearization.
// It takes coordinator.lock (via flock), loads and validates current fleet RUN authority
// (desired=RUN, scope=fleet, unexpired at now), and runs callback under the lock.
// The callback is never called on invalid or expired authority.
// If callback returns an error, no rollback is performed and no new token is consumed;
// the coordinator reconciles possible STARTING.
func (h *Handle) WithCoordinator(ctx context.Context, now time.Time, callback func(State) error) error {
	unlock, err := takeCoordinatorLock(ctx, h.LockPath(), h.lockTimeout)
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
		return err
	}

	return nil
}

// WithCoordinator is a package-level convenience function delegating to h.WithCoordinator.
func WithCoordinator(ctx context.Context, h *Handle, now time.Time, callback func(State) error) error {
	if h == nil {
		return errors.New("nil control Handle")
	}
	return h.WithCoordinator(ctx, now, callback)
}

// Status aggregates acknowledgements against required owner identities.
func (h *Handle) Status(now time.Time, requiredOwners []string) (Status, error) {
	st, err := h.Load(now)
	if err != nil {
		return Status{}, err
	}

	acks, err := h.LoadAcks()
	if err != nil {
		return Status{}, err
	}

	ackMap := make(map[string]Ack, len(acks))
	for _, a := range acks {
		ackMap[a.Owner] = a
	}

	status := Status{
		Generation: st.Generation,
		Desired:    st.Desired,
		Owned:      len(requiredOwners),
	}

	for _, owner := range requiredOwners {
		ack, ok := ackMap[owner]
		if !ok {
			status.Pending++
			continue
		}
		if ack.Generation == st.Generation && ack.Desired == st.Desired {
			status.Acked++
		} else {
			status.Pending++
		}
	}

	status.Complete = status.Owned > 0 && status.Acked == status.Owned
	return status, nil
}

// fsyncDir flushes directory dentries to disk.
func fsyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// makeNonce generates a 16-byte random hex string for unguessable tmp file names.
func makeNonce() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d-%d", time.Now().UnixNano(), os.Getpid())
	}
	return hex.EncodeToString(b[:])
}

// checkDurableMatch verifies whether targetPath exists and has identical byte content to data.
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
// An ambiguous post-rename error is reconciled by verifying targetPath content.
func writeDurableJSON(targetPath string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling json: %w", err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(targetPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
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
		if _, err := f.Write(data); err != nil {
			return err
		}
		if err := f.Sync(); err != nil {
			return err
		}
		return f.Close()
	}()

	if writeErr != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("writing temp file %s: %w", tmpPath, writeErr)
	}

	if err := os.Rename(tmpPath, targetPath); err != nil {
		_ = os.Remove(tmpPath)
		if checkDurableMatch(targetPath, data) {
			return nil
		}
		return fmt.Errorf("renaming %s to %s: %w", tmpPath, targetPath, err)
	}

	if err := fsyncDir(dir); err != nil {
		if checkDurableMatch(targetPath, data) {
			return nil
		}
		return fmt.Errorf("fsync directory %s: %w", dir, err)
	}

	return nil
}
