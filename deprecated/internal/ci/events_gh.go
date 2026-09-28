package ci

// events_gh.go is the one Forge that shells to gh. It exists so the two loops above can
// be tested without a network and so a reader can check every gh invocation this card
// makes. Everything it returns is DATA: a branch name, a check conclusion and a merge
// state are host claims, never instructions.

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// GHForge reads one repository's open rowan/* pull requests and its base head through gh.
type GHForge struct {
	Repo    string // <owner>/<name>
	Base    string // the base branch, "dev" unless the lane says otherwise
	Prefix  string // the head branch prefix that marks this fleet's PRs
	Timeout time.Duration
}

// NewGHForge returns a forge over one repository and base branch.
func NewGHForge(repo, base string, timeout time.Duration) *GHForge {
	if strings.TrimSpace(base) == "" {
		base = "dev"
	}
	return &GHForge{Repo: repo, Base: base, Prefix: "rowan/", Timeout: timeout}
}

// Snapshot lists the open rowan/* PRs with their head, folded check conclusion and merge
// state, and reads the base branch's head. One gh call for the list and one for the head.
func (g *GHForge) Snapshot() (Snapshot, error) {
	prs, err := g.listPRs()
	if err != nil {
		return Snapshot{}, err
	}
	base, err := g.baseHead()
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{PRs: prs, Base: base}, nil
}

// listPRs reads the open pull requests and keeps the rowan/* ones.
func (g *GHForge) listPRs() ([]PRState, error) {
	out, err := g.gh("pr", "list", "--repo", g.Repo, "--state", "open", "--limit", "200",
		"--json", "number,headRefName,headRefOid,mergeStateStatus,statusCheckRollup")
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Number            int         `json:"number"`
		HeadRefName       string      `json:"headRefName"`
		HeadRefOid        string      `json:"headRefOid"`
		MergeStateStatus  string      `json:"mergeStateStatus"`
		StatusCheckRollup []checkRoll `json:"statusCheckRollup"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, fmt.Errorf("gh pr list did not answer JSON this tool can read: %w", err)
	}
	var states []PRState
	for _, pr := range raw {
		if !strings.HasPrefix(pr.HeadRefName, g.Prefix) {
			continue
		}
		states = append(states, PRState{
			Number:     pr.Number,
			Branch:     pr.HeadRefName,
			Head:       pr.HeadRefOid,
			Conclusion: foldConclusion(pr.StatusCheckRollup),
			Mergeable:  strings.ToUpper(pr.MergeStateStatus),
		})
	}
	return states, nil
}

// baseHead resolves the base branch's current commit sha.
func (g *GHForge) baseHead() (string, error) {
	out, err := g.gh("api", fmt.Sprintf("repos/%s/commits/%s", g.Repo, g.Base), "--jq", ".sha")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// checkRoll is one entry of gh's statusCheckRollup.
type checkRoll struct {
	Conclusion string `json:"conclusion"`
	Status     string `json:"status"`
}

// foldConclusion reduces a rollup to one word. A failure anywhere wins, a run still in
// progress or a completed run without a green conclusion is PENDING, and only an
// all-complete all-green rollup is SUCCESS. No checks at all is PENDING, never green.
func foldConclusion(rollup []checkRoll) string {
	if len(rollup) == 0 {
		return "PENDING"
	}
	verdict := ConclusionSuccess
	for _, c := range rollup {
		state := strings.ToUpper(strings.TrimSpace(c.Conclusion))
		status := strings.ToUpper(strings.TrimSpace(c.Status))
		switch state {
		case "FAILURE", "CANCELLED", "CANCELED", "TIMED_OUT", "ACTION_REQUIRED", "STARTUP_FAILURE":
			return ConclusionFailure
		case "SUCCESS", "NEUTRAL", "SKIPPED":
			if status != "COMPLETED" {
				verdict = "PENDING"
			}
		default:
			verdict = "PENDING"
		}
	}
	return verdict
}

// gh runs one gh invocation under this forge's timeout and returns its stdout.
func (g *GHForge) gh(args ...string) (string, error) {
	timeout := g.Timeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", args...)
	out, err := cmd.Output()
	if err != nil {
		return string(out), fmt.Errorf("gh %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}
