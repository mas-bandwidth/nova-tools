// report.go holds the report verb: its flags, its run and the helpers only it uses.

package main

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/redisauth"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/record"
	"github.com/mas-bandwidth/nova-tools/internal/tokens"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// openLedger opens the fleet Redis at addr.
func openLedger(addr, user, passwordEnv string) (record.LedgerStore, error) {
	user, password, err := redisauth.Auth(user, passwordEnv)
	if err != nil {
		return nil, err
	}
	return record.DialLedger(addr, user, password), nil
}

func reportVerb(now time.Time) tool.Verb {
	var sf sourceFlags
	var supersedes stringList
	return tool.Verb{
		Name:    "report",
		Usage:   "report (local mode) --who <name> --day <YYYY-MM-DD> --repos <file>\nreport (store mode) --redis <host:port> --month <YYYY-MM> [--by model|repo|day|tuple] [--max <n>]",
		Example: "report --who ada --day 2026-09-11 --repos ./repos.tsv --claude bench=./transcripts",
		Detail:  "mode: local note body, printed as the tokens note artifact\nmode: Redis month summary",
		Effect:  tool.Effect("local write: --note writes the note body to that file, and --opencode copies the database into --scratch/opencode-<label>/ (replaced, and left); --dry-run names the note, copies the database only into a new directory under --scratch removed before it exits, and writes nothing; --redis reads the ledger store over the network, with or without --dry-run"),
		DryRun:  true,
		Flags: func(f *tool.Flags) {
			f.String("who", "", "name to write in each note body row")
			f.String("day", "", "one UTC day to report as YYYY-MM-DD")
			f.String("note", "", "atomically write the note body to this path")
			f.Var(&supersedes, "supersedes", "note id this report replaces; repeatable")
			f.Max()
			f.String("month", "", "month to summarize as YYYY-MM")
			f.String("by", "model", "Redis summary grouping: model, repo, day or tuple")
			f.String("redis", "", "Redis address for the store summary mode")
			f.String("user", "", "Redis username for the store summary mode")
			f.String("password-env", "", "environment variable holding the Redis password")
			sf.declare(f.FlagSet, true)
			f.Check(func(c *tool.Call) {
				redisAddr := c.Str("redis")
				monthFlag := c.Str("month")
				if redisAddr != "" {
					if monthFlag == "" {
						c.Want("month", wantsMonth)
					} else if !validMonth(monthFlag) {
						c.Problem("--month is not a month: " + monthFlag + "; it wants " + wantsMonth)
					}
					by := c.Str("by")
					if _, ok := record.LedgerGroupings[by]; !ok {
						c.Problem("--by is model, repo, day or tuple, got " + by)
					}
					return
				}
				if monthFlag != "" {
					c.Problem("--month is the store's month report; it wants --redis <host:port>")
					return
				}
				c.Want("who", wantsWho)
				day := c.Str("day")
				switch {
				case day == "":
					c.Want("day", wantsDay)
				case !tokens.ValidDay(day):
					c.Problem("--day is not a day: " + day + "; it wants " + wantsDay)
				}
				sf.check(c)
				seen := map[string]bool{}
				for _, id := range supersedes {
					switch {
					case !tokens.ValidNoteID(id):
						c.Problem("--supersedes " + id + ": it wants a note id of the shape <sender>-<12 hex>")
					case seen[id]:
						c.Problem("--supersedes names " + id + " twice; the predecessor set is a set, with no duplicate")
					}
					seen[id] = true
				}
			})
		},
		Run: func(c *tool.Call) *tool.Out {
			return runReport(c, sf, supersedes, now)
		},
	}
}

func runReport(c *tool.Call, sf sourceFlags, supersedes []string, now time.Time) *tool.Out {
	if c.Str("redis") != "" {
		return runReportStore(c)
	}
	return runReportLocal(c, sf, supersedes, now)
}

