package main

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/redisauth"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/record"
	"github.com/mas-bandwidth/nova-tools/internal/tokens"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// The token ledger (docs/SPEC-STATE.md): `ledger` writes each folded day to
// tokens:ledger:<day> as the day files' index, and `report --redis` is the monthly report
// as one GROUP BY over the month's day hashes.
// The fold is untouched and the day TSVs stay the record; the ledger is what a month query
// reads instead of every file. The key layout is internal/record's package comment.

// openLedger opens the Redis store at addr. The seat is the one every nova tool dials with
// (redisauth.Auth): --user, else NOVA_SPRINT_REDIS_USER; the password is never a flag,
// it is the variable --password-env names, else (for a user) NOVA_SPRINT_REDIS_PASSWORD_ENV's
// or NOVA_REDIS_BENCH_PASSWORD. With no user, no variable is consulted unless --password-env
// names one. Dialing does not ping.
func openLedger(addr, user, passwordEnv string) (record.LedgerStore, error) {
	user, password, err := redisauth.Auth(user, passwordEnv)
	if err != nil {
		return nil, err
	}
	return record.DialLedger(addr, user, password), nil
}

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

func cmdLedger() tool.Verb {
	return tool.Verb{
		Name:   "ledger",
		Usage:  "ledger --out <dir> (--day <YYYY-MM-DD> | --month <YYYY-MM>) --redis <host:port> [--user <name>] [--password-env <NAME>] [--dry-run]",
		Effect: "delivery: writes each day file's rows to the Redis store at --redis (tokens:ledger:<day>); --dry-run reads the day files, prints what it would write, and dials no store",
		Detail: `ledger indexes the day files of one day or one month into tokens:ledger:<day>, each day
replaced whole. The store's ACL user is --user, else NOVA_SPRINT_REDIS_USER; its password
is in the variable --password-env names, else (with a user) the one
NOVA_SPRINT_REDIS_PASSWORD_ENV names, else NOVA_REDIS_BENCH_PASSWORD. The password is
never a flag.`,
		Flags: func(f *tool.Flags) {
			f.Required("out", wantsOut)
			f.String("day", "", "one UTC day to index as YYYY-MM-DD")
			f.String("month", "", "month of day files to index as YYYY-MM")
			f.Required("redis", wantsRedis)
			f.String("user", "", "Redis username for the ledger store")
			f.String("password-env", "", "environment variable holding the Redis password")
			f.Bool("dry-run", false, "read the day files and print the rows that would be written, and dial no store")
			f.Check(func(c *tool.Call) {
				switch day, month := c.Str("day"), c.Str("month"); {
				case day == "" && month == "":
					c.Problem("--day or --month is required; it wants " + wantsDay + " or " + wantsMonth + "; refusing to guess")
				case day != "" && month != "":
					c.Problem("--day and --month are one or the other")
				case day != "" && !tokens.ValidDay(day):
					c.Problem("--day is not a day: " + day + "; it wants " + wantsDay)
				case month != "" && !tokens.ValidMonth(month):
					c.Problem("--month is not a month: " + month + "; it wants " + wantsMonth)
				}
			})
		},
		Run: ledger,
	}
}

