package bus

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// The three state files a lane keeps beside its notes, and why a reader's own lane is
// where they live.
//
// THE PROBLEM. inbox and check walked every lane on every run, so the work of reading a
// bus grew with the bus's whole history: the ten-thousandth note cost ten thousand
// parses to find, and the cost of asking "what is new" rose forever while the answer
// stayed one note. That is the property this file fixes. What a reader needs in order to
// answer "what is new for me" is a place to stand -- the commit they last read to -- and a
// memory of what they were shown and have not answered. Both are per reader, both must
// survive a bench change, and both must be visible to everybody on the bus for the same
// reason a receipt is: a state kept privately on one machine is a state nobody can check.
// So they are files in the reader's OWN lane, committed and pushed exactly the way a
// receipt is, under the same identity and the same fetch-rebase-retry.
//
//	CURSOR  one line: the commit this reader last read to, and when they read it.
//	OPEN    one line per note this reader has been shown and has not yet answered,
//	        carrying the line that note prints as -- see OpenEntry.
//	INDEX   one line per note this lane has SENT: id, path, date, To, Re.
//
// CURSOR and OPEN are a reader's own bookkeeping. INDEX is a lane's catalogue, and it is
// what makes a Re-by-id resolution and an id-uniqueness test a lookup over a few small
// files rather than a walk over every note on the bus.
//
// None of the three is authoritative about anything. A note is on the bus because the
// note is on the bus; these files say only what one reader has already seen and what one
// lane has already written, and every one of them can be rebuilt from the notes
// (`check --full --rebuild-index`) or removed.
//
// REMOVING THEM IS NOT SYMMETRIC, and the first version of this comment said it was.
// Deleting CURSOR costs one full read and nothing else. Deleting INDEX costs a rebuild.
// Deleting OPEN ALONE loses something: the cursor stays valid, so the next run is a cheap
// one over a change set that no longer holds the notes this reader was carrying, and they
// are dropped with `open=0` printed as though nothing were owed. That is why the cursor
// carries the count it was written with (see Cursor.Open) and why an OPEN file that has
// gone out from under a counted cursor is a refusal naming `--full --advance`, rather than
// a silence.
const (
	// CursorName is the reader's place to stand: the commit they last read to.
	CursorName = "CURSOR"
	// OpenName is the reader's memory of what they were shown and have not answered.
	OpenName = "OPEN"
	// IndexName is the lane's catalogue of the notes it has sent.
	IndexName = "INDEX"
)

// laneStateFiles is every state file a lane may hold. A lane holds notes, these, its
// README, and nothing else; anything else is a stray and check says so.
var laneStateFiles = []string{ReceiptsName, CursorName, OpenName, IndexName}

// LaneDocName is the one file in a lane that is neither a note nor a state file and is
// still allowed there.
//
// A README.md IS NOT A NOTE, and until this was written it was read as one: it ends in
// `.md`, it sits in a lane, so the lane walk parsed it, it had no `From:` line, and every
// reader on the bus was told `INBOX UNREADABLE` about it forever while `check` failed the
// whole bus over it. On a real bus it was the single file that failed at every
// date -- the one finding the legacy tolerance could not forgive, because a README genuinely
// cannot say when it was written and genuinely is not a note.
//
// So a lane may hold exactly one non-note, non-state file: `README.md`, the file a person
// opening the lane in a browser reads first. It is stepped over by the lane walk, by the
// incremental check and by the change-set read alike. The list is ONE name and is not a
// general licence: a `NOTES.md` or a `readme.md` in a lane is a stray like any other,
// because a tolerance whose width is "whatever looks like documentation" is not a rule.
const LaneDocName = "README.md"

// laneAllowedFiles is what check's refusal names when it finds something else: the state
// files and the README, in the order a person meets them.
var laneAllowedFiles = append(append([]string{}, laneStateFiles...), LaneDocName)

// isLaneDoc reports whether a name is the one allowed non-note document in a lane.
func isLaneDoc(name string) bool { return name == LaneDocName }

// TempSuffix is what replaceLaneFile writes through before it renames. A run killed
// between the write and the rename leaves one behind, so the name is reserved rather than
// random: the lane walk steps over `CURSOR.tmp` instead of reporting it as a stray file,
// and a person who finds one can delete it knowing what it was.
const TempSuffix = ".tmp"

func isLaneStateFile(name string) bool {
	for _, s := range laneStateFiles {
		if name == s {
			return true
		}
	}
	return false
}

// isLaneStateTemp reports whether a name is a lane state file's stranded temporary. Only
// those four names: a `notes.tmp` somebody left in a lane is a stray like any other, and
// this is not a general licence to leave files in a lane.
func isLaneStateTemp(name string) bool {
	base, cut := strings.CutSuffix(name, TempSuffix)
	return cut && isLaneStateFile(base)
}

