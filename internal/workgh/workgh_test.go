package workgh

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/workfile"
)

func fixture(t *testing.T) Query {
	t.Helper()
	q, err := Replay("testdata/reliable")
	if err != nil {
		t.Fatal(err)
	}
	return q
}

// TestFetchReadsTheRecordedRepository: the recorded public repository (two
// pages of 15) reads into every issue with every connection whole, in three
// calls (SPEC-WORK-V1 section 1.3).
func TestFetchReadsTheRecordedRepository(t *testing.T) {
	t.Parallel()
	f := &Fetcher{Q: fixture(t), PageSize: 15, MaxCalls: 10}
	m, err := f.Repo(context.Background(), "mas-bandwidth/reliable")
	if err != nil {
		t.Fatal(err)
	}
	r, err := f.Issues(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Issues) != m.Issues || m.Issues != 20 {
		t.Fatalf("read %d issues, the listing counts %d, the recording holds 20", len(r.Issues), m.Issues)
	}
	if f.Calls != 3 || f.Points <= 0 {
		t.Fatalf("calls=%d points=%d, want 3 calls and the points GitHub charged", f.Calls, f.Points)
	}
	comments, refs := 0, 0
	for i, is := range r.Issues {
		if i > 0 && is.Number <= r.Issues[i-1].Number {
			t.Fatalf("issues out of order at %d", is.Number)
		}
		if is.URL != workfile.IssueURL("mas-bandwidth/reliable", is.Number) || is.NodeID == "" || is.Created == "" {
			t.Fatalf("issue %d lacks its identity: %+v", is.Number, is)
		}
		if is.Origin != "internal" && is.Origin != "external" {
			t.Fatalf("issue %d origin %q", is.Number, is.Origin)
		}
		comments += len(is.Comments)
		refs += len(is.References)
	}
	if comments == 0 || refs == 0 {
		t.Fatalf("comments=%d references=%d: the recording holds both", comments, refs)
	}
}

// fake answers by document: the issues page, then the follow-up pages.
type fake struct {
	issues   string
	comments []string
	calls    []map[string]any
}

func (f *fake) q(_ context.Context, doc string, vars map[string]any) ([]byte, error) {
	f.calls = append(f.calls, vars)
	switch {
	case strings.Contains(doc, "issues(first:$n"):
		return []byte(f.issues), nil
	case strings.Contains(doc, "comments(first:100,after:$after)"):
		if len(f.comments) == 0 {
			return nil, errors.New("no more comment pages")
		}
		c := f.comments[0]
		f.comments = f.comments[1:]
		return []byte(c), nil
	}
	return nil, fmt.Errorf("unexpected document %.40q", doc)
}

const emptyConn = `{"totalCount":0,"pageInfo":{"hasNextPage":false,"endCursor":null},"nodes":[]}`

func issuePage(total int, commentsTotal int, commentsNext bool, nodes int) string {
	var ns []string
	for n := 1; n <= nodes; n++ {
		ns = append(ns, fmt.Sprintf(`{"id":"I_%d","number":%d,"url":"%s","title":"t","body":"b","state":"OPEN","stateReason":null,
"createdAt":"c","updatedAt":"u","closedAt":null,"locked":false,"activeLockReason":null,"author":{"login":"a"},"authorAssociation":"NONE",
"labels":{"totalCount":0,"nodes":[]},"assignees":{"totalCount":0,"nodes":[]},"milestone":null,
"comments":{"totalCount":%d,"pageInfo":{"hasNextPage":%t,"endCursor":"C1"},"nodes":[{"id":"IC_1","url":"u1","body":"one","createdAt":"c","updatedAt":"u","author":{"login":"a"},"authorAssociation":"NONE"}]},
"timelineItems":%s,"closedByPullRequestsReferences":%s}`, n, n, workfile.IssueURL("o/r", n), commentsTotal, commentsNext, emptyConn, emptyConn))
	}
	return fmt.Sprintf(`{"data":{"rateLimit":{"cost":1,"remaining":4999},"repository":{"issues":{"totalCount":%d,"pageInfo":{"hasNextPage":false,"endCursor":"E"},"nodes":[%s]}}}}`,
		total, strings.Join(ns, ","))
}

