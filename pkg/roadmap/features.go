package roadmap

// The feature-table form: the standard every ROADMAP.md is moving to (owner,
// 2026-10-17), first built as a Lisp renderer in the schema repository and folded
// in here so there is one generator. A page is a series of areas; each
// area is one ## section, a line saying what the area is, and a table with one row
// per feature a user would recognise, one ✅/❌ cell per column (a single "done"
// column, or an axis such as languages or platforms), a "complete" row, and a link
// to the source data. Evidence lives in the data, never in the table.
//
// The data is one restricted plist (read through pkg/sexp, nothing evaluated):
//
//	(:schema 1
//	 :root "<id of the node every area is reached from>"
//	 :page (:title "<page title>"
//	        :roadmaps ("<area id>" ...)            ; the areas, in page order
//	        :closing ((:title "..." :text "...") ...)) ; optional closing sections
//	 :nodes
//	 ((:id "<id>" :type :task :title "..." :state :done :evidence ("<test or commit>" ...))
//	  (:id "<id>" :type :task :title "..." :state :todo)          ; :unknown :todo :doing :done
//	  (:id "<id>" :type :work-set :children ("<id>" ...))          ; a cell may need several tasks
//	  (:id "<area id>" :type :roadmap :title "<heading>" :text "<what the area is>"
//	   :rows (("<row id>" "<feature>") ("<row id>" "<feature>" :later t) ...)
//	   :columns (("done" "done"))                                 ; or one per language, platform ...
//	   :cells (("<row id>" "<column id>" "<task or work-set id>") ...))))
//
// A cell is ✅ only when every task under its node is :done, and a :done task must
// carry non-blank :evidence. Nodes may carry any other keys (notes, sprint metadata);
// they are data for people and other tools and are not rendered. A row marked
// :later t is a feature deliberately left for later and renders "(later)" after its
// name; it still counts in the complete row.
//
// docs/roadmap-evidence/*.sexp beside the data, when present, is the evidence
// overlay: any number of (:id "<task id>" :state :done :evidence (...)) forms, applied
// in file-name order, each moving one task's state, evidence and note, and held to
// the same rule. The cards that prove a task write there, so the graph is not
// rewritten for every proof.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/sexp"
)

// FeatureLimits bounds a feature-form read: 5 MiB per file (schema's graph, the
// largest, is about 1.2 MiB), nesting with room to spare, at most one atom per
// byte, at most MaxFeatureNodes graph nodes and a work-graph walk at most
// MaxFeatureDepth deep.
var FeatureLimits = sexp.Limits{MaxBytes: 5 << 20, MaxDepth: 64, MaxNodes: 5 << 20}

const (
	MaxFeatureNodes = 10000
	MaxFeatureDepth = 64
	// EvidenceDir is the overlay directory's name, beside the data file.
	EvidenceDir = "roadmap-evidence"
)

// Features is a decoded feature-form roadmap.
type Features struct {
	Root     string
	Title    string
	Areas    []string // the :page :roadmaps ids, in page order
	Closing  []Section
	Nodes    map[string]*Node
	Overlays int // overlay entries applied
}

// Section is one closing section of the page.
type Section struct {
	Title string
	Text  []string
}

