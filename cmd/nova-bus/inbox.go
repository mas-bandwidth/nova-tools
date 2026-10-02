// inbox: the notes addressed to a reader that nothing of theirs answers, read from the
// reader's cursor, and the cursor's advance.

package main

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

func cmdInbox(args []string, stdout, stderr io.Writer, now time.Time) int {
	f := newFlags("inbox")
	busDir := f.fs.String("bus", "", "the bus's repository root (required)")
	as := f.fs.String("as", "", "which participant you are (required)")
	maxWords := f.fs.Int("receipt-max-words", 0, "a body under this many words may be a receipt (required, at least 1)")
	full := f.fs.Bool("full", false, "walk the whole bus instead of what changed since your cursor")
	openList := f.fs.Bool("open", false, "list every open note, not only what is new; the default prints one INBOX OPEN line for them")
	openMax := f.fs.Int("open-max", defaultOpenMax, "with --open, how many carried entries to print before saying how many more there are")
	openWarn := f.fs.Int("open-warn", defaultOpenWarn, "how many carried entries before every return adds one line saying the list is large and how to empty it")
	bodies := f.fs.Bool("bodies", false, "print bodies for NEW notes, bounded by --max-notes and --max-bytes")
	maxNotes := f.fs.Int("max-notes", defaultBodiesNotes, "with --bodies, maximum NEW items to print")
	maxBytes := f.fs.Int64("max-bytes", defaultBodiesBytes, "with --bodies, maximum body bytes to print")
	maxCommits := f.fs.Int("max-commits", defaultMaxCommits, "how many commits a since-walk may cross before it stops and names the remedy; raise it to read a staler cursor")
	after := f.fs.String("after", "", "continue a bounded --bodies snapshot")
	advance := f.fs.Bool("advance", false, "move your cursor to HEAD and push it, the way a receipt is pushed")
	dryRun := f.fs.Bool("dry-run", false, "with --advance, print the listing and the cursor the advance would write, and write nothing")
	remote := f.fs.String("remote", "", "the git remote to push the cursor to (required with --advance)")
	branch := f.fs.String("branch", "", "the branch the bus lives on (required with --advance)")
	attempts := f.fs.Int("attempts", defaultAttempts, "how many times to push the cursor before giving up")
	gitSeconds := f.fs.Int("git-timeout", defaultGitTimeoutSeconds, "how long one git subprocess may take before this run gives up on it")
	noPush := f.fs.Bool("no-push", false, "with --advance, commit the cursor but do not push it")
	legacyBefore := f.fs.String("legacy-before", "", "notes dated before this UTC date (YYYY-MM-DD, midnight at its start) or UTC instant (RFC 3339, e.g. 2026-09-09T18:07:00Z) are not carried on your open list, and are counted rather than listed")
	legacyNow := f.fs.Bool("legacy-now", false, "draw the switch-day line at THIS run's UTC instant: exactly --legacy-before <now>, so everything already on the bus is history and everything after this moment is news")
	carryHistory := f.fs.Bool("carry-history", false, "on your FIRST --advance, carry every old note on your open list instead of drawing a switch-day line; does nothing otherwise")
	diagnostics := f.fs.Bool("diagnostics", false, "name every unreadable file with its reason, even ones already shown; the default collapses unchanged ones to one count line")
	f.alsoRefuse = func() string {
		if _, ok := resolveReceiptMaxWords(*maxWords, f.set("receipt-max-words"), *busDir); !ok {
			return receiptMaxWordsRefusal(f.verb, *maxWords)
		}
		return ""
	}
	if !f.parse(args, stderr, map[string]*string{"bus": busDir, "as": as}) {
		return 2
	}
	// --legacy-now IS --legacy-before, with the one value nobody can type worked out here:
	// this run's own UTC instant, in the same RFC 3339 shape the flag takes and the cursor
	// records. It is sugar and nothing else -- it goes through the same parse, lands in the
	// same LegacyLine, and is written into the cursor as the instant it named, so a reader
	// who runs it and a reader who pasted `date -u +%Y-%m-%dT%H:%M:%SZ` end up with the
	// same line. It exists because the correct shape was a twenty-character timestamp a
	// person had to produce before the run that needed it, and the friend this was written
	// for did not produce it: he drew a date instead, and his inbox went quiet.
	legacyValue := *legacyBefore
	if *legacyNow {
		// Two flags naming the same line say nothing about where it stands, whichever way
		// round they disagree. This is exit 2, a bad invocation, like every other pair.
		if strings.TrimSpace(*legacyBefore) != "" {
			fmt.Fprint(stderr, "INBOX REFUSED: --legacy-now draws the switch-day line at this run's instant and --legacy-before draws it where you say; give one or the other; run: nova-bus inbox -h\n")
			return 2
		}
		legacyValue = now.UTC().Format(bus.LegacyInstantLayout)
	}
	flagLegacy, ok := legacyLine("inbox", legacyValue, stderr)
	if !ok {
		return 2
	}
	// The two answers to the first-advance guard are answers to the SAME question -- what
	// this reader does about the history that was on the bus before them -- and giving both
	// says nothing about which. A line is drawn or it is not.
	if *carryHistory && !flagLegacy.Before.IsZero() {
		if *legacyNow {
			fmt.Fprint(stderr, "INBOX REFUSED: --legacy-now draws a switch-day line and --carry-history says there is none to draw; give one or the other; run: nova-bus inbox -h\n")
			return 2
		}
		fmt.Fprint(stderr, "INBOX REFUSED: --legacy-before draws a switch-day line and --carry-history says there is none to draw; give one or the other; run: nova-bus inbox -h\n")
		return 2
	}
	maxWordsValue, ok := f.receiptMaxWords(*maxWords, f.set("receipt-max-words"), *busDir, stderr)
	if !ok {
		return 2
	}
	if !f.count("open-max", *openMax, stderr) {
		return 2
	}
	if !f.atLeastZero("open-warn", *openWarn, stderr) {
		return 2
	}
	if *after != "" && !*bodies {
		fmt.Fprintln(stderr, "INBOX REFUSED: --after requires --bodies; run: nova-bus inbox -h")
		return 2
	}
	if *bodies && !bodyLimit(stderr, "--max-notes", int64(*maxNotes), maxBodiesNotesCeiling) {
		return 2
	}
	if *bodies && !bodyLimit(stderr, "--max-bytes", *maxBytes, maxBodiesBytesCeiling) {
		return 2
	}
	if !f.gitTimeoutFlag(*gitSeconds, stderr) {
		return 2
	}
	if !f.count("max-commits", *maxCommits, stderr) {
		return 2
	}
	// --advance WRITES to the bus, so it takes the same three flags a receipt takes and
	// refuses to guess any of them. Without it, inbox writes nothing at all, which is what
	// a report should do unless it was asked otherwise.
	if *advance {
		if strings.TrimSpace(*remote) == "" || strings.TrimSpace(*branch) == "" {
			fmt.Fprint(stderr, "INBOX REFUSED: --advance moves a cursor onto the bus, so it needs --remote and --branch; refusing to guess; run: nova-bus inbox -h\n")
			return 2
		}
		if !f.attempts(*attempts, stderr) {
			return 2
		}
		if !f.gitArgs(*remote, *branch, stderr) {
			return 2
		}
	}
	o := inboxOpts{
		dryRun: *dryRun,
		busDir: *busDir, as: *as, maxWords: maxWordsValue,
		full: *full, openList: *openList, openMax: *openMax, openWarn: *openWarn, advance: *advance,
		remote: *remote, branch: *branch, attempts: *attempts, noPush: *noPush,
		legacy: flagLegacy, carryHistory: *carryHistory,
		bodies: *bodies, maxNotes: *maxNotes, maxBytes: *maxBytes, after: *after,
		maxCommits: *maxCommits, walkProgress: true,
		diagnostics: *diagnostics,
	}
	// THE ROOT CHECK COMES BEFORE THE ROSTER, and it did not. Point --bus at a
	// subdirectory of a bigger repository and the run refused with "participants.json: no
	// such file" -- true, and the wrong sentence: the caller's mistake is the directory,
	// not the roster, and the refusal that names the repository root is the one that fixes
	// the invocation. The cheaper check is not the more useful one, so the more useful one
	// runs first.
	if !o.full || o.advance {
		if err := bus.IsRepoRoot(o.busDir); err != nil {
			fmt.Fprintf(stderr, "INBOX REFUSED: reading only what changed, and moving a cursor, need git; %s\n", oneline.WithRemedy(oneline.Err(err), "nova-bus inbox -h"))
			return 2
		}
		release, code := lockCheckout("INBOX", o.busDir, stderr)
		if code != 0 {
			return code
		}
		defer release()
	}
	code, r := inboxListing(o, stdout, stderr, now)
	// r.Bounded is the listing that did not run: the since-walk stopped at --max-commits,
	// the remedy is already on stderr, and there is nothing read for a cursor to stand on.
	if code != 0 || r.Bounded || !o.advance || (o.bodies && r.AdvanceTo == "") {
		return code
	}
	if o.bodies {
		return advanceCursorTo(o.busDir, r.Me, r.Open, legacyToken(r.Legacy), r.AdvanceTo, o.remote, o.branch, o.attempts, o.noPush, o.noBeat, o.dryRun, now, stdout, stderr)
	}
	return advanceCursor(o.busDir, r.Me, r.Open, legacyToken(r.Legacy), o.remote, o.branch, o.attempts, o.noPush, o.noBeat, o.dryRun, now, stdout, stderr)
}

