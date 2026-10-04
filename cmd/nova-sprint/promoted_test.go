package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// promoted holds its --answers as every answering verb does (sprint.Promoted, answered): an
// answer naming no open judgment refuses the whole step and records no promotion.
func TestPromotedHoldsItsAnswers(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	code, out, errs := ta.do("promoted --sha 0123abc --answers nope~0")
	assert.Equal(t, 1, code, "%d %s", code, errs)
	assert.Contains(t, errs, "the whole step is refused and nothing was changed")
	assert.NotContains(t, out, "promoted the sprint branch")
	assert.Contains(t, ta.ok("promoted --sha 0123abc"), "promoted the sprint branch into dev")
	ta.clean()
}

// promoted --dry-run holds the sha and the seat as the real word does and records nothing:
// the verb writes, so it takes the --dry-run the onboarding standard asks.
func TestPromotedDryRunRecordsNothingAndHoldsTheSha(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	assert.Contains(t, ta.ok("promoted --sha 0123abc --dry-run"), "PROMOTED DRY-RUN sha=0123abc; nothing was changed")
	code, _, errs := ta.do("promoted --sha xyz --dry-run")
	assert.Equal(t, 2, code, "%s", errs)
	assert.Contains(t, errs, "7 to 40 hex digits")
	assert.NotContains(t, ta.ok("where"), "promoted_sha", "a dry run records no promotion")
	ta.clean()
}
