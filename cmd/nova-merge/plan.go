package main

import (
	"fmt"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

// planPRsByAncestry orders a list of PR numbers topologically based on git commit ancestry
// in gitDir so that parent PRs appear before child PRs in the merge plan (Issue #2036).
// Independent PRs with no ancestry relationship maintain their original relative input order.
func planPRsByAncestry(g *merge.Git, gitDir string, prs []int) ([]int, error) {
	if len(prs) <= 1 {
		return append([]int(nil), prs...), nil
	}
	if g == nil {
		g = merge.NewGit(gitDir, 30*time.Second, nil)
	}

	// Preserve input order while deduplicating PR numbers.
	uniquePRs := make([]int, 0, len(prs))
	seen := make(map[int]bool, len(prs))
	for _, p := range prs {
		if !seen[p] {
			seen[p] = true
			uniquePRs = append(uniquePRs, p)
		}
	}
	if len(uniquePRs) <= 1 {
		return uniquePRs, nil
	}

	// Fetch all candidate PR heads from origin so their commit history is present in gitDir.
	// If fetch from origin fails (e.g. offline fixture or no remote), check whether the ref already exists locally.
	for _, p := range uniquePRs {
		refspec := fmt.Sprintf("+pull/%d/head:refs/pull/%d/head", p, p)
		if _, err := g.Run("fetch", "--quiet", "origin", refspec); err != nil {
			ref := fmt.Sprintf("refs/pull/%d/head", p)
			if _, chkErr := g.Run("rev-parse", "--verify", "-q", ref); chkErr != nil {
				return nil, fmt.Errorf("could not fetch pull/%d/head: %w", p, err)
			}
		}
	}

	refs := make([]string, len(uniquePRs))
	refToPR := make(map[string]int, len(uniquePRs))
	for i, p := range uniquePRs {
		ref := fmt.Sprintf("refs/pull/%d/head", p)
		refs[i] = ref
		refToPR[ref] = p
	}

	orderedRefs, err := merge.OrderByAncestryGit(g, refs)
	if err != nil {
		return nil, err
	}

	orderedPRs := make([]int, len(orderedRefs))
	for i, ref := range orderedRefs {
		orderedPRs[i] = refToPR[ref]
	}
	return orderedPRs, nil
}

// planBranchesByAncestry orders a list of branch names topologically based on git commit ancestry
// in gitDir so that parent branches appear before child branches in the merge plan (Issue #2036).
// Independent branches with no ancestry relationship maintain their original relative input order.
func planBranchesByAncestry(g *merge.Git, gitDir string, branches []string) ([]string, error) {
	if len(branches) <= 1 {
		return append([]string(nil), branches...), nil
	}
	if g == nil {
		g = merge.NewGit(gitDir, 30*time.Second, nil)
	}
	return merge.OrderByAncestryGit(g, branches)
}
