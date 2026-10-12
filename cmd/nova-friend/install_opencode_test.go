package main

import (
	"testing"
)

// TestInstallRequiresModelForOpencode verifies that when installing for the
// opencode harness, the --model flag is required. This ensures that after a
// friend AI moves to a new machine, the owner explicitly specifies the
// provider/model rather than accidentally preserving an old/stale provider.
func TestInstallRequiresModelForOpencode(t *testing.T) {
	t.Parallel()

	t.Run("opencode without model is refused", func(t *testing.T) {
		t.Parallel()
		r := newRig(t, "ada", "bob")
		r.cli().Do(t, "install", "--as", "bob", "--harness", "opencode", "--dir", "/w/bob", "--dry-run").
			Exit(2).Err("--model", "provider/model")
	})

	t.Run("opencode with model succeeds", func(t *testing.T) {
		t.Parallel()
		r := newRig(t, "ada", "bob")
		r.cli().Do(t, "install", "--as", "bob", "--harness", "opencode", "--dir", "/w/bob", "--model", "deepseek/deepseek-v4", "--dry-run").
			Exit(0)
	})

	t.Run("other harnesses do not require model", func(t *testing.T) {
		t.Parallel()
		for _, harness := range []string{"grok"} {
			harness := harness
			t.Run(harness, func(t *testing.T) {
				t.Parallel()
				r := newRig(t, "ada", "bob")
				r.cli().Do(t, "install", "--as", "bob", "--harness", harness, "--dir", "/w/bob", "--dry-run").
					Exit(0)
			})
		}
	})
}
