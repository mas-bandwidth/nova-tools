package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// cost attach prices a consumer record that ended with no tokens (docs/SPEC-SPRINT.md,
// "What a card cost"). verbs.go inserts installVerbs when it builds the verb table,
// and this init runs first, so the row is in that slice before the insert.
func init() {
	installVerbs = append(installVerbs, verb{
		"cost attach",
		"<card>.<attempt> --model <provider/model> --input <n> --cache-read <n> --cache-write <n> --output <n> [--reasoning <n>] [--usd <x>] [--source <text>] [--replace] [--dry-run] | --file <tsv> [--replace] [--dry-run]",
		"cost attach s1-1.1 --model opencode/deepseek-v4-flash --input 1 --cache-read 0 --cache-write 0 --output 1 --dry-run",
		(*app).cmdCostAttach,
	})
	verbClasses["cost attach"] = classCoordinator
	verbEffect["cost attach"] = "store write: prices an existing cost record from the counts given, or from each row of --file, and moves the card's total, a landed card's cost, its stream's sum and the work card's usage; a record that already holds tokens is refused unless --replace, and the old figures go on the card's story; --file is all or none; --dry-run writes nothing"
}

// attachRow is one record to price: a positional card.attempt, or one TSV row.
type attachRow struct {
	card, model, usd, source string
	attempt                  int
	line                     int // 1-based file line; 0 for the positional form
	tokens                   cardcost.Tokens
}

// attachOut is one result line.
type attachOut struct {
	Card, Model, Route, Predicted, Actual, Replaced string
	Attempt                                         int
}

func (r attachOut) line() string {
	s := fmt.Sprintf("COST ATTACH card=%s attempt=%d route=%s model=%s predicted_usd=%s actual_usd=%s",
		oneline.Field(r.Card), r.Attempt, oneline.Field(orDashStr(r.Route, "-")), oneline.Field(r.Model), orDashStr(r.Predicted, "-"), orDashStr(r.Actual, "-"))
	if r.Replaced != "" {
		s += " replaced=" + oneline.Field(r.Replaced)
	}
	return s
}

