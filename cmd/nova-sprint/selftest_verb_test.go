package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSelftestGroupAndVerbHelp(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)

	// Bare group refuses naming its verbs
	code, _, errs := ta.do("selftest")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "selftest wants one of its verbs")
	assert.Contains(t, errs, "selftest land")

	// Help for group
	code, out, _ := ta.do("help selftest")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "selftest land")

	// Verb -h
	code, out, _ = ta.do("selftest land -h")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "nova-sprint selftest land")
	assert.Contains(t, out, "--binary")
	assert.Contains(t, out, "--scratch-dir")
}

func TestSelftestLandRunsAndFailsOnBrokenLander(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)

	// Create a mock "broken" binary script
	dir := t.TempDir()
	brokenBin := filepath.Join(dir, "broken-lander.sh")
	require.NoError(t, os.WriteFile(brokenBin, []byte("#!/bin/sh\nexit 1\n"), 0o755))

	code, _, errs := ta.do("selftest land --binary " + brokenBin)
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "selftest land FAILED")
}

func TestSelftestLandRunsAndPassesOnGoodBinary(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)

	scratch := filepath.Join(t.TempDir(), "nonexistent", "scratch")
	code, out, errs := ta.do("selftest land --scratch-dir " + scratch)
	assert.Equal(t, 0, code, "stdout: %s\nstderr: %s", out, errs)
	assert.Contains(t, out, "SELFTEST LAND OK")
}
