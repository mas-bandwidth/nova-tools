package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// STEP 4 pins the standard's looks-at-nothing rule for check: a verb that reads files is
// FAILED when it read none, with --allow-empty the one word that says the empty --out is
// deliberate. Before this change an empty --out was already FAILED, but the way out it
// named was a fold, and --allow-empty did not exist.
func TestCheckOnAnEmptyOutFailsUnlessAllowEmpty(t *testing.T) {
	t.Parallel()

	out := mkdir(t, filepath.Join(t.TempDir(), "out"))

	r := invoke(t, "check", "--out", out)
	wantExit(t, r, 1)
	assert.Contains(t, r.stdout, "CHECK FAILED")
	assert.Contains(t, r.stdout, "looked at nothing")
	assert.Contains(t, r.stdout, "nova-tokens check --out "+out+" --allow-empty")

	allowed := invoke(t, "check", "--out", out, "--allow-empty")
	wantExit(t, allowed, 0)
	assert.Contains(t, allowed.stdout, "CHECK OK")
	assert.Contains(t, allowed.stdout, "files=0 rows=0")
	assert.Empty(t, allowed.stderr)
}
