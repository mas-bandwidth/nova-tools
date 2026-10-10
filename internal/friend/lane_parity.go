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
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
)

// What a friend's card runner script (runner.zsh, one copy each for two friends, the
// owner 2026-10-05: "We need to get away from these one shot shell scripts") did that the
// one-shot lanes did not, each a small function here (docs/SPEC-FRIEND.md, one-shot lanes
// at parity). They are configured on the friend row, as the beat's answer carries it
// (row_<name>=<value>, LaneRulesOf), with nothing about a friend in the code.

// LaneRules is what a friend's row says about which cards her lanes take and how hard they
// run: the zero value is no filter, no cap, no load rule, and a rate limit that backs off.
type LaneRules struct {
	Tiers     []string // row_tiers=flash,pro: the tiers she works; a dealt card of another tier is taken back; none: every tier
	Streams   []string // row_streams=security*,fp-sec*: patterns a card's stream or id must match; none: every card
	TokenCap  int64    // row_token_cap=<n>: tokens one card may spend, all kinds; 0 none
	LoadMax   float64  // row_load_max=<load>: above this one-minute load the lanes are held to LoadWidth; 0 none
	LoadWidth int      // row_load_width=<n>: lanes while the load is above LoadMax; DefaultLoadWidth when unset
	PauseOn   string   // row_pause_on=funds|any: funds (the default) holds only for out of funds; any holds for a rate limit too
	RefuseGo  bool     // row_refuse_go=1: the lane's PATH carries go and gofmt that refuse
}

// DefaultLoadWidth is how many lanes run while the load is above the row's bound.
const DefaultLoadWidth = 3

// LaneRulesOf reads the lane rules off the friend's beat answer; a word it does not
// know is ignored and a value that does not parse leaves the field unset.
func LaneRulesOf(answer string) LaneRules {
	var r LaneRules
	for _, w := range strings.Fields(answer) {
		k, v, ok := strings.Cut(w, "=")
		if !ok || !strings.HasPrefix(k, "row_") {
			continue
		}
		switch strings.TrimPrefix(k, "row_") {
		case "tiers":
			r.Tiers = splitList(v)
		case "streams":
			r.Streams = splitList(v)
		case "token_cap":
			if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
				r.TokenCap = n
			}
		case "load_max":
			if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
				r.LoadMax = f
			}
		case "load_width":
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				r.LoadWidth = n
			}
		case "pause_on":
			if v == "funds" || v == "any" {
				r.PauseOn = v
			}
		case "refuse_go":
			r.RefuseGo = v == "1" || v == "true"
		}
	}
	return r
}

func splitList(v string) []string {
	var out []string
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" && s != "-" {
			out = append(out, s)
		}
	}
	return out
}

// LaneVerdict is what the rules say of a dealt card.
type LaneVerdict int

const (
	LaneRun  LaneVerdict = iota // the lanes may run it
	LaneSkip                    // not hers to run: left on her row, never started
	LaneTake                    // outside her tiers: taken back for the dealer (friend take)
)

