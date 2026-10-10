package sprint

import (
	"fmt"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// FsckCheckName is the name of the landed-on-base check (docs/SPEC-SPRINT.md section 7,
// fsck-verb-landed-on-base-b.w2): every landed record's merge head (else head) is an
// ancestor of origin/<its base>.
const FsckCheckName = "landed-on-base"

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

// LandOnBase is the landed-on-base check over a snapshot with git facts filled by runGit.
// checks is the list of check names to filter (empty means all checks).
// runGit runs git commands and returns exit code, stdout, stderr.
func LandOnBase(snap *Snapshot, checks []string, runGit func(cmd string, args ...string) (int, string, string)) []LandOnBaseFinding {
	var findings []LandOnBaseFinding
	for _, pr := range snap.Work.Column(Landed) {
		if IsSentinel(pr) {
			continue
		}
		f := checkLandOnBase(pr, checks, runGit)
		if f.ID != "" {
			findings = append(findings, f)
		}
	}
	return findings
}

// checkLandOnBase checks one landed card: its head is an ancestor of origin/<base>.
func checkLandOnBase(pr *Card, checks []string, runGit func(cmd string, args ...string) (int, string, string)) LandOnBaseFinding {
	// check if FsckCheckName is in the checks filter (empty checks means all checks)
	if len(checks) > 0 && !hasCheck(checks, FsckCheckName) {
		return LandOnBaseFinding{}
	}
	cb := swarm.ReadCardBase([]byte(pr.F("brief")))
	f := LandOnBaseFinding{Check: FsckCheckName, ID: pr.ID, Stream: pr.Row,
		Head: pr.F("head"), Repo: cb.Named, Base: "", Fix: "nova-sprint reopen " + pr.ID + " --reason <text>"}
	if cb.Ref != "" {
		f.Base = cb.Ref
	}
	if f.Head == "" {
		f.Why = "no head recorded"
		return f
	}
	if f.Base == "" {
		f.Why = "its brief names no BASE: line; run: nova-sprint verify-landed --base <branch>"
		return f
	}
	if f.Repo == "" {
		f.Why = "its brief names no REPO: line; run: nova-sprint verify-landed --repo-dir <clone>"
		return f
	}
	// check if head is on base tip
	_, out, _ := runGit("git", "ls-remote", f.Repo, f.Base)
	out = strings.TrimSpace(out)
	if out == "" {
		f.Why = "origin/" + f.Base + " could not be read"
		return f
	}
	// ls-remote returns "<sha> <ref>"
	sha, _, ok := strings.Cut(out, "\t")
	if !ok {
		sha = strings.Fields(out)[0]
	}
	f.Tip = sha
	// check if head is ancestor of tip
	exit, _, _ := runGit("git", "merge-base", "--is-ancestor", f.Head, f.Tip)
	f.Ancestor = (exit == 0)
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