// Cursor is one reader's place to stand.
type Cursor struct {
	// Commit is the commit the reader last read to. Empty when the lane has no CURSOR,
	// which is the first run and is not an error: a reader with no cursor reads the whole
	// bus once and has one from then on.
	Commit string
	// Stamp is when they read it, for a person reading the file. Nothing computes from it.
	Stamp string
	// Open is how many notes the run that wrote this cursor was carrying, and Counted says
	// whether the file carried the field at all.
	//
	// It is the ONE thing in this file that another file's absence is checked against, and
	// it is here because deleting OPEN alone is otherwise silent. An empty OPEN list is
	// REMOVED rather than left zero-length -- a reader with nothing open has no OPEN file --
	// so "absent" and "nothing open" are the same state on disk and a reader who deleted
	// OPEN while their cursor stayed valid would simply be told open=0 with the notes they
	// owed gone. With the count in the cursor the two states are different: a cursor that
	// says it was carrying two notes, beside no OPEN file, is a refusal naming
	// `--full --advance`. A cursor written before this field has Counted false and is
	// trusted, because there is nothing to compare.
	Open    int
	Counted bool

	// Legacy is the switch-day line this reader read under, stored EXACTLY as it was given
	// -- a UTC date, or an RFC 3339 UTC instant: notes dated before it are not carried on
	// the open list. It is "" on a cursor written with no line, which is every cursor
	// written before this field existed.
	//
	// It is IN THE CURSOR rather than in a flag the reader must remember because a line
	// that has to be retyped on every run is a line that will be forgotten on one, and the
	// run that forgets it opens six hundred notes the reader had settled. The cursor is
	// already the reader's place to stand; the date they are standing after belongs beside
	// it, on the bus, where everybody can see which notes they have taken as read.
	Legacy string
}

// CursorPath, OpenPath and IndexPath are the repo-relative paths of a lane's state files.
func CursorPath(lane string) string { return lane + "/" + CursorName }
func OpenPath(lane string) string   { return lane + "/" + OpenName }
func IndexPath(lane string) string  { return lane + "/" + IndexName }

// ReadCursor reads a lane's CURSOR. A lane with no CURSOR file returns the zero Cursor and
// no error: that is a reader who has not read yet, not a bus that is broken.
//
// The commit is required to be plain hex HERE, at the read, rather than trusted because
// this tool wrote it. The value goes on to become a git argument, and a CURSOR file is an
// ordinary file on a shared bus that anybody with push access can edit; a cursor of
// `--upload-pack=...` is not a commit, it is an option to git, and this is the place that
// is cheapest to say so.
func ReadCursor(root, lane string) (Cursor, error) {
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(CursorPath(lane))))
	if errors.Is(err, os.ErrNotExist) {
		return Cursor{}, nil
	}
	if err != nil {
		return Cursor{}, err
	}
	var got Cursor
	for i, r := range records(string(raw)) {
		if i > 0 {
			return Cursor{}, fmt.Errorf("%s: line %d: a cursor is one line", CursorPath(lane), r.line)
		}
		// Fields and not Cut, because the line is now up to four tokens and none of them
		// holds a space: the commit, the RFC 3339 stamp, `open=<n>` and `legacy=<date>`. A
		// fifth token is a refusal rather than a guess, on the same rule as an INDEX line's
		// field count.
		//
		// The two trailing tokens are read BY THEIR PREFIX and not by their position, which
		// is what makes a new one addable without every older cursor becoming unreadable:
		// a cursor written before `open=` existed has two tokens and is trusted, and one
		// written before `legacy=` has three. An unknown token is still a refusal -- the
		// tolerance is for tokens this reader knows and the writer did not, never the other
		// way round.
		fields := strings.Fields(r.text)
		if len(fields) > cursorFields {
			return Cursor{}, fmt.Errorf("%s: line %d: a cursor is <commit> [<stamp>] [%s<n>] [%s<date>], got %d tokens", CursorPath(lane), r.line, cursorOpenPrefix, cursorLegacyPrefix, len(fields))
		}
		if err := ValidCommitHex(fields[0]); err != nil {
			return Cursor{}, fmt.Errorf("%s: line %d: %w", CursorPath(lane), r.line, err)
		}
		got = Cursor{Commit: fields[0]}
		if len(fields) > 1 {
			got.Stamp = fields[1]
		}
		for _, tok := range fields[2:] {
			switch {
			case strings.HasPrefix(tok, cursorOpenPrefix):
				n, perr := strconv.Atoi(strings.TrimPrefix(tok, cursorOpenPrefix))
				if perr != nil || n < 0 {
					return Cursor{}, fmt.Errorf("%s: line %d: %q is not %s<n>", CursorPath(lane), r.line, truncate(tok, 40), cursorOpenPrefix)
				}
				got.Open, got.Counted = n, true
			case strings.HasPrefix(tok, cursorLegacyPrefix):
				line := strings.TrimPrefix(tok, cursorLegacyPrefix)
				if _, perr := ParseLegacyBefore(line); perr != nil {
					return Cursor{}, fmt.Errorf("%s: line %d: %q is not %s<date-or-instant>: %w", CursorPath(lane), r.line, truncate(tok, 40), cursorLegacyPrefix, perr)
				}
				got.Legacy = line
			default:
				return Cursor{}, fmt.Errorf("%s: line %d: %q is neither %s<n> nor %s<date-or-instant>", CursorPath(lane), r.line, truncate(tok, 40), cursorOpenPrefix, cursorLegacyPrefix)
			}
		}
	}
	return got, nil
}

