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
// THE PROPERTY THIS FILE HOLDS. `inbox` parses exactly the NEW note files: the ones added
// or modified on the table since this reader's cursor. It parses NO OTHER NOTE FILE,
// whatever the table's history holds and whatever this reader is carrying open. Ten
// thousand notes on the table and one new one is one parse; ten thousand notes, five
// hundred of them open, and one new one is still one parse.
//
// THAT IS THE SECOND HALF OF THE ANSWER TO GLENN, and the first version only had the
// first. He asked for "O(n) where n is the number of new messages to be read, instead of
// O(m) where m is all messages sent so far", and the cursor gave that -- but the read was
// then O(new + open), because every open note was re-opened and re-parsed on every run to
// print its line and to decide whether it had been heard. His answer to that: "O(new +
// open) is not great. Can we make it O(new)." It can, and this is how:
//
//   - the CURSOR gives the diff a place to start, so git names the changed paths instead of
//     this tool walking every lane;
//   - the OPEN list carries the notes that are still owed a reply ACROSS runs, so the
//     cursor can advance past a note without the note disappearing -- and since OPEN v2 it
//     carries each note's DISPLAY LINE and its heard flag, so a carried note is printed
//     from the list and never opened again;
//   - the INDEX makes a thread resolution and an id-uniqueness test a lookup rather than
//     a scan, for the verbs that need one.
//
// So CLOSING IS DRIVEN BY THE NEW NOTES, and by nothing else. A new note of mine carrying
// `Re: <id or path>` closes the open entry it names; a receipt of mine, which reaches this
// run as my own RECEIPTS file in the change set, marks its entry heard. Both are inside the
// walk of what changed. Neither my lane's INDEX nor my RECEIPTS is read whole on a run
// where they did not change, which is what the first version did on every run.
//
// WHAT THAT COSTS, stated here rather than left to be found. `answered` is now built from
// the change set alone, so a reply of mine that has fallen BEHIND my cursor cannot close a
// thread on a later run: if somebody edits the note it answered, that note comes back into
// my open list and I am shown it again. The direction is re-show and never loss -- a reader
// is asked twice, never told a note is answered when it is not -- and a `--full --advance`
// settles it. An open note whose FILE was deleted stays on the list, printed from the
// snapshot in OPEN, until a `--full` read rebuilds the list without it: a deletion is not in
// the change set (`--diff-filter=AM`) and finding one would cost a stat per open note, which
// is the O(open) this file exists to remove.
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

// InboxResult is one inbox run, incremental or full.
type InboxResult struct {
	// Open is BOTH what to print and what to write back: the survivors, in the order they
	// were already in, then what this run added. One list and not two, because an entry
	// carries its own display line now -- a second list would be the same rows in a
	// different order, and two spellings of one thing drift.
	Open []OpenEntry
	// Unreadable is the files in the change set, and the unreadable entries on the open
	// list, that would not parse THIS RUN, with the reason. The reason is not stored in
	// OPEN -- an entry records only that it is unreadable -- so it is carried out here for
	// the run that produced it.
	Unreadable []*Note
	// Unaddressed is the notes this run saw that reach no reader at all. On an incremental
	// run it is the reader's OWN lane and nothing else -- the one place the reader can fix
	// one -- and a full run fills it with every such note on the table. See unaddressed.go.
	Unaddressed []Unaddressed
	// New is how many notes this run added to the open list.
	New int
	// Legacy is how many notes this run left off the open list because they are dated
	// before the switch-day line. They are counted and never listed one by one; see
	// LegacyLine.
	Legacy int
}