func (a *app) cmdCostAttach(args []string, stdout, stderr io.Writer) int {
	const verb = "cost attach"
	fs, c := a.verbSetup(verb)
	model := fs.String("model", "", "the provider/model the run used")
	input := fs.Int64("input", -1, "uncached input tokens")
	cacheRead := fs.Int64("cache-read", -1, "cache read tokens")
	cacheWrite := fs.Int64("cache-write", -1, "cache write tokens")
	output := fs.Int64("output", -1, "output tokens")
	reasoning := fs.Int64("reasoning", -1, "reasoning tokens (default: not reported)")
	usd := fs.String("usd", "", "the harness's own cost, a non-negative decimal")
	source := fs.String("source", "", "where the figures came from, kept on the story when --replace")
	file := fs.String("file", "", "a TSV of card, attempt, model, input, cache_read, cache_write, output, reasoning, usd, source; one bad row writes nothing")
	replace := fs.Bool("replace", false, "price a record that already holds tokens, and keep the old figures on the card's story")
	dry := fs.Bool("dry-run", false, "print what attach would do, and write nothing")
	pos, err := parse(fs, args)
	if err != nil {
		return refuse(stderr, verb, argErr("", err))
	}
	if why := attachUsage(pos, *file, *model, *usd, *source, *input, *cacheRead, *cacheWrite, *output, *reasoning); why != "" {
		return refuse(stderr, verb, why)
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	var rows []attachRow
	if *file != "" {
		var why string
		rows, why = attachFile(*file)
		if why != "" {
			return attachMiss(stderr, why)
		}
	} else {
		card, attempt, ok := splitAttach(pos[0])
		if !ok {
			return refuse(stderr, verb, pos[0]+" is not <card>.<attempt>: a work card s1-1.w1, or a primary and its attempt s1-1.1")
		}
		rows = []attachRow{{
			card: card, attempt: attempt, model: *model, usd: *usd, source: *source,
			tokens: cardcost.Tokens{Input: *input, CacheRead: *cacheRead, CacheWrite: *cacheWrite, Output: *output, Reasoning: *reasoning, Requests: cardcost.Unreported, MaxPrompt: cardcost.Unreported},
		}}
	}
	ctx := context.Background()
	load := func() (*sprint.Snapshot, int) {
		rs, _, err := st.Routes(ctx)
		if err != nil {
			return nil, a.readFailed(verb, err, stderr)
		}
		s, err := st.Load(ctx, []string{sprint.Work, sprint.Merge, sprint.Fleet}, nil)
		if err != nil {
			return nil, a.readFailed(verb, err, stderr)
		}
		s.Routes = rs
		return s, 0
	}
	var outs []attachOut
	step := store.Step{Named: true, Verb: verb, Load: []string{sprint.Work, sprint.Merge, sprint.Fleet}, Prices: true, Mirrors: true,
		Plan: func(s *sprint.Snapshot) sprint.Plan {
			p, got := planAttach(s, rows, *replace, c.actor)
			outs = got
			return p
		}}
	if *dry {
		s, code := load()
		if code != 0 {
			return code
		}
		p := step.Plan(s)
		if len(p.Refused) > 0 {
			return attachMiss(stderr, p.Refused[0].Why)
		}
		return attachPrint(stdout, outs, true, c.json)
	}
	res, err := st.Run(ctx, step)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if len(res.Refused) > 0 {
		return attachMiss(stderr, res.Refused[0].Why)
	}
	if res.Lost {
		return attachMiss(stderr, "the attach lost every attempt to other writers and wrote nothing")
	}
	return attachPrint(stdout, outs, false, c.json)
}

// attachUsage is the flag refusal, before the store is opened. "" is a line the verb can run.
func attachUsage(pos []string, file, model, usd, source string, input, cacheRead, cacheWrite, output, reasoning int64) string {
	if file != "" {
		if len(pos) > 0 || model != "" || usd != "" || source != "" || input >= 0 || cacheRead >= 0 || cacheWrite >= 0 || output >= 0 || reasoning >= 0 {
			return "--file takes the rows alone, with --replace and --dry-run"
		}
		return ""
	}
	if len(pos) != 1 {
		return "wants <card>.<attempt> or --file <tsv>"
	}
	if model == "" {
		return "--model <provider/model> is required"
	}
	if strings.Contains(model, " ") {
		return "--model wants provider/model, one word"
	}
	for _, spec := range []struct {
		name string
		n    int64
	}{{"--input", input}, {"--cache-read", cacheRead}, {"--cache-write", cacheWrite}, {"--output", output}} {
		if spec.n < 0 {
			return spec.name + " is required, a whole number from 0"
		}
	}
	if reasoning < -1 {
		return "--reasoning wants a whole number from 0, or omit it"
	}
	if usd != "" {
		if _, err := cardcost.Decimal(strings.TrimPrefix(usd, "$")); err != nil {
			return "--usd wants a non-negative decimal"
		}
	}
	return ""
}

// attachMiss is a record the verb could not price: exit 1, nothing written.
func attachMiss(stderr io.Writer, what string) int {
	fmt.Fprintf(stderr, "%s cost attach: %s\n", prog, oneline.Escape(what))
	return 1
}

func attachPrint(stdout io.Writer, outs []attachOut, dry, asJSON bool) int {
	if asJSON {
		b, err := json.Marshal(map[string]any{"records": outs, "dry_run": dry})
		if err != nil {
			fmt.Fprintf(stdout, "%s cost attach: %s\n", prog, oneline.Escape(err.Error()))
			return 1
		}
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	for _, o := range outs {
		fmt.Fprintln(stdout, o.line())
	}
	if dry {
		fmt.Fprintf(stdout, "COST ATTACH DRY-RUN records=%d: nothing was written\n", len(outs))
		return 0
	}
	fmt.Fprintf(stdout, "COST ATTACH OK records=%d\n", len(outs))
	return 0
}

// splitAttach reads <card>.<attempt>. Digits after the last dot are the attempt
// (s1-1.1 is primary s1-1 at attempt 1; s1-1.w1.1 is work card s1-1.w1). A work
// card id alone (s1-1.w1) is that card at the attempt its .w suffix names.
func splitAttach(word string) (string, int, bool) {
	i := strings.LastIndex(word, ".")
	if i <= 0 || i == len(word)-1 {
		return "", 0, false
	}
	tail := word[i+1:]
	if n, ok := positiveAttach(tail); ok {
		return word[:i], n, true
	}
	if rest, ok := strings.CutPrefix(tail, "w"); ok {
		if n, ok := positiveAttach(rest); ok {
			return word, n, true
		}
	}
	return "", 0, false
}

func positiveAttach(tail string) (int, bool) {
	n, err := strconv.Atoi(tail)
	if err != nil || n < 1 || strconv.Itoa(n) != tail {
		return 0, false
	}
	return n, true
}

// attachFileCap bounds a --file read.
const attachFileCap = 1 << 20

// attachFile reads the TSV. A bad row is named and the call writes nothing.
func attachFile(path string) ([]attachRow, string) {
	fi, err := os.Lstat(path)
	switch {
	case err != nil:
		return nil, path + ": " + err.Error()
	case !fi.Mode().IsRegular():
		return nil, path + " is a symlink or not a regular file"
	case fi.Size() > attachFileCap:
		return nil, path + " is larger than the attach file can be"
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, path + ": " + err.Error()
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64*1024), attachFileCap)
	var rows []attachRow
	seen := map[string]int{}
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" {
			continue
		}
		fields := strings.Split(text, "\t")
		for i := range fields {
			fields[i] = strings.TrimSpace(fields[i])
		}
		if len(rows) == 0 && line == 1 && strings.EqualFold(fields[0], "card") {
			continue
		}
		row, why := attachParseRow(fields, line)
		if why != "" {
			return nil, why
		}
		key := row.card + "#" + strconv.Itoa(row.attempt)
		if prev, ok := seen[key]; ok {
			return nil, attachWhere(row) + " duplicates line " + strconv.Itoa(prev)
		}
		seen[key] = line
		rows = append(rows, row)
	}
	if err := sc.Err(); err != nil {
		return nil, path + ": " + err.Error()
	}
	if len(rows) == 0 {
		return nil, path + " has no rows"
	}
	return rows, ""
}

