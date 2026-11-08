/*
Package roadmap reads a roadmap's data (docs/roadmap.sexp and the evidence
overlay docs/roadmap-evidence/*.sexp) and writes its feature tables into
ROADMAP.md.

It is a Go port of schema's tools/roadmap/render.lisp, and it renders schema's
committed ROADMAP.md byte for byte from schema's data (roadmap_test.go holds it
to that).

The page is a series of areas. Each area is a table with one row per feature a
user would recognise, one ✅/❌ cell per column (a single "done" column, or an
axis such as languages or platforms), a "complete" row and a link to the source
data. Each table sits between two marker lines in ROADMAP.md:

	<!-- nova-work:<area id>:start -->
	<!-- nova-work:<area id>:end -->

and only the bytes between them are generated. Everything else on the page (the
title, the headings and every paragraph) is written by hand and kept exactly as
it is. The data holds tables and evidence only; prose in the data is refused.

The data is one restricted plist, read as data and never evaluated:

	(:schema 1
	 :root "<id of the node every area is reached from>"
	 :page (:roadmaps ("<area id>" ...))             ; the areas, in page order
	 :nodes
	 ((:id "<id>" :type :task :title "..." :state :done :evidence ("<test or commit>" ...))
	  (:id "<id>" :type :task :title "..." :state :todo)   ; :unknown :todo :doing :done
	  (:id "<id>" :type :work-set :children ("<id>" ...))  ; a cell may need several tasks
	  (:id "<area id>" :type :roadmap :title "<heading>"
	   :rows (("<row id>" "<feature>") ("<row id>" "<feature>" :later t) ...)
	   :columns (("done" "done"))                         ; or one per language, platform ...
	   :cells (("<row id>" "<column id>" "<task or work-set id>") ...))))

A cell is ✅ only when every task under its node is :done, and a :done task must
carry non-blank :evidence. Nodes may carry any other keys (notes, metadata);
they are kept for people and other tools and are not rendered. A row marked
:later t renders "(later)" after its name and still counts in the complete row.

The evidence overlay, roadmap-evidence/*.sexp beside the data, holds any number
of (:id "<task id>" :state :done :evidence (...)) forms, applied in file-name
order, each file whole or not at all, and held to the same rule.
*/
package roadmap

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-roadmap/internal/sexp"
)

// Limits bounds a read: 5 MiB per file (schema's graph, the largest known, is
// about 2.2 MB), nesting with room to spare and at most one atom per byte.
var Limits = sexp.Limits{MaxBytes: 5 << 20, MaxDepth: 64, MaxNodes: 5 << 20}

const (
	// MaxNodes bounds the graph's node count.
	MaxNodes = 10000
	// MaxDepth bounds a walk of the work graph.
	MaxDepth = 64
	// EvidenceDir is the overlay directory's name, beside the data file.
	EvidenceDir = "roadmap-evidence"
)

// Roadmap is a decoded roadmap: the work graph and the areas the page shows.
type Roadmap struct {
	Root  string
	Areas []string // the :page :roadmaps ids, in page order
	Nodes map[string]*Node
}

// Node is one node of the work graph.
type Node struct {
	ID, Type, Title, State string
	Evidence               []string
	Children               []string
	Rows                   []Row
	Columns                []Column
	Cells                  [][3]string // row id, column id, work node id
	hasText                bool
}

// Row is one feature of an area.
type Row struct {
	ID, Title string
	Later     bool
}

// Column is one column of an area: "done", or one value of its axis.
type Column struct {
	ID, Title string
}

// problem is every data problem: one line naming the file and what to fix.
func problem(file, format string, a ...any) error {
	return fmt.Errorf("roadmap: file=%s %s", file, fmt.Sprintf(format, a...))
}

func read(file string, data []byte) (sexp.Form, error) {
	top, err := sexp.Read(file, data, Limits)
	if err != nil {
		var r *sexp.Refusal
		if errors.As(err, &r) {
			return top, fmt.Errorf("roadmap: file=%s: %s", file, r.Reason)
		}
		return top, err
	}
	return top, nil
}

