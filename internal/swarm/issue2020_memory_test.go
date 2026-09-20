package swarm

import "testing"

// TestIssue2020Repro checks for memory limit configuration per card (issue #2020)
func TestIssue2020Repro(t *testing.T) {
	// Issue #2020: Each card's harness process uses ~560MB RSS before tests run
	// The swarm should configure NODE_OPTIONS with --max-old-space-size for memory efficiency
	// This test checks if memory limits are configured for child processes

	w := Worker{Name: "test"}
	env := childEnv(w, 1, "task-id", "key", "/tmp/root")

	// Check for NODE_OPTIONS with memory limits
	hasMemoryLimit := false
	for _, e := range env {
		if len(e) > 12 && e[:12] == "NODE_OPTIONS" {
			if containsSubstring(e, "--max-old-space-size") {
				hasMemoryLimit = true
				break
			}
		}
	}

	if !hasMemoryLimit {
		t.Error("No NODE_OPTIONS memory limit configuration found for card processes - issue #2020 suggests configuring --max-old-space-size to reduce ~560MB RSS per card")
	}
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
