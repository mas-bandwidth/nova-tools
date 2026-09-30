package swarm

import (
	"regexp"
	"strings"
	"testing"
)

// THE CHILD RULES ARE A RULE SET, AND EVERY ROW OF IT IS HELD (lintchild.go).
//
// The general rules every project shares are the rows of DefaultChildRules; one
// repository's own are the sentences of a rules file (fleet/child-rules.txt holds this
// repository's). The tests below are class tests over both sets, so a row added tomorrow is
// held without a new test: its sentence is required, a card without it is refused naming
// it, the template quotes it (the default set), the listing carries its remedy; and every
// scan is held against a violating line and the clean line beside it.

// ourRulesPath is this repository's own rules file, read relative to this package.
const ourRulesPath = "../../fleet/child-rules.txt"

// ourRules is this repository's rule set, read from its file.
func ourRules(t *testing.T) []ChildRule {
	t.Helper()
	rules, err := ReadChildRules(ourRulesPath)
	if err != nil {
		t.Fatal(err)
	}
	return rules
}

// ourCard is the card template with this repository's RULES paragraph in place of the
// general one.
func ourCard(t *testing.T) string {
	t.Helper()
	return strings.Replace(childTemplate(t), ChildRulesParagraph(), RulesParagraph(ourRules(t)), 1)
}

func childTemplate(t *testing.T) string {
	t.Helper()
	body, err := Template("card")
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func childChecks(fs []CardHeaderFinding) []string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Check)
	}
	return out
}

// Every row of the default set and of this repository's file names itself in kebab case
// once, carries a sentence that ends like a sentence and a source, and has its remedy; every
// scan carries a remedy, and the remedy table holds one row per default rule and one per scan.
func TestChildRulesTableIsWellFormed(t *testing.T) {
	t.Parallel()
	name := regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)
	for set, rules := range map[string][]ChildRule{"default": DefaultChildRules, "ours": ourRules(t)} {
		seen := map[string]bool{}
		for _, r := range rules {
			if !name.MatchString(r.Name) || seen[r.Name] {
				t.Errorf("%s: rule name %q is not a unique kebab-case token", set, r.Name)
			}
			seen[r.Name] = true
			if r.Sentence != strings.TrimSpace(r.Sentence) || len(r.Sentence) < 12 {
				t.Errorf("%s: rule %s: sentence %q is not a whole rule", set, r.Name, r.Sentence)
			}
			if strings.TrimSpace(r.Source) == "" {
				t.Errorf("%s: rule %s names no source", set, r.Name)
			}
			remedy := ChildRemedy(rules, "rule-"+r.Name)
			if !strings.Contains(remedy, r.Sentence) || !strings.Contains(remedy, r.Source) {
				t.Errorf("%s: rule %s: remedy %q does not quote the sentence and its source", set, r.Name, remedy)
			}
		}
	}
	for _, r := range DefaultChildRules {
		if got := CardChildRemedies["rule-"+r.Name]; got != ChildRemedy(DefaultChildRules, "rule-"+r.Name) {
			t.Errorf("the listing's remedy for %s differs from the lint's: %q", r.Name, got)
		}
	}
	for _, s := range childScans {
		if !strings.HasPrefix(s.Check, "step-") || strings.TrimSpace(CardChildRemedies[s.Check]) == "" {
			t.Errorf("scan %s is not a step-<what> token with a remedy", s.Check)
		}
		if got := ChildRemedy(nil, s.Check); got != s.Remedy {
			t.Errorf("scan %s: ChildRemedy answers %q, want its remedy", s.Check, got)
		}
		if s.Needs != "" {
			found := false
			for _, r := range ourRules(t) {
				found = found || r.Name == s.Needs
			}
			if !found {
				t.Errorf("scan %s needs the rule %q, which this repository's file does not carry", s.Check, s.Needs)
			}
		}
	}
	if want := len(DefaultChildRules) + len(childScans); len(CardChildRemedies) != want {
		t.Errorf("the remedy table holds %d tokens, want %d: one per default rule and one per scan", len(CardChildRemedies), want)
	}
	if got := ChildRemedy(DefaultChildRules, "rule-nothing"); got != "" {
		t.Errorf("a token that is no rule has the remedy %q", got)
	}
	// the general set is only the general rules: nothing of one repository's
	for _, r := range DefaultChildRules {
		for _, project := range []string{"GOCACHE", "go test", "-timeout", "t.Parallel", "Co-Authored-By", "functionalrun", "./internal/ci", "Claude"} {
			if strings.Contains(r.Sentence, project) {
				t.Errorf("the default rule %s names %q: a project's rule belongs in its rules file", r.Name, project)
			}
		}
	}
	// the rules this repository's coordinator gives every child are rows of its file
	have := map[string]bool{}
	for _, r := range ourRules(t) {
		have[r.Name] = true
	}
	for _, need := range []string{
		"gocache", "no-go-clean", "no-redis-server", "no-kill", "go-test-timeout", "no-rm-rf", "no-force-push", "no-rebase",
		"functional-in-container", "parallel", "class-tests", "no-names", "present-tense", "commit-trailer", "pr-line",
		"never-merge", "pr-diffstat", "report-not-done", "no-stash",
	} {
		if !have[need] {
			t.Errorf("the rule %s the coordinator gives every child is not a row of fleet/child-rules.txt", need)
		}
	}
}

