package workfile

import (
	"fmt"
	"strconv"

	"github.com/mas-bandwidth/nova-tools/internal/worklang"
)

// Limits is the reader's bounds for a tree file of at most maxBytes: the
// depth of the shape (section 1.2) with room to spare, and at most one atom
// per byte, so the byte bound is the one that binds.
func Limits(maxBytes int) worklang.Limits {
	return worklang.Limits{MaxBytes: maxBytes, MaxDepth: 16, MaxNodes: maxBytes}
}

// Decode reads a tree file through internal/worklang (nothing is evaluated)
// and refuses, naming the path and the key, any record that is not exactly
// the shape Encode writes: a missing, repeated or unknown key, a value of the
// wrong kind, an issue whose URL is not the one its path gives.
func Decode(file string, data []byte, lim worklang.Limits) (*Tree, error) {
	top, err := worklang.Read(file, data, lim)
	if err != nil {
		return nil, err
	}
	d := decoder{file: file}
	return d.tree(top)
}

type decoder struct{ file string }

func (d decoder) errf(at, format string, a ...any) error {
	return fmt.Errorf("workfile: file=%s %s: %s", d.file, at, fmt.Sprintf(format, a...))
}

// record reads (head <id?> :key value ...) with exactly the keys given.
func (d decoder) record(at string, f worklang.Form, head string, withID bool, keys []string) (worklang.Form, map[string]worklang.Form, error) {
	var id worklang.Form
	if f.Kind != worklang.List || len(f.List) == 0 || f.List[0].Kind != worklang.Symbol || f.List[0].Value != head {
		return id, nil, d.errf(at, "want a (%s ...) record", head)
	}
	rest := f.List[1:]
	if withID {
		if len(rest) == 0 {
			return id, nil, d.errf(at, "(%s) has no identity", head)
		}
		id, rest = rest[0], rest[1:]
	}
	m, err := d.pairs(at, head, rest, keys)
	return id, m, err
}

func (d decoder) pairs(at, head string, rest []worklang.Form, keys []string) (map[string]worklang.Form, error) {
	want := map[string]bool{}
	for _, k := range keys {
		want[k] = true
	}
	m := map[string]worklang.Form{}
	for i := 0; i < len(rest); i += 2 {
		k := rest[i]
		if k.Kind != worklang.Keyword {
			return nil, d.errf(at, "(%s) holds a value where a key belongs, at byte=%d", head, k.Offset)
		}
		if !want[k.Value] {
			return nil, d.errf(at, "(%s) holds the unknown key :%s", head, k.Value)
		}
		if _, dup := m[k.Value]; dup {
			return nil, d.errf(at, "(%s) holds :%s twice", head, k.Value)
		}
		if i+1 >= len(rest) {
			return nil, d.errf(at, "(%s) :%s has no value", head, k.Value)
		}
		m[k.Value] = rest[i+1]
	}
	for _, k := range keys {
		if _, ok := m[k]; !ok {
			return nil, d.errf(at, "(%s) has no :%s", head, k)
		}
	}
	return m, nil
}

func (d decoder) str(at, key string, f worklang.Form) (string, error) {
	if f.Kind != worklang.String {
		return "", d.errf(at, ":%s wants a string", key)
	}
	return f.Value, nil
}

func (d decoder) num(at, key string, f worklang.Form) (int, error) {
	if f.Kind != worklang.Integer || f.Int <= 0 || f.Int > 1<<31 {
		return 0, d.errf(at, ":%s wants a positive integer", key)
	}
	return int(f.Int), nil
}

func (d decoder) boolean(at, key string, f worklang.Form) (bool, error) {
	if f.Kind == worklang.Symbol && (f.Value == "true" || f.Value == "false") {
		return f.Value == "true", nil
	}
	return false, d.errf(at, ":%s wants true or false", key)
}

// enum reads a keyword back into GitHub's spelling, and () as null.
func (d decoder) enum(at, key string, f worklang.Form) (string, error) {
	if f.Kind == worklang.List && len(f.List) == 0 {
		return "", nil
	}
	if f.Kind != worklang.Keyword {
		return "", d.errf(at, ":%s wants a keyword or ()", key)
	}
	v, err := unkeyword(f.Value)
	if err != nil {
		return "", d.errf(at, ":%s: %v", key, err)
	}
	return v, nil
}

