/*
Package roadmap reads docs/roadmap.sexp and renders ROADMAP.md from it.

The roadmap is data: one `(roadmap "v1" ...)` form read through internal/worklang
(nothing is evaluated), decoded here into typed records, and refused whole, every
problem named, when a record is not exactly the shape below. tools/roadmap writes
the rendered page; internal/docs holds the committed ROADMAP.md to what Render
makes of the committed sexp, so the page is never written by hand (#4855 deleted
the hand-written one).

The shape:

	(roadmap "v1"
	 :title "<page title>"
	 :text "<the page's opening paragraph>"
	 :scheduled ((scheduled "<id>" :release "<vX.Y>" :title "..." :text "..." :date "YYYY-MM-DD") ...)
	 :done ((done "<id>" :title "..." :text "..." :date "YYYY-MM-DD") ...)
	 :groups ((group "<id>" :title "..." :text "...") ...)
	 :items ((item "<id>" :group "<group id>" :title "..." :text "..." :why "..." :date "YYYY-MM-DD"
	          [:area "..."] [:exists "..."] [:cards <n>]) ...))

Text values may be wrapped across lines in the file; Render joins every run of
whitespace into one blank, so the page reads the same however the file is wrapped.
*/
package roadmap

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/worklang"
)

// Format is the one format string the roadmap form carries.
const Format = "v1"

// Limits bounds a roadmap read: a file of at most 512 KiB, the shape's depth with
// room to spare, and at most one atom per byte, so the byte bound is the one that binds.
var Limits = worklang.Limits{MaxBytes: 512 << 10, MaxDepth: 8, MaxNodes: 512 << 10}

// Doc is a decoded roadmap.
type Doc struct {
	Title     string
	Text      string
	Scheduled []Note
	Done      []Note
	Groups    []Group
	Items     []Item
}

// Note is one record of work that is not on the roadmap: scheduled into a named
// release, or already done. Release is empty for a done note.
type Note struct {
	ID, Release, Title, Text, Date string
}

// Group is one heading of the roadmap; every item names the group it sits under.
type Group struct {
	ID, Title, Text string
}

// Item is one piece of work after the last scheduled release. Exists says what of it
// the code already has, so the item covers only the gap; Cards is how many open sprint
// cards the item replaces (each card is mapped to the item's id in the sprint's own
// record: card ids carry friend names, which a living file of this repository never
// does, docs/SPEC-CI.md#generality-text).
type Item struct {
	ID, Group, Title, Text, Why, Date string
	Area, Exists                      string
	Cards                             int
}

var datePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// Decode reads one roadmap file and returns it, or every shape problem of the file
// together in one error.
func Decode(file string, data []byte) (*Doc, error) {
	top, err := worklang.Read(file, data, Limits)
	if err != nil {
		var r *worklang.Refusal
		if errors.As(err, &r) {
			return nil, fmt.Errorf("roadmap: file=%s: %s", file, strings.ReplaceAll(r.Reason, "a plan is data", "a roadmap is data"))
		}
		return nil, err
	}
	d := &decoder{file: file}
	doc := d.doc(top)
	if len(d.probs) > 0 {
		return nil, errors.Join(d.probs...)
	}
	return doc, nil
}

type decoder struct {
	file  string
	probs []error
}

func (d *decoder) fail(at, format string, a ...any) {
	d.probs = append(d.probs, fmt.Errorf("roadmap: file=%s %s: %s", d.file, at, fmt.Sprintf(format, a...)))
}

// record reads (head ["<id>"] :key value ...): the keys in required must each stand
// once, those in optional at most once, and no other key may stand.
func (d *decoder) record(at string, f worklang.Form, head string, withID bool, required, optional []string) (string, map[string]worklang.Form, bool) {
	if f.Kind != worklang.List || len(f.List) == 0 || f.List[0].Kind != worklang.Symbol || f.List[0].Value != head {
		d.fail(at, "want a (%s ...) record", head)
		return "", nil, false
	}
	rest := f.List[1:]
	id := ""
	if withID {
		if len(rest) == 0 || rest[0].Kind != worklang.String || strings.TrimSpace(rest[0].Value) == "" {
			d.fail(at, "(%s) wants its identity, a non-empty string, after the head", head)
			return "", nil, false
		}
		id, rest = rest[0].Value, rest[1:]
		at = at + " " + head + "=" + id
	}
	allowed := map[string]bool{}
	for _, k := range append(append([]string(nil), required...), optional...) {
		allowed[k] = true
	}
	vals := map[string]worklang.Form{}
	ok := true
	for i := 0; i < len(rest); i += 2 {
		if rest[i].Kind != worklang.Keyword {
			d.fail(at, "want a :key at byte=%d", rest[i].Offset)
			return id, nil, false
		}
		k := rest[i].Value
		if i+1 >= len(rest) {
			d.fail(at, ":%s has no value", k)
			return id, nil, false
		}
		switch {
		case !allowed[k]:
			d.fail(at, "unknown key :%s", k)
			ok = false
		case hasKey(vals, k):
			d.fail(at, "key :%s stands twice", k)
			ok = false
		default:
			vals[k] = rest[i+1]
		}
	}
	for _, k := range required {
		if !hasKey(vals, k) {
			d.fail(at, "missing key :%s", k)
			ok = false
		}
	}
	return id, vals, ok
}

