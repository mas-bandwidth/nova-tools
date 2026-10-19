/*
Package roadmap is nova-work's roadmap editor: it edits a repository's own work
record, docs/roadmap.sexp and docs/fixes.sexp, and renders ROADMAP.md and
FIXES.md from them in the same step, so neither the data nor the pages are
edited by hand and no GitHub call is made per entry.

It writes no reader and no renderer of its own. The files are read through
nova-tools pkg/sexp (data, never evaluated), whose forms carry their byte offsets,
and an edit splices only the bytes of the records it changes, so comments
(a removed record's own trailing comment aside), spacing and every other record
stay as they were. A move between the files carries every field; one the target
shape has no key for is kept under :kept (carry.go). Every edit is decoded again
through nova-tools pkg/roadmap (imported as roadmapdoc), the shape the pages are
rendered from, before any
byte is written; an edit roadmapdoc refuses is refused whole, nothing written.
The pages are roadmapdoc.Render and roadmapdoc.RenderFixes of the saved data,
the bytes nova-tools' tools/roadmap writes (`make roadmap`, `go tool roadmap`)
and TestRoadmapIsGeneratedFromTheSexp (generated_test.go) holds.

An entry's life (tla/RoadmapEntry.tla): a roadmap item under a group is todo; a
fix in a point release is open (planned or in-progress); done is a roadmap item
on the :done list, or a fix whose status is done or shipped. Done is final, a
release whose status is shipped is frozen, every group and release keeps at
least one entry, and every entry names a group or release the file declares. A
pull across the two files writes the destination first and the source second,
so a stop between the writes leaves the id in both files, which Load refuses
and Check names, and never in neither.
*/
package roadmap

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/atomicfile"
	roadmapdoc "github.com/mas-bandwidth/nova-tools/pkg/roadmap"
	"github.com/mas-bandwidth/nova-tools/pkg/sexp"
)

// The files of a repository's work record, relative to its root.
const (
	RoadmapFile = "docs/roadmap.sexp"
	RoadmapPage = "ROADMAP.md"
	FixesFile   = "docs/fixes.sexp"
	FixesPage   = "FIXES.md"
)

// The states an entry has (Entry.State).
const (
	Todo       = "todo"        // a roadmap item under a group
	Scheduled  = "scheduled"   // a roadmap :scheduled note: a record, not work
	Done       = "done"        // a roadmap :done note, or a fix whose status is done
	Planned    = "planned"     // a fix not started
	InProgress = "in-progress" // a fix being worked
	Shipped    = "shipped"     // a fix whose release is cut
)

// file is one of the two data files: its path, its bytes, and whether an edit
// changed them.
type file struct {
	rel, page string
	data      []byte
	present   bool
	changed   bool
}

// Set is one repository's work record: the roadmap and the fixes file. Either
// may be absent; a verb that needs an absent one is refused.
type Set struct {
	Dir     string
	road    file
	fix     file
	first   *file // written first by Save: the file an entry moved into
	Today   string
	decoded struct {
		road *roadmapdoc.Doc
		fix  *roadmapdoc.Fixes
	}
}

// Load reads the work record under dir and decodes it; a file that does not
// decode, or an id that stands twice across the two files, is refused with
// every problem named.
func Load(dir, today string) (*Set, error) {
	s := &Set{Dir: dir, Today: today,
		road: file{rel: RoadmapFile, page: RoadmapPage},
		fix:  file{rel: FixesFile, page: FixesPage}}
	for _, f := range []*file{&s.road, &s.fix} {
		raw, err := os.ReadFile(filepath.Join(dir, f.rel))
		switch {
		case errors.Is(err, os.ErrNotExist):
			continue
		case err != nil:
			return nil, err
		}
		f.data, f.present = raw, true
	}
	if !s.road.present && !s.fix.present {
		return nil, fmt.Errorf("%s holds neither %s nor %s; run nova-work roadmap from a repository's root, or name it with --repo", dir, RoadmapFile, FixesFile)
	}
	if err := s.decode(); err != nil {
		return nil, err
	}
	return s, nil
}

