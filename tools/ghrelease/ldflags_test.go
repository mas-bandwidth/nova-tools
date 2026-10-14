package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The ldflags verb, pinned by what the release-checks job asserted of the shell
// helper it replaces: the empty stamp is refused, the composed flag carries the
// stamp, the three alphabet refusals each say their own reason and stop a
// caller, a legal tag is never refused, and a wrong number of arguments is a
// usage error.

func TestLdflagsRefusesAnEmptyStamp(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.wantRun(1, "refusing: the release stamp is empty", "ldflags", "")
	require.Zero(t, h.out.Len(), "a refusal printed a flag on stdout: %q", h.out.String())
}

func TestLdflagsComposesTheStamp(t *testing.T) {
	t.Parallel()
	for _, stamp := range []string{"v0.13.0", "v0.13.0-rc1", "v0.0.0-dry-run"} {
		h := newHarness(t)
		h.wantRC(h.do("ldflags", stamp), 0)
		assert.Equal(t, "-s -w -X main.version="+stamp+"\n", h.out.String(), "<%s> composed flag", stamp)
	}
}

func TestLdflagsRefusesTheTagAlphabetEachByItsOwnReason(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ stamp, phrase string }{
		{"v1.0%s-rc1", "contains %"},
		{"v1.0%d", "contains %"},
		{"v1.0=rc1", "contains ="},
		{"v0.13.0 rc1", "carries whitespace"},
		{"v0.13.0\trc1", "carries whitespace"},
		{"v0.13.0\nrc1", "carries whitespace"},
	} {
		h := newHarness(t)
		h.wantRC(h.do("ldflags", c.stamp), 1)
		assert.Contains(t, h.errb.String(), c.phrase, "<%q> was refused, but not for %s", c.stamp, c.phrase)
		assert.Zero(t, h.out.Len(), "<%q> printed a flag on stdout", c.stamp)
	}
}

func TestLdflagsWrongArgumentCountIsAUsageError(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"ldflags"}, {"ldflags", "v1", "extra"}} {
		newHarness(t).wantRun(2, "usage:", args...)
	}
}

func TestComposedLdflagsAreCheckedNotJustTheirInput(t *testing.T) {
	t.Parallel()
	// The belt-and-braces check on the composed string: no stamp reaches it
	// empty, so it is exercised through the function. A composed flag always
	// carries a non-empty -X main.version.
	flags, refusal := composeLdflags("v1")
	require.Empty(t, refusal)
	assert.True(t, strings.HasSuffix(flags, "-X main.version=v1"), "composeLdflags(v1) = %q", flags)
}
