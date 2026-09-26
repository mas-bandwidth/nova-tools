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
		{RulePlanChildren, cardWith("KIND:", "KIND: plan"), "KIND: plan", remedyPlanChildren},
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
// "> ", and a line inside a code fence is not a card line, except that a
// BUILD list inside one is a BUILD list (the third read).
func TestLintOneInvariantReadsIssueText(t *testing.T) {
	t.Parallel()
	quoted := "RESULT: x sha=0\nKIND: fix\n" + strings.Join(goodCardLines[2:7], "\n") + "\n\n> BUILD:\n> 1. one\n> 2. two\n"
	if got := LintOneInvariant(Card{Text: quoted}); got.Rules() != RuleBuildList {
		t.Errorf("quoted BUILD list: rules %q, want build-list", got.Rules())
	}
	fenced := cardWith("", "") + "```\nBUILD:\n- one\n- two\nbuild issue #1 as written\n```\n"
	if got := LintOneInvariant(Card{Text: fenced}); got.Rules() != RuleBuildList {
		t.Errorf("a fenced block: rules %q, want build-list alone (a fenced build-issue line is no card line)", got.Rules())
	}
}

// TestLintOneInvariantExemptsPlanNotStitch: a KIND plan (taskcard.KindPlan,
// #4388) carries no DONE-WHEN rule, no CLASS-TEST, PATHS over any number of
// packages and a BUILD: list of its children, since its children carry them.
// A KIND stitch header grants nothing: a hand-written stitch (a pointer to an
// issue, five packages, a BUILD: list) is refused like a KIND fix, since only
// card cut --parent's own stitch row goes unlinted, and that by its writer.
func TestLintOneInvariantExemptsPlanNotStitch(t *testing.T) {
	t.Parallel()
	plan := cardWith("KIND:", "KIND: plan")
	plan = strings.Replace(plan, goodCardLines[4]+"\n", "", 1) // no CLASS-TEST
	plan = strings.Replace(plan, goodCardLines[6]+"\n", "", 1) // no DONE-WHEN
	plan = strings.Replace(plan, goodCardLines[3], "PATHS: a/b/x.go, a/c/, a/d/, a/e/", 1)
	plan = strings.Replace(plan, goodCardLines[8], "BUILD:\n1. the child card-lint-a\n2. the child card-lint-b", 1)
	if got := LintOneInvariant(Card{Text: plan}); got != nil {
		t.Errorf("KIND: plan (no DONE-WHEN, no CLASS-TEST, four packages, a BUILD: list of its children) is refused:\n%v", got)
	}
	if got := LintOneInvariant(Card{Text: strings.Replace(plan, "KIND: plan", "KIND: plan\nDONE-WHEN: the children land. The stitch lands.", 1)}); got != nil {
		t.Errorf("KIND: plan with a two-sentence DONE-WHEN is refused:\n%v", got)
	}
	// a plan with no children (no BUILD: item) is refused plan-children
	for _, build := range []string{"BUILD: the children, later.", ""} {
		noKids := strings.Replace(plan, "BUILD:\n1. the child card-lint-a\n2. the child card-lint-b", build, 1)
		if got := LintOneInvariant(Card{Text: noKids}); got.Rules() != RulePlanChildren || got[0].Line != "KIND: plan" {
			t.Errorf("KIND: plan with BUILD %q: refusals %v, want plan-children on the KIND line", build, got)
		}
	}
	for _, kind := range []string{"KIND: fix", "KIND: parent"} {
		want := RuleDoneWhenMissing + "," + RuleClassTestMissing + "," + RulePathsPackages + "," + RuleBuildList
		if got := LintOneInvariant(Card{Text: strings.Replace(plan, "KIND: plan", kind, 1)}); got.Rules() != want {
			t.Errorf("the plan card as %s: rules %q, want %s", kind, got.Rules(), want)
		}
	}
	stitch := "KIND: stitch\nPATHS: a/b/x.go a/c/ a/d/ a/e/ a/f/\nDONE-WHEN: the children land. The stitch lands.\n\n" +
		"Build issue #4352, as written.\n\nBUILD:\n- child one\n- child two\n"
	want := "invariant-missing,done-when-sentences,class-test-missing,paths-packages,build-issue,build-list"
	for _, kind := range []string{"KIND: stitch", "KIND: fix"} {
		if got := LintOneInvariant(Card{Text: strings.Replace(stitch, "KIND: stitch", kind, 1)}); got.Rules() != want {
			t.Errorf("the hand-written stitch as %s: rules %q, want %s", kind, got.Rules(), want)
		}
	}
}

