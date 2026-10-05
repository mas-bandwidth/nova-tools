package friend

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Claude is a headless Claude Code account as a friend (docs/SPEC-FRIEND.md,
// the headless Claude lane): every turn is one `claude -p` run on the
// account's config directory, the text on stdin, the trimmed call
// (ClaudeArgv), its stream-json read for the run's price and its
// rate_limit_event. It replaces the hand-written runner.zsh and reader.zsh of
// 2026-10-04 (the owner: "Golang nova-tools and nova-sprint verbs only").
// A lane's session is no claude session: OpenSession runs nothing and keeps
// the lane's seed, which heads each card's run, so every card starts from the
// friend's own files and nothing of the last card (the trimmed call measured
// about 12.7k tokens a call against about 50k for the full one).
type Claude struct {
	Dir       string
	ConfigDir string    // CLAUDE_CONFIG_DIR: the account; empty is the user's own ~/.claude
	Program   string    // "claude" when empty
	StateDir  string    // where ClaudeLimitsFile is kept; empty keeps it in memory only
	Model     string    // the model when the card's tier names none; empty is ClaudeModels[""]
	Run       Exec      // RealExec in the daemon
	Out       io.Writer // the record: one CLAUDE RUN line a run, CLAUDE LIMIT at a limit
	Now       func() time.Time
	// OnLimit hears a run that met the account's limit, with the limit and its
	// own reset: the daemon's supervisor stops it (no beat, no card) until
	// then. The run that met it returns only when its ctx ends, so no lane
	// takes the card again meanwhile. Nil: the run returns at once.
	OnLimit func(ClaudeLimit)

	mu     sync.Mutex
	seeds  map[string]string
	last   ClaudeRun
	kept   ClaudeLimits
	loaded bool
}

// ClaudeTools are the only tools a run has (--tools): with --strict-mcp-config,
// --disable-slash-commands and --no-chrome, the trimmed call.
var ClaudeTools = []string{"Bash", "Read", "Write", "Edit", "Grep", "Glob"}

// ClaudeModels is the model a card's tier runs on, its brief's `RESULT: <id>
// tier: <tier>` line (the owner, 2026-10-04 4:15 PM); "" is a card that names
// no tier.
var ClaudeModels = map[string]string{
	"frontier": "claude-fable-5-1",
	"heavy":    "claude-opus-5-5",
	"pro":      "claude-sonnet-5-5",
	"flash":    "claude-haiku-4-5-20251001",
	"":         "claude-opus-5-5",
}

// ClaudeArgv is one run's arguments after the program: print mode with
// stream-json (which needs --verbose), the model, the permission mode a
// headless run works under, and the trimmed call. The prompt goes on stdin.
func ClaudeArgv(model string) []string {
	args := []string{"-p", "--output-format", "stream-json", "--verbose", "--model", model,
		"--permission-mode", "bypassPermissions", "--strict-mcp-config", "--disable-slash-commands", "--no-chrome", "--tools"}
	return append(args, ClaudeTools...)
}

// ClaudeLimit is one window of the account's limit as a rate_limit_event said
// it: the window (five_hour, seven_day, ...), allowed, allowed_warning or
// rejected, the utilization as printed, and when the window resets.
type ClaudeLimit struct {
	Type        string    `json:"type"`
	Status      string    `json:"status"`
	Utilization string    `json:"utilization,omitempty"`
	ResetsAt    time.Time `json:"resets_at"`
}

// Rejected is the limit met: no run passes until ResetsAt.
func (l ClaudeLimit) Rejected() bool { return l.Status == "rejected" }

func (l ClaudeLimit) said() string {
	return fmt.Sprintf("%s:%s:%s:%s", dash(l.Type), dash(l.Status), dash(l.Utilization), stamp(l.ResetsAt))
}

// ClaudeRun is what one run's stream-json said: its price and tokens from the
// result event (a class not reported stays empty, never 0), its turns, whether
// it ended in error and the result's text, and the limits of every
// rate_limit_event, the latest per window.
type ClaudeRun struct {
	Session                                    string
	CostUSD                                    string
	Turns                                      int
	TokensIn, TokensOut, CacheWrite, CacheRead string
	IsError                                    bool
	Result                                     string
	Limits                                     []ClaudeLimit
}

// claudeLimitText is a run's text saying the account is out (the words the
// runners matched, 2026-10-04): with no rejected rate_limit_event, the limit
// is still met.
var claudeLimitText = regexp.MustCompile(`(?i)usage limit|hit your limit|limit reached|out of extra usage|weekly limit`)

// ReadClaudeStream reads a run's stream-json, one JSON object a line; a line
// that is not one (a warning on stderr) is skipped.
func ReadClaudeStream(out string) ClaudeRun {
	var r ClaudeRun
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		var ev struct {
			Type      string `json:"type"`
			SessionID string `json:"session_id"`
			Info      *struct {
				Status      string       `json:"status"`
				ResetsAt    json.Number  `json:"resetsAt"`
				Type        string       `json:"rateLimitType"`
				Utilization *json.Number `json:"utilization"`
			} `json:"rate_limit_info"`
			IsError  bool         `json:"is_error"`
			NumTurns int          `json:"num_turns"`
			Result   string       `json:"result"`
			Cost     *json.Number `json:"total_cost_usd"`
			Usage    *struct {
				In         *int64 `json:"input_tokens"`
				Out        *int64 `json:"output_tokens"`
				CacheWrite *int64 `json:"cache_creation_input_tokens"`
				CacheRead  *int64 `json:"cache_read_input_tokens"`
			} `json:"usage"`
		}
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "{") || json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		if ev.SessionID != "" {
			r.Session = ev.SessionID
		}
		switch ev.Type {
		case "rate_limit_event":
			if ev.Info == nil {
				continue
			}
			l := ClaudeLimit{Type: ev.Info.Type, Status: ev.Info.Status}
			if ev.Info.Utilization != nil {
				l.Utilization = ev.Info.Utilization.String()
			}
			if s, err := ev.Info.ResetsAt.Int64(); err == nil && s > 0 {
				l.ResetsAt = time.Unix(s, 0).UTC()
			}
			r.Limits = setLimit(r.Limits, l)
		case "result":
			r.IsError, r.Turns, r.Result = ev.IsError, ev.NumTurns, ev.Result
			if ev.Cost != nil {
				r.CostUSD = ev.Cost.String()
			}
			if u := ev.Usage; u != nil {
				r.TokensIn, r.TokensOut, r.CacheWrite, r.CacheRead = count(u.In), count(u.Out), count(u.CacheWrite), count(u.CacheRead)
			}
		}
	}
	return r
}

