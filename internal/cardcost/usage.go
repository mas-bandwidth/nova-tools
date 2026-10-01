package cardcost

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Usage is a consumer card's usage record: what one run of a work card (a take) or
// of a read card cost, kept in the card's usage field (and in a failed take's
// record) as one line of key=value words, so the sprint's tables gain no column
// (docs/SPEC-SPRINT.md, "What a card cost"). The member writes what the harness
// reported (wall, budget, the tokens by class, the model, the harness's own cost);
// the sprint's step adds the time and the prediction when the card ends:
//
//	wall=49.49s budget=20088/400000 input=19541 cache_read=36336 cache_write=0
//	output=692 reasoning=70 requests=6 max_prompt=9861 model=opencode/deepseek-v4-pro
//	actual_usd=0.04219614 actual_by=harness wait=3s run=52s price_route=pro-a
//	prices=in:0.27,cr:0.07,out:1.1,ro:true predicted_usd=0.00680779 cost=both
//
// A count the harness did not report is left out, never written as 0; a cost is
// never guessed: actual_usd only when the harness reported one, predicted_usd only
// when the route that served the run has a price sheet (else unpriced=<why>). cost
// says which of the two the record holds: both, predicted, actual or none. A line of
// the older shape (wall= and budget= alone) reads as a record with no token, no
// time and no cost.
type Usage struct {
	Wall     string `json:"wall,omitempty"`   // the harness's wall, as native's line says it ("49.49s")
	Budget   string `json:"budget,omitempty"` // the budget word (swarm.BudgetWord)
	Tokens   Tokens `json:"tokens"`
	Model    string `json:"model,omitempty"`      // provider/model, as the harness reported the run
	Actual   string `json:"actual_usd,omitempty"` // USD the harness reported
	ActualBy string `json:"actual_by,omitempty"`  // who reported it: ActualByHarness
	// Wait is the seconds from dealt (asked) to taken (begun), Run from taken (begun)
	// to the end; Unreported when a stamp is missing.
	Wait int64 `json:"wait_s"`
	Run  int64 `json:"run_s"`
	// Route is the route whose price sheet priced the run, Prices the sheet's copy
	// (Prices.Copy), Predicted the USD, Long the long-context pricing, Unpriced why
	// there is no prediction.
	Route     string `json:"price_route,omitempty"`
	Prices    string `json:"prices,omitempty"`
	Predicted string `json:"predicted_usd,omitempty"`
	Long      bool   `json:"long,omitempty"`
	Unpriced  string `json:"unpriced,omitempty"`
	// Extra are words of the line this record does not know, kept in order.
	Extra []string `json:"-"`
}

// ActualByHarness says the harness reported the cost: opencode prices each message
// from its own model table and keeps the cost, a float, beside its tokens; the
// figure is the decimal of their float sum, the harness's computation and never an
// invoice.
const ActualByHarness = "harness"

// The record's presence words (cost=).
const (
	CostBoth      = "both"
	CostPredicted = "predicted"
	CostActual    = "actual"
	CostNone      = "none"
)

// Present is which of the two costs the record holds.
func (u Usage) Present() string {
	switch {
	case u.Predicted != "" && u.Actual != "":
		return CostBoth
	case u.Predicted != "":
		return CostPredicted
	case u.Actual != "":
		return CostActual
	}
	return CostNone
}

// NoUsage is a record with nothing in it: every count and time unreported.
func NoUsage() Usage { return Usage{Tokens: None(), Wait: Unreported, Run: Unreported} }

