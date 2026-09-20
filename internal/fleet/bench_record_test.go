package fleet

import (
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func validSettings() RequestedSettings {
	return RequestedSettings{
		Share:              32,
		MaxLoadPerCore:     2.0,
		MinDiskFreeGB:      25.0,
		MinMemFreeGB:       4.0,
		MinGBPerCard:       0.5,
		ProbeBudgetSeconds: 30,
		Drain:              false,
	}
}

func TestNewBenchRecordValid(t *testing.T) {
	settings := validSettings()
	rec, err := NewBenchRecord("space", settings)
	if err != nil {
		t.Fatalf("NewBenchRecord failed: %v", err)
	}

	snap := rec.Snapshot()
	if snap.Bench != "space" {
		t.Errorf("got bench %q, want %q", snap.Bench, "space")
	}
	if snap.Revision != 1 {
		t.Errorf("got revision %d, want 1", snap.Revision)
	}
	if snap.Requested.Share != 32 {
		t.Errorf("got requested share %d, want 32", snap.Requested.Share)
	}
	if snap.Observed.PeakHeld != 0 {
		t.Errorf("got observed peak %d, want 0", snap.Observed.PeakHeld)
	}
	if snap.Enforced.AdmissionState != AdmissionAdmitting {
		t.Errorf("got admission state %q, want %q", snap.Enforced.AdmissionState, AdmissionAdmitting)
	}
	if snap.Enforced.Allowed != 32 {
		t.Errorf("got allowed %d, want 32", snap.Enforced.Allowed)
	}

	ok, reason := rec.CanAdmit(1)
	if !ok {
		t.Errorf("CanAdmit(1) failed unexpectedly: %s", reason)
	}
}

func TestAtomicMonotonicRevision(t *testing.T) {
	settings := validSettings()
	rec, err := NewBenchRecord("space", settings)
	if err != nil {
		t.Fatalf("NewBenchRecord: %v", err)
	}

	snap1 := rec.Snapshot()
	if snap1.Revision != 1 {
		t.Fatalf("initial revision: got %d, want 1", snap1.Revision)
	}

	// Successful update with matching expected revision
	settings.Share = 40
	err = rec.UpdateRequested(1, settings)
	if err != nil {
		t.Fatalf("UpdateRequested: %v", err)
	}

	snap2 := rec.Snapshot()
	if snap2.Revision != 2 {
		t.Fatalf("after update revision: got %d, want 2", snap2.Revision)
	}
	if snap2.Requested.Share != 40 {
		t.Errorf("got share %d, want 40", snap2.Requested.Share)
	}

	// Attempt stale revision update (expected 1, but current is 2)
	settings.Share = 48
	err = rec.UpdateRequested(1, settings)
	if err == nil {
		t.Fatal("expected error on stale revision update, got nil")
	}

	// Revision remains 2
	if rec.Snapshot().Revision != 2 {
		t.Errorf("revision changed on rejected update: got %d, want 2", rec.Snapshot().Revision)
	}

	// Concurrent atomic updates
	var wg sync.WaitGroup
	successCount := int32(0)
	var mu sync.Mutex
	lastRev := int64(2)

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			mu.Lock()
			curr := rec.Snapshot().Revision
			mu.Unlock()

			obs := ObservedWatermark{
				CurrentHeld: idx,
				ObservedAt:  time.Now(),
			}
			if err := rec.UpdateObserved(curr, obs); err == nil {
				mu.Lock()
				successCount++
				if rec.Snapshot().Revision > lastRev {
					lastRev = rec.Snapshot().Revision
				}
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()

	if rec.Snapshot().Revision <= 2 {
		t.Errorf("expected revision to advance monotonically, got %d", rec.Snapshot().Revision)
	}
}

func TestSeparateFieldsSamplerNeverMutatesPolicy(t *testing.T) {
	settings := validSettings()
	settings.Share = 32
	settings.MaxLoadPerCore = 2.0

	rec, err := NewBenchRecord("space", settings)
	if err != nil {
		t.Fatalf("NewBenchRecord: %v", err)
	}

	// Sampler writes observed watermark
	obs := ObservedWatermark{
		PeakHeld:            60, // higher than requested share
		CurrentHeld:         10,
		ObservedLoad1:       32.0,
		ObservedLoadPerCore: 4.0, // exceeds MaxLoadPerCore (2.0)
		FreeDiskGB:          50.0,
		FreeMemGB:           16.0,
		Cores:               8,
		ProbeSuccess:        true,
		ObservedAt:          time.Now(),
		SampleCount:         1,
	}

	err = rec.UpdateObserved(1, obs)
	if err != nil {
		t.Fatalf("UpdateObserved: %v", err)
	}

	snap := rec.Snapshot()
	// 1. Requested settings MUST remain intact - sampler does not silently raise policy
	if snap.Requested.Share != 32 {
		t.Errorf("sampler mutated requested share: got %d, want 32", snap.Requested.Share)
	}
	if snap.Requested.MaxLoadPerCore != 2.0 {
		t.Errorf("sampler mutated MaxLoadPerCore: got %v, want 2.0", snap.Requested.MaxLoadPerCore)
	}

	// 2. Observed watermark is recorded accurately
	if snap.Observed.PeakHeld != 60 {
		t.Errorf("got peak held %d, want 60", snap.Observed.PeakHeld)
	}
	if snap.Observed.ObservedLoadPerCore != 4.0 {
		t.Errorf("got observed load per core %v, want 4.0", snap.Observed.ObservedLoadPerCore)
	}

	// 3. Enforced allowance reflects the load brake
	if !snap.Enforced.Braked {
		t.Error("expected bench to be braked since load per core (4.0) > max (2.0)")
	}
	if snap.Enforced.AdmissionState != AdmissionRefused {
		t.Errorf("expected admission state %q, got %q", AdmissionRefused, snap.Enforced.AdmissionState)
	}

	ok, reason := rec.CanAdmit(1)
	if ok {
		t.Error("CanAdmit should refuse when braked by load")
	}
	if reason == "" {
		t.Error("CanAdmit refusal reason must not be empty")
	}
}

func TestInvalidUpdateRefusesNewAdmissionNotZeroOrUnlimited(t *testing.T) {
	settings := validSettings()
	rec, err := NewBenchRecord("space", settings)
	if err != nil {
		t.Fatalf("NewBenchRecord: %v", err)
	}

	// Case A: Negative share update
	badSettings := settings
	badSettings.Share = -5
	err = rec.UpdateRequested(1, badSettings)
	if err == nil {
		t.Fatal("expected error on negative share update, got nil")
	}

	// Case B: NaN in float field
	badObs := ObservedWatermark{
		ObservedLoad1: math.NaN(),
	}
	err = rec.UpdateObserved(1, badObs)
	if err == nil {
		t.Fatal("expected error on NaN load update, got nil")
	}

	// Case C: Explicit invalid marking
	rec.MarkInvalid(err)

	snap := rec.Snapshot()
	if snap.Enforced.AdmissionState != AdmissionInvalid {
		t.Errorf("got state %q, want %q", snap.Enforced.AdmissionState, AdmissionInvalid)
	}

	// Refusal must NOT fall back to 0 or unlimited
	ok, reason := rec.CanAdmit(1)
	if ok {
		t.Fatal("CanAdmit(1) succeeded on invalid record! Must refuse admission")
	}
	if reason == "" {
		t.Fatal("refusal reason must be provided")
	}

	// Even asking for 0 slots must refuse rather than silently succeeding as a zero fallback
	ok0, reason0 := rec.CanAdmit(0)
	if ok0 {
		t.Fatal("CanAdmit(0) succeeded on invalid record! Must refuse admission")
	}
	if reason0 == "" {
		t.Fatal("refusal reason for CanAdmit(0) must not be empty on invalid record")
	}
}

func TestAllowanceCalculations(t *testing.T) {
	settings := validSettings()
	settings.Share = 10
	settings.MinDiskFreeGB = 20.0
	settings.MinMemFreeGB = 4.0
	settings.MinGBPerCard = 2.0 // 2 GB per card

	rec, err := NewBenchRecord("space", settings)
	if err != nil {
		t.Fatalf("NewBenchRecord: %v", err)
	}

	// Disk headroom exhaustion
	obs := ObservedWatermark{
		Cores:               8,
		FreeDiskGB:          10.0, // below MinDiskFreeGB (20.0)
		FreeMemGB:           32.0,
		ObservedLoadPerCore: 1.0,
		CurrentHeld:         0,
		ObservedAt:          time.Now(),
	}
	if err := rec.UpdateObserved(1, obs); err != nil {
		t.Fatalf("UpdateObserved: %v", err)
	}

	snap := rec.Snapshot()
	if snap.Enforced.AdmissionState != AdmissionRefused {
		t.Errorf("expected AdmissionRefused due to disk headroom, got %q", snap.Enforced.AdmissionState)
	}
	if ok, _ := rec.CanAdmit(1); ok {
		t.Error("CanAdmit should refuse when disk headroom exhausted")
	}

	// Adequate disk, but memory constrains allowed count
	obs.FreeDiskGB = 100.0
	obs.FreeMemGB = 8.0 // MinMemFreeGB=4, so (8 - 4) = 4 GB usable / 2 GB per card = 2 cards allowed max
	if err := rec.UpdateObserved(2, obs); err != nil {
		t.Fatalf("UpdateObserved: %v", err)
	}

	snap = rec.Snapshot()
	if snap.Enforced.AdmissionState != AdmissionAdmitting {
		t.Errorf("expected AdmissionAdmitting, got %q (reason: %s)", snap.Enforced.AdmissionState, snap.Enforced.RefusalReason)
	}
	if snap.Enforced.Allowed != 2 {
		t.Errorf("got allowed %d, want 2 (constrained by memory)", snap.Enforced.Allowed)
	}

	// Drain setting refuses all admission
	settings.Drain = true
	if err := rec.UpdateRequested(3, settings); err != nil {
		t.Fatalf("UpdateRequested: %v", err)
	}
	snap = rec.Snapshot()
	if snap.Enforced.AdmissionState != AdmissionDrained {
		t.Errorf("got state %q, want %q", snap.Enforced.AdmissionState, AdmissionDrained)
	}
	if ok, _ := rec.CanAdmit(1); ok {
		t.Error("CanAdmit should refuse when drained")
	}
}

func TestAtomicFilePersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bench-space.json")

	settings := validSettings()
	rec, err := NewBenchRecord("space", settings)
	if err != nil {
		t.Fatalf("NewBenchRecord: %v", err)
	}

	if err := rec.SaveAtomic(path); err != nil {
		t.Fatalf("SaveAtomic: %v", err)
	}

	loaded, err := ReadBenchRecord(path)
	if err != nil {
		t.Fatalf("ReadBenchRecord: %v", err)
	}

	snapOriginal := rec.Snapshot()
	snapLoaded := loaded.Snapshot()

	if snapLoaded.Bench != snapOriginal.Bench {
		t.Errorf("bench mismatch: got %q, want %q", snapLoaded.Bench, snapOriginal.Bench)
	}
	if snapLoaded.Revision != snapOriginal.Revision {
		t.Errorf("revision mismatch: got %d, want %d", snapLoaded.Revision, snapOriginal.Revision)
	}
	if snapLoaded.Requested.Share != snapOriginal.Requested.Share {
		t.Errorf("share mismatch: got %d, want %d", snapLoaded.Requested.Share, snapOriginal.Requested.Share)
	}

	// Corrupt file must fail to load and return an error
	badPath := filepath.Join(dir, "corrupt.json")
	if err := os.WriteFile(badPath, []byte("{invalid json"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = ReadBenchRecord(badPath)
	if err == nil {
		t.Fatal("expected error loading corrupted bench record, got nil")
	}
}
