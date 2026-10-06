package friend

import (
	"context"
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

// What the two runner.zsh stopgaps (two friends, 2026-10-04/05) did that the
// opencode one-shot lanes did not, each a small function configured on the friend's row
// and not in code (docs/SPEC-FRIEND.md, one-shot lanes, parity with the runner): the card
// filter and the take back, the job's name, the width under load, the per-card token cap,
// the provider failure, the card's cost, the refusal shims and the note at each finish.
// The row's settings ride the beat's answer beside row_mode and row_width.

// LaneRow is the lane settings of the friend's row, read off her beat's answer
// (ParseLaneRow). Its zero value is the runner with nothing configured: every card runs,
// no cap, no load bound, no price.
type LaneRow struct {
	Set       bool     // the row names any lane setting: the parity is on (a row naming none leaves the lanes as they were)
	Tiers          []string // row_tiers=flash,pro: the tiers she works; a card of another tier not started is taken back
	Streams        []string // row_streams=security*,sec-*: stream or id globs she works; a card matching none is skipped
	StopOnProvider bool     // row_provider_stop=true: any provider failure (402, 429, rate limit) holds every lane and the friend down, as out of funds does
	TokenCap       int64    // row_token_cap=<n>: tokens one card may use, all kinds; 0 none
	LoadMax        float64  // row_load_max=<n>: the machine's 1-minute load above which the width is held to LoadWidth; 0 none
	LoadWidth      int      // row_load_width=<n>: the held width; 0 is 3 (the runner's)
	Model          string   // row_model=provider/model: the model the cost line names
	Route          RouteRow // row_route_*: the price of her model, per million tokens; empty is unpriced
}

// RouteRow is the store's route row for the friend's provider/model, prices in USD per
// million tokens as decimal strings ("" is no price for that kind).
type RouteRow struct {
	Name, Input, CacheRead, CacheWrite, Output string
	ReasoningAsOutput                          bool
	Extra                                      bool // long-context, per-request or gateway prices this pricer does not apply
}

// DefaultLoadWidth is the width a loaded machine holds the lanes to (the runner's 3).
const DefaultLoadWidth = 3

// ParseLaneRow reads the lane settings off her beat's answer; keys it does not find stay
// zero, a value it cannot read is ignored.
func ParseLaneRow(answer string) LaneRow {
	var r LaneRow
	r.Route.ReasoningAsOutput = true
	list := func(v string) []string {
		var out []string
		for _, s := range strings.Split(v, ",") {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	for _, w := range strings.Fields(answer) {
		k, v, ok := strings.Cut(w, "=")
		if !ok || !strings.HasPrefix(k, "row_") {
			continue
		}
		switch k {
		case "row_tiers", "row_streams", "row_token_cap", "row_load_max", "row_load_width", "row_model", "row_route", "row_provider_stop":
			r.Set = true
		}
		switch k {
		case "row_tiers":
			r.Tiers = list(strings.ToLower(v))
		case "row_streams":
			r.Streams = list(v)
		case "row_provider_stop":
			r.StopOnProvider = v == "true"
		case "row_token_cap":
			r.TokenCap, _ = strconv.ParseInt(v, 10, 64)
		case "row_load_max":
			r.LoadMax, _ = strconv.ParseFloat(v, 64)
		case "row_load_width":
			r.LoadWidth, _ = strconv.Atoi(v)
		case "row_model":
			r.Model = v
		case "row_route":
			r.Route.Name = v
		case "row_price_input":
			r.Route.Input = v
		case "row_price_cache_read":
			r.Route.CacheRead = v
		case "row_price_cache_write":
			r.Route.CacheWrite = v
		case "row_price_output":
			r.Route.Output = v
		case "row_price_reasoning_as_output":
			r.Route.ReasoningAsOutput = v != "false"
		case "row_price_extra":
			r.Route.Extra = v == "true"
		}
	}
	return r
}

// FilterAction is what the filter says of a dealt card.
type FilterAction string

const (
	FilterRun  FilterAction = "run"  // the friend works it
	FilterSkip FilterAction = "skip" // not hers; left alone for whoever started it
	FilterTake FilterAction = "take" // not hers and not started: taken back for the dealer
)

// Filter is the row's card filter on a dealt card: a tier outside row_tiers is taken back
// (the friend works only those), an id or stream matching none of row_streams is skipped;
// the why is one line. A card with no tier is of the tier "-", which only an empty
// row_tiers lets run.
func (r LaneRow) Filter(id, stream, tier string) (FilterAction, string) {
	tier = strings.ToLower(strings.TrimSpace(tier))
	if tier == "" {
		tier = "-"
	}
	if len(r.Tiers) > 0 {
		ok := false
		for _, t := range r.Tiers {
			ok = ok || t == tier
		}
		if !ok {
			return FilterTake, fmt.Sprintf("tier %s: this friend works only %s cards; the rest go back to the dealer", tier, strings.Join(r.Tiers, ", "))
		}
	}
	if len(r.Streams) > 0 {
		for _, p := range r.Streams {
			for _, name := range []string{id, stream} {
				if m, err := path.Match(p, name); err == nil && m && name != "" {
					return FilterRun, ""
				}
			}
		}
		return FilterSkip, fmt.Sprintf("card %s (stream %s) matches none of %s", id, dash(stream), strings.Join(r.Streams, ", "))
	}
	return FilterRun, ""
}

// TakeArgv is the sprint server's verb that takes a card back for the dealer:
// `friend take <friend> <card> --reason <why>`.
func TakeArgv(friend, card, why string) []string {
	return []string{"friend", "take", friend, card, "--reason", oneLine(why, 300)}
}

// JobName is the inbox directory friend sync names a card's job: <card>~<epoch>, with
// .g<gen> after it from the card's second generation. An empty epoch is no "~" part.
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

// LaneWidth is the width the lanes run at: the row's width, held to the row's load width
// (3 when it names none) while the machine's 1-minute load is above row_load_max, never
// raised by it. held says it is held.
func (r LaneRow) LaneWidth(width int, load float64) (n int, held bool) {
	if r.LoadMax <= 0 || load <= r.LoadMax {
		return width, false
	}
	low := r.LoadWidth
	if low <= 0 {
		low = DefaultLoadWidth
	}
	if low >= width {
		return width, false
	}
	return low, true
}

// TokenUsage is a run's tokens, its session and its children's, as opencode's own database
// holds them, and the harness's own cost beside them.
type TokenUsage struct {
	Input, CacheRead, CacheWrite, Output, Reasoning int64
	Harness                                         float64 // opencode's own reported cost, USD
	Sessions                                        int64
}

// Total is every kind of token.
func (u TokenUsage) Total() int64 {
	return u.Input + u.CacheRead + u.CacheWrite + u.Output + u.Reasoning
}

// Sub is u less base: what a card used in a lane session that had already used base.
func (u TokenUsage) Sub(base TokenUsage) TokenUsage {
	return TokenUsage{u.Input - base.Input, u.CacheRead - base.CacheRead, u.CacheWrite - base.CacheWrite, u.Output - base.Output, u.Reasoning - base.Reasoning, u.Harness - base.Harness, u.Sessions}
}

// UsageSQL is the query that reads a session's usage and its children's from opencode's
// database: input, cache read, cache write, output, reasoning, harness cost, sessions.
func UsageSQL(session string) string {
	id := strings.ReplaceAll(session, "'", "''")
	return "select coalesce(sum(tokens_input),0), coalesce(sum(tokens_cache_read),0), coalesce(sum(tokens_cache_write),0), coalesce(sum(tokens_output),0), coalesce(sum(tokens_reasoning),0), coalesce(sum(cost),0), count(*) from session where id='" + id + "' or parent_id='" + id + "';"
}

// ParseUsage reads UsageSQL's one row as the sqlite3 command prints it (pipe separated).
func ParseUsage(out string) (TokenUsage, error) {
	f := strings.Split(strings.TrimSpace(out), "|")
	if len(f) != 7 {
		return TokenUsage{}, fmt.Errorf("opencode database: want 7 columns, got %q", oneLine(out, 100))
	}
	var n [7]int64
	for i := range 5 {
		v, err := strconv.ParseInt(f[i], 10, 64)
		if err != nil {
			return TokenUsage{}, fmt.Errorf("opencode database: column %d is no integer: %q", i+1, f[i])
		}
		n[i] = v
	}
	h, err := strconv.ParseFloat(f[5], 64)
	if err != nil {
		return TokenUsage{}, fmt.Errorf("opencode database: cost is no number: %q", f[5])
	}
	s, err := strconv.ParseInt(f[6], 10, 64)
	if err != nil {
		return TokenUsage{}, fmt.Errorf("opencode database: session count is no integer: %q", f[6])
	}
	return TokenUsage{n[0], n[1], n[2], n[3], n[4], h, s}, nil
}

// ReadUsage runs the sqlite3 command read-only on the opencode database at db for the
// session, through run (the Exec seam).
func ReadUsage(ctx context.Context, run Exec, db, session string) (TokenUsage, error) {
	out, exit, err := run(ctx, filepath.Dir(db), "sqlite3", []string{"-readonly", db, UsageSQL(session)}, "")
	if err != nil {
		return TokenUsage{}, fmt.Errorf("opencode database: %w", err)
	}
	if exit != 0 {
		return TokenUsage{}, fmt.Errorf("opencode database: sqlite3 exited %d", exit)
	}
	return ParseUsage(out)
}

// dollars is cents rounded up, as $d.cc.
func dollars(x *big.Rat) string {
	c := new(big.Rat).Mul(x, big.NewRat(100, 1))
	n := new(big.Int).Quo(c.Num(), c.Denom())
	if new(big.Rat).SetInt(n).Cmp(c) < 0 {
		n.Add(n, big.NewInt(1))
	}
	return fmt.Sprintf("$%d.%02d", new(big.Int).Quo(n, big.NewInt(100)), new(big.Int).Rem(n, big.NewInt(100)))
}

// Cost is the card's price at the route row, USD rounded up to the cent ($1.00), or
// "unpriced (<why>)": never a guess. Reasoning is priced as output when the row says so.
func (r RouteRow) Cost(model string, u TokenUsage) string {
	if r.Name == "" {
		return fmt.Sprintf("unpriced (no route row for %s in nova-sprint routes)", model)
	}
	if r.Extra {
		return fmt.Sprintf("unpriced (route %s has long-context, request or gateway prices this pricer does not apply)", r.Name)
	}
	out := u.Output
	if r.ReasoningAsOutput {
		out += u.Reasoning
	}
	sum := new(big.Rat)
	for _, k := range []struct {
		kind, price string
		n           int64
	}{{"input", r.Input, u.Input}, {"cache_read", r.CacheRead, u.CacheRead}, {"cache_write", r.CacheWrite, u.CacheWrite}, {"output", r.Output, out}} {
		if k.n == 0 {
			continue
		}
		p, ok := new(big.Rat).SetString(k.price)
		if k.price == "" || !ok {
			return fmt.Sprintf("unpriced (route %s has no %s price)", r.Name, k.kind)
		}
		sum.Add(sum, p.Mul(p, big.NewRat(k.n, 1)))
	}
	return dollars(sum.Quo(sum, big.NewRat(1_000_000, 1)))
}

// CostLine is the Cost: line of REPORT.md.
func CostLine(model string, route RouteRow, u TokenUsage) string {
	cost := route.Cost(model, u)
	return fmt.Sprintf("Cost: %s (opencode: %s) tokens input=%d cache_read=%d cache_write=%d output=%d reasoning=%d model=%s harness=opencode price_route=%s",
		cost, dollars(new(big.Rat).SetFloat64(max(u.Harness, 0))), u.Input, u.CacheRead, u.CacheWrite, u.Output, u.Reasoning, model, dashIf(route.Name))
}

func dashIf(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

var headLine = regexp.MustCompile(`^[#*_ -]*[Hh]ead:`)

// WithCost is the report with line under its Head: line (at the end when it has none);
// a report that already has a Cost: line is returned as it is.
func WithCost(report, line string) string {
	var b strings.Builder
	done := false
	for _, l := range strings.SplitAfter(report, "\n") {
		if strings.HasPrefix(l, "Cost: ") {
			return report
		}
		b.WriteString(l)
		if !done && headLine.MatchString(l) {
			if !strings.HasSuffix(l, "\n") {
				b.WriteString("\n")
			}
			b.WriteString(line + "\n")
			done = true
		}
	}
	out := b.String()
	if !done {
		if out != "" && !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		out += line + "\n"
	}
	return out
}

// ResultCostLines are the tokens: and cost: lines added to RESULT.md.
func ResultCostLines(model string, route RouteRow, u TokenUsage) string {
	cost := route.Cost(model, u)
	return fmt.Sprintf("tokens: input=%d cache_read=%d cache_write=%d output=%d reasoning=%d model=%s harness=opencode\ncost: %s (opencode: %s)\n",
		u.Input, u.CacheRead, u.CacheWrite, u.Output, u.Reasoning, model, cost, dollars(new(big.Rat).SetFloat64(max(u.Harness, 0))))
}

// PublishCost writes the cost onto the card's REPORT.md (the Cost: line under Head:) and
// RESULT.md (tokens: and cost: lines), each only when the file is there and has none.
func PublishCost(c Card, model string, route RouteRow, u TokenUsage) error {
	if raw, err := os.ReadFile(c.Report()); err == nil {
		if out := WithCost(string(raw), CostLine(model, route, u)); out != string(raw) {
			if err := atomicfile.WriteFile(c.Report(), []byte(out), 0o644); err != nil {
				return err
			}
		}
	}
	raw, err := os.ReadFile(c.Result())
	if err != nil || strings.Contains(string(raw), "\ncost: ") || strings.HasPrefix(string(raw), "cost: ") {
		return nil
	}
	out := string(raw)
	if out != "" && !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return atomicfile.WriteFile(c.Result(), []byte(out+ResultCostLines(model, route, u)), 0o644)
}

// OverCap says a card's usage has reached the row's per-card token cap.
func (r LaneRow) OverCap(u TokenUsage) bool { return r.TokenCap > 0 && u.Total() >= r.TokenCap }

// CapReport is the HOLD REPORT.md of a card whose lane was stopped at the cap: naming the
// cap, the tokens and the last step; Head: none, since whatever was pushed is a draft only.
func CapReport(friend string, c Card, limit int64, u TokenUsage, last string) string {
	if last == "" {
		last = "unknown"
	}
	return fmt.Sprintf("Verdict: HOLD\nHead: none\n\ntoken cap: %d tokens, last step: %s. %s's one-shot lane was stopped on card %s at the per-card cap of %d tokens; the card goes to a bud, and whatever was pushed on its branch is a draft only.\n",
		u.Total(), oneLine(last, 300), friend, c.ID, limit)
}

// WriteCapReport writes CapReport as the card's REPORT.md, so the lane's end finishes it.
func WriteCapReport(c Card, friend string, limit int64, u TokenUsage, last string) error {
	if err := os.MkdirAll(c.Outbox, 0o755); err != nil {
		return err
	}
	return atomicfile.WriteFile(c.Report(), []byte(CapReport(friend, c, limit, u, last)), 0o644)
}

// CapEvery is how often a running lane's tokens are read against the cap.
const CapEvery = 30 * time.Second

var providerFailure = regexp.MustCompile(`(?i)(^|[^0-9])(402|429)([^0-9]|$)|insufficient[ _-]?(funds|credit|balance|quota)|payment required|out of (funds|credits)|credit balance|exceeded your current quota|rate[ _-]?limit|too many requests`)

// ProviderFailureLine is the first Error: line of an opencode run's output that says a
// provider failure (402, 429, out of funds, rate limit), one line; empty when none does.
func ProviderFailureLine(out string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(stripANSI(line))
		if strings.HasPrefix(line, "Error: ") && providerFailure.MatchString(line) {
			return oneLine(line, 300)
		}
	}
	return ""
}

// DownArgv is the sprint server's verb that holds the friend down with the exact message:
// `friend down <friend> --reason <why>`; nothing resumes until a person runs friend up.
func DownArgv(friend, model, message string) []string {
	return []string{"friend", "down", friend, "--reason", fmt.Sprintf("provider failure (%s): %s", model, oneLine(message, 300))}
}

// ShimNames are the commands a lane's PATH refuses: go is never run on the lane machine.
var ShimNames = []string{"go", "gofmt"}

// ShimRefusal is what a shim prints to stderr; the exit is ShimExit.
const ShimRefusal = "REFUSED: no go build, test, vet or gofmt on this machine; run it on the Linux bench over ssh (the bench host the card names)"

// ShimExit is a shim's exit.
const ShimExit = 126

// NoGoRoot is the GOROOT a lane gets, which holds no toolchain.
const NoGoRoot = "/GO-NEVER-RUNS-ON-THIS-MACHINE-run-it-on-the-bench-over-ssh"

// WriteShims makes dir hold a symlink named for each of ShimNames to self, the
// nova-friend binary, which answers as a refusal when run by one of those names
// (ShimMain). It answers the names it made.
func WriteShims(dir, self string) ([]string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	var made []string
	for _, n := range ShimNames {
		p := filepath.Join(dir, n)
		if cur, err := os.Readlink(p); err == nil && cur == self {
			continue
		}
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return made, err
		}
		if err := os.Symlink(self, p); err != nil {
			return made, err
		}
		made = append(made, n)
	}
	return made, nil
}

// ShimMain says whether argv0 is a shim's name and, if so, the refusal to print and exit.
func ShimMain(argv0 string) (refusal string, exit int, is bool) {
	base := filepath.Base(argv0)
	for _, n := range ShimNames {
		if base == n {
			return ShimRefusal, ShimExit, true
		}
	}
	return "", 0, false
}

// LaneEnv is env with the shim directory first on PATH and GOROOT set to no toolchain.
func LaneEnv(env []string, shims string) []string {
	var out []string
	pathSet := false
	for _, kv := range env {
		switch {
		case strings.HasPrefix(kv, "GOROOT="):
			continue
		case strings.HasPrefix(kv, "PATH="):
			out = append(out, "PATH="+shims+string(os.PathListSeparator)+strings.TrimPrefix(kv, "PATH="))
			pathSet = true
			continue
		}
		out = append(out, kv)
	}
	if !pathSet {
		out = append(out, "PATH="+shims)
	}
	return append(out, "GOROOT="+NoGoRoot)
}

// FinishNote is the bus note to the coordinator at a lane's finish: the card, the report's
// verdict line, the cost and the wall.
func FinishNote(friend string, c Card, verdict, cost string, wall time.Duration) (subject, body string) {
	if verdict == "" {
		verdict = "no report"
	}
	subject = fmt.Sprintf("%s card %s: %s", friend, filepath.Base(c.Outbox), verdict)
	body = fmt.Sprintf("%s one-shot lane finished %s: %s; cost %s; wall %s\n", friend, filepath.Base(c.Outbox), verdict, cost, wall.Round(time.Second))
	return subject, body
}

func readFirstLine(p string) (string, error) {
	raw, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	line, _, _ := strings.Cut(string(raw), "\n")
	return strings.TrimSpace(line), nil
}
