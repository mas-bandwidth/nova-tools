package workfile_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/workfile"
	"github.com/mas-bandwidth/nova-tools/internal/workgh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// hard is a tree whose strings hold everything the file must survive:
// quotes, backslashes, newlines, carriage returns, a dispatch macro, a
// comment marker, parentheses, colons and non-ASCII.
func hard() *workfile.Tree {
	body := "line \"one\"\\ \r\n#.(evil) ; not a comment\n:not-a-key (a) 12abc ü 🐛\t"
	return &workfile.Tree{Source: "github", Org: "o", Fetched: "2026-01-01T00:00:00Z", Repos: []workfile.Repo{
		{Name: "o/a", URL: workfile.Web + "o/a", Archived: true},
		{Name: "o/b", URL: workfile.Web + "o/b", Issues: []workfile.Issue{
			{Number: 1, URL: workfile.Web + "o/b/issues/1", NodeID: "I_1", Title: body, State: "CLOSED", StateReason: "NOT_PLANNED",
				Origin: "external", Author: "", AuthorAssociation: "FIRST_TIME_CONTRIBUTOR", Created: "c", Updated: "u", Closed: "x",
				Locked: true, LockReason: "TOO_HEATED", Labels: []string{"a b", "z\"q"}, Assignees: []string{"p"},
				Milestone: &workfile.Milestone{Number: 3, Title: "m\\"}, Body: body,
				Comments:   []workfile.Comment{{ID: "IC_1", URL: "u", Author: "p", AuthorAssociation: "MEMBER", Created: "c", Updated: "u", Body: body}, {ID: "IC_0", Body: ""}},
				References: []workfile.Reference{{Kind: "PullRequest", Repo: "o/a", Number: 9, URL: "u", Actor: "p", At: "t", WillClose: true}, {At: "t"}},
				LinkedPRs:  []workfile.LinkedPR{{Repo: "o/a", Number: 9, URL: "u", State: "MERGED"}}},
			{Number: 4, URL: workfile.Web + "o/b/issues/4", Title: "", State: "OPEN", Origin: "internal", AuthorAssociation: "OWNER"},
		}},
	}}
}

func recorded(t *testing.T) *workfile.Tree {
	t.Helper()
	q, err := workgh.Replay("../workgh/testdata/reliable")
	require.NoError(t, err)
	f := &workgh.Fetcher{Q: q, PageSize: 15, MaxCalls: 10}
	m, err := f.Repo(context.Background(), "mas-bandwidth/reliable")
	require.NoError(t, err)
	r, err := f.Issues(context.Background(), m)
	require.NoError(t, err)
	return &workfile.Tree{Source: "github", Org: "mas-bandwidth", Fetched: "2026-01-01T00:00:00Z", Repos: []workfile.Repo{r}}
}

// TestEncodeDecodeIsTheIdentity (SPEC-WORK-V1 section 1.2): encoded and read
// back, a tree equals itself field for field, and encodes to the same bytes.
func TestEncodeDecodeIsTheIdentity(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		tree func(t *testing.T) *workfile.Tree
	}{
		{name: "hard", tree: func(t *testing.T) *workfile.Tree { return hard() }},
		{name: "recorded", tree: recorded},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tree := tc.tree(t)
			data, err := workfile.Encode(tree)
			require.NoError(t, err, "%s: %v", tc.name, err)
			back, err := workfile.Decode(tc.name, data, workfile.Limits(len(data)))
			require.NoError(t, err, "%s: %v", tc.name, err)
			d := workfile.Diff(back, tree, nil)
			var first any
			if len(d) > 0 {
				first = d[0]
			}
			require.Empty(t, d, "%s: %d differences after the round trip, first %+v", tc.name, len(d), first)
			again, err := workfile.Encode(back)
			require.NoError(t, err, "%s: the second encoding differs (%v)", tc.name, err)
			assert.True(t, bytes.Equal(again, data), "%s: the second encoding differs (%v)", tc.name, err)
			assert.Equal(t, tree.Count(), back.Count(), "%s: counts %+v, want %+v", tc.name, back.Count(), tree.Count())
		})
	}
}

