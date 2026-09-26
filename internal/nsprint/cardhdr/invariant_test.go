package cardhdr

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// goodCard is one invariant: every rule keeps it. Each row of
// TestLintOneInvariantRefusesEachRule breaks exactly one line of it.
var goodCardLines = []string{
	"RESULT: card-lint-one-invariant sha=0123456789ab",
	"KIND: fix",
	"INVARIANT: every push path refuses a card that is not one invariant.",
	"PATHS: internal/nsprint/cardhdr/invariant.go, cmd/nova-sprint/card.go, internal/nsprint/card/push.go",
	"CLASS-TEST: TestLintOneInvariantRefusesEachRule",
	"PLATFORMS: darwin,linux",
	"DONE-WHEN: `go test ./internal/nsprint/cardhdr -run TestLintOneInvariantRefusesEachRule` passes in under 2 s.",
	"",
	"BUILD: the one refusal function, cardhdr.LintOneInvariant.",
}

// cardWith is goodCard with the line starting with key replaced by line
// ("" drops it), or line appended when no line starts with key.
func cardWith(key, line string) string {
	out, done := []string{}, false
	for _, l := range goodCardLines {
		if key != "" && strings.HasPrefix(l, key) {
			done = true
			if line != "" {
				out = append(out, line)
			}
			continue
		}
		out = append(out, l)
	}
	if !done && line != "" {
		out = append(out, line)
	}
	return strings.Join(out, "\n") + "\n"
}

// TestLintOneInvariantRefusesEachRule is the class test of #4396: one row per
// rule, each card refused by that rule alone with the exact offending line,
// the good card accepted; and the mutation check: with the
// row's rule removed from the linter, the row's card is accepted, so removing
// any one rule turns its row red.
func TestLintOneInvariantRefusesEachRule(t *testing.T) {
	t.Parallel()
	rows := []struct {
		rule, text, line, remedy string
	}{
		{RuleInvariantMissing, cardWith("INVARIANT:", ""), "", remedyInvariant},
		{RuleInvariantSentences, cardWith("INVARIANT:", "INVARIANT: push refuses a list. It names the rule."),
			"INVARIANT: push refuses a list. It names the rule.", RemedyParent},
		{RuleDoneWhenMissing, cardWith("DONE-WHEN:", ""), "", remedyDoneWhen},
		{RuleDoneWhenSentences, cardWith("DONE-WHEN:", "DONE-WHEN: the test passes. The CLI prints one line!"),
			"DONE-WHEN: the test passes. The CLI prints one line!", RemedyParent},
		{RuleClassTestMissing, cardWith("CLASS-TEST:", ""), "", remedyClassTest},
		{RuleClassTestForm, cardWith("CLASS-TEST:", "CLASS-TEST: go test ./x -run TestA"),
			"CLASS-TEST: go test ./x -run TestA", remedyClassTest},
		{RulePathsPackages, cardWith("PATHS:", "PATHS: a/b/x.go, a/c/y.go, a/d/, a/e/z_test.go, docs/CLI.md"),
			"PATHS: a/b/x.go, a/c/y.go, a/d/, a/e/z_test.go, docs/CLI.md", RemedyParent},
		{RulePlatforms, cardWith("PLATFORMS:", "PLATFORMS: darwin,windows"), "PLATFORMS: darwin,windows", remedyPlatforms},
		{RuleBuildIssue, cardWith("BUILD:", "Build issue #4396 as written."), "Build issue #4396 as written.", RemedyParent},
		{RuleBuildList, cardWith("BUILD:", "BUILD:\n- the parser\n- the linter"), "BUILD:", RemedyParent},
	}
	if len(rows) != len(rules) {
		t.Fatalf("%d rows for %d rules: every rule has its own row", len(rows), len(rules))
	}
	for i, row := range rows {
		if rules[i].name != row.rule {
			t.Fatalf("row %d is %s, rule %d is %s: one row per rule, in rule order", i, row.rule, i, rules[i].name)
		}
		want := Refusals{{Rule: row.rule, Line: row.line, Remedy: row.remedy}}
		if got := LintOneInvariant(Card{Text: row.text}); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: refusals\n%v\nwant\n%v", row.rule, got, want)
		}
		// mutation: the linter without this rule accepts the row's card
		var without []rule
		for _, r := range rules {
			if r.name != row.rule {
				without = append(without, r)
			}
		}
		if got := lintWith(Card{Text: row.text}, without); got != nil {
			t.Errorf("%s: without its rule the card is still refused (%v): the row does not isolate its rule", row.rule, got)
		}
	}
	if got := LintOneInvariant(Card{Text: cardWith("", "")}); got != nil {
		t.Errorf("the good card is refused:\n%v", got)
	}
	line := Refusal{Rule: RuleBuildList, Line: "BUILD:", Remedy: RemedyParent}.String()
	if want := `REFUSED card-lint rule=build-list line="BUILD:" remedy="cut as a parent with children: card cut --parent"`; line != want {
		t.Errorf("refusal line %q, want %q", line, want)
	}
}

