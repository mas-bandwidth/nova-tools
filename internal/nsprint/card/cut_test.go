package card

import (
	"context"
	"strings"
	"testing"
)

// TestCutDependsVocabulary: an issue's DEPENDS-ON becomes the card
// vocabulary (#3623): #n and name#n are owner/name#n, a (WHY: ...) is the
// why, other parentheticals drop, and prose is refused.
func TestCutDependsVocabulary(t *testing.T) {
	t.Parallel()

	for _, c := range []struct{ in, deps, why, err string }{
		{"none", "none", "", ""},
		{"none (WHY: nothing)", "none", "nothing", ""},
		{"mas-bandwidth/nova-tools#3502 (WHY: the card record shape)", "mas-bandwidth/nova-tools#3502", "the card record shape", ""},
		{"#3502, rowan-tools#12 (landed), stream/swarm-cards, task:t1, card-7", "mas-bandwidth/nova-tools#3502,mas-bandwidth/rowan-tools#12,stream/swarm-cards,task:t1,card-7", "", ""},
		{"after the harvest lands", "", "", `DEPENDS-ON: "after the harvest lands" is not a card id, owner/repo#n, stream/<slug> or task:<id>`},
		{"(WHY: only a why)", "", "", "DEPENDS-ON names nothing; write none"},
	} {
		deps, why, err := CutDepends("mas-bandwidth/nova-tools", c.in)
		got := ""
		if err != nil {
			got = err.Error()
		}
		if deps != c.deps || why != c.why || got != c.err {
			t.Errorf("CutDepends(%q) = %q, %q, %q; want %q, %q, %q", c.in, deps, why, got, c.deps, c.why, c.err)
		}
	}
}

// TestIssueFieldsFirstLineWins: the first line of each key wins, list
// markers and bold keys are read, and a value that is one code span loses its
// backticks while one that contains code keeps them.
func TestIssueFieldsFirstLineWins(t *testing.T) {
	t.Parallel()

	f := IssueFields("intro\n- **DONE-WHEN:** `go test ./x` fails red\nbase-sha: `0123`\nSTREAM: a\nSTREAM: b\nWHY: none of it\n")
	for k, v := range map[string]string{"DONE-WHEN": "`go test ./x` fails red", "base-sha": "0123", "STREAM": "a", "WHY": "none of it"} {
		if f[k] != v {
			t.Errorf("%s = %q, want %q", k, f[k], v)
		}
	}
	if _, ok := f["PATHS"]; ok {
		t.Error("PATHS read from an issue that has none")
	}
}

type cutIssues map[int]Issue

func (c cutIssues) Issue(_ context.Context, _ string, n int) (Issue, error) { return c[n], nil }
func (c cutIssues) BranchSHA(context.Context, string, string) (string, error) {
	return strings.Repeat("ab", 20), nil
}

// TestRenderCutRefusesADoneWhenNoTestCanFail (#4313): a card whose DONE-WHEN
// names no `go test <pkg> -run <TestName>`, with no TEST line or a bare TEST:
// none, is refused at cut with the remedy, before any write; a DONE-WHEN with
// the test, a TEST line, or TEST: none <why> is cut with its TEST line.
func TestRenderCutRefusesADoneWhenNoTestCanFail(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	head := "STREAM: s\nPATHS: docs/CLI.md\nDEPENDS-ON: none\nBASE: dev\n"
	src := cutIssues{
		1: {Title: "no test", Body: head + "DONE-WHEN: the page reads right\n"},
		2: {Title: "bare none", Body: head + "DONE-WHEN: the page reads right\nTEST: none\n"},
		3: {Title: "from done-when", Body: head + "DONE-WHEN: `go test ./internal/x -run TestY` passes\n"},
		4: {Title: "test line", Body: head + "TEST: ./internal/x TestY\nDONE-WHEN: the page reads right\n"},
		5: {Title: "none with why", Body: head + "TEST: none one docs page; the reader checks it\nDONE-WHEN: the page reads right\n"},
	}
	for n, want := range map[int]string{1: "no TEST line", 2: "TEST: none says no why"} {
		_, err := RenderCut(ctx, src, CutInput{Sprint: "s", Repo: "acme/repo", Issue: n})
		if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "TEST: none <why") || strings.ContainsAny(err.Error(), "\n") {
			t.Errorf("issue %d: err %v, want one line with %q and the remedy", n, err, want)
		}
	}
	for n, want := range map[int]string{3: "TEST: ./internal/x TestY\n", 4: "TEST: ./internal/x TestY\n", 5: "TEST: none one docs page; the reader checks it\n"} {
		c, err := RenderCut(ctx, src, CutInput{Sprint: "s", Repo: "acme/repo", Issue: n})
		if err != nil || !strings.Contains(string(c.Body), "\n"+want) {
			t.Errorf("issue %d: err %v body:\n%s\nwant %q", n, err, c.Body, want)
		}
	}
}
