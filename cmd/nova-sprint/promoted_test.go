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
