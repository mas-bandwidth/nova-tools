package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestKindHelpListsVerbsAndFieldsAndFieldFlagsTakeHyphenAliases pins the two
// findings a reader named: a kind named alone prints its verbs and every
// schema field flag with its type (docs/STANDARD.md, section 3, points 1 and
// 6), and each underscore field flag has a hyphenated alias that sets the
// same value (the same schema the add and set flags register from,
// kindverbs.go).
func TestKindHelpListsVerbsAndFieldsAndFieldFlagsTakeHyphenAliases(t *testing.T) {
	t.Parallel()
	h := newHarness()

	// A kind named alone is the group of its verbs: its help names add and
	// history and the friend schema's --width with its type, and reads or
	// dials nothing.
	code, help, errs := h.run(t, "friend", "-h")
	require.Equal(t, 0, code, "friend -h: exit %d, stderr %q", code, errs)
	assert.Empty(t, errs, "friend -h wrote to stderr")
	for _, want := range []string{"add", "history", "--width"} {
		assert.Contains(t, help, want, "friend -h does not name %q:\n%s", want, help)
	}

	// --token-cap is the hyphenated alias of --token_cap: both spellings set
	// the same field to the same value, as the dry run's planned row shows.
	h.dir = t.TempDir()
	code, _, errs = h.run(t, "migrate", "--file", "try.json")
	require.Equal(t, 0, code, "migrate: %s", errs)
	assert.Empty(t, errs, "migrate: %q", errs)
	code, _, errs = h.run(t, "friend", "add", "bob", "--tiers", "flash", "--actor", "a1", "--file", "try.json")
	require.Equal(t, 0, code, "friend add: %s", errs)
	assert.Empty(t, errs, "friend add: %q", errs)

	setPlan := func(flag string) string {
		t.Helper()
		code, out, errs := h.run(t, "friend", "set", "bob", flag, "5", "--dry-run", "--actor", "a1", "--file", "try.json")
		require.Equal(t, 0, code, "friend set %s: %s", flag, errs)
		return out
	}
	viaUnderscore, viaHyphen := setPlan("--token_cap"), setPlan("--token-cap")
	require.NotEmpty(t, viaUnderscore, "the dry run planned nothing")
	assert.Equal(t, viaUnderscore, viaHyphen, "--token-cap and --token_cap do not set the same value")
}
