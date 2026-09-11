package tokens

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The month sum. It ASSERTS NOTHING and it is never a gate: `check` is the gate. Answering
// is this verb's whole job, so it exits 0 over a month with gaps and `missing=<n>` is the
// answer.
//
// A dash adds nothing and is COUNTED. It is never read as zero, because a zero that meant
// "not measured" would sum into a month claiming to be complete, and rule 15 is the whole
// reason the cell is a dash in the first place.

// Agg is one grouping's totals.
type Agg struct {
	Totals [NTypes]int64
	Dashes [NTypes]int
	Rough  int
	NonUTC int
	days   map[string]bool
	keys   map[string]bool
}

func newAgg() *Agg { return &Agg{days: map[string]bool{}, keys: map[string]bool{}} }

// Days is how many days fed this grouping.
func (a *Agg) Days() int { return len(a.days) }

// Keys is how many distinct models or repos fed it.
func (a *Agg) Keys() int { return len(a.keys) }

func (a *Agg) add(r DayRow, key string) {
	for t := Type(0); t < NTypes; t++ {
		if v, ok := r.Counts.Get(t); ok {
			a.Totals[t] += v
		} else {
			a.Dashes[t]++
		}
	}
	a.Rough += r.Rough
	if r.Basis != UTC {
		a.NonUTC++
	}
	a.days[r.Date] = true
	a.keys[key] = true
}

// Pair is one (model, repo) grouping.
type Pair struct {
	Model, Repo string
	Agg         *Agg
}

// ModelSum is one model grouping.
type ModelSum struct {
	Model string
	Agg   *Agg
}

// Sum is a whole month.
type Sum struct {
	Month    string
	Days     []string
	Missing  []string
	Rows     int
	Turns    int
	HaveTurn bool
	Pairs    []Pair
	Models   []ModelSum
	Total    *Agg
}

// SumMonth walks <out>/<month>-??.tsv in name order. A file whose stamp line is not
// `nova-tokens v1` is an error rather than a finding: `sum` reads day files, and a file
// that is not one is not a thing it can answer about.
func SumMonth(out, month string) (*Sum, error) {
	ents, err := os.ReadDir(out)
	if err != nil {
		return nil, err
	}
	s := &Sum{Month: month, Total: newAgg()}
	pairs := map[string]*Pair{}
	models := map[string]*ModelSum{}
	var names []string
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, FileSuffix) || !strings.HasPrefix(name, month+"-") {
			continue
		}
		if !ValidDay(strings.TrimSuffix(name, FileSuffix)) {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		day, findings, err := ReadDayFile(filepath.Join(out, name))
		if err != nil {
			return nil, err
		}
		for _, f := range findings {
			if f.Line == 1 {
				return nil, &BadDayFile{Path: filepath.Join(out, name), Reason: f.Reason}
			}
		}
		s.Days = append(s.Days, day.Day)
		if day.Turns != "" && day.Turns != Dash {
			if n := atoiSafe(day.Turns); n >= 0 {
				s.Turns += n
				s.HaveTurn = true
			}
		}
		for _, r := range day.Rows {
			s.Rows++
			s.Total.add(r, r.Model+"\t"+r.Repo)
			pk := r.Model + "\t" + r.Repo
			p, ok := pairs[pk]
			if !ok {
				p = &Pair{Model: r.Model, Repo: r.Repo, Agg: newAgg()}
				pairs[pk] = p
			}
			p.Agg.add(r, pk)
			m, ok := models[r.Model]
			if !ok {
				m = &ModelSum{Model: r.Model, Agg: newAgg()}
				models[r.Model] = m
			}
			m.Agg.add(r, r.Repo)
		}
	}
	s.Missing = MissingDays(s.Days)
	for _, p := range pairs {
		s.Pairs = append(s.Pairs, *p)
	}
	for _, m := range models {
		s.Models = append(s.Models, *m)
	}
	// Descending by total, ties by name: the biggest spend first is what a reader of a
	// capped listing came for.
	sort.Slice(s.Pairs, func(i, j int) bool {
		a, b := total(s.Pairs[i].Agg), total(s.Pairs[j].Agg)
		if a != b {
			return a > b
		}
		if s.Pairs[i].Model != s.Pairs[j].Model {
			return s.Pairs[i].Model < s.Pairs[j].Model
		}
		return s.Pairs[i].Repo < s.Pairs[j].Repo
	})
	sort.Slice(s.Models, func(i, j int) bool {
		a, b := total(s.Models[i].Agg), total(s.Models[j].Agg)
		if a != b {
			return a > b
		}
		return s.Models[i].Model < s.Models[j].Model
	})
	return s, nil
}

// BadDayFile is a file under --out that `sum` will not read.
type BadDayFile struct{ Path, Reason string }

func (e *BadDayFile) Error() string { return e.Path + ": " + e.Reason }

func total(a *Agg) int64 {
	var n int64
	for t := Type(0); t < NTypes; t++ {
		n += a.Totals[t]
	}
	return n
}

func atoiSafe(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return -1
		}
		n = n*10 + int(r-'0')
	}
	return n
}
