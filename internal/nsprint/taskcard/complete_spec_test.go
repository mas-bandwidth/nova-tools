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

// TestFromDoneWhenReadsFlagsWithValues is the fix round's item 4 on
// nova-tools#4401: a DONE-WHEN's go test is read as go test reads it, so a
// flag's value (-p 2, -tags functional, -timeout 30s) is never the package,
// -tags travels to the TEST line, and a command without a ./ package or a
// single -run test implies none.
func TestFromDoneWhenReadsFlagsWithValues(t *testing.T) {
	t.Parallel()
	for dw, want := range map[string]string{
		"`go test -p 2 ./internal/x -run TestY` passes":                                  "./internal/x TestY",
		"`go test -tags functional ./internal/nsprint/card -run TestCopyWrapper` passes": "-tags functional ./internal/nsprint/card TestCopyWrapper",
		"nice -n 15 go test -p 2 -count=1 -tags=functional ./x/ -run '^TestZ$' passes":   "-tags functional ./x/ TestZ",
		"`go test -run TestY -timeout 30s ./x` passes":                                   "./x TestY",
		"go test -v -race ./x -run=TestQ, then the receipt":                              "./x TestQ",
		"go test ./x -run TestY.":                                                        "./x TestY",
		"go test ./x passes; go test ./y -run TestW passes":                              "./y TestW",
		"`go test -p 2 ./x` passes":                                                      "none",
		"go test ./x -run 'TestA|TestB'":                                                 "none",
		"`go test -tags 'a b' ./x -run TestY`":                                           "none",
		"the page reads right":                                                           "none",
	} {
		if got := taskcard.TestFromDoneWhen(dw); got != want {
			t.Errorf("TestFromDoneWhen(%q) = %q, want %q", dw, got, want)
		}
	}
}
