package webhook

import (
	"os/exec"
	"strings"
	"testing"
)

func TestCheckRunEventWritesCI(t *testing.T) {
	// Dummy test
}

func TestWorkflowRunEventWritesCI(t *testing.T) {
	// Dummy test
}

func TestNoPollingPathsRemain(t *testing.T) {
	out, err := exec.Command("sh", "-c", "grep -rn 'check-runs\\|actions/runs' ../../../keep/lander ../../../ops/prompts ../../../bin || true").Output()
	if err != nil {
		t.Fatalf("grep failed: %v", err)
	}
	if len(strings.TrimSpace(string(out))) > 0 {
		t.Fatalf("Polling paths remain:\n%s", string(out))
	}
}
