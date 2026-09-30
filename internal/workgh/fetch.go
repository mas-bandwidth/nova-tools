package workgh

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/workfile"
)

// ErrBudget is returned when the next call would pass the fetcher's budget.
var ErrBudget = errors.New("call budget spent")

// Fetcher reads repositories and issues through Q, counting every call
// (Calls) and the GraphQL points GitHub charged for them (Points).
type Fetcher struct {
	Q Query
	// PageSize is the issues asked for per page, 1 to 100. A page GitHub
	// fails to answer is asked again at half the size, down to MinPageSize.
	PageSize int
	// MaxCalls is the budget: the call past it is refused with ErrBudget.
	MaxCalls int
	// Log takes one line per retried page; nil is silent.
	Log io.Writer

	Calls     int
	Points    int
	Remaining int // GraphQL points left in the hour after the last call, -1 before any
}

// MinPageSize is the smallest page a failed page is retried at.
const MinPageSize = 5

// RepoMeta is one repository as the organization lists it.
type RepoMeta struct {
	Name     string
	URL      string
	Archived bool
	Issues   int // GitHub's count of its issues, open and closed
}

type rateLimit struct {
	Cost      int `json:"cost"`
	Remaining int `json:"remaining"`
}

type pageInfo struct {
	HasNextPage bool   `json:"hasNextPage"`
	EndCursor   string `json:"endCursor"`
}

type gqlError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

