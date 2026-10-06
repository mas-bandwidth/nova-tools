package friend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/big"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
)

// What the two runner.zsh stopgaps (two friends' copies of one runner, 2026-10-04/05) did that the opencode
// one-shot lanes did not, each a small function the lane's turn calls (lane_parity_loop.go;
// docs/SPEC-FRIEND.md, one-shot lanes, the runner's behaviours): the card filter and the take
// back, the job's name by generation, the width under load, the per-card token cap, the
// provider failure that stops every lane, the card's cost, the go refusal shims and the note at
// each finish. Friend beat's answer carries none of their settings (row_mode, row_width,
// row_config_dir and the like only), so each is a nova-friend run flag with a default
// (LaneParity); the row keys each would need are named in the spec's "What is weak, and known".

// LaneParity is the runner's settings for a friend's opencode one-shot lanes, from nova-friend
// run's flags. Its zero value is the runner with nothing configured: every card runs, no token
// cap, no load bound, no shims; a provider failure still stops every lane (StopOnProvider is
// set by run for an opencode friend).
type LaneParity struct {
	Tiers          []string // --lane-tiers: the tiers she works; a card of another tier, not started, is owed back to the dealer
	Streams        []string // --lane-streams: stream or id globs she works; a card matching none is skipped
	TokenCap       int64    // --lane-token-cap: the tokens one card may use, all kinds; 0 none
	LoadMax        float64  // --lane-load-max: the 1-minute load above which the width is held to LoadWidth; 0 none
	LoadWidth      int      // --lane-load-width: the held width; 0 is DefaultLoadWidth
	StopOnProvider bool     // a provider failure stops every lane and holds them (the runner's PAUSED)
	Model          string   // provider/model, what the cost line names and the route row is found by
	Shims          string   // the directory of the go refusal shims, first on the lane's PATH; "" none
	HoldFile       string   // the file a provider hold is kept in until a person removes it; "" in memory only
	Said           string   // the settings as one line, said on the record at the lanes' first step

	// Load is the machine's 1-minute load; nil reads none (no hold under load).
	Load func(ctx context.Context) (float64, error)
	// Usage is a session's tokens and its children's from opencode's own database; nil reads
	// none (no cap, and the cost line says unread).
	Usage func(ctx context.Context, session string) (TokenUsage, error)
	// Routes is nova-sprint routes --json as the sprint server prints it; nil is no route row.
	Routes func(ctx context.Context) (string, error)
}

// DefaultLoadWidth is the width a loaded machine holds the lanes to (the runner's 3).
const DefaultLoadWidth = 3

// FilterAction is what the filter says of a dealt card.
type FilterAction string

const (
	FilterRun  FilterAction = "run"  // she works it
	FilterSkip FilterAction = "skip" // not hers; left alone
	FilterTake FilterAction = "take" // not hers and not started: owed back to the dealer
)

// Filter is the card filter on a dealt card: a tier outside Tiers is to be taken back (she works
// only those; a card with no tier is of tier "-"), an id or stream matching none of Streams is
// skipped. why is one line.
func (p LaneParity) Filter(id, stream, tier string) (FilterAction, string) {
	tier = strings.ToLower(strings.TrimSpace(tier))
	if tier == "" {
		tier = "-"
	}
	if len(p.Tiers) > 0 {
		ok := false
		for _, t := range p.Tiers {
			ok = ok || strings.EqualFold(t, tier)
		}
		if !ok {
			return FilterTake, fmt.Sprintf("tier %s: she works only %s cards; the rest go back to the dealer", tier, strings.Join(p.Tiers, ", "))
		}
	}
	if len(p.Streams) == 0 {
		return FilterRun, ""
	}
	for _, g := range p.Streams {
		for _, name := range []string{id, stream} {
			if m, err := path.Match(g, name); err == nil && m && name != "" {
				return FilterRun, ""
			}
		}
	}
	return FilterSkip, fmt.Sprintf("card %s (stream %s) matches none of %s", id, dash(stream), strings.Join(p.Streams, ", "))
}

// TakeBackText is the blocker to the coordinator for a dealt card outside her filter and not
// started: friend take is a coordinator verb the sprint server refuses from a friend, so the
// daemon asks the coordinator to run it, the exact line given, and never runs it itself.
func TakeBackText(friend, card, why string) (subject, body string) {
	subject = fmt.Sprintf("friend %s: card %s is outside her filter: take it back", friend, card)
	body = fmt.Sprintf("Card %s was dealt to %s and is outside her lanes' filter (%s). Her lanes will not start it. Taking it back is a coordinator verb the sprint server refuses from a friend; run: nova-sprint friend take %s %s --reason %s\n",
		card, friend, oneLine(why, 200), friend, card, shellQuote(oneLine(why, 200)))
	return subject, body
}

