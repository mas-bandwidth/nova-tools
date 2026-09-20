package fleet

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// Common errors for bench records and versioned updates.
var (
	ErrStaleRevision = errors.New("stale revision: update rejected")
	ErrInvalidUpdate = errors.New("invalid update: validation failed")
	ErrBenchMismatch = errors.New("bench mismatch: record name does not match update")
)

// AdmissionState describes the active admission state of a bench.
type AdmissionState string

const (
	AdmissionAdmitting AdmissionState = "admitting"
	AdmissionRefused   AdmissionState = "refused"
	AdmissionDrained   AdmissionState = "drained"
	AdmissionParked    AdmissionState = "parked"
	AdmissionInvalid   AdmissionState = "invalid"
)

// RequestedSettings holds configured policy for a bench.
// These are target policy settings set by operators or configuration.
type RequestedSettings struct {
	Share              int     `json:"share"`                // configured target slot capacity
	MaxLoadPerCore     float64 `json:"max_load_per_core"`    // max load per core brake limit (0 = unbounded)
	MinDiskFreeGB      float64 `json:"min_disk_free_gb"`     // min required free disk headroom in GB (0 = unchecked)
	MinMemFreeGB       float64 `json:"min_mem_free_gb"`      // min required free memory in GB (0 = unchecked)
	MinGBPerCard       float64 `json:"min_gb_per_card"`      // min memory per card in GB (0 = unchecked)
	ProbeBudgetSeconds int     `json:"probe_budget_seconds"` // probe execution timeout budget in seconds
	Drain              bool    `json:"drain"`                // if true, operator policy drains the bench
}

// Validate checks that RequestedSettings contains valid numeric ranges.
func (r RequestedSettings) Validate() error {
	if r.Share < 0 {
		return fmt.Errorf("%w: share cannot be negative (%d)", ErrInvalidUpdate, r.Share)
	}
	if math.IsNaN(r.MaxLoadPerCore) || math.IsInf(r.MaxLoadPerCore, 0) || r.MaxLoadPerCore < 0 {
		return fmt.Errorf("%w: invalid max_load_per_core (%v)", ErrInvalidUpdate, r.MaxLoadPerCore)
	}
	if math.IsNaN(r.MinDiskFreeGB) || math.IsInf(r.MinDiskFreeGB, 0) || r.MinDiskFreeGB < 0 {
		return fmt.Errorf("%w: invalid min_disk_free_gb (%v)", ErrInvalidUpdate, r.MinDiskFreeGB)
	}
	if math.IsNaN(r.MinMemFreeGB) || math.IsInf(r.MinMemFreeGB, 0) || r.MinMemFreeGB < 0 {
		return fmt.Errorf("%w: invalid min_mem_free_gb (%v)", ErrInvalidUpdate, r.MinMemFreeGB)
	}
	if math.IsNaN(r.MinGBPerCard) || math.IsInf(r.MinGBPerCard, 0) || r.MinGBPerCard < 0 {
		return fmt.Errorf("%w: invalid min_gb_per_card (%v)", ErrInvalidUpdate, r.MinGBPerCard)
	}
	if r.ProbeBudgetSeconds < 0 {
		return fmt.Errorf("%w: probe_budget_seconds cannot be negative (%d)", ErrInvalidUpdate, r.ProbeBudgetSeconds)
	}
	return nil
}

// ObservedWatermark holds measured usage, telemetry, and capacity.
// Per Stella's pre-card ruling (#2164), samplers and probes write here
// and must NEVER silently raise or mutate RequestedSettings.
type ObservedWatermark struct {
	PeakHeld            int       `json:"peak_held"`              // peak active concurrency observed
	CurrentHeld         int       `json:"current_held"`           // current active concurrency held
	ObservedLoad1       float64   `json:"observed_load1"`         // 1-minute load average
	ObservedLoadPerCore float64   `json:"observed_load_per_core"` // measured load1 / cores
	FreeDiskGB          float64   `json:"free_disk_gb"`           // measured free disk space in GB
	FreeMemGB           float64   `json:"free_mem_gb"`            // measured free memory in GB
	Cores               int       `json:"cores"`                  // measured core count
	ProbeSuccess        bool      `json:"probe_success"`          // whether last probe succeeded
	LastProbeDurationMs int64     `json:"last_probe_duration_ms"` // latency of last probe in ms
	ObservedAt          time.Time `json:"observed_at"`            // timestamp of measurement
	SampleCount         int64     `json:"sample_count"`           // monotonic observation sample count
}

