package swarm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// DOGFOOD D12 (2026-09-11, HIGH): a COMPLETE report -- head, `findings: 5`, every item
// with its evidence, gates, one line -- was `RUN MALFORMED line=66` and went to failed/.
//
// Line 66 was a Per item row whose item quoted a grammar alternation:
//
//	| read 6 #2: `day_basis=` enumerates `<utc|zone>`, code prints `mixed` | red | ... |
//
// `tableRow` split the row on every `|` in it, so the `|` INSIDE the backticks made four
// cells out of three and the state column landed on prose: not one of rule 15's three
// words, so the whole report was quarantined and five findings were lost to triage.
//
// A backtick span is a QUOTE, and a quote is text. Rule 2 (SPEC-WORKER.md:82-84) requires
// every finding to quote its rule VERBATIM -- and this tool's own rules are grammar lines
// full of `|`. A reader obeying rule 2 could not write a row about them. The parser gains
// no opinion here and salvages nothing: it reads the cell boundaries of the row the
// template names, and a row whose state word is still not one of three is malformed as
// before.
func TestAPipeInsideAQuoteIsNotACellBoundary(t *testing.T) {
	t.Parallel()

	head := "# t\n\n## Head\nfindings: 1\nrepo: o/n\nrev: abc\na paragraph.\n\n## Per item\n| item | state | evidence |\n| --- | --- | --- |\n"

	got := ParseReport([]byte(head +
		"| `day_basis=` enumerates `<utc|zone>`, code prints `mixed` | red | SPEC-TOKENS.md:436 |\n"))
	require.Equal(t, ClassOK, got.Class, "a row quoting a grammar alternation is a row (rule 2): class=%s line=%d", got.Class, got.MalformedLine)
	require.Len(t, got.Items, 1, "the row wants one item, got %d", len(got.Items))
	it := got.Items[0]
	assert.Equal(t, "red", it.State, "the state and the evidence are the cells beside the quote, got state=%q evidence=%q", it.State, it.Evidence)
	assert.Equal(t, "SPEC-TOKENS.md:436", it.Evidence, "the state and the evidence are the cells beside the quote, got state=%q evidence=%q", it.State, it.Evidence)

	// The same in a Gates row, and the same in the evidence cell.
	gates := ParseReport([]byte("# t\n\n## Head\nfindings: 0\n\n## Gates\n| name | result | seconds |\n| --- | --- | --- |\n" +
		"| `go test ./... | tee log` | pass | 12 |\n"))
	assert.Equal(t, ClassClean, gates.Class, "a gate whose command quotes a pipe is a gate: class=%s gates=%+v", gates.Class, gates.Gates)
	if assert.Len(t, gates.Gates, 1, "a gate whose command quotes a pipe is a gate: class=%s gates=%+v", gates.Class, gates.Gates) {
		assert.Equal(t, "pass", gates.Gates[0].Result, "a gate whose command quotes a pipe is a gate: class=%s gates=%+v", gates.Class, gates.Gates)
	}

	// AND THE QUARANTINE STILL HOLDS. An unquoted fourth state word is malformed on its
	// own line, and an unterminated backtick does not swallow the row's boundaries.
	bad := ParseReport([]byte(head + "| an item | maybe | evidence |\n"))
	assert.Equal(t, ClassMalformed, bad.Class, "a fourth state word is malformed at its line: class=%s line=%d", bad.Class, bad.MalformedLine)
	assert.Equal(t, 12, bad.MalformedLine, "a fourth state word is malformed at its line: class=%s line=%d", bad.Class, bad.MalformedLine)
	open := ParseReport([]byte(head + "| an item with one ` backtick | maybe | evidence |\n"))
	assert.Equal(t, ClassMalformed, open.Class, "an unterminated quote does not turn a bad row good: class=%s", open.Class)
}
