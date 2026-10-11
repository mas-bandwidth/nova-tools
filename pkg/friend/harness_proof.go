package friend

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// HarnessProof is the answer of proving a harness before any card is taken: the
// harness's cheapest no-tool turn run once and its terminal reason read. A
// daemon whose harness cannot answer must take nothing: on 2026-10-11 a lane's
// claude config directory was never signed in, every run ended terminal_reason
// api_error in about a second, and the daemon finished each card FAILED and took
// the next, ~35 attempts burned in six minutes (docs/SPEC-FRIEND.md, the harness
// proof).
type HarnessProof struct {
	Harness string    // the harness kind proved (harness.KindOf)
	OK      bool      // true when the proof turn reached terminal_reason success
	Reason  string    // the terminal reason the harness printed ("" when it printed none)
	At      time.Time // when the proof ran
	Err     error     // a proof that could not run at all; nil when it ran
}

// HarnessProofOK is the one terminal reason a harness prints when its turn
// answered.
const HarnessProofOK = "success"

// HarnessProofPrompt is the no-tool prompt the proof turn is proven with: the
// cheapest turn a harness can take.
const HarnessProofPrompt = "ok"

// terminalResult is the result JSON a one-shot harness prints at the end of a
// turn: claude's --output-format json prints terminal_reason and duration_api_ms
// among its fields.
type terminalResult struct {
	Type           string `json:"type"`
	Subtype        string `json:"subtype"`
	TerminalReason string `json:"terminal_reason"`
	DurationAPIMs  int    `json:"duration_api_ms"`
}

// TerminalReason reads a one-shot harness's result line for its terminal
// reason: the first JSON line carrying a non-empty terminal_reason, in the order
// it printed; "" when the output carries none.
func TerminalReason(out string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, "\"terminal_reason\"") {
			continue
		}
		var r terminalResult
		if json.Unmarshal([]byte(line), &r) == nil && r.TerminalReason != "" {
			return r.TerminalReason
		}
	}
	return ""
}

// HarnessProofArgv is the argv the proof turn runs for harness with configDir as
// its login: the `env` argv (an env assignment first, as the adapters pass
// CLAUDE_CONFIG_DIR) for claude, dsh and gemini, the one-turn no-tool call each
// takes, empty for a harness the proof does not know (harness.Headless).
func HarnessProofArgv(harness, configDir string) ([]string, error) {
	switch harness {
	case "claude":
		return []string{"CLAUDE_CONFIG_DIR=" + configDir, "claude", "-p", HarnessProofPrompt, "--output-format", "json", "--max-turns", "1"}, nil
	case "dsh":
		return []string{"dsh", "-p", HarnessProofPrompt, "--output-format", "json"}, nil
	case "gemini":
		return []string{"gemini", "-p", HarnessProofPrompt, "--output-format", "json"}, nil
	}
	return nil, fmt.Errorf("the harness proof knows no one-turn call for %q; it proves %s", harness, strings.Join([]string{"claude", "dsh", "gemini"}, ", "))
}

// ProveHarness runs the harness's cheapest no-tool turn through run and reads
// its terminal reason: OK only when the turn reached terminal_reason success.
// A turn that could not run, exited nonzero, or printed another reason is not
// OK, with Reason naming what it printed (docs/SPEC-FRIEND.md, the harness
// proof).
func ProveHarness(ctx context.Context, harness, configDir string, run Exec, now time.Time) HarnessProof {
	p := HarnessProof{Harness: harness, At: now}
	argv, err := HarnessProofArgv(harness, configDir)
	if err != nil {
		p.Err = err
		return p
	}
	out, exit, err := run(ctx, "", "env", argv, "")
	if err != nil {
		p.Err = err
		return p
	}
	p.Reason = TerminalReason(out)
	if exit != 0 {
		if p.Reason == "" {
			p.Reason = fmt.Sprintf("exit %d", exit)
		}
		return p
	}
	p.OK = p.Reason == HarnessProofOK
	return p
}

// ProofReasonLine is the down-beat reason a failed proof beats with: the
// harness that cannot answer, its terminal reason (or "no terminal reason") and
// the config directory it was proven with, so a reader knows which login is
// missing (docs/SPEC-FRIEND.md, the harness proof).
func ProofReasonLine(p HarnessProof, configDir string) string {
	reason := p.Reason
	if reason == "" {
		reason = "no terminal reason"
	}
	if p.Err != nil {
		reason = p.Err.Error()
	}
	return fmt.Sprintf("harness %s cannot answer: %s (config dir %s)", p.Harness, reason, configDir)
}
