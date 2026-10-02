package workgh

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/workfile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fixture(t *testing.T) Query {
	t.Helper()
	q, err := Replay("testdata/reliable")
	require.NoError(t, err)
	return q
}

// TestFetchReadsTheRecordedRepository: the recorded public repository (two
// pages of 15) reads into every issue with every connection whole, in three
// calls (SPEC-WORK-V1 section 1.3).
func TestFetchReadsTheRecordedRepository(t *testing.T) {
	t.Parallel()
	f := &Fetcher{Q: fixture(t), PageSize: 15, MaxCalls: 10}
	m, err := f.Repo(context.Background(), "mas-bandwidth/reliable")
	require.NoError(t, err)
	r, err := f.Issues(context.Background(), m)
	require.NoError(t, err)

	require.Len(t, r.Issues, m.Issues, "read %d issues, the listing counts %d, the recording holds 20", len(r.Issues), m.Issues)
	require.Equal(t, 20, m.Issues, "read %d issues, the listing counts %d, the recording holds 20", len(r.Issues), m.Issues)
	require.Equal(t, 3, f.Calls, "calls=%d points=%d, want 3 calls and the points GitHub charged", f.Calls, f.Points)
	require.Greater(t, f.Points, 0, "calls=%d points=%d, want 3 calls and the points GitHub charged", f.Calls, f.Points)

	comments, refs := 0, 0
	for i, is := range r.Issues {
		if i > 0 {
			require.Greater(t, is.Number, r.Issues[i-1].Number, "issues out of order at %d", is.Number)
		}
		require.Equal(t, workfile.IssueURL("mas-bandwidth/reliable", is.Number), is.URL, "issue %d lacks its identity: %+v", is.Number, is)
		require.NotEmpty(t, is.NodeID, "issue %d lacks its identity: %+v", is.Number, is)
		require.NotEmpty(t, is.Created, "issue %d lacks its identity: %+v", is.Number, is)
		require.Contains(t, []string{"internal", "external"}, is.Origin, "issue %d origin %q", is.Number, is.Origin)
		comments += len(is.Comments)
		refs += len(is.References)
	}
	require.NotZero(t, comments, "comments=%d references=%d: the recording holds both", comments, refs)
	require.NotZero(t, refs, "comments=%d references=%d: the recording holds both", comments, refs)
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
	require.NoError(t, err)

	cs := r.Issues[0].Comments
	require.Len(t, cs, 2, "comments %+v, want both pages in order, a deleted author as \"\"", cs)
	require.Equal(t, "one", cs[0].Body, "comments %+v, want both pages in order, a deleted author as \"\"", cs)
	require.Equal(t, "two", cs[1].Body, "comments %+v, want both pages in order, a deleted author as \"\"", cs)
	require.Empty(t, cs[1].Author, "comments %+v, want both pages in order, a deleted author as \"\"", cs)

	require.Equal(t, 2, f.Calls, "calls=%d follow-up vars %v, want one follow-up after C1 for issue 1", f.Calls, fk.calls)
	require.Equal(t, "C1", fk.calls[1]["after"], "calls=%d follow-up vars %v, want one follow-up after C1 for issue 1", f.Calls, fk.calls)
	require.Equal(t, 1, fk.calls[1]["number"], "calls=%d follow-up vars %v, want one follow-up after C1 for issue 1", f.Calls, fk.calls)
}

// TestFetchRefusesACountThatDisagrees: a repository or a connection whose
// pages hold fewer than GitHub counts is refused, never half-captured.
func TestFetchRefusesACountThatDisagrees(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		page string
	}{
		{name: "issues", page: issuePage(3, 1, false, 2)},
		{name: "comments", page: issuePage(1, 5, false, 1)},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fk := &fake{issues: tc.page}
			f := &Fetcher{Q: fk.q, PageSize: 50, MaxCalls: 10}
			_, err := f.Issues(context.Background(), RepoMeta{Name: "o/r"})
			assert.ErrorContains(t, err, "run again", "%s: err=%v, want a refusal that says run again", tc.name, err)
		})
	}
}

