package sprint

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
)

// A roadmap (docs/SPEC-SPRINT.md, "The roadmap: work deferred to a later release"; the
// owner, 2026-10-04: the later-release work leaves the sprint, "making sure it is stored
// somewhere in a sexp data structure"). nova-sprint defer writes the waiting cards of a later
// release, each with its whole brief, into the work record's roadmap of its product and
// release, <record>/roadmaps/<product>-<release>.sexp, reads the file back and counts it, and
// only then drops the cards with one reason naming the release; roadmap restore adds one back
// as its twin (Restore) and takes it out of the file; roadmap render writes the public
// ROADMAP.md, releases, streams and counts only. The file is one :roadmap form a Lisp reader
// reads back:
//
//	(:roadmap
//	 :product "nova-tools"
//	 :releases ("v2")
//	 :streams
//	 ((:name "s1"
//	   :cards
//	   ((:id "s1-4" :stream "s1" :tier "heavy" :needs ("s1-3") :who "" :repo "example/nova-tools"
//	     :replaces () :rules "" :brief "...")))))
//
// A string escapes only \ and " (the Lisp reader's rule), so a brief reads back byte for byte.

// RoadmapCard is one deferred card: what its restore needs to add it back as it was.
type RoadmapCard struct {
	ID       string
	Stream   string
	Tier     string
	Needs    []string
	Who      string
	Repo     string
	Replaces []string // the card's own lineage (FieldReplaces), for its twin's id (TwinIDs)
	Rules    string   // the held rules file of its brief (FieldRules), "" for none
	Brief    string
}

// RoadmapStream is the cards of one stream deferred to the release, in the order deferred.
type RoadmapStream struct {
	Name  string
	Cards []RoadmapCard
}

// Roadmap is one file: the deferred cards of one product's release.
type Roadmap struct {
	Product  string
	Releases []string
	Streams  []RoadmapStream
}

// Count is the number of cards the roadmap holds.
func (r *Roadmap) Count() int {
	n := 0
	for _, s := range r.Streams {
		n += len(s.Cards)
	}
	return n
}

// Find is the card with the id and its stream's index, nil when the roadmap has none.
func (r *Roadmap) Find(id string) (*RoadmapCard, int) {
	for i := range r.Streams {
		for j := range r.Streams[i].Cards {
			if r.Streams[i].Cards[j].ID == id {
				return &r.Streams[i].Cards[j], i
			}
		}
	}
	return nil, -1
}

// Append adds the cards under their streams, a stream not in the file after the others;
// refused, changing nothing, for a card the file holds already.
func (r *Roadmap) Append(cards []RoadmapCard) error {
	for _, c := range cards {
		if have, _ := r.Find(c.ID); have != nil {
			return fmt.Errorf("%s is in the roadmap already (stream %s)", c.ID, have.Stream)
		}
	}
	for _, c := range cards {
		i := slicesIndex(r.Streams, c.Stream)
		if i < 0 {
			r.Streams = append(r.Streams, RoadmapStream{Name: c.Stream})
			i = len(r.Streams) - 1
		}
		r.Streams[i].Cards = append(r.Streams[i].Cards, c)
	}
	return nil
}

// Remove takes the card out, and its stream when it was the last; false when it is not there.
func (r *Roadmap) Remove(id string) bool {
	_, i := r.Find(id)
	if i < 0 {
		return false
	}
	s := &r.Streams[i]
	for j := range s.Cards {
		if s.Cards[j].ID == id {
			s.Cards = append(s.Cards[:j], s.Cards[j+1:]...)
			break
		}
	}
	if len(s.Cards) == 0 {
		r.Streams = append(r.Streams[:i], r.Streams[i+1:]...)
	}
	return true
}

func slicesIndex(streams []RoadmapStream, name string) int {
	for i, s := range streams {
		if s.Name == name {
			return i
		}
	}
	return -1
}

// roadmapNameRE is a product's or a release's name: it is a part of the file's name.
var roadmapNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// RoadmapNameWhy is why a product or release name cannot name a roadmap file, "" when it can.
func RoadmapNameWhy(what, name string) string {
	if !roadmapNameRE.MatchString(name) || strings.Contains(name, "..") {
		return fmt.Sprintf("%s %q wants letters, digits, '.', '_' and '-', starting with a letter or digit", what, name)
	}
	return ""
}

// RoadmapProduct is the product of a card's repo, owner/name: its name.
func RoadmapProduct(repo string) string {
	return path.Base(strings.TrimSuffix(strings.TrimSpace(repo), ".git"))
}

// RoadmapFile is the file name of a product's release: <product>-<release>.sexp.
func RoadmapFile(product, release string) string { return product + "-" + release + ".sexp" }

