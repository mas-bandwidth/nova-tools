package main

import (
	"context"
	"fmt"
	"io"
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

// The token ledger (docs/SPEC-STATE.md test 17, #2201; recut of #3243 on Redis under
// #2623): `ledger` writes each folded day to tokens:ledger:<day> as the day files' index,
// and `report --redis` is the monthly report as one GROUP BY over the month's day hashes.
// The fold is untouched and the day TSVs stay the record; the ledger is what a month query
// reads instead of every file. The key layout is internal/record's package comment.

// openLedger opens the fleet Redis at addr. The seat is the one every nova tool dials with
// (redisauth.Auth, #3461): --user, else NOVA_SPRINT_REDIS_USER; the password is never a flag,
// it is the variable --password-env names, else (for a user) NOVA_SPRINT_REDIS_PASSWORD_ENV's
// or NOVA_REDIS_BENCH_PASSWORD. With no user, no variable is consulted unless --password-env
// names one. Dialing does not ping (#7fbdefecf56e).
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

// cmdLedger indexes the day files of one day or one month into tokens:ledger:<day>. Each day is
// replaced whole, so indexing twice is the table indexing once. It reads the day files and
// writes nothing beside them. Under --dry-run it reads and checks the same day files, prints
// the rows it would write, and dials no store.
func cmdLedger(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("ledger")
	out := fs.String("out", "", "directory containing daily token files")
	day := fs.String("day", "", "one UTC day to index as YYYY-MM-DD")
	month := fs.String("month", "", "month of day files to index as YYYY-MM")
	addr := fs.String("redis", "", "Redis address for the ledger store")
	user := fs.String("user", "", "Redis username for the ledger store")
	passwordEnv := fs.String("password-env", "", "environment variable holding the Redis password")
	dryRun := fs.Bool("dry-run", false, "read the day files and print the rows that would be written, and dial no store")
	s, code, ok := start(fs, args, "LEDGER", stdout, stderr)
	if !ok {
		return code
	}
	r := &refusals{token: "LEDGER", s: s}
	r.required("out", *out, wantsOut)
	r.required("redis", *addr, wantsRedis)
	switch {
	case *day == "" && *month == "":
		r.add("--day or --month is required; it wants " + wantsDay + " or " + wantsMonth + "; refusing to guess")
	case *day != "" && *month != "":
		r.add("--day and --month are one or the other")
	case *day != "" && !tokens.ValidDay(*day):
		r.add("--day is not a day: " + *day + "; it wants " + wantsDay)
	case *month != "" && !validMonth(*month):
		r.add("--month is not a month: " + *month + "; it wants " + wantsMonth)
	}
	if len(r.list) > 0 {
		return r.print(stderr)
	}
	var paths []string
	if *day != "" {
		paths = []string{tokens.Path(*out, *day)}
	} else {
		matches, err := filepath.Glob(filepath.Join(*out, *month+"-*"+tokens.FileSuffix))
		if err != nil {
			r.add("--out " + *out + ": " + err.Error())
			return r.print(stderr)
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
	seatUser, password, err := redisauth.Auth(*user, *passwordEnv)
	if err != nil {
		return ledgerFailed(s, "store", "redis", err)
	}
	var ls record.LedgerStore
	if !*dryRun {
		ls = record.DialLedger(*addr, seatUser, password)
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
	if len(batch) > 0 && !*dryRun {
		if err := ls.ReplaceLedgerDays(context.Background(), batch); err != nil {
			if *day != "" {
				return ledgerFailed(s, "day", *day, err)
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
	scope, value := "day", *day
	if *day == "" {
		scope, value = "month", *month
	}
	fmt.Fprintf(s.out(), "LEDGER %s%s%s\n", oneline.Field(verdict),
		s.factFields(scope, value, "days", days, "rows", rows, "bad", bad), s.dryRunFields(*dryRun))
	return s.done(code, 0)
}

// ledgerFailed is the store that did not answer: one LEDGER FAILED line, exit 1.
func ledgerFailed(s *sink, key, value string, err error) int {
	fmt.Fprintf(s.err(), "LEDGER FAILED %s=%s err=%s\n", oneline.Field(key), oneline.Field(value), oneline.Err(err))
	s.fact(key, value)
	s.o.Why = append(s.o.Why, err.Error())
	return s.done(1, 0)
}

// cmdReportStore is `report --redis`: the month's ledger grouped by model, repo,
// day, or the (day, model, repo) tuple, every one of the five types apart and a dash where
// no row reported a type.
func cmdReportStore(s *sink, addr, user, passwordEnv, month, by string, max int, stderr io.Writer) int {
	r := &refusals{token: "REPORT", s: s}
	switch {
	case month == "":
		r.add("--month is required; it wants " + wantsMonth + "; refusing to guess")
	case !validMonth(month):
		r.add("--month is not a month: " + month + "; it wants " + wantsMonth)
	}
	if _, ok := record.LedgerGroupings[by]; !ok {
		r.add("--by is model, repo, day or tuple, got " + by)
	}
	checkMax(r, max)
	if len(r.list) > 0 {
		return r.print(stderr)
	}
	failed := func(err error) int {
		fmt.Fprintf(s.err(), "REPORT FAILED store=redis err=%s\n", oneline.Err(err))
		s.o.Why = append(s.o.Why, err.Error())
		return s.done(1, 0)
	}
	ls, err := openLedger(addr, user, passwordEnv)
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
