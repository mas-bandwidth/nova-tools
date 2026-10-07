package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A file the tool cannot read as text is not a clean one. A binary used to scan
// green -- `SELFTALK OK files=1 claims=0` -- because a byte page decoded into a
// string like any other. The unreadable-file law (docs/STANDARD.md section 2,
// "It never fails silently") makes it a refusal that names the file, so no
// binary can print an OK line.
func TestABinaryFileIsRefusedNotScannedGreen(t *testing.T) {
	t.Parallel()

	// 0xff is never a valid UTF-8 lead byte, so these bytes are not text.
	bin := filepath.Join(t.TempDir(), "bin.dat")
	require.NoError(t, os.WriteFile(bin, []byte{0xff, 0xfe, 0x00, 0x01, 0x80}, 0o644))

	var stdout, stderr bytes.Buffer
	got := run([]string{bin}, &stdout, &stderr)
	assert.Equal(t, 2, got, "a binary file is not a clean one; want exit 2, got %d\nstdout: %s", got, stdout.String())
	assert.Contains(t, stderr.String(), bin, "the refusal must name the file, got %q", stderr.String())
	assert.Contains(t, stderr.String(), "not valid UTF-8", "the refusal must say the file is not text, got %q", stderr.String())
	assert.NotContains(t, stdout.String(), "SELFTALK OK", "a binary file printed a green: %q", stdout.String())
	assert.Empty(t, stdout.String(), "a refused run prints nothing on stdout, got %q", stdout.String())
}
