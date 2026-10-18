package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/roadmap"
	"github.com/stretchr/testify/require"
)

const fixture = "../../pkg/roadmap/testdata/features/roadmap.sexp"

func runRoadmap(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return code, out.String(), errb.String()
}

// --check is the drift gate: 0 when the page on disk is what the data renders
// (the overlay beside the data applied), 1 when it is not, and it writes nothing.
func TestCheckIsTheDriftGate(t *testing.T) {
	t.Parallel()
	code, out, _ := runRoadmap("--check", "--file", fixture, "--out", "testdata/ROADMAP.md")
	require.Equal(t, 0, code)
	require.Contains(t, out, "roadmap OK items=5 file=testdata/ROADMAP.md up to date")
	code, _, errs := runRoadmap("--check", "--file", fixture, "--out", fixture)
	require.Equal(t, 1, code)
	require.Contains(t, errs, "roadmap DRIFT: "+fixture+" is not what "+fixture+" renders; run: make roadmap")
	code, _, errs = runRoadmap("--check", "--file", fixture, "--out", "testdata/missing.md")
	require.Equal(t, 1, code, "a missing page is drift")
	require.Contains(t, errs, "DRIFT")
}

// A refusal is exit 2 with one line naming what to fix.
func TestRefusalsExitTwo(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"--check", "--file", "testdata/nope.sexp"},
		{"--check", "--file", fixture, "--evidence", "testdata/no-such-dir"},
		{"--check", "--file", fixture, "stray"},
		{"--check", "--kind", "plans", "--file", fixture},
	} {
		code, _, errs := runRoadmap(args...)
		require.Equal(t, 2, code, args)
		require.True(t, strings.HasPrefix(errs, "roadmap REFUSED: "), errs)
		require.Equal(t, 1, strings.Count(errs, "\n"), "one line: %q", errs)
	}
}

// The help is usable cold: it names both forms and the gate, and its feature-form
// example is real data that decodes and renders.
func TestHelpExampleIsRealData(t *testing.T) {
	t.Parallel()
	code, _, help := runRoadmap("--help")
	require.Equal(t, 0, code)
	for _, want := range []string{"--check", "exit 1 when the page", ":later t", "roadmap-evidence", "(roadmap \"v1\""} {
		require.Contains(t, help, want)
	}
	start := strings.Index(usage, "(:schema 1")
	end := strings.Index(usage[start:], "\n\n")
	var lines []string
	for _, l := range strings.Split(usage[start:start+end], "\n") {
		if i := strings.Index(l, ";"); i >= 0 {
			l = l[:i]
		}
		lines = append(lines, l)
	}
	fs, err := roadmap.DecodeFeatures("help", []byte(strings.Join(lines, "\n")))
	require.NoError(t, err)
	page := roadmap.RenderFeatures(fs, "docs/roadmap.sexp", "")
	require.Contains(t, page, "| Connect tokens | ✅ |\n| IPv6 (later) | ❌ |\n| complete | 50% |\n")
}