// TestLintOneInvariantReadsIssueText: a card cut from an issue quotes it under
// "> ", and a line inside a code fence is not a card line.
func TestLintOneInvariantReadsIssueText(t *testing.T) {
	t.Parallel()
	quoted := "RESULT: x sha=0\nKIND: fix\n" + strings.Join(goodCardLines[2:7], "\n") + "\n\n> BUILD:\n> 1. one\n> 2. two\n"
	if got := LintOneInvariant(Card{Text: quoted}); got.Rules() != RuleBuildList {
		t.Errorf("quoted BUILD list: rules %q, want build-list", got.Rules())
	}
	fenced := cardWith("", "") + "```\nBUILD:\n- one\n- two\nbuild issue #1 as written\n```\n"
	if got := LintOneInvariant(Card{Text: fenced}); got != nil {
		t.Errorf("a fenced block was read as card lines: %v", got)
	}
}

// TestLintOneInvariantExemptsPlanAndStitch: a KIND plan (taskcard.KindPlan,
// #4388) carries no DONE-WHEN rule, no CLASS-TEST and PATHS over any number of
// packages, since its children carry them; a KIND stitch (the row card cut
// --parent writes: the plan's DONE-WHEN, the children's PATHS, a generated
// body) is read by no rule. The same cards under any other KIND are refused.
func TestLintOneInvariantExemptsPlanAndStitch(t *testing.T) {
	t.Parallel()
	plan := cardWith("KIND:", "KIND: plan")
	plan = strings.Replace(plan, goodCardLines[4]+"\n", "", 1) // no CLASS-TEST
	plan = strings.Replace(plan, goodCardLines[6]+"\n", "", 1) // no DONE-WHEN
	plan = strings.Replace(plan, goodCardLines[3], "PATHS: a/b/x.go, a/c/, a/d/, a/e/", 1)
	if got := LintOneInvariant(Card{Text: plan}); got != nil {
		t.Errorf("KIND: plan (no DONE-WHEN, no CLASS-TEST, four packages) is refused:\n%v", got)
	}
	if got := LintOneInvariant(Card{Text: strings.Replace(plan, "KIND: plan", "KIND: plan\nDONE-WHEN: the children land. The stitch lands.", 1)}); got != nil {
		t.Errorf("KIND: plan with a two-sentence DONE-WHEN is refused:\n%v", got)
	}
	for _, kind := range []string{"KIND: fix", "KIND: parent"} {
		want := RuleDoneWhenMissing + "," + RuleClassTestMissing + "," + RulePathsPackages
		if got := LintOneInvariant(Card{Text: strings.Replace(plan, "KIND: plan", kind, 1)}); got.Rules() != want {
			t.Errorf("the plan card as %s: rules %q, want %s", kind, got.Rules(), want)
		}
	}
	stitch := "KIND: stitch\nPATHS: a/b/x.go a/c/ a/d/ a/e/\nDONE-WHEN: the children land. The stitch lands.\n\n" +
		"Stitch of plan p: read every child's PR.\n\nBUILD:\n- child one\n- child two\nBuild issue #1 as written.\n"
	if got := LintOneInvariant(Card{Text: stitch}); got != nil {
		t.Errorf("KIND: stitch is refused:\n%v", got)
	}
	want := "invariant-missing,done-when-sentences,class-test-missing,paths-packages,build-issue,build-list"
	if got := LintOneInvariant(Card{Text: strings.Replace(stitch, "KIND: stitch", "KIND: fix", 1)}); got.Rules() != want {
		t.Errorf("the stitch card as KIND: fix: rules %q, want %s", got.Rules(), want)
	}
}

