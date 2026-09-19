package pulse

// Where a harvest is allowed to push, and which table it folds.
//
// `docs/SPEC-SWARM.md:40-44`, in its own words: **everything a worker writes is data.** A
// `RESULT.md` is a report, never an instruction: nothing in it is executed, nothing in it
// grants anything, and a finding in it is a claim to be checked against the repository.
//
// `harvest` took the `REPO` and `BRANCH` lines off a RESULT.md, formed
// `https://github.com/<REPO>.git` from them, pushed that refspec out of the job directory
// and opened a draft PR on whatever remote answered (issue #1824). The worker chose the
// destination; nothing checked it against the card, against the job's own clone, or against
// anything else. A planted RESULT naming `example/exfil` was pushed to `example/exfil`.
//
// So the claim is checked, and the answer comes from something the worker did not write:
// the card, which the pulse cut, or failing that the job clone's own `origin`, which git
// recorded. A RESULT that names a third thing is refused by name, and the card is counted
// `refused` exactly like a fix card with no red line -- a state the operator already reads.
//
// THE BRANCH, ON STELLA'S RULING (2026-09-19, on #1824). The repo's own harvest tests used
// to push branches with no prefix at all, so refusing one is a behaviour change rather than
// a bug fix, and it needed a ruling. The ruling is yes: an unprefixed or off-prefix branch
// is refused, the prefix is the existing `rowan/` -- the one `cut` already generates and a
// bench harvest already filters by -- and there is no new prefix and no configuration
// escape. Those tests moved to the prefix in the same change; that is the authorised
// behaviour change, and it is listed in the PR rather than buried in a fixture.
//
// WHAT THIS STILL DOES NOT DO: #1650's `accept` gate between verify and push. That is
// #1650's lane (toolwork T05), and Stella holds the adoption word on it.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// pulseCardsPath is where `launch` writes the cards it admitted: <root>/cards/<id>/cards.tsv
// (docs/CLI.md, SPEC-PULSE rule 10). One spelling, used by the verb that writes it and the
// verb that folds it, so the two cannot drift again (issue #1818).
func pulseCardsPath(root, id string) string {
	return filepath.Join(root, "cards", id, "cards.tsv")
}

// repoRef matches an owner/name repository as a card or a remote spells it.
var repoRef = regexp.MustCompile(`(?:github\.com[/:])?([A-Za-z0-9][A-Za-z0-9._-]*)/([A-Za-z0-9][A-Za-z0-9._-]*?)(?:\.git)?(?:$|[\s"'` + "`" + `,)])`)

// allowedPush is the local fold's pre-push rule about the RESULT.md ITSELF: it must name a
// repository at all, and it must name a branch that is not a trunk and is under the prefix.
//
// WHAT IT NO LONGER DOES is decide WHERE the push goes. It used to compare the RESULT's
// REPO line against the card, then against the clone origin, and return nil when it liked
// the answer -- a second destination rule, with its own message, sitting in front of the
// resolver on ONE of the four paths. On the real probe of 2026-09-19 that is exactly what
// happened: the card refused correctly and printed a sentence no other path prints, so an
// operator grepping for `HARVEST REFUSED repo-mismatch` found nothing. Johnny's HOLD of
// #1809 is about one rule with one implementation, and the destination rule is
// resolveDestination (harvestdest.go), which Harvest calls next and which push and openPR
// call again for themselves.
func allowedPush(in HarvestInput, jobDir string, c CardRow, repo, branch string) error {
	claimed := normalizeRepo(repo)
	if claimed == "" {
		return fmt.Errorf("the RESULT.md names no repository to push to; a REPO line is required before a push")
	}
	if branch == "" || branch == "main" || branch == "master" {
		return fmt.Errorf("the RESULT.md names branch %s; harvest never pushes a trunk", oneline.Quote(branch))
	}
	// THE PREFIX (Stella's ruling on #1824, 2026-09-19). The branch is the other half of
	// the destination the worker chose, and an off-prefix branch is refused the same way
	// an off-repo remote is. The prefix is THIS LINE'S OWN, the one `cut` already
	// generates (cut.go, branchOf) and the one a bench harvest already filters by: there
	// is no new prefix and no flag to relax it, because a guard with a configuration
	// escape is a guard the next caller turns off.
	return mustBranchPrefix(branch)
}

