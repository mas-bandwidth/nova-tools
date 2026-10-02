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
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/tokens"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// mkdirAllRefuses is the refusal os.MkdirAll(path) would give before it makes anything:
// path, or the nearest ancestor it would stop at, exists and is no directory. It walks
// the path the way MkdirAll does (trailing separators, then the parent's prefix), so the
// error is MkdirAll's own, word for word. What only making the directory can find (a
// parent that is read-only) is not known without making it.
func mkdirAllRefuses(path string) error {
	if fi, err := os.Stat(path); err == nil {
		if fi.IsDir() {
			return nil
		}
		return &os.PathError{Op: "mkdir", Path: path, Err: syscall.ENOTDIR}
	}
	i := len(path)
	for i > 0 && os.IsPathSeparator(path[i-1]) {
		i--
	}
	j := i
	for j > 0 && !os.IsPathSeparator(path[j-1]) {
		j--
	}
	if j > 1 {
		return mkdirAllRefuses(path[:j-1])
	}
	return nil
}

func cmdSession(now time.Time) tool.Verb {
	return tool.Verb{
		Name:    "session",
		Token:   "TOKENS",
		Usage:   "session --claude-session <jsonl> [--out <dir>] [--day <YYYY-MM-DD>] [--dry-run]",
		Example: "session --claude-session ./session.jsonl --out ./out",
		Effect:  "local write: with --out it writes the session's days into the day files there, holding --out/fold.lock; without --out, or with --dry-run, it writes nothing",
		Detail: `session is the coordinator's own window: it sums one Claude Code session jsonl per turn
-- input, cache write, cache read, output, deduplicated on the message id so a streamed
message counts once -- prints one SESSION line with the weighted fresh-input equivalent
(input + 1.25 x cache write + 0.1 x cache read + 5 x output) and the average context per
turn, and with --out folds it into the day file as <model>/coordinator, the model the
transcript names (a transcript that names no model is refused, never booked under a guess).`,
		Flags: func(f *tool.Flags) {
			f.Required("claude-session", "one Claude Code session jsonl, the window whose turns this folds")
			f.String("out", "", "directory for the resulting daily token file")
			f.String("day", "", "one UTC day to write as YYYY-MM-DD; defaults to every stamped day")
			f.Bool("dry-run", false, "with --out, print the days that would be written, and write nothing (no directory, no day file, no lock)")
			f.Check(func(c *tool.Call) {
				if day := c.Str("day"); day != "" && !tokens.ValidDay(day) {
					c.Problem("--day wants one UTC day as YYYY-MM-DD, got " + oneline.Field(day))
				}
			})
		},
		Run: func(c *tool.Call) *tool.Out { return session(c, now) },
	}
}

// refuseVerb is one refusal of a verb's invocation, its remedy the verb's own help.
func refuseVerb(verb, why string) *tool.Out {
	o := tool.Refuse(why)
	o.Remedy = "nova-tokens " + verb + " -h"
	return o
}

