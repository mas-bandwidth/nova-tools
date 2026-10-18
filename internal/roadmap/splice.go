package roadmap

import (
	"fmt"
	"strings"

	roadmapdoc "github.com/mas-bandwidth/nova-tools/pkg/roadmap"
	"github.com/mas-bandwidth/nova-tools/pkg/sexp"
)

// The edits below read the file through nova-tools pkg/sexp, whose forms carry each one's
// first byte and the byte past its last, and splice only the bytes of the
// records they change. Every byte outside those spans is kept.

// top reads f's one form.
func top(f *file) (sexp.Form, error) {
	return sexp.Read(f.rel, f.data, roadmapdoc.Limits)
}

// value returns the value form of :key in a record or the top form, and its
// index in the record's list; -1 when the key is absent.
func value(rec sexp.Form, key string) (sexp.Form, int) {
	for i := 1; i+1 < len(rec.List); i++ {
		if rec.List[i].Kind == sexp.Keyword && rec.List[i].Value == key {
			return rec.List[i+1], i + 1
		}
	}
	return sexp.Form{}, -1
}

// record finds the record whose identity is id in the list :list holds, and
// its index in that list.
func record(t sexp.Form, list, id string) (sexp.Form, sexp.Form, int, error) {
	l, at := value(t, list)
	if at < 0 || l.Kind != sexp.List {
		return sexp.Form{}, sexp.Form{}, -1, fmt.Errorf("no :%s list to find %s in", list, id)
	}
	for i, r := range l.List {
		if r.Kind == sexp.List && len(r.List) > 1 && r.List[1].Kind == sexp.String && r.List[1].Value == id {
			return l, r, i, nil
		}
	}
	return sexp.Form{}, sexp.Form{}, -1, fmt.Errorf("unknown id %q in :%s", id, list)
}

func (f *file) splice(start, end int, with string) {
	out := make([]byte, 0, len(f.data)-(end-start)+len(with))
	out = append(out, f.data[:start]...)
	out = append(out, with...)
	out = append(out, f.data[end:]...)
	f.data, f.changed = out, true
}

// appendRecord puts rec after the last record of :list, on its own line; a
// list that is empty becomes (rec), and an absent list is added at the end of
// the top form.
func (s *Set) appendRecord(f *file, list, rec string) error {
	if !f.present {
		return fmt.Errorf("this repository has no %s", f.rel)
	}
	t, err := top(f)
	if err != nil {
		return err
	}
	l, at := value(t, list)
	switch {
	case at < 0:
		f.splice(t.End-1, t.End-1, "\n :"+list+"\n ("+rec+")")
	case l.Kind != sexp.List:
		return fmt.Errorf("%s: :%s is not a list", f.rel, list)
	case len(l.List) == 0:
		f.splice(l.Offset, l.End, "("+rec+")")
	default:
		last := l.List[len(l.List)-1]
		f.splice(last.End, last.End, "\n  "+rec)
	}
	return nil
}

// removeRecord takes the record id out of :list and keeps every comment
// around it. A record on lines of its own goes with those lines, its own
// trailing comment included; the comments above it and the neighbours'
// trailing comments stay. A record sharing its first line with the list's
// opening parenthesis goes with the blank after it when the next thing is a
// record, so the parenthesis meets the next record, and alone when it is a
// comment, so the comment keeps its line.
func (s *Set) removeRecord(f *file, list, id string) error {
	t, err := top(f)
	if err != nil {
		return err
	}
	_, r, _, err := record(t, list, id)
	if err != nil {
		return err
	}
	d := f.data
	start, end := r.Offset, r.End
	// the record's own trailing comment, on its last line
	e := end
	for e < len(d) && (d[e] == ' ' || d[e] == '\t') {
		e++
	}
	if e < len(d) && d[e] == ';' {
		for e < len(d) && d[e] != '\n' {
			e++
		}
		end = e
	}
	// does the record start its line?
	ls := start
	for ls > 0 && (d[ls-1] == ' ' || d[ls-1] == '\t') {
		ls--
	}
	ownLine := ls > 0 && d[ls-1] == '\n'
	e = end
	for e < len(d) && (d[e] == ' ' || d[e] == '\t') {
		e++
	}
	switch {
	case ownLine && e < len(d) && d[e] == '\n':
		start, end = ls, e+1 // the whole lines
	case ownLine:
		start, end = ls, e // the record's lines up to what follows on the last one
	default:
		w := end
		for w < len(d) && (d[w] == ' ' || d[w] == '\t' || d[w] == '\n' || d[w] == '\r') {
			w++
		}
		if w < len(d) && d[w] == '(' {
			end = w // the next record moves up to meet the parenthesis
		}
	}
	f.splice(start, end, "")
	return nil
}

// appendText appends words to record id's :text, inside its quotes, so every
// byte of the text before them is kept; a record with no :text gains one.
func (s *Set) appendText(f *file, list, id, words string) error {
	t, err := top(f)
	if err != nil {
		return err
	}
	_, r, _, err := record(t, list, id)
	if err != nil {
		return err
	}
	old, at := value(r, "text")
	if at < 0 || strings.TrimSpace(old.Value) == "" {
		return s.setKey(f, list, id, "text", words)
	}
	q := quote(words)
	f.splice(old.End-1, old.End-1, " "+q[1:len(q)-1])
	return nil
}

// retitle gives record id a new title and keeps the one it had under :kept
// (earlier-title, or earlier-title-2 and on when one is kept already).
func (s *Set) retitle(f *file, list, id, title string) error {
	t, err := top(f)
	if err != nil {
		return err
	}
	_, r, _, err := record(t, list, id)
	if err != nil {
		return err
	}
	old, _ := value(r, "title")
	was := old.Value
	if err := s.setKey(f, list, id, "title", title); err != nil {
		return err
	}
	key := "earlier-title"
	k, at := value(r, "kept")
	for n := 2; at >= 0 && hasKeptKey(k, key); n++ {
		key = fmt.Sprintf("earlier-title-%d", n)
	}
	return s.addKept(f, list, id, key, was)
}

func hasKeptKey(k sexp.Form, key string) bool {
	for _, x := range k.List {
		if x.Kind == sexp.Keyword && x.Value == key {
			return true
		}
	}
	return false
}

// addKept adds one :key "value" pair to record id's :kept, creating it.
func (s *Set) addKept(f *file, list, id, key, v string) error {
	t, err := top(f)
	if err != nil {
		return err
	}
	_, r, _, err := record(t, list, id)
	if err != nil {
		return err
	}
	if k, at := value(r, "kept"); at >= 0 {
		f.splice(k.End-1, k.End-1, "\n          :"+key+" "+quote(v))
		return nil
	}
	f.splice(r.End-1, r.End-1, "\n   :kept (:"+key+" "+quote(v)+")")
	return nil
}

// setKey sets :key of record id in :list to the string v: the value's bytes
// are replaced, or the key is added before the record's closing parenthesis.
func (s *Set) setKey(f *file, list, id, key, v string) error {
	t, err := top(f)
	if err != nil {
		return err
	}
	_, r, _, err := record(t, list, id)
	if err != nil {
		return err
	}
	if old, at := value(r, key); at >= 0 {
		f.splice(old.Offset, old.End, quote(v))
		return nil
	}
	f.splice(r.End-1, r.End-1, "\n   :"+key+" "+quote(v))
	return nil
}

// quote writes s as an s-expression string: a double quote and a backslash are
// escaped, nothing else.
func quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}
