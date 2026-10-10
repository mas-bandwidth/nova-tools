package swarm

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	require.NoError(t, err)
	return rules
}

// ourCard is the card template with this repository's RULES paragraph in place of the
// general one.
func ourCard(t *testing.T) string {
	t.Helper()
	return strings.Replace(childTemplate(t), ChildRulesParagraph(), RulesParagraph(ourRules(t)), 1)
}

// childTemplateRaw is the card template as printed: its Libraries considered line still
// carries the writer's placeholder.
func childTemplateRaw(t *testing.T) string {
	t.Helper()
	body, err := Template("card")
	require.NoError(t, err)
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

// Every row of the default set and of this repository's file names itself in kebab case
// once, carries a sentence that ends like a sentence and a source, and has its remedy; every
// scan carries a remedy, and the remedy table holds one row per default rule and one per scan.
func TestChildRulesTableIsWellFormed(t *testing.T) {
	t.Parallel()
	name := regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)
	for set, rules := range map[string][]ChildRule{"default": DefaultChildRules, "ours": ourRules(t)} {
		seen := map[string]bool{}
		for _, r := range rules {
			assert.True(t, name.MatchString(r.Name), "%s: rule name %q is not a unique kebab-case token", set, r.Name)
			assert.False(t, seen[r.Name], "%s: rule name %q is not a unique kebab-case token", set, r.Name)
			seen[r.Name] = true
			assert.Equal(t, strings.TrimSpace(r.Sentence), r.Sentence, "%s: rule %s: sentence %q is not a whole rule", set, r.Name, r.Sentence)
			assert.GreaterOrEqual(t, len(r.Sentence), 12, "%s: rule %s: sentence %q is not a whole rule", set, r.Name, r.Sentence)
			assert.NotEmpty(t, strings.TrimSpace(r.Source), "%s: rule %s names no source", set, r.Name)
			remedy := ChildRemedy(rules, "rule-"+r.Name)
			assert.Contains(t, remedy, r.Sentence, "%s: rule %s: remedy %q does not quote the sentence and its source", set, r.Name, remedy)
			assert.Contains(t, remedy, r.Source, "%s: rule %s: remedy %q does not quote the sentence and its source", set, r.Name, remedy)
		}
	}
	for _, r := range DefaultChildRules {
		got := CardChildRemedies["rule-"+r.Name]
		assert.Equal(t, ChildRemedy(DefaultChildRules, "rule-"+r.Name), got, "the listing's remedy for %s differs from the lint's: %q", r.Name, got)
	}
	for _, s := range childScans {
		assert.True(t, strings.HasPrefix(s.Check, "step-"), "scan %s is not a step-<what> token with a remedy", s.Check)
		assert.NotEqual(t, "", strings.TrimSpace(CardChildRemedies[s.Check]), "scan %s is not a step-<what> token with a remedy", s.Check)
		got := ChildRemedy(nil, s.Check)
		assert.Equal(t, s.Remedy, got, "scan %s: ChildRemedy answers %q, want its remedy", s.Check, got)
		if s.Needs != "" {
			found := false
			for _, r := range ourRules(t) {
				found = found || r.Name == s.Needs
			}
			assert.True(t, found, "scan %s needs the rule %q, which this repository's file does not carry", s.Check, s.Needs)
		}
	}
	// one per default rule, one per scan, the libraries-considered line of the lint and the empty card
	want := len(DefaultChildRules) + len(childScans) + 2
	assert.Len(t, CardChildRemedies, want, "the remedy table holds %d tokens, want %d: one per default rule and one per scan, the libraries-considered line and the empty card", len(CardChildRemedies), want)
	got := ChildRemedy(DefaultChildRules, "rule-nothing")
	assert.Empty(t, got, "a token that is no rule has the remedy %q", got)
	// the general set is only the general rules: nothing of one repository's
	for _, r := range DefaultChildRules {
		for _, project := range []string{"GOCACHE", "go test", "-timeout", "t.Parallel", "Co-Authored-By", "functionalrun", "./internal/ci", "Claude"} {
			assert.NotContains(t, r.Sentence, project, "the default rule %s names %q: a project's rule belongs in its rules file", r.Name, project)
		}
	}
	// the rules this repository's coordinator gives every child are rows of its file
	have := map[string]bool{}
	for _, r := range ourRules(t) {
		have[r.Name] = true
	}
	assert.Contains(t, CardChildRemedies[LibrariesConsideredRule], "Libraries considered:")
	for _, need := range []string{
		"gocache", "no-go-clean", "no-redis-server", "no-kill", "go-test-timeout", "no-rm-rf", "no-force-push", "no-rebase",
		"functional-in-container", "parallel", "class-tests", "no-names", "present-tense", "commit-trailer", "pr-line",
		"never-merge", "pr-diffstat", "report-not-done", "no-stash",
	} {
		assert.True(t, have[need], "the rule %s the coordinator gives every child is not a row of fleet/child-rules.txt", need)
	}
}

