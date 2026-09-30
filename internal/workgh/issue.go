package workgh

import (
	"context"
	"fmt"
	"sort"

	"github.com/mas-bandwidth/nova-tools/internal/workfile"
)

type login struct {
	Login string `json:"login"`
}

func (l *login) name() string {
	if l == nil {
		return ""
	}
	return l.Login
}

type rawComment struct {
	ID                string `json:"id"`
	URL               string `json:"url"`
	Body              string `json:"body"`
	CreatedAt         string `json:"createdAt"`
	UpdatedAt         string `json:"updatedAt"`
	Author            *login `json:"author"`
	AuthorAssociation string `json:"authorAssociation"`
}

type rawRef struct {
	CreatedAt       string `json:"createdAt"`
	WillCloseTarget bool   `json:"willCloseTarget"`
	Actor           *login `json:"actor"`
	Source          *struct {
		Typename   string `json:"__typename"`
		URL        string `json:"url"`
		Number     int    `json:"number"`
		Repository struct {
			NameWithOwner string `json:"nameWithOwner"`
		} `json:"repository"`
	} `json:"source"`
}

type rawPR struct {
	Number     int    `json:"number"`
	URL        string `json:"url"`
	State      string `json:"state"`
	Repository struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
}

type commentConn struct {
	TotalCount int          `json:"totalCount"`
	PageInfo   pageInfo     `json:"pageInfo"`
	Nodes      []rawComment `json:"nodes"`
}

type refConn struct {
	TotalCount int      `json:"totalCount"`
	PageInfo   pageInfo `json:"pageInfo"`
	Nodes      []rawRef `json:"nodes"`
}

type prConn struct {
	TotalCount int      `json:"totalCount"`
	PageInfo   pageInfo `json:"pageInfo"`
	Nodes      []rawPR  `json:"nodes"`
}

type rawIssue struct {
	ID                string `json:"id"`
	Number            int    `json:"number"`
	URL               string `json:"url"`
	Title             string `json:"title"`
	Body              string `json:"body"`
	State             string `json:"state"`
	StateReason       string `json:"stateReason"`
	CreatedAt         string `json:"createdAt"`
	UpdatedAt         string `json:"updatedAt"`
	ClosedAt          string `json:"closedAt"`
	Locked            bool   `json:"locked"`
	ActiveLockReason  string `json:"activeLockReason"`
	Author            *login `json:"author"`
	AuthorAssociation string `json:"authorAssociation"`
	Labels            struct {
		TotalCount int `json:"totalCount"`
		Nodes      []struct {
			Name string `json:"name"`
		} `json:"nodes"`
	} `json:"labels"`
	Assignees struct {
		TotalCount int     `json:"totalCount"`
		Nodes      []login `json:"nodes"`
	} `json:"assignees"`
	Milestone *struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
	} `json:"milestone"`
	Comments      commentConn `json:"comments"`
	TimelineItems refConn     `json:"timelineItems"`
	ClosedBy      prConn      `json:"closedByPullRequestsReferences"`
}

