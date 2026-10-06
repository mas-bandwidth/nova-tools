package friend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
)

// What the two runner.zsh stopgaps (one per friend) did for an opencode friend's
// cards, as small functions the one-shot lanes call, configured on the friend row
// (LaneRules) and never in code (docs/SPEC-FRIEND.md, lane parity). The owner,
// 2026-10-05: "We need to get away from these one shot shell scripts."

// LaneRules is the lane policy on a friend's row: which cards she works, how wide under
// load, the per-card token cap, and the provider and model her cards are priced by.
// The zero value works every card at the row's width with no cap.
type LaneRules struct {
	Tiers    []string `json:"tiers,omitempty"`   // tiers she works ("flash"); empty: every tier
	Streams  []string `json:"streams,omitempty"` // stream globs ("security*"); with IDs, a card must match one when either is set
	IDs      []string `json:"ids,omitempty"`     // card id globs ("fp-sec*", "sec-*")
	LoadMax  float64  `json:"load_max,omitempty"`
	LoadTo   int      `json:"load_width,omitempty"` // the width held to while the load is above LoadMax (3 when LoadMax is set and this is not)
	TokenCap int64    `json:"token_cap,omitempty"`  // per card, all token kinds; 0 is no cap
	Provider string   `json:"provider,omitempty"`
	Model    string   `json:"model,omitempty"`
}

// ParseRules reads the rules off the row's JSON (the beat's row_lane_rules= value, or the
// row column); empty is the zero rules.
func ParseRules(raw string) (LaneRules, error) {
	var r LaneRules
	if strings.TrimSpace(raw) == "" {
		return r, nil
	}
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		return LaneRules{}, fmt.Errorf("the row's lane rules are not JSON: %v", err)
	}
	return r, nil
}

// CardFacts is what the filter reads of a dealt card.
type CardFacts struct {
	ID, Stream, Tier string
}

// FilterAction is what a lane does with a dealt card.
type FilterAction string

const (
	FilterRun  FilterAction = "run"  // the friend works it
	FilterSkip FilterAction = "skip" // not hers by stream or id: left alone
	FilterTake FilterAction = "take" // not hers by tier: taken back for the dealer, if not started
)

func globAny(patterns []string, s string) bool {
	for _, p := range patterns {
		if ok, _ := path.Match(p, s); ok {
			return true
		}
	}
	return false
}

// Filter is the friend's card filter: a tier outside Tiers is taken back, a card that
// matches neither a stream nor an id pattern (when either list is set) is skipped, else run.
func (r LaneRules) Filter(c CardFacts) (FilterAction, string) {
	if len(r.Tiers) > 0 && !globAny(r.Tiers, c.Tier) {
		return FilterTake, fmt.Sprintf("tier %s: this friend works %s cards only; the rest go back to the dealer", c.Tier, strings.Join(r.Tiers, ","))
	}
	if len(r.Streams)+len(r.IDs) > 0 && !globAny(r.Streams, c.Stream) && !globAny(r.IDs, c.ID) {
		return FilterSkip, fmt.Sprintf("not a card of hers (stream %s)", c.Stream)
	}
	return FilterRun, ""
}

// TakeArgv is the sprint verb that takes a dealt, unstarted card back for the dealer:
// `friend take <friend> <card> --reason <why>`.
func TakeArgv(friend, card, why string) []string {
	return []string{"friend", "take", friend, card, "--reason", why}
}

// TakeBack answers whether a card is taken back: the filter says take and no job
// directory of it exists yet (a started card is hers to finish).
func TakeBack(action FilterAction, jobDirExists bool) bool {
	return action == FilterTake && !jobDirExists
}

// JobName is a card's job directory as friend sync names it: <card>~<epoch>, with
// .g<gen> after it from the second generation; the bare card id when no epoch is known
// (epoch < 0).
func JobName(card string, epoch, gen int) string {
	job := card
	if epoch >= 0 {
		job = card + "~" + strconv.Itoa(epoch)
	}
	if gen > 1 {
		job += ".g" + strconv.Itoa(gen)
	}
	return job
}