// The template is the shape the coordinator starts from: it carries every default rule
// sentence, and once the writer has filled its Libraries considered line it lints clean. As
// printed the line still carries its placeholder, and that is the one finding it draws.
func TestChildTemplateLintsClean(t *testing.T) {
	t.Parallel()
	body := childTemplate(t)
	got := LintCardChild([]byte(body))
	require.Empty(t, got, "the filled card template draws findings: %v", got)
	assert.Empty(t, LintCardChild([]byte(childTemplateRaw(t))), "the default rules carry no libraries check")
	rawOurs := strings.Replace(childTemplateRaw(t), ChildRulesParagraph(), RulesParagraph(ourRules(t)), 1)
	raw := LintCardChildWith([]byte(rawOurs), ourRules(t))
	require.Len(t, raw, 1)
	assert.Equal(t, LibrariesConsideredRule, raw[0].Check)
	assert.Contains(t, raw[0].Excerpt, "unfilled: Libraries considered: <")
	for _, r := range DefaultChildRules {
		assert.Contains(t, body, r.Sentence, "the card template does not quote the rule %s: %s", r.Name, r.Sentence)
	}
	assert.True(t, IsCardContractLine(strings.SplitN(body, "\n", 2)[0]), "line 1 of the template is not the contract line")
	// this repository's own card, with its paragraph, is admitted under its own rules
	got = LintCardChildWith([]byte(ourCard(t)), ourRules(t))
	assert.Empty(t, got, "a card carrying this repository's rules draws findings under them: %v", got)
}

// A card for any other project, with no Go in it, is admitted under the default rules: the
// rules of one repository (its caches, its test flags, its trailer) are not the tool's.
func TestChildNonGoCardIsAdmittedUnderTheDefaultRules(t *testing.T) {
	t.Parallel()
	card := "RESULT: docs sha=abc\nFix the typo in README.md, then run make check.\n\n" + ChildRulesParagraph() + "\nSTEP 1. cd site && npm test\nSTEP 2. git push origin HEAD\n"
	got := LintCardChild([]byte(card))
	require.Empty(t, got, "a non-Go card with the default rules draws %v", got)
	// the same card under this repository's rules is refused for what it does not carry
	got = LintCardChildWith([]byte(card), ourRules(t))
	assert.GreaterOrEqual(t, len(got), 10, "the card carries none of this repository's rules and draws only %d findings", len(got))
	// a Go project's scans run only where its rules are in the set
	goCard := card + "STEP 3. go test ./...\nSTEP 4. go clean -cache\n"
	got = LintCardChild([]byte(goCard))
	assert.Empty(t, got, "the default set scans Go commands it has no rule for: %v", got)
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
	require.NoError(t, err)
	card := "RESULT: x sha=abc\n\nRULES.\nSay which environment the change is for.\nQuote the ticket number.\n\nSTEP 1. cd x\n"
	got := LintCardChildWith([]byte(card), rules)
	require.Empty(t, got, "a card carrying both sentences draws %v", got)
	got = LintCardChildWith([]byte(strings.Replace(card, "Quote the ticket number.\n", "", 1)), rules)
	require.Len(t, got, 1, "a card without the unnamed third line's sentence draws %v, want rule-line3", got)
	require.Equal(t, "rule-line3", got[0].Check, "a card without the unnamed third line's sentence draws %v, want rule-line3", got)
	require.Contains(t, got[0].Excerpt, "Quote the ticket number.", "a card without the unnamed third line's sentence draws %v, want rule-line3", got)
	got = LintCardChildWith([]byte(strings.Replace(card, "Say which environment the change is for.\n", "", 1)), rules)
	require.Len(t, got, 1, "a card without the named sentence draws %v, want rule-deploy-note", got)
	require.Equal(t, "rule-deploy-note", got[0].Check, "a card without the named sentence draws %v, want rule-deploy-note", got)
	r := ChildRemedy(rules, "rule-deploy-note")
	assert.Contains(t, r, "Say which environment", "the remedy %q does not quote the sentence and its line in the file", r)
	assert.Contains(t, r, "rules.txt:2", "the remedy %q does not quote the sentence and its line in the file", r)
}