// TestTheReaderRefusesWhatTheWriterWouldNotWrite: the strict reader refuses
// the whole file, naming the path and key.
func TestTheReaderRefusesWhatTheWriterWouldNotWrite(t *testing.T) {
	t.Parallel()
	data, err := workfile.Encode(hard())
	require.NoError(t, err)
	s := string(data)
	cases := []struct {
		name, from, to, want string
	}{
		{"unknown key", `:node-id "I_1"`, `:node-id "I_1" :colour "red"`, "unknown key :colour"},
		{"repeated key", `:node-id "I_1"`, `:node-id "I_1" :node-id "I_2"`, ":node-id twice"},
		{"missing key", `:node-id "I_1"`, ``, "has no :node-id"},
		{"url not path", "\"" + workfile.Web + "o/b/issues/1\"", "\"" + workfile.Web + "o/b/issues/2\"", "is not the URL its path gives"},
		{"out of order", "(issue 4\n      :url \"" + workfile.Web + "o/b/issues/4\"", "(issue 1\n      :url \"" + workfile.Web + "o/b/issues/1\"", "out of order or repeated"},
		{"evaluating", `:locked true`, `:locked #.true`, "byte="},
		{"bad enum", `:state :open`, `:state :Open`, "outside [a-z-]"},
		{"wrong kind", `:author ""`, `:author 5`, ":author wants a string"},
		{"format", `(work-tree "v1"`, `(work-tree "v2"`, `format "v2"`},
		{"repos order", `(repo "o/a"`, `(repo "o/c"`, "out of order or repeated"},
		{"origin", `:origin :internal`, `:origin :elsewhere`, ":origin wants"},
		{"boolean", `:archived true`, `:archived yes`, ":archived wants true or false"},
		{"milestone key", `(:number 3 :title`, `(:number 3 :name`, "unknown key :name"},
		{"repeated comment", `(comment "IC_0"`, `(comment "IC_1"`, "the comment id is repeated"},
		{"ref number 0", `:kind "PullRequest" :repo "o/a" :number 9`, `:kind "PullRequest" :repo "o/a" :number 0`, ":number wants a positive integer"},
		{"no-source number", `:kind "" :repo "" :number 0`, `:kind "" :repo "" :number 3`, "a reference with no source wants :number 0"},
		{"unsorted", `("a b" "z\"q")`, `("z\"q" "a b")`, "must be sorted"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Contains(t, s, tc.from, "the fixture lacks %q", tc.from)
			bad := strings.Replace(s, tc.from, tc.to, 1)
			_, err := workfile.Decode("t.lisp", []byte(bad), workfile.Limits(len(bad)))
			assert.ErrorContains(t, err, tc.want, "err=%v, want it to say %q", err, tc.want)
		})
	}

	t.Run("evaluating tree description", func(t *testing.T) {
		t.Parallel()
		evil := strings.Replace(s, `:locked true`, `:locked #.true`, 1)
		_, err := workfile.Decode("t.lisp", []byte(evil), workfile.Limits(len(evil)))
		require.Error(t, err, "a refused tree is described as %v", err)
		require.NotContains(t, err.Error(), "plan", "a refused tree is described as %v", err)
		require.Contains(t, err.Error(), "tree file=t.lisp", "a refused tree is described as %v", err)
		require.Contains(t, err.Error(), "a tree is data", "a refused tree is described as %v", err)
	})

	t.Run("byte bound", func(t *testing.T) {
		t.Parallel()
		_, err := workfile.Decode("t.lisp", data, workfile.Limits(len(data)-1))
		require.ErrorContains(t, err, "--max-bytes", "a file past the byte bound was read: %v", err)
	})
}

// TestEncodeRefusesALossyValue: a value the file could not give back
// unchanged is refused, never written.
func TestEncodeRefusesALossyValue(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		mut  func(*workfile.Tree)
	}{
		{"enum", func(t *workfile.Tree) { t.Repos[1].Issues[0].State = "open" }},
		{"enum digit", func(t *workfile.Tree) { t.Repos[1].Issues[0].StateReason = "R2" }},
		{"origin", func(t *workfile.Tree) { t.Repos[1].Issues[0].Origin = "" }},
		{"unsorted", func(t *workfile.Tree) { t.Repos[1].Issues[0].Labels = []string{"z", "a"} }},
		{"issue order", func(t *workfile.Tree) { t.Repos[1].Issues[1].Number = 1 }},
		{"repo order", func(t *workfile.Tree) { t.Repos[0].Name = "o/c" }},
		{"repo name", func(t *workfile.Tree) { t.Repos[0].Name = "a" }},
		{"number", func(t *workfile.Tree) { t.Repos[1].Issues[0].Number = 0 }},
		{"repeated comment id", func(t *workfile.Tree) {
			t.Repos[1].Issues[0].Comments = append(t.Repos[1].Issues[0].Comments, workfile.Comment{ID: "IC_1"})
		}},
		{"ref kind with no number", func(t *workfile.Tree) { t.Repos[1].Issues[0].References[0].Number = 0 }},
		{"ref number with no kind", func(t *workfile.Tree) { t.Repos[1].Issues[0].References[1].Number = 5 }},
		{"ref no kind but a url", func(t *workfile.Tree) { t.Repos[1].Issues[0].References[1].URL = "u" }},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tr := hard()
			tc.mut(tr)
			_, err := workfile.Encode(tr)
			assert.Error(t, err, "%s: encoded", tc.name)
		})
	}
}

