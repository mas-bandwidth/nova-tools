package pulse

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// CI-wall (issue #888): a run whose wall (created_at to updated_at) is over
// 120 s writes one REDS line naming the longest job, and the gate's status
// line carries STOP: ci-wall. A wall under 120 s changes nothing.

type wallRuns struct {
	runs []CIRun
	jobs []CIJob
}

func (f *wallRuns) LatestRun(repo, branch string) ([]CIRun, error) { return f.runs, nil }
func (f *wallRuns) FailedJob(repo string, runID int64) (CIJob, error) {
	if len(f.jobs) > 0 {
		return f.jobs[0], nil
	}
	return CIJob{}, nil
}
func (f *wallRuns) Jobs(repo string, runID int64) ([]CIJob, error) { return f.jobs, nil }

func TestGateCIWallOverTwoMinutesWritesReds(t *testing.T) {
	queue := t.TempDir()
	src := &wallRuns{
		runs: []CIRun{{ID: 101, Status: "completed", Conclusion: "success", HeadSHA: "aaaaaaaaaaaaaaaa", Workflow: "ci", Event: "push", CreatedAt: "2026-09-17T12:00:00Z", UpdatedAt: "2026-09-17T12:03:20Z"}},
		jobs: []CIJob{
			{Name: "quick", StartedAt: "2026-09-17T12:00:00Z", CompletedAt: "2026-09-17T12:00:30Z"},
			{Name: "long-pole-job", StartedAt: "2026-09-17T12:00:00Z", CompletedAt: "2026-09-17T12:03:10Z"},
		},
	}
	out, errb, _ := gateRun(t, queue, src)
	raw, err := os.ReadFile(filepath.Join(queue, "REDS"))
	if err != nil {
		t.Fatalf("a 200 s run wrote no REDS line (err=%v); out=%q err=%q", err, out, errb)
	}
	line := strings.TrimSpace(string(raw))
	if !strings.Contains(line, "CI-WALL 101 200") {
		t.Fatalf("REDS line = %q, want CI-WALL 101 200", line)
	}
	if !strings.Contains(line, "long-pole=long-pole-job") {
		t.Fatalf("REDS line = %q, want it to name the longest job", line)
	}
	if !strings.Contains(out+errb, "STOP: ci-wall") {
		t.Fatalf("gate status line = %q, want it to carry STOP: ci-wall", out+errb)
	}
}

func TestGateCIWallUnderTwoMinutesChangesNothing(t *testing.T) {
	queue := t.TempDir()
	src := &wallRuns{
		runs: []CIRun{{ID: 102, Status: "completed", Conclusion: "success", HeadSHA: "bbbbbbbbbbbbbbbb", Workflow: "ci", Event: "push", CreatedAt: "2026-09-17T12:00:00Z", UpdatedAt: "2026-09-17T12:00:30Z"}},
		jobs: []CIJob{
			{Name: "quick", StartedAt: "2026-09-17T12:00:00Z", CompletedAt: "2026-09-17T12:00:30Z"},
		},
	}
	out, errb, _ := gateRun(t, queue, src)
	if raw, err := os.ReadFile(filepath.Join(queue, "REDS")); err == nil && strings.Contains(string(raw), "CI-WALL") {
		t.Fatalf("a 30 s run wrote a REDS line: %q", string(raw))
	}
	if strings.Contains(out+errb, "ci-wall") {
		t.Fatalf("a 30 s run carried ci-wall on the status line: %q%q", out, errb)
	}
}
