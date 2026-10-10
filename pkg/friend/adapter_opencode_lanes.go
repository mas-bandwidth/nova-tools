package friend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/atomicfile"
)

// LaneHarness is a Deliverer that can open a session of the friend and
// deliver into a session it names: what a one-shot lane needs, each lane its
// own session in the same harness and directory (docs/SPEC-FRIEND.md,
// one-shot lanes; the owner, 2026-10-04: "so [she] can still be wide, it's
// just 8 [of her]"). OpenCode is one.
type LaneHarness interface {
	Deliverer
	// OpenSession starts a new session of the friend with seed as its first
	// turn, and answers the new session's id.
	OpenSession(ctx context.Context, seed string) (session string, err error)
	// DeliverTo pushes text into session as one turn and blocks until it
	// ends: its exit, and a permission the harness refused without asking,
	// read from the turn's output (empty when none). A rate limit is
	// RateLimited and out of funds OutOfFunds (ProviderLimit); a provider's
	// refusal is ProviderRefused, as Deliver answers it.
	DeliverTo(ctx context.Context, session, text string) (LaneTurn, error)
}

// LaneTurn is how one lane turn ended.
type LaneTurn struct {
	Exit     int
	Rejected string // the line of the output where the harness refused a permission, if any
	// Windows is the subscription windows' use the harness reported in the turn
	// (a Claude Code run's rate_limit_event lines, ReadRateLimitEvents); nil when
	// it reported none. The lanes are paced by it (pacing.go).
	Windows []WindowUse
	// FirstError is the first line of the turn's output that says an error
	// (HarnessFirstError), "" when none does: a run that exits 0 with no report
	// is a harness fault said with it (lanes.go, faultTurn).
	FirstError string
}

// permissionRejected is a line of a turn's output where a tool call was
// refused for want of a permission: a headless opencode run auto-rejects any
// call that would prompt (measured 2026-10-04: external_directory for a path
// through a symlink to the friend's directory), and the session says so.
var permissionRejected = regexp.MustCompile(`(?i)(\b(permission|external_directory)\b.*\b(reject\w*|denied|refused|blocked)\b|\b(reject\w*|denied|blocked)\b.*\bpermission\b)`)

// PermissionRejection is the first line of out that says a permission was
// refused, one line, at most 200 bytes; empty when none does.
func PermissionRejection(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if permissionRejected.MatchString(line) {
			return oneLine(stripANSI(line), 200)
		}
	}
	return ""
}

var ansi = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

func stripANSI(s string) string { return ansi.ReplaceAllString(s, "") }

// OpenCodeConfig is the project config file opencode reads in its directory.
const OpenCodeConfig = "opencode.json"

// AllowDirs writes into dir's opencode.json that a tool call may touch each
// of paths and everything under it without a prompt (permission
// external_directory, a pattern map of path globs to allow), merged into what
// the file holds, written only when it changes. A headless opencode run
// auto-rejects a call that would prompt, and the turn ends there (measured
// 2026-10-04: a friend's working directory reached through its symlink in the
// home directory). It answers whether it wrote.
func AllowDirs(dir string, paths []string) (bool, error) {
	path := filepath.Join(dir, OpenCodeConfig)
	cfg := map[string]any{}
	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		cfg["$schema"] = "https://opencode.ai/config.json"
	case err != nil:
		return false, err
	default:
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return false, fmt.Errorf("%s is not a JSON object, so the lanes' directories cannot be allowed in it: %v", path, err)
		}
	}
	perm, _ := cfg["permission"].(map[string]any)
	if perm == nil {
		perm = map[string]any{}
	}
	if s, ok := perm["external_directory"].(string); ok && s == "allow" {
		return false, nil // every directory is allowed already
	}
	allowed, _ := perm["external_directory"].(map[string]any)
	if allowed == nil {
		allowed = map[string]any{}
	}
	changed := false
	for _, p := range paths {
		pattern := strings.TrimRight(p, "/") + "/**"
		if allowed[pattern] != "allow" {
			allowed[pattern], changed = "allow", true
		}
	}
	if !changed {
		return false, nil
	}
	perm["external_directory"], cfg["permission"] = allowed, perm
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return false, err
	}
	return true, atomicfile.WriteFile(path, append(out, '\n'), 0o644)
}