func count(n *int64) string {
	if n == nil {
		return ""
	}
	return strconv.FormatInt(*n, 10)
}

// setLimit is ls with l in its window's place, the windows in the order first said.
func setLimit(ls []ClaudeLimit, l ClaudeLimit) []ClaudeLimit {
	for i := range ls {
		if ls[i].Type == l.Type {
			ls[i] = l
			return ls
		}
	}
	return append(ls, l)
}

// Met is the limit the run met, if any: a rejected window (the latest reset
// of those rejected), else, when the run ended in error saying it is out, the
// latest reset any window said, else an hour from now, said as a guess.
func (r ClaudeRun) Met(now time.Time) (ClaudeLimit, bool) {
	var met ClaudeLimit
	for _, l := range r.Limits {
		if l.Rejected() && l.ResetsAt.After(met.ResetsAt) {
			met = l
		}
	}
	if met.Status != "" {
		return met, true
	}
	if !r.IsError || !claudeLimitText.MatchString(r.Result) {
		return ClaudeLimit{}, false
	}
	met = ClaudeLimit{Type: "unknown", Status: "rejected", ResetsAt: now.Add(time.Hour).UTC()}
	for _, l := range r.Limits {
		if l.ResetsAt.After(now) && (met.Type == "unknown" || l.ResetsAt.After(met.ResetsAt)) {
			met.Type, met.ResetsAt = l.Type, l.ResetsAt
		}
	}
	return met, true
}

// ClaudeLimits is the account as its runs have said it, kept in the state
// directory (ClaudeLimitsFile) for status: the runs, their summed price, the
// latest limits per window, and the reset of a limit met (zero when none).
type ClaudeLimits struct {
	At      time.Time     `json:"at"`
	Runs    int           `json:"runs"`
	CostUSD string        `json:"cost_usd"`
	Limits  []ClaudeLimit `json:"limits,omitempty"`
	Until   time.Time     `json:"until,omitzero"`
	Met     string        `json:"met,omitempty"` // the window met
}

