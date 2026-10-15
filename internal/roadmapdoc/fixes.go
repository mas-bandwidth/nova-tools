package roadmapdoc

import (
	"errors"
	"fmt"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/worklang"
)

// FixesFormat is the one format string the fixes form carries.
const FixesFormat = "v1"

// The fixes file (docs/fixes.sexp) is the point releases: the fixes each one ships,
// is shipping or will ship. It is read the same way as the roadmap (data, never
// evaluated, refused whole with every problem named) and renders FIXES.md:
//
//	(fixes "v1"
//	 :title "<page title>"
//	 :text "<the page's opening paragraph>"
//	 :releases ((release "<vX.Y.Z>" :status "shipped|in-progress|planned" :text "..." [:date "YYYY-MM-DD"]) ...)
//	 :items ((fix "<id>" :release "<vX.Y.Z>" :title "..." :origin "..." :status "planned|in-progress|done|shipped"
//	          [:text "..."] [:kept (:<key> "<value>" ...)]) ...))
//
// Every fix names a release the file declares, and every release holds a fix. A
// fix's status is planned, in-progress, done (landed, with its evidence in its
// text, before its release is cut) or shipped; a release's is planned,
// in-progress or shipped. nova-work roadmap edits the file (internal/roadmap);
// :kept is as in the roadmap (the fields a move carried), and is not rendered.

// Fixes is a decoded fixes file.
type Fixes struct {
	Title, Text string
	Releases    []Release
	Items       []Fix
}

// Release is one point release.
type Release struct {
	ID, Status, Text, Date string
}

// Fix is one item of a point release: where it came from (a pull request, a card,
// a commit or a ledger line) and how far it has got.
type Fix struct {
	ID, Release, Title, Text, Origin, Status string
	Kept                                     []Pair
}

var statuses = map[string]bool{"shipped": true, "in-progress": true, "planned": true}

// fixStatuses adds done to a fix's statuses: a fix that has landed, with its
// evidence, in a release not yet cut.
var fixStatuses = map[string]bool{"shipped": true, "in-progress": true, "planned": true, "done": true}

// DecodeFixes reads one fixes file and returns it, or every shape problem of the
// file together in one error.
func DecodeFixes(file string, data []byte) (*Fixes, error) {
	top, d, err := readTop(file, data, "fixes", "the fixes file is data")
	if err != nil {
		return nil, err
	}
	fx := d.fixes(top)
	if len(d.probs) > 0 {
		return nil, errors.Join(d.probs...)
	}
	return fx, nil
}

// readTop reads one sexp file and returns its top form and a decoder ready to
// fill it; it is the shared door of Decode and DecodeFixes, which use the same
// limits, refusal rewording and problem collection, differing only in the kind
// they name and the word for what the file is.
func readTop(file string, data []byte, kind, substitute string) (worklang.Form, *decoder, error) {
	top, err := worklang.Read(file, data, Limits)
	if err != nil {
		var r *worklang.Refusal
		if errors.As(err, &r) {
			return worklang.Form{}, nil, fmt.Errorf("%s: file=%s: %s", kind, file, strings.ReplaceAll(r.Reason, "a plan is data", substitute))
		}
		return worklang.Form{}, nil, err
	}
	return top, &decoder{file: file}, nil
}

func (d *decoder) status(at string, vals map[string]worklang.Form) string {
	v := d.str(at, vals, "status")
	if v != "" && !statuses[v] {
		d.fail(at, ":status %q wants shipped, in-progress or planned", v)
	}
	return v
}

func (d *decoder) fixStatus(at string, vals map[string]worklang.Form) string {
	v := d.str(at, vals, "status")
	if v != "" && !fixStatuses[v] {
		d.fail(at, ":status %q wants planned, in-progress, done or shipped", v)
	}
	return v
}

func (d *decoder) fixes(top worklang.Form) *Fixes {
	if top.Kind != worklang.List || len(top.List) < 2 || top.List[0].Kind != worklang.Symbol || top.List[0].Value != "fixes" ||
		top.List[1].Kind != worklang.String || top.List[1].Value != FixesFormat {
		d.fail("top", "want one (fixes %q ...) form", FixesFormat)
		return nil
	}
	_, vals, ok := d.record("top", top, "fixes", true, []string{"title", "text", "releases", "items"}, nil)
	if !ok {
		return nil
	}
	fx := &Fixes{Title: d.str("top", vals, "title"), Text: d.str("top", vals, "text")}
	ids := map[string]bool{}
	seen := func(at, id string) {
		if ids[id] {
			d.fail(at, "id %q is used twice", id)
		}
		ids[id] = true
	}
	releases := map[string]bool{}
	for i, f := range d.list("top", vals, "releases") {
		at := fmt.Sprintf(":releases[%d]", i)
		id, v, ok := d.record(at, f, "release", true, []string{"status", "text"}, []string{"date"})
		if !ok {
			continue
		}
		seen(at, id)
		releases[id] = true
		fx.Releases = append(fx.Releases, Release{ID: id, Status: d.status(at, v), Text: d.str(at, v, "text"), Date: d.date(at, v)})
	}
	used := map[string]bool{}
	for i, f := range d.list("top", vals, "items") {
		at := fmt.Sprintf(":items[%d]", i)
		id, v, ok := d.record(at, f, "fix", true, []string{"release", "title", "origin", "status"}, []string{"text", "kept"})
		if !ok {
			continue
		}
		seen(at, id)
		it := Fix{ID: id, Release: d.str(at, v, "release"), Title: d.str(at, v, "title"), Text: d.str(at, v, "text"),
			Origin: d.str(at, v, "origin"), Status: d.fixStatus(at, v), Kept: d.kept(at, v)}
		if it.Release != "" && !releases[it.Release] {
			d.fail(at, ":release %q names no (release ...) record", it.Release)
		}
		used[it.Release] = true
		fx.Items = append(fx.Items, it)
	}
	for _, r := range fx.Releases {
		if !used[r.ID] {
			d.fail(":releases", "release %q holds no fix", r.ID)
		}
	}
	return fx
}

// RenderFixes returns the FIXES.md the file makes: the same bytes for the same
// file, every time. Releases and their fixes keep the file's order.
func RenderFixes(fx *Fixes, source string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", flat(fx.Title))
	fmt.Fprintf(&b, "<!-- Generated from %s by tools/roadmap. Do not edit this file: edit %s and run `make roadmap`. -->\n\n", source, source)
	fmt.Fprintf(&b, "%s\n", flat(fx.Text))
	for _, r := range fx.Releases {
		head := r.ID + " (" + r.Status
		if r.Date != "" {
			head += ", " + r.Date
		}
		fmt.Fprintf(&b, "\n## %s)\n\n%s\n\n", head, flat(r.Text))
		for _, it := range fx.Items {
			if it.Release != r.ID {
				continue
			}
			line := "- **" + flat(it.Title) + "** (" + it.Status + ")."
			if it.Text != "" {
				line += " " + flat(it.Text)
			}
			fmt.Fprintf(&b, "%s From: %s.\n", line, strings.TrimSuffix(flat(it.Origin), "."))
		}
	}
	return b.String()
}