// Node is one node of the work graph.
type Node struct {
	ID, Type, Title, State string
	Text                   []string
	Evidence               []string
	Children               []string
	Rows                   []Row
	Columns                []Column
	Cells                  [][3]string // row id, column id, work node id
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

// IsFeatureForm reports whether data is the feature form (a top-level plist,
// `(:schema 1 ...`) rather than the card-list form `(roadmap "v1" ...)`. A file
// neither reads as is reported false, and Decode names what is wrong with it.
func IsFeatureForm(file string, data []byte) bool {
	top, err := sexp.Read(file, data, FeatureLimits)
	return err == nil && top.Kind == sexp.List && len(top.List) > 0 && top.List[0].Kind == sexp.Keyword
}

// featureError is every feature-form problem: one line naming the file and what to fix.
func featureError(file, format string, a ...any) error {
	return fmt.Errorf("roadmap: file=%s %s", file, fmt.Sprintf(format, a...))
}

func readFeatureForm(file string, data []byte) (sexp.Form, error) {
	top, err := sexp.Read(file, data, FeatureLimits)
	if err != nil {
		var r *sexp.Refusal
		if errors.As(err, &r) {
			return top, fmt.Errorf("roadmap: file=%s: %s", file, strings.ReplaceAll(r.Reason, "a plan is data", "a roadmap is data"))
		}
		return top, err
	}
	return top, nil
}

// plist reads a (:key value ...) list: an even length, keyword keys, none twice.
func plist(file, at string, f sexp.Form) (map[string]sexp.Form, error) {
	if f.Kind != sexp.List || len(f.List)%2 != 0 {
		return nil, featureError(file, "%s: want a (:key value ...) property list", at)
	}
	out := map[string]sexp.Form{}
	for i := 0; i < len(f.List); i += 2 {
		k := f.List[i]
		if k.Kind != sexp.Keyword {
			return nil, featureError(file, "%s: want a :key at byte=%d", at, k.Offset)
		}
		if _, dup := out[k.Value]; dup {
			return nil, featureError(file, "%s: key :%s stands twice", at, k.Value)
		}
		out[k.Value] = f.List[i+1]
	}
	return out, nil
}

// isNil is an absent value: no key, the symbol nil, or ().
func isNil(f sexp.Form, ok bool) bool {
	return !ok || (f.Kind == sexp.Symbol && strings.EqualFold(f.Value, "nil")) || (f.Kind == sexp.List && len(f.List) == 0)
}

// blank is the whitespace the data's text is trimmed and joined on: blank, tab,
// newline, return and form feed, exactly, so a no-break blank in a title stays text.
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

// strings returns the list of strings under k; nil when absent.
func strList(file, at string, m map[string]sexp.Form, k string) ([]string, error) {
	f, ok := m[k]
	if isNil(f, ok) {
		return nil, nil
	}
	if f.Kind != sexp.List {
		return nil, featureError(file, "%s: :%s wants a list of strings", at, k)
	}
	out := make([]string, 0, len(f.List))
	for _, s := range f.List {
		if s.Kind != sexp.String {
			return nil, featureError(file, "%s: :%s wants a list of strings", at, k)
		}
		out = append(out, s.Value)
	}
	return out, nil
}

// paragraphs is :text: a string is one paragraph, a list of strings several, absent none.
func paragraphs(file, at string, m map[string]sexp.Form) ([]string, error) {
	if f, ok := m["text"]; ok && f.Kind == sexp.String {
		return []string{flatText(f.Value)}, nil
	}
	l, err := strList(file, at, m, "text")
	if err != nil {
		return nil, featureError(file, "%s: :text must be a string or a list of strings", at)
	}
	for i := range l {
		l[i] = flatText(l[i])
	}
	return l, nil
}

var states = map[string]bool{"unknown": true, "todo": true, "doing": true, "done": true}

// checkTask is the rule a task is held to: a known state, and evidence when it is done.
func checkTask(file string, n *Node) error {
	if !states[n.State] {
		return featureError(file, "task %q has state :%s; want :unknown, :todo, :doing or :done", n.ID, n.State)
	}
	if n.State == "done" {
		for _, e := range n.Evidence {
			if nonBlank(e) {
				return nil
			}
		}
		return featureError(file, "done task %q has no :evidence; name the test or commit that proves it", n.ID)
	}
	return nil
}

// pair reads ("<id>" "<title>" ...) for a row or a column.
func pair(file, at string, f sexp.Form) (id, title string, rest []sexp.Form, err error) {
	if f.Kind != sexp.List || len(f.List) < 2 || f.List[0].Kind != sexp.String || f.List[1].Kind != sexp.String {
		return "", "", nil, featureError(file, "%s: want (\"<id>\" \"<title>\")", at)
	}
	id, title = f.List[0].Value, f.List[1].Value
	if strings.ContainsAny(title, "|\n\r") {
		return "", "", nil, featureError(file, "%s: title %q holds | or a newline, which would break the table", at, title)
	}
	return id, title, f.List[2:], nil
}

// DecodeFeatures reads one feature-form file and checks the whole graph: every
// reference resolves, every area's table is complete, no cycle, every done task
// carries evidence. The page itself (its areas reachable from :root) is checked by
// RenderFeatures.
func DecodeFeatures(file string, data []byte) (*Features, error) {
	top, err := readFeatureForm(file, data)
	if err != nil {
		return nil, err
	}
	root, err := plist(file, "top", top)
	if err != nil {
		return nil, err
	}
	if f, ok := root["schema"]; !ok || f.Kind != sexp.Integer || f.Int != 1 {
		return nil, featureError(file, "top: want :schema 1")
	}
	fs := &Features{Nodes: map[string]*Node{}}
	if fs.Root, _ = strVal(root, "root"); strings.TrimSpace(fs.Root) == "" {
		return nil, featureError(file, "top: want a non-empty string :root")
	}
	nodes, ok := root["nodes"]
	if !ok || nodes.Kind != sexp.List || len(nodes.List) == 0 {
		return nil, featureError(file, "top: :nodes must be a non-empty list")
	}
	if len(nodes.List) > MaxFeatureNodes {
		return nil, featureError(file, "top: %d nodes is past the bound of %d", len(nodes.List), MaxFeatureNodes)
	}
	for i, nf := range nodes.List {
		at := fmt.Sprintf(":nodes[%d]", i)
		m, err := plist(file, at, nf)
		if err != nil {
			return nil, err
		}
		n := &Node{}
		if n.ID, _ = strVal(m, "id"); strings.TrimSpace(n.ID) == "" {
			return nil, featureError(file, "%s: want a non-empty string :id", at)
		}
		at = "node " + fmt.Sprintf("%q", n.ID)
		if _, dup := fs.Nodes[n.ID]; dup {
			return nil, featureError(file, "%s: the id stands twice", at)
		}
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
				return nil, featureError(file, "%s: :implementation wants :implemented, :partial or :unsupported with non-blank :implementation-evidence", at)
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
				return nil, featureError(file, "%s: a work-set wants a non-empty :children", at)
			}
		case "roadmap":
			if n.Text, err = paragraphs(file, at, m); err != nil {
				return nil, err
			}
			if err := decodeArea(file, at, m, n); err != nil {
				return nil, err
			}
		default:
			return nil, featureError(file, "%s: :type wants :task, :work-set or :roadmap", at)
		}
		fs.Nodes[n.ID] = n
	}
	if fs.Nodes[fs.Root] == nil {
		return nil, featureError(file, "top: :root %q names no node", fs.Root)
	}
	ids := make([]string, 0, len(fs.Nodes))
	for id := range fs.Nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		n := fs.Nodes[id]
		for _, c := range n.Children {
			if fs.Nodes[c] == nil {
				return nil, featureError(file, "work-set %q: child %q names no node", id, c)
			}
		}
		for _, c := range n.Cells {
			if fs.Nodes[c[2]] == nil {
				return nil, featureError(file, "area %q: cell (%q, %q) names no node %q", id, c[0], c[1], c[2])
			}
		}
	}
	memo := map[string][]string{}
	for _, id := range ids {
		if _, err := fs.leaves(file, id, memo, nil); err != nil {
			return nil, err
		}
	}
	if err := decodePage(file, root, fs); err != nil {
		return nil, err
	}
	return fs, nil
}

