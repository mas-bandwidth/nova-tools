package docs

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClassRuleNamesComeOnlyFromLiveEntries(t *testing.T) {
	t.Parallel()
	spec := "### `outside` — not indexed\n" +
		"## The class tests\n### `wall clock` — clock rule\n" +
		"```markdown\n### `example` — not indexed\n```\n" +
		"   ~~~~\n## Other section\n### `nested example` — not indexed\n~~~\n~~~~\n" +
		"### Tests this spec demands\n### `alpha` — first rule\n" +
		"## Parked class tests\n### `parked` — not live\n"
	got, err := classRules(spec)
	require.NoError(t, err)
	require.Equal(t, []string{"alpha", "wall clock"}, got, "sorted live names only")
}

func TestClassRuleExtractionRefusesBrokenAuthority(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, spec, want string
	}{
		{"missing section", "### `rule` — description\n", "no named class rules"},
		{"empty section", "## The class tests\n## Other\n### `rule` — description\n", "no named class rules"},
		{"duplicate", "## The class tests\n### `rule` — first\n### `rule` — second\n", "duplicate class rule"},
		{"repeated section", "## The class tests\n### `alpha` — first\n## The class tests\n### `beta` — second\n", "duplicate ## The class tests section"},
		{"later repeated section", "## The class tests\n### `alpha` — first\n## Other\n## The class tests\n### `beta` — second\n", "duplicate ## The class tests section"},
		{"empty name", "## The class tests\n### `` — empty\n", "malformed class-rule heading"},
		{"padded name", "## The class tests\n### ` rule` — padded\n", "malformed class-rule heading"},
		{"missing separator", "## The class tests\n### `rule` description\n", "malformed class-rule heading"},
		{"empty description", "## The class tests\n### `rule` — \n", "malformed class-rule heading"},
		{"unclosed fence", "## The class tests\n### `rule` — description\n```\n", "unclosed fence"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := classRules(tc.spec)
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestClassRuleGenerationChangesOnlyMarkedNames(t *testing.T) {
	t.Parallel()
	const spec = "## The class tests\n### `zeta` — last\n### `alpha` — first\n"
	before, after := "# Standard\n\nKeep this prose.\n", "\nKeep the remedy.\n"
	for _, old := range []string{"", "\n`stale`.\n"} {
		input := before + classRulesStart + "\n" + old + classRulesEnd + "\n" + after
		want := before + classRulesStart + "\n\n`alpha`, `zeta`.\n\n" + classRulesEnd + "\n" + after
		got, err := classRuleStandard(input, spec)
		require.NoError(t, err)
		require.Equal(t, want, got, "generated standard")
		again, err := classRuleStandard(got, spec)
		require.NoError(t, err)
		require.Equal(t, got, again, "second generation must not change the standard")
	}
}

func TestClassRuleGenerationRefusesAmbiguousMarkers(t *testing.T) {
	t.Parallel()
	const spec = "## The class tests\n### `rule` — description\n"
	for _, tc := range []struct {
		name, page string
	}{
		{"missing", "# Standard\n"},
		{"duplicate start", "\n" + classRulesStart + "\n" + classRulesStart + "\n" + classRulesEnd + "\n"},
		{"duplicate end", "\n" + classRulesStart + "\n" + classRulesEnd + "\n" + classRulesEnd + "\n"},
		{"reversed", "\n" + classRulesEnd + "\n" + classRulesStart + "\n"},
		{"inline", "prefix " + classRulesStart + "\n" + classRulesEnd + "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := classRuleStandard(tc.page, spec)
			require.ErrorContains(t, err, "markers")
		})
	}
}
