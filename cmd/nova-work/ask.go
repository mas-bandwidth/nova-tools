package main

// ask and asks are the machinery's side of the rule of the day (Glenn, 2026-09-18):
// THE MACHINERY ROUTES TO FRIENDS. A unit whose owner is a friend is not cut as a
// Flash card and dropped in a bench queue; it is delivered as a bus ask, and the
// friend pulls it over the bus the way a bench pulls cards.
//
// `ask` turns ONE unit of a work set into ONE note in the house shape and sends it
// through nova-bus's own send path, then records the bus's note id back on the unit.
// `asks` reads those records and says what is still out, how long it has been out,
// and what is past its deadline. Between them there is no state anywhere else: the
// work set is the record, and both verbs are ordinary file reads and one flagged
// subprocess.
//
// Every path comes from a flag. There is no default bus, no default work set, no
// default deadline and no discovery from the working directory.

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/friends"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// errSendRefused is what a sender answers when the bus would not take the note. It
// is named here so a test can hand it to a fake sender and watch nothing be recorded.
var errSendRefused = errors.New("the bus refused the note")

// askMaxRows is how many rows `asks` prints before one MORE line. Bounded output by
// design: a coordinator wants the oldest asks and a count, never a list that scrolls.
const askMaxRows = 40

// cmdAsk is the verb; askWith is the same verb with the send seam handed in, which is
// what the tests drive. A nil sender means the real one, built from the flags.
func cmdAsk(args []string, stdout, stderr io.Writer) int {
	return askWith(args, stdout, stderr, nil)
}

