package swarm

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// THE CHILD RULES ARE ONE TABLE, AND EVERY ROW IS HELD (lintchild.go).
//
// The rules the coordinator gives every child are rows of CardChildRules. The tests below
// are class tests over the table, so a row added tomorrow is held without a new test: its
// sentence is required, a card without it is refused naming it, the template quotes it, the
// listing carries its remedy; and every scan is held against a violating line and the
// clean line beside it.

// childTemplateRaw is the card template as printed: its Libraries considered line still
// carries the writer's placeholder.
func childTemplateRaw(t *testing.T) string {
	t.Helper()
	body, err := Template("card")
	if err != nil {
		t.Fatal(err)
	}
	return body
}

const childLibrariesFilledLine = "Libraries considered: testify for the asserts, used; nothing else found for this work\n"

// childTemplate is the card template with its Libraries considered line filled, the state
// a card is in when a writer has finished with it.
func childTemplate(t *testing.T) string {
	t.Helper()
	body := childTemplateRaw(t)
	start := strings.Index(body, "Libraries considered:")
	require.GreaterOrEqual(t, start, 0, "the card template carries a Libraries considered line")
	end := start + strings.Index(body[start:], "\n") + 1
	return body[:start] + childLibrariesFilledLine + body[end:]
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
	// one per rule, one per scan, and the libraries-considered line
	if want := len(CardChildRules) + len(childScans) + 1; len(CardChildRemedies) != want {
		t.Errorf("the remedy table holds %d tokens, want %d: one per rule and one per scan, and the libraries-considered line", len(CardChildRemedies), want)
	}
	assert.Contains(t, CardChildRemedies[LibrariesConsideredRule], "Libraries considered:")
	for _, need := range []string{
		"gocache", "no-go-clean", "no-redis-server", "no-kill", "go-test-timeout", "no-rm-rf", "no-force-push", "no-rebase",
		"functional-in-container", "parallel", "class-tests", "no-names", "present-tense", "commit-trailer", "pr-line",
		"never-merge", "pr-diffstat", "report-not-done", "no-stash",
	} {
		if !seen[need] {
			t.Errorf("the rule %s the coordinator gives every child is not a row", need)
		}
	}
}

// The template is the shape the coordinator starts from: it carries every rule sentence,
// and once the writer has filled its Libraries considered line it lints clean. As printed
// the line still carries its placeholder, and that is the one finding it draws.
func TestChildTemplateLintsClean(t *testing.T) {
	t.Parallel()
	body := childTemplate(t)
	if got := LintCardChild([]byte(body)); len(got) != 0 {
		t.Fatalf("the filled card template draws findings: %v", got)
	}
	raw := LintCardChild([]byte(childTemplateRaw(t)))
	require.Len(t, raw, 1)
	assert.Equal(t, LibrariesConsideredRule, raw[0].Check)
	assert.Contains(t, raw[0].Excerpt, "unfilled: Libraries considered: <")
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

// LIBRARIES CONSIDERED (docs/STANDARD.md section 7, library first): a card that builds code
// carries the line, filled; a card that builds none is not asked for it.
func TestChildCardThatBuildsCodeCarriesLibrariesConsidered(t *testing.T) {
	t.Parallel()
	raw := childTemplateRaw(t)
	start := strings.Index(raw, "Libraries considered:")
	require.GreaterOrEqual(t, start, 0)
	end := start + strings.Index(raw[start:], "\n") + 1
	with := func(line string) string { return raw[:start] + line + raw[end:] }
	rules := ChildRulesParagraph()
	lib := []string{LibrariesConsideredRule}
	tests := []struct {
		name string
		card string
		want []string
	}{
		{"template as printed, placeholder unfilled", raw, lib},
		{"line removed", with(""), lib},
		{"line empty", with("Libraries considered:   \n"), lib},
		{"line empty, bulleted", with("- Libraries considered:\n"), lib},
		{"line keeps a placeholder", with("Libraries considered: testify; <why errgroup was not used>\n"), lib},
		{"line filled", with("Libraries considered: testify for asserts, used; errgroup, not needed\n"), nil},
		{"line bulleted", with("- Libraries considered: none found\n"), nil},
		{"one of two lines filled", with("Libraries considered: <x>\nLibraries considered: testify, used\n"), nil},
		{"runs go test", "RESULT: x sha=abc\nRun go test -timeout 600s ./internal/x/\n\n" + rules, lib},
		{"runs go build", "RESULT: x sha=abc\nThen go build ./...\n\n" + rules, lib},
		{"runs go run", "RESULT: x sha=abc\nTry go run ./cmd/x\n\n" + rules, lib},
		{"runs go generate", "RESULT: x sha=abc\nRun go generate ./...\n\n" + rules, lib},
		{"writes a go file", "RESULT: x sha=abc\nWrite a helper in internal/x/y.go\n\n" + rules, lib},
		{"adds to a go file", "RESULT: x sha=abc\nAdd a case to internal/x/y.go.\n\n" + rules, lib},
		{"implements in a go file", "RESULT: x sha=abc\nImplement the verb in cmd/x/main.go\n\n" + rules, lib},
		{"only reads a go file", "RESULT: x sha=abc\nRead internal/x/y.go and report what it does.\n\n" + rules, nil},
		{"cites pkg.go.dev", "RESULT: x sha=abc\nWrite up what pkg.go.dev says about errgroup.\n\n" + rules, nil},
		{"runs go vet only", "RESULT: x sha=abc\nRun go vet ./internal/x/\n\n" + rules, nil},
		{"builds no code", "RESULT: x sha=abc\nRewrite the README paragraph on seats.\n\n" + rules, nil},
		{"rules paragraph alone", rules, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.ElementsMatch(t, tc.want, childChecks(LintCardChild([]byte(tc.card))))
		})
	}
}