// ledger indexes the day files of one day or one month into tokens:ledger:<day>. Each day is
// replaced whole, so indexing twice is the table indexing once. It reads the day files and
// writes nothing beside them. Under --dry-run it reads and checks the same day files, prints
// the rows it would write, and dials no store.
func ledger(c *tool.Call) *tool.Out {
	dryRun := c.DryRun()
	out, day, month := c.Str("out"), c.Str("day"), c.Str("month")
	s := newSink(c, "ledger")
	var paths []string
	if day != "" {
		paths = []string{tokens.Path(out, day)}
	} else {
		matches, err := filepath.Glob(filepath.Join(out, month+"-*"+tokens.FileSuffix))
		if err != nil {
			return tool.Refuse("--out " + out + ": " + err.Error())
		}
		for _, m := range matches {
			if tokens.ValidDay(strings.TrimSuffix(filepath.Base(m), tokens.FileSuffix)) {
				paths = append(paths, m)
			}
		}
		sort.Strings(paths)
	}
	// The seat is resolved under --dry-run too, so a dry run refuses a login the real run
	// would refuse; only the dial and the write are skipped.
	seatUser, password, err := redisauth.Auth(c.Str("user"), c.Str("password-env"))
	if err != nil {
		return ledgerFailed(s, "store", "redis", err)
	}
	var ls record.LedgerStore
	if !dryRun {
		ls = record.DialLedger(c.Str("redis"), seatUser, password)
		defer ls.Close()
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
			if day != "" {
				return ledgerFailed(s, "day", day, err)
			}
			return ledgerFailed(s, "store", "redis", err)
		}
	}
	for _, res := range results {
		if res.bad {
			fmt.Fprintf(s.out(), "LEDGER BAD day=%s why=%s\n", oneline.Field(res.day), oneline.Escape(res.why))
			s.item("bad", "day", res.day, "why", tool.Text(res.why))
		} else {
			fmt.Fprintf(s.out(), "LEDGER day=%s rows=%d\n", oneline.Field(res.day), res.rows)
			s.item("day", "day", res.day, "rows", res.rows)
		}
	}
	verdict, code := "OK", 0
	if bad > 0 || days == 0 {
		verdict, code = "NO", 1
	}
	scope, value := "day", day
	if day == "" {
		scope, value = "month", month
	}
	fmt.Fprintf(s.out(), "LEDGER %s%s%s\n", oneline.Field(verdict),
		s.factFields(scope, value, "days", days, "rows", rows, "bad", bad), s.dryRunFields(dryRun))
	return s.done(code, 0)
}

// ledgerFailed is the store that did not answer: one LEDGER FAILED line, exit 1.
func ledgerFailed(s *sink, key, value string, err error) *tool.Out {
	fmt.Fprintf(s.err(), "LEDGER FAILED %s=%s err=%s\n", oneline.Field(key), oneline.Field(value), oneline.Err(err))
	s.fact(key, value)
	s.o.Why = append(s.o.Why, err.Error())
	return s.done(1, 0)
}

// reportStore is `report --redis`: the month's ledger grouped by model, repo,
// day, or the (day, model, repo) tuple, every one of the five types apart and a dash where
// no row reported a type.
func reportStore(c *tool.Call, s *sink) *tool.Out {
	month, by, max := c.Str("month"), c.Str("by"), c.Int("max")
	failed := func(err error) *tool.Out {
		fmt.Fprintf(s.err(), "REPORT FAILED store=redis err=%s\n", oneline.Err(err))
		s.o.Why = append(s.o.Why, err.Error())
		return s.done(1, 0)
	}
	ls, err := openLedger(c.Str("redis"), c.Str("user"), c.Str("password-env"))
	if err != nil {
		return failed(err)
	}
	defer ls.Close()
	totals, indexed, missing, err := ls.LedgerReport(context.Background(), month, by)
	if err != nil {
		return failed(err)
	}
	rows := 0
	for i, t := range totals {
		rows += t.Rows
		if max != 0 && i >= max {
			continue
		}
		var keys []string
		var kv []any
		for _, c := range record.LedgerGroupings[by] {
			switch c {
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
		fmt.Fprintln(s.out(), line)
		s.item("group", kv...)
	}
	if max != 0 && len(totals) > max {
		fmt.Fprintf(s.out(), "REPORT MORE shown=%d of=%d; raise --max (0 = all)\n", max, len(totals))
		s.o.More = append(s.o.More, tool.More{Kind: "group", Shown: max, Total: len(totals), Remedy: tool.MaxRemedy})
	}
	s.fact("month", month)
	s.fact("source", "redis")
	if indexed == 0 {
		fmt.Fprintf(s.out(), "REPORT NO month=%s source=redis indexed=0\n", oneline.Field(month))
		s.fact("indexed", 0)
		return s.done(1, 0)
	}
	fmt.Fprintf(s.out(), "REPORT OK month=%s source=redis groups=%d rows=%d indexed=%d missing=%d\n", oneline.Field(month), len(totals), rows, indexed, missing)
	s.fact("groups", len(totals))
	s.fact("rows", rows)
	s.fact("indexed", indexed)
	s.fact("missing", missing)
	return s.done(0, 0)
}
