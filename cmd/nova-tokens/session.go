package main

// The session verb: the coordinator's own window, folded (G5 of pit stop 3, #828).
//
// It is a VERB OF ITS OWN and not a flag on fold, deliberately. fold's source flags are
// declared once, in sourceFlags, and shared by fold, sources and report, so a flag added
// for one of them is a flag the other two must also mean something by; and a session file
// is not a source in that sense -- it is ONE file, read once, folded into one model row,
// and nothing about a month or a set of friends. A verb keeps fold's surface exactly as it
// was and keeps the new thing readable on its own line.

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/tokens"
)

func cmdSession(args []string, stdout, stderr io.Writer, now time.Time) int {
	fs := newFlagSet("session")
	session := fs.String("claude-session", "", "")
	out := fs.String("out", "", "")
	day := fs.String("day", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, " session", err.Error())
	}
	if n := fs.NArg(); n > 0 {
		return refuse(stderr, " session", fmt.Sprintf("takes no positional arguments, got %d (flags come before arguments)", n))
	}
	r := &refusals{token: "TOKENS"}
	r.required("claude-session", *session, "one Claude Code session jsonl, the window whose turns this folds")
	if *day != "" && !tokens.ValidDay(*day) {
		r.add("--day wants one UTC day as YYYY-MM-DD, got " + oneline.Field(*day))
	}
	if len(r.list) > 0 {
		return r.print(stderr)
	}

	sum, err := tokens.ReadClaudeSession(*session)
	if err != nil {
		return refuse(stderr, " session", fmt.Sprintf("cannot read %s: %s", oneline.Field(*session), oneline.Err(err)))
	}
	// Every field of the SESSION line is a %d over an integer, so the escape is a no-op --
	// and it is here anyway, because the tripwire that keeps this binary's output one line
	// per event does not take a promise about a value, only the call that enforces it.
	fmt.Fprintln(stdout, oneline.Escape(sum.Line()))
	if sum.Unstamped > 0 {
		fmt.Fprintf(stdout, "TOKENS NOTE unstamped=%d turns are in the totals and in no day; they are not dated by a guess\n", sum.Unstamped)
	}
	if *out == "" {
		return 0
	}

	// The fold. One day file per day the session's turns fell on, merged by source the way
	// every other fold merges: this run recomputes the rows its own source wrote and keeps
	// every other row exactly as it is.
	if err := os.MkdirAll(*out, 0o755); err != nil {
		return refuse(stderr, " session", fmt.Sprintf("cannot open --out: %s", oneline.Err(err)))
	}
	release, err := tokens.TakeFoldLock(*out, tokens.LockWait)
	if err != nil {
		return refuse(stderr, " session", oneline.Err(err))
	}
	defer release()

	days := sum.DayList()
	if *day != "" {
		days = []string{*day}
	}
	if len(days) == 0 {
		fmt.Fprintln(stdout, "TOKENS DAY day=- written=false rows=0 (no turn in this session carries a day)")
		return 1
	}
	exit := 0
	for _, d := range days {
		// A --day the session has no turn on is a row of zeros, not a nil: the claim "this
		// window spent nothing that day" is a measurement, and the row carries it.
		part := sum.Days[d]
		if part == nil {
			part = &tokens.SessionSum{}
		}
		fresh := []tokens.DayRow{sum.Row(d)}
		var old []tokens.DayRow
		if f, findings, err := tokens.ReadDayFile(tokens.Path(*out, d)); err == nil {
			if len(findings) > 0 {
				fmt.Fprintf(stderr, "TOKENS REFUSED: the day file %s has %d findings; the repair is nova-tokens check --out %s\n",
					oneline.Field(tokens.Path(*out, d)), len(findings), oneline.Field(*out))
				exit = 1
				continue
			}
			old = f.Rows
		}
		rows, retained, partials := tokens.MergeDay(old, fresh, []string{tokens.SessionLabel})
		if len(partials) > 0 {
			fmt.Fprintf(stderr, "TOKENS PARTIAL day=%s rows=%d: a row already summed over this source and another cannot be taken apart; nothing written (fold that day whole)\n",
				oneline.Field(d), len(partials))
			exit = 1
			continue
		}
		f := &tokens.DayFile{
			Day: d, At: stamp(now), Build: buildVersion(),
			Turns:   strconv.Itoa(part.Turns),
			Sources: tokens.SourcesOf(rows), Rows: rows,
		}
		if err := f.Save(*out); err != nil {
			fmt.Fprintf(stderr, "TOKENS REFUSED: cannot write %s: %s\n", oneline.Field(tokens.Path(*out, d)), oneline.Err(err))
			exit = 1
			continue
		}
		fmt.Fprintf(stdout, "TOKENS DAY day=%s written=true rows=%d retained=%d model=%s weighted=%d\n",
			oneline.Field(d), len(rows), retained, oneline.Field(tokens.CoordinatorModel), part.Weighted())
	}
	return exit
}