// decode decodes both files through roadmapdoc and holds the ids unique across
// them.
func (s *Set) decode() error {
	var probs []error
	if s.road.present {
		d, err := roadmapdoc.Decode(s.road.rel, s.road.data)
		if err != nil {
			probs = append(probs, err)
		}
		s.decoded.road = d
	}
	if s.fix.present {
		fx, err := roadmapdoc.DecodeFixes(s.fix.rel, s.fix.data)
		if err != nil {
			probs = append(probs, err)
		}
		s.decoded.fix = fx
	}
	if len(probs) == 0 {
		seen := map[string]string{}
		for _, e := range s.Entries() {
			if prev, dup := seen[e.ID]; dup && prev != e.File {
				probs = append(probs, fmt.Errorf("id %q stands in both %s and %s (a pull stopped between its two writes?); remove one copy", e.ID, prev, e.File))
			}
			seen[e.ID] = e.File
		}
	}
	return errors.Join(probs...)
}

// Entry is one record of the work record, as list prints it.
type Entry struct {
	ID    string `json:"id"`
	File  string `json:"file"`
	Kind  string `json:"kind"`  // item, scheduled, done or fix
	Place string `json:"place"` // the group of an item, the release of a fix or a scheduled note, "" for a done note
	State string `json:"state"`
	Title string `json:"title"`
	Text  string `json:"text,omitempty"`
}

// Entries are every record of both files, the roadmap's first, in file order.
func (s *Set) Entries() []Entry {
	var out []Entry
	if d := s.decoded.road; d != nil {
		for _, n := range d.Scheduled {
			out = append(out, Entry{ID: n.ID, File: RoadmapFile, Kind: "scheduled", Place: n.Release, State: Scheduled, Title: flat(n.Title), Text: flat(n.Text)})
		}
		for _, n := range d.Done {
			out = append(out, Entry{ID: n.ID, File: RoadmapFile, Kind: "done", State: Done, Title: flat(n.Title), Text: flat(n.Text)})
		}
		for _, it := range d.Items {
			out = append(out, Entry{ID: it.ID, File: RoadmapFile, Kind: "item", Place: it.Group, State: Todo, Title: flat(it.Title), Text: flat(it.Text)})
		}
	}
	if fx := s.decoded.fix; fx != nil {
		for _, it := range fx.Items {
			out = append(out, Entry{ID: it.ID, File: FixesFile, Kind: "fix", Place: it.Release, State: it.Status, Title: flat(it.Title), Text: flat(it.Text)})
		}
	}
	return out
}

func flat(s string) string { return strings.Join(strings.Fields(s), " ") }

// Query selects entries: each non-empty field must match.
type Query struct {
	File  string // "roadmap" or "fixes"
	Place string // a group or release id
	State string
	Text  string // a case-insensitive substring of the id, title or text
}

// Find returns the entries q selects.
func (s *Set) Find(q Query) []Entry {
	var out []Entry
	for _, e := range s.Entries() {
		switch {
		case q.File == "roadmap" && e.File != RoadmapFile, q.File == "fixes" && e.File != FixesFile:
		case q.Place != "" && e.Place != q.Place:
		case q.State != "" && e.State != q.State:
		case q.Text != "" && !strings.Contains(strings.ToLower(e.ID+"\n"+e.Title+"\n"+e.Text), strings.ToLower(q.Text)):
		default:
			out = append(out, e)
		}
	}
	return out
}

// entry returns the entry id names, or a refusal naming the id.
func (s *Set) entry(id string) (Entry, error) {
	for _, e := range s.Entries() {
		if e.ID == id {
			return e, nil
		}
	}
	return Entry{}, fmt.Errorf("unknown id %q: no entry of %s has it; nova-work roadmap list --text <word> finds one", id, s.files())
}

func (s *Set) files() string {
	var f []string
	if s.road.present {
		f = append(f, RoadmapFile)
	}
	if s.fix.present {
		f = append(f, FixesFile)
	}
	return strings.Join(f, " or ")
}

