package friend

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// ClaudeTrim is the trimmed call of a headless Claude Code lane
// (docs/SPEC-FRIEND.md, claude one-shot lanes): no MCP servers beyond the
// run's own, no slash commands, no browser, and the six tools a card needs.
// Measured 2026-10-04: it cut the per-call context from about 50k tokens to
// 12.7k. --tools is variadic, so the prompt never follows it: it goes in on
// stdin.
var ClaudeTrim = []string{"--strict-mcp-config", "--disable-slash-commands", "--no-chrome", "--tools", "Bash", "Read", "Write", "Edit", "Grep", "Glob"}

// Claude is a friend on a headless Claude Code account: every lane a session
// of `claude -p`, each turn one run that prints stream-json, read for its
// session, its cost and its rate_limit_events. It is a LaneHarness.
type Claude struct {
	Dir, Session string
	Run          Exec
	Program      string    // "claude" when empty
	Out          io.Writer // where each run's one-line account goes, when set: the daemon's record
	Spend        *Spend    // what the runs cost and the limits they read; nil keeps none
	Now          func() time.Time
}

func (c *Claude) program() string {
	if c.Program == "" {
		return "claude"
	}
	return c.Program
}

func (c *Claude) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// argv is one run: print mode, stream-json (which --verbose requires), the
// trimmed call, and --resume when session names one.
func (c *Claude) argv(session string) []string {
	args := []string{"-p", "--output-format", "stream-json", "--verbose"}
	if session != "" {
		args = append(args, "--resume", session)
	}
	return append(args, ClaudeTrim...)
}

// ClaudeRun is what one run's stream-json says.
type ClaudeRun struct {
	Session  string
	CostUSD  float64
	HasCost  bool
	IsError  bool
	Result   string
	Limit    Limit
	HasLimit bool
}

type claudeStreamLine struct {
	Type      string   `json:"type"`
	Subtype   string   `json:"subtype"`
	SessionID string   `json:"session_id"`
	IsError   bool     `json:"is_error"`
	Result    string   `json:"result"`
	Cost      *float64 `json:"total_cost_usd"`
}

// ParseClaudeRun reads a run's stream-json at now: the session from its init
// (else its result), the cost and the result from its result event, and the
// limit its rate_limit_events say, each window measured by the event that
// names it (MergeLimit). Lines that are not JSON are skipped: stderr follows
// stdout on a failed run.
func ParseClaudeRun(out string, now time.Time) ClaudeRun {
	var run ClaudeRun
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 0, 64<<10), 16<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var ev claudeStreamLine
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "system":
			if ev.Subtype == "init" && ev.SessionID != "" {
				run.Session = ev.SessionID
			}
		case "result":
			if run.Session == "" {
				run.Session = ev.SessionID
			}
			run.IsError, run.Result = ev.IsError, ev.Result
			if ev.Cost != nil {
				run.CostUSD, run.HasCost = *ev.Cost, true
			}
		case "rate_limit_event":
			var rl rateLimitEvent
			if json.Unmarshal([]byte(line), &rl) == nil {
				run.Limit, run.HasLimit = MergeLimit(run.Limit, claudeLimit(rl, now), run.HasLimit), true
			}
		}
	}
	return run
}

// MergeLimit is the limit of two rate_limit_events of one run: Claude Code
// prints one per window, so each window's measure is kept (the later event's
// when both name it), a rejection anywhere is a limit, and the limit lasts to
// the latest reset of a window that rejected.
func MergeLimit(a, b Limit, haveA bool) Limit {
	if !haveA {
		return b
	}
	m := a
	m.Usage.At = b.Usage.At
	if b.Usage.FiveHour > 0 || !b.Usage.FiveHourResets.IsZero() {
		m.Usage.FiveHour, m.Usage.FiveHourResets = b.Usage.FiveHour, b.Usage.FiveHourResets
	}
	if b.Usage.SevenDay > 0 || !b.Usage.SevenDayResets.IsZero() {
		m.Usage.SevenDay, m.Usage.SevenDayResets = b.Usage.SevenDay, b.Usage.SevenDayResets
	}
	m.Overage = a.Overage || b.Overage
	switch {
	case a.Limited && b.Limited:
		if b.Until.After(a.Until) {
			m.Until = b.Until
		}
		m.Reason = a.Reason
	case b.Limited:
		m.Limited, m.Until, m.Reason = true, b.Until, b.Reason
	case !a.Limited && b.Until.After(a.Until):
		m.Until = b.Until
	}
	return m
}

// run is one claude run of lane work: the prompt on stdin, the stream read,
// priced and limited. A rejected window is RateLimited until its reset.
func (c *Claude) run(ctx context.Context, session, text string) (ClaudeRun, LaneTurn, error) {
	exe := c.Run
	if exe == nil {
		exe = RealExec
	}
	out, exit, err := exe(ctx, c.Dir, c.program(), c.argv(session), text)
	now := c.now()
	run := ParseClaudeRun(out, now)
	if run.Session == "" {
		run.Session = session
	}
	if c.Spend != nil {
		c.Spend.Add(run, now)
	}
	if c.Out != nil {
		fmt.Fprintf(c.Out, "claude run session=%s exit=%d cost_usd=%s%s\n", dash(run.Session), exit, costText(run), limitText(run))
	}
	turn := LaneTurn{Exit: exit, Rejected: PermissionRejection(out)}
	if err != nil {
		return run, turn, err
	}
	if run.HasLimit && run.Limit.Limited {
		return run, turn, RateLimited{Session: run.Session, Reason: run.Limit.Reason, Until: run.Limit.Until}
	}
	if limit := ProviderLimit(dash(run.Session), tail(out)); limit != nil && exit != 0 {
		return run, turn, limit
	}
	turn.Exit, err = refused(dash(run.Session), out, exit, nil)
	return run, turn, err
}

