package dogfood

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseCLIReadsTheVerbsAReferenceDeclares(t *testing.T) {
	t.Parallel()

	verbs, err := ParseCLI(filepath.Join("testdata", "cli-example.md"))
	require.NoError(t, err, "ParseCLI: %v", err)
	got := make([]string, 0, len(verbs))
	for _, v := range verbs {
		got = append(got, v.Key())
	}
	want := []string{
		"nova-example quickstart",
		"nova-example links",
		"nova-example help",
		"nova-example version",
		// A worked transcript declares: see the shape tests below, and the
		// dogfood pass that found nova-sandbox documented only this way.
		"nova-example transcript-only",
		"nova-fixture lift quarantine",
		"nova-fixture lift lockdown",
		"nova-fixture path",
		// A synopsis line with no verb declares the tool's bare invocation,
		// which is how `nova-decide --questions <file>` is run.
		"nova-fixture",
		"nova-example status",
		"nova-indented session start",
		"nova-indented session stop",
		"nova-indented query",
		"nova-indented version",
		"nova-indented help",
		"nova-transcript check",
		"nova-transcript probe",
		"nova-prose cut",
		"nova-prose serve",
	}
	require.Equal(t, strings.Join(want, "|"), strings.Join(got, "|"), "verbs:\n got %q\nwant %q", got, want)
}

// The document's order is the ledger's order, and the line number is how a
// reader finds the declaration the row came from.
func TestParseCLIKeepsTheDocumentsOrderAndTheDeclaringLine(t *testing.T) {
	t.Parallel()

	verbs, err := ParseCLI(filepath.Join("testdata", "cli-example.md"))
	require.NoError(t, err, "ParseCLI: %v", err)
	require.Equal(t, "quickstart", verbs[0].Verb, "first verb = %q, want quickstart", verbs[0].Verb)
	for i := 1; i < len(verbs); i++ {
		require.Greater(t, verbs[i].Line, 0, "%s carries no line number", verbs[i].Key())
	}
	// `links` is declared twice; the row is the first declaration.
	for _, v := range verbs {
		if v.Key() == "nova-example links" {
			require.Equal(t, 10, v.Line, "nova-example links declared at line %d, want the FIRST declaration (10)", v.Line)
		}
	}
}

func TestParseCLIRefusesTheShapesThatAreNotDeclarations(t *testing.T) {
	t.Parallel()

	verbs, err := ParseCLI(filepath.Join("testdata", "cli-example.md"))
	require.NoError(t, err, "ParseCLI: %v", err)
	for _, v := range verbs {
		switch v.Key() {
		case "nova-example ghost":
			require.Fail(t, "prose that names a verb declared it")
		case "nova-example version print", "nova-indented version print":
			require.Fail(t, fmt.Sprintf("a pasted help block's description ran into the verb: %q", v.Verb))
		case "nova-fixture lift lockdown REFUSED", "nova-fixture lift":
			require.Fail(t, fmt.Sprintf("the description after a flagless synopsis leaked into the verb: %q", v.Verb))
		}
		require.False(t, strings.HasPrefix(v.Verb, "-") || strings.Contains(v.Verb, "<"), "a flag or a placeholder became a verb: %q", v.Verb)
	}
}

func TestParseCLIRefusesAReferenceWithNoVerbs(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "empty.md")
	require.NoError(t, os.WriteFile(path, []byte("# Command reference\n\nNo verbs here.\n"), 0o644))
	_, err := ParseCLI(path)
	require.Error(t, err, "a reference declaring no verbs was accepted; a ledger over nothing says OK about nothing")
}

func TestParseCLIRefusesAMissingReference(t *testing.T) {
	t.Parallel()

	_, err := ParseCLI(filepath.Join(t.TempDir(), "nope.md"))
	require.Error(t, err, "a missing reference was accepted")
}

// The real reference is the one the gate will run on, so the parser is held to
// it here — loosely, on shape and not on a count that every doc change breaks.
func TestParseCLIReadsThisRepositorysOwnReference(t *testing.T) {
	t.Parallel()

	path := filepath.Join("..", "..", "docs", "CLI.md")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("docs/CLI.md not present: %v", err)
	}
	verbs, err := ParseCLI(path)
	require.NoError(t, err, "ParseCLI(docs/CLI.md): %v", err)
	require.GreaterOrEqual(t, len(verbs), 40, "docs/CLI.md parsed to %d verbs; the reference declares many more", len(verbs))
	want := map[string]bool{
		"nova-check links":          false,
		"nova-check corpus":         false,
		"nova-swarm native":         false,
		"nova-bus send":             false,
		"nova-fuse lift quarantine": false,
		"nova-version snapshot":     false,
		"nova-memory quickstart":    false,
	}
	tools := map[string]bool{}
	for _, v := range verbs {
		tools[v.Tool] = true
		if _, ok := want[v.Key()]; ok {
			want[v.Key()] = true
		}
	}
	for key, found := range want {
		assert.True(t, found, "docs/CLI.md declares %q and the parser missed it", key)
	}
	require.GreaterOrEqual(t, len(tools), 10, "found verbs for %d tools, want at least 10", len(tools))
}

