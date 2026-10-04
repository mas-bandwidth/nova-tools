package swarm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
	"github.com/mas-bandwidth/nova-tools/internal/harness"
)

// THE HEADLESS HARNESSES (docs/SPEC-SWARM.md, the headless harnesses). The heavy tier
// runs its cards through the subscription logins of one machine: `claude`, `codex` and
// `grok`, each a one-shot child that takes the card as its prompt, runs under the same
// wall and in the same job directory as an opencode child, and prints its own usage in
// its machine output when it ends. There is no session database to sample: the usage is
// read once, from the capture, when the child is gone, so the deadline is the live stop
// and the budgets are asked of the final read.
//
// LOGIC APART FROM TRANSPORT. Everything in this file is a pure function of its arguments:
// the argv of a launch, the usage and the failure read out of a capture, the login read
// out of a status verb's output. The runner (cmd/nova-swarm native.go) starts the
// child and reads the files; nothing here opens one.

// HeadlessArgv is the argv of one headless harness launch: the binary, its one-shot form,
// the model, machine output, the permission mode a walled child runs under -- the wall
// is the boundary, as it is for an opencode child whose own fence allows every tool
// inside it (fence.go) -- and the prompt last, after `--` where the program takes one.
// An unknown kind is refused, never guessed.
func HeadlessArgv(kind, bin, model, prompt string) ([]string, error) {
	switch kind {
	case harness.Claude:
		return []string{bin, "-p", "--model", model, "--output-format", "json", "--permission-mode", "bypassPermissions", "--", prompt}, nil
	case harness.Codex:
		// --ephemeral: no session rollout under CODEX_HOME; the usage is the final event's
		return []string{bin, "exec", "--skip-git-repo-check", "--json", "--ephemeral", "--dangerously-bypass-approvals-and-sandbox", "--model", model, "--", prompt}, nil
	case harness.Grok:
		// --single=<prompt>: the `=` form, so a prompt beginning with a dash is a prompt
		return []string{bin, "--output-format", "json", "--permission-mode", "bypassPermissions", "--model", model, "--single=" + prompt}, nil
	}
	return nil, fmt.Errorf("%q is not a headless harness; want one of %s", kind, strings.Join(harness.Headless, ", "))
}

// HeadlessHome is where a headless harness keeps its login and settings on the bench,
// and how the child is pointed at it: the bench's own `~/.<kind>`, which the wall must
// grant as a write (the harness writes its sessions there), handed by name in Env
// (claude: CLAUDE_CONFIG_DIR, codex: CODEX_HOME) or, for a program that derives it from
// HOME alone (grok), by a link of that name under the child's data home (Link).
type HeadlessHome struct {
	Dir  string   // the harness's own directory on the bench
	Env  []string // NAME=value entries the child carries, nil when the harness reads none
	Link string   // the name under the data home linked to Dir, "" when none
}

// HeadlessHomeOf is HeadlessHome for one harness under the bench's home directory.
func HeadlessHomeOf(kind, benchHome string) HeadlessHome {
	dir := filepath.Join(benchHome, "."+kind)
	switch kind {
	case harness.Claude:
		return HeadlessHome{Dir: dir, Env: []string{"CLAUDE_CONFIG_DIR=" + dir}}
	case harness.Codex:
		return HeadlessHome{Dir: dir, Env: []string{"CODEX_HOME=" + dir}}
	default:
		return HeadlessHome{Dir: dir, Link: "." + kind}
	}
}

// HeadlessLoginArgv is the verb that says whether a headless harness is logged in,
// read by HeadlessLogin; nil for a kind that is not headless.
func HeadlessLoginArgv(kind string) []string {
	switch kind {
	case harness.Claude:
		return []string{"auth", "status"}
	case harness.Codex:
		return []string{"login", "status"}
	case harness.Grok:
		return []string{"models"}
	}
	return nil
}