// Validate checks that ObservedWatermark contains valid numeric ranges.
func (o ObservedWatermark) Validate() error {
	if o.PeakHeld < 0 {
		return fmt.Errorf("%w: peak_held cannot be negative (%d)", ErrInvalidUpdate, o.PeakHeld)
	}
	if o.CurrentHeld < 0 {
		return fmt.Errorf("%w: current_held cannot be negative (%d)", ErrInvalidUpdate, o.CurrentHeld)
	}
	if math.IsNaN(o.ObservedLoad1) || math.IsInf(o.ObservedLoad1, 0) || o.ObservedLoad1 < 0 {
		return fmt.Errorf("%w: invalid observed_load1 (%v)", ErrInvalidUpdate, o.ObservedLoad1)
	}
	if math.IsNaN(o.ObservedLoadPerCore) || math.IsInf(o.ObservedLoadPerCore, 0) || o.ObservedLoadPerCore < 0 {
		return fmt.Errorf("%w: invalid observed_load_per_core (%v)", ErrInvalidUpdate, o.ObservedLoadPerCore)
	}
	if math.IsNaN(o.FreeDiskGB) || math.IsInf(o.FreeDiskGB, 0) || o.FreeDiskGB < 0 {
		return fmt.Errorf("%w: invalid free_disk_gb (%v)", ErrInvalidUpdate, o.FreeDiskGB)
	}
	if math.IsNaN(o.FreeMemGB) || math.IsInf(o.FreeMemGB, 0) || o.FreeMemGB < 0 {
		return fmt.Errorf("%w: invalid free_mem_gb (%v)", ErrInvalidUpdate, o.FreeMemGB)
	}
	if o.Cores < 0 {
		return fmt.Errorf("%w: cores cannot be negative (%d)", ErrInvalidUpdate, o.Cores)
	}
	if o.LastProbeDurationMs < 0 {
		return fmt.Errorf("%w: last_probe_duration_ms cannot be negative (%d)", ErrInvalidUpdate, o.LastProbeDurationMs)
	}
	if o.SampleCount < 0 {
		return fmt.Errorf("%w: sample_count cannot be negative (%d)", ErrInvalidUpdate, o.SampleCount)
	}
	return nil
}

// EnforcedAllowance holds the active admission boundary derived from policy and observations.
type EnforcedAllowance struct {
	Allowed        int            `json:"allowed"`         // number of new admissions permitted right now
	EffectiveLimit int            `json:"effective_limit"` // ceiling on total concurrent cards
	AdmissionState AdmissionState `json:"admission_state"` // admitting, refused, drained, parked, invalid
	RefusalReason  string         `json:"refusal_reason"`  // non-empty when AdmissionState != admitting
	Braked         bool           `json:"braked"`          // true if load brake is tripped
	UpdatedAt      time.Time      `json:"updated_at"`      // timestamp allowance was recalculated
}

// BenchRecordSnapshot is an immutable value copy of a BenchRecord at a point in time.
type BenchRecordSnapshot struct {
	Bench     string            `json:"bench"`
	Revision  int64             `json:"revision"`
	Requested RequestedSettings `json:"requested"`
	Observed  ObservedWatermark `json:"observed"`
	Enforced  EnforcedAllowance `json:"enforced"`
	UpdatedAt time.Time         `json:"updated_at"`
	Invalid   bool              `json:"invalid,omitempty"`
}

