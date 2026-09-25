package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/redisauth"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/record"
	"github.com/mas-bandwidth/nova-tools/internal/tokens"
)

// The token ledger (docs/SPEC-STATE.md test 17, #2201; recut of #3243 on Redis under
// #2623): `ledger` writes each folded day to tokens:ledger:<day> as the day files' index,
// and `report --redis` is the monthly report as one GROUP BY over the month's day hashes.
// The fold is untouched and the day TSVs stay the record; the ledger is what a month query
// reads instead of every file. The key layout is internal/record's package comment.

// openLedger opens the fleet Redis at addr and pings it, so an unreachable or refusing
// store is named before any file is read. The seat is the one every nova tool dials with
// (redisauth.Auth, #3461): --user, else NOVA_SPRINT_REDIS_USER; the password is never a flag,
// it is the variable --password-env names, else (for a user) NOVA_SPRINT_REDIS_PASSWORD_ENV's
// or NOVA_REDIS_BENCH_PASSWORD. With no user, no variable is consulted unless --password-env
// names one.
func openLedger(addr, user, passwordEnv string) (record.LedgerStore, error) {
	user, password, err := redisauth.Auth(user, passwordEnv)
	if err != nil {
		return nil, err
	}
	s := record.DialLedger(addr, user, password)
	if err := s.Ping(context.Background()); err != nil {
		_ = s.Close()
		return nil, err
	}
	return s, nil
}

const wantsRedis = "the fleet Redis host:port whose tokens:ledger:<day> hashes this reads or writes"

// ledgerEntries turns one day file into its ledger rows: the card is the row's unit
// (`-` when none), the provider is the source kinds, and two rows under one key are summed
// with the fold's per-type rule.
func ledgerEntries(d tokens.DayFile) []record.LedgerEntry {
	type acc struct {
		e       record.LedgerEntry
		c       tokens.Counts
		sources map[string]bool
	}
	byKey := map[[3]string]*acc{}
	var order [][3]string
	for _, r := range d.Rows {
		card := strings.TrimSpace(r.Unit)
		if card == "" {
			card = tokens.Dash
		}
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
		var ks []string
		for k := range kinds {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		a.e.Sources = strings.Join(srcs, ",")
		a.e.Provider = strings.Join(ks, ",")
		out = append(out, a.e)
	}
	return out
}

// cmdLedger indexes the day files of one day or one month into tokens:ledger:<day>. Each day is
// replaced whole, so indexing twice is the table indexing once. It reads the day files and
// writes nothing beside them.
func cmdLedger(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("ledger")
	out := fs.String("out", "", "")
	day := fs.String("day", "", "")
	month := fs.String("month", "", "")
	addr := fs.String("redis", "", "")
	user := fs.String("user", "", "")
	passwordEnv := fs.String("password-env", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, " ledger", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if code, refused := noPositional(fs, stderr, "ledger"); refused {
		return code
	}
	r := &refusals{token: "LEDGER"}
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
	ls, err := openLedger(*addr, *user, *passwordEnv)
	if err != nil {
		fmt.Fprintf(stderr, "LEDGER FAILED store=redis err=%s\n", oneline.Err(err))
		return 1
	}
	defer ls.Close()
	ctx := context.Background()
	days, rows, bad := 0, 0, 0
	for _, p := range paths {
		name := strings.TrimSuffix(filepath.Base(p), tokens.FileSuffix)
		d, findings, err := tokens.ReadDayFile(p)
		if err != nil {
			bad++
			why := err.Error()
			if os.IsNotExist(err) {
				why = "no day file; fold --day " + name + " first"
			}
			fmt.Fprintf(stdout, "LEDGER BAD day=%s why=%s\n", oneline.Field(name), oneline.Escape(why))
			continue
		}
		if len(findings) > 0 {
			bad++
			fmt.Fprintf(stdout, "LEDGER BAD day=%s why=%s\n", oneline.Field(name), oneline.Escape(findings[0].Reason))
			continue
		}
		entries := ledgerEntries(d)
		if err := ls.ReplaceLedgerDay(ctx, d.Day, entries); err != nil {
			fmt.Fprintf(stderr, "LEDGER FAILED day=%s err=%s\n", oneline.Field(name), oneline.Err(err))
			return 1
		}
		days++
		rows += len(entries)
		fmt.Fprintf(stdout, "LEDGER day=%s rows=%d\n", oneline.Field(d.Day), len(entries))
	}
	verdict, code := "OK", 0
	if bad > 0 || days == 0 {
		verdict, code = "NO", 1
	}
	if *day != "" {
		fmt.Fprintf(stdout, "LEDGER %s day=%s days=%d rows=%d bad=%d\n", oneline.Field(verdict), oneline.Field(*day), days, rows, bad)
	} else {
		fmt.Fprintf(stdout, "LEDGER %s month=%s days=%d rows=%d bad=%d\n", oneline.Field(verdict), oneline.Field(*month), days, rows, bad)
	}
	return code
}

// cmdReportStore is `report --redis`: the month's ledger grouped by model, repo,
// day, or the (day, model, repo) tuple, every one of the five types apart and a dash where
// no row reported a type.
func cmdReportStore(addr, user, passwordEnv, month, by string, max int, stdout, stderr io.Writer) int {
	r := &refusals{token: "REPORT"}
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
	ls, err := openLedger(addr, user, passwordEnv)
	if err != nil {
		fmt.Fprintf(stderr, "REPORT FAILED store=redis err=%s\n", oneline.Err(err))
		return 1
	}
	defer ls.Close()
	totals, indexed, missing, err := ls.LedgerReport(context.Background(), month, by)
	if err != nil {
		fmt.Fprintf(stderr, "REPORT FAILED store=redis err=%s\n", oneline.Err(err))
		return 1
	}
	rows := 0
	for i, t := range totals {
		rows += t.Rows
		if max != 0 && i >= max {
			continue
		}
		var keys []string
		for _, c := range record.LedgerGroupings[by] {
			switch c {
			case "day":
				keys = append(keys, "day="+oneline.Field(t.Day))
			case "model":
				keys = append(keys, "model="+oneline.Field(t.Model))
			case "repo":
				keys = append(keys, "repo="+oneline.Field(t.Repo))
			}
		}
		line := "REPORT " + strings.Join(keys, " ") + fmt.Sprintf(" rows=%d", t.Rows)
		for i, name := range record.LedgerTypes {
			cell := tokens.Dash
			if t.Known[i] {
				cell = strconv.FormatInt(t.Tokens[i], 10)
			}
			line += " " + name + "=" + cell
		}
		fmt.Fprintln(stdout, line)
	}
	if max != 0 && len(totals) > max {
		fmt.Fprintf(stdout, "REPORT MORE shown=%d of=%d; raise --max (0 = all)\n", max, len(totals))
	}
	if indexed == 0 {
		fmt.Fprintf(stdout, "REPORT NO month=%s source=redis indexed=0\n", oneline.Field(month))
		return 1
	}
	fmt.Fprintf(stdout, "REPORT OK month=%s source=redis groups=%d rows=%d indexed=%d missing=%d\n", oneline.Field(month), len(totals), rows, indexed, missing)
	return 0
}
