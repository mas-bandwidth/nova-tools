package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/onboarding"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// workRecord is an invented repository work record: one group with two items,
// one planned release with two fixes. The pages are written by render.
const (
	testRoadmap = `(roadmap "v1"
 :title "T"
 :text "Roadmap."
 :groups ((group "g" :title "G" :text "Group."))
 :items
 ((item "i1" :group "g" :date "2026-10-01" :title "I1" :text "One.")
  (item "i2" :group "g" :date "2026-10-01" :title "I2" :text "Two.")))
`
	testFixes = `(fixes "v1"
 :title "F"
 :text "Fixes."
 :releases ((release "v1.0.1" :status "planned" :text "Next."))
 :items
 ((fix "f1" :release "v1.0.1" :status "planned" :title "F1" :origin "PR #1")
  (fix "f2" :release "v1.0.1" :status "planned" :title "F2" :origin "PR #2")))
`
)

func workRecord(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "docs"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "docs/roadmap.sexp"), []byte(testRoadmap), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "docs/fixes.sexp"), []byte(testFixes), 0o644))
	res := workMain(unreachable(t)).Run("roadmap", "render", "--repo", dir)
	require.Equal(t, 0, res.Code, "%s%s", res.Stdout, res.Stderr)
	return dir
}

func TestRoadmapVerbsEndToEnd(t *testing.T) {
	t.Parallel()
	dir := workRecord(t)
	run := func(want int, args ...string) string {
		t.Helper()
		res := workMain(unreachable(t)).Run(append(args, "--repo", dir)...)
		require.Equal(t, want, res.Code, "%v\n%s%s", args, res.Stdout, res.Stderr)
		return res.Stdout + res.Stderr
	}
	out := run(0, "roadmap", "check")
	assert.Contains(t, out, "ROADMAP-CHECK OK")
	assert.Contains(t, out, "entries=4")

	out = run(0, "roadmap", "add", "--id", "f3", "--release", "v1.0.1", "--title", "F3", "--origin", "PR #3")
	assert.Contains(t, out, "ROADMAP-ADD OK")
	assert.Contains(t, out, "file=docs/fixes.sexp")
	assert.Contains(t, out, "file=FIXES.md")
	run(0, "roadmap", "add", "--id", "i3", "--group", "g", "--title", "I3", "--text", "Three.")
	run(0, "roadmap", "pull", "--id", "i3", "--release", "v1.0.1")
	run(0, "roadmap", "pull", "--id", "f2", "--group", "g")
	run(0, "roadmap", "note", "--id", "f1", "--text", "A note.")
	run(0, "roadmap", "done", "--id", "f1", "--id", "i1", "--evidence", "PR #9")
	run(0, "roadmap", "remove", "--id", "f3")
	run(0, "roadmap", "check")

	res := workMain(unreachable(t)).Run("roadmap", "list", "--repo", dir, "--json")
	require.Equal(t, 0, res.Code, res.Stderr)
	var got struct {
		Items []struct {
			Fields map[string]any `json:"fields"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal([]byte(res.Stdout), &got), res.Stdout)
	states := map[string]string{}
	for _, it := range got.Items {
		states[it.Fields["id"].(string)] = it.Fields["state"].(string) + "@" + it.Fields["place"].(string)
	}
	assert.Equal(t, map[string]string{
		"i1": "done@", "i2": "todo@g", "f2": "todo@g",
		"f1": "done@v1.0.1", "i3": "planned@v1.0.1",
	}, states)

	out = run(0, "roadmap", "list", "--state", "planned")
	assert.Contains(t, out, "id=i3")
	assert.NotContains(t, out, "id=f1")
	fixes, err := os.ReadFile(filepath.Join(dir, "FIXES.md"))
	require.NoError(t, err)
	assert.Contains(t, string(fixes), "**F1** (done). A note. Done: PR #9. From: PR #1.")
}

func TestRoadmapRefusalsWriteNothing(t *testing.T) {
	t.Parallel()
	dir := workRecord(t)
	before := func() map[string]string {
		m := map[string]string{}
		for _, f := range []string{"docs/roadmap.sexp", "docs/fixes.sexp", "ROADMAP.md", "FIXES.md"} {
			b, err := os.ReadFile(filepath.Join(dir, f))
			require.NoError(t, err)
			m[f] = string(b)
		}
		return m
	}
	was := before()
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"roadmap", "add", "--id", "f1", "--release", "v1.0.1", "--title", "x", "--origin", "y"}, `duplicate id "f1"`},
		{[]string{"roadmap", "add", "--id", "n", "--title", "x"}, "--release or --group is required"},
		{[]string{"roadmap", "add", "--id", "n", "--group", "g", "--title", "x"}, "--text is required for a roadmap item"},
		{[]string{"roadmap", "remove", "--id", "nope"}, `unknown id "nope"`},
		{[]string{"roadmap", "remove"}, "--id is required"},
		{[]string{"roadmap", "pull", "--id", "i1", "--group", "nope"}, `--group "nope" names no group`},
		{[]string{"roadmap", "done", "--id", "f1", "--evidence", "trust me"}, "is not a pull request"},
		{[]string{"roadmap", "pull", "--id", "i1", "--release", "v1.0.1", "--group", "g"}, "give one"},
	} {
		res := workMain(unreachable(t)).Run(append(c.args, "--repo", dir)...)
		assert.Equal(t, 2, res.Code, "%v\n%s%s", c.args, res.Stdout, res.Stderr)
		assert.Contains(t, res.Stderr, c.want, c.args)
	}
	// the last fix of a release, after the other one leaves: a parent link
	res := workMain(unreachable(t)).Run("roadmap", "pull", "--id", "f1", "--id", "f2", "--group", "g", "--repo", dir)
	assert.Equal(t, 2, res.Code)
	assert.Contains(t, res.Stderr, "last entry of release v1.0.1")
	assert.Equal(t, was, before(), "a refused verb wrote")
}

func TestRoadmapCheckFailsOnAStalePage(t *testing.T) {
	t.Parallel()
	dir := workRecord(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ROADMAP.md"), []byte("by hand\n"), 0o644))
	res := workMain(unreachable(t)).Run("roadmap", "check", "--repo", dir)
	assert.Equal(t, 1, res.Code, res.Stdout+res.Stderr)
	assert.Contains(t, res.Stdout+res.Stderr, "ROADMAP.md differs from what its data renders")
	res = workMain(unreachable(t)).Run("roadmap", "render", "--repo", dir)
	assert.Equal(t, 0, res.Code)
	assert.Contains(t, res.Stdout, "file=ROADMAP.md")
}

func TestRoadmapRenderOfDataHeldElsewhere(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	in, out := filepath.Join(dir, "other.sexp"), filepath.Join(dir, "PAGE.md")
	require.NoError(t, os.WriteFile(in, []byte(testRoadmap), 0o644))
	res := workMain(unreachable(t)).Run("roadmap", "render", "--file", in, "--out", out, "--source", "work/roadmaps/other.sexp")
	require.Equal(t, 0, res.Code, res.Stdout+res.Stderr)
	page, err := os.ReadFile(out)
	require.NoError(t, err)
	assert.Contains(t, string(page), "<!-- Generated from work/roadmaps/other.sexp by tools/roadmap.")
	res = workMain(unreachable(t)).Run("roadmap", "render", "--file", in)
	assert.Equal(t, 2, res.Code)
	assert.Contains(t, res.Stderr, "--file and --out go together")
}

// TestHelpNamesTheRoadmapVerbsAndWhereTheOldOnesWent: the banner lists each
// roadmap verb with its usage and one example, and says the old work-record
// verbs are gone and what replaced them.
func TestHelpNamesTheRoadmapVerbsAndWhereTheOldOnesWent(t *testing.T) {
	t.Parallel()
	help := workMain(unreachable(t)).Run("help")
	require.Equal(t, 0, help.Code)
	_, examples, ok := strings.Cut(help.Stdout, "example:\n")
	require.True(t, ok, help.Stdout)
	for _, v := range []string{"check", "list", "add", "remove", "pull", "done", "note", "render"} {
		assert.Contains(t, help.Stdout, "  nova-work roadmap "+v+" ", "usage of roadmap %s", v)
		assert.Equal(t, 1, strings.Count(examples, "nova-work roadmap "+v+" ")+strings.Count(examples, "nova-work roadmap "+v+"\n"), "one example of roadmap %s:\n%s", v, examples)
	}
	assert.Contains(t, help.Stdout, "The old roadmap\nverbs, which edited the private work record's roadmaps/*.sexp, are gone")
	assert.Contains(t, help.Stdout, "these replace them")

	group := workMain(unreachable(t)).Run("roadmap", "-h")
	assert.Equal(t, 0, group.Code)
	assert.Contains(t, group.Stdout, "nova-work roadmap done")
	_, groupExamples, ok := strings.Cut(group.Stdout, "example:\n")
	require.True(t, ok, group.Stdout)
	for _, v := range []string{"check", "list", "add", "remove", "pull", "done", "note", "render"} {
		assert.Contains(t, groupExamples, "  nova-work roadmap "+v, "roadmap -h has no example of %s:\n%s", v, group.Stdout)
	}
	assert.Contains(t, group.Stdout, "are gone (the v1.2.3 re-seed);\nthese replace them")
	one := workMain(unreachable(t)).Run("help", "roadmap", "pull")
	assert.Equal(t, 0, one.Code)
	assert.Contains(t, one.Stdout, "The old nova-work roadmap verbs edited the private work record's")
}

// TestTheRoadmapExamplesRunInOrder runs the banner's roadmap example lines, in
// order, against a repository with a v1.2.6 release (my-old-fix, other-fix) and
// a group holding my-item: every line exits 0 and check is clean after.
func TestTheRoadmapExamplesRunInOrder(t *testing.T) {
	t.Parallel()
	documented := []string{
		"nova-work roadmap check",
		"nova-work roadmap list --release v1.2.6 --state planned",
		`nova-work roadmap add --id my-fix --release v1.2.6 --title "What the fix does" --origin "PR #12"`,
		"nova-work roadmap remove --id my-old-fix",
		"nova-work roadmap pull --id my-item --release v1.2.6",
		`nova-work roadmap done --id my-fix --evidence "PR #12"`,
		`nova-work roadmap note --id my-fix --text "Needs the store migration first."`,
		"nova-work roadmap render",
	}
	all, err := onboarding.ExampleLines(workTool(realGitHub()).Banner(), "nova-work")
	require.NoError(t, err)
	var got []string
	for _, e := range all {
		if strings.HasPrefix(e, "nova-work roadmap ") {
			got = append(got, e)
		}
	}
	require.Equal(t, documented, got)

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "docs"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "docs/roadmap.sexp"), []byte(strings.NewReplacer(`"i1"`, `"my-item"`).Replace(testRoadmap)), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "docs/fixes.sexp"), []byte(strings.NewReplacer(`"v1.0.1"`, `"v1.2.6"`, `"f1"`, `"my-old-fix"`).Replace(testFixes)), 0o644))
	pages := workMain(unreachable(t)).Run("roadmap", "render", "--repo", dir)
	require.Equal(t, 0, pages.Code, pages.Stdout+pages.Stderr)
	for _, line := range got {
		words, err := onboarding.Steps("nova-work", []string{"$ " + line})
		require.NoError(t, err, line)
		require.Len(t, words, 1, line)
		res := workMain(unreachable(t)).Run(append(words[0].Args, "--repo", dir)...)
		require.Equal(t, 0, res.Code, "%s\n%s%s", line, res.Stdout, res.Stderr)
	}
	res := workMain(unreachable(t)).Run("roadmap", "check", "--repo", dir)
	require.Equal(t, 0, res.Code, res.Stdout+res.Stderr)
}

// TestRoadmapAddMissingOriginExample verifies that roadmap add invoked without
// --origin prints valid origin format examples (PR #123, 40-char SHA, or commit reference)
// and exits 2 with a runnable remedy in the usage block.
func TestRoadmapAddMissingOriginExample(t *testing.T) {
	t.Parallel()
	dir := workRecord(t)
	res := workMain(unreachable(t)).Run("roadmap", "add", "--repo", dir, "--id", "my-fix", "--release", "v1.0.1", "--title", "My fix")
	require.Equal(t, 2, res.Code, "%s%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stderr, "ROADMAP-ADD REFUSED")
	assert.Contains(t, res.Stderr, "--origin")
	assert.Contains(t, res.Stderr, "PR #123")
	assert.Contains(t, res.Stderr, "40-char SHA")
	assert.Contains(t, res.Stderr, "commit reference")
	assert.Contains(t, res.Stderr, "; run: ")
}