func (d decoder) list(at, key string, f worklang.Form) ([]worklang.Form, error) {
	if f.Kind != worklang.List {
		return nil, d.errf(at, ":%s wants a list", key)
	}
	return f.List, nil
}

func (d decoder) strs(at, key string, f worklang.Form) ([]string, error) {
	xs, err := d.list(at, key, f)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, x := range xs {
		s, err := d.str(at, key, x)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

func (d decoder) tree(f worklang.Form) (*Tree, error) {
	id, m, err := d.record("(root)", f, "work-tree", true, []string{"source", "org", "fetched", "repos"})
	if err != nil {
		return nil, err
	}
	if id.Kind != worklang.String || id.Value != Format {
		return nil, d.errf("(root)", "format %q, this reader reads %q", id.Value, Format)
	}
	t := &Tree{}
	if t.Source, err = d.str("(root)", "source", m["source"]); err != nil {
		return nil, err
	}
	if t.Org, err = d.str("(root)", "org", m["org"]); err != nil {
		return nil, err
	}
	if t.Fetched, err = d.str("(root)", "fetched", m["fetched"]); err != nil {
		return nil, err
	}
	repos, err := d.list("(root)", "repos", m["repos"])
	if err != nil {
		return nil, err
	}
	for i, rf := range repos {
		r, err := d.repo(rf)
		if err != nil {
			return nil, err
		}
		if i > 0 && r.Name <= t.Repos[i-1].Name {
			return nil, d.errf("repos/"+r.Name, "out of order or repeated")
		}
		t.Repos = append(t.Repos, r)
	}
	return t, nil
}

func (d decoder) repo(f worklang.Form) (Repo, error) {
	id, m, err := d.record("repos", f, "repo", true, []string{"url", "archived", "issues"})
	if err != nil {
		return Repo{}, err
	}
	if id.Kind != worklang.String {
		return Repo{}, d.errf("repos", "a repo's identity is its owner/name string")
	}
	r := Repo{Name: id.Value}
	at := "repos/" + r.Name
	if r.URL, err = d.str(at, "url", m["url"]); err != nil {
		return r, err
	}
	if r.Archived, err = d.boolean(at, "archived", m["archived"]); err != nil {
		return r, err
	}
	issues, err := d.list(at, "issues", m["issues"])
	if err != nil {
		return r, err
	}
	for i, isf := range issues {
		is, err := d.issue(r.Name, isf)
		if err != nil {
			return r, err
		}
		if i > 0 && is.Number <= r.Issues[i-1].Number {
			return r, d.errf(Path(r.Name, is.Number), "out of order or repeated")
		}
		r.Issues = append(r.Issues, is)
	}
	return r, nil
}

var issueKeys = []string{"url", "node-id", "title", "state", "state-reason", "origin", "author", "author-association",
	"created", "updated", "closed", "locked", "lock-reason", "labels", "assignees", "milestone", "body",
	"comments", "references", "linked-prs"}

func (d decoder) issue(repo string, f worklang.Form) (Issue, error) {
	id, m, err := d.record("repos/"+repo+"/issues", f, "issue", true, issueKeys)
	if err != nil {
		return Issue{}, err
	}
	n, err := d.num("repos/"+repo+"/issues", "number", id)
	if err != nil {
		return Issue{}, err
	}
	at := Path(repo, n)
	is := Issue{Number: n}
	for _, s := range []struct {
		key string
		dst *string
	}{{"url", &is.URL}, {"node-id", &is.NodeID}, {"title", &is.Title}, {"author", &is.Author},
		{"created", &is.Created}, {"updated", &is.Updated}, {"closed", &is.Closed}, {"body", &is.Body}} {
		if *s.dst, err = d.str(at, s.key, m[s.key]); err != nil {
			return is, err
		}
	}
	for _, s := range []struct {
		key string
		dst *string
	}{{"state", &is.State}, {"state-reason", &is.StateReason}, {"author-association", &is.AuthorAssociation}, {"lock-reason", &is.LockReason}} {
		if *s.dst, err = d.enum(at, s.key, m[s.key]); err != nil {
			return is, err
		}
	}
	if o := m["origin"]; o.Kind == worklang.Keyword && (o.Value == "internal" || o.Value == "external") {
		is.Origin = o.Value
	} else {
		return is, d.errf(at, ":origin wants :internal or :external")
	}
	if want := "https://github.com/" + repo + "/issues/" + strconv.Itoa(n); is.URL != want {
		return is, d.errf(at, ":url %q is not the URL its path gives, %q", is.URL, want)
	}
	if is.Locked, err = d.boolean(at, "locked", m["locked"]); err != nil {
		return is, err
	}
	if is.Labels, err = d.strs(at, "labels", m["labels"]); err != nil {
		return is, err
	}
	if is.Assignees, err = d.strs(at, "assignees", m["assignees"]); err != nil {
		return is, err
	}
	ms, err := d.list(at, "milestone", m["milestone"])
	if err != nil {
		return is, err
	}
	if len(ms) > 0 {
		mm, err := d.pairs(at, "milestone", ms, []string{"number", "title"})
		if err != nil {
			return is, err
		}
		is.Milestone = &Milestone{}
		if is.Milestone.Number, err = d.num(at, "milestone :number", mm["number"]); err != nil {
			return is, err
		}
		if is.Milestone.Title, err = d.str(at, "milestone :title", mm["title"]); err != nil {
			return is, err
		}
	}
	cs, err := d.list(at, "comments", m["comments"])
	if err != nil {
		return is, err
	}
	for _, cf := range cs {
		cid, cm, err := d.record(at+"/comments", cf, "comment", true, []string{"url", "author", "author-association", "created", "updated", "body"})
		if err != nil {
			return is, err
		}
		c := Comment{}
		if c.ID, err = d.str(at+"/comments", "id", cid); err != nil {
			return is, err
		}
		cat := at + "/comments/" + c.ID
		for _, s := range []struct {
			key string
			dst *string
		}{{"url", &c.URL}, {"author", &c.Author}, {"created", &c.Created}, {"updated", &c.Updated}, {"body", &c.Body}} {
			if *s.dst, err = d.str(cat, s.key, cm[s.key]); err != nil {
				return is, err
			}
		}
		if c.AuthorAssociation, err = d.enum(cat, "author-association", cm["author-association"]); err != nil {
			return is, err
		}
		is.Comments = append(is.Comments, c)
	}
	rs, err := d.list(at, "references", m["references"])
	if err != nil {
		return is, err
	}
	for _, rf := range rs {
		_, rm, err := d.record(at+"/references", rf, "ref", false, []string{"kind", "repo", "number", "url", "actor", "at", "will-close"})
		if err != nil {
			return is, err
		}
		r := Reference{}
		rat := at + "/references"
		for _, s := range []struct {
			key string
			dst *string
		}{{"kind", &r.Kind}, {"repo", &r.Repo}, {"url", &r.URL}, {"actor", &r.Actor}, {"at", &r.At}} {
			if *s.dst, err = d.str(rat, s.key, rm[s.key]); err != nil {
				return is, err
			}
		}
		if r.Number, err = d.num(rat, "number", rm["number"]); err != nil {
			return is, err
		}
		if r.WillClose, err = d.boolean(rat, "will-close", rm["will-close"]); err != nil {
			return is, err
		}
		is.References = append(is.References, r)
	}
	ps, err := d.list(at, "linked-prs", m["linked-prs"])
	if err != nil {
		return is, err
	}
	for _, pf := range ps {
		_, pm, err := d.record(at+"/linked-prs", pf, "pr", false, []string{"repo", "number", "url", "state"})
		if err != nil {
			return is, err
		}
		l := LinkedPR{}
		pat := at + "/linked-prs"
		if l.Repo, err = d.str(pat, "repo", pm["repo"]); err != nil {
			return is, err
		}
		if l.Number, err = d.num(pat, "number", pm["number"]); err != nil {
			return is, err
		}
		if l.URL, err = d.str(pat, "url", pm["url"]); err != nil {
			return is, err
		}
		if l.State, err = d.enum(pat, "state", pm["state"]); err != nil {
			return is, err
		}
		is.LinkedPRs = append(is.LinkedPRs, l)
	}
	return is, nil
}
