//go:build unix

package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// snapshot lists the rows it records, and --dry-run takes the same reads and
// writes no --out (STANDARD §2, "a verb that writes has a dry run"; ledger X12).
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

// TestSnapshotRefusesABinEntryWhoseNameIsNotOneTSVField pins security#81 finding 1:
// snapshot writes a nova-* file name into the TSV, so a name holding a tab or
// newline would forge rows past the mixed-stamp gate. An entry whose name is not
// one TSV field is refused before running, naming the file escaped, and no --out
// is written; an ordinary bin still snapshots.
func TestSnapshotRefusesABinEntryWhoseNameIsNotOneTSVField(t *testing.T) {
	t.Parallel()
	bin := t.TempDir()
	good := filepath.Join(bin, "nova-good")
	require.NoError(t, os.WriteFile(good, []byte("#!/bin/sh\nprintf 'nova-good v1.0.0 linux/amd64 go1.0\\n'\n"), 0o755))

	badName := "nova-b\tv9.9.9\tdeadbeefcafe\tlinux-amd64\nnova-c"
	bad := filepath.Join(bin, badName)
	require.NoError(t, os.WriteFile(bad, []byte("#!/bin/sh\nprintf 'nova-b v1.0.0 linux/amd64 go1.0\\n'\n"), 0o755))

	out := filepath.Join(t.TempDir(), "s.tsv")
	code, stdout, stderr := runTool(t, "nova-version", "snapshot", "--bin", bin, "--out", out)
	assert.Equal(t, 2, code, "stdout: %s\nstderr: %s", stdout, stderr)
	assert.Contains(t, stderr, oneline.Escape(badName))
	assert.NoFileExists(t, out)

	// An ordinary bin still snapshots.
	binOrdinary := t.TempDir()
	ordinary := filepath.Join(binOrdinary, "nova-good")
	require.NoError(t, os.WriteFile(ordinary, []byte("#!/bin/sh\nprintf 'nova-good v1.0.0 linux/amd64 go1.0\\n'\n"), 0o755))
	outOrdinary := filepath.Join(t.TempDir(), "ordinary.tsv")
	code, _, stderr = runTool(t, "nova-version", "snapshot", "--bin", binOrdinary, "--out", outOrdinary)
	assert.Equal(t, 0, code, stderr)
	assert.FileExists(t, outOrdinary)
}

// TestSnapshotNotesASymlinkedNovaEntryItSkips pins security#81 finding 2:
// a symlinked nova-* entry in --bin is skipped with a note naming it and
// explaining why, while the snapshot still records the regular file.
// A bin without symlinks carries no such note.
func TestSnapshotNotesASymlinkedNovaEntryItSkips(t *testing.T) {
	t.Parallel()
	bin := t.TempDir()
	good := filepath.Join(bin, "nova-good")
	require.NoError(t, os.WriteFile(good, []byte("#!/bin/sh\nprintf 'nova-good v1.0.0 linux/amd64 go1.0\\n'\n"), 0o755))

	linked := filepath.Join(bin, "nova-linked")
	require.NoError(t, os.Symlink(good, linked))

	out := filepath.Join(t.TempDir(), "s.tsv")
	code, stdout, stderr := runTool(t, "nova-version", "snapshot", "--bin", bin, "--out", out)
	assert.Equal(t, 0, code, stderr)
	assert.Contains(t, stdout, "tools=1")
	assert.Contains(t, stdout, "SNAPSHOT ROW name=nova-good")
	assert.NotContains(t, stdout, "SNAPSHOT ROW name=nova-linked")
	assert.Contains(t, stdout, "SNAPSHOT NOTE "+oneline.Quote("nova-linked")+": symlink, not a regular file; not snapshotted")
	assert.FileExists(t, out)

	// A bin without symlinks has no such note.
	binClean := t.TempDir()
	cleanGood := filepath.Join(binClean, "nova-good")
	require.NoError(t, os.WriteFile(cleanGood, []byte("#!/bin/sh\nprintf 'nova-good v1.0.0 linux/amd64 go1.0\\n'\n"), 0o755))
	cleanOut := filepath.Join(t.TempDir(), "clean.tsv")
	code, stdout, stderr = runTool(t, "nova-version", "snapshot", "--bin", binClean, "--out", cleanOut)
	assert.Equal(t, 0, code, stderr)
	assert.Contains(t, stdout, "tools=1")
	assert.NotContains(t, stdout, "symlink, not a regular file; not snapshotted")
	assert.NotContains(t, stdout, "NOTE")
	assert.FileExists(t, cleanOut)
}

// A row whose source metadata is partial or contradictory (some but not all of the
// four source keys, dirty=maybe; a duplicate key never gets this far, buildinfo.Parse refuses
// the whole line) is noted by name and ignored by the
// mixed-source gate rather than refused; a row with none of the four keys, or all four
// well formed, gets no note (SPEC-VERSION item 6).
func TestSnapshotNotesARowWhoseSourceMetadataIsPartial(t *testing.T) {
	t.Parallel()
	bin := t.TempDir()
	stubs := map[string]string{
		"nova-partial": "repo=my/repo revision=abcdef123456",
		"nova-maybe":   "repo=my/repo revision=abcdef123456 dirty=maybe build_host=h",
		"nova-none":    "",
		"nova-whole":   "repo=my/repo revision=abcdef123456 dirty=false build_host=h",
	}
	for name, extras := range stubs {
		line := name + " v1.0.0 linux/amd64 go1.0 " + extras
		require.NoError(t, os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nprintf '"+line+"\\n'\n"), 0o755))
	}
	out := filepath.Join(t.TempDir(), "s.tsv")

	code, stdout, stderr := runTool(t, "nova-version", "snapshot", "--bin", bin, "--out", out)
	assert.Equal(t, 0, code, stderr)
	assert.Contains(t, stdout, "SNAPSHOT OK")
	for _, name := range []string{"nova-partial", "nova-maybe"} {
		assert.Contains(t, stdout, `NOTE partial source metadata: `+oneline.Quote(name))
		assert.Contains(t, stdout, "SNAPSHOT ROW name="+name)
	}
	assert.Equal(t, 2, strings.Count(stdout, "NOTE partial source metadata"), stdout)
	assert.NotContains(t, stdout, oneline.Quote("nova-none"))
	assert.NotContains(t, stdout, oneline.Quote("nova-whole"))
}
