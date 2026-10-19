package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTheUnknownVerbListNamesHelp pins the door for the tool's own help in
// the list an unknown verb is answered with (STANDARD section 2, the names
// there are for the reader).
func TestTheUnknownVerbListNamesHelp(t *testing.T) {
	t.Parallel()
	res := workMain(unreachable(t)).Run("frob")
	require.Equal(t, 2, res.Code, "unknown verb exit %d\n%s%s", res.Code, res.Stdout, res.Stderr)
	require.Contains(t, res.Stderr, `unknown verb "frob"`, res.Stderr)

	_, list, ok := strings.Cut(res.Stderr, "the verbs are ")
	require.True(t, ok, "the refusal names no verb list:\n%s", res.Stderr)
	list, _, _ = strings.Cut(list, ";")
	assert.Contains(t, strings.Split(list, ", "), "help", "the unknown-verb list omits help: %q", list)
}