// FormatRoadmap writes the roadmap as its :roadmap form.
func FormatRoadmap(r Roadmap) []byte {
	var b strings.Builder
	b.WriteString(";; the roadmap of " + r.Product + ": cards deferred to a later release (nova-sprint defer;\n")
	b.WriteString(";; nova-sprint roadmap restore <id> adds one back, roadmap render writes ROADMAP.md)\n")
	b.WriteString("(:roadmap\n :product " + sexpString(r.Product) + "\n :releases " + sexpList(r.Releases) + "\n :streams\n (")
	for i, s := range r.Streams {
		if i > 0 {
			b.WriteString("\n  ")
		}
		b.WriteString("(:name " + sexpString(s.Name) + "\n   :cards\n   (")
		for j, c := range s.Cards {
			if j > 0 {
				b.WriteString("\n    ")
			}
			fmt.Fprintf(&b, "(:id %s :stream %s :tier %s :needs %s :who %s :repo %s\n     :replaces %s :rules %s\n     :brief %s)",
				sexpString(c.ID), sexpString(c.Stream), sexpString(c.Tier), sexpList(c.Needs), sexpString(c.Who), sexpString(c.Repo),
				sexpList(c.Replaces), sexpString(c.Rules), sexpString(c.Brief))
		}
		b.WriteString("))")
	}
	b.WriteString("))\n")
	return []byte(b.String())
}

func sexpString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func sexpList(xs []string) string {
	if len(xs) == 0 {
		return "()"
	}
	q := make([]string, len(xs))
	for i, x := range xs {
		q[i] = sexpString(x)
	}
	return "(" + strings.Join(q, " ") + ")"
}

// sexp is one datum read: a string, a symbol (a keyword keeps its colon) or a list.
type sexp struct {
	str    *string
	sym    string
	list   []sexp
	isList bool
}

// sexpReader reads the forms of a text: strings, symbols, lists, ; comments.
type sexpReader struct {
	text string
	at   int
}

func (r *sexpReader) skip() {
	for r.at < len(r.text) {
		switch c := r.text[r.at]; {
		case c == ';':
			for r.at < len(r.text) && r.text[r.at] != '\n' {
				r.at++
			}
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			r.at++
		default:
			return
		}
	}
}

func (r *sexpReader) read() (sexp, error) {
	r.skip()
	if r.at >= len(r.text) {
		return sexp{}, errors.New("the form ends early")
	}
	switch r.text[r.at] {
	case '(':
		r.at++
		out := sexp{isList: true}
		for {
			r.skip()
			if r.at >= len(r.text) {
				return sexp{}, errors.New("a list is not closed")
			}
			if r.text[r.at] == ')' {
				r.at++
				return out, nil
			}
			d, err := r.read()
			if err != nil {
				return sexp{}, err
			}
			out.list = append(out.list, d)
		}
	case ')':
		return sexp{}, fmt.Errorf("a ) with no ( at byte %d", r.at)
	case '"':
		r.at++
		var b strings.Builder
		for r.at < len(r.text) {
			c := r.text[r.at]
			switch c {
			case '\\':
				if r.at+1 >= len(r.text) {
					return sexp{}, errors.New("a string ends in \\")
				}
				b.WriteByte(r.text[r.at+1])
				r.at += 2
				continue
			case '"':
				r.at++
				s := b.String()
				return sexp{str: &s}, nil
			}
			b.WriteByte(c)
			r.at++
		}
		return sexp{}, errors.New("a string is not closed")
	}
	start := r.at
	for r.at < len(r.text) && !strings.ContainsRune(" \t\r\n()\";", rune(r.text[r.at])) {
		r.at++
	}
	return sexp{sym: strings.ToLower(r.text[start:r.at])}, nil
}

// plist is a keyword list's values by keyword, refused for a key twice, a key with no value,
// or a key not among known: a form of another shape is refused, never rewritten without the
// keys it does not know.
func (d sexp) plist(what string, known ...string) (map[string]sexp, error) {
	if !d.isList || len(d.list)%2 != 0 {
		return nil, fmt.Errorf("%s is not a keyword list", what)
	}
	out := map[string]sexp{}
	for i := 0; i < len(d.list); i += 2 {
		k := d.list[i].sym
		if !strings.HasPrefix(k, ":") {
			return nil, fmt.Errorf("%s: %s is no keyword", what, d.list[i].text())
		}
		if _, ok := out[k]; ok {
			return nil, fmt.Errorf("%s: %s twice", what, k)
		}
		if !contains(known, k) {
			return nil, fmt.Errorf("%s: %s is no key of a roadmap's (%s)", what, k, strings.Join(known, " "))
		}
		out[k] = d.list[i+1]
	}
	return out, nil
}

