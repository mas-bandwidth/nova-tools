package ci

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// reachOf ignores Transcript calls where the heading argument is not a string literal.
func TestReachOfIgnoresNonStringTranscriptHeading(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := `package sample

func TestDummy(t *testing.T) {
	onboarding.Transcript(t, "mytool", 42)
}
`
	err := os.WriteFile(filepath.Join(dir, "sample_test.go"), []byte(src), 0o644)
	require.NoError(t, err)

	reach, found, err := reachOf(dir, "TestDummy")
	require.NoError(t, err)
	require.True(t, found, "TestDummy should be found in package")
	assert.Empty(t, reach.transcripts, "Transcript with non-string heading must not be recorded")
}
