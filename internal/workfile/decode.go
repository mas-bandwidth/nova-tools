package workfile

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

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
// wrong kind, an issue whose URL is not the one its path gives. Every such
// problem of the file is named in the one error (SPEC-WORK-V1 section 1.2).
func Decode(file string, data []byte, lim worklang.Limits) (*Tree, error) {
	top, err := worklang.Read(file, data, lim)
	if err != nil {
		// The reader speaks of plans; the refusal here is of a tree.
		var r *worklang.Refusal
		if errors.As(err, &r) {
			return nil, fmt.Errorf("workfile: tree file=%s: %s", file, strings.ReplaceAll(r.Reason, "a plan is data", "a tree is data"))
		}
		return nil, err
	}
	d := &decoder{file: file}
	return d.tree(top)
}

// decoder collects every shape problem of one tree (SPEC-WORK-V1 section
// 1.2). A structural break stops that one record; the rest of the file is
// still read, and Decode returns every problem together.
type decoder struct {
	file  string
	probs []error
}

func (d *decoder) errf(at, format string, a ...any) error {
	err := fmt.Errorf("workfile: file=%s %s: %s", d.file, at, fmt.Sprintf(format, a...))
	d.probs = append(d.probs, err)
	return err
}

// fail records a problem for finish to report with the others and goes on;
// a caller that needs the error itself uses errf.
func (d *decoder) fail(at, format string, a ...any) { _ = d.errf(at, format, a...) }

// finish returns the tree when nothing was wrong, or every problem found,
// together (SPEC-WORK-V1 section 1.2).
func (d *decoder) finish(t *Tree) (*Tree, error) {
	if len(d.probs) == 0 {
		return t, nil
	}
	return nil, errors.Join(d.probs...)
}

// record reads (head <id?> :key value ...) with exactly the keys given.
func (d *decoder) record(at string, f worklang.Form, head string, withID bool, keys []string) (worklang.Form, map[string]worklang.Form, error) {
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

func (d *decoder) pairs(at, head string, rest []worklang.Form, keys []string) (map[string]worklang.Form, error) {
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
			d.fail(at, "(%s) holds the unknown key :%s", head, k.Value)
			continue
		}
		if _, dup := m[k.Value]; dup {
			d.fail(at, "(%s) holds :%s twice", head, k.Value)
			continue
		}
		if i+1 >= len(rest) {
			d.fail(at, "(%s) :%s has no value", head, k.Value)
			continue
		}
		m[k.Value] = rest[i+1]
	}
	for _, k := range keys {
		if _, ok := m[k]; !ok {
			d.fail(at, "(%s) has no :%s", head, k)
		}
	}
	return m, nil
}

func (d *decoder) str(at, key string, f worklang.Form) (string, error) {
	if f.Kind != worklang.String {
		return "", d.errf(at, ":%s wants a string", key)
	}
	return f.Value, nil
}

// num reads a positive integer whose spelling is the one strconv.Itoa
// writes (SPEC-WORK-V1 section 1.2: the file is canonical, one tree, one
// file, so the SHA-256 names the tree): the form's source span is the
// number's spelling, so a leading '+' or leading zeros, which the worklang
// integer reader takes, is refused here.
func (d *decoder) num(at, key string, f worklang.Form) (int, error) {
	if f.Kind != worklang.Integer || f.Int <= 0 || f.Int > maxNumber ||
		f.End-f.Offset != len(strconv.Itoa(int(f.Int))) {
		return 0, d.errf(at, ":%s wants a positive integer written canonically", key)
	}
	return int(f.Int), nil
}

func (d *decoder) boolean(at, key string, f worklang.Form) (bool, error) {
	if f.Kind == worklang.Symbol && (f.Value == "true" || f.Value == "false") {
		return f.Value == "true", nil
	}
	return false, d.errf(at, ":%s wants true or false", key)
}