func (d sexp) text() string {
	switch {
	case d.str != nil:
		return sexpString(*d.str)
	case d.isList:
		return "(...)"
	}
	return d.sym
}

// items is a list's elements; nil and () are the empty list.
func (d sexp) items(what string) ([]sexp, error) {
	if d.sym == "nil" {
		return nil, nil
	}
	if !d.isList {
		return nil, fmt.Errorf("%s is not a list", what)
	}
	return d.list, nil
}

func (d sexp) strs(what string) ([]string, error) {
	xs, err := d.items(what)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, x := range xs {
		if x.str == nil {
			return nil, fmt.Errorf("%s holds %s, not a string", what, x.text())
		}
		out = append(out, *x.str)
	}
	return out, nil
}

func strField(m map[string]sexp, key, what string) (string, error) {
	d, ok := m[key]
	if !ok {
		return "", nil
	}
	if d.sym == "nil" {
		return "", nil
	}
	if d.str == nil {
		return "", fmt.Errorf("%s %s is %s, not a string", what, key, d.text())
	}
	return *d.str, nil
}

// ParseRoadmap reads a :roadmap form back.
func ParseRoadmap(text []byte) (Roadmap, error) {
	var rm Roadmap
	rd := &sexpReader{text: string(text)}
	d, err := rd.read()
	if err != nil {
		return rm, err
	}
	if rd.skip(); rd.at != len(rd.text) {
		return rm, errors.New("more than one form")
	}
	if !d.isList || len(d.list) == 0 || d.list[0].sym != ":roadmap" {
		return rm, errors.New("not a :roadmap form")
	}
	top, err := sexp{isList: true, list: d.list[1:]}.plist(":roadmap", ":product", ":releases", ":streams")
	if err != nil {
		return rm, err
	}
	if rm.Product, err = strField(top, ":product", ":roadmap"); err != nil {
		return rm, err
	}
	if rel, ok := top[":releases"]; ok {
		if rm.Releases, err = rel.strs(":releases"); err != nil {
			return rm, err
		}
	}
	streams, err := top[":streams"].items(":streams")
	if _, ok := top[":streams"]; !ok {
		streams, err = nil, nil
	}
	if err != nil {
		return rm, err
	}
	for _, sd := range streams {
		sm, err := sd.plist("a stream", ":name", ":cards")
		if err != nil {
			return rm, err
		}
		var s RoadmapStream
		if s.Name, err = strField(sm, ":name", "a stream"); err != nil {
			return rm, err
		}
		cards, err := sm[":cards"].items("stream " + s.Name + " :cards")
		if _, ok := sm[":cards"]; !ok {
			cards, err = nil, nil
		}
		if err != nil {
			return rm, err
		}
		for _, cd := range cards {
			cm, err := cd.plist("a card of stream "+s.Name, ":id", ":stream", ":tier", ":needs", ":who", ":repo", ":replaces", ":rules", ":brief")
			if err != nil {
				return rm, err
			}
			var c RoadmapCard
			for key, dst := range map[string]*string{":id": &c.ID, ":stream": &c.Stream, ":tier": &c.Tier, ":who": &c.Who, ":repo": &c.Repo, ":rules": &c.Rules, ":brief": &c.Brief} {
				if *dst, err = strField(cm, key, "a card"); err != nil {
					return rm, err
				}
			}
			for key, dst := range map[string]*[]string{":needs": &c.Needs, ":replaces": &c.Replaces} {
				if v, ok := cm[key]; ok {
					if *dst, err = v.strs("card " + c.ID + " " + key); err != nil {
						return rm, err
					}
				}
			}
			if c.ID == "" {
				return rm, errors.New("a card of stream " + s.Name + " has no :id")
			}
			if c.Stream == "" {
				c.Stream = s.Name
			}
			s.Cards = append(s.Cards, c)
		}
		rm.Streams = append(rm.Streams, s)
	}
	return rm, nil
}

// RoadmapCardOf is the card as the roadmap keeps it.
func RoadmapCardOf(c *Card, repo string) RoadmapCard {
	return RoadmapCard{ID: c.ID, Stream: c.Row, Tier: c.F(FieldTier), Needs: Split(c.F("needs")), Who: c.F(FieldWho), Repo: repo,
		Replaces: Split(c.F(FieldReplaces)), Rules: c.F(FieldRules), Brief: c.F("brief")}
}