// ParseUsage reads a usage line (Usage.String, or the older wall= budget= shape).
func ParseUsage(line string) Usage {
	u := NoUsage()
	counts := map[string]*int64{"input": &u.Tokens.Input, "cache_read": &u.Tokens.CacheRead, "cache_write": &u.Tokens.CacheWrite,
		"output": &u.Tokens.Output, "reasoning": &u.Tokens.Reasoning, "requests": &u.Tokens.Requests, "max_prompt": &u.Tokens.MaxPrompt}
	texts := map[string]*string{"wall": &u.Wall, "budget": &u.Budget, "model": &u.Model, "actual_usd": &u.Actual, "actual_by": &u.ActualBy,
		"price_route": &u.Route, "prices": &u.Prices, "predicted_usd": &u.Predicted, "unpriced": &u.Unpriced}
	for _, w := range strings.Fields(line) {
		k, v, ok := strings.Cut(w, "=")
		switch {
		case !ok:
			u.Extra = append(u.Extra, w)
		case counts[k] != nil:
			if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
				*counts[k] = n
			}
		case texts[k] != nil:
			*texts[k] = v
		case k == "wait" || k == "run":
			d, err := time.ParseDuration(v)
			if err == nil && d >= 0 {
				if k == "wait" {
					u.Wait = int64(d / time.Second)
				} else {
					u.Run = int64(d / time.Second)
				}
			}
		case k == "long":
			u.Long = v == "yes"
		case k == "cost":
			// derived (Present): read back from the fields
		default:
			u.Extra = append(u.Extra, w)
		}
	}
	return u
}

// String is the record's one line, in the order the type documents, each word left
// out when it holds nothing; cost= always closes it.
func (u Usage) String() string {
	var ws []string
	add := func(k, v string) {
		if v != "" {
			ws = append(ws, k+"="+word(v))
		}
	}
	count := func(k string, n int64) {
		if n >= 0 {
			ws = append(ws, k+"="+strconv.FormatInt(n, 10))
		}
	}
	add("wall", u.Wall)
	add("budget", u.Budget)
	count("input", u.Tokens.Input)
	count("cache_read", u.Tokens.CacheRead)
	count("cache_write", u.Tokens.CacheWrite)
	count("output", u.Tokens.Output)
	count("reasoning", u.Tokens.Reasoning)
	count("requests", u.Tokens.Requests)
	count("max_prompt", u.Tokens.MaxPrompt)
	add("model", u.Model)
	add("actual_usd", u.Actual)
	add("actual_by", u.ActualBy)
	if u.Wait >= 0 {
		add("wait", strconv.FormatInt(u.Wait, 10)+"s")
	}
	if u.Run >= 0 {
		add("run", strconv.FormatInt(u.Run, 10)+"s")
	}
	add("price_route", u.Route)
	add("prices", u.Prices)
	add("predicted_usd", u.Predicted)
	if u.Long {
		add("long", "yes")
	}
	add("unpriced", u.Unpriced)
	ws = append(ws, u.Extra...)
	return strings.Join(append(ws, "cost="+u.Present()), " ")
}

// word keeps a value one word: a blank becomes an underscore.
func word(v string) string { return strings.Join(strings.Fields(v), "_") }

// Priced is the record with its prediction under the sheet of the route named,
// the sheet copied beside it: route "" (no route found) is unpriced=no-route.
func (u Usage) Priced(route string, p Prices) Usage {
	u.Route, u.Prices, u.Predicted, u.Long, u.Unpriced = "", "", "", false, ""
	if route == "" {
		u.Unpriced = WhyNoRoute
		return u
	}
	u.Route = route
	pr := Predict(u.Tokens, p)
	if p.Priced() {
		u.Prices = p.Copy()
	}
	u.Predicted, u.Long, u.Unpriced = pr.USD, pr.Long, pr.Why
	return u
}

// Timed is the record with its waiting and running time: from the stamp it was
// dealt (asked) to the one it was taken (begun), and from that to end; a stamp
// missing or unreadable leaves its time unreported.
func (u Usage) Timed(from, began string, end time.Time) Usage {
	t0, e0 := time.Parse(time.RFC3339, from)
	t1, e1 := time.Parse(time.RFC3339, began)
	u.Wait, u.Run = Unreported, Unreported
	if e0 == nil && e1 == nil && !t1.Before(t0) {
		u.Wait = int64(t1.Sub(t0) / time.Second)
	}
	if e1 == nil && !end.Before(t1) {
		u.Run = int64(end.Sub(t1) / time.Second)
	}
	return u
}

