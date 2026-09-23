package wake

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Default constants for slot leasing in wake.
// 15s TTL and 5s heartbeat loop ensure responsive liveness and fast recovery
// while remaining robust under bench load.
const (
	DefaultSlotLeaseTTL       = 15 * time.Second
	DefaultSlotLeaseHeartbeat = 5 * time.Second
	SlotLeaseFileName         = ".lease"
)

// SlotLease represents an on-disk lease record for a wake daemon or runner.
type SlotLease struct {
	Owner     string    `json:"owner"`
	PID       int       `json:"pid"`
	Label     string    `json:"label"`
	Until     time.Time `json:"until"`
	Started   time.Time `json:"started"`
	Heartbeat time.Time `json:"heartbeat"`
	Nonce     string    `json:"nonce"`
	Host      string    `json:"host"`
	Path      string    `json:"path"`
}

// State returns "live", "DRIFT", or "expired" based on the lease's Until timestamp
// and whether the holding PID is still alive.
//
// Liveness semantics:
// - If Until is in the future AND PID is alive: "live"
// - If Until has expired AND PID is STILL ALIVE: "DRIFT" (DRIFT preservation: must not be reclaimed!)
// - If PID is dead (regardless of Until, or past Until): "expired" (dead PID reclaimable)
func (l SlotLease) State(now time.Time) string {
	return l.StateWithAlive(now, defaultPIDAlive)
}

// StateWithAlive evaluates lease state with a custom PID liveness function.
func (l SlotLease) StateWithAlive(now time.Time, isAlive func(int) bool) string {
	if isAlive == nil {
		isAlive = defaultPIDAlive
	}
	alive := isAlive(l.PID)
	if l.Until.After(now) {
		if alive {
			return "live"
		}
		// PID is dead even though Until has not arrived: dead process.
		return "expired"
	}
	// Past Until:
	if alive {
		// DRIFT: Expired Until, but PID is still alive. Preserved and must not be stolen!
		return "DRIFT"
	}
	return "expired"
}

// SlotLeaseHeldError is returned when attempting to acquire a lease that is currently
// held by a live process or preserved under DRIFT.
type SlotLeaseHeldError struct {
	Holder SlotLease
	State  string // "live" or "DRIFT"
	Reason string
}

func (e *SlotLeaseHeldError) Error() string {
	return fmt.Sprintf("slot lease held (state=%s, pid=%d, owner=%s, until=%s): %s",
		e.State, e.Holder.PID, e.Holder.Owner, e.Holder.Until.UTC().Format(time.RFC3339), e.Reason)
}

// HeldSlotLease extracts the holder if err is a *SlotLeaseHeldError.
func HeldSlotLease(err error) (SlotLease, bool) {
	var held *SlotLeaseHeldError
	if errors.As(err, &held) {
		return held.Holder, true
	}
	return SlotLease{}, false
}

// defaultPIDAlive checks if a process is alive using signal 0.
func defaultPIDAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = process.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	if errors.Is(err, syscall.EPERM) {
		return true
	}
	return false
}

// newLeaseNonce generates a unique hex token.
func newLeaseNonce() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b[:])
}

// FormatSlotLease formats the lease into key=value lines.
func FormatSlotLease(l *SlotLease) string {
	return fmt.Sprintf("owner=%s\npid=%d\nlabel=%s\nuntil=%s\nstarted=%s\nheartbeat=%s\nnonce=%s\nhost=%s\n",
		l.Owner,
		l.PID,
		l.Label,
		l.Until.UTC().Format(time.RFC3339Nano),
		l.Started.UTC().Format(time.RFC3339Nano),
		l.Heartbeat.UTC().Format(time.RFC3339Nano),
		l.Nonce,
		l.Host,
	)
}

