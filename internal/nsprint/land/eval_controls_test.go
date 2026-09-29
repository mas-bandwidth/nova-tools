package land_test

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
)

// TestRunnerIDTracksBuild: runner_id names the stamped build, not a constant.
func TestRunnerIDTracksBuild(t *testing.T) {
	t.Parallel()
	if id := land.RunnerIDFrom("v9.9.9-test"); !strings.HasPrefix(id, "nova-tools-v9.9.9-test/go-") {
		t.Fatalf("runner id %q does not track the stamped build", id)
	}
	if id := land.RunnerIDFrom(""); strings.Contains(id, "v0.12.0") {
		t.Fatalf("runner id %q is the old hard-coded version", id)
	}
}