func runReportStore(c *tool.Call) *tool.Out {
	addr := c.Str("redis")
	user := c.Str("user")
	passwordEnv := c.Str("password-env")
	month := c.Str("month")
	by := c.Str("by")
	maxRows := c.Int("max")
	asJSON := c.Bool("json")

	failed := func(err error) *tool.Out {
		if !asJSON {
			fmt.Fprintf(c.Stderr, "REPORT FAILED store=redis err=%s\n", oneline.Err(err))
			return tool.Exit(1)
		}
		o := tool.Fail(err.Error())
		o.Fact("month", month)
		o.Fact("source", "redis")
		return o
	}
	ls, err := openLedger(addr, user, passwordEnv)
	if err != nil {
		return failed(err)
	}
	defer func() { _ = ls.Close() }()
	totals, indexed, missing, err := ls.LedgerReport(context.Background(), month, by)
	if err != nil {
		return failed(err)
	}
	rows := 0
	var o *tool.Out
	if asJSON {
		o = tool.Done()
	}
	for i, t := range totals {
		rows += t.Rows
		if maxRows != 0 && i >= maxRows {
			continue
		}
		var keys []string
		var kv []any
		for _, col := range record.LedgerGroupings[by] {
			switch col {
			case "day":
				keys, kv = append(keys, "day="+oneline.Field(t.Day)), append(kv, "day", t.Day)
			case "model":
				keys, kv = append(keys, "model="+oneline.Field(t.Model)), append(kv, "model", t.Model)
			case "repo":
				keys, kv = append(keys, "repo="+oneline.Field(t.Repo)), append(kv, "repo", t.Repo)
			}
		}
		line := "REPORT " + strings.Join(keys, " ") + fmt.Sprintf(" rows=%d", t.Rows)
		kv = append(kv, "rows", t.Rows)
		for i, name := range record.LedgerTypes {
			cell := tokens.Dash
			if t.Known[i] {
				cell = strconv.FormatInt(t.Tokens[i], 10)
			}
			line += " " + name + "=" + cell
			kv = append(kv, name, cell)
		}
		if asJSON {
			o.Item("group", kv...)
		} else {
			fmt.Fprintln(c.Stdout, line)
		}
	}
	if maxRows != 0 && len(totals) > maxRows {
		if asJSON {
			o.More = append(o.More, tool.More{Kind: "group", Shown: maxRows, Total: len(totals), Remedy: tool.MaxRemedy})
		} else {
			fmt.Fprintf(c.Stdout, "REPORT MORE shown=%d of=%d; raise --max (0 = all)\n", maxRows, len(totals))
		}
	}
	if indexed == 0 {
		if asJSON {
			o = tool.Fail()
			o.Fact("month", month)
			o.Fact("source", "redis")
			o.Fact("indexed", 0)
			return o
		}
		fmt.Fprintf(c.Stdout, "REPORT FAILED month=%s source=redis indexed=0\n", oneline.Field(month))
		return tool.Exit(1)
	}
	if asJSON {
		o.Fact("month", month)
		o.Fact("source", "redis")
		o.Fact("groups", len(totals))
		o.Fact("rows", rows)
		o.Fact("indexed", indexed)
		o.Fact("missing", missing)
		return o
	}
	fmt.Fprintf(c.Stdout, "REPORT OK month=%s source=redis groups=%d rows=%d indexed=%d missing=%d\n", oneline.Field(month), len(totals), rows, indexed, missing)
	return tool.Exit(0)
}