// inboxOpts is one listing's whole invocation, read from the flags and checked.
//
// It is a struct because there are now TWO verbs that produce an inbox listing -- `inbox`,
// and `wait`, which is `inbox` on a clock -- and a listing assembled twice is two inboxes
// that will disagree about something on the day one of them is changed. Everything below
// this line is written once and both verbs run it.
type inboxOpts struct {
	busDir, as     string
	maxWords       int
	full, openList bool
	// openMax is how many carried entries a listing run prints before it says how many it
	// did not, and openWarn is how large the open list gets before every return says so.
	// Both are on the struct rather than read at the print site because `wait` is `inbox`
	// on a clock and a flag one verb honoured and the other did not is two inboxes.
	openMax  int
	openWarn int
	advance  bool
	// dryRun is `inbox --dry-run`: an advance prints the cursor it would write and writes nothing.
	dryRun         bool
	remote, branch string
	attempts       int
	noPush         bool
	// legacy is the --legacy-before line as the flag gave it, or the zero line for no flag.
	// What a run actually reads under is effectiveLegacy of this and the cursor's own.
	legacy       bus.LegacyLine
	carryHistory bool
	bodies       bool
	maxNotes     int
	maxBytes     int64
	after        string
	// maxCommits bounds the since-walk: a cursor more than this many commits behind HEAD
	// stops the run with one INBOX WALK bounded line and a remedy rather than walking a
	// history nobody asked to read. Zero means the default; `wait` leaves it zero.
	maxCommits int
	// walkProgress is set by `inbox` (and not by `wait`, whose polls are short and plural)
	// so the since-walk reports INBOX WALK progress on stderr. The bound applies either
	// way; only the narration is a verb's choice.
	walkProgress bool
	diagnostics  bool
	// quietBeats records that the caller passed `wait --quiet-beats`. A change
	// that is only beats and cursors never wakes a wait, so the flag is accepted and
	// changes nothing; it is kept so callers that pass it keep working. It is `wait`'s
	// only; `inbox` leaves it false.
	quietBeats bool
	// me is the reader resolved against the roster, carried so `wait` can name its lane's
	// files without resolving the roster twice. It is `wait`'s only; `inbox` leaves it zero.
	me bus.Participant
	// noBeat is `wait --no-beat`: this wait does not own the lane's BEAT. No
	// wait writes a BEAT or makes a beat commit -- presence is friend:<name> in Redis --
	// so what the flag still decides is whether a dirty BEAT an older wait left behind is
	// this wait's to discard (a process BESIDE a line never discards the line's).
	// `inbox` leaves it false.
	noBeat bool
	// onNote is `wait --on-note`: on a note arrival exit 0 with the note, on an empty
	// tick print WAIT TIMEOUT and the rearm line so the harness can re-arm. Prints no
	// INBOX OPEN frame. `inbox` leaves it false.
	onNote bool
}

// inboxReading is what one listing found, for the caller that has to act on it: `inbox`
// advances a cursor over it, and `wait` decides from it whether to stop waiting.
type inboxReading struct {
	// Me is the reader, resolved against the roster.
	Me bus.Participant
	// Open is the open list this run derived, which is what an --advance writes back.
	Open []bus.OpenEntry
	// Legacy is the switch-day line this run READ UNDER: the flag, or the cursor's.
	Legacy bus.LegacyLine
	// Cursor is the commit this run read from, and "" for a full read.
	Cursor string
	// Bounded says the since-walk STOPPED at --max-commits and this listing read nothing:
	// the run printed one INBOX WALK bounded line with the remedy and exited 0. It is on
	// the reading rather than in the exit code because "nothing to report" and "I did not
	// look" are the same 0 to a shell and must not be the same thing to a caller holding
	// --advance: a cursor moved over a walk nobody made takes every note behind the bound
	// as read.
	Bounded bool
	// Full says the run walked the whole bus.
	Full bool
	// SwitchDay says this listing PRINTED the INBOX SWITCH line: the reader's cursor
	// carries a forward-drawn date line, and the listing has already handed them the
	// command that redraws it. `wait` reads it so that the one sentence about a line
	// drawn forward is said once, by the listing, and not again by the clock around it.
	SwitchDay bool
	// New is how many notes this run would show a reader as NEWS: the notes it put on the
	// open list that were not on it before, and on a full read -- a reader with no cursor,
	// who has been shown nothing yet -- the whole open list. It is what `wait` returns on,
	// and it is deliberately not the size of the open list on an incremental run: a reader
	// carrying five hundred settled notes is not a reader with news.
	New int
	// HeardNew is how many of the notes this run would show as NEWS are already heard by
	// this reader -- receipted with `receipt --note` since the cursor, not answered. They
	// are still new to the open list (heard is not answered), but they are news the reader
	// has already taken; `wait --advance` skips them rather than returning on them.
	HeardNew int
	// Fresh is the NEW entries themselves -- the whole open list on a full read -- so
	// `wait --on-note` can wake on a note addressed To: the reader and on nothing else.
	Fresh []bus.OpenEntry
	// Changed is how many lane paths the incremental diff named. It is scope.Changed, held
	// here for a caller that wants to tell a beat/cursor-only change from no change at all.
	Changed int
	// NoteChanges is how many of those changed paths were notes; see bus.InboxResult.
	NoteChanges int
	Next        string
	BodyBytes   int64
	BodyPrinted int
	BodyGaps    int
	AdvanceTo   string
}

