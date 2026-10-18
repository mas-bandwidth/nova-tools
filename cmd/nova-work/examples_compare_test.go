package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/onboarding"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// offlineExamples are the banner's example lines that need no GitHub (the
// example fixture and the roadmap verbs), each with what it prints, run in one
// sitting through the comparator (SPEC-TOOLWORK.md documents rule 6). The
// GitHub lines are TestTESTSFirstRunIsWhatTheToolPrints's. Each roadmap line
// runs against a work record of the test's own, named by --repo.
var offlineExamples = []struct {
	line string
	want []string
}{
	{"nova-work example --dir ./example", []string{
		"EXAMPLE OK dir=./example",
		"EXAMPLE WROTE file=./example/tree.lisp",
		"EXAMPLE WROTE file=./example/against.lisp",
		"EXAMPLE WROTE file=./example/a.lisp",
		"EXAMPLE WROTE file=./example/b.lisp",
		"EXAMPLE WROTE file=./example/call-01.json",
		"EXAMPLE WROTE file=./example/call-02.json",
		"EXAMPLE WROTE file=./example/calls/call-01.json",
		"EXAMPLE WROTE file=./example/calls/call-02.json",
	}},
	{"nova-work roadmap check", []string{"ROADMAP-CHECK OK repo=./repo entries=4"}},
	{"nova-work roadmap list --release v1.2.6 --state planned", []string{
		"ROADMAP-LIST OK repo=./repo entries=2",
		"ROADMAP-LIST ENTRY id=my-old-fix file=docs/fixes.sexp kind=fix place=v1.2.6 state=planned title=F1",
		"ROADMAP-LIST ENTRY id=f2 file=docs/fixes.sexp kind=fix place=v1.2.6 state=planned title=F2",
	}},
	{`nova-work roadmap add --id my-fix --release v1.2.6 --title "What the fix does" --origin "PR #12"`, wrote("ROADMAP-ADD OK repo=./repo id=my-fix place=v1.2.6", "ROADMAP-ADD", "docs/fixes.sexp", "FIXES.md")},
	{"nova-work roadmap remove --id my-old-fix", wrote("ROADMAP-REMOVE OK repo=./repo removed=1", "ROADMAP-REMOVE", "docs/fixes.sexp", "FIXES.md")},
	{"nova-work roadmap pull --id my-item --release v1.2.6", wrote("ROADMAP-PULL OK repo=./repo moved=1 place=v1.2.6", "ROADMAP-PULL", "docs/roadmap.sexp", "ROADMAP.md", "docs/fixes.sexp", "FIXES.md")},
	{`nova-work roadmap done --id my-fix --evidence "PR #12"`, wrote(`ROADMAP-DONE OK repo=./repo done=1 evidence=PR\x20#12`, "ROADMAP-DONE", "docs/fixes.sexp", "FIXES.md")},
	{`nova-work roadmap note --id my-fix --text "Needs the store migration first."`, wrote("ROADMAP-NOTE OK repo=./repo noted=1", "ROADMAP-NOTE", "docs/fixes.sexp", "FIXES.md")},
	{"nova-work roadmap render", []string{"ROADMAP-RENDER OK repo=./repo changed=0"}},
}

// wrote is a writing verb's lines: its result, then one WROTE line per file.
func wrote(result, verb string, files ...string) []string {
	lines := []string{result}
	for _, f := range files {
		lines = append(lines, verb+" WROTE file="+f)
	}
	return lines
}

// TestOfflineExamplesThroughTheComparator runs every banner example that needs
// no GitHub and compares what each prints with the lines above.
func TestOfflineExamplesThroughTheComparator(t *testing.T) {
	t.Parallel()

	all, err := onboarding.ExampleLines(workTool(realGitHub()).Banner(), "nova-work")
	require.NoError(t, err)
	var banner, documented []string
	for _, e := range all {
		if strings.HasPrefix(e, "nova-work roadmap ") || strings.HasPrefix(e, "nova-work example ") {
			banner = append(banner, e)
		}
	}
	for _, e := range offlineExamples {
		documented = append(documented, e.line)
	}
	require.Equal(t, documented, banner, "the banner's offline examples are one list with this test's")

	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "docs"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "docs/roadmap.sexp"), []byte(strings.NewReplacer(`"i1"`, `"my-item"`).Replace(testRoadmap)), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "docs/fixes.sexp"), []byte(strings.NewReplacer(`"v1.0.1"`, `"v1.2.6"`, `"f1"`, `"my-old-fix"`).Replace(testFixes)), 0o644))
	pages := workMain(unreachable(t)).Run("roadmap", "render", "--repo", repo)
	require.Equal(t, 0, pages.Code, pages.Stdout+pages.Stderr)

	steps := make([]onboarding.Step, 0, len(offlineExamples))
	got := make([]onboarding.Result, 0, len(offlineExamples))
	for _, e := range offlineExamples {
		parsed, err := onboarding.Steps("nova-work", []string{"$ " + e.line})
		require.NoError(t, err, e.line)
		require.Len(t, parsed, 1, e.line)
		s := parsed[0]
		s.Want = e.want
		steps = append(steps, s)
		args := make([]string, 0, len(s.Args)+2)
		for _, a := range s.Args {
			args = append(args, strings.Replace(a, "./", dir+"/", 1))
		}
		if s.Args[0] == "roadmap" {
			args = append(args, "--repo", repo)
		}
		res := workMain(unreachable(t)).Run(args...)
		assert.Equal(t, 0, res.Code, "%s\n%s%s", e.line, res.Stdout, res.Stderr)
		got = append(got, onboarding.Result{Code: res.Code, Stdout: res.Stdout, Stderr: res.Stderr})
	}
	assert.Empty(t, onboarding.CompareTranscript(steps, got, []onboarding.Field{{Name: "tmpdir", Doc: ".", Run: dir}}))
}