func decodeArea(file, at string, m map[string]sexp.Form, n *Node) error {
	list := func(k string) ([]sexp.Form, error) {
		f, ok := m[k]
		if !ok || f.Kind != sexp.List || len(f.List) == 0 {
			return nil, featureError(file, "%s: an area wants a non-empty :%s", at, k)
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
			return featureError(file, "%s: row %q stands twice", at, id)
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
					return featureError(file, "%s row %q: unknown key :%s (a row takes only :later t)", at, id, k)
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
			return featureError(file, "%s: column %q stands twice", at, id)
		}
		colIDs[id] = true
		n.Columns = append(n.Columns, Column{ID: id, Title: title})
	}
	seen := map[[2]string]bool{}
	for _, f := range cells {
		if f.Kind != sexp.List || len(f.List) < 3 || f.List[0].Kind != sexp.String || f.List[1].Kind != sexp.String || f.List[2].Kind != sexp.String {
			return featureError(file, "%s: want a cell (\"<row>\" \"<column>\" \"<work node>\")", at)
		}
		c := [3]string{f.List[0].Value, f.List[1].Value, f.List[2].Value}
		if !rowIDs[c[0]] || !colIDs[c[1]] {
			return featureError(file, "%s: cell (%q, %q) names an unknown row or column", at, c[0], c[1])
		}
		if seen[[2]string{c[0], c[1]}] {
			return featureError(file, "%s: cell (%q, %q) stands twice", at, c[0], c[1])
		}
		seen[[2]string{c[0], c[1]}] = true
		n.Cells = append(n.Cells, c)
	}
	for _, r := range n.Rows {
		for _, c := range n.Columns {
			if !seen[[2]string{r.ID, c.ID}] {
				return featureError(file, "%s: no cell for row %q and column %q", at, r.ID, c.ID)
			}
		}
	}
	return nil
}

