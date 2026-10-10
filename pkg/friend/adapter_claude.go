package friend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ClaudeTrim is the trimmed call of a headless Claude Code run (the owner's
// finding of 2026-10-04: no MCP servers, no slash commands, no browser, six
// tools, which cut the context of each call from about 50k tokens to 12.7k).
// --tools takes every argument after it, so it is last, after the prompt.
var ClaudeTrim = []string{"--strict-mcp-config", "--disable-slash-commands", "--no-chrome", "--tools", "Bash", "Read", "Write", "Edit", "Grep", "Glob"}

func (c *Claude) now() time.Time {
	if c.Now == nil {
		return time.Now()
	}
	return c.Now()
}

// Spent is the cost of every run so far, in US dollars, and the last usage
// a run measured (zero: none yet): what the daemon says on its record.
func (c *Claude) Spent() (cost float64, usage Usage) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cost, c.usage
}

// Spender is a lane harness that prices its runs (docs/SPEC-FRIEND.md, the
// Claude lanes, on the beat): SpendLine is what its runs have cost so far and
// the limit it last read, one line the daemon says on its beat when it
// changed; empty before any run.
type Spender interface {
	SpendLine() string
}

// SpendLine is every run's cost so far and the five-hour and weekly windows
// the last run read: `spend: harness=claude runs=<n> cost_usd=<sum>
// five_hour=<f> seven_day=<f> five_hour_resets=<t> seven_day_resets=<t>`.
func (c *Claude) SpendLine() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.runs == 0 {
		return ""
	}
	line := fmt.Sprintf("spend: harness=claude runs=%d cost_usd=%.4f", c.runs, c.cost)
	if !c.usage.At.IsZero() {
		line += fmt.Sprintf(" five_hour=%.2f seven_day=%.2f five_hour_resets=%s seven_day_resets=%s", c.usage.FiveHour, c.usage.SevenDay, resetText(c.usage.FiveHourResets), resetText(c.usage.SevenDayResets))
	}
	return line
}

// claudeResult is the stream-json result line of a run.
type claudeResult struct {
	Type      string  `json:"type"`
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

// account prices the run whose stream-json is out (docs/SPEC-FRIEND.md,
// one-shot lanes, the Claude lanes), reads its rate_limit_event (the
// five-hour and weekly utilization, resetsAt) and says both on the record,
// one line per run. A rejected event is UsageLimited until its reset, so the
// lanes stop taking until then (LaneGovernor.PauseUntil) with no script and
// no guessed time; nil otherwise.
func (c *Claude) account(id, out string) error {
	cost := runCost(out)
	lim, found := ReadLimit(out, c.now())
	c.mu.Lock()
	c.runs++
	c.cost += cost
	total := c.cost
	if found && !lim.Usage.At.IsZero() {
		c.usage = lim.Usage
	}
	usage := c.usage
	c.mu.Unlock()
	if c.Out != nil {
		line := fmt.Sprintf("claude: run=%s cost=$%.4f total=$%.4f", id, cost, total)
		if !usage.At.IsZero() {
			line += fmt.Sprintf(" five_hour=%.2f seven_day=%.2f five_hour_resets=%s seven_day_resets=%s", usage.FiveHour, usage.SevenDay, resetText(usage.FiveHourResets), resetText(usage.SevenDayResets))
		}
		fmt.Fprintln(c.Out, line)
	}
	if found && lim.Limited {
		return UsageLimited{Session: id, Reason: lim.Reason, Until: lim.Until}
	}
	return nil
}

func resetText(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.UTC().Format(time.RFC3339)
}

// RunRead is one read of the friend's reader row (ReadHarness;
// docs/SPEC-FRIEND.md, the reader row): a card run's call with the prompt
// for the brief and the model of the read's tier ("" is the account's own)
// before the trim, as her own account, priced and its limit read as a card's
// run is.
func (c *Claude) RunRead(ctx context.Context, model, prompt string) (LaneTurn, error) {
	if why := c.Refusal(); why != "" {
		return LaneTurn{}, errors.New(why)
	}
	args := []string{"CLAUDE_CONFIG_DIR=" + c.configDir(), c.program(), "-p", prompt, "--output-format", "stream-json", "--verbose"}
	if model != "" {
		args = append(args, "--model", model)
	}
	out, exit, err := c.Run(ctx, c.Dir, "env", append(args, ClaudeTrim...), "")
	if c.Out != nil && out != "" {
		fmt.Fprintln(c.Out, strings.TrimRight(Head(out, OutputKept), "\n"))
	}
	if err == nil {
		if limited := c.account("(read)", out); limited != nil {
			return LaneTurn{Exit: exit}, limited
		}
	}
	exit, err = refused("(read)", out, exit, err)
	return LaneTurn{Exit: exit, Rejected: PermissionRejection(out)}, err
}