// enum reads a keyword back into GitHub's spelling, and () as null.
func (d *decoder) enum(at, key string, f worklang.Form) (string, error) {
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

func (d *decoder) list(at, key string, f worklang.Form) ([]worklang.Form, error) {
	if f.Kind != worklang.List {
		return nil, d.errf(at, ":%s wants a list", key)
	}
	return f.List, nil
}

func (d *decoder) strs(at, key string, f worklang.Form) ([]string, error) {
	xs, err := d.list(at, key, f)
	if err != nil {
		return nil, err
	}
	var out []string
	bad := false
	for _, x := range xs {
		s, err := d.str(at, key, x)
		if err != nil {
			bad = true
			continue
		}
		out = append(out, s)
	}
	if bad {
		// Each wrong element is already named. The caller skips the list.
		return nil, errors.New("workfile: a list holds a value of the wrong kind")
	}
	return out, nil
}

func (d *decoder) takeStr(at, key string, m map[string]worklang.Form) (string, bool) {
	f, ok := m[key]
	if !ok {
		return "", false
	}
	s, err := d.str(at, key, f)
	if err != nil {
		return "", false
	}
	return s, true
}

func (d *decoder) tree(f worklang.Form) (*Tree, error) {
	id, m, _ := d.record("(root)", f, "work-tree", true, []string{"source", "org", "fetched", "repos"})
	if m == nil {
		return d.finish(nil)
	}
	if id.Kind != worklang.String || id.Value != Format {
		d.fail("(root)", "format %q, this reader reads %q", id.Value, Format)
	}
	t := &Tree{}
	if s, ok := d.takeStr("(root)", "source", m); ok {
		t.Source = s
	}
	if s, ok := d.takeStr("(root)", "org", m); ok {
		t.Org = s
	}
	if s, ok := d.takeStr("(root)", "fetched", m); ok {
		t.Fetched = s
	}
	if reposForm, ok := m["repos"]; ok {
		if repos, err := d.list("(root)", "repos", reposForm); err == nil {
			for _, rf := range repos {
				r, bad := d.repo(rf)
				if bad {
					continue
				}
				if len(t.Repos) > 0 && r.Name <= t.Repos[len(t.Repos)-1].Name {
					d.fail("repos/"+r.Name, "out of order or repeated")
				}
				t.Repos = append(t.Repos, r)
			}
		}
	}
	return d.finish(t)
}

func (d *decoder) repo(f worklang.Form) (Repo, bool) {
	id, m, _ := d.record("repos", f, "repo", true, []string{"url", "archived", "issues"})
	if m == nil {
		return Repo{}, true
	}
	if id.Kind != worklang.String {
		d.fail("repos", "a repo's identity is its owner/name string")
		return Repo{}, true
	}
	r := Repo{Name: id.Value}
	at := "repos/" + r.Name
	if s, ok := d.takeStr(at, "url", m); ok {
		r.URL = s
	}
	if archived, ok := m["archived"]; ok {
		if v, err := d.boolean(at, "archived", archived); err == nil {
			r.Archived = v
		}
	}
	if issuesForm, ok := m["issues"]; ok {
		if issues, err := d.list(at, "issues", issuesForm); err == nil {
			for _, isf := range issues {
				is, bad := d.issue(r.Name, isf)
				if bad {
					continue
				}
				if len(r.Issues) > 0 && is.Number <= r.Issues[len(r.Issues)-1].Number {
					d.fail(Path(r.Name, is.Number), "out of order or repeated")
				}
				r.Issues = append(r.Issues, is)
			}
		}
	}
	return r, false
}

var issueKeys = []string{"url", "node-id", "title", "state", "state-reason", "origin", "author", "author-association",
	"created", "updated", "closed", "locked", "lock-reason", "labels", "assignees", "milestone", "body",
	"comments", "references", "linked-prs"}

func (d *decoder) issue(repo string, f worklang.Form) (Issue, bool) {
	id, m, _ := d.record("repos/"+repo+"/issues", f, "issue", true, issueKeys)
	if m == nil {
		return Issue{}, true
	}
	n, numErr := d.num("repos/"+repo+"/issues", "number", id)
	at := "repos/" + repo + "/issues"
	if numErr == nil {
		at = Path(repo, n)
	}
	is := Issue{Number: n}
	for _, s := range []struct {
		key string
		dst *string
	}{{"url", &is.URL}, {"node-id", &is.NodeID}, {"title", &is.Title}, {"author", &is.Author},
		{"created", &is.Created}, {"updated", &is.Updated}, {"closed", &is.Closed}, {"body", &is.Body}} {
		if v, ok := d.takeStr(at, s.key, m); ok {
			*s.dst = v
		}
	}
	for _, s := range []struct {
		key string
		dst *string
	}{{"state", &is.State}, {"state-reason", &is.StateReason}, {"author-association", &is.AuthorAssociation}, {"lock-reason", &is.LockReason}} {
		if fv, ok := m[s.key]; ok {
			if v, err := d.enum(at, s.key, fv); err == nil {
				*s.dst = v
			}
		}
	}
	if o, ok := m["origin"]; ok {
		if o.Kind == worklang.Keyword && (o.Value == "internal" || o.Value == "external") {
			is.Origin = o.Value
		} else {
			d.fail(at, ":origin wants :internal or :external")
		}
	}
	if numErr == nil {
		if fv, ok := m["url"]; ok && fv.Kind == worklang.String {
			if want := IssueURL(repo, n); is.URL != want {
				d.fail(at, ":url %q is not the URL its path gives, %q", is.URL, want)
			}
		}
	}
	if fv, ok := m["locked"]; ok {
		if v, err := d.boolean(at, "locked", fv); err == nil {
			is.Locked = v
		}
	}
	labelsOK, assigneesOK := false, false
	if fv, ok := m["labels"]; ok {
		if v, err := d.strs(at, "labels", fv); err == nil {
			is.Labels = v
			labelsOK = true
		}
	}
	if fv, ok := m["assignees"]; ok {
		if v, err := d.strs(at, "assignees", fv); err == nil {
			is.Assignees = v
			assigneesOK = true
		}
	}
	if labelsOK && assigneesOK && (!sort.StringsAreSorted(is.Labels) || !sort.StringsAreSorted(is.Assignees)) {
		d.fail(at, ":labels and :assignees must be sorted")
	}
	if fv, ok := m["milestone"]; ok {
		if ms, err := d.list(at, "milestone", fv); err == nil && len(ms) > 0 {
			mm, _ := d.pairs(at, "milestone", ms, []string{"number", "title"})
			if mm != nil {
				is.Milestone = &Milestone{}
				if n, ok := mm["number"]; ok {
					is.Milestone.Number, _ = d.num(at, "milestone :number", n)
				}
				if title, ok := mm["title"]; ok {
					is.Milestone.Title, _ = d.str(at, "milestone :title", title)
				}
			}
		}
	}
	if fv, ok := m["comments"]; ok {
		if cs, err := d.list(at, "comments", fv); err == nil {
			seen := map[string]bool{}
			for _, cf := range cs {
				cid, cm, _ := d.record(at+"/comments", cf, "comment", true, []string{"url", "author", "author-association", "created", "updated", "body"})
				if cm == nil {
					continue
				}
				c := Comment{}
				idStr, idErr := d.str(at+"/comments", "id", cid)
				if idErr != nil {
					continue
				}
				c.ID = idStr
				if seen[c.ID] {
					d.fail(at+"/comments/"+c.ID, "the comment id is repeated")
					continue
				}
				seen[c.ID] = true
				cat := at + "/comments/" + c.ID
				if v, ok := d.takeStr(cat, "url", cm); ok {
					c.URL = v
				}
				if v, ok := d.takeStr(cat, "author", cm); ok {
					c.Author = v
				}
				if v, ok := d.takeStr(cat, "created", cm); ok {
					c.Created = v
				}
				if v, ok := d.takeStr(cat, "updated", cm); ok {
					c.Updated = v
				}
				if v, ok := d.takeStr(cat, "body", cm); ok {
					c.Body = v
				}
				if assoc, ok := cm["author-association"]; ok {
					c.AuthorAssociation, _ = d.enum(cat, "author-association", assoc)
				}
				is.Comments = append(is.Comments, c)
			}
		}
	}
	if fv, ok := m["references"]; ok {
		if rs, err := d.list(at, "references", fv); err == nil {
			for _, rf := range rs {
				_, rm, _ := d.record(at+"/references", rf, "ref", false, []string{"kind", "repo", "number", "url", "actor", "at", "will-close"})
				if rm == nil {
					continue
				}
				r := Reference{}
				rat := at + "/references"
				kindOK := false
				if v, ok := d.takeStr(rat, "kind", rm); ok {
					r.Kind = v
					kindOK = true
				}
				repoOK := false
				if v, ok := d.takeStr(rat, "repo", rm); ok {
					r.Repo = v
					repoOK = true
				}
				urlOK := false
				if v, ok := d.takeStr(rat, "url", rm); ok {
					r.URL = v
					urlOK = true
				}
				if v, ok := d.takeStr(rat, "actor", rm); ok {
					r.Actor = v
				}
				if v, ok := d.takeStr(rat, "at", rm); ok {
					r.At = v
				}
				if kindOK && r.Kind == "" && ((repoOK && r.Repo != "") || (urlOK && r.URL != "")) {
					d.fail(rat, ":kind \"\" wants :repo \"\" and :url \"\"")
				}
				if kindOK {
					if num, ok := rm["number"]; ok {
						r.Number, _ = d.refNumber(rat, r.Kind, num)
					}
				}
				if wc, ok := rm["will-close"]; ok {
					r.WillClose, _ = d.boolean(rat, "will-close", wc)
				}
				is.References = append(is.References, r)
			}
		}
	}
	if fv, ok := m["linked-prs"]; ok {
		if ps, err := d.list(at, "linked-prs", fv); err == nil {
			for _, pf := range ps {
				_, pm, _ := d.record(at+"/linked-prs", pf, "pr", false, []string{"repo", "number", "url", "state"})
				if pm == nil {
					continue
				}
				l := LinkedPR{}
				pat := at + "/linked-prs"
				if v, ok := d.takeStr(pat, "repo", pm); ok {
					l.Repo = v
				}
				if num, ok := pm["number"]; ok {
					l.Number, _ = d.num(pat, "number", num)
				}
				if v, ok := d.takeStr(pat, "url", pm); ok {
					l.URL = v
				}
				if st, ok := pm["state"]; ok {
					l.State, _ = d.enum(pat, "state", st)
				}
				is.LinkedPRs = append(is.LinkedPRs, l)
			}
		}
	}
	return is, numErr != nil
}

// refNumber reads a reference's :number: positive for a reference with a
// source, 0 for one whose source GitHub does not show this login (a
// private repository's issue), which carries :kind "" (SPEC-WORK-V1
// section 1.3).
func (d *decoder) refNumber(at, kind string, f worklang.Form) (int, error) {
	if kind == "" {
		if f.Kind != worklang.Integer || f.Int != 0 {
			return 0, d.errf(at, "a reference with no source wants :number 0")
		}
		return 0, nil
	}
	return d.num(at, "number", f)
}
