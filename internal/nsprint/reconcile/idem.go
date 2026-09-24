// Package reconcile holds the nova-sprint reconciler's duties (#2756 section 5).
//
// This file: idempotency keys for external effects (5.4: a PR open is keyed
// pr:<repo>:<branch> in s:<S>:idem and is never made twice) and the one-call
// assignment of a ready task to a consumer (control 9). reclaim.go expires
// cards; orphan.go resolves reconcile-required. Every state change is one
// nova_sprint function call, fenced by the reconciler lease token.
package reconcile

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// Result is one reconciler call. Code is the process exit: 0 applied or
// nothing to do, 1 usage, 2 a guard refused, 3 fenced, 4 conflict, 5 not
// found. Status names what happened (NOTHING, QUEUED, LAUNCHED, REQUIRED,
// ORPHAN, ENDED, OK, ...). Receipt is the log entry id of the transition, or
// the stored one when the call was a repeat.
type Result struct {
	Code    int
	Verb    string
	ID      string
	Status  string
	Attempt int
	Receipt string
}

func (r Result) Line() string {
	return fmt.Sprintf("%s %s %s attempt=%d receipt=%s code=%d", r.Status, r.Verb, r.ID, r.Attempt, r.Receipt, r.Code)
}

func call(ctx context.Context, st *store.Store, verb, id, name string, args ...any) (Result, error) {
	if st == nil || st.Client() == nil {
		return Result{Code: 1, Verb: verb, ID: id, Status: "USAGE"}, nil
	}
	if err := fn.Load(ctx, st.Client()); err != nil {
		return Result{}, err
	}
	raw, err := st.Client().FCall(ctx, name, nil, args...).Text()
	if err != nil {
		return Result{}, err
	}
	parts := strings.Split(raw, "|")
	if len(parts) != 4 {
		return Result{}, fmt.Errorf("%s reply %q", name, raw)
	}
	code, err := strconv.Atoi(parts[0])
	if err != nil {
		return Result{}, fmt.Errorf("%s reply %q", name, raw)
	}
	attempt := 0
	if parts[2] != "" {
		if attempt, err = strconv.Atoi(parts[2]); err != nil {
			return Result{}, fmt.Errorf("%s reply %q", name, raw)
		}
	}
	return Result{Code: code, Verb: verb, ID: id, Status: parts[1], Attempt: attempt, Receipt: parts[3]}, nil
}

// AssignRequest routes one ready task to one consumer (a friend, or
// harvest:<bench>). Fence is the reconciler lease token.
type AssignRequest struct {
	Sprint   string
	ID       string
	Consumer string
	Fence    string
}

// Assign moves a ready task to open:<consumer> with its owner, receipt and
// idem key route:<id>:<attempt> in one function call. Two concurrent refills
// of one task give one assignment and one receipt; a repeat by the winner
// returns the stored receipt (Code 0), a loser gets Code 4 CONFLICT.
func Assign(ctx context.Context, st *store.Store, req AssignRequest) (Result, error) {
	return call(ctx, st, "task assign", req.ID, "ns_task_assign", req.Sprint, req.ID, req.Consumer, req.Fence)
}

// PRHost is the forge side of a PR open. FindOpen looks up an open PR whose
// head is branch; Open opens one. RESTPRHost is the GitHub REST version.
type PRHost interface {
	FindOpen(ctx context.Context, repo, branch string) (url string, found bool, err error)
	Open(ctx context.Context, repo, branch, base, title, body string) (url string, err error)
}

// PRRequest is one idempotent PR open for a pushed branch.
type PRRequest struct {
	Sprint string
	Repo   string // owner/name
	Branch string
	Base   string
	Title  string
	Body   string
	Who    string // the harvest worker instance, recorded while the open is pending
	Fence  string // the reconciler lease token; a stale one writes no idem key
}

// PRResult is the recorded PR. Opened is true only when this call opened it.
type PRResult struct {
	URL     string
	Opened  bool
	Receipt string
}

// PRKey is the idem key of a PR open (5.4).
func PRKey(repo, branch string) string { return "pr:" + repo + ":" + branch }

