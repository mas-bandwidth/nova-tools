package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// An --out directory that does not exist is refused with the act that makes it,
// not with the tool's help: the one remedy line is "run: mkdir -p <out>", so the
// next turn is a paste and not a search through the banner.
func TestFoldRefusesAMissingOutWithTheMkdirRemedy(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tr := mkdir(t, filepath.Join(dir, "tr"))
	out := filepath.Join(dir, "missing", "out")
	r := invoke(t, "fold", "--out", out, "--day", "2026-09-11", "--repos", reposFile(t, dir), "--claude", "bench="+tr)
	wantExit(t, r, 2)
	refusal := lineWith(r.stderr, "TOKENS REFUSED")
	assert.Contains(t, refusal, "run: mkdir -p "+out, "the refusal does not offer the act that makes --out:\n%s", refusal)
	assert.NotContains(t, refusal, "run: nova-tokens help")
}
