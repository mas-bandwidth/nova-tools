package main

// The session verb folds the caller's own window into one session record.
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

func sessionVerb(now time.Time) tool.Verb {
	return tool.Verb{
		Name:    "session",
		Usage:   "session --claude-session <jsonl> [--out <dir>] [--day <YYYY-MM-DD>] [--dry-run]\n                      [--role <name>] [--weights <in,cw,cr,out>]",
		Example: "session --claude-session ./session.jsonl --out ./out",
		Effect:  tool.Effect("local write: with --out it writes the session's days into the day files there, holding --out/fold.lock; without --out, or with --dry-run, it writes nothing"),
		DryRun:  true,
		Flags: func(f *tool.Flags) {
			f.String("claude-session", "", "one Claude Code session transcript jsonl")
			f.String("out", "", "directory for the resulting daily token file")
			f.String("day", "", "one UTC day to write as YYYY-MM-DD; defaults to every stamped day")
			f.String("role", "", "the role the rows are booked under: given, the row is <model>/<role>; the default books the bare model")
			f.String("weights", tokens.DefaultWeights.Flag(), "the WEIGHTED ratios as in,cw,cr,out -- a comparison, not a price: the defaults are the ratios of one vendor's published list prices; set your own")
			f.Check(func(c *tool.Call) {
				c.Want("claude-session", "one Claude Code session jsonl, the window whose turns this folds")
				day := c.Str("day")
				if day != "" && !tokens.ValidDay(day) {
					c.Problem("--day wants one UTC day as YYYY-MM-DD, got " + oneline.Field(day))
				}
				_, werr := tokens.ParseWeights(c.Str("weights"))
				if werr != nil {
					c.Problem(werr.Error())
				}
			})
		},
		Run: func(c *tool.Call) *tool.Out {
			return runSession(c, now)
		},
	}
}