func decodePage(file string, root map[string]sexp.Form, fs *Features) error {
	pf, ok := root["page"]
	if !ok {
		return featureError(file, "top: want :page (the title, the areas in order, the closing sections)")
	}
	page, err := plist(file, ":page", pf)
	if err != nil {
		return err
	}
	if fs.Title, _ = strVal(page, "title"); !nonBlank(fs.Title) {
		return featureError(file, ":page: want a non-empty :title")
	}
	if fs.Areas, err = strList(file, ":page", page, "roadmaps"); err != nil || len(fs.Areas) == 0 {
		return featureError(file, ":page: :roadmaps wants a non-empty list of area ids")
	}
	reach := fs.reachable()
	seen := map[string]bool{}
	for _, id := range fs.Areas {
		if seen[id] {
			return featureError(file, ":page: :roadmaps names %q twice", id)
		}
		seen[id] = true
		if n := fs.Nodes[id]; n == nil || n.Type != "roadmap" || !reach[id] {
			return featureError(file, ":page: :roadmaps names %q, which is no area reachable from :root %q", id, fs.Root)
		}
		if !nonBlank(fs.Nodes[id].Title) {
			return featureError(file, "area %q: want a non-empty :title", id)
		}
	}
	cf, ok := page["closing"]
	if isNil(cf, ok) {
		return nil
	}
	if cf.Kind != sexp.List {
		return featureError(file, ":page: :closing must be a list")
	}
	for i, sf := range cf.List {
		at := fmt.Sprintf(":page :closing[%d]", i)
		m, err := plist(file, at, sf)
		if err != nil {
			return err
		}
		s := Section{}
		if s.Title, _ = strVal(m, "title"); !nonBlank(s.Title) {
			return featureError(file, "%s: want a non-empty :title", at)
		}
		if s.Text, err = paragraphs(file, at, m); err != nil {
			return err
		}
		if len(s.Text) == 0 {
			return featureError(file, "%s: want :text", at)
		}
		fs.Closing = append(fs.Closing, s)
	}
	return nil
}

