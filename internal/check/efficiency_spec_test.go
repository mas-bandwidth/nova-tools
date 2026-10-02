package check

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The efficiency contract of nova-check lives in docs/SPEC.md: one walk of the
// root per quickstart behind a shared path list, a capped read with the remedy
// on the MORE line, and no clock, subprocess, network or lock to wait on. This
// test reads that section out of the spec, because the spec is the one place
// the contract is written.
func TestNovaCheckEfficiencyCardNamesItsRules(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC.md"))
	require.NoError(t, err, "the nova-check efficiency contract is the spec's")
	spec := string(raw)
	section := novaCheckEfficiencySection(t, spec)
	// The contract is prose, so its line wrapping is the spec's; collapse runs
	// of whitespace so a phrase is checked for its words, not its column.
	section = strings.Join(strings.Fields(section), " ")
	for _, want := range []string{
		// the measurement the section publishes.
		"1,496 + 1,810",
		"71 MB",
		"0.27 s",
		// the repeat: two walks of one tree in one quickstart, at the two
		// file:line sites the section names.
		"two full walks of one tree in one quickstart",
		"the same tree, read twice",
		"internal/check/links.go:52",
		"internal/check/nocode.go:356",
		"the path list",
		// the rule: one walk per quickstart, shared by links and nocode.
		"one walk of the root per `quickstart`",
		"`links` and `nocode` consume the path list",
		// the coordinator read: capped per kind, remedy on the MORE line.
		"capped",
		"`--fail-max <n>`",
		"`--fail-max 0`",
		"MORE",
		// waits on nothing: no clock, subprocess, network or lock.
		"WAITS ON: nothing",
		"no clock, no subprocess, no network and no lock",
		"under 0.30 s",
		// the Red tests list.
		"Red tests",
		"a `quickstart` of one tree walks the root once and hands the same path list to `links` and `nocode`",
		"`--fail-max <n>` caps each kind's findings at `n`, the `MORE` line names the flag that lifts it",
	} {
		assert.Contains(t, section, want, "the SPEC.md nova-check efficiency section must name it")
	}
}

// novaCheckEfficiencySection returns the body of the `## The efficiency card,
// nova-check` section: the contract lives under that header and ends at
// the next top-level `## ` header.
func novaCheckEfficiencySection(t *testing.T, spec string) string {
	t.Helper()
	const header = "## The efficiency card, nova-check"
	start := strings.Index(spec, header)
	require.GreaterOrEqual(t, start, 0, "the spec has no %q section", header)
	rest := spec[start+len(header):]
	end := strings.Index(rest, "\n## ")
	if end < 0 {
		return rest
	}
	return rest[:end]
}