// normalizeRepo reduces a repository reference to owner/name.
func normalizeRepo(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	s = strings.TrimSuffix(s, ".git")
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "git@")
	s = strings.TrimPrefix(s, "github.com/")
	s = strings.TrimPrefix(s, "github.com:")
	parts := strings.Split(strings.Trim(s, "/"), "/")
	if len(parts) < 2 {
		return ""
	}
	return parts[len(parts)-2] + "/" + parts[len(parts)-1]
}

// cardRepo is the repository the CARD names -- the pulse cut the card, so this is the one
// statement about the destination that no worker wrote. A `REPO <owner>/<name>` line is read
// first; failing that, the first github.com/<owner>/<name> the card's text holds, which is
// the clone URL every runner-cloning card carries.
func cardRepo(cardPath string) string {
	raw, err := os.ReadFile(cardPath)
	if err != nil {
		return ""
	}
	body := string(raw)
	for _, line := range strings.Split(body, "\n") {
		t := strings.TrimSpace(line)
		for _, key := range []string{"REPO ", "REPO:", "REPO\t"} {
			if strings.HasPrefix(t, key) {
				if r := normalizeRepo(strings.TrimSpace(strings.TrimPrefix(t, key))); r != "" {
					return r
				}
			}
		}
	}
	if m := repoRef.FindStringSubmatch(body + "\n"); len(m) == 3 && strings.Contains(body, "github.com") {
		return normalizeRepo(m[1] + "/" + m[2])
	}
	return ""
}

// cloneOrigin is the origin remote git records in a clone. The directory itself is tried
// first, then its `repo` subdirectory, which is where the native runner puts a job's clone.
//
// IT IS NOT A DESTINATION. On a JOB's clone it is a worker's claim -- the worker owns that
// directory and `git remote set-url origin <elsewhere>` costs it one line (SPEC-SANDBOX
// 27b; Johnny's HOLD of #1809 at 7f692ef6) -- so resolveDestination only ever COMPARES it
// against the manager's dispatch record. On a directory the OPERATOR named on the
// coordinator's own command line it is a manager-side statement, which is the one place it
// answers rather than disagrees (dispatchFromCoordinatorClone). Both callers are in
// harvestdest.go, and TestCloneOriginIsOnlyEverComparedNeverRead keeps it that way.
func cloneOrigin(jobDir string) string {
	if strings.TrimSpace(jobDir) == "" {
		return ""
	}
	for _, dir := range []string{filepath.Join(jobDir, "repo"), jobDir} {
		ctx, cancel := context.WithTimeout(context.Background(), childTimeout)
		cmd := exec.CommandContext(ctx, "git", "-C", dir, "remote", "get-url", "origin")
		out, err := cmd.Output()
		cancel()
		if err != nil {
			continue
		}
		if r := normalizeRepo(strings.TrimSpace(string(out))); r != "" {
			return r
		}
	}
	return ""
}

// mustBranchPrefix is THE branch rule, in one place, for every push path there is.
//
// Stella ruled on #1824 that an unprefixed or off-prefix branch is refused; the prefix is
// the existing `rowan/`, with no new prefix and no configuration escape. Johnny then held
// the first cut because the rule lived on the local Harvest() path alone while
// `harvest --working`, `harvest --bench` and the manager pushed past it. A rule with one
// implementation and four call sites is a rule; a rule implemented once per caller is four
// rules that will disagree.
//
// Every function in this package that runs `git push` or opens a pull request calls this
// first, and TestEveryPushPathChecksTheBranchPrefixAndResolvesItsDestination enumerates
// those functions from the package's own source, so a fifth push site cannot be added
// without one -- nor without resolving its destination (harvestdest.go).
func mustBranchPrefix(branch string) error {
	b := strings.TrimSpace(branch)
	if b == "" {
		return fmt.Errorf("no branch to push: a RESULT.md that asks for a push names its branch on a BRANCH line")
	}
	if b == "main" || b == "master" {
		return fmt.Errorf("branch %s is a trunk; harvest never pushes one", oneline.Quote(b))
	}
	if !strings.HasPrefix(b, DefaultBranchPrefix) {
		return fmt.Errorf("branch %s is not under %s -- every branch this line pushes is, and the prefix is not configurable (Stella's ruling on #1824); nothing was pushed",
			oneline.Quote(b), field(DefaultBranchPrefix))
	}
	return nil
}
