package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The tier a card is tiered with is written on the card's own line and never on a header: a
// brief whose first line is a store or card header (PATHS:, REPO:, BASE:, TEST:, ...) is
// given back as written, so the header line is not corrupted (brief_tier.go; the review of
// attempt 3: a leading PATHS: line was stamped with " tier: frontier", corrupting the path
// list the card's readers read as written).
func TestTieredBriefNeverStampsAHeaderLine(t *testing.T) {
	t.Parallel()
	for _, first := range []string{
		"PATHS: tla/Lease.tla,internal/x/*.go",
		"REPO: mas-bandwidth/nova-tools",
		"BASE: sprint/mechanical-2026-10-02",
		"STATUS: nova-sprint card c",
		"TEST: ./internal/x TestX",
		"KIND: fix",
		"SHARED: docs/SPEC-SPRINT.md",
		"DEPENDS-ON: s1-1",
		"START: internal/x",
		"STOP: TestX is green",
		"model: frontier/x",
		"tokens: unmetered",
		"deadline: 60",
	} {
		brief := first + "\nREPO: mas-bandwidth/nova-tools\n\nThe task."
		assert.Equal(t, brief, tieredBrief(brief, "frontier"), "the header line %q is stamped", first)
	}
}

// The card's own line is stamped: its RESULT line when the brief carries one, else its
// title (the card decides its model; docs/SPEC-SPRINT.md).
func TestTieredBriefStampsTheResultLineElseTheTitle(t *testing.T) {
	t.Parallel()
	result := "RESULT: c sha=0123456789ab\nREPO: mas-bandwidth/nova-tools\n\nThe task."
	assert.Equal(t,
		"RESULT: c sha=0123456789ab tier: frontier\nREPO: mas-bandwidth/nova-tools\n\nThe task.",
		tieredBrief(result, "frontier"))
	title := "c: a lease model\nREPO: mas-bandwidth/nova-tools\n\nThe task."
	assert.Equal(t,
		"c: a lease model tier: frontier\nREPO: mas-bandwidth/nova-tools\n\nThe task.",
		tieredBrief(title, "frontier"))
}

// A model brief whose first line is a header carries no card's own line, so the tier writer
// leaves it as written and says nothing: the missing tier is the card checks' to refuse, and
// the PATHS line is never rewritten (brief_tier.go).
func TestAModelBriefWithALeadingHeaderIsGivenBackAsItIs(t *testing.T) {
	t.Parallel()
	brief := "PATHS: tla/Lease.tla,internal/x/*.go\n" +
		"REPO: mas-bandwidth/nova-tools\n" +
		"BASE: sprint/mechanical-2026-10-02\n" +
		"TEST: ./internal/x TestX\n" +
		"\nTHE TASK. Fix the lease model."
	out, said, why := ModelTier(brief)
	assert.Equal(t, brief, out, "the leading PATHS line is not rewritten")
	assert.Empty(t, said)
	assert.Empty(t, why)
}