// LoadWidth is the width a lane set may run at under the machine's 1-minute load: the
// row's width, held to the rules' LoadTo (3 by default) while load is above LoadMax.
// A width already at or below it is left.
func (r LaneRules) LoadWidth(width int, load float64) int {
	if r.LoadMax <= 0 || load <= r.LoadMax {
		return width
	}
	to := r.LoadTo
	if to <= 0 {
		to = 3
	}
	return min(width, to)
}

// Tokens is one card's use as opencode's database holds it: its session and its children.
type Tokens struct {
	Input, CacheRead, CacheWrite, Output, Reasoning int64
	HarnessUSD                                      float64 // opencode's own reported cost, kept beside ours
	Sessions                                        int
	Turns                                           int
}

// Total is every kind of token.
func (t Tokens) Total() int64 { return t.Input + t.CacheRead + t.CacheWrite + t.Output + t.Reasoning }

// OverCap says the card has used its cap.
func (r LaneRules) OverCap(t Tokens) bool { return r.TokenCap > 0 && t.Total() >= r.TokenCap }

// CapHoldReport is the REPORT.md of a card stopped at the token cap: line 1 the HOLD
// verdict, line 2 no head, then the paragraph that names the cap.
func CapHoldReport(friend string, r LaneRules, t Tokens, last string) string {
	if last == "" {
		last = "unknown"
	}
	return fmt.Sprintf("Verdict: HOLD\nHead: none\n\ntoken cap: %d tokens, %d turns, last step: %s. %s's one-shot lane was stopped at the per-card cap of %d tokens; the card goes to a bud, and whatever was pushed on its branch is a draft only.\n",
		t.Total(), t.Turns, oneLine(last, 300), friend, r.TokenCap)
}

// TokensSQL is the query that reads a run's tokens from opencode's database: the session
// titled title and its children, as `in|cache_read|cache_write|out|reasoning|cost|sessions`.
func TokensSQL(title string) string {
	q := strings.ReplaceAll(title, "'", "''")
	return "with s as (select id from session where title='" + q + "') select coalesce(sum(tokens_input),0), coalesce(sum(tokens_cache_read),0), coalesce(sum(tokens_cache_write),0), coalesce(sum(tokens_output),0), coalesce(sum(tokens_reasoning),0), coalesce(sum(cost),0), count(*) from session where id in (select id from s) or parent_id in (select id from s);"
}

// ParseTokens reads the line TokensSQL's query prints.
func ParseTokens(line string) (Tokens, error) {
	f := strings.Split(strings.TrimSpace(line), "|")
	if len(f) != 7 {
		return Tokens{}, fmt.Errorf("opencode's database answered %q, not seven fields", oneLine(line, 100))
	}
	var n [5]int64
	for i := range n {
		v, err := strconv.ParseInt(f[i], 10, 64)
		if err != nil {
			return Tokens{}, fmt.Errorf("opencode's database: token field %d is %q", i+1, f[i])
		}
		n[i] = v
	}
	usd, err := strconv.ParseFloat(f[5], 64)
	if err != nil {
		return Tokens{}, fmt.Errorf("opencode's database: cost field is %q", f[5])
	}
	sessions, err := strconv.Atoi(f[6])
	if err != nil {
		return Tokens{}, fmt.Errorf("opencode's database: session count is %q", f[6])
	}
	return Tokens{n[0], n[1], n[2], n[3], n[4], usd, sessions, 0}, nil
}

// ReadTokens runs the sqlite3 CLI read-only over db (the tree has no sqlite driver) and
// answers the tokens of the run titled title.
func ReadTokens(ctx context.Context, run Exec, db, title string) (Tokens, error) {
	out, exit, err := run(ctx, filepath.Dir(db), "sqlite3", []string{"-readonly", db, TokensSQL(title)}, "")
	if err != nil {
		return Tokens{}, err
	}
	if exit != 0 {
		return Tokens{}, fmt.Errorf("sqlite3 over %s exited %d", db, exit)
	}
	return ParseTokens(out)
}

