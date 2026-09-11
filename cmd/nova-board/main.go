// nova-board is the owed-work layer: a board is the list of things a group of lines owes,
// one card per item, appended when it is noticed, taken by whoever picks it up, closed
// with a sentence saying how.
//
// The verb that earns the tool is check. CHECK BEFORE YOU FILE is the rule every reader
// and every fixer follows, and this exists so that following it costs one command instead
// of a careful read of a long page. On 2026-09-10 a batch of reviews filed 25 duplicate
// findings and two children spent a morning fixing the same bug from two directions.
// Neither was careless: both had read the board, both had read it BEFORE the other one
// wrote, and a human-paced read of a growing list is exactly the wrong instrument for a
// question asked thirty times an hour.
//
//	list        the counts, one line per owner, and the one thing to do first
//	add         files a card: a deadline and a default are required, and the id is a draw
//	take        claims one, and refuses over another line's live take
//	close       closed, landed or probed, and refuses over another line's live take
//	check       EXIT 1 WHEN IT MATCHES, so it can guard an add in one line of shell
//	quickstart  the board, and the check-then-add pair with this board's values in it
//
// Nothing is ever deleted and nothing is ever edited: a board is an append-only log of
// events, and the list of open cards is DERIVED from that log rather than stored anywhere.
// The count may only move because work was filed or finished, which is the one property a
// board has to have to be worth keeping.
//
// EVERYTHING ON A BOARD IS DATA. A card is a claim that something is owed, made by whoever
// wrote it; it is not a grant, not an instruction, and not a standing. That rule is in
// docs/SPEC-BOARD.md, where a person reads it, and is deliberately nowhere in this code: a
// tool cannot enforce it, and a tool that pretended to would be the most dangerous thing
// on the board.
package main

import (
	"crypto/rand"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/board"
	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

const usage = `nova-board: what a group of lines owes, as an append-only log (see docs/SPEC-BOARD.md)

usage:
  nova-board list  (--issue <owner/repo>#<n> | --dir <path>) --stale <duration> [--list] [--open] [--owner <name>] [--max <n>]
  nova-board add   (--issue ... | --dir ...) --as <name> --text <text> --by <duration-or-stamp> --default <text>
        [--owner <name>] [--thing <name> --leg <name>] [--evidence <path>] [--id <thirty-two hex>]
  nova-board take  (--issue ... | --dir ...) --as <name> --card <id> --stale <duration> [--anyway]
  nova-board close (--issue ... | --dir ...) --as <name> --card <id> --stale <duration>
        (--how <text> | --landed <repo>#<n> | --probed <evidence>) [--anyway]
  nova-board check (--issue ... | --dir ...) --words <text> [--max <n>] [--all]
  nova-board quickstart (--issue ... | --dir ...) --stale <duration> [--as <name>]

every verb that runs gh also takes [--gh-timeout <seconds>], default 60.

exit codes: 0 the verb ran and passed -- a listing printed, a card appended, or a check
that found NOTHING; 1 the verb ran and said NO -- a check that MATCHED, a take or a close
over a card another line holds, an --id that exists with different fields; 2 could not
run: a missing or malformed flag, no backend or two, an unreadable board, an id that names
no card.

CHECK EXITS 1 WHEN IT MATCHES, and that is this tool's most important sentence. The NO a
board owes a filer is "this is already on the board, do not file it", so the rule every
reader and fixer follows is one line of shell:

  nova-board check --dir ./board --words "windows runner skips" || { [ $? -eq 1 ] && exit 0; exit 2; }
  nova-board add   --dir ./board --as rowan --text "the Windows runner skips three steps" \
                   --by 4h --default "rowan files it on the schema board as a known gap"

The guard tells a NO from a could-not-run: check at exit 2 is an operational error, and a
guard that read every non-zero as "already filed" would turn a broken board into a quiet
one. --all makes check report over closed cards too, because "somebody already fixed
this" is as good a reason not to file as "somebody already filed it".

Exactly one backend per invocation, named. --dir <path> is a directory of card files, one
file per card, appended and never committed: landing it is yours. --issue <owner/repo>#<n>
is issue comments, one event per comment, durable when the command returns. Neither is
refusing to guess; both together is two boards with one name.

No default paths and no default durations. --stale wants how long a card may go without an
event before it lists as takeable again; this family's number is 10m and quickstart passes
it in words rather than guessing it. --by wants a duration from now (4h) or an RFC 3339
stamp, and --default wants one line saying what happens if nobody closes the card by then:
every ask has a written deadline and a default action. --max keeps its 20, because it is
neither a path nor a duration. No environment variable configures anything here.

The default view is COUNTS, not cards: one line per owner, one per leg, one BOARD OK and
exactly one BOARD NEXT naming the one thing to do first. Cards print under --list, capped
at --max with one MORE line; --list --owner <name> prints one line's own batch.

A take over another line's LIVE take is refused, and so is a close: that is the
two-children-one-bug failure closed at the only moment a tool can see it. Past --stale the
take is silent rather than live and either is permitted without --anyway. --anyway turns
the refusal into a 0 and records override=true in the event, because a close going over a
live take is a thing the log should say out loud.

There is no edit, no delete, no reopen and no release. A card closed in error is a new card
whose text names the old id; a deadline that has to move is closed "superseded by <id>".

example:
  nova-board quickstart --dir ./board --stale 10m
  nova-board list --dir ./board --stale 10m --list --max 3
  nova-board check --dir ./board --words windows
`

// The hints. Every refusal says what the flag WANTS, not only what was wrong, because a
// refusal naming only the fault has moved the guessing onto the reader.
const (
	backendHint = "no backend: --issue <owner/repo>#<n> for issue comments, or --dir <path> for a directory of card files; refusing to guess"
	twoBackends = "both --issue and --dir were given; a board written to two places is two boards with one name, so name one"
	staleHint   = "--stale is required and wants how long a card may go without an event before it lists as takeable again, as in --stale 10m; this family's number is 10m and there is no default duration; refusing to guess"
	asHint      = "--as is required on every verb that writes and wants the name that goes in the event, as in --as rowan; it is a label the board repeats, never a credential; refusing to guess"
	textHint    = "--text is required and wants one line saying what is owed, as in --text \"the Windows runner skips three steps\"; refusing to guess"
	byHint      = "--by is required and wants a deadline: a duration from now like 4h, or an RFC 3339 stamp like 2026-09-12T09:00:00Z; a card with no deadline cannot be filed"
	defHint     = "--default is required and wants one line saying what happens if nobody closes the card by then, as in --default \"rowan files it as a known gap\"; never wait forever"
	cardHint    = "--card is required and wants the thirty-two hex id of a card on this board, which `nova-board list --list` prints; refusing to guess"
	wordsHint   = "--words is required and wants the words you would file, as in --words \"windows runner skips\"; every word must appear in a card's text for it to match"
	howHint     = "one of --how <text>, --landed <repo>#<n> or --probed <evidence> is required and says HOW this was closed; --landed is the close a reader can verify, --probed is the only close for a row of the owed ledger"
	legHint     = "--thing and --leg go together: a row of the owed ledger is one thing owed on one leg, and half of one is neither"
	idHint      = "--id wants the thirty-two hex id an earlier add printed, and is the retry after an append whose outcome you do not know"
	maxHint     = "--max is a ceiling on printed lines: 0 means all, and a negative one is a typo with two readings"
	remedyMore  = "--max 0 shows all"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, time.Now().UTC(), rand.Reader))
}

