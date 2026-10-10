// report.go holds the report verb: its flags, its run and the helpers only it uses.

package main

import (
	"context"
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
		Token:   "REPORT",
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
			sf.declare(f, true, false)
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
				c.Want("repos", wantsRepos)
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

	failed := func(err error) *tool.Out {
		o := tool.Fail()
		o.Fact("store", "redis")
		o.Fact("err", tool.Text(err.Error()))
		return o
	}
	ls, err := openLedger(addr, user, passwordEnv)
	if err != nil {
		return failed(err)
	}
	// ignored: a deferred close on a read-only store: every read already carried its answer to the printed report
	defer func() { _ = ls.Close() }()
	totals, indexed, missing, err := ls.LedgerReport(context.Background(), month, by)
	if err != nil {
		return failed(err)
	}
	o := tool.Done()
	rows := 0
	for _, t := range totals {
		rows += t.Rows
		var kv []any
		for _, col := range record.LedgerGroupings[by] {
			switch col {
			case "day":
				kv = append(kv, "day", t.Day)
			case "model":
				kv = append(kv, "model", t.Model)
			case "repo":
				kv = append(kv, "repo", t.Repo)
			}
		}
		kv = append(kv, "rows", t.Rows)
		for i, name := range record.LedgerTypes {
			cell := tokens.Dash
			if t.Known[i] {
				cell = strconv.FormatInt(t.Tokens[i], 10)
			}
			kv = append(kv, name, cell)
		}
		o.Item("group", kv...)
	}
	if indexed == 0 {
		o.Status = tool.Failed
		o.Exit = 1
		o.Fact("month", month)
		o.Fact("source", "redis")
		o.Fact("indexed", 0)
		return o
	}
	o.Fact("month", month).
		Fact("source", "redis").
		Fact("groups", len(totals)).
		Fact("rows", rows).
		Fact("indexed", indexed).
		Fact("missing", missing)
	return o
}

func runReportLocal(c *tool.Call, sf sourceFlags, supersedes []string, now time.Time) *tool.Out {
	who := c.Str("who")
	day := c.Str("day")
	notePath := c.Str("note")
	dryRun := c.DryRun()

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

	o := tool.Done()
	o.Findings("unreadable", "unparsed", "mixed", "avg", "avg-all")

	unreadable, unparsed := 0, 0
	for _, src := range sources {
		for _, u := range src.Unreadables {
			o.ItemText("unreadable", oneline.Cap(u.Why, oneline.TailBytes), "label", u.Label, "path", u.Path)
			unreadable++
		}
	}
	for _, src := range sources {
		for _, u := range src.Unparseds {
			o.ItemText("unparsed", oneline.Cap(u.Text, oneline.TailBytes), "label", u.Label, "note", u.Note, "line", u.Line)
			unparsed++
		}
	}

	if dropped := noidAndDup(sources); dropped != "" {
		o.Note(dropped)
	}
	copyNotes(o, copyNotesList)

	rows, mixed := folder.DayRows(day)
	for _, m := range mixed {
		o.ItemText("mixed", "two day bases on one row; declare one export for that day",
			"date", m.Day, "model", m.Model, "repo", m.Repo, "bases", strings.Join(m.Bases, ","))
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
	o.Payload = body
	o.Fact("who", who)
	o.Fact("day", day)
	o.Fact("rows", lines)

	if lines == 0 || len(mixed) > 0 {
		o.Status = tool.Failed
		o.Exit = 1
		o.Fact("unreadable", unreadable)
		return o
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
	for _, a := range sortedAvg {
		allTokens += a.tokens
		allPricedTokens += a.pricedTokens
		allUsd += a.usd
		allPriced = allPriced || a.priced
		o.Item("avg", "day", day, "model", a.name, "tokens", a.tokens,
			"usd", usdCell(a.usd, a.priced), "usd_per_mtok", usdPerMtokCell(a.usd, a.pricedTokens, a.priced),
			"unpriced", a.tokens-a.pricedTokens)
	}
	o.Item("avg-all", "day", day, "tokens", allTokens,
		"usd", usdCell(allUsd, allPriced), "usd_per_mtok", usdPerMtokCell(allUsd, allPricedTokens, allPriced),
		"unpriced", allTokens-allPricedTokens)

	subject := tokens.Subject(day, stamp(now), buildVersion(), sorted)
	o.Fact("at", stamp(now)).
		Fact("build", buildVersion())
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
