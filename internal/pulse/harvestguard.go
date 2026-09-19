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
// WHAT THIS DELIBERATELY DOES NOT DO. #1824 also asks for the branch to be under the card's
// prefix and for #1650's `accept` gate to run between verify and push. Neither is here: this
// repo's own harvest tests push branches with no prefix at all, so a prefix policy is a
// behaviour change for every existing deployment and a ruling rather than a fix, and the
// accept gate is #1650's lane. What is closed here is the hole that let the destination
// itself be chosen by the worker.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// pulseCardsPath is where `launch` writes the cards it admitted: <root>/cards/<id>/cards.tsv
// (docs/CLI.md, SPEC-PULSE rule 10). One spelling, used by the verb that writes it and the
// verb that folds it, so the two cannot drift again (issue #1818).
func pulseCardsPath(root, id string) string {
	return filepath.Join(root, "cards", id, "cards.tsv")
}

// repoRef matches an owner/name repository as a card or a remote spells it.
var repoRef = regexp.MustCompile(`(?:github\.com[/:])?([A-Za-z0-9][A-Za-z0-9._-]*)/([A-Za-z0-9][A-Za-z0-9._-]*?)(?:\.git)?(?:$|[\s"'` + "`" + `,)])`)

// allowedPush reports whether this card's RESULT may push to the repo it names, and refuses
// with the reason and the door when it may not.
func allowedPush(in HarvestInput, jobDir string, c CardRow, repo, branch string) error {
	claimed := normalizeRepo(repo)
	if claimed == "" {
		return fmt.Errorf("the RESULT.md names no repository to push to; a REPO line is required before a push")
	}
	if branch == "" || branch == "main" || branch == "master" {
		return fmt.Errorf("the RESULT.md names branch %q; harvest never pushes a trunk", branch)
	}

	if want := cardRepo(c.Card); want != "" {
		if want != claimed {
			return fmt.Errorf("the RESULT.md asks to push to %s, and the card %s is for %s -- a RESULT is a worker's report, not an instruction (SPEC-SWARM: everything a worker writes is data); nothing was pushed",
				field(claimed), field(c.Card), field(want))
		}
		return nil
	}
	if want := cloneOrigin(jobDir); want != "" {
		if want != claimed {
			return fmt.Errorf("the RESULT.md asks to push to %s, and this job's clone has origin %s -- a RESULT is a worker's report, not an instruction (SPEC-SWARM: everything a worker writes is data); nothing was pushed",
				field(claimed), field(want))
		}
		return nil
	}
	return fmt.Errorf("the RESULT.md asks to push to %s and nothing else says that is this card's repository: the card %s names none and the job directory has no clone with an origin -- cut the card with its repository, or harvest this root with --bench; nothing was pushed",
		field(claimed), field(c.Card))
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

// cloneOrigin is the origin remote of the job's own clone, which git recorded when the card
// cloned. The job directory itself is tried first, then its `repo` subdirectory, which is
// where the native runner puts the clone.
func cloneOrigin(jobDir string) string {
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