// CanAdmit checks whether needed slots can be admitted.
// If the record is in an invalid state, it REFUSES new admission with the failure reason,
// rather than falling back to zero or unlimited.
func (s BenchRecordSnapshot) CanAdmit(needed int) (bool, string) {
	if s.Invalid || s.Enforced.AdmissionState == AdmissionInvalid {
		reason := s.Enforced.RefusalReason
		if reason == "" {
			reason = "bench record is invalid"
		}
		return false, fmt.Sprintf("refused: %s (refusing new admission; never falling back to zero or unlimited)", reason)
	}
	if needed <= 0 {
		return false, "refused: non-positive admission count requested"
	}
	if s.Enforced.AdmissionState == AdmissionDrained {
		return false, "refused: bench is drained by policy"
	}
	if s.Enforced.AdmissionState == AdmissionParked {
		return false, "refused: bench is parked"
	}
	if s.Enforced.Braked {
		return false, fmt.Sprintf("refused: load brake tripped: %s", s.Enforced.RefusalReason)
	}
	if s.Enforced.AdmissionState == AdmissionRefused {
		return false, fmt.Sprintf("refused: %s", s.Enforced.RefusalReason)
	}
	if s.Enforced.Allowed < needed {
		return false, fmt.Sprintf("refused: requested %d slots exceeds allowed capacity %d (effective limit %d, held %d)",
			needed, s.Enforced.Allowed, s.Enforced.EffectiveLimit, s.Observed.CurrentHeld)
	}
	return true, ""
}

// BenchRecord is the atomic versioned record representing bench capacity and admission state.
type BenchRecord struct {
	mu         sync.RWMutex
	bench      string
	revision   int64
	requested  RequestedSettings
	observed   ObservedWatermark
	enforced   EnforcedAllowance
	updatedAt  time.Time
	invalid    bool
	invalidErr error
}

// NewBenchRecord initializes a new atomic versioned bench record at revision 1.
func NewBenchRecord(bench string, settings RequestedSettings) (*BenchRecord, error) {
	bench = strings.TrimSpace(bench)
	if bench == "" {
		return nil, fmt.Errorf("%w: bench name cannot be empty", ErrInvalidUpdate)
	}
	if err := settings.Validate(); err != nil {
		return nil, err
	}

	r := &BenchRecord{
		bench:     bench,
		revision:  1,
		requested: settings,
		updatedAt: time.Now(),
	}
	r.recalculateAllowanceLocked()
	return r, nil
}

// Snapshot returns an immutable point-in-time copy of the record.
func (r *BenchRecord) Snapshot() BenchRecordSnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return BenchRecordSnapshot{
		Bench:     r.bench,
		Revision:  r.revision,
		Requested: r.requested,
		Observed:  r.observed,
		Enforced:  r.enforced,
		UpdatedAt: r.updatedAt,
		Invalid:   r.invalid,
	}
}

// CanAdmit returns whether needed slots may be admitted under current allowance.
// If the record is invalid, it strictly refuses admission without fallback.
func (r *BenchRecord) CanAdmit(needed int) (bool, string) {
	return r.Snapshot().CanAdmit(needed)
}

// AdmitLease atomically checks admission and reserves needed slots, advancing revision.
func (r *BenchRecord) AdmitLease(needed int) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	snap := BenchRecordSnapshot{
		Bench:     r.bench,
		Revision:  r.revision,
		Requested: r.requested,
		Observed:  r.observed,
		Enforced:  r.enforced,
		UpdatedAt: r.updatedAt,
		Invalid:   r.invalid,
	}
	ok, reason := snap.CanAdmit(needed)
	if !ok {
		return errors.New(reason)
	}

	r.observed.CurrentHeld += needed
	if r.observed.CurrentHeld > r.observed.PeakHeld {
		r.observed.PeakHeld = r.observed.CurrentHeld
	}
	r.revision++
	r.updatedAt = time.Now()
	r.recalculateAllowanceLocked()
	return nil
}

// ReleaseLease atomically releases held slots, advancing revision.
func (r *BenchRecord) ReleaseLease(count int) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if count <= 0 {
		return fmt.Errorf("%w: release count must be positive (%d)", ErrInvalidUpdate, count)
	}
	if r.observed.CurrentHeld < count {
		r.observed.CurrentHeld = 0
	} else {
		r.observed.CurrentHeld -= count
	}
	r.revision++
	r.updatedAt = time.Now()
	r.recalculateAllowanceLocked()
	return nil
}