// run is the whole tool, with its streams, its clock and its random source injected so the
// tests can drive it. The id is 128 bits from that source and is computed from NOTHING, so
// two adds anywhere at one second with one text get two ids without reading anything first.
func run(args []string, stdout, stderr io.Writer, now time.Time, rnd io.Reader) int {
	if len(args) == 0 {
		return refuse(stderr, "", "no verb given; `list --dir <path> --stale 10m` is the one that only looks")
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	case "list":
		return cmdList(rest, stdout, stderr, now)
	case "add":
		return cmdAdd(rest, stdout, stderr, now, rnd)
	case "take":
		return cmdTake(rest, stdout, stderr, now, rnd)
	case "close":
		return cmdClose(rest, stdout, stderr, now, rnd)
	case "check":
		return cmdCheck(rest, stdout, stderr, now)
	case "quickstart":
		return cmdQuickstart(rest, stdout, stderr, now)
	}
	return refuse(stderr, "", fmt.Sprintf("unknown verb %q", verb))
}

// refuse is what an unusable invocation costs: ONE line naming what was wrong, and the
// door to the usage rather than the usage itself.
func refuse(stderr io.Writer, where, what string) int {
	fmt.Fprintf(stderr, "nova-board%s: %s; run: nova-board help\n", oneline.Escape(where), oneline.Escape(what))
	return 2
}

// ---------------------------------------------------------------------------- the flags

// flags is one verb's flag set, with package flag's two mouths closed: its error text
// quotes the argument it could not parse and its usage dump follows, so an argument
// beginning with a dash could otherwise author a whole line of stderr before any code here
// ran.
type flags struct {
	verb string
	fs   *flag.FlagSet

	issue, dir string
	ghTimeout  int
	problems   []string
}

