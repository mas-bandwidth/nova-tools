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
		t.Fatalf("%v\n(docs/STANDARD.md, onboarding point 5(c))", err)
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
	firstRun, err := firstRunSection(md)
	if err != nil {
		return "", err
	}
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

// firstRunSection is the text of docs/CLI.md's nova-bus `### First run`, up to the next
// `### ` heading.
func firstRunSection(md string) (string, error) {
	section, ok := onboarding.Section(md, "nova-bus")
	if !ok {
		return "", fmt.Errorf("docs/CLI.md has no `## nova-bus` section")
	}
	_, firstRun, ok := strings.Cut(section, onboarding.FirstRunHeading+"\n")
	if !ok {
		return "", fmt.Errorf("docs/CLI.md's `## nova-bus` has no %s", onboarding.FirstRunHeading)
	}
	firstRun, _, _ = strings.Cut(firstRun, "\n### ")
	return firstRun, nil
}

// documentedSetup returns the `sh` block docs/CLI.md's nova-bus `### First run` opens
// with: the whole of what a reader does, from a source checkout, before the first
// nova-bus line. The tests build their bus by running exactly this, so a prerequisite
// the page does not write is one the tests do not have either. It is an error when the
// block is missing, or gives the bus no `origin` to push to, because the sitting's
// `send` and `--advance` lines push there.
func documentedSetup(md string) (string, error) {
	firstRun, err := firstRunSection(md)
	if err != nil {
		return "", err
	}
	_, tail, ok := strings.Cut(firstRun, "\n```sh\n")
	if !ok {
		return "", fmt.Errorf("docs/CLI.md's nova-bus %s opens with no ```sh setup block; a reader has nothing to run before the first nova-bus line", onboarding.FirstRunHeading)
	}
	block, _, ok := strings.Cut(tail, "\n```\n")
	if !ok {
		return "", fmt.Errorf("docs/CLI.md's nova-bus %s setup block is not closed", onboarding.FirstRunHeading)
	}
	if strings.Contains(firstRun[:strings.Index(firstRun, "\n```sh\n")], "\n$ ") {
		return "", fmt.Errorf("docs/CLI.md's nova-bus %s runs a nova-bus line before its setup block", onboarding.FirstRunHeading)
	}
	for _, want := range []string{"cp -R cmd/nova-bus/testdata/example-bus ", " remote add origin ", " push "} {
		if !strings.Contains(block, want) {
			return "", fmt.Errorf("docs/CLI.md's nova-bus %s setup block carries no %q; without it the sitting's `send` line is refused:\n%s", onboarding.FirstRunHeading, strings.TrimSpace(want), block)
		}
	}
	return block, nil
}

// documentedReply returns what docs/CLI.md's `reply` paragraph has a reader run after
// the first run: the one-line body step (an `echo ... > reply.md` code span), the lines
// of the fenced block under it, and the note id its `reply` line names after `--re`.
// That id is the page's sample; a reader puts their own in its place, and so does the
// test that runs the block.
func documentedReply(md string) (body string, lines []string, re string, err error) {
	section, ok := onboarding.Section(md, "nova-bus")
	if !ok {
		return "", nil, "", fmt.Errorf("docs/CLI.md has no `## nova-bus` section")
	}
	_, tail, ok := strings.Cut(section, "\n**`reply`**")
	if !ok {
		return "", nil, "", fmt.Errorf("docs/CLI.md's `## nova-bus` has no **`reply`** paragraph")
	}
	prose, rest, ok := strings.Cut(tail, "\n```\n")
	if !ok {
		return "", nil, "", fmt.Errorf("docs/CLI.md's **`reply`** paragraph is followed by no fenced block")
	}
	for _, m := range inlineCode.FindAllStringSubmatch(prose, -1) {
		if strings.HasPrefix(m[1], "echo ") && strings.HasSuffix(m[1], "> reply.md") {
			if body != "" {
				return "", nil, "", fmt.Errorf("docs/CLI.md's **`reply`** paragraph writes reply.md twice")
			}
			body = m[1]
		}
	}
	if body == "" {
		return "", nil, "", fmt.Errorf("docs/CLI.md's **`reply`** paragraph writes no body to reply.md (an `echo ... > reply.md` code span) before its reply line reads it")
	}
	block, _, _ := strings.Cut(rest, "\n```\n")
	lines = strings.Split(block, "\n")
	for _, l := range lines {
		if !strings.HasPrefix(l, "nova-bus reply ") {
			continue
		}
		f := strings.Fields(l)
		for i := range f[:len(f)-1] {
			if f[i] == "--re" {
				re = f[i+1]
			}
		}
	}
	if re == "" {
		return "", nil, "", fmt.Errorf("docs/CLI.md's **`reply`** block holds no `nova-bus reply ... --re <id>` line")
	}
	return body, lines, re, nil
}

// The unit-tier half of the setup and reply runs in firstrun_functional_test.go: the
// page carries a setup that gives the bus an origin, and a reply step that writes its
// body before it runs.
func TestTheCommandReferenceFirstRunWritesItsSetupAndReply(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "CLI.md"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := documentedSetup(string(raw)); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := documentedReply(string(raw)); err != nil {
		t.Fatal(err)
	}
}
