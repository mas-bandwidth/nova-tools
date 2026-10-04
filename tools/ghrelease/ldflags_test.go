package main

import (
	"strings"
	"testing"
)

// The ldflags verb, pinned by what the release-checks job asserted of the shell
// helper it replaces: the empty stamp is refused, the composed flag carries the
// stamp, the three alphabet refusals each say their own reason and stop a
// caller, a legal tag is never refused, and a wrong number of arguments is a
// usage error.

func TestLdflagsRefusesAnEmptyStamp(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.wantRC(h.do("ldflags", ""), 1)
	h.mustContain("refusing: the release stamp is empty")
	if h.out.Len() != 0 {
		t.Fatalf("a refusal printed a flag on stdout: %q", h.out.String())
	}
}

func TestLdflagsComposesTheStamp(t *testing.T) {
	t.Parallel()
	for _, stamp := range []string{"v0.13.0", "v0.13.0-rc1", "v0.0.0-dry-run"} {
		h := newHarness(t)
		h.wantRC(h.do("ldflags", stamp), 0)
		if got, want := h.out.String(), "-s -w -X main.version="+stamp+"\n"; got != want {
			t.Errorf("<%s> composed %q, want %q", stamp, got, want)
		}
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
		if !strings.Contains(h.errb.String(), c.phrase) {
			t.Errorf("<%q> was refused, but not for %s:\n%s", c.stamp, c.phrase, h.errb.String())
		}
		if h.out.Len() != 0 {
			t.Errorf("<%q> printed a flag on stdout: %q", c.stamp, h.out.String())
		}
	}
}

func TestLdflagsWrongArgumentCountIsAUsageError(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"ldflags"}, {"ldflags", "v1", "extra"}} {
		h := newHarness(t)
		h.wantRC(h.do(args...), 2)
		h.mustContain("usage:")
	}
}

func TestComposedLdflagsAreCheckedNotJustTheirInput(t *testing.T) {
	t.Parallel()
	// The belt-and-braces check on the composed string: no stamp reaches it
	// empty, so it is exercised through the function. A composed flag always
	// carries a non-empty -X main.version.
	flags, refusal := composeLdflags("v1")
	if refusal != nil || !strings.HasSuffix(flags, "-X main.version=v1") {
		t.Fatalf("composeLdflags(v1) = %q, %v", flags, refusal)
	}
}
