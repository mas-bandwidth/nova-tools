package swarm

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIssue2579 verifies that a card ended for idleness (not wall refusal)
// reports CARD IDLE, not WALL REFUSED. The bug was that WriteBlockedResult
// always wrote WALL REFUSED regardless of whether a refusal occurred.
//
// Measured 2026-09-22: a card that stopped making progress cost its whole
// deadline before anybody looked. The idle watch was added to end such cards
// early, but the report it wrote said WALL REFUSED even when no wall refused
// anything -- just the card became silent.
//
// From nova-tools#2579 issue: "the report says WALL REFUSED for a card no
// wall refused. This is exactly the misattribution `pkg/swarm/nativeidle.go:96-103`
// was written to prevent: It is deliberately not a WALL line."
func TestIssue2579(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, task, kind, target, step, reason, want, unwanted string
	}{
		{"idle", "task-idle", "", "", "-", "the card's log and its process tree were both still for 300s; the run ended it rather than holding the slot to its deadline", "CARD IDLE task=task-idle step=- idle=300s", "WALL REFUSED"},
		{"wall refused", "task-wall", "write", "/etc/passwd", "2", "the wall refused write /etc/passwd and the card wrote nothing for 300s after it", "WALL REFUSED write /etc/passwd", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path, wrote, err := WriteBlockedResult(t.TempDir(), tc.task, tc.kind, tc.target, tc.step, 300*time.Second, tc.reason)
			require.NoError(t, err)
			assert.True(t, wrote)

			raw, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Contains(t, string(raw), tc.want)
			if tc.unwanted != "" {
				assert.NotContains(t, string(raw), tc.unwanted)
			}
		})
	}
}
