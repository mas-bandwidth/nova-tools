package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every verb whose --dry-run is its store step planned (stepdry.go) lists the
// flag on its -h and states an effect that says --dry-run writes nothing: the
// onboarding standard's a verb that writes takes --dry-run that writes nothing
// (tool ledger X12).
func TestEveryStepDryRunVerbListsTheFlagAndItsEffect(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	var names []string
	for v := range stepDryRun {
		names = append(names, v)
	}
	slices.Sort(names)
	for _, v := range names {
		code, out, errs := ta.do(v + " -h")
		require.Equal(t, 0, code, "%s -h: %s", v, errs)
		assert.Contains(t, out, "  --dry-run  ", "%s -h lists no --dry-run", v)
		assert.Contains(t, out, "effect: local write: ", "%s -h states no write", v)
		assert.Contains(t, out, "--dry-run writes nothing", "%s -h does not say its dry run writes nothing", v)
	}
}

// --dry-run plans the step on one read and writes nothing: the reads ask would
// make are said and none is asked; the real ask then makes them. A step the
// real run refuses is refused by the dry run, at exit 1, and nothing changes.
func TestAStepDryRunPlansTheStepAndWritesNothing(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b,reader-c --members m1")
	ta.inReview(1)

	out := ta.ok("ask s1-1 --dry-run")
	assert.True(t, strings.HasPrefix(out, "ASK OK DRY-RUN changes=1 "), out)
	assert.Contains(t, out, "; nothing was written\nWOULD ", out)
	for _, r := range []string{"reader-a", "reader-b", "reader-c"} {
		assert.Empty(t, ta.askedOf(r), "a dry run asked %s", r)
	}
	ta.ok("ask s1-1")
	assert.NotEmpty(t, append(ta.askedOf("reader-a"), ta.askedOf("reader-b")...), "the real ask asks what the dry run said")

	code, out, errs := ta.do("move s9-9 --stream s2 --dry-run")
	assert.Equal(t, 1, code, "%s%s", out, errs)
	assert.Contains(t, out, "MOVE REFUSED DRY-RUN ", out)
	assert.Contains(t, out, "REFUSED s9-9: ", out)
	ta.clean()
}
