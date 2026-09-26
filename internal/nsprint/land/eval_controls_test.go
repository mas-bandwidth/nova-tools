package land_test

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
)

// TestRunnerIDTracksBuild: runner_id names the stamped build, not a constant.
func TestRunnerIDTracksBuild(t *testing.T) {
	old := land.ToolsVersion
	t.Cleanup(func() { land.ToolsVersion = old })
	land.ToolsVersion = "v9.9.9-test"
	if id := land.RunnerID(); !strings.HasPrefix(id, "nova-tools-v9.9.9-test/go-") {
		t.Fatalf("runner id %q does not track the stamped build", id)
	}
	land.ToolsVersion = ""
	if id := land.RunnerID(); strings.Contains(id, "v0.12.0") {
		t.Fatalf("runner id %q is the old hard-coded version", id)
	}
}
