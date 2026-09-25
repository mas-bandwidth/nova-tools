package stream

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// GitHub is the REST seam: the only GitHub calls the lander makes are the
// ones a git remote cannot avoid (open the stream PR, merge it, comment and
// close the members). Budget caps the calls one verb run makes; a run that
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
		msg := strings.TrimSpace(string(b))
		var e struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(b, &e) == nil && e.Message != "" {
			msg = e.Message
		}
		return resp.StatusCode, fmt.Errorf("%s %s: HTTP %d: %s", method, path, resp.StatusCode, msg)
	}
	if out != nil && len(b) > 0 {
		if err := json.Unmarshal(b, out); err != nil {
			return resp.StatusCode, fmt.Errorf("%s %s: %v", method, path, err)
		}
	}
	return resp.StatusCode, nil
}

// OpenPR opens head -> base and returns its number.
func (g *GitHub) OpenPR(ctx context.Context, repo, head, base, title, body string) (int, error) {
	var out struct {
		Number int `json:"number"`
	}
	_, err := g.do(ctx, http.MethodPost, "/repos/"+repo+"/pulls",
		map[string]string{"title": title, "head": head, "base": base, "body": body}, &out)
	if err != nil {
		return 0, err
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