// TestLintOneInvariantReadsLetteredBuildList: a BUILD: list lettered A. B.
// (the #4352 A-F shape), a. b., a) b), (a) (b), numbered (1) (2) or roman
// i. ii., (i) (ii), I. II. is a list, refused build-list like - and 1.
func TestLintOneInvariantReadsLetteredBuildList(t *testing.T) {
	t.Parallel()
	for name, list := range map[string]string{
		"A-F":  "BUILD:\nA. the parser.\nB. the linter.\nC. the push path.\nD. the cut path.\nE. the task path.\nF. the docs.",
		"A)":   "BUILD:\nA) the parser\nB) the linter",
		"dash": "BUILD:\n- the parser\n- the linter",
		"1.":   "BUILD:\n1. the parser\n2. the linter",
		"(1)":  "BUILD:\n(1) the parser\n(2) the linter",
		"a.":   "BUILD:\na. the parser.\nb. the linter.",
		"a)":   "BUILD:\na) the parser\nb) the linter",
		"(a)":  "BUILD:\n(a) the parser\n(b) the linter",
		"i.":   "BUILD:\ni. the parser\nii. the linter\niii. the docs",
		"(i)":  "BUILD:\n(i) the parser\n(ii) the linter",
		"I.":   "BUILD:\nI. the parser\nII. the linter",
		"1)":   "BUILD:\n1) the parser\n2) the linter",
	} {
		if got := LintOneInvariant(Card{Text: cardWith("BUILD:", list)}); got.Rules() != RuleBuildList {
			t.Errorf("%s: rules %q, want build-list", name, got.Rules())
		}
	}
	for _, one := range []string{"BUILD:\nA. the parser.", "BUILD: the parser.\nA good test names the rule.",
		"BUILD:\na. the parser.", "BUILD:\n(i) the parser.\ne.g. the linter reads it.\nivory is no numeral."} {
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

// TestLintOneInvariantReadsAbbreviationsAsOneSentence: e.g., i.e., vs. and
// etc. inside a DONE-WHEN or an INVARIANT end no sentence, so the line is
// one sentence and accepted; a real second sentence after one is refused.
func TestLintOneInvariantReadsAbbreviationsAsOneSentence(t *testing.T) {
	t.Parallel()
	done := "DONE-WHEN: the class test passes on each platform, e.g. darwin, i.e. the Studio, vs. linux, etc. in under 2 s."
	inv := "INVARIANT: every push path (e.g. card push, i.e. the one writer, vs. task push, etc.) lints the card."
	card := strings.Replace(cardWith("DONE-WHEN:", done), goodCardLines[2], inv, 1)
	if got := LintOneInvariant(Card{Text: card}); got != nil {
		t.Errorf("abbreviations counted as sentence ends:\n%v", got)
	}
	if got := LintOneInvariant(Card{Text: cardWith("DONE-WHEN:", done+" Then it lands.")}); got.Rules() != RuleDoneWhenSentences {
		t.Errorf("a second sentence after the abbreviations: rules %q, want done-when-sentences", got.Rules())
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
		// e.g., i.e., vs. and etc. end no sentence
		"a card, e.g. this one, holds.":        1,
		"one package (i.e. cardhdr) holds.":    1,
		"push vs. cut: both lint.":             1,
		"PATHS, CLASS-TEST, etc. are read.":    1,
		"E.g. a plan holds. Its children land": 2,
		"lists a, b, etc.":                     1,
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

// TestSentencesEndAtClosingQuoteOrBracket (#4396, the third read's probes):
// . ! or ? then a closing quote or bracket then a space ends a sentence, and
// a backtick with no closing backtick on its line hides nothing after it.
func TestSentencesEndAtClosingQuoteOrBracket(t *testing.T) {
	t.Parallel()
	for s, want := range map[string]int{
		`A prints "done." B prints ok.`:  2,
		`A passes (see x.) B passes.`:    2,
		`A says 'ok!' B says 'no?' C.`:   3,
		`A prints "done."`:               1,
		`A passes (see x.)`:              1,
		`A prints "done.")`:              1,
		`A prints "v1.2" and holds.`:     1,
		"A holds `x. B holds.":           2,
		"A holds `x.\nB holds. C` holds": 3,
		"A holds `x. y` then.":           1,
	} {
		if got := Sentences(s); got != want {
			t.Errorf("Sentences(%q) = %d, want %d", s, got, want)
		}
	}
	for _, done := range []string{`DONE-WHEN: A prints "done." B prints ok.`, "DONE-WHEN: A passes (see x.) B passes.",
		"DONE-WHEN: `go test ./x passes. B holds."} {
		if got := LintOneInvariant(Card{Text: cardWith("DONE-WHEN:", done)}); got.Rules() != RuleDoneWhenSentences {
			t.Errorf("%q: rules %q, want done-when-sentences", done, got.Rules())
		}
	}
}

// TestLintOneInvariantReadsContinuationLines: a line after DONE-WHEN: or
// INVARIANT: (up to a blank line, a KEY: line, a list item, a heading, a
// rule or a fence) continues it, so a second sentence there is refused.
func TestLintOneInvariantReadsContinuationLines(t *testing.T) {
	t.Parallel()
	done := "DONE-WHEN: `go test ./internal/nsprint/cardhdr -run TestX` passes."
	inv := goodCardLines[2]
	for name, c := range map[string]struct {
		text, rules string
	}{
		"done-when next line": {strings.Replace(cardWith("DONE-WHEN:", done), done, done+"\nThe CLI prints one line.", 1), RuleDoneWhenSentences},
		"invariant next line": {strings.Replace(cardWith("", ""), inv, inv+"\nIt names the rule.", 1), RuleInvariantSentences},
		"one sentence wrapped": {strings.Replace(cardWith("DONE-WHEN:", "DONE-WHEN: `go test ./x -run TestX`"), "-run TestX`",
			"-run TestX`\npasses in under 2 s.", 1), ""},
		"blank line then prose": {cardWith("DONE-WHEN:", done+"\n\nA note follows. It is prose."), ""},
		"list after":            {cardWith("DONE-WHEN:", done+"\n- one receipt line."), ""},
		"key after":             {cardWith("DONE-WHEN:", done+"\nNOTE: a note. Two."), ""},
	} {
		if got := LintOneInvariant(Card{Text: c.text}); got.Rules() != c.rules {
			t.Errorf("%s: rules %q, want %q\n%s", name, got.Rules(), c.rules, c.text)
		}
	}
}

// TestLintOneInvariantReadsBuildListAnyForm (#4396, the third read's
// probes): a BUILD list inside a ``` fence, under a Build: header in any
// letter case, or numbered on the BUILD: line itself is a BUILD list.
func TestLintOneInvariantReadsBuildListAnyForm(t *testing.T) {
	t.Parallel()
	for name, list := range map[string]string{
		"fenced items":  "BUILD:\n```\n1. the parser\n2. the linter\n```",
		"fenced header": "```\nBUILD:\n- the parser\n- the linter\n```",
		"Build:":        "Build:\n- the parser\n- the linter",
		"build:":        "build:\n1. the parser\n2. the linter",
		"bUiLd:":        "bUiLd:\n- the parser\n- the linter",
		"one line":      "BUILD: 1. a 2. b",
		"one line (1)":  "BUILD: (1) the parser (2) the linter",
		"line and list": "BUILD: 1. the parser\n2. the linter",
	} {
		if got := LintOneInvariant(Card{Text: cardWith("BUILD:", list)}); got.Rules() != RuleBuildList {
			t.Errorf("%s: rules %q, want build-list", name, got.Rules())
		}
	}
	for _, one := range []string{"Build: the parser, go 1.27.", "BUILD: 1. the parser", "```\nBuild:\n- the parser\n```"} {
		if got := LintOneInvariant(Card{Text: cardWith("BUILD:", one)}); got != nil {
			t.Errorf("%q: refused %q, want accepted (one item)", one, got.Rules())
		}
	}
	// a plan's children may sit on its BUILD: line
	if got := LintOneInvariant(Card{Text: "KIND: plan\nINVARIANT: x holds.\n\nBuild: 1. child-a 2. child-b\n"}); got != nil {
		t.Errorf("plan with inline children: refused %q", got.Rules())
	}
}

// TestLintTitleKind (#4396, the third read's task push probes): KIND stitch
// is refused on every task push (card cut --parent is its one writer), KIND
// plan with no card is refused plan-children, and a title that says "build
// issue #N as written" is refused build-issue; the card's own rules and
// these merge to one line per rule.
func TestLintTitleKind(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		kind, title string
		card        bool
		rules       string
	}{
		{"stitch", "stitch", false, RuleStitchWriter},
		{"stitch", "stitch", true, RuleStitchWriter},
		{"plan", "the plan", false, RulePlanChildren},
		{"plan", "the plan", true, ""},
		{"work", "Build issue #4352 as written", false, RuleBuildIssue},
		{"stitch", "build nova-tools#4352, as written", false, RuleStitchWriter + "," + RuleBuildIssue},
		{"work", "the parser refuses a list", false, ""},
		{"", "", false, ""},
		{"fix", "Rebuild issue #4352 as written", false, ""},
	} {
		if got := LintTitleKind(c.kind, c.title, c.card); got.Rules() != c.rules {
			t.Errorf("LintTitleKind(%q, %q, %v) = %q, want %q", c.kind, c.title, c.card, got.Rules(), c.rules)
		}
	}
	rs := LintTitleKind("work", "Build issue #1 as written", false)
	if want := `REFUSED card-lint rule=build-issue line="--title Build issue #1 as written" remedy="cut as a parent with children: card cut --parent"`; rs.Error() != want {
		t.Errorf("line %q, want %q", rs.Error(), want)
	}
	body := LintOneInvariant(Card{Text: cardWith("BUILD:", "Build issue #1 as written.")})
	if got := body.Merge(rs).Rules(); got != RuleBuildIssue {
		t.Errorf("merge = %q, want one build-issue", got)
	}
}