// inboxListing is the whole of an inbox report: what to read, what it found, and every
// line of it printed. It writes nothing to the bus -- moving the cursor is advanceCursor,
// which its caller runs after it -- and it takes no lock: the caller holds the checkout,
// because `wait` holds it across a poll's fetch as well as its listing.
func inboxListing(o inboxOpts, stdout, stderr io.Writer, now time.Time) (int, inboxReading) {
	var r inboxReading
	c, err := bus.LoadConfig(o.busDir)
	if err != nil {
		fmt.Fprintf(stderr, "INBOX REFUSED: %s\n", oneline.WithRemedy(oneline.Err(err), "nova-bus inbox -h"))
		return 2, r
	}
	me, found := c.Lookup(o.as)
	if !found {
		fmt.Fprintf(stderr, "INBOX REFUSED: --as %q names no one on this bus (known: %s); run: nova-bus inbox -h\n", o.as, oneline.Escape(strings.Join(c.KnownNames(), "; ")))
		return 2, r
	}
	if me.Lane == "" {
		fmt.Fprintf(stderr, "INBOX REFUSED: %q has no lane on this bus, so nothing can answer for them; run: nova-bus inbox -h\n", me.Name)
		return 2, r
	}

	scope := bus.Scope{Full: o.full}
	var res bus.InboxResult
	var cursor bus.Cursor
	var priorOpen []bus.OpenEntry
	// held is the cursor as it stands on the bus, read for the SWITCH-DAY LINE it carries
	// as well as for the commit. A full run reads it too, and ignores a cursor it cannot
	// read: `--full --advance` is the documented repair for a broken cursor, and a repair
	// that refuses to run is not one. What a full run must not do is silently forget a line
	// a reader drew months ago, which is what reading it here prevents.
	held := bus.Cursor{}
	if o.full {
		held, _ = bus.ReadCursor(o.busDir, me.Lane)
		// A full read rebuilds the current listing, but an existing valid OPEN file is
		// still the bookkeeping that distinguishes carried work from NEW bodies.  Keep
		// its survivors when a bounded full page advances; only newly eligible bodies
		// wait for their own emitted prefix.
		priorOpen, _ = bus.ReadOpen(o.busDir, me.Lane)
	}
	// The line this run reads under, whichever mode it is in.
	var legacy bus.LegacyLine
	if !o.full {
		cursor, err = bus.ReadCursor(o.busDir, me.Lane)
		if err != nil {
			fmt.Fprintf(stderr, "INBOX REFUSED: %s\n", oneline.WithRemedy(oneline.Err(err), "nova-bus inbox -h"))
			return 1, r
		}
		held = cursor
		if cursor.Commit == "" {
			// A reader with no cursor has no place to stand, so the first run is a full
			// one and --advance gives them a cursor from then on. That is the adoption
			// path, and it costs exactly one full read, ever.
			scope.Full = true
		} else {
			// A line that MOVES EARLIER re-opens every note between the two dates, which
			// on the bus this was written for is hundreds -- and it would arrive as a
			// listing the reader has already settled, with nothing saying why. Moving it
			// later forgives more and is fine; moving it earlier is a refusal that names
			// the read which can honestly do it, because a --full run derives the whole
			// open list again rather than taking the cursor's word for it.
			if !o.legacy.Before.IsZero() && held.Legacy != "" && o.legacy.Before.Before(held.LegacyBefore()) {
				fmt.Fprintf(stderr, "INBOX REFUSED: your cursor was written with legacy=%s and --legacy-before %s moves the line earlier, which would put the notes between the two back on your open list; read once with --full --legacy-before %s --advance, which builds the open list again from the whole bus, or leave the flag off and the cursor's line stands; run: nova-bus inbox -h\n",
					oneline.Field(held.Legacy), oneline.Field(o.legacy.Text), oneline.Field(o.legacy.Text))
				return 1, r
			}
			ok, err := bus.IsAncestor(o.busDir, cursor.Commit)
			if err != nil {
				fmt.Fprintf(stderr, "INBOX REFUSED: %s\n", oneline.WithRemedy(oneline.Err(err), "nova-bus inbox -h"))
				return 1, r
			}
			if !ok {
				fmt.Fprintf(stderr, "INBOX REFUSED: the cursor %s is not an ancestor of HEAD, so a diff from it would report changes that are not changes and miss notes that are (a rewritten history, or a cursor from another branch); read once with --full, and --advance will replace it; run: nova-bus inbox -h\n", oneline.Field(cursor.Commit))
				return 1, r
			}
			// The other way a cursor stops being trustworthy: the OPEN list it was
			// written beside is gone. An empty OPEN list is REMOVED rather than left
			// zero-length, so absent and nothing-open are one state on disk -- which is
			// why the cursor carries the count it was written with, and why a cursor
			// that says it was carrying notes with no OPEN beside it is refused here
			// instead of quietly reporting open=0 over the notes it dropped.
			if cursor.Counted && cursor.Open > 0 && !bus.OpenPresent(o.busDir, me.Lane) {
				fmt.Fprintf(stderr, "INBOX REFUSED: your cursor %s says it was carrying %d notes and %s is not on the bus, so a read from it would drop them and print open=0; read once with --full --advance, which rebuilds the open list from the whole bus; run: nova-bus inbox -h\n",
					oneline.Field(cursor.Commit), cursor.Open, oneline.Field(bus.OpenPath(me.Lane)))
				return 1, r
			}
			// THE SINCE-WALK IS BOUNDED AND IT SAYS SO. A cursor left hundreds of commits
			// behind turns one diff into a long silence: on the bus this was written for a
			// 285-commit stale cursor over 2150 open notes ran four minutes and printed
			// nothing new. The count is cheap and comes first, so an over-bound cursor costs
			// one `rev-list --count` and one line rather than the walk. The line carries the
			// remedy and the run is exit 0: refusing to read is not this verb's business,
			// saying what the read would cost is.
			//
			// THE COUNT IS BOUNDED TOO, which is the half that was missing. `rev-list --count`
			// walks the whole distance before it can answer, so the run that reads NOTHING --
			// the over-bound one -- was paying for every commit between the cursor and the head
			// in order to be told it should not read them. Asked with the bound
			// (CommitsSinceBounded), git stops one commit past it: a cursor five hundred
			// commits behind and one fifty thousand commits behind now cost the same 501, and a
			// cursor inside the bound still gets its exact total for the progress line.
			limit := o.maxCommits
			if limit <= 0 {
				limit = defaultMaxCommits
			}
			total, over, err := bus.CommitsSinceBounded(o.busDir, cursor.Commit, limit)
			if err != nil {
				fmt.Fprintf(stderr, "INBOX REFUSED: %s\n", oneline.WithRemedy(oneline.Err(err), "nova-bus inbox -h"))
				return 1, r
			}
			if over {
				fmt.Fprintf(stderr, "INBOX WALK bounded commits=%d %s\n", limit, boundedWalkRemedy)
				// THE BOUND ENDS THE RUN, AND THAT INCLUDES THE ADVANCE. This is the one
				// exit-0 way out of a listing that did not run, and the caller reads exit 0
				// plus --advance as "the listing is done, move the cursor". It used to hand
				// back the ZERO reading -- Me is resolved onto it at the END of a listing
				// that finished -- so the advance ran with an EMPTY LANE, wrote CURSOR at
				// the checkout ROOT, and left it there for every later run on that bus to
				// refuse over until a human deleted it.
				//
				// Both halves are named here: who the reader is, so nothing downstream is
				// guessing, and Bounded, which is the caller's instruction not to advance. A
				// walk that read NOTHING has no claim to make -- advancing over it would take
				// every unread note behind the bound as read, which is the one outcome the
				// bound exists to prevent.
				r.Me, r.Bounded = me, true
				return 0, r
			}
			var walk *walkProgress
			// A continuation (`--after`) is an explicit resume of a bounded snapshot, and its
			// refusals are the ONE line refuseContinuation prints, validated AFTER this walk:
			// narrating the walk first would put a second line above a refusal the contract
			// says is one. So the narration is for the ordinary inbox read and the resume
			// stays quiet; the bound above still applies to both.
			if o.walkProgress && o.after == "" {
				walk = newWalkProgress(stderr, total)
			}
			changed, err := bus.ChangedSince(o.busDir, cursor.Commit)
			if err != nil {
				if walk != nil {
					walk.abort()
				}
				fmt.Fprintf(stderr, "INBOX REFUSED: %s\n", oneline.WithRemedy(oneline.Err(err), "nova-bus inbox -h"))
				return 1, r
			}
			if walk != nil {
				// The commits are behind us; what is left is parsing the notes they named.
				walk.advance(total, 0)
			}
			open, err := bus.ReadOpen(o.busDir, me.Lane)
			if err != nil {
				if walk != nil {
					walk.abort()
				}
				fmt.Fprintf(stderr, "INBOX REFUSED: %s\n", oneline.WithRemedy(oneline.Err(err), "nova-bus inbox -h"))
				return 1, r
			}
			priorOpen = open
			scope.From, scope.Changed = cursor.Commit, len(changed)
			legacy = effectiveLegacy(o.legacy, held)
			res, err = bus.InboxSince(o.busDir, c, me, changed, open, o.maxWords, legacy)
			if err != nil {
				if walk != nil {
					walk.abort()
				}
				fmt.Fprintf(stderr, "INBOX REFUSED: %s; run: nova-bus inbox -h\n", oneline.Err(err))
				return 2, r
			}
			if walk != nil {
				walk.finish(total, res.NoteChanges)
			}
		}
	}
	if scope.Full {
		t, err := bus.ReadBus(o.busDir, c)
		if err != nil {
			fmt.Fprintf(stderr, "INBOX REFUSED: %s; run: nova-bus inbox -h\n", oneline.Err(err))
			return 2, r
		}
		legacy = effectiveLegacy(o.legacy, held)
		// A full read is the one that DERIVES the open list rather than inheriting it: the
		// display line of every open note, the heard flag of every receipted one rebuilt from
		// RECEIPTS, and the unreadable files carried rather than named once and dropped. Every
		// later incremental run prints from what this run writes.
		res.Unreadable = t.Unreadable(me.Lane)
		// EVERY note on the bus that reaches no reader, named on a full read to every
		// reader. A note whose To line resolves to nobody parses, so it is not unreadable,
		// and it is in no inbox, so no listing has ever mentioned it: 22 of them on the
		// family's real bus. See internal/bus/unaddressed.go.
		res.Unaddressed = t.UnaddressedOnBus()
		// The line still shapes the open list a full run WRITES -- an old note is not
		// carried, and neither is an old file nobody can read -- but a full run LISTS
		// everything it found, unreadable files included, whatever their date. That is what
		// a full read is for: the whole picture, asked for on purpose, on the day you adopt
		// the tool or the day something looks wrong. The quiet is the incremental run's.
		res.Open, res.Legacy, res.LegacyUnreadable = bus.SplitLegacy(bus.OpenFromFull(t.Inbox(me, o.maxWords), res.Unreadable), legacy)
	}

	// THE FIRST ADVANCE ON A LANE, on a bus that is older than this reader's cursor.
	//
	// THE FAILURE, from a live line. Its first run was
	// `inbox --full --advance` with no switch-day line, on a bus holding about 1,900 notes.
	// It worked exactly as written: 602 old notes went onto the open list, the cursor was
	// written beside them, and every poll from then on printed the same 602 carried notes,
	// forever, because an open note only comes off the list when something answers it.
	// Read back, the polls were mostly noise, and every line of noise costs tokens.
	// Every one of those lines was paid for, on every run, by a reader who had never said
	// they meant to carry the history.
	//
	// The switch-day line already existed and would have prevented all of it. What did not
	// exist was anything making the reader MEET it: the flag had to be known about before
	// the run that needed it, and the run that needed it is by definition the first one.
	// So the first advance on a lane is the one place this tool asks. It fires only when
	// all four are true -- an --advance, no CURSOR file on this lane, no line in force, and
	// notes on the open list dated before today -- and it names the number it would have
	// carried and the exact line to run. `--carry-history` is the other answer, for the
	// reader who means it, and it is a flag rather than a default because the default that
	// carried 602 notes is the one being fixed.
	//
	// It refuses BEFORE the listing is printed. A first full read of an old bus prints a
	// line per open note, which on that bus is the six hundred lines this guard exists to
	// stop; printing them and then refusing would charge the reader for them anyway.
	if o.advance && !o.carryHistory && legacy.Before.IsZero() && !bus.CursorPresent(o.busDir, me.Lane) {
		_, oldNotes, oldUnreadable := bus.SplitLegacy(res.Open, bus.LegacyLine{Before: utcDay(now)})
		old := oldNotes + oldUnreadable
		if old > 0 {
			// THE SUGGESTED LINE IS AN INSTANT, and it used to be tomorrow's DATE. A date
			// is midnight at its START, so tomorrow's date is a moment AFTER everything
			// written today: the line this guard handed a stuck reader made the whole switch
			// day legacy, and the notes they were being refused for carrying were joined by
			// every note anybody sent while they were reading the refusal. Drawn at an
			// instant, everything on the bus at that moment is history and everything after
			// it is news, which is the sentence this message actually makes.
			//
			// It hands over --legacy-now rather than the timestamp it would print, and that
			// is a second fix on top of the first. A pasted timestamp is a line drawn at the
			// moment of the REFUSAL, which may be an hour before the reader gets round to
			// running it; --legacy-now is drawn at the moment of the READ. And a command
			// nobody has to retype correctly is a command nobody mistypes into a date, which
			// is exactly what the reader this was written for did.
			fmt.Fprintf(stderr, "INBOX REFUSED: this is the first advance on %s and %d of the %d notes it would carry are dated before now, so every run after it would print all %d again; draw the switch-day line at this instant with `nova-bus inbox --bus %s --as %s --receipt-max-words %d --full --legacy-now --advance --remote %s --branch %s`, which takes everything already on the bus as read and leaves you what arrives after that moment, or pass --carry-history to carry all %d\n",
				oneline.Field(bus.CursorPath(me.Lane)), old, len(res.Open), len(res.Open),
				oneline.Quote(o.busDir), oneline.Quote(me.Name), o.maxWords,
				oneline.Quote(o.remote), oneline.Quote(o.branch), len(res.Open))
			return 1, r
		}
	}

	// What was walked, said FIRST, because a listing that does not say what it looked at
	// is a listing a reader will mistake for everything.
	fmt.Fprintf(stdout, "INBOX SCOPE mode=%s cursor=%s changed=%d carrying=%d\n",
		oneline.Field(scope.Mode()), oneline.Field(dash(cursor.Commit)), scope.Changed, len(res.Open))
	// AND THEN, IF THIS READER'S LINE IS DRAWN FORWARD, THE SENTENCE THAT SAYS SO. It is
	// printed from the cursor as it stands on the bus -- not from the flag this run was
	// given -- because it is a fact about the state a reader is stuck in, and the run that
	// is finally fixing it should say once what it is fixing. See the site below and
	// bus.LegacyDateAtOrAfterToday.
	r.SwitchDay = printSwitchDayNote(stdout, held.Legacy, o.busDir, me.Name, fmt.Sprintf("%d", o.maxWords), o.remote, o.branch, now)
	// The switch-day line, said as ONE line and next to the scope, because it is part of
	// what this run looked at: `notes=` is how many notes it left off the open list for
	// being older than the line, and `unreadable=` how many FILES it left off for the same
	// reason. They are never listed one by one -- the whole point of the line is that six
	// hundred of them do not become a listing -- and this is the only place a reader is
	// told either number, so it is printed whenever a line is in force, counts of zero
	// included. Two numbers and not one, because they are two different facts: a note taken
	// as read, and a file nobody could read in the first place.
	if !legacy.Before.IsZero() {
		fmt.Fprintf(stdout, "INBOX LEGACY before=%s notes=%d unreadable=%d\n",
			oneline.Field(legacy.Text), res.Legacy, res.LegacyUnreadable)
	}
	// The files that would not parse are named next and are never silent. A note on the
	// bus that this tool cannot read is not a note that does not exist, and dropping it
	// from the listing was the same failure as a lost push with a quieter cause. The one
	// thing that is not named per file is a file dated BEHIND the switch-day line on an
	// incremental run -- history, counted on the LEGACY line above; see bus.LegacyLine.
	//
	// An unreadable file that is CARRIED rather than new is unchanged since the cursor:
	// the reader has already been shown it, and a long list of them printed on every poll
	// buries the inbox above them. So the default collapses an unchanged set to one count
	// line. The per-file lines stay for anything new -- which is news, and needs its reason
	// before a reader can fix it -- for a full read, which lists everything on purpose,
	// and for --diagnostics, which asks for the whole picture whatever the default is.
	if len(res.Unreadable) > 0 && (o.diagnostics || res.UnreadableChanged || scope.Full) {
		for _, n := range res.Unreadable {
			fmt.Fprintf(stdout, "INBOX UNREADABLE path=%s: %s\n", oneline.Field(n.Path), oneline.Err(n.Parse.Err))
		}
	} else if len(res.Unreadable) > 0 {
		fmt.Fprintf(stdout, "INBOX UNREADABLE count=%d unchanged=true first=%s\n",
			len(res.Unreadable), oneline.Field(res.Unreadable[0].Path))
	}
	// And the notes that PARSE and reach nobody, which is the quieter half of the same
	// failure: a file this tool cannot read is at least reported, and a note addressed to a
	// name the roster does not hold was in no listing at all. Whichever way the run was
	// asked, for the same reason UNREADABLE is: a note nobody is shown is not a listing
	// choice.
	for _, u := range res.Unaddressed {
		fmt.Fprintf(stdout, "INBOX UNADDRESSED path=%s: %s\n", oneline.Field(u.Path), oneline.Escape(u.Reason))
	}
	notes, receipts, heard := res.Counts()
	// WHAT IS NEW, IN FULL, ON EVERY RUN. This is the listing a poll is FOR, and until now
	// there was no way to get it on its own: the choice was one summary line, or the whole
	// carried list. So a reader who wanted to see the note that had just arrived asked for
	// `--open` and got every note they had ever failed to answer, on every return, above the
	// one they were looking for. A line on a 260K-token model did exactly that with 74
	// carried notes and blew its context. News and backlog are different questions, and the
	// news is the one every run answers.
	//
	// It is skipped on a run that is about to list the WHOLE open list -- `--open`, or a
	// full read -- because the new entries are in that list and printing them twice is the
	// same noise from the other end.
	listCarried := o.openList || scope.Full
	if o.bodies || !listCarried {
		if o.bodies {
			head, headErr := bus.HeadCommit(o.busDir)
			if headErr != nil {
				fmt.Fprintf(stderr, "INBOX REFUSED: %s\n", oneline.WithRemedy(oneline.Err(headErr), "nova-bus inbox -h"))
				return 1, r
			}
			base, expected := cursor.Commit, cursor.Commit
			if scope.Full {
				base, expected = held.Commit, held.Commit
			}
			snapshot := bus.BodySnapshot{Base: base, Head: head, Reader: me.Name, Selector: "inbox-new"}
			if o.after != "" {
				snapshot, _, headErr = bus.BodyContinuation(o.after)
				if headErr != nil {
					return refuseContinuation(stderr, headErr), r
				}
				if snapshot.Reader != me.Name || snapshot.Selector != "inbox-new" {
					return refuseContinuation(stderr, errors.New("it belongs to another reader or selector")), r
				}
			}
			// Rebuild C0..H directly. Current OPEN is deliberately not an input: a note
			// after H may have answered an older item, while that older token still owes
			// the body from its immutable snapshot.
			items, pageErr := bus.BodyNewItemsAtSnapshot(o.busDir, snapshot, c, me, o.maxWords, legacy)
			if pageErr != nil {
				if o.after != "" {
					return refuseContinuation(stderr, pageErr), r
				}
				fmt.Fprintf(stderr, "INBOX REFUSED: %s\n", oneline.WithRemedy(oneline.Err(pageErr), "nova-bus inbox -h"))
				return 2, r
			}
			page, pageErr := bus.BodyPageFor(items, bus.BodyPageRequest{Snapshot: snapshot, ExpectedCursor: expected, Advance: o.advance, Token: o.after, MaxNotes: o.maxNotes, MaxBytes: o.maxBytes})
			if pageErr != nil {
				if o.after != "" {
					return refuseContinuation(stderr, pageErr), r
				}
				fmt.Fprintf(stderr, "INBOX REFUSED: %s\n", oneline.WithRemedy(oneline.Err(pageErr), "nova-bus inbox -h"))
				return 2, r
			}
			if err := printBodyPage(stdout, page, o.maxBytes); err != nil {
				fmt.Fprintf(stderr, "INBOX FAIL output: %s\n", oneline.Err(err))
				return 1, r
			}
			r.BodyPrinted, r.BodyBytes, r.BodyGaps, r.Next = page.Frames, page.PrintedBytes, page.GapCount, page.Next
			// Only the NEW entries fully emitted on this page become carried.  Keeping
			// every entry from the current listing here would move CURSOR past B while
			// silently placing B in OPEN, so its body would never get its own NEW turn.
			r.Open = openAfterBodies(res.Open, res.Fresh, priorOpen, items, page, scope.Full)
			if page.SafeFrontier != expected {
				r.AdvanceTo = page.SafeFrontier
			}
			// THE REMEDY STANDS IMMEDIATELY BEFORE THE RECEIPT AND NOWHERE ELSE. A chain
			// holding many gaps names the earliest one and prints no list, so this is one
			// line at every state -- and it is on stdout, beside the receipt it explains,
			// because a caller reconciling `complete=false` reads one stream.
			if page.Drained && !page.Complete && page.EarliestGap != nil {
				gap := page.EarliestGap
				kind, retry := "over-budget", strconv.FormatInt(gap.Bytes, 10)
				if gap.Bytes > maxBodiesBytesCeiling {
					// No value under the hard ceiling carries it, so there is no number to
					// name: the door is the file, and path= is what opens it.
					kind, retry = "over-ceiling", "-"
				}
				if _, err := fmt.Fprintf(stdout, "INBOX BODIES GAP id=%s kind=%s retry-max-bytes=%s path=%s\n", oneline.Field(dash(gap.ID)), oneline.Field(kind), oneline.Field(retry), oneline.Field(gap.Path)); err != nil {
					fmt.Fprintf(stderr, "INBOX FAIL output: %s\n", oneline.Err(err))
					return 1, inboxReading{}
				}
			}
			if _, err := fmt.Fprintf(stdout, "INBOX BODIES printed=%d bytes=%d oversize=%d gaps=%d drained=%t complete=%t next=%s\n", r.BodyPrinted, r.BodyBytes, len(page.Gaps), r.BodyGaps, page.Drained, page.Complete, oneline.Field(dash(r.Next))); err != nil {
				fmt.Fprintf(stderr, "INBOX FAIL output: %s\n", oneline.Err(err))
				return 1, inboxReading{}
			}
		} else {
			if _, err := printOpenEntries(stdout, res.Fresh, len(res.Fresh)); err != nil {
				fmt.Fprintf(stderr, "INBOX FAIL output: %s\n", oneline.Err(err))
				return 1, r
			}
		}
	}
	// THEN ONE LINE FOR THE BACKLOG, whichever way the run was asked. It was printed only on
	// the runs that did NOT list, which meant the two shapes of return had no line in common
	// and a reader parsing `--open` output could not find the count at all. One line, always,
	// under one name. The large-list sentence used to be a SECOND line, past a size, with a
	// whole command pasted into it; a reader parsing OPEN could not tell small from large
	// without knowing the threshold, and the command duplicated every run's own flags. One
	// line carries both now: `large=` is the sentence, and `remedy=` names the one whole-
	// backlog way out without spelling a command the caller already built. A large set's
	// remedy is not `inbox --advance`: that moves the read cursor while the OPEN entries
	// stay carried, so it loops without resolving anything. Reply or receipt each note is
	// the normal answer, and `close --before` is the explicit opt-in bulk cutoff.
	if len(res.Open) > o.openWarn {
		fmt.Fprintf(stdout, "INBOX OPEN carrying=%d heard=%d large=true remedy=%s\n",
			len(res.Open), heard, remedyLarge)
	} else {
		fmt.Fprintf(stdout, "INBOX OPEN carrying=%d heard=%d large=false remedy=%s\n",
			len(res.Open), heard, remedyAdvance)
	}
	// LISTING THE CARRIED NOTES IS A CHOICE, and the default is not to. A reader carrying five
	// hundred notes gets five hundred lines on every run, and the one new note is in the
	// middle of them -- which is the listing-nobody-reads failure the switch-day line exists
	// to stop, arriving from the other end. So `--open` prints the entries, and `--full` lists
	// them too because a full read is what a person asks for when they want the whole picture.
	// Nothing is hidden either way: the counts are on the OK line, and the entries are in
	// OPEN, which is a file.
	//
	// AND IT IS CAPPED. `--open` on a long list was the footgun itself: a flag whose cost
	// grows with the backlog, asked for by the reader with the biggest backlog, printed into
	// a context window that has no way to refuse it. --open-max is the cap, it has a default
	// so that nobody has to know about it before the run that needed it, and the line under
	// the listing says how many were left and which flag widens it.
	if listCarried {
		rows := bus.SortForListing(res.Open)
		shown, err := printOpenEntries(stdout, rows, o.openMax)
		if err != nil {
			fmt.Fprintf(stderr, "INBOX FAIL output: %s\n", oneline.Err(err))
			return 1, r
		}
		if more := countListable(rows) - shown; more > 0 {
			fmt.Fprintf(stdout, "INBOX OPEN listed=%d and %d more (--open-max to widen)\n", shown, more)
		}
	}
	// AND, PAST A SIZE, THE LINE THAT SAYS THE LIST IS LARGE AND HOW TO EMPTY IT. A backlog
	// grows one unanswered note at a time and nothing about any single run says it is
	// growing: `carrying=74` is a number, and a number is not a sentence. The line that
	// carried 74 was answering every one of those notes by hand -- and none of the answers
	// carried a Re line, so none of them closed anything, and nothing anywhere said so.
	// The word for it is now `large=` on the one OPEN line above, and the remedy names the
	// normal answer (reply or receipt each carried note) with `close --before` as the
	// explicit opt-in bulk cutoff, never `--advance` alone.
	// THE TWO NUMBERS, and why they are both here under the names they are printed under
	// elsewhere. `carrying=` on the SCOPE and OPEN lines is the size of the OPEN LIST -- every
	// entry on it, the heard and the unreadable included -- and `open=` is what is still
	// WAITING ON ME, which is the notes and the bare receipts and nothing else. A note I
	// have receipted is on the list and out of that count: I have answered the sender's
	// question about whether it arrived, and not their note. The two therefore differ, by
	// exactly heard + unreadable, and on one real run they read `carrying=658` and
	// `open=657` on two lines with no third line saying why. So the OK line now carries
	// BOTH, under the same names, beside the decomposition that makes them add up.
	fmt.Fprintf(stdout, "INBOX OK as=%s carrying=%d open=%d notes=%d receipts=%d heard=%d unaddressed=%d unreadable=%d\n",
		oneline.Field(me.Name), len(res.Open), notes+receipts, notes, receipts, heard, len(res.Unaddressed), len(res.Unreadable))
	r.Me, r.Legacy, r.Cursor, r.Full = me, legacy, cursor.Commit, scope.Full
	r.Changed, r.NoteChanges = scope.Changed, res.NoteChanges
	if !o.bodies {
		r.Open = res.Open
	}
	// What this run would show a reader as news; see inboxReading.New.
	r.New = res.New
	r.Fresh = res.Fresh
	if scope.Full {
		r.New = len(res.Open)
		r.Fresh = res.Open
	}
	for _, e := range res.Fresh {
		if e.Heard {
			r.HeardNew++
		}
	}
	return 0, r
}

