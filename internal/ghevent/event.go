// Package ghevent turns one GitHub webhook delivery into one entry on the
// Redis stream ev:github (nova-tools #2657, #2685).
//
// The carried events are ping, pull_request, issue_comment,
// pull_request_review, check_suite, check_run, merge_group, issues (actions
// opened, labeled, unlabeled, edited, closed, reopened) and workflow_run.
// Anything else, and an issues action outside that set, is ErrNotCarried and
// is not written. One delivery is one entry: a check that names several pull
// requests does not fan out. The number kept is the first pull request the
// payload lists.
//
// A ping is carried (#3177): creating a hook, or POST .../hooks/<id>/pings, is
// how the hook proves it reaches the stream, so a ping is one entry with
// kind=ping, action=ping, repo the repository's full name or, on an org hook,
// the organization's login, and at the hook's updated_at.
//
// Every entry has the fields repo, kind, number, head, action, at, sender,
// comment_id. kind is the X-GitHub-Event header, which is not in the JSON
// body. Three kinds add their own fields, always all of them, empty included:
// issues adds labels (a JSON array of the issue's label names after the
// change), body, state and state_reason; workflow_run adds run_id, workflow,
// status and conclusion; check_run adds check (check_run.name), check_run_id
// (check_run.id), status and conclusion (nova-tools #3040: pr-to-read keys
// runner rows by the check name and orders attempts by id and status). An issue_comment payload has no pull-request head; when the
// body carries a typed DISPOSITION line, head is the sha that line names,
// read with the lander's parser after quotes and fences are stripped. A
// comment that is not a typed line is still one entry, with an empty head.
//
// Delivery id is not one of the named fields, so this slice does not dedupe a
// GitHub retry. This package does not listen and does not check a webhook
// secret. A receiver on the tailnet is a later slice.
package ghevent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
	"github.com/redis/go-redis/v9"
)

// Stream is the Redis stream every carried delivery is appended to.
const Stream = "ev:github"

// ErrNotCarried is a delivery this stream does not record.
var ErrNotCarried = errors.New("ev:github: event is not carried")

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

	// check_run only: the check's name and its id (a decimal string).
	Check      string
	CheckRunID string

	// workflow_run and check_run.
	Status     string
	Conclusion string
}

// Decode reads one delivery. event is the X-GitHub-Event header.
func Decode(event string, payload []byte) (Entry, error) {
	event = strings.TrimSpace(event)
	if !carried(event) {
		return Entry{}, ErrNotCarried
	}
	var p delivery
	if err := json.Unmarshal(payload, &p); err != nil {
		return Entry{}, fmt.Errorf("ev:github: payload: %w", err)
	}
	e := Entry{
		Repo:   strings.TrimSpace(p.Repository.FullName),
		Kind:   event,
		Action: strings.TrimSpace(p.Action),
		Sender: strings.TrimSpace(p.Sender.Login),
	}
	if event == "ping" {
		return decodePing(e, &p), nil
	}
	if e.Repo == "" {
		return Entry{}, errMissing("repository.full_name")
	}
	if e.Action == "" {
		return Entry{}, errMissing("action")
	}
	switch event {
	case "pull_request":
		if p.PullRequest == nil {
			return Entry{}, errMissing("pull_request")
		}
		e.Number = firstID(p.PullRequest.Number, p.Number)
		e.Head = strings.TrimSpace(p.PullRequest.Head.SHA)
		e.At = pullAt(p.PullRequest)
	case "issue_comment":
		if p.Comment == nil {
			return Entry{}, errMissing("comment")
		}
		if p.Issue != nil {
			e.Number = formatID(p.Issue.Number)
		}
		if e.Number == "" {
			e.Number = formatID(p.Number)
		}
		e.CommentID = formatID(p.Comment.ID)
		e.At = first(strp(p.Comment.UpdatedAt), strp(p.Comment.CreatedAt))
		fillHeadFromLine(&e, p.Comment.Body)
	case "pull_request_review":
		if p.Review == nil {
			return Entry{}, errMissing("review")
		}
		e.Head = strings.TrimSpace(p.Review.CommitID)
		e.At = strp(p.Review.SubmittedAt)
		if p.PullRequest != nil {
			e.Number = formatID(p.PullRequest.Number)
			if e.Head == "" {
				e.Head = strings.TrimSpace(p.PullRequest.Head.SHA)
			}
			if e.At == "" {
				e.At = pullAt(p.PullRequest)
			}
		}
		if e.Number == "" {
			e.Number = formatID(p.Number)
		}
		fillHeadFromLine(&e, p.Review.Body)
	case "check_run":
		if err := applyCheck(&e, p.CheckRun, "check_run"); err != nil {
			return Entry{}, err
		}
		e.Check = strings.TrimSpace(p.CheckRun.Name)
		e.CheckRunID = formatID(p.CheckRun.ID)
		e.Status = strings.TrimSpace(p.CheckRun.Status)
		e.Conclusion = strp(p.CheckRun.Conclusion)
	case "check_suite":
		if err := applyCheck(&e, p.CheckSuite, "check_suite"); err != nil {
			return Entry{}, err
		}
	case "merge_group":
		if p.MergeGroup == nil {
			return Entry{}, errMissing("merge_group")
		}
		e.Head = strings.TrimSpace(p.MergeGroup.HeadSHA)
		e.Number = mergeGroupNumber(p.MergeGroup.HeadRef)
		if p.MergeGroup.HeadCommit != nil {
			e.At = strings.TrimSpace(p.MergeGroup.HeadCommit.Timestamp)
		}
	case "issues":
		if !issueActionCarried(e.Action) {
			return Entry{}, ErrNotCarried
		}
		if p.Issue == nil {
			return Entry{}, errMissing("issue")
		}
		e.Number = formatID(p.Issue.Number)
		e.At = first(strp(p.Issue.UpdatedAt), strp(p.Issue.CreatedAt))
		e.Labels = make([]string, 0, len(p.Issue.Labels))
		for _, l := range p.Issue.Labels {
			if n := strings.TrimSpace(l.Name); n != "" {
				e.Labels = append(e.Labels, n)
			}
		}
		e.Body = strp(p.Issue.Body)
		e.State = strings.TrimSpace(p.Issue.State)
		e.StateReason = strp(p.Issue.StateReason)
	case "workflow_run":
		w := p.WorkflowRun
		if w == nil {
			return Entry{}, errMissing("workflow_run")
		}
		e.Head = strings.TrimSpace(w.HeadSHA)
		e.At = first(strp(w.UpdatedAt), strp(w.RunStartedAt), strp(w.CreatedAt))
		for _, pr := range w.PullRequests {
			if e.Number = formatID(pr.Number); e.Number != "" {
				break
			}
		}
		e.RunID = formatID(w.ID)
		e.Workflow = strings.TrimSpace(w.Name)
		e.Status = strings.TrimSpace(w.Status)
		e.Conclusion = strp(w.Conclusion)
	default:
		return Entry{}, fmt.Errorf("ev:github: kind %q is carried but not decoded", event)
	}
	return e, nil
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
// the issues, workflow_run or check_run fields for those kinds.
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
	case "check_run":
		v["check"] = e.Check
		v["check_run_id"] = e.CheckRunID
		v["status"] = e.Status
		v["conclusion"] = e.Conclusion
	}
	return v, nil
}

