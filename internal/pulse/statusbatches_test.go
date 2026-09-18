package pulse

// The two lines status.sh printed that the status verb did not: the recurring-fault
// detector (Glenn 2026-09-16, "when one small fault recurs, fixing it beats continuing")
// and the swarm's first-attempt success rate, both folded from the batch outputs the
// swarm writes. Every input here is a file a test writes under t.TempDir; no test starts
// nova-swarm and none reads a real batch.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeBatchOut writes one batch output file, newest last, with the stamps spaced a
// minute apart so the newest-first window is deterministic.
func writeBatchOut(t *testing.T, dir, name, body string, mod time.Time) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, mod, mod); err != nil {
		t.Fatal(err)
	}
}

func runStatusBatches(t *testing.T, queue, roots, batches string, now time.Time) (string, string, int) {
	t.Helper()
	var out, errs bytes.Buffer
	code := Status(StatusInput{
		Queue: queue, Roots: roots, Batches: batches, Max: 20,
		Stdout: &out, Stderr: &errs, Now: func() time.Time { return now },
	})
	return out.String(), errs.String(), code
}

// status-swarm-first-attempt: done and abstain are summed over the batch outputs in the
// window, the ratio is done/(done+abstain), and below 0.90 the hedge names the remedy
// (Glenn: Opus hedges the critical path until it holds above 0.90 for a day).
func TestStatusFoldsSwarmFirstAttemptFromBatchOutputs(t *testing.T) {
	queue, roots, _, now := setupStatus(t, t.TempDir())
	batches := filepath.Join(t.TempDir(), "batches")
	base := now.Add(-time.Hour)
	writeBatchOut(t, batches, "batch-1.out",
		"BATCH b1 n=10 done=8 abstain=2 in=1 out=2 usd=0.10 idle=0 stalled=0\n", base)
	writeBatchOut(t, batches, "batch-2.out",
		"BATCH b2 n=10 done=2 abstain=8 in=1 out=2 usd=0.10 idle=0 stalled=0\n", base.Add(time.Minute))

	out, errs, code := runStatusBatches(t, queue, roots, batches, now)
	if code != 0 {
		t.Fatalf("status exit = %d, want 0; stderr=%s", code, errs)
	}
	want := "STATUS SWARM first_attempt=0.50 done=10 abstain=10 batches=2 hedge=opus-on-critical-path"
	if !strings.Contains(out, want) {
		t.Fatalf("status does not carry %q:\n%s", want, out)
	}
}

// A rate at or above the floor hedges nothing: the line still prints, and it says none.
func TestStatusSwarmAboveTheFloorHedgesNothing(t *testing.T) {
	queue, roots, _, now := setupStatus(t, t.TempDir())
	batches := filepath.Join(t.TempDir(), "batches")
	writeBatchOut(t, batches, "batch-1.out",
		"BATCH b1 n=10 done=10 abstain=0 in=1 out=2 usd=0.10 idle=0 stalled=0\n", now.Add(-time.Minute))

	out, errs, code := runStatusBatches(t, queue, roots, batches, now)
	if code != 0 {
		t.Fatalf("status exit = %d, want 0; stderr=%s", code, errs)
	}
	want := "STATUS SWARM first_attempt=1.00 done=10 abstain=0 batches=1 hedge=none"
	if !strings.Contains(out, want) {
		t.Fatalf("status does not carry %q:\n%s", want, out)
	}
}

// status-pit-stop: one reason recurring five times or more in the window is the pit stop
// -- fix the machinery before more cards -- and it names the reason and the count.
func TestStatusPitStopWhenOneReasonRecurs(t *testing.T) {
	queue, roots, _, now := setupStatus(t, t.TempDir())
	batches := filepath.Join(t.TempDir(), "batches")
	var b strings.Builder
	b.WriteString("BATCH b1 n=7 done=2 abstain=5 in=1 out=2 usd=0.10 idle=0 stalled=0\n")
	for i := 0; i < 5; i++ {
		b.WriteString("card-a" + string(rune('0'+i)) + " slot=1: ABSTAIN reason=no-branch log=40\n")
	}
	b.WriteString("card-b1 slot=2: ABSTAIN reason=timeout log=12\n")
	writeBatchOut(t, batches, "batch-1.out", b.String(), now.Add(-time.Minute))

	out, errs, code := runStatusBatches(t, queue, roots, batches, now)
	if code != 0 {
		t.Fatalf("status exit = %d, want 0; stderr=%s", code, errs)
	}
	for _, want := range []string{
		"STATUS FAULT reason=no-branch count=5",
		"STATUS FAULT reason=timeout count=1",
		"STATUS PIT-STOP reason=no-branch count=5",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("status does not carry %q:\n%s", want, out)
		}
	}
}