func hasKey(vals map[string]worklang.Form, k string) bool {
	_, ok := vals[k]
	return ok
}

// str returns the string value of key k, "" when the key is absent; a value of
// another kind, or an empty one, is a problem.
func (d *decoder) str(at string, vals map[string]worklang.Form, k string) string {
	f, ok := vals[k]
	if !ok {
		return ""
	}
	if f.Kind != worklang.String || strings.TrimSpace(f.Value) == "" {
		d.fail(at, ":%s wants a non-empty string", k)
		return ""
	}
	return f.Value
}

func (d *decoder) date(at string, vals map[string]worklang.Form) string {
	v := d.str(at, vals, "date")
	if v != "" && !datePattern.MatchString(v) {
		d.fail(at, ":date %q wants YYYY-MM-DD", v)
	}
	return v
}

// count returns the positive integer value of key k, 0 when the key is absent.
func (d *decoder) count(at string, vals map[string]worklang.Form, k string) int {
	f, ok := vals[k]
	if !ok {
		return 0
	}
	if f.Kind != worklang.Integer || f.Int <= 0 || f.Int > 1<<20 {
		d.fail(at, ":%s wants a positive integer", k)
		return 0
	}
	return int(f.Int)
}

// list returns the members of the list value of key k, nil when the key is absent.
func (d *decoder) list(at string, vals map[string]worklang.Form, k string) []worklang.Form {
	f, ok := vals[k]
	if !ok {
		return nil
	}
	if f.Kind != worklang.List {
		d.fail(at, ":%s wants a list", k)
		return nil
	}
	return f.List
}

func (d *decoder) doc(top worklang.Form) *Doc {
	if top.Kind != worklang.List || len(top.List) < 2 || top.List[0].Kind != worklang.Symbol || top.List[0].Value != "roadmap" ||
		top.List[1].Kind != worklang.String || top.List[1].Value != Format {
		d.fail("top", "want one (roadmap %q ...) form", Format)
		return nil
	}
	// The format string is the record's identity slot.
	_, vals, ok := d.record("top", top, "roadmap", true, []string{"title", "text", "groups", "items"}, []string{"scheduled", "done"})
	if !ok {
		return nil
	}
	doc := &Doc{Title: d.str("top", vals, "title"), Text: d.str("top", vals, "text")}
	ids := map[string]string{}
	seen := func(at, kind, id string) {
		if prev, dup := ids[id]; dup {
			d.fail(at, "id %q is already used by a (%s ...) record", id, prev)
			return
		}
		ids[id] = kind
	}
	for i, f := range d.list("top", vals, "scheduled") {
		at := fmt.Sprintf(":scheduled[%d]", i)
		id, v, ok := d.record(at, f, "scheduled", true, []string{"release", "title", "text", "date"}, nil)
		if !ok {
			continue
		}
		seen(at, "scheduled", id)
		doc.Scheduled = append(doc.Scheduled, Note{ID: id, Release: d.str(at, v, "release"), Title: d.str(at, v, "title"), Text: d.str(at, v, "text"), Date: d.date(at, v)})
	}
	for i, f := range d.list("top", vals, "done") {
		at := fmt.Sprintf(":done[%d]", i)
		id, v, ok := d.record(at, f, "done", true, []string{"title", "text", "date"}, nil)
		if !ok {
			continue
		}
		seen(at, "done", id)
		doc.Done = append(doc.Done, Note{ID: id, Title: d.str(at, v, "title"), Text: d.str(at, v, "text"), Date: d.date(at, v)})
	}
	groups := map[string]bool{}
	for i, f := range d.list("top", vals, "groups") {
		at := fmt.Sprintf(":groups[%d]", i)
		id, v, ok := d.record(at, f, "group", true, []string{"title", "text"}, nil)
		if !ok {
			continue
		}
		seen(at, "group", id)
		groups[id] = true
		doc.Groups = append(doc.Groups, Group{ID: id, Title: d.str(at, v, "title"), Text: d.str(at, v, "text")})
	}
	used := map[string]bool{}
	for i, f := range d.list("top", vals, "items") {
		at := fmt.Sprintf(":items[%d]", i)
		id, v, ok := d.record(at, f, "item", true,
			[]string{"group", "title", "text", "why", "date"},
			[]string{"area", "exists", "cards"})
		if !ok {
			continue
		}
		seen(at, "item", id)
		it := Item{ID: id, Group: d.str(at, v, "group"), Title: d.str(at, v, "title"), Text: d.str(at, v, "text"),
			Why: d.str(at, v, "why"), Date: d.date(at, v), Area: d.str(at, v, "area"), Exists: d.str(at, v, "exists"),
			Cards: d.count(at, v, "cards")}
		if it.Group != "" && !groups[it.Group] {
			d.fail(at, ":group %q names no (group ...) record", it.Group)
		}
		used[it.Group] = true
		doc.Items = append(doc.Items, it)
	}
	for _, g := range doc.Groups {
		if !used[g.ID] {
			d.fail(":groups", "group %q holds no item", g.ID)
		}
	}
	return doc
}