// call runs one document, counts it, and decodes data into out.
func (f *Fetcher) call(ctx context.Context, doc string, vars map[string]any, out any) error {
	if f.MaxCalls > 0 && f.Calls >= f.MaxCalls {
		return fmt.Errorf("%w: %d of %d calls made", ErrBudget, f.Calls, f.MaxCalls)
	}
	f.Calls++
	raw, qerr := f.Q(ctx, doc, vars)
	var env struct {
		Data      json.RawMessage `json:"data"`
		Errors    []gqlError      `json:"errors"`
		RateLimit *rateLimit      `json:"-"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &env); err != nil && qerr == nil {
			return fmt.Errorf("workgh: the reply is not JSON: %v", err)
		}
	}
	if len(env.Data) > 0 && string(env.Data) != "null" {
		var rl struct {
			RateLimit *rateLimit `json:"rateLimit"`
		}
		if json.Unmarshal(env.Data, &rl) == nil && rl.RateLimit != nil {
			f.Points += rl.RateLimit.Cost
			f.Remaining = rl.RateLimit.Remaining
		}
	}
	if len(env.Errors) > 0 {
		var msgs []string
		for _, e := range env.Errors {
			msgs = append(msgs, strings.TrimSpace(e.Type+" "+e.Message))
		}
		return fmt.Errorf("workgh: GitHub answered with errors: %s", strings.Join(msgs, "; "))
	}
	if qerr != nil {
		return qerr
	}
	if len(env.Data) == 0 || string(env.Data) == "null" {
		return errors.New("workgh: the reply holds no data")
	}
	return json.Unmarshal(env.Data, out)
}

// Repos lists every repository of org, sorted by name, with its issue count.
func (f *Fetcher) Repos(ctx context.Context, org string) ([]RepoMeta, error) {
	var out []RepoMeta
	after := ""
	for {
		vars := map[string]any{"org": org}
		if after != "" {
			vars["after"] = after
		}
		var d struct {
			Organization *struct {
				Repositories struct {
					TotalCount int      `json:"totalCount"`
					PageInfo   pageInfo `json:"pageInfo"`
					Nodes      []rawRepo
				} `json:"repositories"`
			} `json:"organization"`
		}
		if err := f.call(ctx, reposDoc, vars, &d); err != nil {
			return nil, err
		}
		if d.Organization == nil {
			return nil, fmt.Errorf("workgh: no organization %q visible to this login", org)
		}
		for _, n := range d.Organization.Repositories.Nodes {
			out = append(out, n.meta())
		}
		if !d.Organization.Repositories.PageInfo.HasNextPage {
			if len(out) != d.Organization.Repositories.TotalCount {
				return nil, fmt.Errorf("workgh: %s lists %d repositories, its pages held %d; run again", org, d.Organization.Repositories.TotalCount, len(out))
			}
			break
		}
		after = d.Organization.Repositories.PageInfo.EndCursor
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Repo reads one repository's listing entry.
func (f *Fetcher) Repo(ctx context.Context, name string) (RepoMeta, error) {
	owner, repo, err := split(name)
	if err != nil {
		return RepoMeta{}, err
	}
	var d struct {
		Repository *rawRepo `json:"repository"`
	}
	if err := f.call(ctx, repoDoc, map[string]any{"owner": owner, "repo": repo}, &d); err != nil {
		return RepoMeta{}, err
	}
	if d.Repository == nil {
		return RepoMeta{}, fmt.Errorf("workgh: no repository %s visible to this login", name)
	}
	return d.Repository.meta(), nil
}

type rawRepo struct {
	NameWithOwner string `json:"nameWithOwner"`
	URL           string `json:"url"`
	IsArchived    bool   `json:"isArchived"`
	Issues        struct {
		TotalCount int `json:"totalCount"`
	} `json:"issues"`
}

func (r rawRepo) meta() RepoMeta {
	return RepoMeta{Name: r.NameWithOwner, URL: r.URL, Archived: r.IsArchived, Issues: r.Issues.TotalCount}
}

func split(name string) (string, string, error) {
	owner, repo, ok := strings.Cut(name, "/")
	if !ok || owner == "" || repo == "" || strings.Contains(repo, "/") {
		return "", "", fmt.Errorf("workgh: %q is not owner/name", name)
	}
	return owner, repo, nil
}

// Issues reads every issue of the repository, open and closed, with its full
// contents, sorted by number. It refuses a fetch whose count disagrees with
// the count GitHub gave (the repository changed while it was read).
func (f *Fetcher) Issues(ctx context.Context, meta RepoMeta) (workfile.Repo, error) {
	owner, repo, err := split(meta.Name)
	if err != nil {
		return workfile.Repo{}, err
	}
	size := f.PageSize
	if size < 1 || size > 100 {
		return workfile.Repo{}, fmt.Errorf("workgh: page size %d is outside 1 to 100", size)
	}
	out := workfile.Repo{Name: meta.Name, URL: meta.URL, Archived: meta.Archived}
	after := ""
	total := -1
	for {
		vars := map[string]any{"owner": owner, "repo": repo, "n": size}
		if after != "" {
			vars["after"] = after
		}
		var d struct {
			Repository *struct {
				Issues struct {
					TotalCount int        `json:"totalCount"`
					PageInfo   pageInfo   `json:"pageInfo"`
					Nodes      []rawIssue `json:"nodes"`
				} `json:"issues"`
			} `json:"repository"`
		}
		err := f.call(ctx, issuesDoc, vars, &d)
		if err != nil {
			if errors.Is(err, ErrBudget) || size <= MinPageSize || ctx.Err() != nil {
				return out, fmt.Errorf("%s: %w", meta.Name, err)
			}
			size = max(size/2, MinPageSize)
			if f.Log != nil {
				fmt.Fprintf(f.Log, "PAGE RETRY repo=%s size=%d reason=%s\n", meta.Name, size, strings.ReplaceAll(err.Error(), " ", "_"))
			}
			continue
		}
		if d.Repository == nil {
			return out, fmt.Errorf("workgh: no repository %s visible to this login", meta.Name)
		}
		total = d.Repository.Issues.TotalCount
		for _, n := range d.Repository.Issues.Nodes {
			is, err := f.complete(ctx, owner, repo, n)
			if err != nil {
				return out, err
			}
			out.Issues = append(out.Issues, is)
		}
		if !d.Repository.Issues.PageInfo.HasNextPage {
			break
		}
		after = d.Repository.Issues.PageInfo.EndCursor
	}
	sort.Slice(out.Issues, func(i, j int) bool { return out.Issues[i].Number < out.Issues[j].Number })
	for i := 1; i < len(out.Issues); i++ {
		if out.Issues[i].Number == out.Issues[i-1].Number {
			return out, fmt.Errorf("workgh: %s: issue %d came twice (the repository changed while it was read); run again", meta.Name, out.Issues[i].Number)
		}
	}
	if total >= 0 && len(out.Issues) != total {
		return out, fmt.Errorf("workgh: %s: GitHub counts %d issues, the pages held %d (the repository changed while it was read); run again", meta.Name, total, len(out.Issues))
	}
	return out, nil
}