// reachable is every node reached from :root through work-set children and area cells.
func (fs *Features) reachable() map[string]bool {
	seen := map[string]bool{}
	var walk func(string)
	walk = func(id string) {
		n := fs.Nodes[id]
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
	walk(fs.Root)
	return seen
}

// leaves returns the task ids under a node, each once, sorted; a cycle or a walk
// past MaxFeatureDepth is refused.
func (fs *Features) leaves(file, id string, memo map[string][]string, path []string) ([]string, error) {
	if len(path) > MaxFeatureDepth {
		return nil, featureError(file, "the walk from %q is past the depth bound of %d", path[0], MaxFeatureDepth)
	}
	for _, p := range path {
		if p == id {
			return nil, featureError(file, "cycle: %s -> %s", strings.Join(path, " -> "), id)
		}
	}
	if l, ok := memo[id]; ok {
		return l, nil
	}
	n := fs.Nodes[id]
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
		l, err := fs.leaves(file, r, memo, below)
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

// ApplyEvidence applies one overlay file: any number of (:id ... :state ...) forms,
// each moving one task's :state, and its :evidence when given, held to the task rule.
// It returns the entries applied; a refused file changes nothing.
func (fs *Features) ApplyEvidence(file string, data []byte) (int, error) {
	// The reader takes one form; the file holds many, so it is read as one list.
	// Byte offsets in a refusal are therefore two past the file's own.
	wrapped := append(append([]byte("(\n"), data...), "\n)"...)
	top, err := readFeatureForm(file, wrapped)
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
			return 0, featureError(file, "%s: want a non-empty string :id", at)
		}
		n := fs.Nodes[id]
		if n == nil {
			return 0, featureError(file, "%s: id %q is no node of the roadmap", at, id)
		}
		if n.Type != "task" {
			return 0, featureError(file, "%s: id %q is a %s, not a task", at, id, n.Type)
		}
		s, ok := m["state"]
		if isNil(s, ok) || s.Kind != sexp.Keyword {
			return 0, featureError(file, "%s: id %q carries no :state", at, id)
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
	fs.Overlays += len(moves)
	return len(moves), nil
}

// ApplyEvidenceDir applies every *.sexp under dir whose name does not start with a
// dot, in name order. A missing dir is no overlay. It returns the files applied.
func (fs *Features) ApplyEvidenceDir(dir string) (int, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.sexp"))
	if err != nil {
		return 0, err
	}
	sort.Strings(paths)
	files := 0
	for _, p := range paths {
		if strings.HasPrefix(filepath.Base(p), ".") {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return files, err
		}
		if _, err := fs.ApplyEvidence(p, data); err != nil {
			return files, err
		}
		files++
	}
	return files, nil
}

// Cell reports whether every task under a node is done: the one rule for ✅.
func (fs *Features) Cell(id string) bool {
	l, err := fs.leaves("", id, map[string][]string{}, nil)
	if err != nil {
		return false
	}
	for _, t := range l {
		if fs.Nodes[t].State != "done" {
			return false
		}
	}
	return true
}

// AreaTotals returns the rows of an area and how many are ✅ in every column.
func (fs *Features) AreaTotals(id string) (rows, done int) {
	n := fs.Nodes[id]
	green := map[[2]string]bool{}
	for _, c := range n.Cells {
		green[[2]string{c[0], c[1]}] = fs.Cell(c[2])
	}
	for _, r := range n.Rows {
		all := true
		for _, c := range n.Columns {
			all = all && green[[2]string{r.ID, c.ID}]
		}
		if all {
			done++
		}
	}
	return len(n.Rows), done
}

// renderArea is one area's table, the complete row and the source link.
func (fs *Features) renderArea(n *Node, source string) string {
	var b strings.Builder
	green := map[[2]string]bool{}
	for _, c := range n.Cells {
		green[[2]string{c[0], c[1]}] = fs.Cell(c[2])
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
	fmt.Fprintf(&b, " |\n\n[Source data](%s)", source)
	return b.String()
}

// RenderFeatures returns the ROADMAP.md the data makes: the title, the generated-file
// notice, one section per area in :page order (heading, what the area is, the table
// between its nova-work markers), then the closing sections. evidence is the overlay
// directory as the page names it, "" when the data has none. The same bytes for the
// same data, every time.
func RenderFeatures(fs *Features, source, evidence string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", flatText(fs.Title))
	from := source
	if evidence != "" {
		from += " and " + evidence
	}
	fmt.Fprintf(&b, "<!-- Generated from %s by nova-roadmap. Do not edit this file: edit the data and run `nova-roadmap render`. -->\n", from)
	for _, id := range fs.Areas {
		n := fs.Nodes[id]
		fmt.Fprintf(&b, "\n## %s\n", flatText(n.Title))
		for _, p := range n.Text {
			fmt.Fprintf(&b, "\n%s\n", p)
		}
		fmt.Fprintf(&b, "\n<!-- nova-work:%s:start -->\n\n%s\n\n<!-- nova-work:%s:end -->\n", id, fs.renderArea(n, source), id)
	}
	for _, s := range fs.Closing {
		fmt.Fprintf(&b, "\n## %s\n", flatText(s.Title))
		for _, p := range s.Text {
			fmt.Fprintf(&b, "\n%s\n", p)
		}
	}
	return b.String()
}

// Page renders the page one data file makes, the one path every caller takes
// (nova-roadmap render and check, and tools/roadmap until it retires): kind is
// "roadmap" (either form, told apart by IsFeatureForm) or "fixes". evidence is
// the overlay directory for the feature form, "" for roadmap-evidence/ beside
// file; a missing default is no overlay, a missing named one is refused. It
// returns the page and its row or item count.
func Page(kind, file, evidence string) (page string, items int, err error) {
	raw, err := os.ReadFile(file)
	if err != nil {
		return "", 0, fmt.Errorf("reading %s: %v", file, err)
	}
	switch {
	case kind == "roadmap" && IsFeatureForm(file, raw):
		fs, err := DecodeFeatures(file, raw)
		if err != nil {
			return "", 0, err
		}
		dir := evidence
		if dir == "" {
			dir = filepath.Join(filepath.Dir(file), EvidenceDir)
		}
		named := ""
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			if _, err := fs.ApplyEvidenceDir(dir); err != nil {
				return "", 0, err
			}
			named = filepath.ToSlash(dir)
		} else if evidence != "" {
			return "", 0, fmt.Errorf("--evidence %s is not a directory", evidence)
		}
		for _, id := range fs.Areas {
			rows, _ := fs.AreaTotals(id)
			items += rows
		}
		return RenderFeatures(fs, file, named), items, nil
	case kind == "roadmap":
		if evidence != "" {
			return "", 0, fmt.Errorf("--evidence is for the feature-table form only; %s is the card list", file)
		}
		doc, err := Decode(file, raw)
		if err != nil {
			return "", 0, err
		}
		return Render(doc, file), len(doc.Items), nil
	case kind == "fixes":
		if evidence != "" {
			return "", 0, fmt.Errorf("--evidence is for the feature-table form only")
		}
		fx, err := DecodeFixes(file, raw)
		if err != nil {
			return "", 0, err
		}
		return RenderFixes(fx, file), len(fx.Items), nil
	}
	return "", 0, fmt.Errorf("--kind %q is not roadmap or fixes", kind)
}
