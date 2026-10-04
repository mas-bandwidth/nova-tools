package workgh

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// repoJSON is one repository node in a repositories listing reply.
func repoJSON(name, url string, archived bool, issues int) string {
	return fmt.Sprintf(`{"nameWithOwner":%q,"url":%q,"isArchived":%t,"issues":{"totalCount":%d}}`, name, url, archived, issues)
}

// orgPage is one page of an organization's repository listing: the page's total
// count, whether another page follows, the EndCursor, and the nodes.
func orgPage(total int, hasNext bool, cursor string, nodes []string) string {
	return fmt.Sprintf(`{"data":{"rateLimit":{"cost":1,"remaining":4999,"resetAt":"r"},"organization":{"repositories":{"totalCount":%d,"pageInfo":{"hasNextPage":%t,"endCursor":%q},"nodes":[%s]}}}}`,
		total, hasNext, cursor, strings.Join(nodes, ","))
}

// noOrgPage is a reply in which no organization is visible to this login.
const noOrgPage = `{"data":{"rateLimit":{"cost":1,"remaining":4999,"resetAt":"r"},"organization":null}}`

// repoReply is one canned reply of the repos fake: a body or an error.
type repoReply struct {
	body []byte
	err  error
}

// reposFake is a Query that returns its reply bodies in order, recording the
// variables of each call. It is the seam for Repos tests; it reaches no network.
type reposFake struct {
	replies []repoReply
	calls   int
	vars    []map[string]any
}

func (f *reposFake) q(_ context.Context, _ string, vars map[string]any) ([]byte, error) {
	idx := f.calls
	f.calls++
	f.vars = append(f.vars, vars)
	if idx >= len(f.replies) {
		return nil, fmt.Errorf("reposFake: call %d past %d replies", idx+1, len(f.replies))
	}
	r := f.replies[idx]
	return r.body, r.err
}

// TestFetchCoverRepos: the listing is paginated to its end by EndCursor, every
// repository's fields are kept, and the result is sorted by name
// (SPEC-WORK-V1 section 1.3).
func TestFetchCoverRepos(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		org        string
		replies    []repoReply
		want       []RepoMeta // expected, sorted, on success
		wantPoints int        // GraphQL points GitHub charged
		wantCalls  int
		nextVar    string // expected "after" on the second call, "" if single-page
	}{
		{
			name: "single page sorted by name",
			org:  "o",
			replies: []repoReply{{body: []byte(orgPage(2, false, "", []string{
				repoJSON("o/beta", "https://example.com/o/beta", false, 2),
				repoJSON("o/alpha", "https://example.com/o/alpha", true, 5),
			}))}},
			want: []RepoMeta{
				{Name: "o/alpha", URL: "https://example.com/o/alpha", Archived: true, Issues: 5},
				{Name: "o/beta", URL: "https://example.com/o/beta", Archived: false, Issues: 2},
			},
			wantPoints: 1,
			wantCalls:  1,
		},
		{
			name: "two pages followed by cursor and sorted",
			org:  "o",
			replies: []repoReply{
				{body: []byte(orgPage(3, true, "C1", []string{
					repoJSON("o/beta", "u", false, 1),
					repoJSON("o/alpha", "u", true, 3),
				}))},
				{body: []byte(orgPage(3, false, "", []string{
					repoJSON("o/gamma", "u", false, 0),
				}))},
			},
			want: []RepoMeta{
				{Name: "o/alpha", URL: "u", Archived: true, Issues: 3},
				{Name: "o/beta", URL: "u", Archived: false, Issues: 1},
				{Name: "o/gamma", URL: "u", Archived: false, Issues: 0},
			},
			wantPoints: 2,
			wantCalls:  2,
			nextVar:    "C1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			q := &reposFake{replies: tc.replies}
			f := &Fetcher{Q: q.q, PageSize: 50, MaxCalls: 10}
			got, err := f.Repos(context.Background(), tc.org)
			require.NoError(t, err, "got %v", got)
			require.Equal(t, tc.want, got, "names=%v", names(got))
			require.Equal(t, tc.wantPoints, f.Points, "points=%d calls=%d", f.Points, q.calls)
			require.Equal(t, tc.wantCalls, q.calls, "calls=%d", q.calls)
			require.Equal(t, tc.org, q.vars[0]["org"], "first vars=%v", q.vars[0])
			if tc.nextVar != "" {
				require.Len(t, q.vars, 2, "vars=%v", q.vars)
				require.Equal(t, tc.nextVar, q.vars[1]["after"], "second vars=%v", q.vars[1])
			}
		})
	}
}

func names(ms []RepoMeta) []string {
	ns := make([]string, len(ms))
	for i, m := range ms {
		ns[i] = m.Name
	}
	return ns
}

// TestFetchCoverReposRefusals: Repos refuses each named fault and returns no
// result, so a caller never sees half a listing.
func TestFetchCoverReposRefusals(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		org       string
		maxCalls  int
		replies   []repoReply
		wantErr   string
		wantIs    error
		wantCalls int
	}{
		{
			name:     "budget refused on the next page",
			org:      "o",
			maxCalls: 1,
			replies: []repoReply{{body: []byte(orgPage(2, true, "C1", []string{
				repoJSON("o/alpha", "u", false, 1),
				repoJSON("o/beta", "u", false, 1),
			}))}},
			wantErr:   "budget",
			wantIs:    ErrBudget,
			wantCalls: 1,
		},
		{
			name:      "no organization visible",
			org:       "ghost",
			maxCalls:  10,
			replies:   []repoReply{{body: []byte(noOrgPage)}},
			wantErr:   "no organization",
			wantCalls: 1,
		},
		{
			name:     "count disagrees with the pages",
			org:      "o",
			maxCalls: 10,
			replies: []repoReply{{body: []byte(orgPage(3, false, "", []string{
				repoJSON("o/only", "u", false, 1),
			}))}},
			wantErr:   "run again",
			wantCalls: 1,
		},
		{
			name:      "query error",
			org:       "o",
			maxCalls:  10,
			replies:   []repoReply{{err: errors.New("HTTP 502")}},
			wantErr:   "HTTP 502",
			wantCalls: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			q := &reposFake{replies: tc.replies}
			f := &Fetcher{Q: q.q, PageSize: 50, MaxCalls: tc.maxCalls}
			got, err := f.Repos(context.Background(), tc.org)
			require.Error(t, err, "got %v, want an error", got)
			if tc.wantIs != nil {
				require.ErrorIs(t, err, tc.wantIs)
			}
			assert.ErrorContains(t, err, tc.wantErr, "err=%v", err)
			require.Empty(t, got, "got %v, want no result on error", got)
			require.Equal(t, tc.wantCalls, q.calls, "calls=%d want=%d", q.calls, tc.wantCalls)
		})
	}
}