func openAfterBodies(current, fresh, prior []bus.OpenEntry, items []bus.BodyItem, page bus.BodyPage, full bool) []bus.OpenEntry {
	freshPath := make(map[string]bool, len(fresh))
	for _, entry := range fresh {
		freshPath[entry.Path] = true
	}
	// A full read derives OPEN anew.  Its listing may contain every outstanding note
	// while a bodies page has emitted only one, so retaining current here would move a
	// cursor past unprinted bodies.  Incremental reads retain only their prior carry;
	// current-fresh rows are rebuilt below from the immutable emitted prefix.
	out := make([]bus.OpenEntry, 0, len(current)+len(items))
	if full {
		currentByPath := make(map[string]bus.OpenEntry, len(current))
		for _, entry := range current {
			currentByPath[entry.Path] = entry
		}
		for _, entry := range prior {
			if survivor, ok := currentByPath[entry.Path]; ok {
				out = append(out, survivor)
			}
		}
	} else {
		for _, entry := range current {
			if !freshPath[entry.Path] {
				out = append(out, entry)
			}
		}
	}
	inOut := make(map[string]bool, len(out))
	for _, entry := range out {
		inOut[entry.Path] = true
	}
	// SafeFrontier is precisely the greatest complete emitted prefix.  Re-add every
	// eligible item through it, rather than only this page's Items: the final page of a
	// two-note commit must persist both A and B, and a later page must not replace A.
	frontier := -1
	for i, item := range items {
		if item.Commit == page.SafeFrontier {
			frontier = i
		}
	}
	for i := 0; i <= frontier; i++ {
		item := items[i]
		if !inOut[item.Entry.Path] {
			out = append(out, item.Entry)
			inOut[item.Entry.Path] = true
		}
	}
	return out
}