// EnsurePR opens at most one PR for repo:branch, whatever crashed before.
// The key pr:<repo>:<branch> is written pending before the forge call and the
// URL after it. A key that is already a URL returns it. A pending key (a crash
// after the forge opened the PR and before its URL was recorded) and a fresh
// key both look up an open PR on the branch by REST first: found, record it;
// absent, open once. A second PR is never opened (control 8). Both idem
// calls carry req.Fence: a stale or missing reconciler token is refused
// (FENCED) before the idem hash is read or written, and before the forge is
// asked anything.
func EnsurePR(ctx context.Context, st *store.Store, host PRHost, req PRRequest) (PRResult, error) {
	if host == nil || req.Repo == "" || req.Branch == "" || req.Who == "" {
		return PRResult{}, errors.New("ensure pr: host, repo, branch and who are required")
	}
	key := PRKey(req.Repo, req.Branch)
	begun, err := call(ctx, st, "pr open", key, "ns_idem_begin", req.Sprint, key, req.Who, req.Fence)
	if err != nil {
		return PRResult{}, err
	}
	if begun.Code != 0 {
		return PRResult{}, fmt.Errorf("ensure pr %s: %s", key, begun.Line())
	}
	if begun.Status == "EXISTS" && !strings.HasPrefix(begun.Receipt, "pending:") {
		return PRResult{URL: begun.Receipt}, nil
	}
	found, ok, err := host.FindOpen(ctx, req.Repo, req.Branch)
	if err != nil {
		return PRResult{}, fmt.Errorf("ensure pr %s: look up: %w", key, err)
	}
	reason, opened := "pr-found", false
	if !ok {
		if found, err = host.Open(ctx, req.Repo, req.Branch, req.Base, req.Title, req.Body); err != nil {
			return PRResult{}, fmt.Errorf("ensure pr %s: open: %w", key, err)
		}
		reason, opened = "pr-opened", true
	}
	done, err := call(ctx, st, "pr open", key, "ns_idem_commit", req.Sprint, key, found, req.Who, reason, req.Fence)
	if err != nil {
		return PRResult{}, err
	}
	if done.Code != 0 {
		return PRResult{}, fmt.Errorf("ensure pr %s: record %s: %s", key, found, done.Line())
	}
	return PRResult{URL: found, Opened: opened, Receipt: done.Receipt}, nil
}

// RESTPRHost talks to the GitHub REST API (BaseURL defaults to
// https://api.github.com). It never uses GraphQL.
type RESTPRHost struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

func (h RESTPRHost) do(ctx context.Context, method, path string, body any, out any) (int, error) {
	base := h.BaseURL
	if base == "" {
		base = "https://api.github.com"
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(base, "/")+path, rd)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if h.Token != "" {
		req.Header.Set("Authorization", "Bearer "+h.Token)
	}
	client := h.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, err
	}
	if resp.StatusCode/100 != 2 {
		return resp.StatusCode, fmt.Errorf("%s %s: %d %s", method, path, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, err
		}
	}
	return resp.StatusCode, nil
}

func (h RESTPRHost) FindOpen(ctx context.Context, repo, branch string) (string, bool, error) {
	owner, _, ok := strings.Cut(repo, "/")
	if !ok {
		return "", false, fmt.Errorf("repo %q is not owner/name", repo)
	}
	var pulls []struct {
		HTMLURL string `json:"html_url"`
	}
	q := url.Values{"state": {"open"}, "head": {owner + ":" + branch}, "per_page": {"10"}}
	if _, err := h.do(ctx, http.MethodGet, "/repos/"+repo+"/pulls?"+q.Encode(), nil, &pulls); err != nil {
		return "", false, err
	}
	if len(pulls) == 0 || pulls[0].HTMLURL == "" {
		return "", false, nil
	}
	return pulls[0].HTMLURL, true, nil
}

func (h RESTPRHost) Open(ctx context.Context, repo, branch, base, title, body string) (string, error) {
	var pr struct {
		HTMLURL string `json:"html_url"`
	}
	payload := map[string]string{"head": branch, "base": base, "title": title, "body": body}
	status, err := h.do(ctx, http.MethodPost, "/repos/"+repo+"/pulls", payload, &pr)
	if status == http.StatusUnprocessableEntity {
		// "A pull request already exists": record that one, never a second.
		if found, ok, ferr := h.FindOpen(ctx, repo, branch); ferr == nil && ok {
			return found, nil
		}
	}
	if err != nil {
		return "", err
	}
	if pr.HTMLURL == "" {
		return "", errors.New("pr open: no html_url in the reply")
	}
	return pr.HTMLURL, nil
}