func runSession(c *tool.Call, now time.Time) *tool.Out {
	sessionPath := c.Str("claude-session")
	outDir := c.Str("out")
	day := c.Str("day")
	role := c.Str("role")
	weights := c.Str("weights")
	dryRun := c.DryRun()
	asJSON := c.Bool("json")

	w, _ := tokens.ParseWeights(weights)
	sum, err := tokens.ReadClaudeSession(sessionPath)
	if err != nil {
		return tool.Refuse(fmt.Sprintf("cannot read %s: %s", oneline.Field(sessionPath), oneline.Err(err)))
	}

	var o *tool.Out
	if asJSON {
		o = tool.Done()
		o.Fact("turns", sum.Turns)
		o.Fact("input", sum.Input)
		o.Fact("cache_write", sum.CacheWrite)
		o.Fact("cache_read", sum.CacheRead)
		o.Fact("output", sum.Output)
		o.Fact("weighted", sum.Weighted(w))
		o.Fact("avg_context", sum.AvgContext())
	} else {
		fmt.Fprintln(c.Stdout, oneline.Escape(sum.Line(w)))
	}
	if sum.Unstamped > 0 {
		note := fmt.Sprintf("unstamped=%d turns are in the totals and in no day; they are not dated by a guess", sum.Unstamped)
		if asJSON {
			o.Note(note)
		} else {
			fmt.Fprintf(c.Stdout, "SESSION NOTE %s\n", oneline.Escape(note))
		}
	}
	if outDir == "" {
		if asJSON {
			return o
		}
		return tool.Exit(0)
	}

	if why := sum.UnbookableReason(); why != "" {
		return tool.Refuse(why)
	}

	mkdir := func() error { return os.MkdirAll(outDir, 0o755) }
	if dryRun {
		mkdir = func() error { return mkdirAllRefuses(outDir) }
	}
	if err := mkdir(); err != nil {
		return tool.Refuse(fmt.Sprintf("cannot open --out: %s", oneline.Err(err)))
	}
	if !dryRun {
		release, err := tokens.TakeFoldLock(outDir, tokens.LockWait)
		if err != nil {
			return tool.Refuse(oneline.Err(err))
		}
		defer release()
	}

	days := sum.DayList()
	if day != "" {
		days = []string{day}
	}
	if len(days) == 0 {
		if asJSON {
			o.Status = tool.Failed
			o.Exit = 1
			o.Why = append(o.Why, "no turn in this session carries a day")
			return o
		}
		fmt.Fprintln(c.Stdout, "SESSION DAY day=- written=false rows=0 (no turn in this session carries a day)")
		return tool.Exit(1)
	}
	if dryRun && asJSON {
		o.Fact("dry_run", true)
	}

	exit := 0
	for _, d := range days {
		part := sum.Days[d]
		if part == nil {
			part = &tokens.SessionSum{}
		}
		fresh := sum.Rows(d, role)
		var old []tokens.DayRow
		prior, findings, err := tokens.ReadDayFile(tokens.Path(outDir, d))
		if err != nil {
			if !os.IsNotExist(err) {
				if asJSON {
					o.Item("refused", "day", d, "why", tool.Text("cannot read "+tokens.Path(outDir, d)+": "+err.Error()))
				} else {
					fmt.Fprintf(c.Stderr, "SESSION REFUSED: cannot read %s: %s\n",
						oneline.Field(tokens.Path(outDir, d)), oneline.WithRemedy(oneline.Err(err), "nova-tokens session -h"))
				}
				exit = 1
				continue
			}
		} else {
			if len(findings) > 0 {
				if asJSON {
					o.Item("refused", "day", d, "findings", len(findings), "why", tool.Text("the day file has findings; the repair is nova-tokens check --out "+outDir))
				} else {
					fmt.Fprintf(c.Stderr, "SESSION REFUSED: the day file %s has %d findings; the repair is nova-tokens check --out %s\n",
						oneline.Field(tokens.Path(outDir, d)), len(findings), oneline.Field(outDir))
				}
				exit = 1
				continue
			}
			old = prior.Rows
		}
		rows, retained, partials := tokens.MergeDay(old, fresh, []string{tokens.SessionLabel})
		if len(partials) > 0 {
			if asJSON {
				o.Item("partial", "day", d, "rows", len(partials), "why", tool.Text("a row already summed over this source and another cannot be taken apart; nothing written; run: nova-tokens fold -h, and fold that day whole"))
			} else {
				fmt.Fprintln(c.Stderr, formatLine("SESSION", "PARTIAL", "a row already summed over this source and another cannot be taken apart; nothing written; run: nova-tokens fold -h, and fold that day whole", "day", d, "rows", len(partials)))
			}
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
			if err := f.Save(outDir); err != nil {
				if asJSON {
					o.Item("refused", "day", d, "why", tool.Text("cannot write "+tokens.Path(outDir, d)+": "+err.Error()))
				} else {
					fmt.Fprintf(c.Stderr, "SESSION REFUSED: cannot write %s: %s\n", oneline.Field(tokens.Path(outDir, d)), oneline.WithRemedy(oneline.Err(err), "nova-tokens session -h"))
				}
				exit = 1
				continue
			}
			written = true
		}
		booked := make([]string, 0, len(fresh))
		for _, r := range fresh {
			booked = append(booked, r.Model)
		}
		kv := []any{"day", d, "written", written, "rows", len(rows), "retained", retained, "model", strings.Join(booked, ","), "weighted", part.Weighted(w)}
		if dryRun {
			kv = append(kv, "dry_run", true)
		}
		if asJSON {
			o.Item("day", kv...)
		} else {
			fmt.Fprintln(c.Stdout, formatLine("SESSION", "DAY", "", kv...))
		}
	}
	if asJSON {
		o.Exit = exit
		if exit != 0 {
			o.Status = tool.Failed
		}
		return o
	}
	return tool.Exit(exit)
}
