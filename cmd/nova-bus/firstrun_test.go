package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

// The exemption is gone, and it stays gone: a `### First run` for nova-bus exists in
// the document the walk reads, and no entry in internal/ci's skip list (onboarding_functional_test.go) names this
// tool. Asserted here as well as there because the skip list is one map entry away
// from being back, and the tool the README sends people to first is the worst one to
// excuse.
func TestNovaBusIsNotExemptFromTheOnboardingStandard(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := onboarding.FirstRun(string(raw), "nova-bus"); err != nil {
		t.Fatalf("%v\n(docs/ONBOARDING.md point 5(c))", err)
	}
	// The walk is a functional test since #4372 (it builds and runs every command).
	walk, err := os.ReadFile(filepath.Join("..", "..", "internal", "ci", "onboarding_functional_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	body, _, ok := strings.Cut(string(walk), "func TestEveryCommandMeetsTheOnboardingStandard")
	if !ok {
		t.Fatal("internal/ci/onboarding_functional_test.go no longer holds the walk this test is about")
	}
	if strings.Contains(body, `"nova-bus"`) {
		t.Error("internal/ci/onboarding_functional_test.go's skip list names nova-bus again; the first run it was waiting for is in docs/TESTS.md and is executed by firstrun_functional_test.go")
	}
}

// The documented first sitting's draft line and send line, as docs/CLI.md writes them.
const (
	cliFirstRunDraft = "$ nova-bus draft --bus ./bus --as Bo --to Ada --subject gate > draft.md"
	cliFirstRunSend  = "$ nova-bus send --bus ./bus --file draft.md --as Bo --remote origin --branch main"
)

// inlineCode is a markdown code span on one line.
var inlineCode = regexp.MustCompile("`([^`\n]+)`")

// documentedDraftEdit returns the one step docs/CLI.md's nova-bus `### First run`
// writes between `draft` and `send`: a `sed` code span, in the prose between the two
// fenced blocks, that writes a body over the draft's placeholder. It is an error when
// the page carries none, carries more than one, or carries it anywhere but between the
// draft line and the send line -- each of which is a page a stranger cannot follow.
func documentedDraftEdit(md string) (string, error) {
	section, ok := onboarding.Section(md, "nova-bus")
	if !ok {
		return "", fmt.Errorf("docs/CLI.md has no `## nova-bus` section")
	}
	_, firstRun, ok := strings.Cut(section, onboarding.FirstRunHeading+"\n")
	if !ok {
		return "", fmt.Errorf("docs/CLI.md's `## nova-bus` has no %s", onboarding.FirstRunHeading)
	}
	firstRun, _, _ = strings.Cut(firstRun, "\n### ")
	draftAt := strings.Index(firstRun, cliFirstRunDraft+"\n")
	sendAt := strings.Index(firstRun, cliFirstRunSend+"\n")
	if draftAt < 0 || sendAt < 0 || sendAt < draftAt {
		return "", fmt.Errorf("docs/CLI.md's nova-bus %s does not run\n  %s\nand then\n  %s", onboarding.FirstRunHeading, cliFirstRunDraft, cliFirstRunSend)
	}
	var edits []string
	for _, m := range inlineCode.FindAllStringSubmatchIndex(firstRun, -1) {
		span := firstRun[m[2]:m[3]]
		if strings.HasPrefix(span, "sed ") && strings.Contains(span, bus.PlaceholderBody) && m[0] > draftAt && m[1] < sendAt {
			edits = append(edits, span)
		}
	}
	if len(edits) != 1 {
		return "", fmt.Errorf("docs/CLI.md's nova-bus %s writes %d `sed` step(s) over %q between\n  %s\nand\n  %s\nwant exactly one: `send` refuses a draft whose body is still the placeholder (SEND FAIL, exit 1), so a page without that step cannot be followed as written", onboarding.FirstRunHeading, len(edits), bus.PlaceholderBody, cliFirstRunDraft, cliFirstRunSend)
	}
	return edits[0], nil
}

// The unit-tier half of TestTheCommandReferenceFirstRunRunsAsWritten: without git, the
// page is held to carrying the step that makes its `send` line possible. The functional
// test RUNS that step; this one fails in a second when the step is gone.
func TestTheCommandReferenceFirstRunWritesTheNoteBeforeSend(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "CLI.md"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := documentedDraftEdit(string(raw)); err != nil {
		t.Fatal(err)
	}
}