// The template is the shape the coordinator starts from: it carries every default rule
// sentence, and it lints clean as printed.
func TestChildTemplateLintsClean(t *testing.T) {
	t.Parallel()
	body := childTemplate(t)
	if got := LintCardChild([]byte(body)); len(got) != 0 {
		t.Fatalf("the card template draws findings: %v", got)
	}
	for _, r := range DefaultChildRules {
		if !strings.Contains(body, r.Sentence) {
			t.Errorf("the card template does not quote the rule %s: %s", r.Name, r.Sentence)
		}
	}
	if !IsCardContractLine(strings.SplitN(body, "\n", 2)[0]) {
		t.Errorf("line 1 of the template is not the contract line")
	}
	// this repository's own card, with its paragraph, is admitted under its own rules
	if got := LintCardChildWith([]byte(ourCard(t)), ourRules(t)); len(got) != 0 {
		t.Errorf("a card carrying this repository's rules draws findings under them: %v", got)
	}
}

// A card for any other project, with no Go in it, is admitted under the default rules: the
// rules of one repository (its caches, its test flags, its trailer) are not the tool's.
func TestChildNonGoCardIsAdmittedUnderTheDefaultRules(t *testing.T) {
	t.Parallel()
	card := "RESULT: docs sha=abc\nFix the typo in README.md, then run make check.\n\n" + ChildRulesParagraph() + "\nSTEP 1. cd site && npm test\nSTEP 2. git push origin HEAD\n"
	if got := LintCardChild([]byte(card)); len(got) != 0 {
		t.Fatalf("a non-Go card with the default rules draws %v", got)
	}
	// the same card under this repository's rules is refused for what it does not carry
	got := LintCardChildWith([]byte(card), ourRules(t))
	if len(got) < 10 {
		t.Errorf("the card carries none of this repository's rules and draws only %d findings", len(got))
	}
	// a Go project's scans run only where its rules are in the set
	goCard := card + "STEP 3. go test ./...\nSTEP 4. go clean -cache\n"
	if got := LintCardChild([]byte(goCard)); len(got) != 0 {
		t.Errorf("the default set scans Go commands it has no rule for: %v", got)
	}
	for _, f := range LintCardChildWith([]byte(goCard), ourRules(t)) {
		if f.Check == "step-go-test-timeout" || f.Check == "step-go-clean" {
			return
		}
	}
	t.Errorf("this repository's rules do not scan a go test without its timeout or a go clean")
}

// A card missing one rule of a file's own set is refused naming exactly that rule.
func TestChildRulesFileNamesTheRuleAMissingSentenceBreaks(t *testing.T) {
	t.Parallel()
	rules, err := ParseChildRules("# ours\n[deploy-note] Say which environment the change is for.\nQuote the ticket number.\n", "rules.txt")
	if err != nil {
		t.Fatal(err)
	}
	card := "RESULT: x sha=abc\n\nRULES.\nSay which environment the change is for.\nQuote the ticket number.\n\nSTEP 1. cd x\n"
	if got := LintCardChildWith([]byte(card), rules); len(got) != 0 {
		t.Fatalf("a card carrying both sentences draws %v", got)
	}
	got := LintCardChildWith([]byte(strings.Replace(card, "Quote the ticket number.\n", "", 1)), rules)
	if len(got) != 1 || got[0].Check != "rule-line3" || !strings.Contains(got[0].Excerpt, "Quote the ticket number.") {
		t.Fatalf("a card without the unnamed third line's sentence draws %v, want rule-line3", got)
	}
	got = LintCardChildWith([]byte(strings.Replace(card, "Say which environment the change is for.\n", "", 1)), rules)
	if len(got) != 1 || got[0].Check != "rule-deploy-note" {
		t.Fatalf("a card without the named sentence draws %v, want rule-deploy-note", got)
	}
	if r := ChildRemedy(rules, "rule-deploy-note"); !strings.Contains(r, "Say which environment") || !strings.Contains(r, "rules.txt:2") {
		t.Errorf("the remedy %q does not quote the sentence and its line in the file", r)
	}
}

