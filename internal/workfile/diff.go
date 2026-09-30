package workfile

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// Difference is one way a tree and a fresh fetch disagree, at one path:
// MISSING is on the source and not in the tree, EXTRA is in the tree and not
// on the source, DRIFT is a field whose value differs. Want is the source's
// value and Got the tree's, each rendered by Show.
type Difference struct {
	Kind  string // MISSING, EXTRA or DRIFT
	Path  string
	Field string
	Want  string
	Got   string
}

// Diff compares the tree (got) with a fresh fetch (want), field for field,
// over the repositories named in scope (every repository of either when
// scope is empty). The result is sorted by path, then field; empty means the
// tree holds everything the source holds and nothing else.
func Diff(got, want *Tree, scope []string) []Difference {
	var out []Difference
	in := func(name string) bool {
		if len(scope) == 0 {
			return true
		}
		for _, s := range scope {
			if s == name {
				return true
			}
		}
		return false
	}
	drift := func(path, field, w, g string) {
		if w != g {
			out = append(out, Difference{Kind: "DRIFT", Path: path, Field: field, Want: Show(w), Got: Show(g)})
		}
	}
	gotRepos := map[string]*Repo{}
	for i := range got.Repos {
		gotRepos[got.Repos[i].Name] = &got.Repos[i]
	}
	wantRepos := map[string]*Repo{}
	for i := range want.Repos {
		wantRepos[want.Repos[i].Name] = &want.Repos[i]
	}
	for name, w := range wantRepos {
		if !in(name) {
			continue
		}
		g, ok := gotRepos[name]
		if !ok {
			out = append(out, Difference{Kind: "MISSING", Path: "repos/" + name, Field: "repo", Want: strconv.Itoa(len(w.Issues)) + "-issues"})
			continue
		}
		drift("repos/"+name, "url", w.URL, g.URL)
		drift("repos/"+name, "archived", strconv.FormatBool(w.Archived), strconv.FormatBool(g.Archived))
		out = append(out, diffIssues(name, g.Issues, w.Issues)...)
	}
	for name, g := range gotRepos {
		if !in(name) {
			continue
		}
		if _, ok := wantRepos[name]; !ok {
			out = append(out, Difference{Kind: "EXTRA", Path: "repos/" + name, Field: "repo", Got: strconv.Itoa(len(g.Issues)) + "-issues"})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		if out[i].Field != out[j].Field {
			return out[i].Field < out[j].Field
		}
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Want+"\x00"+out[i].Got < out[j].Want+"\x00"+out[j].Got
	})
	return out
}

func diffIssues(repo string, got, want []Issue) []Difference {
	var out []Difference
	g := map[int]*Issue{}
	for i := range got {
		g[got[i].Number] = &got[i]
	}
	w := map[int]*Issue{}
	for i := range want {
		w[want[i].Number] = &want[i]
	}
	for n, wi := range w {
		gi, ok := g[n]
		if !ok {
			out = append(out, Difference{Kind: "MISSING", Path: Path(repo, n), Field: "issue", Want: Show(wi.Title)})
			continue
		}
		out = append(out, DiffIssue(repo, gi, wi)...)
	}
	for n, gi := range g {
		if _, ok := w[n]; !ok {
			out = append(out, Difference{Kind: "EXTRA", Path: Path(repo, n), Field: "issue", Got: Show(gi.Title)})
		}
	}
	return out
}

