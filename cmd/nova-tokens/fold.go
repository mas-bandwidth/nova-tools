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

// cmdFold is the wall: it reads every declared source whole and writes the days it could
// compute. It says FAILED when a source could not be read, a bus line or note did not parse,
// a row mixed two day bases, a lane-day had competing reports, or a day would have shrunk
// -- and it still writes the rest, because the exit code is about the claim. Under
// --dry-run it reads and decides exactly the same and writes nothing, the lock included.
func cmdFold(args []string, stdout, stderr io.Writer, now time.Time) int {
	fs := newFlagSet("fold")
	out := fs.String("out", "", "directory for daily token files")
	day := fs.String("day", "", "one UTC day to fold as YYYY-MM-DD")
	all := fs.Bool("all", false, "fold every day named by the sources")
	allowShrink := fs.Bool("allow-shrink", false, "write a day even when its totals shrink")
	max := fs.Int("max", bounded.Default, "maximum findings or rows to print; 0 prints all")
	dryRun := fs.Bool("dry-run", false, "read the sources and print what would be written, and write nothing (no day file, no lock)")
	var sf sourceFlags
	sf.declare(fs, true)
	s, code, ok := start(fs, args, "TOKENS", stdout, stderr)
	if !ok {
		return code
	}
	r := &refusals{token: "TOKENS", s: s}
	r.required("out", *out, wantsOut)
	checkDay(r, *day, *all)
	sf.check(r)
	checkMax(r, *max)
	if len(r.list) > 0 {
		return r.print(stderr)
	}
	fi, statErr := os.Stat(*out)
	if statErr != nil || !fi.IsDir() {
		if statErr != nil && os.IsNotExist(statErr) {
			r.add("--out does not exist: " + *out + "; it wants " + wantsOut)
		} else if statErr != nil {
			r.add("--out " + *out + ": " + statErr.Error() + "; it wants " + wantsOut)
		} else {
			r.add("--out is not a directory: " + *out + "; it wants " + wantsOut)
		}
		return r.print(stderr)
	}
	rules, err := tokens.LoadRules(sf.repos)
	if err != nil {
		r.add("--repos " + sf.repos + ": " + err.Error() + "; it wants " + wantsRepos)
		return r.print(stderr)
	}
	sources, copyNotes := sf.read(rules, now, *dryRun)
	folder := tokens.NewFolder()
	for _, src := range sources {
		for _, m := range src.Stream {
			folder.Add(src.Label, m)
		}
	}
	// TokenFold invariant OverlapRefusedBeforeWrite: refuse before the lock and
	// before any day file. An unscoped id is not this check (UnscopedIDNotDeduped).
	if folder.RefuseWrite() {
		return refuseOverlap(s, folder.Overlaps())
	}
	if !*dryRun {
		release, err := tokens.TakeFoldLock(*out, tokens.LockWait)
		if err != nil {
			r.add(err.Error())
			return r.print(stderr)
		}
		defer release()
	}

	fmt.Fprintln(s.out(), s.line("TOKENS", "FOLD", "", "at", stamp(now), "build", buildVersion(), "out", *out,
		"sources", len(sources), "days", daysAsked(*day, *all), "repos", sf.repos))

	lists := map[string]*bounded.List{}
	for _, l := range foldLists {
		lists[l.kind] = s.list(l.toErr, *max, "TOKENS", l.kind, maxRemedy("fold"))
	}
	conflictDays := map[string]bool{}
	// The labels this run declared: exactly what lands in a row's sources column, and so
	// exactly the rows this fold is entitled to recompute (rule 10).
	declared := make([]string, 0, len(sources))
	for _, src := range sources {
		declared = append(declared, src.Label)
		lists["source"].Line(sourceLine(s, "TOKENS", src))
	}
	lists["source"].More()
	for _, src := range sources {
		for _, u := range src.Unreadables {
			lists["unreadable"].Line(unreadableLine(s, "TOKENS", u))
		}
	}
	// The unreadable listing is NOT closed here: a day file this run could not WRITE is an
	// unreadable too (below), and its MORE line and its count are closed after the last
	// line either can get.
	for _, src := range sources {
		for _, u := range src.Unparseds {
			lists["unparsed"].Line(unparsedLine(s, "TOKENS", u))
		}
	}
	lists["unparsed"].More()
	for _, src := range sources {
		for _, sp := range src.Supersededs {
			lists["superseded"].Line(s.line("TOKENS", "SUPERSEDED", "", "label", sp.Label, "note", sp.Note, "by", sp.By, "day", sp.Day))
		}
	}
	lists["superseded"].More()
	for _, src := range sources {
		for _, c := range src.Conflicts {
			conflictDays[c.Day] = true
			notes := strings.Join(c.Notes, ",")
			lists["conflict"].Line(s.line("TOKENS", "CONFLICT", "competing reports; send a correction whose subject carries supersedes="+oneline.Field(notes),
				"label", c.Label, "day", c.Day, "notes", notes))
		}
	}
	lists["conflict"].More()
	for _, src := range sources {
		for _, t := range src.Toucheds {
			lists["touched"].Line(s.line("TOKENS", "TOUCHED", "", "label", t.Label, "day", t.Day, "repos", strings.Join(t.Repos, ",")))
		}
	}
	lists["touched"].More()

	days := folder.Days()
	if !*all {
		days = []string{*day}
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
			lists["mixed"].Line(s.line("TOKENS", "MIXED", "two day bases on one row; declare one export for that day",
				"date", m.Day, "model", m.Model, "repo", m.Repo, "bases", strings.Join(m.Bases, ",")))
		}
		outPath := tokens.Path(*out, d)
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
				lists["unreadable"].Line(unreadableLine(s, "TOKENS", tokens.Unreadable{Label: "out", Path: outPath, Why: readErr.Error()}))
			case readErr == nil && len(findings) > 0:
				for _, f := range findings {
					why := oneline.Escape(f.Reason)
					if f.Line > 0 {
						why = fmt.Sprintf("line %d: %s", f.Line, oneline.Escape(f.Reason))
					}
					lists["unreadable"].Line(unreadableLine(s, "TOKENS", tokens.Unreadable{Label: "out", Path: outPath, Why: why}))
				}
			case readErr == nil && len(findings) == 0:
				// Merge by source BEFORE anything else touches the file: a row no
				// declared source wrote is carried over, a row they all wrote is
				// replaced, and a row this fold can neither keep nor recompute refuses
				// the day. Rule 10 then compares the file with the merged file, so it
				// compares like with like and cannot hide an erased row when this run's
				// numbers are bigger.
				merged, retained, partials := tokens.MergeDay(old.Rows, file.Rows, declared)
				for _, pt := range partials {
					partial = true
					lists["partial"].Line(s.line("TOKENS", "PARTIAL", "this fold declared only some of the sources that wrote the row; declare every source in the file's sources= line, or fold this day into its own --out",
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
						// turns= counts the messages THIS run read and cannot be split per
						// source, so a file holding a row this run did not read is a file
						// whose turns nobody can state.
						file.Turns = tokens.Dash
					}
					dayShrinks = tokens.Shrinks(old.Totals(), file.Totals(), d)
					shrank = len(dayShrinks) > 0
				}
				// A declared source with zero samples for an explicitly selected existing
				// day is quiet: the day file still names it, and the fold prints a bounded
				// line naming it rather than letting the refusal speak only in day totals.
				for _, src := range sources {
					fedDay := slices.ContainsFunc(src.Stream, func(m tokens.Message) bool { return m.Day == d })
					if fedDay || !slices.Contains(old.Sources, src.Label) {
						continue
					}
					quiet++
					if firstQuiet == "" {
						firstQuiet = src.Label + " on " + d
					}
					lists["quiet"].Line(s.line("TOKENS", "QUIET", "a declared source has zero samples for an explicitly selected existing day",
						"label", src.Label, "day", d))
				}
			}
			// --allow-shrink is a person's word about a day going backwards. It is NOT a
			// word about a row this fold cannot compute, so it does not override a partial,
			// and not a word about replacing a malformed file. Absent and empty are one
			// state: a fold never writes a day file with no rows.
			wouldWrite = !partial && (!shrank || *allowShrink) && len(file.Rows) > 0 &&
				(readErr == nil && len(findings) == 0 || readErr != nil && os.IsNotExist(readErr))
			switch {
			case wouldWrite && *dryRun:
				daysWritten++
				rowsWritten += len(rows)
			case wouldWrite:
				if err := file.Save(*out); err != nil {
					lists["unreadable"].Line(unreadableLine(s, "TOKENS", tokens.Unreadable{Label: "out", Path: outPath, Why: err.Error()}))
				} else {
					written = true
					daysWritten++
					rowsWritten += len(rows)
				}
			}
			for _, sh := range dayShrinks {
				lists["shrank"].Line(s.line("TOKENS", "SHRANK", "a source went quiet; --allow-shrink writes it anyway",
					"date", sh.Day, "type", tokens.TypeNames[sh.Type], "file", sh.File, "now", sh.Now, "written", written))
			}
		}
		lists["day"].Line(dayLine(s, d, file, written, *dryRun, wouldWrite))
	}
	for _, kind := range []string{"unreadable", "mixed", "day", "shrank", "partial", "quiet"} {
		lists[kind].More()
	}

	n := func(kind string) int { return lists[kind].Total() }
	counts := []any{"days", daysWritten, "rows", rowsWritten, "sources", len(sources), "unreadable", n("unreadable"),
		"unparsed", n("unparsed"), "mixed", n("mixed"), "conflict", n("conflict"), "shrank", n("shrank"),
		"partial", n("partial"), "quiet", quiet}
	if *dryRun {
		counts = append(counts, "dry_run", true)
	}
	// A FOLD THAT DROPPED EVERY MESSAGE FOLDED NOTHING, AND A GATE READING THE EXIT CODE MUST
	// SEE IT. Some messages dropped is a TOKENS NOTE (the day is short and the note says
	// so); every message dropped, with none folded, is a fold that did not do its job:
	// exit 1 with the counts so the caller sees that no work was done.
	dropped, of, allDropped := allMessagesDropped(sources)
	bad := n("unreadable") > 0 || n("unparsed") > 0 || n("mixed") > 0 || n("conflict") > 0 ||
		(n("shrank") > 0 && !*allowShrink) || n("partial") > 0 || allDropped
	if allDropped {
		fmt.Fprintf(s.err(), "FOLD FAILED dropped=%d of %d: %s\n", dropped, of, allDroppedWhy)
		s.o.Why = append(s.o.Why, fmt.Sprintf("dropped=%d of %d: %s", dropped, of, allDroppedWhy))
	}
	if bad {
		fmt.Fprintf(s.err(), "TOKENS FAILED%s\n", s.factFields(counts...))
	} else {
		fmt.Fprintf(s.out(), "TOKENS OK%s\n", s.factFields(counts...))
	}
	note := remedy(sources, n("unreadable"), n("unparsed"), n("mixed"), n("conflict"), n("shrank"), n("partial"), quiet,
		*allowShrink, *dryRun, *out, mixedLabels, firstPartial, firstQuiet)
	fmt.Fprintf(s.out(), "TOKENS NOTE %s\n", oneline.Escape(note))
	s.note(note)
	copyNote(s, "TOKENS", copyNotes)
	if bad {
		return s.done(1, *max)
	}
	return s.done(0, *max)
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

