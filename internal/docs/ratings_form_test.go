package docs

import (
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// inFormRating is a rating file in the form docs/ratings/README.md defines;
// each case below breaks exactly one part of it.
const inFormRating = `# nova-x READ and USE rating, nova-tools 1.2.0

Rater: a cold reader
Build: 7525efe26a92
READ: 7/10
USE: 7.5/10

## Reasons

READ. Why.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-x/main.go:54 | The banner says ` + "`a | b`" + `. | Say c. | S |

## Good, keep

The refusals.

## Compared with earlier ratings

Better.
`

// inFormRatingFindings is inFormRating with its Findings section's body
// replaced.
func inFormRatingFindings(body string) string {
	head, rest, _ := strings.Cut(inFormRating, "## Findings\n")
	_, tail, _ := strings.Cut(rest, "## Good, keep")
	return head + "## Findings\n\n" + body + "\n## Good, keep" + tail
}

// TestRatingFileIsInForm checks every docs/ratings/<release>/<tool>-<friend>.md
// of a release in the combined form (1.2.0 on) against the form
// docs/ratings/README.md defines: the READ n/10, USE n/10 and Build header
// lines, and the sections Reasons, Findings (each finding with a place and a
// fix), Good, keep and Compared with earlier ratings. A file missing any part
// fails with the file and the part named. The table drives the checker over a
// fake tree first, so each refusal is witnessed, then walks the real tree.
func TestRatingFileIsInForm(t *testing.T) {
	t.Parallel()

	const file = "1.2.0/nova-x-emma.md"
	cases := []struct {
		name string
		body string
		want []string // the missing parts named, in order; empty means in form
	}{
		{"in form", inFormRating, nil},
		{"whole score", strings.Replace(inFormRating, "USE: 7.5/10", "USE: 10/10", 1), nil},
		{"no READ line", strings.Replace(inFormRating, "READ: 7/10\n", "", 1), []string{"header line READ: n/10"}},
		{"READ with two decimals", strings.Replace(inFormRating, "READ: 7/10", "READ: 7.25/10", 1), []string{"header line READ: n/10"}},
		{"READ over ten", strings.Replace(inFormRating, "READ: 7/10", "READ: 11/10", 1), []string{"header line READ: n/10"}},
		{"READ below the first section", strings.Replace(strings.Replace(inFormRating, "READ: 7/10\n", "", 1), "READ. Why.", "READ: 7/10", 1), []string{"header line READ: n/10"}},
		{"no USE line", strings.Replace(inFormRating, "USE: 7.5/10\n", "", 1), []string{"header line USE: n/10"}},
		{"USE not a number", strings.Replace(inFormRating, "USE: 7.5/10", "USE: good/10", 1), []string{"header line USE: n/10"}},
		{"no Build line", strings.Replace(inFormRating, "Build: 7525efe26a92\n", "", 1), []string{"header line Build: <commit>"}},
		{"Build names no commit", strings.Replace(inFormRating, "Build: 7525efe26a92", "Build: head of main", 1), []string{"header line Build: <commit>"}},
		{"no Reasons", strings.Replace(inFormRating, "## Reasons", "## Why", 1), []string{"section ## Reasons"}},
		{"no Findings", strings.Replace(inFormRating, "## Findings", "## Problems", 1), []string{"section ## Findings"}},
		{"no Good, keep", strings.Replace(inFormRating, "## Good, keep", "## Good", 1), []string{"section ## Good, keep"}},
		{"no Compared", strings.Replace(inFormRating, "## Compared with earlier ratings", "## Compared", 1), []string{"section ## Compared with earlier ratings"}},
		{"Findings with no table", inFormRatingFindings("1. cmd/nova-x/main.go:54: something; say c.\n"), []string{"Findings table with where and fix columns"}},
		{"Findings with no rows", strings.Replace(inFormRating, "| 1 | cmd/nova-x/main.go:54 | The banner says `a | b`. | Say c. | S |\n", "", 1), []string{"Findings table with at least one finding"}},
		{"Findings with no fix column", strings.Replace(inFormRating, "| # | where | finding | fix | size |", "| # | where | finding | remedy | size |", 1), []string{"Findings table with where and fix columns"}},
		{"a finding with no place", strings.Replace(inFormRating, "| 1 | cmd/nova-x/main.go:54 |", "| 1 |  |", 1), []string{"finding 1: a place (where)"}},
		{"a finding with no fix", strings.Replace(inFormRating, "| Say c. |", "|  |", 1), []string{"finding 1: a fix"}},
		{"everything missing", "# nothing\n", []string{
			"header line READ: n/10", "header line USE: n/10", "header line Build: <commit>",
			"section ## Reasons", "section ## Findings", "section ## Good, keep", "section ## Compared with earlier ratings",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fsys := fstest.MapFS{
				file:                {Data: []byte(tc.body)},
				"README.md":         {Data: []byte("the form\n")},
				"1.1.0/bus-read.md": {Data: []byte("# the earlier split form, not checked\n")},
				"snapshots/x/a.md":  {Data: []byte("# a snapshot, not checked\n")},
			}
			checked, problems, err := checkRatingForms(fsys)
			require.NoError(t, err)
			assert.Equal(t, []string{file}, checked, "the walk checks the combined-form release only")
			var want []string
			for _, part := range tc.want {
				want = append(want, file+": missing "+part)
			}
			assert.Equal(t, want, problems)
		})
	}

	t.Run("a stray file in a combined-form release", func(t *testing.T) {
		t.Parallel()
		fsys := fstest.MapFS{
			"1.2.0/notes.txt":   {Data: []byte("x")},
			"1.2.0/emma/x.md":   {Data: []byte("x")},
			"1.2.0/nova-x-a.md": {Data: []byte(inFormRating)},
		}
		_, problems, err := checkRatingForms(fsys)
		require.NoError(t, err)
		assert.Equal(t, []string{
			"1.2.0/emma: not a <tool>-<friend>.md rating file",
			"1.2.0/notes.txt: not a <tool>-<friend>.md rating file",
		}, problems)
	})

	t.Run("the tree", func(t *testing.T) {
		t.Parallel()
		checked, problems, err := checkRatingForms(os.DirFS("../../docs/ratings"))
		require.NoError(t, err)
		require.NotEmpty(t, checked, "no combined-form rating file was found under docs/ratings; the walk checked nothing")
		for _, p := range problems {
			t.Errorf("docs/ratings/%s", p)
		}
	})

	t.Run("the README defines the form the test checks", func(t *testing.T) {
		t.Parallel()
		body, err := os.ReadFile("../../docs/ratings/README.md")
		require.NoError(t, err)
		for _, part := range ratingFormParts {
			assert.Contains(t, string(body), part, "docs/ratings/README.md does not name the part %q the test checks", part)
		}
		assert.Contains(t, string(body), "TestRatingFileIsInForm")
		assert.Contains(t, string(body), ratingFormFirstRelease)
	})
}
