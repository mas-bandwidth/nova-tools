package docs

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// processor_doc_test.go holds docs/PROCESSOR.md to the processor view
// (docs/PROCESSOR.md): the mapping table marks every row MECHANISM or
// METAPHOR, a MECHANISM row names a file that is in the tree or a card, and a
// METAPHOR row names no code path. The page is where a metaphor lives, so a
// metaphor row that names a path is the concept back in code.

const processorDocPath = "../../docs/PROCESSOR.md"

// codePathRe is a repository path a mapping row can name. A metaphor row that
// matches it names a code path.
var codePathRe = regexp.MustCompile(`(?:docs|internal|cmd|tla|tools|fleet)/[A-Za-z0-9_./-]+`)

// cardTokenRe is a card id in backticks (isa-spec), the other name a
// MECHANISM row may carry when the layer is a card and not yet a file.
var cardTokenRe = regexp.MustCompile("`([a-z][a-z0-9]+(?:-[a-z0-9]+)+)`")

// processorConcepts are the mapping rows the accepted design names. Reorder
// buffer and retire may share one row; each phrase still has to appear.
var processorConcepts = []string{
	"ISA",
	"assembler",
	"out-of-order issue",
	"speculation",
	"prediction",
	"renaming",
	"reorder buffer",
	"retire",
	"heterogeneous cores",
	"SIMD",
	"caches",
	"interrupts",
	"counters",
	"power",
	"debugger",
}

// processorNeedles are the sentences the page has to carry: the simplicity
// test, the ISA page, and the out-of-order section.
var processorNeedles = []string{
	"nova-sprint is a processor",
	"Cards are its instructions",
	"the front end that issues work and handles exceptions, never executes it",
	"docs/SPEC-ISA.md",
	"A feature enters code only if it (a) removes concepts or code that exist today, (b) shows a measured gain in cost, wall clock or reliability from the counters, or (c) makes the machine easier to model and check in TLA+",
	"Anything that is only metaphor goes in docs/PROCESSOR.md, not in code.",
	"## OUT OF ORDER",
	"Reservation stations are DEPENDS-ON plus external operands",
	"a card is issued early and dispatches when its operands are ready",
	"I/O stalls",
	"a card waiting on a PR, a branch, a time",
	"Interrupt-driven release is the target",
	"the tick releases a wait when its operand holds",
	"a coordinator re-checking by hand",
	"exception escalation vector",
}

func TestProcessorDocNamesEachMetaphorAndItsMechanism(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(processorDocPath)
	require.NoError(t, err, "docs/PROCESSOR.md: %v", err)
	text := string(raw)

	for _, needle := range processorNeedles {
		assert.Contains(t, text, needle, "docs/PROCESSOR.md lacks %q", needle)
	}
	assert.Contains(t, text, "rule, bud, coordinator, owner",
		"the exception escalation vector does not name its last step")
	assert.Regexp(t, `(?m)^## OUT OF ORDER$`, text, "the out-of-order section is not a heading")

	rows := processorMappingRows(t, text)
	require.NotEmpty(t, rows, "docs/PROCESSOR.md has no mapping table")

	var concepts []string
	for _, row := range rows {
		concepts = append(concepts, row.concept)
		paths := codePaths(row.line)
		cards := cardTokens(row.line)
		switch row.mark {
		case "MECHANISM":
			assert.True(t, len(paths) > 0 || len(cards) > 0,
				"MECHANISM %q names no file or card: %s", row.concept, row.line)
			for _, p := range paths {
				_, statErr := os.Stat(filepath.Join("../..", p))
				assert.NoError(t, statErr, "MECHANISM %q names %s, which is not in the tree", row.concept, p)
			}
		case "METAPHOR":
			assert.Empty(t, paths, "METAPHOR %q names a code path: %s", row.concept, row.line)
			assert.Empty(t, cards, "METAPHOR %q names a card: %s", row.concept, row.line)
		default:
			assert.Fail(t, "row mark", "%q is %q, want MECHANISM or METAPHOR", row.concept, row.mark)
		}
	}
	joined := strings.ToLower(strings.Join(concepts, "\n"))
	for _, concept := range processorConcepts {
		assert.Contains(t, joined, strings.ToLower(concept), "mapping table has no row for %s", concept)
	}
}

type processorRow struct {
	concept string
	mark    string
	line    string
}

func processorMappingRows(t *testing.T, text string) []processorRow {
	t.Helper()
	lines := strings.Split(text, "\n")
	var rows []processorRow
	inTable := false
	conceptCol, markCol := 0, 1
	for i, line := range lines {
		cells := tableCells(line)
		if cells == nil {
			inTable = false
			continue
		}
		if isSeparatorRow(cells) {
			continue
		}
		if !inTable && isMarkHeader(cells) {
			inTable = true
			conceptCol, markCol = headerIndexes(t, cells, i+1)
			continue
		}
		if !inTable {
			continue
		}
		require.Greater(t, len(cells), markCol, "docs/PROCESSOR.md:%d: short mapping row %q", i+1, line)
		mark := cells[markCol]
		assert.Contains(t, []string{"MECHANISM", "METAPHOR"}, mark,
			"docs/PROCESSOR.md:%d: row is %q, want MECHANISM or METAPHOR", i+1, mark)
		rows = append(rows, processorRow{concept: cells[conceptCol], mark: mark, line: line})
	}
	return rows
}

func isMarkHeader(cells []string) bool {
	for _, c := range cells {
		if strings.EqualFold(c, "mark") {
			return true
		}
	}
	return false
}

func headerIndexes(t *testing.T, cells []string, line int) (concept, mark int) {
	t.Helper()
	concept, mark = -1, -1
	for i, c := range cells {
		switch strings.ToLower(c) {
		case "concept":
			concept = i
		case "mark":
			mark = i
		}
	}
	require.NotEqual(t, -1, concept, "docs/PROCESSOR.md:%d: mapping header has no concept column", line)
	require.NotEqual(t, -1, mark, "docs/PROCESSOR.md:%d: mapping header has no mark column", line)
	return concept, mark
}

func tableCells(line string) []string {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "|") {
		return nil
	}
	parts := strings.Split(line, "|")
	var cells []string
	for _, p := range parts {
		cells = append(cells, strings.TrimSpace(p))
	}
	if len(cells) > 0 && cells[0] == "" {
		cells = cells[1:]
	}
	if len(cells) > 0 && cells[len(cells)-1] == "" {
		cells = cells[:len(cells)-1]
	}
	if len(cells) == 0 {
		return nil
	}
	return cells
}

func isSeparatorRow(cells []string) bool {
	for _, c := range cells {
		if !regexp.MustCompile(`^:?-+:?$`).MatchString(c) {
			return false
		}
	}
	return true
}

func codePaths(line string) []string {
	var out []string
	for _, m := range codePathRe.FindAllString(line, -1) {
		out = append(out, strings.TrimRight(m, ".,;:)`"))
	}
	return out
}

func cardTokens(line string) []string {
	var out []string
	for _, m := range cardTokenRe.FindAllStringSubmatch(line, -1) {
		out = append(out, m[1])
	}
	return out
}