func attachWhere(row attachRow) string {
	if row.line == 0 {
		return row.card + " attempt " + strconv.Itoa(row.attempt)
	}
	return "line " + strconv.Itoa(row.line) + ": " + row.card + " attempt " + strconv.Itoa(row.attempt)
}

func attachParseRow(fields []string, line int) (attachRow, string) {
	row := attachRow{line: line, tokens: cardcost.None()}
	name := func(why string) (attachRow, string) {
		if row.card == "" {
			row.card = "-"
		}
		return attachRow{}, "line " + strconv.Itoa(line) + ": " + row.card + " attempt " + strconv.Itoa(row.attempt) + ": " + why
	}
	if len(fields) < 7 || len(fields) > 10 {
		return name("want 7 to 10 tab-separated fields (card, attempt, model, input, cache_read, cache_write, output, reasoning, usd, source)")
	}
	for len(fields) < 10 {
		fields = append(fields, "")
	}
	row.card = fields[0]
	if row.card == "" || strings.ContainsAny(row.card, " \t") {
		return name("card is missing")
	}
	n, err := strconv.Atoi(fields[1])
	if err != nil || n < 1 || strconv.Itoa(n) != fields[1] {
		return name("attempt wants a whole number from 1")
	}
	row.attempt = n
	row.model = fields[2]
	if row.model == "" || strings.Contains(row.model, " ") {
		return name("model wants provider/model")
	}
	counts := []*int64{&row.tokens.Input, &row.tokens.CacheRead, &row.tokens.CacheWrite, &row.tokens.Output}
	names := []string{"input", "cache_read", "cache_write", "output"}
	for i, p := range counts {
		v, err := strconv.ParseInt(fields[3+i], 10, 64)
		if err != nil || v < 0 || strconv.FormatInt(v, 10) != fields[3+i] {
			return name(names[i] + " wants a whole number from 0")
		}
		*p = v
	}
	if fields[7] == "" {
		row.tokens.Reasoning = cardcost.Unreported
	} else {
		v, err := strconv.ParseInt(fields[7], 10, 64)
		if err != nil || v < 0 || strconv.FormatInt(v, 10) != fields[7] {
			return name("reasoning wants a whole number from 0, or nothing")
		}
		row.tokens.Reasoning = v
	}
	if fields[8] != "" {
		r, err := cardcost.Decimal(strings.TrimPrefix(fields[8], "$"))
		if err != nil {
			return name("usd " + strconv.Quote(fields[8]) + " is not a decimal")
		}
		row.usd = cardcost.Text(r)
	}
	row.source = fields[9]
	return row, ""
}

// attachHit is the one consumer a row prices.
type attachHit struct {
	pr   *sprint.Card
	cons sprint.Consumer
}