// DiffIssue compares every field of one issue, its comments by id, and its
// references and linked pull requests as sets of records.
func DiffIssue(repo string, g, w *Issue) []Difference {
	var out []Difference
	at := Path(repo, w.Number)
	drift := func(field, wv, gv string) {
		if wv != gv {
			out = append(out, Difference{Kind: "DRIFT", Path: at, Field: field, Want: Show(wv), Got: Show(gv)})
		}
	}
	drift("url", w.URL, g.URL)
	drift("node-id", w.NodeID, g.NodeID)
	drift("title", w.Title, g.Title)
	drift("state", w.State, g.State)
	drift("state-reason", w.StateReason, g.StateReason)
	drift("origin", w.Origin, g.Origin)
	drift("author", w.Author, g.Author)
	drift("author-association", w.AuthorAssociation, g.AuthorAssociation)
	drift("created", w.Created, g.Created)
	drift("updated", w.Updated, g.Updated)
	drift("closed", w.Closed, g.Closed)
	drift("locked", strconv.FormatBool(w.Locked), strconv.FormatBool(g.Locked))
	drift("lock-reason", w.LockReason, g.LockReason)
	if !slices.Equal(w.Labels, g.Labels) {
		out = append(out, Difference{Kind: "DRIFT", Path: at, Field: "labels", Want: Show(fmt.Sprintf("%q", w.Labels)), Got: Show(fmt.Sprintf("%q", g.Labels))})
	}
	if !slices.Equal(w.Assignees, g.Assignees) {
		out = append(out, Difference{Kind: "DRIFT", Path: at, Field: "assignees", Want: Show(fmt.Sprintf("%q", w.Assignees)), Got: Show(fmt.Sprintf("%q", g.Assignees))})
	}
	drift("milestone", milestone(w.Milestone), milestone(g.Milestone))
	drift("body", w.Body, g.Body)

	// Comments by id, in order. A repeated id in either list is never folded
	// into its first: the order line names it and each repeat is its own
	// EXTRA or MISSING (a comment injected under an existing id is found).
	gOrder, wOrder := commentIDs(g.Comments), commentIDs(w.Comments)
	occ := map[string][]*Comment{} // each id's occurrences in the tree, in order
	for i := range g.Comments {
		occ[g.Comments[i].ID] = append(occ[g.Comments[i].ID], &g.Comments[i])
	}
	wCount := count(wOrder)
	gCount := count(gOrder)
	wSeen := map[string]int{}
	for i := range w.Comments {
		c := &w.Comments[i]
		wSeen[c.ID]++
		cat := at + "/comments/" + c.ID
		if wSeen[c.ID] > gCount[c.ID] {
			out = append(out, Difference{Kind: "MISSING", Path: cat, Field: "comment", Want: Show(c.Body)})
			continue
		}
		gcm := occ[c.ID][wSeen[c.ID]-1]
		for _, f := range []struct{ name, w, g string }{
			{"url", c.URL, gcm.URL}, {"author", c.Author, gcm.Author},
			{"author-association", c.AuthorAssociation, gcm.AuthorAssociation},
			{"created", c.Created, gcm.Created}, {"updated", c.Updated, gcm.Updated}, {"body", c.Body, gcm.Body},
		} {
			if f.w != f.g {
				out = append(out, Difference{Kind: "DRIFT", Path: cat, Field: f.name, Want: Show(f.w), Got: Show(f.g)})
			}
		}
	}
	gSeen := map[string]int{}
	for i := range g.Comments {
		c := &g.Comments[i]
		gSeen[c.ID]++
		if gSeen[c.ID] > wCount[c.ID] {
			out = append(out, Difference{Kind: "EXTRA", Path: at + "/comments/" + c.ID, Field: "comment", Got: Show(c.Body)})
		}
	}
	if !slices.Equal(gOrder, wOrder) {
		out = append(out, Difference{Kind: "DRIFT", Path: at, Field: "comments-order", Want: Show(strings.Join(wOrder, ",")), Got: Show(strings.Join(gOrder, ","))})
	}

	out = append(out, diffList(at, "references", "reference", refLines(w.References), refLines(g.References))...)
	out = append(out, diffList(at, "linked-prs", "linked-pr", prLines(w.LinkedPRs), prLines(g.LinkedPRs))...)
	return out
}

func milestone(m *Milestone) string {
	if m == nil {
		return ""
	}
	return strconv.Itoa(m.Number) + ":" + m.Title
}

func refLines(rs []Reference) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = fmt.Sprintf("%s %s#%d %s by=%s at=%s will-close=%t", r.Kind, r.Repo, r.Number, r.URL, r.Actor, r.At, r.WillClose)
	}
	return out
}

func prLines(ps []LinkedPR) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = fmt.Sprintf("%s#%d %s %s", p.Repo, p.Number, p.URL, p.State)
	}
	return out
}

func count(ids []string) map[string]int {
	m := map[string]int{}
	for _, id := range ids {
		m[id]++
	}
	return m
}

func commentIDs(cs []Comment) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.ID
	}
	return out
}

// diffList compares two lists of records in order (SPEC-WORK-V1 section 1.2:
// references in timeline order, linked pull requests in GitHub's): each
// record on one side and not the other is MISSING or EXTRA, and a list whose
// order differs is DRIFT on <key>-order.
func diffList(at, key, field string, want, got []string) []Difference {
	out := diffSet(at+"/"+key, field, want, got)
	if !slices.Equal(want, got) {
		out = append(out, Difference{Kind: "DRIFT", Path: at, Field: key + "-order", Want: Show(strings.Join(want, "; ")), Got: Show(strings.Join(got, "; "))})
	}
	return out
}

// diffSet compares two lists of records as multisets.
func diffSet(path, field string, want, got []string) []Difference {
	count := map[string]int{}
	for _, w := range want {
		count[w]++
	}
	for _, g := range got {
		count[g]--
	}
	var out []Difference
	for k, n := range count {
		for ; n > 0; n-- {
			out = append(out, Difference{Kind: "MISSING", Path: path, Field: field, Want: Show(k)})
		}
		for ; n < 0; n++ {
			out = append(out, Difference{Kind: "EXTRA", Path: path, Field: field, Got: Show(k)})
		}
	}
	return out
}

// Show renders a value for a difference line: whole when it is short and one
// line, else its length and the head of its SHA-256, so a line never carries
// a body and two different values never render alike.
func Show(s string) string {
	if len(s) <= 80 && !strings.ContainsAny(s, "\n\r") {
		return s
	}
	sum := sha256.Sum256([]byte(s))
	return fmt.Sprintf("bytes:%d:sha256:%s", len(s), hex.EncodeToString(sum[:])[:16])
}