// TestTheBudgetRefusesTheCallPastIt: the call past --max-calls is refused
// before it is made.
func TestTheBudgetRefusesTheCallPastIt(t *testing.T) {
	t.Parallel()
	fk := &fake{issues: issuePage(1, 2, true, 1)}
	f := &Fetcher{Q: fk.q, PageSize: 50, MaxCalls: 1}
	_, err := f.Issues(context.Background(), RepoMeta{Name: "o/r"})
	require.ErrorIs(t, err, ErrBudget, "err=%v calls made=%d, want ErrBudget after exactly one call", err, len(fk.calls))
	require.Len(t, fk.calls, 1, "err=%v calls made=%d, want ErrBudget after exactly one call", err, len(fk.calls))
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
	_, err := f.Issues(context.Background(), RepoMeta{Name: "o/r"})
	require.NoError(t, err)
	require.Equal(t, []int{80, 40, 20}, sizes, "sizes %v log %q, want 80 40 20 with two PAGE RETRY lines", sizes, log.String())
	require.Equal(t, 2, strings.Count(log.String(), "PAGE RETRY"), "sizes %v log %q, want 80 40 20 with two PAGE RETRY lines", sizes, log.String())
}

// TestRefuseMutation: the seam refuses a document that could write.
func TestRefuseMutation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		doc  string
	}{
		{name: "closeIssue mutation", doc: `mutation{closeIssue(input:{issueId:"x"}){clientMutationId}}`},
		{name: "subscription", doc: `subscription{x}`},
		{name: "query and mutation", doc: `query{ a } mutation{ b }`},
		{name: "empty document", doc: ``},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Error(t, RefuseMutation(tc.doc), "%q was not refused", tc.doc)
		})
	}
	t.Run("issues document allowed", func(t *testing.T) {
		t.Parallel()
		err := RefuseMutation(issuesDoc)
		require.NoError(t, err, "the issues document was refused: %v", err)
	})
	t.Run("GhQuery refuses mutation", func(t *testing.T) {
		t.Parallel()
		_, err := GhQuery("/nonexistent/gh")(context.Background(), `mutation{x}`, nil)
		require.ErrorContains(t, err, "refused", "GhQuery ran a mutation document: %v", err)
	})
}

// TestANullSourceIsKeptAsNoSource: a cross-reference whose source GitHub
// does not show this login is kept as a reference with no source (kind "",
// number 0), and the tree file writes and reads it back.
func TestANullSourceIsKeptAsNoSource(t *testing.T) {
	t.Parallel()
	page := strings.Replace(issuePage(1, 1, false, 1), `"timelineItems":`+emptyConn,
		`"timelineItems":{"totalCount":1,"pageInfo":{"hasNextPage":false,"endCursor":"T1"},"nodes":[{"createdAt":"t","willCloseTarget":false,"actor":null,"source":null}]}`, 1)
	fk := &fake{issues: page}
	f := &Fetcher{Q: fk.q, PageSize: 50, MaxCalls: 10}
	r, err := f.Issues(context.Background(), RepoMeta{Name: "o/r", URL: workfile.Web + "o/r"})
	require.NoError(t, err)

	refs := r.Issues[0].References
	require.Len(t, refs, 1, "references %+v, want one with no source", refs)
	require.Empty(t, refs[0].Kind, "references %+v, want one with no source", refs)
	require.Equal(t, 0, refs[0].Number, "references %+v, want one with no source", refs)
	require.Equal(t, "t", refs[0].At, "references %+v, want one with no source", refs)

	tree := &workfile.Tree{Source: "github", Org: "o", Repos: []workfile.Repo{r}}
	data, err := workfile.Encode(tree)
	require.NoError(t, err)
	back, err := workfile.Decode("t", data, workfile.Limits(len(data)))
	require.NoError(t, err)
	d := workfile.Diff(back, tree, nil)
	require.Empty(t, d, "differences after the round trip: %+v", d)
}
