package taskcard_test

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
)

// TestSwarmCardIsRefusedWithoutATest is nova-tools#4313 at push: a swarm
// card whose DONE-WHEN names no `go test <pkg> -run <TestName>`, and that
// carries no TEST line or a bare TEST: none, is incomplete, named with the
// remedy; one that names its test, or says why it has none, is complete. A
// friend card is not held to it here (its copy is, by the wrapper).
func TestSwarmCardIsRefusedWithoutATest(t *testing.T) {
	t.Parallel()

	sha := strings.Repeat("a", 40)
	spec := func(text string) taskcard.Spec {
		s := taskcard.ParseIssue("ROUTE: flash\nBASE: dev\nbase-sha: " + sha + "\nPATHS: internal/x\n" + text)
		return s
	}
	for _, text := range []string{
		"DONE-WHEN: the page reads right",
		"DONE-WHEN: the page reads right\nTEST: none",
		"DONE-WHEN: the page reads right\nTEST: rm -rf /",
	} {
		s := spec(text)
		missing := s.Complete("mas-bandwidth/nova-tools#4313", "")
		if len(missing) != 1 || !strings.HasPrefix(missing[0], "TEST (") || !strings.Contains(missing[0], "TEST: none <why") {
			t.Errorf("%q: Complete = %v, want one TEST entry with the remedy", text, missing)
		}
	}
	for _, text := range []string{
		"DONE-WHEN: `go test ./internal/x -run TestY` passes",
		"DONE-WHEN: the page reads right\nTEST: ./internal/x TestY",
		"DONE-WHEN: the page reads right\nTEST: none one docs page; the reader checks it",
	} {
		s := spec(text)
		if missing := s.Complete("mas-bandwidth/nova-tools#4313", ""); len(missing) != 0 {
			t.Errorf("%q: Complete = %v, want complete", text, missing)
		}
		if s.Test == "none" || s.Test == "" {
			t.Errorf("%q: TEST = %q", text, s.Test)
		}
	}
	s := taskcard.ParseIssue("ROUTE: friend\nDONE-WHEN: the page reads right")
	if missing := s.Complete("", ""); len(missing) != 0 {
		t.Errorf("a friend card: Complete = %v", missing)
	}
	if got := taskcard.TestFromDoneWhen("`go test ./cmd/nova-sprint/ -run TestCardCutWritesRecord` prints one --- PASS"); got != "./cmd/nova-sprint/ TestCardCutWritesRecord" {
		t.Errorf("TestFromDoneWhen = %q", got)
	}
	if got := taskcard.TestFromDoneWhen("the page reads right"); got != "none" {
		t.Errorf("TestFromDoneWhen = %q", got)
	}
}