func newFlags(verb string) *flags {
	fs := flag.NewFlagSet(verb, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	f := &flags{verb: verb, fs: fs}
	fs.StringVar(&f.issue, "issue", "", "")
	fs.StringVar(&f.dir, "dir", "", "")
	fs.IntVar(&f.ghTimeout, "gh-timeout", 60, "")
	return f
}

// want records one independent problem. ONE RUN REPORTS EVERY PROBLEM IT CAN FIND:
// sending a first run back three times for three independent flags is three refusals the
// first one already knew about.
func (f *flags) want(hint string) { f.problems = append(f.problems, hint) }

// need records a hint when the value is empty.
func (f *flags) need(value, hint string) {
	if strings.TrimSpace(value) == "" {
		f.want(hint)
	}
}

// parse runs the flag set. A flag package error is its own single-line refusal, because
// there is nothing else to report about a line that would not parse.
func (f *flags) parse(args []string, stderr io.Writer) bool {
	if err := f.fs.Parse(args); err != nil {
		refuse(stderr, " "+f.verb, oneline.Cap(err.Error(), oneline.TailBytes))
		return false
	}
	if n := f.fs.NArg(); n > 0 {
		f.want(fmt.Sprintf("takes no positional arguments, got %d (flags come before arguments)", n))
	}
	return true
}

// backend names the one backend this invocation works on. Neither is refusing to guess;
// both is two boards with one name.
func (f *flags) backend() (board.Backend, string, string) {
	switch {
	case f.issue == "" && f.dir == "":
		f.want(backendHint)
	case f.issue != "" && f.dir != "":
		f.want(twoBackends)
	case f.dir != "":
		b, err := board.NewDir(f.dir)
		if err != nil {
			f.want(oneline.Err(err))
			return nil, "", ""
		}
		return b, "dir", b.Source()
	default:
		if f.ghTimeout < 1 {
			f.want("--gh-timeout is a number of seconds and is at least 1")
			return nil, "", ""
		}
		b, err := board.NewIssue(f.issue, time.Duration(f.ghTimeout)*time.Second)
		if err != nil {
			f.want(oneline.Err(err))
			return nil, "", ""
		}
		return b, "issue", b.Source()
	}
	return nil, "", ""
}

// duration reads a required duration flag. There are no default durations here: every one
// comes from a flag, because a board found through a default is a board a line writes to
// by accident.
func (f *flags) duration(value, hint string) time.Duration {
	if strings.TrimSpace(value) == "" {
		f.want(hint)
		return 0
	}
	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		f.want(hint)
		return 0
	}
	return d
}

// cap reads the one flag in this tool that has a default, and 0 lifts it.
func (f *flags) cap(max int) int {
	if max < 0 {
		f.want(maxHint)
	}
	return max
}

// refused prints every problem this run found, one line each, each naming the door. A
// single problem therefore costs exactly one line, which is what a missing flag should
// cost.
func (f *flags) refused(stderr io.Writer) int {
	for _, p := range f.problems {
		fmt.Fprintf(stderr, "nova-board %s: %s; run: nova-board help\n", oneline.Field(f.verb), oneline.Escape(p))
	}
	return 2
}

// read folds the whole log. THE READ IS THE WHOLE LOG: there is no window, because a card
// older than a window VANISHES, and a vanished card makes the count fall without any work
// being done.
func read(b board.Backend, source string, now time.Time, stale time.Duration, stderr io.Writer) (*board.Board, bool) {
	log, err := b.Events()
	if err != nil {
		fmt.Fprintf(stderr, "BOARD FAIL %s: %s\n", oneline.Escape(source), oneline.Err(err))
		return nil, false
	}
	return board.Derive(log, now, stale), true
}

// ---------------------------------------------------------------------------- the verbs

func cmdList(args []string, stdout, stderr io.Writer, now time.Time) int {
	f := newFlags("list")
	var staleFlag, owner string
	var cards, open bool
	var max int
	f.fs.StringVar(&staleFlag, "stale", "", "")
	f.fs.StringVar(&owner, "owner", "", "")
	f.fs.BoolVar(&cards, "list", false, "")
	f.fs.BoolVar(&open, "open", false, "")
	f.fs.IntVar(&max, "max", bounded.Default, "")
	if !f.parse(args, stderr) {
		return 2
	}
	backend, kind, source := f.backend()
	stale := f.duration(staleFlag, staleHint)
	max = f.cap(max)
	if len(f.problems) > 0 {
		return f.refused(stderr)
	}
	b, ok := read(backend, source, now, stale, stderr)
	if !ok {
		return 2
	}
	printBoard(stdout, b, kind, source, cards || open || owner != "", open || owner != "", owner, max)
	return 0
}