// Accept decodes one delivery and appends it. A delivery the stream does not
// carry returns ErrNotCarried and writes nothing.
func Accept(ctx context.Context, rdb *redis.Client, event string, payload []byte) (string, error) {
	e, err := Decode(event, payload)
	if err != nil {
		return "", err
	}
	return Publish(ctx, rdb, e)
}

func carried(event string) bool {
	switch event {
	case "ping", "pull_request", "issue_comment", "pull_request_review", "check_suite", "check_run",
		"merge_group", "issues", "workflow_run":
		return true
	default:
		return false
	}
}

// issueActionCarried is the issues actions the stream records (#3177).
func issueActionCarried(action string) bool {
	switch action {
	case "opened", "labeled", "unlabeled", "edited", "closed", "reopened":
		return true
	default:
		return false
	}
}

// decodePing fills a ping entry. A ping is never refused for a missing field:
// it is the hook's proof of reach, so it is written with whatever it names.
func decodePing(e Entry, p *delivery) Entry {
	if e.Repo == "" && p.Organization != nil {
		e.Repo = strings.TrimSpace(p.Organization.Login)
	}
	e.Action = "ping"
	if p.Hook != nil {
		e.At = first(strp(p.Hook.UpdatedAt), strp(p.Hook.CreatedAt))
	}
	return e
}

func errMissing(what string) error {
	return fmt.Errorf("ev:github: payload has no %s", what)
}

func applyCheck(e *Entry, c *checkHead, what string) error {
	if c == nil {
		return errMissing(what)
	}
	e.Head = strings.TrimSpace(c.HeadSHA)
	e.At = first(strp(c.CompletedAt), strp(c.UpdatedAt), strp(c.StartedAt), strp(c.CreatedAt))
	for _, pr := range c.PullRequests {
		if e.Number == "" {
			e.Number = formatID(pr.Number)
		}
		if e.Head == "" {
			e.Head = strings.TrimSpace(pr.Head.SHA)
		}
		if e.Number != "" && e.Head != "" {
			break
		}
	}
	return nil
}

func pullAt(p *pullRequest) string {
	if p == nil {
		return ""
	}
	return first(strp(p.UpdatedAt), strp(p.CreatedAt))
}