// plist reads a (:key value ...) list: an even length, keyword keys, none twice.
func plist(file, at string, f sexp.Form) (map[string]sexp.Form, error) {
	if f.Kind != sexp.List || len(f.List)%2 != 0 {
		return nil, problem(file, "%s: want a (:key value ...) property list", at)
	}
	out := map[string]sexp.Form{}
	for i := 0; i < len(f.List); i += 2 {
		k := f.List[i]
		if k.Kind != sexp.Keyword {
			return nil, problem(file, "%s: want a :key at byte=%d", at, k.Offset)
		}
		if _, dup := out[k.Value]; dup {
			return nil, problem(file, "%s: key :%s stands twice", at, k.Value)
		}
		out[k.Value] = f.List[i+1]
	}
	return out, nil
}

// isNil is an absent value: no key, the symbol nil, or ().
func isNil(f sexp.Form, ok bool) bool {
	return !ok || (f.Kind == sexp.Symbol && strings.EqualFold(f.Value, "nil")) || (f.Kind == sexp.List && len(f.List) == 0)
}

// isBlank is the whitespace text is trimmed and joined on: blank, tab, newline,
// return and form feed, exactly, so a no-break blank in a title stays text.
func isBlank(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\f' }

func nonBlank(s string) bool { return strings.TrimFunc(s, isBlank) != "" }

// flatText joins every run of blank into one and trims the ends.
func flatText(s string) string { return strings.Join(strings.FieldsFunc(s, isBlank), " ") }

func strVal(m map[string]sexp.Form, k string) (string, bool) {
	f, ok := m[k]
	if !ok || f.Kind != sexp.String {
		return "", false
	}
	return f.Value, true
}

// strList returns the list of strings under k; nil when absent.
func strList(file, at string, m map[string]sexp.Form, k string) ([]string, error) {
	f, ok := m[k]
	if isNil(f, ok) {
		return nil, nil
	}
	if f.Kind != sexp.List {
		return nil, problem(file, "%s: :%s wants a list of strings", at, k)
	}
	out := make([]string, 0, len(f.List))
	for _, s := range f.List {
		if s.Kind != sexp.String {
			return nil, problem(file, "%s: :%s wants a list of strings", at, k)
		}
		out = append(out, s.Value)
	}
	return out, nil
}

var states = map[string]bool{"unknown": true, "todo": true, "doing": true, "done": true}

// checkTask is the rule a task is held to: a known state, and evidence when it is done.
func checkTask(file string, n *Node) error {
	if !states[n.State] {
		return problem(file, "task %q has state :%s; want :unknown, :todo, :doing or :done", n.ID, n.State)
	}
	if n.State == "done" {
		for _, e := range n.Evidence {
			if nonBlank(e) {
				return nil
			}
		}
		return problem(file, "done task %q has no :evidence; name the test or commit that proves it", n.ID)
	}
	return nil
}

// pair reads ("<id>" "<title>" ...) for a row or a column.
func pair(file, at string, f sexp.Form) (id, title string, rest []sexp.Form, err error) {
	if f.Kind != sexp.List || len(f.List) < 2 || f.List[0].Kind != sexp.String || f.List[1].Kind != sexp.String {
		return "", "", nil, problem(file, "%s: want (\"<id>\" \"<title>\")", at)
	}
	id, title = f.List[0].Value, f.List[1].Value
	if strings.ContainsAny(title, "|\n\r") {
		return "", "", nil, problem(file, "%s: title %q holds | or a newline, which would break the table", at, title)
	}
	return id, title, f.List[2:], nil
}

// Decode reads one data file and checks the whole graph: every reference
// resolves, every area's table is complete, no cycle, every done task carries
// evidence, and :page names areas reachable from :root, none carrying prose.
func Decode(file string, data []byte) (*Roadmap, error) {
	top, err := read(file, data)
	if err != nil {
		return nil, err
	}
	root, err := plist(file, "top", top)
	if err != nil {
		return nil, err
	}
	if f, ok := root["schema"]; !ok || f.Kind != sexp.Integer || f.Int != 1 {
		return nil, problem(file, "top: want :schema 1")
	}
	rm := &Roadmap{Nodes: map[string]*Node{}}
	if rm.Root, _ = strVal(root, "root"); strings.TrimSpace(rm.Root) == "" {
		return nil, problem(file, "top: want a non-empty string :root")
	}
	nodes, ok := root["nodes"]
	if !ok || nodes.Kind != sexp.List || len(nodes.List) == 0 {
		return nil, problem(file, "top: :nodes must be a non-empty list")
	}
	if len(nodes.List) > MaxNodes {
		return nil, problem(file, "top: %d nodes is past the bound of %d", len(nodes.List), MaxNodes)
	}
	for i, nf := range nodes.List {
		n, err := decodeNode(file, fmt.Sprintf(":nodes[%d]", i), nf)
		if err != nil {
			return nil, err
		}
		if _, dup := rm.Nodes[n.ID]; dup {
			return nil, problem(file, "node %q: the id stands twice", n.ID)
		}
		rm.Nodes[n.ID] = n
	}
	if rm.Nodes[rm.Root] == nil {
		return nil, problem(file, "top: :root %q names no node", rm.Root)
	}
	ids := make([]string, 0, len(rm.Nodes))
	for id := range rm.Nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		n := rm.Nodes[id]
		for _, c := range n.Children {
			if rm.Nodes[c] == nil {
				return nil, problem(file, "work-set %q: child %q names no node", id, c)
			}
		}
		for _, c := range n.Cells {
			if rm.Nodes[c[2]] == nil {
				return nil, problem(file, "area %q: cell (%q, %q) names no node %q", id, c[0], c[1], c[2])
			}
		}
	}
	memo := map[string][]string{}
	for _, id := range ids {
		if _, err := rm.leaves(file, id, memo, nil); err != nil {
			return nil, err
		}
	}
	if err := decodePage(file, root, rm); err != nil {
		return nil, err
	}
	return rm, nil
}