// A rules file is one sentence per line; every problem of a bad file is reported together.
func TestParseChildRules(t *testing.T) {
	t.Parallel()
	rules, err := ParseChildRules("# a comment\n\n[a-b] First   rule  here.\r\nSecond rule here.\n  \n", "f.txt")
	if err != nil || len(rules) != 2 {
		t.Fatalf("got %v, %v", rules, err)
	}
	if rules[0] != (ChildRule{"a-b", "First rule here.", "f.txt:3"}) || rules[1] != (ChildRule{"line4", "Second rule here.", "f.txt:4"}) {
		t.Errorf("rules %+v", rules)
	}
	_, err = ParseChildRules("[Bad Name] x y z\n[ok] same sentence\n[ok] another\n[two] same sentence\n[open sentence\n[named]\n", "f.txt")
	if err == nil {
		t.Fatal("a file with six problems is accepted")
	}
	for _, want := range []string{"line 1", "line 3", "line 4", "line 5", "line 6"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not name %s", err, want)
		}
	}
	for _, empty := range []string{"", "\n\n", "# only a comment\n"} {
		if _, err := ParseChildRules(empty, "f.txt"); err == nil || !strings.Contains(err.Error(), "holds no rule") {
			t.Errorf("%q: got %v, want the empty-file refusal", empty, err)
		}
	}
	if _, err := ReadChildRules("testdata/no-such-rules-file"); err == nil {
		t.Error("an unreadable rules file is accepted")
	}
}

// A card without one rule is refused naming exactly that rule, at line 1, with the
// sentence in the excerpt; a card with none of them draws every rule. Held over the default
// set and over this repository's file.
func TestChildRuleMissingIsRefusedNamingIt(t *testing.T) {
	t.Parallel()
	for set, c := range map[string]struct {
		body  string
		rules []ChildRule
	}{
		"default": {childTemplate(t), DefaultChildRules},
		"ours":    {ourCard(t), ourRules(t)},
	} {
		for _, r := range c.rules {
			without := strings.Replace(c.body, r.Sentence+"\n", "", 1)
			if without == c.body {
				t.Fatalf("%s: the card does not carry %s on a line of its own", set, r.Name)
			}
			got := LintCardChildWith([]byte(without), c.rules)
			if len(got) != 1 || got[0].Check != "rule-"+r.Name || got[0].Line != 1 || !strings.Contains(got[0].Excerpt, r.Sentence) {
				t.Errorf("%s: a card without %s draws %v, want exactly rule-%s naming the sentence", set, r.Name, got, r.Name)
			}
		}
		bare := LintCardChildWith([]byte("RESULT: x sha=abc\nfix the thing\n"), c.rules)
		if len(bare) != len(c.rules) {
			t.Errorf("%s: a card with no rule draws %d findings, want %d", set, len(bare), len(c.rules))
		}
	}
}

// A rule the writer wrapped over two lines, or indented, is still quoted.
func TestChildRuleSurvivesWrapping(t *testing.T) {
	t.Parallel()
	body := childTemplate(t)
	r := DefaultChildRules[len(DefaultChildRules)-3]
	words := strings.Fields(r.Sentence)
	wrapped := strings.Join(words[:len(words)/2], " ") + "\n   " + strings.Join(words[len(words)/2:], " ")
	if got := LintCardChild([]byte(strings.Replace(body, r.Sentence, wrapped, 1))); len(got) != 0 {
		t.Errorf("a wrapped %s is refused: %v", r.Name, got)
	}
}

func scanFindings(t *testing.T, line string) []string {
	t.Helper()
	var out []string
	for _, f := range LintCardChildWith([]byte(ourCard(t)+line+"\n"), ourRules(t)) {
		out = append(out, f.Check)
	}
	return out
}

