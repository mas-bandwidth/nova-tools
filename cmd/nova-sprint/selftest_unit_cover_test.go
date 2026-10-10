package main

// selftest_unit_cover_test.go is the unit tier's cover of the selftest verb's
// three string readers and its refusals (selftest.go): the whole flow of
// cmdSelftest runs git and go as children (selftestFlow, selftest.go:136) and is
// pinned end to end by TestSelftestLandsOneCardThroughTheTreeGate, so these
// tests take the pure readers alone and the refusals that return before the
// flow starts.

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNovaSprintSelftestCoverStepWhy pins selftestStepWhy (selftest.go:269):
// the first non-blank line of stderr, else of stdout, else the fallback.
func TestNovaSprintSelftestCoverStepWhy(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name      string
		out, errs string
		want      string
	}{
		{"stderr wins when both are set", "stdout first\nstdout second", "stderr first\nstderr second", "stderr first"},
		{"blank lines and tab-only lines are skipped", "\n\t\t\n\t\nreal line\nmore", "", "real line"},
		{"stderr empty, stdout's first line", "\n\nstdout first\nstdout second", "", "stdout first"},
		{"both empty, the fallback text", "", "", "it failed and said nothing"},
	} {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, c.want, selftestStepWhy(c.out, c.errs))
		})
	}
}

// TestNovaSprintSelftestCoverLandWhy pins selftestLandWhy (selftest.go:283):
// the reason= of a LAND REFUSED or LAND FAILED line, else selftestStepWhy.
func TestNovaSprintSelftestCoverLandWhy(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name      string
		out, errs string
		want      string
	}{
		{"a LAND REFUSED reason in stdout", "LAND REFUSED cards=3 reason=the base is red\n", "", "the base is red"},
		{"a LAND FAILED reason in stderr", "", "LAND FAILED step=land reason=go build ./... failed\n", "go build ./... failed"},
		{"reason= with no LAND prefix is not taken, the first line", "REFUSED reason=x\nsecond line\n", "", "REFUSED reason=x"},
		{"no such line, the first thing it said", "first thing it said\nsecond line\n", "", "first thing it said"},
	} {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, c.want, selftestLandWhy(c.out, c.errs))
		})
	}
}

// TestNovaSprintSelftestCoverLanded pins selftestLanded (selftest.go:297): the
// cards= of a LAND OK line, and 0 when there is none or it is not a number.
func TestNovaSprintSelftestCoverLanded(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		out  string
		want int
	}{
		{"a LAND OK line's cards", "LAND OK cards=3 landed=1\n", 3},
		{"cards= that is not a number is not taken", "LAND OK cards=x landed=1\n", 0},
		{"no LAND OK line", "LAND REFUSED cards=3 reason=x\n", 0},
		{"a LAND OK line among others", "noise\nLAND OK cards=2 landed=1\nmore\n", 2},
	} {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, c.want, selftestLanded(c.out))
		})
	}
}

// TestNovaSprintSelftestCoverRefusals pins the three refusals cmdSelftest makes
// before it runs anything (selftest.go:72-86): a bad flag, a positional word and
// a --dir under which no directory can be made. Each is a nonzero exit with a
// refusal on stderr naming selftest and nothing on stdout.
func TestNovaSprintSelftestCoverRefusals(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "regular-file")
	require.NoError(t, os.WriteFile(file, []byte("not a directory\n"), 0o600))
	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{"an unknown flag", []string{"--nope"}, "unknown flag --nope"},
		{"a word after the flags", []string{"--keep", "now"}, "takes no words, found now"},
		{"a --dir that is a regular file", []string{"--dir", file}, "the fresh directory could not be made"},
	} {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			ta := newTestApp(t)
			var out, errs bytes.Buffer
			code := ta.a.cmdSelftest(c.args, &out, &errs)
			assert.NotZero(t, code, "a refusal is a nonzero exit")
			assert.Contains(t, errs.String(), "selftest", "the refusal names the verb: %s", errs.String())
			assert.Contains(t, errs.String(), c.want, "the refusal's reason: %s", errs.String())
			assert.Empty(t, out.String(), "a refusal writes nothing to stdout")
		})
	}
}
