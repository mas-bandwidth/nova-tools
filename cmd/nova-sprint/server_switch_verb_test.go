package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServerGroupAndSwitchHelp(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)

	// Bare group refuses naming its verbs
	code, _, errs := ta.do("server")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "server wants one of its verbs")
	assert.Contains(t, errs, "server switch")

	// Help for group
	code, out, _ := ta.do("help server")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "server switch")

	// Verb -h
	code, out, _ = ta.do("server switch -h")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "nova-sprint server switch")
	assert.Contains(t, out, "--rollback")
	assert.Contains(t, out, "--window")
}

func TestServerSwitchVerbSwitchesAndRollsBack(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)

	dir := t.TempDir()
	target := filepath.Join(dir, "nova-sprint")
	candidate := filepath.Join(dir, "nova-sprint-candidate")

	require.NoError(t, os.WriteFile(target, []byte("version-1"), 0o755))
	require.NoError(t, os.WriteFile(candidate, []byte("version-2"), 0o755))

	// Refuses with no args and no --rollback
	code, _, errs := ta.do("server switch")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "wants <binary> [--rollback] or --rollback alone")

	// Switch to candidate with rollback enabled
	code, out, _ := ta.do("server switch " + candidate + " --target " + target + " --rollback")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "SERVER SWITCH OK")

	// Verify target is now version-2 and target.prev is version-1
	content, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "version-2", string(content))

	prevContent, err := os.ReadFile(target + ".prev")
	require.NoError(t, err)
	assert.Equal(t, "version-1", string(prevContent))

	// Explicit rollback command
	code, out, _ = ta.do("server switch --target " + target + " --rollback")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "SERVER SWITCH ROLLED BACK")

	restored, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "version-1", string(restored))
}
