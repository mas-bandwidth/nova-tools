package swarm

import (
	"strings"
	"testing"
)

// EMMA'S ITEM-4 DOGFOOD, TURNED INTO TESTS (#1853, #1854).
//
// Emma dogfooded `nova-pulse cut` and `nova-swarm lint --card` on darwin/arm64 against
// dev@0878987f and returned NOT CLEAN with 17 defects. The ones that are this lint's are
// below, each named for what it lets through. They are all one family: the typed-header
// checks were written as a restatement of a parser that is not on `dev` yet, and a
// restatement drifts. Where the real validator exists it is now called instead.

// headerCard is a card with a typed header whose PATHS: line is the one thing varying.
func headerCard(t *testing.T, header ...string) []byte {
	t.Helper()
	body := append([]string{"RESULT: CARD-1 sha=0123456789ab"}, header...)
	body = append(body,
		"",
		"STEP 1. git clone -q https://github.com/mas-bandwidth/nova-tools.git repo && cd repo",
		"STEP 2. finish within 20 minutes; run go test ./internal/x/ -run TestA",
		"STEP 3. Write RESULT.md, whose line 1 is the RESULT: line above.",
		"")
	return []byte(strings.Join(body, "\n"))
}

func findingsOn(raw []byte) []CardHeaderFinding { return LintCardHeader(raw, nil, false) }

func hasCheck(fs []CardHeaderFinding, check string) bool {
	for _, f := range fs {
		if f.Check == check {
			return true
		}
	}
	return false
}

func dumpFindings(fs []CardHeaderFinding) string {
	var b strings.Builder
	for _, f := range fs {
		b.WriteString(f.Check + ": " + f.Excerpt + "\n")
	}
	if b.Len() == 0 {
		return "(no findings)"
	}
	return b.String()
}

// #1853.1 A WINDOWS DRIVE LETTER IS AN ABSOLUTE PATH, ON EVERY BENCH. `validGlob` refused
// a leading `/` and a leading `\` and let `C:/Windows/...` straight through, so a card
// could declare paths outside the repository and be admitted. The check may not ask the
// host: a card is linted on the bench that cuts it and run on another, so the rule is
// lexical and the same on darwin, linux and windows.
func TestAWindowsDriveLetterIsNotARepoRelativeGlob(t *testing.T) {
	for _, g := range []string{`C:/Windows/system32/evil.go`, `C:\Windows\system32\evil.go`, `d:/x/y.go`} {
		fs := findingsOn(headerCard(t, "KIND: fix-red", "PATHS: "+g, "TEST: ./internal/x TestA"))
		if !hasCheck(fs, "paths-declared") {
			t.Errorf("a drive-letter path is absolute and is not repo-relative: %q\n%s", g, dumpFindings(fs))
		}
	}
	// And the same glob in the TEST: package field, which runs the same rule.
	fs := findingsOn(headerCard(t, "KIND: fix-red", "PATHS: internal/x/a.go", `TEST: C:/x TestA`))
	if !hasCheck(fs, "test-named") {
		t.Errorf("the TEST: package runs the same path rule\n%s", dumpFindings(fs))
	}
}

// #1853.2 THE CAP ON PATHS: IS EIGHT (SPEC-TOOLWORK.md:579-580, internal/hygiene maxPaths).
// A card whose bound names nine files has a bound that has stopped bounding anything.
func TestNineGlobsIsOverThePathsCap(t *testing.T) {
	nine := "a1.go, a2.go, a3.go, a4.go, a5.go, a6.go, a7.go, a8.go, a9.go"
	fs := findingsOn(headerCard(t, "KIND: fix-red", "PATHS: "+nine, "TEST: ./internal/x TestA"))
	if !hasCheck(fs, "paths-declared") {
		t.Fatalf("nine globs is over the cap of eight\n%s", dumpFindings(fs))
	}
	// Eight is the cap, not the refusal.
	eight := "a1.go, a2.go, a3.go, a4.go, a5.go, a6.go, a7.go, a8.go"
	if fs := findingsOn(headerCard(t, "KIND: fix-red", "PATHS: "+eight, "TEST: ./internal/x TestA")); len(fs) != 0 {
		t.Fatalf("eight globs is exactly the cap and is clean\n%s", dumpFindings(fs))
	}
}

