package docs

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
)

// The two spec drafts this file pins were written for Stella's and Johnny's
// read, and the thing a review can lose most easily is the SHAPE: a rule that
// loses its number cannot be referred to in a bus note, and a rule that loses
// its red test is a paragraph of prose rather than something a build can be
// held to. So these tests assert the numbering and the named red tests, not the
// wording — the wording is the reviewers' to change and the numbering is the
// contract their notes stand on.

// ruleNumber matches a numbered rule at the start of a line in the house style:
// `12. **The rule in bold.**`. The bold is part of the match because a plain
// numbered list item is a list, not a rule.
var ruleNumber = regexp.MustCompile(`(?m)^(\d+)\. \*\*`)

// windowsRuleNumber matches the Windows sandbox section's own numbering, W1
// through W12. It is a separate namespace on purpose: a rule of that section
// must never be confused with a rule of the platform-independent set, which is
// numbered 1 through 22 in the same file.
var windowsRuleNumber = regexp.MustCompile(`(?m)^W(\d+)\. \*\*`)

// TestSpecReleaseCarriesItsRulesAndRedTests pins docs/SPEC-RELEASE.md: the
// sections a reader navigates by, rules 1 through 27 each numbered and each
// naming at least one red test, the house closing headings, and the open
// questions the draft could not settle. Rules 4, 8, 13, 20, 21, 22, 24 and 25
// describe machinery that does not exist on dev at 220c05d7, and the draft says
// so in each of their own texts; the NOT BUILT marker is pinned here because a
// spec that quietly starts describing an unbuilt tool as if it were built is the
// failure this repository's doc tests exist to catch.
func TestSpecReleaseCarriesItsRulesAndRedTests(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile("../../docs/SPEC-RELEASE.md")
	if err != nil {
		t.Fatalf("docs/SPEC-RELEASE.md: %v", err)
	}
	spec := string(body)

	for _, want := range []string{
		"## What a release IS",
		"## What gates it",
		"## How it is cut, built, installed and adopted",
		"## How adoption is proven",
		"## How a bad release is undone",
		"## What a version number means for us",
		"## The release's definition of done",
		"## Says NO when",
		"## Refuses when",
		"## Deliberately does not",
		"## Open questions for Stella and Johnny",
	} {
		if !strings.Contains(spec, want) {
			t.Errorf("docs/SPEC-RELEASE.md is missing the section %q", want)
		}
	}

	// Every rule number from 1 to 27, in order, with none repeated and none
	// skipped: a spec whose rule 14 is missing is a spec whose reviewers cannot
	// agree on what rule 15 is.
	assertRuleRun(t, "docs/SPEC-RELEASE.md", ruleNumber, spec, 27)

	// Each rule names at least one red test, because "each rule with its red
	// test named" is the shape the drafts were asked for.
	assertEveryRuleNamesARedTest(t, "docs/SPEC-RELEASE.md", ruleNumber, spec)

	// The load-bearing claims, each in the words a later change would have to
	// argue with rather than merely reword.
	for _, want := range []string{
		"a tag on a green commit on `main`",
		"SHA256SUMS",
		"is recorded in the tag's annotation",
		"nova-check dogfood gate --cli docs/CLI.md --receipts <dir> --require-all",
		"hulk builds, the Studio adopts",
		"never a bare name on `$PATH`",
		"ForwardAgent yes",
		"`eval` remote output",
		"Keys never move between machines",
		"read from what the remote SAID, never from its exit code",
		"nova-update release install\n    --version <previous>",
		"refuses the live one",
		"`v0.MINOR.PATCH`",
		"`v0.16.0-dev.<sha>`",
		"A release is done at (6), not at (5).",
		// The draft is honest about what it proposes rather than describes.
		"NOT BUILT at `220c05d7`",
	} {
		if !strings.Contains(spec, want) {
			t.Errorf("docs/SPEC-RELEASE.md is missing the claim %q", want)
		}
	}

	// The open questions are lettered, because the pull request body refers to
	// them by letter and a reviewer answering "C" must find a C here.
	for _, letter := range []string{"A", "B", "C", "D", "E", "F", "G", "H"} {
		if !strings.Contains(spec, "- **"+letter+".") {
			t.Errorf("docs/SPEC-RELEASE.md is missing open question %s", letter)
		}
	}
}

