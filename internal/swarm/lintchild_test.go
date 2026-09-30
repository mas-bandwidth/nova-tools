package swarm

import (
	"regexp"
	"strings"
	"testing"
)

// THE CHILD RULES ARE ONE TABLE, AND EVERY ROW IS HELD (lintchild.go).
//
// The rules the coordinator gives every child are rows of CardChildRules. The tests below
// are class tests over the table, so a row added tomorrow is held without a new test: its
// sentence is required, a card without it is refused naming it, the template quotes it, the
// listing carries its remedy; and every scan is held against a violating line and the
// clean line beside it.

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

// Every row names itself in kebab case once, carries a sentence that ends like a sentence
// and a source, and has its remedy in the table `--rules` prints; every scan enforces a
// row that exists and carries a remedy.
func TestChildRulesTableIsWellFormed(t *testing.T) {
	t.Parallel()
	name := regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)
	seen := map[string]bool{}
	for _, r := range CardChildRules {
		if !name.MatchString(r.Name) || seen[r.Name] {
			t.Errorf("rule name %q is not a unique kebab-case token", r.Name)
		}
		seen[r.Name] = true
		if r.Sentence != strings.TrimSpace(r.Sentence) || len(r.Sentence) < 12 {
			t.Errorf("rule %s: sentence %q is not a whole rule", r.Name, r.Sentence)
		}
		if strings.TrimSpace(r.Source) == "" {
			t.Errorf("rule %s names no source", r.Name)
		}
		remedy, ok := CardChildRemedies["rule-"+r.Name]
		if !ok || !strings.Contains(remedy, r.Sentence) || !strings.Contains(remedy, r.Source) {
			t.Errorf("rule %s: remedy %q does not quote the sentence and its source", r.Name, remedy)
		}
	}
	for _, s := range childScans {
		if !seen[s.Rule] {
			t.Errorf("scan %s enforces %q, which is no rule", s.Check, s.Rule)
		}
		if !strings.HasPrefix(s.Check, "step-") || strings.TrimSpace(CardChildRemedies[s.Check]) == "" {
			t.Errorf("scan %s is not a step-<what> token with a remedy", s.Check)
		}
	}
	if want := len(CardChildRules) + len(childScans); len(CardChildRemedies) != want {
		t.Errorf("the remedy table holds %d tokens, want %d: one per rule and one per scan", len(CardChildRemedies), want)
	}
	for _, need := range []string{
		"gocache", "no-go-clean", "no-redis-server", "no-kill", "go-test-timeout", "no-rm-rf", "no-force-push", "no-rebase",
		"functional-in-container", "parallel", "class-tests", "no-names", "present-tense", "commit-trailer", "pr-line",
		"never-merge", "exit-codes", "pr-diffstat", "report-not-done", "no-stash",
	} {
		if !seen[need] {
			t.Errorf("the rule %s the coordinator gives every child is not a row", need)
		}
	}
}

// The template is the shape the coordinator starts from: it carries every rule sentence,
// and it lints clean as printed.
func TestChildTemplateLintsClean(t *testing.T) {
	t.Parallel()
	body := childTemplate(t)
	if got := LintCardChild([]byte(body)); len(got) != 0 {
		t.Fatalf("the card template draws findings: %v", got)
	}
	for _, r := range CardChildRules {
		if !strings.Contains(body, r.Sentence) {
			t.Errorf("the card template does not quote the rule %s: %s", r.Name, r.Sentence)
		}
	}
	if !IsCardContractLine(strings.SplitN(body, "\n", 2)[0]) {
		t.Errorf("line 1 of the template is not the contract line")
	}
}

// A card without one rule is refused naming exactly that rule, at line 1, with the
// sentence in the excerpt; a card with none of them draws every rule.
func TestChildRuleMissingIsRefusedNamingIt(t *testing.T) {
	t.Parallel()
	body := childTemplate(t)
	for _, r := range CardChildRules {
		without := strings.Replace(body, r.Sentence+"\n", "", 1)
		if without == body {
			t.Fatalf("the template does not carry %s on a line of its own", r.Name)
		}
		got := LintCardChild([]byte(without))
		if len(got) != 1 || got[0].Check != "rule-"+r.Name || got[0].Line != 1 || !strings.Contains(got[0].Excerpt, r.Sentence) {
			t.Errorf("a card without %s draws %v, want exactly rule-%s naming the sentence", r.Name, got, r.Name)
		}
	}
	bare := LintCardChild([]byte("RESULT: x sha=abc\nfix the thing\n"))
	if len(bare) != len(CardChildRules) {
		t.Errorf("a card with no rule draws %d findings, want %d", len(bare), len(CardChildRules))
	}
}

// A rule the writer wrapped over two lines, or indented, is still quoted.
func TestChildRuleSurvivesWrapping(t *testing.T) {
	t.Parallel()
	body := childTemplate(t)
	r := CardChildRules[len(CardChildRules)-3]
	words := strings.Fields(r.Sentence)
	wrapped := strings.Join(words[:len(words)/2], " ") + "\n   " + strings.Join(words[len(words)/2:], " ")
	if got := LintCardChild([]byte(strings.Replace(body, r.Sentence, wrapped, 1))); len(got) != 0 {
		t.Errorf("a wrapped %s is refused: %v", r.Name, got)
	}
}

func scanFindings(t *testing.T, line string) []string {
	t.Helper()
	var out []string
	for _, f := range LintCardChild([]byte(childTemplate(t) + line + "\n")) {
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

// The RULES paragraph is where a card quotes what it forbids: a command in it is prose.
// The same line after a STEP is a command, and a markdown heading ends the paragraph.
func TestChildScansSkipTheRulesParagraph(t *testing.T) {
	t.Parallel()
	card := "RESULT: x sha=abc\nRULES.\nrun git stash then rm -rf /\nSTEP 1. cd x\n"
	if got := childChecks(LintCardChild([]byte(card))); containsStep(got) {
		t.Errorf("the RULES paragraph is scanned: %v", got)
	}
	card = "RESULT: x sha=abc\nRULES.\nprose\nSTEP 1. git stash\n"
	if got := childChecks(LintCardChild([]byte(card))); !contains(got, "step-stash") {
		t.Errorf("a STEP after the RULES paragraph is not scanned: %v", got)
	}
	card = "RESULT: x sha=abc\n## RULES\nprose\n## Steps\ngit stash\n"
	if got := childChecks(LintCardChild([]byte(card))); !contains(got, "step-stash") {
		t.Errorf("a heading does not end the RULES paragraph: %v", got)
	}
}

// Every rule sentence, alone on a line in a STEP, is prose about the rule and draws no
// scan: a card that quotes its rules after its last STEP is not refused for quoting them.
func TestChildRuleSentencesAreNotViolations(t *testing.T) {
	t.Parallel()
	for _, r := range CardChildRules {
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
