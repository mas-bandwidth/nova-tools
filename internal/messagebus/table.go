package messagebus

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ReceiptsName is the file, in a sender's own lane, that records notes heard without a
// reply. It is one file per lane and append-only, so two lines recording receipts at the
// same moment touch different files and cannot conflict.
const ReceiptsName = "RECEIPTS"

// ReceiptStampLayout is how a receipt line writes its time: RFC 3339 in UTC, which holds
// no spaces, so the rest of the line is the target and a target with a space in it still
// parses.
const ReceiptStampLayout = "2006-01-02T15:04:05Z"

// Receipt is one recorded hearing.
type Receipt struct {
	Lane   string
	Stamp  time.Time
	Raw    string // the stamp exactly as written
	Target string // an id, or a repo-relative path
	Line   int
}

// Table is the whole table, read once.
type Table struct {
	Root   string
	Config *Config

	Notes    []Note
	Receipts []Receipt

	byID     map[string]*Note
	byPath   map[string]*Note
	laneDirs []string
	strays   []string
}

// When is the note's moment, for ordering. The Date line is preferred because it is what
// the author wrote; the filename's UTC minute is the fallback, because a legacy note may
// carry a Date this tool cannot parse and still sort correctly by its name.
func (n Note) When() time.Time {
	for _, layout := range []string{DateLayout, time.RFC1123, time.RFC1123Z, time.RFC3339, time.UnixDate, time.ANSIC} {
		if t, err := time.Parse(layout, n.Header.Date); err == nil {
			return t.UTC()
		}
	}
	base := filepath.Base(n.Path)
	if len(base) >= len(FileTimeLayout) {
		if t, err := time.Parse(FileTimeLayout, base[:len(FileTimeLayout)]); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// ReadTable reads every lane the roster declares, plus every from-* directory on disk, so
// that a lane nobody owns is visible to check rather than invisible to everything.
//
// A file that will not parse is NOT an error here: it becomes a Note with a ParseError, so
// that inbox keeps working on a table with one bad file and check can name every bad file
// in one run instead of the first.
func ReadTable(root string, c *Config) (*Table, error) {
	t := &Table{Root: root, Config: c, byID: map[string]*Note{}, byPath: map[string]*Note{}}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var lanes []string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "from-") {
			lanes = append(lanes, e.Name())
		}
	}
	sort.Strings(lanes)
	t.laneDirs = lanes
	for _, lane := range lanes {
		if err := t.readLane(lane); err != nil {
			return nil, err
		}
	}
	sort.Slice(t.Notes, func(i, j int) bool { return t.Notes[i].Path < t.Notes[j].Path })
	for i := range t.Notes {
		n := &t.Notes[i]
		t.byPath[n.Path] = n
		if id := n.Header.ID; id != "" {
			if _, dup := t.byID[id]; !dup {
				t.byID[id] = n
			}
		}
	}
	return t, nil
}

// ParseError is set on a note whose header would not parse. Its body and header are then
// empty and every other rule skips it, because there is nothing honest to say about them.
type ParseError struct{ Err error }

func (p *ParseError) Error() string { return p.Err.Error() }

func (t *Table) readLane(lane string) error {
	dir := filepath.Join(t.Root, lane)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if e.IsDir() {
			t.strays = append(t.strays, lane+"/"+name+"/")
			continue
		}
		if name == ReceiptsName {
			if err := t.readReceipts(lane, filepath.Join(dir, name)); err != nil {
				return err
			}
			continue
		}
		if !strings.HasSuffix(name, ".md") {
			t.strays = append(t.strays, lane+"/"+name)
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		path := lane + "/" + name
		n, perr := ParseNote(path, string(raw))
		if perr != nil {
			n = Note{Path: path, Lane: lane, Parse: &ParseError{perr}}
		}
		t.Notes = append(t.Notes, n)
	}
	return nil
}

func (t *Table) readReceipts(lane, path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	for i, line := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		r := Receipt{Lane: lane, Line: i + 1}
		stamp, target, ok := strings.Cut(line, " ")
		r.Raw, r.Target = stamp, strings.TrimSpace(target)
		if !ok {
			r.Target = ""
		}
		if ts, perr := time.Parse(ReceiptStampLayout, stamp); perr == nil {
			r.Stamp = ts.UTC()
		}
		t.Receipts = append(t.Receipts, r)
	}
	return nil
}

