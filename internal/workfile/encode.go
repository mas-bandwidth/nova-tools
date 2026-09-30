package workfile

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Encode writes the tree in its canonical form: repositories sorted by name,
// issues by number, labels and assignees sorted, every key of every record
// always written in one order, so one tree has exactly one file. It refuses
// what it could not read back unchanged: an enumeration outside [A-Z_], a
// repository or issue out of order or repeated, a number that is not
// positive. SPEC-WORK-V1 section 1.2 is the shape.
func Encode(t *Tree) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString("(work-tree " + quote(Format) + "\n")
	b.WriteString(" :source " + quote(t.Source) + "\n")
	b.WriteString(" :org " + quote(t.Org) + "\n")
	b.WriteString(" :fetched " + quote(t.Fetched) + "\n")
	b.WriteString(" :repos\n (")
	for i, r := range t.Repos {
		if i > 0 {
			if r.Name <= t.Repos[i-1].Name {
				return nil, fmt.Errorf("workfile: repository %q is out of order or repeated after %q", r.Name, t.Repos[i-1].Name)
			}
			b.WriteString("\n  ")
		}
		if err := encodeRepo(&b, r); err != nil {
			return nil, err
		}
	}
	b.WriteString("))\n")
	return b.Bytes(), nil
}

func encodeRepo(b *bytes.Buffer, r Repo) error {
	if strings.Count(r.Name, "/") != 1 || strings.HasPrefix(r.Name, "/") || strings.HasSuffix(r.Name, "/") {
		return fmt.Errorf("workfile: repository name %q is not owner/name", r.Name)
	}
	b.WriteString("(repo " + quote(r.Name) + "\n")
	b.WriteString("   :url " + quote(r.URL) + "\n")
	b.WriteString("   :archived " + boolean(r.Archived) + "\n")
	b.WriteString("   :issues\n   (")
	for i, is := range r.Issues {
		if is.Number <= 0 {
			return fmt.Errorf("workfile: %s: issue number %d is not positive", r.Name, is.Number)
		}
		if i > 0 {
			if is.Number <= r.Issues[i-1].Number {
				return fmt.Errorf("workfile: %s: issue %d is out of order or repeated after %d", r.Name, is.Number, r.Issues[i-1].Number)
			}
			b.WriteString("\n    ")
		}
		if err := encodeIssue(b, r.Name, is); err != nil {
			return err
		}
	}
	b.WriteString("))")
	return nil
}

func encodeIssue(b *bytes.Buffer, repo string, is Issue) error {
	at := Path(repo, is.Number)
	kw := func(field, v string) (string, error) {
		s, err := keyword(v)
		if err != nil {
			return "", fmt.Errorf("workfile: %s %s: %w", at, field, err)
		}
		return s, nil
	}
	state, err := kw("state", is.State)
	if err != nil {
		return err
	}
	reason, err := kw("state-reason", is.StateReason)
	if err != nil {
		return err
	}
	assoc, err := kw("author-association", is.AuthorAssociation)
	if err != nil {
		return err
	}
	lock, err := kw("lock-reason", is.LockReason)
	if err != nil {
		return err
	}
	if is.Origin != "internal" && is.Origin != "external" {
		return fmt.Errorf("workfile: %s origin %q is neither internal nor external", at, is.Origin)
	}
	if !sort.StringsAreSorted(is.Labels) || !sort.StringsAreSorted(is.Assignees) {
		return fmt.Errorf("workfile: %s labels and assignees must be sorted", at)
	}
	p := func(key, val string) { b.WriteString("\n      :" + key + " " + val) }
	b.WriteString("(issue " + strconv.Itoa(is.Number))
	p("url", quote(is.URL))
	p("node-id", quote(is.NodeID))
	p("title", quote(is.Title))
	p("state", state)
	p("state-reason", reason)
	p("origin", ":"+is.Origin)
	p("author", quote(is.Author))
	p("author-association", assoc)
	p("created", quote(is.Created))
	p("updated", quote(is.Updated))
	p("closed", quote(is.Closed))
	p("locked", boolean(is.Locked))
	p("lock-reason", lock)
	p("labels", stringList(is.Labels))
	p("assignees", stringList(is.Assignees))
	if is.Milestone == nil {
		p("milestone", "()")
	} else {
		p("milestone", "(:number "+strconv.Itoa(is.Milestone.Number)+" :title "+quote(is.Milestone.Title)+")")
	}
	p("body", quote(is.Body))
	b.WriteString("\n      :comments (")
	for i, c := range is.Comments {
		a, err := kw("comment "+c.ID+" author-association", c.AuthorAssociation)
		if err != nil {
			return err
		}
		if i > 0 {
			b.WriteString("\n        ")
		}
		b.WriteString("(comment " + quote(c.ID) + " :url " + quote(c.URL) + " :author " + quote(c.Author) +
			" :author-association " + a + " :created " + quote(c.Created) + " :updated " + quote(c.Updated) +
			"\n          :body " + quote(c.Body) + ")")
	}
	b.WriteString(")")
	b.WriteString("\n      :references (")
	for i, r := range is.References {
		if i > 0 {
			b.WriteString("\n        ")
		}
		b.WriteString("(ref :kind " + quote(r.Kind) + " :repo " + quote(r.Repo) + " :number " + strconv.Itoa(r.Number) +
			" :url " + quote(r.URL) + " :actor " + quote(r.Actor) + " :at " + quote(r.At) + " :will-close " + boolean(r.WillClose) + ")")
	}
	b.WriteString(")")
	b.WriteString("\n      :linked-prs (")
	for i, l := range is.LinkedPRs {
		s, err := kw("linked-pr state", l.State)
		if err != nil {
			return err
		}
		if i > 0 {
			b.WriteString("\n        ")
		}
		b.WriteString("(pr :repo " + quote(l.Repo) + " :number " + strconv.Itoa(l.Number) + " :url " + quote(l.URL) + " :state " + s + ")")
	}
	b.WriteString("))")
	return nil
}

// quote writes s as a worklang string: a backslash before every `"` and `\`,
// every other byte as it is (the reader takes the byte after a backslash
// literally, SPEC-WORKLANG section 1).
func quote(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '"' || c == '\\' {
			b.WriteByte('\\')
		}
		b.WriteByte(c)
	}
	b.WriteByte('"')
	return b.String()
}

func boolean(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func stringList(xs []string) string {
	if len(xs) == 0 {
		return "()"
	}
	q := make([]string, len(xs))
	for i, x := range xs {
		q[i] = quote(x)
	}
	return "(" + strings.Join(q, " ") + ")"
}

// keyword writes a GitHub enumeration (OPEN, NOT_PLANNED) as a keyword
// (:open, :not-planned), and GitHub's null (the empty string) as (). Only
// [A-Z_] is accepted, so the mapping is one to one and unkeyword inverts it.
func keyword(v string) (string, error) {
	if v == "" {
		return "()", nil
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		if !(c >= 'A' && c <= 'Z' || c == '_') {
			return "", fmt.Errorf("enumeration %q holds a byte outside [A-Z_]; refusing to write it lossily", v)
		}
	}
	return ":" + strings.ReplaceAll(strings.ToLower(v), "_", "-"), nil
}

func unkeyword(name string) (string, error) {
	for i := 0; i < len(name); i++ {
		c := name[i]
		if !(c >= 'a' && c <= 'z' || c == '-') {
			return "", fmt.Errorf("keyword :%s holds a byte outside [a-z-]", name)
		}
	}
	return strings.ToUpper(strings.ReplaceAll(name, "-", "_")), nil
}
