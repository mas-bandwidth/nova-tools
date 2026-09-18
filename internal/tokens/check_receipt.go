package tokens

import (
	"fmt"
	"sort"
)

// `check receipt` is the join gate between two records of the same spend: the day file,
// which names what was actually written, and the receipts, which name the individual
// charges a run collected. A receipt is a child of a (day, model, repo) row, and the join
// checks three ways that parentage can be wrong.

// The row this join reads is ReceiptRow, declared once in receipt.go (slice L01) where
// the receipt file's seventeen columns are parsed and formatted. It is one collected
// charge for a run: its (Day, Model, Repo) must name a day file row, its token counts
// must be a subset of that row's, and its Receipt id must be unique among the receipts
// presented together. This join reads only those fields, so it is the same check over
// the parsed row that it was over the sketch it was written against.

// JoinFindingType is the way one receipt join can fail.
type JoinFindingType int

// The three failures a receipt join can name.
const (
	JoinDuplicate JoinFindingType = iota
	JoinExceeds
	JoinOrphan
)

// String is the finding's kind, as it is printed.
func (t JoinFindingType) String() string {
	switch t {
	case JoinDuplicate:
		return "DUPLICATE"
	case JoinExceeds:
		return "EXCEEDS"
	case JoinOrphan:
		return "ORPHAN"
	}
	return "UNKNOWN"
}

// JoinFinding is one wrong join between the receipts and the day rows.
type JoinFinding struct {
	Type    JoinFindingType
	Day     string
	Model   string
	Repo    string
	Receipt string
	Detail  string
}

// checkReceiptCounts turns one receipt's own columns into the five types the day file
// keeps apart, so the two records can be compared type for type.
func checkReceiptCounts(r ReceiptRow) Counts {
	var c Counts
	c.Set(Input, r.InputTokens)
	c.Set(Output, r.OutputTokens)
	c.Set(CacheWrite, r.CacheWriteTokens)
	c.Set(CacheRead, r.CacheReadTokens)
	c.Set(Reasoning, r.ReasoningTokens)
	return c
}

// receiptKey is the join key a receipt and a day row share.
func receiptKey(day, model, repo string) string { return day + "\t" + model + "\t" + repo }

// CheckReceiptJoins joins receipts onto day rows and names every way the join fails. A
// clean run is an empty result: every receipt names a real (day, model, repo) row, every
// receipt id is unique, and the receipts' tokens are a subset of the row's.
func CheckReceiptJoins(dayRows []DayRow, receipts []ReceiptRow) []JoinFinding {
	rowByKey := make(map[string]dayRowRef, len(dayRows))
	for i := range dayRows {
		rowByKey[receiptKey(dayRows[i].Date, dayRows[i].Model, dayRows[i].Repo)] = dayRowRef{row: &dayRows[i]}
	}

	seen := map[string]bool{}
	sums := map[string]*Counts{}

	var out []JoinFinding

	for _, r := range receipts {
		if seen[r.Receipt] {
			out = append(out, JoinFinding{
				Type: JoinDuplicate, Day: r.Day, Model: r.Model, Repo: r.Repo,
				Receipt: r.Receipt,
				Detail:  "a second receipt carries the id " + r.Receipt + "; receipt ids must be unique",
			})
		}
		seen[r.Receipt] = true

		key := receiptKey(r.Day, r.Model, r.Repo)
		if _, ok := rowByKey[key]; !ok {
			out = append(out, JoinFinding{
				Type: JoinOrphan, Day: r.Day, Model: r.Model, Repo: r.Repo,
				Receipt: r.Receipt,
				Detail:  "receipt " + r.Receipt + " names (" + r.Day + ", " + r.Model + ", " + r.Repo + "), which no day row carries",
			})
			continue
		}
		if sums[key] == nil {
			sums[key] = &Counts{}
		}
		sums[key].Add(checkReceiptCounts(r))
	}

	for key, sum := range sums {
		ref, ok := rowByKey[key]
		if !ok {
			continue
		}
		row := ref.row
		for t := Type(0); t < NTypes; t++ {
			dayN, dayHas := row.Counts.Get(t)
			gotN, gotHas := sum.Get(t)
			if !dayHas || !gotHas || gotN <= dayN {
				continue
			}
			out = append(out, JoinFinding{
				Type: JoinExceeds, Day: row.Date, Model: row.Model, Repo: row.Repo,
				Detail: fmt.Sprintf("receipts sum %d %s tokens, over the %d the day file carries", gotN, TypeNames[t], dayN),
			})
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		if out[i].Day != out[j].Day {
			return out[i].Day < out[j].Day
		}
		if out[i].Model != out[j].Model {
			return out[i].Model < out[j].Model
		}
		if out[i].Repo != out[j].Repo {
			return out[i].Repo < out[j].Repo
		}
		if out[i].Receipt != out[j].Receipt {
			return out[i].Receipt < out[j].Receipt
		}
		return out[i].Detail < out[j].Detail
	})
	return out
}

type dayRowRef struct {
	row *DayRow
}