// ClaudeLimitsFile is the account's limits in the state directory.
const ClaudeLimitsFile = "claude-limits.json"

// ReadClaudeLimits is what the runs last kept; found is false when none has.
func ReadClaudeLimits(stateDir string) (l ClaudeLimits, found bool, err error) {
	found, err = read(filepath.Join(stateDir, ClaudeLimitsFile), &l)
	return l, found, err
}

func (c *Claude) program() string {
	if c.Program == "" {
		return "claude"
	}
	return c.Program
}

func (c *Claude) now() time.Time {
	if c.Now == nil {
		return time.Now()
	}
	return c.Now()
}

// Last is the last run as its stream said it.
func (c *Claude) Last() ClaudeRun {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last
}

// Limited is the limit met, while now is before its reset.
func (c *Claude) Limited(now time.Time) (ClaudeLimit, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.load()
	if c.kept.Until.IsZero() || !now.Before(c.kept.Until) {
		return ClaudeLimit{}, false
	}
	return ClaudeLimit{Type: c.kept.Met, Status: "rejected", ResetsAt: c.kept.Until}, true
}

// Begin writes the account's kept limits as the daemon starts, before any
// run, so status knows a headless account from its first beat: one that has
// run nothing yet is decided by its beat, never by a session that does not
// exist (docs/SPEC-FRIEND.md, the headless Claude lane).
func (c *Claude) Begin() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.load()
	if c.StateDir == "" {
		return nil
	}
	return write(filepath.Join(c.StateDir, ClaudeLimitsFile), c.kept)
}

// load reads the kept limits once, so a restarted daemon knows a limit met
// before it; c.mu is held.
func (c *Claude) load() {
	if c.loaded {
		return
	}
	c.loaded = true
	if c.StateDir != "" {
		if k, found, err := ReadClaudeLimits(c.StateDir); err == nil && found {
			c.kept = k
		}
	}
}

// OpenSession runs nothing: it names a new lane and keeps its seed, the
// friend's identity that heads each card's run.
func (c *Claude) OpenSession(_ context.Context, seed string) (string, error) {
	var raw [4]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	id := "claude-" + hex.EncodeToString(raw[:])
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.seeds == nil {
		c.seeds = map[string]string{}
	}
	c.seeds[id] = seed
	return id, nil
}

// identity is the part of a lane's seed that heads a one-shot run: who the
// friend is, up to the seed's word about later turns, which a run of its own
// does not have. A lane the daemon kept from before a restart has no seed
// here: its runs are headed by the friend's AGENTS.md alone.
func (c *Claude) identity(session string) string {
	c.mu.Lock()
	seed, ok := c.seeds[session]
	c.mu.Unlock()
	if !ok {
		if exists(filepath.Join(c.Dir, "AGENTS.md")) {
			return "Read " + filepath.Join(c.Dir, "AGENTS.md") + " first: it is who you are.\n"
		}
		return ""
	}
	if i := strings.Index(seed, "From the next turn on"); i >= 0 {
		seed = seed[:i]
	}
	return seed
}

// cardBrief is the brief a card's turn names (CardText's first step).
var cardBrief = regexp.MustCompile(`Its brief is (.+?); work as it says`)

// cardTier is the tier on a brief's RESULT line.
var cardTier = regexp.MustCompile(`(?m)^RESULT:.*\btier:\s*([a-z]+)`)

// model is the model a turn's card runs on: its brief's tier, else Model,
// else the model of a card with no tier. The card's id comes back with it.
func (c *Claude) model(text string) (model, card string) {
	model = c.Model
	if model == "" {
		model = ClaudeModels[""]
	}
	m := cardBrief.FindStringSubmatch(text)
	if m == nil {
		return model, ""
	}
	card = filepath.Base(filepath.Dir(m[1]))
	card, _, _ = strings.Cut(card, "~")
	raw, err := os.ReadFile(m[1])
	if err != nil {
		return model, card
	}
	if t := cardTier.FindSubmatch(raw); t != nil {
		if mm, ok := ClaudeModels[string(t[1])]; ok {
			return mm, card
		}
	}
	return model, card
}

// Deliver is a batch turn: one run with the waiting messages.
func (c *Claude) Deliver(ctx context.Context, text string) (int, error) {
	lt, err := c.DeliverTo(ctx, "batch", text)
	return lt.Exit, err
}

