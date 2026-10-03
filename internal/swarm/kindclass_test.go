package swarm

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestKindClassificationPreservesTestNone pins today's header decisions.
// The six gated names refuse TEST: none with a why. The five ungated names
// accept it. A bare TEST: none is refused for an ungated name. An unknown
// name stays kind-declared and is not treated as ungated. A named test draws
// no new token: the class set is not on, so a card that does not carry gofmt,
// vet, or the class-test command is not a new refusal.
func TestKindClassificationPreservesTestNone(t *testing.T) {
	t.Parallel()

	src, err := os.ReadFile("lintheader.go")
	require.NoError(t, err)
	require.Contains(t, string(src), "hygiene.KindUngated(")
	require.NotContains(t, string(src), "ungatedKinds")
	require.Equal(t, []string{"kind-declared", "paths-declared", "paused", "test-named"}, CardHeaderChecks())

	gated := []string{"fix-red", "transcript-test", "rebase", "sweep", "mutation-kill", "guard"}
	ungated := []string{"read", "probe", "text", "tone", "report"}

	for _, kind := range gated {
		t.Run("none-refused-"+kind, func(t *testing.T) {
			t.Parallel()
			raw := headerCard(t, "KIND: "+kind, "PATHS: internal/swarm/a.go", "TEST: none the fixture has a why")
			fs := findingsOn(raw)
			require.Equal(t, []string{"test-named"}, findingChecks(fs), "kind %q:\n%s", kind, dumpFindings(fs))
			require.Contains(t, fs[0].Excerpt, `TEST: none is not allowed for kind "`+kind+`"`)
			assert.NotContains(t, fs[0].Excerpt, "gofmt")
			assert.NotContains(t, fs[0].Excerpt, "go vet")
			assert.NotContains(t, fs[0].Excerpt, "gate-class")
		})
		t.Run("named-test-draws-nothing-"+kind, func(t *testing.T) {
			t.Parallel()
			raw := headerCard(t, "KIND: "+kind, "PATHS: internal/swarm/a.go", "TEST: internal/swarm TestA")
			fs := findingsOn(raw)
			assert.Empty(t, fs, "kind %q with a named test and no class commands:\n%s", kind, dumpFindings(fs))
		})
	}
	for _, kind := range ungated {
		t.Run("none-accepted-"+kind, func(t *testing.T) {
			t.Parallel()
			raw := headerCard(t, "KIND: "+kind, "PATHS: internal/swarm/a.go", "TEST: none the kind changes no code")
			fs := findingsOn(raw)
			assert.Empty(t, fs, "kind %q is ungated:\n%s", kind, dumpFindings(fs))
		})
		t.Run("bare-none-refused-"+kind, func(t *testing.T) {
			t.Parallel()
			raw := headerCard(t, "KIND: "+kind, "PATHS: internal/swarm/a.go", "TEST: none")
			fs := findingsOn(raw)
			require.Equal(t, []string{"test-named"}, findingChecks(fs), "bare TEST: none on %q:\n%s", kind, dumpFindings(fs))
			require.Contains(t, dumpFindings(fs), "says no why")
			assert.NotContains(t, dumpFindings(fs), "gate-class")
		})
	}

	t.Run("unknown-is-not-ungated", func(t *testing.T) {
		t.Parallel()
		raw := headerCard(t, "KIND: completely-unknown-kind", "PATHS: internal/swarm/a.go", "TEST: none the fixture has a why")
		fs := findingsOn(raw)
		require.Equal(t, []string{"kind-declared", "test-named"}, findingChecks(fs), dumpFindings(fs))
		assert.NotContains(t, dumpFindings(fs), "gate-class")
	})
	t.Run("empty-kind-is-not-a-second-refusal", func(t *testing.T) {
		t.Parallel()
		raw := headerCard(t, "KIND:", "PATHS: internal/swarm/a.go", "TEST: none the fixture has a why")
		fs := findingsOn(raw)
		require.Equal(t, []string{"kind-declared"}, findingChecks(fs), dumpFindings(fs))
	})
}

func findingChecks(fs []CardHeaderFinding) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.Check)
	}
	return out
}