// complete turns one page node into a tree issue, fetching the rest of any
// connection longer than its first page. A label or assignee list longer
// than one page is refused: GitHub caps both below it.
func (f *Fetcher) complete(ctx context.Context, owner, repo string, r rawIssue) (workfile.Issue, error) {
	at := workfile.Path(owner+"/"+repo, r.Number)
	if len(r.Labels.Nodes) != r.Labels.TotalCount || len(r.Assignees.Nodes) != r.Assignees.TotalCount {
		return workfile.Issue{}, fmt.Errorf("workgh: %s holds more labels or assignees than one page; refusing a partial record", at)
	}
	is := workfile.Issue{
		Number: r.Number, URL: r.URL, NodeID: r.ID, Title: r.Title, Body: r.Body,
		State: r.State, StateReason: r.StateReason,
		Author: r.Author.name(), AuthorAssociation: r.AuthorAssociation,
		Origin:  workfile.OriginOf(r.AuthorAssociation),
		Created: r.CreatedAt, Updated: r.UpdatedAt, Closed: r.ClosedAt,
		Locked: r.Locked, LockReason: r.ActiveLockReason,
	}
	for _, l := range r.Labels.Nodes {
		is.Labels = append(is.Labels, l.Name)
	}
	sort.Strings(is.Labels)
	for _, a := range r.Assignees.Nodes {
		is.Assignees = append(is.Assignees, a.Login)
	}
	sort.Strings(is.Assignees)
	if r.Milestone != nil {
		is.Milestone = &workfile.Milestone{Number: r.Milestone.Number, Title: r.Milestone.Title}
	}

	comments := r.Comments
	for {
		for _, c := range comments.Nodes {
			is.Comments = append(is.Comments, workfile.Comment{ID: c.ID, URL: c.URL, Author: c.Author.name(),
				AuthorAssociation: c.AuthorAssociation, Created: c.CreatedAt, Updated: c.UpdatedAt, Body: c.Body})
		}
		if !comments.PageInfo.HasNextPage {
			break
		}
		var d struct {
			Repository struct {
				Issue struct {
					Comments commentConn `json:"comments"`
				} `json:"issue"`
			} `json:"repository"`
		}
		if err := f.call(ctx, commentsDoc, f.more(owner, repo, r.Number, comments.PageInfo.EndCursor), &d); err != nil {
			return is, fmt.Errorf("%s comments: %w", at, err)
		}
		comments = d.Repository.Issue.Comments
	}
	if len(is.Comments) != comments.TotalCount {
		return is, fmt.Errorf("workgh: %s: GitHub counts %d comments, the pages held %d; run again", at, comments.TotalCount, len(is.Comments))
	}

	refs := r.TimelineItems
	for {
		for _, x := range refs.Nodes {
			ref := workfile.Reference{Actor: x.Actor.name(), At: x.CreatedAt, WillClose: x.WillCloseTarget}
			if x.Source != nil {
				ref.Kind, ref.Repo, ref.Number, ref.URL = x.Source.Typename, x.Source.Repository.NameWithOwner, x.Source.Number, x.Source.URL
			}
			is.References = append(is.References, ref)
		}
		if !refs.PageInfo.HasNextPage {
			break
		}
		var d struct {
			Repository struct {
				Issue struct {
					TimelineItems refConn `json:"timelineItems"`
				} `json:"issue"`
			} `json:"repository"`
		}
		if err := f.call(ctx, refsDoc, f.more(owner, repo, r.Number, refs.PageInfo.EndCursor), &d); err != nil {
			return is, fmt.Errorf("%s references: %w", at, err)
		}
		refs = d.Repository.Issue.TimelineItems
	}
	if len(is.References) != refs.TotalCount {
		return is, fmt.Errorf("workgh: %s: GitHub counts %d cross-references, the pages held %d; run again", at, refs.TotalCount, len(is.References))
	}

	prs := r.ClosedBy
	for {
		for _, p := range prs.Nodes {
			is.LinkedPRs = append(is.LinkedPRs, workfile.LinkedPR{Repo: p.Repository.NameWithOwner, Number: p.Number, URL: p.URL, State: p.State})
		}
		if !prs.PageInfo.HasNextPage {
			break
		}
		var d struct {
			Repository struct {
				Issue struct {
					ClosedBy prConn `json:"closedByPullRequestsReferences"`
				} `json:"issue"`
			} `json:"repository"`
		}
		if err := f.call(ctx, prsDoc, f.more(owner, repo, r.Number, prs.PageInfo.EndCursor), &d); err != nil {
			return is, fmt.Errorf("%s linked pull requests: %w", at, err)
		}
		prs = d.Repository.Issue.ClosedBy
	}
	if len(is.LinkedPRs) != prs.TotalCount {
		return is, fmt.Errorf("workgh: %s: GitHub counts %d linked pull requests, the pages held %d; run again", at, prs.TotalCount, len(is.LinkedPRs))
	}
	return is, nil
}

func (f *Fetcher) more(owner, repo string, number int, after string) map[string]any {
	return map[string]any{"owner": owner, "repo": repo, "number": number, "after": after}
}