// Under the threshold the faults print and the pit stop does not: a fault that happened
// twice is not a reason to stop the fleet.
func TestStatusFaultsBelowTheThresholdAreNotAPitStop(t *testing.T) {
	queue, roots, _, now := setupStatus(t, t.TempDir())
	batches := filepath.Join(t.TempDir(), "batches")
	writeBatchOut(t, batches, "batch-1.out",
		"BATCH b1 n=4 done=2 abstain=2 in=1 out=2 usd=0.10 idle=0 stalled=0\n"+
			"card-a1 slot=1: ABSTAIN reason=timeout log=40\n"+
			"card-a2 slot=2: ABSTAIN reason=timeout log=40\n", now.Add(-time.Minute))

	out, errs, code := runStatusBatches(t, queue, roots, batches, now)
	if code != 0 {
		t.Fatalf("status exit = %d, want 0; stderr=%s", code, errs)
	}
	if !strings.Contains(out, "STATUS FAULT reason=timeout count=2") {
		t.Errorf("status does not carry the fault count:\n%s", out)
	}
	if strings.Contains(out, "STATUS PIT-STOP") {
		t.Errorf("two faults are not a pit stop:\n%s", out)
	}
}

// The BATCH line's own uniform-abstain= field is a label, not a count: a fold that read it
// as the abstain count would double the day's abstains.
func TestStatusSwarmIgnoresUniformAbstainField(t *testing.T) {
	queue, roots, _, now := setupStatus(t, t.TempDir())
	batches := filepath.Join(t.TempDir(), "batches")
	writeBatchOut(t, batches, "batch-1.out",
		"BATCH b1 n=2 done=0 abstain=2 in=1 out=2 usd=0.10 idle=0 stalled=0 uniform-abstain=admission\n",
		now.Add(-time.Minute))

	out, _, _ := runStatusBatches(t, queue, roots, batches, now)
	if !strings.Contains(out, "done=0 abstain=2 batches=1") {
		t.Fatalf("the uniform-abstain label was folded as a count:\n%s", out)
	}
}

// No --batches is no claim about the swarm: the two lines do not print, and the rest of
// the report is untouched. A path is a flag here and there is no default to guess.
func TestStatusWithoutBatchesSaysNothingAboutTheSwarm(t *testing.T) {
	queue, roots, _, now := setupStatus(t, t.TempDir())
	out, errs, code := runStatusBatches(t, queue, roots, "", now)
	if code != 0 {
		t.Fatalf("status exit = %d, want 0; stderr=%s", code, errs)
	}
	for _, banned := range []string{"STATUS SWARM", "STATUS FAULT", "STATUS PIT-STOP"} {
		if strings.Contains(out, banned) {
			t.Errorf("status claims %q with no --batches:\n%s", banned, out)
		}
	}
}

// A --batches that names nothing readable is refused with one remedy line, never folded as
// a healthy swarm: a quiet zero would read as "no faults" when the truth is "no answer".
func TestStatusRefusesUnreadableBatches(t *testing.T) {
	queue, roots, _, now := setupStatus(t, t.TempDir())
	missing := filepath.Join(t.TempDir(), "no-such-dir")
	out, errs, code := runStatusBatches(t, queue, roots, missing, now)
	if code != 2 {
		t.Fatalf("status exit = %d, want 2; stdout=%s stderr=%s", code, out, errs)
	}
	if !strings.Contains(errs, "--batches") {
		t.Errorf("the refusal does not name --batches:\n%s", errs)
	}
	if got := strings.Count(strings.TrimSpace(errs), "\n"); got != 0 {
		t.Errorf("the refusal is %d lines, want one remedy line:\n%s", got+1, errs)
	}
}

// The window is the newest outputs by modification time: an old batch outside it is not
// folded, so yesterday's faults never look like this hour's.
func TestStatusBatchWindowIsNewestFirst(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "batches")
	base := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	for i := 0; i < batchWindow+3; i++ {
		writeBatchOut(t, dir, fmt.Sprintf("batch-%03d.out", i),
			"BATCH b n=1 done=1 abstain=0 in=0 out=0 usd=0 idle=0 stalled=0\n",
			base.Add(time.Duration(i)*time.Minute))
	}
	got, err := readBatchOutputs(dir)
	if err != nil {
		t.Fatalf("readBatchOutputs: %v", err)
	}
	if got.files != batchWindow {
		t.Fatalf("read %d files, want the newest %d", got.files, batchWindow)
	}
	if got.done != batchWindow {
		t.Fatalf("done = %d, want %d (one per file in the window)", got.done, batchWindow)
	}
}
