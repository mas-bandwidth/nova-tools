package merge

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// The production LandForge: one gh invocation per question, through the same guard every
// other command in this package passes. It hangs off GH, the host the rest of the tool
// already reads the forge with, so a caller holds ONE forge rather than two that could
// disagree about which repository they are looking at.
//
// There is no `pr merge` in any spelling here and there never will be: admission to a merge
// queue is Enqueuer.Enqueue (2026-09-18), and internal/ci's class test refuses the spelling
// in every non-test file of the tools.

// PRForHead reads the open pull request whose head branch is head.
func (h *GH) PRForHead(head string) (LandPR, bool, error) {
	if err := ValidRefName(head); err != nil {
		return LandPR{}, false, fmt.Errorf("the head branch %q is not a name this tool hands to gh: %w", head, err)
	}
	out, err := h.gh("pr", "list", "--repo", h.Repo, "--state", "open", "--head", head,
		"--limit", "1", "--json", "number,url,headRefName")
	if err != nil {
		return LandPR{}, false, err
	}
	var raw []struct {
		Number      int    `json:"number"`
		URL         string `json:"url"`
		HeadRefName string `json:"headRefName"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return LandPR{}, false, fmt.Errorf("gh pr list did not answer JSON this tool can read: %w", err)
	}
	if len(raw) == 0 {
		return LandPR{}, false, nil
	}
	return LandPR{Number: raw[0].Number, URL: raw[0].URL, HeadRef: raw[0].HeadRefName}, true, nil
}

// OpenPR opens the batch's pull request onto base.
//
// The title and the body are the gate's own words -- the BATCH OK line, the members and the
// drops -- handed to gh as ARGUMENTS and never as a shell string, so nothing in them can be
// read as anything but a title and a body.
func (h *GH) OpenPR(head, base, title, body string) (LandPR, error) {
	if err := ValidRefName(head); err != nil {
		return LandPR{}, fmt.Errorf("the head branch %q is not a name this tool hands to gh: %w", head, err)
	}
	if err := ValidRefName(base); err != nil {
		return LandPR{}, fmt.Errorf("the base branch %q is not a name this tool hands to gh: %w", base, err)
	}
	if _, err := h.gh("pr", "create", "--repo", h.Repo, "--head", head, "--base", base,
		"--title", title, "--body", body); err != nil {
		return LandPR{}, err
	}
	// The created pull request is read BACK rather than parsed out of the create call's
	// own output: gh prints a URL there and the number this verb hands the one door has to
	// be the forge's answer to "what is open on this head", which is the same question the
	// next run of this verb asks.
	pr, ok, err := h.PRForHead(head)
	if err != nil {
		return LandPR{}, err
	}
	if !ok {
		return LandPR{}, fmt.Errorf("gh opened a pull request from %s and the forge then reported none open on that head", head)
	}
	return pr, nil
}

// UpdatePR rewrites an open pull request's body.
func (h *GH) UpdatePR(n int, body string) error {
	_, err := h.gh("pr", "edit", strconv.Itoa(n), "--repo", h.Repo, "--body", body)
	return err
}

// HeadRun reads the newest workflow run over one commit, its jobs and their failures.
func (h *GH) HeadRun(sha string) (LandRun, error) {
	if !IsSHA(sha) {
		return LandRun{}, fmt.Errorf("a run is read for a full 40-character sha, got %q", sha)
	}
	out, err := h.gh("run", "list", "--repo", h.Repo, "--commit", sha, "--limit", "1",
		"--json", "databaseId")
	if err != nil {
		return LandRun{}, err
	}
	var raw []struct {
		DatabaseID int64 `json:"databaseId"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return LandRun{}, fmt.Errorf("gh run list did not answer JSON this tool can read: %w", err)
	}
	if len(raw) == 0 {
		return LandRun{}, fmt.Errorf("the forge reports no run over %s", Short(sha))
	}
	return h.landRun(raw[0].DatabaseID)
}

// QueueRun reads the merge-group run of one pull request's queue entry.
//
// A merge group runs on a ref of its own -- gh-readonly-queue/<base>/pr-<n>-<sha> -- so the
// run is found by the BRANCH PREFIX rather than by a commit this side ever sees: the queue
// builds the group's commit, and the lander only ever knew the entry's own head.
func (h *GH) QueueRun(pr int, base string) (LandRun, error) {
	if err := ValidRefName(base); err != nil {
		return LandRun{}, fmt.Errorf("the base branch %q is not a name this tool hands to gh: %w", base, err)
	}
	out, err := h.gh("api", fmt.Sprintf("repos/%s/actions/runs?event=merge_group&per_page=50", h.Repo),
		"--jq", `.workflow_runs[] | [(.id|tostring), .head_branch] | @tsv`)
	if err != nil {
		return LandRun{}, err
	}
	want := fmt.Sprintf("gh-readonly-queue/%s/pr-%d-", base, pr)
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		id, branch, _ := strings.Cut(strings.TrimSpace(line), "\t")
		if !strings.HasPrefix(strings.TrimSpace(branch), want) {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSpace(id), 10, 64)
		if err != nil {
			continue
		}
		return h.landRun(n)
	}
	return LandRun{}, fmt.Errorf("the forge reports no merge-group run for #%d on %s", pr, base)
}

