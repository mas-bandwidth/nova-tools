package friend

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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

// What the one-shot lanes took over from the runner stopgaps (the owner, 2026-10-05: "We need
// to get away from these one shot shell scripts"; two friends' copies of one zsh runner,
// runner.zsh): the card filter and the take
// back, the generation's job name, the width under load, the per-card token cap, the hold on a
// provider out of funds, the card's cost from opencode's own database priced by the route row,
// the refusal shims for go on the lanes' PATH, and a bus note at each finish
// (docs/SPEC-FRIEND.md, one-shot lanes: what the runner did).

// LaneConfig is what a friend's one-shot lanes take beyond her row's mode and width: the
// cards she works (Tiers, and Cards, glob patterns over a card's stream or id), the width
// held while the machine is loaded, the per-card token cap, and the model the route row
// prices. Its zero value filters nothing, holds nothing and caps nothing.
type LaneConfig struct {
	Tiers     []string // the tiers she works; none is every tier
	Cards     []string // glob patterns (path.Match) over a card's stream or id; none is every card
	LoadMax   float64  // the 1-minute load above which new lanes are held to LoadWidth; 0 is off
	LoadWidth int      // the lanes held to while the load is above LoadMax; 0 is DefaultLoadWidth
	TokenCap  int64    // a card's tokens, every kind, at which its lane is stopped with a HOLD; 0 is off
	Model     string   // provider/model the lanes run, the route row that prices a card
}

// DefaultLoadWidth is the lanes held to while the machine's load is above LoadMax (the
// runner's 3).
const DefaultLoadWidth = 3

// The friend-row words a beat's answer may carry for the lanes, beside row_mode and
// row_width. nova-sprint friend beat prints none of them yet (its row has no such
// columns): until it does, the flags of nova-friend run say them.
const (
	RowTiers     = "row_tiers"
	RowCards     = "row_cards"
	RowLoadMax   = "row_load_max"
	RowLoadWidth = "row_load_width"
	RowTokenCap  = "row_token_cap"
	RowModel     = "row_model"
)

// ParseLaneRow is c with what a beat's answer says of the lanes (row_tiers=, row_cards=
// comma lists; row_load_max=, row_load_width=, row_token_cap= numbers; row_model=) in place
// of the flags'; a word the answer does not carry, or carries unreadable, leaves c's.
func ParseLaneRow(answer string, c LaneConfig) LaneConfig {
	for _, w := range strings.Fields(answer) {
		k, v, ok := strings.Cut(w, "=")
		if !ok {
			continue
		}
		switch k {
		case RowTiers:
			c.Tiers = commaWords(v)
		case RowCards:
			c.Cards = commaWords(v)
		case RowLoadMax:
			if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 {
				c.LoadMax = f
			}
		case RowLoadWidth:
			if n, err := strconv.Atoi(v); err == nil && n >= 0 {
				c.LoadWidth = n
			}
		case RowTokenCap:
			if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
				c.TokenCap = n
			}
		case RowModel:
			c.Model = v
		}
	}
	return c
}

// commaWords is a comma list's words, blanks dropped.
func commaWords(s string) []string {
	var out []string
	for _, w := range strings.Split(s, ",") {
		if w = strings.TrimSpace(w); w != "" {
			out = append(out, w)
		}
	}
	return out
}

// Dealt is one card the sprint dealt her, as `nova-sprint queue --as friend.<me> --json`
// answers it: its id, column, stream, generation and epoch, and the tier its brief says.
type Dealt struct {
	ID, Col, Stream, Tier string
	Epoch                 uint64
	Gen                   int
}

// JobName is the card's job, the directory friend sync delivers it to: <id>~<epoch>, and
// .g<gen> past the first generation (cmd/nova-sprint friendJobOf).
func JobName(id string, epoch uint64, gen int) string {
	job := id + "~" + strconv.FormatUint(epoch, 10)
	if gen > 1 {
		job += ".g" + strconv.Itoa(gen)
	}
	return job
}

// Job is the dealt card's job name.
func (d Dealt) Job() string { return JobName(d.ID, d.Epoch, d.Gen) }

// briefTier is the tier a brief says (the runner's capture: "tier: <word>").
var briefTier = regexp.MustCompile(`\btier: ?([a-z]+)`)

// TierOfBrief is the tier the brief says, "" when it says none.
func TierOfBrief(brief string) string {
	if m := briefTier.FindStringSubmatch(brief); m != nil {
		return m[1]
	}
	return ""
}

