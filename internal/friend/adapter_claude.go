package friend

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// ClaudeTrim is the trimmed call of a headless Claude Code run (the owner's
// finding of 2026-10-04: no MCP servers, no slash commands, no browser, six
// tools, which cut the context of each call from about 50k tokens to 12.7k).
// --tools takes every argument after it, so it is last.
var ClaudeTrim = []string{"--strict-mcp-config", "--disable-slash-commands", "--no-chrome", "--tools", "Bash", "Read", "Write", "Edit", "Grep", "Glob"}

// Claude is a headless Claude Code account as a lane harness (LaneHarness;
// docs/SPEC-FRIEND.md, one-shot lanes, the Claude lanes): `claude -p` with
// ClaudeTrim and --output-format stream-json --verbose, in the friend's own
// config directory. A lane's session is named by its first run
// (--session-id <uuid>), each card is a --resume turn in it. Every run's
// stream-json is read for its cost (the result's total_cost_usd) and its
// rate_limit_event (the five-hour and weekly utilization, resetsAt): a
// rejected one is UsageLimited until its reset, so the lanes stop taking
// until then with no script and no guessed time.
type Claude struct {
	Dir, Session string
	ConfigDir    string // the friend's CLAUDE_CONFIG_DIR; "" is the environment's
	Run          Exec
	Program      string           // "claude" when empty
	Out          io.Writer        // the daemon's record: one cost and limits line per run
	Now          func() time.Time // time.Now when nil

	mu    sync.Mutex
	cost  float64
	usage Usage
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

// Spent is the cost of every run so far, in US dollars, and the last usage
// a run measured (zero: none yet): what the daemon reports on its record.
func (c *Claude) Spent() (cost float64, usage Usage) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cost, c.usage
}

// claudeResult is the stream-json result line of a run.
type claudeResult struct {
	Type      string  `json:"type"`
	IsError   bool    `json:"is_error"`
	TotalCost float64 `json:"total_cost_usd"`
}

// runCost is the cost of the run whose stream-json is out: its result line's
// total_cost_usd; 0 when it printed none.
func runCost(out string) float64 {
	cost := 0.0
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, `"result"`) {
			continue
		}
		var r claudeResult
		if json.Unmarshal([]byte(line), &r) == nil && r.Type == "result" {
			cost = r.TotalCost
		}
	}
	return cost
}

// argv is the run: the program (through env when the friend has her own
// config directory, so no shell and no inherited account), -p, the model
// when one is named (before the trim: --tools takes every argument after
// it), the trim, the session flag, and the text.
func (c *Claude) argv(session flagPair, model, text string) (name string, args []string) {
	var run []string
	if c.ConfigDir != "" {
		name, run = "env", []string{"CLAUDE_CONFIG_DIR=" + c.ConfigDir, c.program()}
	} else {
		name = c.program()
	}
	run = append(run, "-p", "--output-format", "stream-json", "--verbose")
	if model != "" {
		run = append(run, "--model", model)
	}
	run = append(run, ClaudeTrim...)
	if session.flag != "" {
		run = append(run, session.flag, session.id)
	} else {
		run = append(run, "--continue")
	}
	return name, append(run, text)
}

type flagPair struct{ flag, id string }

// turn runs one `claude -p`, prices it, reads its limit and says both on the
// record. A limit that stops the lanes is UsageLimited, whatever the exit.
func (c *Claude) turn(ctx context.Context, id string, session flagPair, model, text string) (LaneTurn, error) {
	name, args := c.argv(session, model, text)
	out, exit, err := c.Run(ctx, c.Dir, name, args, "")
	if err != nil {
		return LaneTurn{Exit: exit}, err
	}
	now := c.now()
	cost := runCost(out)
	lim, found := ReadLimit(out, now)
	c.mu.Lock()
	c.cost += cost
	total := c.cost
	if found && !lim.Usage.At.IsZero() {
		c.usage = lim.Usage
	}
	usage := c.usage
	c.mu.Unlock()
	if c.Out != nil {
		line := fmt.Sprintf("claude: cost=$%.4f total=$%.4f", cost, total)
		if !usage.At.IsZero() {
			line += fmt.Sprintf(" five_hour=%.2f seven_day=%.2f five_hour_resets=%s seven_day_resets=%s", usage.FiveHour, usage.SevenDay, resetText(usage.FiveHourResets), resetText(usage.SevenDayResets))
		}
		fmt.Fprintln(c.Out, line)
	}
	if found && lim.Limited {
		return LaneTurn{Exit: exit, Rejected: PermissionRejection(out)}, UsageLimited{Session: id, Reason: lim.Reason, Until: lim.Until}
	}
	exit, err = refused(id, out, exit, err)
	return LaneTurn{Exit: exit, Rejected: PermissionRejection(out)}, err
}

func resetText(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.UTC().Format(time.RFC3339)
}

// OpenSession runs the seed as the first turn of a new session, named by a
// fresh uuid (--session-id), and answers it.
func (c *Claude) OpenSession(ctx context.Context, seed string) (string, error) {
	id, err := newUUID()
	if err != nil {
		return "", err
	}
	lt, err := c.turn(ctx, id, flagPair{"--session-id", id}, "", seed)
	if err != nil {
		return "", err
	}
	if lt.Exit != 0 {
		return "", fmt.Errorf("claude -p (a new session in %s) exited %d", c.Dir, lt.Exit)
	}
	return id, nil
}

// DeliverTo is one card's turn in a lane's session: `claude -p --resume <id>`.
func (c *Claude) DeliverTo(ctx context.Context, id, text string) (LaneTurn, error) {
	return c.turn(ctx, id, flagPair{"--resume", id}, "", text)
}

// RunRead is one read of the friend's reader row (ReadHarness;
// docs/SPEC-FRIEND.md, the reader row): a session of its own named by a
// fresh uuid, the prompt its only turn, on model ("" is the account's own),
// priced and its limit read as a card's turn is.
func (c *Claude) RunRead(ctx context.Context, model, prompt string) (LaneTurn, error) {
	id, err := newUUID()
	if err != nil {
		return LaneTurn{}, err
	}
	return c.turn(ctx, id, flagPair{"--session-id", id}, model, prompt)
}

// Deliver is the batch turn: into the named session, else the newest of the
// directory (--continue).
func (c *Claude) Deliver(ctx context.Context, text string) (int, error) {
	pair := flagPair{}
	if c.Session != "" {
		pair = flagPair{"--resume", c.Session}
	}
	lt, err := c.turn(ctx, c.Session, pair, "", text)
	return lt.Exit, err
}

func newUUID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

// Alive: the runner, claude, which every turn and every lane starts afresh.
func (c *Claude) Alive(context.Context) Liveness { return runnerAlive(exec.LookPath, c.program()) }
