package main

// The PROBE REFUSED reason set is the spec's, read off the spec, never copied into a test.
//
// nova-tools #119 item 1, from the Fable read of #108: `probeRefusalReasons` in main_test.go
// was a hand copy of the output grammar ("copied verbatim") that nothing read back, so an
// edit to docs/SPEC-SANDBOX.md's grammar would leave it green with the tool and the spec
// silently apart. This is the pattern of cmd/nova-tokens/grammar_test.go (SPEC-TOKENS) and
// internal/swarm/grammar_run_test.go (SPEC-SWARM), for the third spec with a fixed reason
// set. Both tests read the FENCED block, not the prose: a spec sentence that mentions a
// reason token cannot satisfy either, and the block is what a caller's parser stands on.

import (
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// probeRefusedGrammarPrefix is how the grammar writes the line whose alternatives are the
// published set. The `<` is part of the prefix so that a prose line quoting a concrete
// refusal (`PROBE REFUSED reason=check: ...`) is not mistaken for the grammar.
const probeRefusedGrammarPrefix = "PROBE REFUSED reason=<"

// probeStepRefusalReason is the one reason token the binary prints that the grammar set
// does not hold -- the internal verb's guard (see main.go's probe-step refusals). It is
// deliberately outside the six, and #119 item 2 is that the spec has to SAY so.
const probeStepRefusalReason = "probe_step_not_a_child"

func specSandbox(t *testing.T) string {
	t.Helper()
	return testkit.ReadFile(t, filepath.Join("..", "..", "docs", "SPEC-SANDBOX.md"))
}

// specFenced is every line of the spec inside a fenced block that begins with prefix.
func specFenced(t *testing.T, prefix string) []string {
	t.Helper()
	var found []string
	inFence := false
	for _, line := range strings.Split(specSandbox(t), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
		} else if inFence && strings.HasPrefix(line, prefix) {
			found = append(found, line)
		}
	}
	return found
}

// specProbeRefusalReasons returns the reason alternatives of the spec's own PROBE REFUSED
// grammar line, and fails if there is not exactly one such line inside a fenced block: two
// would mean two contracts, none would mean this test was asserting nothing and passing.
func specProbeRefusalReasons(t *testing.T) map[string]bool {
	t.Helper()
	found := specFenced(t, probeRefusedGrammarPrefix)
	require.Len(t, found, 1, "docs/SPEC-SANDBOX.md's fenced lines beginning %q, want exactly 1", probeRefusedGrammarPrefix)
	alts, _, ok := strings.Cut(strings.TrimPrefix(found[0], probeRefusedGrammarPrefix), ">")
	require.True(t, ok, "the grammar's PROBE REFUSED alternatives are not closed by `>`: %q", found[0])
	set := map[string]bool{}
	for _, word := range strings.Split(alts, "|") {
		word = strings.TrimSpace(word)
		require.NotEmpty(t, word, "the grammar's PROBE REFUSED alternatives hold an empty word: %q", found[0])
		set[word] = true
	}
	return set
}

// The set the tests police is the set the spec publishes, both directions: a reason added
// to the spec that no test knows about, and a reason the tests still allow after the spec
// dropped it, are the same defect from either end. The spec's grammar block is the
// contract; update the tests to it (or the spec, if the tool changed).
func TestProbeRefusalReasonsAreTheSpecsOwnSet(t *testing.T) {
	t.Parallel()
	assert.Equal(t, slices.Sorted(maps.Keys(specProbeRefusalReasons(t))), slices.Sorted(maps.Keys(probeRefusalReasons)), "the PROBE REFUSED reason set in these tests is not docs/SPEC-SANDBOX.md's")
}

// #119 item 2. The internal verb prints a SEVENTH reason, rightly outside the six (no
// caller runs the verb, so no parser stands on it), but the spec's internal-verb section
// never named the refusal its guard prints. All three halves: the binary prints it (or the
// test points at nothing), the grammar set does not hold it, and the section says so.
func TestTheInternalVerbsRefusalIsOutsideTheSetAndNamedInTheSpec(t *testing.T) {
	t.Parallel()
	newJob(t).run(t, "probe-step").ExitErr(2, "reason="+probeStepRefusalReason)

	assert.False(t, specProbeRefusalReasons(t)[probeStepRefusalReason], "%s is inside the spec's PROBE REFUSED set; it is the internal verb's own refusal and the set is the contract a caller's parser stands on -- if it genuinely joined the set, probeRefusalReasons and this test's premise both change", probeStepRefusalReason)

	// Bounded to the verb's own section (anywhere in the file, a changelog line would do),
	// ending at the next `### ` or `## `. Cutting only at `## ` was the read's LOW: measured,
	// with the paragraph moved into a sibling `###` section that cut stayed green and this
	// one goes red.
	const heading = "### The internal verb, and what its guard is and is not"
	_, after, ok := strings.Cut(specSandbox(t), heading)
	require.True(t, ok, "docs/SPEC-SANDBOX.md has no %q section; this test cannot say where the refusal belongs", heading)
	for _, next := range []string{"\n### ", "\n## "} {
		if i := strings.Index(after, next); i >= 0 {
			after = after[:i]
		}
	}
	assert.Contains(t, after, probeStepRefusalReason, "the internal verb's section does not name `PROBE REFUSED reason=%s`, the refusal its guard prints; the grammar is fixed at six, so a reader has nowhere to learn that this seventh token exists", probeStepRefusalReason)
}