// ParseDealt reads the cards of a queue answer: each card's packet gives its epoch and
// brief (the answer's epoch when the card carries no packet), its brief the tier.
func ParseDealt(answer string) ([]Dealt, error) {
	var q struct {
		Epoch uint64 `json:"epoch"`
		Cards []struct {
			ID     string `json:"id"`
			Col    string `json:"col"`
			Stream string `json:"stream"`
			Gen    int    `json:"gen"`
			Packet *struct {
				Epoch  uint64 `json:"epoch"`
				Brief  string `json:"brief"`
				Stream string `json:"stream"`
			} `json:"packet"`
		} `json:"cards"`
	}
	if err := json.Unmarshal([]byte(answer), &q); err != nil {
		return nil, fmt.Errorf("the queue's answer is not its JSON: %v", err)
	}
	var out []Dealt
	for _, c := range q.Cards {
		d := Dealt{ID: c.ID, Col: c.Col, Stream: c.Stream, Gen: max(c.Gen, 1), Epoch: q.Epoch}
		if p := c.Packet; p != nil {
			if p.Epoch != 0 {
				d.Epoch = p.Epoch
			}
			if d.Stream == "" {
				d.Stream = p.Stream
			}
			d.Tier = TierOfBrief(p.Brief)
		}
		out = append(out, d)
	}
	return out, nil
}

// Works says whether her lanes run the card, and when not, why: a tier she does not work,
// or a stream and an id that match none of her card patterns.
func (c LaneConfig) Works(d Dealt) (bool, string) {
	if len(c.Tiers) > 0 && !slices.Contains(c.Tiers, d.Tier) {
		return false, fmt.Sprintf("tier %s is not one she works (%s)", dash(d.Tier), strings.Join(c.Tiers, ","))
	}
	if len(c.Cards) > 0 && !slices.ContainsFunc(c.Cards, func(p string) bool { return globbed(p, d.Stream) || globbed(p, d.ID) }) {
		return false, fmt.Sprintf("stream %s and id %s match none of her cards (%s)", dash(d.Stream), d.ID, strings.Join(c.Cards, ","))
	}
	return true, ""
}

func globbed(pattern, s string) bool {
	ok, err := path.Match(pattern, s)
	return err == nil && ok && s != ""
}

// TakeBack is a dealt card her lanes give back, and why.
type TakeBack struct {
	Card Dealt
	Why  string
}

// TakeBacks is the dealt cards outside her filter that she has not started (started: a
// lane holds it, its jobs/<job> exists, or its outbox has a REPORT.md) and that were not
// asked for already (asked, by job), in the queue's order.
func TakeBacks(c LaneConfig, dealt []Dealt, started func(Dealt) bool, asked map[string]bool) []TakeBack {
	var out []TakeBack
	for _, d := range dealt {
		if ok, why := c.Works(d); !ok && !asked[d.Job()] && !started(d) {
			out = append(out, TakeBack{Card: d, Why: why})
		}
	}
	return out
}

// TakeBackText is the one blocker that asks the coordinator to take the cards back: the
// sprint server keeps friend take the coordinator's, so her daemon asks with the exact line
// (cmd/nova-sprint coordinator.go, verbClasses).
func TakeBackText(friend string, takes []TakeBack) (subject, body string) {
	ids := make([]string, len(takes))
	var why []string
	for i, t := range takes {
		ids[i] = t.Card.ID
		why = append(why, t.Card.ID+": "+t.Why)
	}
	subject = fmt.Sprintf("friend %s: take back %d card(s) outside her filter: %s", friend, len(takes), strings.Join(ids, " "))
	body = fmt.Sprintf("Her lanes do not run these cards and have not started them:\n%s\nRun: nova-sprint friend take %s %s --reason %s\n",
		strings.Join(why, "\n"), friend, strings.Join(ids, " "), shellQuote("outside "+friend+"'s filter: her lanes do not run it"))
	return subject, body
}

// LoadCap is the lanes allowed at the machine's 1-minute load: width, held to LoadWidth
// (DefaultLoadWidth when zero) while the load is above LoadMax; an unread load holds nothing.
func (c LaneConfig) LoadCap(width int, load float64, read bool) int {
	held := c.LoadWidth
	if held <= 0 {
		held = DefaultLoadWidth
	}
	if c.LoadMax <= 0 || !read || load <= c.LoadMax {
		return width
	}
	return min(width, held)
}

// Tokens is a run's token counts as opencode's database keeps them on its sessions, and
// the cost opencode itself reports.
type Tokens struct {
	Input, CacheRead, CacheWrite, Output, Reasoning int64
	Cost                                            float64 // opencode's own, USD
	Sessions                                        int
}