// tail is the last LimitTail bytes of out, where a harness says its limit.
func tail(out string) string {
	if len(out) > LimitTail {
		return out[len(out)-LimitTail:]
	}
	return out
}

func costText(r ClaudeRun) string {
	if !r.HasCost {
		return "-"
	}
	return fmt.Sprintf("%.4f", r.CostUSD)
}

func limitText(r ClaudeRun) string {
	if !r.HasLimit {
		return ""
	}
	u := r.Limit.Usage
	s := fmt.Sprintf(" five_hour=%.2f seven_day=%.2f", u.FiveHour, u.SevenDay)
	if r.Limit.Limited {
		s += " limited_until=" + r.Limit.Until.UTC().Format(time.RFC3339)
	}
	return s
}

// OpenSession runs the seed as the first turn of a new session in Dir and
// answers the session id its stream's init names.
func (c *Claude) OpenSession(ctx context.Context, seed string) (string, error) {
	run, turn, err := c.run(ctx, "", seed)
	if err != nil {
		return "", err
	}
	if turn.Exit != 0 {
		return "", fmt.Errorf("claude -p (a new session in %s) exited %d: %s", c.Dir, turn.Exit, oneLine(run.Result, 200))
	}
	if run.Session == "" {
		return "", fmt.Errorf("claude -p started no session in %s: its stream named none", c.Dir)
	}
	return run.Session, nil
}

// DeliverTo is one card's turn in a lane's session: `claude -p --resume <id>`
// with the trimmed call, the text on stdin.
func (c *Claude) DeliverTo(ctx context.Context, id, text string) (LaneTurn, error) {
	_, turn, err := c.run(ctx, id, text)
	return turn, err
}

// Deliver is one batch turn: into Session when one is named, else a run of
// its own, which a session check or a push proof is answered by all the same.
func (c *Claude) Deliver(ctx context.Context, text string) (int, error) {
	_, turn, err := c.run(ctx, c.Session, text)
	return turn.Exit, err
}

// Spend is what a headless harness's runs cost and the limits they read, kept
// across the lanes' concurrent runs; the daemon says it on its beat.
type Spend struct {
	mu       sync.Mutex
	snap     SpendSnapshot
	sessions map[string]float64 // each session's total as its record last said it
}

// SpendSnapshot is Spend at one moment: runs priced, their summed cost in USD
// (a run that printed no cost adds none), the last measured usage, and the
// limit the last run read.
type SpendSnapshot struct {
	Runs         int       `json:"runs"`
	CostUSD      float64   `json:"cost_usd"`
	Usage        Usage     `json:"usage"`
	Limited      bool      `json:"limited,omitempty"`
	LimitedUntil time.Time `json:"limited_until,omitempty"`
	At           time.Time `json:"at"`
}

// Add counts one run at now.
func (s *Spend) Add(r ClaudeRun, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snap.Runs++
	s.snap.CostUSD += r.CostUSD
	s.snap.At = now
	if r.HasLimit {
		s.snap.Usage = r.Limit.Usage
		s.snap.Limited, s.snap.LimitedUntil = r.Limit.Limited, r.Limit.Until
		if !r.Limit.Limited {
			s.snap.LimitedUntil = time.Time{}
		}
	}
}

// AddSession counts the cost of a session whose own record says its total so
// far (opencode export): what the total grew by since the last said, so a
// turn is priced once however often the record is read. A total below the
// last said is a session that started over and counts whole.
func (s *Spend) AddSession(id string, total float64, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions == nil {
		s.sessions = map[string]float64{}
	}
	delta := total - s.sessions[id]
	if delta < 0 {
		delta = total
	}
	s.sessions[id] = total
	s.snap.Runs++
	s.snap.CostUSD += delta
	s.snap.At = now
}

// Snapshot is the counts so far.
func (s *Spend) Snapshot() SpendSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snap
}

// Line is the snapshot as the one line the beat's record carries.
func (n SpendSnapshot) Line() string {
	line := fmt.Sprintf("spend runs=%d cost_usd=%.4f five_hour=%.2f seven_day=%.2f", n.Runs, n.CostUSD, n.Usage.FiveHour, n.Usage.SevenDay)
	if !n.Usage.FiveHourResets.IsZero() {
		line += " five_hour_resets=" + n.Usage.FiveHourResets.UTC().Format(time.RFC3339)
	}
	if !n.Usage.SevenDayResets.IsZero() {
		line += " seven_day_resets=" + n.Usage.SevenDayResets.UTC().Format(time.RFC3339)
	}
	if n.Limited {
		line += " limited_until=" + n.LimitedUntil.UTC().Format(time.RFC3339)
	}
	return line
}
