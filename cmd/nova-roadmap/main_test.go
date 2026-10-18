package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/roadmap"
	"github.com/stretchr/testify/require"
)

const fixture = "../../pkg/roadmap/testdata/features/roadmap.sexp"

type result struct {
	code           int
	stdout, stderr string
}

func run(args ...string) result {
	var out, errb bytes.Buffer
	code := novaRoadmap().Run(args, strings.NewReader(""), &out, &errb)
	return result{code, out.String(), errb.String()}
}

// The tool is held to the shape every nova command has.
func TestTheToolIsWellFormed(t *testing.T) {
	t.Parallel()
	require.Empty(t, novaRoadmap().Problems())
}

// check is the drift gate: 0 when the page on disk is what the data renders (the
// overlay beside the data applied), 1 when it is not or is missing, and it writes nothing.
func TestCheckIsTheDriftGate(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	page := filepath.Join(dir, "ROADMAP.md")
	golden, err := os.ReadFile("testdata/ROADMAP.md")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(page, golden, 0o644))
	r := run("check", "--file", fixture, "--out", page)
	require.Equal(t, 0, r.code, r.stderr)
	require.Contains(t, r.stdout, "CHECK OK kind=roadmap items=5 file="+page)
	r = run("check", "--file", fixture, "--out", fixture)
	require.Equal(t, 1, r.code)
	require.Contains(t, r.stderr, "is not what "+fixture+" renders; run: nova-roadmap render")
	r = run("check", "--file", fixture, "--out", filepath.Join(dir, "missing.md"))
	require.Equal(t, 1, r.code, "a missing page is drift")
}

// render writes the page check then passes; --dry-run writes nothing.
func TestRenderWritesWhatCheckPasses(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "ROADMAP.md")
	r := run("render", "--dry-run", "--file", fixture, "--out", out)
	require.Equal(t, 0, r.code, r.stderr)
	_, err := os.Stat(out)
	require.True(t, os.IsNotExist(err), "--dry-run wrote the page")
	require.Equal(t, 0, run("render", "--file", fixture, "--out", out).code)
	require.Equal(t, 0, run("check", "--file", fixture, "--out", out).code)
	want, err := os.ReadFile("testdata/ROADMAP.md")
	require.NoError(t, err)
	got, err := os.ReadFile(out)
	require.NoError(t, err)
	require.Equal(t, string(want), string(got))
}

// Every refusal is exit 2 and names what to fix.
func TestRefusalsExitTwo(t *testing.T) {
	t.Parallel()
	for args, want := range map[string]string{
		"check --file testdata/nope.sexp":                         "reading testdata/nope.sexp",
		"check --file " + fixture + " --evidence testdata/no-dir": "is not a directory",
		"check --file " + fixture + " --kind plans":               "--kind plans is not roadmap or fixes",
		"check --file testdata/ROADMAP.md":                        "roadmap: file=testdata/ROADMAP.md",
		"render --dry-run --file " + fixture + " --kind fixes":    "want one (fixes",
	} {
		r := run(strings.Fields(args)...)
		require.Equal(t, 2, r.code, args)
		require.Contains(t, r.stderr+r.stdout, want, args)
	}
}

// The help is usable cold: the banner names both verbs and the form topic, and
// the topic's example is real data that decodes and renders.
func TestHelpFormExampleIsRealData(t *testing.T) {
	t.Parallel()
	r := run("help")
	require.Equal(t, 0, r.code)
	for _, want := range []string{"render", "check", "form", "make roadmap-check"} {
		require.Contains(t, r.stdout, want)
	}
	r = run("help", "form")
	require.Equal(t, 0, r.code)
	start := strings.Index(r.stdout, "(:schema 1")
	end := strings.Index(r.stdout[start:], "\n\n")
	fs, err := roadmap.DecodeFeatures("help", []byte(r.stdout[start:start+end]))
	require.NoError(t, err)
	require.Contains(t, roadmap.RenderFeatures(fs, "docs/roadmap.sexp", ""),
		"| Connect tokens | ✅ |\n| IPv6 (later) | ❌ |\n| complete | 50% |\n")
}