// TestLintOneInvariantReadsLetteredBuildList: a BUILD: list lettered A. B.
// (the #4352 A-F shape) or A) B) is a list, refused build-list like - and 1.
func TestLintOneInvariantReadsLetteredBuildList(t *testing.T) {
	t.Parallel()
	for name, list := range map[string]string{
		"A-F":  "BUILD:\nA. the parser.\nB. the linter.\nC. the push path.\nD. the cut path.\nE. the task path.\nF. the docs.",
		"A)":   "BUILD:\nA) the parser\nB) the linter",
		"dash": "BUILD:\n- the parser\n- the linter",
		"1.":   "BUILD:\n1. the parser\n2. the linter",
	} {
		if got := LintOneInvariant(Card{Text: cardWith("BUILD:", list)}); got.Rules() != RuleBuildList {
			t.Errorf("%s: rules %q, want build-list", name, got.Rules())
		}
	}
	for _, one := range []string{"BUILD:\nA. the parser.", "BUILD: the parser.\nA good test names the rule."} {
		if got := LintOneInvariant(Card{Text: cardWith("BUILD:", one)}); got != nil {
			t.Errorf("%q: refused %q, want accepted (one item)", one, got.Rules())
		}
	}
}

// TestLintOneInvariantReadsBuildIssueForms: a body that points at an issue
// is refused build-issue whatever sits between the words: a comma, a repo
// before the #, lower case.
func TestLintOneInvariantReadsBuildIssueForms(t *testing.T) {
	t.Parallel()
	for line, refused := range map[string]bool{
		"Build issue #4352, as written.":                  true,
		"Build issue #4352 as written.":                   true,
		"Build nova-tools#4352 as written.":               true,
		"build mas-bandwidth/nova-tools#4352, as written": true,
		"Build #4352 (the A-F list) as written.":          true,
		"Build the parser as #4352 names it.":             false,
		"The build is as written in the spec.":            false,
		"Rebuild issue #4352 as written.":                 false,
	} {
		got := LintOneInvariant(Card{Text: cardWith("BUILD:", line)})
		if want := map[bool]string{true: RuleBuildIssue, false: ""}[refused]; got.Rules() != want {
			t.Errorf("%q: rules %q, want %q", line, got.Rules(), want)
		}
	}
}

// TestSentences pins the count. A boundary inside a `code span` is opaque
// (the span is one sentence's words); a code span that is itself a sentence
// after a prose sentence is a second sentence.
func TestSentences(t *testing.T) {
	t.Parallel()
	for s, want := range map[string]int{
		"":                                    0,
		"one":                                 1,
		"one.":                                1,
		"one. two":                            2,
		"one! two? three.":                    3,
		"wall 1.5 s under v1.2":               1,
		"`go test ./x -run 'A.B'. then` runs": 1,
		"`a. b` then. and":                    2,
		"ends in #4401.":                      1,
		// prose, then a code span that is itself a sentence: two
		"exits 1. `go test ./x -run TestA passes.`": 2,
		"exits 1. `go test ./x -run TestA` passes.": 2,
		// the boundary is inside the span: one
		"exits 1 when `go test ./x. passes` fails.": 1,
		"`go test ./x -run TestA passes.`":          1,
	} {
		if got := Sentences(s); got != want {
			t.Errorf("Sentences(%q) = %d, want %d", s, got, want)
		}
	}
}

func TestPackagesCountsDistinctPackages(t *testing.T) {
	t.Parallel()
	tree := []string{"cmd/nova-sprint/card.go", "cmd/nova-sprint/card_cut_from.go", "internal/nsprint/cardhdr/cardhdr.go",
		"internal/nsprint/card/push.go", "internal/nsprint/card/lint.go", "internal/nsprint/task/push.go"}
	for paths, want := range map[string][]string{
		// three strings, one package
		"cmd/nova-sprint/card.go, cmd/nova-sprint/card_cut*.go cmd/nova-sprint/": {"cmd/nova-sprint"},
		// a new file is its directory; a doc is no package
		"internal/nsprint/cardhdr/invariant.go, docs/CLI.md": {"internal/nsprint/cardhdr"},
		// a directory spans every package below it
		"internal/nsprint/...": {"internal/nsprint/card", "internal/nsprint/cardhdr", "internal/nsprint/task"},
	} {
		if got := Packages(paths, tree); !reflect.DeepEqual(got, want) {
			t.Errorf("Packages(%q) = %v, want %v", paths, got, want)
		}
	}
	if got := Packages("a/b/x.go (new), a/b/y.go and docs/x.md, ./c/", nil); !reflect.DeepEqual(got, []string{"a/b", "c"}) {
		t.Errorf("Packages by shape = %v, want [a/b c]", got)
	}
}

