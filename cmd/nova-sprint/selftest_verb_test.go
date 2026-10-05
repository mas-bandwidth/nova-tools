package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSelftestLandVerbHelp(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)

	// Verb -h
	code, out, _ := ta.do("selftest land -h")
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

func TestSelftestLandRunsAndFailsOnNoopBinary(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)

	dir := t.TempDir()
	noopBin := filepath.Join(dir, "noop-lander.sh")
	require.NoError(t, os.WriteFile(noopBin, []byte("#!/bin/sh\nexit 0\n"), 0o755))

	code, _, errs := ta.do("selftest land --binary " + noopBin)
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "selftest land FAILED")
	assert.Contains(t, errs, "landing commit not found in origin main")
}

func TestSelftestLandRunsAndSucceedsOnGoodBinary(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)

	dir := t.TempDir()
	goodBin := filepath.Join(dir, "good-lander.sh")
	script := `#!/bin/sh
if [ "$1" = "land" ]; then
	git merge -q --no-ff sprint/selftest-canned-1 -m "land selftest-1"
	git push -q origin main
fi
exit 0
`
	require.NoError(t, os.WriteFile(goodBin, []byte(script), 0o755))

	code, out, errs := ta.do("selftest land --binary " + goodBin)
	assert.Equal(t, 0, code, errs)
	assert.Contains(t, out, "SELFTEST LAND OK")
}