// effectiveLegacy is the line a run reads under: the flag when it is given, and otherwise
// the line this reader's cursor was already written with. A line that had to be retyped on
// every run is a line that would be forgotten on one, and the run that forgot it would open
// every note behind it at once.
func effectiveLegacy(flag bus.LegacyLine, held bus.Cursor) bus.LegacyLine {
	if !flag.Before.IsZero() {
		return flag
	}
	return held.LegacyLine()
}

// legacyToken is the line as a cursor records it -- the text it was given, so that a line
// drawn to the second is stored to the second -- or "" for no line at all.
func legacyToken(l bus.LegacyLine) string {
	if l.Before.IsZero() {
		return ""
	}
	return l.Text
}

// advanceCursor writes this reader's CURSOR and OPEN, commits them under their own
// identity, and pushes them with the same protocol a receipt uses.
//
// The same protocol and not a lighter one, on purpose. A cursor is a claim that everything
// up to a commit has been read, and a claim only one bench can see is a claim nobody at the
// bus can check -- the same reason a receipt is a file and not a mood. So it refuses a
// dirty checkout, refuses a branch ahead of the remote, commits naming its paths under the
// roster's identity, and recovers a rejected push by fetching and rebasing.
//
// It runs AFTER the listing is printed. A reader who was shown their inbox and whose push
// then failed has still been shown their inbox; the cursor simply has not moved, and the
// next run shows them the same notes again -- which is the safe direction to fail in.
//
// The cursor also records the SWITCH-DAY LINE this run read under, so the next run honours
// it without the flag and everybody on the bus can see which notes this reader has taken
// as read.
func advanceCursorTo(busDir string, me bus.Participant, open []bus.OpenEntry, legacy, head, remote, branch string, attempts int, noPush, noBeat, dryRun bool, now time.Time, stdout, stderr io.Writer) int {
	// NO LANE, NO WRITE, AND NOTHING TOUCHED. Every state path this function builds is
	// lane + "/" + name, so a lane-less reader names "/CURSOR" and "/OPEN" -- absolute
	// paths that land at the checkout ROOT and that git refuses to stage as outside the
	// repository. That is how one break ended: the cursor was written at the
	// root, the staging failed, and the stray file refused every later run on the bus.
	//
	// A lane-less reader is a shape the roster can hold -- a participant with no lane of
	// their own is listed so they can be addressed -- so this is a refusal and not a
	// panic, and it comes FIRST, before the checkout is read or a byte is written. The
	// cost of the old order was never the exit code; it was the file left behind.
	if me.Lane == "" {
		// A reader who reached here with no name either is not on the roster at all or was
		// never resolved against it, and "" has no lane on this bus is a line nobody can act
		// on. The refusal says which reader it is when it knows and says so plainly when it
		// does not.
		who := me.Name
		if who == "" {
			who = "this reader"
		}
		fmt.Fprintf(stderr, "INBOX REFUSED: %s has no lane on this bus, so there is nowhere to write a cursor; run: nova-bus inbox -h\n", oneline.Field(who))
		return 1
	}
	paths := []string{bus.CursorPath(me.Lane), bus.OpenPath(me.Lane)}
	// --no-beat: the cursor commit does not name the BEAT, so a read-only wait
	// that advances its cursor still writes nothing of the line's presence onto the bus.
	if !noBeat {
		paths = append(paths, bus.BeatPath(me.Lane))
	}
	if err := checkoutReady(busDir, branch, paths); err != nil {
		fmt.Fprintf(stderr, "INBOX FAIL %s: %s\n", oneline.Escape(bus.CursorPath(me.Lane)), oneline.Err(err))
		return 1
	}
	if dryRun { // every check above is the real run's; everything below writes
		fmt.Fprintf(stdout, "INBOX CURSOR commit=%s carrying=%d pushed=false attempts=0 dry_run=true\n", oneline.Field(head), len(open))
		return 0
	}
	if err := levelWithRemote(busDir, remote, branch, noPush); err != nil {
		fmt.Fprintf(stderr, "INBOX REFUSED: %s\n", oneline.WithRemedy(oneline.Err(err), "nova-bus inbox -h"))
		return 1
	}
	if err := bus.WriteOpen(busDir, me.Lane, open); err != nil {
		fmt.Fprintf(stderr, "INBOX FAIL %s: %s\n", oneline.Escape(bus.OpenPath(me.Lane)), oneline.Err(err))
		return 1
	}
	if err := bus.WriteCursor(busDir, me.Lane, head, len(open), legacy, now); err != nil {
		fmt.Fprintf(stderr, "INBOX FAIL %s: %s\n", oneline.Escape(bus.CursorPath(me.Lane)), oneline.Err(err))
		return 1
	}
	// The commit names both paths, so an OPEN file this run REMOVED -- a reader with
	// nothing left open -- is staged as the deletion it is rather than left behind. A
	// reader who never had one is a different case: there is nothing to record, and asking
	// git to stage a path that is neither on disk nor in the index is exit 128.
	staged, err := bus.StagePaths(busDir, paths)
	if err != nil {
		fmt.Fprintf(stderr, "INBOX FAIL %s: %s\n", oneline.Escape(bus.CursorPath(me.Lane)), oneline.Err(err))
		return 1
	}
	res, err := commit(busDir, me, staged,
		bus.WithTrailer(me.Slug()+": read to "+head[:shortSHA], bus.TrailerCursor),
		remote, branch, attempts, noPush)
	if err != nil {
		fmt.Fprintf(stderr, "INBOX FAIL %s: %s\n", oneline.Escape(bus.CursorPath(me.Lane)), oneline.Err(err))
		printTranscript(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "INBOX CURSOR commit=%s carrying=%d pushed=%t attempts=%d\n",
		oneline.Field(head), len(open), res.Pushed, res.Attempts)
	return 0
}

// advanceCursor preserves the released full-head behaviour for listings without bodies.
// Bodies mode calls advanceCursorTo with the paginator's whole-commit safe frontier.
func advanceCursor(busDir string, me bus.Participant, open []bus.OpenEntry, legacy, remote, branch string, attempts int, noPush, noBeat, dryRun bool, now time.Time, stdout, stderr io.Writer) int {
	head, err := bus.HeadCommit(busDir)
	if err != nil {
		fmt.Fprintf(stderr, "INBOX REFUSED: %s\n", oneline.WithRemedy(oneline.Err(err), "nova-bus inbox -h"))
		return 1
	}
	return advanceCursorTo(busDir, me, open, legacy, head, remote, branch, attempts, noPush, noBeat, dryRun, now, stdout, stderr)
}

// defaultOpenMax is how many carried entries `--open` prints before it says how many it
// did not. Twenty is a screen: enough that a reader with an ordinary backlog sees all of
// it, and few enough that a reader with a big one is not paying for the whole of it on
// every return. It is a DEFAULT rather than a required flag, on the same test the tool's
// other defaults pass -- it is not a fact about a bus that only its owner can supply --
// and the line under the listing names the flag that widens it.
const defaultOpenMax = 20

// defaultOpenWarn is how large a backlog gets before every return says so. Forty is above
// what a working line carries between reads and below the seventy-four that broke one, so
// the line fires while a backlog is still answerable and not after it is hopeless.
const defaultOpenWarn = 40

// remedyAdvance is the INBOX OPEN `remedy=` for a small list: moving the read cursor past
// what is already heard is the normal small-list action. It never claims to close carried
// entries.
const remedyAdvance = "inbox --advance"

// remedyLarge is the INBOX OPEN `remedy=` for a large carrying set. Reply or receipt is
// the normal path that actually resolves the carried notes, and `close --before` is named
// only as the explicit opt-in bulk cutoff -- never `--advance` alone, which loops without
// resolving anything.
const remedyLarge = "reply or receipt each note, or close --before <instant> as an explicit bulk cutoff"

// defaultMaxCommits is how many commits a since-walk may cross before it stops and hands
// back the remedy instead of walking. A cursor hundreds of commits stale was left, not a
// bus that is unreadable, and the run says so in one line rather than spending minutes
// parsing what the reader never asked for. It is a DEFAULT for the same reason `--attempts`
// is: it is not a fact about a bus only its owner can supply, and a caller who has to name
// a number names one too small and is then refused the read they wanted.
const defaultMaxCommits = 500

// boundedWalkRemedy is the one-line remedy an INBOX WALK bounded line carries. It names
// both doors: raise the bound to read the stale cursor, or draw a switch-day line with
// close --before to take the history as read and start the cursor over.
const boundedWalkRemedy = `remedy="raise --max-commits or close --before <instant>"`

// walkProgress narrates a since-walk on stderr:
//
//	INBOX WALK commits=<n>/<total> notes=<n> elapsed=<s>
//
// at most once a second, and once at the end. The throttle is what makes it a progress
// line rather than a transcript: a fast walk prints exactly one, at the end, and a slow one
// prints one a second until it is done. It exists because a program that takes longer than
// 0.1 s says what it is doing, and the failure this closes ran four minutes in silence.
type walkProgress struct {
	mu      sync.Mutex
	stderr  io.Writer
	start   time.Time
	last    time.Time
	total   int
	done    int
	notes   int
	stop    chan struct{}
	stopped sync.WaitGroup
}

// newWalkProgress starts the ticker. The caller must call finish, which stops it.
func newWalkProgress(stderr io.Writer, total int) *walkProgress {
	p := &walkProgress{stderr: stderr, start: time.Now(), total: total, stop: make(chan struct{})}
	p.last = p.start
	p.stopped.Go(func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-p.stop:
				return
			case <-t.C:
				p.emit(false)
			}
		}
	})
	return p
}

