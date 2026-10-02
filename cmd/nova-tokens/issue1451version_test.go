package main

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

// TestVersionsStrayArgumentRefusalNamesTheDoor is the site PR #1749 disclosed as owed:
// `nova-tokens version <stray>` is an invocation the tool could not run, so it exits 2
// and its one refusal line must end in the door. issue1451_test.go enumerates BARE verbs
// from the help banner, and a bare `version` is not a refusal at all -- it prints the
// version and exits 0 -- so the stray-argument path is outside that table. This asserts
// the door is the literal end of the line, appears exactly once, and that the refusal
// still says what was wrong.
func TestVersionsStrayArgumentRefusalNamesTheDoor(t *testing.T) {
	t.Parallel()

	r := invoke(t, "version", "extra")
	require.Equal(t, 2, r.exit, "exit %d, want 2\nstdout:\n%s\nstderr:\n%s", r.exit, r.stdout, r.stderr)
	line := strings.TrimSpace(r.stderr)
	require.True(t, strings.Contains(line, "takes no flags and no arguments"), "refusal no longer says what was wrong: %q", line)
	assert.True(t, strings.HasSuffix(line, "; run: nova-tokens help"), "refusal line has no door: %q", line)
	{
		got := strings.Count(line, "; run: nova-tokens help")
		assert.Equal(t, 1, got, "door appears %d times, want exactly once: %q", got, line)
	}
}