// RoutePrices is a route row's prices, USD per million tokens, as decimal strings so the
// sum is exact; "" is no price.
type RoutePrices struct {
	Name, Provider, Model string
	Input, CacheRead      string
	CacheWrite, Output    string
	ReasoningAsOutput     bool
	Extra                 bool // long-context, per-request or gateway prices this pricing does not apply
}

// Priced is a card's cost: whole cents rounded up, or why it is unpriced.
type Priced struct {
	Cents    int64
	Unpriced string
	Route    string
}

// Dollars is the cost as $d.cc, or the unpriced reason.
func (p Priced) Dollars() string {
	if p.Unpriced != "" {
		return "unpriced (" + p.Unpriced + ")"
	}
	return fmt.Sprintf("$%d.%02d", p.Cents/100, p.Cents%100)
}

// Price prices t by route (nil: no row), rounded up to the cent; never a guess.
func Price(route *RoutePrices, model string, t Tokens) Priced {
	if route == nil {
		return Priced{Unpriced: "no route row for " + model + " in nova-sprint routes"}
	}
	why := func(what string) Priced {
		return Priced{Unpriced: "route " + route.Name + " " + what, Route: route.Name}
	}
	if route.Extra {
		return why("has long-context, request or gateway prices this pricing does not apply")
	}
	out := t.Output
	if route.ReasoningAsOutput {
		out += t.Reasoning
	}
	sum := new(big.Rat)
	for _, k := range []struct {
		name, price string
		n           int64
	}{{"input", route.Input, t.Input}, {"cache_read", route.CacheRead, t.CacheRead}, {"cache_write", route.CacheWrite, t.CacheWrite}, {"output", route.Output, out}} {
		if k.n == 0 {
			continue
		}
		if k.price == "" {
			return why("has no " + k.name + " price")
		}
		p, ok := new(big.Rat).SetString(k.price)
		if !ok {
			return why("has an unreadable " + k.name + " price " + strconv.Quote(k.price))
		}
		sum.Add(sum, p.Mul(p, big.NewRat(k.n, 1)))
	}
	// sum is USD*1e6; cents = ceil(sum*100/1e6)
	cents := sum.Mul(sum, big.NewRat(100, 1000000))
	q, m := new(big.Int).DivMod(cents.Num(), cents.Denom(), new(big.Int))
	if m.Sign() > 0 {
		q.Add(q, big.NewInt(1))
	}
	return Priced{Cents: q.Int64(), Route: route.Name}
}

// ParseRoutes reads `nova-sprint routes --json` and answers the row for provider/model
// (nil when none): any object that carries prices and names the pair, wherever it sits.
func ParseRoutes(raw []byte, provider, model string) (*RoutePrices, error) {
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("nova-sprint routes --json is not JSON: %v", err)
	}
	var found *RoutePrices
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if pr, ok := x["prices"].(map[string]any); ok && found == nil && x["provider"] == provider && x["model"] == model {
				str := func(k string) string {
					switch v := pr[k].(type) {
					case string:
						return v
					case float64:
						return strconv.FormatFloat(v, 'f', -1, 64)
					}
					return ""
				}
				name, _ := x["name"].(string)
				rao := true
				if b, ok := pr["reasoning_as_output"].(bool); ok {
					rao = b
				}
				lc, _ := pr["long_context"].(float64)
				found = &RoutePrices{Name: name, Provider: provider, Model: model,
					Input: str("input"), CacheRead: str("cache_read"), CacheWrite: str("cache_write"), Output: str("output"),
					ReasoningAsOutput: rao, Extra: lc > 0 || str("request") != "" || str("gateway_percent") != ""}
				return
			}
			for _, c := range x {
				walk(c)
			}
		case []any:
			for _, c := range x {
				walk(c)
			}
		}
	}
	walk(doc)
	return found, nil
}