// emit writes one progress line unless a line was written less than a second ago and force
// is false. The lock spans the write so the ticker and the closing line cannot interleave,
// which is what keeps a plain bytes.Buffer stderr safe under a test.
func (p *walkProgress) emit(force bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	if !force && now.Sub(p.last) < time.Second {
		return
	}
	p.last = now
	fmt.Fprintf(p.stderr, "INBOX WALK commits=%d/%d notes=%d elapsed=%s\n",
		p.done, p.total, p.notes, oneline.Field(now.Sub(p.start).Round(time.Millisecond).String()))
}

// advance records how far the walk has got and writes a throttled line, so the note parse
// after the commit walk still shows the commits behind it as done rather than at zero.
func (p *walkProgress) advance(done, notes int) {
	p.mu.Lock()
	p.done, p.notes = done, notes
	p.mu.Unlock()
	p.emit(false)
}

// finish stops the ticker, records what the walk found and writes the closing line. It is
// the one write that always happens, so a walk that never reached a second still says it
// ran.
func (p *walkProgress) finish(done, notes int) {
	close(p.stop)
	p.stopped.Wait()
	p.mu.Lock()
	p.done, p.notes = done, notes
	p.mu.Unlock()
	p.emit(true)
}

// abort stops the ticker without writing the closing line. A since-walk that ends in a
// refusal is ONE line, the refusal, and the narration this run would have printed above it
// is not the answer: the dev contract that every refusal is a single INBOX REFUSED line
// predates the walk, and a run that cannot read the bus has nothing to report about how far
// it got. The ticker is stopped the same way finish stops it so no goroutine is left behind.
func (p *walkProgress) abort() {
	close(p.stop)
	p.stopped.Wait()
}

