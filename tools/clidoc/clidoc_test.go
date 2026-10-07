package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/testbin"
)

func TestExtractUsage(t *testing.T) {
	t.Parallel()

	input := `nova-sample: a demonstration tool

how it works: runs a demo.

usage:
  nova-sample run --target <t> [--fast]
  nova-sample verify --report <f>

exit codes: 0 ok, 1 failed, 2 bad invocation.

example:
  nova-sample run --target all
`
	got := ExtractUsage(input)
	want := "nova-sample run --target <t> [--fast]\nnova-sample verify --report <f>"
	assert.Equal(t, want, got)
}

func TestExtractUsageMultipleHeaders(t *testing.T) {
	t.Parallel()

	input := `nova-ci: tests and verification

usage, in any Go module (no state):
  nova-ci slowtests [--budget <s>]
  nova-ci functional <pkg>...

usage, in a nova-tools checkout:
  nova-ci local [--functional]

exit codes: 0 ok, 1 failed
`
	got := ExtractUsage(input)
	want := "nova-ci slowtests [--budget <s>]\nnova-ci functional <pkg>...\nnova-ci local [--functional]"
	assert.Equal(t, want, got)
}

func TestExtractUsageStopsAtSectionAndFlags(t *testing.T) {
	t.Parallel()

	input := `nova-sample: tool

usage:
  nova-sample run --file <f>

  --file <f>  the file path
`
	got := ExtractUsage(input)
	want := "nova-sample run --file <f>"
	assert.Equal(t, want, got)
}

func TestExtractUsageLiftsNote(t *testing.T) {
	t.Parallel()

	input := `usage:
  nova-cairn open --store <dir>
  nova-cairn NOTE: --publish is a recorded word, nothing more.
  nova-cairn append --store <dir>
`
	assert.Equal(t, "nova-cairn open --store <dir>\nnova-cairn append --store <dir>", ExtractUsage(input))
	assert.Equal(t, "nova-cairn NOTE: --publish is a recorded word, nothing more.", ExtractUsageNotes(input))
}

func TestUpdateDocLiftsNoteOutsideFence(t *testing.T) {
	t.Parallel()

	doc := "## nova-cairn\n\n<!-- clidoc:begin nova-cairn -->\n```\nold\n```\n<!-- clidoc:end nova-cairn -->\n"
	usage := "nova-cairn open --store <dir>\nnova-cairn append --store <dir>"
	notes := "nova-cairn NOTE: --publish is a recorded word, nothing more."
	updated, err := UpdateDoc(doc, "nova-cairn", usage, notes)
	require.NoError(t, err)

	fence := "```\n" + usage + "\n```\n"
	assert.Contains(t, updated, fence)
	assert.Contains(t, updated, fence+notes+"\n<!-- clidoc:end nova-cairn -->")
	open := strings.Index(updated, "```\n")
	require.GreaterOrEqual(t, open, 0)
	rest := updated[open+len("```\n"):]
	closeRel := strings.Index(rest, "\n```\n")
	require.GreaterOrEqual(t, closeRel, 0)
	inside := rest[:closeRel]
	assert.Equal(t, usage, inside)
	assert.NotContains(t, inside, "NOTE:")
}

func TestUpdateDoc(t *testing.T) {
	t.Parallel()

	doc := "# Reference\n\n## nova-sample\n\nIntro paragraph here.\n\n<!-- clidoc:begin nova-sample -->\n```\nold stale usage\n```\n<!-- clidoc:end nova-sample -->\n\nWorked example:\n```sh\nnova-sample run\n```\n"

	usage := "nova-sample run --target <t>\nnova-sample verify"
	updated, err := UpdateDoc(doc, "nova-sample", usage, "")
	require.NoError(t, err)

	assert.Contains(t, updated, "Intro paragraph here.")
	assert.Contains(t, updated, "<!-- clidoc:begin nova-sample -->\n```\nnova-sample run --target <t>\nnova-sample verify\n```\n<!-- clidoc:end nova-sample -->")
	assert.Contains(t, updated, "Worked example:\n```sh\nnova-sample run\n```\n")
	assert.NotContains(t, updated, "old stale usage")
}

