package buildinfo

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestVersionNamesTheVerbSetGeneration pins the fact every version line gains
// (docs/SPEC-VERSION.md): the number of verbs and the generation hash of their
// sorted names. The generation is over the SORTED set, so two binaries whose
// helps list the same verbs in different orders carry the same hash, and one
// added verb is a new generation. The line writer and reader are one pair, so
// the facts are asserted through pkg/buildinfo's own Line and Parse.
func TestVersionNamesTheVerbSetGeneration(t *testing.T) {
	t.Parallel()

	verbs := []string{"snapshot", "moved", "diff", "version"}
	n, hash := VerbSet(verbs)
	require.Equal(t, len(verbs), n, "the count is the size of the set")
	require.Len(t, hash, 12, "the generation hash is 12 hex digits, the length a short revision uses")

	// The same verb list in another order is the same generation.
	shuffled := []string{"diff", "version", "snapshot", "moved"}
	nSame, hashSame := VerbSet(shuffled)
	assert.Equal(t, n, nSame)
	assert.Equal(t, hash, hashSame, "two binaries with the same verb set share the generation, whatever order their helps list it in")

	// One added verb is a different generation.
	nMore, hashMore := VerbSet(append([]string{"report"}, verbs...))
	assert.Equal(t, n+1, nMore)
	assert.NotEqual(t, hash, hashMore, "one added verb changes the generation")

	// The version line carries both facts as named extras a reader asks for by name.
	line := Line("nova-version", "v1.3.0", VerbSetExtras(verbs)...)
	fields, ok := Parse(line)
	require.Truef(t, ok, "the line pkg/buildinfo wrote is one pkg/buildinfo.Parse refuses: %s", line)
	gotN, ok := fields.Extra("verbs")
	require.True(t, ok, "the version line names the verb count")
	assert.Equal(t, strconv.Itoa(n), gotN)
	gotHash, ok := fields.Extra("verbset")
	require.True(t, ok, "the version line names the verb set generation")
	assert.Equal(t, hash, gotHash)
}
