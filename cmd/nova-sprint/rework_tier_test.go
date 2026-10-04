package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rework --tier (nova-tools#5090): the tier is checked before any store is
// written, every problem of the call named at once; a known tier is kept on the
// card, and the REWORK line says it.
func TestReworkTakesATier(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 1")
	ta.deal(1)
	ta.failOnce("m1", "s1-1.w1@1", "tests red")
	writes := ta.applies()
	code, _, errs := ta.do("rework --tier medium")
	require.Equal(t, 2, code, "a bad tier is usage: %s", errs)
	assert.Contains(t, errs, "wants ids")
	assert.Contains(t, errs, "--tier wants frontier, pro or flash, found medium")
	assert.Equal(t, writes, ta.applies(), "a refused rework wrote")
	out := ta.ok("rework s1-1 --fix again --tier pro")
	assert.Contains(t, out, "tier pro")
	assert.Contains(t, ta.ok("card s1-1 --fields"), "tier=pro", "the card keeps its tier")
}
