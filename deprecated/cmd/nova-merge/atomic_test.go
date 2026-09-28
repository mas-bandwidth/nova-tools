package main

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

// The fake is a guard, not a recorder: a two-precondition merge that arrives without both
// preconditions is refused, so no test can pass branch (a) by accident.
func TestTheHostPrimitiveRefusesAMergeMissingAPrecondition(t *testing.T) {
	t.Parallel()
	h := merge.NewFakeHost()
	h.Atomic = true
	if err := h.Merge(1, strings.Repeat("a", 40), "", strings.Repeat("c", 40)); err == nil {
		t.Error("a merge with no expected base is not a two-precondition merge")
	}
	if err := h.Merge(1, "", strings.Repeat("b", 40), strings.Repeat("c", 40)); err == nil {
		t.Error("a merge with no expected head is not a two-precondition merge")
	}
	if err := h.Merge(1, strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)); err != nil {
		t.Errorf("both preconditions supplied and the fake refused: %v", err)
	}
}
