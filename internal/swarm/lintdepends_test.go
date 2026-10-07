package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// #2636, THE LINT THIRD. Three refusals and two passes. The card under test is
// the typedCard fixture, whose contract line names its own id as CARD-0000.

func dependsHeader(value string) []byte {
	h := append(fullHeader(), "DEPENDS-ON: "+value)
	return typedCard(h...)
}

func TestLintDependsRefusesAMissingKey(t *testing.T) {
	t.Parallel()

	fs := LintCardDepends(typedCard(fullHeader()...), nil)
	require.Len(t, fs, 1, "a typed card with no DEPENDS-ON key draws depends-on, got %v", fs)
	require.Equal(t, "depends-on", fs[0].Check, "a typed card with no DEPENDS-ON key draws depends-on, got %v", fs)
	require.Contains(t, fs[0].Excerpt, "DEPENDS-ON", "the refusal names the key: %q", fs[0].Excerpt)
}

func TestLintDependsRefusesASelfDependency(t *testing.T) {
	t.Parallel()

	fs := LintCardDepends(dependsHeader("other-card, CARD-0000"), Lineup{"other-card": true})
	require.Len(t, fs, 1, "a card that names its own id is a self-dependency, got %v", fs)
	require.Contains(t, fs[0].Excerpt, "CARD-0000", "a card that names its own id is a self-dependency, got %v", fs)
	require.Contains(t, fs[0].Excerpt, "own id", "a card that names its own id is a self-dependency, got %v", fs)
	require.NotContains(t, fs[0].Excerpt, "not in the lineup", "a self-dependency is not reported as an unknown id: %q", fs[0].Excerpt)
}

func TestLintDependsRefusesAnUnknownID(t *testing.T) {
	t.Parallel()

	fs := LintCardDepends(dependsHeader("missing-card"), Lineup{"other-card": true})
	require.Len(t, fs, 1, "an id the lineup does not hold is refused, got %v", fs)
	require.Contains(t, fs[0].Excerpt, "missing-card", "an id the lineup does not hold is refused, got %v", fs)
	require.Contains(t, fs[0].Excerpt, "not in the lineup", "an id the lineup does not hold is refused, got %v", fs)
	// No lineup is not an empty lineup: the lint does not guess.
	fs = LintCardDepends(dependsHeader("missing-card"), nil)
	require.Empty(t, fs, "with no lineup an id is not called unknown, got %v", fs)
}

func TestLintDependsDashPasses(t *testing.T) {
	t.Parallel()

	fs := LintCardDepends(dependsHeader("-"), Lineup{"other-card": true})
	require.Empty(t, fs, "DEPENDS-ON: - passes, and `-` is not looked up as an id, got %v", fs)
}

func TestLintDependsReferenceIsNotLookedUp(t *testing.T) {
	t.Parallel()

	const ref = "mas-bandwidth/nova-tools#2550"
	lineup := Lineup{"other-card": true}
	_, ok := lineup[ref]
	require.False(t, ok, "the fixture lineup must not contain the reference")
	fs := LintCardDepends(dependsHeader(ref), lineup)
	require.Empty(t, fs, "owner/repo#n passes and is not looked up in the lineup, got %v", fs)
}

func TestLintDependsRefusesASpaceAndDogfood(t *testing.T) {
	t.Parallel()

	lineup := Lineup{"other-card": true}
	fs := LintCardDepends(dependsHeader("nova-tools #2550"), lineup)
	require.NotEmpty(t, fs, "nova-tools #2550 (a space) is refused by name, got %v", fs)
	require.True(t, dependsNames(fs, "nova-tools #2550"), "nova-tools #2550 (a space) is refused by name, got %v", fs)
	fs = LintCardDepends(dependsHeader("dogfood"), lineup)
	require.NotEmpty(t, fs, "dogfood is refused by name, got %v", fs)
	require.True(t, dependsNames(fs, "dogfood"), "dogfood is refused by name, got %v", fs)
}

func dependsNames(fs []CardHeaderFinding, name string) bool {
	for _, f := range fs {
		if f.Check == "depends-on" && strings.Contains(f.Excerpt, name) {
			return true
		}
	}
	return false
}

func TestLintDependsKnownIDPasses(t *testing.T) {
	t.Parallel()

	lineup := Lineup{"other-card": true, "third-card": true}
	fs := LintCardDepends(dependsHeader("other-card, third-card"), lineup)
	require.Empty(t, fs, "ids the lineup holds pass, got %v", fs)
}

func TestReadLineupSkipsTheDependsOnHeader(t *testing.T) {
	t.Parallel()

	p := filepath.Join(t.TempDir(), "ORDER.tsv")
	body := "id\tdepends-on\nother-card\t-\nthird-card\tother-card\n# a comment\n\n"
	require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	got, err := ReadLineup(p)
	require.NoError(t, err)
	require.True(t, got["other-card"], "the lineup is the id column, not the header and not `-`: %v", got)
	require.True(t, got["third-card"], "the lineup is the id column, not the header and not `-`: %v", got)
	require.False(t, got["id"], "the lineup is the id column, not the header and not `-`: %v", got)
	require.False(t, got["-"], "the lineup is the id column, not the header and not `-`: %v", got)
	require.False(t, got["depends-on"], "the lineup is the id column, not the header and not `-`: %v", got)
}
