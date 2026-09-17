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
// appends one row per (model, repo) pair to the ledger. A second run for the same day
// replaces that day's rows rather than doubling them, so the ledger can be filled again and
// again and never counts a card twice.

// cardColumns are the thirteen columns of one card's usage.tsv, in order, the contract
// between the native run that writes it and this reader.
var cardColumns = []string{
	"job", "attempt", "started", "ended", "rc", "provider", "model",
	"tokens_in", "tokens_out", "cache_write", "cache_read", "reasoning", "usd",
}

// ledgerColumns are the columns of the daily token ledger, in order. The header the caller
// keeps is read and refused when it differs, so a ledger filled by hand and one filled by
// this verb can never disagree about the order. The row key is (day, model, repo), because
// the same model costs differently against a small tool repo and a large one (#64).
var ledgerColumns = []string{"day", "model", "repo", "tokens_in", "tokens_out", "usd", "cards", "completed", "usd_per_task", "dashes"}

const wantsLedger = "the ledger file, <ledger>.tsv, whose header is day, model, repo, tokens_in, tokens_out, usd, cards, completed, usd_per_task, dashes"

// cardSum is one (model, repo) pair's folded totals over the day: the tokens in and out,
// the dollars, how many cards (jobs) reported it, how many of those finished (rc=0), plus
// how many of those cards left each field unknown (a dash, an empty cell, or a cell that is
// not a number). completed is the routing metric's denominator and is its own count, never
// cards minus anything.
type cardSum struct {
	in, out                           int64
	usd                               float64
	cards                             int
	completed                         int
	unknownIn, unknownOut, unknownUsd int
	unknownRC                         int
}

func (c *cardSum) add(in, out int64, usd float64, inKnown, outKnown, usdKnown bool, rc string) {
	if inKnown {
		c.in += in
	} else {
		c.unknownIn++
	}
	if outKnown {
		c.out += out
	} else {
		c.unknownOut++
	}
	if usdKnown {
		c.usd += usd
	} else {
		c.unknownUsd++
	}
	// completed counts only a receipt that names rc=0, a task that finished. An rc that is
	// absent or not a number is neither failed nor finished: it is a dash in the dashes
	// column and never in completed.
	if n, known := parseCardCount(rc); known {
		if n == 0 {
			c.completed++
		}
	} else {
		c.unknownRC++
	}
	c.cards++
}

// parseCardCount reads a numeric cell and whether the cell held one. A dash, an empty cell,
// or a cell that is not a number is an absence, and an absence is unknown: never the zero
// that would make a route that did not report a count look free.
func parseCardCount(v string) (int64, bool) {
	v = strings.TrimSpace(v)
	if v == "" || v == tokens.Dash {
		return 0, false
	}
	if n, err := strconv.ParseInt(v, 10, 64); err == nil {
		return n, true
	}
	return 0, false
}

// parseCardUsd reads the dollar cell and whether the cell held it. An absence here is
// unknown too, never a free-route zero.
func parseCardUsd(v string) (float64, bool) {
	v = strings.TrimSpace(v)
	if v == "" || v == tokens.Dash {
		return 0, false
	}
	if f, err := strconv.ParseFloat(v, 64); err == nil {
		return f, true
	}
	return 0, false
}

// cardCell renders one count cell: the summed count, or `-` when every card left the field
// unknown, matching the ledger-wide rule that an unmeasured type is `-`, never 0.
func cardCell(n int64, unknown, cards int) string {
	if unknown == cards {
		return tokens.Dash
	}
	return strconv.FormatInt(n, 10)
}

// usdCell renders the dollar cell: `-` when every card left it unknown, else four decimals.
func usdCell(n float64, unknown, cards int) string {
	if unknown == cards {
		return tokens.Dash
	}
	return strconv.FormatFloat(n, 'f', 4, 64)
}

