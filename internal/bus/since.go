package bus

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Reading a table without reading its history.
//
// THE PROPERTY THIS FILE HOLDS. `inbox` parses new + open note files, where `new` is the
// number of note files added or modified on the table since this reader's cursor and
// `open` is the number of notes this reader has already been shown and has not yet
// answered. It parses NO OTHER NOTE FILE, whatever the table's history holds. Ten thousand
// notes on the table and one new one is one parse, and the same is true at a hundred
// thousand: the cost of a read is the size of the change, never the size of the record.
//
// That is what Glenn asked for -- "O(n) where n is the number of new messages to be read,
// instead of O(m) where m is all messages sent so far" -- and the three pieces that make
// it true are the three files in cursor.go:
//
//   - the CURSOR gives the diff a place to start, so git names the changed paths instead of
//     this tool walking every lane;
//   - the OPEN list carries the notes that are still owed a reply ACROSS runs, so the
//     cursor can advance past a note without the note disappearing. Without it, either the
//     cursor could never move past an unanswered note (and the read would be O(history)
//     again the first slow week) or an unanswered note would be shown once and lost;
//   - the INDEX makes a thread resolution and an id-uniqueness test a lookup rather than
//     a scan, for the verbs that need one.
//
// And RECEIPTS is read whole on every run. That is one file per reader, of one short line
// per note that reader has ever heard -- a line scan and never a parse -- and reading it
// whole is what makes "heard" survive a cursor that has moved past the receipt.
//
// WHAT AN INCREMENTAL RUN CANNOT SAY. It reports on the change set and on the open list,
// so it names the unreadable files AMONG THOSE, not every unreadable file on the table. A
// reader who wants the whole picture asks for it: `--full` walks everything and is what
// adoption and CI on main use. The scope line says which of the two happened, on every
// run, because a listing that does not say what it looked at is a listing a reader will
// mistake for everything.

// Scope is what one run looked at, for the line that says so.
type Scope struct {
	// Full is set when the run walked the whole table.
	Full bool
	// From is the commit an incremental run started at, or "" for a full one.
	From string
	// Changed is how many lane paths the diff named.
	Changed int
}

// Mode is "full" or "since", for the output line.
func (s Scope) Mode() string {
	if s.Full {
		return "full"
	}
	return "since"
}

// InboxResult is one incremental inbox run.
type InboxResult struct {
	// Items is what to print: the notes that were open before this run and are still
	// open, plus the notes that are new to this reader, newest first.
	Items []InboxItem
	// Unreadable is the files in the change set (and the open list) that would not parse.
	Unreadable []*Note
	// Open is the OPEN list to write back: the survivors, in the order they were already
	// in, then what this run added.
	Open []OpenEntry
	// New is how many notes this run added to the open list.
	New int
	// Legacy is how many notes this run left off the open list because they are dated
	// before the switch-day line. They are counted and never listed one by one; see
	// LegacyLine.
	Legacy int
}

// LegacyLine is the switch-day line: the UTC date before which a note is not carried on
// this reader's open list.
//
// THE PROBLEM IT SOLVES, from the day the family's own table adopted this tool. The first
// `inbox --as Rowan --full` reported 657 notes open -- 623 notes and 34 receipts, most of
// them from months before anybody could have receipted them with this -- and because the
// open list is what makes the cursor able to move, every run after it reported the same
// 657, forever, until each one was answered or receipted one at a time. Nobody was going
// to do that, and a listing nobody reads is a listing that hides the one new note in it.
//
// So a line is drawn on a date: a note dated before it is not carried, is not listed, and
// is counted on ONE line so that the reader knows exactly how much they took as read.
// Nothing is deleted, nothing is marked answered, and no note anywhere on the table is
// changed -- the notes are still there, still readable, still findable by `check --full`
// and by opening the lane in a browser. What the line changes is one reader's own open
// list, which is the one thing on the table that was theirs alone anyway.
//
// A note whose date cannot be read AT ALL -- no parseable Date line, and no day at the
// front of its filename either -- is never legacy, on the same rule the check tolerance
// uses: a file that cannot say when it was written cannot claim to predate anything, and
// the safe direction for a note nobody can date is to carry it. See Note.legacyDay for
// what counts as saying it.
type LegacyLine struct {
	// Before is the moment, or the zero time for no line at all.
	Before time.Time
}

// covers reports whether a note is on the old side of the line.
func (l LegacyLine) covers(n *Note) bool {
	if l.Before.IsZero() {
		return false
	}
	when := n.legacyDay()
	if when.IsZero() {
		return false
	}
	return when.Before(l.Before)
}