// printOpenEntries prints an open list in the order it is listed in -- the notes that carry
// something, then what has been heard and still owes an answer, then the bare
// acknowledgements -- and stops after max of them. It returns how many it printed.
//
// Three groups, in that order, because the listing that hid four real notes among the
// receipts is the reason they are separated rather than interleaved by clock. HEARD is
// between them because a note I have already said "heard" to is still owed an answer, and
// the receipt that says so must not make it disappear. Every field comes from the open
// list, so listing a header never opens or classifies the note body.
//
// The cap counts PRINTED entries and not entries considered, so a capped listing is the
// first max of the same order a full one would have printed: the notes first, and the bare
// acknowledgements last, which is the right end to lose.
func printOpenEntries(stdout io.Writer, entries []bus.OpenEntry, max int) (int, error) {
	shown := 0
	for _, group := range []string{"NOTE", "HEARD", "RECEIPT"} {
		for _, e := range entries {
			if e.Kind == bus.OpenUnreadable {
				continue
			}
			token := "NOTE"
			switch {
			case e.Heard:
				token = "HEARD"
			case e.Kind == bus.OpenReceipt:
				token = "RECEIPT"
			}
			if token != group {
				continue
			}
			if shown >= max {
				return shown, nil
			}

			fmt.Fprintf(stdout, "INBOX %s id=%s from=%s %saddr=%s at=%s path=%s: %s\n",
				token, oneline.Field(dash(e.ID)), oneline.Field(dash(e.From)), hostField(e.Host), oneline.Field(dash(e.Addr)),
				oneline.Field(dash(e.Date)), oneline.Field(e.Path), oneline.Escape(e.Subject))
			shown++
		}
	}
	return shown, nil
}

// bodyLimit checks one of the two --bodies budgets. Zero is not "unlimited" and
// over-ceiling is not "as much as you can": either one names the flag, the value given and
// the ceiling, in the INBOX REFUSED shape the rest of this listing's refusals use, and is
// exit 2 -- a bad invocation, like every other pair of numbers this tool will not guess.
func bodyLimit(stderr io.Writer, name string, value, ceiling int64) bool {
	if value < 1 {
		fmt.Fprintf(stderr, "INBOX REFUSED: %s %d is not unlimited; give 1 to %d\n", oneline.Field(name), value, ceiling)
		return false
	}
	if value > ceiling {
		fmt.Fprintf(stderr, "INBOX REFUSED: %s %d is over the ceiling %d; run: nova-bus inbox -h\n", oneline.Field(name), value, ceiling)
		return false
	}
	return true
}

// refuseContinuation is the ONE shape a --after refusal takes, and it always carries the
// same remedy: the same command without --after. Two of the reasons have their own
// sentence because their remedy needs one -- a token that names no item in this range, and
// a cursor another read moved under an open chain -- and everything else says what did not
// match. None of them moves a cursor, writes a file, or leaves a half-listing behind.
func refuseContinuation(stderr io.Writer, err error) int {
	var moved *bus.BodyCursorMismatchError
	switch {
	case errors.As(err, &moved):
		fmt.Fprintf(stderr, "INBOX REFUSED: --after names cursor %s and this reader's cursor is %s; rerun without --after\n",
			oneline.Field(dash(moved.Token)), oneline.Field(dash(moved.Persisted)))
	case errors.Is(err, bus.ErrBodyTokenNoItem):
		fmt.Fprintln(stderr, "INBOX REFUSED: --after <token> names no item in this range; rerun without --after")
	default:
		fmt.Fprintf(stderr, "INBOX REFUSED: --after <token> is not a continuation for this read: %s; rerun without --after\n", oneline.Err(err))
	}
	return 2
}

const (
	defaultBodiesNotes          = 20
	defaultBodiesBytes    int64 = 65536
	maxBodiesNotesCeiling       = 1000
	maxBodiesBytesCeiling int64 = 1048576
)

func printBodyPage(stdout io.Writer, page bus.BodyPage, maxBytes int64) error {
	// Selection, continuation identities, and safe-frontier accounting stay in canonical
	// snapshot order. Display follows the inbox's established NOTE, HEARD, RECEIPT groups;
	// an earlier receipt must not push a later note below the summary groups on this page.
	for _, group := range []string{"NOTE", "HEARD", "RECEIPT"} {
		for _, emission := range page.Emissions {
			if emission.Gap != nil {
				continue
			}
			if emission.Item == nil {
				return fmt.Errorf("body page has an empty emission")
			}
			if bodyDisplayGroup(*emission.Item) != group {
				continue
			}
			if err := printBodyItem(stdout, *emission.Item); err != nil {
				return err
			}
		}
	}
	// Gaps are accounting events rather than listing groups. Preserve every selected gap
	// in canonical order after the grouped listing rows, and retain the same write-error
	// path that prevents a cursor advance when stdout breaks.
	for _, emission := range page.Emissions {
		if emission.Gap == nil {
			continue
		}
		gap := emission.Gap
		if _, err := fmt.Fprintf(stdout, "INBOX BODY OVERSIZE id=%s bytes=%d max-bytes=%d path=%s\n", oneline.Field(dash(gap.ID)), gap.Bytes, maxBytes, oneline.Field(gap.Path)); err != nil {
			return err
		}
	}
	return nil
}

func bodyDisplayGroup(item bus.BodyItem) string {
	e := item.Entry
	if e.Heard {
		return "HEARD"
	}
	if e.Kind == bus.OpenReceipt {
		return "RECEIPT"
	}
	return "NOTE"
}

