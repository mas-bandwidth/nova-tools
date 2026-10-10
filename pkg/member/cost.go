package member

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/cardcost"
)

// A RUN'S COST IS RECORDED WHATEVER ITS END (docs/SPEC-SPRINT.md, "What a card cost"). A
// launch's child appends one row to its job's usage.tsv as each attempt of it ends, before
// the final summary line that carries the launch's spend; a launch stopped between the two
// (killed at its cap, its log cut, ended with no result) still spent what those rows say.
// ReceiptUsage reads them back, so the finish or the read carries the cost of every run,
// and the sprint prices what the provider did not (tokens at the route's prices).

// ReceiptName is a launch's durable receipt in its job directory: one row per attempt.
const ReceiptName = "usage.tsv"

// receiptTokens maps the receipt's token columns to the cost record's words.
var receiptTokens = [][2]string{
	{"tokens_in", "input"}, {"cache_read", "cache_read"}, {"cache_write", "cache_write"},
	{"tokens_out", "output"}, {"reasoning", "reasoning"},
}

// ReceiptUsage is the cost record of a launch read from its job's receipt (ReceiptName): the
// rows' tokens summed by class, the provider/model the last row named, and the harness's
// cost only when every row that reported tokens reported one (a cost for part of the launch
// is not the launch's; the sprint then prices the tokens). found is false with no receipt
// or no row; a receipt that does not read is an error, never a zero.
func ReceiptUsage(job string) (usage cardcost.Usage, found bool, err error) {
	f, err := os.Open(filepath.Join(job, ReceiptName))
	if errors.Is(err, fs.ErrNotExist) {
		return cardcost.NoUsage(), false, nil
	}
	if err != nil {
		return cardcost.NoUsage(), false, err
	}
	defer f.Close() // ignored: a read-only receipt needs no close result
	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		return cardcost.NoUsage(), false, sc.Err()
	}
	col := map[string]int{}
	for i, name := range strings.Split(sc.Text(), "\t") {
		col[name] = i
	}
	for _, name := range []string{"provider", "model", "tokens_in", "tokens_out", "cache_write", "cache_read", "reasoning", "usd"} {
		if _, ok := col[name]; !ok {
			return cardcost.NoUsage(), false, fmt.Errorf("%s has no %s column", ReceiptName, name)
		}
	}
	var runs []cardcost.Usage
	model, priced := "", true
	for sc.Scan() {
		row := strings.Split(sc.Text(), "\t")
		if len(row) != len(col) {
			return cardcost.NoUsage(), false, fmt.Errorf("%s row has %d fields; its header has %d", ReceiptName, len(row), len(col))
		}
		value := func(name string) string {
			if v := row[col[name]]; v != "-" {
				return v
			}
			return ""
		}
		var words []string
		for _, t := range receiptTokens {
			v := value(t[0])
			if v == "" {
				continue
			}
			if n, err := strconv.ParseInt(v, 10, 64); err != nil || n < 0 {
				return cardcost.NoUsage(), false, fmt.Errorf("%s row has %s %q, not a count", ReceiptName, t[0], v)
			}
			words = append(words, t[1]+"="+v)
		}
		u := cardcost.ParseUsage(strings.Join(words, " "))
		if usd := value("usd"); usd != "" {
			if _, ok := cardcost.Sum(usd, "0"); !ok {
				return cardcost.NoUsage(), false, fmt.Errorf("%s row has usd %q, not an amount", ReceiptName, usd)
			}
			u.Actual, u.ActualBy = usd, cardcost.ActualByHarness // a measured zero is present, not absent
		} else if u.Tokens.Reported() {
			priced = false
		}
		if p, m := value("provider"), value("model"); p != "" && m != "" {
			model = p + "/" + m
		}
		runs = append(runs, u)
	}
	if err := sc.Err(); err != nil {
		return cardcost.NoUsage(), false, err
	}
	if len(runs) == 0 {
		return cardcost.NoUsage(), false, nil
	}
	total := cardcost.SumUsage(runs)
	if !priced {
		total.Actual = ""
	}
	return cardcost.ParseSpend(cardcost.SpendWord(total.Tokens, total.Actual, model)), true, nil
}