// spendKeys are the parts of native's spend= word, in its order.
var spendKeys = []string{"input", "cache_read", "cache_write", "output", "reasoning", "requests", "max_prompt", "cost", "model"}

// SpendWord is what a job spent as native's NATIVE line carries it (spend=), one
// word: input:<n>,cache_read:<n>,cache_write:<n>,output:<n>,reasoning:<n>,
// requests:<n>,max_prompt:<n>,cost:<usd>,model:<provider/model>, each part left out
// when the harness did not report it; "" when it reported nothing.
func SpendWord(t Tokens, cost, model string) string {
	vals := map[string]string{"cost": cost, "model": word(model)}
	for k, n := range map[string]int64{"input": t.Input, "cache_read": t.CacheRead, "cache_write": t.CacheWrite, "output": t.Output,
		"reasoning": t.Reasoning, "requests": t.Requests, "max_prompt": t.MaxPrompt} {
		if n >= 0 {
			vals[k] = strconv.FormatInt(n, 10)
		}
	}
	var parts []string
	for _, k := range spendKeys {
		if v := vals[k]; v != "" && !strings.Contains(v, ",") {
			parts = append(parts, k+":"+v)
		}
	}
	return strings.Join(parts, ",")
}

// ParseSpend is a spend= word's record: its tokens, the harness's cost and its model,
// the cost marked as the harness's.
func ParseSpend(w string) Usage {
	u := NoUsage()
	var line []string
	for _, part := range strings.Split(w, ",") {
		k, v, ok := strings.Cut(part, ":")
		if !ok || v == "" {
			continue
		}
		switch k {
		case "cost":
			if _, err := amount(v); err == nil {
				u.Actual, u.ActualBy = v, ActualByHarness
			}
		case "model":
			u.Model = v
		default:
			line = append(line, k+"="+v)
		}
	}
	u.Tokens = ParseUsage(strings.Join(line, " ")).Tokens
	return u
}

// Total is the sum over a producer card's consumer records: the tokens of each
// class over the records that reported it (Unreported when none did), the waiting
// and running seconds over the records that have them, and each cost over the
// records that hold it, with how many of the records did. Charged is the producer's
// one figure: each record's actual cost where reported, else its predicted one.
type Total struct {
	Records   int    `json:"records"`
	Tokens    Tokens `json:"tokens"`
	Wait      int64  `json:"wait_s"`
	Run       int64  `json:"run_s"`
	Predicted string `json:"predicted_usd"` // "" when no record holds one
	PredOf    int    `json:"predicted_records"`
	Actual    string `json:"actual_usd"`
	ActualOf  int    `json:"actual_records"`
	// ActualBy is who reported the actual costs summed: one word when every record's
	// came from the same reporter, the words joined with + when they differ, "" when
	// none did. The harness's figure is its own (opencode prices each message from
	// its model table), never an invoice.
	ActualBy string `json:"actual_by"`
	// Charged is the sum, over the records, of each one's actual cost where reported,
	// else its predicted one; ChargedOf is how many records had either.
	Charged   string `json:"charged_usd"`
	ChargedOf int    `json:"charged_records"`
}

// NoTotal is the total of no record.
func NoTotal() Total { return Total{Tokens: None(), Wait: Unreported, Run: Unreported} }

