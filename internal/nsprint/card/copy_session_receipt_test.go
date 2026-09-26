package card_test

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
)

// TestCopySessionFailureLinesNameCopyCauseOutcomeAndNext (finding 3 of
// stella-e6353bf80360): one line per copy that could not start, with the
// copy, the cause, whether the give-back took it and where the primary went
// or that its state is unknown with the inspect and repair verbs, and the
// next action; none when every copy started.
func TestCopySessionFailureLinesNameCopyCauseOutcomeAndNext(t *testing.T) {
	t.Parallel()
	s := card.CopySession{Enrolled: true, Failed: []card.CopyFailure{
		{Copy: "p~1", Why: "card launch: wrapper /x/nova-card MISSING", To: "waiting", GaveBack: true},
		{Copy: "p~2", Why: "wrapper acknowledgement timed out"},
	}}
	lines := s.FailureLines("b")
	if len(lines) != 2 {
		t.Fatalf("lines %q", lines)
	}
	for _, want := range []string{"SESSION bench:b copy=p~1 launch FAILED: card launch: wrapper /x/nova-card MISSING; given back (primary to waiting); next: fix the wrapper", "then deal the primary again: nova-sprint card deal --to bench:b --ids p --actor bench:b; the next beat or `card session --as bench:b` then takes the new copy"} {
		if !strings.Contains(lines[0], want) {
			t.Fatalf("line %q lacks %q", lines[0], want)
		}
	}
	for _, want := range []string{"copy=p~2 launch FAILED: wrapper acknowledgement timed out; give back FAILED: the copy may still be working on bench:b", "nova-sprint card render --id p~2", "nova-sprint card cancel --actor bench:b --why launch --id p~2"} {
		if !strings.Contains(lines[1], want) {
			t.Fatalf("line %q lacks %q", lines[1], want)
		}
	}
	if got := (card.CopySession{Enrolled: true, Launched: []card.CopyLaunch{{Copy: "p~3"}}}).FailureLines("b"); len(got) != 0 {
		t.Fatalf("a clean session has failure lines %q", got)
	}
}