// RenderRoadmaps is the public ROADMAP.md: per product and release, its streams and their card
// counts, and nothing of a card (no id, brief or name).
func RenderRoadmaps(rms []Roadmap) string {
	sort.SliceStable(rms, func(i, j int) bool {
		if rms[i].Product != rms[j].Product {
			return rms[i].Product < rms[j].Product
		}
		return strings.Join(rms[i].Releases, ",") < strings.Join(rms[j].Releases, ",")
	})
	var b strings.Builder
	b.WriteString("# Roadmap\n\nThe work planned for later releases: each release, its streams and how many cards each holds.\n")
	if len(rms) == 0 {
		b.WriteString("\nNothing is planned for a later release.\n")
	}
	for _, rm := range rms {
		fmt.Fprintf(&b, "\n## %s %s\n\n| Stream | Cards |\n|---|---|\n", rm.Product, strings.Join(rm.Releases, ", "))
		for _, s := range rm.Streams {
			fmt.Fprintf(&b, "| %s | %d |\n", s.Name, len(s.Cards))
		}
		fmt.Fprintf(&b, "\n%d cards in %d streams.\n", rm.Count(), len(rm.Streams))
	}
	return b.String()
}

// DeferReq drops the waiting cards a roadmap now holds, with one reason naming the release.
type DeferReq struct {
	IDs    []string
	Reason string
	Who    string
}

// Defer is defer's drop: every card named still waiting on the table, then Drop. Refused
// whole, writing nothing, for a card gone or in another state since the roadmap was written.
func Defer(s *Snapshot, r DeferReq) Plan {
	var p Plan
	for _, id := range r.IDs {
		c := s.Work.Placed(id)
		switch {
		case c == nil:
			p.refuse(id, "not on the table now; nothing was changed")
		case c.Col != Waiting:
			p.refuse(id, "is "+c.Col+" now, not waiting: only a waiting card is deferred; nothing was changed")
		}
	}
	if len(p.Refused) > 0 {
		return p
	}
	return Drop(s, DropReq{Sel: Sel{IDs: r.IDs}, Reason: r.Reason, Who: r.Who})
}

// RestoreReq adds a deferred card back from its roadmap.
type RestoreReq struct {
	Card RoadmapCard
	Who  string
}

// RestoreTwin is the old card a restore names its twin by: the dropped card when the table
// keeps it, else the card as the roadmap holds it.
func RestoreTwin(s *Snapshot, c RoadmapCard) *Card {
	if old := s.Work.Card(c.ID); old != nil {
		return old
	}
	return &Card{ID: c.ID, Fields: map[string]string{FieldReplaces: strings.Join(c.Replaces, ",")}}
}

// Restore is roadmap restore: the deferred card added back as its twin (TwinID), in its
// stream, with its brief, rules and tier as the roadmap holds them and the needs of it still
// on the table (a need gone is left out, and said); the twin records the id it replaces
// (FieldReplaces). Refused, writing nothing, for a card on the table under its own id, every
// twin id taken, or any refusal of the add.
func Restore(s *Snapshot, r RestoreReq) Plan {
	var p Plan
	c := r.Card
	if on := s.Work.Placed(c.ID); on != nil && IsOpen(on.Col) {
		p.refuse(c.ID, "is on the table ("+on.Col+"): nothing to restore; the roadmap keeps it")
		return p
	}
	for _, on := range s.Work.Column(Waiting, Ready, Working, Review, Merging) {
		if contains(Split(on.F(FieldReplaces)), c.ID) {
			p.refuse(c.ID, "is restored already, as "+on.ID+" ("+on.Col+"); take it out of the roadmap by hand")
			return p
		}
	}
	old := RestoreTwin(s, c)
	nw := TwinID(s, old)
	if nw == "" {
		ids := TwinIDs(old)
		p.refuse(c.ID, "every twin id of "+c.ID+" is taken, "+ids[0]+" to "+ids[len(ids)-1]+"; the roadmap keeps it")
		return p
	}
	var needs, gone []string
	for _, n := range c.Needs {
		if s.Work.Placed(n) != nil {
			needs = append(needs, n)
		} else {
			gone = append(gone, n)
		}
	}
	p = Add(s, AddReq{Stream: c.Stream, IDs: []string{nw}, Needs: needs, Brief: c.Brief, Rules: c.Rules, Who: r.Who})
	if len(p.Refused) > 0 {
		return p
	}
	for i := range p.Units {
		u := &p.Units[i]
		for j, ch := range u.Changes {
			if ch.Table == Work && ch.Entry.ID == nw && ch.Entry.Create != nil {
				u.Changes[j].Entry.Set[FieldReplaces] = c.ID
				if c.Tier != "" {
					u.Changes[j].Entry.Set[FieldTier] = c.Tier
				}
				u.Moved += "; restored from the roadmap as the twin of " + c.ID
			}
		}
	}
	if len(gone) > 0 {
		p.Said = append(p.Said, nw+" restored without its needs "+strings.Join(gone, ",")+": not on the table")
	}
	return p
}