// HeadlessLogin reads a login verb's output: whether the harness is logged in, and the
// line it said. claude prints JSON with `loggedIn`; codex exits 0 saying `Logged in ...`
// and 1 saying `Not logged in`; grok's model list opens with `You are logged in ...` or
// `You are not authenticated.`.
func HeadlessLogin(kind string, out []byte, rc int) (loggedIn bool, said string) {
	text := strings.TrimSpace(string(out))
	switch kind {
	case harness.Claude:
		var v struct {
			LoggedIn   bool   `json:"loggedIn"`
			AuthMethod string `json:"authMethod"`
		}
		if err := json.Unmarshal(out, &v); err != nil {
			return false, strings.TrimSpace(firstLine([]byte(text)))
		}
		return v.LoggedIn, "loggedIn=" + strconv.FormatBool(v.LoggedIn) + " authMethod=" + v.AuthMethod
	case harness.Codex:
		return rc == 0 && strings.HasPrefix(text, "Logged in"), strings.TrimSpace(firstLine([]byte(text)))
	case harness.Grok:
		return strings.HasPrefix(text, "You are logged in"), strings.TrimSpace(firstLine([]byte(text)))
	}
	return false, strings.TrimSpace(firstLine([]byte(text)))
}

// HeadlessUsage is the usage a headless child printed, read from its capture once it has
// gone: claude's one JSON result (its `usage` and `total_cost_usd`), grok's (the same
// names), codex's last `turn.completed` event of its JSONL (no cost: the route's price
// sheet prices it). A capture with no such record reports nothing (Observed false), an
// absence; one whose record will not parse is an error, a read that failed.
//
// The Values are the usage row's columns (TokenColumns, cost, provider, model): a class
// the harness did not report stays a dash, never 0 (rule 12).
func HeadlessUsage(kind string, out []byte) (ProviderUsage, error) {
	switch kind {
	case harness.Claude, harness.Grok:
		return resultUsage(kind, out)
	case harness.Codex:
		return codexUsage(out)
	}
	return ProviderUsage{}, fmt.Errorf("%q is not a headless harness", kind)
}

// resultObject is the final result claude and grok print: the token counts under
// `usage`, the cost, the models it ran, and the error flags.
type resultObject struct {
	Usage *struct {
		Input      *int64 `json:"input_tokens"`
		CacheWrite *int64 `json:"cache_creation_input_tokens"`
		CacheRead  *int64 `json:"cache_read_input_tokens"`
		Output     *int64 `json:"output_tokens"`
		Reasoning  *int64 `json:"reasoning_tokens"`
	} `json:"usage"`
	Cost       *json.Number    `json:"total_cost_usd"`
	ModelUsage map[string]any  `json:"modelUsage"`
	NumTurns   int             `json:"num_turns"`
	IsError    bool            `json:"is_error"`
	Result     string          `json:"result"`
	Type       string          `json:"type"`
	Message    string          `json:"message"`
	StopReason string          `json:"stopReason"`
	Raw        json.RawMessage `json:"-"`
}

// resultUsage reads the last JSON object of the capture that carries a `usage`.
func resultUsage(kind string, out []byte) (ProviderUsage, error) {
	obj, found, err := lastResult(out)
	if err != nil {
		return ProviderUsage{}, err
	}
	if !found || obj.Usage == nil {
		return ProviderUsage{Values: map[string]string{}}, nil
	}
	values := map[string]string{}
	for col, n := range map[string]*int64{
		"tokens_in": obj.Usage.Input, "tokens_out": obj.Usage.Output, "cache_write": obj.Usage.CacheWrite,
		"cache_read": obj.Usage.CacheRead, "reasoning": obj.Usage.Reasoning,
	} {
		values[col] = Dash
		if n != nil {
			if *n < 0 {
				return ProviderUsage{}, fmt.Errorf("%s usage column %s is negative", kind, col)
			}
			values[col] = strconv.FormatInt(*n, 10)
		}
	}
	if obj.Cost != nil {
		r, ok := new(big.Rat).SetString(obj.Cost.String())
		if !ok || r.Sign() < 0 {
			return ProviderUsage{}, fmt.Errorf("%s total_cost_usd %s is not a nonnegative number", kind, obj.Cost.String())
		}
		values["cost"] = cardcost.Text(r)
		values["usd"] = values["cost"] // the row's column, the spend's word: one figure
	}
	model := Dash
	for m := range obj.ModelUsage {
		model = m // one model per launch; a second is the same route's
	}
	values["provider"], values["model"], values["repo"] = kind, model, Dash
	turns := obj.NumTurns
	if turns <= 0 {
		turns = 1
	}
	return ProviderUsage{Values: values, Observed: true, Turns: turns}, nil
}