func session(c *tool.Call, now time.Time) *tool.Out {
	dryRun := c.DryRun()
	session, out, day := c.Str("claude-session"), c.Str("out"), c.Str("day")
	s := newSink(c, "session")
	sum, err := tokens.ReadClaudeSession(session)
	if err != nil {
		return refuseVerb("session", fmt.Sprintf("cannot read %s: %s", oneline.Field(session), oneline.Err(err)))
	}
	// Every field of the SESSION line is a %d over an integer, so the escape is a no-op --
	// and it is here anyway, because the tripwire that keeps this binary's output one line
	// per event does not take a promise about a value, only the call that enforces it.
	fmt.Fprintln(s.out(), oneline.Escape(sum.Line()))
	s.fact("turns", sum.Turns)
	s.fact("input", sum.Input)
	s.fact("cache_write", sum.CacheWrite)
	s.fact("cache_read", sum.CacheRead)
	s.fact("output", sum.Output)
	s.fact("weighted", sum.Weighted())
	s.fact("avg_context", sum.AvgContext())
	if sum.Unstamped > 0 {
		note := fmt.Sprintf("unstamped=%d turns are in the totals and in no day; they are not dated by a guess", sum.Unstamped)
		fmt.Fprintf(s.out(), "TOKENS NOTE %s\n", oneline.Escape(note))
		s.note(note)
	}
	if out == "" {
		return s.done(0, 0)
	}

	// THE ROW IS BOOKED UNDER THE MODEL THE TRANSCRIPT NAMES. A transcript that names none has
	// no honest row, and the refusal says so before anything is created or written.
	if why := sum.UnbookableReason(); why != "" {
		return refuseVerb("session", why)
	}

	// The fold. One day file per day the session's turns fell on, merged by source the way
	// every other fold merges: this run recomputes the rows its own source wrote and keeps
	// every other row exactly as it is. A dry run reads the day files and writes nothing.
	mkdir := func() error { return os.MkdirAll(out, 0o755) }
	if dryRun {
		mkdir = func() error { return mkdirAllRefuses(out) }
	}
	if err := mkdir(); err != nil {
		return refuseVerb("session", fmt.Sprintf("cannot open --out: %s", oneline.Err(err)))
	}
	if !dryRun {
		release, err := tokens.TakeFoldLock(out, tokens.LockWait)
		if err != nil {
			return refuseVerb("session", oneline.Err(err))
		}
		defer release()
	}

	days := sum.DayList()
	if day != "" {
		days = []string{day}
	}
	if len(days) == 0 {
		fmt.Fprintln(s.out(), "TOKENS DAY day=- written=false rows=0 (no turn in this session carries a day)")
		s.o.Why = append(s.o.Why, "no turn in this session carries a day")
		return s.done(1, 0)
	}
	if dryRun {
		s.fact("dry_run", true)
	}
	exit := 0
	for _, d := range days {
		// A --day the session has no turn on is a row of zeros, not a nil: the claim "this
		// window spent nothing that day" is a measurement, and the row carries it.
		part := sum.Days[d]
		if part == nil {
			part = &tokens.SessionSum{}
		}
		fresh := sum.Rows(d)
		var old []tokens.DayRow
		prior, findings, err := tokens.ReadDayFile(tokens.Path(out, d))
		if err != nil {
			if !os.IsNotExist(err) {
				fmt.Fprintf(s.err(), "TOKENS REFUSED: cannot read %s: %s\n",
					oneline.Field(tokens.Path(out, d)), oneline.WithRemedy(oneline.Err(err), "nova-tokens session -h"))
				s.item("refused", "day", d, "why", tool.Text("cannot read "+tokens.Path(out, d)+": "+err.Error()))
				exit = 1
				continue
			}
		} else {
			if len(findings) > 0 {
				fmt.Fprintf(s.err(), "TOKENS REFUSED: the day file %s has %d findings; the repair is nova-tokens check --out %s\n",
					oneline.Field(tokens.Path(out, d)), len(findings), oneline.Field(out))
				s.item("refused", "day", d, "findings", len(findings), "why", tool.Text("the day file has findings; the repair is nova-tokens check --out "+out))
				exit = 1
				continue
			}
			old = prior.Rows
		}
		rows, retained, partials := tokens.MergeDay(old, fresh, []string{tokens.SessionLabel})
		if len(partials) > 0 {
			fmt.Fprintln(s.err(), s.line("TOKENS", "PARTIAL", "a row already summed over this source and another cannot be taken apart; nothing written; run: nova-tokens fold -h, and fold that day whole",
				"day", d, "rows", len(partials)))
			exit = 1
			continue
		}
		f := &tokens.DayFile{
			Day: d, At: stamp(now), Build: buildVersion(),
			Turns:   strconv.Itoa(part.Turns),
			Sources: tokens.SourcesOf(rows), Rows: rows,
		}
		written := false
		if !dryRun {
			if err := f.Save(out); err != nil {
				fmt.Fprintf(s.err(), "TOKENS REFUSED: cannot write %s: %s\n", oneline.Field(tokens.Path(out, d)), oneline.WithRemedy(oneline.Err(err), "nova-tokens session -h"))
				s.item("refused", "day", d, "why", tool.Text("cannot write "+tokens.Path(out, d)+": "+err.Error()))
				exit = 1
				continue
			}
			written = true
		}
		booked := make([]string, 0, len(fresh))
		for _, r := range fresh {
			booked = append(booked, r.Model)
		}
		kv := []any{"day", d, "written", written, "rows", len(rows), "retained", retained, "model", strings.Join(booked, ","), "weighted", part.Weighted()}
		if dryRun {
			kv = append(kv, "dry_run", true)
		}
		fmt.Fprintln(s.out(), s.line("TOKENS", "DAY", "", kv...))
	}
	return s.done(exit, 0)
}
