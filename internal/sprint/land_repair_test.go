package sprint_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The two refusals of 2026-10-05 were one stray backquote each (SPEC-BUS.md:100 "takes
// 0 backquotes and puts back 1"; an audit file of the private record's security/**, 119
// backquotes). A stray backquote a change leaves is dropped and the note says where; a
// paragraph whose stray backquote could be either of two is refused with its line; a
// prose path's backquotes are not read; the faults a formatter fixes are fixed only on
// the change's lines.
func TestADocumentFaultIsRepairedOnceOrRefusedWithItsLine(t *testing.T) {
	t.Parallel()
	diff := "diff --git a/docs/SPEC-BUS.md b/docs/SPEC-BUS.md\n" +
		"--- a/docs/SPEC-BUS.md\n+++ b/docs/SPEC-BUS.md\n" +
		"@@ -1,3 +1,3 @@\n # Bus\n \n-The `push` verb.\n+The `push` verb takes `--proof first.\n"
	changed := sprint.DocChanged(diff)
	require.Equal(t, map[string]sprint.DocLines{"docs/SPEC-BUS.md": {Added: []int{3}, Deleted: []sprint.DocCut{{At: 3, Lines: []string{"The `push` verb."}}}}}, changed)

	head := "# Bus\n\nThe `push` verb takes `--proof first.\n"
	fixed, fixes, refused := sprint.RepairDoc("docs/SPEC-BUS.md", head, changed["docs/SPEC-BUS.md"], false)
	assert.Empty(t, refused)
	assert.Equal(t, "# Bus\n\nThe `push` verb takes --proof first.\n", fixed)
	assert.Zero(t, strings.Count(fixed, "`")%2, "the repaired file has an even count")
	assert.Equal(t, "the documents were repaired at the merge: docs/SPEC-BUS.md:3 a stray backquote dropped at column 23", sprint.RepairNote(fixes))

	// a lone backquote is one repair however it sits
	fixed, fixes, refused = sprint.RepairDoc("a.md", "one ` two\n", sprint.DocLines{Added: []int{1}}, false)
	assert.Equal(t, "one  two\n", fixed)
	assert.Len(t, fixes, 1)
	assert.Empty(t, refused)

	// two backquotes could be the stray one: refused, naming the line
	amb := "# T\n\nSee `a` b `c` ` here.\n"
	fixed, fixes, refused = sprint.RepairDoc("a.md", amb, sprint.DocLines{Added: []int{3}}, false)
	assert.Equal(t, amb, fixed)
	assert.Empty(t, fixes)
	require.Len(t, refused, 1)
	assert.Equal(t, "a.md:3 leaves a code span unmatched and the repair is ambiguous: 2 backquotes could be the stray one: See `a` b `c` ` here.", refused[0].String())

	// a span the change wraps across two of its lines is joined; one wrapped on the base's is left
	span := "The `stream\nset` verb.\n"
	fixed, fixes, refused = sprint.RepairDoc("a.md", span, sprint.DocLines{Added: []int{1, 2}}, false)
	assert.Equal(t, "The `stream set` verb.\n", fixed)
	assert.Equal(t, []sprint.DocFix{{File: "a.md", Line: 1, What: sprint.SpanJoined}}, fixes)
	assert.Empty(t, refused)
	fixed, fixes, refused = sprint.RepairDoc("a.md", span, sprint.DocLines{}, false)
	assert.Equal(t, span, fixed)
	assert.Empty(t, fixes)
	assert.Empty(t, refused)

	// the base's own fault, on a line the change does not write, is left
	base := "Old `fault.\nnew line\n"
	fixed, fixes, _ = sprint.RepairDoc("a.md", base, sprint.DocLines{Added: []int{2}}, false)
	assert.Equal(t, base, fixed)
	assert.Empty(t, fixes)

	// a prose path's backquotes are not read
	assert.True(t, sprint.DocProse([]string{"security/**", "ratings/**"}, "security/audits/rocketnet-c-api.md"))
	assert.False(t, sprint.DocProse([]string{"security/**", "ratings/**"}, "docs/SPEC-BUS.md"))
	audit := strings.Repeat("`x` ", 59) + "`\n"
	fixed, fixes, refused = sprint.RepairDoc("security/audits/rocketnet-c-api.md", audit, sprint.DocLines{Added: []int{1}}, true)
	assert.Equal(t, 119, strings.Count(fixed, "`"))
	assert.Empty(t, fixes)
	assert.Empty(t, refused)

	// code is not a document
	fixed, fixes, _ = sprint.RepairDoc("x.go", "// a `b\n", sprint.DocLines{Added: []int{1}}, false)
	assert.Equal(t, "// a `b\n", fixed)
	assert.Empty(t, fixes)
}

func TestTheFormatterFaultsAreRepairedOnTheChangesLines(t *testing.T) {
	t.Parallel()
	fixed, fixes, refused := sprint.RepairDoc("a.md", "keep  \nkept\r\none \t\ntwo\r\nlast", sprint.DocLines{Added: []int{1, 3, 4, 5}}, false)
	assert.Empty(t, refused)
	assert.Equal(t, "keep  \nkept\r\none\ntwo\r\nlast\n", fixed, "a CRLF file keeps CRLF; a hard break is not trailing whitespace")
	assert.Equal(t, "the documents were repaired at the merge: a.md:3 trailing whitespace trimmed; a.md:5 a final newline added", sprint.RepairNote(fixes))

	fixed, fixes, _ = sprint.RepairDoc("a.txt", "one\r\ntwo \n", sprint.DocLines{Added: []int{1, 2}}, false)
	assert.Equal(t, "one\ntwo\n", fixed)
	assert.Len(t, fixes, 2)

	// a fence the change opens and runs to the end is closed there
	fixed, fixes, refused = sprint.RepairDoc("a.md", "Text.\n\n```go\nx := 1\n", sprint.DocLines{Added: []int{3, 4}}, false)
	assert.Empty(t, refused)
	assert.Equal(t, "Text.\n\n```go\nx := 1\n```\n", fixed)
	assert.Equal(t, "the documents were repaired at the merge: a.md:5 the code fence opened at line 3 closed at the end of the file", sprint.RepairNote(fixes))

	// a fence with a blank line and text after it has no one end
	open := "```\nx\n\nMore text.\n"
	fixed, fixes, refused = sprint.RepairDoc("a.md", open, sprint.DocLines{Added: []int{1}}, false)
	assert.Equal(t, open, fixed)
	assert.Empty(t, fixes)
	require.Len(t, refused, 1)
	assert.Equal(t, 1, refused[0].Line)

	// backquotes and whitespace inside a block are its own
	block := "```\na ` b  \n```\n"
	fixed, fixes, refused = sprint.RepairDoc("a.md", block, sprint.DocLines{Added: []int{2}}, false)
	assert.Equal(t, block, fixed)
	assert.Empty(t, fixes)
	assert.Empty(t, refused)

	assert.Empty(t, sprint.RepairNote(nil))
}