func cmdAdd(args []string, stdout, stderr io.Writer, now time.Time, rnd io.Reader) int {
	f := newFlags("add")
	var as, text, by, deflt, owner, thing, leg, evidence, id string
	f.fs.StringVar(&as, "as", "", "")
	f.fs.StringVar(&text, "text", "", "")
	f.fs.StringVar(&by, "by", "", "")
	f.fs.StringVar(&deflt, "default", "", "")
	f.fs.StringVar(&owner, "owner", "", "")
	f.fs.StringVar(&thing, "thing", "", "")
	f.fs.StringVar(&leg, "leg", "", "")
	f.fs.StringVar(&evidence, "evidence", "", "")
	f.fs.StringVar(&id, "id", "", "")
	if !f.parse(args, stderr) {
		return 2
	}
	backend, kind, source := f.backend()
	f.need(as, asHint)
	f.need(text, textHint)
	f.need(by, byHint)
	f.need(deflt, defHint)
	if (strings.TrimSpace(thing) == "") != (strings.TrimSpace(leg) == "") {
		f.want(legHint)
	}
	if id != "" && !board.Hex(id, board.IDHex) {
		f.want(idHint)
	}
	deadline, err := deadlineOf(by, now)
	if err != nil && strings.TrimSpace(by) != "" {
		f.want(byHint)
	}
	tail := board.Tail(text)
	if strings.TrimSpace(text) != "" && strings.TrimSpace(tail) == "" {
		f.want("--text is empty once its control characters are escaped; a card a person cannot read is not a card")
	}
	if len(f.problems) > 0 {
		return f.refused(stderr)
	}

	// The fold is read BEFORE the append: for the hash note, and for the --id retry.
	b, ok := read(backend, source, now, time.Duration(0), stderr)
	if !ok {
		return 2
	}
	if owner == "" {
		owner = as
	}
	event := board.Event{
		Verb: "card", As: as, At: now, Hash: board.HashOf(tail), Owner: owner,
		By: deadline, Default: deflt, Thing: thing, Leg: leg, Evidence: evidence, Tail: tail,
	}

	if id != "" {
		// A RETRY AFTER AN UNCERTAIN APPEND REUSES THE ID IT DREW. An existing card whose
		// creation fields are identical is the same filing; one whose fields differ is a
		// refusal, and nothing is written either way.
		if existing := b.Card(id); existing != nil {
			event.ID = id
			if sameCreation(existing, event) {
				fmt.Fprintf(stdout, "ADD OK id=%s owner=%s at=%s backend=%s durable=%s existed=true\n",
					oneline.Field(id), oneline.Field(existing.Owner), oneline.Field(existing.SinceRaw),
					oneline.Field(fileOrIssue(kind)), oneline.Field(durable(kind)))
				return 0
			}
			fmt.Fprintf(stderr, "ADD REFUSED: id %s exists with different fields; nothing written\n", oneline.Field(id))
			return 1
		}
		event.ID = id
	} else {
		drawn, err := board.NewID(rnd)
		if err != nil {
			fmt.Fprintf(stderr, "ADD REFUSED: %s\n", oneline.Err(err))
			return 2
		}
		event.ID = drawn
	}

	// The hash is for a person's eye and never refuses: a silent deduplication is the one
	// thing The races forbids.
	for _, card := range b.Cards {
		if card.Open() && card.Hash == event.Hash {
			fmt.Fprintf(stderr, "ADD NOTE hash=%s matches open card %s owner=%s; filing anyway\n",
				oneline.Field(event.Hash), oneline.Field(card.ID), oneline.Field(card.Owner))
			break
		}
	}

	if err := backend.Append(event.Render()); err != nil {
		if err == board.ErrExists {
			fmt.Fprintf(stderr, "ADD REFUSED: id %s exists; nothing written\n", oneline.Field(event.ID))
			return 1
		}
		fmt.Fprintf(stderr, "ADD REFUSED: %s\n", oneline.Err(err))
		return 1
	}
	fmt.Fprintf(stdout, "ADD OK id=%s owner=%s at=%s backend=%s durable=%s existed=false\n",
		oneline.Field(event.ID), oneline.Field(owner), oneline.Field(board.Stamp(now)),
		oneline.Field(fileOrIssue(kind)), oneline.Field(durable(kind)))
	return 0
}