// A rules file is one sentence per line; every problem of a bad file is reported together.
func TestParseChildRules(t *testing.T) {
	t.Parallel()
	rules, err := ParseChildRules("# a comment\n\n[a-b] First   rule  here.\r\nSecond rule here.\n  \n", "f.txt")
	require.NoError(t, err, "got %v, %v", rules, err)
	require.Len(t, rules, 2, "got %v, %v", rules, err)
	assert.Equal(t, (ChildRule{"a-b", "First rule here.", "f.txt:3"}), rules[0], "rules %+v", rules)
	assert.Equal(t, (ChildRule{"line4", "Second rule here.", "f.txt:4"}), rules[1], "rules %+v", rules)
	_, err = ParseChildRules("[Bad Name] x y z\n[ok] same sentence\n[ok] another\n[two] same sentence\n[open sentence\n[named]\n", "f.txt")
	require.Error(t, err, "a file with six problems is accepted")
	for _, want := range []string{"line 1", "line 3", "line 4", "line 5", "line 6"} {
		assert.Contains(t, err.Error(), want, "the refusal %q does not name %s", err, want)
	}
	for _, empty := range []string{"", "\n\n", "# only a comment\n"} {
		_, err := ParseChildRules(empty, "f.txt")
		assert.ErrorContains(t, err, "holds no rule", "%q: got %v, want the empty-file refusal", empty, err)
	}
	_, err = ReadChildRules("testdata/no-such-rules-file")
	assert.Error(t, err, "an unreadable rules file is accepted")
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
		presence := 0
		for _, r := range c.rules {
			if r.Name == LibrariesConsideredName {
				continue // read as a filled line of a card that builds code, not as a quoted sentence
			}
			presence++
			without := strings.Replace(c.body, r.Sentence+"\n", "", 1)
			require.NotEqual(t, c.body, without, "%s: the card does not carry %s on a line of its own", set, r.Name)
			got := LintCardChildWith([]byte(without), c.rules)
			if assert.Len(t, got, 1, "%s: a card without %s draws %v, want exactly rule-%s naming the sentence", set, r.Name, got, r.Name) {
				assert.Equal(t, "rule-"+r.Name, got[0].Check, "%s: a card without %s draws %v, want exactly rule-%s naming the sentence", set, r.Name, got, r.Name)
				assert.Equal(t, 1, got[0].Line, "%s: a card without %s draws %v, want exactly rule-%s naming the sentence", set, r.Name, got, r.Name)
				assert.Contains(t, got[0].Excerpt, r.Sentence, "%s: a card without %s draws %v, want exactly rule-%s naming the sentence", set, r.Name, got, r.Name)
			}
		}
		bare := LintCardChildWith([]byte("RESULT: x sha=abc\nfix the thing\n"), c.rules)
		assert.Len(t, bare, presence, "%s: a card with no rule draws %d findings, want %d", set, len(bare), presence)
	}
}

// A rule the writer wrapped over two lines, or indented, is still quoted.
func TestChildRuleSurvivesWrapping(t *testing.T) {
	t.Parallel()
	body := childTemplate(t)
	r := DefaultChildRules[len(DefaultChildRules)-3]
	words := strings.Fields(r.Sentence)
	wrapped := strings.Join(words[:len(words)/2], " ") + "\n   " + strings.Join(words[len(words)/2:], " ")
	got := LintCardChild([]byte(strings.Replace(body, r.Sentence, wrapped, 1)))
	assert.Empty(t, got, "a wrapped %s is refused: %v", r.Name, got)
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
		{"step-push-proof",
			[]string{
				"STEP 7. Run git ls-remote and verify remote tip equals HEAD",
				"STEP 7. Parent: abc123",
				"STEP 7. Send proof of push",
				"STEP 7. Run git ls-remote and check friendcards.go uses it",
			},
			[]string{
				"STEP 7. Head: e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
				"STEP 7. Check friendcards.go ls-remote logic",
			}},
	}
	covered := map[string]bool{}
	for _, c := range cases {
		covered[c.check] = true
		for _, line := range c.bad {
			got := scanFindings(t, line)
			if assert.Len(t, got, 1, "%q draws %v, want exactly %s", line, got, c.check) {
				assert.Equal(t, c.check, got[0], "%q draws %v, want exactly %s", line, got, c.check)
			}
		}
		for _, line := range c.ok {
			got := scanFindings(t, line)
			assert.Empty(t, got, "%q draws %v, want none", line, got)
		}
	}
	for _, s := range childScans {
		assert.True(t, covered[s.Check], "the scan %s has no violating case in this test", s.Check)
	}
}

