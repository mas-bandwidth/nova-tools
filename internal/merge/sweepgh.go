package merge

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// GHSweep is the production SweepHost: one gh invocation per question, under the run's
// --timeout. It reaches the merge queue through gh's GraphQL edge, which is the only
// edge that answers for a merge queue at all, and it reaches the runs through `gh run`.
//
// ENQUEUE GOES THROUGH THE ONE DOOR (Enqueuer.Enqueue, enqueue.go) and DEQUEUE THROUGH THE
// QUEUE MUTATION, never `gh pr merge`. That is rule 21's shape as much as it is GitHub's:
// `gh pr merge` takes a head precondition and nothing about the base, and this host's own
// guard refuses `--auto`, so a merge-queue admission is a queue mutation and never a merge
// call. The card that added this verb named `gh pr merge` as the loop's spelling; this is
// its equivalent, and the interface the tests drive is the same either way.
type GHSweep struct {
	Repo    string
	Branch  string
	Timeout time.Duration
	Runner  Runner
}

// NewGHSweep returns a sweep host against one repository's merge queue.
func NewGHSweep(repo, branch string, timeout time.Duration, runner Runner) *GHSweep {
	if runner == nil {
		runner = Exec{}
	}
	return &GHSweep{Repo: repo, Branch: branch, Timeout: timeout, Runner: runner}
}

// gh runs one gh command through the same mutation guard every other command passes,
// before the command is built.
func (h *GHSweep) gh(args ...string) (string, error) {
	if err := guard(args, ""); err != nil {
		return "", err
	}
	g := NewGit("", h.Timeout, h.Runner)
	ctx, cancel := contextWithTimeout(h.Timeout)
	defer cancel()
	out, err := g.Runner.Run(ctx, "", "gh", args...)
	if err != nil {
		return out, fmt.Errorf("gh %s: %w: %s", strings.Join(args, " "), err, oneLineOf(out))
	}
	return out, nil
}

// mergeQueueQuery reads the queue's entries and each entry's state in one call.
const mergeQueueQuery = `query($owner:String!,$name:String!,$branch:String!){repository(owner:$owner,name:$name){mergeQueue(branch:$branch){entries(first:100){nodes{state pullRequest{number}}}}}}`

// Queue reads the merge queue of h.Branch.
func (h *GHSweep) Queue() ([]QueueEntry, error) {
	owner, name, _ := strings.Cut(h.Repo, "/")
	out, err := h.gh("api", "graphql",
		"--raw-field", "query="+mergeQueueQuery,
		"--field", "owner="+owner, "--field", "name="+name, "--field", "branch="+h.Branch,
		"--jq", `.data.repository.mergeQueue.entries.nodes[] | [.state, (.pullRequest.number|tostring)] | @tsv`)
	if err != nil {
		return nil, err
	}
	var entries []QueueEntry
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		state, number, _ := strings.Cut(line, "\t")
		pr, err := strconv.Atoi(strings.TrimSpace(number))
		if err != nil {
			return nil, fmt.Errorf("the merge queue answered an entry this tool cannot read: %q", line)
		}
		entries = append(entries, QueueEntry{PR: pr, State: strings.TrimSpace(state)})
	}
	return entries, nil
}

// OpenPRs reads the repository's open pull requests, the fields the pass filters on.
func (h *GHSweep) OpenPRs() ([]SweepPR, error) {
	out, err := h.gh("pr", "list", "-R", h.Repo, "--state", "open", "--limit", "400",
		"--json", "number,headRefName,mergeStateStatus")
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Number           int    `json:"number"`
		HeadRefName      string `json:"headRefName"`
		MergeStateStatus string `json:"mergeStateStatus"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, fmt.Errorf("gh pr list did not answer JSON this tool can read: %w", err)
	}
	prs := make([]SweepPR, 0, len(raw))
	for _, r := range raw {
		// Lesson 48: the head ref becomes an argument to `gh run list --branch`, so
		// it is checked where it arrives, before this decode hands it on.
		if err := ValidRefName(r.HeadRefName); err != nil {
			return nil, fmt.Errorf("pull request %d's head branch is not a name this tool hands to gh: %w", r.Number, err)
		}
		prs = append(prs, SweepPR{Number: r.Number, HeadRef: r.HeadRefName, MergeState: r.MergeStateStatus})
	}
	return prs, nil
}

// HeadRun reads the newest run of the branch's CI workflow.
func (h *GHSweep) HeadRun(branch string) (SweepRun, error) {
	out, err := h.gh("run", "list", "-R", h.Repo, "--branch", branch, "--workflow", "ci.yml",
		"--limit", "1", "--json", "databaseId,status,conclusion")
	if err != nil {
		return SweepRun{}, err
	}
	var raw []struct {
		DatabaseID int64  `json:"databaseId"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return SweepRun{}, fmt.Errorf("gh run list did not answer JSON this tool can read: %w", err)
	}
	if len(raw) == 0 {
		return SweepRun{}, nil
	}
	return SweepRun{ID: raw[0].DatabaseID, Status: raw[0].Status, Conclusion: raw[0].Conclusion}, nil
}

// Enqueue admits a pull request to the merge queue THROUGH THE ONE DOOR. The mutation is
// not repeated here: this method is the sweep's seam onto Enqueuer.Enqueue, which is where
// the batch rule and the GraphQL both live. A sweep that enqueued by itself would be a
// second entrance to the queue, which is the thing this session took away.
func (h *GHSweep) Enqueue(pr SweepPR) error {
	return NewEnqueuer(NewGHEnqueue(h.Repo, h.Timeout, h.Runner)).Enqueue(
		context.Background(), EnqueuePR{Number: pr.Number, HeadRef: pr.HeadRef}, false)
}

// Dequeue removes a pull request from the merge queue.
func (h *GHSweep) Dequeue(pr int) error {
	id, err := h.nodeID(pr)
	if err != nil {
		return err
	}
	_, err = h.gh("api", "graphql",
		"--raw-field", "query=mutation($id:ID!){dequeuePullRequest(input:{id:$id}){clientMutationId}}",
		"--field", "id="+id)
	return err
}

// Rerun reruns one completed run.
func (h *GHSweep) Rerun(run int64) error {
	_, err := h.gh("run", "rerun", strconv.FormatInt(run, 10), "-R", h.Repo)
	return err
}

// nodeID resolves the pull request's GraphQL node id, which is what the queue mutations
// take in place of a number.
func (h *GHSweep) nodeID(pr int) (string, error) {
	out, err := h.gh("pr", "view", strconv.Itoa(pr), "-R", h.Repo, "--json", "id", "--jq", ".id")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}
