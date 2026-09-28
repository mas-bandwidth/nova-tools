package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/tokens"
)

func collateLine(res *tokens.CollateDayResult) string {
	srcs := "-"
	if len(res.Sources) > 0 {
		srcs = strings.Join(res.Sources, ",")
	}
	turns := "-"
	if res.Turns != "" {
		turns = res.Turns
	}
	return fmt.Sprintf("TOKENS COLLATE day=%s written=%t sources=%s turns=%s rows=%d",
		oneline.Field(res.Day), res.Written, oneline.Field(srcs), oneline.Field(turns), res.Rows)
}

// cmdCollate coordinates daily token log aggregation into reports/tokens/<day>.tsv
// with source merging, Rule 10 shrink refusal, flock-based file locking,
// atomic writes via atomicfile.WriteFile, and staleness detection.
func cmdCollate(args []string, stdout, stderr io.Writer, now time.Time) int {
	fs := newFlagSet("collate")
	out := fs.String("out", "", "")
	day := fs.String("day", "", "")
	today := fs.Bool("today", false, "")
	yesterday := fs.Bool("yesterday", false, "")
	all := fs.Bool("all", false, "")
	allowShrink := fs.Bool("allow-shrink", false, "")
	unitsPath := fs.String("units", "", "")
	max := fs.Int("max", bounded.Default, "")
	maxStalenessHours := fs.Int("max-staleness", 36, "")
	strict := fs.Bool("strict", false, "")
	noSpend := fs.String("no-spend", "", "")

	var sf sourceFlags
	sf.declare(fs, true)
	if err := verbflag.Parse(fs, args); err != nil {
		return refuse(stderr, " collate", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if code, refused := noPositional(fs, stderr, "collate"); refused {
		return code
	}

	r := &refusals{token: "TOKENS"}
	if *out == "" {
		*out = "reports/tokens"
	}
	fi, statErr := os.Stat(*out)
	if statErr != nil && os.IsNotExist(statErr) && *out == "reports/tokens" {
		if mkErr := os.MkdirAll(*out, 0o755); mkErr == nil {
			statErr = nil
			fi, _ = os.Stat(*out)
		}
	}
	if statErr != nil || (fi != nil && !fi.IsDir()) {
		if statErr != nil && os.IsNotExist(statErr) {
			r.add("--out does not exist: " + *out + "; it wants " + wantsOut)
		} else if statErr != nil {
			r.add("--out " + *out + ": " + statErr.Error() + "; it wants " + wantsOut)
		} else {
			r.add("--out is not a directory: " + *out + "; it wants " + wantsOut)
		}
	}

	dateFlagsCount := 0
	if *day != "" {
		dateFlagsCount++
	}
	if *today {
		dateFlagsCount++
	}
	if *yesterday {
		dateFlagsCount++
	}
	if *all {
		dateFlagsCount++
	}
	if dateFlagsCount > 1 {
		r.add("--day, --today, --yesterday, and --all are mutually exclusive; give one")
	}
	if *day != "" && !tokens.ValidDay(*day) {
		r.add("--day is not a day: " + *day + "; it wants " + wantsDay)
	}

	sf.check(r)
	checkMax(r, *max)
	if *strict && strings.TrimSpace(*noSpend) != "" {
		r.add("--strict and --no-spend are two answers to one question; give one")
	}
	if len(r.list) > 0 {
		return r.print(stderr)
	}

	rules, err := tokens.LoadRules(sf.repos)
	if err != nil {
		r.add("--repos " + sf.repos + ": " + err.Error() + "; it wants " + wantsRepos)
		return r.print(stderr)
	}
	var units *tokens.Units
	if *unitsPath != "" {
		units, err = tokens.LoadUnits(*unitsPath)
		if err != nil {
			r.add("--units " + *unitsPath + ": " + err.Error() + "; it wants " + wantsUnits)
			return r.print(stderr)
		}
	}

	release, err := tokens.TakeFoldLock(*out, tokens.LockWait)
	if err != nil {
		r.add(err.Error())
		return r.print(stderr)
	}
	defer release()

	sources := sf.readWithUnits(rules, units, now)
	folder := tokens.NewFolder()
	for _, s := range sources {
		for _, m := range s.Stream {
			folder.Add(s.Label, m)
		}
	}

	srcList := bounded.Capped(stdout, *max, "TOKENS", "source", maxRemedy("collate"))
	unreadable := bounded.Capped(stderr, *max, "TOKENS", "unreadable", maxRemedy("collate"))
	unparsed := bounded.Capped(stderr, *max, "TOKENS", "unparsed", maxRemedy("collate"))
	superseded := bounded.Capped(stdout, *max, "TOKENS", "superseded", maxRemedy("collate"))
	conflicts := bounded.Capped(stderr, *max, "TOKENS", "conflict", maxRemedy("collate"))
	touched := bounded.Capped(stdout, *max, "TOKENS", "touched", maxRemedy("collate"))
	mixedList := bounded.Capped(stderr, *max, "TOKENS", "mixed", maxRemedy("collate"))
	collateList := bounded.Capped(stdout, *max, "TOKENS", "collate", maxRemedy("collate"))
	shrankList := bounded.Capped(stderr, *max, "TOKENS", "shrank", maxRemedy("collate"))
	partialList := bounded.Capped(stderr, *max, "TOKENS", "partial", maxRemedy("collate"))
	quietList := bounded.Capped(stderr, *max, "TOKENS", "quiet", maxRemedy("collate"))
	staleList := bounded.Capped(stderr, *max, "TOKENS", "stale", maxRemedy("collate"))

	conflictDays := map[string]bool{}
	declared := make([]string, 0, len(sources))
	for _, s := range sources {
		declared = append(declared, s.Label)
		srcList.Line(sourceLine("TOKENS", s))
	}
	srcList.More()
	for _, s := range sources {
		for _, u := range s.Unreadables {
			unreadable.Line(unreadableLine("TOKENS", u))
		}
	}
	for _, s := range sources {
		for _, u := range s.Unparseds {
			unparsed.Line(unparsedLine("TOKENS", u))
		}
	}
	unparsed.More()
	for _, s := range sources {
		for _, sp := range s.Supersededs {
			superseded.Line(fmt.Sprintf("TOKENS SUPERSEDED label=%s note=%s by=%s day=%s",
				oneline.Field(sp.Label), oneline.Field(sp.Note), oneline.Field(sp.By), oneline.Field(sp.Day)))
		}
	}
	superseded.More()
	for _, s := range sources {
		for _, c := range s.Conflicts {
			conflictDays[c.Day] = true
			conflicts.Line(fmt.Sprintf("TOKENS CONFLICT label=%s day=%s notes=%s: competing reports; send a correction whose subject carries supersedes=%s",
				oneline.Field(c.Label), oneline.Field(c.Day), oneline.Field(strings.Join(c.Notes, ",")),
				oneline.Field(strings.Join(c.Notes, ","))))
		}
	}
	conflicts.More()
	for _, s := range sources {
		for _, t := range s.Toucheds {
			touched.Line(fmt.Sprintf("TOKENS TOUCHED label=%s day=%s repos=%s",
				oneline.Field(t.Label), oneline.Field(t.Day), oneline.Field(strings.Join(t.Repos, ","))))
		}
	}
	touched.More()

	var days []string
	switch {
	case *day != "":
		days = []string{*day}
	case *today:
		days = []string{now.UTC().Format("2006-01-02")}
	case *yesterday:
		days = []string{now.UTC().AddDate(0, 0, -1).Format("2006-01-02")}
	default:
		days = folder.Days()
	}

	daysWritten, rowsWritten, quiet := 0, 0, 0
	hasPartial := false
	hasShrank := false

	for _, d := range days {
		rows, mixed := folder.DayRows(d)
		for _, m := range mixed {
			mixedList.Line(fmt.Sprintf("TOKENS MIXED date=%s model=%s repo=%s bases=%s: two day bases on one row; declare one export for that day",
				oneline.Field(m.Day), oneline.Field(m.Model), oneline.Field(m.Repo), oneline.Field(strings.Join(m.Bases, ","))))
		}
		if conflictDays[d] {
			continue
		}
		turns := tokens.Dash
		if n, ok := folder.Turns(d); ok {
			turns = strconv.Itoa(n)
		}
		res, err := tokens.CollateDay(*out, d, rows, turns, declared, *allowShrink, buildVersion(), now)
		if err != nil {
			unreadable.Line(unreadableLine("TOKENS", tokens.Unreadable{Label: "out", Path: tokens.Path(*out, d), Why: err.Error()}))
			continue
		}
		for _, u := range res.Unreadable {
			unreadable.Line(unreadableLine("TOKENS", u))
		}
		if res.Partial {
			hasPartial = true
			for _, pt := range res.Partials {
				partialList.Line(fmt.Sprintf("TOKENS PARTIAL date=%s model=%s repo=%s sources=%s folded=%s written=false: this fold declared only some of the sources that wrote the row; declare every source in the file's sources= line, or fold this day into its own --out",
					oneline.Field(pt.Day), oneline.Field(pt.Model), oneline.Field(pt.Repo),
					oneline.Field(strings.Join(pt.Sources, ",")), oneline.Field(strings.Join(pt.Folded, ","))))
			}
		}
		if res.Shrank {
			hasShrank = true
			for _, sh := range res.Shrinks {
				shrankList.Line(fmt.Sprintf("TOKENS SHRANK date=%s type=%s file=%s now=%s written=%t: a source went quiet; --allow-shrink writes it anyway",
					oneline.Field(sh.Day), oneline.Field(tokens.TypeNames[sh.Type]),
					oneline.Field(sh.File), oneline.Field(sh.Now), res.Written))
			}
		}
		if res.Written {
			daysWritten++
			rowsWritten += res.Rows
		}
		oldPath := tokens.Path(*out, d)
		if old, findings, readErr := tokens.ReadDayFile(oldPath); readErr == nil && len(findings) == 0 {
			for _, s := range sources {
				if sourceNamedDay(s, d) || !dayNames(old.Sources, s.Label) {
					continue
				}
				quiet++
				quietList.Line(fmt.Sprintf("TOKENS QUIET label=%s day=%s: a declared source has zero samples for an explicitly selected existing day",
					oneline.Field(s.Label), oneline.Field(d)))
			}
		}
		if res.Written || len(res.Sources) > 0 || res.Rows > 0 {
			collateList.Line(collateLine(res))
		}
	}
	unreadable.More()
	mixedList.More()
	collateList.More()
	shrankList.More()
	partialList.More()
	quietList.More()

	checkOpt := tokens.CheckOptions{Strict: *strict}
	if strings.TrimSpace(*noSpend) != "" {
		if ns, nsErr := tokens.ReadNoSpendFile(*noSpend); nsErr == nil {
			checkOpt.NoSpend = ns
		}
	}
	chkRes, _ := tokens.Check(*out, checkOpt)
	lastDay := ""
	if chkRes != nil {
		lastDay = chkRes.Last
	}

	staleCount := 0
	hasActivity := len(folder.Days()) > 0
	maxStaleness := time.Duration(*maxStalenessHours) * time.Hour
	stale, expDay, thHours := tokens.CheckStaleness(lastDay, now, maxStaleness, hasActivity)
	if stale {
		staleCount = 1
		staleList.Line(fmt.Sprintf("TOKENS STALE last=%s expected=%s threshold=%dh: day files have ceased advancing",
			oneline.Field(orDashText(lastDay)), oneline.Field(expDay), thHours))
	}
	staleList.More()

	currentMonth := now.UTC().Format("2006-01")
	var sumInput, sumOutput int64
	if s, sumErr := tokens.SumMonth(*out, currentMonth); sumErr == nil && s != nil && s.Total != nil {
		sumInput = s.Total.Totals[tokens.Input]
		sumOutput = s.Total.Totals[tokens.Output]
	}

	bad := unreadable.Total() > 0 || unparsed.Total() > 0 || mixedList.Total() > 0 ||
		conflicts.Total() > 0 || (hasShrank && !*allowShrink) || hasPartial || staleCount > 0
	if bad {
		fmt.Fprintf(stderr, "COLLATE FAIL days=%d unreadable=%d partial=%d shrank=%d stale=%d\n",
			daysWritten, unreadable.Total(), partialList.Total(), shrankList.Total(), staleCount)
		return 1
	}
	fmt.Fprintf(stdout, "COLLATE OK days=%d rows=%d sum_input=%d sum_output=%d ledger=%s published=%s\n",
		daysWritten, rowsWritten, sumInput, sumOutput, oneline.Field("skipped"), oneline.Field("skipped"))
	return 0
}
