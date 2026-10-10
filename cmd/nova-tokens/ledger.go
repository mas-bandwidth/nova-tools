package main

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/redisauth"
	"github.com/mas-bandwidth/nova-tools/internal/record"
	"github.com/mas-bandwidth/nova-tools/internal/tokens"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

const wantsRedis = "the fleet Redis host:port whose tokens:ledger:<day> hashes this reads or writes"

// ledgerEntries turns one day file into its ledger rows: the card is `-` (a day file names
// no unit, and the store's key keeps the slot), the provider is the source kinds, and two
// rows under one key are summed with the fold's per-type rule.
func ledgerEntries(d tokens.DayFile) []record.LedgerEntry {
	type acc struct {
		e       record.LedgerEntry
		c       tokens.Counts
		sources map[string]bool
	}
	byKey := map[[3]string]*acc{}
	var order [][3]string
	for _, r := range d.Rows {
		card := tokens.Dash
		k := [3]string{card, r.Model, r.Repo}
		a := byKey[k]
		if a == nil {
			a = &acc{e: record.LedgerEntry{Day: d.Day, Card: card, Model: r.Model, Repo: r.Repo}, sources: map[string]bool{}}
			byKey[k] = a
			order = append(order, k)
		}
		a.c.Add(r.Counts)
		a.e.Rough += r.Rough
		for _, s := range r.Sources {
			a.sources[s] = true
		}
	}
	out := make([]record.LedgerEntry, 0, len(order))
	for _, k := range order {
		a := byKey[k]
		for ty := tokens.Type(0); ty < tokens.NTypes; ty++ {
			a.e.Tokens[ty], a.e.Known[ty] = a.c.Get(ty)
		}
		var srcs []string
		kinds := map[string]bool{}
		for s := range a.sources {
			srcs = append(srcs, s)
			kind, _, _ := strings.Cut(s, ":")
			kinds[kind] = true
		}
		sort.Strings(srcs)
		a.e.Sources = strings.Join(srcs, ",")
		a.e.Provider = strings.Join(slices.Sorted(maps.Keys(kinds)), ",")
		out = append(out, a.e)
	}
	return out
}

func ledgerVerb() tool.Verb {
	return tool.Verb{
		Name:   "ledger",
		Token:  "LEDGER",
		Usage:  "ledger --out <dir> (--day <YYYY-MM-DD> | --month <YYYY-MM>) --redis <host:port>\n                      [--user <name>] [--password-env <NAME>] [--dry-run]",
		Effect: "delivery: writes each day file's rows to the Redis store at --redis (tokens:ledger:<day>); --dry-run reads the day files, prints what it would write, and dials no store",
		DryRun: true,
		Flags: func(f *tool.Flags) {
			f.Required("out", wantsOut)
			f.String("day", "", "one UTC day to index as YYYY-MM-DD")
			f.String("month", "", "month of day files to index as YYYY-MM")
			f.Required("redis", wantsRedis)
			f.String("user", "", "Redis username for the ledger store")
			f.String("password-env", "", "environment variable holding the Redis password")
			f.Check(func(c *tool.Call) {
				day, month := c.Str("day"), c.Str("month")
				switch {
				case day == "" && month == "":
					c.Problem("--day or --month is required; it wants " + wantsDay + " or " + wantsMonth + "; refusing to guess")
				case day != "" && month != "":
					c.Problem("--day and --month are one or the other")
				case day != "" && !tokens.ValidDay(day):
					c.Problem("--day is not a day: " + day + "; it wants " + wantsDay)
				case month != "" && !validMonth(month):
					c.Problem("--month is not a month: " + month + "; it wants " + wantsMonth)
				}
			})
		},
		Run: func(c *tool.Call) *tool.Out {
			return runLedger(c)
		},
	}
}

// runLedger indexes the day files of one day or one month into tokens:ledger:<day>. Each day is
// replaced whole, so indexing twice is the table indexing once. It reads the day files and
// writes nothing beside them. Under --dry-run it reads and checks the same day files, prints
// the rows it would write, and dials no store.
func runLedger(c *tool.Call) *tool.Out {
	outDir := c.Str("out")
	day := c.Str("day")
	month := c.Str("month")
	addr := c.Str("redis")
	user := c.Str("user")
	passwordEnv := c.Str("password-env")
	dryRun := c.DryRun()

	var paths []string
	if day != "" {
		paths = []string{tokens.Path(outDir, day)}
	} else {
		matches, err := filepath.Glob(filepath.Join(outDir, month+"-*"+tokens.FileSuffix))
		if err != nil {
			return tool.Refuse("--out " + outDir + ": " + err.Error())
		}
		for _, m := range matches {
			if tokens.ValidDay(strings.TrimSuffix(filepath.Base(m), tokens.FileSuffix)) {
				paths = append(paths, m)
			}
		}
		sort.Strings(paths)
	}

	o := tool.Done()
	seatUser, password, err := redisauth.Auth(user, passwordEnv)
	if err != nil {
		f := tool.Fail(err.Error())
		f.Fact("store", "redis")
		return f
	}
	var ls record.LedgerStore
	if !dryRun {
		ls = record.DialLedger(addr, seatUser, password)
		// ignored: a deferred close of a store whose batch was applied in one answered round trip: the close holds nothing the answer has not already reported
		defer func() { _ = ls.Close() }()
	}
	days, rows, bad := 0, 0, 0
	type dayResult struct {
		day  string
		rows int
		bad  bool
		why  string
	}
	results := make([]dayResult, 0, len(paths))
	var batch []record.LedgerDay
	for _, p := range paths {
		name := strings.TrimSuffix(filepath.Base(p), tokens.FileSuffix)
		d, findings, err := tokens.ReadDayFile(p)
		if err != nil {
			bad++
			why := err.Error()
			if os.IsNotExist(err) {
				why = "no day file; fold --day " + name + " first"
			}
			results = append(results, dayResult{day: name, bad: true, why: why})
			continue
		}
		if len(findings) > 0 {
			bad++
			results = append(results, dayResult{day: name, bad: true, why: findings[0].Reason})
			continue
		}
		entries := ledgerEntries(d)
		batch = append(batch, record.LedgerDay{Day: d.Day, Entries: entries})
		results = append(results, dayResult{day: d.Day, rows: len(entries)})
		days++
		rows += len(entries)
	}
	if len(batch) > 0 && !dryRun {
		if err := ls.ReplaceLedgerDays(context.Background(), batch); err != nil {
			f := tool.Fail(err.Error())
			if day != "" {
				f.Fact("day", day)
			} else {
				f.Fact("store", "redis")
			}
			return f
		}
	}

	if bad > 0 || days == 0 {
		o.Status = tool.Failed
		o.Exit = 1
	}
	for _, res := range results {
		if res.bad {
			o.ItemText("failed", res.why, "day", res.day)
		} else {
			o.Item("day", "day", res.day, "rows", res.rows)
		}
	}
	scope, value := "day", day
	if day == "" {
		scope, value = "month", month
	}
	o.Fact(scope, value).
		Fact("days", days).
		Fact("rows", rows).
		Fact("bad", bad)
	if dryRun {
		o.Fact("dry_run", true)
	}
	return o
}
