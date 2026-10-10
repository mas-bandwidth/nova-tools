// report.go holds the report verb: its flags, its run and the helpers only it uses.

package main

import (
	"context"
	"fmt"
	"io"
	"maps"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/record"
	"github.com/mas-bandwidth/nova-tools/internal/tokens"
	"github.com/mas-bandwidth/nova-tools/pkg/atomicfile"
	"github.com/mas-bandwidth/nova-tools/pkg/bounded"
	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/redisauth"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// openLedger opens the fleet Redis at addr. The seat is the one every nova tool dials with
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
	// ignored: a deferred close on a read-only store: every read already carried its answer to the printed report
	defer func() { _ = ls.Close() }()
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
		fmt.Fprintf(s.out(), "REPORT FAILED month=%s source=redis indexed=0\n", oneline.Field(month))
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

// avgRate is a model-day's dollars per million tokens (usdMicro / tokens), for sorting the
// AVG listing highest first. A zero-token or unpriced model has no average and sorts below
// every real rate, which is never negative.
func avgRate(usdMicro, tokens int64, priced bool) float64 {
	if tokens == 0 || !priced {
		return -1
	}
	return float64(usdMicro) / float64(tokens)
}

// cmdReport is the verb for a friend on another machine, and the first user of this tool
// is not this bench. It folds that machine's own sources for one day, the same sources and
// the same attribution as fold, and prints EXACTLY the body lines of a tokens note and
// nothing else: no heading, no stamp, no comment. The stamp and the build id go on the
// subject, which it prints on its one OK line.
//
// THIS IS THE ONE PLACE IN THE FAMILY WHERE THE OK LINE LEAVES STDOUT, because here stdout
// is the artifact. The spec says so in as many words, which is the exception SPEC.md's
// Conventions allow when a spec states one.
func cmdReport(args []string, stdout, stderr io.Writer, now time.Time) int {
	fs := newFlagSet("report")
	who := fs.String("who", "", "name to write in each note body row")
	day := fs.String("day", "", "one UTC day to report as YYYY-MM-DD")
	notePath := fs.String("note", "", "atomically write the note body to this path")
	var supersedes stringList
	fs.Var(&supersedes, "supersedes", "note id this report replaces; repeatable")
	max := fs.Int("max", bounded.Default, "maximum summary rows to print; 0 prints all")
	monthFlag := fs.String("month", "", "month to summarize as YYYY-MM")
	byFlag := fs.String("by", "model", "Redis summary grouping: model, repo, day or tuple")
	redisAddr := fs.String("redis", "", "Redis address for the store summary mode")
	redisUser := fs.String("user", "", "Redis username for the store summary mode")
	passwordEnv := fs.String("password-env", "", "environment variable holding the Redis password")
	dryRun := fs.Bool("dry-run", false, "print the body and name the --note file, and write no file")
	var sf sourceFlags
	sf.declare(fs, true)
	s, code, ok := start(fs, args, "REPORT", stdout, stderr)
	if !ok {
		return code
	}
	if *redisAddr != "" {
		return cmdReportStore(s, *redisAddr, *redisUser, *passwordEnv, *monthFlag, *byFlag, *max, stderr)
	}
	if *monthFlag != "" {
		return (&refusals{token: "REPORT", s: s, list: []problem{{why: "--month is the store's month report; it wants --redis <host:port>"}}}).print(stderr)
	}
	r := &refusals{token: "REPORT", s: s}
	r.required("who", *who, wantsWho)
	switch {
	case *day == "":
		r.add("--day is required; it wants " + wantsDay + "; refusing to guess")
	case !tokens.ValidDay(*day):
		r.add("--day is not a day: " + *day + "; it wants " + wantsDay)
	}
	sf.check(r)
	checkMax(r, *max)
	seen := map[string]bool{}
	for _, id := range supersedes {
		switch {
		case !tokens.ValidNoteID(id):
			r.add("--supersedes " + id + ": it wants a note id of the shape <sender>-<12 hex>")
		case seen[id]:
			r.add("--supersedes names " + id + " twice; the predecessor set is a set, with no duplicate")
		}
		seen[id] = true
	}
	if len(r.list) > 0 {
		return r.print(stderr)
	}
	rules, err := tokens.LoadRules(sf.repos)
	if err != nil {
		r.add("--repos " + sf.repos + ": " + err.Error() + "; it wants " + wantsRepos)
		return r.print(stderr)
	}
	sorted := slices.Sorted(slices.Values(supersedes))

	sources, copyNotes := sf.read(rules, now, *dryRun)
	folder := tokens.NewFolder()
	for _, src := range sources {
		for _, m := range src.Stream {
			folder.Add(src.Label, m)
		}
	}
	// Rule 20: report folds that the caller's own sources produce for one day, with the same
	// sources and the same attribution as fold. That has to include what the fold SAYS about them.
	// This verb counted only the unreadables, so a transcript line whose stamp does not
	// parse and a message with no id -- both counted by the reader, both dropped before
	// the body -- left no trace at all, and the friend pasted a short day onto the bus
	// under REPORT OK (rule 3: counted and printed, never skipped silently).
	unreadable, unparsed := 0, 0
	for _, src := range sources {
		for _, u := range src.Unreadables {
			fmt.Fprintln(s.err(), unreadableLine(s, "TOKENS", u))
			unreadable++
		}
	}
	for _, src := range sources {
		for _, u := range src.Unparseds {
			fmt.Fprintln(s.err(), unparsedLine(s, "TOKENS", u))
			unparsed++
		}
	}
	// A message the fold could not count by id is not an unparsed line and is not a
	// refusal; it is spend that was read and then dropped, and fold names it on its one
	// remedy line. So does this verb.
	if dropped := noidAndDup(sources); dropped != "" {
		fmt.Fprintf(s.err(), "TOKENS NOTE %s\n", oneline.Escape(dropped))
		s.note(dropped)
	}
	copyNote(s, "TOKENS", copyNotes)
	rows, mixed := folder.DayRows(*day)
	for _, m := range mixed {
		fmt.Fprintln(s.err(), s.line("TOKENS", "MIXED", "two day bases on one row; declare one export for that day",
			"date", m.Day, "model", m.Model, "repo", m.Repo, "bases", strings.Join(m.Bases, ",")))
	}
	// A mixed key is not in `rows` at all (Folder.DayRows keeps them apart), so the body
	// below is exactly "no line for that key" and every other key's lines.
	var rendered []string
	for _, row := range rows {
		for t := tokens.Type(0); t < tokens.NTypes; t++ {
			v, ok := row.Counts.Get(t)
			if !ok {
				continue
			}
			rendered = append(rendered, tokens.BodyLine(*day, *who, row.Model, row.Repo, t, v, row.Basis()))
		}
	}
	lines := len(rendered)
	body := ""
	if lines > 0 {
		body = strings.Join(rendered, "\n") + "\n"
	}
	s.o.Payload = body
	s.fact("who", *who)
	s.fact("day", *day)
	s.fact("rows", lines)
	if lines == 0 || len(mixed) > 0 {
		// A friend with nothing to show says so, and never sends zeros. A REPORT FAILED
		// writes nothing: an existing --note file is left byte-unchanged. A mixed key fails
		// the day, and the rest of the body is still printed: the spec's sentence
		// is "no line for that key", not no line for any key.
		if lines > 0 {
			fmt.Fprint(s.out(), body)
		}
		fmt.Fprintf(s.err(), "REPORT FAILED who=%s day=%s rows=%d unreadable=%d\n",
			oneline.Field(*who), oneline.Field(*day), lines, unreadable)
		s.fact("unreadable", unreadable)
		return s.done(1, *max)
	}
	fmt.Fprint(s.out(), body)
	if *notePath != "" {
		// A dry run checks the note's path exactly as the write would (atomicfile.Check)
		// and refuses what it refuses; only the write itself is skipped.
		write := func() error { return atomicfile.Write(filepath.Clean(*notePath), []byte(body), 0o644) }
		if *dryRun {
			write = func() error { return atomicfile.Check(filepath.Clean(*notePath), 0o644) }
		}
		if err := write(); err != nil {
			r.add("--note " + *notePath + ": " + err.Error())
			return r.print(stderr)
		}
	}
	// One TOKENS AVG line per model, after the body lines: the daily blended cost per
	// token, summed over every repo the model wrote that day. Four types count toward
	// tokens (input, output, cache write, cache read); reasoning is its own column and is
	// not in the denominator. usd= is the cost the sources reported, usd_per_mtok= divides
	// it by the tokens that cost covers and no others, and unpriced= counts the tokens no
	// source priced. A model no source priced prints usd=- and usd_per_mtok=-: a cost
	// nobody reported is no measurement, and never a zero.
	type modelAvg struct {
		name         string // provider/model, or model where no source named a provider
		tokens       int64  // every billed token the model's rows hold
		pricedTokens int64  // the tokens the reported cost covers
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
	avgList := s.list(true, *max, "TOKENS", "avg", maxRemedy("report"))
	var allTokens, allPricedTokens, allUsd int64
	allPriced := false
	for _, a := range sortedAvg {
		allTokens += a.tokens
		allPricedTokens += a.pricedTokens
		allUsd += a.usd
		allPriced = allPriced || a.priced
		avgList.Line(s.line("TOKENS", "AVG", "", "day", *day, "model", a.name, "tokens", a.tokens,
			"usd", usdCell(a.usd, a.priced), "usd_per_mtok", usdPerMtokCell(a.usd, a.pricedTokens, a.priced),
			"unpriced", a.tokens-a.pricedTokens))
	}
	avgList.More()
	fmt.Fprintln(s.err(), s.line("TOKENS", "AVG-ALL", "", "day", *day, "tokens", allTokens,
		"usd", usdCell(allUsd, allPriced), "usd_per_mtok", usdPerMtokCell(allUsd, allPricedTokens, allPriced),
		"unpriced", allTokens-allPricedTokens))
	// The closing line is the grammar's, field for field (SPEC-TOKENS' TOKENS SOURCE
	// section): what says the day is short is the TOKENS UNREADABLE / TOKENS UNPARSED
	// lines above it, the TOKENS NOTE, and exit 1. Under --dry-run --note was not
	// written, and the line says so. The subject is one quoted value (oneline.Quote):
	// it holds blanks and repeats at= and build= inside itself, and unquoted those
	// inner pairs read as keys of the line.
	subject := tokens.Subject(*day, stamp(now), buildVersion(), sorted)
	s.fact("at", stamp(now))
	s.fact("build", buildVersion())
	s.fact("subject", tool.Text(subject))
	// Rule 3, and the exit table: "a declared source with an unreadable file" is exit 1,
	// and a line that did not parse is the same wall under fold. The body still printed and
	// --note still landed -- exit 1 still writes -- but a friend about to paste this onto
	// the bus is told it does not cover what it claims. The status word follows the exit
	// (skeleton contract 1.5), so that run says FAILED: a report that printed the body
	// over a source it could not read whole said REPORT OK and exited 1, while --json
	// already said status=failed.
	if unreadable > 0 || unparsed > 0 {
		fmt.Fprintf(s.err(), "REPORT FAILED who=%s day=%s rows=%d at=%s build=%s%s subject=%s\n",
			oneline.Field(*who), oneline.Field(*day), lines, oneline.Field(stamp(now)),
			oneline.Field(buildVersion()), s.dryRunFields(*dryRun, "note", *notePath), oneline.Quote(subject))
		return s.done(1, *max)
	}
	fmt.Fprintf(s.err(), "REPORT OK who=%s day=%s rows=%d at=%s build=%s%s subject=%s\n",
		oneline.Field(*who), oneline.Field(*day), lines, oneline.Field(stamp(now)),
		oneline.Field(buildVersion()), s.dryRunFields(*dryRun, "note", *notePath), oneline.Quote(subject))
	return s.done(0, *max)
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
