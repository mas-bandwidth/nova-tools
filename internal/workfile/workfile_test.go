package workfile_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/workfile"
	"github.com/mas-bandwidth/nova-tools/internal/workgh"
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
				References: []workfile.Reference{{Kind: "PullRequest", Repo: "o/a", Number: 9, URL: "u", Actor: "p", At: "t", WillClose: true}, {Kind: "", Repo: "", Number: 0 + 1, URL: "", At: "t"}},
				LinkedPRs:  []workfile.LinkedPR{{Repo: "o/a", Number: 9, URL: "u", State: "MERGED"}}},
			{Number: 4, URL: workfile.Web + "o/b/issues/4", Title: "", State: "OPEN", Origin: "internal", AuthorAssociation: "OWNER"},
		}},
	}}
}

func recorded(t *testing.T) *workfile.Tree {
	t.Helper()
	q, err := workgh.Replay("../workgh/testdata/reliable")
	if err != nil {
		t.Fatal(err)
	}
	f := &workgh.Fetcher{Q: q, PageSize: 15, MaxCalls: 10}
	m, err := f.Repo(context.Background(), "mas-bandwidth/reliable")
	if err != nil {
		t.Fatal(err)
	}
	r, err := f.Issues(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	return &workfile.Tree{Source: "github", Org: "mas-bandwidth", Fetched: "2026-01-01T00:00:00Z", Repos: []workfile.Repo{r}}
}

// TestEncodeDecodeIsTheIdentity (SPEC-WORK-V1 section 1.2): encoded and read
// back, a tree equals itself field for field, and encodes to the same bytes.
func TestEncodeDecodeIsTheIdentity(t *testing.T) {
	t.Parallel()
	for name, tree := range map[string]*workfile.Tree{"hard": hard(), "recorded": recorded(t)} {
		data, err := workfile.Encode(tree)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		back, err := workfile.Decode(name, data, workfile.Limits(len(data)))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if d := workfile.Diff(back, tree, nil); len(d) != 0 {
			t.Fatalf("%s: %d differences after the round trip, first %+v", name, len(d), d[0])
		}
		again, err := workfile.Encode(back)
		if err != nil || !bytes.Equal(again, data) {
			t.Fatalf("%s: the second encoding differs (%v)", name, err)
		}
		if back.Count() != tree.Count() {
			t.Fatalf("%s: counts %+v, want %+v", name, back.Count(), tree.Count())
		}
	}
}

// TestTheReaderRefusesWhatTheWriterWouldNotWrite: the strict reader refuses
// the whole file, naming the path and key.
func TestTheReaderRefusesWhatTheWriterWouldNotWrite(t *testing.T) {
	t.Parallel()
	data, err := workfile.Encode(hard())
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for name, c := range map[string]struct{ from, to, want string }{
		"unknown key":   {`:node-id "I_1"`, `:node-id "I_1" :colour "red"`, "unknown key :colour"},
		"repeated key":  {`:node-id "I_1"`, `:node-id "I_1" :node-id "I_2"`, ":node-id twice"},
		"missing key":   {`:node-id "I_1"`, ``, "has no :node-id"},
		"url not path":  {"\"" + workfile.Web + "o/b/issues/1\"", "\"" + workfile.Web + "o/b/issues/2\"", "is not the URL its path gives"},
		"out of order":  {"(issue 4\n      :url \"" + workfile.Web + "o/b/issues/4\"", "(issue 1\n      :url \"" + workfile.Web + "o/b/issues/1\"", "out of order or repeated"},
		"evaluating":    {`:locked true`, `:locked #.true`, "byte="},
		"bad enum":      {`:state :open`, `:state :Open`, "outside [a-z-]"},
		"wrong kind":    {`:author ""`, `:author 5`, ":author wants a string"},
		"format":        {`(work-tree "v1"`, `(work-tree "v2"`, `format "v2"`},
		"repos order":   {`(repo "o/a"`, `(repo "o/c"`, "out of order or repeated"},
		"origin":        {`:origin :internal`, `:origin :elsewhere`, ":origin wants"},
		"boolean":       {`:archived true`, `:archived yes`, ":archived wants true or false"},
		"milestone key": {`(:number 3 :title`, `(:number 3 :name`, "unknown key :name"},
		"unsorted":      {`("a b" "z\"q")`, `("z\"q" "a b")`, "must be sorted"},
	} {
		if !strings.Contains(s, c.from) {
			t.Fatalf("%s: the fixture lacks %q", name, c.from)
		}
		bad := strings.Replace(s, c.from, c.to, 1)
		_, err := workfile.Decode("t.lisp", []byte(bad), workfile.Limits(len(bad)))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: err=%v, want it to say %q", name, err, c.want)
		}
	}
	if _, err := workfile.Decode("t.lisp", data, workfile.Limits(len(data)-1)); err == nil || !strings.Contains(err.Error(), "--max-bytes") {
		t.Fatalf("a file past the byte bound was read: %v", err)
	}
}

