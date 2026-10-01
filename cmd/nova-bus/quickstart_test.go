package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQuickstartHelp(t *testing.T) {
	t.Parallel()
	h := invoke(t, "", "quickstart", "-h").mustCode(t, 0)
	assert.True(t, strings.HasPrefix(h.stdout, "usage: nova-bus quickstart"))
	assert.Contains(t, h.stdout, "--dir")
	assert.Empty(t, h.stderr)

	help := invoke(t, "", "quickstart", "--help").mustCode(t, 0)
	assert.Equal(t, h.stdout, help.stdout)

	verbHelp := invoke(t, "", "help", "quickstart").mustCode(t, 0)
	assert.Equal(t, h.stdout, verbHelp.stdout)
}

func TestQuickstartEndToEndWithDir(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "demo-bus")
	res := invoke(t, "", "quickstart", "--dir", dir).mustCode(t, 0)
	assert.Empty(t, res.stderr)

	// Verify all 4 command steps and their outputs are printed in sequence.
	steps := []struct {
		cmd string
		out string
	}{
		{"$ nova-bus send --bus " + dir + " --file " + filepath.Join(dir, ".nova-bus", "draft.md") + " --as Ada --remote origin --branch main", "SEND OK "},
		{"$ nova-bus inbox --bus " + dir + " --as Bo --receipt-max-words 40", "INBOX OK "},
		{"$ nova-bus reply --bus " + dir + " --as Bo --re ", "REPLY OK "},
		{"$ nova-bus close --bus " + dir + " --as Ada --before ", "CLOSE OK "},
	}

	for _, s := range steps {
		assert.Contains(t, res.stdout, s.cmd)
		assert.Contains(t, res.stdout, s.out)
	}

	lines := strings.Split(strings.TrimSpace(res.stdout), "\n")
	last := lines[len(lines)-1]
	assert.Equal(t, "BUS QUICKSTART OK dir="+dir+" steps=4", last)

	// The created bus must be valid and pass check --full.
	check := invoke(t, "", "check", "--bus", dir, "--full").mustCode(t, 0)
	assert.Contains(t, check.stdout, "BUS OK ")
}

func TestQuickstartDefaultTempDir(t *testing.T) {
	t.Parallel()
	res := invoke(t, "", "quickstart").mustCode(t, 0)
	assert.Empty(t, res.stderr)

	assert.Contains(t, res.stdout, "$ nova-bus send --bus ")
	assert.Contains(t, res.stdout, "SEND OK ")
	assert.Contains(t, res.stdout, "$ nova-bus inbox --bus ")
	assert.Contains(t, res.stdout, "INBOX OK ")
	assert.Contains(t, res.stdout, "$ nova-bus reply --bus ")
	assert.Contains(t, res.stdout, "REPLY OK ")
	assert.Contains(t, res.stdout, "$ nova-bus close --bus ")
	assert.Contains(t, res.stdout, "CLOSE OK ")

	lines := strings.Split(strings.TrimSpace(res.stdout), "\n")
	last := lines[len(lines)-1]
	assert.True(t, strings.HasPrefix(last, "BUS QUICKSTART OK dir="))
	assert.True(t, strings.HasSuffix(last, " steps=4"))
}

func TestQuickstartRefusesNonEmptyDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	err := os.WriteFile(filepath.Join(dir, "existing.txt"), []byte("already here"), 0o644)
	require.NoError(t, err)
	res := invoke(t, "", "quickstart", "--dir", dir).mustCode(t, 2)
	assert.Contains(t, res.stderr, "is not empty")
}

func TestQuickstartRefusesPositionalArgs(t *testing.T) {
	t.Parallel()
	res := invoke(t, "", "quickstart", "extra").mustCode(t, 2)
	assert.Contains(t, res.stderr, "takes no positional arguments")
}