// dayLine is one line per day written or refused, and the two shares on it are how a
// person sees whether the rules file is good enough. Under --dry-run nothing is written,
// and would_write says whether the real run would have written the day.
func dayLine(s *sink, day string, file *tokens.DayFile, written, dryRun, wouldWrite bool) string {
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
	kv := []any{"date", day, "rows", len(file.Rows), "models", len(models), "repos", len(repos), "turns", count(file.Turns),
		"unknown", tokens.Percent(unknown, whole) + "%", "other", tokens.Percent(other, whole) + "%",
		"rough", rough, "dashes", dashes, "nonutc", nonutc, "sources", strings.Join(file.Sources, ","), "written", written}
	if dryRun {
		kv = append(kv, "would_write", wouldWrite)
	}
	return s.line("TOKENS", "DAY", "", kv...)
}

// refuseOverlap is the refusal a detected overlap prints before any day file is
// written. TokenFold invariant OverlapRefusedBeforeWrite: the line names the two
// source labels and the duplicate count, and the remedy drops one of the two flags.
func refuseOverlap(s *sink, overlaps []tokens.Overlap) int {
	if s.json {
		whys := make([]string, len(overlaps))
		for i, o := range overlaps {
			whys[i] = overlapWhy(o)
		}
		out := tool.Refuse(whys...)
		out.Verb, out.Remedy = s.o.Verb, overlapRemedy(overlaps[0])
		return out.Render(s.stdout, true)
	}
	for _, o := range overlaps {
		writeRefusal(s.stderr, "TOKENS", overlapWhy(o), overlapRemedy(o))
	}
	return 2
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
