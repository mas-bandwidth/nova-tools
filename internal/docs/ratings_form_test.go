package docs

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"regexp"
	"strconv"
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

// checkRatingForms walks a docs/ratings tree and checks every rating file of
// a combined-form release. It returns the files checked and one problem per
// missing part, each naming the file and the part.
func checkRatingForms(fsys fs.FS) (checked, problems []string, err error) {
	releases, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, nil, err
	}
	for _, rel := range releases {
		if !rel.IsDir() || !ratingFormRelease(rel.Name()) {
			continue
		}
		entries, err := fs.ReadDir(fsys, rel.Name())
		if err != nil {
			return nil, nil, err
		}
		for _, e := range entries {
			name := path.Join(rel.Name(), e.Name())
			if !e.Type().IsRegular() || !ratingFileRE.MatchString(e.Name()) {
				problems = append(problems, name+": not a <tool>-<friend>.md rating file")
				continue
			}
			body, err := fs.ReadFile(fsys, name)
			if err != nil {
				return nil, nil, err
			}
			checked = append(checked, name)
			for _, part := range ratingFormMissing(string(body)) {
				problems = append(problems, name+": missing "+part)
			}
		}
	}
	return checked, problems, nil
}

// ratingFormRelease reports whether a directory name is a release at or
// after ratingFormFirstRelease.
func ratingFormRelease(name string) bool {
	m := ratingReleaseRE.FindStringSubmatch(name)
	first := ratingReleaseRE.FindStringSubmatch(ratingFormFirstRelease)
	if m == nil {
		return false
	}
	for i := 1; i <= 3; i++ {
		a, _ := strconv.Atoi(m[i])
		b, _ := strconv.Atoi(first[i])
		if a != b {
			return a > b
		}
	}
	return true
}

// ratingFormMissing names every part of the form a rating file lacks.
func ratingFormMissing(body string) []string {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	var header []string
	sections := map[string][]string{}
	current := ""
	inHeader := true
	for _, line := range lines {
		line = strings.TrimRight(line, " \t")
		if strings.HasPrefix(line, "## ") {
			inHeader = false
			current = strings.TrimSpace(strings.TrimPrefix(line, "## "))
			if _, ok := sections[current]; !ok {
				sections[current] = []string{}
			}
			continue
		}
		if inHeader {
			header = append(header, line)
		} else {
			sections[current] = append(sections[current], line)
		}
	}

	var missing []string
	for _, h := range []struct {
		re   *regexp.Regexp
		part string
	}{
		{ratingReadRE, "header line READ: n/10"},
		{ratingUseRE, "header line USE: n/10"},
		{ratingBuildRE, "header line Build: <commit>"},
	} {
		if !anyLineMatches(header, h.re) {
			missing = append(missing, h.part)
		}
	}
	for _, s := range ratingSections {
		if _, ok := sections[s]; !ok {
			missing = append(missing, "section ## "+s)
		}
	}
	if findings, ok := sections["Findings"]; ok {
		missing = append(missing, ratingFindingsMissing(findings)...)
	}
	return missing
}

func anyLineMatches(lines []string, re *regexp.Regexp) bool {
	for _, l := range lines {
		if re.MatchString(l) {
			return true
		}
	}
	return false
}

// ratingFindingsMissing checks the Findings section: a table with a where
// column (the place) and a fix column, at least one row, and every row with
// both cells filled.
func ratingFindingsMissing(lines []string) []string {
	where, fix, start := -1, -1, -1
	for i := 0; i+1 < len(lines); i++ {
		if !strings.HasPrefix(lines[i], "|") || !ratingTableSep.MatchString(lines[i+1]) {
			continue
		}
		where, fix = -1, -1
		for j, c := range markdownCells(lines[i]) {
			switch strings.ToLower(c) {
			case "where":
				where = j
			case "fix":
				fix = j
			}
		}
		if where >= 0 && fix >= 0 {
			start = i + 2
			break
		}
	}
	if start < 0 {
		return []string{"Findings table with where and fix columns"}
	}
	var missing []string
	n := 0
	for _, line := range lines[start:] {
		if !strings.HasPrefix(line, "|") {
			break
		}
		n++
		cells := markdownCells(line)
		if where >= len(cells) || cells[where] == "" {
			missing = append(missing, fmt.Sprintf("finding %d: a place (where)", n))
		}
		if fix >= len(cells) || cells[fix] == "" {
			missing = append(missing, fmt.Sprintf("finding %d: a fix", n))
		}
	}
	if n == 0 {
		return []string{"Findings table with at least one finding"}
	}
	return missing
}

// markdownCells splits a table row into its trimmed cells. A pipe inside a
// code span or escaped as \| is part of the cell.
func markdownCells(row string) []string {
	row = strings.TrimSpace(row)
	row = strings.TrimPrefix(row, "|")
	var cells []string
	var cell strings.Builder
	inCode := false
	for i := 0; i < len(row); i++ {
		c := row[i]
		switch {
		case c == '\\' && i+1 < len(row) && row[i+1] == '|':
			cell.WriteString(`\|`)
			i++
		case c == '`':
			inCode = !inCode
			cell.WriteByte(c)
		case c == '|' && !inCode:
			cells = append(cells, strings.TrimSpace(cell.String()))
			cell.Reset()
		default:
			cell.WriteByte(c)
		}
	}
	if rest := strings.TrimSpace(cell.String()); rest != "" {
		cells = append(cells, rest)
	}
	return cells
}

// TestEveryToolHasACurrentReadRating checks that every tool under cmd/ has a
// 1.2.0 READ rating in docs/ratings/1.2.0/. A tool is identified by its
// directory name (e.g. "nova-bus" from cmd/nova-bus). The test walks cmd/
// and for each nova-* directory, looks for a rating file named
// "nova-<tool>-<friend>.md" with READ score in the header.
func TestEveryToolHasACurrentReadRating(t *testing.T) {
	t.Parallel()

	// Get all tools under cmd/
	cmdDir := "../../cmd"
	tools, err := os.ReadDir(cmdDir)
	require.NoError(t, err)

	// Filter nova-* directories
	var toolNames []string
	for _, e := range tools {
		if e.IsDir() && strings.HasPrefix(e.Name(), "nova-") {
			toolNames = append(toolNames, e.Name())
		}
	}

	// Check ratings directory
	ratingsDir := "../../docs/ratings/1.2.0"
	ratings, err := os.ReadDir(ratingsDir)
	require.NoError(t, err)

	// Build a set of tools that have ratings
	hasRating := make(map[string]bool)
	re := regexp.MustCompile(`^nova-(.+)-[a-z]+\.md$`)
	for _, e := range ratings {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			matches := re.FindStringSubmatch(e.Name())
			if len(matches) == 2 {
				hasRating[matches[1]] = true
			}
		}
	}

	// Check each tool has a rating
	for _, tool := range toolNames {
		t.Run(tool, func(t *testing.T) {
			t.Parallel()
			toolBase := strings.TrimPrefix(tool, "nova-")
			require.True(t, hasRating[toolBase], "tool %s has no 1.2.0 READ rating in %s", tool, ratingsDir)
		})
	}
}
