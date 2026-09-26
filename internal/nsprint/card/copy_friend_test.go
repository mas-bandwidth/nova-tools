package card_test

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
)

func friendCopy(leg string) card.CopyCard {
	c := card.CopyCard{ID: "p1~2", Primary: "p1", Leg: leg, Kind: "build", Repo: "mas-bandwidth/nova-tools",
		Base: "dev", BaseSHA: strings.Repeat("ab", 20), Paths: "internal/x.go", DoneWhen: "TestX passes",
		Title: "one verb", Origin: "issue:nova-tools#4233", Stream: "swarm: cards", Consumer: "friend:rowan",
		Body: "the issue text"}
	if leg != "work" {
		c.PR, c.Head, c.Branch, c.Finding = "4300", strings.Repeat("cd", 20), "nova/copies/p1-c1-a1", "PATHS too narrow"
	}
	return c
}

// TestFriendCopyRendersThePersonsBrief (#4233, Glenn 2026-09-26 9:03 AM
// ET: friends run themselves and pull from their ready queue): a copy whose
// consumer is a friend renders the person's brief, not the wrapper's card:
// the same linted header, and a body that tells the friend to clone at
// BASE/base_sha, branch as the wrapper would, commit, push, open the PR
// under its own identity and end the copy with card end (--ok --pr --head,
// --fail, or --score for a read), beating with friend beat meanwhile. No
// line names the wrapper or forbids the push.
func TestFriendCopyRendersThePersonsBrief(t *testing.T) {
	t.Parallel() // the linter's pass over a friend's card is TestReadCopyDealtToBenchRendersALintedCard's (it reads the mirror)
	for _, tc := range []struct {
		leg  string
		want []string
	}{
		{"work", []string{
			"RESULT: p1.c2 sha=abababababab\n", "\nKIND: model\n", "\nBASE: dev\n", "\nbase-sha: " + strings.Repeat("ab", 20) + "\n",
			"\nDONE-WHEN: TestX passes\n", "\nCOPY: p1~2\n",
			"FRIEND: friend:rowan owns this copy end to end (#4233)",
			"CLONE: git clone --depth 50 --single-branch --branch dev https://", "/mas-bandwidth/nova-tools p1.c2 (from your mirror when you keep one)",
			"checkout " + strings.Repeat("ab", 20) + " and git -C p1.c2 checkout -b nova/copies/p1-c2-a2.\n",
			"commit on nova/copies/p1-c2-a2 with the DONE-WHEN summary as the first line, push nova/copies/p1-c2-a2 and open the PR against dev",
			"END: nova-sprint friend done --as friend:rowan --id p1~2 --ok --pr nova-tools#<n> --head <sha>",
			"nova-sprint friend done --as friend:rowan --id p1~2 --fail '<why>'",
			"nova-sprint card owner --as friend:rowan --id p1~2 --token <claim-token> --pid <harness-pid>",
			"Missing, remote or unreadable identity is UNKNOWN and does not renew;",
			"quoted below (issue:nova-tools#4233)",
			"\n---\n> the issue text\n",
		}},
		{"read", []string{
			"\nKIND: read\n", "\nROUTE: pro\n", "\nPR: mas-bandwidth/nova-tools#4300\n",
			"\nDONE-WHEN: this copy is ended with the score of nova-tools#4300 at head " + strings.Repeat("cd", 20) + ": nova-sprint friend done --as friend:rowan --id p1~2 --score N/10\n",
			"FRIEND: friend:rowan reads this PR itself (#4233)",
			"DO: read nova-tools#4300 at head " + strings.Repeat("cd", 20) + " against dev@" + strings.Repeat("ab", 20),
			"END: nova-sprint friend done --as friend:rowan --id p1~2 --score N/10 --gates ci:<green|red>,base:<ok|behind>,scope:<ok|over> --finding '<one line>'",
			"nova-sprint friend done --as friend:rowan --id p1~2 --fail 'ABSTAIN <why>'",
		}},
		{"fix", []string{
			"\nKIND: fix\n", "\nbase-sha: " + strings.Repeat("cd", 20) + "\n", "\nBRANCH: nova/copies/p1-c1-a1\n",
			"FRIEND: friend:rowan fixes this PR itself (#4233)",
			"CLONE: git clone --depth 50 --single-branch --branch nova/copies/p1-c1-a1 https://", "/mas-bandwidth/nova-tools p1.c2 (from your mirror when you keep one), then git -C p1.c2 checkout " + strings.Repeat("cd", 20) + ".\n",
			"the read found: PATHS too narrow. Change only PATHS, close the finding, commit on top of " + strings.Repeat("cd", 20),
			"push to nova/copies/p1-c1-a1 under your own GitHub identity",
			"END: nova-sprint friend done --as friend:rowan --id p1~2 --ok --pr nova-tools#<n> --head <sha>",
			"nova-sprint card owner --as friend:rowan --id p1~2 --token <claim-token> --pid <harness-pid>",
			"Missing, remote or unreadable identity is UNKNOWN and does not renew;",
		}},
	} {
		body, err := card.RenderCopy(friendCopy(tc.leg))
		if err != nil {
			t.Fatalf("%s: %v", tc.leg, err)
		}
		for _, want := range tc.want {
			if !strings.Contains(string(body), want) {
				t.Fatalf("%s brief lacks %q:\n%s", tc.leg, want, body)
			}
		}
		for _, never := range []string{"the wrapper", "never push", "never open a PR", "RESULT.md", "NO-SUBAGENTS", "card end", "your session's"} {
			if strings.Contains(string(body), never) {
				t.Fatalf("%s brief still says %q (a friend has no wrapper):\n%s", tc.leg, never, body)
			}
		}
	}
	// no origin: the DO line names the primary, never "()"
	noOrigin := friendCopy("work")
	noOrigin.Origin = ""
	body, err := card.RenderCopy(noOrigin)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "quoted below (primary p1)") || strings.Contains(string(body), "quoted below ()") {
		t.Fatalf("no-origin brief:\n%s", body)
	}
	// the same record dealt to a bench still renders the wrapper's card
	bench := friendCopy("work")
	bench.Consumer = "bench:b"
	body, err = card.RenderCopy(bench)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "the wrapper pushes the branch and opens the PR") || strings.Contains(string(body), "FRIEND:") {
		t.Fatalf("bench work copy:\n%s", body)
	}
}