// JobName is the inbox directory friend sync names a card's job: <card>~<epoch>, with .g<gen>
// after it from the card's second generation (ParseJob reads it back).
func JobName(card string, epoch uint64, gen int) string {
	job := card + "~" + strconv.FormatUint(epoch, 10)
	if gen > 1 {
		job += ".g" + strconv.Itoa(gen)
	}
	return job
}

// heldJob is a held card's job: the one the server names, else JobName of its card, epoch and
// generation.
func heldJob(h HeldCard) string {
	if h.Job != "" {
		return h.Job
	}
	return JobName(h.Card, h.Epoch, h.Gen)
}

// LoadWidthAt is the width the lanes run at: width, held to LoadWidth (DefaultLoadWidth when
// zero) while load is above LoadMax, never raised by it. held says it is held.
func (p LaneParity) LoadWidthAt(width int, load float64) (n int, held bool) {
	if p.LoadMax <= 0 || load <= p.LoadMax {
		return width, false
	}
	low := p.LoadWidth
	if low <= 0 {
		low = DefaultLoadWidth
	}
	if low >= width {
		return width, false
	}
	return low, true
}

// LoadOf reads the 1-minute load from /proc/loadavg ("0.52 0.58 0.59 1/389 12345") or
// sysctl -n vm.loadavg ("{ 0.52 0.58 0.59 }").
func LoadOf(s string) (float64, error) {
	for _, f := range strings.Fields(s) {
		if f == "{" {
			continue
		}
		return strconv.ParseFloat(f, 64)
	}
	return 0, fmt.Errorf("no load in %q", oneLine(s, 80))
}

// ReadLoad is the machine's 1-minute load: /proc/loadavg where there is one, else sysctl -n
// vm.loadavg through run.
func ReadLoad(ctx context.Context, run Exec) (float64, error) {
	if raw, err := os.ReadFile("/proc/loadavg"); err == nil {
		return LoadOf(string(raw))
	}
	out, exit, err := run(ctx, "/", "sysctl", []string{"-n", "vm.loadavg"}, "")
	if err != nil {
		return 0, err
	}
	if exit != 0 {
		return 0, fmt.Errorf("sysctl -n vm.loadavg exited %d", exit)
	}
	return LoadOf(out)
}

// TokenUsage is a run's tokens, its session's and its children's, as opencode's own database
// holds them, and opencode's own cost beside them.
type TokenUsage struct {
	Input, CacheRead, CacheWrite, Output, Reasoning int64
	Harness                                         float64 // opencode's own reported cost, USD
}

// Total is every kind of token.
func (u TokenUsage) Total() int64 {
	return u.Input + u.CacheRead + u.CacheWrite + u.Output + u.Reasoning
}

// Since is u less base: what one card used in a lane session that had used base before it.
func (u TokenUsage) Since(base TokenUsage) TokenUsage {
	return TokenUsage{u.Input - base.Input, u.CacheRead - base.CacheRead, u.CacheWrite - base.CacheWrite, u.Output - base.Output, u.Reasoning - base.Reasoning, u.Harness - base.Harness}
}

// UsageSQL reads a session's usage and its children's from opencode's database: input, cache
// read, cache write, output, reasoning, opencode's cost.
func UsageSQL(session string) string {
	id := strings.ReplaceAll(session, "'", "''")
	return "select coalesce(sum(tokens_input),0), coalesce(sum(tokens_cache_read),0), coalesce(sum(tokens_cache_write),0), coalesce(sum(tokens_output),0), coalesce(sum(tokens_reasoning),0), coalesce(sum(cost),0) from session where id='" + id + "' or parent_id='" + id + "';"
}

// TokenUsageOf reads UsageSQL's one row as sqlite3 prints it, pipe separated.
func TokenUsageOf(out string) (TokenUsage, error) {
	f := strings.Split(strings.TrimSpace(out), "|")
	if len(f) != 6 {
		return TokenUsage{}, fmt.Errorf("opencode database: want 6 columns, got %q", oneLine(out, 100))
	}
	var n [5]int64
	for i := range n {
		v, err := strconv.ParseInt(f[i], 10, 64)
		if err != nil {
			return TokenUsage{}, fmt.Errorf("opencode database: column %d is no whole number: %q", i+1, f[i])
		}
		n[i] = v
	}
	h, err := strconv.ParseFloat(f[5], 64)
	if err != nil {
		return TokenUsage{}, fmt.Errorf("opencode database: cost is no number: %q", f[5])
	}
	return TokenUsage{n[0], n[1], n[2], n[3], n[4], h}, nil
}