// NoteByID and NoteByPath are the two ways a Re line or a receipt can name a note: by the
// id assigned at send, or by the repo-relative path, which is how every note written
// before this tool existed is named and stays named.
func (t *Table) NoteByID(id string) (*Note, bool)     { n, ok := t.byID[id]; return n, ok }
func (t *Table) NoteByPath(path string) (*Note, bool) { n, ok := t.byPath[path]; return n, ok }

// Resolve turns one Re or receipt target into the note it names.
func (t *Table) Resolve(target string) (*Note, bool) {
	if n, ok := t.byID[target]; ok {
		return n, true
	}
	if n, ok := t.byPath[target]; ok {
		return n, true
	}
	return nil, false
}

// names reports every way the given note can be referred to: its id, if it has one, and
// its path, always.
func names(n *Note) []string {
	out := []string{n.Path}
	if n.Header.ID != "" {
		out = append(out, n.Header.ID)
	}
	return out
}

// AnswerKind is what closed a note for one reader: nothing, a reply, or a receipt.
//
// The distinction is not bookkeeping. A receipt says HEARD and a reply says ANSWERED, and
// collapsing them loses the state the table's people spend the most time in: a note read,
// acknowledged, and still owed an answer. Both leave the unanswered count, and only one of
// them leaves the listing.
type AnswerKind int

const (
	// NotAnswered: nothing in this reader's lane names the note.
	NotAnswered AnswerKind = iota
	// AnsweredByReply: a note in this reader's lane carries it on a Re line.
	AnsweredByReply
	// HeardByReceipt: this reader's RECEIPTS records it, and nothing else does.
	HeardByReceipt
)

// AnsweredBy reports whether anything in lane answers the note, and what did.
//
// THE ANSWERED RULE. A note is answered, for one reader, when a file in THAT READER'S OWN
// LANE carries the note's id on a Re line; or carries the note's repo-relative path on a
// Re line, which is how a note written before ids is answered and stays answered; or when
// that reader's RECEIPTS file records the note's id or path.
//
// It measures whether a note has had a reply, never whether the work in it is finished --
// the distinction the table was already making, kept.
func (t *Table) AnsweredBy(n *Note, lane string) (by string, answered bool) {
	by, kind := t.AnswerFor(n, lane)
	return by, kind != NotAnswered
}

// AnswerFor is AnsweredBy with the two kinds kept apart. A reply is looked for first: a
// note both receipted and replied to has been answered, and reporting it as merely heard
// would be the same collapse in the other direction.
func (t *Table) AnswerFor(n *Note, lane string) (by string, kind AnswerKind) {
	keys := names(n)
	for i := range t.Notes {
		m := &t.Notes[i]
		if m.Lane != lane || m.Parse != nil || m.Path == n.Path {
			continue
		}
		for _, re := range m.Header.Re {
			for _, k := range keys {
				if re == k {
					return m.Path, AnsweredByReply
				}
			}
		}
	}
	for _, r := range t.Receipts {
		if r.Lane != lane {
			continue
		}
		for _, k := range keys {
			if r.Target == k {
				return lane + "/" + ReceiptsName, HeardByReceipt
			}
		}
	}
	return "", NotAnswered
}

// InboxItem is one open note addressed to the caller.
type InboxItem struct {
	Note    *Note
	From    string // the note's resolved sender name
	Address string // "to" or "cc"
	Receipt bool   // true when the note only acknowledges
	Heard   bool   // true when my RECEIPTS records it and nothing of mine replies
}