// UpdateRequested atomically updates configured policy under CAS check.
// If expectedRev > 0 and does not match current revision, returns ErrStaleRevision.
func (r *BenchRecord) UpdateRequested(expectedRev int64, settings RequestedSettings) error {
	if err := settings.Validate(); err != nil {
		r.MarkInvalid(err)
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if expectedRev > 0 && r.revision != expectedRev {
		return fmt.Errorf("%w: current revision is %d, expected %d", ErrStaleRevision, r.revision, expectedRev)
	}

	r.requested = settings
	r.revision++
	r.updatedAt = time.Now()
	r.recalculateAllowanceLocked()
	return nil
}

// UpdateObserved atomically updates telemetry/watermark under CAS check.
// Per Stella's rule, this NEVER alters RequestedSettings.
func (r *BenchRecord) UpdateObserved(expectedRev int64, obs ObservedWatermark) error {
	if err := obs.Validate(); err != nil {
		r.MarkInvalid(err)
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if expectedRev > 0 && r.revision != expectedRev {
		return fmt.Errorf("%w: current revision is %d, expected %d", ErrStaleRevision, r.revision, expectedRev)
	}

	if obs.PeakHeld < r.observed.PeakHeld {
		obs.PeakHeld = r.observed.PeakHeld
	}
	if obs.CurrentHeld > obs.PeakHeld {
		obs.PeakHeld = obs.CurrentHeld
	}

	r.observed = obs
	r.revision++
	r.updatedAt = time.Now()
	r.recalculateAllowanceLocked()
	return nil
}

// MarkInvalid sets the record into an invalid state that explicitly refuses new admission.
func (r *BenchRecord) MarkInvalid(reason error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.invalid = true
	r.invalidErr = reason
	r.enforced = EnforcedAllowance{
		Allowed:        0,
		EffectiveLimit: 0,
		AdmissionState: AdmissionInvalid,
		RefusalReason:  fmt.Sprintf("invalid update: %v", reason),
		UpdatedAt:      time.Now(),
	}
}

// recalculateAllowanceLocked recalculates the active admission boundary. Must be called under r.mu.
func (r *BenchRecord) recalculateAllowanceLocked() {
	now := time.Now()
	if r.invalid {
		r.enforced = EnforcedAllowance{
			Allowed:        0,
			EffectiveLimit: 0,
			AdmissionState: AdmissionInvalid,
			RefusalReason:  fmt.Sprintf("invalid bench record: %v", r.invalidErr),
			UpdatedAt:      now,
		}
		return
	}

	if r.requested.Drain {
		r.enforced = EnforcedAllowance{
			Allowed:        0,
			EffectiveLimit: 0,
			AdmissionState: AdmissionDrained,
			RefusalReason:  "bench is drained by policy",
			UpdatedAt:      now,
		}
		return
	}

	effectiveLimit := r.requested.Share
	if effectiveLimit <= 0 {
		r.enforced = EnforcedAllowance{
			Allowed:        0,
			EffectiveLimit: 0,
			AdmissionState: AdmissionRefused,
			RefusalReason:  "requested share is zero",
			UpdatedAt:      now,
		}
		return
	}

	// 1. Load brake check
	if r.requested.MaxLoadPerCore > 0 && r.observed.Cores > 0 {
		loadPerCore := r.observed.ObservedLoadPerCore
		if loadPerCore == 0 && r.observed.ObservedLoad1 > 0 {
			loadPerCore = r.observed.ObservedLoad1 / float64(r.observed.Cores)
		}
		if loadPerCore > r.requested.MaxLoadPerCore {
			r.enforced = EnforcedAllowance{
				Allowed:        0,
				EffectiveLimit: effectiveLimit,
				AdmissionState: AdmissionRefused,
				Braked:         true,
				RefusalReason:  fmt.Sprintf("load per core %.2f exceeds brake threshold %.2f", loadPerCore, r.requested.MaxLoadPerCore),
				UpdatedAt:      now,
			}
			return
		}
	}

	// 2. Disk headroom check
	if r.requested.MinDiskFreeGB > 0 && r.observed.FreeDiskGB > 0 {
		if r.observed.FreeDiskGB < r.requested.MinDiskFreeGB {
			r.enforced = EnforcedAllowance{
				Allowed:        0,
				EffectiveLimit: effectiveLimit,
				AdmissionState: AdmissionRefused,
				RefusalReason:  fmt.Sprintf("free disk %.1f GB below required headroom %.1f GB", r.observed.FreeDiskGB, r.requested.MinDiskFreeGB),
				UpdatedAt:      now,
			}
			return
		}
	}

	// 3. Memory headroom check
	if r.requested.MinMemFreeGB > 0 && r.observed.FreeMemGB > 0 {
		if r.observed.FreeMemGB < r.requested.MinMemFreeGB {
			r.enforced = EnforcedAllowance{
				Allowed:        0,
				EffectiveLimit: effectiveLimit,
				AdmissionState: AdmissionRefused,
				RefusalReason:  fmt.Sprintf("free memory %.1f GB below required headroom %.1f GB", r.observed.FreeMemGB, r.requested.MinMemFreeGB),
				UpdatedAt:      now,
			}
			return
		}
	}

	// 4. Calculate available slots
	available := effectiveLimit - r.observed.CurrentHeld
	if available < 0 {
		available = 0
	}

	// 5. Memory per card constraint
	if r.requested.MinGBPerCard > 0 && r.observed.FreeMemGB > r.requested.MinMemFreeGB {
		usableMem := r.observed.FreeMemGB - r.requested.MinMemFreeGB
		cardsByMem := int(usableMem / r.requested.MinGBPerCard)
		if cardsByMem < available {
			available = cardsByMem
		}
	}

	if available <= 0 {
		r.enforced = EnforcedAllowance{
			Allowed:        0,
			EffectiveLimit: effectiveLimit,
			AdmissionState: AdmissionRefused,
			RefusalReason:  "no free capacity available",
			UpdatedAt:      now,
		}
		return
	}

	r.enforced = EnforcedAllowance{
		Allowed:        available,
		EffectiveLimit: effectiveLimit,
		AdmissionState: AdmissionAdmitting,
		UpdatedAt:      now,
	}
}

// MarshalJSON serializes the BenchRecord snapshot.
func (r *BenchRecord) MarshalJSON() ([]byte, error) {
	return json.Marshal(r.Snapshot())
}

// SaveAtomic saves the bench record to disk atomically using a temporary file and rename.
func (r *BenchRecord) SaveAtomic(path string) error {
	data, err := r.MarshalJSON()
	if err != nil {
		return fmt.Errorf("marshal bench record: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create parent directory: %w", err)
	}

	tmpFile, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		_ = os.Remove(tmpPath)
	}()

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename to target path: %w", err)
	}
	return nil
}