// ParseSlotLease parses key=value lines from a raw byte slice into a SlotLease.
func ParseSlotLease(raw []byte, path string) (*SlotLease, error) {
	lease := &SlotLease{Path: path}
	lines := strings.Split(string(raw), "\n")
	seen := make(map[string]bool)

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)

		switch key {
		case "owner", "as":
			lease.Owner = val
			seen["owner"] = true
		case "pid":
			if n, err := strconv.Atoi(val); err == nil {
				lease.PID = n
				seen["pid"] = true
			}
		case "label":
			lease.Label = val
			seen["label"] = true
		case "until":
			if t, err := parseTime(val); err == nil {
				lease.Until = t
				seen["until"] = true
			}
		case "started":
			if t, err := parseTime(val); err == nil {
				lease.Started = t
				seen["started"] = true
			}
		case "heartbeat":
			if t, err := parseTime(val); err == nil {
				lease.Heartbeat = t
				seen["heartbeat"] = true
			}
		case "nonce":
			lease.Nonce = val
			seen["nonce"] = true
		case "host":
			lease.Host = val
			seen["host"] = true
		}
	}

	if !seen["pid"] {
		return nil, errors.New("lease record missing or invalid pid")
	}
	return lease, nil
}

func parseTime(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339, s)
}

// ReadSlotLease reads and parses the lease file from path or path/.lease if path is a directory.
func ReadSlotLease(target string) (*SlotLease, error) {
	path := target
	st, err := os.Stat(path)
	if err == nil && st.IsDir() {
		path = filepath.Join(path, SlotLeaseFileName)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseSlotLease(raw, path)
}

// WriteSlotLeaseAtomic atomically writes the lease file using a temporary file
// in the same directory and renaming it into place.
func WriteSlotLeaseAtomic(leasePath string, lease *SlotLease) error {
	dir := filepath.Dir(leasePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create lease directory %s: %w", dir, err)
	}

	nonce := lease.Nonce
	if nonce == "" {
		nonce = newLeaseNonce()
		lease.Nonce = nonce
	}

	tmpPath := filepath.Join(dir, fmt.Sprintf(".lease.%s.tmp", nonce))
	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("failed to create temp lease file %s: %w", tmpPath, err)
	}

	body := FormatSlotLease(lease)
	if _, err := f.WriteString(body); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to write lease content: %w", err)
	}

	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to sync lease file: %w", err)
	}

	if err := f.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to close temp lease file: %w", err)
	}

	if err := os.Rename(tmpPath, leasePath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to atomically rename %s to %s: %w", tmpPath, leasePath, err)
	}

	lease.Path = leasePath
	return nil
}

// SlotLeaseConfig contains configuration for acquiring and maintaining a slot lease.
type SlotLeaseConfig struct {
	Dir               string
	LeasePath         string
	Owner             string
	Label             string
	TTL               time.Duration
	HeartbeatInterval time.Duration
	PID               int
	PIDAlive          func(pid int) bool
	Clock             Clock
}

// SlotLeaseManager coordinates slot lease acquisition, background heartbeats,
// and fenced release.
type SlotLeaseManager struct {
	mu       sync.Mutex
	cfg      SlotLeaseConfig
	lease    *SlotLease
	clock    Clock
	stopCh   chan struct{}
	doneCh   chan struct{}
	released bool
}