func decodeNode(file, at string, nf sexp.Form) (*Node, error) {
	m, err := plist(file, at, nf)
	if err != nil {
		return nil, err
	}
	n := &Node{}
	if n.ID, _ = strVal(m, "id"); strings.TrimSpace(n.ID) == "" {
		return nil, problem(file, "%s: want a non-empty string :id", at)
	}
	at = fmt.Sprintf("node %q", n.ID)
	if t, ok := m["type"]; ok && t.Kind == sexp.Keyword {
		n.Type = t.Value
	}
	n.Title, _ = strVal(m, "title")
	if impl, ok := m["implementation"]; !isNil(impl, ok) {
		ev, err := strList(file, at, m, "implementation-evidence")
		valid := err == nil && impl.Kind == sexp.Keyword && (impl.Value == "implemented" || impl.Value == "partial" || impl.Value == "unsupported") && len(ev) > 0
		for _, e := range ev {
			valid = valid && nonBlank(e)
		}
		if !valid {
			return nil, problem(file, "%s: :implementation wants :implemented, :partial or :unsupported with non-blank :implementation-evidence", at)
		}
	}
	switch n.Type {
	case "task":
		if s, ok := m["state"]; ok && s.Kind == sexp.Keyword {
			n.State = s.Value
		}
		if n.Evidence, err = strList(file, at, m, "evidence"); err != nil {
			return nil, err
		}
		if err := checkTask(file, n); err != nil {
			return nil, err
		}
	case "work-set":
		if n.Children, err = strList(file, at, m, "children"); err != nil {
			return nil, err
		}
		if len(n.Children) == 0 {
			return nil, problem(file, "%s: a work-set wants a non-empty :children", at)
		}
	case "roadmap":
		t, ok := m["text"]
		n.hasText = !isNil(t, ok)
		if err := decodeArea(file, at, m, n); err != nil {
			return nil, err
		}
	default:
		return nil, problem(file, "%s: :type wants :task, :work-set or :roadmap", at)
	}
	return n, nil
}

