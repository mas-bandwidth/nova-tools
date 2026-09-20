package pulse

import (
	"errors"
	"testing"
)

func TestPulseBenchRecordReExport(t *testing.T) {
	settings := RequestedSettings{
		Share:          16,
		MaxLoadPerCore: 3.0,
	}

	rec, err := NewBenchRecord("local", settings)
	if err != nil {
		t.Fatalf("NewBenchRecord from pulse package failed: %v", err)
	}

	snap := rec.Snapshot()
	if snap.Bench != "local" {
		t.Errorf("got bench %q, want local", snap.Bench)
	}
	if snap.Enforced.AdmissionState != AdmissionAdmitting {
		t.Errorf("got admission state %q, want admitting", snap.Enforced.AdmissionState)
	}
	if ok, _ := rec.CanAdmit(1); !ok {
		t.Errorf("CanAdmit(1) failed")
	}

	// Stale revision check via pulse re-export
	err = rec.UpdateRequested(99, settings)
	if !errors.Is(err, ErrStaleRevision) {
		t.Errorf("expected ErrStaleRevision, got %v", err)
	}
}