// usedID reports whether any record of either file, a group or a release
// included, has id.
func (s *Set) usedID(id string) bool {
	for _, e := range s.Entries() {
		if e.ID == id {
			return true
		}
	}
	if d := s.decoded.road; d != nil && slices.ContainsFunc(d.Groups, func(g roadmapdoc.Group) bool { return g.ID == id }) {
		return true
	}
	if fx := s.decoded.fix; fx != nil && slices.ContainsFunc(fx.Releases, func(r roadmapdoc.Release) bool { return r.ID == id }) {
		return true
	}
	return false
}

func (s *Set) group(id string) error {
	if s.decoded.road == nil {
		return fmt.Errorf("--group %q: this repository has no %s", id, RoadmapFile)
	}
	var ids []string
	for _, g := range s.decoded.road.Groups {
		if g.ID == id {
			return nil
		}
		ids = append(ids, g.ID)
	}
	return fmt.Errorf("--group %q names no group of %s; the groups are %s", id, RoadmapFile, strings.Join(ids, ", "))
}

// release returns the release id names, refusing one that is not there or is
// cut: a shipped release is frozen.
func (s *Set) release(id string) (roadmapdoc.Release, error) {
	if s.decoded.fix == nil {
		return roadmapdoc.Release{}, fmt.Errorf("--release %q: this repository has no %s", id, FixesFile)
	}
	var ids []string
	for _, r := range s.decoded.fix.Releases {
		if r.ID == id {
			if r.Status == Shipped {
				return r, fmt.Errorf("release %s is shipped; a cut release is frozen, so nothing goes into or out of it", id)
			}
			return r, nil
		}
		ids = append(ids, r.ID)
	}
	return roadmapdoc.Release{}, fmt.Errorf("--release %q names no release of %s; the releases are %s", id, FixesFile, strings.Join(ids, ", "))
}

// open returns the entry id names when it is still work: a todo item or an
// open fix in a release not cut.
func (s *Set) open(id, verb string) (Entry, error) {
	e, err := s.entry(id)
	if err != nil {
		return e, err
	}
	switch {
	case e.Kind == "scheduled" || e.Kind == "done":
		return e, fmt.Errorf("%s is a %s note of %s, a record and not work; it is not %s", id, e.Kind, RoadmapFile, verbPast(verb))
	case e.State == Done || e.State == Shipped:
		return e, fmt.Errorf("%s is %s, which is final; it is not %s", id, e.State, verbPast(verb))
	}
	if e.Kind == "fix" {
		if _, err := s.release(e.Place); err != nil {
			return e, fmt.Errorf("%s: %v", id, err)
		}
	}
	return e, nil
}

// movable returns the entry id names when it may leave its place: open, and
// never the last entry of its group or release.
func (s *Set) movable(id, verb string) (Entry, error) {
	e, err := s.open(id, verb)
	if err != nil {
		return e, err
	}
	if n := s.siblings(e); n <= 1 {
		what := "group"
		if e.Kind == "fix" {
			what = "release"
		}
		return e, fmt.Errorf("%s is the last entry of %s %s, and every %s must hold one; add another entry to %s first, or edit %s to drop the %s", id, what, e.Place, what, e.Place, e.File, what)
	}
	return e, nil
}

func verbPast(verb string) string {
	switch verb {
	case "remove":
		return "removed"
	case "pull":
		return "moved"
	case "done":
		return "done again"
	}
	return verb + "ed"
}

// siblings counts the entries in e's place, e included.
func (s *Set) siblings(e Entry) int {
	n := 0
	for _, o := range s.Entries() {
		if o.Kind == e.Kind && o.Place == e.Place {
			n++
		}
	}
	return n
}

// Item is a new roadmap item (add).
type Item struct {
	ID, Group, Title, Text, Origin, Why string
}

// Fix is a new fix (add).
type Fix struct {
	ID, Release, Title, Text, Origin, Status string
}