// CostLine is the Cost: line of REPORT.md: the price, opencode's own figure beside it,
// the tokens, the model and the route.
func CostLine(p Priced, t Tokens, model string) string {
	route := p.Route
	if route == "" {
		route = "-"
	}
	return fmt.Sprintf("Cost: %s (opencode: $%.2f) tokens input=%d cache_read=%d cache_write=%d output=%d reasoning=%d model=%s harness=opencode price_route=%s",
		p.Dollars(), t.HarnessUSD, t.Input, t.CacheRead, t.CacheWrite, t.Output, t.Reasoning, model, route)
}

// WithCost is a report with the Cost: line under its Head: line (or at its end when it has
// none); a report that already has a Cost: line is returned as it is.
func WithCost(report, cost string) string {
	lines := strings.Split(strings.TrimRight(report, "\n"), "\n")
	for _, l := range lines {
		if strings.HasPrefix(l, "Cost: ") {
			return report
		}
	}
	var out []string
	done := false
	for _, l := range lines {
		out = append(out, l)
		if !done && strings.HasPrefix(strings.ToLower(strings.TrimLeft(l, "#*_ -")), "head:") {
			out, done = append(out, cost), true
		}
	}
	if !done {
		out = append(out, cost)
	}
	return strings.Join(out, "\n") + "\n"
}

// ResultLines are the tokens: and cost: lines added to RESULT.md.
func ResultLines(p Priced, t Tokens, model string) string {
	return fmt.Sprintf("tokens: input=%d cache_read=%d cache_write=%d output=%d reasoning=%d model=%s harness=opencode\ncost: %s (opencode: $%.2f)\n",
		t.Input, t.CacheRead, t.CacheWrite, t.Output, t.Reasoning, model, p.Dollars(), t.HarnessUSD)
}

// PublishCost writes the cost onto a finished card: the friend's REPORT.draft.md (or a
// REPORT.md without a Cost: line) is published as REPORT.md with the Cost: line, the
// draft removed, and the two lines are appended to RESULT.md when it is there. Nothing is
// written for a card with no report.
func PublishCost(outbox string, p Priced, t Tokens, model string) error {
	src := filepath.Join(outbox, "REPORT.draft.md")
	raw, err := os.ReadFile(src)
	if err != nil {
		src = filepath.Join(outbox, "REPORT.md")
		if raw, err = os.ReadFile(src); err != nil {
			return nil
		}
	}
	if err := atomicfile.WriteFile(filepath.Join(outbox, "REPORT.md"), []byte(WithCost(string(raw), CostLine(p, t, model))), 0o644); err != nil {
		return err
	}
	if filepath.Base(src) == "REPORT.draft.md" {
		_ = os.Remove(src) // ignored: the published REPORT.md stands; a draft left behind is published again unchanged
	}
	rp := filepath.Join(outbox, "RESULT.md")
	res, err := os.ReadFile(rp)
	if err != nil || strings.Contains(string(res), "\ncost: ") {
		return nil
	}
	return atomicfile.WriteFile(rp, append(res, ResultLines(p, t, model)...), 0o644)
}

// ProviderFailure is the exact provider failure a lane's output says (402, 429, out of
// funds, a rate limit), "" when it says none: the stopgap's pause, for the friend who is
// held down for a person to bring up rather than backed off.
func ProviderFailure(id, out string) string {
	var rate RateLimited
	var funds OutOfFunds
	err := ProviderLimit(id, out)
	switch {
	case errors.As(err, &funds):
		return funds.Reason
	case errors.As(err, &rate):
		return rate.Reason
	}
	return ""
}

// PauseFile is the record of a provider pause in the state directory: while it is there
// no lane starts, whatever the daemon's restarts; a person removes it (and runs friend up).
const PauseFile = "PAUSED"

