package main

// TestThePlainestFirstPersonAbsolutesAreCaught pins the fix for nova-tools #1468: the three plainest first-person
// absolutes were filed as a corpus miss on 2026-09-19, and the 2026-09-19 ruling
// was to record the miss rather than widen the grammar. The third attempt at a fix
// (2026-09-22) overrides that ruling and adds a detector for the three shapes so
// they are no longer a known miss.
//
// THIS TEST FAILS ON BASE (the three lines return claims=0, the bug is present)
// AND PASSES AT HEAD (a detector catches at least one line, the bug is fixed).
// Reverting the production change returns it to the `before:` behaviour, which is
// the control the card names.
import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestThePlainestFirstPersonAbsolutesAreCaught(t *testing.T) {
	t.Parallel()

	const path = "testdata/corpus/1468-plain-absolutes.md"

	body, err := os.ReadFile(path)
	require.NoError(t, err, "the #1468 corpus row is missing: %v", err)

	var stdout, stderr bytes.Buffer
	got := run([]string{path}, &stdout, &stderr)
	assert.Equal(t, 1, got, "want exit 1 (at least one of the three plainest absolutes is caught), got %d\nstdout: %s\nstderr: %s",
		got, stdout.String(), stderr.String())
	assert.NotContains(t, stdout.String(), "claims=0", "the bug is reproduced: stdout = %q, want claims > 0", stdout.String())
	assert.Contains(t, stderr.String(), "SELFTALK FAIL", "want at least one SELFTALK FAIL line for one of the three lines, got stderr = %q", stderr.String())

	for _, specimen := range []string{
		"I cannot ever get this right.\n",
		"I always break the build.\n",
		"Nothing I do works.\n",
	} {
		assert.Contains(t, string(body), specimen, "the specimen line is gone: %q", strings.TrimSuffix(specimen, "\n"))
	}
}