func runReportLocal(c *tool.Call, sf sourceFlags, supersedes []string, now time.Time) *tool.Out {
	who := c.Str("who")
	day := c.Str("day")
	notePath := c.Str("note")
	maxRows := c.Int("max")
	dryRun := c.DryRun()
	asJSON := c.Bool("json")

	rules, err := tokens.LoadRules(sf.repos)
	if err != nil {
		return tool.Refuse("--repos " + sf.repos + ": " + err.Error() + "; it wants " + wantsRepos)
	}
	sorted := slices.Sorted(slices.Values(supersedes))
	sources, copyNotesList := sf.read(rules, now, dryRun)
	folder := tokens.NewFolder()
	for _, src := range sources {
		for _, m := range src.Stream {
			folder.Add(src.Label, m)
		}
	}

	unreadable, unparsed := 0, 0
	for _, src := range sources {
		for _, u := range src.Unreadables {
			if !asJSON {
				fmt.Fprintln(c.Stderr, formatLine("TOKENS", "UNREADABLE", oneline.Cap(u.Why, oneline.TailBytes), "label", u.Label, "path", u.Path))
			}
			unreadable++
		}
	}
	for _, src := range sources {
		for _, u := range src.Unparseds {
			if !asJSON {
				fmt.Fprintln(c.Stderr, formatLine("TOKENS", "UNPARSED", oneline.Cap(u.Text, oneline.TailBytes), "label", u.Label, "note", u.Note, "line", u.Line))
			}
			unparsed++
		}
	}

	var o *tool.Out
	if asJSON {
		o = tool.Done()
	}

	if dropped := noidAndDup(sources); dropped != "" {
		if !asJSON {
			fmt.Fprintf(c.Stderr, "TOKENS NOTE %s\n", oneline.Escape(dropped))
		} else {
			o.Note(dropped)
		}
	}
	if !asJSON {
		copyNotesWriter(c.Stderr, "TOKENS", copyNotesList)
	} else {
		copyNotes(o, copyNotesList)
	}

	rows, mixed := folder.DayRows(day)
	for _, m := range mixed {
		if !asJSON {
			fmt.Fprintln(c.Stderr, formatLine("TOKENS", "MIXED", "two day bases on one row; declare one export for that day",
				"date", m.Day, "model", m.Model, "repo", m.Repo, "bases", strings.Join(m.Bases, ",")))
		}
	}

	var rendered []string
	for _, row := range rows {
		for t := tokens.Type(0); t < tokens.NTypes; t++ {
			v, ok := row.Counts.Get(t)
			if !ok {
				continue
			}
			rendered = append(rendered, tokens.BodyLine(day, who, row.Model, row.Repo, t, v, row.Basis()))
		}
	}
	lines := len(rendered)
	body := ""
	if lines > 0 {
		body = strings.Join(rendered, "\n") + "\n"
	}

	if asJSON {
		o.Payload = body
		o.Fact("who", who)
		o.Fact("day", day)
		o.Fact("rows", lines)
	}

	if lines == 0 || len(mixed) > 0 {
		if !asJSON {
			if lines > 0 {
				fmt.Fprint(c.Stdout, body)
			}
			fmt.Fprintf(c.Stderr, "REPORT FAILED who=%s day=%s rows=%d unreadable=%d\n",
				oneline.Field(who), oneline.Field(day), lines, unreadable)
			return tool.Exit(1)
		}
		o.Status = tool.Failed
		o.Exit = 1
		o.Fact("unreadable", unreadable)
		return o
	}

	if !asJSON {
		fmt.Fprint(c.Stdout, body)
	}

	if notePath != "" {
		write := func() error { return atomicfile.Write(filepath.Clean(notePath), []byte(body), 0o644) }
		if dryRun {
			write = func() error { return atomicfile.Check(filepath.Clean(notePath), 0o644) }
		}
		if err := write(); err != nil {
			return tool.Refuse("--note " + notePath + ": " + err.Error())
		}
	}

	type modelAvg struct {
		name         string
		tokens       int64
		pricedTokens int64
		usd          int64
		priced       bool
	}
	avgs := map[string]*modelAvg{}
	for _, row := range rows {
		name := row.Model
		if row.Provider != "" {
			name = row.Provider + "/" + row.Model
		}
		a, ok := avgs[name]
		if !ok {
			a = &modelAvg{name: name}
			avgs[name] = a
		}
		a.tokens += row.Counts.Billed()
		a.pricedTokens += row.PricedTokens
		a.usd += row.Usd
		a.priced = a.priced || row.Priced
	}
	sortedAvg := slices.Collect(maps.Values(avgs))
	sort.Slice(sortedAvg, func(i, j int) bool {
		pi := avgRate(sortedAvg[i].usd, sortedAvg[i].pricedTokens, sortedAvg[i].priced)
		pj := avgRate(sortedAvg[j].usd, sortedAvg[j].pricedTokens, sortedAvg[j].priced)
		if pi != pj {
			return pi > pj
		}
		return sortedAvg[i].name < sortedAvg[j].name
	})

	var allTokens, allPricedTokens, allUsd int64
	allPriced := false
	for i, a := range sortedAvg {
		allTokens += a.tokens
		allPricedTokens += a.pricedTokens
		allUsd += a.usd
		allPriced = allPriced || a.priced
		kv := []any{"day", day, "model", a.name, "tokens", a.tokens,
			"usd", usdCell(a.usd, a.priced), "usd_per_mtok", usdPerMtokCell(a.usd, a.pricedTokens, a.priced),
			"unpriced", a.tokens - a.pricedTokens}
		if asJSON {
			o.Item("avg", kv...)
		} else if maxRows == 0 || i < maxRows {
			fmt.Fprintln(c.Stderr, formatLine("TOKENS", "AVG", "", kv...))
		}
	}
	if !asJSON && maxRows != 0 && len(sortedAvg) > maxRows {
		fmt.Fprintf(c.Stderr, "TOKENS MORE shown=%d of=%d; raise --max (0 = all)\n", maxRows, len(sortedAvg))
	} else if asJSON && maxRows != 0 && len(sortedAvg) > maxRows {
		o.More = append(o.More, tool.More{Kind: "avg", Shown: maxRows, Total: len(sortedAvg), Remedy: maxRemedy("report")})
	}

	avgAllKV := []any{"day", day, "tokens", allTokens,
		"usd", usdCell(allUsd, allPriced), "usd_per_mtok", usdPerMtokCell(allUsd, allPricedTokens, allPriced),
		"unpriced", allTokens - allPricedTokens}
	if asJSON {
		o.Item("avg-all", avgAllKV...)
	} else {
		fmt.Fprintln(c.Stderr, formatLine("TOKENS", "AVG-ALL", "", avgAllKV...))
	}

	subject := tokens.Subject(day, stamp(now), buildVersion(), sorted)

	if asJSON {
		o.Fact("at", stamp(now))
		o.Fact("build", buildVersion())
		if dryRun {
			o.Fact("dry_run", true)
			if notePath != "" {
				o.Fact("note", notePath)
			}
		}
		o.Fact("subject", tool.Text(subject))
		if unreadable > 0 || unparsed > 0 {
			o.Status = tool.Failed
			o.Exit = 1
		}
		return o
	}

	if unreadable > 0 || unparsed > 0 {
		fmt.Fprintf(c.Stderr, "REPORT FAILED who=%s day=%s rows=%d at=%s build=%s%s subject=%s\n",
			oneline.Field(who), oneline.Field(day), lines, oneline.Field(stamp(now)),
			oneline.Field(buildVersion()), dryRunFields(dryRun, "note", notePath), oneline.Quote(subject))
		return tool.Exit(1)
	}
	fmt.Fprintf(c.Stderr, "REPORT OK who=%s day=%s rows=%d at=%s build=%s%s subject=%s\n",
		oneline.Field(who), oneline.Field(day), lines, oneline.Field(stamp(now)),
		oneline.Field(buildVersion()), dryRunFields(dryRun, "note", notePath), oneline.Quote(subject))
	return tool.Exit(0)
}

func avgRate(usdMicro, tokens int64, priced bool) float64 {
	if tokens == 0 || !priced {
		return -1
	}
	return float64(usdMicro) / float64(tokens)
}

// usdCell is a cost as a field: the dollars, or - when no source reported one. The cost
// type carries that same value into --json as null for the dash.
func usdCell(micro int64, priced bool) cost {
	if !priced {
		return cost(tokens.Dash)
	}
	return cost(tokens.Usd(micro))
}

// usdPerMtokCell is the blended rate as a field: - when no source reported a cost, or the
// model had no tokens to divide by. The cost type makes that same dash null in --json.
func usdPerMtokCell(micro, n int64, priced bool) cost {
	if !priced {
		return cost(tokens.Dash)
	}
	return cost(tokens.UsdPerMtok(micro, n))
}