// LegacyBefore is the cursor's switch-day line as a moment, or the zero time when it has
// none. It parses what ReadCursor already validated, so a caller that has a Cursor never
// has to know the layout.
func (c Cursor) LegacyBefore() time.Time {
	if c.Legacy == "" {
		return time.Time{}
	}
	when, err := ParseLegacyBefore(c.Legacy)
	if err != nil {
		return time.Time{}
	}
	return when.UTC()
}

// LegacyLine is the cursor's switch-day line as a run reads under it: the moment, and the
// text the run that drew it gave. The TEXT is carried and not re-rendered so that a line
// drawn as a date stays a date and one drawn as an instant stays an instant, in the cursor
// and on the `INBOX LEGACY` line -- a reader checking which notes they took as read is
// checking against what they typed.
func (c Cursor) LegacyLine() LegacyLine {
	when := c.LegacyBefore()
	if when.IsZero() {
		return LegacyLine{}
	}
	return LegacyLine{Before: when, Text: c.Legacy}
}

// cursorFields and the two token prefixes are the shape of a cursor line: the commit, the
// stamp, how many notes the run that wrote it was carrying, and the switch-day line it
// read under.
const (
	cursorFields       = 4
	cursorOpenPrefix   = "open="
	cursorLegacyPrefix = "legacy="
)

// LegacyDateLayout and LegacyInstantLayout are the two shapes a legacy line takes, in a
// cursor and in the two flags that write one: a UTC calendar date, which means midnight at
// its start, or an RFC 3339 UTC instant.
//
// WHY THE INSTANT EXISTS, measured on the hour a family of five switched to this tool. They
// drew the line at TOMORROW's date, reasonably: nothing written before tomorrow was written
// under the tool, so the open list would start at zero. It did — and it stayed at zero. A
// date is midnight at its START, so every note any of them sent that same afternoon was
// dated before tomorrow's midnight and was therefore legacy: five lines writing to each
// other all day, and not one note on anybody's open list, not even under `--full`. A date
// can only ever name a boundary between days, and the boundary they needed was a MOMENT --
// the moment they switched, which they knew to the second and could not say.
//
// So the line takes either, and the comparison is by instant in both cases: a date is the
// instant of its own midnight, which is exactly what it always meant, and an instant is
// itself. The date form is kept because the thing usually being drawn IS a day a bus adopted
// a tool, nobody knows that to the second, and every cursor already on a bus holds one.
//
// The instant is required to end in `Z` and not an offset. A switch-day line is compared
// against note dates that are all UTC, and a line written `+10:00` would be read correctly
// and reviewed wrongly by the person who has to check it against a filename.
const (
	LegacyDateLayout    = "2006-01-02"
	LegacyInstantLayout = "2006-01-02T15:04:05Z"
)