// usdPerTaskCell renders cost per completed task: usd/completed to six decimals, `-` when
// no card finished or when every card left usd unknown. There is no cost over no finished
// task, and the ratio is never divided.
func usdPerTaskCell(usd float64, completed int, usdUnknown bool) string {
	if completed == 0 || usdUnknown {
		return tokens.Dash
	}
	return strconv.FormatFloat(usd/float64(completed), 'f', 6, 64)
}

// dashesCell counts, per kept field, how many cards left it unknown. The last count is rc,
// where a card that named no completion is reported rather than read as a failed task.
func dashesCell(in, out, usd, rc int) string {
	return strconv.Itoa(in) + "," + strconv.Itoa(out) + "," + strconv.Itoa(usd) + "," + strconv.Itoa(rc)
}

// readCardFile reads one card's usage.tsv, mapping its columns by the header so the reader
// never depends on a fixed index. It returns the started stamp, the model, the repo the
// receipt names (unattributed when it names none), the tool, the rc, and the three numbers
// the ledger keeps, and reports whether the file held a row at all.
func readCardFile(path string) (started, model, repo, tool, rc string, in, out int64, usd float64, inKnown, outKnown, usdKnown, ok bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", "", "", "", "", 0, 0, 0, false, false, false, false
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) < 2 {
		return "", "", "", "", "", 0, 0, 0, false, false, false, false
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
		in, inKnown = parseCardCount(get(row, "tokens_in"))
		out, outKnown = parseCardCount(get(row, "tokens_out"))
		usd, usdKnown = parseCardUsd(get(row, "usd"))
		repo = strings.TrimSpace(get(row, "repo"))
		if repo == "" || repo == tokens.Dash {
			repo = tokens.Unattributed
		}
		return s, m, repo, get(row, "tool"), get(row, "rc"), in, out, usd, inKnown, outKnown, usdKnown, true
	}
	return "", "", "", "", "", 0, 0, 0, false, false, false, false
}

// sumSwarmRoot is the --swarm-root mode of `sum`: it walks the card usage files, folds the
// day's cards per (model, repo), and lands one row per pair in the ledger, idempotently.
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
	tools := map[string]int{}
	totalCards := 0
	for _, p := range paths {
		started, model, repo, tool, rc, in, out, usd, inKnown, outKnown, usdKnown, ok := readCardFile(p)
		if !ok {
			continue
		}
		if !strings.HasPrefix(started, day+"T") {
			continue
		}
		key := model + "\t" + repo
		m, seen := models[key]
		if !seen {
			m = &cardSum{}
			models[key] = m
		}
		m.add(in, out, usd, inKnown, outKnown, usdKnown, rc)
		if tool != "" && tool != tokens.Dash {
			tools[tool]++
		}
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
	if len(tools) > 0 {
		toolNames := make([]string, 0, len(tools))
		for name := range tools {
			toolNames = append(toolNames, name)
		}
		sort.Strings(toolNames)
		parts := make([]string, 0, len(toolNames))
		for _, name := range toolNames {
			parts = append(parts, oneline.Field(name)+":"+strconv.Itoa(tools[name]))
		}
		fmt.Fprintf(stdout, "TOOLS %s\n", oneline.Field(strings.Join(parts, ",")))
	}
	return 0
}

// writeLedger lands one row per (model, repo) pair in the ledger, replacing any rows this
// day already has so a second run never doubles them. The header is read and must match the
// canonical order; a ledger whose header differs is refused rather than written around.
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
		model, repo, _ := strings.Cut(name, "\t")
		out = append(out, strings.Join([]string{
			day, model, repo,
			cardCell(m.in, m.unknownIn, m.cards),
			cardCell(m.out, m.unknownOut, m.cards),
			usdCell(m.usd, m.unknownUsd, m.cards),
			strconv.Itoa(m.cards),
			strconv.Itoa(m.completed),
			usdPerTaskCell(m.usd, m.completed, m.unknownUsd == m.cards),
			dashesCell(m.unknownIn, m.unknownOut, m.unknownUsd, m.unknownRC),
		}, "\t"))
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(out, "\n")+"\n"), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