// Total is every kind of token.
func (t Tokens) Total() int64 { return t.Input + t.CacheRead + t.CacheWrite + t.Output + t.Reasoning }

// Since is t less what base counted: a card's tokens in a lane's session it shares with
// the cards before it.
func (t Tokens) Since(base Tokens) Tokens {
	return Tokens{Input: t.Input - base.Input, CacheRead: t.CacheRead - base.CacheRead, CacheWrite: t.CacheWrite - base.CacheWrite,
		Output: t.Output - base.Output, Reasoning: t.Reasoning - base.Reasoning, Cost: t.Cost - base.Cost, Sessions: t.Sessions}
}

// Said is the token counts as the Cost: line and RESULT.md say them.
func (t Tokens) Said() string {
	return fmt.Sprintf("input=%d cache_read=%d cache_write=%d output=%d reasoning=%d", t.Input, t.CacheRead, t.CacheWrite, t.Output, t.Reasoning)
}

// sessionID is what an opencode session id looks like; nothing else is put in a query.
var sessionID = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// SessionTokensSQL is the query of a session's tokens and its children's, as the runner
// read them (opencode's session table keeps each session's sums).
func SessionTokensSQL(session string) (string, error) {
	if !sessionID.MatchString(session) {
		return "", fmt.Errorf("%q is no opencode session id", session)
	}
	return "select coalesce(sum(tokens_input),0), coalesce(sum(tokens_cache_read),0), coalesce(sum(tokens_cache_write),0), " +
		"coalesce(sum(tokens_output),0), coalesce(sum(tokens_reasoning),0), coalesce(sum(cost),0), count(*) from session " +
		"where id = '" + session + "' or parent_id = '" + session + "';", nil
}

// ParseSessionTokens reads the query's one row (tab or | separated).
func ParseSessionTokens(out string) (Tokens, error) {
	f := strings.FieldsFunc(strings.TrimSpace(out), func(r rune) bool { return r == '\t' || r == '|' || r == ' ' })
	if len(f) != 7 {
		return Tokens{}, fmt.Errorf("the session tokens query answered %q, not seven columns", oneLine(out, 200))
	}
	var n [5]int64
	for i := range n {
		v, err := strconv.ParseInt(f[i], 10, 64)
		if err != nil {
			return Tokens{}, fmt.Errorf("the session tokens query's column %d is %q, not a count", i+1, f[i])
		}
		n[i] = v
	}
	cost, err := strconv.ParseFloat(f[5], 64)
	if err != nil {
		return Tokens{}, fmt.Errorf("the session tokens query's cost is %q", f[5])
	}
	sessions, err := strconv.Atoi(f[6])
	if err != nil {
		return Tokens{}, fmt.Errorf("the session tokens query's count is %q", f[6])
	}
	return Tokens{Input: n[0], CacheRead: n[1], CacheWrite: n[2], Output: n[3], Reasoning: n[4], Cost: cost, Sessions: sessions}, nil
}

// SessionTokens reads session's tokens and its children's from opencode's database at db
// with `sqlite3 -readonly` through run (internal/swarm reads the same database the same way:
// the tree has no SQLite driver).
func SessionTokens(ctx context.Context, run Exec, db, session string) (Tokens, error) {
	q, err := SessionTokensSQL(session)
	if err != nil {
		return Tokens{}, err
	}
	out, exit, err := run(ctx, "", "sqlite3", []string{"-readonly", "-tabs", db, q}, "")
	if err != nil {
		return Tokens{}, fmt.Errorf("sqlite3 on %s: %v", db, err)
	}
	if exit != 0 {
		return Tokens{}, fmt.Errorf("sqlite3 on %s exited %d: %s", db, exit, oneLine(out, 200))
	}
	return ParseSessionTokens(out)
}

// Route is the route row that prices a card: its name and price sheet.
type Route struct {
	Name   string
	Prices cardcost.Prices
}

// FindRoute is the route of `nova-sprint routes --json` whose provider/model is model;
// found is false when there is none.
func FindRoute(answer, model string) (r Route, found bool, err error) {
	var q struct {
		Routes []struct {
			Route struct {
				Name     string          `json:"name"`
				Provider string          `json:"provider"`
				Model    string          `json:"model"`
				Prices   cardcost.Prices `json:"prices"`
			} `json:"route"`
		} `json:"routes"`
	}
	if err := json.Unmarshal([]byte(answer), &q); err != nil {
		return Route{}, false, fmt.Errorf("the routes answer is not its JSON: %v", err)
	}
	for _, s := range q.Routes {
		if s.Route.Provider+"/"+s.Route.Model == model || (!strings.Contains(model, "/") && s.Route.Model == model) {
			return Route{Name: s.Route.Name, Prices: s.Route.Prices}, true, nil
		}
	}
	return Route{}, false, nil
}