// TestFetchFollowsALongConnection: a comment list longer than its first
// page is read to its end, never cut (SPEC-WORK-V1 section 1.3).
func TestFetchFollowsALongConnection(t *testing.T) {
	t.Parallel()
	fk := &fake{issues: issuePage(1, 2, true, 1), comments: []string{
		`{"data":{"rateLimit":{"cost":1,"remaining":4998},"repository":{"issue":{"comments":{"totalCount":2,"pageInfo":{"hasNextPage":false,"endCursor":"C2"},
"nodes":[{"id":"IC_2","url":"u2","body":"two","createdAt":"c","updatedAt":"u","author":null,"authorAssociation":"NONE"}]}}}}}`,
	}}
	f := &Fetcher{Q: fk.q, PageSize: 50, MaxCalls: 10}
	r, err := f.Issues(context.Background(), RepoMeta{Name: "o/r", URL: workfile.Web + "o/r", Issues: 1})
	if err != nil {
		t.Fatal(err)
	}
	cs := r.Issues[0].Comments
	if len(cs) != 2 || cs[0].Body != "one" || cs[1].Body != "two" || cs[1].Author != "" {
		t.Fatalf("comments %+v, want both pages in order, a deleted author as \"\"", cs)
	}
	if f.Calls != 2 || fk.calls[1]["after"] != "C1" || fk.calls[1]["number"] != 1 {
		t.Fatalf("calls=%d follow-up vars %v, want one follow-up after C1 for issue 1", f.Calls, fk.calls)
	}
}

// TestFetchRefusesACountThatDisagrees: a repository or a connection whose
// pages hold fewer than GitHub counts is refused, never half-captured.
func TestFetchRefusesACountThatDisagrees(t *testing.T) {
	t.Parallel()
	for name, page := range map[string]string{
		"issues":   issuePage(3, 1, false, 2),
		"comments": issuePage(1, 5, false, 1),
	} {
		fk := &fake{issues: page}
		f := &Fetcher{Q: fk.q, PageSize: 50, MaxCalls: 10}
		_, err := f.Issues(context.Background(), RepoMeta{Name: "o/r"})
		if err == nil || !strings.Contains(err.Error(), "run again") {
			t.Fatalf("%s: err=%v, want a refusal that says run again", name, err)
		}
	}
}

// TestTheBudgetRefusesTheCallPastIt: the call past --max-calls is refused
// before it is made.
func TestTheBudgetRefusesTheCallPastIt(t *testing.T) {
	t.Parallel()
	fk := &fake{issues: issuePage(1, 2, true, 1)}
	f := &Fetcher{Q: fk.q, PageSize: 50, MaxCalls: 1}
	_, err := f.Issues(context.Background(), RepoMeta{Name: "o/r"})
	if !errors.Is(err, ErrBudget) || len(fk.calls) != 1 {
		t.Fatalf("err=%v calls made=%d, want ErrBudget after exactly one call", err, len(fk.calls))
	}
}

// TestAFailedPageIsAskedAgainSmaller: a page GitHub fails to answer is
// asked again at half the size, and the log says so.
func TestAFailedPageIsAskedAgainSmaller(t *testing.T) {
	t.Parallel()
	var sizes []int
	q := func(_ context.Context, doc string, vars map[string]any) ([]byte, error) {
		n := vars["n"].(int)
		sizes = append(sizes, n)
		if n > 20 {
			return nil, errors.New("HTTP 502: timeout")
		}
		return []byte(issuePage(1, 1, false, 1)), nil
	}
	var log strings.Builder
	f := &Fetcher{Q: q, PageSize: 80, MaxCalls: 10, Log: &log}
	if _, err := f.Issues(context.Background(), RepoMeta{Name: "o/r"}); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(sizes) != "[80 40 20]" || strings.Count(log.String(), "PAGE RETRY") != 2 {
		t.Fatalf("sizes %v log %q, want 80 40 20 with two PAGE RETRY lines", sizes, log.String())
	}
}

// TestRefuseMutation: the seam refuses a document that could write.
func TestRefuseMutation(t *testing.T) {
	t.Parallel()
	for _, doc := range []string{`mutation{closeIssue(input:{issueId:"x"}){clientMutationId}}`, `subscription{x}`, `query{ a } mutation{ b }`, ``} {
		if RefuseMutation(doc) == nil {
			t.Fatalf("%q was not refused", doc)
		}
	}
	if err := RefuseMutation(issuesDoc); err != nil {
		t.Fatalf("the issues document was refused: %v", err)
	}
	if _, err := GhQuery("/nonexistent/gh")(context.Background(), `mutation{x}`, nil); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("GhQuery ran a mutation document: %v", err)
	}
}
