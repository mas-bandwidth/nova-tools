// fold.go holds the fold verb: its flags, its run and the helpers only it uses.

package main

import (
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/tokens"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// foldLists are fold's listings in the order their lines print, each capped by --max with
// its own MORE line; true is a list whose lines are findings, on stderr.
var foldLists = []struct {
	kind  string
	toErr bool
}{
	{"source", false}, {"unreadable", true}, {"unparsed", true}, {"superseded", false},
	{"conflict", true}, {"touched", false}, {"mixed", true}, {"day", false},
	{"shrank", true}, {"partial", true}, {"quiet", true},
}

// foldVerb is the wall: it reads every declared source whole and writes the days it could
// compute. It says FAILED when a source could not be read, a bus line or note did not parse,
// a row mixed two day bases, a lane-day had competing reports, or a day would have shrunk
// -- and it still writes the rest, because the exit code is about the claim. Under
// --dry-run it reads and decides exactly the same and writes nothing, the lock included.
func foldVerb(now time.Time) tool.Verb {
	var sf sourceFlags
	return tool.Verb{
		Name:    "fold",
		Usage:   "fold --out <dir> (--day <YYYY-MM-DD> | --all) --repos <file>\n                      [--claude <label>=<dir>]... [--opencode <label>=<file>]... [--swarm <label>=<pool>]... [--bus <dir>]\n                      [--provider <kind>:<label>=<file>]... [--scratch <dir>] [--timeout <seconds>] [--allow-shrink] [--max <n>] [--dry-run]",
		Example: "fold --out ./out --day 2026-09-11 --repos ./repos.tsv --claude bench=./transcripts",
		Effect:  "local write: writes the day files in --out, holding --out/fold.lock while it writes, and with --opencode copies the database into --scratch/opencode-<label>/ (replaced, and left); --dry-run reads the same sources, refuses what the real run refuses, and writes nothing (its database copy is made in a new directory under --scratch and removed before it exits)",
		DryRun:  true,
		Flags: func(f *tool.Flags) {
			f.String("out", "", "directory for daily token files")
			f.String("day", "", "one UTC day to fold as YYYY-MM-DD")
			f.Bool("all", false, "fold every day named by the sources")
			f.Bool("allow-shrink", false, "write a day even when its totals shrink")
			f.Max()
			sf.declare(f.FlagSet, true)
			f.Check(func(c *tool.Call) {
				c.Want("out", wantsOut)
				checkDay(c, c.Str("day"), c.Bool("all"))
				sf.check(c)
			})
		},
		Run: func(c *tool.Call) *tool.Out {
			return runFold(c, sf, now)
		},
	}
}

func runFold(c *tool.Call, sf sourceFlags, now time.Time) *tool.Out {
	outDir := c.Str("out")
	day := c.Str("day")
	all := c.Bool("all")
	allowShrink := c.Bool("allow-shrink")
	dryRun := c.DryRun()

	fi, statErr := os.Stat(outDir)
	if statErr != nil || !fi.IsDir() {
		if statErr != nil && os.IsNotExist(statErr) {
			ref := tool.Refuse("--out does not exist: " + outDir + "; it wants " + wantsOut)
			ref.Remedy = "mkdir -p " + outDir
			return ref
		} else if statErr != nil {
			return tool.Refuse("--out " + outDir + ": " + statErr.Error() + "; it wants " + wantsOut)
		}
		return tool.Refuse("--out is not a directory: " + outDir + "; it wants " + wantsOut)
	}

	rules, err := tokens.LoadRules(sf.repos)
	if err != nil {
		return tool.Refuse("--repos " + sf.repos + ": " + err.Error() + "; it wants " + wantsRepos)
	}
	sources, copyNotesList := sf.read(rules, now, dryRun)
	folder := tokens.NewFolder()
	for _, src := range sources {
		for _, m := range src.Stream {
			folder.Add(src.Label, m)
		}
	}
	// TokenFold invariant OverlapRefusedBeforeWrite: refuse before the lock and
	// before any day file. An unscoped id is not this check (UnscopedIDNotDeduped).
	if folder.RefuseWrite() {
		return refuseOverlap(folder.Overlaps())
	}
	if !dryRun {
		release, err := tokens.TakeFoldLock(outDir, tokens.LockWait)
		if err != nil {
			return tool.Refuse(err.Error())
		}
		defer release()
	}

	asJSON := c.Bool("json")
	max := c.Int("max")

	if !asJSON {
		fmt.Fprintln(c.Stdout, formatLine("FOLD", "FOLD", "", "at", stamp(now), "build", buildVersion(), "out", outDir,
			"sources", len(sources), "days", daysAsked(day, all), "repos", sf.repos))

		lists := map[string]*bounded.List{}
		for _, l := range foldLists {
			var w io.Writer = c.Stdout
			if l.toErr {
				w = c.Stderr
			}
			lists[l.kind] = bounded.Capped(w, max, "FOLD", l.kind, maxRemedy("fold"))
		}
		conflictDays := map[string]bool{}
		declared := make([]string, 0, len(sources))
		for _, src := range sources {
			declared = append(declared, src.Label)
			lists["source"].Line(sourceLine("FOLD", src))
		}
		lists["source"].More()
		for _, src := range sources {
			for _, u := range src.Unreadables {
				lists["unreadable"].Line(unreadableLine("FOLD", u))
			}
		}
		for _, src := range sources {
			for _, u := range src.Unparseds {
				lists["unparsed"].Line(unparsedLine("FOLD", u))
			}
		}
		lists["unparsed"].More()
		for _, src := range sources {
			for _, sp := range src.Supersededs {
				lists["superseded"].Line(formatLine("FOLD", "SUPERSEDED", "", "label", sp.Label, "note", sp.Note, "by", sp.By, "day", sp.Day))
			}
		}
		lists["superseded"].More()
		for _, src := range sources {
			for _, conf := range src.Conflicts {
				conflictDays[conf.Day] = true
				notes := strings.Join(conf.Notes, ",")
				lists["conflict"].Line(formatLine("FOLD", "CONFLICT", "competing reports; send a correction whose subject carries supersedes="+oneline.Field(notes),
					"label", conf.Label, "day", conf.Day, "notes", notes))
			}
		}
		lists["conflict"].More()
		for _, src := range sources {
			for _, t := range src.Toucheds {
				lists["touched"].Line(formatLine("FOLD", "TOUCHED", "", "label", t.Label, "day", t.Day, "repos", strings.Join(t.Repos, ",")))
			}
		}
		lists["touched"].More()

		days := folder.Days()
		if !all {
			days = []string{day}
		}
		daysWritten, rowsWritten, quiet := 0, 0, 0
		mixedLabels := "-"
		firstPartial, firstQuiet := "", ""
		for _, d := range days {
			rows, mixed := folder.DayRows(d)
			for _, m := range mixed {
				if mixedLabels == "-" && len(m.Labels) > 0 {
					mixedLabels = strings.Join(m.Labels, " and ")
				}
				lists["mixed"].Line(formatLine("FOLD", "MIXED", "two day bases on one row; declare one export for that day",
					"date", m.Day, "model", m.Model, "repo", m.Repo, "bases", strings.Join(m.Bases, ",")))
			}
			outPath := tokens.Path(outDir, d)
			old, findings, readErr := tokens.ReadDayFile(outPath)
			if len(rows) == 0 && !conflictDays[d] && readErr != nil && os.IsNotExist(readErr) {
				continue
			}
			file := buildDayFile(d, rows, folder, now)
			written, wouldWrite := false, false
			shrank := false
			partial := false
			var dayShrinks []tokens.Shrink
			if !conflictDays[d] {
				switch {
				case readErr != nil && !os.IsNotExist(readErr):
					lists["unreadable"].Line(unreadableLine("FOLD", tokens.Unreadable{Label: "out", Path: outPath, Why: readErr.Error()}))
				case readErr == nil && len(findings) > 0:
					for _, f := range findings {
						why := oneline.Escape(f.Reason)
						if f.Line > 0 {
							why = fmt.Sprintf("line %d: %s", f.Line, oneline.Escape(f.Reason))
						}
						lists["unreadable"].Line(unreadableLine("FOLD", tokens.Unreadable{Label: "out", Path: outPath, Why: why}))
					}
				case readErr == nil && len(findings) == 0:
					merged, retained, partials := tokens.MergeDay(old.Rows, file.Rows, declared)
					for _, pt := range partials {
						partial = true
						lists["partial"].Line(formatLine("FOLD", "PARTIAL", "this fold declared only some of the sources that wrote the row; declare every source in the file's sources= line, or fold this day into its own --out",
							"date", pt.Day, "model", pt.Model, "repo", pt.Repo, "sources", strings.Join(pt.Sources, ","),
							"folded", strings.Join(pt.Folded, ","), "written", false))
						if firstPartial == "" {
							firstPartial = pt.Model + " on " + pt.Repo + " for " + pt.Day
						}
					}
					if !partial {
						file.Rows = merged
						file.Sources = tokens.SourcesOf(merged)
						if retained > 0 {
							file.Turns = tokens.Dash
						}
						dayShrinks = tokens.Shrinks(old.Totals(), file.Totals(), d)
						shrank = len(dayShrinks) > 0
					}
					for _, src := range sources {
						fedDay := slices.ContainsFunc(src.Stream, func(m tokens.Message) bool { return m.Day == d })
						if fedDay || !slices.Contains(old.Sources, src.Label) {
							continue
						}
						quiet++
						if firstQuiet == "" {
							firstQuiet = src.Label + " on " + d
						}
						lists["quiet"].Line(formatLine("FOLD", "QUIET", "a declared source has zero samples for an explicitly selected existing day",
							"label", src.Label, "day", d))
					}
				}
				wouldWrite = !partial && (!shrank || allowShrink) && len(file.Rows) > 0 &&
					(readErr == nil && len(findings) == 0 || readErr != nil && os.IsNotExist(readErr))
				switch {
				case wouldWrite && dryRun:
					daysWritten++
					rowsWritten += len(rows)
				case wouldWrite:
					if err := file.Save(outDir); err != nil {
						lists["unreadable"].Line(unreadableLine("FOLD", tokens.Unreadable{Label: "out", Path: outPath, Why: err.Error()}))
					} else {
						written = true
						daysWritten++
						rowsWritten += len(rows)
					}
				}
				for _, sh := range dayShrinks {
					lists["shrank"].Line(formatLine("FOLD", "SHRANK", "a source went quiet; --allow-shrink writes it anyway",
						"date", sh.Day, "type", tokens.TypeNames[sh.Type], "file", sh.File, "now", sh.Now, "written", written))
				}
			}
			lists["day"].Line(dayLine("FOLD", d, file, written, dryRun, wouldWrite))
		}
		for _, kind := range []string{"unreadable", "mixed", "day", "shrank", "partial", "quiet"} {
			lists[kind].More()
		}

		n := func(kind string) int { return lists[kind].Total() }
		counts := []any{"days", daysWritten, "rows", rowsWritten, "sources", len(sources), "unreadable", n("unreadable"),
			"unparsed", n("unparsed"), "mixed", n("mixed"), "conflict", n("conflict"), "shrank", n("shrank"),
			"partial", n("partial"), "quiet", quiet}
		if dryRun {
			counts = append(counts, "dry_run", true)
		}
		dropped, of, allDropped := allMessagesDropped(sources)
		bad := n("unreadable") > 0 || n("unparsed") > 0 || n("mixed") > 0 || n("conflict") > 0 ||
			(n("shrank") > 0 && !allowShrink) || n("partial") > 0 || allDropped
		if allDropped {
			fmt.Fprintf(c.Stderr, "FOLD FAILED dropped=%d of %d: %s\n", dropped, of, allDroppedWhy)
		}
		if bad {
			fmt.Fprintf(c.Stderr, "FOLD FAILED%s\n", formatFields(counts...))
		} else {
			fmt.Fprintf(c.Stdout, "FOLD OK%s\n", formatFields(counts...))
		}
		note := remedy(sources, n("unreadable"), n("unparsed"), n("mixed"), n("conflict"), n("shrank"), n("partial"), quiet,
			allowShrink, dryRun, outDir, mixedLabels, firstPartial, firstQuiet)
		fmt.Fprintf(c.Stdout, "FOLD NOTE %s\n", oneline.Escape(note))
		copyNotesWriter(c.Stdout, "FOLD", copyNotesList)
		if bad {
			return tool.Exit(1)
		}
		return tool.Exit(0)
	}

	o := tool.Done()

	o.Item("fold", "at", stamp(now), "build", buildVersion(), "out", outDir,
		"sources", len(sources), "days", daysAsked(day, all), "repos", sf.repos)

	conflictDays := map[string]bool{}
	declared := make([]string, 0, len(sources))
	for _, src := range sources {
		declared = append(declared, src.Label)
		addSourceItem(o, src)
	}
	for _, src := range sources {
		for _, u := range src.Unreadables {
			addUnreadableItem(o, u)
		}
	}
	for _, src := range sources {
		for _, u := range src.Unparseds {
			addUnparsedItem(o, u)
		}
	}
	for _, src := range sources {
		for _, sp := range src.Supersededs {
			o.Item("superseded", "label", sp.Label, "note", sp.Note, "by", sp.By, "day", sp.Day)
		}
	}
	for _, src := range sources {
		for _, c := range src.Conflicts {
			conflictDays[c.Day] = true
			notes := strings.Join(c.Notes, ",")
			o.ItemText("conflict", "competing reports; send a correction whose subject carries supersedes="+oneline.Field(notes),
				"label", c.Label, "day", c.Day, "notes", notes)
		}
	}
	for _, src := range sources {
		for _, t := range src.Toucheds {
			o.Item("touched", "label", t.Label, "day", t.Day, "repos", strings.Join(t.Repos, ","))
		}
	}

	days := folder.Days()
	if !all {
		days = []string{day}
	}
	daysWritten, rowsWritten, quiet := 0, 0, 0
	mixedLabels := "-"
	firstPartial, firstQuiet := "", ""
	for _, d := range days {
		rows, mixed := folder.DayRows(d)
		for _, m := range mixed {
			if mixedLabels == "-" && len(m.Labels) > 0 {
				mixedLabels = strings.Join(m.Labels, " and ")
			}
			o.ItemText("mixed", "two day bases on one row; declare one export for that day",
				"date", m.Day, "model", m.Model, "repo", m.Repo, "bases", strings.Join(m.Bases, ","))
		}
		outPath := tokens.Path(outDir, d)
		old, findings, readErr := tokens.ReadDayFile(outPath)
		if len(rows) == 0 && !conflictDays[d] && readErr != nil && os.IsNotExist(readErr) {
			continue
		}
		file := buildDayFile(d, rows, folder, now)
		written, wouldWrite := false, false
		shrank := false
		partial := false
		var dayShrinks []tokens.Shrink
		if !conflictDays[d] {
			switch {
			case readErr != nil && !os.IsNotExist(readErr):
				addUnreadableItem(o, tokens.Unreadable{Label: "out", Path: outPath, Why: readErr.Error()})
			case readErr == nil && len(findings) > 0:
				for _, f := range findings {
					why := oneline.Escape(f.Reason)
					if f.Line > 0 {
						why = fmt.Sprintf("line %d: %s", f.Line, oneline.Escape(f.Reason))
					}
					addUnreadableItem(o, tokens.Unreadable{Label: "out", Path: outPath, Why: why})
				}
			case readErr == nil && len(findings) == 0:
				merged, retained, partials := tokens.MergeDay(old.Rows, file.Rows, declared)
				for _, pt := range partials {
					partial = true
					o.ItemText("partial", "this fold declared only some of the sources that wrote the row; declare every source in the file's sources= line, or fold this day into its own --out",
						"date", pt.Day, "model", pt.Model, "repo", pt.Repo, "sources", strings.Join(pt.Sources, ","),
						"folded", strings.Join(pt.Folded, ","), "written", false)
					if firstPartial == "" {
						firstPartial = pt.Model + " on " + pt.Repo + " for " + pt.Day
					}
				}
				if !partial {
					file.Rows = merged
					file.Sources = tokens.SourcesOf(merged)
					if retained > 0 {
						file.Turns = tokens.Dash
					}
					dayShrinks = tokens.Shrinks(old.Totals(), file.Totals(), d)
					shrank = len(dayShrinks) > 0
				}
				for _, src := range sources {
					fedDay := slices.ContainsFunc(src.Stream, func(m tokens.Message) bool { return m.Day == d })
					if fedDay || !slices.Contains(old.Sources, src.Label) {
						continue
					}
					quiet++
					if firstQuiet == "" {
						firstQuiet = src.Label + " on " + d
					}
					o.ItemText("quiet", "a declared source has zero samples for an explicitly selected existing day",
						"label", src.Label, "day", d)
				}
			}
			wouldWrite = !partial && (!shrank || allowShrink) && len(file.Rows) > 0 &&
				(readErr == nil && len(findings) == 0 || readErr != nil && os.IsNotExist(readErr))
			switch {
			case wouldWrite && dryRun:
				daysWritten++
				rowsWritten += len(rows)
			case wouldWrite:
				if err := file.Save(outDir); err != nil {
					addUnreadableItem(o, tokens.Unreadable{Label: "out", Path: outPath, Why: err.Error()})
				} else {
					written = true
					daysWritten++
					rowsWritten += len(rows)
				}
			}
			for _, sh := range dayShrinks {
				o.ItemText("shrank", "a source went quiet; --allow-shrink writes it anyway",
					"date", sh.Day, "type", tokens.TypeNames[sh.Type], "file", sh.File, "now", sh.Now, "written", written)
			}
		}
		addDayItem(o, d, file, written, dryRun, wouldWrite)
	}

	unreadableTotal := countItems(o, "unreadable")
	unparsedTotal := countItems(o, "unparsed")
	mixedTotal := countItems(o, "mixed")
	conflictTotal := countItems(o, "conflict")
	shrankTotal := countItems(o, "shrank")
	partialTotal := countItems(o, "partial")

	dropped, of, allDropped := allMessagesDropped(sources)
	bad := unreadableTotal > 0 || unparsedTotal > 0 || mixedTotal > 0 || conflictTotal > 0 ||
		(shrankTotal > 0 && !allowShrink) || partialTotal > 0 || allDropped

	if bad {
		o.Status = tool.Failed
		o.Exit = 1
	}
	if allDropped {
		o.Why = append(o.Why, fmt.Sprintf("dropped=%d of %d: %s", dropped, of, allDroppedWhy))
		o.Remedy = "nova-tokens sources"
	}
	o.Fact("days", daysWritten).
		Fact("rows", rowsWritten).
		Fact("sources", len(sources)).
		Fact("unreadable", unreadableTotal).
		Fact("unparsed", unparsedTotal).
		Fact("mixed", mixedTotal).
		Fact("conflict", conflictTotal).
		Fact("shrank", shrankTotal).
		Fact("partial", partialTotal).
		Fact("quiet", quiet)

	note := remedy(sources, unreadableTotal, unparsedTotal, mixedTotal, conflictTotal, shrankTotal, partialTotal, quiet,
		allowShrink, dryRun, outDir, mixedLabels, firstPartial, firstQuiet)
	o.Note(note)
	copyNotes(o, copyNotesList)
	return o
}

func daysAsked(day string, all bool) string {
	if all {
		return "all"
	}
	return day
}

// buildDayFile turns a day's folded rows into the file that will be written.
func buildDayFile(day string, rows []*tokens.Row, folder *tokens.Folder, now time.Time) *tokens.DayFile {
	f := &tokens.DayFile{Day: day, At: stamp(now), Build: buildVersion(), Turns: tokens.Dash}
	if n, ok := folder.Turns(day); ok {
		f.Turns = strconv.Itoa(n)
	}
	labels := map[string]bool{}
	for _, r := range rows {
		for _, l := range r.Sources() {
			labels[l] = true
		}
		f.Rows = append(f.Rows, tokens.DayRow{
			Date: day, Model: r.Model, Repo: r.Repo, Counts: r.Counts,
			Rough: r.Rough, Basis: r.Basis(), Sources: r.Sources(),
		})
	}
	f.Sources = slices.Sorted(maps.Keys(labels))
	return f
}

// addDayItem is one line per day written or refused, and the two shares on it are how a
// person sees whether the rules file is good enough. Under --dry-run nothing is written,
// and would_write says whether the real run would have written the day.
func addDayItem(o *tool.Out, day string, file *tokens.DayFile, written, dryRun, wouldWrite bool) {
	models, repos := map[string]bool{}, map[string]bool{}
	var whole, unknown, other int64
	dashes, nonutc := 0, 0
	rough := 0
	for _, r := range file.Rows {
		models[r.Model] = true
		repos[r.Repo] = true
		t := r.Counts.Total()
		whole += t
		switch r.Repo {
		case "unknown":
			unknown += t
		case "other":
			other += t
		}
		dashes += r.Counts.Dashes()
		rough += r.Rough
		if r.Basis != tokens.UTC {
			nonutc++
		}
	}
	kv := []any{"day", day, "rows", len(file.Rows), "models", len(models), "repos", len(repos), "turns", count(file.Turns),
		"unknown", tokens.Percent(unknown, whole) + "%", "other", tokens.Percent(other, whole) + "%",
		"rough", rough, "dashes", dashes, "nonutc", nonutc, "sources", strings.Join(file.Sources, ","), "written", written}
	if dryRun {
		kv = append(kv, "would_write", wouldWrite)
	}
	o.Item("day", kv...)
}

func dayLine(token string, day string, file *tokens.DayFile, written, dryRun, wouldWrite bool) string {
	models, repos := map[string]bool{}, map[string]bool{}
	var whole, unknown, other int64
	dashes, nonutc := 0, 0
	rough := 0
	for _, r := range file.Rows {
		models[r.Model] = true
		repos[r.Repo] = true
		t := r.Counts.Total()
		whole += t
		switch r.Repo {
		case "unknown":
			unknown += t
		case "other":
			other += t
		}
		dashes += r.Counts.Dashes()
		rough += r.Rough
		if r.Basis != tokens.UTC {
			nonutc++
		}
	}
	kv := []any{"date", day, "rows", len(file.Rows), "models", len(models), "repos", len(repos), "turns", file.Turns,
		"unknown", tokens.Percent(unknown, whole) + "%", "other", tokens.Percent(other, whole) + "%",
		"rough", rough, "dashes", dashes, "nonutc", nonutc, "sources", strings.Join(file.Sources, ","), "written", written}
	if dryRun {
		kv = append(kv, "would_write", wouldWrite)
	}
	return formatLine(token, "DAY", "", kv...)
}

// refuseOverlap is the refusal a detected overlap prints before any day file is
// written. TokenFold invariant OverlapRefusedBeforeWrite: the line names the two
// source labels and the duplicate count, and the remedy drops one of the two flags.
func refuseOverlap(overlaps []tokens.Overlap) *tool.Out {
	whys := make([]string, len(overlaps))
	for i, o := range overlaps {
		whys[i] = overlapWhy(o)
	}
	out := tool.Refuse(whys...)
	out.Remedy = overlapRemedy(overlaps[0])
	return out
}

func overlapWhy(o tokens.Overlap) string {
	return "two declared sources fed the same " + strconv.Itoa(o.IDs) + " message ids (" + o.A + " and " + o.B + ")"
}

func overlapRemedy(o tokens.Overlap) string {
	kind, name, ok := strings.Cut(o.B, ":")
	if !ok {
		kind, name = "claude", o.B
	}
	return "nova-tokens fold ... without --" + kind + " " + name
}

// allDroppedWhy is the tail of the TOKENS FAILED line for a fold that dropped every message.
const allDroppedWhy = "no message had an id, so none was folded (a message is counted by its id: a transcript's message.id, an opencode message id, a swarm row's job); run: nova-tokens sources <the same source flags> --day <d> to see noid= per source"

// allMessagesDropped says whether the sources read at least one message and dropped every
// one of them for having no id (rule 4): the count dropped, the count read (dropped, plus
// the messages folded), and whether that is the whole of it. Some dropped and some folded
// is false: the TOKENS NOTE names that one.
func allMessagesDropped(sources []*tokens.Source) (dropped, of int, all bool) {
	folded := 0
	for _, s := range sources {
		dropped += s.Stat.NoID
		folded += len(s.Stream)
	}
	return dropped, dropped + folded, dropped > 0 && folded == 0
}