// ParseLegacyBefore reads a switch-day line, in either shape, as the moment it names. It is
// the ONE place either shape is read -- the two flags, a cursor at the read, and a cursor at
// the write all come here -- so no two of them can accept different spellings of a line, and
// the error names both shapes because a caller who got one wrong wants to be told the other.
func ParseLegacyBefore(value string) (time.Time, error) {
	if when, err := time.Parse(LegacyInstantLayout, value); err == nil {
		return when.UTC(), nil
	}
	if when, err := time.Parse(LegacyDateLayout, value); err == nil {
		return when.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("%s is neither a UTC date of the form %s nor a UTC instant of the form %s",
		strconv.Quote(truncate(value, 40)), LegacyDateLayout, LegacyInstantLayout)
}

// LegacyDateAtOrAfterToday is the shape of switch-day line that quietly empties an inbox,
// and it is the reason this tool now says so out loud instead of listing nothing.
//
// THE FAILURE, from a friend's first week. A line drew its switch at a DATE -- the shape
// v0.10.0's own first-advance guard handed out, tomorrow's -- and a date is midnight at its
// START, so the line stood in front of everything anybody had written that day. His cursor
// read `open=0 legacy=2026-09-10` and his inbox listed nothing, run after run, on a bus
// that was busy. Nothing was broken and nothing was lost: every note was still on the bus,
// behind a line he had drawn himself and had no way to see. What was missing was a
// SENTENCE. The tool knew the line was a date, knew the date was still to come, and knew
// the one command that would fix it, and it said none of the three.
//
// So this is the test the sentence is printed under, and it is a test about the LINE and
// never about what a run found: an empty inbox is not evidence of anything, and a reader
// whose line is drawn forward is owed the sentence on the busy runs too.
//
// It returns the day the line is drawn on, the last day that line hides -- the day BEFORE
// it, because a date is midnight at its start -- and true, for a line that is BOTH:
//
//   - a bare DATE, and not an instant. An instant was drawn to the second by somebody who
//     meant a moment; whatever it hides, they said where it stood.
//   - standing at TODAY or later in UTC. A date at today took the whole of yesterday, a
//     date at tomorrow takes the whole of today, and each was drawn at a day boundary for a
//     switch that happens at a moment. A date already behind today is history properly
//     drawn -- the day a bus adopted a tool, months ago, which nobody knows to the second --
//     and there is nothing to say about it.
//
// Everything else is false, the empty line included.
func LegacyDateAtOrAfterToday(text string, now time.Time) (drawn, hides time.Time, yes bool) {
	if text == "" {
		return time.Time{}, time.Time{}, false
	}
	// The INSTANT is read first, exactly as ParseLegacyBefore reads it, so a line that is
	// an instant can never be mistaken for a date by a looser parse underneath it.
	if _, err := time.Parse(LegacyInstantLayout, text); err == nil {
		return time.Time{}, time.Time{}, false
	}
	day, err := time.Parse(LegacyDateLayout, text)
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	day = day.UTC()
	u := now.UTC()
	today := time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
	if day.Before(today) {
		return time.Time{}, time.Time{}, false
	}
	return day, day.AddDate(0, 0, -1), true
}

// OpenPresent reports whether a lane has an OPEN file at all, which is a different
// question from whether it holds anything. See Cursor.Open.
func OpenPresent(root, lane string) bool {
	return laneFilePresent(root, OpenPath(lane))
}

// CursorPresent reports whether a lane has a CURSOR file at all, which is a different
// question from whether it READS: a cursor this tool cannot parse is present.
//
// The distinction is load-bearing for exactly one caller, the first-advance guard in
// cmd/nova-bus. That guard fires on a reader who has never advanced, and "never advanced"
// has to mean "no CURSOR file", not "no cursor I could read" -- because
// `--full --advance` is the documented repair for a cursor that will not read, and a
// repair that a guard refuses to run is not one.
func CursorPresent(root, lane string) bool {
	return laneFilePresent(root, CursorPath(lane))
}

func laneFilePresent(root, path string) bool {
	st, err := os.Stat(filepath.Join(root, filepath.FromSlash(path)))
	return err == nil && !st.IsDir()
}

// WriteCursor replaces a lane's CURSOR. It is a REPLACE and not an append: a cursor is one
// value that moves, unlike RECEIPTS, which is a log. That is also why two benches of one
// line can conflict on it -- and why the settlement in conflict.go cannot union this file
// the way it unions RECEIPTS. Two cursors are two claims about how far one reader has read,
// so the FURTHER read wins: the one whose commit is a descendant of the other's, and failing
// that the later stamp. The OPEN list beside it is taken from the same side, because an open
// list is the list that belongs to a cursor.
//
// The sha is written first and the stamp second, the other way round from a receipt line,
// because a cursor's subject is the commit and the time is annotation for a person,
// whereas a receipt is a log entry whose subject is when it was made. The third field is
// how many notes this run was carrying; see Cursor.Open for why a count of another file's
// contents lives here.
// The fourth field is the switch-day line this reader read under, written only when there
// is one: a cursor with no legacy line is three tokens, which is exactly what every cursor
// written before the line existed already is.
func WriteCursor(root, lane, commit string, open int, legacy string, now time.Time) error {
	if err := ValidCommitHex(commit); err != nil {
		return err
	}
	if open < 0 {
		return fmt.Errorf("a cursor cannot be carrying %d notes", open)
	}
	line := commit + " " + now.UTC().Format(ReceiptStampLayout) + " " + cursorOpenPrefix + strconv.Itoa(open)
	if legacy != "" {
		// Written EXACTLY as it was given, once it parses: a line drawn as an instant is
		// recorded as that instant and not rounded back to its day, which is the whole of
		// what makes a same-day switch survive into the next run.
		if _, err := ParseLegacyBefore(legacy); err != nil {
			return fmt.Errorf("a cursor's %s is a UTC date or instant: %w", cursorLegacyPrefix, err)
		}
		line += " " + cursorLegacyPrefix + legacy
	}
	return replaceLaneFile(root, CursorPath(lane), line+"\n")
}

// OpenKind is what one open entry IS, decided once when the note went open and recorded,
// so that a later run can print the entry without opening the note again.
type OpenKind string

const (
	// OpenNote is a note that carries something: a question, a finding, a request.
	OpenNote OpenKind = "note"
	// OpenReceipt is a bare acknowledgement, by the receipt heuristic or by a Kind line.
	OpenReceipt OpenKind = "receipt"
	// OpenUnreadable is a file on the bus this tool could not parse. It is CARRIED
	// rather than dropped: see the comment on OpenEntry.
	OpenUnreadable OpenKind = "unreadable"
)

func validOpenKind(k OpenKind) bool {
	return k == OpenNote || k == OpenReceipt || k == OpenUnreadable
}

// OpenEntry is one note a reader has been shown and has not yet answered -- and, since
// OPEN v2, the whole of the line that note prints as.
//
// WHY THE ENTRY CARRIES THE DISPLAY AND NOT JUST THE PATH. Dana's requirement, verbatim:
// "O(new + open) is not great. Can we make it O(new)." Under v1 an entry was `<id> <path>`,
// so every run re-opened and re-parsed every open note -- to print its sender, its date and
// its subject, and to decide whether it was a receipt. A reader carrying five hundred notes
// paid five hundred parses to be told nothing new. The fix is to write the display line ONCE,
// when the note goes open, and to print later runs' open list from this file: a read is then
// the size of what is NEW in parses, plus the bytes of one small file.
//
// What that costs, named rather than discovered: these fields are a SNAPSHOT taken when the
// note went open. A note whose subject is edited afterwards keeps the subject it had when it
// arrived, until an edit puts it back in the change set (which re-parses it) or a `--full`
// read rewrites the list. That is the same direction every other cache here fails in: a
// reader sees what they were shown, never less than they are owed.
type OpenEntry struct {
	// ID is the note's id, or "" for a legacy note, which is addressed by path.
	ID string
	// Kind is what the note is, decided when it went open.
	Kind OpenKind
	// Heard is set when this reader's RECEIPTS records the note. Heard is not answered: a
	// heard note stays on the list and leaves the open count, and the flag lives HERE so
	// that RECEIPTS does not have to be read whole on a run that changed nothing.
	Heard bool
	// From is the resolved sender's name, Addr is "to" or "cc", Date is the note's moment
	// as RFC 3339 in UTC (or "" when it has none), and Subject is its subject. All four are
	// for the listing and nothing computes from them but the switch-day line, which reads
	// Date. They are "" on an unreadable entry, which has no header to read them from.
	From    string
	Addr    string
	Date    string
	Subject string
	// Path is repo-relative and is always present. It is the only field an unreadable entry
	// has, and it is what every entry is ultimately identified by.
	Path string
}

// Target is how this note is named on a Re line or in a RECEIPTS file: its id when it has
// one, its path when it does not.
func (e OpenEntry) Target() string {
	if e.ID != "" {
		return e.ID
	}
	return e.Path
}

// OpenHeader is the first line of an OPEN file, and the whole of the version handshake.
//
// It is here because the format CHANGED under readers that already have one: a v1 entry is
// `<id or -> <path>`, a v2 entry is eight tab-separated fields, and a v1 line read as v2
// would be one field -- a path with no id, no kind and no date, which a run would then print
// as a note nobody sent and carry forever. The cursor could not catch it either: a v1 OPEN
// beside a counted cursor is exactly the state a healthy v2 reader is in. So the file says
// its own version on its first line, a file without it is refused at the read, and the
// refusal names `--full --advance`, which writes the list again from the whole bus. That
// is the same repair, and the same words, as an OPEN that went missing.
const OpenHeader = "OPEN v2"

// openFields is how many tab-separated fields an open entry has:
//
//	<id|->  <kind>  <heard|->  <from|->  <addr|->  <date|->  <path>  <subject|->
//
// Tabs and one-line escaping, the same as INDEX and for the same reason: a subject holding
// a tab or a newline cannot make one record look like two. An absent value is "-" and never
// empty, so no line ends in an invisible tab.
const openFields = 8

// openHeardToken is what the heard flag looks like when it is set. A word rather than a 1,
// because the file is read by people as often as by this tool.
const openHeardToken = "heard"

// ValidOpenID checks an id appearing in an OPEN entry.
//
// Modern notes carry <sender>-<12 hex>, generated by AssignID. Historical notes written
// under earlier tools or conventions carry legacy IDs (such as "freddy-pr827-001" or
// "bo-legacy-001"), which are accepted here as valid slugs so that carry-history does
// not publish an OPEN state that subsequent incremental reads refuse.
func ValidOpenID(id string) error {
	if err := ValidID(id); err == nil {
		return nil
	}
	if err := ValidSlug(id); err == nil {
		return nil
	}
	return ValidID(id)
}

// ReadOpen reads a lane's OPEN list. A lane with no OPEN file has nothing open.
func ReadOpen(root, lane string) ([]OpenEntry, error) {
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(OpenPath(lane))))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rows := records(string(raw))
	if len(rows) == 0 || rows[0].text != OpenHeader {
		return nil, fmt.Errorf("%s: this open list does not begin with %q, so it was written by a version of this tool whose entries were %s and cannot be read as the display lines this one prints from; read once with --full --advance, which writes the list again from the whole bus",
			OpenPath(lane), OpenHeader, "<id or -> <path>")
	}
	var out []OpenEntry
	for _, r := range rows[1:] {
		fields := strings.Split(r.text, "\t")
		if len(fields) != openFields {
			return nil, fmt.Errorf("%s: line %d: an open entry is %d tab-separated fields (id, kind, heard, from, addr, date, path, subject), got %d", OpenPath(lane), r.line, openFields, len(fields))
		}
		e := OpenEntry{
			ID:      undash(fields[0]),
			Kind:    OpenKind(fields[1]),
			Heard:   fields[2] == openHeardToken,
			From:    undash(fields[3]),
			Addr:    undash(fields[4]),
			Date:    undash(fields[5]),
			Path:    undash(fields[6]),
			Subject: undash(fields[7]),
		}
		if e.Path == "" {
			return nil, fmt.Errorf("%s: line %d: an open entry names no path", OpenPath(lane), r.line)
		}
		if !validOpenKind(e.Kind) {
			return nil, fmt.Errorf("%s: line %d: %q is not one of %s, %s, %s", OpenPath(lane), r.line, truncate(fields[1], 40), OpenNote, OpenReceipt, OpenUnreadable)
		}
		if fields[2] != openHeardToken && fields[2] != "-" {
			return nil, fmt.Errorf("%s: line %d: the heard flag is %q or -, got %q", OpenPath(lane), r.line, openHeardToken, truncate(fields[2], 40))
		}
		if e.ID != "" {
			if err := ValidOpenID(e.ID); err != nil {
				return nil, fmt.Errorf("%s: line %d: %w", OpenPath(lane), r.line, err)
			}
		}
		out = append(out, e)
	}
	return out, nil
}

