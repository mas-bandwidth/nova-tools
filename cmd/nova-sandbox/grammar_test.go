package main

// The PROBE REFUSED reason set is the spec's, read off the spec, never copied into a test.
//
// nova-tools #119 item 1, from the Fable read of #108: `probeRefusalReasons` in main_test.go
// was a hand copy of the output grammar with a comment saying it was "copied verbatim", and
// nothing read the line it was copied from. An edit to docs/SPEC-SANDBOX.md's grammar would
// leave the copy green, and the tool and the spec would be silently apart -- the exact
// disagreement the comment says it guards. The repository already answers this two ways:
// cmd/nova-tokens/grammar_test.go reads docs/SPEC-TOKENS.md's fenced grammar block, and
// internal/swarm/grammar_run_test.go reads SPEC-SWARM's. This is the same pattern for the
// third spec that publishes a fixed reason set.
//
// Both tests here read the FENCED block, not the prose: a spec sentence that happens to
// mention a reason token cannot satisfy either one, and the grammar a caller's parser
// stands on is the block.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
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
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC-SANDBOX.md"))
	if err != nil {
		t.Fatalf("the grammar is the spec's, so the spec has to be readable: %s", err)
	}
	return string(raw)
}

// specProbeRefusalReasons returns the reason alternatives of the spec's own PROBE REFUSED
// grammar line, and fails if there is not exactly one such line inside a fenced block: two
// would mean two contracts, none would mean this test was asserting nothing and passing.
func specProbeRefusalReasons(t *testing.T) map[string]bool {
	t.Helper()
	var found []string
	inFence := false
	for _, line := range strings.Split(specSandbox(t), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if inFence && strings.HasPrefix(line, probeRefusedGrammarPrefix) {
			found = append(found, line)
		}
	}
	if len(found) != 1 {
		t.Fatalf("docs/SPEC-SANDBOX.md has %d fenced lines beginning %q, want exactly 1; this test would otherwise pass by reading nothing:\n%s",
			len(found), probeRefusedGrammarPrefix, strings.Join(found, "\n"))
	}
	alts, _, ok := strings.Cut(strings.TrimPrefix(found[0], probeRefusedGrammarPrefix), ">")
	if !ok {
		t.Fatalf("the grammar's PROBE REFUSED alternatives are not closed by `>`: %q", found[0])
	}
	set := map[string]bool{}
	for _, word := range strings.Split(alts, "|") {
		word = strings.TrimSpace(word)
		if word == "" {
			t.Fatalf("the grammar's PROBE REFUSED alternatives hold an empty word: %q", found[0])
		}
		set[word] = true
	}
	return set
}

func sorted(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// The set the tests police is the set the spec publishes, both directions: a reason added
// to the spec that no test knows about, and a reason the tests still allow after the spec
// dropped it, are the same defect from either end.
func TestProbeRefusalReasonsAreTheSpecsOwnSet(t *testing.T) {
	spec := specProbeRefusalReasons(t)
	if got, want := sorted(probeRefusalReasons), sorted(spec); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("the PROBE REFUSED reason set in these tests is not docs/SPEC-SANDBOX.md's:\n  tests: %s\n  spec:  %s\nthe spec's grammar block is the contract; update the tests to it (or the spec, if the tool changed)",
			strings.Join(got, "|"), strings.Join(want, "|"))
	}
}

// #119 item 2. The internal verb prints a SEVENTH reason, and leaving it out of the six is
// right -- no caller runs the verb, so no parser stands on that token -- but the grammar
// line then does not describe every `PROBE REFUSED` the binary can print, and the spec's
// internal-verb section named the guard's three mechanisms without ever naming the refusal
// the guard prints. This asserts all three halves: the binary prints it, the grammar set
// does not hold it, and the internal-verb section says so in words.
func TestTheInternalVerbsRefusalIsOutsideTheSetAndNamedInTheSpec(t *testing.T) {
	// The binary, first: a test that the spec describes a line the tool never prints is a
	// test pointed at nothing.
	j := newJob(t)
	code, _, errOut := j.tool(t, j.env(), "probe-step")
	if code != 2 || !strings.Contains(errOut, "reason="+probeStepRefusalReason) {
		t.Fatalf("`probe-step` by hand does not refuse with reason=%s: exit %d, stderr %q", probeStepRefusalReason, code, errOut)
	}

	if specProbeRefusalReasons(t)[probeStepRefusalReason] {
		t.Errorf("%s is inside the spec's PROBE REFUSED set; it is the internal verb's own refusal and the set is the contract a caller's parser stands on -- if it genuinely joined the set, probeRefusalReasons and this test's premise both change", probeStepRefusalReason)
	}

	// And the section that describes the verb has to name what it prints. Bounded to that
	// section on purpose: the token appearing anywhere in the file would be satisfied by a
	// changelog line, and a reader looking for this refusal reads the verb's section.
	const heading = "### The internal verb, and what its guard is and is not"
	spec := specSandbox(t)
	_, after, ok := strings.Cut(spec, heading)
	if !ok {
		t.Fatalf("docs/SPEC-SANDBOX.md has no %q section; this test cannot say where the refusal belongs", heading)
	}
	if next := strings.Index(after, "\n## "); next >= 0 {
		after = after[:next]
	}
	if !strings.Contains(after, probeStepRefusalReason) {
		t.Errorf("the internal verb's section does not name `PROBE REFUSED reason=%s`, the refusal its guard prints; the grammar above is fixed at six, so a reader has nowhere to learn that this seventh token exists", probeStepRefusalReason)
	}
}