// ReadTokenUsage runs sqlite3 read-only on opencode's database at db for the session, through
// run (the tree has no sqlite driver; the CLI is the runner's own read).
func ReadTokenUsage(ctx context.Context, run Exec, db, session string) (TokenUsage, error) {
	out, exit, err := run(ctx, filepath.Dir(db), "sqlite3", []string{"-readonly", db, UsageSQL(session)}, "")
	if err != nil {
		return TokenUsage{}, fmt.Errorf("opencode database: %w", err)
	}
	if exit != 0 {
		return TokenUsage{}, fmt.Errorf("opencode database: sqlite3 exited %d: %s", exit, oneLine(out, 200))
	}
	return TokenUsageOf(out)
}

// RouteRow is the store's route row for her provider/model: its name and price sheet.
type RouteRow struct {
	Name   string
	Prices cardcost.Prices
}

// RouteOf finds the route row for model (provider/model) anywhere in nova-sprint routes --json:
// the first object with prices whose provider and model are model's; found is false when none.
func RouteOf(routesJSON, model string) (RouteRow, bool, error) {
	provider, id, ok := strings.Cut(model, "/")
	if !ok {
		return RouteRow{}, false, nil
	}
	var v any
	if err := json.Unmarshal([]byte(routesJSON), &v); err != nil {
		return RouteRow{}, false, fmt.Errorf("nova-sprint routes --json: %v", err)
	}
	var found map[string]any
	var walk func(any)
	walk = func(v any) {
		if found != nil {
			return
		}
		switch x := v.(type) {
		case map[string]any:
			if _, has := x["prices"]; has && x["provider"] == provider && x["model"] == id {
				found = x
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
	walk(v)
	if found == nil {
		return RouteRow{}, false, nil
	}
	raw, _ := json.Marshal(found) // ignored: it was just decoded from JSON
	var r struct {
		Name   string          `json:"name"`
		Prices cardcost.Prices `json:"prices"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return RouteRow{}, false, fmt.Errorf("nova-sprint routes --json: route for %s: %v", model, err)
	}
	return RouteRow{Name: r.Name, Prices: r.Prices}, true, nil
}

// centsUp is x as $d.cc, rounded up to the cent.
func centsUp(x *big.Rat) string {
	c := new(big.Rat).Mul(x, big.NewRat(100, 1))
	n := new(big.Int).Quo(c.Num(), c.Denom())
	if new(big.Rat).SetInt(n).Cmp(c) < 0 {
		n.Add(n, big.NewInt(1))
	}
	return fmt.Sprintf("$%d.%02d", new(big.Int).Quo(n, big.NewInt(100)), new(big.Int).Rem(n, big.NewInt(100)))
}

// Cost is the card's price at the route row, rounded up to the cent, or "unpriced (<why>)",
// never a guess: no row, a sheet with prices this pricer does not apply, or a kind of token used
// that the sheet has no price for. Reasoning is priced as output when the sheet says so.
func (r RouteRow) Cost(model string, u TokenUsage) string {
	p := r.Prices
	switch {
	case r.Name == "":
		return fmt.Sprintf("unpriced (no route row for %s in nova-sprint routes)", dash(model))
	case p.LongContext > 0 || p.Request != "" || p.GatewayPercent != "":
		return fmt.Sprintf("unpriced (route %s has long-context, request or gateway prices this pricer does not apply)", r.Name)
	}
	out := u.Output
	if p.ReasoningAsOutput {
		out += u.Reasoning
	}
	sum := new(big.Rat)
	for _, k := range []struct {
		kind, price string
		n           int64
	}{{"input", p.Input, u.Input}, {"cache_read", p.CacheRead, u.CacheRead}, {"cache_write", p.CacheWrite, u.CacheWrite}, {"output", p.Output, out}} {
		if k.n == 0 {
			continue
		}
		price, err := cardcost.Decimal(k.price)
		if k.price == "" || err != nil {
			return fmt.Sprintf("unpriced (route %s has no %s price)", r.Name, k.kind)
		}
		sum.Add(sum, new(big.Rat).Mul(price, big.NewRat(k.n, 1)))
	}
	return centsUp(sum.Quo(sum, big.NewRat(1_000_000, 1)))
}

// CostWords is the card's cost at the route row with opencode's own figure beside it.
func CostWords(model string, r RouteRow, u TokenUsage) string {
	return fmt.Sprintf("%s (opencode: %s)", r.Cost(model, u), centsUp(new(big.Rat).SetFloat64(max(u.Harness, 0))))
}

// CostLine is the Cost: line of REPORT.md and RESULT.md: the cost words and the tokens.
func CostLine(model string, r RouteRow, u TokenUsage) string {
	return fmt.Sprintf("Cost: %s tokens input=%d cache_read=%d cache_write=%d output=%d reasoning=%d model=%s harness=opencode price_route=%s",
		CostWords(model, r, u), u.Input, u.CacheRead, u.CacheWrite, u.Output, u.Reasoning, dash(model), dash(r.Name))
}

// UnreadCostLine is the Cost: line when the tokens could not be read.
func UnreadCostLine(model, why string) string {
	return fmt.Sprintf("Cost: unpriced (the tokens were not read: %s) model=%s harness=opencode", oneLine(why, 200), dash(model))
}

var headLine = regexp.MustCompile(`^[#*_ -]*[Hh]ead:`)

// WithCost is report with line under its Head: line (at the end when it has none); a report
// with a Cost: line already is returned as it is.
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

// PublishCost puts the cost line on the card's REPORT.md (under Head:) and on its RESULT.md (at
// the end), each only when the file is there and carries no Cost: line yet.
func PublishCost(c Card, line string) error {
	for _, p := range []string{c.Report(), c.Result()} {
		raw, err := os.ReadFile(p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		out := string(raw)
		if p == c.Report() {
			out = WithCost(out, line)
		} else if !strings.Contains("\n"+out, "\nCost: ") {
			if out != "" && !strings.HasSuffix(out, "\n") {
				out += "\n"
			}
			out += line + "\n"
		}
		if out != string(raw) {
			if err := atomicfile.WriteFile(p, []byte(out), 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}

// OverCap says a card's tokens have reached the per-card cap.
func (p LaneParity) OverCap(u TokenUsage) bool { return p.TokenCap > 0 && u.Total() >= p.TokenCap }

// CapReport is the HOLD REPORT.md of a card whose lane was stopped at the token cap: the cap,
// the tokens and the last line the run printed; Head: none, whatever was pushed is a draft.
func CapReport(friend string, c Card, limit int64, u TokenUsage, last string) string {
	if last = oneLine(last, 300); last == "" {
		last = "unknown"
	}
	return fmt.Sprintf("Verdict: HOLD\nHead: none\n\ntoken cap: %d tokens of a cap of %d, last step: %s. %s's one-shot lane was stopped on card %s at the per-card token cap (nova-friend run --lane-token-cap); the card goes back for another worker, and whatever was pushed on its branch is a draft only.\n",
		u.Total(), limit, last, friend, c.ID)
}

// ProviderFailure matches a provider failure in an opencode run's Error: line: 402, 429, out of
// funds or credit, a rate limit (the runner's PROV_RE).
var providerFailure = regexp.MustCompile(`(?i)(^|[^0-9])(402|429)([^0-9]|$)|insufficient[ _-]?(funds|credit|balance|quota)|payment required|out of (funds|credits)|credit balance|exceeded your current quota|rate[ _-]?limit|too many requests`)

// ProviderFailureLine is the first Error: line of an opencode run's output that says a provider
// failure, one line, the exact message; "" when none does.
func ProviderFailureLine(out string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(stripANSI(line))
		if strings.HasPrefix(line, "Error: ") && providerFailure.MatchString(line) {
			return oneLine(line, 300)
		}
	}
	return ""
}

// ProviderStopText is the blocker to the coordinator when a provider failure stopped every
// lane: the exact message, the lanes held, and friend down, a coordinator verb the sprint
// server refuses from a friend, as the line to run.
func ProviderStopText(friend, model, message, holdFile string) (subject, body string) {
	reason := fmt.Sprintf("provider failure (%s): %s", dash(model), oneLine(message, 300))
	subject = fmt.Sprintf("friend %s: %s", friend, oneLine(reason, 160))
	resume := "restart her daemon"
	if holdFile != "" {
		resume = "remove " + holdFile
	}
	body = fmt.Sprintf("A provider failure stopped every one of %s's lanes: %s. Each running turn was ended and its card kept to run again, never counted as an attempt; her lanes start nothing until a person brings them up (%s). Showing her down on her row is a coordinator verb the sprint server refuses from a friend; run: nova-sprint friend down %s --reason %s\n",
		friend, oneLine(message, 300), resume, friend, shellQuote(reason))
	return subject, body
}

// ShimNames are the commands a lane's PATH refuses: go never runs on the lane machine.
var ShimNames = []string{"go", "gofmt"}

// ShimRefusal is what a shim prints on stderr; ShimExit its exit.
const (
	ShimRefusal = "go is refused on this machine (nova-friend run --refuse-go): no go, gofmt, go test or go vet here; sync the clone to the Linux bench the card names and run it there over ssh"
	ShimExit    = 126
	// NoGoRoot is the GOROOT a lane gets, holding no toolchain.
	NoGoRoot = "/GO-NEVER-RUNS-ON-THIS-MACHINE-run-it-on-the-bench-over-ssh"
)

// WriteShims makes dir hold a symlink named for each of ShimNames to self, the nova-friend
// binary, which refuses when run by one of those names (ShimRefuses). It answers the names it
// made or changed.
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
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return made, err
		}
		if err := os.Symlink(self, p); err != nil {
			return made, err
		}
		made = append(made, n)
	}
	return made, nil
}

// ShimRefuses says whether argv0 names a shim, so nova-friend run by that name refuses.
func ShimRefuses(argv0 string) bool {
	base := filepath.Base(argv0)
	for _, n := range ShimNames {
		if base == n {
			return true
		}
	}
	return false
}

// RunShim is nova-friend run by a shim's name: the refusal on stderr, and its exit.
func RunShim(stderr io.Writer) int {
	fmt.Fprintln(stderr, ShimRefusal)
	return ShimExit
}

// ShimEnv is env with shims first on PATH and GOROOT pointing at no toolchain.
func ShimEnv(env []string, shims string) []string {
	out := make([]string, 0, len(env)+2)
	pathSet := false
	for _, kv := range env {
		switch {
		case strings.HasPrefix(kv, "GOROOT="):
			continue
		case strings.HasPrefix(kv, "PATH="):
			kv = "PATH=" + shims + string(os.PathListSeparator) + strings.TrimPrefix(kv, "PATH=")
			pathSet = true
		}
		out = append(out, kv)
	}
	if !pathSet {
		out = append(out, "PATH="+shims)
	}
	return append(out, "GOROOT="+NoGoRoot)
}

type shimKey struct{}

// WithShims is ctx carrying the shim directory: a command run under it (RealExec) has ShimEnv.
func WithShims(ctx context.Context, shims string) context.Context {
	if shims == "" {
		return ctx
	}
	return context.WithValue(ctx, shimKey{}, shims)
}

func shimsOf(ctx context.Context) string {
	s, _ := ctx.Value(shimKey{}).(string)
	return s
}

// FinishNote is the bus note to the coordinator at a lane's finish: the job, the report's first
// line, the cost and the wall.
func FinishNote(friend, job, verdict, cost string, wall time.Duration) (subject, body string) {
	if verdict == "" {
		verdict = "no report"
	}
	subject = fmt.Sprintf("%s card %s: %s", friend, job, oneLine(verdict, 120))
	body = fmt.Sprintf("%s's one-shot lane finished %s: %s; %s; wall %s\n", friend, job, oneLine(verdict, 200), cost, wall.Round(time.Second))
	return subject, body
}

// firstLine is the first line of the file at p; "" when it cannot be read.
func firstLine(p string) string {
	raw, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	line, _, _ := strings.Cut(string(raw), "\n")
	return strings.TrimSpace(line)
}

// ModelInOpenCodeConfig is the model <dir>/opencode.json names ("" none).
func ModelInOpenCodeConfig(dir string) string {
	raw, err := os.ReadFile(filepath.Join(dir, OpenCodeConfig))
	if err != nil {
		return ""
	}
	var cfg struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(raw, &cfg) != nil {
		return ""
	}
	return cfg.Model
}

// briefTierLine is a brief's "tier: <t>" line.
var briefTierLine = regexp.MustCompile(`(?im)\btier:\s*([a-z]+)`)

// briefTier is the tier the brief's tier line names ("" none): the runner's fallback when her row
// does not say the card's tier.
func briefTier(brief string) string {
	raw, err := os.ReadFile(brief)
	if err != nil {
		return ""
	}
	if m := briefTierLine.FindSubmatch(raw); m != nil {
		return strings.ToLower(string(m[1]))
	}
	return ""
}
