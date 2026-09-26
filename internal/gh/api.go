package gh

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// The typed calls every verb shares. Each is one Do; none reads a check
// state (that is ci:<repo>:<sha>:gh, from the webhook), and only PRBody
// and IssueBody read a body (the import and the landed record are the two
// readers of a body, #4343 BUILD 4; everything else reads the Redis copy).

// PR is the fields of GET /repos/{repo}/pulls/{n} the verbs read.
type PR struct {
	Number         int    `json:"number"`
	State          string `json:"state"`
	Merged         bool   `json:"merged"`
	MergeCommitSHA string `json:"merge_commit_sha"`
	MergeableState string `json:"mergeable_state"`
	Title          string `json:"title"`
	Body           string `json:"body"`
	HTMLURL        string `json:"html_url"`
	Head           struct {
		SHA string `json:"sha"`
		Ref string `json:"ref"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
}

// OpenPR opens head -> base and returns its number.
func (c *Client) OpenPR(ctx context.Context, repo, head, base, title, body string) (int, error) {
	var out struct {
		Number int `json:"number"`
	}
	_, err := c.Do(ctx, http.MethodPost, "/repos/"+repo+"/pulls",
		map[string]string{"title": title, "head": head, "base": base, "body": body}, &out)
	if err != nil {
		return 0, err
	}
	if out.Number <= 0 {
		return 0, fmt.Errorf("POST /repos/%s/pulls: no number in the reply", repo)
	}
	return out.Number, nil
}

// ViewPR reads one PR: state, merged, head, mergeable_state, title, body.
func (c *Client) ViewPR(ctx context.Context, repo string, n int) (PR, error) {
	var pr PR
	_, err := c.Do(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/pulls/%d", repo, n), nil, &pr)
	return pr, err
}

// MergePR merges the PR at exactly sha (GitHub refuses when the head moved)
// and returns the merge commit.
func (c *Client) MergePR(ctx context.Context, repo string, n int, sha, title string) (string, error) {
	var out struct {
		SHA    string `json:"sha"`
		Merged bool   `json:"merged"`
	}
	_, err := c.Do(ctx, http.MethodPut, fmt.Sprintf("/repos/%s/pulls/%d/merge", repo, n),
		map[string]string{"merge_method": "merge", "sha": sha, "commit_title": title}, &out)
	if err != nil {
		return "", err
	}
	if !out.Merged || out.SHA == "" {
		return "", fmt.Errorf("PUT merge %s#%d: not merged", repo, n)
	}
	return out.SHA, nil
}

// Comment posts one comment on an issue or PR and returns its id.
func (c *Client) Comment(ctx context.Context, repo string, n int, body string) (int64, error) {
	var out struct {
		ID int64 `json:"id"`
	}
	_, err := c.Do(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/issues/%d/comments", repo, n), map[string]string{"body": body}, &out)
	return out.ID, err
}

// Close sets a PR's state to closed.
func (c *Client) Close(ctx context.Context, repo string, n int) error {
	_, err := c.Do(ctx, http.MethodPatch, fmt.Sprintf("/repos/%s/pulls/%d", repo, n), map[string]string{"state": "closed"}, nil)
	return err
}

// CloseIssue sets an issue's state to closed (completed). GitHub closes a
// "Closes #n" issue only on a merge into the default branch; the lander
// lands on dev, so it closes the issue itself.
func (c *Client) CloseIssue(ctx context.Context, repo string, n int) error {
	_, err := c.Do(ctx, http.MethodPatch, fmt.Sprintf("/repos/%s/issues/%d", repo, n), map[string]string{"state": "closed", "state_reason": "completed"}, nil)
	return err
}

// PRBody reads a PR's body: the landed record's one read, for a member
// whose record does not say which issues it closes.
func (c *Client) PRBody(ctx context.Context, repo string, n int) (string, error) {
	pr, err := c.ViewPR(ctx, repo, n)
	return pr.Body, err
}

// CreateIssue files an issue and returns its number and html_url.
func (c *Client) CreateIssue(ctx context.Context, repo, title, body string) (int, string, error) {
	var out struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
	}
	_, err := c.Do(ctx, http.MethodPost, "/repos/"+repo+"/issues", map[string]string{"title": title, "body": body}, &out)
	if err != nil {
		return 0, "", err
	}
	if out.Number <= 0 {
		return 0, "", fmt.Errorf("POST /repos/%s/issues: no number in the reply", repo)
	}
	return out.Number, out.HTMLURL, nil
}

// IssueBody reads an issue's body: the import's read (issue -> card).
func (c *Client) IssueBody(ctx context.Context, repo string, n int) (string, error) {
	var out struct {
		Body *string `json:"body"`
	}
	if _, err := c.Do(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/issues/%d", repo, n), nil, &out); err != nil {
		return "", err
	}
	if out.Body == nil {
		return "", nil
	}
	return *out.Body, nil
}

// CommentBody reads one comment's body back (the file verb's read-back).
func (c *Client) CommentBody(ctx context.Context, repo string, id int64) (string, error) {
	var out struct {
		Body *string `json:"body"`
	}
	if _, err := c.Do(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/issues/comments/%d", repo, id), nil, &out); err != nil {
		return "", err
	}
	if out.Body == nil {
		return "", nil
	}
	return *out.Body, nil
}

// Issues lists /repos/{repo}/issues with the query (state, since, ...).
func (c *Client) Issues(ctx context.Context, repo string, q url.Values, out any) error {
	_, err := c.Do(ctx, http.MethodGet, "/repos/"+repo+"/issues?"+q.Encode(), nil, out)
	return err
}

// RateLimit is GET /rate_limit: the core and graphql remaining, by REST.
func (c *Client) RateLimit(ctx context.Context) (core, graphql int, err error) {
	var out struct {
		Resources struct {
			Core    struct{ Remaining int }
			GraphQL struct{ Remaining int }
		}
	}
	_, err = c.Do(ctx, http.MethodGet, "/rate_limit", nil, &out)
	return out.Resources.Core.Remaining, out.Resources.GraphQL.Remaining, err
}
