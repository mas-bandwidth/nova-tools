package friend

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// The harness proof (docs/SPEC-FRIEND.md, "A lane proves its harness before it
// takes"): a lane's harness runs its cheapest no-tool one-turn invocation, the
// daemon reads the terminal reason and, when the harness cannot answer, the
// daemon takes nothing until the proof passes. A harness whose config dir was
// never signed in answers every run in about a second with terminal_reason
// api_error and duration_api_ms 0 (the finding of 2026-10-11: ~35 card
// attempts burned in 6 minutes, each failure charged to its card). The proof is
// the one test the daemon runs at start and after the second instant failure,
// never a card.

// ProofHarnesses are the harnesses whose cheapest no-tool one-turn invocation
// the proof knows, each the argv after the program (ProofArgv).
var ProofHarnesses = map[string][]string{
	"claude": {"-p", "ok", "--output-format", "json", "--max-turns", "1"},
	"dsh":    {"-p", "ok"},
	"gemini": {"-p", "ok"},
}

// ProofArgv is the harness's cheapest no-tool one-turn invocation, program
// first, or nil when the harness has no such proof. Claude's json one-turn run
// is `claude -p "ok" --output-format json --max-turns 1` (docs/SPEC-FRIEND.md,
// the harness proof); dsh and gemini take their own one-turn form.
func ProofArgv(harness string) []string {
	rest, ok := ProofHarnesses[harness]
	if !ok {
		return nil
	}
	return append([]string{harness}, rest...)
}

// ProofEnv is the environment the proof runs the harness with: the lane's own
// config directory for claude (CLAUDE_CONFIG_DIR), and nothing extra for the
// others. The proof runs as the lane's account, so a dir never signed in fails
// here, not on a card.
func ProofEnv(harness, configDir string) []string {
	if harness == "claude" && configDir != "" {
		return []string{"CLAUDE_CONFIG_DIR=" + configDir}
	}
	return nil
}

// proofResult is the JSON a headless one-turn run prints when it is asked for
// json: the terminal reason and how much of the run reached the provider.
type proofResult struct {
	TerminalReason string `json:"terminal_reason"`
	DurationAPI    int64  `json:"duration_api_ms"`
}

// ParseProof reads a proof run's terminal reason from its json output, ok when
// the output carried one. A harness whose output is not json (or carries no
// terminal_reason) says ok false, and the caller falls back to the exit.
func ParseProof(out string) (reason string, ok bool) {
	var r proofResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &r); err != nil {
		return "", false
	}
	return r.TerminalReason, r.TerminalReason != ""
}

// HarnessProof is one proof run's result: whether the harness answered, the
// terminal reason it gave when it did not, the config dir it ran with and when
// and why the proof ran. The daemon status shows its Line.
type HarnessProof struct {
	OK             bool      `json:"ok"`
	Harness        string    `json:"harness"`
	TerminalReason string    `json:"terminal_reason,omitempty"`
	ConfigDir      string    `json:"config_dir,omitempty"`
	At             time.Time `json:"at"`
	Err            string    `json:"error,omitempty"`
}

// Reason is the daemon's one-line reason for a proof that failed, in the shape
// its down beat names: `harness claude cannot answer: api_error (config dir
// /home/ada/.claude)`. Empty when the proof passed.
func (p HarnessProof) Reason() string {
	if p.OK {
		return ""
	}
	why := p.TerminalReason
	if why == "" {
		why = p.Err
	}
	if why == "" {
		why = "no answer"
	}
	if p.ConfigDir != "" {
		return fmt.Sprintf("harness %s cannot answer: %s (config dir %s)", p.Harness, why, p.ConfigDir)
	}
	return fmt.Sprintf("harness %s cannot answer: %s", p.Harness, why)
}

// Line is the one line the daemon records and its status shows for a proof:
// `harness=claude ok` or `harness=claude cannot answer: api_error (config dir
// /home/ada/.claude)`, with its time.
func (p HarnessProof) Line() string {
	at := p.At.UTC().Format(time.RFC3339)
	if p.OK {
		return fmt.Sprintf("%s harness proof: harness=%s ok", at, p.Harness)
	}
	return fmt.Sprintf("%s harness proof: %s", at, p.Reason())
}

// ProveHarness runs the harness's cheapest no-tool one-turn invocation in dir
// with the lane's config dir and reads its terminal reason. It is ok only when
// the run exits zero with terminal_reason success: a run that never reached
// the provider (api_error, duration_api_ms 0) fails here, before any card. A
// harness the proof does not know is not ok with a reason saying so.
func ProveHarness(ctx context.Context, dir, harness, program, configDir string, run Exec, now time.Time) HarnessProof {
	p := HarnessProof{Harness: harness, ConfigDir: configDir, At: now}
	argv := ProofArgv(harness)
	if len(argv) == 0 {
		p.Err = "the proof knows no one-turn invocation for harness " + harness
		return p
	}
	if program != "" {
		argv[0] = program
	}
	env := ProofEnv(harness, configDir)
	name, args := argv[0], argv[1:]
	if len(env) > 0 {
		name, args = "env", append(env, argv...)
	}
	out, exit, err := run(ctx, dir, name, args, "")
	reason, seen := ParseProof(out)
	p.TerminalReason = reason
	switch {
	case err != nil:
		p.Err = err.Error()
	case exit != 0:
		p.Err = fmt.Sprintf("exit %d", exit)
	case !seen:
		p.Err = "the run printed no terminal_reason"
	case reason != "success":
		// the reason stands (api_error, max_turns)
	default:
		p.OK = true
	}
	return p
}