// Judge is the card filter: a card of a tier outside Tiers (an unknown tier is outside) is
// taken back; a card whose stream and id match none of Streams is skipped; any other runs.
// The reason is a sentence for the record and for friend take.
func (r LaneRules) Judge(id, stream, tier string) (LaneVerdict, string) {
	if len(r.Tiers) > 0 && !contains(r.Tiers, tier) {
		if tier == "" {
			tier = "-"
		}
		return LaneTake, fmt.Sprintf("tier %s is outside the tiers this friend works (%s); taken back for the dealer", tier, strings.Join(r.Tiers, ","))
	}
	if len(r.Streams) > 0 {
		for _, pat := range r.Streams {
			if m, _ := path.Match(pat, stream); m {
				return LaneRun, ""
			}
			if m, _ := path.Match(pat, id); m {
				return LaneRun, ""
			}
		}
		return LaneSkip, fmt.Sprintf("neither stream %s nor id %s matches %s", stream, id, strings.Join(r.Streams, ","))
	}
	return LaneRun, ""
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// TakeArgv is the coordinator's verb that takes a dealt, unstarted card back from the friend
// for the dealer: `friend take <friend> <card> --reason <why>`. The sprint server does not
// serve it to a friend's daemon (it is the coordinator's class, and the server runs only the
// workers' verbs), so the lanes never send it: they ask the coordinator (TakeBackNote).
func TakeArgv(friend, card, reason string) []string {
	return []string{"friend", "take", friend, card, "--reason", oneLine(reason, 300)}
}

// TakeBackNote is the bus note that asks the coordinator to take a card back for the dealer:
// the subject, and a body naming the card, why, and the exact verb to run (TakeArgv).
func TakeBackNote(friend, card, job, why string) (subject, body string) {
	argv := TakeArgv(friend, card, why)
	verb := "nova-sprint " + strings.Join(argv[:len(argv)-1], " ") + " " + shellQuote(argv[len(argv)-1])
	subject = fmt.Sprintf("friend %s: take card %s back for the dealer", friend, card)
	body = fmt.Sprintf("%s's lanes will not run card %s (job %s): %s. No lane has begun it and no jobs/%s exists. The sprint server serves no friend's take-back, so take it back: %s\n", friend, card, job, why, job, verb)
	return subject, body
}

// LaneWidthUnderLoad is how many lanes may start with the machine's one-minute load at load:
// the row's width, held to LoadWidth while the load is above the row's LoadMax (a width at
// or below LoadWidth is never raised). held says it was lowered.
func (r LaneRules) LaneWidthUnderLoad(width int, load float64) (n int, held bool) {
	lower := r.LoadWidth
	if lower <= 0 {
		lower = DefaultLoadWidth
	}
	if r.LoadMax > 0 && load > r.LoadMax && width > lower {
		return lower, true
	}
	return width, false
}

// OverTokenCap says a card has spent its cap; a rule with no cap is never over.
func (r LaneRules) OverTokenCap(tokens int64) bool { return r.TokenCap > 0 && tokens >= r.TokenCap }

// TokenCapReport is the REPORT.md of a card whose lane was stopped at the per-card token cap:
// HOLD with no head, one paragraph naming the cap, the tokens and turns it had spent and the
// last step; the card goes to a bud and whatever was pushed is a draft only.
func TokenCapReport(friend string, c int64, tokens int64, turns int, last string) string {
	if last == "" {
		last = "unknown"
	}
	return fmt.Sprintf("Verdict: HOLD\nHead: none\n\ntoken cap: %d tokens, %d turns, last step: %s. %s's one-shot lane was stopped at the per-card cap of %d tokens set on the friend row; the card goes to a bud, and whatever was pushed on its branch is a draft only.\n",
		tokens, turns, oneLine(last, 300), friend, c)
}

// ProviderStop is the provider failure that stops every lane, and the message to hold the
// friend down with, as the provider said it: out of funds (402) always; a rate limit (429)
// only when the row says pause_on=any (otherwise it backs off, rate-limit-backs-off-not-down).
func (r LaneRules) ProviderStop(err error) (message string, stop bool) {
	var funds OutOfFunds
	var rate RateLimited
	switch {
	case errors.As(err, &funds):
		return funds.Reason, true
	case errors.As(err, &rate):
		if r.PauseOn == "any" {
			return rate.Reason, true
		}
	}
	return "", false
}

// PauseFile is the marker in the state directory that holds the lanes down: it outlives the
// daemon, and nothing resumes until a person removes it (nova-friend resume).
const PauseFile = "PAUSED"

// WritePause records the exact provider message that held the friend down.
func WritePause(stateDir, message string, at time.Time) error {
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return err
	}
	return atomicfile.WriteFile(filepath.Join(stateDir, PauseFile), []byte(at.UTC().Format(time.RFC3339)+" "+oneLine(message, 400)+"\n"), 0o644)
}