// TestSpecSandboxCarriesTheWindowsPlace pins the Windows section added to
// docs/SPEC-SANDBOX.md. The section is the disposable PLACE on Windows — a Job
// Object and a per-run scratch, with Windows Sandbox as the one-off — and it is
// deliberately separate from the AppContainer section above it, which is the
// WALL. The two named nevers are pinned by name: WSL is never a backend and
// never a fallback, and a promise the platform cannot keep is written down as
// unkeepable rather than quietly made.
func TestSpecSandboxCarriesTheWindowsPlace(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile("../../docs/SPEC-SANDBOX.md")
	if err != nil {
		t.Fatalf("docs/SPEC-SANDBOX.md: %v", err)
	}
	spec := string(body)

	const heading = "## Windows — the disposable place"
	idx := strings.Index(spec, heading)
	if idx < 0 {
		t.Fatalf("docs/SPEC-SANDBOX.md is missing the section %q", heading)
	}
	// The section runs to the next top-level heading; every assertion below is
	// made against the section itself, so a phrase that happens to appear in the
	// darwin half cannot satisfy a Windows rule.
	section := spec[idx:]
	if end := strings.Index(section[len(heading):], "\n## "); end >= 0 {
		section = section[:len(heading)+end]
	}

	// The wall section above must still be there and must still be the wall:
	// this section adds a place, it does not replace AppContainer.
	if !strings.Contains(spec[:idx], "## Windows — AppContainer, no admin") {
		t.Error("docs/SPEC-SANDBOX.md lost the AppContainer section: the Windows place is added beside the wall, never instead of it")
	}

	assertRuleRun(t, "the Windows section of docs/SPEC-SANDBOX.md", windowsRuleNumber, section, 12)
	assertEveryRuleNamesARedTest(t, "the Windows section of docs/SPEC-SANDBOX.md", windowsRuleNumber, section)

	for _, want := range []string{
		// The place, and the same contract as darwin.
		"CreateJobObjectW",
		"JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE",
		"JOB_OBJECT_LIMIT_BREAKAWAY_OK",
		"PROC_THREAD_ATTRIBUTE_JOB_LIST",
		"JOBOBJECT_CPU_RATE_CONTROL_INFORMATION",
		"SANDBOX DONE name=<n> exit=<code> wall=<s> freed=<bytes>",
		"SANDBOX LEAK",
		"exit **3**",
		"124",
		"reason=volume_exists",
		"reason=size_unenforceable",
		"reason=bad_scratch",
		// Windows Sandbox, and what it can and cannot be.
		"`.wsb`",
		"WindowsSandbox.exe",
		"reason=no_wsb",
		"reason=wsb_busy",
		"one instance at a time",
		// The never.
		"WSL is never the answer",
		"`reason=no_sandbox`",
		// Johnny's rulings.
		"QueryFullProcessImageNameW",
		"IsProcessInJob",
		"never as a file inside the scratch",
		"windowsIsNotABench(t)",
		// What cannot be promised.
		"There is no execute bit",
		"PATHEXT",
		"There is no `128+N`",
		"ERROR_SHARING_VIOLATION",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("the Windows section of docs/SPEC-SANDBOX.md is missing %q", want)
		}
	}

	// The run verb's own paragraph must point at this section, or a reader of
	// the darwin half is told every other platform refuses and stops there.
	if !strings.Contains(spec[:idx], "**Windows has its own place**") {
		t.Error("docs/SPEC-SANDBOX.md: the run verb section must point at the Windows place, since it otherwise reads as `every other platform REFUSES`")
	}
}

// assertRuleRun checks that the numbers a spec's rules carry are exactly 1..want
// in order, with nothing repeated and nothing skipped.
func assertRuleRun(t *testing.T, where string, re *regexp.Regexp, text string, want int) {
	t.Helper()
	found := re.FindAllStringSubmatch(text, -1)
	if len(found) != want {
		t.Errorf("%s carries %d numbered rules, want %d", where, len(found), want)
	}
	for i, m := range found {
		if got, expect := m[1], fmt.Sprint(i+1); got != expect {
			t.Errorf("%s: rule %d in document order is numbered %q, want %q", where, i+1, got, expect)
			return
		}
	}
}

// assertEveryRuleNamesARedTest checks that each numbered rule's own text names a
// red test. The block is the text from one rule's number to the next one's, so a
// red test named under rule 7 cannot stand in for rule 8's.
func assertEveryRuleNamesARedTest(t *testing.T, where string, re *regexp.Regexp, text string) {
	t.Helper()
	locs := re.FindAllStringSubmatchIndex(text, -1)
	for i, loc := range locs {
		end := len(text)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		block := text[loc[0]:end]
		if !strings.Contains(block, "Red test") {
			t.Errorf("%s: rule %s names no red test", where, text[loc[2]:loc[3]])
		}
	}
}
