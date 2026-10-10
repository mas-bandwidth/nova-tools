package sprint

import (
	"fmt"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// FsckCheckName is the name of the landed-on-base check (docs/SPEC-SPRINT.md section 7,
// fsck-verb-landed-on-base-b.w2): every landed record's merge head (else head) is an
// ancestor of origin/<its base>.
const FsckCheckName = "landed-on-base"

// FsckGit is the git fact the command fills for one landed card, read in the clone the
// command keeps under the sprint root: repo is the card's repository as its brief names it,
// base its base branch, head its recorded head. tip is origin/base's tip commit id, on says
// git found head an ancestor of it, and why, when set, is why git could not say. A head the
// clone does not hold after the base's fetch is not on the base: on is false and why is
// empty, a violation, never an unreadable check.
type FsckGit func(repo, base, head string) (tip string, on bool, why string)

// LandOnBaseFinding is the result of the landed-on-base check.
type LandOnBaseFinding struct {
	Check    string `json:"check"`
	ID       string `json:"id"`
	Stream   string `json:"stream"`
	Head     string `json:"head"`
	Repo     string `json:"repo,omitempty"`
	Base     string `json:"base"`
	Tip      string `json:"tip,omitempty"`
	Ancestor bool   `json:"ancestor"`
	Why      string `json:"why,omitempty"`
	Fix      string `json:"fix"`
}

// Line is the finding as fsck prints it: OK or VIOLATION, the check and the values.
func (f LandOnBaseFinding) Line() string {
	if f.Why != "" {
		// could not check, report as violation with reason
		return fmt.Sprintf("FSCK VIOLATION check=%s subject=%s stream=%s repo=%s base=%s head=%s why=%s",
			oneline.Field(f.Check), oneline.Field(f.ID), oneline.Field(f.Stream),
			oneline.Field(f.Repo), oneline.Field(f.Base), oneline.Field(f.Head),
			oneline.Escape(f.Why))
	}
	if f.Ancestor {
		return fmt.Sprintf("FSCK OK check=%s subject=%s", oneline.Field(f.Check), oneline.Field(f.ID))
	}
	return fmt.Sprintf("FSCK VIOLATION check=%s subject=%s want=ancestor of %s tip=%s got=%s fix=%s",
		oneline.Field(f.Check), oneline.Field(f.ID), oneline.Field(f.Base), oneline.Field(f.Tip),
		oneline.Field(f.Head), oneline.Field(f.Fix))
}

// LandOnBase is the landed-on-base check over a snapshot with git facts filled by git.
// checks is the list of check names to filter (empty means all checks). git is asked once
// per landed card, in the clone the command keeps under the sprint root, so ancestry is
// decided against the repository and never against the process's own directory.
func LandOnBase(snap *Snapshot, checks []string, git FsckGit) []LandOnBaseFinding {
	var findings []LandOnBaseFinding
	for _, pr := range snap.Work.Column(Landed) {
		if IsSentinel(pr) {
			continue
		}
		f := checkLandOnBase(pr, checks, git)
		if f.ID != "" {
			findings = append(findings, f)
		}
	}
	return findings
}

// checkLandOnBase checks one landed card: its head is an ancestor of origin/<base>, a git
// fact the command reads in the card's repository clone. The repository is the brief's
// resolved REPO: URL (cb.Repo), the one git can clone and read, never its raw display name.
func checkLandOnBase(pr *Card, checks []string, git FsckGit) LandOnBaseFinding {
	// check if FsckCheckName is in the checks filter (empty checks means all checks)
	if len(checks) > 0 && !hasCheck(checks, FsckCheckName) {
		return LandOnBaseFinding{}
	}
	cb := swarm.ReadCardBase([]byte(pr.F("brief")))
	f := LandOnBaseFinding{Check: FsckCheckName, ID: pr.ID, Stream: pr.Row,
		Head: pr.F("head"), Repo: cb.Repo, Base: cb.Ref,
		Fix: "nova-sprint reopen " + pr.ID + " --reason <text>"}
	switch {
	case f.Head == "":
		f.Why = "no head recorded"
	case f.Base == "":
		f.Why = "its brief names no BASE: line; run: nova-sprint verify-landed --base <branch>"
	case f.Repo == "":
		f.Why = "its brief names no REPO: line; run: nova-sprint verify-landed --repo-dir <clone>"
	default:
		f.Tip, f.Ancestor, f.Why = git(f.Repo, f.Base, f.Head)
	}
	return f
}

func hasCheck(s []string, e string) bool {
	for _, v := range s {
		if v == e {
			return true
		}
	}
	return false
}
