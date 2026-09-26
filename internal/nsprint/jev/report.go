package jev

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"
)

// Row is one decision row as the report reads it.
type Row struct {
	Type, Subject       string
	Rules, Jev, Version string
	Outcome, OutcomeBy  string
	Cost                float64
	CostKnown           bool
	MS                  int64
	HasMS               bool
}

// RowFrom reads a row hash.
func RowFrom(h map[string]string) Row {
	r := Row{Type: h["type"], Subject: h["subject"], Rules: h["rules"], Jev: h["jev"], Version: h["prompt_version"],
		Outcome: h["outcome"], OutcomeBy: h["outcome_by"]}
	if c, err := strconv.ParseFloat(h["cost"], 64); err == nil {
		r.Cost, r.CostKnown = c, true
	}
	if ms, err := strconv.ParseInt(h["ms"], 10, 64); err == nil {
		r.MS, r.HasMS = ms, true
	}
	return r
}

// Sources are who answers a decision: the rules the structure acts on today,
// and Jev in shadow.
const (
	SourceRules = "rules"
	SourceJev   = "jev"
)

// Line is one report line: one type, one source, one prompt version (the
// rules' version is "rules").
type Line struct {
	Type, Source, Version string
	// Answers is the rows this source answered; Outcomes those whose
	// outcome is recorded; Agree those whose answer is the outcome; Open
	// those still waiting for one.
	Answers, Outcomes, Agree, Open int
	// Overrides is the outcomes a person recorded (not card, merging,
	// landed or closed): the coordinator confirming or overriding; and
	// AgreeOverrides how many of those this source's answer matched.
	Overrides, AgreeOverrides int
	Cost                      float64
	Costed                    int
	MSTotal                   int64
	Timed                     int
}

// mechanical are the outcome writers that are the system, not a person.
var mechanical = map[string]bool{"card": true, "merging": true, kLanded: true, kClosed: true}

// Summarize is the report: per type, source and version, answers against
// outcomes. Types in Types order, then any other, sorted; rules before jev;
// versions sorted.
func Summarize(rows []Row) []Line {
	idx := map[[3]string]*Line{}
	var order [][3]string
	add := func(r Row, source, version, answer string) {
		k := [3]string{r.Type, source, version}
		l := idx[k]
		if l == nil {
			l = &Line{Type: r.Type, Source: source, Version: version}
			idx[k] = l
			order = append(order, k)
		}
		l.Answers++
		if r.Outcome == "" {
			l.Open++
		} else {
			l.Outcomes++
			if answer == r.Outcome {
				l.Agree++
			}
			if !mechanical[r.OutcomeBy] {
				l.Overrides++
				if answer == r.Outcome {
					l.AgreeOverrides++
				}
			}
		}
		if source == SourceJev {
			if r.CostKnown {
				l.Cost += r.Cost
				l.Costed++
			}
			if r.HasMS {
				l.MSTotal += r.MS
				l.Timed++
			}
		}
	}
	for _, r := range rows {
		if r.Rules != "" {
			add(r, SourceRules, SourceRules, r.Rules)
		}
		if r.Jev != "" {
			v := r.Version
			if v == "" {
				v = "-"
			}
			add(r, SourceJev, v, r.Jev)
		}
	}
	rank := map[string]int{}
	for i, t := range Types {
		rank[t] = i + 1
	}
	sort.SliceStable(order, func(i, j int) bool {
		a, b := order[i], order[j]
		ra, rb := rank[a[0]], rank[b[0]]
		if ra == 0 {
			ra = len(Types) + 1
		}
		if rb == 0 {
			rb = len(Types) + 1
		}
		if ra != rb {
			return ra < rb
		}
		if a[0] != b[0] {
			return a[0] < b[0]
		}
		if a[1] != b[1] {
			return a[1] == SourceRules
		}
		return a[2] < b[2]
	})
	out := make([]Line, len(order))
	for i, k := range order {
		out[i] = *idx[k]
	}
	return out
}

// pct is n of d as a whole percent; "-" when d is 0 (no evidence is not
// negative evidence).
func pct(n, d int) string {
	if d == 0 {
		return "-"
	}
	return strconv.Itoa((200*n+d)/(2*d)) + "%"
}

// String is the line as the report prints it.
func (l Line) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "JEV type=%s source=%s version=%s answers=%d outcomes=%d agree=%d agreement=%s open=%d overrides=%d override_agreement=%s",
		l.Type, l.Source, l.Version, l.Answers, l.Outcomes, l.Agree, pct(l.Agree, l.Outcomes), l.Open, l.Overrides,
		pct(l.AgreeOverrides, l.Overrides))
	if l.Source == SourceJev {
		cost, ms := "-", "-"
		if l.Costed > 0 {
			cost = "$" + strconv.FormatFloat(l.Cost, 'f', 6, 64)
		}
		if l.Timed > 0 {
			ms = strconv.FormatInt(l.MSTotal/int64(l.Timed), 10)
		}
		fmt.Fprintf(&b, " cost=%s ms=%s", cost, ms)
	}
	return b.String()
}

// Filter keeps the lines of type t and prompt version v ("" is any; a
// version filter keeps the rules' lines of the same type beside it, the
// baseline it is measured against).
func Filter(lines []Line, t, v string) []Line {
	var out []Line
	for _, l := range lines {
		if t != "" && l.Type != t {
			continue
		}
		if v != "" && l.Version != v && l.Source != SourceRules {
			continue
		}
		out = append(out, l)
	}
	if v == "" {
		return out
	}
	// a rules line stays only beside a jev line of the version asked
	keep := map[string]bool{}
	for _, l := range out {
		if l.Source == SourceJev {
			keep[l.Type] = true
		}
	}
	var kept []Line
	for _, l := range out {
		if l.Source == SourceJev || keep[l.Type] {
			kept = append(kept, l)
		}
	}
	return kept
}

// LoadRows reads every row of the named types (every type in jev:types when
// none is named): one SMEMBERS, one ZRANGE per type in one pipeline, and one
// HGETALL per row in one pipeline; pending is SCARD jev:pending.
func LoadRows(ctx context.Context, c redis.Cmdable, types []string) (rows []Row, pending int64, err error) {
	var pend *redis.IntCmd
	var known *redis.StringSliceCmd
	if _, err := c.Pipelined(ctx, func(p redis.Pipeliner) error {
		pend = p.SCard(ctx, KeyPending)
		known = p.SMembers(ctx, KeyTypes)
		return nil
	}); err != nil {
		return nil, 0, fmt.Errorf("read %s: %w", KeyTypes, err)
	}
	if len(types) == 0 {
		types = known.Val()
		sort.Strings(types)
	}
	if len(types) == 0 {
		return nil, pend.Val(), nil
	}
	subs := make([]*redis.StringSliceCmd, len(types))
	if _, err := c.Pipelined(ctx, func(p redis.Pipeliner) error {
		for i, t := range types {
			subs[i] = p.ZRange(ctx, IndexKey(t), 0, -1)
		}
		return nil
	}); err != nil {
		return nil, 0, fmt.Errorf("read the row indexes: %w", err)
	}
	var keys []string
	for i, t := range types {
		for _, s := range subs[i].Val() {
			keys = append(keys, RowKey(t, s))
		}
	}
	hs := map[string]map[string]string{}
	if err := hgetAll(ctx, c, keys, func(k string) string { return k }, hs); err != nil {
		return nil, 0, err
	}
	for _, k := range keys {
		if h := hs[k]; h != nil {
			rows = append(rows, RowFrom(h))
		}
	}
	return rows, pend.Val(), nil
}
