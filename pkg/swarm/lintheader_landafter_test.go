package swarm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLintHeaderStrandedReadsLandAfter pins LAND-AFTER: as a card header key (nova-sprint
// c69945c7, soft dependencies): in the header it is read and draws no finding, and a
// LAND-AFTER: line below the first prose line is one stranded-key finding naming it.
func TestLintHeaderStrandedReadsLandAfter(t *testing.T) {
	t.Parallel()
	inHeader := []byte("RESULT: c1 sha=000000000000 tier: flash\n" +
		"KIND: fix\n" +
		"LAND-AFTER: a\n" +
		"PATHS: internal/x.go\n" +
		"\n" +
		"Fix c1.\n")
	got := LintHeaderStranded(inHeader)
	require.Empty(t, got, "a LAND-AFTER in the header drew %+v; want no finding", got)

	below := []byte("RESULT: c1 sha=000000000000 tier: flash\n" +
		"KIND: fix\n" +
		"PATHS: internal/x.go\n" +
		"\n" +
		"Fix c1.\n" +
		"LAND-AFTER: a\n")
	got = LintHeaderStranded(below)
	require.Len(t, got, 1, "a stranded LAND-AFTER drew %+v; want one finding", got)
	assert.Equal(t, 6, got[0].Line, "the stranded LAND-AFTER's line")
	assert.Contains(t, got[0].Excerpt, "LAND-AFTER: on line 6", "the excerpt names the key and its line")
}