// fillHeadFromLine records the sha on the first typed DISPOSITION line when
// the payload itself named no head. Quoted lines and fenced blocks are not
// typed lines: the lander strips them before it reads a verdict.
func fillHeadFromLine(e *Entry, body string) {
	if e == nil || e.Head != "" || strings.TrimSpace(body) == "" {
		return
	}
	clean := merge.StripQuotedAndCode(body)
	for _, line := range strings.Split(clean, "\n") {
		_, head, _, _, ok := merge.ParseDispositionLine(line)
		if ok && head != "" {
			e.Head = head
			return
		}
	}
}

// mergeGroupNumber reads the pull number from a merge-queue head ref:
// refs/heads/gh-readonly-queue/<base>/pr-<number>-<sha>. The base may contain
// slashes, so the number is the leading digits of the last segment. Any other
// shape contributes no number rather than a guess.
func mergeGroupNumber(ref string) string {
	seg := ref
	if i := strings.LastIndex(ref, "/"); i >= 0 {
		seg = ref[i+1:]
	}
	if !strings.HasPrefix(seg, "pr-") {
		return ""
	}
	rest := seg[len("pr-"):]
	i := 0
	for i < len(rest) && rest[i] >= '0' && rest[i] <= '9' {
		i++
	}
	if i == 0 || i >= len(rest) || rest[i] != '-' {
		return ""
	}
	return rest[:i]
}

func formatID(n int64) string {
	if n <= 0 {
		return ""
	}
	return strconv.FormatInt(n, 10)
}

func firstID(a, b int64) string {
	if s := formatID(a); s != "" {
		return s
	}
	return formatID(b)
}

func strp(p *string) string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(*p)
}

func first(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

type delivery struct {
	Action      string       `json:"action"`
	Number      int64        `json:"number"`
	Repository  repoName     `json:"repository"`
	Sender      userLogin    `json:"sender"`
	PullRequest *pullRequest `json:"pull_request"`
	Issue       *struct {
		Number      int64   `json:"number"`
		Body        *string `json:"body"`
		State       string  `json:"state"`
		StateReason *string `json:"state_reason"`
		CreatedAt   *string `json:"created_at"`
		UpdatedAt   *string `json:"updated_at"`
		Labels      []struct {
			Name string `json:"name"`
		} `json:"labels"`
	} `json:"issue"`
	Organization *userLogin `json:"organization"`
	Hook         *struct {
		CreatedAt *string `json:"created_at"`
		UpdatedAt *string `json:"updated_at"`
	} `json:"hook"`
	WorkflowRun *struct {
		ID           int64   `json:"id"`
		Name         string  `json:"name"`
		HeadSHA      string  `json:"head_sha"`
		Status       string  `json:"status"`
		Conclusion   *string `json:"conclusion"`
		CreatedAt    *string `json:"created_at"`
		UpdatedAt    *string `json:"updated_at"`
		RunStartedAt *string `json:"run_started_at"`
		PullRequests []struct {
			Number int64 `json:"number"`
		} `json:"pull_requests"`
	} `json:"workflow_run"`
	Comment *struct {
		ID        int64   `json:"id"`
		Body      string  `json:"body"`
		CreatedAt *string `json:"created_at"`
		UpdatedAt *string `json:"updated_at"`
	} `json:"comment"`
	Review *struct {
		CommitID    string  `json:"commit_id"`
		SubmittedAt *string `json:"submitted_at"`
		Body        string  `json:"body"`
	} `json:"review"`
	CheckRun   *checkHead `json:"check_run"`
	CheckSuite *checkHead `json:"check_suite"`
	MergeGroup *struct {
		HeadSHA    string `json:"head_sha"`
		HeadRef    string `json:"head_ref"`
		HeadCommit *struct {
			Timestamp string `json:"timestamp"`
		} `json:"head_commit"`
	} `json:"merge_group"`
}

type repoName struct {
	FullName string `json:"full_name"`
}

type userLogin struct {
	Login string `json:"login"`
}

type pullRequest struct {
	Number    int64   `json:"number"`
	UpdatedAt *string `json:"updated_at"`
	CreatedAt *string `json:"created_at"`
	Head      struct {
		SHA string `json:"sha"`
	} `json:"head"`
}

type checkHead struct {
	ID           int64   `json:"id"`   // check_run only
	Name         string  `json:"name"` // check_run only
	Status       string  `json:"status"`
	Conclusion   *string `json:"conclusion"`
	HeadSHA      string  `json:"head_sha"`
	StartedAt    *string `json:"started_at"`
	CompletedAt  *string `json:"completed_at"`
	CreatedAt    *string `json:"created_at"`
	UpdatedAt    *string `json:"updated_at"`
	PullRequests []struct {
		Number int64 `json:"number"`
		Head   struct {
			SHA string `json:"sha"`
		} `json:"head"`
	} `json:"pull_requests"`
}