// hostField is the `host=<name> ` token an inbox line carries when the note named the
// machine it was posted from, and the empty string when it did not. It is written this way
// -- the whole token, blank and all, or nothing -- so a note with no Host line prints the
// line it has always printed, byte for byte, and every parser that reads field 4 of an
// `INBOX NOTE` as `from=` keeps reading it there.
func hostField(host string) string {
	if host == "" {
		return ""
	}
	return "host=" + oneline.Field(host) + " "
}

func printBodyItem(stdout io.Writer, item bus.BodyItem) error {
	e := item.Entry
	kind := bodyDisplayGroup(item)
	if kind != "NOTE" {
		if _, err := fmt.Fprintf(stdout, "INBOX %s id=%s from=%s %saddr=%s at=%s path=%s: %s\n", kind, oneline.Field(dash(e.ID)), oneline.Field(dash(e.From)), hostField(e.Host), oneline.Field(dash(e.Addr)), oneline.Field(dash(e.Date)), oneline.Field(e.Path), oneline.Escape(e.Subject)); err != nil {
			return err
		}
		return nil
	}
	bodyBytes := item.Body
	if _, err := fmt.Fprintf(stdout, "INBOX NOTE id=%s from=%s %saddr=%s at=%s path=%s: %s\n", oneline.Field(dash(e.ID)), oneline.Field(dash(e.From)), hostField(e.Host), oneline.Field(dash(e.Addr)), oneline.Field(dash(e.Date)), oneline.Field(e.Path), oneline.Escape(e.Subject)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stdout, "INBOX BODY id=%s bytes=%d\n", oneline.Field(dash(e.ID)), len(item.Body)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stdout, "%s", bodyBytes); err != nil {
		return err
	}
	if len(item.Body) == 0 || item.Body[len(item.Body)-1] != '\n' {
		if _, err := fmt.Fprint(stdout, "\n"); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(stdout, "INBOX BODY END id=%s\n", oneline.Field(dash(e.ID))); err != nil {
		return err
	}
	return nil
}

// countListable is how many entries printOpenEntries would print with no cap: the whole
// list bar the unreadable entries, which have no header to print a line from and are named
// on their own INBOX UNREADABLE lines instead.
func countListable(entries []bus.OpenEntry) int {
	n := 0
	for _, e := range entries {
		if e.Kind != bus.OpenUnreadable {
			n++
		}
	}
	return n
}

// hiddenWholeWait reports whether a switch-day line is drawn after every moment this call
// could see, so that nothing arriving during it would be listed.
//
// THE TRAP IT NAMES. A line given as a DATE is midnight at that date's start, so a line of
// tomorrow's date -- which is what a reader who wanted "from today" naturally types -- is a
// moment after everything anybody writes today. Every note that arrives during the wait is
// then history: not carried, not listed, counted on INBOX LEGACY and nowhere else. The wait
// would run its whole timeout beside a bus that was answering it, which is the exact shape
// of failure this verb exists to end. So it is reported, and the wait returns instead of
// waiting on a line that hides everything.
func hiddenWholeWait(legacy bus.LegacyLine, horizon time.Time) bool {
	return !legacy.Before.IsZero() && legacy.Before.After(horizon)
}

// hiddenReason is that sentence, with the line the reader is standing behind and the line
// they probably meant: an INSTANT, which is what a switch-day line drawn today has to be.
//
// ONE SENTENCE PER LINE DRAWN FORWARD, and where two verbs could both say it, the shared
// listing says it. A forward-drawn DATE is the shape with a remedy, and inboxListing prints
// that remedy -- INBOX SWITCH, with the whole command in it -- on every listing `wait`
// returns; both WAIT NOTE sites above stand down when it did (inboxReading.SwitchDay). What
// is left for WAIT NOTE is the case the listing has nothing to say about: a line drawn
// forward as an INSTANT, which somebody set to the second on purpose and which no canned
// command can fix for them.
func hiddenReason(legacy bus.LegacyLine, now time.Time) string {
	at := now.UTC().Format(bus.LegacyInstantLayout)
	return fmt.Sprintf("your switch-day line is %s, which is after a note written now (%s), so a note arriving during this wait would be taken as history rather than listed; a line drawn today has to be an INSTANT -- read once with `--full --legacy-before %s --advance` to draw it at this moment, or leave it where it is and wait for what comes after it",
		legacy.Text, at, at)
}

// utcDay is the UTC calendar day a moment falls in, at its start. It is what the first-advance
// guard asks its question against: a bus with notes dated before TODAY has a history somebody
// has to decide about, and a bus whose notes are all from today does not. What the guard then
// SUGGESTS is the instant of the refusal rather than a day, because a day boundary in the
// future is a moment after everything written today; see the site above.
func utcDay(now time.Time) time.Time {
	u := now.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
}

// printSwitchDayNote prints the ONE line this whole change exists to print, and prints
// nothing at all when there is nothing to say.
//
// THE SILENCE IT ENDS. A reader's cursor read `... open=0 legacy=<a date>` and the inbox
// listed nothing, day after day, on a bus that was busy. Every note was there; his own
// switch-day line stood in front of all of them, because a date is midnight at its START
// and that date was tomorrow's. v0.10.1 made the line an instant and wrote the recovery
// down, and a recovery written down is a recovery for whoever goes looking. He had no
// reason to go looking: from where he sat the tool was working and nobody was writing to
// him. THAT is the bug -- not the date, which he was entitled to draw, but a tool that knew
// exactly what was wrong and exactly what to run, and said neither.
//
// So the line is printed on EVERY run whose cursor carries a forward-drawn date line,
// incremental or full, busy or empty, and it carries the whole command with this run's own
// values in it: nothing to work out, nothing to look up, one line to paste. It is a NOTE
// and not a refusal -- exit codes are untouched and nothing moves without the flag. The
// reader runs the command; the tool only names it.
//
// IT HAS A TOKEN OF ITS OWN, and it wanted one. This sentence first went out under
// `INBOX NOTE`, which is already the token for a listed note -- `INBOX NOTE id=<id> ...` --
// and every line parser on this bus reads field 3 of an `INBOX NOTE` as `id=`. A second
// shape under one token is a grammar that cannot be parsed without reading the whole line,
// so the remedy is `INBOX SWITCH` and `INBOX NOTE` keeps its one meaning. It is also the
// better name: it says what the line is about.
//
// The values this run was not given are printed as the placeholders they are, because
// `check` has no --receipt-max-words, --remote or --branch and a run without --advance has
// no remote either. A quoted `"<remote>"` says "your remote goes here" in a slot that is
// otherwise pasteable; inventing `origin` would be a guess, and this tool does not guess.
//
// The names and paths in it are QUOTED rather than field-escaped, exactly as the
// first-advance guard quotes them, because this half of the sentence is meant to be pasted:
// a bus directory holding a blank is `--bus "/a bus/here"` and not `--bus /a\x20bus/here`.
func printSwitchDayNote(stdout io.Writer, legacy, busDir, name, words, remote, branch string, now time.Time) bool {
	drawn, hides, yes := bus.LegacyDateAtOrAfterToday(legacy, now)
	if !yes {
		return false
	}
	fmt.Fprintf(stdout, "INBOX SWITCH your switch-day line is the date %s, which hides every note dated %s or earlier; draw it at an instant, once: nova-bus inbox --bus %s --as %s --receipt-max-words %s --full --legacy-now --advance --remote %s --branch %s\n",
		oneline.Field(drawn.Format(bus.LegacyDateLayout)), oneline.Field(hides.Format(bus.LegacyDateLayout)),
		oneline.Quote(busDir), oneline.Quote(orPlaceholder(name, "<you>")), oneline.Field(words),
		oneline.Quote(orPlaceholder(remote, "<remote>")), oneline.Quote(orPlaceholder(branch, "<branch>")))
	return true
}

// orPlaceholder is a value this run has, or the angle-bracket name of the value it does not.
func orPlaceholder(value, placeholder string) string {
	if value == "" {
		return placeholder
	}
	return value
}

// legacyLine reads a --legacy-before flag: the empty string is no line, and anything that is
// neither a UTC calendar date nor an RFC 3339 UTC instant is exit 2, a bad invocation rather
// than a guess. Both verbs that take the flag read it here, and it reads through
// bus.NewLegacyLine, so the two flags and a cursor cannot accept different spellings of one
// line. The TEXT is kept beside the moment: what a reader typed is what the cursor records
// and what INBOX LEGACY echoes back.
func legacyLine(verb, value string, stderr io.Writer) (bus.LegacyLine, bool) {
	if strings.TrimSpace(value) == "" {
		return bus.LegacyLine{}, true
	}
	line, err := bus.NewLegacyLine(value)
	if err != nil {
		fmt.Fprintf(stderr, "%s REFUSED: --legacy-before %s\n", strings.ToUpper(verb), oneline.WithRemedy(oneline.Err(err), verbHelp(verb)))
		return bus.LegacyLine{}, false
	}
	return line, true
}
