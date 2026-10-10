package main_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestInstallRequiresModelForOpencode verifies that when installing for the
// opencode harness, the --model flag is required. This ensures that after a
// friend AI moves to a new machine, the owner explicitly specifies the
// provider/model rather than accidentally preserving an old/stale provider.
func TestInstallRequiresModelForOpencode(t *testing.T) {
	t.Parallel()

	// opencode harness without --model should fail
	t.Run("opencode without model is refused", func(t *testing.T) {
		t.Parallel()
		out := cli.Do(t, "install", "--as", "bob", "--harness", "opencode", "--dir", "/w/bob", "--dry-run")
		assert.NotEqual(t, 0, out.Exit)
		assert.Contains(t, out.Err(), "--model")
		assert.Contains(t, out.Err(), "provider/model")
	})

	// opencode harness with --model should pass
	t.Run("opencode with model succeeds", func(t *testing.T) {
		t.Parallel()
		out := cli.Do(t, "install", "--as", "bob", "--harness", "opencode", "--dir", "/w/bob", "--model", "deepseek/deepseek-v4", "--dry-run")
		assert.Equal(t, 0, out.Exit)
	})

	// other harnesses should not require --model
	t.Run("other harnesses do not require model", func(t *testing.T) {
		t.Parallel()
		for _, harness := range []string{"codex", "dsh", "grok"} {
			out := cli.Do(t, "install", "--as", "bob", "--harness", harness, "--dir", "/w/bob", "--dry-run")
			assert.Equal(t, 0, out.Exit, "harness %s should not require --model", harness)
		}
	})
}
