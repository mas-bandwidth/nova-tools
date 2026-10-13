package docs

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The bench lock is disabled until v1.3 (Glenn, 2026-10-13: "Stop with these bench
// locks. Verboten." "Disable until then. Not a sanctioned or stable feature. Not to be
// used."; v1.3 card the-server-schedules-bench-work-not-a-lock). This repository has no
// code that hands out a bench or machine lock; the rule that asked for one was the
// standard's "one test stream per machine", which children carried out as a machine-wide
// flock and queued behind each other's unrelated jobs. The standard, and the AGENTS.md
// that embeds it, now say to take no bench lock, and never again ask for one stream per
// machine. It reads text and runs nothing.
func TestTheStandardTakesNoBenchLock(t *testing.T) {
	t.Parallel()
	for _, path := range []string{standardPath, agentsPath} {
		page, err := os.ReadFile(path)
		require.NoError(t, err, "%s: %v", path, err)
		body := string(page)
		assert.NotContains(t, body, "one test stream per machine", "%s asks for a machine-wide lock; bench locks are disabled until v1.3", path)
		assert.Contains(t, body, "take no bench lock", "%s must say to take no bench lock until v1.3", path)
	}
}