// InboxSince computes a reader's listing from a change set and their OPEN list, and never
// touches a note outside the two.
//
// The rule it implements, stated as a rule: a note is OPEN for me from the moment I am
// shown it until something of mine answers it -- a reply in my lane carrying its id or its
// path on a Re line, or a line in my RECEIPTS naming it. "Shown" is the load-bearing word.
// The cursor advances past a note the run after it arrives, so without a memory the note
// would fall out of every later listing while still being owed an answer; the OPEN list IS
// that memory, and it is the reason the cursor is allowed to move at all.
func InboxSince(root string, c *Config, me Participant, changed []string, open []OpenEntry, maxWords int, legacy LegacyLine) (InboxResult, error) {
	var res InboxResult
	if me.Lane == "" {
		return res, fmt.Errorf("%q has no lane on this table, so nothing can answer for them", me.Name)
	}
	parsed := newNoteCache(root)

	// What have I answered? Two sources, and both are cheap.
	//
	// My own lane's CATALOGUE first: one line per note I have sent, carrying that note's
	// Re targets, so every thread I have ever answered is a line scan away and no note of
	// mine is opened. Without it a note I answered last month and that somebody EDITED
	// today would come back into my open list, because the reply that closed it is behind
	// the cursor and the edit is in front of it.
	//
	// THE GAP IN THAT, exactly: the catalogue holds the replies SEND wrote, and a reply
	// written by hand -- in a browser, which this table's whole form exists to allow -- has
	// no line in it. Such a reply closes its thread only while it is still in the change
	// set; once the cursor moves past it, this run cannot see it, and an edit to the note it
	// answered brings that note back into the open list. The failure direction is RE-SHOW
	// and never loss: nothing is dropped, a reader is asked twice. `check --full` warns
	// about every note with no INDEX line and `--rebuild-index` writes them, which is the
	// repair; see the warning in noteChecker.check below.
	answered := map[string]bool{}
	mine, err := ReadLaneIndex(root, me.Lane)
	if err != nil {
		return res, err
	}
	for _, e := range mine {
		for _, re := range e.Re {
			answered[re] = true
		}
	}
	// Then my own changed files, which cover the reply written since the catalogue was
	// last appended to -- and every reply written by hand, which is in no catalogue at all.
	for _, path := range changed {
		if laneOf(path) != me.Lane || !isNotePath(path) {
			continue
		}
		n, err := parsed.get(path)
		if err != nil || n.Parse != nil {
			// A note of MY OWN that will not parse is my business and check names it; it
			// simply answers nothing.
			continue
		}
		for _, re := range n.Header.Re {
			answered[re] = true
		}
	}

	// RECEIPTS is read whole for the same reason: one short line per note I have ever
	// heard, a line scan and never a parse, and reading it whole is what makes "heard"
	// outlive a cursor that has moved past the receipt.
	heard, herr := readReceiptTargets(root, me.Lane)
	if herr != nil {
		return res, herr
	}

	// The survivors, in the order they were already in.
	var keep []OpenEntry
	inOpen := map[string]bool{}
	for _, e := range open {
		if answered[e.Target()] || answered[e.Path] {
			continue
		}
		inOpen[e.Path] = true
		keep = append(keep, e)
	}

	// What is new: a changed note outside my lane, addressed to me, that I am not already
	// carrying and have not already answered.
	var fresh []OpenEntry
	for _, path := range changed {
		if laneOf(path) == me.Lane || !isNotePath(path) || inOpen[path] {
			continue
		}
		n, err := parsed.get(path)
		if err != nil {
			continue
		}
		if n.Parse != nil {
			res.Unreadable = append(res.Unreadable, n)
			continue
		}
		if !addressedTo(c, n, me) {
			continue
		}
		if answered[n.Header.ID] || answered[path] {
			continue
		}
		inOpen[path] = true
		fresh = append(fresh, OpenEntry{ID: n.Header.ID, Path: path})
	}
	sort.Slice(fresh, func(i, j int) bool { return fresh[i].Path < fresh[j].Path })
	res.New = len(fresh)

	// Render. An open entry whose file has gone is dropped from the list and NAMED: a note
	// I was shown and can no longer read is a fact about the table, and dropping it in
	// silence is the failure the unreadable listing exists to end.
	var final []OpenEntry
	for _, e := range append(keep, fresh...) {
		n, err := parsed.get(e.Path)
		if err != nil {
			res.Unreadable = append(res.Unreadable, &Note{
				Path:  e.Path,
				Lane:  laneOf(e.Path),
				Parse: &ParseError{fmt.Errorf("this note was open and is no longer on the table: %w", err)},
			})
			continue
		}
		if n.Parse != nil {
			res.Unreadable = append(res.Unreadable, n)
			final = append(final, e)
			continue
		}
		// The switch-day line, applied in ONE place so that a note it covers is left off
		// the open list whether it arrived in this change set or has been carried since
		// before the line was drawn. Leaving it off is the whole of what happens to it: it
		// is counted, and the note is untouched.
		if legacy.covers(n) {
			res.Legacy++
			continue
		}
		final = append(final, e)
		res.Items = append(res.Items, item(c, n, me, heard[e.Target()] || heard[e.Path], maxWords))
	}
	res.Open = final
	sortInbox(res.Items)
	sort.Slice(res.Unreadable, func(i, j int) bool { return res.Unreadable[i].Path < res.Unreadable[j].Path })
	return res, nil
}

