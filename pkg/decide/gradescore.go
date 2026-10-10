package decide

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Grading the grades (SPEC-NOVA-DECIDE section 11, "Scoring the grades"): the grade
// decisions made in one UTC day, joined by card to what the sprint log says happened to
// the card, tabled by Jev's grade against the tier the card was first dealt on, and by the
// p of its grade against whether the flash first attempt failed.

// LogLine is one line of `nova-sprint log --json`, the fields the score reads.
type LogLine struct {
	Card    string            `json:"card"`
	Primary string            `json:"primary"`
	Table   string            `json:"table"`
	To      string            `json:"to"`
	Removed bool              `json:"removed"`
	Set     map[string]string `json:"set"`
}

// CardFacts is what the log holds of one card: where it ended, the tier of its first
// attempt ("" when it was never dealt or attempt 1 is before the log), the highest attempt
// number, whether attempt 1 failed, and whether a pro attempt ran.
type CardFacts struct {
	State        string // landed, dropped, or the column it sits in
	Start        string
	MaxAttempt   int
	FirstFailed  bool
	Escalated    bool
	attempts     map[int]costRecord
	dealtOnFleet bool
}

type costRecord struct {
	attempt          int
	tier, end, route string
}

// ReadLog reads a `nova-sprint log --json` export ({"lines":[...]}) into each primary's
// facts: its last placement on the work table, and the last text under each of its
// cost_record keys (SPEC-NOVA-DECIDE section 11).
func ReadLog(r io.Reader) (map[string]*CardFacts, error) {
	var doc struct {
		Lines []LogLine `json:"lines"`
	}
	if err := json.NewDecoder(r).Decode(&doc); err != nil {
		return nil, fmt.Errorf("the log is not a nova-sprint log --json export: %w", err)
	}
	facts := map[string]*CardFacts{}
	for _, l := range doc.Lines {
		if l.Primary == "" {
			continue
		}
		f := facts[l.Primary]
		if f == nil {
			f = &CardFacts{attempts: map[int]costRecord{}}
			facts[l.Primary] = f
		}
		if l.Table == "work" && l.Card == l.Primary {
			if _, col, ok := strings.Cut(l.To, ":"); ok && !l.Removed {
				f.State = col
			}
			if l.Removed && l.Set["outcome"] == "dropped" {
				f.State = LabelDropped
			}
		}
		for k, v := range l.Set {
			if strings.HasPrefix(k, "cost_record:") {
				if r := parseCost(v); r.attempt > 0 {
					f.attempts[r.attempt] = r
				}
			}
		}
	}
	for _, f := range facts {
		f.settle()
	}
	return facts, nil
}

// parseCost reads a work cost record, `kind=work attempt=1 on_tier=flash end=failed ...`;
// any other kind has attempt 0.
func parseCost(s string) costRecord {
	var r costRecord
	work := false
	for _, kv := range strings.Fields(s) {
		k, v, _ := strings.Cut(kv, "=")
		switch k {
		case "kind":
			work = v == "work"
		case "attempt":
			r.attempt, _ = strconv.Atoi(v)
		case "on_tier":
			r.tier = v
		case "end":
			r.end = v
		case "on_route":
			r.route = v
		}
	}
	if !work {
		r.attempt = 0
	}
	return r
}

func (f *CardFacts) settle() {
	if f.State == "" {
		f.State = "unplaced"
	}
	for _, a := range f.attempts {
		f.MaxAttempt = max(f.MaxAttempt, a.attempt)
		f.Escalated = f.Escalated || a.tier == GradePro
		f.dealtOnFleet = f.dealtOnFleet || a.route != "-"
	}
	if a, ok := f.attempts[1]; ok {
		f.Start, f.FirstFailed = a.tier, a.end != "ok"
	}
}

// GradeRow is one line of the grade-by-dealt table: the cards Jev graded Grade that were
// first dealt on Dealt.
type GradeRow struct {
	Grade, Dealt                             string
	N, Landed2, Landed, ToPro, Dropped, Open int
}

