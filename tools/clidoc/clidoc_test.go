package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestMarkerToolsInFileOrderOnce: the begin markers name the tools in file
// order, each once; a marker for a name outside nova-<word> is not one.
func TestMarkerToolsInFileOrderOnce(t *testing.T) {
	t.Parallel()

	doc := "x\n<!-- clidoc:begin nova-bus -->\n<!-- clidoc:end nova-bus -->\n" +
		"<!-- clidoc:begin nova-fuse -->\n<!-- clidoc:begin nova-bus -->\n<!-- clidoc:begin other-tool -->\n<!-- clidoc:begin nova_bad -->\n"
	assert.Equal(t, []string{"nova-bus", "nova-fuse"}, markerTools(doc))
	assert.Empty(t, markerTools("no markers here"))
}

// TestReplaceSectionKeepsOutsideAndLiteralDollars: only the block between
// one tool's markers is rewritten, across lines; the other tool's block and
// the prose outside stay; a $ in the pasted help is literal, never a group
// reference.
func TestReplaceSectionKeepsOutsideAndLiteralDollars(t *testing.T) {
	t.Parallel()

	doc := "intro\n<!-- clidoc:begin nova-bus -->\nold\nlines\n<!-- clidoc:end nova-bus -->\nmiddle\n" +
		"<!-- clidoc:begin nova-fuse -->\nkeep\n<!-- clidoc:end nova-fuse -->\nend\n"
	got := replaceSection(doc, "nova-bus", "\nnew $1 costs $20\n")
	want := "intro\n<!-- clidoc:begin nova-bus -->\nnew $1 costs $20\n<!-- clidoc:end nova-bus -->\nmiddle\n" +
		"<!-- clidoc:begin nova-fuse -->\nkeep\n<!-- clidoc:end nova-fuse -->\nend\n"
	assert.Equal(t, want, got)
	assert.Equal(t, doc, replaceSection(doc, "nova-none", "x"), "a tool with no markers changes nothing")
}

// TestUsageVerbsReadTheUsageBlock: verbs are the leading bare words of the
// usage block's lines that begin with the tool's name, nested paths kept,
// cut at a flag, placeholder or bracket group and at the description
// column; help is left out, each path once, and an unindented line ends
// the block.
func TestUsageVerbsReadTheUsageBlock(t *testing.T) {
	t.Parallel()

	banner := strings.Join([]string{
		"nova-x: does things",
		"",
		"usage:",
		"  nova-x help",
		"  nova-x send <to> [--max N]",
		"  nova-x lift quarantine --force",
		"  nova-x version    print this build identity",
		"  nova-x send again",
		"  nova-x send <to>",
		"  nova-x [flags]",
		"  nova-x <path>",
		"  nova-x recv | nova-x peek",
		"  other-tool foo",
		"",
		"flags:",
		"  nova-x notverb",
		"",
	}, "\n")
	assert.Equal(t, []string{"send", "lift quarantine", "version", "send again", "recv"}, usageVerbs(banner, "nova-x"))
	assert.Empty(t, usageVerbs("nova-x: no usage block\n  nova-x send\n", "nova-x"))
}

// TestFirstColumnAndVerbWords: two or more blanks open the description
// column and one never does; a verb path ends at the first word holding a
// flag dash, an angle bracket, a square bracket, a pipe or a parenthesis.
func TestFirstColumnAndVerbWords(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in, column string
		words      []string
	}{
		{"version    print this", "version", []string{"version"}},
		{"lift quarantine --force", "lift quarantine --force", []string{"lift", "quarantine"}},
		{"send <to>  deliver", "send <to>", []string{"send"}},
		{"a b [c]", "a b [c]", []string{"a", "b"}},
		{"a (b)", "a (b)", []string{"a"}},
		{"a | b", "a | b", []string{"a"}},
		{"", "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.column, firstColumn(tc.in))
			assert.Equal(t, tc.words, verbWords(firstColumn(tc.in)))
		})
	}
}

// TestTypedLinesKeepTypedSections: line 1 and every section opened by a
// section word stay with their indented lines; a free-text paragraph is
// dropped; blank runs collapse to one and the edges are trimmed.
func TestTypedLinesKeepTypedSections(t *testing.T) {
	t.Parallel()

	text := strings.Join([]string{
		"nova-x send: deliver a note",
		"",
		"This paragraph is prose the hand-written docs carry.",
		"It runs two lines.",
		"",
		"",
		"usage:",
		"  nova-x send <to>",
		"",
		"flags:",
		"  --max N   widen the listing",
		"more prose after flags",
		"exit codes:",
		"  0 done",
		"effect: writes one note",
		"example: nova-x send a",
		"from `nova-x help`",
		"Help: see the banner",
		"",
		"",
	}, "\n")
	want := strings.Join([]string{
		"nova-x send: deliver a note",
		"",
		"usage:",
		"  nova-x send <to>",
		"",
		"flags:",
		"  --max N   widen the listing",
		"exit codes:",
		"  0 done",
		"effect: writes one note",
		"example: nova-x send a",
		"from `nova-x help`",
		"Help: see the banner",
	}, "\n")
	assert.Equal(t, want, typedLines(text))
	assert.Equal(t, "one line", typedLines("one line\n\n\n"))
	for _, w := range []string{"usage:", "flags:", "exit codes", "effect:", "Help:", "example: ", "from `"} {
		assert.True(t, hasSectionWord(w+" tail"), w)
	}
	assert.False(t, hasSectionWord("usage"))
	assert.False(t, hasSectionWord("example:x"))
}

// TestFenceWidensOnAFenceRun: a body is fenced with three backticks, or
// four when any of its lines opens with a run of three or more.
func TestFenceWidensOnAFenceRun(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "```\nplain\n```", fence("plain"))
	assert.Equal(t, "````\nx\n```go\ny\n````", fence("x\n```go\ny"))
	assert.Equal(t, "```\n``two\n```", fence("``two"))
}

// TestCollapseBlanks: runs of blank lines become one; a single blank and
// the text around it are kept as they are.
func TestCollapseBlanks(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "a\n\nb\n", collapseBlanks([]string{"a", "", "", "", "b", ""}))
	assert.Equal(t, "", collapseBlanks(nil))
	assert.Equal(t, "", collapseBlanks([]string{"", ""}))
}