func decodeArea(file, at string, m map[string]sexp.Form, n *Node) error {
	list := func(k string) ([]sexp.Form, error) {
		f, ok := m[k]
		if !ok || f.Kind != sexp.List || len(f.List) == 0 {
			return nil, problem(file, "%s: an area wants a non-empty :%s", at, k)
		}
		return f.List, nil
	}
	rows, err := list("rows")
	if err != nil {
		return err
	}
	cols, err := list("columns")
	if err != nil {
		return err
	}
	cells, err := list("cells")
	if err != nil {
		return err
	}
	rowIDs, colIDs := map[string]bool{}, map[string]bool{}
	for _, f := range rows {
		id, title, rest, err := pair(file, at+" row", f)
		if err != nil {
			return err
		}
		if rowIDs[id] {
			return problem(file, "%s: row %q stands twice", at, id)
		}
		rowIDs[id] = true
		r := Row{ID: id, Title: title}
		if len(rest) > 0 {
			opts, err := plist(file, at+" row "+id, sexp.Form{Kind: sexp.List, List: rest})
			if err != nil {
				return err
			}
			for k, v := range opts {
				if k != "later" {
					return problem(file, "%s row %q: unknown key :%s (a row takes only :later t)", at, id, k)
				}
				r.Later = !isNil(v, true)
			}
		}
		n.Rows = append(n.Rows, r)
	}
	for _, f := range cols {
		id, title, _, err := pair(file, at+" column", f)
		if err != nil {
			return err
		}
		if colIDs[id] {
			return problem(file, "%s: column %q stands twice", at, id)
		}
		colIDs[id] = true
		n.Columns = append(n.Columns, Column{ID: id, Title: title})
	}
	seen := map[[2]string]bool{}
	for _, f := range cells {
		if f.Kind != sexp.List || len(f.List) < 3 || f.List[0].Kind != sexp.String || f.List[1].Kind != sexp.String || f.List[2].Kind != sexp.String {
			return problem(file, "%s: want a cell (\"<row>\" \"<column>\" \"<work node>\")", at)
		}
		c := [3]string{f.List[0].Value, f.List[1].Value, f.List[2].Value}
		if !rowIDs[c[0]] || !colIDs[c[1]] {
			return problem(file, "%s: cell (%q, %q) names an unknown row or column", at, c[0], c[1])
		}
		if seen[[2]string{c[0], c[1]}] {
			return problem(file, "%s: cell (%q, %q) stands twice", at, c[0], c[1])
		}
		seen[[2]string{c[0], c[1]}] = true
		n.Cells = append(n.Cells, c)
	}
	for _, r := range n.Rows {
		for _, c := range n.Columns {
			if !seen[[2]string{r.ID, c.ID}] {
				return problem(file, "%s: no cell for row %q and column %q", at, r.ID, c.ID)
			}
		}
	}
	return nil
}

// decodePage checks :page: it holds :roadmaps only, naming areas reachable from
// :root once each, none of which carries :text. Headings and prose live in
// ROADMAP.md, written by hand.
func decodePage(file string, root map[string]sexp.Form, rm *Roadmap) error {
	pf, ok := root["page"]
	if !ok {
		return problem(file, "top: want :page (:roadmaps (\"<area id>\" ...)), the areas in page order")
	}
	page, err := plist(file, ":page", pf)
	if err != nil {
		return err
	}
	for k := range page {
		if k != "roadmaps" {
			return problem(file, ":page: carries :%s; it holds :roadmaps only (headings and prose live in ROADMAP.md, written by hand)", k)
		}
	}
	if rm.Areas, err = strList(file, ":page", page, "roadmaps"); err != nil || len(rm.Areas) == 0 {
		return problem(file, ":page: :roadmaps wants a non-empty list of area ids")
	}
	reach := rm.reachable()
	seen := map[string]bool{}
	for _, id := range rm.Areas {
		if seen[id] {
			return problem(file, ":page: :roadmaps names %q twice", id)
		}
		seen[id] = true
		n := rm.Nodes[id]
		if n == nil || n.Type != "roadmap" || !reach[id] {
			return problem(file, ":page: :roadmaps names %q, which is no area reachable from :root %q", id, rm.Root)
		}
		if n.hasText {
			return problem(file, "area %q carries :text; prose lives in ROADMAP.md, written by hand, never in the data", id)
		}
	}
	return nil
}

