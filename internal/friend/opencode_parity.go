package friend

import (
	"fmt"
	"math/big"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
)

// What the two runner.zsh stopgaps (two friends' copies of one script, 2026-10-04/05) did around
// `opencode run`, as small functions the one-shot lanes call, each configured on the
// friend row and not in code (docs/SPEC-FRIEND.md, opencode lane parity): the card
// filter and the take back, the job name, the width under load, the token cap, the
// provider pause, the cost line, the go shims, and the bus note at a finish.

// LaneRules is the friend row's settings for her opencode lanes, as her beat answers
// them (ParseLaneRules); the zero value runs every card, uncapped, at the row's width.
type LaneRules struct {
	Tiers     []string // the tiers she works (flash); none is every tier
	Patterns  []string // stream or id globs she works (security*); none is every card
	TokenCap  int64    // tokens one card may spend, all kinds; 0 is no cap
	LoadBound int      // the 1-minute load above which new lanes are held to LoadWidth; 0 is never
	LoadWidth int      // the width held to, at least 1
}

// ParseLaneRules reads the rules off her beat's answer: row_tiers=flash,
// row_streams=security*,fp-sec* (stream or id globs), row_token_cap=<n>,
// row_load_bound=<n>, row_load_width=<n>.
func ParseLaneRules(answer string) LaneRules {
	var r LaneRules
	list := func(v string) []string {
		var out []string
		for _, s := range strings.Split(v, ",") {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	num := func(v string) int64 {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			return 0
		}
		return n
	}
	for _, w := range strings.Fields(answer) {
		k, v, ok := strings.Cut(w, "=")
		if !ok {
			continue
		}
		switch k {
		case "row_tiers":
			r.Tiers = list(strings.ToLower(v))
		case "row_streams":
			r.Patterns = list(v)
		case "row_token_cap":
			r.TokenCap = num(v)
		case "row_load_bound":
			r.LoadBound = int(num(v))
		case "row_load_width":
			r.LoadWidth = int(num(v))
		}
	}
	return r
}

// Decide is whether a dealt card passes her filter: ok, or the reason it does not,
// which is the reason a take back gives. A card passes when its tier is one she works
// (when she names any) and its stream or its id matches a pattern (when she names any).
func (r LaneRules) Decide(id, stream, tier string) (ok bool, why string) {
	if len(r.Tiers) > 0 && !contains(r.Tiers, strings.ToLower(tier)) {
		return false, fmt.Sprintf("tier %s: her row works %s only", dash(tier), strings.Join(r.Tiers, ", "))
	}
	if len(r.Patterns) > 0 {
		for _, p := range r.Patterns {
			if m, _ := path.Match(p, stream); m {
				return true, ""
			}
			if m, _ := path.Match(p, id); m {
				return true, ""
			}
		}
		return false, fmt.Sprintf("stream %s, id %s: her row works %s only", dash(stream), id, strings.Join(r.Patterns, ", "))
	}
	return true, ""
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// TakeBack is whether a dealt card outside her filter goes back to the dealer: it is
// outside the filter, no lane has started it, and no jobs/<job> exists (made outside
// the lanes), so nothing of hers is lost; and it was not taken already.
func TakeBack(passes, started, jobDirExists, tookAlready bool) bool {
	return !passes && !started && !jobDirExists && !tookAlready
}

// TakeArgv is the sprint server's take back: `friend take <friend> <card> --reason <why>`.
func TakeArgv(friend, card, why string) []string {
	return []string{"friend", "take", friend, card, "--reason", why}
}

// JobName is the directory friend sync names a card's inbox and outbox: <card>~<epoch>,
// with .g<gen> after it from the second generation; the bare card when it has no epoch.
func JobName(card, epoch string, gen int) string {
	job := card
	if epoch != "" {
		job += "~" + epoch
	}
	if gen > 1 {
		job += ".g" + strconv.Itoa(gen)
	}
	return job
}

// LaneWidth is how many lanes may run at once: the row's width, held to the row's
// load width while the machine's 1-minute load is above its bound.
func LaneWidth(width, load int, r LaneRules) int {
	if r.LoadBound > 0 && load > r.LoadBound {
		return max(1, min(width, max(1, r.LoadWidth)))
	}
	return width
}

// Tokens is a card's tokens as opencode's database holds them: its session and its
// children, summed.
type Tokens struct {
	Input, CacheRead, CacheWrite, Output, Reasoning int64
	Harness                                         float64 // opencode's own cost, USD
	Sessions                                        int64
}

// Total is every kind of token.
func (t Tokens) Total() int64 { return t.Input + t.CacheRead + t.CacheWrite + t.Output + t.Reasoning }

// TokensSQL is the query over opencode's database (run through `sqlite3 -readonly`) for the
// run titled title; the title is quoted, so none can end the string.
func TokensSQL(title string) string {
	q := strings.ReplaceAll(title, "'", "''")
	return "with s as (select id from session where title='" + q + "') select coalesce(sum(tokens_input),0), coalesce(sum(tokens_cache_read),0), coalesce(sum(tokens_cache_write),0), coalesce(sum(tokens_output),0), coalesce(sum(tokens_reasoning),0), coalesce(sum(cost),0), count(*) from session where id in (select id from s) or parent_id in (select id from s);"
}

// ParseTokens reads the one row TokensSQL prints (sqlite3's pipe-separated).
func ParseTokens(row string) (Tokens, error) {
	f := strings.Split(strings.TrimSpace(row), "|")
	if len(f) != 7 {
		return Tokens{}, fmt.Errorf("opencode's database answered %d columns, want 7: %q", len(f), oneLine(row, 100))
	}
	var n [7]int64
	var h float64
	for i, s := range f {
		if i == 5 {
			v, err := strconv.ParseFloat(s, 64)
			if err != nil {
				return Tokens{}, fmt.Errorf("opencode's cost column is no number: %q", s)
			}
			h = v
			continue
		}
		v, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return Tokens{}, fmt.Errorf("opencode's database column %d is no integer: %q", i+1, s)
		}
		n[i] = v
	}
	return Tokens{n[0], n[1], n[2], n[3], n[4], h, n[6]}, nil
}

// OverCap is whether a card has spent its cap; no cap is never over.
func OverCap(t Tokens, cap int64) bool { return cap > 0 && t.Total() >= cap }

// CapHoldReport is the REPORT.md of a card whose lane was stopped at the cap: HOLD, no
// head, naming the cap, the tokens and turns spent and the last step.
func CapHoldReport(friend string, cap int64, t Tokens, turns int, last string) string {
	if last == "" {
		last = "unknown"
	}
	return fmt.Sprintf("Verdict: HOLD\nHead: none\n\ntoken cap: %d tokens, %d turns, last step: %s. %s's one-shot lane was stopped at the row's per-card cap of %d tokens; the card goes to a bud, and whatever was pushed on its branch is a draft only.\n",
		t.Total(), turns, oneLine(last, 300), friend, cap)
}

var providerFailure = regexp.MustCompile(`(?i)((^|[^0-9])(402|429)([^0-9]|$)|insufficient[ _-]?(funds|credit|balance|quota)|payment required|out of (funds|credits)|credit balance|exceeded your current quota|rate[ _-]?limit|too many requests)`)

// ProviderFailure is the first `Error:` line of a lane's output that says the provider
// failed (402, 429, out of funds, a rate limit), one line, at most 300 bytes; empty when
// none does. Its answer is a pause of every lane (ProviderPause).
func ProviderFailure(out string) string {
	for _, line := range strings.Split(out, "\n") {
		line = stripANSI(line)
		if strings.HasPrefix(line, "Error: ") && providerFailure.MatchString(line) {
			return oneLine(line, 300)
		}
	}
	return ""
}

// ProviderPause is what a provider failure does: every lane stops, the friend is held
// down with the exact message, and nothing resumes until a person brings her up.
type ProviderPause struct {
	Message string
	Job     string
	At      time.Time
}

// Line is the one line the hold file and the record carry.
func (p ProviderPause) Line() string {
	return p.At.UTC().Format(time.RFC3339) + " " + p.Job + ": " + p.Message
}

// DownArgv is the verb that holds her down: `friend down <friend> --reason <text>`, the
// message cut to 300 bytes.
func (p ProviderPause) DownArgv(friend, model string) []string {
	return []string{"friend", "down", friend, "--reason", "provider failure (" + model + "): " + oneLine(p.Message, 300)}
}

// PauseFile is the hold file in a lane state directory; its presence holds every lane.
const PauseFile = "PAUSED"

// Pause writes the hold file once and answers whether it did: a second failure while
// held changes nothing.
func (p ProviderPause) Pause(stateDir string) (bool, error) {
	file := filepath.Join(stateDir, PauseFile)
	if exists(file) {
		return false, nil
	}
	return true, atomicfile.WriteFile(file, []byte(p.Line()+"\n"), 0o644)
}

// Paused is whether a person has not yet brought her up: the hold file is there.
func Paused(stateDir string) bool { return exists(filepath.Join(stateDir, PauseFile)) }

// Price is a route row's prices, USD per million tokens; a nil price is none.
type Price struct {
	Route                                string
	Input, CacheRead, CacheWrite, Output *big.Rat
	ReasoningAsOutput                    bool
	Extra                                bool // long-context, per-request or gateway prices this does not apply
}

// CostOf is a card's cost at the route's price, USD rounded up to the cent, as "$1.23";
// or "unpriced (<why>)" when there is no row or the row cannot price a kind of token
// the card spent. A guess is never made.
func CostOf(t Tokens, p *Price, model string) string {
	if p == nil {
		return "unpriced (no route row for " + model + " in nova-sprint routes)"
	}
	if p.Extra {
		return "unpriced (route " + p.Route + " has long-context, request or gateway prices this does not apply)"
	}
	out := t.Output
	if p.ReasoningAsOutput {
		out += t.Reasoning
	}
	for _, k := range []struct {
		n     int64
		price *big.Rat
		name  string
	}{{t.Input, p.Input, "input"}, {t.CacheRead, p.CacheRead, "cache_read"}, {t.CacheWrite, p.CacheWrite, "cache_write"}, {out, p.Output, "output"}} {
		if k.n > 0 && k.price == nil {
			return "unpriced (route " + p.Route + " has no " + k.name + " price)"
		}
	}
	sum := new(big.Rat)
	for _, k := range []struct {
		n     int64
		price *big.Rat
	}{{t.Input, p.Input}, {t.CacheRead, p.CacheRead}, {t.CacheWrite, p.CacheWrite}, {out, p.Output}} {
		if k.price != nil {
			sum.Add(sum, new(big.Rat).Mul(k.price, big.NewRat(k.n, 1)))
		}
	}
	return Dollars(sum.Quo(sum, big.NewRat(1_000_000, 1)))
}

// Dollars is usd rounded up to the cent.
func Dollars(usd *big.Rat) string {
	cents := new(big.Rat).Mul(usd, big.NewRat(100, 1))
	c := new(big.Int).Div(cents.Num(), cents.Denom())
	if new(big.Rat).SetInt(c).Cmp(cents) < 0 {
		c.Add(c, big.NewInt(1))
	}
	q, r := new(big.Int).DivMod(c, big.NewInt(100), new(big.Int))
	return fmt.Sprintf("$%s.%02d", q, r.Int64())
}

// CostLine is the line a REPORT.md carries under Head: and friend sync records with the
// finish, the harness's own cost beside the route's.
func CostLine(t Tokens, cost, model, route string) string {
	if route == "" {
		route = "-"
	}
	return fmt.Sprintf("Cost: %s (opencode: %s) tokens input=%d cache_read=%d cache_write=%d output=%d reasoning=%d model=%s harness=opencode price_route=%s",
		cost, Dollars(new(big.Rat).SetFloat64(t.Harness)), t.Input, t.CacheRead, t.CacheWrite, t.Output, t.Reasoning, model, route)
}

// WithCost is report with line added under its Head: line (at the end when it has none);
// a report that already carries a Cost: line is returned as it was.
func WithCost(report, line string) string {
	lines := strings.Split(strings.TrimRight(report, "\n"), "\n")
	for _, l := range lines {
		if strings.HasPrefix(l, "Cost: ") {
			return report
		}
	}
	out := make([]string, 0, len(lines)+1)
	done := false
	headLine := regexp.MustCompile(`^[#*_ -]*[Hh]ead:`)
	for _, l := range lines {
		out = append(out, l)
		if !done && headLine.MatchString(l) {
			out, done = append(out, line), true
		}
	}
	if !done {
		out = append(out, line)
	}
	return strings.Join(out, "\n") + "\n"
}

// ResultLines is what RESULT.md gains: the tokens and the cost.
func ResultLines(t Tokens, cost, model string) string {
	return fmt.Sprintf("tokens: input=%d cache_read=%d cache_write=%d output=%d reasoning=%d model=%s harness=opencode\ncost: %s (opencode: %s)\n",
		t.Input, t.CacheRead, t.CacheWrite, t.Output, t.Reasoning, model, cost, Dollars(new(big.Rat).SetFloat64(t.Harness)))
}

// ShimDir is the directory under a lane state directory that holds the refusal shims.
const ShimDir = "bin"

// GoRefusal is why no go command runs on this machine.
const GoRefusal = "go is refused on this machine (this machine runs no go build, test or vet): sync the clone to the bench and run it there over ssh"

// WriteShims writes the refusal shims `go` and `gofmt` into stateDir/bin: each prints
// GoRefusal and exits 126. They are put first on a lane's PATH (LaneEnv); they are the
// lane's wall against the one command the machine forbids, not a product.
func WriteShims(stateDir string) (dir string, err error) {
	dir = filepath.Join(stateDir, ShimDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	for _, name := range []string{"go", "gofmt"} {
		body := "#!/bin/sh\necho '" + name + ": " + GoRefusal + "' >&2\nexit 126\n"
		if err := atomicfile.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// LaneEnv is env with shimDir first on PATH and GOROOT pointing nowhere, so a lane's go
// command is the shim's.
func LaneEnv(env []string, shimDir string) []string {
	out := make([]string, 0, len(env)+2)
	pathSet := false
	for _, kv := range env {
		switch {
		case strings.HasPrefix(kv, "GOROOT="):
			continue
		case strings.HasPrefix(kv, "PATH="):
			kv, pathSet = "PATH="+shimDir+string(os.PathListSeparator)+strings.TrimPrefix(kv, "PATH="), true
		}
		out = append(out, kv)
	}
	if !pathSet {
		out = append(out, "PATH="+shimDir)
	}
	return append(out, "GOROOT=/GO-NEVER-RUNS-HERE")
}

// FinishNote is the bus note to the coordinator at a card's finish: its subject and body.
func FinishNote(friend, job, verdict, cost string, wall time.Duration) (subject, body string) {
	if verdict == "" {
		verdict = "no report"
	}
	return fmt.Sprintf("%s card %s: %s", friend, job, verdict),
		fmt.Sprintf("%s one-shot lane finished %s: %s; cost %s; wall %s", friend, job, verdict, cost, wall.Round(time.Second))
}
