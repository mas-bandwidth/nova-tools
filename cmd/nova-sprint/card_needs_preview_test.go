package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// A card with a thousand needs prints them as one short line: how many are
// still open of how many, the first few, and the rest counted.
func TestCardNeedsPreview(t *testing.T) {
	t.Parallel()
	needs := make([]sprint.NeedState, 1000)
	for i := range needs {
		st := sprint.Merging
		if i%4 == 0 {
			st = string(sprint.Landed)
		}
		needs[i] = sprint.NeedState{ID: fmt.Sprintf("a-%d", i), State: st}
	}
	got := needsPreview(needs)
	if len(got) >= 300 || !strings.HasPrefix(got, "needs 750 of 1000 still open: a-1 (merging), a-2 (merging), a-3 (merging), ") ||
		!strings.HasSuffix(got, ", ... and 992 more") {
		t.Fatalf("%d bytes: %s", len(got), got)
	}
}