// reachable is every node reached from :root through work-set children and area cells.
func (rm *Roadmap) reachable() map[string]bool {
	seen := map[string]bool{}
	var walk func(string)
	walk = func(id string) {
		n := rm.Nodes[id]
		if seen[id] || n == nil {
			return
		}
		seen[id] = true
		for _, c := range n.Children {
			walk(c)
		}
		for _, c := range n.Cells {
			walk(c[2])
		}
	}
	walk(rm.Root)
	return seen
}

// leaves returns the task ids under a node, each once, sorted; a cycle or a walk
// past MaxDepth is refused.
func (rm *Roadmap) leaves(file, id string, memo map[string][]string, path []string) ([]string, error) {
	if len(path) > MaxDepth {
		return nil, problem(file, "the walk from %q is past the depth bound of %d", path[0], MaxDepth)
	}
	if slices.Contains(path, id) {
		return nil, problem(file, "cycle: %s -> %s", strings.Join(path, " -> "), id)
	}
	if l, ok := memo[id]; ok {
		return l, nil
	}
	n := rm.Nodes[id]
	var refs []string
	switch n.Type {
	case "task":
		memo[id] = []string{id}
		return memo[id], nil
	case "work-set":
		refs = n.Children
	default:
		for _, c := range n.Cells {
			refs = append(refs, c[2])
		}
	}
	set := map[string]bool{}
	below := append(append([]string(nil), path...), id)
	for _, r := range refs {
		l, err := rm.leaves(file, r, memo, below)
		if err != nil {
			return nil, err
		}
		for _, t := range l {
			set[t] = true
		}
	}
	out := make([]string, 0, len(set))
	for t := range set {
		out = append(out, t)
	}
	sort.Strings(out)
	memo[id] = out
	return out, nil
}

// ApplyEvidence applies one overlay file: any number of (:id ... :state ...)
// forms, each moving one task's :state, and its :evidence when given, held to the
// task rule. Other keys (:note, :tested-revision) are kept for people and not
// rendered. It returns the entries applied; a refused file changes nothing.
func (rm *Roadmap) ApplyEvidence(file string, data []byte) (int, error) {
	// The reader takes one form; the file holds many, so it is read as one list.
	// Byte offsets in a refusal are therefore two past the file's own.
	wrapped := append(append([]byte("(\n"), data...), "\n)"...)
	top, err := read(file, wrapped)
	if err != nil {
		return 0, err
	}
	type move struct {
		n     *Node
		state string
		ev    []string
	}
	var moves []move
	for i, f := range top.List {
		at := fmt.Sprintf("entry %d", i+1)
		m, err := plist(file, at, f)
		if err != nil {
			return 0, err
		}
		id, _ := strVal(m, "id")
		if !nonBlank(id) {
			return 0, problem(file, "%s: want a non-empty string :id", at)
		}
		n := rm.Nodes[id]
		if n == nil {
			return 0, problem(file, "%s: id %q is no node of the roadmap", at, id)
		}
		if n.Type != "task" {
			return 0, problem(file, "%s: id %q is a %s, not a task", at, id, n.Type)
		}
		s, ok := m["state"]
		if isNil(s, ok) || s.Kind != sexp.Keyword {
			return 0, problem(file, "%s: id %q carries no :state", at, id)
		}
		ev, err := strList(file, at, m, "evidence")
		if err != nil {
			return 0, err
		}
		if ev == nil {
			ev = n.Evidence
		}
		if err := checkTask(file, &Node{ID: id, State: s.Value, Evidence: ev}); err != nil {
			return 0, err
		}
		moves = append(moves, move{n, s.Value, ev})
	}
	for _, mv := range moves {
		mv.n.State, mv.n.Evidence = mv.state, mv.ev
	}
	return len(moves), nil
}

// ApplyEvidenceDir applies every *.sexp under dir whose name does not start with
// a dot, in name order. It returns the files and entries applied.
func (rm *Roadmap) ApplyEvidenceDir(dir string) (files, entries int, err error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.sexp"))
	if err != nil {
		return 0, 0, err
	}
	sort.Strings(paths)
	for _, p := range paths {
		if strings.HasPrefix(filepath.Base(p), ".") {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return files, entries, err
		}
		n, err := rm.ApplyEvidence(p, data)
		if err != nil {
			return files, entries, err
		}
		files++
		entries += n
	}
	return files, entries, nil
}

