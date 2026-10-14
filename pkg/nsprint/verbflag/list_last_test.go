package verbflag

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestListPrintsTheFinalNameWhenOnlyOneMoreRemains pins the fix that a list one
// longer than ListMax prints every name: cutting the last name saves three
// words and may hide the flag the reader wanted, so one run no longer hides it
// (docs/STANDARD.md §3 point 2: one run reports every problem it can find).
func TestListPrintsTheFinalNameWhenOnlyOneMoreRemains(t *testing.T) {
	t.Parallel()
	var names []string
	for i := range ListMax + 1 {
		names = append(names, "--f"+strconv.Itoa(i))
	}
	last := "--f" + strconv.Itoa(ListMax)

	got := List(names)
	assert.Equal(t, strings.Join(names, ", "), got)
	assert.Contains(t, got, last)
	assert.NotContains(t, got, "more")

	longer := make([]string, ListMax+2)
	for i := range longer {
		longer[i] = "--f" + strconv.Itoa(i)
	}
	assert.True(t, strings.HasSuffix(List(longer), ", --f"+strconv.Itoa(ListMax-1)+" and 2 more"), List(longer))
}
