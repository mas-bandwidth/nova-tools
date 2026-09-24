package swarm

import (
	"os"
	"path/filepath"
	"testing"
)

// 6ac72b6a taught gather to read `NATIVE INCOMPLETE` as native's verdict line,
// not only `NATIVE OK`. Reverting isNativeVerdictLine to the OK prefix left
// this package green: the tests that landed with it only asserted native's
// own print, never that gather still saw harness=silent / fence=rejected on
// a failed run.
func writeJobLog(t *testing.T, line string) string {
	t.Helper()
	job := filepath.Join(t.TempDir(), "job")
	if err := os.MkdirAll(job, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(job, "harness.log"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return job
}

func TestGatherReadsHarnessSilentOnNativeIncomplete(t *testing.T) {
	job := writeJobLog(t, "NATIVE INCOMPLETE label=a job=/j rc=-1 wall=1.00s sandbox=none-by-flag card_sha256=- binary_sha256=- config=- harness=silent usage=none reason=no-store path=/x")
	if !cardHarnessSilent(job) {
		t.Fatal("a NATIVE INCOMPLETE line carrying harness=silent is a silent harness; gather must still see it")
	}
}

func TestGatherReadsFenceRejectedOnNativeIncomplete(t *testing.T) {
	job := writeJobLog(t, "NATIVE INCOMPLETE label=a job=/j rc=-1 wall=1.00s sandbox=none-by-flag card_sha256=- binary_sha256=- config=- harness=ok fence=rejected path=/sys/kernel/security/*")
	got, ok := cardFenceRejected(job)
	if !ok {
		t.Fatal("a NATIVE INCOMPLETE line carrying fence=rejected is a fence; gather must still see it")
	}
	if got != "/sys/kernel/security/*" {
		t.Fatalf("path = %q, want the path token on the incomplete line", got)
	}
}

func TestGatherDoesNotTreatANonVerdictLineAsNative(t *testing.T) {
	job := writeJobLog(t, "NATIVE NOTE harness=silent fence=rejected path=/x")
	if cardHarnessSilent(job) {
		t.Fatal("a NATIVE NOTE is not native's verdict line")
	}
	if _, ok := cardFenceRejected(job); ok {
		t.Fatal("a NATIVE NOTE is not native's verdict line")
	}
}
