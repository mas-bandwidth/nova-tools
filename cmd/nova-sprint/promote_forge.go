package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// promoteForge is every call promote makes to the forge: open the pull request,
// read it, read its checks, admit it to the merge queue, and read the queue's
// merge-group run. Nothing in promote.go spells gh; the real one is ghForge and
// a test hands the step a fake (docs/SPEC-SPRINT.md section 11, promote).
type promoteForge interface {
	// Open opens the pull request and returns its number.
	Open(ctx context.Context, base, head, title, body string) (string, error)
	// View reads the pull request: its node id, and the merge commit once merged.
	View(ctx context.Context, number string) (prJSON, error)
	// Checks reads the pull request's own checks.
	Checks(ctx context.Context, number string) (promoteChecks, error)
	// Enqueue admits the pull request (by node id) to the merge queue and
	// returns the entry id. It carries no merge strategy.
	Enqueue(ctx context.Context, id string) (string, error)
	// Entry is the queue entry of the pull request, empty when it has none.
	Entry(ctx context.Context, id string) (string, error)
	// GroupFailure is the failing merge-group run of the branch, if any.
	GroupFailure(ctx context.Context, branch string) (promoteFailure, error)
}

// promoteChecks is the state of a pull request's checks: promoteChecksPass,
// promoteChecksPending or promoteChecksFail. Failing names the failed checks.
type promoteChecks struct {
	State   string
	Failing []string
	Pending []string
}

const (
	promoteChecksPass    = "pass"
	promoteChecksPending = "pending"
	promoteChecksFail    = "fail"
)

// promoteFailure is a failed merge-group run: the check's name and a log tail.
type promoteFailure struct {
	Name string
	Log  string
}

// ghForge is the forge over the gh CLI. run is promoter.gh, which runs gh
// through internal/subproc and refuses a strategy flag.
type ghForge struct {
	run func(ctx context.Context, args ...string) (string, error)
}

func (g ghForge) Open(ctx context.Context, base, head, title, body string) (string, error) {
	url, err := g.run(ctx, "pr", "create", "--base", base, "--head", head, "--title", title, "--body", body)
	if err != nil {
		return "", err
	}
	return prNumber(url)
}

func (g ghForge) View(ctx context.Context, number string) (prJSON, error) {
	raw, err := g.run(ctx, "pr", "view", number, "--json", "id,state,mergeCommit")
	if err != nil {
		return prJSON{}, err
	}
	var v prJSON
	if jerr := json.Unmarshal([]byte(raw), &v); jerr != nil {
		return prJSON{}, fmt.Errorf("pull request %s view: %s", number, oneLine(raw))
	}
	if v.ID == "" && !strings.EqualFold(v.State, "MERGED") {
		return prJSON{}, errors.New("the pull request " + number + " has no id (" + oneLine(raw) + ")")
	}
	return v, nil
}

func (g ghForge) Checks(ctx context.Context, number string) (promoteChecks, error) {
	// gh pr checks exits 8 while a check is pending and 1 on a failure, and
	// prints the json either way: the output is read before the error is.
	raw, err := g.run(ctx, "pr", "checks", number, "--json", "name,bucket")
	var rows []struct {
		Name   string `json:"name"`
		Bucket string `json:"bucket"`
	}
	if jerr := json.Unmarshal([]byte(raw), &rows); jerr != nil {
		if err != nil {
			return promoteChecks{}, err
		}
		return promoteChecks{}, fmt.Errorf("pull request %s checks: %s", number, oneLine(raw))
	}
	c := promoteChecks{State: promoteChecksPass}
	for _, r := range rows {
		switch r.Bucket {
		case "fail", "cancel":
			c.Failing = append(c.Failing, r.Name)
		case "pending":
			c.Pending = append(c.Pending, r.Name)
		}
	}
	switch {
	case len(c.Failing) > 0:
		c.State = promoteChecksFail
	case len(c.Pending) > 0 || len(rows) == 0:
		// no check reported yet is pending: the forge registers them a moment
		// after the pull request opens
		c.State = promoteChecksPending
	}
	return c, nil
}

func (g ghForge) Enqueue(ctx context.Context, id string) (string, error) {
	raw, err := g.run(ctx, "api", "graphql", "-f", "query="+promoteEnqueueQuery, "-f", "id="+id)
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

func (g ghForge) Entry(ctx context.Context, id string) (string, error) {
	raw, err := g.run(ctx, "api", "graphql", "-f", "query="+promoteQueueQuery, "-f", "id="+id)
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

func (g ghForge) GroupFailure(ctx context.Context, branch string) (promoteFailure, error) {
	raw, err := g.run(ctx, "run", "list", "--branch", branch, "--event", "merge_group", "--json", "databaseId,conclusion,status,name", "--limit", "5")
	if err != nil {
		return promoteFailure{}, err
	}
	var runs []struct {
		DatabaseID int    `json:"databaseId"`
		Conclusion string `json:"conclusion"`
		Name       string `json:"name"`
	}
	if jerr := json.Unmarshal([]byte(raw), &runs); jerr != nil {
		return promoteFailure{}, fmt.Errorf("merge-group runs: %s", oneLine(raw))
	}
	for _, r := range runs {
		if r.Conclusion != "failure" {
			continue
		}
		// a log that cannot be read still leaves the failure named
		logText, _ := g.run(ctx, "run", "view", strconv.Itoa(r.DatabaseID), "--log-failed")
		return promoteFailure{Name: r.Name, Log: logText}, nil
	}
	return promoteFailure{}, nil
}
