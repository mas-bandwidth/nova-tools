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
	units := fs.String("units", "", "the work set holding the unit, as JSON (required)")
	deadline := fs.String("deadline", "", "when the answer is owed, RFC3339 (required)")
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
		{"deadline", *deadline}, {"bus", *busDir}, {"as", *as},
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
	due, err := time.Parse(time.RFC3339, *deadline)
	if err != nil {
		return refuse(stderr, " ask", fmt.Sprintf("--deadline %q is not an RFC3339 instant", *deadline))
	}
	if !due.After(at) {
		return refuse(stderr, " ask", fmt.Sprintf("--deadline %q is not after %s; an ask that is late before it is sent is not an ask", *deadline, oneline.Field(at.UTC().Format(time.RFC3339))))
	}

	ws, err := friends.Load(*units, *maxBytes)
	if err != nil {
		return refuse(stderr, " ask", oneline.Err(err))
	}
	u, err := ws.Unit(*unit)
	if err != nil {
		return refuse(stderr, " ask", oneline.Err(err))
	}
	reply := *replyBranch
	if reply == "" {
		reply = u.Branch
	}
	note, err := friends.Render(friends.AskSpec{
		From: *as, Owner: *owner, Cc: *cc, Kind: *kind, Branch: reply, Unit: *u, Deadline: due,
	})
	if err != nil {
		return refuse(stderr, " ask", oneline.Err(err))
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
	a := friends.Ask{ID: id, Owner: *owner, Kind: *kind, Unit: *unit, Sent: at.UTC(), Deadline: due.UTC(), Branch: reply, By: *as, Bus: *busDir}
	if err := ws.Record(*unit, a); err != nil {
		fmt.Fprintf(stderr, "ASK FAIL id=%s unit=%s: %s\n", oneline.Field(id), oneline.Field(*unit), oneline.Err(err))
		return 1
	}
	if err := friends.Save(*units, ws); err != nil {
		fmt.Fprintf(stderr, "ASK FAIL id=%s units=%s: the note landed and could NOT be recorded: %s\n",
			oneline.Field(id), oneline.Field(*units), oneline.Err(err))
		return 1
	}
	fmt.Fprintf(stdout, "ASK OK id=%s owner=%s unit=%s kind=%s deadline=%s branch=%s units=%s\n",
		oneline.Field(id), oneline.Field(*owner), oneline.Field(*unit), oneline.Field(*kind),
		oneline.Field(due.UTC().Format(time.RFC3339)), oneline.Field(reply), oneline.Field(*units))
	return 0
}

// cmdAsks is the other half: what is out, how long it has been out, and what is late.
func cmdAsks(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("asks", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	units := fs.String("units", "", "the work set holding the recorded asks (required)")
	owner := fs.String("owner", "", "show only the asks of this friend")
	as := fs.String("as", "", "show only the asks sent by this name")
	busDir := fs.String("bus", "", "show only the asks sent on this bus")
	max := fs.Int("max", askMaxRows, "rows before one MORE line; 0 is every row")
	maxBytes := fs.Int64("max-bytes", friends.DefaultMaxBytes, "the work set's byte ceiling")
	now := fs.String("now", "", "the instant ages and deadlines are measured against, RFC3339; default this run's clock")
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, " asks", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if fs.NArg() > 0 {
		return refuse(stderr, " asks", fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	if strings.TrimSpace(*units) == "" {
		return refuse(stderr, " asks", "--units is required; refusing to guess")
	}
	if *max < 0 {
		return refuse(stderr, " asks", fmt.Sprintf("--max %d is not a count", *max))
	}
	at, ok := askNow(*now)
	if !ok {
		return refuse(stderr, " asks", fmt.Sprintf("--now %q is not an RFC3339 instant", *now))
	}
	ws, err := friends.Load(*units, *maxBytes)
	if err != nil {
		return refuse(stderr, " asks", oneline.Err(err))
	}

	rows := ws.Open(at)
	shown, open, overdue := 0, 0, 0
	for _, r := range rows {
		if *owner != "" && r.Owner != *owner {
			continue
		}
		if *as != "" && r.By != *as {
			continue
		}
		if *busDir != "" && r.Bus != *busDir {
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
			fmt.Fprintf(stdout, "ASK id=%s owner=%s unit=%s kind=%s age=%s deadline=%s state=%s\n",
				oneline.Field(r.ID), oneline.Field(r.Owner), oneline.Field(r.Unit), oneline.Field(r.Kind),
				oneline.Field(askAge(r.Age)), oneline.Field(askStamp(r.Deadline)), oneline.Field(state))
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
