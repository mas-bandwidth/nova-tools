package main

import (
	"context"
	"os"
	"path/filepath"
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

func TestUpdateDoc(t *testing.T) {
	t.Parallel()

	doc := "# Reference\n\n## nova-sample\n\nIntro paragraph here.\n\n<!-- clidoc:begin nova-sample -->\n```\nold stale usage\n```\n<!-- clidoc:end nova-sample -->\n\nWorked example:\n```sh\nnova-sample run\n```\n"

	usage := "nova-sample run --target <t>\nnova-sample verify"
	updated, err := UpdateDoc(doc, "nova-sample", usage)
	require.NoError(t, err)

	assert.Contains(t, updated, "Intro paragraph here.")
	assert.Contains(t, updated, "<!-- clidoc:begin nova-sample -->\n```\nnova-sample run --target <t>\nnova-sample verify\n```\n<!-- clidoc:end nova-sample -->")
	assert.Contains(t, updated, "Worked example:\n```sh\nnova-sample run\n```\n")
	assert.NotContains(t, updated, "old stale usage")
}

func TestUpdateDocMissingMarker(t *testing.T) {
	t.Parallel()

	doc := "## nova-sample\n\nNo markers here\n"
	_, err := UpdateDoc(doc, "nova-sample", "usage")
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
`
	verbs := DiscoveredVerbs("nova-sample", helpText)
	assert.Equal(t, []string{"run", "verify check", "help", "version"}, verbs)
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
