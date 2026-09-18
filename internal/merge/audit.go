package merge

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
)

// THE AUTO-MERGE AUDIT: find every standing instruction to land, and take it off.
//
// Auto-merge is a promise the forge keeps when nobody is there. On 2026-09-18 twenty-seven
// open pull requests carried one, every one of them left by a `gh pr merge` call made
// while its pull request was red, and four of them walked into the dev merge queue on
// their own as their checks went green. A hand sweep took them off that morning. This is
// that sweep, so that the next one is a verb with a count rather than a person noticing.
//
// It is the one place in the tools allowed to speak `gh pr merge` at all, in the ONE
// spelling that unmerges rather than merges -- `--disable-auto` -- built in one function
// and pinned byte for byte by its test. internal/ci's class rule refuses every other
// spelling in every non-test Go file and in .github/.

// AutoMergePR is one open pull request carrying an auto-merge, as the forge reports it.
type AutoMergePR struct {
	Number  int
	HeadRef string
	Title   string
}

// AuditHost is the audit's edge onto the forge: read the open pull requests that carry an
// auto-merge, and take one off.
type AuditHost interface {
	AutoMergePRs(ctx context.Context) ([]AutoMergePR, error)
	DisableAutoMerge(ctx context.Context, pr int) error
}

// AuditResult is what one pass found and did.
type AuditResult struct {
	Found    int
	Disabled int
	Failed   int
	// PRs is what was found, in the order the forge reported it, so the verb can name
	// every one on its own line -- a count with no names is a number nobody can check.
	PRs []AutoMergePR
	// Refused is the pull requests the forge would not clear.
	Refused []int
}

// Audit reads every open pull request carrying an auto-merge and disables it. A refusal on
// one does not end the pass: the rest are still cleared, and the ones that were not are
// counted and named, because a sweep that stopped at the first stubborn pull request would
// leave the other twenty-six armed.
//
// dry reports and writes nothing.
func Audit(ctx context.Context, host AuditHost, dry bool) (AuditResult, error) {
	if host == nil {
		return AuditResult{}, fmt.Errorf("no forge to audit; this tool reads a repository's auto-merges only through an AuditHost")
	}
	prs, err := host.AutoMergePRs(ctx)
	if err != nil {
		return AuditResult{}, err
	}
	res := AuditResult{Found: len(prs), PRs: prs}
	if dry {
		return res, nil
	}
	for _, pr := range prs {
		if err := host.DisableAutoMerge(ctx, pr.Number); err != nil {
			res.Failed++
			res.Refused = append(res.Refused, pr.Number)
			continue
		}
		res.Disabled++
	}
	return res, nil
}

// autoMergeListArgs is the read: the open pull requests and whether each carries an
// auto-merge request. The filtering is here rather than in a --jq expression so that a
// reader of this package sees the rule, and the fields are the three a refusal names.
func autoMergeListArgs(repo string) []string {
	return []string{"pr", "list", "-R", repo, "--state", "open", "--limit", "400",
		"--json", "number,headRefName,title,autoMergeRequest"}
}

// disableAutoMergeArgs is THE ONE ALLOWED `pr merge` SPELLING IN THE TOOLS, and the only
// reason it is allowed is that it unmerges: `gh pr merge <n> -R <repo> --disable-auto`
// withdraws the standing instruction and lands nothing.
//
// It is a function of its own so that the flag cannot go missing. `gh pr merge <n> -R
// <repo>` without it MERGES the pull request, which is one deleted word away from the
// accident this whole file exists about; TestDisableAutoMergeArgsCarryTheDisableFlagAndNothingElse
// asserts the list byte for byte, and internal/ci's class rule allows this function by name
// and refuses the spelling everywhere else.
func disableAutoMergeArgs(repo string, pr int) []string {
	return []string{"pr", "merge", strconv.Itoa(pr), "-R", repo, "--disable-auto"}
}

// AutoMergePRs reads the open pull requests that carry an auto-merge.
func (h *GHEnqueue) AutoMergePRs(ctx context.Context) ([]AutoMergePR, error) {
	out, err := h.gh(ctx, autoMergeListArgs(h.Repo)...)
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Number           int    `json:"number"`
		HeadRefName      string `json:"headRefName"`
		Title            string `json:"title"`
		AutoMergeRequest *struct {
			EnabledAt string `json:"enabledAt"`
		} `json:"autoMergeRequest"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, fmt.Errorf("gh pr list did not answer JSON this tool can read: %w", err)
	}
	var prs []AutoMergePR
	for _, r := range raw {
		if r.AutoMergeRequest == nil {
			continue
		}
		prs = append(prs, AutoMergePR{Number: r.Number, HeadRef: r.HeadRefName, Title: r.Title})
	}
	return prs, nil
}

// DisableAutoMerge takes the standing instruction off one pull request.
func (h *GHEnqueue) DisableAutoMerge(ctx context.Context, pr int) error {
	_, err := h.gh(ctx, disableAutoMergeArgs(h.Repo, pr)...)
	return err
}
