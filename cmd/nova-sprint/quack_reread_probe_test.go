package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A quack call is one step: a generated card id that the store will refuse
// must keep every other stream in that call unchanged.
func TestQuackRereadRefusesOverlongGeneratedIDAtomically(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	before := ta.applies()
	stream := strings.Repeat("b", 128)
	code, out, errs := ta.do("quack --streams a," + stream + " --count 1 --repo https://example.com/quack.git")
	assert.NotEqual(t, 0, code, out+errs)
	assert.Equal(t, before, ta.applies(), "a refused pass must add no cards: %s%s", out, errs)
}

// Teardown removes the operation receipt and the epoch, so init starts again
// at epoch zero. Reusing an op after that lifecycle must still name new files
// because the test repository keeps files from the earlier pass.
func TestQuackRereadTeardownInitSameOpUsesNewFileNames(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	const line = "quack --streams a,b --count 1 --repo https://example.com/quack.git --op reused-after-teardown"
	first, _ := ta.quackCards(line)
	require.Len(t, first, 2)
	ta.ok("teardown --confirm sprint")
	ta.ok("init --readers reader-a,reader-b --members m1")
	second, _ := ta.quackCards(line)
	require.Len(t, second, 2)
	for _, id := range second {
		assert.False(t, slices.Contains(first, id), "%s reuses a file path still present in the test repository", id)
	}
}

// Each store begins at epoch zero. The same caller operation may legitimately
// be used in two independent sprints that target the same test repository.
func TestQuackRereadIndependentStoresSameOpUseNewFileNames(t *testing.T) {
	t.Parallel()
	const line = "quack --streams a,b --count 1 --repo https://example.com/quack.git --op shared-operation-name"
	firstStore := newTestApp(t)
	firstStore.ok("init --readers reader-a,reader-b --members m1")
	first, _ := firstStore.quackCards(line)
	require.Len(t, first, 2)
	secondStore := newTestApp(t)
	secondStore.ok("init --readers reader-a,reader-b --members m1")
	second, _ := secondStore.quackCards(line)
	require.Len(t, second, 2)
	for _, id := range second {
		assert.False(t, slices.Contains(first, id), "%s repeats across independent sprint stores that target the same repository", id)
	}
}
