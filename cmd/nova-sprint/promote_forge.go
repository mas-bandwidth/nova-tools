package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// promoteForge is every forge call a promotion makes (promote.go), so a test
// stands a fake in for the forge. The one implementation that talks to a forge
// is ghForge, the gh CLI through pkg/subproc.
type promoteForge interface {
	// OpenPR opens the pull request of head into base and answers its number.
	OpenPR(ctx context.Context, base, head, title, body string) (string, error)
	// PR is the pull request's id, state and merge commit.
	PR(ctx context.Context, number string) (prJSON, error)
	// Checks is the pull request's checks, each with its bucket: pass, fail,
	// pending, skipping or cancel. None is a pull request whose checks have not
	// been reported yet.
	Checks(ctx context.Context, number string) ([]promoteCheck, error)
	// QueueEntry is the pull request's merge-queue entry, "" when it has none.
	QueueEntry(ctx context.Context, id string) (string, error)
	// Enqueue admits the pull request to the merge queue, with no strategy.
	Enqueue(ctx context.Context, id string) (string, error)
	// QueueRuns is the merge-group runs of the pull request into base.
	QueueRuns(ctx context.Context, base, number string) ([]promoteRun, error)
	// RunLog is a run's failed steps' log.
	RunLog(ctx context.Context, id int) (string, error)
	// FailedRuns is the failed runs of a branch (the pull request's checks), or
	// of the base at one commit when commit is set (promote_red.go, devRed).
	FailedRuns(ctx context.Context, branch, commit string) ([]promoteRun, error)
	// Repo is the owner/name of the clone's repository, for the fix cards.
	Repo(ctx context.Context) (string, error)
}

// promoteCheck is one check of a pull request.
type promoteCheck struct {
	Name   string `json:"name"`
	Bucket string `json:"bucket"`
}

// promoteRun is one workflow run: a merge-group run, or a failed run of a
// branch.
type promoteRun struct {
	ID         int    `json:"databaseId"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	HeadBranch string `json:"headBranch"`
}

// promoteForges is the forge a test stands in for the promote verb, keyed by
// the --repo-dir it runs in (each test's own temporary clone). A clone with
// none talks to gh.
var promoteForges sync.Map

// forger is the promoter's forge: its own, else gh.
func (p *promoter) forger() promoteForge {
	if p.forge != nil {
		return p.forge
	}
	return ghForge{p: p}
}

// The queries are one literal each, so they are not an argument list of pr and
// the word the class test refuses after it, and neither is a strategy flag.
const (
	promoteEnqueueQuery = `mutation($id:ID!){enqueuePullRequest(input:{pullRequestId:$id}){mergeQueueEntry{id}}}`
	promoteQueueQuery   = `query($id:ID!){node(id:$id){... on PullRequest{mergeQueueEntry{id state}}}}`
)

// ghForge is the forge through gh (promoter.gh: pkg/subproc, or the
// promoter's ghRun in a test).
type ghForge struct{ p *promoter }

func (g ghForge) OpenPR(ctx context.Context, base, head, title, body string) (string, error) {
	url, err := g.p.gh(ctx, "pr", "create", "--base", base, "--head", head, "--title", title, "--body", body)
	if err != nil {
		return "", err
	}
	return prNumber(url)
}

func (g ghForge) PR(ctx context.Context, number string) (prJSON, error) {
	raw, err := g.p.gh(ctx, "pr", "view", number, "--json", "id,state,mergeCommit")
	if err != nil {
		return prJSON{}, err
	}
	var v prJSON
	if jerr := json.Unmarshal([]byte(raw), &v); jerr != nil {
		return prJSON{}, fmt.Errorf("pull request %s view: %s", number, oneLine(raw))
	}
	return v, nil
}

func (g ghForge) Checks(ctx context.Context, number string) ([]promoteCheck, error) {
	// gh pr checks exits 8 while a check is pending and 1 when one failed, and
	// prints the list either way; no checks yet is its own refusal
	raw, err := g.p.gh(ctx, "pr", "checks", number, "--json", "name,bucket")
	if err != nil && strings.Contains(err.Error(), "no checks reported") {
		return nil, nil
	}
	if strings.TrimSpace(raw) == "" {
		if err != nil {
			return nil, err
		}
		return nil, nil
	}
	var checks []promoteCheck
	if jerr := json.Unmarshal([]byte(raw), &checks); jerr != nil {
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("pull request %s checks: %s", number, oneLine(raw))
	}
	return checks, nil
}

func (g ghForge) QueueEntry(ctx context.Context, id string) (string, error) {
	raw, err := g.p.gh(ctx, "api", "graphql", "-f", "query="+promoteQueueQuery, "-f", "id="+id)
	if err != nil {
		return "", err
	}
	var resp struct {
		Data struct {
			Node struct {
				MergeQueueEntry *struct {
					ID string `json:"id"`
				} `json:"mergeQueueEntry"`
			} `json:"node"`
		} `json:"data"`
	}
	if jerr := json.Unmarshal([]byte(raw), &resp); jerr != nil {
		return "", fmt.Errorf("queue query: %s", oneLine(raw))
	}
	if resp.Data.Node.MergeQueueEntry == nil {
		return "", nil
	}
	return resp.Data.Node.MergeQueueEntry.ID, nil
}

func (g ghForge) Enqueue(ctx context.Context, id string) (string, error) {
	raw, err := g.p.gh(ctx, "api", "graphql", "-f", "query="+promoteEnqueueQuery, "-f", "id="+id)
	if err != nil {
		return "", err
	}
	var resp struct {
		Data struct {
			EnqueuePullRequest struct {
				MergeQueueEntry *struct {
					ID string `json:"id"`
				} `json:"mergeQueueEntry"`
			} `json:"enqueuePullRequest"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if jerr := json.Unmarshal([]byte(raw), &resp); jerr != nil {
		return "", fmt.Errorf("enqueue: %s", oneLine(raw))
	}
	if len(resp.Errors) > 0 {
		return "", errors.New("enqueue: " + resp.Errors[0].Message)
	}
	if resp.Data.EnqueuePullRequest.MergeQueueEntry == nil || resp.Data.EnqueuePullRequest.MergeQueueEntry.ID == "" {
		return "", errors.New("enqueue returned no merge queue entry")
	}
	return resp.Data.EnqueuePullRequest.MergeQueueEntry.ID, nil
}

