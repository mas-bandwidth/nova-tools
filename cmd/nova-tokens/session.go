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
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/tokens"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// mkdirAllRefuses is the refusal os.MkdirAll(path) would give before it makes anything:
// path, or the nearest ancestor that exists, is not a directory. wouldMkdir says whether
// MkdirAll would have made the directory, so a dry run reports the plan. What only making
// the directory can find (a read-only parent) is not known without making it, and a stat
// that cannot see is no refusal.
func mkdirAllRefuses(path string) (wouldMkdir bool, err error) {
	if fi, statErr := os.Stat(path); statErr == nil {
		if fi.IsDir() {
			return false, nil
		}
		return false, &os.PathError{Op: "mkdir", Path: path, Err: syscall.ENOTDIR}
	}
	for p := filepath.Dir(path); ; p = filepath.Dir(p) {
		fi, statErr := os.Stat(p)
		if statErr == nil {
			if fi.IsDir() {
				return true, nil
			}
			return false, &os.PathError{Op: "mkdir", Path: p, Err: syscall.ENOTDIR}
		}
		if p == filepath.Dir(p) {
			return true, nil
		}
	}
}

func sessionVerb(now time.Time) tool.Verb {
	return tool.Verb{
		Name:    "session",
		Token:   "SESSION",
		Usage:   "session --claude-session <jsonl> [--out <dir>] [--day <YYYY-MM-DD>] [--dry-run]\n                      [--role <name>] [--weights <in,cw,cr,out>]",
		Example: "session --claude-session ./session.jsonl --out ./out",
		Effect:  tool.Effect("local write: with --out it writes the session's days into the day files there, holding --out/fold.lock; without --out, or with --dry-run, it writes nothing"),
		DryRun:  true,
		Flags: func(f *tool.Flags) {
			f.Required("claude-session", "one Claude Code session jsonl, the window whose turns this folds")
			f.String("out", "", "directory for the resulting daily token file")
			f.String("day", "", "one UTC day to write as YYYY-MM-DD; defaults to every stamped day")
			f.String("role", "", "the role the rows are booked under: given, the row is <model>/<role>; the default books the bare model")
			f.String("weights", tokens.DefaultWeights.Flag(), "the WEIGHTED ratios as in,cw,cr,out -- a comparison, not a price: the defaults are the ratios of one vendor's published list prices; set your own")
			f.Check(func(c *tool.Call) {
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

	w, _ := tokens.ParseWeights(weights)
	sum, err := tokens.ReadClaudeSession(sessionPath)
	if err != nil {
		return tool.Refuse(fmt.Sprintf("cannot read %s: %s", oneline.Field(sessionPath), oneline.Err(err)))
	}

	o := tool.Done()
	o.Findings("refused", "partial")
	o.Fact("turns", sum.Turns).
		Fact("input", sum.Input).
		Fact("cache_write", sum.CacheWrite).
		Fact("cache_read", sum.CacheRead).
		Fact("output", sum.Output).
		Fact("weighted", sum.Weighted(w)).
		Fact("avg_context", sum.AvgContext())
	if sum.Unstamped > 0 {
		o.Note(fmt.Sprintf("unstamped=%d turns are in the totals and in no day; they are not dated by a guess", sum.Unstamped))
	}
	if outDir == "" {
		return o
	}

	if why := sum.UnbookableReason(); why != "" {
		return tool.Refuse(why)
	}

	mkdir := func() error { return os.MkdirAll(outDir, 0o755) }
	wouldMkdir := false
	if dryRun {
		mkdir = func() error {
			var err error
			wouldMkdir, err = mkdirAllRefuses(outDir)
			return err
		}
	}
	if err := mkdir(); err != nil {
		return tool.Refuse(fmt.Sprintf("cannot open --out: %s", oneline.Err(err)))
	}
	if dryRun {
		o.Fact("dry_run", true).Fact("would_mkdir", wouldMkdir)
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
		o.Status = tool.Failed
		o.Exit = 1
		o.Why = append(o.Why, "no turn in this session carries a day")
		return o
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
				o.Item("refused", "day", d, "why", tool.Text("cannot read "+tokens.Path(outDir, d)+": "+err.Error()))
				exit = 1
				continue
			}
		} else {
			if len(findings) > 0 {
				o.Item("refused", "day", d, "findings", len(findings), "why", tool.Text("the day file has findings; the repair is nova-tokens check --out "+outDir))
				exit = 1
				continue
			}
			old = prior.Rows
		}
		rows, retained, partials := tokens.MergeDay(old, fresh, []string{tokens.SessionLabel})
		if len(partials) > 0 {
			o.Item("partial", "day", d, "rows", len(partials), "why", tool.Text("a row already summed over this source and another cannot be taken apart; nothing written; run: nova-tokens fold -h, and fold that day whole"))
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
				o.Item("refused", "day", d, "why", tool.Text("cannot write "+tokens.Path(outDir, d)+": "+err.Error()))
				exit = 1
				continue
			}
			written = true
		}
		booked := make([]string, 0, len(fresh))
		for _, r := range fresh {
			booked = append(booked, r.Model)
		}
		o.Item("day", "day", d, "written", written, "rows", len(rows), "retained", retained, "model", strings.Join(booked, ","), "weighted", part.Weighted(w))
	}
	o.Exit = exit
	if exit != 0 {
		o.Status = tool.Failed
	}
	return o
}