// GroupCount returns the number of items under each group id.
func (doc *Doc) GroupCount() map[string]int {
	n := map[string]int{}
	for _, it := range doc.Items {
		n[it.Group]++
	}
	return n
}

// flat joins every run of whitespace into one blank.
func flat(s string) string { return strings.Join(strings.Fields(s), " ") }

// Render returns the ROADMAP.md the document makes: the same bytes for the same
// document, every time.
func Render(doc *Doc, source string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", flat(doc.Title))
	fmt.Fprintf(&b, "<!-- Generated from %s by tools/roadmap. Do not edit this file: edit %s and run `make roadmap`. -->\n\n", source, source)
	fmt.Fprintf(&b, "%s\n", flat(doc.Text))
	if len(doc.Scheduled) > 0 {
		b.WriteString("\n## Scheduled in releases\n\nThese were roadmap items and now have a release. They are listed here so nobody looks for them below.\n\n")
		for _, n := range doc.Scheduled {
			fmt.Fprintf(&b, "- **%s** (%s). %s\n", flat(n.Title), n.Release, flat(n.Text))
		}
	}
	if len(doc.Done) > 0 {
		b.WriteString("\n## Done\n\nDecided and in place. Recorded so the items that measure them have their starting point.\n\n")
		for _, n := range doc.Done {
			fmt.Fprintf(&b, "- **%s** (%s). %s\n", flat(n.Title), n.Date, flat(n.Text))
		}
	}
	count := doc.GroupCount()
	b.WriteString("\n## Contents\n\n")
	for _, g := range doc.Groups {
		fmt.Fprintf(&b, "- [%s](#%s) (%d)\n", flat(g.Title), anchor(g.Title), count[g.ID])
	}
	for _, g := range doc.Groups {
		fmt.Fprintf(&b, "\n## %s\n\n%s\n", flat(g.Title), flat(g.Text))
		for _, it := range doc.Items {
			if it.Group != g.ID {
				continue
			}
			fmt.Fprintf(&b, "\n### %s\n\n%s\n", flat(it.Title), flat(it.Text))
			if it.Exists != "" {
				fmt.Fprintf(&b, "\nAlready in the code: %s\n", flat(it.Exists))
			}
			fmt.Fprintf(&b, "\nWhy it waits: %s\n", flat(it.Why))
			if it.Cards > 0 {
				fmt.Fprintf(&b, "\nReplaces %d open sprint %s, each mapped to `%s`.\n",
					it.Cards, plural(it.Cards, "card", "cards"), it.ID)
			}
		}
	}
	return b.String()
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

var anchorDrop = regexp.MustCompile(`[^a-z0-9 -]`)

// anchor is GitHub's heading anchor for a title: lower case, punctuation dropped,
// blanks to hyphens.
func anchor(title string) string {
	return strings.ReplaceAll(anchorDrop.ReplaceAllString(strings.ToLower(flat(title)), ""), " ", "-")
}