// Each scan refuses the violating lines and lets the clean line beside them through.
func TestChildScansRefuseTheCommandAndAllowTheRest(t *testing.T) {
	t.Parallel()
	cases := []struct {
		check string
		bad   []string
		ok    []string
	}{
		{"step-redis-server",
			[]string{"STEP 7. Start redis-server --port 7000.", "STEP 7. Run: cd x && redis-server &", "$ redis-server"},
			[]string{"STEP 7. Never start a redis-server.", "STEP 7. Run redis-cli ping against the container store.", "STEP 7. The my-redis-server-notes.md file."}},
		{"step-go-clean",
			[]string{"STEP 7. go clean -cache", "STEP 7. Run: cd x && go clean -modcache"},
			[]string{"STEP 7. Do not go clean anything.", "STEP 7. go build ./..."}},
		{"step-kill",
			[]string{"STEP 7. kill 1234", "STEP 7. pkill -f nova", "STEP 7. killall go", "STEP 7. kill -9 $PID"},
			[]string{"STEP 7. sleep 100 & kill $!", "STEP 7. kill %1", "STEP 7. the kill-switch test", "STEP 7. Never kill a process you did not start", "STEP 7. skill list"}},
		{"step-rm-rf",
			[]string{"STEP 7. rm -rf /tmp/x", "STEP 7. rm -rf ~/x", "STEP 7. rm -rf $HOME/cache", "STEP 7. rm -fr ../x", "STEP 7. rm -r --force ./a/../..", "STEP 7. rm -rf $DIR", "STEP 7. rm -rf .", "STEP 7. rm -rf *", "STEP 7. rm -f a && rm --recursive /x"},
			[]string{"STEP 7. rm -rf scratch", "STEP 7. rm -rf $PWD/work", "STEP 7. rm -rf \"$PWD/work\"", "STEP 7. rm -rf <job>/tmp", "STEP 7. rm -f file.txt", "STEP 7. No rm -rf outside the job."}},
		{"step-force-push",
			[]string{"STEP 7. git push --force", "STEP 7. git push -f origin x", "STEP 7. git push --force-with-lease", "STEP 7. git push origin +HEAD:x", "STEP 7. git -C d push -fu origin x", "STEP 7. git merge --no-edit && git push --force"},
			[]string{"STEP 7. git push origin HEAD", "STEP 7. git push -u origin feat", "STEP 7. git push --follow-tags", "STEP 7. Never git push --force."}},
		{"step-rebase",
			[]string{"STEP 7. git rebase origin/main", "STEP 7. git -c x=y rebase -i HEAD~3"},
			[]string{"STEP 7. git merge --no-edit origin/main", "STEP 7. Never git rebase."}},
		{"step-stash",
			[]string{"STEP 7. git stash", "STEP 7. git stash pop"},
			[]string{"STEP 7. Do not use git stash."}},
		{"step-merge",
			[]string{"STEP 7. gh pr merge 12 --squash", "STEP 7. Run: gh pr merge"},
			[]string{"STEP 7. Never gh pr merge.", "STEP 7. Open the pull request and stop."}},
		{"step-go-test-timeout",
			[]string{"STEP 7. go test ./...", "STEP 7. Run go test -count=1 ./x/", "STEP 7. go test ./a && go test -timeout 600s ./b"},
			[]string{"STEP 7. go test -timeout 600s ./...", "STEP 7. go test -count=1 -timeout 600s ./x/ -run TestY", "STEP 7. go vet ./...", "STEP 7. Never go test the whole tree."}},
	}
	covered := map[string]bool{}
	for _, c := range cases {
		covered[c.check] = true
		for _, line := range c.bad {
			if got := scanFindings(t, line); len(got) != 1 || got[0] != c.check {
				t.Errorf("%q draws %v, want exactly %s", line, got, c.check)
			}
		}
		for _, line := range c.ok {
			if got := scanFindings(t, line); len(got) != 0 {
				t.Errorf("%q draws %v, want none", line, got)
			}
		}
	}
	for _, s := range childScans {
		if !covered[s.Check] {
			t.Errorf("the scan %s has no violating case in this test", s.Check)
		}
	}
}

// The finding carries the line number of the offending line and the line's text.
func TestChildScanNamesTheLine(t *testing.T) {
	t.Parallel()
	body := childTemplate(t) + "STEP 7. git stash\n"
	got := LintCardChild([]byte(body))
	want := strings.Count(body, "\n") // the last line
	if len(got) != 1 || got[0].Check != "step-stash" || got[0].Line != want || got[0].Excerpt != "STEP 7. git stash" {
		t.Fatalf("got %v, want step-stash at line %d", got, want)
	}
}