// Cell reports whether every task under a node is done: the one rule for ✅.
func (rm *Roadmap) Cell(id string) bool {
	l, err := rm.leaves("", id, map[string][]string{}, nil)
	if err != nil {
		return false
	}
	for _, t := range l {
		if rm.Nodes[t].State != "done" {
			return false
		}
	}
	return true
}

// Rows is the number of rows across the page's areas.
func (rm *Roadmap) Rows() int {
	n := 0
	for _, id := range rm.Areas {
		n += len(rm.Nodes[id].Rows)
	}
	return n
}

// Table is one area's table, its complete row and its source link: what sits
// between the area's markers.
func (rm *Roadmap) Table(id, source string) string {
	n := rm.Nodes[id]
	var b strings.Builder
	green := map[[2]string]bool{}
	for _, c := range n.Cells {
		green[[2]string{c[0], c[1]}] = rm.Cell(c[2])
	}
	b.WriteString("| feature")
	for _, c := range n.Columns {
		fmt.Fprintf(&b, " | %s", c.Title)
	}
	b.WriteString(" |\n|---")
	for range n.Columns {
		b.WriteString("|:---:")
	}
	b.WriteString("|\n")
	for _, r := range n.Rows {
		title := r.Title
		if r.Later {
			title += " (later)"
		}
		fmt.Fprintf(&b, "| %s", title)
		for _, c := range n.Columns {
			mark := "❌"
			if green[[2]string{r.ID, c.ID}] {
				mark = "✅"
			}
			fmt.Fprintf(&b, " | %s", mark)
		}
		b.WriteString(" |\n")
	}
	b.WriteString("| complete")
	for _, c := range n.Columns {
		g := 0
		for _, r := range n.Rows {
			if green[[2]string{r.ID, c.ID}] {
				g++
			}
		}
		fmt.Fprintf(&b, " | %d%%", 100*g/len(n.Rows))
	}
	fmt.Fprintf(&b, " |\n\n[Source data](%s)\n", source)
	return b.String()
}

// The markers: each on a line of its own, exactly once.
const markerPrefix = "<!-- nova-work:"

// StartMarker is the line that opens an area's table block.
func StartMarker(id string) string { return markerPrefix + id + ":start -->" }

// EndMarker is the line that closes an area's table block.
func EndMarker(id string) string { return markerPrefix + id + ":end -->" }

func isMarkerLine(line string) bool {
	return len(line) > len(markerPrefix)+len(" -->") && strings.HasPrefix(line, markerPrefix) && strings.HasSuffix(line, " -->")
}

// findMarkerLine is the start of the one line of text that is exactly marker; a
// marker that is missing, repeated, or shares its line with other text is refused.
func findMarkerLine(text, marker string) (int, error) {
	found := -1
	for from := 0; ; {
		i := strings.Index(text[from:], marker)
		if i < 0 {
			break
		}
		pos := from + i
		start := strings.LastIndexByte(text[:pos], '\n') + 1
		end := len(text)
		if nl := strings.IndexByte(text[pos:], '\n'); nl >= 0 {
			end = pos + nl
		}
		if start != pos || end != pos+len(marker) {
			return 0, fmt.Errorf("%q shares its line with other text", marker)
		}
		if found >= 0 {
			return 0, fmt.Errorf("%q appears more than once", marker)
		}
		found = start
		from = pos + len(marker)
	}
	if found < 0 {
		return 0, fmt.Errorf("%q is missing from the page", marker)
	}
	return found, nil
}

