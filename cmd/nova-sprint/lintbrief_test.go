package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// passingBrief is a brief that passes the card lint: what the writer says, then the RULES
// paragraph with every rule the coordinator gives a child (swarm.ChildRulesParagraph).
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
			[]string{"LINT DRIFT brief rule-gocache: 1: missing: Export a private GOCACHE", "rule-no-redis-server", "rule-commit-trailer", "remedy=the card quotes this rule verbatim", "nova-swarm template --name card"}},
		{"bare --brief-file", "add --stream s1 --count 1 --brief-file " + bare,
			[]string{"LINT DRIFT brief rule-worktree: 1: missing: Work only in the NEW worktree this card names.", "fails the card lint"}},
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
	if got := strings.Count(all, "LINT DRIFT brief "); got != len(swarm.CardChildRules) {
		t.Fatalf("--max 0 prints every finding: %d, want %d (every rule missing)", got, len(swarm.CardChildRules))
	}
}

// The exit-codes rule the card lint requires is nova-sprint's own banner line, read from
// the usage text, so the rule and the tool cannot drift: a change to the codes the banner
// prints is red here until the row of swarm.CardChildRules says the same.
func TestExitCodesRuleIsTheBannersLine(t *testing.T) {
	t.Parallel()
	var line string
	for _, l := range strings.Split(banner(), "\n") {
		if strings.HasPrefix(l, "exit codes: ") {
			line = l
		}
	}
	if line == "" {
		t.Fatal("the usage text has no `exit codes:` line")
	}
	var row *swarm.ChildRule
	for i := range swarm.CardChildRules {
		if swarm.CardChildRules[i].Name == "exit-codes" {
			row = &swarm.CardChildRules[i]
		}
	}
	if row == nil || row.Sentence != line {
		t.Fatalf("the exit-codes rule is %+v; the banner prints %q", row, line)
	}
}