// WritePause records the exact message of the failure; the first one stays.
func WritePause(stateDir, job, message, at string) error {
	p := filepath.Join(stateDir, PauseFile)
	if _, err := os.Stat(p); err == nil {
		return nil
	}
	return atomicfile.WriteFile(p, []byte(fmt.Sprintf("%s %s: %s\n", at, job, message)), 0o644)
}

// ReadPause is the pause on record, "" when there is none.
func ReadPause(stateDir string) string {
	raw, err := os.ReadFile(filepath.Join(stateDir, PauseFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// DownArgv is the sprint verb that holds the friend down with the failure's exact words.
func DownArgv(friend, model, message string) []string {
	return []string{"friend", "down", friend, "--reason", fmt.Sprintf("provider failure (%s): %s", model, oneLine(message, 300))}
}

// ShimTools are the commands a lane's PATH refuses on a machine where go never runs.
var ShimTools = []string{"go", "gofmt"}

// ShimBody is a refusing shim's text: it says where to run the command and exits 126.
func ShimBody(tool, bench string) string {
	return fmt.Sprintf("#!/bin/sh\necho '%s is refused on this machine: run it on %s over ssh (rsync the clone there first)' >&2\nexit 126\n", tool, bench)
}

// WriteShims writes the refusing shims into dir (to be put first on the lane's PATH) and
// answers dir; GOROOT for the lane is ShimGoroot.
func WriteShims(dir, bench string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	for _, tool := range ShimTools {
		if err := atomicfile.WriteFile(filepath.Join(dir, tool), []byte(ShimBody(tool, bench)), 0o755); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// ShimGoroot points the lane's GOROOT nowhere, so a go found by another path fails too.
const ShimGoroot = "/GO-NEVER-RUNS-HERE-run-it-on-the-bench-over-ssh"

// ShimEnv is env with the shims first on PATH and GOROOT pointed nowhere.
func ShimEnv(env []string, shimDir string) []string {
	var out []string
	pathSet := false
	for _, e := range env {
		switch {
		case strings.HasPrefix(e, "GOROOT="):
			continue
		case strings.HasPrefix(e, "PATH="):
			e, pathSet = "PATH="+shimDir+string(os.PathListSeparator)+strings.TrimPrefix(e, "PATH="), true
		}
		out = append(out, e)
	}
	if !pathSet {
		out = append(out, "PATH="+shimDir)
	}
	return append(out, "GOROOT="+ShimGoroot)
}

// FinishNote is the bus note to the coordinator at a card's finish: subject and body.
func FinishNote(friend, job, report, cost string, wallSeconds int) (subject, body string) {
	return fmt.Sprintf("%s card %s: %s", friend, job, report),
		fmt.Sprintf("%s one-shot lane finished %s: %s; cost %s; wall %ds", friend, job, report, cost, wallSeconds)
}

// ParseLaneRules reads the row's lane rules off her beat's answer
// (row_lane_rules=<compact JSON, no spaces>, beside row_mode and row_width); none is the
// zero rules.
func ParseLaneRules(answer string) LaneRules {
	for _, w := range strings.Fields(answer) {
		if v, found := strings.CutPrefix(w, "row_lane_rules="); found {
			r, err := ParseRules(v)
			if err != nil {
				return LaneRules{}
			}
			return r
		}
	}
	return LaneRules{}
}

// ParseLoad1 reads the 1-minute load off `sysctl -n vm.loadavg` ("{ 1.5 2 3 }") or
// /proc/loadavg ("1.5 2 3 1/2 3"); ok is false when it reads as neither.
func ParseLoad1(s string) (load float64, ok bool) {
	f := strings.Fields(strings.Trim(strings.TrimSpace(s), "{}"))
	if len(f) == 0 {
		return 0, false
	}
	n, err := strconv.ParseFloat(f[0], 64)
	return n, err == nil
}