func cmdTake(args []string, stdout, stderr io.Writer, now time.Time, rnd io.Reader) int {
	f := newFlags("take")
	var as, card, staleFlag string
	var anyway bool
	f.fs.StringVar(&as, "as", "", "")
	f.fs.StringVar(&card, "card", "", "")
	f.fs.StringVar(&staleFlag, "stale", "", "")
	f.fs.BoolVar(&anyway, "anyway", false, "")
	if !f.parse(args, stderr) {
		return 2
	}
	backend, _, source := f.backend()
	f.need(as, asHint)
	f.need(card, cardHint)
	stale := f.duration(staleFlag, staleHint)
	if len(f.problems) > 0 {
		return f.refused(stderr)
	}
	b, target, code := find(backend, source, card, now, stale, stderr)
	if b == nil {
		return code
	}
	// A TAKE OF A FRESH TAKE BY ANOTHER LINE IS REFUSED: that is the 2026-09-10
	// two-children-one-bug failure, closed at the only moment a tool can see it. Past
	// --stale the take is silent rather than live and this is permitted without --anyway.
	previous := "-"
	if target.HasTake {
		previous = target.Owner
	}
	if !target.Open() {
		fmt.Fprintf(stderr, "TAKE REFUSED: %s is CLOSED (by %s at %s); there is no reopen — a card closed in error is a NEW card whose text names this id\n",
			oneline.Field(card), oneline.Field(closedBy(target)), oneline.Field(closedAt(target)))
		return 1
	}
	if target.HasTake && target.Owner != as && !target.Stale && !anyway {
		fmt.Fprintf(stderr, "TAKE REFUSED: %s is held by %s, taken %s ago and not yet stale at %s; --anyway takes it and says so in the log\n",
			oneline.Field(card), oneline.Field(target.Owner), oneline.Field(board.Dur(now.Sub(target.TakenAt))), oneline.Field(stale.String()))
		return 1
	}
	override := anyway && target.HasTake && target.Owner != as && !target.Stale
	if code := appendEvent(backend, target, board.Event{Verb: "taken", ID: card, As: as, At: now, Override: override}, rnd, stderr, "TAKE"); code != 0 {
		return code
	}
	fmt.Fprintf(stdout, "TAKE OK id=%s owner=%s at=%s previous=%s override=%s\n",
		oneline.Field(card), oneline.Field(as), oneline.Field(board.Stamp(now)),
		oneline.Field(previous), oneline.Field(yesNo(override)))
	return 0
}

func cmdClose(args []string, stdout, stderr io.Writer, now time.Time, rnd io.Reader) int {
	f := newFlags("close")
	var as, card, staleFlag, how, landed, probed string
	var anyway bool
	f.fs.StringVar(&as, "as", "", "")
	f.fs.StringVar(&card, "card", "", "")
	f.fs.StringVar(&staleFlag, "stale", "", "")
	f.fs.StringVar(&how, "how", "", "")
	f.fs.StringVar(&landed, "landed", "", "")
	f.fs.StringVar(&probed, "probed", "", "")
	f.fs.BoolVar(&anyway, "anyway", false, "")
	if !f.parse(args, stderr) {
		return 2
	}
	backend, _, source := f.backend()
	f.need(as, asHint)
	f.need(card, cardHint)
	stale := f.duration(staleFlag, staleHint)
	given := 0
	for _, v := range []string{how, landed, probed} {
		if strings.TrimSpace(v) != "" {
			given++
		}
	}
	if given != 1 {
		f.want(howHint)
	}
	if len(f.problems) > 0 {
		return f.refused(stderr)
	}
	b, target, code := find(backend, source, card, now, stale, stderr)
	if b == nil {
		return code
	}
	event := board.Event{ID: card, As: as, At: now}
	where := "-"
	switch {
	case landed != "":
		event.Verb, event.In, where = "landed", landed, landed
	case probed != "":
		event.Verb, event.Tail = "probed", board.Tail(probed)
		where = probed
	default:
		event.Verb, event.Tail = "closed", board.Tail(how)
	}
	// A ROW IS CLOSED ONLY BY probed, because a row that was not probed was not done.
	if target.Row && event.Verb != "probed" {
		fmt.Fprintf(stderr, "CLOSE REFUSED: %s is a row of the owed ledger (thing=%s leg=%s) and a row is closed only by --probed <evidence>; a row that was not probed was not done\n",
			oneline.Field(card), oneline.Field(target.Thing), oneline.Field(target.Leg))
		return 1
	}
	// A CLOSE OF A CARD SOMEBODY JUST TOOK, closed by reading at write time.
	if target.Open() && target.HasTake && target.Owner != as && !target.Stale && !anyway {
		fmt.Fprintf(stderr, "CLOSE REFUSED: %s is held by %s, taken %s ago and not yet stale at %s; --anyway closes it and records override=true\n",
			oneline.Field(card), oneline.Field(target.Owner), oneline.Field(board.Dur(now.Sub(target.TakenAt))), oneline.Field(stale.String()))
		return 1
	}
	event.Override = anyway && target.Open() && target.HasTake && target.Owner != as && !target.Stale
	if code := appendEvent(backend, target, event, rnd, stderr, "CLOSE"); code != 0 {
		return code
	}
	owner := "-"
	if target.HasTake {
		owner = target.Owner
	}
	fmt.Fprintf(stdout, "CLOSE OK id=%s how=%s where=%s at=%s owner=%s override=%s\n",
		oneline.Field(card), oneline.Field(event.Verb), oneline.Field(where),
		oneline.Field(board.Stamp(now)), oneline.Field(owner), oneline.Field(yesNo(event.Override)))
	return 0
}

