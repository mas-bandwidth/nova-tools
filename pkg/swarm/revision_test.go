package swarm

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Demanded test 16's last line: THE SOURCE TRIPWIRE. No modification time decides anything
// in this package -- not what is fresh, not what was consumed, not which revision a page
// holds. A revision is its bytes.
func TestNoModTimeDecidesAnythingInThisPackage(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		// THE EXCEPTIONS, each about LIVENESS ON DISK and never about a report's identity.
		// The bytes-are-revision rule this test guards is unbroken by both.
		//
		//   reap.go (issue #1048, SPEC-SWARM rule 19) reads a harness log's AGE to decide
		//   whether a slot is finished.
		//
		//   lease.go (issue #1585) reads the job lease's mtime, which IS the heartbeat --
		//   the reaper reads the same rule. It is read for one question only: a lease whose
		//   owner this kernel cannot be asked about (another host, or a record that did not
		//   parse) is HELD until its heartbeat is older than JobLeaseStale. Without it an
		//   unfinished record reads as a dead owner and a second launcher takes a live job
		//   directory, which is the P1 Stella held the first repair for.
		if name == "reap.go" || name == "lease.go" {
			continue
		}
		raw, err := os.ReadFile(name)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), "ModTime", "%s reads a modification time; a revision is its bytes and never its mtime", name)
	}
}
