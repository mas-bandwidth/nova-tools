package record

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// token_ledger is docs/SPEC-STATE.md's per card, model, repo, day table: the index of the day
// TSVs nova-tokens folds, keyed exactly (day, card, model, repo) with the five token types
// kept apart. A type no source reported is SQL NULL, never a zero: a dash in the day file is
// an absence, and the monthly GROUP BY sums over the rows that reported a type exactly as
// the fold's per-type rule does. The writer is nova-tokens; the day files stay the record.

// LedgerTypes are the five token types in the order every row and report prints them.
var LedgerTypes = [5]string{"input", "output", "cache_write", "cache_read", "reasoning"}

// LedgerEntry is one token_ledger row. Tokens[i] counts only when Known[i].
type LedgerEntry struct {
	Day, Card, Model, Repo string
	Provider               string
	Tokens                 [5]int64
	Known                  [5]bool
	Rough                  int
	Sources                string
}

// LedgerTotal is one GROUP BY group: the key columns the report grouped on (empty when not
// grouped on), how many ledger rows it summed, and each type's sum and whether any row
// reported it.
type LedgerTotal struct {
	Day, Model, Repo string
	Rows             int
	Tokens           [5]int64
	Known            [5]bool
}

// LedgerStore is the durable side of the token ledger. ReplaceLedgerDay swaps one day's rows
// for the given ones in one transaction, so indexing a day twice is the same table as once.
// LedgerReport is the monthly GROUP BY.
type LedgerStore interface {
	Migrate(ctx context.Context) error
	ReplaceLedgerDay(ctx context.Context, day string, entries []LedgerEntry) error
	LedgerReport(ctx context.Context, month, by string) ([]LedgerTotal, error)
	Close() error
}

// LedgerGroupings are the --by values the report takes, and the key columns each groups on.
var LedgerGroupings = map[string][]string{
	"model": {"model"},
	"repo":  {"repo"},
	"day":   {"day"},
	"tuple": {"day", "model", "repo"},
}

// ErrLedgerKey is a row that does not name its whole key.
var ErrLedgerKey = errors.New("a token_ledger row names day, card, model and repo")

// CheckLedgerDay refuses a batch that is not all of one day or repeats a key: the key is the
// table's primary key, and a caller that meant two rows under one key meant their sum.
func CheckLedgerDay(day string, entries []LedgerEntry) error {
	seen := map[[4]string]bool{}
	for _, e := range entries {
		if e.Day == "" || e.Card == "" || e.Model == "" || e.Repo == "" {
			return ErrLedgerKey
		}
		if e.Day != day {
			return fmt.Errorf("a row of day %s in the batch for day %s", e.Day, day)
		}
		k := [4]string{e.Day, e.Card, e.Model, e.Repo}
		if seen[k] {
			return fmt.Errorf("the key (%s) appears twice in one day; sum it first", strings.Join(k[:], ", "))
		}
		seen[k] = true
	}
	return nil
}

// SortLedgerTotals is the report's order: by the key columns ascending.
func SortLedgerTotals(t []LedgerTotal) {
	sort.SliceStable(t, func(i, j int) bool {
		a, b := t[i], t[j]
		if a.Day != b.Day {
			return a.Day < b.Day
		}
		if a.Model != b.Model {
			return a.Model < b.Model
		}
		return a.Repo < b.Repo
	})
}

// ReplaceLedgerDay swaps the day's rows in the fake.
func (f *FakeStore) ReplaceLedgerDay(ctx context.Context, day string, entries []LedgerEntry) error {
	if err := CheckLedgerDay(day, entries); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	kept := f.ledger[:0:0]
	for _, e := range f.ledger {
		if e.Day != day {
			kept = append(kept, e)
		}
	}
	f.ledger = append(kept, entries...)
	return nil
}

// LedgerReport is the fake's GROUP BY, the same sums as the Postgres query: SUM over the
// rows that reported a type, NULL (unknown) when none did.
func (f *FakeStore) LedgerReport(ctx context.Context, month, by string) ([]LedgerTotal, error) {
	cols, ok := LedgerGroupings[by]
	if !ok {
		return nil, fmt.Errorf("--by %q is not one of model, repo, day, tuple", by)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	groups := map[[3]string]*LedgerTotal{}
	var order [][3]string
	for _, e := range f.ledger {
		if !strings.HasPrefix(e.Day, month+"-") {
			continue
		}
		var k [3]string
		for _, c := range cols {
			switch c {
			case "day":
				k[0] = e.Day
			case "model":
				k[1] = e.Model
			case "repo":
				k[2] = e.Repo
			}
		}
		g := groups[k]
		if g == nil {
			g = &LedgerTotal{Day: k[0], Model: k[1], Repo: k[2]}
			groups[k] = g
			order = append(order, k)
		}
		g.Rows++
		for i := range e.Tokens {
			if e.Known[i] {
				g.Tokens[i] += e.Tokens[i]
				g.Known[i] = true
			}
		}
	}
	out := make([]LedgerTotal, 0, len(order))
	for _, k := range order {
		out = append(out, *groups[k])
	}
	SortLedgerTotals(out)
	return out, nil
}