func cmdQuickstart(args []string, stdout, stderr io.Writer, now time.Time) int {
	f := newFlags("quickstart")
	var staleFlag, as string
	f.fs.StringVar(&staleFlag, "stale", "", "")
	f.fs.StringVar(&as, "as", "", "")
	if !f.parse(args, stderr) {
		return 2
	}
	backend, kind, source := f.backend()
	stale := f.duration(staleFlag, staleHint)
	if len(f.problems) > 0 {
		return f.refused(stderr)
	}
	b, ok := read(backend, source, now, stale, stderr)
	if !ok {
		return 2
	}
	if as == "" {
		as = "your-name"
	}
	fmt.Fprintf(stdout, "QUICKSTART OK backend=%s source=%s stale=%s: the board, then the rule every filer runs in front of add\n",
		oneline.Field(kind), oneline.Field(source), oneline.Field(stale.String()))
	printBoard(stdout, b, kind, source, false, false, "", bounded.Default)
	words, text := quickstartWords(b)
	fmt.Fprintf(stdout, "QUICKSTART LINE n=1 what=check: %s\n", oneline.Quote(
		"nova-board check "+backendFlag(kind, source)+" --words "+quote(words)+
			" || { [ $? -eq 1 ] && exit 0; exit 2; }"))
	fmt.Fprintf(stdout, "QUICKSTART LINE n=2 what=add: %s\n", oneline.Quote(
		"nova-board add "+backendFlag(kind, source)+" --as "+as+" --text "+quote(text)+
			" --by 4h --default "+quote("the filer files it as a known gap")))
	fmt.Fprintf(stdout, "QUICKSTART NOTE check EXITS 1 WHEN IT MATCHES, so the guard reads \"if it is already there, stop\"; the exit-2 arm tells a NO from a board that could not be read\n")
	fmt.Fprintf(stdout, "QUICKSTART NOTE --stale %s is this family's number and this run passed it in words: there is no default duration here, and --by and --default are required on every card\n",
		oneline.Field(stale.String()))
	return 0
}

// ------------------------------------------------------------------------- the printing

// printBoard is the default view and the listing under it. RULE 1: COUNTS, NOT LISTS. A
// listing is a context window spent on the good news, so the default prints one line per
// owner, one per leg, one BOARD NEXT and one BOARD OK — and cards only when asked.
func printBoard(stdout io.Writer, b *board.Board, kind, source string, cards, openOnly bool, owner string, max int) {
	shown := 0
	if cards {
		var wanted []*board.Card
		for _, c := range b.Cards {
			if openOnly && !c.Open() {
				continue
			}
			if owner != "" && c.Owner != owner {
				continue
			}
			wanted = append(wanted, c)
		}
		// The cap counts CARDS. A closed card's two facts live on two lines, and the
		// second one rides with the first: it prints when its card printed, so the cap is
		// a prefix of the cards and never half of one card's pair.
		elided := 0
		if max > 0 && len(wanted) > max {
			elided = len(wanted) - max
		}
		list := bounded.Capped(stdout, max, "BOARD", "card",
			fmt.Sprintf("and %d more; --max 0 shows all, or --owner <name> for one line's own batch", elided))
		closes := bounded.Capped(stdout, 0, "BOARD", "close", remedyMore)
		for _, c := range wanted {
			before := list.Shown()
			list.Line(cardLine(c))
			if list.Shown() > before && !openOnly && c.Close != nil {
				closes.Line(closeLine(c))
			}
		}
		list.More()
		shown += list.Shown() + closes.Shown()
		if elided > 0 {
			shown++
		}
	}
	var lineRows []string
	for _, l := range b.Lines() {
		lineRows = append(lineRows, fmt.Sprintf("BOARD LINE name=%s open=%d overdue=%d stale=%d",
			oneline.Field(l.Name), l.Open, l.Overdue, l.Stale))
	}
	shown += capped(stdout, max, "line", lineRows, remedyMore)
	var legRows []string
	for _, l := range b.Legs() {
		legRows = append(legRows, fmt.Sprintf("BOARD LEG leg=%s owed=%d probed=%d",
			oneline.Field(l.Name), l.Owed, l.Probed))
	}
	shown += capped(stdout, max, "leg", legRows, remedyMore)

	fmt.Fprintf(stdout, "BOARD NEXT %s\n", oneline.Escape(nextLine(b)))
	shown++
	for _, note := range notes(b) {
		fmt.Fprintf(stdout, "BOARD NOTE %s\n", oneline.Escape(note))
		shown++
	}
	c := b.Counts()
	// THE COUNTS PRINT ON FAILURE AS WELL AS SUCCESS and they are the truth about the
	// BOARD, not about the output: the listing is capped, the counting never is.
	fmt.Fprintf(stdout, "BOARD OK cards=%d open=%d closed=%d stale=%d overdue=%d owed=%d lines=%d conflicts=%d quarantined=%d shown=%d backend=%s source=%s\n",
		c.Cards, c.Open, c.Closed, c.Stale, c.Overdue, c.Owed, c.Lines, c.Conflicts, c.Quarantined,
		shown, oneline.Field(kind), oneline.Field(source))
}

