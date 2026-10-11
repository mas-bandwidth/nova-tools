package friend

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// A harness proof is the harness's cheapest no-tool turn, run at the daemon's
// start and after the second instant failure, before any take: a daemon whose
// harness cannot answer (its config directory never signed in) must take
// nothing, where on 2026-10-11 the coordinator's lane ended every card
// terminal_reason api_error in about a second and the daemon finished each
// FAILED and took the next, ~35 attempts burned in 6 minutes
// (docs/SPEC-FRIEND.md, "A lane proves its harness").

// ClaudeProofArgs is claude's cheapest no-tool turn: one prompt, JSON out, one
// turn, no tools. The prompt travels as an argument, never parsed.
func ClaudeProofArgs() []string {
	return []string{"-p", "ok", "--output-format", "json", "--max-turns", "1"}
}

// terminalReason reads the terminal_reason of a claude --output-format json
// answer: the last such line's, "" when the answer names none. The reason that
// the harness cannot answer (api_error: its config was never signed in) is the
// failure the proof catches; duration_api_ms is read beside it and never used.
func terminalReason(out string) string {
	var reason string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] != '{' {
			continue
		}
		var r struct {
			TerminalReason string `json:"terminal_reason"`
		}
		if json.Unmarshal([]byte(line), &r) == nil && r.TerminalReason != "" {
			reason = r.TerminalReason
		}
	}
	return reason
}

// ClaudeProofWhy runs claude's cheapest no-tool turn through run (the daemon's
// own exec, with CLAUDE_CONFIG_DIR=configDir) and answers why the harness
// cannot answer: "" when it can, else "harness claude cannot answer:
// <terminal_reason> (config dir <dir>)". A run that answers nothing, exits
// nonzero, or names a terminal reason that is not a normal end fails.
func ClaudeProofWhy(ctx context.Context, run Exec, dir, configDir, program string) string {
	args := append([]string{"CLAUDE_CONFIG_DIR=" + configDir, program}, ClaudeProofArgs()...)
	out, exit, err := run(ctx, dir, "env", args, "")
	if err != nil {
		return fmt.Sprintf("harness claude cannot answer: %v (config dir %s)", err, configDir)
	}
	if reason := terminalReason(out); reason != "" && reason != "end_turn" {
		return fmt.Sprintf("harness claude cannot answer: %s (config dir %s)", reason, configDir)
	}
	if exit != 0 {
		return fmt.Sprintf("harness claude cannot answer: exit %d (config dir %s)", exit, configDir)
	}
	return ""
}