// SplitLegacy is the switch-day line applied to a FULL walk's listing: the notes that stay,
// and how many were left off. It is the same rule InboxSince applies to a change set, kept
// in one place so the two reads cannot draw the line differently -- which matters most on
// the one run where it is drawn, because a `--full` read is what writes the open list every
// later run inherits.
func SplitLegacy(items []InboxItem, legacy LegacyLine) (keep []InboxItem, covered int) {
	if legacy.Before.IsZero() {
		return items, 0
	}
	keep = make([]InboxItem, 0, len(items))
	for _, it := range items {
		if legacy.covers(it.Note) {
			covered++
			continue
		}
		keep = append(keep, it)
	}
	return keep, covered
}

// OpenFromFull is the OPEN list a --full run implies: every note the full walk found still
// open for this reader. It is how a reader adopts the cursor on a table that already
// exists, and how `--full --advance` repairs an OPEN list that drifted.
func OpenFromFull(items []InboxItem) []OpenEntry {
	out := make([]OpenEntry, 0, len(items))
	for _, it := range items {
		out = append(out, OpenEntry{ID: it.Note.Header.ID, Path: it.Note.Path})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// item builds one listing row.
func item(c *Config, n *Note, me Participant, heard bool, maxWords int) InboxItem {
	to, _ := n.Header.Recipients(c)
	addr := "cc"
	if contains(to, me.Name) {
		addr = "to"
	}
	from := n.Header.From
	if p, ok := c.ResolveOne(n.Header.From); ok {
		from = p.Name
	}
	return InboxItem{
		Note:    n,
		From:    from,
		Address: addr,
		Receipt: IsReceipt(*n, maxWords),
		Heard:   heard,
	}
}

func addressedTo(c *Config, n *Note, me Participant) bool {
	to, cc := n.Header.Recipients(c)
	return contains(to, me.Name) || contains(cc, me.Name)
}

// sortInbox is the one ordering, newest first, shared by the full and the incremental
// listing so the two cannot drift.
func sortInbox(items []InboxItem) {
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i].Note.When(), items[j].Note.When()
		if !a.Equal(b) {
			return a.After(b)
		}
		return items[i].Note.Path > items[j].Note.Path
	})
}

// noteCache parses each path at most once per run, so a note that is both in the change
// set and in the open list costs one parse and the count in the test means what it says.
type noteCache struct {
	root string
	by   map[string]*Note
}

func newNoteCache(root string) *noteCache { return &noteCache{root: root, by: map[string]*Note{}} }

func (nc *noteCache) get(path string) (*Note, error) {
	if n, ok := nc.by[path]; ok {
		return n, nil
	}
	full := filepath.Join(nc.root, filepath.FromSlash(path))
	if err := insideRoot(nc.root, full); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(full)
	if err != nil {
		return nil, err
	}
	n, perr := ParseNote(path, string(raw))
	if perr != nil {
		n = Note{Path: path, Lane: laneOf(path), Parse: &ParseError{perr}}
	}
	nc.by[path] = &n
	return &n, nil
}

// readReceiptTargets reads one lane's RECEIPTS into the set of things it records. It is a
// line scan and parses no note.
func readReceiptTargets(root, lane string) (map[string]bool, error) {
	out := map[string]bool{}
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(lane+"/"+ReceiptsName)))
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	for _, r := range records(string(raw)) {
		if _, target, ok := strings.Cut(r.text, " "); ok {
			out[strings.TrimSpace(target)] = true
		}
	}
	return out, nil
}