// planAttach is the step: every row, or one refusal and nothing written.
func planAttach(s *sprint.Snapshot, rows []attachRow, replace bool, who string) (sprint.Plan, []attachOut) {
	var p sprint.Plan
	if s == nil || s.Work == nil {
		p.Refused = []sprint.Refusal{{Key: "cost attach", Why: "the work table is not there"}}
		return p, nil
	}
	hits := make([]attachHit, len(rows))
	for i, row := range rows {
		hit, why := findAttach(s, row.card, row.attempt)
		if why != "" {
			p.Refused = []sprint.Refusal{{Key: row.card, Why: attachWhere(row) + ": " + why}}
			return p, nil
		}
		if hit.cons.Usage.Tokens.Reported() && !replace {
			p.Refused = []sprint.Refusal{{Key: hit.cons.Key, Why: attachWhere(row) + ": the record already holds tokens; pass --replace to keep the old figures on the story"}}
			return p, nil
		}
		hits[i] = hit
	}
	type primary struct {
		pr    *sprint.Card
		set   map[string]string
		usage map[string]cardcost.Usage
		fleet map[string]string
		story string
		cut   bool
		total cardcost.Total
	}
	by := map[string]*primary{}
	var order []string
	outs := make([]attachOut, 0, len(rows))
	newCost := map[string]string{}
	for i, row := range rows {
		hit := hits[i]
		b := by[hit.pr.ID]
		if b == nil {
			b = &primary{pr: hit.pr, set: map[string]string{}, usage: map[string]cardcost.Usage{}, fleet: map[string]string{}}
			for _, c := range sprint.CardCostOf(hit.pr).Consumers {
				b.usage[c.Key] = c.Usage
			}
			b.cut = hit.pr.Int(sprint.FieldCostCut) > 0
			if b.cut {
				b.total = cardcost.ParseTotal(hit.pr.F(sprint.FieldCostTotal))
			}
			by[hit.pr.ID] = b
			order = append(order, hit.pr.ID)
		}
		priced, route := priceAttach(s.Routes, row, hit.cons.Usage)
		cons := hit.cons
		if route != "" {
			cons.Route = route
		}
		cons.Model = row.model
		cons.Usage = priced
		b.set[sprint.FieldCostRecord+cons.Key] = attachConsumerLine(cons)
		if b.cut {
			b.total = b.total.Repriced(b.usage[cons.Key], priced)
		}
		b.usage[cons.Key] = priced
		if s.Fleet != nil {
			if wc := s.Fleet.Card(cons.Card); wc != nil && wc.Placed() {
				prev := cardcost.ParseUsage(wc.F(sprint.FieldUsage))
				if cons.Take == 0 || !prev.Tokens.Reported() || replace {
					b.fleet[wc.ID] = priced.String()
				}
			}
		}
		out := attachOut{Card: row.card, Attempt: row.attempt, Model: row.model, Route: priced.Route, Predicted: priced.Predicted, Actual: priced.Actual}
		if replace {
			old := hit.cons.Usage.String()
			out.Replaced = old
			bit := "replaced " + old
			if row.source != "" {
				bit += " source " + row.source
			}
			if b.story != "" {
				b.story += "; "
			}
			b.story += bit
		}
		outs = append(outs, out)
	}
	for _, id := range order {
		b := by[id]
		var us []cardcost.Usage
		for _, u := range b.usage {
			us = append(us, u)
		}
		total := cardcost.SumUsage(us)
		if b.cut {
			total = b.total
		}
		b.set[sprint.FieldCostTotal] = total.String()
		var unset []string
		if b.pr.Col == sprint.Landed && total.Charged != b.pr.F(sprint.FieldCost) {
			if total.Charged == "" {
				unset = append(unset, sprint.FieldCost)
			} else {
				b.set[sprint.FieldCost] = total.Charged
			}
			newCost[b.pr.ID] = total.Charged
		}
		changes := []sprint.Change{{Table: sprint.Work, Entry: attachEntry(b.pr, b.set, unset...)}}
		for _, fid := range fleetIDs(b.fleet) {
			wc := s.Fleet.Card(fid)
			changes = append(changes, sprint.Change{Table: sprint.Fleet, Entry: attachEntry(wc, map[string]string{sprint.FieldUsage: b.fleet[fid]})})
		}
		moved := b.pr.ID + " cost attached"
		var notes []sprint.Note
		if b.story != "" {
			moved = b.story
			notes = []sprint.Note{{Kind: sprint.Happened, Type: "cost attached", Stream: b.pr.Row, Primaries: []string{b.pr.ID}, Count: 1, At: s.Now, Who: who, What: b.story}}
		}
		p.Units = append(p.Units, sprint.Unit{Key: b.pr.ID, Stream: b.pr.Row, Changes: changes, Notes: notes, Moved: moved})
	}
	if s.Merge != nil {
		streams := map[string]bool{}
		for id := range newCost {
			streams[by[id].pr.Row] = true
		}
		names := make([]string, 0, len(streams))
		for name := range streams {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, stName := range names {
			ctl := s.StreamCtl(stName)
			if ctl == nil {
				continue
			}
			var sum []string
			for _, c := range s.Work.Cell(stName, sprint.Landed) {
				v, ok := newCost[c.ID]
				if !ok {
					v = c.F(sprint.FieldCost)
				}
				if v != "" {
					sum = append(sum, v)
				}
			}
			total, ok := cardcost.Sum(sum...)
			if len(sum) == 0 {
				total, ok = "", true
			}
			if !ok || total == ctl.F(sprint.FieldCost) {
				continue
			}
			set := map[string]string{}
			var unset []string
			if total == "" {
				unset = attachUnset(ctl, sprint.FieldCost)
			} else {
				set[sprint.FieldCost] = total
			}
			p.Units = append(p.Units, sprint.Unit{Key: ctl.ID, Stream: stName, Changes: []sprint.Change{{Table: sprint.Merge, Entry: attachEntry(ctl, set, unset...)}},
				Moved: stName + " cost attached"})
		}
	}
	return p, outs
}

func fleetIDs(m map[string]string) []string {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// findAttach is the attempt's record: the card's own take when there is one.
func findAttach(s *sprint.Snapshot, card string, attempt int) (attachHit, string) {
	var hits []attachHit
	for _, pr := range s.Work.Column(sprint.States...) {
		for _, cons := range sprint.CardCostOf(pr).Consumers {
			if cons.Attempt != attempt {
				continue
			}
			if cons.Card != card && pr.ID != card {
				continue
			}
			hits = append(hits, attachHit{pr: pr, cons: cons})
		}
	}
	var own []attachHit
	for _, h := range hits {
		if h.cons.Take == 0 {
			own = append(own, h)
		}
	}
	switch {
	case len(own) == 1:
		return own[0], ""
	case len(own) > 1:
		return attachHit{}, "more than one record for that attempt; name the work card"
	case len(hits) == 1:
		return hits[0], ""
	case len(hits) > 1:
		return attachHit{}, "more than one record for that attempt; name the work card"
	default:
		return attachHit{}, "no cost record"
	}
}

// priceAttach is the row priced by the first route, in name order, whose provider/model
// matches. No row leaves the counts unpriced=no-route. Waiting and running time stay.
func priceAttach(routes []sprint.Route, row attachRow, old cardcost.Usage) (cardcost.Usage, string) {
	u := old
	u.Tokens = row.tokens
	u.Model = row.model
	u.Extra = nil
	u.Long = false
	if row.usd != "" {
		u.Actual, u.ActualBy = row.usd, cardcost.ActualByHarness
	} else {
		u.Actual, u.ActualBy = "", ""
	}
	rs := append([]sprint.Route(nil), routes...)
	sort.Slice(rs, func(i, j int) bool { return rs[i].Name < rs[j].Name })
	for _, r := range rs {
		if r.Provider+"/"+r.Model == row.model {
			return u.Priced(r.Name, r.Prices), r.Name
		}
	}
	return u.Priced("", cardcost.Prices{}), ""
}

// attachConsumerLine is the primary's record line (sprint.Consumer.line is unexported).
func attachConsumerLine(c sprint.Consumer) string {
	orDash := func(v string) string {
		if v == "" {
			return "-"
		}
		return v
	}
	w := []string{
		"kind=" + c.Kind, "card=" + c.Card, "attempt=" + strconv.Itoa(c.Attempt), "take=" + strconv.Itoa(c.Take), "gen=" + strconv.Itoa(c.Gen),
		"who=" + orDash(c.Who), "on_route=" + orDash(c.Route), "on_model=" + orDash(c.Model), "on_tier=" + orDash(c.Tier),
		"end=" + orDash(strings.ReplaceAll(c.End, " ", "-")), "at=" + orDash(c.At),
	}
	if c.Cap != "" {
		w = append(w, "lane_cap="+c.Cap, "lane_overrun="+orDash(c.Overrun))
	}
	return strings.Join(w, " ") + " " + c.Usage.String()
}

// attachEntry changes fields of a card in place (sprint.setEntry is unexported).
func attachEntry(c *sprint.Card, set map[string]string, unset ...string) ntable.BatchMemberEntry {
	e := ntable.BatchMemberEntry{ID: c.ID, Expect: &ntable.MemberExpect{Revision: strconv.FormatUint(c.Rev, 10), Place: &ntable.PlaceExpect{Row: c.Row, Col: c.Col}}}
	if len(set) > 0 {
		e.Set = set
	}
	if u := attachUnset(c, unset...); len(u) > 0 {
		e.Unset = u
	}
	return e
}

func attachUnset(c *sprint.Card, names ...string) []string {
	var out []string
	for _, n := range names {
		if c.Has(n) {
			out = append(out, n)
		}
	}
	return out
}