// CostOf is a card's cost: its tokens priced by the route row, rounded up to the cent
// ("$0.43"), else "unpriced (<why>)", never a guess. A sheet with a long-context price is
// not applied: opencode's session sums do not keep each request's prompt.
func CostOf(t Tokens, r Route, found bool, model string) string {
	if !found {
		return fmt.Sprintf("unpriced (no route row for %s in nova-sprint routes)", dash(model))
	}
	if r.Prices.LongContext > 0 {
		return fmt.Sprintf("unpriced (route %s has a long-context price, and opencode's session sums keep no request's prompt)", r.Name)
	}
	p := cardcost.Predict(cardcost.Tokens{Input: t.Input, CacheRead: t.CacheRead, CacheWrite: t.CacheWrite, Output: t.Output, Reasoning: t.Reasoning,
		Requests: cardcost.Unreported, MaxPrompt: cardcost.Unreported}, r.Prices)
	if p.USD == "" {
		return fmt.Sprintf("unpriced (route %s: %s)", r.Name, p.Why)
	}
	usd, err := cardcost.Decimal(p.USD)
	if err != nil {
		return fmt.Sprintf("unpriced (route %s: %v)", r.Name, err)
	}
	return cardcost.Cents(usd)
}

// usdCents is opencode's own cost as a line says money: rounded up to the cent.
func usdCents(f float64) string {
	r := new(big.Rat)
	if f > 0 {
		r.SetFloat64(f)
	}
	return cardcost.Cents(r)
}

// CostLine is the card's Cost: line on REPORT.md.
func CostLine(cost string, t Tokens, model, route string) string {
	return fmt.Sprintf("Cost: %s (opencode: %s) tokens %s model=%s harness=opencode price_route=%s", cost, usdCents(t.Cost), t.Said(), dash(model), dash(route))
}

// The report a lane's friend may leave unpublished, and the files a finish writes.
const (
	ReportDraft = "REPORT.draft.md"
	ReportFile  = "REPORT.md"
)

var headLine = regexp.MustCompile(`(?i)^[#*_ -]*head:`)

// WithCost is report with line under its Head: line (at its end when it has none); a report
// with a Cost: line already is as it was.
func WithCost(report, line string) string {
	lines := strings.Split(strings.TrimRight(report, "\n"), "\n")
	for _, l := range lines {
		if strings.HasPrefix(l, "Cost: ") {
			return report
		}
	}
	at := len(lines)
	for i, l := range lines {
		if headLine.MatchString(l) {
			at = i + 1
			break
		}
	}
	return strings.Join(slices.Insert(lines, at, line), "\n") + "\n"
}

// PublishCost writes the card's cost into its outbox: REPORT.draft.md, else REPORT.md, is
// published as REPORT.md with the Cost: line, and RESULT.md gets a tokens: and a cost: line;
// with no line (a harness whose tokens are not read) the draft is published as it is and
// RESULT.md is left alone. It answers the report's first line ("no report" when there is none).
func PublishCost(outbox, line string, t Tokens, model, cost string) (string, error) {
	first := "no report"
	src := filepath.Join(outbox, ReportDraft)
	raw, err := os.ReadFile(src)
	if err != nil {
		src = filepath.Join(outbox, ReportFile)
		raw, err = os.ReadFile(src)
	}
	if err == nil {
		first, _, _ = strings.Cut(string(raw), "\n")
		text := string(raw)
		if line != "" {
			text = WithCost(text, line)
		}
		if err := atomicfile.WriteFile(filepath.Join(outbox, ReportFile), []byte(text), 0o644); err != nil {
			return first, err
		}
		if filepath.Base(src) == ReportDraft {
			if err := os.Remove(src); err != nil {
				return first, err
			}
		}
	}
	result := filepath.Join(outbox, "RESULT.md")
	if raw, err := os.ReadFile(result); err == nil && line != "" && !strings.Contains(string(raw), "\ncost: ") {
		text := strings.TrimRight(string(raw), "\n") + "\n" +
			fmt.Sprintf("tokens: %s model=%s harness=opencode\ncost: %s (opencode: %s)\n", t.Said(), dash(model), cost, usdCents(t.Cost))
		if err := atomicfile.WriteFile(result, []byte(text), 0o644); err != nil {
			return first, err
		}
	}
	return first, nil
}