// Unreadable lists the notes on the table that would not parse, outside the given lane.
//
// It exists because inbox used to step over them in silence. A note somebody wrote, on the
// table, addressed to a reader who is never told it is there, is the exact failure this
// tool was built to end -- and it is worse than a lost push, because nothing about it looks
// wrong. An unreadable note has no To line to test, so this cannot say whether it was
// addressed to the caller; it says the file is there and cannot be read, which is all
// anyone can honestly say about it, and check says the rest.
//
// The caller's own lane is excluded on the same rule the inbox uses: a note of mine is my
// own business, and check names it.
func (t *Table) Unreadable(lane string) []*Note {
	var out []*Note
	for i := range t.Notes {
		n := &t.Notes[i]
		if n.Parse == nil || n.Lane == lane {
			continue
		}
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// Inbox lists the notes addressed to me that no reply of mine answers, newest first. A
// note my RECEIPTS records is still listed, marked Heard: heard is not answered.
//
// Notes in my own lane are never in my inbox, whoever they are addressed to: the answered
// rule reads my lane for answers, and a note answering itself is not a state this reports.
func (t *Table) Inbox(me Participant, maxWords int) []InboxItem {
	var out []InboxItem
	for i := range t.Notes {
		n := &t.Notes[i]
		if n.Parse != nil || n.Lane == me.Lane {
			continue
		}
		to, cc := n.Header.Recipients(t.Config)
		addr := ""
		if contains(to, me.Name) {
			addr = "to"
		} else if contains(cc, me.Name) {
			addr = "cc"
		} else {
			continue
		}
		_, kind := t.AnswerFor(n, me.Lane)
		if kind == AnsweredByReply {
			continue
		}
		from := n.Header.From
		if p, ok := t.Config.ResolveOne(n.Header.From); ok {
			from = p.Name
		}
		out = append(out, InboxItem{
			Note:    n,
			From:    from,
			Address: addr,
			Receipt: IsReceipt(*n, maxWords),
			Heard:   kind == HeardByReceipt,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].Note.When(), out[j].Note.When()
		if !a.Equal(b) {
			return a.After(b)
		}
		return out[i].Note.Path > out[j].Note.Path
	})
	return out
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// Problem is one check finding: a place, a reason, and whether it was tolerated.
type Problem struct {
	Where  string // a path, a path:line, or a lane
	Reason string

	// Warn is set when the finding was inside the legacy tolerance: it is reported and
	// does not fail the run.
	Warn bool
}

// CheckOptions is what a check run tolerates. The zero value tolerates nothing, which is
// what CI on a table that has only ever been written by this tool should use.
type CheckOptions struct {
	// LegacyBefore, when non-zero, is the moment before which a note's parse failure or
	// dangling Re line is a WARN rather than a FAIL.
	//
	// THE ADOPTION PROBLEM, which this exists for and nothing else. A table that has been
	// running for months was written by people, by hand, in a shape no tool checked. Point
	// check at it and every note that predates the tool fails at once, so the first run is
	// a wall of red that nobody can act on and the check gets turned off -- which is worse
	// than not having it. The tolerance draws a line at the day the table adopted the
	// tool: everything after it is held to the rule, everything before it is reported and
	// forgiven.
	//
	// It is a DATE and not a switch, so the forgiven set can only shrink, and it is
	// narrow: only two findings are tolerated, both of them about a note nobody can read
	// or a thread that points at nothing. An unknown recipient, a note in the wrong lane,
	// a malformed or duplicated id, a broken receipt line, an unowned lane and a stray
	// file all still FAIL at any date, because none of them is a thing the table's history
	// made unavoidable.
	//
	// A note whose date cannot be read AT ALL -- no parseable Date line and no UTC minute
	// in its filename -- is never tolerated, because there is nothing to compare. That is
	// the honest answer and not an oversight: a file that cannot say when it was written
	// cannot claim to predate anything.
	LegacyBefore time.Time
}

// tolerates reports whether a finding about this note is inside the legacy window.
func (o CheckOptions) tolerates(n *Note) bool {
	if o.LegacyBefore.IsZero() {
		return false
	}
	when := n.When()
	if when.IsZero() {
		return false
	}
	return when.Before(o.LegacyBefore)
}

// Check validates the whole table and returns every problem it found, sorted. This is what
// CI on a table runs, so it names every failure in one pass rather than the first.
//
// What it asserts: every note parses; every header is valid against the roster; every note
// sits in the lane its From line names; every id is well formed, carries its own lane's
// slug, and is unique across the table; every Re resolves to an id or to a path that
// exists; every receipt line parses and names something that exists; every from-* lane on
// disk has an owner in the roster; and a lane holds notes and its RECEIPTS file and
// nothing else.
//
// What it deliberately does not assert: anything about a note's body. A body is prose,
// and prose is the part of the table no tool has an opinion about.
//
// Two of its findings can be TOLERATED for old notes rather than failed; see CheckOptions.
// Check itself tolerates nothing.
func (t *Table) Check() []Problem { return t.CheckWith(CheckOptions{}) }

// CheckWith is Check with a stated tolerance. See CheckOptions.
func (t *Table) CheckWith(o CheckOptions) []Problem {
	var ps []Problem
	add := func(where, format string, args ...any) {
		ps = append(ps, Problem{Where: where, Reason: fmt.Sprintf(format, args...)})
	}
	// warn is add for the two findings the legacy tolerance can forgive. It is a separate
	// call rather than a flag on add so that every tolerated site is visible in this
	// function as a different verb.
	warn := func(n *Note, where, format string, args ...any) {
		ps = append(ps, Problem{Where: where, Reason: fmt.Sprintf(format, args...), Warn: o.tolerates(n)})
	}
	for _, lane := range t.lanesOnDisk() {
		if _, ok := t.Config.LaneOwner(lane); !ok {
			add(lane, "no participant in %s owns this lane", ConfigName)
		}
	}
	for _, stray := range t.strays {
		add(stray, "a lane holds notes (*.md) and its %s file, and nothing else", ReceiptsName)
	}
	seenID := map[string]string{}
	for i := range t.Notes {
		n := &t.Notes[i]
		if n.Parse != nil {
			warn(n, n.Path, "%s", n.Parse.Err)
			continue
		}
		if err := n.Header.Validate(t.Config); err != nil {
			add(n.Path, "%s", err)
		}
		if sender, ok := t.Config.ResolveOne(n.Header.From); ok && sender.Lane != n.Lane {
			where := n.Path
			if line := n.Header.LineOf(KeyFrom); line > 0 {
				where = fmt.Sprintf("%s:%d", n.Path, line)
			}
			add(where, "%s names %q, whose lane is %q", KeyFrom, sender.Name, sender.Lane)
		}
		if id := n.Header.ID; id != "" {
			where := n.Path
			if line := n.Header.LineOf(KeyID); line > 0 {
				where = fmt.Sprintf("%s:%d", n.Path, line)
			}
			if err := ValidID(id); err != nil {
				add(where, "%s: %s", KeyID, err)
			} else if slug := SlugOfID(id); "from-"+slug != n.Lane {
				add(where, "%s: %q carries the slug of lane %q", KeyID, id, "from-"+slug)
			}
			if prev, dup := seenID[id]; dup {
				add(where, "%s: %q is also the id of %s", KeyID, id, prev)
			} else {
				seenID[id] = n.Path
			}
		}
		for _, re := range n.Header.Re {
			if re == "new" {
				continue
			}
			if _, ok := t.Resolve(re); !ok {
				where := n.Path
				if line := n.Header.LineOf(KeyRe); line > 0 {
					where = fmt.Sprintf("%s:%d", n.Path, line)
				}
				warn(n, where, "%s: %q is neither an id on this table nor a note that exists", KeyRe, re)
			}
		}
	}
	for _, r := range t.Receipts {
		where := fmt.Sprintf("%s/%s:%d", r.Lane, ReceiptsName, r.Line)
		if r.Target == "" {
			add(where, "a receipt is <%s> <id or path>", ReceiptStampLayout)
			continue
		}
		if r.Stamp.IsZero() {
			add(where, "%q is not a UTC stamp of the form %s", r.Raw, ReceiptStampLayout)
		}
		if _, ok := t.Resolve(r.Target); !ok {
			add(where, "%q is neither an id on this table nor a note that exists", r.Target)
		}
	}
	sort.Slice(ps, func(i, j int) bool {
		if ps[i].Where != ps[j].Where {
			return ps[i].Where < ps[j].Where
		}
		return ps[i].Reason < ps[j].Reason
	})
	return ps
}

func (t *Table) lanesOnDisk() []string { return t.laneDirs }
