package fleet

import (
	"errors"
	"math"
	"testing"
	"time"
)

// fakeProber simulates a bench capacity reader / telemetry prober.
type fakeProber struct {
	cores       int
	load1       float64
	freeDiskGB  float64
	freeMemGB   float64
	currentHeld int
	probeFail   bool
	probeDelay  time.Duration
	unreadable  bool
}

func (f *fakeProber) Sample(now time.Time, sampleIdx int64) (ObservedWatermark, error) {
	if f.probeFail {
		return ObservedWatermark{}, errors.New("probe connection refused: timeout")
	}
	if f.unreadable {
		return ObservedWatermark{
			ObservedLoad1:       math.NaN(),
			ObservedLoadPerCore: math.NaN(),
			ObservedAt:          now,
		}, nil
	}

	loadPerCore := 0.0
	if f.cores > 0 {
		loadPerCore = f.load1 / float64(f.cores)
	}

	return ObservedWatermark{
		Cores:               f.cores,
		ObservedLoad1:       f.load1,
		ObservedLoadPerCore: loadPerCore,
		FreeDiskGB:          f.freeDiskGB,
		FreeMemGB:           f.freeMemGB,
		CurrentHeld:         f.currentHeld,
		ProbeSuccess:        true,
		LastProbeDurationMs: f.probeDelay.Milliseconds(),
		ObservedAt:          now,
		SampleCount:         sampleIdx,
	}, nil
}

func TestFakeProberTelemetryLifecycle(t *testing.T) {
	settings := RequestedSettings{
		Share:          20,
		MaxLoadPerCore: 2.0,
		MinDiskFreeGB:  10.0,
		MinMemFreeGB:   2.0,
		MinGBPerCard:   1.0,
	}

	rec, err := NewBenchRecord("hulk", settings)
	if err != nil {
		t.Fatalf("NewBenchRecord: %v", err)
	}

	clock := time.Date(2026, 9, 20, 22, 0, 0, 0, time.UTC)

	// 1. Healthy initial probe
	prober := &fakeProber{
		cores:       16,
		load1:       8.0, // 8 / 16 = 0.5 per core (< 2.0)
		freeDiskGB:  100.0,
		freeMemGB:   32.0,
		currentHeld: 4,
	}

	obs, err := prober.Sample(clock, 1)
	if err != nil {
		t.Fatalf("prober sample: %v", err)
	}
	if err := rec.UpdateObserved(1, obs); err != nil {
		t.Fatalf("UpdateObserved: %v", err)
	}

	snap := rec.Snapshot()
	if snap.Enforced.AdmissionState != AdmissionAdmitting {
		t.Errorf("expected admitting, got %q", snap.Enforced.AdmissionState)
	}
	if snap.Enforced.Allowed != 16 { // 20 - 4 = 16 free
		t.Errorf("expected allowed 16, got %d", snap.Enforced.Allowed)
	}

	// 2. Load surge trips load brake
	clock = clock.Add(1 * time.Minute)
	prober.load1 = 40.0 // 40 / 16 = 2.5 per core (> 2.0 threshold)
	obs, err = prober.Sample(clock, 2)
	if err != nil {
		t.Fatalf("prober sample: %v", err)
	}
	if err := rec.UpdateObserved(2, obs); err != nil {
		t.Fatalf("UpdateObserved: %v", err)
	}

	snap = rec.Snapshot()
	if !snap.Enforced.Braked {
		t.Error("expected load brake to trip")
	}
	if snap.Enforced.AdmissionState != AdmissionRefused {
		t.Errorf("expected AdmissionRefused, got %q", snap.Enforced.AdmissionState)
	}

	ok, reason := rec.CanAdmit(1)
	if ok {
		t.Error("CanAdmit should refuse when load brake is active")
	}
	if reason == "" {
		t.Error("refusal reason should explain load brake")
	}

	// 3. Unreadable measurement (NaN) rejected by validation and refuses new admission
	clock = clock.Add(1 * time.Minute)
	prober.unreadable = true
	obs, err = prober.Sample(clock, 3)
	if err != nil {
		t.Fatalf("prober sample: %v", err)
	}
	err = rec.UpdateObserved(3, obs)
	if err == nil {
		t.Fatal("expected error updating observed with unreadable/NaN metrics, got nil")
	}

	// Record marked invalid refuses new admission and does NOT fallback to zero or unlimited
	rec.MarkInvalid(err)
	snap = rec.Snapshot()
	if snap.Enforced.AdmissionState != AdmissionInvalid {
		t.Errorf("expected AdmissionInvalid, got %q", snap.Enforced.AdmissionState)
	}
	ok, reason = rec.CanAdmit(1)
	if ok {
		t.Fatal("CanAdmit(1) succeeded on invalid record! Must refuse admission")
	}
	if reason == "" {
		t.Fatal("refusal reason must not be empty on invalid record")
	}

	// Even asking for 0 slots must refuse
	ok0, reason0 := rec.CanAdmit(0)
	if ok0 {
		t.Fatal("CanAdmit(0) succeeded on invalid record! Must refuse admission")
	}
	if reason0 == "" {
		t.Fatal("refusal reason for CanAdmit(0) must not be empty on invalid record")
	}
}