// lanePaths is the friend's directory as every path a tool call may name it
// by: as given, its real path, and each alias (a symlink in the home
// directory), deduplicated.
func (o *OpenCode) lanePaths() []string {
	paths := []string{o.Dir}
	if real, err := filepath.EvalSymlinks(o.Dir); err == nil {
		paths = append(paths, real)
	}
	paths = append(paths, o.Allow...)
	slices.Sort(paths)
	return slices.Compact(paths)
}

// allow is AllowDirs over the friend's paths before a turn; a config that
// cannot be written is said in the record and the turn goes ahead: the run
// may still need no prompt.
func (o *OpenCode) allow() {
	if _, err := AllowDirs(o.Dir, o.lanePaths()); err != nil && o.Out != nil {
		fmt.Fprintf(o.Out, "opencode: the friend's directories are not allowed in %s: %v; a tool call there may be auto-rejected\n", filepath.Join(o.Dir, OpenCodeConfig), err)
	}
}

// opening serialises the lanes' session opens: each finds its new session by
// the listing before and after its first turn.
var opening sync.Mutex

// OpenSession runs the seed as the first turn of a new session in Dir
// (`opencode run <seed>` in Dir, no --session) and answers the session
// that appeared in the listing of Dir, the newest the listing before it did
// not hold. The lanes' directories are allowed in the project config first.
func (o *OpenCode) OpenSession(ctx context.Context, seed string) (string, error) {
	o.allow()
	opening.Lock()
	defer opening.Unlock()
	before, err := o.sessions(ctx)
	if err != nil {
		return "", err
	}
	out, exit, err := o.Run(ctx, o.Dir, o.program(), o.runVerb(seed), "")
	if o.Out != nil && out != "" {
		fmt.Fprintln(o.Out, strings.TrimRight(Head(out, OutputKept), "\n"))
	}
	if exit != 0 && err == nil {
		if limit := laneLimit("(new)", out); limit != nil {
			return "", limit // a limit or out of funds: the lanes' governor answers it, not the open's retry alone
		}
	}
	exit, err = refused("(new)", out, exit, err)
	o.turns.saw("(new)", exit, err)
	if err != nil {
		return "", err
	}
	if exit != 0 {
		return "", fmt.Errorf("opencode run (a new session in %s) exited %d", o.Dir, exit)
	}
	after, err := o.sessions(ctx)
	if err != nil {
		return "", err
	}
	best := session{}
	for _, s := range after {
		if s.Directory == o.Dir && !slices.ContainsFunc(before, func(b session) bool { return b.ID == s.ID }) && s.Updated > best.Updated {
			best = s
		}
	}
	if best.ID == "" {
		return "", fmt.Errorf("opencode run started no new session in %s that the listing shows", o.Dir)
	}
	return best.ID, nil
}

func (o *OpenCode) sessions(ctx context.Context) ([]session, error) {
	listing, exit, err := o.Run(ctx, o.Dir, o.program(), o.listVerb(), "")
	if err != nil {
		return nil, fmt.Errorf("opencode session list: %w", err)
	}
	if exit != 0 {
		if match := wallRefusalReason.FindStringSubmatch(listing); match != nil {
			if match[1] == "bad_profile" && strings.Contains(match[0], "denies nothing") {
				return nil, fmt.Errorf("opencode session list: friend wall refused reason=bad_profile: the profile denies nothing; the daemon needs a coordinator-self deny path (nova-friend run --deny-self) before a lane can open (exit %d)", exit)
			}
			return nil, fmt.Errorf("opencode session list: friend wall refused reason=%s (exit %d)", match[1], exit)
		}
		return nil, fmt.Errorf("opencode session list exited %d", exit)
	}
	rows, err := decodeSessions(listing)
	if err != nil {
		recordListing(o.Out, listing)
	}
	return rows, err
}

