package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// SPEC CARD #280 (docs/SPEC-SWARM-PROFILES.md, Explicit reasoning effort and
// variant selection): the profile contract names its rules for typed,
// provider-supported effort/variant selection, requested-versus-resolved
// provenance, the unsupported/unknown/disabled distinction, the shared
// accounting vocabulary and the post-adoption measurement protocol. This doc
// test reads the section out of the spec the way
// TestBenchSlotLeasesSectionNamesItsRules reads SPEC-SWARM.md: the spec is the
// one place the contract is written, so a rule renamed out of the doc is red.
func TestProfilesNameReasoningEffortAndVariantRules(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC-SWARM-PROFILES.md"))
	if err != nil {
		t.Fatalf("the reasoning-effort contract is the profile spec's: %s", err)
	}
	spec := string(raw)
	section := reasoningEffortSection(t, spec)
	for _, want := range []string{
		"typed",
		"provider-supported",
		"reasoning effort",
		"variant",
		"requested",
		"resolved",
		"provenance",
		"model defaults",
		"refuse before",
		"unsupported",
		"unknown",
		"disabled",
		"no silent coercion",
		"nova-local",
		"one-shot",
		"synthetic credentials",
		"budget overshoot",
		"raw native counters",
		"equal quality",
		"one report is insufficient",
		// the Red tests list.
		"Red tests",
		"an effort the model has no variant for refuses before any provider call",
		"requested and resolved settings are both retained",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("SPEC-SWARM-PROFILES.md Explicit reasoning effort and variant selection names %q; the section holds:\n%s", want, section)
		}
	}
}

// reasoningEffortSection returns the body of the `## Explicit reasoning effort
// and variant selection` section: the contract lives under that header and ends
// at the next top-level `## ` header.
func reasoningEffortSection(t *testing.T, spec string) string {
	t.Helper()
	const header = "## Explicit reasoning effort and variant selection"
	start := strings.Index(spec, header)
	if start < 0 {
		t.Fatalf("the spec has no %q section", header)
		return ""
	}
	rest := spec[start+len(header):]
	end := strings.Index(rest, "\n## ")
	if end < 0 {
		return rest
	}
	return rest[:end]
}