// OpenLine renders one open entry.
func OpenLine(e OpenEntry) string {
	heard := "-"
	if e.Heard {
		heard = openHeardToken
	}
	return strings.Join([]string{
		oneline.Escape(orDash(e.ID)),
		oneline.Escape(orDash(string(e.Kind))),
		heard,
		oneline.Escape(orDash(e.From)),
		oneline.Escape(orDash(e.Addr)),
		oneline.Escape(orDash(e.Date)),
		oneline.Escape(orDash(e.Path)),
		oneline.Escape(orDash(e.Subject)),
	}, "\t")
}

// WriteOpen replaces a lane's OPEN list, in the order given. Callers keep the survivors in
// the order they were already in and append what is new, so the file's diff between two
// runs is the notes that actually opened and closed.
//
// Nothing open still REMOVES the file rather than leaving the header alone in it: absent and
// nothing-open are one state on disk, which is the invariant the cursor's `open=` count is
// checked against.
func WriteOpen(root, lane string, entries []OpenEntry) error {
	if len(entries) == 0 {
		return replaceLaneFile(root, OpenPath(lane), "")
	}
	var b strings.Builder
	b.WriteString(OpenHeader + "\n")
	for _, e := range entries {
		b.WriteString(OpenLine(e) + "\n")
	}
	return replaceLaneFile(root, OpenPath(lane), b.String())
}