// CapReport is the HOLD a lane writes when its card reaches the token cap: the head the friend
// pushed on the brief's branch when a clone holds it (PushedHead), else none.
func CapReport(friend, card string, tokens, limit int64, head, branch string) string {
	pushed := "Head: none\n"
	draft := "nothing was pushed on its branch"
	if head != "" {
		pushed = "Head: " + head + "\n"
		draft = "the head pushed on " + branch + " is a draft only"
	}
	return fmt.Sprintf("Verdict: HOLD\n%s\ntoken cap: %s's one-shot lane was stopped on card %s at %d tokens, at or above her per-card cap of %d tokens (every kind of token, read from opencode's database); the card goes back to the coordinator, and %s.\n",
		pushed, friend, card, tokens, limit, draft)
}

// FinishText is the bus note to the coordinator at each card's finish.
func FinishText(friend, job, first, cost string, wall time.Duration) (subject, body string) {
	subject = fmt.Sprintf("%s card %s: %s", friend, job, oneLine(first, 120))
	body = fmt.Sprintf("%s's one-shot lane finished %s: %s; cost %s; wall %s\n", friend, job, oneLine(first, 200), cost, wall.Round(time.Second))
	return subject, body
}

// HoldFile is the provider's hold in the state directory: while it is there the lanes start
// nothing and the daemon does not beat, across restarts, until a person runs
// nova-friend resume.
const HoldFile = "HELD"

// ReadHold is the hold's message, found false when there is none.
func ReadHold(path string) (string, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(raw)), true
}

// WriteHold writes the hold with its exact message.
func WriteHold(path, message string) error {
	return atomicfile.WriteFile(path, []byte(message+"\n"), 0o644)
}

// The refusal shims: go and gofmt on a lane's PATH are this binary, which refuses
// (RefuseGo), and GOROOT points nowhere, so no go command runs on a machine that forbids
// them (the runner's runner/bin/go and gofmt).
const (
	ShimDir     = "bin"
	NoGoRoot    = "/GO-NEVER-RUNS-HERE-run-it-on-a-bench-over-ssh"
	GoBenchEnv  = "NOVA_FRIEND_GO_BENCH"
	ShimRefused = "GO REFUSED"
)

// ShimNames are the programs a shim stands in for.
var ShimNames = []string{"go", "gofmt"}

// WriteShims makes dir/bin/go and dir/bin/gofmt links to self, the binary that refuses
// when run by those names; it answers the bin directory.
func WriteShims(dir, self string) (string, error) {
	bin := filepath.Join(dir, ShimDir)
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return "", err
	}
	for _, name := range ShimNames {
		p := filepath.Join(bin, name)
		if to, err := os.Readlink(p); err == nil && to == self {
			continue
		}
		_ = os.Remove(p) // ignored: a path not there is the link to make; one that stays fails the Symlink below
		if err := os.Symlink(self, p); err != nil {
			return "", err
		}
	}
	return bin, nil
}

// ShimEnv is the environment words a lane's child runs with: bin first on PATH, GOROOT
// nowhere, and the benches the refusal names.
func ShimEnv(bin, path, benches string) []string {
	return []string{"PATH=" + bin + string(os.PathListSeparator) + path, "GOROOT=" + NoGoRoot, GoBenchEnv + "=" + benches}
}

// RefuseGo is what a shim says: the program is refused here, and how to run it on a bench.
func RefuseGo(program, benches string) string {
	if benches == "" {
		benches = "a bench"
	}
	return fmt.Sprintf("%s %s: go never runs on this machine; sync the clone to %s and run it there over ssh: rsync -a --delete jobs/<job>/repo/ <bench>:<dir>/ && ssh <bench> 'cd <dir> && %s ...'\n",
		ShimRefused, program, benches, program)
}

// RunShim is the binary run by a shim's name: the refusal on stderr, exit 1.
func RunShim(argv0, benches string, stderr io.Writer) int {
	fmt.Fprint(stderr, RefuseGo(filepath.Base(argv0), benches))
	return 1
}

// IsShim says whether the binary was run by a shim's name.
func IsShim(argv0 string) bool { return slices.Contains(ShimNames, filepath.Base(argv0)) }

// ShimExec is run with every lane child's environment led by env (ShimEnv): a command whose
// context is a lane's (LaneContext) runs as `env <env> <name> <args>`, inside its wall when
// run walls it; any other runs as it was, and so does every command when env is empty.
func ShimExec(env []string, run Exec) Exec {
	return func(ctx context.Context, dir, name string, args []string, stdin string) (string, int, error) {
		if len(env) == 0 || !InLane(ctx) {
			return run(ctx, dir, name, args, stdin)
		}
		return run(ctx, dir, "env", slices.Concat(env, []string{name}, args), stdin)
	}
}