// #1853.3 A COMMA-ONLY PATHS: LINE DECLARES NOTHING. `PATHS: , , ` is not `PATHS: none`
// and it is not a list of globs; the loop skipped every empty entry and said the line was
// fine, which is the worst of the three answers.
func TestACommaOnlyPathsLineDeclaresNothing(t *testing.T) {
	for _, v := range []string{", , ", ",", " , "} {
		fs := findingsOn(headerCard(t, "KIND: fix-red", "PATHS: "+v, "TEST: ./internal/x TestA"))
		if !hasCheck(fs, "paths-declared") {
			t.Errorf("PATHS: %q names no glob and is not `none`\n%s", v, dumpFindings(fs))
		}
	}
	// The ordinary spelling, a comma and a space between globs, stays clean.
	if fs := findingsOn(headerCard(t, "KIND: fix-red", "PATHS: internal/x/a.go, internal/x/a_test.go", "TEST: ./internal/x TestA")); len(fs) != 0 {
		t.Fatalf("`<glob>, <glob>` is how every card writes it\n%s", dumpFindings(fs))
	}
}

// #1854.1 A TYPED HEADER LINE THE GATE WILL NEVER READ IS A DEFECT, NOT A SILENCE. The
// block ends at the first line that is not `KEY: value`, which is the gate parser's own
// rule and stays. What was wrong is what happened next: a card with a sentence above its
// KIND: line got NO typed checks at all and passed, then died at the gate. The stranded
// line is now named, on its own line number.
func TestATypedHeaderBelowTheBlockIsNamedNotSkipped(t *testing.T) {
	raw := headerCard(t,
		"This card is about a thing.",
		"KIND: fix-red",
		"PATHS: internal/x/a.go",
		"TEST: ./internal/x TestA")
	fs := findingsOn(raw)
	if len(fs) == 0 {
		t.Fatalf("a KIND: the gate will never read is a finding, not a pass\n%s", dumpFindings(fs))
	}
	if !hasCheck(fs, "kind-declared") {
		t.Fatalf("the stranded line is named by its own token\n%s", dumpFindings(fs))
	}
	for _, f := range fs {
		if f.Check == "kind-declared" && f.Line != 3 {
			t.Fatalf("the finding names the line the stranded KIND: sits on, got %d\n%s", f.Line, dumpFindings(fs))
		}
	}
}

// #1854.2 A DUPLICATE HEADER KEY IS AMBIGUOUS, AND SILENTLY TAKING THE FIRST IS THE ONE
// ANSWER NOBODY CAN ACT ON. Two KIND: lines is a card whose kind is unknown.
func TestADuplicateHeaderKeyIsRefusedNotSilentlyDropped(t *testing.T) {
	fs := findingsOn(headerCard(t,
		"KIND: fix-red",
		"KIND: transcript-test",
		"PATHS: internal/x/a.go",
		"TEST: ./internal/x TestA"))
	if !hasCheck(fs, "kind-declared") {
		t.Fatalf("two KIND: lines is a card with no kind\n%s", dumpFindings(fs))
	}
	fs = findingsOn(headerCard(t,
		"KIND: fix-red",
		"PATHS: internal/x/a.go",
		"PATHS: internal/y/b.go",
		"TEST: ./internal/x TestA"))
	if !hasCheck(fs, "paths-declared") {
		t.Fatalf("two PATHS: lines is a card with no bound\n%s", dumpFindings(fs))
	}
	fs = findingsOn(headerCard(t,
		"KIND: fix-red",
		"PATHS: internal/x/a.go",
		"TEST: ./internal/x TestA",
		"TEST: ./internal/y TestB"))
	if !hasCheck(fs, "test-named") {
		t.Fatalf("two TEST: lines is a card with no gate\n%s", dumpFindings(fs))
	}
}

// THE VALIDATOR IS CALLED, NOT RESTATED. lintheader.go's own comment said `validGlob`
// should become a call to `hygiene.ValidatePaths` the day T02 landed. It has landed, and
// every escape above is a rule that validator already held. This test is the one that
// keeps the two from drifting apart again: the lint's answer on a glob is the validator's.
func TestThePathRuleIsTheValidatorsNotACopy(t *testing.T) {
	for _, g := range []string{"../out/x.go", "/etc/passwd", "**/*", "*/**", "*"} {
		fs := findingsOn(headerCard(t, "KIND: fix-red", "PATHS: "+g, "TEST: ./internal/x TestA"))
		if !hasCheck(fs, "paths-declared") {
			t.Errorf("hygiene.ValidatePaths refuses %q, so the lint does\n%s", g, dumpFindings(fs))
		}
	}
	for _, g := range []string{"sign/**", "*.go", "**/*.go", "internal/x/a.go"} {
		fs := findingsOn(headerCard(t, "KIND: fix-red", "PATHS: "+g, "TEST: ./internal/x TestA"))
		if len(fs) != 0 {
			t.Errorf("hygiene.ValidatePaths clears %q, so the lint does\n%s", g, dumpFindings(fs))
		}
	}
}
