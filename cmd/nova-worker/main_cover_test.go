package main

import (
	"bytes"
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The unit tier's per-function coverage table showed two functions of
// cmd/nova-worker/main.go at 0.0%: stringListValue.Set (line 297), the repeatable
// --repo and --recipient flag's value, and ratText (line 397), a budget's decimal.
// These tests reach both through the package's own seams: Set through the native
// flag set that is its only caller, and ratText on the values cmdNative builds.
// No file, socket, clock, subprocess or store is touched.

// TestMainCoverStringListValueSet pins the repeatable flag's value: every
// occurrence appends, in order, so `--repo a --repo b` is ["a" "b"]; the value
// itself never refuses, and a bad call is refused by the flag set before any
// occurrence is recorded.
func TestMainCoverStringListValueSet(t *testing.T) {
	t.Parallel()

	t.Run("each occurrence appends", func(t *testing.T) {
		t.Parallel()
		var got []string
		s := stringListValue{&got}
		require.NoError(t, s.Set("owner/one"), "a first occurrence is accepted")
		require.NoError(t, s.Set("owner/two"), "a second occurrence is accepted")
		assert.Equal(t, []string{"owner/one", "owner/two"}, got, "each occurrence appends, in order")
		assert.Equal(t, "owner/one,owner/two", s.String(), "the value's spelling joins the occurrences")
	})

	t.Run("through the native flag set", func(t *testing.T) {
		t.Parallel()
		f, nf := nativeFlagSet()
		var stderr bytes.Buffer
		require.True(t, f.parse([]string{"--repo", "a/one", "--repo", "a/two", "--recipient", "lane"}, &stderr),
			"the flag set accepts repeated --repo and --recipient: %s", stderr.String())
		assert.Equal(t, []string{"a/one", "a/two"}, nf.repos, "the run's repos, one entry per occurrence")
		assert.Equal(t, []string{"lane"}, nf.recipients, "the run's recipients, one entry per occurrence")
	})

	t.Run("the value never refuses", func(t *testing.T) {
		t.Parallel()
		var got []string
		s := stringListValue{&got}
		require.NoError(t, s.Set(""), "the value itself takes every string, empty too; the refusals are the flag set's")
		assert.Equal(t, []string{""}, got, "an empty occurrence is still recorded")
		var stderr bytes.Buffer
		f, _ := nativeFlagSet()
		assert.False(t, f.parse([]string{"--repo", "a/one", "--no-such-flag"}, &stderr),
			"an unknown flag is refused by the flag set, before any code here runs")
		assert.Contains(t, stderr.String(), "REFUSED", "the refusal is one line in the tool's grammar")
	})
}

// TestMainCoverRatText pins a budget's decimal: the dollar budget cmdNative parses
// prints exactly, with no trailing zero after the point, and a run that named no
// dollar budget prints nothing.
func TestMainCoverRatText(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		r    *big.Rat
		want string
	}{
		"none":          {r: nil, want: ""},
		"half":          {r: big.NewRat(1, 2), want: "0.5"},
		"whole":         {r: big.NewRat(2, 1), want: "2"},
		"dollars-cents": {r: big.NewRat(124, 100), want: "1.24"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, ratText(tc.r), "the budget's decimal, exactly")
		})
	}
}