func laneOf(path string) string {
	if i := strings.IndexByte(path, '/'); i > 0 {
		return path[:i]
	}
	return ""
}

// isNotePath reports whether a changed path is a note rather than one of a lane's state
// files. A lane's state files change on almost every run and are not notes; reading one as
// a note would report a parse failure to every reader on the table.
func isNotePath(path string) bool {
	if !strings.HasSuffix(path, ".md") {
		return false
	}
	return strings.Count(path, "/") == 1 && strings.HasPrefix(path, "from-")
}

// ------------------------------------------------------------------------ check, likewise

// noteChecker holds every finding that can be decided from ONE note plus two lookups, so
// that the full walk and the incremental one cannot say different things about the same
// file. They differ only in where the lookups come from: the full walk answers them from
// the table it has just read, the incremental one from the catalogue.
type noteChecker struct {
	c *Config
	// resolves answers whether a Re target names something on this table.
	resolves func(target string) bool
	// idOwner answers which note already holds this id, if any.
	idOwner func(id string) (path string, ok bool)
	// indexed answers whether this note has a line in its lane's INDEX. A note with no id
	// is never asked about: a legacy note is addressed by path and inventing a catalogue
	// entry keyed on an id it does not have would be inventing the id.
	indexed func(n *Note) bool
	opts    CheckOptions
	seenID  map[string]string
}

func (k *noteChecker) check(n *Note) []Problem {
	var ps []Problem
	add := func(where, format string, args ...any) {
		ps = append(ps, Problem{Where: where, Reason: fmt.Sprintf(format, args...)})
	}
	warn := func(where, format string, args ...any) {
		ps = append(ps, Problem{Where: where, Reason: fmt.Sprintf(format, args...), Warn: k.opts.tolerates(n)})
	}
	// THE HEADER'S OWN FINDINGS ARE INSIDE THE TOLERANCE, and an earlier revision had them
	// outside it. A dry run over the family's real table, with the line drawn at the day it
	// adopted this tool, still failed 163 times: 109 notes with no Subject line, 21 whose
	// To names somebody the roster does not know, 16 whose From does, 10 whose Cc does.
	// Every one of them is a note written by hand before there was a roster to check
	// against, which is exactly what this tolerance is for -- and a first run that fails
	// 163 times is the wall of red the tolerance exists to prevent, whatever the findings
	// in it are called. So a note dated before the line WARNS on its header and fails on
	// nothing about it; a note dated on or after it fails as before. The narrow findings
	// stay narrow: a note in the wrong lane, a malformed or duplicated id, a broken receipt
	// line, an unowned lane and a stray file all still FAIL at any date, because none of
	// them is a thing a table's history made unavoidable.
	if err := n.Header.Validate(k.c); err != nil {
		warn(n.Path, "%s", err)
	}
	if sender, ok := k.c.ResolveOne(n.Header.From); ok && sender.Lane != n.Lane {
		add(atLine(n, KeyFrom), "%s names %q, whose lane is %q", KeyFrom, sender.Name, sender.Lane)
	}
	if id := n.Header.ID; id != "" {
		where := atLine(n, KeyID)
		if err := ValidID(id); err != nil {
			add(where, "%s: %s", KeyID, err)
		} else if slug := SlugOfID(id); "from-"+slug != n.Lane {
			add(where, "%s: %q carries the slug of lane %q", KeyID, id, "from-"+slug)
		}
		if prev, dup := k.seenID[id]; dup {
			add(where, "%s: %q is also the id of %s", KeyID, id, prev)
		} else {
			k.seenID[id] = n.Path
		}
		if other, taken := k.idOwner(id); taken && other != n.Path {
			add(where, "%s: %q is also the id of %s", KeyID, id, other)
		}
		// A note with no INDEX line is a WARN and never a failure, at any date. The
		// catalogue is a cache and the notes are the record: a person who wrote a note by
		// hand in a browser -- which this table's whole form exists to allow -- has not
		// broken anything. A catalogue line that DISAGREES with a note is a different
		// matter and does fail: see CheckIndex, where a wrong line could resolve a thread
		// to the wrong note.
		//
		// The warning says what it COSTS, and the cost is not the same everywhere. In
		// somebody else's lane it is one lookup an incremental check answers from the
		// filesystem instead. In YOUR OWN lane it is a reply the incremental read cannot
		// see: `answered` is built from your lane's INDEX plus your own files in the change
		// set, so a reply you wrote by hand stops closing its thread the moment it falls
		// behind your cursor, and an edit to the note it answered puts that note back in
		// your open list. Never a lost note -- a re-shown one -- but a reader who cannot
		// tell the two apart will answer twice.
		if !k.indexed(n) {
			ps = append(ps, Problem{
				Where:  n.Path,
				Reason: fmt.Sprintf("no line in %s names this note; in your own lane that is a reply the incremental read cannot see once it falls behind your cursor, so a note it answers can re-appear as open; check --full --rebuild-index writes one", IndexPath(n.Lane)),
				Warn:   true,
			})
		}
	}
	for _, re := range n.Header.Re {
		if re == "new" {
			continue
		}
		if !k.resolves(re) {
			warn(atLine(n, KeyRe), "%s: %q is neither an id on this table nor a note that exists", KeyRe, re)
		}
	}
	return ps
}