// spliceBlock is text with block id's contents replaced by table; everything
// outside the block's two marker lines is returned unchanged.
func spliceBlock(text, id, table string) (string, error) {
	start, err := findMarkerLine(text, StartMarker(id))
	if err != nil {
		return "", err
	}
	inner := start + len(StartMarker(id))
	end, err := findMarkerLine(text, EndMarker(id))
	if err != nil {
		return "", err
	}
	if start >= end {
		return "", fmt.Errorf("block %q opens after it closes", id)
	}
	if strings.Contains(text[inner:end], markerPrefix) {
		return "", fmt.Errorf("block %q holds another marker", id)
	}
	return text[:inner] + "\n\n" + strings.Trim(table, "\n \t") + "\n\n" + text[end:], nil
}

// lines splits text after each newline, keeping it.
func lines(text string) []string {
	return strings.SplitAfter(text, "\n")
}

// HandWritten is text with the contents of every block removed and its marker
// lines kept: the part of the page that is written by hand.
func HandWritten(text string) string {
	var b strings.Builder
	inside := false
	for _, l := range lines(text) {
		line := strings.TrimSuffix(l, "\n")
		switch {
		case isMarkerLine(line):
			b.WriteString(l)
			inside = strings.HasSuffix(line, ":start -->")
		case !inside:
			b.WriteString(l)
		}
	}
	return b.String()
}

// Blocks is the ids of the start markers in text, in page order.
func Blocks(text string) []string {
	var ids []string
	for _, l := range lines(text) {
		line := strings.TrimSuffix(l, "\n")
		if isMarkerLine(line) && strings.HasSuffix(line, ":start -->") {
			ids = append(ids, line[len(markerPrefix):len(line)-len(":start -->")])
		}
	}
	return ids
}

// Splice is page with every area's table re-rendered from the data, in :page
// order. A page whose blocks are not exactly the :page areas in order is
// refused, and so is a result whose hand-written part differs from page's.
// source is the data file as the [Source data] link names it.
func (rm *Roadmap) Splice(page, source string) (string, error) {
	if have := Blocks(page); !slices.Equal(have, rm.Areas) {
		return "", fmt.Errorf("the page carries blocks %q; the data's :page :roadmaps are %q: give each area its own pair of marker lines, in that order (nova-roadmap help form)", have, rm.Areas)
	}
	text := page
	for _, id := range rm.Areas {
		var err error
		if text, err = spliceBlock(text, id, rm.Table(id, source)); err != nil {
			return "", err
		}
	}
	if HandWritten(text) != HandWritten(page) {
		return "", errors.New("splicing changed the page outside the nova-work markers")
	}
	return text, nil
}

// Starter is a first page for the data: a title, a notice, and for each area a
// heading (its :title, or its id) and its block. It is the page render writes
// when there is none; after that, everything outside the markers is the
// author's.
func (rm *Roadmap) Starter(source string) (string, error) {
	var b strings.Builder
	b.WriteString("# Roadmap\n\n")
	fmt.Fprintf(&b, "<!-- The tables between the nova-work markers are generated from %s by nova-roadmap: edit the data and run `nova-roadmap render`. Everything else on this page is written by hand. -->\n", source)
	for _, id := range rm.Areas {
		heading := flatText(rm.Nodes[id].Title)
		if heading == "" {
			heading = id
		}
		fmt.Fprintf(&b, "\n## %s\n\n%s\n%s\n", heading, StartMarker(id), EndMarker(id))
	}
	return rm.Splice(b.String(), source)
}

// Load reads the data file, decodes it and applies the evidence overlay:
// evidence names the overlay directory, or "" for roadmap-evidence/ beside file.
// A missing default is no overlay; a missing named directory is refused. It
// returns the roadmap and the overlay files and entries applied.
func Load(file, evidence string) (rm *Roadmap, files, entries int, err error) {
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("reading %s: %v", file, err)
	}
	if rm, err = Decode(file, raw); err != nil {
		return nil, 0, 0, err
	}
	dir := evidence
	if dir == "" {
		dir = filepath.Join(filepath.Dir(file), EvidenceDir)
	}
	if st, err := os.Stat(dir); err == nil && st.IsDir() {
		if files, entries, err = rm.ApplyEvidenceDir(dir); err != nil {
			return nil, 0, 0, err
		}
	} else if evidence != "" {
		return nil, 0, 0, fmt.Errorf("--evidence %s is not a directory", evidence)
	}
	return rm, files, entries, nil
}
