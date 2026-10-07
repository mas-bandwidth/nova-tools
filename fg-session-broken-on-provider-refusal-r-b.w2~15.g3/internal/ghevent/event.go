// Package ghevent appends entries to the Redis stream ev:github and reads them back.
//
// Every entry has the fields repo, kind, number, head, action, at, sender,
// comment_id. Four kinds add their own fields, always all of them, empty included:
// pull_request adds branch, base and merged (closed only: true or false);
// issues adds labels (a JSON array of the issue's label names after the
// change), body, state and state_reason; workflow_run adds run_id, workflow,
// status and conclusion; check_run adds check (check_run.name), check_run_id
// (check_run.id), status and conclusion; runner rows are keyed by check name
// and ordered by attempt id and status.
package ghevent

import (
	"context"
	"encoding/json"
	"fmt"

	gheventwire "github.com/mas-bandwidth/nova-tools/internal/ghevent/wire"
	"github.com/redis/go-redis/v9"
)

// Stream is the Redis stream every carried delivery is appended to.
const Stream = gheventwire.Stream

// Entry is one ev:github record. Number and CommentID are decimal strings,
// empty when the payload has none, so a missing pull request is not number 0.
type Entry struct {
	Repo      string
	Kind      string
	Number    string
	Head      string
	Action    string
	At        string
	Sender    string
	CommentID string

	// issues only.
	Labels      []string
	Body        string
	State       string
	StateReason string

	// workflow_run only.
	RunID    string
	Workflow string

	// pull_request only (card pr-record-follows-github): the head branch,
	// the base branch and, on closed, "true" when the PR merged ("false"
	// otherwise, "" on the other actions).
	Branch string
	Base   string
	Merged string

	// check_run only: the check's name and its id (a decimal string).
	Check      string
	CheckRunID string

	// workflow_run and check_run.
	Status     string
	Conclusion string
}

// Publish appends one entry. Every named field of the entry's kind is written,
// empty string included, so a reader can rely on the key set. A ping may have
// no repo (a hook with neither repository nor organization); every other kind
// needs one.
func Publish(ctx context.Context, rdb *redis.Client, e Entry) (string, error) {
	if rdb == nil {
		return "", fmt.Errorf("ev:github: nil redis client")
	}
	if e.Kind == "" || e.Action == "" || (e.Repo == "" && e.Kind != "ping") {
		return "", fmt.Errorf("ev:github: entry needs repo, kind and action")
	}
	values, err := Fields(e)
	if err != nil {
		return "", err
	}
	return rdb.XAdd(ctx, &redis.XAddArgs{Stream: Stream, Values: values}).Result()
}

// Fields is the stream entry's key set for e: the eight common fields, plus
// the issues, workflow_run, check_run or pull_request fields for those kinds.
func Fields(e Entry) (map[string]interface{}, error) {
	v := map[string]interface{}{
		"repo":       e.Repo,
		"kind":       e.Kind,
		"number":     e.Number,
		"head":       e.Head,
		"action":     e.Action,
		"at":         e.At,
		"sender":     e.Sender,
		"comment_id": e.CommentID,
	}
	switch e.Kind {
	case "issues":
		labels := e.Labels
		if labels == nil {
			labels = []string{}
		}
		b, err := json.Marshal(labels)
		if err != nil {
			return nil, fmt.Errorf("ev:github: labels: %w", err)
		}
		v["labels"] = string(b)
		v["body"] = e.Body
		v["state"] = e.State
		v["state_reason"] = e.StateReason
	case "workflow_run":
		v["run_id"] = e.RunID
		v["workflow"] = e.Workflow
		v["status"] = e.Status
		v["conclusion"] = e.Conclusion
	case "pull_request":
		v["branch"] = e.Branch
		v["base"] = e.Base
		v["merged"] = e.Merged
	case "check_run":
		v["check"] = e.Check
		v["check_run_id"] = e.CheckRunID
		v["status"] = e.Status
		v["conclusion"] = e.Conclusion
	}
	return v, nil
}
