package pulse

// The gate verb's acceptance replay (rule C of #828, pit stop 3): the mechanical red gate.
// A fake run source, not gh and not the network, drives every test below; the real source
// is the RuneSource the verb's flags fall back to, and it is never reached here.

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeRunSource answers the one query the gate makes with a canned run or an error.
type fakeRunSource struct {
	run Run
	err error
}

func (f fakeRunSource) LatestRun(context.Context, string, string, time.Duration) (Run, error) {
	return f.run, f.err
}

func gateInput(root, admission string, src RunSource) GateInput {
	return GateInput{
		Repo:      "mas-bandwidth/nova-tools",
		Branch:    "dev",
		Root:      root,
		Admission: admission,
		Source:    src,
	}
}

func runGate(in GateInput) (stdout, stderr string, code int) {
	var out, errs bytes.Buffer
	in.Stdout = &out
	in.Stderr = &errs
	code = Gate(in)
	return out.String(), errs.String(), code
}

func stopPath(root string) string { return filepath.Join(root, "STOP") }

func readStop(t *testing.T, root string) string {
	t.Helper()
	raw, err := os.ReadFile(stopPath(root))
	if err != nil {
		return ""
	}
	return string(raw)
}

func TestGateFailureWritesStop(t *testing.T) {
	root := t.TempDir()
	run := Run{
		Branch: "dev", SHA: "abc123", Status: "completed", Conclusion: "failure",
		Job: "go test ./internal/pulse/", Test: "TestGateSuccessKeepsPersonStop",
	}
	out, errs, code := runGate(gateInput(root, "#828", fakeRunSource{run: run}))

	if code != 0 {
		t.Fatalf("exit %d, want 0; stderr=%q", code, errs)
	}
	stop := readStop(t, root)
	if stop == "" {
		t.Fatal("no STOP file written on a failed run")
	}
	lines := strings.Split(strings.TrimRight(stop, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("STOP has %d lines, want 2:\n%s", len(lines), stop)
	}
	if !strings.Contains(lines[0], "branch=dev") || !strings.Contains(lines[0], "sha=abc123") ||
		!strings.Contains(lines[0], "test=TestGateSuccessKeepsPersonStop") {
		t.Errorf("STOP line 1 does not name branch, sha and test: %q", lines[0])
	}
	if lines[1] != "#828" {
		t.Errorf("STOP line 2 (admission) = %q, want %q", lines[1], "#828")
	}
	if !strings.Contains(out, "GATE RED branch=dev sha=abc123") {
		t.Errorf("GATE RED line missing from stdout: %q", out)
	}
}

func TestGateCancelledHoldsVerdict(t *testing.T) {
	root := t.TempDir()
	// A red run first: the gate writes its own STOP.
	runGate(gateInput(root, "#828", fakeRunSource{run: Run{
		Branch: "dev", SHA: "abc123", Status: "completed", Conclusion: "failure",
		Job: "build", Test: "TestA",
	}}))
	before := readStop(t, root)

	// A cancelled run is not red: it holds the previous verdict and leaves STOP alone.
	out, _, code := runGate(gateInput(root, "#828", fakeRunSource{run: Run{
		Branch: "dev", SHA: "def456", Status: "completed", Conclusion: "cancelled",
	}}))

	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	if after := readStop(t, root); after != before {
		t.Errorf("cancelled run changed the STOP file:\nbefore=%q\nafter=%q", before, after)
	}
	if !strings.Contains(out, "HELD") {
		t.Errorf("GATE line should say held, got %q", out)
	}
}

func TestGateInProgressNotRed(t *testing.T) {
	root := t.TempDir()
	out, _, code := runGate(gateInput(root, "#828", fakeRunSource{run: Run{
		Branch: "dev", SHA: "abc123", Status: "in_progress", Conclusion: "",
	}}))

	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	if readStop(t, root) != "" {
		t.Error("an in-progress run wrote a STOP; in_progress is not red")
	}
	if !strings.Contains(out, "HELD") {
		t.Errorf("GATE line should say held, got %q", out)
	}
}

func TestGateSuccessRemovesGateStop(t *testing.T) {
	root := t.TempDir()
	// The gate writes its own STOP on a red run.
	runGate(gateInput(root, "#828", fakeRunSource{run: Run{
		Branch: "dev", SHA: "abc123", Status: "completed", Conclusion: "failure",
		Job: "build", Test: "TestA",
	}}))
	if readStop(t, root) == "" {
		t.Fatal("setup: the red run wrote no STOP")
	}

	out, _, code := runGate(gateInput(root, "#828", fakeRunSource{run: Run{
		Branch: "dev", SHA: "abc123", Status: "completed", Conclusion: "success",
	}}))

	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	if readStop(t, root) != "" {
		t.Errorf("success did not remove the STOP the gate wrote: %q", readStop(t, root))
	}
	if !strings.Contains(out, "GATE GREEN") {
		t.Errorf("GATE GREEN line missing from stdout: %q", out)
	}
}

func TestGateSuccessKeepsPersonStop(t *testing.T) {
	root := t.TempDir()
	// A person wrote a STOP, free text -- not the gate's shape.
	if err := os.WriteFile(stopPath(root), []byte("STOP: hold all fixes; see the flaky run\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, _, code := runGate(gateInput(root, "#828", fakeRunSource{run: Run{
		Branch: "dev", SHA: "abc123", Status: "completed", Conclusion: "success",
	}}))

	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	if readStop(t, root) == "" {
		t.Error("success removed a STOP a person wrote; only the gate's own STOP is removed")
	}
	if !strings.Contains(out, "GATE GREEN") {
		t.Errorf("GATE GREEN line missing from stdout: %q", out)
	}
}

func TestGateRefusesUnreadableRun(t *testing.T) {
	root := t.TempDir()
	_, errs, code := runGate(gateInput(root, "#828", fakeRunSource{
		err: context.DeadlineExceeded,
	}))

	if code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if !strings.Contains(errs, "GATE REFUSED") {
		t.Errorf("refusal missing from stderr: %q", errs)
	}
}
