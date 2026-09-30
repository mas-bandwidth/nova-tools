package typedrec

import (
	"strings"
	"testing"
)

// TestResultFormatIsTheTwoLineContract: the brief is line 1 and line 2 (and,
// for a kind that needs one, the fields only the model can know); it never asks
// for a field the wrapper writes.
func TestResultFormatIsTheTwoLineContract(t *testing.T) {
	t.Parallel()

	for _, kind := range Kinds {
		f := ResultFormat(kind)
		if !strings.Contains(f, "line 1: this card's line 1, verbatim") || !strings.Contains(f, "`DONE`, `ABSTAIN <why>` or `BLOCKED <why>`") {
			t.Fatalf("KIND %s: no two-line contract:\n%s", kind, f)
		}
		for _, owned := range WrapperOwned {
			if strings.Contains(f, "`"+owned+":") {
				t.Errorf("KIND %s: the brief asks the model for %s, a field the wrapper writes:\n%s", kind, owned, f)
			}
		}
		fields, sections := JudgementFields(kind)
		for _, x := range fields {
			if !strings.Contains(f, "`"+x+": <value>`") {
				t.Errorf("KIND %s: the brief does not name %s, which only the model knows", kind, x)
			}
		}
		for _, s := range sections {
			if !strings.Contains(f, "`## "+s+"`") {
				t.Errorf("KIND %s: the brief does not name ## %s", kind, s)
			}
		}
	}
	if f, s := JudgementFields(KindFix); len(f) != 0 || len(s) != 0 {
		t.Fatalf("a fix card needs nothing from the model beyond two lines, got %v %v", f, s)
	}
}
