package roadmap

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/sexp"
)

// UnsortedArea is the area deferred work lands in when it leaves the sprint for a
// feature-form roadmap: one row per deferred card, its cell a :todo task carrying
// the card, until someone places the feature in its own area.
const UnsortedArea = "unsorted"

// Deferred is one piece of work moving out of the sprint into the roadmap data:
// a card's id, its title (the row's feature name), its whole brief, the date it
// moved and where it came from. The brief and origin are kept on the task, never
// rendered.
type Deferred struct {
	ID, Title, Brief, Date, Origin string
}

// AddDeferred adds each deferred card to a feature-form roadmap as a row of the
// Unsorted area, its cell a :todo task (id "card/<card id>") holding the card id,
// brief, date and origin. The area is made, and added to the page and to the root
// work-set, when the file has none. Every byte outside the inserted text is kept:
// the insertions are made at the byte offsets of the lists they extend. The file
// must decode before and after; a card the file already holds is refused, and
// nothing is changed.
func AddDeferred(file string, raw []byte, items []Deferred) ([]byte, error) {
	if len(items) == 0 {
		return nil, fmt.Errorf("roadmap: file=%s: no deferred work to add", file)
	}
	if !IsFeatureForm(file, raw) {
		return nil, fmt.Errorf("roadmap: file=%s: is not the feature-table form (:schema 1 ...); AddDeferred edits only that form", file)
	}
	fs, err := DecodeFeatures(file, raw)
	if err != nil {
		return nil, err
	}
	top, err := readFeatureForm(file, raw)
	if err != nil {
		return nil, err
	}
	nodes := plistValue(top, "nodes")
	page := plistValue(top, "page")
	var rows, cells, tasks strings.Builder
	seen := map[string]bool{}
	for _, it := range items {
		if strings.TrimSpace(it.ID) == "" {
			return nil, fmt.Errorf("roadmap: file=%s: a deferred card has no id", file)
		}
		tid := "card/" + it.ID
		if fs.Nodes[tid] != nil || seen[it.ID] {
			return nil, fmt.Errorf("roadmap: file=%s: the roadmap already holds card %q (node %q); defer it under another name", file, it.ID, tid)
		}
		if a := fs.Nodes[UnsortedArea]; a != nil {
			for _, r := range a.Rows {
				if r.ID == it.ID {
					return nil, fmt.Errorf("roadmap: file=%s: the %s area already has a row %q", file, UnsortedArea, it.ID)
				}
			}
		}
		seen[it.ID] = true
		title := rowTitle(it.Title, it.ID)
		fmt.Fprintf(&rows, "\n          (%s %s)", quote(it.ID), quote(title))
		fmt.Fprintf(&cells, "\n           (%s \"done\" %s)", quote(it.ID), quote(tid))
		fmt.Fprintf(&tasks, "\n  (:id %s :type :task :title %s :state :todo :sprint-card %s", quote(tid), quote(title), quote(it.ID))
		for _, kv := range [][2]string{{"date", it.Date}, {"origin", it.Origin}, {"brief", it.Brief}} {
			if strings.TrimSpace(kv[1]) != "" {
				fmt.Fprintf(&tasks, "\n   :%s %s", kv[0], quote(kv[1]))
			}
		}
		tasks.WriteString(")")
	}
	type insert struct {
		at   int
		text string
	}
	var ins []insert
	if area := findNode(nodes, UnsortedArea); area != nil {
		if fs.Nodes[UnsortedArea].Type != "roadmap" {
			return nil, fmt.Errorf("roadmap: file=%s: node %q is a %s, not the %s area", file, UnsortedArea, fs.Nodes[UnsortedArea].Type, UnsortedArea)
		}
		if cols := fs.Nodes[UnsortedArea].Columns; len(cols) != 1 || cols[0].ID != "done" {
			return nil, fmt.Errorf("roadmap: file=%s: the %s area must have the one column \"done\"", file, UnsortedArea)
		}
		ins = append(ins,
			insert{plistValue(*area, "rows").End - 1, rows.String()},
			insert{plistValue(*area, "cells").End - 1, cells.String()},
			insert{nodes.End - 1, tasks.String()})
	} else {
		root := findNode(nodes, fs.Root)
		if fs.Nodes[fs.Root].Type != "work-set" {
			return nil, fmt.Errorf("roadmap: file=%s: :root %q is a %s; the %s area is added to a work-set root, so add it by hand", file, fs.Root, fs.Nodes[fs.Root].Type, UnsortedArea)
		}
		area := fmt.Sprintf("\n  (:id %s :type :roadmap :title \"Unsorted\"\n   :text \"Work moved out of the sprint, not yet placed in an area.\"\n   :rows (%s)\n   :columns ((\"done\" \"done\"))\n   :cells (%s))",
			quote(UnsortedArea), strings.TrimPrefix(rows.String(), "\n          "), strings.TrimPrefix(cells.String(), "\n           "))
		ins = append(ins,
			insert{plistValue(page, "roadmaps").End - 1, " " + quote(UnsortedArea)},
			insert{plistValue(*root, "children").End - 1, " " + quote(UnsortedArea)},
			insert{nodes.End - 1, area + tasks.String()})
	}
	sort.Slice(ins, func(i, j int) bool { return ins[i].at > ins[j].at })
	out := string(raw)
	for _, in := range ins {
		out = out[:in.at] + in.text + out[in.at:]
	}
	if _, err := DecodeFeatures(file, []byte(out)); err != nil {
		return nil, fmt.Errorf("roadmap: file=%s: the data with the deferred work does not decode: %w", file, err)
	}
	return []byte(out), nil
}

// plistValue is the value of :key in a (:key value ...) list, or the zero Form.
func plistValue(f sexp.Form, key string) sexp.Form {
	for i := 0; i+1 < len(f.List); i += 2 {
		if f.List[i].Kind == sexp.Keyword && f.List[i].Value == key {
			return f.List[i+1]
		}
	}
	return sexp.Form{}
}

// findNode is the node form with :id id in the :nodes list, or nil.
func findNode(nodes sexp.Form, id string) *sexp.Form {
	for i := range nodes.List {
		if v := plistValue(nodes.List[i], "id"); v.Kind == sexp.String && v.Value == id {
			return &nodes.List[i]
		}
	}
	return nil
}

// rowTitle is a feature name fit for a table cell: whitespace joined, | made /,
// and the id when nothing is left.
func rowTitle(title, id string) string {
	t := strings.ReplaceAll(flatText(title), "|", "/")
	if t == "" {
		return id
	}
	return t
}

// quote is s as a data string literal.
func quote(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
}
