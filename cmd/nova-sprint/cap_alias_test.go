package main

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var opField = regexp.MustCompile(` op=\S+`)

func stripOp(s string) string { return opField.ReplaceAllString(s, "") }

// TestLimitAliasIsMax pins the cap-flag rename (docs/STANDARD.md section 2):
// --max is the count, --limit sets that same count and prints NOTE --limit is
// --max, and a verb that never had --limit still refuses it.
func TestLimitAliasIsMax(t *testing.T) {
	t.Parallel()

	ready := func(t *testing.T) *testApp {
		t.Helper()
		ta := newTestApp(t)
		ta.ok("init --readers reader-a,reader-b --members m1:8")
		ta.ok("add --stream s1 --count 3")
		ta.deal(3)
		return ta
	}
	finished := func(t *testing.T) *testApp {
		t.Helper()
		ta := ready(t)
		ta.ok("take --as m1 --max 3")
		var q struct{ Cards []queueCard }
		ta.json("queue --as m1", &q)
		var words []string
		for _, c := range q.Cards {
			words = append(words, c.ID+"@"+strconv.Itoa(c.Gen))
		}
		require.NotEmpty(t, words)
		ta.ok("finish --as m1 " + strings.Join(words, " "))
		return ta
	}

	cases := []struct {
		name  string
		setup func(t *testing.T) *testApp
		limit string
		max   string
	}{
		{"take", ready, "take --as m1 --limit 2", "take --as m1 --max 2"},
		{"ask", finished, "ask --limit 2", "ask --max 2"},
		{"read", func(t *testing.T) *testApp {
			t.Helper()
			ta := finished(t)
			ta.ok("ask --max 3")
			return ta
		}, "read --as reader-a --begin --limit 2", "read --as reader-a --begin --max 2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a := tc.setup(t)
			codeL, outL, errL := a.do(tc.limit)
			b := tc.setup(t)
			codeM, outM, errM := b.do(tc.max)
			assert.Equal(t, 0, codeL, "%s\n%s", outL, errL)
			assert.Equal(t, codeL, codeM)
			assert.Equal(t, stripOp(outL), stripOp(outM), "the two names set the same count")
			assert.Contains(t, errL, "NOTE --limit is --max\n")
			assert.NotContains(t, errM, "NOTE --limit is --max")
			assert.Equal(t, 2, strings.Count(outL, "MOVED "), "the count is the number given, not the listing default")
		})
	}

	t.Run("omitted still takes one", func(t *testing.T) {
		t.Parallel()
		ta := ready(t)
		code, out, errs := ta.do("take --as m1")
		assert.Equal(t, 0, code, "%s%s", out, errs)
		assert.Equal(t, 1, strings.Count(out, "MOVED "))
		assert.NotContains(t, errs, "NOTE --limit is --max")
	})

	t.Run("a verb without the count refuses the old name", func(t *testing.T) {
		t.Parallel()
		ta := ready(t)
		code, _, errs := ta.do("where --limit 1")
		assert.Equal(t, 2, code)
		assert.Contains(t, errs, "unknown flag --limit")
		assert.NotContains(t, errs, "NOTE --limit is --max")
	})
}