// BucketRow is one line of the calibration table: the cards graded Grade, dealt on flash,
// whose p for the grade fell in Bucket.
type BucketRow struct {
	Grade, Bucket           string
	N, FirstFailed, Landed2 int
}

// GradeScore is the day's score.
type GradeScore struct {
	Decisions, Cards, NoLog int // grade decisions in the day, the cards they grade, cards the log lacks
	Rows                    []GradeRow
	Buckets                 []BucketRow
}

var gradeBuckets = []struct {
	name   string
	lo, hi float64
}{{"<0.7", 0, 0.7}, {"0.7-0.85", 0.7, 0.85}, {"0.85-0.95", 0.85, 0.95}, {">=0.95", 0.95, 1.01}}

// ScoreGrades scores the grade decisions whose At is in [from, to): each card by its newest
// grade in the window, against facts. A card the log does not hold, or never dealt to the
// fleet, is counted and left out of the tables (a friend's card has no tier to grade).
// Open is a card neither landed nor dropped; Landed2 is a card landed by its second attempt.
func ScoreGrades(ds []Decision, facts map[string]*CardFacts, from, to time.Time) GradeScore {
	var s GradeScore
	latest := map[string]Decision{}
	for _, d := range ds {
		at, err := time.Parse(time.RFC3339, d.At)
		if d.Decision != GradeName || err != nil || at.Before(from) || !at.Before(to) {
			continue
		}
		s.Decisions++
		card, _, _ := strings.Cut(d.ID, "@")
		if cur, ok := latest[card]; !ok || d.At >= cur.At {
			latest[card] = d
		}
	}
	s.Cards = len(latest)
	rows := map[[2]string]*GradeRow{}
	buckets := map[[2]string]*BucketRow{}
	for _, card := range slices.Sorted(maps.Keys(latest)) {
		f, ok := facts[card]
		if !ok {
			s.NoLog++
			continue
		}
		if f.Start == "" || !f.dealtOnFleet {
			continue
		}
		a := latest[card].Answers[GradeQuestion]
		landed2 := f.State == "landed" && f.MaxAttempt <= 2
		r := cell(rows, [2]string{a.Value, f.Start}, func(k [2]string) *GradeRow { return &GradeRow{Grade: k[0], Dealt: k[1]} })
		r.N++
		r.ToPro += b2i(f.Start == GradeFlash && f.Escalated)
		switch f.State {
		case "landed":
			r.Landed++
			r.Landed2 += b2i(landed2)
		case LabelDropped:
			r.Dropped++
		default:
			r.Open++
		}
		if f.Start != GradeFlash || a.Value == GradeScript {
			continue
		}
		p := a.P[a.Value]
		for _, b := range gradeBuckets {
			if p >= b.lo && p < b.hi {
				br := cell(buckets, [2]string{a.Value, b.name}, func(k [2]string) *BucketRow { return &BucketRow{Grade: k[0], Bucket: k[1]} })
				br.N++
				br.FirstFailed += b2i(f.FirstFailed)
				br.Landed2 += b2i(landed2)
			}
		}
	}
	for _, g := range []string{GradeFlash, GradePro, GradeScript} {
		for _, d := range []string{GradeFlash, GradePro} {
			if r := rows[[2]string{g, d}]; r != nil {
				s.Rows = append(s.Rows, *r)
			}
		}
	}
	for _, g := range []string{GradeFlash, GradePro} {
		for _, b := range gradeBuckets {
			if r := buckets[[2]string{g, b.name}]; r != nil {
				s.Buckets = append(s.Buckets, *r)
			}
		}
	}
	return s
}

func cell[T any](m map[[2]string]*T, k [2]string, mk func([2]string) *T) *T {
	if m[k] == nil {
		m[k] = mk(k)
	}
	return m[k]
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
