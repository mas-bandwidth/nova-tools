package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Muse harness (docs/FLEET.md, "Muse"): Muse Code run headless for one card.
// The provider key arrives on stdin (--api-key-stdin): never in argv, never in
// the child's environment, never written to disk by this package.

// Usage is the usage one muse run reported, in the columns the other
// headless harnesses report (tokens per class). A class muse did not report
// stays "-", never 0.
type Usage struct {
	Values   map[string]string
	Observed bool
	Turns    int
}

// MuseResult is one finished muse run: its final text and its usage.
type MuseResult struct {
	Text  string
	Usage Usage
}

// FailureKind classes a failed muse run.
type FailureKind string

const (
	FailureExit            FailureKind = "exit"
	FailureProviderRefused FailureKind = "provider-refused"
	FailureNoResult        FailureKind = "no-result"
	FailureParse           FailureKind = "parse"
)

// MuseError is a failed muse run: its kind, the child's exit code (0 when
// the child exited cleanly but reported or omitted its result), and what it said.
type MuseError struct {
	Kind     FailureKind
	ExitCode int
	Message  string
}

func (e *MuseError) Error() string {
	return fmt.Sprintf("muse run failed (kind=%s, exit=%d): %s", e.Kind, e.ExitCode, e.Message)
}

// MuseArgv is the argv of one headless muse run, the binary first. The key
// is not in it: --api-key-stdin takes it from stdin.
func MuseArgv(bin, promptFile, model string) []string {
	return []string{bin, "exec", "--prompt-file", promptFile, "--json", "--model", model, "--api-key-stdin"}
}

type museEvent struct {
	Type        string          `json:"type"`
	PayloadType string          `json:"payload_type"`
	Result      string          `json:"result"`
	Text        string          `json:"text"`
	Message     string          `json:"message"`
	Model       string          `json:"model"`
	Usage       *museUsage      `json:"usage"`
	Payload     json.RawMessage `json:"payload"`
}

type museUsage struct {
	Input      *int64 `json:"input_tokens"`
	CacheWrite *int64 `json:"cache_creation_input_tokens"`
	CacheRead  *int64 `json:"cache_read_input_tokens"`
	Output     *int64 `json:"output_tokens"`
	Reasoning  *int64 `json:"reasoning_tokens"`
}

type musePayload struct {
	Kind     string     `json:"kind"`
	Terminal string     `json:"terminal"`
	Text     string     `json:"text"`
	Reason   string     `json:"reason"`
	Message  string     `json:"message"`
	Model    string     `json:"model"`
	Usage    *museUsage `json:"usage"`
	Event    *struct {
		Kind   string `json:"kind"`
		Reason string `json:"reason"`
	} `json:"event"`
}

func refusedProvider(text string) bool {
	t := strings.ToLower(text)
	for _, s := range []string{"401", "403", "unauthorized", "invalid api key", "invalid_api_key", "provider refused", "provider_refused", "authentication failed", "permission denied by provider"} {
		if strings.Contains(t, s) {
			return true
		}
	}
	return false
}

func usageValues(u *museUsage, model string) (map[string]string, error) {
	values := map[string]string{"provider": Muse, "model": "-"}
	if model != "" {
		values["model"] = model
	}
	for col, n := range map[string]*int64{
		"tokens_in": u.Input, "tokens_out": u.Output, "cache_write": u.CacheWrite,
		"cache_read": u.CacheRead, "reasoning": u.Reasoning,
	} {
		values[col] = "-"
		if n != nil {
			if *n < 0 {
				return nil, fmt.Errorf("muse usage column %s is negative", col)
			}
			values[col] = strconv.FormatInt(*n, 10)
		}
	}
	return values, nil
}

// ParseMuse reads muse's JSONL events: the result text (a result event, or a
// run.terminal.completed payload), the last usage reported, and a reported
// provider refusal. Non-JSON lines are skipped (muse prints human warnings
// outside the JSONL stream).
func ParseMuse(out []byte) (MuseResult, error) {
	var res MuseResult
	res.Usage.Values = map[string]string{}
	sawEvent := false
	for _, line := range bytes.Split(out, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var ev museEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		sawEvent = true
		var p musePayload
		if len(ev.Payload) > 0 {
			_ = json.Unmarshal(ev.Payload, &p)
		}
		switch {
		case ev.Type == "error" || p.Kind == "error":
			msg := firstNonEmpty(ev.Message, p.Message, p.Reason)
			kind := FailureExit
			if refusedProvider(msg) {
				kind = FailureProviderRefused
			}
			return res, &MuseError{Kind: kind, Message: msg}
		case ev.PayloadType == "run.terminal.failed" || p.Terminal == "failed":
			msg := firstNonEmpty(p.Reason, p.Message, ev.Message, "run failed")
			kind := FailureExit
			if refusedProvider(msg) {
				kind = FailureProviderRefused
			}
			return res, &MuseError{Kind: kind, Message: msg}
		case ev.PayloadType == "run.terminal.completed" || p.Terminal == "completed":
			res.Text = firstNonEmpty(p.Text, ev.Text, res.Text)
		case ev.Type == "result":
			res.Text = firstNonEmpty(ev.Result, ev.Text, res.Text)
		}
		if u := ev.Usage; u != nil {
			v, err := usageValues(u, firstNonEmpty(ev.Model, p.Model))
			if err != nil {
				return res, &MuseError{Kind: FailureParse, Message: err.Error()}
			}
			res.Usage = Usage{Values: v, Observed: true, Turns: 1}
		} else if p.Usage != nil {
			v, err := usageValues(p.Usage, firstNonEmpty(p.Model, ev.Model))
			if err != nil {
				return res, &MuseError{Kind: FailureParse, Message: err.Error()}
			}
			res.Usage = Usage{Values: v, Observed: true, Turns: 1}
		}
	}
	if !sawEvent {
		return res, &MuseError{Kind: FailureNoResult, Message: "muse printed no JSONL events"}
	}
	if res.Text == "" {
		return res, &MuseError{Kind: FailureNoResult, Message: "muse printed no result"}
	}
	return res, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// RunMuse runs one card headless under muse in workdir: it launches
// MuseArgv, feeds apiKey on stdin alone, and reads the JSONL on stdout.
// env is the child's whole environment; the key is never added to it.
func RunMuse(ctx context.Context, bin, workdir, promptFile, model, apiKey string, env []string) (MuseResult, error) {
	argv := MuseArgv(bin, promptFile, model)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = workdir
	cmd.Env = env
	cmd.Stdin = strings.NewReader(apiKey)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	res, parseErr := ParseMuse(stdout.Bytes())
	if runErr != nil {
		msg := strings.TrimSpace(stderr.String())
		if me, ok := parseErr.(*MuseError); ok && me.Message != "" {
			msg = me.Message
		}
		if msg == "" {
			msg = runErr.Error()
		}
		kind := FailureExit
		if refusedProvider(msg) || refusedProvider(stdout.String()) {
			kind = FailureProviderRefused
		}
		return res, &MuseError{Kind: kind, ExitCode: exitCode, Message: msg}
	}
	if parseErr != nil {
		if me, ok := parseErr.(*MuseError); ok {
			me.ExitCode = exitCode
			if refusedProvider(me.Message) {
				me.Kind = FailureProviderRefused
			}
		}
		return res, parseErr
	}
	return res, nil
}