// DeliverTo is one card's run, headed by the lane's seed. At a limit met it
// runs nothing. The run is priced and its limits read, both said on the
// record and kept; a run that meets the limit tells OnLimit and returns when
// ctx ends.
func (c *Claude) DeliverTo(ctx context.Context, session, text string) (LaneTurn, error) {
	if l, limited := c.Limited(c.now()); limited {
		return LaneTurn{}, fmt.Errorf("the account is at its %s limit until %s; nothing runs before then", l.Type, stamp(l.ResetsAt))
	}
	model, card := c.model(text)
	name, args := c.program(), ClaudeArgv(model)
	if c.ConfigDir != "" {
		// the account by its config directory: env sets it for the one program, no shell
		name, args = "env", append([]string{"CLAUDE_CONFIG_DIR=" + c.ConfigDir, c.program()}, args...)
	}
	out, exit, err := c.Run(ctx, c.Dir, name, args, c.identity(session)+text)
	run := ReadClaudeStream(out)
	now := c.now()
	met, isMet := run.Met(now)
	c.keep(run, met, isMet, now)
	c.say(fmt.Sprintf("CLAUDE RUN session=%s card=%s model=%s exit=%d cost_usd=%s turns=%d tokens_in=%s tokens_out=%s cache_write=%s cache_read=%s limit=%s",
		session, dash(card), model, exit, dash(run.CostUSD), run.Turns, dash(run.TokensIn), dash(run.TokensOut), dash(run.CacheWrite), dash(run.CacheRead), SaidLimits(run.Limits)))
	if run.IsError && run.Result != "" {
		c.say("CLAUDE ERROR session=" + session + ": " + oneLine(run.Result, 300))
	}
	if isMet {
		c.say(fmt.Sprintf("CLAUDE LIMIT %s rejected until %s; the account takes no card until then", met.Type, stamp(met.ResetsAt)))
		limitErr := fmt.Errorf("the account met its %s limit until %s", met.Type, stamp(met.ResetsAt))
		if c.OnLimit != nil {
			c.OnLimit(met)
			<-ctx.Done() // the daemon stops with the limit: this card is not counted, and is handed again after
		}
		return LaneTurn{Exit: exit}, limitErr
	}
	if err == nil && exit != 0 {
		if reason, ok := ProviderRefusal(out); ok {
			return LaneTurn{Exit: exit}, ProviderRefused{Session: session, Reason: reason}
		}
	}
	return LaneTurn{Exit: exit, Rejected: PermissionRejection(run.Result)}, err
}

// SaidLimits is the limits as the record says them, each
// <window>:<status>:<utilization>:<resets_at>, comma-separated; "-" for none.
func SaidLimits(ls []ClaudeLimit) string {
	if len(ls) == 0 {
		return "-"
	}
	var out []string
	for _, l := range ls {
		out = append(out, l.said())
	}
	return strings.Join(out, ",")
}

func (c *Claude) say(line string) {
	if c.Out != nil {
		fmt.Fprintln(c.Out, line)
	}
}

// keep adds the run to the account's kept limits and writes them.
func (c *Claude) keep(run ClaudeRun, met ClaudeLimit, isMet bool, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.load()
	c.last = run
	k := &c.kept
	k.At, k.Runs = now.UTC(), k.Runs+1
	if run.CostUSD != "" {
		k.CostUSD = addUSD(k.CostUSD, run.CostUSD)
	}
	for _, l := range run.Limits {
		k.Limits = setLimit(k.Limits, l)
	}
	if isMet {
		k.Until, k.Met = met.ResetsAt, met.Type
	}
	if c.StateDir != "" {
		if err := write(filepath.Join(c.StateDir, ClaudeLimitsFile), *k); err != nil && c.Out != nil {
			fmt.Fprintln(c.Out, "CLAUDE the limits file: "+err.Error())
		}
	}
}

// addUSD is a+b as exact decimals; b alone when a is empty or unreadable.
func addUSD(a, b string) string {
	y, ok := new(big.Rat).SetString(b)
	if !ok {
		return a
	}
	x, ok := new(big.Rat).SetString(a)
	if !ok {
		x = new(big.Rat)
	}
	s := x.Add(x, y).FloatString(8)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	if s == "" {
		return "0"
	}
	return s
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.UTC().Format(time.RFC3339)
}