// ReadBenchRecord reads and validates an atomic versioned bench record from disk.
func ReadBenchRecord(path string) (*BenchRecord, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read bench record %s: %w", oneline.Field(path), err)
	}
	return ParseBenchRecord(raw)
}

// ParseBenchRecord parses and validates a bench record from bytes.
func ParseBenchRecord(data []byte) (*BenchRecord, error) {
	var snap BenchRecordSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("%w: corrupt json payload: %v", ErrInvalidUpdate, err)
	}

	if strings.TrimSpace(snap.Bench) == "" {
		return nil, fmt.Errorf("%w: missing bench name", ErrInvalidUpdate)
	}
	if snap.Revision <= 0 {
		return nil, fmt.Errorf("%w: invalid revision %d (must be >= 1)", ErrInvalidUpdate, snap.Revision)
	}
	if err := snap.Requested.Validate(); err != nil {
		return nil, err
	}
	if err := snap.Observed.Validate(); err != nil {
		return nil, err
	}

	rec := &BenchRecord{
		bench:     snap.Bench,
		revision:  snap.Revision,
		requested: snap.Requested,
		observed:  snap.Observed,
		updatedAt: snap.UpdatedAt,
		invalid:   snap.Invalid,
	}
	rec.recalculateAllowanceLocked()
	return rec, nil
}