// TestDiffNamesEveryKind (SPEC-WORK-V1 section 1.6): each change is one
// line at its path, of the kind that names it.
func TestDiffNamesEveryKind(t *testing.T) {
	t.Parallel()
	want := hard()
	got := hard()
	got.Repos[1].Issues = got.Repos[1].Issues[:1]                                            // issue 4 MISSING
	got.Repos[1].Issues = append(got.Repos[1].Issues, workfile.Issue{Number: 9, Title: "x"}) // issue 9 EXTRA
	is := &got.Repos[1].Issues[0]
	is.Body = "changed"
	is.Labels = []string{"a b"}
	is.Comments[0].Body = "edited"
	is.Comments = append(is.Comments[:1], workfile.Comment{ID: "IC_7"})
	is.References = is.References[:1]
	is.LinkedPRs[0].State = "OPEN"
	got.Repos = append(got.Repos, workfile.Repo{Name: "o/z"})
	lines := map[string]bool{}
	for _, d := range workfile.Diff(got, want, nil) {
		lines[d.Kind+" "+d.Path+" "+d.Field] = true
	}
	for _, l := range []string{
		"MISSING repos/o/b/issues/4 issue",
		"EXTRA repos/o/b/issues/9 issue",
		"DRIFT repos/o/b/issues/1 body",
		"DRIFT repos/o/b/issues/1 labels",
		"DRIFT repos/o/b/issues/1/comments/IC_1 body",
		"MISSING repos/o/b/issues/1/comments/IC_0 comment",
		"EXTRA repos/o/b/issues/1/comments/IC_7 comment",
		"MISSING repos/o/b/issues/1/references reference",
		"MISSING repos/o/b/issues/1/linked-prs linked-pr",
		"EXTRA repos/o/b/issues/1/linked-prs linked-pr",
		"EXTRA repos/o/z repo",
	} {
		assert.True(t, lines[l], "no %q among %v", l, lines)
	}
	d := workfile.Diff(got, want, []string{"o/a"})
	require.Empty(t, d, "a scope of o/a still reported %v", d)
	s := workfile.Show(strings.Repeat("x", 81))
	require.True(t, strings.HasPrefix(s, "bytes:81:sha256:"), "a long value shows as %q", s)
}

// TestDiffSeesRepeatsAndOrder: a comment injected under an existing id, a
// swapped pair of references or linked pull requests, and a label list that
// joins to the same text are each found; nothing is folded into a set.
func TestDiffSeesRepeatsAndOrder(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		mut  func(want *workfile.Tree, is *workfile.Issue)
		want []string
	}{
		{
			name: "injected same-id comment",
			mut: func(want *workfile.Tree, is *workfile.Issue) {
				is.Comments = append(is.Comments, workfile.Comment{ID: "IC_1", Body: "injected"})
			},
			want: []string{"EXTRA repos/o/b/issues/1/comments/IC_1 comment", "DRIFT repos/o/b/issues/1 comments-order"},
		},
		{
			name: "duplicated comment",
			mut: func(want *workfile.Tree, is *workfile.Issue) {
				is.Comments = append(is.Comments, is.Comments[0])
			},
			want: []string{"EXTRA repos/o/b/issues/1/comments/IC_1 comment", "DRIFT repos/o/b/issues/1 comments-order"},
		},
		{
			name: "swapped comments",
			mut: func(want *workfile.Tree, is *workfile.Issue) {
				is.Comments[0], is.Comments[1] = is.Comments[1], is.Comments[0]
			},
			want: []string{"DRIFT repos/o/b/issues/1 comments-order"},
		},
		{
			name: "swapped references",
			mut: func(want *workfile.Tree, is *workfile.Issue) {
				is.References[0], is.References[1] = is.References[1], is.References[0]
			},
			want: []string{"DRIFT repos/o/b/issues/1 references-order"},
		},
		{
			name: "swapped linked prs",
			mut: func(want *workfile.Tree, is *workfile.Issue) {
				is.LinkedPRs = append(is.LinkedPRs, workfile.LinkedPR{Repo: "o/a", Number: 10, URL: "v", State: "OPEN"})
				want.Repos[1].Issues[0].LinkedPRs = []workfile.LinkedPR{is.LinkedPRs[1], is.LinkedPRs[0]}
			},
			want: []string{"DRIFT repos/o/b/issues/1 linked-prs-order"},
		},
		{
			name: "labels joined alike",
			mut: func(want *workfile.Tree, is *workfile.Issue) {
				is.Labels = []string{"a b,z\"q"}
				want.Repos[1].Issues[0].Labels = []string{"a b", "z\"q"}
			},
			want: []string{"DRIFT repos/o/b/issues/1 labels"},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			localWant := hard()
			got := hard()
			tc.mut(localWant, &got.Repos[1].Issues[0])
			m := map[string]bool{}
			for _, d := range workfile.Diff(got, localWant, nil) {
				m[d.Kind+" "+d.Path+" "+d.Field] = true
			}
			for _, w := range tc.want {
				assert.True(t, m[w], "%s: no %q among %v", tc.name, w, m)
			}
			assert.Len(t, m, len(tc.want), "%s: lines %v, want exactly %v", tc.name, m, tc.want)
		})
	}
}

