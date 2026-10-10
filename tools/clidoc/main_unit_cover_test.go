package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestClidocMainCoverMarkerTools pins markerTools: the tools a document's begin
// markers name, in file order, each once, and a marker that does not name a
// nova tool is not a marker at all.
func TestClidocMainCoverMarkerTools(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		doc  string
		want []string
	}{
		{
			name: "named once each in file order",
			doc: "intro\n<!-- clidoc:begin nova-a -->\na\n" +
				"<!-- clidoc:begin nova-b -->\nb\n" +
				"<!-- clidoc:begin nova-a -->\na again\n" +
				"<!-- clidoc:begin other-tool -->\nnot ours\n",
			want: []string{"nova-a", "nova-b"},
		},
		{
			name: "no marker at all",
			doc:  "prose with no marker\n<!-- clidoc:begin other-tool -->\n",
			want: nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, markerTools(tc.doc))
		})
	}
}

// TestClidocMainCoverReplaceSection pins replaceSection: only the named tool's
// block is rewritten, the prose before, between and after and the other tool's
// block stay byte for byte, and a document without that tool's markers is left
// unchanged.
func TestClidocMainCoverReplaceSection(t *testing.T) {
	t.Parallel()

	const doc = "before\n" +
		"<!-- clidoc:begin nova-a -->\nold a\n<!-- clidoc:end nova-a -->\n" +
		"between\n" +
		"<!-- clidoc:begin nova-b -->\nold b\n<!-- clidoc:end nova-b -->\n" +
		"after\n"
	const ref = "\nnew a\n"
	want := "before\n" +
		"<!-- clidoc:begin nova-a -->\nnew a\n<!-- clidoc:end nova-a -->\n" +
		"between\n" +
		"<!-- clidoc:begin nova-b -->\nold b\n<!-- clidoc:end nova-b -->\n" +
		"after\n"

	assert.Equal(t, want, replaceSection(doc, "nova-a", ref))
	assert.Equal(t, doc, replaceSection(doc, "nova-c", ref))
}

// TestClidocMainCoverUsageVerbs pins usageVerbs: the verb paths a banner's usage
// block names, in order, with the nested path kept whole, the description
// column cut off, help and a repeated verb left out, another tool's line
// ignored, and nothing read once the block ends at its first unindented line.
func TestClidocMainCoverUsageVerbs(t *testing.T) {
	t.Parallel()

	const banner = "nova-a keeps a ledger of records\n" +
		"\n" +
		"usage:\n" +
		"  nova-a start\n" +
		"  nova-a stop    stop it\n" +
		"  nova-a lift quarantine    take a record out of quarantine\n" +
		"  nova-a version    print the build identity\n" +
		"  nova-a help    show help\n" +
		"  nova-a start    run it again\n" +
		"  nova-b status    another tool\n" +
		"the prose after the usage block\n" +
		"  nova-a hidden    never read\n"

	assert.Equal(t, []string{"start", "stop", "lift quarantine", "version"}, usageVerbs(banner, "nova-a"))
	assert.Nil(t, usageVerbs("nova-a has no usage block\n", "nova-a"))
}

// TestClidocMainCoverFirstColumn pins firstColumn: a run of two or more blanks
// separates the command from its description column and one gap never does.
func TestClidocMainCoverFirstColumn(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "two blanks cut", in: "version    print this build identity", want: "version"},
		{name: "one blank stays", in: "lift quarantine", want: "lift quarantine"},
		{name: "no gap", in: "status", want: "status"},
		{name: "flag after one blank", in: "start --json", want: "start --json"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, firstColumn(tc.in))
		})
	}
}

// TestClidocMainCoverVerbWords pins verbWords: the leading bare words, stopping
// at the first flag, placeholder, group, pipe or parenthesis.
func TestClidocMainCoverVerbWords(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want []string
	}{
		{name: "plain verb", in: "status", want: []string{"status"}},
		{name: "nested verb", in: "lift quarantine", want: []string{"lift", "quarantine"}},
		{name: "stops at flag", in: "status --json", want: []string{"status"}},
		{name: "stops at placeholder", in: "show <name>", want: []string{"show"}},
		{name: "stops at group", in: "list [all]", want: []string{"list"}},
		{name: "stops at pipe", in: "get a|b", want: []string{"get"}},
		{name: "stops at paren", in: "run (fast)", want: []string{"run"}},
		{name: "only a flag", in: "--json", want: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, verbWords(tc.in))
		})
	}
}

// TestClidocMainCoverTypedLines pins typedLines: line 0 and the typed sections
// and their indented lines are kept, a free-text paragraph and its indented
// tail are dropped, and a run of blank lines never leaves two in a row.
func TestClidocMainCoverTypedLines(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		text string
		want string
	}{
		{
			name: "keeps typed sections and drops prose",
			text: "nova-a start — the banner\n" +
				"\n" +
				"free-text paragraph line\n" +
				"  its indented continuation\n" +
				"usage:\n" +
				"  nova-a start [--json]\n" +
				"flags:\n" +
				"  --json    output JSON\n" +
				"\n" +
				"\n" +
				"exit codes\n" +
				"  0   done\n" +
				"\n" +
				"effect: starts nova-a\n",
			want: "nova-a start — the banner\n" +
				"\n" +
				"usage:\n" +
				"  nova-a start [--json]\n" +
				"flags:\n" +
				"  --json    output JSON\n" +
				"\n" +
				"exit codes\n" +
				"  0   done\n" +
				"\n" +
				"effect: starts nova-a",
		},
		{
			name: "only line 0 with no section",
			text: "only a banner\nfree text follows\n",
			want: "only a banner",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, typedLines(tc.text))
		})
	}
}

// TestClidocMainCoverHasSectionWord pins hasSectionWord: true for each line
// prefix that opens a typed section and false for prose.
func TestClidocMainCoverHasSectionWord(t *testing.T) {
	t.Parallel()

	for _, w := range sectionWords {
		assert.True(t, hasSectionWord(w+" some text"), "prefix %q", w)
	}
	for _, prose := range []string{"usage", "the usage: later", "help", "narrate the flags: here", ""} {
		assert.False(t, hasSectionWord(prose), "prose %q", prose)
	}
}

// TestClidocMainCoverCollapseBlanks pins collapseBlanks: a run of blank lines
// folds to one and non-blank lines keep their place.
func TestClidocMainCoverCollapseBlanks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   []string
		want string
	}{
		{name: "a run of three folds to one", in: []string{"a", "", "", "b"}, want: "a\n\nb"},
		{name: "a run of four folds to one", in: []string{"a", "", "", "", "b"}, want: "a\n\nb"},
		{name: "a single blank stays", in: []string{"a", "", "b"}, want: "a\n\nb"},
		{name: "no blanks", in: []string{"a", "b"}, want: "a\nb"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, collapseBlanks(tc.in))
		})
	}
}

// TestClidocMainCoverFence pins fence: three backticks for plain text and four
// when any line opens with a run of three or more.
func TestClidocMainCoverFence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "plain text", body: "hello", want: "```\nhello\n```"},
		{name: "a fence line widens", body: "a\n```\nb", want: "````\na\n```\nb\n````"},
		{name: "a fence with a language widens", body: "```go\nx", want: "````\n```go\nx\n````"},
		{name: "two backticks stay", body: "``\nx", want: "```\n``\nx\n```"},
		{name: "empty body", body: "", want: "```\n\n```"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, fence(tc.body))
		})
	}
}
