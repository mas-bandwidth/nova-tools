package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// passingBrief is a brief that passes the card lint: what the writer says, then the RULES
// paragraph with every general rule (swarm.ChildRulesParagraph).
func passingBrief(lead string) string { return lead + "\n\n" + swarm.ChildRulesParagraph() }

// writeBrief writes a passing brief leading with lead under t.TempDir() and returns the
// path, for `add --brief-file`.
func writeBrief(t *testing.T, lead string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "brief.md")
	if err := os.WriteFile(path, []byte(passingBrief(lead)), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// add holds every brief, --brief and --brief-file alike, to the card lint in process: a
// brief that lacks the child rules or runs a forbidden command is refused, exit 2, with
// the lint's own lines naming each check and its remedy, and nothing is written. A card
// with no brief (a --count card, a sentinel) carries none to check and is admitted.
func TestAddRefusesABriefThatFailsTheCardLint(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	dir := t.TempDir()
	bare := filepath.Join(dir, "bare.md")
	if err := os.WriteFile(bare, []byte("handle the empty case\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	forbidden := filepath.Join(dir, "forbidden.md")
	if err := os.WriteFile(forbidden, []byte(passingBrief("handle the empty case")+"\nSTEP 2. redis-server --port 7000 && git push --force origin HEAD\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := ta.applies()
	for _, c := range []struct {
		name, line string
		want       []string
	}{
		{"bare --brief", "add --stream s1 --count 1 --brief 'handle the empty case'",
			[]string{"LINT DRIFT brief rule-worktree: 1: missing: Work only in the job directory this card names.", "rule-no-server", "rule-report-not-done", "remedy=the card quotes this rule verbatim", "nova-swarm template --name card"}},
		{"bare --brief-file", "add --stream s1 --count 1 --brief-file " + bare,
			[]string{"LINT DRIFT brief rule-worktree: 1: missing: Work only in the job directory this card names.", "fails the card lint"}},
		{"a forbidden command", "add --stream s1 --count 1 --brief-file " + forbidden,
			[]string{"LINT DRIFT brief step-redis-server: ", "LINT DRIFT brief step-force-push: ", "remedy=no line starts a redis-server"}},
	} {
		code, out, errs := ta.do(c.line)
		if code != 2 || strings.Contains(out, "MOVED") {
			t.Errorf("%s: exit %d, out %q; want exit 2 and nothing moved", c.name, code, out)
		}
		for _, w := range c.want {
			if !strings.Contains(errs, w) {
				t.Errorf("%s: stderr has no %q:\n%s", c.name, w, errs)
			}
		}
	}
	if ta.applies() != before {
		t.Fatal("a refused add wrote")
	}
	// no brief, no lint: a count card, an id card and a sentinel are admitted as before
	ta.ok("add --stream s1 --count 2")
	ta.ok("add --stream s1 s1-x")
	ta.ok("add --stream s1 --sentinel gate-1")
	// a brief that carries every rule is admitted, from the flag and from the file
	ta.ok("add --stream s2 --count 1 --brief-file " + writeBrief(t, "handle the empty case"))
	ta.ok("add --stream s3 --count 1 --brief '" + strings.ReplaceAll(passingBrief("handle the empty case"), "'", "") + "'")
}

// --max bounds the lint's lines like every listing: at most --max findings, then one
// MORE line naming how to see the rest.
func TestAddBriefLintLinesAreBounded(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	code, _, errs := ta.do("add --stream s1 --count 1 --max 3 --brief 'handle the empty case'")
	if code != 2 || strings.Count(errs, "LINT DRIFT brief ") != 3 || !strings.Contains(errs, "LINT MORE brief findings=") {
		t.Fatalf("exit %d; want three drifts and a MORE line:\n%s", code, errs)
	}
	_, _, all := ta.do("add --stream s1 --count 1 --max 0 --brief 'handle the empty case'")
	if got := strings.Count(all, "LINT DRIFT brief "); got != len(swarm.DefaultChildRules) {
		t.Fatalf("--max 0 prints every finding: %d, want %d (every rule missing)", got, len(swarm.DefaultChildRules))
	}
}

// A sentinel carries no brief and is exempt from the card lint: a sentinel given a brief
// that would fail the lint on any other card is admitted, and the lint writes nothing of
// its own for it.
func TestAddSentinelWithABriefIsNotLinted(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	code, out, errs := ta.do("add --stream s1 --sentinel gate-1 --brief 'stop here'")
	if code != 0 || strings.Contains(errs, "LINT") {
		t.Fatalf("a sentinel with a brief that fails the lint: exit %d, out %q, err %q; want admitted, not linted", code, out, errs)
	}
	// the same brief on a card that carries one is refused
	if code, _, errs := ta.do("add --stream s1 --count 1 --brief 'stop here'"); code != 2 || !strings.Contains(errs, "LINT DRIFT brief ") {
		t.Fatalf("the same brief on a --count card: exit %d, err %q; want the lint's refusal", code, errs)
	}
}

// writeRules writes a rules file under t.TempDir() and returns its path.
func writeRules(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rules.txt")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// ourRulesFile is this repository's own rules file, read relative to this package.
const ourRulesFile = "../../fleet/child-rules.txt"

// The tool is general: a brief for any other project, with none of one repository's rules
// in it, is admitted under the default rules, and the same brief is refused under a rules
// file naming rules it does not carry, each refusal naming the rule.
func TestAddAdmitsAnyProjectsBriefUnderTheDefaultRules(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1")
	brief := "Fix the typo in site/index.html, then run npm test and push the branch.\n\n" + swarm.ChildRulesParagraph()
	if code, _, errs := ta.do("add --stream web --count 1 --brief '" + strings.ReplaceAll(brief, "'", "") + "'"); code != 0 {
		t.Fatalf("a non-Go brief with the general rules: exit %d\n%s", code, errs)
	}
	code, _, errs := ta.do("add --stream web2 --count 1 --max 0 --rules " + ourRulesFile + " --brief '" + strings.ReplaceAll(brief, "'", "") + "'")
	if code != 2 || !strings.Contains(errs, "LINT DRIFT brief rule-gocache: 1: missing: Export a private GOCACHE") || !strings.Contains(errs, "LINT DRIFT brief rule-commit-trailer: ") {
		t.Fatalf("the same brief under this repository's file: exit %d\n%s", code, errs)
	}
}

// This repository's own file admits a card that carries its rules, and refuses one that
// lacks one, naming it; the rules file is read whole, one sentence per line.
func TestAddHoldsABriefToTheRulesFileItIsGiven(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1")
	ours, err := swarm.ReadChildRules(ourRulesFile)
	if err != nil {
		t.Fatal(err)
	}
	body := "handle the empty case\n\n" + swarm.RulesParagraph(ours)
	brief := filepath.Join(t.TempDir(), "brief.md")
	if err := os.WriteFile(brief, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	ta.ok("add --stream s1 --count 1 --rules " + ourRulesFile + " --brief-file " + brief)
	without := strings.Replace(body, "Every new test opens with `t.Parallel()`.\n", "", 1)
	if err := os.WriteFile(brief, []byte(without), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, errs := ta.do("add --stream s2 --count 1 --rules " + ourRulesFile + " --brief-file " + brief)
	if code != 2 || strings.Count(errs, "LINT DRIFT brief ") != 1 || !strings.Contains(errs, "LINT DRIFT brief rule-parallel: 1: missing: Every new test opens with `t.Parallel()`.") {
		t.Fatalf("a brief missing one named rule: exit %d\n%s", code, errs)
	}
}

// init --rules records the file for the sprint, add holds every brief to it without being
// told, --rules on the add overrides it, and a file that cannot serve is refused naming it.
func TestInitRulesIsTheSprintsRuleSet(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	rules := writeRules(t, "# docs project\n[ticket] Quote the ticket number.\n[tone] Write in the present tense.\n")
	if code, _, errs := ta.do("init --readers reader-a --members m1 --rules " + filepath.Join(t.TempDir(), "absent.txt")); code != 2 || !strings.Contains(errs, "--rules: ") || !strings.Contains(errs, "absent.txt") {
		t.Fatalf("init with an unreadable rules file: exit %d, %q", code, errs)
	}
	if code, _, errs := ta.do("init --readers reader-a --members m1 --rules " + writeRules(t, "# nothing\n")); code != 2 || !strings.Contains(errs, "holds no rule") {
		t.Fatalf("init with an empty rules file: exit %d, %q", code, errs)
	}
	ta.ok("init --readers reader-a --members m1 --rules " + rules)
	st, err := ta.a.store(common{redis: "mem:0", actor: "coordinator"})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := st.RulesPath(context.Background()); err != nil || got != rules {
		t.Fatalf("the recorded rules path is %q, %v; want %q", got, err, rules)
	}
	code, _, errs := ta.do("add --stream s1 --count 1 --max 0 --brief 'Fix the footer.'")
	if code != 2 || !strings.Contains(errs, "LINT DRIFT brief rule-ticket: 1: missing: Quote the ticket number.") || !strings.Contains(errs, "rule-tone") || strings.Contains(errs, "rule-worktree") {
		t.Fatalf("a brief under the recorded file: exit %d\n%s", code, errs)
	}
	ta.ok("add --stream s1 --count 1 --brief 'Quote the ticket number. Write in the present tense.'")
	// --rules on the add overrides the recorded file
	other := writeRules(t, "Keep it short.\n")
	ta.ok("add --stream s2 --count 1 --rules " + other + " --brief 'Keep it short.'")
	// a recorded file that is gone is named, and the add says how to go on
	if err := os.Remove(rules); err != nil {
		t.Fatal(err)
	}
	code, _, errs = ta.do("add --stream s3 --count 1 --brief 'Keep it short.'")
	if code != 2 || !strings.Contains(errs, "the sprint's rules file") || !strings.Contains(errs, "give --rules <file>") {
		t.Fatalf("a recorded file that is gone: exit %d, %q", code, errs)
	}
	ta.ok("add --stream s3 --count 1 --rules " + other + " --brief 'Keep it short.'")
	// a rules file with no brief to hold is a mistake, not a silent no-op
	if code, _, errs := ta.do("add --stream s4 --count 1 --rules " + other); code != 2 || !strings.Contains(errs, "gives no brief") {
		t.Fatalf("--rules with no brief: exit %d, %q", code, errs)
	}
}

// The model lines are part of the brief's lint: a brief that pins its model under line 1
// (model:, tokens:, deadline:) is admitted, and one whose model lines the deal could not
// read (an unknown tier, a pin that is not provider/model) is refused, exit 2, nothing
// written, naming the line.
func TestAddReadsTheBriefsModelLines(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1")
	code, _, errs := ta.do("add --stream s1 c1 --brief-file " + writeBrief(t, "c1: the work (s1) tier: pro\nmodel: anthropic/claude-x\ntokens: 5000\ndeadline: 600"))
	require.Equal(t, 0, code, "a pinned brief: %s", errs)
	for lead, want := range map[string]string{
		"c2: the work (s1) tier: medium":               "line 1 names tier medium",
		"c3: the work (s1) tier: pro\nmodel: claude-x": "model: claude-x is not <provider>/<model>",
	} {
		code, _, errs := ta.do("add --stream s1 --count 1 --brief-file " + writeBrief(t, lead))
		assert.Equal(t, 2, code, lead)
		assert.Contains(t, errs, want, lead)
	}
}
