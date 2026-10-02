//go:build unix

package update

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// snapshot lists the rows it records, and --dry-run takes the same reads and
// writes no --out (STANDARD §2, "a verb that writes has a dry run").
func TestSnapshotListsItsRowsAndDryRunWritesNothing(t *testing.T) {
	t.Parallel()
	bin := t.TempDir()
	stub := filepath.Join(bin, "nova-stub")
	require.NoError(t, os.WriteFile(stub, []byte("#!/bin/sh\nprintf 'nova-stub v1.0.0 linux/amd64 go1.0\\n'\n"), 0o755))
	out := filepath.Join(t.TempDir(), "s.tsv")

	code, stdout, stderr := runTool(t, "nova-version", "snapshot", "--bin", bin, "--out", out, "--dry-run")
	assert.Equal(t, 0, code, stderr)
	assert.Contains(t, stdout, "SNAPSHOT OK bin="+bin)
	assert.Contains(t, stdout, "dry_run=true")
	assert.Contains(t, stdout, "SNAPSHOT ROW name=nova-stub stamp=v1.0.0 revision=- platform=linux/amd64")
	assert.NoFileExists(t, out)

	code, stdout, stderr = runTool(t, "nova-version", "snapshot", "--bin", bin, "--out", out)
	assert.Equal(t, 0, code, stderr)
	assert.Contains(t, stdout, "SNAPSHOT ROW name=nova-stub")
	assert.FileExists(t, out)
}