var wallRefusalReason = regexp.MustCompile(`(?m)^WALL REFUSED reason=([a-z_]+)\b[^\n]*`)

// DeliverTo is one card's turn in a lane's session: `opencode run --session
// <id> <text>` in Dir, its output read for a refused permission, and its
// tail for a rate limit or out of funds (ProviderLimit, whatever the exit:
// the lanes heed it only when the card has no RESULT.md) before a provider's
// refusal of the session.
//
// A lane's turn runs in the card's job directory, the one its context carries
// (WithLaneDir), so a path the model reads relative is read inside the job;
// with none it runs in Dir.
func (o *OpenCode) DeliverTo(ctx context.Context, id, text string) (LaneTurn, error) {
	o.allow()
	out, exit, err := o.Run(ctx, LaneDirOf(ctx, o.Dir), o.program(), o.runVerb("--session", id, text), "")
	if o.Out != nil && out != "" {
		fmt.Fprintln(o.Out, strings.TrimRight(Head(out, OutputKept), "\n"))
	}
	if err == nil {
		if limit := laneLimit(id, out); limit != nil {
			o.turns.saw(id, exit, limit)
			return LaneTurn{Exit: exit, Rejected: PermissionRejection(out), FirstError: HarnessFirstError(out)}, limit
		}
	}
	exit, err = refused(id, out, exit, err)
	o.turns.saw(id, exit, err)
	return LaneTurn{Exit: exit, Rejected: PermissionRejection(out), FirstError: HarnessFirstError(out)}, err
}

// RunRead is one read as a one-shot of the friend's opencode: `opencode run
// [--model <model>] <prompt>` in Dir, a session of its own that no listing is read for, so reads
// never queue behind the lanes' session opens. Its output is read as a lane turn's is.
func (o *OpenCode) RunRead(ctx context.Context, model, prompt string) (LaneTurn, error) {
	o.allow()
	args := o.runVerb()
	if model != "" {
		args = append(args, "--model", model)
	}
	out, exit, err := o.Run(ctx, o.Dir, o.program(), append(args, prompt), "")
	if o.Out != nil && out != "" {
		fmt.Fprintln(o.Out, strings.TrimRight(Head(out, OutputKept), "\n"))
	}
	if err == nil {
		if limit := ProviderLimit("(read)", out); limit != nil {
			return LaneTurn{Exit: exit, Rejected: PermissionRejection(out)}, limit
		}
	}
	exit, err = refused("(read)", out, exit, err)
	return LaneTurn{Exit: exit, Rejected: PermissionRejection(out)}, err
}

// laneLimit is a lane turn's limit: a rate limit or out of funds
// (ProviderLimit), else the harness's own limit with its reset beside it
// ("Insufficient AI Credits ... will refresh in 3 hours", ReadLimit), which
// is UsageLimited until that reset, as a Claude lane's rejected
// rate_limit_event is: the lanes stop taking until it, no guessed time.
func laneLimit(session, out string) error {
	if limit := ProviderLimit(session, out); limit != nil {
		return limit
	}
	if lim, found := ReadLimit(stripANSI(out), time.Now()); found && lim.Limited {
		return UsageLimited{Session: session, Reason: lim.Reason, Until: lim.Until}
	}
	return nil
}

// OpenCodePriced is an OpenCode friend's lanes with every run priced from
// opencode's own session record (docs/SPEC-FRIEND.md, the Claude lanes, the
// OpenCode lane): after each turn `opencode export <session>` is read, and
// the run's cost is what the session's assistant messages gained since the
// last read (each message's cost is opencode's own figure), said on the
// record as one line per run. The daemon wraps the friend's OpenCode in it;
// a bare OpenCode runs no export. A card's turn is capped by its tokens, read
// from the same record (tokencap.go).
type OpenCodePriced struct {
	*OpenCode

	// Friend is the friend's name, as a capped card's report says it.
	Friend string
	// TokenCap is the friend row's per-card token cap as the daemon last read
	// it (TokenCapOf; 0 none); nil is DefaultTokenCap.
	TokenCap func() int64
	// Tick is the clock a running turn's usage is polled on (every TokenPoll);
	// a real ticker when nil.
	Tick func(time.Duration) (<-chan time.Time, func())

	mu    sync.Mutex
	seen  map[string]float64 // each session's cost when last read
	cost  float64
	runs  int       // the runs priced so far
	until time.Time // the reset of the last run's usage limit; zero when it ran unlimited
	cards cardRuns  // each card's tokens over its finished turns
}