// Notes, Receipts and Heard are the three counts on the OK line, taken from the open list
// rather than from a second walk. A heard note is in neither of the first two: I have
// answered the sender's question about whether it arrived, and the open count is what is
// still waiting on me.
func (r InboxResult) Counts() (notes, receipts, heard int) {
	for _, e := range r.Open {
		switch {
		case e.Kind == OpenUnreadable:
		case e.Heard:
			heard++
		case e.Kind == OpenReceipt:
			receipts++
		default:
			notes++
		}
	}
	return notes, receipts, heard
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

// covers reports whether a day is on the old side of the line. A zero day is never covered:
// a file that cannot say when it was written cannot claim to predate anything.
func (l LegacyLine) covers(day time.Time) bool {
	if l.Before.IsZero() || day.IsZero() {
		return false
	}
	return day.Before(l.Before)
}

// day is an open entry's day, for the switch-day line and for nothing else, and it is read
// WITHOUT opening the note: the entry's recorded moment when it has one, and otherwise the
// YYYY-MM-DD at the front of its filename. That is exactly Note.legacyDay -- the entry's
// Date field is Note.When rendered -- so the line falls in the same place whether it is
// drawn over a change set, over a carried entry, or over a full walk.
func (e OpenEntry) day() time.Time {
	if when := e.when(); !when.IsZero() {
		return when
	}
	return filenameDay(e.Path)
}

// when is the entry's own moment, for ordering the listing: Note.When as it stood when the
// note went open, and the zero time for a note that had none. Unlike day it does NOT fall
// back to the filename, because the listing's order is the one Note.When gives and widening
// it here would put an entry in a different place from the note it stands for.
func (e OpenEntry) when() time.Time {
	if e.Date == "" {
		return time.Time{}
	}
	when, err := time.Parse(ReceiptStampLayout, e.Date)
	if err != nil {
		return time.Time{}
	}
	return when.UTC()
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

	// ONE WALK OF THE CHANGE SET, and everything this run knows comes out of it.
	//
	// What have I answered? A note of MY OWN in the change set, carrying a Re line. That is
	// every reply I have written since my cursor, whether `send` wrote it or I typed it in a
	// browser -- the two are the same file and this does not care which.
	//
	// What have I heard? My own RECEIPTS, and only when it is IN THE CHANGE SET, which is
	// exactly the runs on which I have receipted something since my cursor. It is read whole
	// then -- one short line per note I have ever heard, a line scan and never a parse -- and
	// the flags it sets are written into OPEN, so the next run knows what was heard without
	// reading the file at all. That is the difference between this and the first version,
	// which read RECEIPTS whole on every run for a fact that changes about once a day.
	answered := map[string]bool{}
	heard := map[string]bool{}
	for _, path := range changed {
		if path == me.Lane+"/"+ReceiptsName {
			targets, err := readReceiptTargets(root, me.Lane)
			if err != nil {
				return res, err
			}
			for target := range targets {
				heard[target] = true
			}
			continue
		}
		if laneOf(path) != me.Lane || !isNotePath(path) {
			continue
		}
		n, err := parsed.get(path)
		if err != nil || n.Parse != nil {
			// A note of MY OWN that will not parse is my business and check names it; it
			// simply answers nothing.
			continue
		}
		// A note of my own that reaches NOBODY is also my business, and it is the one thing
		// about my own lane this listing says out loud, on every run and in every mode. See
		// unaddressed.go: `send` cannot write one, so this is a note I typed by hand whose
		// To line named nobody, and nothing else on the table would ever tell me.
		if reason, yes := UnaddressedReason(c, n); yes {
			res.Unaddressed = append(res.Unaddressed, Unaddressed{Path: n.Path, Reason: reason})
		}
		for _, re := range n.Header.Re {
			answered[re] = true
		}
	}
	settled := func(e OpenEntry) bool { return answered[e.Target()] || answered[e.Path] }
	told := func(e OpenEntry) bool { return heard[e.Target()] || heard[e.Path] }

	// The survivors, in the order they were already in, PRINTED FROM THE LIST. A readable
	// entry costs nothing here: no open, no parse, no stat. It carries its own line.
	var keep []OpenEntry
	inOpen := map[string]bool{}
	for _, e := range open {
		if settled(e) {
			continue
		}
		if e.Kind == OpenUnreadable {
			// THE ONE ENTRY THAT COSTS A PARSE PER RUN, and it is carried on purpose. A file
			// this tool cannot read has no `To:` line, so it cannot be listed as a note and it
			// cannot be dropped either -- dropping it is how the first version lost it: named
			// once on a `--full` read, absent from OPEN, and never mentioned again by any
			// incremental run. So it is carried as an `unreadable` entry and re-checked, which
			// costs ONE parse per unreadable entry per run and nothing for anybody else's
			// notes. It leaves the list when the file parses -- becoming an ordinary entry if
			// it turns out to be addressed to me, and going quietly if it is not -- or when I
			// receipt it, which is how a reader says "I have seen this file" about something
			// with no id to answer.
			if told(e) {
				continue
			}
			n, err := parsed.get(e.Path)
			if err != nil {
				res.Unreadable = append(res.Unreadable, &Note{
					Path:  e.Path,
					Lane:  laneOf(e.Path),
					Parse: &ParseError{fmt.Errorf("this file was open and is no longer on the table: %w", err)},
				})
				continue
			}
			if n.Parse != nil {
				res.Unreadable = append(res.Unreadable, n)
				inOpen[e.Path] = true
				keep = append(keep, e)
				continue
			}
			if !addressedTo(c, n, me) || answered[n.Header.ID] {
				continue
			}
			// Its id is only knowable now that it parses, so the heard flag is asked for by
			// id here: the path was already asked for above, and answered no.
			e = openEntryFor(c, n, me, heard[n.Header.ID], maxWords)
		} else if told(e) {
			e.Heard = true
		}
		inOpen[e.Path] = true
		keep = append(keep, e)
	}

	// What is new: a changed note outside my lane, addressed to me, that I am not already
	// carrying and have not already answered. This is the whole of what this run parses.
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
			inOpen[path] = true
			fresh = append(fresh, OpenEntry{Kind: OpenUnreadable, Path: path})
			continue
		}
		if !addressedTo(c, n, me) {
			continue
		}
		if answered[n.Header.ID] || answered[path] {
			continue
		}
		inOpen[path] = true
		fresh = append(fresh, openEntryFor(c, n, me, heard[n.Header.ID] || heard[path], maxWords))
	}
	sort.Slice(fresh, func(i, j int) bool { return fresh[i].Path < fresh[j].Path })
	res.New = len(fresh)

	// The switch-day line, applied in ONE place so that a note it covers is left off the
	// open list whether it arrived in this change set or has been carried since before the
	// line was drawn. Leaving it off is the whole of what happens to it: it is counted, and
	// the note is untouched.
	res.Open, res.Legacy = SplitLegacy(append(keep, fresh...), legacy)
	sort.Slice(res.Unreadable, func(i, j int) bool { return res.Unreadable[i].Path < res.Unreadable[j].Path })
	sort.Slice(res.Unaddressed, func(i, j int) bool { return res.Unaddressed[i].Path < res.Unaddressed[j].Path })
	return res, nil
}