// Add is the total with one more record in it, every sum exact.
func (t Total) Add(u Usage) Total {
	add := func(to *int64, n int64) {
		if n >= 0 {
			*to = max(*to, 0) + n
		}
	}
	sum := func(to *string, v string) {
		if s, ok := Sum(*to, v); ok {
			*to = s
		}
	}
	t.Records++
	add(&t.Tokens.Input, u.Tokens.Input)
	add(&t.Tokens.CacheRead, u.Tokens.CacheRead)
	add(&t.Tokens.CacheWrite, u.Tokens.CacheWrite)
	add(&t.Tokens.Output, u.Tokens.Output)
	add(&t.Tokens.Reasoning, u.Tokens.Reasoning)
	add(&t.Tokens.Requests, u.Tokens.Requests)
	if u.Tokens.MaxPrompt > t.Tokens.MaxPrompt {
		t.Tokens.MaxPrompt = u.Tokens.MaxPrompt
	}
	add(&t.Wait, u.Wait)
	add(&t.Run, u.Run)
	if u.Predicted != "" {
		sum(&t.Predicted, u.Predicted)
		t.PredOf++
	}
	if u.Actual != "" {
		sum(&t.Actual, u.Actual)
		t.ActualOf++
		bys := Words(t.ActualBy)
		if by := cmp.Or(u.ActualBy, "-"); !slices.Contains(bys, by) {
			t.ActualBy = strings.Join(append(bys, by), "+")
		}
	}
	if c := cmp.Or(u.Actual, u.Predicted); c != "" {
		sum(&t.Charged, c)
		t.ChargedOf++
	}
	return t
}

// Words splits an actual_by list ("" is none).
func Words(by string) []string {
	if by == "" {
		return nil
	}
	return strings.Split(by, "+")
}

// SumUsage is the total of the records.
func SumUsage(us []Usage) Total {
	t := NoTotal()
	for _, u := range us {
		t = t.Add(u)
	}
	return t
}

// String is the total as a producer card keeps it, one line of key=value words, a
// count or time not reported left out.
func (t Total) String() string {
	ws := []string{"records=" + strconv.Itoa(t.Records)}
	for _, kv := range []struct {
		k string
		n int64
	}{{"input", t.Tokens.Input}, {"cache_read", t.Tokens.CacheRead}, {"cache_write", t.Tokens.CacheWrite}, {"output", t.Tokens.Output},
		{"reasoning", t.Tokens.Reasoning}, {"requests", t.Tokens.Requests}, {"max_prompt", t.Tokens.MaxPrompt}, {"wait_s", t.Wait}, {"run_s", t.Run}} {
		if kv.n >= 0 {
			ws = append(ws, kv.k+"="+strconv.FormatInt(kv.n, 10))
		}
	}
	for _, kv := range []struct{ k, v string }{{"predicted_usd", t.Predicted}, {"actual_usd", t.Actual}, {"actual_by", t.ActualBy}, {"charged_usd", t.Charged}} {
		if kv.v != "" {
			ws = append(ws, kv.k+"="+kv.v)
		}
	}
	return strings.Join(append(ws, "predicted_of="+strconv.Itoa(t.PredOf), "actual_of="+strconv.Itoa(t.ActualOf), "charged_of="+strconv.Itoa(t.ChargedOf)), " ")
}

// ParseTotal reads a total's line (Total.String); "" is the total of no record.
func ParseTotal(line string) Total {
	t := NoTotal()
	counts := map[string]*int64{"input": &t.Tokens.Input, "cache_read": &t.Tokens.CacheRead, "cache_write": &t.Tokens.CacheWrite, "output": &t.Tokens.Output,
		"reasoning": &t.Tokens.Reasoning, "requests": &t.Tokens.Requests, "max_prompt": &t.Tokens.MaxPrompt, "wait_s": &t.Wait, "run_s": &t.Run}
	ints := map[string]*int{"records": &t.Records, "predicted_of": &t.PredOf, "actual_of": &t.ActualOf, "charged_of": &t.ChargedOf}
	texts := map[string]*string{"predicted_usd": &t.Predicted, "actual_usd": &t.Actual, "actual_by": &t.ActualBy, "charged_usd": &t.Charged}
	for _, w := range strings.Fields(line) {
		k, v, _ := strings.Cut(w, "=")
		switch {
		case counts[k] != nil:
			if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
				*counts[k] = n
			}
		case ints[k] != nil:
			if n, err := strconv.Atoi(v); err == nil && n >= 0 {
				*ints[k] = n
			}
		case texts[k] != nil:
			*texts[k] = v
		}
	}
	return t
}