// IndexEntry is one line of a lane's INDEX: everything about a note that a reader needs in
// order to resolve a thread or refuse a duplicate id WITHOUT opening the note.
type IndexEntry struct {
	ID   string
	Path string
	Date string   // RFC 3339 in UTC, or "" when the note's Date line will not parse
	To   []string // the RESOLVED recipients, the same names the id was hashed over
	Re   []string // the note's Re targets, verbatim
	Lane string
	Line int
}

// indexFields is how many tab-separated fields an INDEX line has. A line with a different
// number is malformed and check says so rather than guessing at which field is missing.
const indexFields = 5

// IndexLine renders one INDEX record.
//
// The fields are separated by TABS and each is rendered through the repo's own one-line
// escape, so a roster name or a subject holding a tab or a newline cannot make one record
// look like two. It is the same guarantee the printed lines have, applied to a file,
// because this file is read back by a program.
//
// An absent value is written "-" rather than left empty, which is the same convention the
// printed grammar uses and is here for a mechanical reason as well as a readable one: a
// record ending in an empty field ends in a TAB, and a trailing tab is invisible, is
// stripped by half the editors a person might open this file in, and would turn a
// five-field line into a four-field one that this reader then refuses.
//
// THE ONE EDGE THIS SHAPE DOES NOT CLOSE, named because a silent one is worse than a known
// one: the To and Re lists are joined with ";" and split back on ";", so a LEGACY note
// whose repo-relative PATH holds a semicolon -- the only value in either list that is not a
// roster name or an id, and one written before this tool existed and therefore under no
// rule of its own -- round-trips through the catalogue as two targets rather than one. The
// consequence is bounded: the thread resolves by path from the filesystem instead (see
// Index.resolves), and a check reports the halves as dangling rather than resolving them
// wrongly. Fixing it means escaping the separator as well as the field, which is a format
// change to every INDEX on every bus; it is not worth that for a filename nobody has
// written yet, and the day one is written this comment is where to start.
func IndexLine(e IndexEntry) string {
	return strings.Join([]string{
		oneline.Escape(orDash(e.ID)),
		oneline.Escape(orDash(e.Path)),
		oneline.Escape(orDash(e.Date)),
		oneline.Escape(orDash(strings.Join(e.To, ";"))),
		oneline.Escape(orDash(strings.Join(e.Re, ";"))),
	}, "\t")
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// IndexEntryFor builds the INDEX record for a note that has an id.
func IndexEntryFor(c *Config, n Note) IndexEntry {
	to, cc := n.Header.Recipients(c)
	date := ""
	if when := n.When(); !when.IsZero() {
		date = when.Format(ReceiptStampLayout)
	}
	return IndexEntry{
		ID:   n.Header.ID,
		Path: n.Path,
		Date: date,
		To:   append(to, cc...),
		Re:   n.Header.Re,
		Lane: n.Lane,
	}
}

// ReadLaneIndex reads one lane's INDEX. A lane with no INDEX has none, which is a bus
// that predates this file and is exactly what `check --full --rebuild-index` is for.
func ReadLaneIndex(root, lane string) ([]IndexEntry, error) {
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(IndexPath(lane))))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []IndexEntry
	for _, r := range records(string(raw)) {
		fields := strings.Split(r.text, "\t")
		if len(fields) != indexFields {
			return nil, fmt.Errorf("%s: line %d: an index line is %d tab-separated fields (id, path, date, to, re), got %d", IndexPath(lane), r.line, indexFields, len(fields))
		}
		out = append(out, IndexEntry{
			ID:   fields[0],
			Path: fields[1],
			Date: undash(fields[2]),
			To:   splitList(fields[3]),
			Re:   splitList(fields[4]),
			Lane: lane,
			Line: r.line,
		})
	}
	return out, nil
}