// Spent is the cost of every run priced so far, in US dollars.
func (p *OpenCodePriced) Spent() float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cost
}

// SpendLine is every run's cost so far and, when the last run stopped at a
// usage limit, its reset: `spend: harness=opencode runs=<n> cost_usd=<sum>
// [limited_until=<t>]`. An API friend has no five-hour or weekly window to
// read; her limit is what the run said (Spender).
func (p *OpenCodePriced) SpendLine() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.runs == 0 {
		return ""
	}
	line := fmt.Sprintf("spend: harness=opencode runs=%d cost_usd=%.4f", p.runs, p.cost)
	if !p.until.IsZero() {
		line += " limited_until=" + p.until.UTC().Format(time.RFC3339)
	}
	return line
}

// OpenSession is OpenCode's, then the first run priced.
func (p *OpenCodePriced) OpenSession(ctx context.Context, seed string) (string, error) {
	id, err := p.OpenCode.OpenSession(ctx, seed)
	if err == nil {
		p.price(ctx, id)
	}
	return id, err
}

// DeliverTo is OpenCode's under the card's token cap (deliverCapped), then the
// run priced, whatever it answered.
func (p *OpenCodePriced) DeliverTo(ctx context.Context, id, text string) (LaneTurn, error) {
	lt, err := p.deliverCapped(ctx, id, text)
	var limited UsageLimited
	p.mu.Lock()
	p.until = time.Time{}
	if errors.As(err, &limited) {
		p.until = limited.Until
	}
	p.mu.Unlock()
	p.price(ctx, id)
	return lt, err
}

// openCodeExport is the part of `opencode export <session>` a price reads.
type openCodeExport struct {
	Messages []struct {
		Info struct {
			Role string  `json:"role"`
			Cost float64 `json:"cost"`
		} `json:"info"`
	} `json:"messages"`
}

// SessionCost is the cost of a session from its export: the sum of its
// assistant messages' costs. The export may be preceded by a line of its
// own (opencode says what it exports on stderr); the JSON starts at the
// first '{'.
func SessionCost(export string) (float64, error) {
	at := strings.IndexByte(export, '{')
	if at < 0 {
		return 0, fmt.Errorf("opencode export: no JSON object in %q", oneLine(export, 120))
	}
	var e openCodeExport
	if err := json.NewDecoder(strings.NewReader(export[at:])).Decode(&e); err != nil {
		return 0, fmt.Errorf("opencode export: %v", err)
	}
	cost := 0.0
	for _, m := range e.Messages {
		if m.Info.Role == "assistant" {
			cost += m.Info.Cost
		}
	}
	return cost, nil
}

// price reads the session's record and says the run's cost on the record;
// a record that cannot be read is said there, and the turn stands.
func (p *OpenCodePriced) price(ctx context.Context, id string) {
	out, exit, err := p.Run(ctx, p.Dir, p.program(), []string{"export", id}, "")
	if err == nil && exit != 0 {
		err = fmt.Errorf("exited %d: %s", exit, oneLine(out, 200))
	}
	var now float64
	if err == nil {
		now, err = SessionCost(out)
	}
	if err != nil {
		if p.Out != nil {
			fmt.Fprintf(p.Out, "opencode: session=%s cost=- (its record was not read: %v)\n", id, err)
		}
		return
	}
	p.mu.Lock()
	if p.seen == nil {
		p.seen = map[string]float64{}
	}
	p.runs++
	run := max(now-p.seen[id], 0)
	p.seen[id] = now
	p.cost += run
	total := p.cost
	p.mu.Unlock()
	if p.Out != nil {
		fmt.Fprintf(p.Out, "opencode: session=%s cost=$%.4f total=$%.4f\n", id, run, total)
	}
}
