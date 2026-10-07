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

// promoteForge is the forge the verb talks to, so a test can stand in a fake.
type promoteForge interface {
	// OpenPR opens the pull request and returns its number.
	OpenPR(ctx context.Context, base, head, title, body string) (string, error)
	// View reads the pull request's node id and, when merged, its merge commit.
	View(ctx context.Context, number string) (prView, error)
	// Checks reads the pull request's checks.
	Checks(ctx context.Context, number string) (prChecks, error)
	// Enqueue admits the pull request to the merge queue.
	Enqueue(ctx context.Context, id string) (string, error)
	// Confirm returns the pull request's merge queue entry, "" when it has none.
	Confirm(ctx context.Context, id string) (string, error)
	// FailedRuns returns the failed runs under a gh run list filter.
	FailedRuns(ctx context.Context, filter ...string) ([]ghRunRow, error)
	// RunLog reads a failed run's failed steps' log.
	RunLog(ctx context.Context, id int) (string, error)
	// Repo is the owner/name of the clone's repository.
	Repo(ctx context.Context) (string, error)
}

// prView is what the pass reads of a pull request.
type prView struct {
	ID     string
	Merged string // the merge commit, "" until it merges
}

const (
	checksPass    = "pass"
	checksPending = "pending"
	checksFail    = "fail"
)

// prChecks is the state of a pull request's checks; Name is the failing (or a waiting) check.
type prChecks struct {
	State, Name string
}

// promoteForges is the forge a test stands in for promote, keyed by the repo dir.
var promoteForges sync.Map

// forged returns the promoter's forge: its own, else promoteForges, else ghForge.
func (p *promoter) forged() promoteForge {
	if p.forge != nil {
		return p.forge
	}
	if p.dir != "" {
		if f, ok := promoteForges.Load(p.dir); ok {
			return f.(promoteForge)
		}
	}
	return ghForge{p}
}

// ghForge is the gh CLI behind promoteForge.
type ghForge struct{ p *promoter }

func (g ghForge) OpenPR(ctx context.Context, base, head, title, body string) (string, error) {
	url, err := g.p.gh(ctx, "pr", "create", "--base", base, "--head", head, "--title", title, "--body", body)
	if err != nil {
		return "", err
	}
	return prNumber(url)
}

func (g ghForge) View(ctx context.Context, number string) (prView, error) {
	raw, err := g.p.gh(ctx, "pr", "view", number, "--json", "id,state,mergeCommit")
	if err != nil {
		return prView{}, err
	}
	var v prJSON
	if jerr := json.Unmarshal([]byte(raw), &v); jerr != nil {
		return prView{}, fmt.Errorf("pull request %s view: %s", number, oneLine(raw))
	}
	return prView{ID: v.ID, Merged: mergedSHA(v)}, nil
}

func (g ghForge) Checks(ctx context.Context, number string) (prChecks, error) {
	raw, err := g.p.gh(ctx, "pr", "checks", number, "--json", "name,bucket")
	var list []struct {
		Name   string `json:"name"`
		Bucket string `json:"bucket"`
	}
	if jerr := json.Unmarshal([]byte(raw), &list); jerr != nil {
		if err != nil {
			if strings.Contains(err.Error(), "no checks reported") || strings.Contains(raw, "no checks reported") {
				return prChecks{State: checksPending, Name: "no checks reported yet"}, nil
			}
			return prChecks{}, err
		}
		return prChecks{}, fmt.Errorf("pull request %s checks: %s", number, oneLine(raw))
	}
	res := prChecks{State: checksPass}
	if len(list) == 0 {
		return prChecks{State: checksPending, Name: "no checks reported yet"}, nil
	}
	for _, c := range list {
		switch c.Bucket {
		case "fail", "cancel":
			return prChecks{State: checksFail, Name: c.Name}, nil
		case "pending":
			if res.State == checksPass {
				res = prChecks{State: checksPending, Name: c.Name}
			}
		}
	}
	return res, nil
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

func (g ghForge) Confirm(ctx context.Context, id string) (string, error) {
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

func (g ghForge) FailedRuns(ctx context.Context, filter ...string) ([]ghRunRow, error) {
	args := append(append([]string{"run", "list"}, filter...), "--json", "databaseId,conclusion,status,name", "--limit", "20")
	raw, err := g.p.gh(ctx, args...)
	if err != nil {
		return nil, err
	}
	var runs []ghRunRow
	if jerr := json.Unmarshal([]byte(raw), &runs); jerr != nil {
		return nil, fmt.Errorf("runs: %s", oneLine(raw))
	}
	var failed []ghRunRow
	for _, r := range runs {
		if r.Conclusion == "failure" {
			failed = append(failed, r)
		}
	}
	return failed, nil
}

func (g ghForge) RunLog(ctx context.Context, id int) (string, error) {
	return g.p.gh(ctx, "run", "view", strconv.Itoa(id), "--log-failed")
}

func (g ghForge) Repo(ctx context.Context) (string, error) {
	repo, err := g.p.gh(ctx, "repo", "view", "--json", "nameWithOwner", "--jq", ".nameWithOwner")
	return strings.TrimSpace(repo), err
}