// CheckSince validates only the lane files that changed since a commit, resolving threads
// and ids through the INDEX instead of through the table.
//
// It asserts everything Check asserts that can be decided about one file plus the
// catalogue. It also validates the lane state files that changed, because those are files
// on the table like any other and a malformed OPEN is a reader who refuses on their next
// run with nothing on the table saying why.
//
// What it CANNOT assert, and says so by being named `--since` rather than `check`: a
// property of the WHOLE table. An unowned lane that nothing touched, a stray file that has
// been there a month, a duplicate id between two notes neither of which changed and
// neither of which is in an INDEX -- those are found by `check --full`, which is what CI
// on main runs and what a table runs before it trusts a first `--since`.
func CheckSince(root string, c *Config, idx *Index, changed []string, o CheckOptions) ([]Problem, CheckStats) {
	var ps []Problem
	var stats CheckStats
	lanes := map[string]bool{}
	add := func(where, format string, args ...any) {
		ps = append(ps, Problem{Where: where, Reason: fmt.Sprintf(format, args...)})
	}
	parsed := newNoteCache(root)
	k := &noteChecker{
		c:        c,
		resolves: idx.resolves,
		idOwner: func(id string) (string, bool) {
			e, ok := idx.ByID(id)
			if !ok {
				return "", false
			}
			return e.Path, true
		},
		indexed: func(n *Note) bool { _, ok := idx.ByID(n.Header.ID); return ok },
		opts:    o,
		seenID:  map[string]string{},
	}
	unowned := map[string]bool{}
	for _, path := range changed {
		lane := laneOf(path)
		lanes[lane] = true
		if _, ok := c.LaneOwner(lane); !ok {
			if !unowned[lane] {
				unowned[lane] = true
				add(lane, "no participant in %s owns this lane", ConfigName)
			}
			continue
		}
		base := filepath.Base(path)
		if base == ReceiptsName {
			found, n := checkReceipts(root, lane, idx.resolves)
			stats.Receipts += n
			ps = append(ps, found...)
			continue
		}
		if isLaneStateFile(base) {
			ps = append(ps, checkLaneStateFile(root, lane, base)...)
			continue
		}
		// A state file's stranded temporary is stepped over here exactly as the full walk
		// steps over it, so the two modes cannot say different things about one file.
		if isLaneStateTemp(base) {
			continue
		}
		if !isNotePath(path) {
			add(path, "a lane holds notes (*.md), its %s, and nothing else", strings.Join(laneStateFiles, ", "))
			continue
		}
		n, err := parsed.get(path)
		if err != nil {
			add(path, "%s", err)
			continue
		}
		stats.Notes++
		if n.Parse != nil {
			ps = append(ps, Problem{Where: path, Reason: n.Parse.Err.Error(), Warn: o.tolerates(n)})
			continue
		}
		ps = append(ps, k.check(n)...)
	}
	stats.Lanes = len(lanes)
	sortProblems(ps)
	return ps, stats
}

// CheckStats is what a run looked at, for the OK line. A check that passed over two files
// and one that passed over the whole table are not the same run, and the line says which.
type CheckStats struct{ Notes, Lanes, Receipts int }

// resolves answers a Re or receipt target from the catalogue, falling back to the one
// thing a catalogue cannot hold: a note written before ids existed, which is named by path
// and is answered by asking the filesystem whether that path is a note. Both are lookups
// and neither is a scan.
func (i *Index) resolves(target string) bool {
	if _, ok := i.byID[target]; ok {
		return true
	}
	if _, ok := i.byPath[target]; ok {
		return true
	}
	if !isNotePath(target) {
		return false
	}
	st, err := os.Stat(filepath.Join(i.root, filepath.FromSlash(target)))
	return err == nil && !st.IsDir()
}

