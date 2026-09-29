package ci

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/cicost"
	"github.com/mas-bandwidth/nova-tools/internal/cireceipt"
	"gopkg.in/yaml.v3"
)

// TestCISpinCeilingHoldsRatchet verifies that the CI spin ceiling loaded from
// testdata/ci_spin.txt is ratcheted at or below zero (or positive ceiling) and
// that cicost.CheckSpinCeiling and cicost.CheckDevSpin enforce that limit.
func TestCISpinCeilingHoldsRatchet(t *testing.T) {
	t.Parallel()

	ceilingPath := filepath.Join(repoRoot(t), "internal", "ci", "testdata", "ci_spin.txt")
	ceiling, err := cicost.LoadSpinCeiling(ceilingPath)
	if err != nil {
		t.Fatalf("LoadSpinCeiling(%s): %v", ceilingPath, err)
	}
	if ceiling < 0 {
		t.Fatalf("spin ceiling must be >= 0, got %d", ceiling)
	}

	// Compliant entry: spin <= ceiling passes.
	entries := []cicost.Entry{
		{
			ID: "1-0",
			Receipt: cireceipt.Receipt{
				Repo:       "mas-bandwidth/nova-tools",
				RunID:      "1001",
				Conclusion: "success",
			},
			Cost: cicost.Cost{
				Total: 120,
				Spin:  ceiling,
			},
		},
	}
	if err := cicost.CheckSpinCeiling(entries, ceiling); err != nil {
		t.Errorf("CheckSpinCeiling should pass when spin <= ceiling: %v", err)
	}

	// Non-compliant entry: spin > ceiling fails.
	badEntries := []cicost.Entry{
		{
			ID: "1-1",
			Receipt: cireceipt.Receipt{
				Repo:       "mas-bandwidth/nova-tools",
				RunID:      "1002",
				Conclusion: "success",
			},
			Cost: cicost.Cost{
				Total: 150,
				Spin:  ceiling + 1,
			},
		},
	}
	if err := cicost.CheckSpinCeiling(badEntries, ceiling); err == nil {
		t.Error("CheckSpinCeiling should fail when spin > ceiling")
	} else if !strings.Contains(err.Error(), "exceeds ceiling") {
		t.Errorf("error message should mention 'exceeds ceiling': %v", err)
	}

	// CheckDevSpin enforces ceiling on dev branch runs.
	devEntries := []cicost.Entry{
		{
			ID: "2-0",
			Receipt: cireceipt.Receipt{
				Repo:       "mas-bandwidth/nova-tools",
				Workflow:   "ci",
				Conclusion: "success",
				PR:         "", // empty PR is a push to dev
			},
			Cost: cicost.Cost{
				Total: 200,
				Spin:  ceiling,
			},
		},
	}
	if err := cicost.CheckDevSpin(devEntries, ceiling, 5); err != nil {
		t.Errorf("CheckDevSpin should pass on compliant dev entries: %v", err)
	}

	badDevEntries := []cicost.Entry{
		{
			ID: "2-1",
			Receipt: cireceipt.Receipt{
				Repo:       "mas-bandwidth/nova-tools",
				Workflow:   "ci",
				Conclusion: "success",
				PR:         "",
			},
			Cost: cicost.Cost{
				Total: 250,
				Spin:  ceiling + 10,
			},
		},
	}
	if err := cicost.CheckDevSpin(badDevEntries, ceiling, 5); err == nil {
		t.Error("CheckDevSpin should fail when dev spin > ceiling")
	}
}

// TestCIOKReportsCostStepInWorkflow verifies that the ci-ok job in ci.yml
// carries the report CI cost step, which fetches run jobs and reports to
// ci:cost stream via nova-ci cost.
func TestCIOKReportsCostStepInWorkflow(t *testing.T) {
	t.Parallel()

	raw := readFile(t, filepath.Join(repoRoot(t), ".github", "workflows", "ci.yml"))
	var wf ciokWorkflow
	if err := yaml.Unmarshal([]byte(raw), &wf); err != nil {
		t.Fatal(err)
	}
	job, ok := wf.Jobs["ci-ok"]
	if !ok {
		t.Fatal("ci.yml has no ci-ok job")
	}

	var costStepFound bool
	for _, s := range job.Steps {
		if s.Name != "report CI cost" {
			continue
		}
		costStepFound = true
		if strings.TrimSpace(s.If) != "always()" {
			t.Errorf("report CI cost step must have if: always(), got: %s", s.If)
		}
		if !strings.Contains(s.Run, "gh api") || !strings.Contains(s.Run, "actions/runs/${{ github.run_id }}/jobs") {
			t.Errorf("report CI cost step must fetch jobs from GitHub API:\n%s", s.Run)
		}
		if !strings.Contains(s.Run, "go run ./cmd/nova-ci cost") {
			t.Errorf("report CI cost step must call 'go run ./cmd/nova-ci cost':\n%s", s.Run)
		}
		if !strings.Contains(s.Run, "--redis") {
			t.Errorf("report CI cost step must support reporting to Redis with --redis:\n%s", s.Run)
		}
	}
	if !costStepFound {
		t.Fatal("ci-ok has no 'report CI cost' step")
	}
}
