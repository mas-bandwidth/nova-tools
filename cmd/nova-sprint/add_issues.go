package main

import (
	"github.com/mas-bandwidth/nova-tools/internal/forge"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// IssueRepoCheck is the brief lint's finding of an issue reference the card's landing could
// not close as written (docs/SPEC-SPRINT.md section 7, "A landing closes the card's issues"):
// a short form, owner/name#N, naming a repository other than the brief's REPO:, or #N in a
// brief that names no repository on GitHub. A full URL is the one form that names an issue of
// another repository, so add refuses the short one rather than close the wrong issue, or
// none, when the card lands.
const IssueRepoCheck = "issue-repo"

// briefIssueFindings is a brief's issue references that close nothing as written, one finding
// each, its excerpt the reason and the full URL to write instead (forge.Refs).
func briefIssueFindings(brief string) []swarm.CardHeaderFinding {
	var out []swarm.CardHeaderFinding
	for _, r := range forge.Refs(brief, forge.BriefRepo(brief)) {
		if r.Why != "" {
			out = append(out, swarm.CardHeaderFinding{Check: IssueRepoCheck, Line: r.Line, Excerpt: r.Why})
		}
	}
	return out
}