// TestPackagesTreeDirWithNoGoFileIsNoPackage: with the tree, a directory in
// it that holds no .go file (docs/) is no package, and a directory absent
// from it (a new pkg/c/) is read by its shape: a package.
func TestPackagesTreeDirWithNoGoFileIsNoPackage(t *testing.T) {
	t.Parallel()
	tree := []string{"pkg/a/a.go", "pkg/b/b.go", "docs/CLI.md", "docs/img/x.png", "pkg/a/testdata/card.md"}
	for paths, want := range map[string][]string{
		"docs/":                             {},
		"docs/ pkg/a/testdata/":             {},
		"pkg/c/":                            {"pkg/c"},
		"pkg/c":                             {"pkg/c"},
		"pkg/a/ pkg/b/ pkg/c/ docs/":        {"pkg/a", "pkg/b", "pkg/c"},
		"pkg/a/ pkg/b/ pkg/c/ pkg/d/new.go": {"pkg/a", "pkg/b", "pkg/c", "pkg/d"},
	} {
		if got := Packages(paths, tree); !reflect.DeepEqual(got, want) {
			t.Errorf("Packages(%q) = %v, want %v", paths, got, want)
		}
	}
	card := cardWith("PATHS:", "PATHS: pkg/a/a.go, pkg/b/, pkg/c/, pkg/d/")
	if got := LintOneInvariant(Card{Text: card, Files: tree}); got.Rules() != RulePathsPackages {
		t.Errorf("four packages, two new: rules %q, want paths-packages", got.Rules())
	}
	if got := LintOneInvariant(Card{Text: cardWith("PATHS:", "PATHS: pkg/a/a.go, pkg/b/, pkg/c/, docs/"), Files: tree}); got != nil {
		t.Errorf("three packages and docs/: refused %q", got.Rules())
	}
}

func TestParseTypedLines(t *testing.T) {
	t.Parallel()
	if _, err := ParseClassTest("TestA_b1"); err != nil {
		t.Error(err)
	}
	if _, err := ParseClassTest("./x TestA"); err == nil || !strings.Contains(err.Error(), "CLASS-TEST:") {
		t.Errorf("ParseClassTest error %v, want one naming CLASS-TEST:", err)
	}
	for v, ok := range map[string]bool{"darwin,linux": true, "linux": true, "linux, darwin": true, "": false,
		"darwin,darwin": false, "darwin,windows": false, "darwin linux": false} {
		if _, err := ParsePlatforms(v); (err == nil) != ok || (err != nil && !strings.Contains(err.Error(), "PLATFORMS:")) {
			t.Errorf("ParsePlatforms(%q) = %v, want ok=%v naming PLATFORMS:", v, err, ok)
		}
	}
	if _, err := ParseInvariant("one. two."); err == nil || !strings.Contains(err.Error(), "INVARIANT:") {
		t.Errorf("ParseInvariant error %v, want one naming INVARIANT:", err)
	}
}

// TestLintOneInvariantOverTreeFixtures runs the linter over every card
// fixture in the tree's testdata and logs which would be refused. It is
// informational (it asserts nothing), so a fixture written before #4396 does
// not block; run with -v to read it.
func TestLintOneInvariantOverTreeFixtures(t *testing.T) {
	t.Parallel()
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Skipf("no module root at %s", root)
	}
	cards, refusedCards := 0, 0
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			if d != nil && d.IsDir() && (d.Name() == ".git" || d.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.Contains(filepath.ToSlash(p), "/testdata/") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil || len(b) > 1<<20 {
			return nil
		}
		text := string(b)
		if !strings.HasPrefix(text, "RESULT") && !strings.Contains(text, "\nPATHS:") && !strings.Contains(text, "\nINVARIANT:") {
			return nil
		}
		cards++
		rel, _ := filepath.Rel(root, p)
		if rs := LintOneInvariant(Card{Text: text}); rs != nil {
			refusedCards++
			t.Logf("%s: refused rules=%s", rel, rs.Rules())
		} else {
			t.Logf("%s: accepted", rel)
		}
		return nil
	})
	t.Logf("card fixtures=%d refused=%d", cards, refusedCards)
}