// checkReceipts validates one lane's RECEIPTS file with the given resolver. Every line, not
// only the new ones: the file is short, it is one line per note that lane ever heard, and
// reading it whole is what the answered rule already does.
func checkReceipts(root, lane string, resolves func(string) bool) ([]Problem, int) {
	var ps []Problem
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(lane+"/"+ReceiptsName)))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, 0
		}
		return []Problem{{Where: lane + "/" + ReceiptsName, Reason: err.Error()}}, 0
	}
	lines := records(string(raw))
	for _, r := range lines {
		where := fmt.Sprintf("%s/%s:%d", lane, ReceiptsName, r.line)
		stamp, target, ok := strings.Cut(r.text, " ")
		target = strings.TrimSpace(target)
		if !ok || target == "" {
			ps = append(ps, Problem{Where: where, Reason: fmt.Sprintf("a receipt is <%s> <id or path>", ReceiptStampLayout)})
			continue
		}
		if _, perr := time.Parse(ReceiptStampLayout, stamp); perr != nil {
			ps = append(ps, Problem{Where: where, Reason: fmt.Sprintf("%q is not a UTC stamp of the form %s", stamp, ReceiptStampLayout)})
		}
		if !resolves(target) {
			ps = append(ps, Problem{Where: where, Reason: fmt.Sprintf("%q is neither an id on this table nor a note that exists", target)})
		}
	}
	return ps, len(lines)
}

// checkLaneStateFile reads one of a lane's state files and reports it if it will not parse.
func checkLaneStateFile(root, lane, name string) []Problem {
	var err error
	switch name {
	case CursorName:
		_, err = ReadCursor(root, lane)
	case OpenName:
		_, err = ReadOpen(root, lane)
	case IndexName:
		_, err = ReadLaneIndex(root, lane)
	}
	if err != nil {
		return []Problem{{Where: lane + "/" + name, Reason: err.Error()}}
	}
	return nil
}

// CheckIndex is the INDEX half of `check --full`: the catalogue agrees with the lane, in
// both directions. An entry naming a note that is not there, or naming it with the wrong
// id, is a catalogue that would resolve a thread to the wrong note; a note with an id and
// no entry is a catalogue that would let a duplicate id through. `--rebuild-index` writes
// both away, from the notes, which are the record either way.
func CheckIndex(c *Config, t *Table, idx *Index) []Problem {
	var ps []Problem
	for _, e := range idx.Entries {
		where := fmt.Sprintf("%s:%d", IndexPath(e.Lane), e.Line)
		n, ok := t.NoteByPath(e.Path)
		if !ok {
			ps = append(ps, Problem{Where: where, Reason: fmt.Sprintf("names %q, which is not a note on this table", e.Path)})
			continue
		}
		if n.Parse != nil {
			continue
		}
		if n.Header.ID != e.ID {
			ps = append(ps, Problem{Where: where, Reason: fmt.Sprintf("gives %s the id %q; the note says %q", e.Path, e.ID, n.Header.ID)})
			continue
		}
		if want := IndexLine(IndexEntryFor(c, *n)); want != IndexLine(e) {
			ps = append(ps, Problem{Where: where, Reason: fmt.Sprintf("does not match the note; check --full --rebuild-index writes %q", truncate(want, 120))})
		}
	}
	for i := range t.Notes {
		n := &t.Notes[i]
		if n.Parse != nil || n.Header.ID == "" {
			continue
		}
		if _, ok := idx.ByID(n.Header.ID); !ok {
			ps = append(ps, Problem{
				Where:  n.Path,
				Reason: fmt.Sprintf("no line in %s names this note; in your own lane that is a reply the incremental read cannot see once it falls behind your cursor, so a note it answers can re-appear as open; check --full --rebuild-index writes one", IndexPath(n.Lane)),
				Warn:   true,
			})
		}
	}
	sortProblems(ps)
	return ps
}

func atLine(n *Note, key string) string {
	if line := n.Header.LineOf(key); line > 0 {
		return fmt.Sprintf("%s:%d", n.Path, line)
	}
	return n.Path
}

func sortProblems(ps []Problem) {
	sort.Slice(ps, func(i, j int) bool {
		if ps[i].Where != ps[j].Where {
			return ps[i].Where < ps[j].Where
		}
		return ps[i].Reason < ps[j].Reason
	})
}
