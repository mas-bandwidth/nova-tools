package stream

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// GitHub is the REST seam: the only GitHub calls the lander makes are the
// ones a git remote cannot avoid (open the stream PR, merge it, comment and
// close the members, read a member's body when its record does not say which
// issues it closes, and comment on and close those issues, which GitHub does
// not close on a merge into a branch other than the default). Budget caps the calls one verb run makes; a run that
// would exceed it stops and says so. Never GraphQL.
type GitHub struct {
	API    string // default https://api.github.com
	Token  string
	HTTP   *http.Client
	Budget int // 0: unlimited
	Calls  int
}

// ErrBudget is returned once the run's REST budget is spent.
var ErrBudget = errors.New("REST budget spent")

func (g *GitHub) do(ctx context.Context, method, path string, body any, out any) (int, error) {
	if g.Budget > 0 && g.Calls >= g.Budget {
		return 0, ErrBudget
	}
	g.Calls++
	api := strings.TrimRight(g.API, "/")
	if api == "" {
		api = "https://api.github.com"
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, api+path, rd)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if g.Token != "" {
		req.Header.Set("Authorization", "Bearer "+g.Token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	hc := g.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, newAPIError(method, path, resp.StatusCode, b)
	}
	if out != nil && len(b) > 0 {
		if err := json.Unmarshal(b, out); err != nil {
			return resp.StatusCode, fmt.Errorf("%s %s: %v", method, path, err)
		}
	}
	return resp.StatusCode, nil
}

// APIError is a non-2xx reply from the GitHub API. Its text carries
// GitHub's message and every errors[] entry, so nothing errors out without
// saying why.
type APIError struct {
	Method, Path string
	Status       int
	Message      string
	Errors       []string // errors[].message, or its code and field when the message is empty
}

func (e *APIError) Error() string {
	why := e.Message
	for _, m := range e.Errors {
		if why == "" {
			why = m
		} else {
			why += ": " + m
		}
	}
	return fmt.Sprintf("%s %s: HTTP %d: %s", e.Method, e.Path, e.Status, why)
}

// newAPIError reads GitHub's error body: message and errors[] (each a
// message, or resource, field and code). A body that is not that JSON is
// kept whole.
func newAPIError(method, path string, status int, b []byte) *APIError {
	e := &APIError{Method: method, Path: path, Status: status}
	var reply struct {
		Message string            `json:"message"`
		Errors  []json.RawMessage `json:"errors"`
	}
	if json.Unmarshal(b, &reply) != nil {
		e.Message = strings.TrimSpace(string(b))
		return e
	}
	e.Message = strings.TrimSpace(reply.Message)
	for _, raw := range reply.Errors {
		var one struct {
			Resource string `json:"resource"`
			Field    string `json:"field"`
			Code     string `json:"code"`
			Message  string `json:"message"`
		}
		if json.Unmarshal(raw, &one) != nil {
			var s string // GitHub sometimes sends errors as plain strings
			if json.Unmarshal(raw, &s) == nil && strings.TrimSpace(s) != "" {
				e.Errors = append(e.Errors, strings.TrimSpace(s))
			}
			continue
		}
		if m := strings.TrimSpace(one.Message); m != "" {
			e.Errors = append(e.Errors, m)
			continue
		}
		var parts []string
		if one.Resource != "" {
			parts = append(parts, "resource="+one.Resource)
		}
		if one.Field != "" {
			parts = append(parts, "field="+one.Field)
		}
		if one.Code != "" {
			parts = append(parts, "code="+one.Code)
		}
		if len(parts) > 0 {
			e.Errors = append(e.Errors, strings.Join(parts, " "))
		}
	}
	if e.Message == "" && len(e.Errors) == 0 {
		e.Message = strings.TrimSpace(string(b))
	}
	return e
}

// prExists reports whether err is GitHub's 422 "A pull request already
// exists" for the head.
func prExists(err error) bool {
	var e *APIError
	if !errors.As(err, &e) || e.Status != http.StatusUnprocessableEntity {
		return false
	}
	for _, m := range append([]string{e.Message}, e.Errors...) {
		if strings.HasPrefix(strings.TrimSpace(m), "A pull request already exists") {
			return true
		}
	}
	return false
}

// OpenPRFor returns the number of the open PR whose head is branch (owner
// taken from repo unless branch is owner:branch), or 0 when none is open.
func (g *GitHub) OpenPRFor(ctx context.Context, repo, branch string) (int, error) {
	head := branch
	if !strings.Contains(head, ":") {
		owner, _, _ := strings.Cut(repo, "/")
		head = owner + ":" + branch
	}
	var out []struct {
		Number int `json:"number"`
	}
	q := url.Values{"head": {head}, "state": {"open"}}
	if _, err := g.do(ctx, http.MethodGet, "/repos/"+repo+"/pulls?"+q.Encode(), nil, &out); err != nil {
		return 0, err
	}
	for _, p := range out {
		if p.Number > 0 {
			return p.Number, nil
		}
	}
	return 0, nil
}

// OpenPR opens head -> base and returns its number. When GitHub answers 422
// "A pull request already exists", the open PR on head is adopted: the
// branch already carries the new head, so it is the stream PR.
func (g *GitHub) OpenPR(ctx context.Context, repo, head, base, title, body string) (int, error) {
	var out struct {
		Number int `json:"number"`
	}
	_, err := g.do(ctx, http.MethodPost, "/repos/"+repo+"/pulls",
		map[string]string{"title": title, "head": head, "base": base, "body": body}, &out)
	if err != nil {
		if !prExists(err) {
			return 0, err
		}
		n, lerr := g.OpenPRFor(ctx, repo, head)
		if lerr != nil {
			return 0, fmt.Errorf("%v; adopting the open PR: %v", err, lerr)
		}
		if n <= 0 {
			return 0, fmt.Errorf("%v; no open PR with head %s to adopt", err, head)
		}
		return n, nil
	}
	if out.Number <= 0 {
		return 0, fmt.Errorf("POST /repos/%s/pulls: no number in the reply", repo)
	}
	return out.Number, nil
}

// MergePR merges the PR at exactly sha (GitHub refuses when the head moved)
// and returns the merge commit.
func (g *GitHub) MergePR(ctx context.Context, repo string, n int, sha, title string) (string, error) {
	var out struct {
		SHA    string `json:"sha"`
		Merged bool   `json:"merged"`
	}
	_, err := g.do(ctx, http.MethodPut, fmt.Sprintf("/repos/%s/pulls/%d/merge", repo, n),
		map[string]string{"merge_method": "merge", "sha": sha, "commit_title": title}, &out)
	if err != nil {
		return "", err
	}
	if !out.Merged || out.SHA == "" {
		return "", fmt.Errorf("PUT merge %s#%d: not merged", repo, n)
	}
	return out.SHA, nil
}

// Comment posts one comment on an issue or PR.
func (g *GitHub) Comment(ctx context.Context, repo string, n int, body string) error {
	_, err := g.do(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/issues/%d/comments", repo, n), map[string]string{"body": body}, nil)
	return err
}

// Close sets a PR's state to closed.
func (g *GitHub) Close(ctx context.Context, repo string, n int) error {
	_, err := g.do(ctx, http.MethodPatch, fmt.Sprintf("/repos/%s/pulls/%d", repo, n), map[string]string{"state": "closed"}, nil)
	return err
}

// PRBody reads a PR's body: the lander's one read, for a member whose record
// does not say which issues it closes.
func (g *GitHub) PRBody(ctx context.Context, repo string, n int) (string, error) {
	var out struct {
		Body string `json:"body"`
	}
	_, err := g.do(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/pulls/%d", repo, n), nil, &out)
	return out.Body, err
}

// CloseIssue sets an issue's state to closed (completed). GitHub closes a
// "Closes #n" issue only on a merge into the default branch; the lander
// lands on dev, so it closes the issue itself.
func (g *GitHub) CloseIssue(ctx context.Context, repo string, n int) error {
	_, err := g.do(ctx, http.MethodPatch, fmt.Sprintf("/repos/%s/issues/%d", repo, n), map[string]string{"state": "closed", "state_reason": "completed"}, nil)
	return err
}