// SplitLegacy is the switch-day line applied to an open list: the entries that stay, and how
// many were left off. Both reads go through it -- the incremental one over what it kept and
// added, the full one over what the walk found -- so the two cannot draw the line
// differently, which matters most on the one run where it is drawn, because a `--full` read
// is what writes the open list every later run inherits.
func SplitLegacy(entries []OpenEntry, legacy LegacyLine) (keep []OpenEntry, covered int) {
	if legacy.Before.IsZero() {
		return entries, 0
	}
	keep = make([]OpenEntry, 0, len(entries))
	for _, e := range entries {
		if legacy.covers(e.day()) {
			covered++
			continue
		}
		keep = append(keep, e)
	}
	return keep, covered
}

// OpenFromFull is the OPEN list a --full run implies: every note the full walk found still
// open for this reader, plus every file it could not read. It is how a reader adopts the
// cursor on a table that already exists, how `--full --advance` repairs an OPEN list that
// drifted, and the one place a heard flag is rebuilt from RECEIPTS rather than carried.
//
// THE UNREADABLE FILES ARE CARRIED, and until this was written they were not. A `--full`
// read named them and then wrote an open list without them, so the next incremental run --
// which sees only what changed -- never mentioned them again. A file somebody wrote, on the
// table, that its reader is told about exactly once and then never again is the same failure
// as a lost push with a slower fuse. They go on the list, they are re-checked, and they come
// off it when they parse or are receipted.
func OpenFromFull(items []InboxItem, unreadable []*Note) []OpenEntry {
	out := make([]OpenEntry, 0, len(items)+len(unreadable))
	for _, it := range items {
		kind := OpenNote
		if it.Receipt {
			kind = OpenReceipt
		}
		date := ""
		if when := it.Note.When(); !when.IsZero() {
			date = when.Format(ReceiptStampLayout)
		}
		out = append(out, OpenEntry{
			ID:      it.Note.Header.ID,
			Kind:    kind,
			Heard:   it.Heard,
			From:    it.From,
			Addr:    it.Address,
			Date:    date,
			Path:    it.Note.Path,
			Subject: it.Note.Header.Subject,
		})
	}
	for _, n := range unreadable {
		out = append(out, OpenEntry{Kind: OpenUnreadable, Path: n.Path})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// openEntryFor builds the open entry for a note this run parsed: everything a later run
// needs in order to print it without opening it again.
func openEntryFor(c *Config, n *Note, me Participant, heard bool, maxWords int) OpenEntry {
	to, _ := n.Header.Recipients(c)
	addr := "cc"
	if contains(to, me.Name) {
		addr = "to"
	}
	from := n.Header.From
	if p, ok := c.ResolveOne(n.Header.From); ok {
		from = p.Name
	}
	kind := OpenNote
	if IsReceipt(*n, maxWords) {
		kind = OpenReceipt
	}
	date := ""
	if when := n.When(); !when.IsZero() {
		date = when.Format(ReceiptStampLayout)
	}
	return OpenEntry{
		ID:      n.Header.ID,
		Kind:    kind,
		Heard:   heard,
		From:    from,
		Addr:    addr,
		Date:    date,
		Path:    n.Path,
		Subject: n.Header.Subject,
	}
}

func addressedTo(c *Config, n *Note, me Participant) bool {
	to, cc := n.Header.Recipients(c)
	return contains(to, me.Name) || contains(cc, me.Name)
}

// SortForListing puts an open list in the order it is PRINTED in -- newest first, path
// descending to break a tie -- on a copy of the caller's slice, because the order the list
// is WRITTEN in is the order its entries arrived in and a run must not reshuffle a file to
// print it.
func SortForListing(entries []OpenEntry) []OpenEntry {
	out := append([]OpenEntry(nil), entries...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].when(), out[j].when()
		if !a.Equal(b) {
			return a.After(b)
		}
		return out[i].Path > out[j].Path
	})
	return out
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
// files or its README. A lane's state files change on almost every run and are not notes;
// reading one as a note would report a parse failure to every reader on the table. A
// README.md ends in .md and is not a note either, for the same reason and with a worse
// symptom: it parsed as a broken note and was reported to every reader forever.
func isNotePath(path string) bool {
	if !strings.HasSuffix(path, ".md") {
		return false
	}
	if strings.Count(path, "/") != 1 || !strings.HasPrefix(path, "from-") {
		return false
	}
	return !isLaneDoc(filepath.Base(path))
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
		// steps over it, so the two modes cannot say different things about one file. So is
		// the lane's README, which is not a note and is not a stray.
		if isLaneStateTemp(base) || isLaneDoc(base) {
			continue
		}
		if !isNotePath(path) {
			add(path, "a lane holds notes (*.md), its %s, and nothing else", strings.Join(laneAllowedFiles, ", "))
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