// landRun is the shared read: one run's jobs, their conclusions, and -- for the ones that
// went red -- the test names and failing lines out of their logs.
//
// ONE `gh run view --log-failed` carries every failed job's log, each line prefixed with
// the job's name and a tab. A log that cannot be read leaves the job named with no test,
// which is the honest answer: a failed job whose evidence this tool could not fetch.
func (h *GH) landRun(id int64) (LandRun, error) {
	run := LandRun{ID: id}
	out, err := h.gh("api", fmt.Sprintf("repos/%s/actions/runs/%d/jobs?per_page=100", h.Repo, id))
	if err != nil {
		return LandRun{}, err
	}
	var raw struct {
		Jobs []struct {
			Name       string `json:"name"`
			Conclusion string `json:"conclusion"`
			Status     string `json:"status"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return LandRun{}, fmt.Errorf("gh api run %d jobs did not answer JSON this tool can read: %w", id, err)
	}
	red := false
	for _, j := range raw.Jobs {
		conclusion := strings.TrimSpace(j.Conclusion)
		if conclusion == "" {
			conclusion = strings.TrimSpace(j.Status)
		}
		if Bucket(conclusion) == "red" {
			red = true
		}
		run.Jobs = append(run.Jobs, LandJob{Name: j.Name, Conclusion: conclusion})
	}
	if !red {
		return run, nil
	}
	logs, err := h.gh("run", "view", strconv.Itoa(int(id)), "--repo", h.Repo, "--log-failed")
	if err != nil {
		return run, nil
	}
	byJob := logsByJob(logs)
	for i, j := range run.Jobs {
		if Bucket(j.Conclusion) != "red" {
			continue
		}
		log, ok := byJob[j.Name]
		if !ok {
			continue
		}
		run.Jobs[i].Tests, run.Jobs[i].Package, run.Jobs[i].Lines = ParseRunFailure(log)
	}
	return run, nil
}

// RerunFailed reruns one run's failed jobs, and only those.
func (h *GH) RerunFailed(id int64) error {
	_, err := h.gh("run", "rerun", strconv.FormatInt(id, 10), "--repo", h.Repo, "--failed")
	return err
}

// QueueState is the state of this pull request's merge-queue entry, or "" when the queue
// holds none. An entry that was there a poll ago and is not there now was DEQUEUED, which
// is the one thing this read exists to tell a caller.
func (h *GH) QueueState(pr int) (string, error) {
	out, err := h.gh("api", "graphql", "--raw-field", "query="+queueStateQuery,
		"--field", "owner="+ownerOf(h.Repo), "--field", "name="+nameOf(h.Repo))
	if err != nil {
		return "", err
	}
	var raw struct {
		Data struct {
			Repository struct {
				MergeQueue struct {
					Entries struct {
						Nodes []struct {
							State       string `json:"state"`
							PullRequest struct {
								Number int `json:"number"`
							} `json:"pullRequest"`
						} `json:"nodes"`
					} `json:"entries"`
				} `json:"mergeQueue"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return "", fmt.Errorf("the forge's merge queue did not answer JSON this tool can read: %w", err)
	}
	for _, n := range raw.Data.Repository.MergeQueue.Entries.Nodes {
		if n.PullRequest.Number == pr {
			return strings.TrimSpace(n.State), nil
		}
	}
	return "", nil
}

// queueStateQuery reads the merge queue's entries and the pull request each one carries. It
// is a READ and the whole of this file's contact with a merge queue: there is no mutation
// here, because the one that exists lives in enqueue.go.
const queueStateQuery = `query($owner:String!,$name:String!){repository(owner:$owner,name:$name){mergeQueue{entries(first:50){nodes{state pullRequest{number}}}}}}`

// CloseMember comments on a member and then closes it, in that order: the pointer to the
// batch that carried it is on the pull request BEFORE it stops being open, so a reader who
// finds it closed finds the reason on it.
func (h *GH) CloseMember(pr int, comment string) error {
	if _, err := h.gh("pr", "comment", strconv.Itoa(pr), "--repo", h.Repo, "--body", comment); err != nil {
		return err
	}
	_, err := h.gh("pr", "close", strconv.Itoa(pr), "--repo", h.Repo)
	return err
}

// ownerOf and nameOf split an <owner>/<name> slug for the GraphQL read above. A slug this
// tool was given that holds no slash answers the whole string as the owner and an empty
// name, which the forge then refuses by name rather than this tool guessing at one.
func ownerOf(repo string) string {
	owner, _, _ := strings.Cut(repo, "/")
	return owner
}

func nameOf(repo string) string {
	_, name, _ := strings.Cut(repo, "/")
	return name
}
