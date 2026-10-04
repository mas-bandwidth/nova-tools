package docs

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// spec_isa_test.go holds docs/SPEC-ISA.md, layer 1 of the nova-sprint
// processor, to its own gate: the instruction set keeps a kind only if a
// current card uses it, the one wait kind names hold, sentinel and wave as
// mapped onto it, and the what-changes table shows fewer concepts after than
// before. It reads the document as text and runs no product code.

// specISAPath is the instruction set, relative to this package.
const specISAPath = "../../docs/SPEC-ISA.md"

var (
	// isaCandidatesRe reads the one line the spec decides its kinds on.
	isaCandidatesRe = regexp.MustCompile(`(?m)^Candidates:\s*(.+)$`)
	// isaCountRe reads the before and after counts of the what-changes table.
	isaCountRe = regexp.MustCompile(`(?m)^concepts (before|after):\s*(\d+)\s*$`)
)

// TestSpecISAEveryKindIsUsedByACurrentCard is the spec's contract. Every
// candidate kind is either a row of the mapping table tied to a current card,
// or it is named as folded into another kind or reserved for a later layer;
// fence and vector are never rows, because no current card uses them. The one
// wait kind names hold, sentinel and wave as mapped onto it. The what-changes
// count falls, which is the (a) simplicity check.
func TestSpecISAEveryKindIsUsedByACurrentCard(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(specISAPath)
	require.NoError(t, err, "%s: the layer 1 instruction set", specISAPath)
	doc := string(raw)

	candidates := isaCandidates(t, doc)
	require.NotEmpty(t, candidates, "%s: no `Candidates:` line; the spec names the kinds it decides", specISAPath)

	rows := isaTableRows(t, isaSection(t, doc, "## The kinds"))
	require.NotEmpty(t, rows, "%s: the `## The kinds` mapping table is empty", specISAPath)
	require.Contains(t, rows[0], "current card", "%s: the kinds mapping table has no `current card` column", specISAPath)

	folded := isaSection(t, doc, "## Folded and reserved")
	require.NotEmpty(t, folded, "%s: no `## Folded and reserved` section; a folded or reserved kind has nowhere to be named", specISAPath)

	for _, c := range candidates {
		row, ok := isaKindRow(rows, c)
		if !ok {
			assert.Contains(t, folded, "`"+c+"`",
				"%s: candidate kind %q is neither a mapping-table row with a current card nor named in `## Folded and reserved`", specISAPath, c)
			continue
		}
		card := isaCell(rows[0], row, "current card")
		assert.NotEmpty(t, strings.TrimSpace(card),
			"%s: kind %q is in the mapping table with no current card", specISAPath, c)
		assert.Regexp(t, `(?i)card|primary|held|sentinel`, card,
			"%s: kind %q is not tied to a current card", specISAPath, c)
	}

	// A kind no current card uses is not a kind: fence folds into wait and
	// vector is reserved, so neither may be a mapping-table row.
	for _, c := range []string{"fence", "vector"} {
		_, ok := isaKindRow(rows, c)
		assert.False(t, ok, "%s: %q is listed as a kind, but no current card uses it", specISAPath, c)
	}

	// The one wait kind maps hold, sentinel and wave onto it.
	waitRows := isaTableRows(t, isaSection(t, doc, "## The one wait kind"))
	require.NotEmpty(t, waitRows, "%s: the `## The one wait kind` mapping table is empty", specISAPath)
	for _, old := range []string{"held", "sentinel", "wave"} {
		found := false
		for _, r := range waitRows[1:] {
			if !strings.Contains(strings.ToLower(r[0]), old) {
				continue
			}
			found = true
			assert.Contains(t, strings.ToLower(strings.Join(r, " ")), "wait",
				"%s: %q is not mapped onto the one wait kind", specISAPath, old)
		}
		assert.True(t, found, "%s: the one wait kind does not name %q as mapped onto it", specISAPath, old)
	}

	// The what-changes table shows fewer concepts after than before.
	before, after := isaConceptCounts(t, doc)
	assert.Less(t, after, before,
		"%s: concepts after (%d) is not fewer than before (%d); the wait kind must remove concepts, not add them", specISAPath, after, before)
}

// isaCandidates reads the kinds the spec decides on, lower cased.
func isaCandidates(t *testing.T, doc string) []string {
	t.Helper()
	m := isaCandidatesRe.FindStringSubmatch(doc)
	if m == nil {
		return nil
	}
	var out []string
	for _, part := range strings.Split(m[1], ",") {
		part = strings.ToLower(strings.Trim(strings.TrimSpace(part), "`."))
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// isaConceptCounts reads the two counts of the what-changes table. A missing
// count is -1, so a document that dropped one fails the comparison rather than
// passing it.
func isaConceptCounts(t *testing.T, doc string) (before, after int) {
	t.Helper()
	before, after = -1, -1
	for _, m := range isaCountRe.FindAllStringSubmatch(doc, -1) {
		n, err := strconv.Atoi(m[2])
		require.NoError(t, err, "%s: count %q is not a number", specISAPath, m[2])
		if m[1] == "before" {
			before = n
		} else {
			after = n
		}
	}
	return before, after
}

// isaSection returns the text from a heading to the next top-level heading, or
// "" when the heading is absent.
func isaSection(t *testing.T, doc, heading string) string {
	t.Helper()
	i := strings.Index(doc, heading)
	if i < 0 {
		return ""
	}
	rest := doc[i+len(heading):]
	if j := strings.Index(rest, "\n## "); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

// isaTableRows reads a Markdown table as rows of trimmed cells, header first,
// separator rows dropped. A non-table line is skipped.
func isaTableRows(t *testing.T, section string) [][]string {
	t.Helper()
	var rows [][]string
	for _, line := range strings.Split(section, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		if isaSeparator(cells) {
			continue
		}
		rows = append(rows, cells)
	}
	return rows
}

// isaSeparator says the row is the `|---|---|` line under a header.
func isaSeparator(cells []string) bool {
	for _, c := range cells {
		if strings.Trim(c, ":- ") != "" {
			return false
		}
	}
	return len(cells) > 0
}

// isaKindRow finds the table row whose `kind` cell is the candidate.
func isaKindRow(rows [][]string, kind string) ([]string, bool) {
	if len(rows) == 0 {
		return nil, false
	}
	idx := isaCol(rows[0], "kind")
	if idx < 0 {
		return nil, false
	}
	for _, r := range rows[1:] {
		if len(r) > idx && strings.EqualFold(strings.Trim(r[idx], "` "), kind) {
			return r, true
		}
	}
	return nil, false
}

// isaCell returns the named cell of a row, "" when the column is absent.
func isaCell(header, row []string, name string) string {
	idx := isaCol(header, name)
	if idx < 0 || idx >= len(row) {
		return ""
	}
	return row[idx]
}

// isaCol is the index of a header cell, matched without case, -1 when absent.
func isaCol(header []string, name string) int {
	for i, h := range header {
		if strings.EqualFold(strings.Trim(h, "` "), name) {
			return i
		}
	}
	return -1
}
