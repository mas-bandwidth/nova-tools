package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The provider-key verb is the one verb that adds a provider key to the fleet:
// the owner enters the key and presses enter, and the rest happens
// (tla/ProviderKey.tla, OnePromptOnePR). These tests drive the verb in process:
// no binary is built, no store is opened and no value is read.

func providerKeyRun(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(args, strings.NewReader(""), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// TestProviderKeyAddRefusesWhatItCannotRun is the refusal rule: a verb with no
// name, a verb with no NAME and a flag the verb does not take are all refused
// with a remedy, and none of them opens the store.
func TestProviderKeyAddRefusesWhatItCannotRun(t *testing.T) {
	t.Parallel()

	t.Run("the group without a verb names the verb", func(t *testing.T) {
		t.Parallel()
		code, _, stderr := providerKeyRun(t, "provider-key")
		assert.Equal(t, 2, code, "a group without a verb must be refused")
		assert.Contains(t, stderr, "run: nova-secrets provider-key add -h", "the refusal names no remedy: %s", stderr)
	})

	t.Run("add without a NAME is refused", func(t *testing.T) {
		t.Parallel()
		code, _, stderr := providerKeyRun(t, "provider-key", "add")
		assert.Equal(t, 2, code, "add without a NAME must be refused")
		assert.Contains(t, stderr, "NAME", "the refusal does not say what it wants: %s", stderr)
	})

	t.Run("a flag the verb does not take is refused", func(t *testing.T) {
		t.Parallel()
		code, _, stderr := providerKeyRun(t, "provider-key", "add", "MINIMAX_API_KEY", "--no-such-flag-breadcrumb")
		assert.Equal(t, 2, code, "a flag the verb does not take must be refused")
		assert.Contains(t, stderr, "run:", "the refusal carries no remedy: %s", stderr)
	})
}

// TestProviderKeyAddHelpStatesItsEffect is the help rule: the verb's help names
// what it does, the flags it takes and the seat it needs, and it touches nothing.
func TestProviderKeyAddHelpStatesItsEffect(t *testing.T) {
	t.Parallel()

	code, stdout, stderr := providerKeyRun(t, "provider-key", "add", "-h")
	require.Equal(t, 0, code, "the verb's help must exit 0: %s", stderr)
	help := stdout
	assert.Contains(t, help, "effect: store write", "the help states no effect:\n%s", help)
	assert.Contains(t, help, "--provider", "the help omits a flag:\n%s", help)
	assert.Contains(t, help, "--nova-config", "the help omits a flag:\n%s", help)
	assert.Contains(t, help, "one pull request", "the help does not say what the verb makes:\n%s", help)
}

// TestProviderKeyAddNeverPrintsAValue is the leak rule: the verb's own refusals
// carry names and seats, never a value.
func TestProviderKeyAddNeverPrintsAValue(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	code := run([]string{"provider-key", "add", "MINIMAX_API_KEY", "--store", t.TempDir(), "--as", "ada", "--key", "/nonexistent/key", "--sops", "/nonexistent/sops"}, strings.NewReader("sk-a-value-that-must-not-be-printed\n"), &stdout, &stderr)
	assert.NotEqual(t, 0, code, "the run with no store must not succeed")
	assert.NotContains(t, stdout.String(), "sk-a-value-that-must-not-be-printed", "the value reached stdout:\n%s", stdout.String())
	assert.NotContains(t, stderr.String(), "sk-a-value-that-must-not-be-printed", "the value reached stderr:\n%s", stderr.String())
}