func askWith(args []string, stdout, stderr io.Writer, sender friends.Sender) int {
	fs := flag.NewFlagSet("ask", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	owner := fs.String("owner", "", "the friend this unit belongs to (required)")
	unit := fs.String("unit", "", "the unit's id in --units (required)")
	units := fs.String("units", "", "the work set holding the unit, JSON or SPEC-WORKLANG (required)")
	record := fs.String("record", "", "a JSON file to record the ask in; a SPEC-WORKLANG work set is never written back")
	deadline := fs.String("deadline", "", "when the answer is owed, RFC3339; the unit's own :deadline when absent")
	busDir := fs.String("bus", "", "the bus checkout the note is sent on (required)")
	as := fs.String("as", "", "which participant you send as (required)")
	kind := fs.String("kind", "work", "what the ask is: work or read")
	cc := fs.String("cc", "", "who else the note goes to")
	replyBranch := fs.String("reply-branch", "", "the branch the owner replies on; default the unit's own")
	remote := fs.String("remote", "origin", "the git remote nova-bus pushes to")
	branch := fs.String("branch", "main", "the branch the bus lives on, as nova-bus send spells it")
	novaBus := fs.String("nova-bus", "nova-bus", "the nova-bus binary that carries the note")
	attempts := fs.Int("attempts", 0, "how many times nova-bus pushes before giving up; 0 is its own default")
	timeout := fs.Duration("timeout", 5*time.Minute, "how long one nova-bus send may take")
	maxBytes := fs.Int64("max-bytes", friends.DefaultMaxBytes, "the work set's byte ceiling")
	now := fs.String("now", "", "the instant to measure the deadline against, RFC3339; default this run's clock")
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, " ask", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if fs.NArg() > 0 {
		return refuse(stderr, " ask", fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	for _, need := range []struct{ name, value string }{
		{"owner", *owner}, {"unit", *unit}, {"units", *units},
		{"bus", *busDir}, {"as", *as},
	} {
		if strings.TrimSpace(need.value) == "" {
			return refuse(stderr, " ask", "--"+need.name+" is required; refusing to guess")
		}
	}
	if err := friends.ValidKind(*kind); err != nil {
		return refuse(stderr, " ask", err.Error())
	}
	at, ok := askNow(*now)
	if !ok {
		return refuse(stderr, " ask", fmt.Sprintf("--now %q is not an RFC3339 instant", *now))
	}
	var err2 error
	var due time.Time
	if strings.TrimSpace(*deadline) != "" {
		if due, err2 = friends.ParseStamp(*deadline); err2 != nil {
			return refuse(stderr, " ask", fmt.Sprintf("--deadline %q is not an instant", *deadline))
		}
	}

	ws, err := friends.Load(*units, *maxBytes)
	if err != nil {
		return refuse(stderr, " ask", oneline.Err(err))
	}
	u, err := ws.Unit(*unit)
	if err != nil {
		return refuse(stderr, " ask", oneline.Err(err))
	}
	// The unit's OWN :deadline stands when the command line names none. That is not a
	// default: it is the deadline a coordinator already wrote down in the work set, and
	// a unit with neither is still refused rather than given one.
	if due.IsZero() {
		due = u.Deadline.Time
	}
	if due.IsZero() {
		return refuse(stderr, " ask", fmt.Sprintf("neither --deadline nor unit %q carries a deadline; refusing to guess one", *unit))
	}
	if !due.After(at) {
		return refuse(stderr, " ask", fmt.Sprintf("the deadline %s is not after %s; an ask that is late before it is sent is not an ask",
			oneline.Field(due.UTC().Format(time.RFC3339)), oneline.Field(at.UTC().Format(time.RFC3339))))
	}
	reply := *replyBranch
	if reply == "" {
		reply = u.Branch
	}
	note, notices, err := friends.Render(friends.AskSpec{
		From: *as, Owner: *owner, Cc: *cc, Kind: *kind, Branch: reply, Unit: *u, Deadline: due,
	})
	if err != nil {
		return refuse(stderr, " ask", oneline.Err(err))
	}
	// What this run did that a reader of the note could not otherwise check, one line
	// each, before anything is sent -- nova-bus's own SEND NOTE discipline.
	for _, notice := range notices {
		fmt.Fprintf(stderr, "ASK NOTE %s\n", oneline.Escape(notice))
	}
	if sender == nil {
		sender = friends.BusSender{
			Bin: *novaBus, Bus: *busDir, As: *as,
			Remote: *remote, Branch: *branch, Attempts: *attempts, Timeout: *timeout,
		}
	}
	// The send is a push against somebody else's server and is the one slow step here,
	// so it says what it is doing before it starts rather than after it has finished.
	fmt.Fprintf(stderr, "nova-work ask: sending unit %s to %s over the bus at %s\n",
		oneline.Field(*unit), oneline.Field(*owner), oneline.Field(sender.Where()))

	id, err := sender.Send(note)
	if err != nil {
		fmt.Fprintf(stderr, "ASK FAIL owner=%s unit=%s: %s\n", oneline.Field(*owner), oneline.Field(*unit), oneline.Err(err))
		return 1
	}
	// Recorded only now. An ask written down for a note that never landed is a
	// coordinator waiting on a deadline its owner was never given.
	a := friends.Ask{ID: id, Owner: *owner, Kind: *kind, Unit: *unit, Lane: u.Lane,
		Sent: at.UTC(), Deadline: due.UTC(), Branch: reply, By: *as, Bus: *busDir}
	// WHERE THE RECORD GOES. A JSON work set is this package's own file and the ask is
	// written back onto its unit. A SPEC-WORKLANG work set is a PERSON'S document --
	// comments, order, keys no reader here knows -- and is never rewritten: the ask goes
	// to --record when one is named, and otherwise the bus note is the record, which
	// `asks --bus --as` reads back.
	into, ws2 := *units, ws
	if ws.Lisp {
		into = *record
		ws2 = &friends.WorkSet{Units: []friends.Unit{{ID: *unit, Title: u.Title, Owner: u.Owner, Lane: u.Lane}}}
		if into != "" {
			if have, err := friends.Load(into, *maxBytes); err == nil {
				if _, err := have.Unit(*unit); err == nil {
					ws2 = have
				} else {
					have.Units = append(have.Units, ws2.Units[0])
					ws2 = have
				}
			}
		}
	}
	switch {
	case into == "":
		fmt.Fprintf(stderr, "ASK NOTE %s\n", oneline.Escape(
			"the work set is SPEC-WORKLANG and is never written back; the bus note is the record, read it with: nova-work asks --bus <dir> --as "+*as))
	default:
		if err := ws2.Record(*unit, a); err != nil {
			fmt.Fprintf(stderr, "ASK FAIL id=%s unit=%s: %s\n", oneline.Field(id), oneline.Field(*unit), oneline.Err(err))
			return 1
		}
		if err := friends.Save(into, ws2); err != nil {
			fmt.Fprintf(stderr, "ASK FAIL id=%s units=%s: the note landed and could NOT be recorded: %s\n",
				oneline.Field(id), oneline.Field(into), oneline.Err(err))
			return 1
		}
	}
	fmt.Fprintf(stdout, "ASK OK id=%s owner=%s unit=%s kind=%s lane=%s deadline=%s branch=%s record=%s\n",
		oneline.Field(id), oneline.Field(*owner), oneline.Field(*unit), oneline.Field(*kind),
		oneline.Field(dash(u.Lane)), oneline.Field(due.UTC().Format(time.RFC3339)),
		oneline.Field(dash(reply)), oneline.Field(dash(into)))
	return 0
}

// cmdAsks is the other half: what is out, how long it has been out, and what is late.
func cmdAsks(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("asks", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	units := fs.String("units", "", "a work set whose units record asks; either form")
	owner := fs.String("owner", "", "show only the asks of this friend")
	as := fs.String("as", "", "the name you read the bus as; with --bus, whose asks these are")
	busDir := fs.String("bus", "", "a bus checkout to read the sent notes from, the source of truth for what went out")
	max := fs.Int("max", askMaxRows, "rows before one MORE line; 0 is every row")
	maxNotes := fs.Int("max-notes", 0, "note files read per lane with --bus, newest first; 0 is every note")
	maxBytes := fs.Int64("max-bytes", friends.DefaultMaxBytes, "the work set's byte ceiling")
	now := fs.String("now", "", "the instant ages and deadlines are measured against, RFC3339; default this run's clock")
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, " asks", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if fs.NArg() > 0 {
		return refuse(stderr, " asks", fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	// TWO SOURCES, either or both. --units is what this tool recorded; --bus is what the
	// bus itself holds, which is what actually went out. A run with neither has nothing
	// to read and is refused naming both rather than printing an empty count.
	if strings.TrimSpace(*units) == "" && strings.TrimSpace(*busDir) == "" {
		return refuse(stderr, " asks", "name a source: --units <file> (what was recorded), --bus <dir> --as <name> (what the bus holds), or both; refusing to guess")
	}
	if strings.TrimSpace(*busDir) != "" && strings.TrimSpace(*as) == "" {
		return refuse(stderr, " asks", "--bus needs --as: there is no flag that reads the bus as somebody else")
	}
	if *max < 0 {
		return refuse(stderr, " asks", fmt.Sprintf("--max %d is not a count", *max))
	}
	if *maxNotes < 0 {
		return refuse(stderr, " asks", fmt.Sprintf("--max-notes %d is not a count", *maxNotes))
	}
	at, ok := askNow(*now)
	if !ok {
		return refuse(stderr, " asks", fmt.Sprintf("--now %q is not an RFC3339 instant", *now))
	}
	var rows []friends.Row
	if strings.TrimSpace(*units) != "" {
		ws, err := friends.Load(*units, *maxBytes)
		if err != nil {
			return refuse(stderr, " asks", oneline.Err(err))
		}
		rows = ws.Open(at)
	}
	if strings.TrimSpace(*busDir) != "" {
		// --max is the ROW bound and is NOT passed here. It used to be, and it capped the
		// files read instead: on a lane of 1,734 notes the default read the oldest fifty,
		// found nothing open among them and printed `ASKS n=0` at a bench with four asks
		// outstanding. The file bound is --max-notes, it is off by default, and when a
		// caller does set it the run says on stderr which lane it bit and what to raise.
		onBus, bounded, err := friends.OnBus(*busDir, *as, *owner, at, *maxNotes)
		if err != nil {
			return refuse(stderr, " asks", oneline.Err(err))
		}
		for _, b := range bounded {
			fmt.Fprintf(stderr, "ASKS BOUNDED lane=%s notes=%d read=%d remedy=%q\n",
				oneline.Field(b.Lane), b.Notes, b.Read, "raise --max-notes or drop it to read every note")
		}
		// The bus wins a tie: an ask recorded in a work set AND found on the bus is one
		// ask, and the note is the thing that went out.
		seen := map[string]bool{}
		for _, r := range onBus {
			seen[r.ID] = true
		}
		kept := onBus
		for _, r := range rows {
			if !seen[r.ID] {
				kept = append(kept, r)
			}
		}
		rows = kept
		friends.SortOldestFirst(rows)
	}

	shown, open, overdue := 0, 0, 0
	for _, r := range rows {
		if *owner != "" && r.Owner != *owner {
			continue
		}
		if *as != "" && r.By != *as {
			continue
		}
		state := "open"
		if r.Overdue {
			state = "overdue"
			overdue++
		} else {
			open++
		}
		if *max == 0 || shown < *max {
			fmt.Fprintf(stdout, "ASK id=%s owner=%s unit=%s kind=%s lane=%s age=%s deadline=%s state=%s src=%s\n",
				oneline.Field(r.ID), oneline.Field(r.Owner), oneline.Field(dash(r.Unit)), oneline.Field(r.Kind),
				oneline.Field(dash(r.Lane)), oneline.Field(askAge(r.Age)), oneline.Field(askStamp(r.Deadline)),
				oneline.Field(state), oneline.Field(dash(r.Source)))
		}
		shown++
	}
	total := open + overdue
	if *max != 0 && shown > *max {
		fmt.Fprintf(stdout, "MORE %d\n", shown-*max)
	}
	fmt.Fprintf(stdout, "ASKS n=%d open=%d overdue=%d\n", total, open, overdue)
	return 0
}

// askNow is this run's clock, or the instant a caller named so a test and a replay
// measure the same ages. An unparsable --now is a refusal, never a silent fallback
// to the wall clock: a age measured against the wrong instant reads as a fact.
func askNow(spelled string) (time.Time, bool) {
	if strings.TrimSpace(spelled) == "" {
		return time.Now().UTC(), true
	}
	t, err := time.Parse(time.RFC3339, spelled)
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

// askAge is a duration a person reads at a glance: 30m, 3h, 3h20m, 2d4h. Seconds are
// dropped because no ask is decided by them.
func askAge(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	mins := int(d.Round(time.Minute) / time.Minute)
	days, rem := mins/(24*60), mins%(24*60)
	hours, m := rem/60, rem%60
	switch {
	case days > 0 && hours > 0:
		return fmt.Sprintf("%dd%dh", days, hours)
	case days > 0:
		return fmt.Sprintf("%dd", days)
	case hours > 0 && m > 0:
		return fmt.Sprintf("%dh%dm", hours, m)
	case hours > 0:
		return fmt.Sprintf("%dh", hours)
	default:
		return fmt.Sprintf("%dm", m)
	}
}

// askStamp is a deadline as it was recorded, or a dash when an old record carries none.
func askStamp(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.UTC().Format(time.RFC3339)
}

// dash is a field a person reads: an empty value is a dash, never an empty token that
// makes a one-line row ambiguous about which field is missing.
func dash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}