// AcquireSlotLease attempts to acquire a slot lease under cfg.
//
// Rules enforced:
// 1. If an existing lease is "live" (held by another alive process): refused.
// 2. If an existing lease is in "DRIFT" (expired Until, but PID is STILL ALIVE):
//    DRIFT IS PRESERVED: refused, never stolen or reclaimed!
// 3. If an existing lease is "expired" (PID is dead):
//    DEAD PID IS RECLAIMED: the dead holder's lease is overwritten atomically.
// 4. Default TTL is 15s; default heartbeat is 5s.
// 5. Writes are atomic via temp file + rename.
func AcquireSlotLease(cfg SlotLeaseConfig) (*SlotLeaseManager, error) {
	if cfg.TTL <= 0 {
		cfg.TTL = DefaultSlotLeaseTTL
	}
	if cfg.HeartbeatInterval <= 0 {
		cfg.HeartbeatInterval = DefaultSlotLeaseHeartbeat
	}
	if cfg.PID <= 0 {
		cfg.PID = os.Getpid()
	}
	if cfg.PIDAlive == nil {
		cfg.PIDAlive = defaultPIDAlive
	}
	if cfg.Clock == nil {
		cfg.Clock = Real{}
	}
	if cfg.LeasePath == "" {
		if cfg.Dir == "" {
			return nil, errors.New("either Dir or LeasePath must be specified")
		}
		cfg.LeasePath = filepath.Join(cfg.Dir, SlotLeaseFileName)
	}

	now := cfg.Clock.Now()

	existing, err := ReadSlotLease(cfg.LeasePath)
	if err == nil {
		state := existing.StateWithAlive(now, cfg.PIDAlive)
		switch state {
		case "live":
			if existing.PID == cfg.PID && existing.Owner == cfg.Owner {
				// Re-acquiring our own lease; proceed to renew
			} else {
				return nil, &SlotLeaseHeldError{
					Holder: *existing,
					State:  "live",
					Reason: fmt.Sprintf("lease held by live pid %d", existing.PID),
				}
			}
		case "DRIFT":
			// DRIFT PRESERVATION: The lease's TTL expired, but the holding process is still alive!
			// We MUST NOT reclaim or overwrite it.
			return nil, &SlotLeaseHeldError{
				Holder: *existing,
				State:  "DRIFT",
				Reason: fmt.Sprintf("lease is in DRIFT: expired at %s but holder pid %d is still alive",
					existing.Until.UTC().Format(time.RFC3339), existing.PID),
			}
		case "expired":
			// DEAD PID RECLAIM: The holding process is dead.
			// We can safely reclaim the slot.
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		// Could not read or parse; if it's not simply non-existent, check if it's a directory error
		// or unreadable file.
	}

	host, _ := os.Hostname()
	lease := &SlotLease{
		Owner:     cfg.Owner,
		PID:       cfg.PID,
		Label:     cfg.Label,
		Until:     now.Add(cfg.TTL),
		Started:   now,
		Heartbeat: now,
		Nonce:     newLeaseNonce(),
		Host:      host,
		Path:      cfg.LeasePath,
	}

	if err := WriteSlotLeaseAtomic(cfg.LeasePath, lease); err != nil {
		return nil, err
	}

	mgr := &SlotLeaseManager{
		cfg:    cfg,
		lease:  lease,
		clock:  cfg.Clock,
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}

	go mgr.heartbeatLoop()
	return mgr, nil
}

// Lease returns a copy of the currently held lease record.
func (m *SlotLeaseManager) Lease() SlotLease {
	m.mu.Lock()
	defer m.mu.Unlock()
	return *m.lease
}

// Heartbeat performs an immediate synchronous lease renewal.
func (m *SlotLeaseManager) Heartbeat() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.released {
		return errors.New("lease already released")
	}
	now := m.clock.Now()
	m.lease.Heartbeat = now
	m.lease.Until = now.Add(m.cfg.TTL)
	return WriteSlotLeaseAtomic(m.cfg.LeasePath, m.lease)
}

func (m *SlotLeaseManager) heartbeatLoop() {
	defer close(m.doneCh)
	ticker := time.NewTicker(m.cfg.HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.mu.Lock()
			if m.released {
				m.mu.Unlock()
				return
			}
			now := m.clock.Now()
			m.lease.Heartbeat = now
			m.lease.Until = now.Add(m.cfg.TTL)

			// Fenced update: verify lease on disk is still ours
			existing, err := ReadSlotLease(m.cfg.LeasePath)
			if err == nil {
				if existing.PID != m.lease.PID || (existing.Nonce != "" && existing.Nonce != m.lease.Nonce) {
					// Someone else took or modified the lease; stop beating
					m.mu.Unlock()
					return
				}
			}

			_ = WriteSlotLeaseAtomic(m.cfg.LeasePath, m.lease)
			m.mu.Unlock()
		}
	}
}

// Release stops the heartbeat loop, waits for it to exit, and removes
// the lease file ONLY if the file on disk still matches this manager's PID and nonce.
func (m *SlotLeaseManager) Release() error {
	m.mu.Lock()
	if m.released {
		m.mu.Unlock()
		return nil
	}
	m.released = true
	close(m.stopCh)
	m.mu.Unlock()

	// Wait for heartbeat loop to exit
	<-m.doneCh

	m.mu.Lock()
	defer m.mu.Unlock()

	existing, err := ReadSlotLease(m.cfg.LeasePath)
	if err == nil {
		if existing.PID == m.lease.PID && (existing.Nonce == "" || existing.Nonce == m.lease.Nonce) {
			_ = os.Remove(m.cfg.LeasePath)
		}
	}
	return nil
}

// Close is an alias for Release.
func (m *SlotLeaseManager) Close() error {
	return m.Release()
}