func TestUpdateDocMissingMarker(t *testing.T) {
	t.Parallel()

	doc := "## nova-sample\n\nNo markers here\n"
	_, err := UpdateDoc(doc, "nova-sample", "usage", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing marker")
}

func TestDiscoveredVerbs(t *testing.T) {
	t.Parallel()

	helpText := `usage:
  nova-sample run --target <t>
  nova-sample verify check --report <f>
  nova-sample help [<verb>]
  nova-sample version
  nova-sample [inspect] [--local]
  nova-sample scan [flags] <file>...
  nova-sample friend sync install --every <duration>
  nova-sample install server|member [--dry-run]

example:
  nova-sample scan fixture
`
	verbs := DiscoveredVerbs("nova-sample", helpText)
	assert.Equal(t, []string{"run", "verify check", "help", "version", "inspect", "scan", "friend sync install", "install server", "install member"}, verbs)
}

func TestProcessAllCheckOnlyDetectsDrift(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	require.NoError(t, os.MkdirAll(binDir, 0o755))

	fakeScript := `#!/bin/sh
if [ "$1" = "help" ]; then
  cat <<'EOF'
usage:
  nova-sample run --flag
EOF
  exit 0
fi
if [ "$1" = "run" ] && [ "$2" = "-h" ]; then
  echo "usage: nova-sample run --flag"
  exit 0
fi
exit 0
`
	require.NoError(t, testbin.WriteExecutable(filepath.Join(binDir, "nova-sample"), []byte(fakeScript), 0o755))

	docPath := filepath.Join(dir, "CLI.md")
	docStale := "## nova-sample\n\n<!-- clidoc:begin nova-sample -->\n```\nnova-sample old\n```\n<!-- clidoc:end nova-sample -->\n"
	require.NoError(t, os.WriteFile(docPath, []byte(docStale), 0o644))

	ok, diff, err := ProcessAll(context.Background(), docPath, binDir, true)
	require.NoError(t, err)
	assert.False(t, ok, "checkOnly should return false when doc is stale")
	assert.Contains(t, diff, "nova-sample section in")

	// Now run update
	ok, _, err = ProcessAll(context.Background(), docPath, binDir, false)
	require.NoError(t, err)
	assert.True(t, ok)

	// Now checkOnly should pass
	ok, _, err = ProcessAll(context.Background(), docPath, binDir, true)
	require.NoError(t, err)
	assert.True(t, ok, "checkOnly should return true when doc is up to date")
}

// A repeated or reversed marker must not leave a stale block outside the check.
func TestUpdateDocRejectsMalformedMarkers(t *testing.T) {
	t.Parallel()
	begin := "<!-- clidoc:begin nova-sample -->"
	end := "<!-- clidoc:end nova-sample -->"
	for _, tc := range []struct{ name, doc string }{
		{"duplicate begin", begin + begin + end},
		{"duplicate end", begin + end + end},
		{"reversed", end + begin},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := UpdateDoc(tc.doc, "nova-sample", "nova-sample run", "")
			require.Error(t, err)
		})
	}
}

// A command named inside a deeper prose continuation is not a declared verb.
func TestDiscoveredVerbsIgnoresIndentedContinuationCommands(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, indent string }{
		{"two spaces", "  "},
		{"four spaces", "    "},
		{"tab", "\t"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			help := "usage:\n" + tc.indent + "nova-sample install <kind> [--dry-run]\n" +
				tc.indent + "                    (writes the unit; its loop runs\n" +
				tc.indent + "                     nova-sample mirror as a managed loop row.)\n" +
				tc.indent + "nova-sample mirror --repos <names>\n" +
				tc.indent + "nova-sample slots take --store <dir>\n"
			assert.Equal(t, []string{"install", "mirror", "slots take"}, DiscoveredVerbs("nova-sample", help))
			assert.Contains(t, ExtractUsage(help), "nova-sample mirror as a managed loop row.)", "prose stays in the reference")
		})
	}
}