// The finding carries the line number of the offending line and the line's text.
func TestChildScanNamesTheLine(t *testing.T) {
	t.Parallel()
	body := childTemplate(t) + "STEP 7. git stash\n"
	got := LintCardChild([]byte(body))
	want := strings.Count(body, "\n") // the last line
	require.Len(t, got, 1, "got %v, want step-stash at line %d", got, want)
	require.Equal(t, "step-stash", got[0].Check, "got %v, want step-stash at line %d", got, want)
	require.Equal(t, want, got[0].Line, "got %v, want step-stash at line %d", got, want)
	require.Equal(t, "STEP 7. git stash", got[0].Excerpt, "got %v, want step-stash at line %d", got, want)
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
			assert.False(t, got, "%s: scanned", name)
			continue
		}
		assert.Equal(t, c.hit, got, "%s: step-stash found=%v, want %v", name, got, c.hit)
	}
	// the same paragraph in the template, with a force-push in it, is refused too
	tmpl := strings.Replace(childTemplate(t), "THE TASK. <", "THE TASK. Then git push --force. <", 1)
	got := LintCardChild([]byte(tmpl))
	if assert.Len(t, got, 1, "a force-push in the template's task paragraph draws %v", got) {
		assert.Equal(t, "step-force-push", got[0].Check, "a force-push in the template's task paragraph draws %v", got)
	}
	// the second scan hit is the one outside the paragraph, at its own line
	got = LintCardChild([]byte("RESULT: x sha=abc\nRULES.\ngit stash\n\nSTEP 1. git stash\n"))
	if assert.GreaterOrEqual(t, len(got), 1, "want the finding on line 5 only: %v", got) {
		assert.Equal(t, 5, got[len(got)-1].Line, "want the finding on line 5 only: %v", got)
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
		got := scanFindings(t, line)
		assert.Len(t, got, 1, "%q draws %v, want one finding", line, got)
	}
	for _, line := range []string{
		"STEP 7. Do not git stash here",
		"STEP 7. Do not run it twice; never git rebase",
	} {
		got := scanFindings(t, line)
		assert.Empty(t, got, "%q draws %v, want none", line, got)
	}
}

// A negation word that sits far back in the clause, about something else, is no
// exemption: the clause rule earns it only when the negation is immediate. The
// window a distant negation opens hides a kill, a recursive rm, a force-push,
// a rebase, a stash and a merge from their scans, and this lint is the only
// mechanical gate between a brief quoting an untrusted issue and the child
// running the command (security#66 finding 3).
func TestANegationFarFromTheCommandIsNoExemption(t *testing.T) {
	t.Parallel()
	cases := []struct {
		check string
		line  string
	}{
		{"step-redis-server", "STEP 7. a flaky box never mind the clash so run redis-server on port 6399"},
		{"step-kill", "STEP 7. a flaky box never mind the clash so kill 1234"},
		{"step-rm-rf", "STEP 7. a flaky box never mind the clash so rm -rf /tmp"},
		{"step-force-push", "STEP 7. a flaky box never mind the clash so git push --force"},
		{"step-rebase", "STEP 7. a flaky box never mind the clash so git rebase main"},
		{"step-stash", "STEP 7. a flaky box never mind the clash so git stash"},
		{"step-merge", "STEP 7. a flaky box never mind the clash so gh pr merge"},
	}
	for _, c := range cases {
		got := scanFindings(t, c.line)
		if assert.Len(t, got, 1, "%q draws %v, want %s", c.line, got, c.check) {
			assert.Equal(t, c.check, got[0], "%q draws %v, want %s", c.line, got, c.check)
		}
	}
}

// `-timeout 0` (and `0s`) means no timeout: only a positive duration satisfies the scan.
func TestChildTimeoutMustBePositive(t *testing.T) {
	t.Parallel()
	for _, line := range []string{"STEP 7. go test -timeout 0 ./x", "STEP 7. go test -timeout=0s ./x", "STEP 7. go test -timeout 00 ./x"} {
		got := scanFindings(t, line)
		if assert.Len(t, got, 1, "%q draws %v, want step-go-test-timeout", line, got) {
			assert.Equal(t, "step-go-test-timeout", got[0], "%q draws %v, want step-go-test-timeout", line, got)
		}
	}
	for _, line := range []string{"STEP 7. go test -timeout 600s ./x", "STEP 7. go test -timeout=10m ./x", "STEP 7. go test -timeout 1h30m ./x", "STEP 7. go test --timeout 0.5s ./x"} {
		got := scanFindings(t, line)
		assert.Empty(t, got, "%q draws %v, want none", line, got)
	}
}