func splitList(s string) []string {
	if s == "" || s == "-" {
		return nil
	}
	return strings.Split(s, ";")
}

// Index is every lane's INDEX, read together: the bus's catalogue.
//
// It is what a resolution over ids costs instead of a walk. Reading it is one small file
// per lane and one line per note SENT BY THIS TOOL -- no note is parsed, no body is read,
// and a lane with no INDEX simply contributes nothing rather than failing.
type Index struct {
	Entries []IndexEntry
	root    string
	byID    map[string]*IndexEntry
	byPath  map[string]*IndexEntry
}

// ReadIndex reads the INDEX of every lane the roster declares.
func ReadIndex(root string, c *Config) (*Index, error) {
	idx := &Index{root: root, byID: map[string]*IndexEntry{}, byPath: map[string]*IndexEntry{}}
	for _, lane := range c.Lanes() {
		entries, err := ReadLaneIndex(root, lane)
		if err != nil {
			return nil, err
		}
		idx.Entries = append(idx.Entries, entries...)
	}
	for i := range idx.Entries {
		e := &idx.Entries[i]
		if _, dup := idx.byID[e.ID]; !dup {
			idx.byID[e.ID] = e
		}
		idx.byPath[e.Path] = e
	}
	return idx, nil
}

// ByID and ByPath are the two lookups a Re line or a receipt needs.
func (i *Index) ByID(id string) (*IndexEntry, bool)     { e, ok := i.byID[id]; return e, ok }
func (i *Index) ByPath(path string) (*IndexEntry, bool) { e, ok := i.byPath[path]; return e, ok }