// lastResult decodes the last JSON object in the capture that begins a line, searching
// from the end: the result is the last thing the harness prints, and what came before it
// (a warning, a tool's own output) is not JSON. found is false when no line begins an
// object; an object that begins a line and will not decode is the error.
func lastResult(out []byte) (obj resultObject, found bool, err error) {
	for off := len(out); off > 0; {
		i := bytes.LastIndex(out[:off], []byte("\n{"))
		start := i + 1
		if i < 0 {
			if len(out) > 0 && out[0] == '{' {
				start = 0
			} else {
				return obj, false, nil
			}
		}
		dec := json.NewDecoder(bytes.NewReader(out[start:]))
		var o resultObject
		if derr := dec.Decode(&o); derr == nil {
			return o, true, nil
		} else if err == nil {
			err = fmt.Errorf("the harness's result at byte %d does not parse: %v", start, derr)
		}
		if start == 0 {
			break
		}
		off = i
	}
	return obj, false, err
}

// codexEvent is one line of codex's JSONL.
type codexEvent struct {
	Type  string `json:"type"`
	Usage *struct {
		Input      *int64 `json:"input_tokens"`
		CacheRead  *int64 `json:"cached_input_tokens"`
		CacheWrite *int64 `json:"cache_write_input_tokens"`
		Output     *int64 `json:"output_tokens"`
		Reasoning  *int64 `json:"reasoning_output_tokens"`
	} `json:"usage"`
	Message string `json:"message"`
	Item    *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"item"`
}

// codexEvents is every JSONL line of the capture that decodes as an event, in order.
func codexEvents(out []byte) []codexEvent {
	var events []codexEvent
	for _, line := range bytes.Split(out, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var e codexEvent
		if json.Unmarshal(line, &e) == nil && e.Type != "" {
			events = append(events, e)
		}
	}
	return events
}

// codexUsage sums every `turn.completed` event's usage: one launch is one turn, and a
// child that ran two reports both.
func codexUsage(out []byte) (ProviderUsage, error) {
	sums := map[string]int64{}
	seen := map[string]bool{}
	turns := 0
	for _, e := range codexEvents(out) {
		if e.Type != "turn.completed" || e.Usage == nil {
			continue
		}
		turns++
		for col, n := range map[string]*int64{
			"tokens_in": e.Usage.Input, "tokens_out": e.Usage.Output, "cache_write": e.Usage.CacheWrite,
			"cache_read": e.Usage.CacheRead, "reasoning": e.Usage.Reasoning,
		} {
			if n == nil {
				continue
			}
			if *n < 0 {
				return ProviderUsage{}, fmt.Errorf("codex usage column %s is negative", col)
			}
			sums[col] += *n
			seen[col] = true
		}
	}
	if turns == 0 {
		return ProviderUsage{Values: map[string]string{}}, nil
	}
	values := map[string]string{"provider": harness.Codex, "model": Dash, "repo": Dash}
	for _, col := range TokenColumns {
		values[col] = Dash
		if seen[col] {
			values[col] = strconv.FormatInt(sums[col], 10)
		}
	}
	return ProviderUsage{Values: values, Observed: true, Turns: turns}, nil
}

// HeadlessFailure is the failure a headless child reported in its own output, when it
// did: claude's result with `is_error` (its `result` is the message), codex's and grok's
// `{"type":"error","message":...}` line, codex's `turn.failed`. The cause is classed as a
// provider's log line is (CauseFromText), so an expired login is `auth` and a quota is
// `out-of-credit`. ok is false when the output names none.
func HeadlessFailure(kind string, out []byte) (ProviderCause, bool) {
	switch kind {
	case harness.Claude, harness.Grok:
		obj, found, err := lastResult(out)
		if err != nil || !found {
			break
		}
		switch {
		case obj.IsError:
			return CauseFromText(obj.Result), true
		case obj.Type == "error":
			return CauseFromText(obj.Message), true
		}
	case harness.Codex:
		for _, e := range codexEvents(out) {
			switch {
			case e.Type == "error":
				return CauseFromText(e.Message), true
			case e.Type == "turn.failed":
				return CauseFromText(firstNonEmpty(e.Message, "turn failed")), true
			}
		}
	}
	return ProviderCause{}, false
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