var idRE = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*$`)

func (s *Set) newID(id string) error {
	if !idRE.MatchString(id) {
		return fmt.Errorf("id %q wants lower-case letters, digits, dots and dashes, starting with a letter or digit", id)
	}
	if e, err := s.entry(id); err == nil {
		place := e.Place
		if place == "" {
			place = "the :done list"
		}
		article := "a"
		if e.Kind == "item" {
			article = "an"
		}
		return fmt.Errorf("duplicate id %q: %s holds it, %s %s in %s (%s); ids are unique across both files, so pull, note or done that entry instead", id, e.File, article, e.Kind, place, e.State)
	}
	if s.usedID(id) {
		return fmt.Errorf("duplicate id %q: a group or release of %s has it; ids are unique across both files", id, s.files())
	}
	return nil
}

// AddItem adds a roadmap item under its group.
func (s *Set) AddItem(it Item) error {
	if err := s.newID(it.ID); err != nil {
		return err
	}
	if err := s.group(it.Group); err != nil {
		return err
	}
	rec := roadmapdoc.Item{ID: it.ID, Group: it.Group, Title: it.Title, Text: it.Text, Why: it.Why, Origin: it.Origin, Date: s.Today}
	if err := s.appendRecord(&s.road, "items", itemRecord(rec)); err != nil {
		return err
	}
	return s.decode()
}

// AddFix adds a fix to its release.
func (s *Set) AddFix(fx Fix) error {
	if err := s.newID(fx.ID); err != nil {
		return err
	}
	if _, err := s.release(fx.Release); err != nil {
		return err
	}
	if fx.Status == "" {
		fx.Status = Planned
	}
	if fx.Status != Planned && fx.Status != InProgress {
		return fmt.Errorf("--status %q: a new fix is planned or in-progress", fx.Status)
	}
	if err := s.appendRecord(&s.fix, "items", fixRecord(roadmapdoc.Fix{ID: fx.ID, Release: fx.Release, Title: fx.Title, Text: fx.Text, Origin: fx.Origin, Status: fx.Status})); err != nil {
		return err
	}
	s.first = &s.fix
	return s.decode()
}

// Remove takes the entries out; each must be movable.
func (s *Set) Remove(ids ...string) error {
	for _, id := range ids {
		e, err := s.movable(id, "remove")
		if err != nil {
			return err
		}
		if err := s.removeRecord(s.fileOf(e), "items", id); err != nil {
			return err
		}
		if err := s.decode(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Set) fileOf(e Entry) *file {
	if e.File == FixesFile {
		return &s.fix
	}
	return &s.road
}

// Pull moves the entries to a release (toRelease) or a group (toGroup): an
// item into a fixes release becomes a fix, planned; a fix onto the roadmap
// becomes an item; within a file only the :group or :release changes. A move
// across the files carries every field: one the other file has no key for
// goes under :kept, and a move back restores it (carry.go).
func (s *Set) Pull(toRelease, toGroup string, ids ...string) error {
	for _, id := range ids {
		e, err := s.movable(id, "pull")
		if err != nil {
			return err
		}
		if (e.Kind == "fix" && toRelease != "" && e.Place == toRelease) || (e.Kind == "item" && toGroup != "" && e.Place == toGroup) {
			return fmt.Errorf("%s is already in %s", id, e.Place)
		}
		if toRelease != "" {
			if _, err := s.release(toRelease); err != nil {
				return err
			}
		} else if err := s.group(toGroup); err != nil {
			return err
		}
		switch {
		case e.Kind == "fix" && toRelease != "":
			err = s.setKey(&s.fix, "items", id, "release", toRelease)
		case e.Kind == "item" && toGroup != "":
			err = s.setKey(&s.road, "items", id, "group", toGroup)
		case e.Kind == "item":
			err = s.cross(&s.fix, &s.road, fixRecord(itemToFix(s.item(id), toRelease)), id)
		default:
			err = s.cross(&s.road, &s.fix, itemRecord(fixToItem(s.fix1(id), toGroup, s.Today)), id)
		}
		if err != nil {
			return err
		}
		if err := s.decode(); err != nil {
			return err
		}
	}
	return nil
}

// cross moves one record from one file to the other: the destination gains
// it first, so Save writes the destination first.
func (s *Set) cross(to, from *file, rec, id string) error {
	if !to.present {
		return fmt.Errorf("this repository has no %s to move %s into", to.rel, id)
	}
	if err := s.appendRecord(to, "items", rec); err != nil {
		return err
	}
	if err := s.removeRecord(from, "items", id); err != nil {
		return err
	}
	s.first = to
	return nil
}

func (s *Set) item(id string) roadmapdoc.Item {
	for _, it := range s.decoded.road.Items {
		if it.ID == id {
			return it
		}
	}
	return roadmapdoc.Item{}
}

func (s *Set) fix1(id string) roadmapdoc.Fix {
	for _, it := range s.decoded.fix.Items {
		if it.ID == id {
			return it
		}
	}
	return roadmapdoc.Fix{}
}

var evidenceRE = regexp.MustCompile(`^((PR )?#[0-9]+|[0-9a-f]{7,40}|https://github\.com/[^ ]+/(pull|commit)/[^ ]+)$`)

// MarkDone marks the entries done with the evidence that they are: a pull
// request (PR #n or #n), a commit (7 to 40 hex digits) or its GitHub link. A
// fix's status becomes done; an item moves to the roadmap's :done list, dated
// today. The evidence is appended to the text.
func (s *Set) MarkDone(evidence string, ids ...string) error {
	if !evidenceRE.MatchString(evidence) {
		return fmt.Errorf("--evidence %q is not a pull request (PR #12, #12), a commit (7 to 40 hex digits) or a github.com pull or commit link", evidence)
	}
	for _, id := range ids {
		e, err := s.open(id, "done")
		if err != nil {
			return err
		}
		if e.Kind == "item" {
			// an item leaves its group for the :done list
			if _, err := s.movable(id, "done"); err != nil {
				return err
			}
		}
		note := " Done: " + evidence + "."
		if e.Kind == "fix" {
			if err := s.setKey(&s.fix, "items", id, "status", Done); err != nil {
				return err
			}
			if err := s.appendText(&s.fix, "items", id, strings.TrimSpace(note)); err != nil {
				return err
			}
		} else {
			rec := doneRecord(itemToDone(s.item(id), note, s.Today))
			if err := s.removeRecord(&s.road, "items", id); err != nil {
				return err
			}
			if err := s.appendRecord(&s.road, "done", rec); err != nil {
				return err
			}
		}
		if err := s.decode(); err != nil {
			return err
		}
	}
	return nil
}

// Note appends text to each entry's :text and, with a title, gives it that
// title; the title it had is kept under :kept (earlier-title), never dropped.
//
// tla/RoadmapEntry.tla Note
func (s *Set) Note(text, title string, ids ...string) error {
	if strings.TrimSpace(text) == "" && strings.TrimSpace(title) == "" {
		return errors.New("--text and --title are empty; note wants words to append, a new title, or both")
	}
	for _, id := range ids {
		e, err := s.entry(id)
		if err != nil {
			return err
		}
		f, list := s.fileOf(e), "items"
		switch e.Kind {
		case "scheduled":
			list = "scheduled"
		case "done":
			list = "done"
		}
		if title != "" {
			if err := s.retitle(f, list, id, title); err != nil {
				return err
			}
		}
		if strings.TrimSpace(text) != "" {
			if err := s.appendText(f, list, id, flat(text)); err != nil {
				return err
			}
		}
		if err := s.decode(); err != nil {
			return err
		}
	}
	return nil
}

// Changed names the files an edit changed.
func (s *Set) Changed() []string {
	var out []string
	for _, f := range []*file{&s.road, &s.fix} {
		if f.changed {
			out = append(out, f.rel, f.page)
		}
	}
	return out
}

// Pages renders the page of each file present, from its current data.
func (s *Set) Pages() map[string]string {
	out := map[string]string{}
	if s.decoded.road != nil {
		out[RoadmapPage] = roadmapdoc.Render(s.decoded.road, RoadmapFile)
	}
	if s.decoded.fix != nil {
		out[FixesPage] = roadmapdoc.RenderFixes(s.decoded.fix, FixesFile)
	}
	return out
}

// Save writes each changed file and its page, each through a temporary file
// and a rename: the file an entry moved into first, so a stop between the
// writes leaves the entry in both files (Load refuses it, Check names it),
// never in neither. Within one file the data is written before its page; a
// stop between those two leaves the page stale, which Check names and
// `nova-work roadmap render` mends.
//
// tla/RoadmapEntry.tla Save
func (s *Set) Save() error {
	order := []*file{&s.road, &s.fix}
	if s.first == &s.fix {
		order = []*file{&s.fix, &s.road}
	}
	pages := s.Pages()
	for _, f := range order {
		if !f.changed {
			continue
		}
		if err := atomicfile.Write(filepath.Join(s.Dir, f.rel), f.data, 0o644); err != nil {
			return err
		}
		if err := atomicfile.Write(filepath.Join(s.Dir, f.page), []byte(pages[f.page]), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// Check is every way the work record is not sound: a file that does not
// decode, an id in both files, a page that differs from what its data renders.
// Load already refuses the first two, so Check reads the files itself.
func Check(dir string) ([]string, error) {
	s, err := Load(dir, "")
	if err != nil {
		return strings.Split(err.Error(), "\n"), nil
	}
	var probs []string
	for page, want := range s.Pages() {
		got, err := os.ReadFile(filepath.Join(dir, page))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if string(got) != want {
			probs = append(probs, fmt.Sprintf("%s differs from what its data renders; run: nova-work roadmap render", page))
		}
	}
	slices.Sort(probs)
	return probs, nil
}

// Render writes the page each file present renders, and names the pages it
// changed.
func Render(dir string) ([]string, error) {
	s, err := Load(dir, "")
	if err != nil {
		return nil, err
	}
	var changed []string
	for _, page := range []string{RoadmapPage, FixesPage} {
		want, ok := s.Pages()[page]
		if !ok {
			continue
		}
		path := filepath.Join(dir, page)
		if got, err := os.ReadFile(path); err == nil && string(got) == want {
			continue
		}
		if err := atomicfile.Write(path, []byte(want), 0o644); err != nil {
			return changed, err
		}
		changed = append(changed, page)
	}
	return changed, nil
}

// RenderFile renders one data file held anywhere (a work record's roadmap
// kept outside the repository whose page it is) to out: the roadmap or fixes
// shape, read off the file's head form, through the same renderer. source is
// the name the page's header gives the data.
func RenderFile(in, out, source string) (kind string, items int, err error) {
	raw, err := os.ReadFile(in)
	if err != nil {
		return "", 0, err
	}
	top, err := sexp.Read(in, raw, roadmapdoc.Limits)
	if err != nil {
		return "", 0, err
	}
	if top.Kind != sexp.List || len(top.List) == 0 || top.List[0].Kind != sexp.Symbol {
		return "", 0, fmt.Errorf("%s: want one (roadmap \"v1\" ...) or (fixes \"v1\" ...) form", in)
	}
	var page string
	switch kind = top.List[0].Value; kind {
	case "roadmap":
		d, err := roadmapdoc.Decode(in, raw)
		if err != nil {
			return kind, 0, err
		}
		page, items = roadmapdoc.Render(d, source), len(d.Items)
	case "fixes":
		fx, err := roadmapdoc.DecodeFixes(in, raw)
		if err != nil {
			return kind, 0, err
		}
		page, items = roadmapdoc.RenderFixes(fx, source), len(fx.Items)
	default:
		return kind, 0, fmt.Errorf("%s: the head form is (%s ...); nova-work renders (roadmap \"v1\" ...) and (fixes \"v1\" ...)", in, kind)
	}
	return kind, items, atomicfile.Write(out, []byte(page), 0o644)
}