// capped prints a listing through internal/bounded, with the remedy naming how many were
// not printed. The lines are built first so that "and <t-n> more" can be true.
func capped(w io.Writer, max int, kind string, lines []string, remedy string) int {
	if len(lines) == 0 {
		return 0
	}
	elided := 0
	if max > 0 && len(lines) > max {
		elided = len(lines) - max
	}
	list := bounded.Capped(w, max, "BOARD", kind, fmt.Sprintf("and %d more; %s", elided, remedy))
	for _, l := range lines {
		list.Line(l)
	}
	list.More()
	if elided > 0 {
		return list.Shown() + 1
	}
	return list.Shown()
}

func cardLine(c *board.Card) string {
	return fmt.Sprintf("BOARD CARD id=%s state=%s owner=%s since=%s by=%s age=%s taken=%s stale=%s overdue=%s conflicts=%d conflict=%s quarantined=%d default=%s thing=%s leg=%s evidence=%s: %s",
		oneline.Field(c.ID), oneline.Field(c.State), oneline.Field(dash(c.Owner)), oneline.Field(c.SinceRaw),
		oneline.Field(dash(c.By)), oneline.Field(c.Age), oneline.Field(c.Taken), oneline.Field(yesNo(c.Stale)),
		oneline.Field(yesNo(c.Overdue)), c.Conflicts, oneline.Field(yesNo(c.Conflict)), c.Quarantined,
		oneline.Field(c.Default), oneline.Field(dash(c.Thing)), oneline.Field(dash(c.Leg)),
		oneline.Field(dash(c.Evidence)), oneline.Escape(c.Text))
}

// closeLine is the second of the two facts a closed card carries. The prototype printed
// `CLOSED/#942` as ONE field, which is a state and a location joined by a slash and
// unparseable by the scanner it was written for.
func closeLine(c *board.Card) string {
	where := "-"
	tail := c.Close.Tail
	if c.Close.Verb == "landed" {
		where, tail = c.Close.In, c.Close.In
	}
	return fmt.Sprintf("BOARD CLOSE id=%s by=%s at=%s how=%s override=%s where=%s: %s",
		oneline.Field(c.ID), oneline.Field(c.Close.As), oneline.Field(c.Close.AtRaw),
		oneline.Field(c.Close.Verb), oneline.Field(yesNo(c.Close.Override)),
		oneline.Field(where), oneline.Escape(tail))
}

// nextLine is the one thing to do first, by rule 1's fixed order.
func nextLine(b *board.Board) string {
	kind, card, leg := b.Next()
	switch kind {
	case "overdue":
		return "the oldest OVERDUE card " + card.ID + ", owed by " + card.Owner + ", due " + card.By + " -- " + card.Text
	case "stale":
		return "the oldest STALE card " + card.ID + ", last touched by " + card.Owner + " -- " + card.Text
	case "leg":
		return "the leg with the most owed rows, " + leg + " -- probe a row on it and the leg's owed count falls"
	case "open":
		return "the oldest open card " + card.ID + ", owed by " + card.Owner + " -- " + card.Text
	}
	return "nothing owed"
}

// notes are the things true about this board that are not cards. Nothing is guessed at:
// an event in nearly the right shape is counted and reported, never silently treated as a
// close.
func notes(b *board.Board) []string {
	var out []string
	if b.Unparsed > 0 {
		out = append(out, fmt.Sprintf("unparsed events=%d -- counted, never guessed at: a line in nearly the right shape is not an event", b.Unparsed))
	}
	if b.UnparsedFiles > 0 {
		out = append(out, fmt.Sprintf("unparsed files=%d -- a file in the board directory that is not <thirty-two hex>.board is never read as a card", b.UnparsedFiles))
	}
	if b.Quarantined > 0 {
		out = append(out, fmt.Sprintf("quarantined events=%d first=%s -- an after= naming no event of its card, or one on a cycle: not folded and not guessed at", b.Quarantined, oneline.Field(b.FirstQuarantined)))
	}
	return out
}

// ------------------------------------------------------------------------------ helpers

