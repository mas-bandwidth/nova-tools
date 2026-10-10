package sprint_test

import (
	"os"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/release"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Moved from internal/release/journeygate_test.go (the nova-sprint split): the chaos
// suite lives with the sprint, so its promise is checked beside it.

// The promise names the chaos suite's own subtests: a journey renamed there
// and not here would be not-run forever, and the gate would refuse every cut
// for a test nobody can run.
func TestThePromisedJourneysAreTheChaosSuitesSubtests(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("friend_chaos_functional_test.go")
	require.NoError(t, err)
	src := string(raw)
	require.NotEmpty(t, release.PromisedJourneys)
	for _, j := range release.PromisedJourneys {
		parent, sub, ok := strings.Cut(j.Test, "/")
		require.True(t, ok, "%s names no subtest", j.Test)
		assert.Contains(t, src, "func "+parent+"(t *testing.T)", "the chaos suite has no %s", parent)
		assert.Contains(t, src, "t.Run(\""+sub+"\"", "the chaos suite has no subtest %q", sub)
		assert.Equal(t, "internal/sprint", j.Package)
	}
}