// Every rule sentence, alone on a line in a STEP, is prose about the rule and draws no
// scan: a card that quotes its rules after its last STEP is not refused for quoting them.
func TestChildRuleSentencesAreNotViolations(t *testing.T) {
	t.Parallel()
	for _, r := range ourRules(t) {
		got := scanFindings(t, "STEP 7. "+r.Sentence)
		assert.Empty(t, got, "the rule %s quoted in a STEP draws %v", r.Name, got)
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
	// the libraries check is on where the rule set carries the rule; here it carries only that
	libRules := []ChildRule{{Name: LibrariesConsideredName, Sentence: "A card that builds code carries a filled Libraries considered line.", Source: "the test"}}
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
			assert.ElementsMatch(t, tc.want, childChecks(LintCardChildWith([]byte(tc.card), libRules)))
		})
	}
}

// The libraries check is the rule set's switch: the default set, which names no such rule,
// never draws it, and this repository's file does.
func TestChildLibrariesCheckIsOnlyWhereTheRuleSetCarriesIt(t *testing.T) {
	t.Parallel()
	card := "RESULT: x sha=abc\nRun go test -timeout 600s ./internal/x/\n\n" + ChildRulesParagraph()
	assert.Empty(t, childChecks(LintCardChild([]byte(card))))
	ours := strings.Replace(card, ChildRulesParagraph(), RulesParagraph(ourRules(t)), 1)
	assert.Contains(t, childChecks(LintCardChildWith([]byte(ours), ourRules(t))), LibrariesConsideredRule)
	assert.Contains(t, ChildRemedy(ourRules(t), LibrariesConsideredRule), "Libraries considered:")
}

// A STEP that asks for a push proof draws step-push-proof at that STEP's own line
// (goal of the-finish-form-is-one-line-bbc).
func TestAPushProofStepIsRefused(t *testing.T) {
	t.Parallel()
	for _, line := range []string{
		"STEP 1. Run git ls-remote and check if remote tip equals HEAD",
		"STEP 2. Report ls-remote matched HEAD: yes",
		"STEP 3. Parent: abc1234",
		"STEP 4. Send proof of push",
		"STEP 5. Verify remote tip matches HEAD",
		"STEP 6. Run git ls-remote and check friendcards.go uses it",
	} {
		card := ourCard(t) + line + "\n"
		want := strings.Count(card, "\n") // the line just appended, the card's last
		got := LintCardChildWith([]byte(card), ourRules(t))
		if assert.Len(t, got, 1, "%q draws %v, want step-push-proof", line, got) {
			assert.Equal(t, "step-push-proof", got[0].Check, "%q draws %v, want step-push-proof", line, got)
			assert.Equal(t, want, got[0].Line, "%q draws %v, want it at line %d", line, got, want)
		}
	}
}

// The friend report's `Head:` line, prose that names friendcards.go's own ls-remote, and the
// bare word `push` are not a push proof (goal of the-finish-form-is-one-line-bbc).
func TestAHeadLineIsNotAPushProof(t *testing.T) {
	t.Parallel()
	for _, line := range []string{
		"Head: e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		"STEP 1. Run push",
		"STEP 2. Check friendcards.go ls-remote logic",
		"STEP 3. git push origin HEAD",
		"STEP 4. The git shim records a push; the member makes both from outside the wall",
		"STEP 5. The result equals the head of the list",
		"STEP 6. The label matches the head of the table",
	} {
		got := scanFindings(t, line)
		assert.Empty(t, got, "%q draws %v, want none", line, got)
	}
	assert.Empty(t, childChecks(LintCardChildWith([]byte(ourCard(t)), ourRules(t))), "a brief with none of these draws none")
}

// A STEP that asks to run git ls-remote is refused even when it mentions that friendcards.go
// uses it; the exception allows only prose describing the machine-owned ls-remote.
func TestAPushProofStepMentioningFriendcardsIsRefused(t *testing.T) {
	t.Parallel()
	for _, line := range []string{
		"STEP 1. Run git ls-remote and check friendcards.go uses it",
		"STEP 2. Run git ls-remote origin feat, which friendcards.go uses",
		"STEP 3. git ls-remote origin (friendcards.go uses it)",
	} {
		card := ourCard(t) + line + "\n"
		want := strings.Count(card, "\n")
		got := LintCardChildWith([]byte(card), ourRules(t))
		if assert.Len(t, got, 1, "%q draws %v, want step-push-proof", line, got) {
			assert.Equal(t, "step-push-proof", got[0].Check, "%q draws %v, want step-push-proof", line, got)
			assert.Equal(t, want, got[0].Line, "%q draws %v, want it at line %d", line, got, want)
		}
	}
}
