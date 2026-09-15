package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/tokens"
)

// SWARM-ROOT SUM (the daily ledger from every job's usage.tsv).
//
// The pool's own usage file is sixteen columns and lives outside the reclaimable subtree,
// but a native run also writes one card's usage.tsv beside its RESULT.md — thirteen columns
// starting at `job` and ending at `usd` — and those are the files spread under a swarm
// root at `<root>/*/jobs/*/usage.tsv`. The daily token ledger used to be filled by hand;
// this verb walks those files, keeps the cards whose `started` stamp is on --day, and
// appends one row per model to the ledger. A second run for the same day replaces that
// day's rows rather than doubling them, so the ledger can be filled again and again and
// never counts a card twice.

// cardColumns are the thirteen columns of one card's usage.tsv, in order, the contract
// between the native run that writes it and this reader.
var cardColumns = []string{
	"job", "attempt", "started", "ended", "rc", "provider", "model",
	"tokens_in", "tokens_out", "cache_write", "cache_read", "reasoning", "usd",
}

// ledgerColumns are the columns of the daily token ledger, in order. The header the caller
// keeps is read and refused when it differs, so a ledger filled by hand and one filled by
// this verb can never disagree about the order.
var ledgerColumns = []string{"day", "model", "tokens_in", "tokens_out", "usd", "cards"}

const wantsLedger = "the ledger file, <ledger>.tsv, whose header is day, model, tokens_in, tokens_out, usd, cards"

// cardSum is one model's folded totals over the day: the tokens in and out, the dollars,
// and how many cards (jobs) reported it.
type cardSum struct {
	in, out int64
	usd     float64
	cards   int
}

func (c *cardSum) add(in, out int64, usd float64) {
	c.in += in
	c.out += out
	c.usd += usd
	c.cards++
}

// parseCardCount reads a numeric cell; a dash or an empty cell is an absence, which this
// ledger reads as zero, because the ledger keeps only tokens in, tokens out and dollars and
// never claims the other three types exist.
func parseCardCount(v string) int64 {
	v = strings.TrimSpace(v)
	if v == "" || v == tokens.Dash {
		return 0
	}
	if n, err := strconv.ParseInt(v, 10, 64); err == nil {
		return n
	}
	return 0
}

// parseCardUsd reads the dollar cell to four decimals; a dash is zero here too.
func parseCardUsd(v string) float64 {
	v = strings.TrimSpace(v)
	if v == "" || v == tokens.Dash {
		return 0
	}
	if f, err := strconv.ParseFloat(v, 64); err == nil {
		return f
	}
	return 0
}

// readCardFile reads one card's usage.tsv, mapping its columns by the header so the reader
// never depends on a fixed index. It returns the started stamp, the model, and the three
// numbers the ledger keeps, and reports whether the file held a row at all.
func readCardFile(path string) (started, model string, in, out int64, usd float64, ok bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", "", 0, 0, 0, false
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) < 2 {
		return "", "", 0, 0, 0, false
	}
	head := strings.Split(lines[0], "\t")
	idx := map[string]int{}
	for i, name := range head {
		if _, seen := idx[name]; !seen {
			idx[name] = i
		}
	}
	get := func(row []string, name string) string {
		if i, has := idx[name]; has && i < len(row) {
			return row[i]
		}
		return ""
	}
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		row := strings.Split(line, "\t")
		s := get(row, "started")
		m := get(row, "model")
		if s == "" || m == "" {
			continue
		}
		return s, m, parseCardCount(get(row, "tokens_in")), parseCardCount(get(row, "tokens_out")),
			parseCardUsd(get(row, "usd")), true
	}
	return "", "", 0, 0, 0, false
}

// sumSwarmRoot is the --swarm-root mode of `sum`: it walks the card usage files, folds the
// day's cards per model, and lands one row per model in the ledger, idempotently.
func sumSwarmRoot(root, day, out string, stdout, stderr io.Writer, r *refusals) int {
	switch {
	case root == "":
		r.add("--swarm-root is required; it wants the directory the swarm batches live under; refusing to guess")
	case day == "":
		r.add("--day is required with --swarm-root; it wants " + wantsDay + "; refusing to guess")
	case !tokens.ValidDay(day):
		r.add("--day is not a day: " + day + "; it wants " + wantsDay)
	case out == "":
		r.add("--out is required with --swarm-root; it wants " + wantsLedger + "; refusing to guess")
	}
	if len(r.list) > 0 {
		return r.print(stderr)
	}

	paths, err := filepath.Glob(filepath.Join(root, "*", "jobs", "*", "usage.tsv"))
	if err != nil {
		r.add("--swarm-root " + root + ": " + err.Error() + "; it wants the directory the swarm batches live under")
		return r.print(stderr)
	}
	sort.Strings(paths)

	models := map[string]*cardSum{}
	totalCards := 0
	for _, p := range paths {
		started, model, in, out, usd, ok := readCardFile(p)
		if !ok {
			continue
		}
		if !strings.HasPrefix(started, day+"T") {
			continue
		}
		m, seen := models[model]
		if !seen {
			m = &cardSum{}
			models[model] = m
		}
		m.add(in, out, usd)
		totalCards++
	}

	names := make([]string, 0, len(models))
	for name := range models {
		names = append(names, name)
	}
	sort.Strings(names)

	var totalIn, totalOut int64
	var totalUsd float64
	for _, name := range names {
		totalIn += models[name].in
		totalOut += models[name].out
		totalUsd += models[name].usd
	}

	if err := writeLedger(out, day, names, models); err != nil {
		r.add("--out " + out + ": " + err.Error())
		return r.print(stderr)
	}

	fmt.Fprintf(stdout, "SUM OK day=%s models=%d cards=%d in=%d out=%d usd=%.4f\n",
		oneline.Field(day), len(names), totalCards, totalIn, totalOut, totalUsd)
	return 0
}

// writeLedger lands one row per model in the ledger, replacing any rows this day already
// has so a second run never doubles them. The header is read and must match the canonical
// order; a ledger whose header differs is refused rather than written around.
func writeLedger(path, day string, names []string, models map[string]*cardSum) error {
	if fi, err := os.Stat(path); err == nil && fi.IsDir() {
		return fmt.Errorf("--out is a directory; it must be a file, the ledger path (<ledger>.tsv), not a directory")
	}
	raw, err := os.ReadFile(path)
	existed := err == nil
	var kept []string
	if existed {
		lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
		if len(lines) > 0 && lines[0] != strings.Join(ledgerColumns, "\t") {
			return fmt.Errorf("the ledger's header is %q; it wants %q, and a ledger filled by hand must agree with the one this verb writes",
				lines[0], strings.Join(ledgerColumns, "\t"))
		}
		for _, line := range lines[1:] {
			if strings.TrimSpace(line) == "" {
				continue
			}
			if fields := strings.Split(line, "\t"); len(fields) > 0 && fields[0] == day {
				continue
			}
			kept = append(kept, line)
		}
	}

	var out []string
	out = append(out, strings.Join(ledgerColumns, "\t"))
	out = append(out, kept...)
	for _, name := range names {
		m := models[name]
		out = append(out, strings.Join([]string{
			day, name,
			strconv.FormatInt(m.in, 10),
			strconv.FormatInt(m.out, 10),
			strconv.FormatFloat(m.usd, 'f', 4, 64),
			strconv.Itoa(m.cards),
		}, "\t"))
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(out, "\n")+"\n"), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