// find reads the board and names the card, or refuses at exit 2: an id that names no card
// is a bad invocation and not a board that said no.
func find(backend board.Backend, source, id string, now time.Time, stale time.Duration, stderr io.Writer) (*board.Board, *board.Card, int) {
	if !board.Hex(id, board.IDHex) {
		return nil, nil, refuse(stderr, "", cardHint)
	}
	b, ok := read(backend, source, now, stale, stderr)
	if !ok {
		return nil, nil, 2
	}
	card := b.Card(id)
	if card == nil {
		fmt.Fprintf(stderr, "nova-board: no card %s on %s; `nova-board list --list` prints the ids this board holds; run: nova-board help\n",
			oneline.Field(id), oneline.Field(source))
		return nil, nil, 2
	}
	return b, card, 0
}

// appendEvent draws the event's own id, names what its writer saw, and appends. A writer
// never names an after= its own fold does not hold.
func appendEvent(backend board.Backend, card *board.Card, event board.Event, rnd io.Reader, stderr io.Writer, token string) int {
	ev, err := board.NewEv(rnd)
	if err != nil {
		fmt.Fprintf(stderr, "%s REFUSED: %s\n", oneline.Field(token), oneline.Err(err))
		return 2
	}
	event.Ev, event.After = ev, card.After()
	if !card.Holds(event.After) {
		fmt.Fprintf(stderr, "%s REFUSED: this fold does not hold the event %s it would name as after=; nothing written\n",
			oneline.Field(token), oneline.Field(event.After))
		return 1
	}
	if err := backend.Append(event.Render()); err != nil {
		fmt.Fprintf(stderr, "%s REFUSED: %s\n", oneline.Field(token), oneline.Err(err))
		return 1
	}
	return 0
}

// deadlineOf reads --by: a duration from now, or an RFC 3339 stamp STORED AS GIVEN. A
// deadline is a fact about the work rather than about when the tool wrote, and it is never
// used as `since`.
func deadlineOf(by string, now time.Time) (string, error) {
	by = strings.TrimSpace(by)
	if by == "" {
		return "", fmt.Errorf("empty")
	}
	if d, err := time.ParseDuration(by); err == nil {
		if d <= 0 {
			return "", fmt.Errorf("a deadline in the past is not a deadline")
		}
		return board.Stamp(now.Add(d)), nil
	}
	if _, err := time.Parse(time.RFC3339, by); err == nil {
		return by, nil
	}
	return "", fmt.Errorf("neither a duration nor an RFC 3339 stamp")
}

// sameCreation compares everything on a card line but the stamp.
func sameCreation(existing *board.Card, event board.Event) bool {
	was := board.Event{
		As: existing.Filer, Hash: existing.Hash, Owner: existing.Owner, By: existing.By,
		Default: existing.Default, Thing: existing.Thing, Leg: existing.Leg,
		Evidence: existing.Evidence, Tail: existing.Text,
	}
	rendered := board.Event{
		As: oneline.Field(event.As), Hash: event.Hash, Owner: oneline.Field(event.Owner),
		By: oneline.Field(event.By), Default: oneline.Field(event.Default),
		Thing: oneline.Field(event.Thing), Leg: oneline.Field(event.Leg),
		Evidence: oneline.Field(event.Evidence), Tail: event.Tail,
	}
	return was.CreationFields() == rendered.CreationFields()
}

// quickstartWords picks the words and the text the printed pair carries: this board's own
// values, so the line is one a reader can paste rather than a sketch.
func quickstartWords(b *board.Board) (words, text string) {
	for _, c := range b.Cards {
		if c.Open() {
			fields := strings.Fields(c.Text)
			if len(fields) > 3 {
				fields = fields[:3]
			}
			return strings.Join(fields, " "), c.Text
		}
	}
	return "the thing you are about to file", "the thing you are about to file"
}

func backendFlag(kind, source string) string {
	if kind == "issue" {
		return "--issue " + source
	}
	return "--dir " + source
}

// quote wraps a value for the shell lines quickstart prints, which are meant to be pasted.
func quote(s string) string { return "\"" + strings.ReplaceAll(s, "\"", "\\\"") + "\"" }

func fileOrIssue(kind string) string {
	if kind == "issue" {
		return "issue"
	}
	return "file"
}

// durable says when the card is durable, and it is the one asymmetry between the backends:
// an add --issue is durable when the command returns, an add --dir when the caller lands it.
func durable(kind string) string {
	if kind == "issue" {
		return "true"
	}
	return "false"
}

func yesNo(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func closedBy(c *board.Card) string {
	if c.Close == nil {
		return "-"
	}
	return c.Close.As
}

func closedAt(c *board.Card) string {
	if c.Close == nil {
		return "-"
	}
	return c.Close.AtRaw
}