// TestPathAndURLAreOneLookupEachWay (SPEC-WORK-V1 section 1.5).
func TestPathAndURLAreOneLookupEachWay(t *testing.T) {
	t.Parallel()
	p, err := workfile.PathOfURL(workfile.Web + "o/r/issues/12")
	require.NoError(t, err, "path %q %v", p, err)
	require.Equal(t, "repos/o/r/issues/12", p, "path %q %v", p, err)

	u, err := workfile.URLOfPath(p)
	require.NoError(t, err, "url %q %v", u, err)
	require.Equal(t, workfile.Web+"o/r/issues/12", u, "url %q %v", u, err)

	t.Run("bad URLs", func(t *testing.T) {
		t.Parallel()
		for _, bad := range []string{
			workfile.Web + "o/r/pull/12",
			workfile.Web + "o/r/issues/012",
			"https://example.com/o/r/issues/1",
			workfile.Web + "o/r/issues/0",
		} {
			_, err := workfile.PathOfURL(bad)
			assert.Error(t, err, "%q was taken for an issue URL", bad)
		}
	})

	t.Run("bad paths", func(t *testing.T) {
		t.Parallel()
		for _, bad := range []string{
			"repos/o/issues/1",
			"repos/o/r/pulls/1",
			"repos/o/r/issues/x",
		} {
			_, err := workfile.URLOfPath(bad)
			assert.Error(t, err, "%q was taken for an issue path", bad)
		}
	})

	t.Run("origin", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, "internal", workfile.OriginOf("COLLABORATOR"), "origin")
		require.Equal(t, "external", workfile.OriginOf("CONTRIBUTOR"), "origin")
		require.Equal(t, "external", workfile.OriginOf(""), "origin")
	})
}

type probeT struct {
	failed  bool
	message string
}

var _ require.TestingT = (*probeT)(nil)

func (p *probeT) Errorf(format string, args ...interface{}) {
	p.failed = true
	p.message = fmt.Sprintf(format, args...)
}

func (p *probeT) FailNow() {
	p.failed = true
}

func (p *probeT) Helper() {}

// TestPathOfURLRetainedCheckFailsOnNonNilError proves that returning the expected
// string plus a non-nil error passes require.Equal alone, but fails require.NoError
// with the complete "path %q %v" diagnostic.
func TestPathOfURLRetainedCheckFailsOnNonNilError(t *testing.T) {
	t.Parallel()
	mock := &probeT{}
	p := "repos/o/r/issues/12"
	err := errors.New("synthetic error")

	// Equal alone passes when p matches, missing the non-nil error entirely.
	require.Equal(mock, "repos/o/r/issues/12", p, "path %q %v", p, err)
	require.False(t, mock.failed, "require.Equal alone must not fail on matching string")

	// The retained require.NoError fails on non-nil error and emits the diagnostic.
	require.NoError(mock, err, "path %q %v", p, err)
	require.True(t, mock.failed, "require.NoError must fail on non-nil error")
	require.Contains(t, mock.message, fmt.Sprintf("path %q %v", p, err))

	// URLOfPath retained check similarly fails on non-nil error:
	mockURL := &probeT{}
	u := workfile.Web + "o/r/issues/12"
	require.Equal(mockURL, workfile.Web+"o/r/issues/12", u, "url %q %v", u, err)
	require.False(t, mockURL.failed, "require.Equal alone must not fail on matching string")
	require.NoError(mockURL, err, "url %q %v", u, err)
	require.True(t, mockURL.failed, "require.NoError must fail on non-nil error")
	require.Contains(t, mockURL.message, fmt.Sprintf("url %q %v", u, err))
}