// TestEncodeRefusesALossyValue: a value the file could not give back
// unchanged is refused, never written.
func TestEncodeRefusesALossyValue(t *testing.T) {
	t.Parallel()
	for name, mut := range map[string]func(*workfile.Tree){
		"enum":        func(t *workfile.Tree) { t.Repos[1].Issues[0].State = "open" },
		"enum digit":  func(t *workfile.Tree) { t.Repos[1].Issues[0].StateReason = "R2" },
		"origin":      func(t *workfile.Tree) { t.Repos[1].Issues[0].Origin = "" },
		"unsorted":    func(t *workfile.Tree) { t.Repos[1].Issues[0].Labels = []string{"z", "a"} },
		"issue order": func(t *workfile.Tree) { t.Repos[1].Issues[1].Number = 1 },
		"repo order":  func(t *workfile.Tree) { t.Repos[0].Name = "o/c" },
		"repo name":   func(t *workfile.Tree) { t.Repos[0].Name = "a" },
		"number":      func(t *workfile.Tree) { t.Repos[1].Issues[0].Number = 0 },
	} {
		tr := hard()
		mut(tr)
		if _, err := workfile.Encode(tr); err == nil {
			t.Fatalf("%s: encoded", name)
		}
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
		if !lines[l] {
			t.Errorf("no %q among %v", l, lines)
		}
	}
	if d := workfile.Diff(got, want, []string{"o/a"}); len(d) != 0 {
		t.Fatalf("a scope of o/a still reported %v", d)
	}
	if s := workfile.Show(strings.Repeat("x", 81)); !strings.HasPrefix(s, "bytes:81:sha256:") {
		t.Fatalf("a long value shows as %q", s)
	}
}

// TestPathAndURLAreOneLookupEachWay (SPEC-WORK-V1 section 1.5).
func TestPathAndURLAreOneLookupEachWay(t *testing.T) {
	t.Parallel()
	p, err := workfile.PathOfURL(workfile.Web + "o/r/issues/12")
	if err != nil || p != "repos/o/r/issues/12" {
		t.Fatalf("path %q %v", p, err)
	}
	u, err := workfile.URLOfPath(p)
	if err != nil || u != workfile.Web+"o/r/issues/12" {
		t.Fatalf("url %q %v", u, err)
	}
	for _, bad := range []string{workfile.Web + "o/r/pull/12", workfile.Web + "o/r/issues/012", "https://example.com/o/r/issues/1", workfile.Web + "o/r/issues/0"} {
		if _, err := workfile.PathOfURL(bad); err == nil {
			t.Fatalf("%q was taken for an issue URL", bad)
		}
	}
	for _, bad := range []string{"repos/o/issues/1", "repos/o/r/pulls/1", "repos/o/r/issues/x"} {
		if _, err := workfile.URLOfPath(bad); err == nil {
			t.Fatalf("%q was taken for an issue path", bad)
		}
	}
	if workfile.OriginOf("COLLABORATOR") != "internal" || workfile.OriginOf("CONTRIBUTOR") != "external" || workfile.OriginOf("") != "external" {
		t.Fatal("origin")
	}
}