// The dogfood pass of 2026-09-18, edge 2, and the biggest one: nova-sandbox and
// nova-work contributed ZERO of the 77 rows, because docs/CLI.md documents the
// first as prose with a worked transcript and the second by pasting its own
// indented help block. A tool goes un-dogfooded forever by being documented in
// a shape the extractor does not read, and nothing in the ledger says so.
func TestParseCLIReadsTheIndentedUsageBlockShape(t *testing.T) {
	t.Parallel()

	verbs, err := ParseCLI(filepath.Join("testdata", "cli-example.md"))
	require.NoError(t, err, "ParseCLI: %v", err)
	got := map[string]bool{}
	for _, v := range verbs {
		got[v.Key()] = true
	}
	for _, want := range []string{
		"nova-indented session start",
		"nova-indented session stop",
		"nova-indented query",
		"nova-indented version",
		"nova-indented help",
	} {
		assert.True(t, got[want], "an indented usage block declared %q and the parser missed it", want)
	}
	// The prose under `wire:` and `flags:` is indented too, and declares nothing.
	for _, never := range []string{"nova-indented one", "nova-indented the"} {
		assert.False(t, got[never], "indented prose declared a verb: %q", never)
	}
}

func TestParseCLIReadsTheTranscriptOnlyShape(t *testing.T) {
	t.Parallel()

	verbs, err := ParseCLI(filepath.Join("testdata", "cli-example.md"))
	require.NoError(t, err, "ParseCLI: %v", err)
	got := map[string]bool{}
	for _, v := range verbs {
		got[v.Key()] = true
	}
	for _, want := range []string{"nova-transcript check", "nova-transcript probe"} {
		assert.True(t, got[want], "a tool documented only by a transcript declared %q and the parser missed it", want)
	}
}

func TestParseCLIReadsTheVerbPerHeadingShape(t *testing.T) {
	t.Parallel()

	verbs, err := ParseCLI(filepath.Join("testdata", "cli-example.md"))
	require.NoError(t, err, "ParseCLI: %v", err)
	got := map[string]bool{}
	for _, v := range verbs {
		got[v.Key()] = true
	}
	for _, want := range []string{"nova-prose cut", "nova-prose serve"} {
		assert.True(t, got[want], "a heading declared %q and the parser missed it", want)
	}
	// A heading is a verb only when the heading IS the verb: a sentence that
	// happens to start in lower case is prose about the tool.
	for _, never := range []string{"nova-prose native and", "nova-prose native", "nova-prose the"} {
		assert.False(t, got[never], "a prose heading declared a verb: %q", never)
	}
}

// The class test the dogfood pass asked for: every tool the reference gives a
// section to yields at least one verb. A tool with a section and no rows is
// exactly the failure that hid nova-sandbox and nova-work. The tools asked are the ones
// whose cmd/ directory is in the tree: a section for a deleted binary (nova-pulse's,
// which says where its verbs went) has nothing to dogfood.
func TestEveryToolSectionOfTheRealReferenceYieldsAVerb(t *testing.T) {
	t.Parallel()

	path := filepath.Join("..", "..", "docs", "CLI.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("docs/CLI.md not present: %v", err)
	}
	verbs, err := ParseCLI(path)
	require.NoError(t, err, "ParseCLI: %v", err)
	withVerbs := map[string]bool{}
	for _, v := range verbs {
		withVerbs[v.Tool] = true
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(line, "## nova-") {
			continue
		}
		tool := strings.Fields(strings.TrimPrefix(line, "## "))[0]
		if _, err := os.Stat(filepath.Join("..", "..", "cmd", tool)); err != nil {
			continue
		}
		assert.True(t, withVerbs[tool], "docs/CLI.md gives %s a section and the ledger reads no verb out of it; that tool can never be dogfooded", tool)
	}
}

// TestParseHelpReadsAStageLineAsProse: a skeleton banner's line 2 names the
// tool and its stage; it is a sentence, not the verb "is", so the verb-help
// walk and the dogfood ledger never ask `<tool> is -h`.
func TestParseHelpReadsAStageLineAsProse(t *testing.T) {
	t.Parallel()
	help := "nova-work: every issue in one tree file\nnova-work is pre-alpha: not ready for production use.\n\nusage:\n  nova-work import --org <org>\n  nova-work verify --tree <tree.lisp>\n"
	var keys []string
	for _, v := range ParseHelp(help) {
		keys = append(keys, v.Key())
	}
	assert.Equal(t, []string{"nova-work import", "nova-work verify"}, keys)
}