// QueueRuns reads the merge-group runs and keeps the pull request's: the
// queue's branch is gh-readonly-queue/<base>/pr-<n>-<sha>, never the head.
func (g ghForge) QueueRuns(ctx context.Context, base, number string) ([]promoteRun, error) {
	raw, err := g.p.gh(ctx, "run", "list", "--event", "merge_group", "--json", "databaseId,conclusion,status,name,headBranch", "--limit", "50")
	if err != nil {
		return nil, err
	}
	var runs []promoteRun
	if jerr := json.Unmarshal([]byte(raw), &runs); jerr != nil {
		return nil, fmt.Errorf("merge-group runs: %s", oneLine(raw))
	}
	prefix := "gh-readonly-queue/" + base + "/pr-" + number + "-"
	var mine []promoteRun
	for _, r := range runs {
		if strings.HasPrefix(r.HeadBranch, prefix) {
			mine = append(mine, r)
		}
	}
	return mine, nil
}

func (g ghForge) RunLog(ctx context.Context, id int) (string, error) {
	return g.p.gh(ctx, "run", "view", strconv.Itoa(id), "--log-failed")
}

// FailedRuns reads the branch's runs, at the commit when one is named, and
// keeps the failed ones.
func (g ghForge) FailedRuns(ctx context.Context, branch, commit string) ([]promoteRun, error) {
	args := []string{"run", "list", "--branch", branch}
	if commit != "" {
		args = append(args, "--commit", commit)
	}
	raw, err := g.p.gh(ctx, append(args, "--json", "databaseId,conclusion,status,name", "--limit", "20")...)
	if err != nil {
		return nil, err
	}
	var runs []promoteRun
	if jerr := json.Unmarshal([]byte(raw), &runs); jerr != nil {
		return nil, fmt.Errorf("runs: %s", oneLine(raw))
	}
	var failed []promoteRun
	for _, r := range runs {
		if r.Conclusion == "failure" {
			failed = append(failed, r)
		}
	}
	return failed, nil
}

func (g ghForge) Repo(ctx context.Context) (string, error) {
	repo, err := g.p.gh(ctx, "repo", "view", "--json", "nameWithOwner", "--jq", ".nameWithOwner")
	return strings.TrimSpace(repo), err
}