// AppendIndexLine adds one record to a lane's INDEX. Append-only, like RECEIPTS: a sender
// only ever adds to their own lane, so two DIFFERENT senders writing at once touch
// different files and cannot conflict.
//
// TWO BENCHES OF ONE LANE DO CONFLICT HERE, and both of them land anyway. Append-only is
// not conflict-free: both benches add a line at the end of the same file over the same base,
// git's three-way merge sees an edit/edit at that spot, and the rebase in CommitAndPush
// stops. Before this file existed, two sessions of one line sending different notes rebased
// CLEAN -- the note paths differed and nothing else was touched -- so the catalogue widened
// the conflict surface.
//
// It used to be documented and left there, and the scenario run showed what that cost: the
// tool aborted cleanly and said so, and the bench was then WEDGED, because the person's own
// `git pull --rebase` landed in a half-done rebase with `UU from-<lane>/INDEX` and nothing
// on the bus saying what to do next. So the conflict is settled instead, by union, in two
// places -- the `merge=union` attribute this tool writes at the bus root (attributes.go)
// and the tool's own resolution during its own rebase (conflict.go), because the attribute
// only reaches a checkout once it has been pulled. Both sides' lines land, identical lines
// once, and neither bench is left with anything to rescue.
//
// The shapes that would remove the conflict rather than settle it -- one INDEX file per
// bench, or one file per note named by its id -- both trade a conflict that is now automatic
// for a lane directory whose file count grows with its notes, which is the cost the
// catalogue exists to avoid, and neither can be adopted without changing the layout of every
// bus already running this tool.
func AppendIndexLine(root string, e IndexEntry) error {
	full := filepath.Join(root, filepath.FromSlash(IndexPath(e.Lane)))
	if err := insideRoot(root, full); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(full, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.WriteString(IndexLine(e) + "\n"); err != nil {
		return err
	}
	return f.Close()
}

// RebuildLaneIndex writes a lane's INDEX from the notes on disk, replacing whatever was
// there. It is a REPAIR and is only ever reached through `check --full --rebuild-index`,
// which is a thing a person asks for and watches: it rewrites a file rather than adding to
// one, and a rewrite of shared state is not something a report should do on its own.
//
// A note with no id contributes no line. A legacy note is addressed by path, everywhere,
// and putting it in a catalogue keyed on ids would be inventing an id for it.
func RebuildLaneIndex(root string, c *Config, t *Bus, lane string) (int, error) {
	var entries []IndexEntry
	for i := range t.Notes {
		n := &t.Notes[i]
		if n.Lane != lane || n.Parse != nil || n.Header.ID == "" {
			continue
		}
		entries = append(entries, IndexEntryFor(c, *n))
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	var b strings.Builder
	for _, e := range entries {
		b.WriteString(IndexLine(e) + "\n")
	}
	return len(entries), replaceLaneFile(root, IndexPath(lane), b.String())
}

// replaceLaneFile writes a lane state file whole, refusing to write outside the bus for
// the same reason Save does: every path reaching here was built from a validated lane, so
// the assertion should be unreachable, which is exactly why it is made.
//
// An empty body REMOVES the file rather than leaving a zero-length one, so a reader with
// nothing open has no OPEN file and a lane with no notes has no INDEX -- the same state a
// lane starts in, rather than a second spelling of it that every reader would have to
// know about.
//
// IT WRITES THROUGH A TEMPORARY AND RENAMES, and an earlier version wrote the file in
// place. Every file that reaches here is a file another run refuses on: a CURSOR whose
// commit will not read is `INBOX REFUSED`, an OPEN that is half a line is a reader whose
// next run stops, an INDEX cut in two is a catalogue that resolves a thread to nothing. A
// write in place makes all three reachable by killing the tool in the microsecond between
// truncate and write -- a laptop lid, a CI timeout, a ctrl-C -- and the file it leaves is
// not the old one and not the new one. A rename is atomic on every filesystem this runs
// on, so a kill leaves the OLD file, entire, which is a state every reader here already
// handles. The temporary is in the SAME DIRECTORY, because a rename across filesystems is
// not a rename, and it is named `<file>.tmp` rather than randomly so that a stranded one
// is a single predictable name a person can see and the lane walk can step over.
func replaceLaneFile(root, path, content string) error {
	full := filepath.Join(root, filepath.FromSlash(path))
	if err := insideRoot(root, full); err != nil {
		return err
	}
	if content == "" {
		if err := os.Remove(full); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	tmp := full + TempSuffix
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, full); err != nil {
		// The rename is the commit point. If it fails there is no half-written file to
		// leave behind, so the temporary goes rather than staying as a stray.
		os.Remove(tmp)
		return err
	}
	return nil
}

// record is one meaningful line of a lane state file, with the 1-based line number it was
// read at, so a refusal names the line a person opens to.
type record struct {
	line int
	text string
}

// records splits a lane state file into its meaningful lines IN ORDER, skipping blanks and
// `#` comments the way RECEIPTS does. In order, and therefore a slice and not a map,
// because OPEN's order is the order a reader's open notes arrived in and a map would
// reshuffle it on every run.
func records(raw string) []record {
	var out []record
	for i, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, record{line: i + 1, text: line})
	}
	return out
}

func undash(s string) string {
	if s == "-" {
		return ""
	}
	return s
}