// ReadPause is the pause marker's line; "" when the lanes are not paused.
func ReadPause(stateDir string) string {
	raw, err := os.ReadFile(filepath.Join(stateDir, PauseFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// ClearPause is a person bringing the lanes up: the marker removed; whether one was there.
func ClearPause(stateDir string) (bool, error) {
	err := os.Remove(filepath.Join(stateDir, PauseFile))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// PauseBeatAhead is how far ahead the friend's down beat sets its --until while the pause
// marker stands. Every beat sends it again, so it never lapses while she is paused; once a
// person clears the marker the next beat goes without --until and withdraws it.
const PauseBeatAhead = time.Hour

// PauseBeat is the friend's beat while the pause marker (its line, ReadPause) stands: down
// until PauseBeatAhead from now, with the provider's exact message as the reason. `friend beat
// <friend> --until <t> --reason <r>` is a worker's verb the sprint server serves; while her
// last beat says so she is down and the dealer deals her nothing (docs/SPEC-SPRINT.md).
func PauseBeat(marker, model string, now time.Time) (until time.Time, reason string) {
	msg := marker
	if at, rest, ok := strings.Cut(marker, " "); ok {
		if _, err := time.Parse(time.RFC3339, at); err == nil {
			msg = rest
		}
	}
	if model == "" {
		model = "-"
	}
	return now.Add(PauseBeatAhead).UTC().Truncate(time.Second), oneLine("provider failure ("+model+"): "+msg, 300)
}

// LaneTokens is what a session and its children spent, as opencode's own database says it,
// and what opencode itself priced it at.
type LaneTokens struct {
	cardcost.Tokens
	USD      string // opencode's own reported cost, a decimal; "" unknown
	Sessions int
}

// Sub is the tokens spent after base: a lane's session serves many cards, so a card's are
// the session's totals at its end less its totals at its start.
func (t LaneTokens) Sub(base LaneTokens) LaneTokens {
	d := func(a, b int64) int64 { return max(a-b, 0) }
	out := LaneTokens{Tokens: cardcost.Tokens{Input: d(t.Input, base.Input), CacheRead: d(t.CacheRead, base.CacheRead), CacheWrite: d(t.CacheWrite, base.CacheWrite),
		Output: d(t.Output, base.Output), Reasoning: d(t.Reasoning, base.Reasoning), Requests: cardcost.Unreported, MaxPrompt: cardcost.Unreported}, Sessions: t.Sessions}
	if a, err := cardcost.Decimal(t.USD); err == nil {
		b, berr := cardcost.Decimal(base.USD)
		if berr != nil {
			b = new(big.Rat)
		}
		if diff := new(big.Rat).Sub(a, b); diff.Sign() > 0 {
			out.USD = cardcost.Text(diff)
		} else {
			out.USD = "0"
		}
	}
	return out
}

var sessionID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)

// TokensSQL is the query for the tokens of a session and its children from opencode's database.
func TokensSQL(session string) (string, error) {
	if !sessionID.MatchString(session) {
		return "", fmt.Errorf("session %q is no opencode session id", session)
	}
	return "select coalesce(sum(tokens_input),0), coalesce(sum(tokens_cache_read),0), coalesce(sum(tokens_cache_write),0), coalesce(sum(tokens_output),0), coalesce(sum(tokens_reasoning),0), coalesce(sum(cost),0), count(*) from session where id='" + session + "' or parent_id='" + session + "';", nil
}

// TokensOf reads the one row TokensSQL prints (sqlite3's default, | between columns).
func TokensOf(out string) (LaneTokens, error) {
	f := strings.Split(strings.TrimSpace(out), "|")
	if len(f) != 7 {
		return LaneTokens{}, fmt.Errorf("opencode's database answered %d columns for the tokens, want 7: %q", len(f), oneLine(out, 100))
	}
	var n [7]int64
	for i := range n {
		if i == 5 {
			continue
		}
		v, err := strconv.ParseInt(f[i], 10, 64)
		if err != nil {
			return LaneTokens{}, fmt.Errorf("opencode's database: column %d of the tokens is no count: %q", i+1, f[i])
		}
		n[i] = v
	}
	usd := f[5]
	if _, err := cardcost.Decimal(usd); err != nil {
		usd = ""
	}
	return LaneTokens{Tokens: cardcost.Tokens{Input: n[0], CacheRead: n[1], CacheWrite: n[2], Output: n[3], Reasoning: n[4], Requests: cardcost.Unreported, MaxPrompt: cardcost.Unreported}, USD: usd, Sessions: int(n[6])}, nil
}

// TokensFromOpenCode reads a session's tokens (and its children's) from opencode's own database
// through the sqlite3 CLI, read only (the tree has no sqlite driver).
func TokensFromOpenCode(ctx context.Context, run Exec, db, session string) (LaneTokens, error) {
	sql, err := TokensSQL(session)
	if err != nil {
		return LaneTokens{}, err
	}
	out, exit, err := run(ctx, filepath.Dir(db), "sqlite3", []string{"-readonly", db, sql}, "")
	if err != nil {
		return LaneTokens{}, fmt.Errorf("sqlite3 %s: %w", db, err)
	}
	if exit != 0 {
		return LaneTokens{}, fmt.Errorf("sqlite3 %s exited %d", db, exit)
	}
	return TokensOf(out)
}

// ErrSessionInNoDB is a session that none of the databases read holds: its tokens are unread,
// never zero.
var ErrSessionInNoDB = errors.New("no opencode database read holds the session")

// TokensFromOpenCodeIn reads a session's tokens from the first of dbs that holds it (a row for
// the session or a child). A lane's opencode runs inside the wall with the wall's HOME (the
// friend's config directory, else her working directory: sandbox.LaneProfile), so it writes
// its sessions under that HOME, not the daemon's; a batch turn writes under the daemon's
// (--db). A session in none of them is ErrSessionInNoDB, naming each database and why, never the
// all-zero tokens the sums give for no row (2026-10-10: every walled opencode lane read zero
// tokens from the daemon's database, and its runs were judged empty).
func TokensFromOpenCodeIn(ctx context.Context, run Exec, dbs []string, session string) (LaneTokens, error) {
	var seen, tried []string
	for _, db := range dbs {
		if db == "" || slices.Contains(seen, db) {
			continue
		}
		seen = append(seen, db)
		t, err := TokensFromOpenCode(ctx, run, db, session)
		switch {
		case err != nil:
			tried = append(tried, db+" ("+oneLine(err.Error(), 120)+")")
		case t.Sessions == 0:
			tried = append(tried, db+" (no row)")
		default:
			return t, nil
		}
	}
	return LaneTokens{}, fmt.Errorf("%w %s: %s", ErrSessionInNoDB, session, strings.Join(tried, ", "))
}

// RoutePrice is the store's route row for the friend's exact provider/model: its name and
// price sheet; Found is false when there is none.
type RoutePrice struct {
	Name   string
	Prices cardcost.Prices
	Found  bool
}

// CostOf prices a card's tokens at the route row, rounded up to the cent; with no row, or a
// sheet that cannot price them, "unpriced (<why>)": never a guess.
func CostOf(t cardcost.Tokens, rp RoutePrice, model string) string {
	if !rp.Found {
		return "unpriced (no route row for " + model + " in the store's routes)"
	}
	p := cardcost.Predict(t, rp.Prices)
	if p.USD == "" {
		return "unpriced (route " + rp.Name + ": " + p.Why + ")"
	}
	usd, err := cardcost.Decimal(p.USD)
	if err != nil {
		return "unpriced (route " + rp.Name + ": " + cardcost.WhyBadPrice + "total)"
	}
	return cardcost.Cents(usd)
}

func opencodeCents(usd string) string {
	r, err := cardcost.Decimal(usd)
	if err != nil {
		return "-"
	}
	return cardcost.Cents(r)
}

// CostLine is the Cost: line a REPORT.md carries under its Head: line, and the tokens: and
// cost: lines a RESULT.md does. The card is priced by the route row; opencode's own figure is
// kept beside it.
func CostLine(t LaneTokens, rp RoutePrice, model string) (report, tokens, cost string) {
	cost = CostOf(t.Tokens, rp, model) + " (opencode: " + opencodeCents(t.USD) + ")"
	route := "-"
	if rp.Found {
		route = rp.Name
	}
	tokens = fmt.Sprintf("input=%s cache_read=%s cache_write=%s output=%s reasoning=%s model=%s harness=opencode", count(t.Input), count(t.CacheRead), count(t.CacheWrite), count(t.Output), count(t.Reasoning), model)
	return fmt.Sprintf("Cost: %s tokens %s price_route=%s", cost, tokens, route), "tokens: " + tokens, "cost: " + cost
}

// WithCost is report with line under its Head: line (at the end when it has none); a report
// that already carries a Cost: line is returned as it is.
func WithCost(report, line string) string {
	lines := strings.Split(strings.TrimRight(report, "\n"), "\n")
	at := -1
	for i, l := range lines {
		if strings.HasPrefix(l, "Cost: ") {
			return report
		}
		if at < 0 && regexp.MustCompile(`^[#*_ -]*[Hh]ead:`).MatchString(l) {
			at = i + 1
		}
	}
	if at < 0 {
		at = len(lines)
	}
	lines = append(lines[:at], append([]string{line}, lines[at:]...)...)
	return strings.Join(lines, "\n") + "\n"
}

// PublishCost writes the card's cost into its outbox: the Cost: line into REPORT.md, which
// friend sync reads, and the tokens: and cost: lines onto RESULT.md when it is there. A
// report not there is not made.
func PublishCost(outbox string, t LaneTokens, rp RoutePrice, model string) error {
	report, tokens, cost := CostLine(t, rp, model)
	if raw, err := os.ReadFile(filepath.Join(outbox, "REPORT.md")); err == nil {
		if out := WithCost(string(raw), report); out != string(raw) {
			if err := atomicfile.WriteFile(filepath.Join(outbox, "REPORT.md"), []byte(out), 0o644); err != nil {
				return err
			}
		}
	}
	if raw, err := os.ReadFile(filepath.Join(outbox, "RESULT.md")); err == nil && !strings.Contains(string(raw), "\ncost: ") {
		out := strings.TrimRight(string(raw), "\n") + "\n" + tokens + "\n" + cost + "\n"
		return atomicfile.WriteFile(filepath.Join(outbox, "RESULT.md"), []byte(out), 0o644)
	}
	return nil
}

// FinishNote is the bus note to the coordinator at a lane's finish: the subject and body.
func FinishNote(friend, job, verdictLine, cost string, wall time.Duration) (subject, body string) {
	if verdictLine == "" {
		verdictLine = "no report"
	}
	subject = fmt.Sprintf("%s card %s: %s", friend, job, verdictLine)
	body = fmt.Sprintf("%s one-shot lane finished %s: %s; cost %s; wall %s\n", friend, job, verdictLine, cost, wall.Round(time.Second))
	return subject, body
}

// The go refusal: no go command runs on the machine that runs the lanes (the owner's rule: it
// is not a build machine). A lane's PATH carries go and gofmt that refuse, and GOROOT points nowhere.
const (
	NoGoRoot    = "/NO-GO-ON-THIS-MACHINE-run-it-on-a-bench-over-ssh"
	ShimDirName = "shims"
)

// GoShimNames are the commands the shims stand in for.
var GoShimNames = []string{"go", "gofmt"}

// GoShimName says whether argv0 names a shim (this binary run as go or gofmt, by symlink).
func GoShimName(argv0 string) (string, bool) {
	name := filepath.Base(argv0)
	return name, contains(GoShimNames, name)
}

// GoRefusal is what a shim prints, and exits 2 with.
func GoRefusal(name string) string {
	return name + " is refused: no go command runs on this machine, it is not a build machine; run: rsync the job's clone to a bench, then ssh <bench> 'cd <clone> && " + name + " ...'"
}

// GoShims makes dir hold a go and a gofmt that are this binary (self) by symlink, so that
// run by those names it refuses (GoShimName, GoRefusal): no script, nothing to drift. Links
// already right are kept.
func GoShims(dir, self string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, name := range GoShimNames {
		link := filepath.Join(dir, name)
		if cur, err := os.Readlink(link); err == nil && cur == self {
			continue
		}
		_ = os.Remove(link) // ignored: absent is the usual case; a link that stays fails the Symlink below
		if err := os.Symlink(self, link); err != nil {
			return err
		}
	}
	return nil
}

// ShimExec is run with the shim directory first on the PATH of every lane child and GOROOT
// pointing nowhere: a command whose context is a lane's runs as `env PATH=<shims>:<path>
// GOROOT=<nowhere> <name> <args>`; any other runs as it was. Put it outside Wall.Exec so the
// env runs inside the wall.
func ShimExec(run Exec, shimDir, pathEnv string) Exec {
	return func(ctx context.Context, dir, name string, args []string, stdin string) (string, int, error) {
		if !InLane(ctx) || shimDir == "" {
			return run(ctx, dir, name, args, stdin)
		}
		pre := []string{"PATH=" + shimDir + string(os.PathListSeparator) + pathEnv, "GOROOT=" + NoGoRoot, name}
		return run(ctx, dir, "env", append(pre, args...), stdin)
	}
}

// count is a token count as a line shows it; a class the harness did not report is "unreported".
func count(n int64) string {
	if n < 0 {
		return "unreported"
	}
	return strconv.FormatInt(n, 10)
}

// Load1Of reads the one-minute load off `sysctl -n vm.loadavg` ("{ 1.23 1.50 1.60 }") or
// /proc/loadavg ("1.23 1.50 1.60 1/300 12345").
func Load1Of(out string) (float64, bool) {
	f := strings.Fields(strings.NewReplacer("{", " ", "}", " ").Replace(out))
	if len(f) == 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(f[0], 64)
	return v, err == nil && v >= 0
}

// Over is r with every field row sets in place of its own: the flags give the defaults and
// the friend row, as her beat answers it, wins.
func (r LaneRules) Over(row LaneRules) LaneRules {
	if len(row.Tiers) > 0 {
		r.Tiers = row.Tiers
	}
	if len(row.Streams) > 0 {
		r.Streams = row.Streams
	}
	if row.TokenCap > 0 {
		r.TokenCap = row.TokenCap
	}
	if row.LoadMax > 0 {
		r.LoadMax = row.LoadMax
	}
	if row.LoadWidth > 0 {
		r.LoadWidth = row.LoadWidth
	}
	if row.PauseOn != "" {
		r.PauseOn = row.PauseOn
	}
	r.RefuseGo = r.RefuseGo || row.RefuseGo
	return r
}

// RoutePriceOf finds the route row for provider/model in the sprint's `routes --json`
// answer: the first object anywhere in it that has a prices object and this provider and
// model. A route that does not say reasoning_as_output bills reasoning as output.
func RoutePriceOf(routesJSON, provider, model string) RoutePrice {
	var doc any
	if err := json.Unmarshal([]byte(routesJSON), &doc); err != nil {
		return RoutePrice{}
	}
	var found *RoutePrice
	var walk func(v any)
	walk = func(v any) {
		if found != nil {
			return
		}
		switch x := v.(type) {
		case map[string]any:
			if pr, ok := x["prices"].(map[string]any); ok && x["provider"] == provider && x["model"] == model {
				if _, said := pr["reasoning_as_output"]; !said {
					pr["reasoning_as_output"] = true
				}
				raw, _ := json.Marshal(pr)
				var p cardcost.Prices
				if json.Unmarshal(raw, &p) == nil {
					name, _ := x["name"].(string)
					found = &RoutePrice{Name: name, Prices: p, Found: true}
					return
				}
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
	if found == nil {
		return RoutePrice{}
	}
	return *found
}

// The lane hands the brief by absolute path (the-lane-hands-the-brief-by-absolute-path-bb; on
// 2026-10-07 five lanes of a flash friend ended "File not found: Volumes/nova/ai/<friend>/working/
// inbox/<card>/BRIEF.md": the model dropped the path's leading slash, read it relative to the
// friend's working directory, found nothing, wrote no report, and even made a stray
// <working>/Volumes/nova/... tree there). A lane runs its harness in the card's job directory,
// names every path absolute, and hands the brief's text inline, so a model that reads a path
// relative still has the brief and writes inside the job; a run that exits 0 with no report is a
// harness fault, never the worker's failed attempt (docs/SPEC-FRIEND.md, the lane's paths).

// LaneJob is a card as its lane hands it to the harness: the job directory the harness runs in,
// the card with its brief and outbox absolute, and the brief's text, which the prompt carries.
type LaneJob struct {
	Dir   string // <friend dir>/jobs/<job>: the harness's working directory
	Card  Card   // Brief and Outbox absolute
	Brief string // the brief's text, inline in the prompt; "" when it is not read yet
}

// LaneJobOf is card c of the friend's working directory dir as its lane hands it: every path
// made absolute by abs (filepath.Abs in the daemon), the job directory dir/jobs/<job>. A path
// abs cannot make absolute is refused: the error is one REFUSED line naming the path, and the
// lane does not start.
func LaneJobOf(dir string, c Card, abs func(string) (string, error)) (LaneJob, error) {
	if abs == nil {
		abs = filepath.Abs
	}
	made := func(p string) (string, error) {
		a, err := abs(p)
		if err == nil && !filepath.IsAbs(a) {
			err = errors.New("not absolute after filepath.Abs")
		}
		if err != nil {
			return "", fmt.Errorf("REFUSED lane path not absolute: %s: %v; the lane does not start", p, err)
		}
		return filepath.Clean(a), nil
	}
	root, err := made(dir)
	if err != nil {
		return LaneJob{}, err
	}
	brief, err := made(c.Brief)
	if err != nil {
		return LaneJob{}, err
	}
	outbox, err := made(c.Outbox)
	if err != nil {
		return LaneJob{}, err
	}
	c.Brief, c.Outbox = brief, outbox
	return LaneJob{Dir: filepath.Join(root, "jobs", filepath.Base(outbox)), Card: c}, nil
}

// laneDirKey marks a context with the working directory a lane's harness runs in.
type laneDirKey struct{}

// WithLaneDir is ctx carrying dir, the job directory the lane's harness runs in (LaneJob.Dir).
func WithLaneDir(ctx context.Context, dir string) context.Context {
	return context.WithValue(ctx, laneDirKey{}, dir)
}

// LaneDirOf is the job directory ctx carries, else def: a harness's run in a lane goes there.
func LaneDirOf(ctx context.Context, def string) string {
	if d, _ := ctx.Value(laneDirKey{}).(string); d != "" {
		return d
	}
	return def
}

// HarnessFaultNoReport is the fault of a lane run that exited 0 and left no REPORT.md or
// RESULT.md: the harness's, never the worker's failed attempt.
const HarnessFaultNoReport = "harness-fault: no report"

var harnessErrorLine = regexp.MustCompile(`(?i)\b(error|errors|not found|no such file|failed|failure|exception|denied|refused|cannot|could not)\b`)

// HarnessFirstError is the first line of a harness's output that says an error, one line, at
// most 200 bytes; "" when none does.
func HarnessFirstError(out string) string {
	for _, line := range strings.Split(stripANSI(out), "\n") {
		if line = strings.TrimSpace(line); line != "" && harnessErrorLine.MatchString(line) {
			return oneLine(line, 200)
		}
	}
	return ""
}

// NoReport is a card runner's run that ended with its outbox holding neither REPORT.md nor
// RESULT.md (the Claude lane's), its words as the run says them.
type NoReport struct {
	Run    string // the harness as the run names it, "claude -p"
	Exit   int
	Outbox string
	Lacks  []string // the files the outbox lacks
}

func (e NoReport) Error() string {
	return fmt.Sprintf("%s exited %d and %s holds no %s", e.Run, e.Exit, e.Outbox, strings.Join(e.Lacks, " and no "))
}

// FaultWords is a harness fault as the record and the row say it: the fault and the harness's
// first error line ("the harness printed no error line" when it printed none).
func FaultWords(fault, first string) string {
	if first == "" {
		first = "the harness printed no error line"
	}
	return fault + "; first error: " + oneLine(first, 200)
}

// The bound on a harness fault: the same fault FaultRepeats times within FaultWithin on one
// row marks the row down for FaultDownFor, once, with one judgment to the seat.
const (
	FaultRepeats = 3
	FaultWithin  = 10 * time.Minute
	FaultDownFor = 15 * time.Minute
)

// FaultWatch counts one row's harness faults: the FaultRepeats-th of the same fault within
// FaultWithin is one down until FaultDownFor later; a fault while that down stands adds
// nothing, and the count starts again once it has passed. The zero value is ready; it reads no
// clock of its own.
type FaultWatch struct {
	seen  map[string][]time.Time
	until time.Time
}

// Observe adds one fault at now and answers the down the row is owed, once: until and true on
// the FaultRepeats-th of that fault within FaultWithin.
func (w *FaultWatch) Observe(fault string, now time.Time) (until time.Time, down bool) {
	if now.Before(w.until) {
		return time.Time{}, false // the row is down already: one down, not one per card
	}
	if w.seen == nil {
		w.seen = map[string][]time.Time{}
	}
	var kept []time.Time
	for _, at := range w.seen[fault] {
		if now.Sub(at) < FaultWithin {
			kept = append(kept, at)
		}
	}
	kept = append(kept, now)
	if len(kept) < FaultRepeats {
		w.seen[fault] = kept
		return time.Time{}, false
	}
	w.seen = nil
	w.until = now.Add(FaultDownFor)
	return w.until, true
}

// FaultDownText is the one judgment the seat is told when a row's lanes hit the same harness
// fault FaultRepeats times within FaultWithin: the subject and the body, naming the reason the
// row is down with and until when.
func FaultDownText(friend, reason string, until time.Time) (subject, body string) {
	subject = fmt.Sprintf("friend %s down until %s: %s", friend, until.UTC().Format(time.RFC3339), oneLine(reason, 120))
	body = fmt.Sprintf("%s: her lanes hit the same harness fault %d times within %s: %s\nHer lanes are held and her beat says her down until then; the cards stay in her lanes' hands, no attempt counted, and run again after it. The fault is the machine's, never the worker's: find its cause.\n",
		subject, FaultRepeats, FaultWithin, reason)
	return subject, body
}

// RescueStray moves a report a run wrote under a relative spelling of the outbox (the job
// directory joined with the outbox path less its leading slash: the stray tree of 2026-10-07)
// into the outbox, REPORT.md and RESULT.md each when the outbox lacks it; it answers the files
// moved.
func RescueStray(job LaneJob) []string {
	if job.Dir == "" || !filepath.IsAbs(job.Card.Outbox) {
		return nil
	}
	stray := filepath.Join(job.Dir, strings.TrimLeft(job.Card.Outbox, string(filepath.Separator)))
	var moved []string
	for _, f := range []string{"REPORT.md", "RESULT.md"} {
		from, to := filepath.Join(stray, f), filepath.Join(job.Card.Outbox, f)
		if fi, err := os.Lstat(from); err != nil || !fi.Mode().IsRegular() || exists(to) {
			continue
		}
		if err := os.MkdirAll(job.Card.Outbox, 0o755); err != nil {
			continue
		}
		if err := os.Rename(from, to); err == nil {
			moved = append(moved, to)
		}
	}
	return moved
}