// The RULES paragraph is where a card quotes what it forbids: it runs from its RULES line
// to the first blank line, and a command in it is prose. Every other line is scanned: the
// prose paragraph between RULES and STEP 1, a STEP, and a brief with no STEP at all.
func TestChildScansSkipOnlyTheRulesParagraph(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		card string
		hit  bool
	}{
		"inside the paragraph":        {"RESULT: x sha=abc\nRULES.\nrun git stash then rm -rf /\n\nSTEP 1. cd x\n", false},
		"a STEP after it":             {"RESULT: x sha=abc\nRULES.\nprose\n\nSTEP 1. git stash\n", true},
		"the task paragraph":          {"RESULT: x sha=abc\nRULES.\nprose\n\nTHE TASK. Then git stash.\n\nSTEP 1. cd x\n", true},
		"prose after RULES, no STEP":  {"RESULT: x sha=abc\nRULES.\nprose\n\nRun git stash before you start.\n", true},
		"the next line after RULES":   {"RESULT: x sha=abc\nRULES.\nprose\nSTEP 1. git stash\n", false},
		"a heading rules paragraph":   {"RESULT: x sha=abc\n## RULES\ngit stash\n\n## Steps\ngit stash\n", true},
		"the template's task section": {"", true},
	} {
		card := c.card
		if card == "" {
			card = strings.Replace(childTemplate(t), "THE TASK. <", "THE TASK. First git stash. <", 1)
		}
		got := contains(childChecks(LintCardChild([]byte(card))), "step-stash")
		if name == "the next line after RULES" {
			// STEP 1 follows RULES with no blank line: it is inside the paragraph by rule
			if got {
				t.Errorf("%s: scanned", name)
			}
			continue
		}
		if got != c.hit {
			t.Errorf("%s: step-stash found=%v, want %v", name, got, c.hit)
		}
	}
	// the same paragraph in the template, with a force-push in it, is refused too
	tmpl := strings.Replace(childTemplate(t), "THE TASK. <", "THE TASK. Then git push --force. <", 1)
	if got := LintCardChild([]byte(tmpl)); len(got) != 1 || got[0].Check != "step-force-push" {
		t.Errorf("a force-push in the template's task paragraph draws %v", got)
	}
	// the second scan hit is the one outside the paragraph, at its own line
	got := LintCardChild([]byte("RESULT: x sha=abc\nRULES.\ngit stash\n\nSTEP 1. git stash\n"))
	if len(got) < 1 || got[len(got)-1].Line != 5 {
		t.Errorf("want the finding on line 5 only: %v", got)
	}
}

// A negation reaches only its own clause: a `;`, `,`, `&&`, `||` or `. ` ends it.
func TestChildNegationIsLimitedToTheClause(t *testing.T) {
	t.Parallel()
	for _, line := range []string{
		"STEP 7. Do not stop; git push --force",
		"STEP 7. If it is not green, run go test ./x",
		"STEP 7. Do not wait && git stash",
		"STEP 7. No problem. git rebase main",
		"STEP 7. never mind, kill 1234",
	} {
		if got := scanFindings(t, line); len(got) != 1 {
			t.Errorf("%q draws %v, want one finding", line, got)
		}
	}
	for _, line := range []string{
		"STEP 7. Do not git stash here",
		"STEP 7. Do not run it twice; never git rebase",
	} {
		if got := scanFindings(t, line); len(got) != 0 {
			t.Errorf("%q draws %v, want none", line, got)
		}
	}
}

// `-timeout 0` (and `0s`) means no timeout: only a positive duration satisfies the scan.
func TestChildTimeoutMustBePositive(t *testing.T) {
	t.Parallel()
	for _, line := range []string{"STEP 7. go test -timeout 0 ./x", "STEP 7. go test -timeout=0s ./x", "STEP 7. go test -timeout 00 ./x"} {
		if got := scanFindings(t, line); len(got) != 1 || got[0] != "step-go-test-timeout" {
			t.Errorf("%q draws %v, want step-go-test-timeout", line, got)
		}
	}
	for _, line := range []string{"STEP 7. go test -timeout 600s ./x", "STEP 7. go test -timeout=10m ./x", "STEP 7. go test -timeout 1h30m ./x", "STEP 7. go test --timeout 0.5s ./x"} {
		if got := scanFindings(t, line); len(got) != 0 {
			t.Errorf("%q draws %v, want none", line, got)
		}
	}
}

// Every rule sentence, alone on a line in a STEP, is prose about the rule and draws no
// scan: a card that quotes its rules after its last STEP is not refused for quoting them.
func TestChildRuleSentencesAreNotViolations(t *testing.T) {
	t.Parallel()
	for _, r := range ourRules(t) {
		if got := scanFindings(t, "STEP 7. "+r.Sentence); len(got) != 0 {
			t.Errorf("the rule %s quoted in a STEP draws %v", r.Name, got)
		}
	}
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

func containsStep(xs []string) bool {
	for _, x := range xs {
		if strings.HasPrefix(x, "step-") {
			return true
		}
	}
	return false
}
