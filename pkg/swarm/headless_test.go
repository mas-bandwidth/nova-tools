package swarm

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/harness"
)

// The three captures below are what the three CLIs printed on 2026-10-04 for the one-line
// card "Reply with the single word pong and nothing else." (claude 2.1.220, codex 0.153.4,
// grok 1.0.46), the claude one its logged-out answer.
const (
	claudeErrorCapture = `{"is_error":true,"duration_api_ms":0,"num_turns":1,"stop_reason":"stop_sequence","session_id":"95a7bb08","total_cost_usd":0,"usage":{"input_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":0},"modelUsage":{},"permission_denials":[],"terminal_reason":"api_error","subtype":"success","api_error_status":null,"result":"Failed to authenticate: OAuth session expired and could not be refreshed","type":"result","duration_ms":43,"uuid":"2661fe74"}
`
	claudeOKCapture = "Warning: no stdin data received in 3s, proceeding without it.\n" +
		`{"type":"result","subtype":"success","is_error":false,"num_turns":2,"result":"pong","total_cost_usd":0.0412,"usage":{"input_tokens":12,"cache_creation_input_tokens":3000,"cache_read_input_tokens":21000,"output_tokens":7},"modelUsage":{"claude-opus-5-5":{"inputTokens":12,"outputTokens":7,"costUSD":0.0412}}}` + "\n"
	codexCapture = `Reading additional input from stdin...
{"type":"thread.started","thread_id":"01a1070d"}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"pong"}}
{"type":"turn.completed","usage":{"input_tokens":18880,"cached_input_tokens":0,"cache_write_input_tokens":0,"output_tokens":5,"reasoning_output_tokens":0}}
`
	grokCapture = `{
  "text": "pong",
  "stopReason": "end_turn",
  "sessionId": "01a1070d",
  "usage": {
    "input_tokens": 13586,
    "cache_read_input_tokens": 10240,
    "cache_creation_input_tokens": 0,
    "output_tokens": 83,
    "reasoning_tokens": 82,
    "total_tokens": 23909
  },
  "num_turns": 1,
  "total_cost_usd": 0.0111486,
  "modelUsage": {
    "grok-4.7-build": {
      "inputTokens": 13586,
      "outputTokens": 83,
      "costUSD": 0.0111486
    }
  }
}
`
)

// One argv shape per harness: the one-shot form, the model, machine output, the
// permission mode the wall makes safe, and the prompt last, verbatim; a prompt that begins
// with a dash is still the prompt.
func TestEachHeadlessHarnessHasOneArgvShape(t *testing.T) {
	t.Parallel()
	prompt := "- do the thing\n{model} stays"
	argv, err := HeadlessArgv(harness.Claude, "/b/claude", "opus-5-5", prompt)
	require.NoError(t, err)
	assert.Equal(t, []string{"/b/claude", "-p", "--model", "opus-5-5", "--output-format", "json", "--permission-mode", "bypassPermissions", "--disallowed-tools", "WebFetch,WebSearch", "--", prompt}, argv)
	argv, err = HeadlessArgv(harness.Codex, "codex", "gpt-6-astra", prompt)
	require.NoError(t, err)
	assert.Equal(t, []string{"codex", "exec", "--skip-git-repo-check", "--json", "--ephemeral", "--dangerously-bypass-approvals-and-sandbox", "-c", `web_search="disabled"`, "--model", "gpt-6-astra", "--", prompt}, argv)
	argv, err = HeadlessArgv(harness.Grok, "grok", "grok-4.7", prompt)
	require.NoError(t, err)
	assert.Equal(t, []string{"grok", "--output-format", "json", "--permission-mode", "bypassPermissions", "--disable-web-search", "--model", "grok-4.7", "--single=" + prompt}, argv)
	_, err = HeadlessArgv(harness.OpenCode, "opencode", "m", prompt)
	assert.Error(t, err, "opencode launches through the providers table, never here")
}

// The usage is read from what the child printed: claude's and grok's result object,
// codex's turn.completed event; a class not reported is a dash, the cost the harness's own
// where it printed one.
func TestHeadlessUsageIsReadFromTheCapture(t *testing.T) {
	t.Parallel()
	u, err := HeadlessUsage(harness.Claude, []byte(claudeOKCapture))
	require.NoError(t, err)
	assert.True(t, u.Observed)
	assert.Equal(t, map[string]string{"tokens_in": "12", "tokens_out": "7", "cache_write": "3000", "cache_read": "21000", "reasoning": Dash,
		"cost": "0.0412", "usd": "0.0412", "provider": "claude", "model": "claude-opus-5-5", "repo": Dash}, u.Values)
	assert.Equal(t, 2, u.Turns)

	u, err = HeadlessUsage(harness.Codex, []byte(codexCapture))
	require.NoError(t, err)
	assert.True(t, u.Observed)
	assert.Equal(t, map[string]string{"tokens_in": "18880", "tokens_out": "5", "cache_write": "0", "cache_read": "0", "reasoning": "0",
		"provider": "codex", "model": Dash, "repo": Dash}, u.Values, "codex prints no cost and no model: the route prices and names it")
	assert.Equal(t, 1, u.Turns)

	u, err = HeadlessUsage(harness.Grok, []byte(grokCapture))
	require.NoError(t, err)
	assert.True(t, u.Observed)
	assert.Equal(t, "13586", u.Values["tokens_in"])
	assert.Equal(t, "82", u.Values["reasoning"])
	assert.Equal(t, "0.0111486", u.Values["cost"])
	assert.Equal(t, "grok-4.7-build", u.Values["model"])
	sum, seen, partial := u.Budget()
	assert.Equal(t, 13586+83+82, sum)
	assert.Equal(t, 3, seen)
	assert.False(t, partial)
}

// A capture with no result is an absence, never a failure and never a zero; one whose
// result will not parse is a read that failed.
func TestHeadlessUsageKeepsAbsenceApartFromFailure(t *testing.T) {
	t.Parallel()
	for _, kind := range harness.Headless {
		u, err := HeadlessUsage(kind, []byte("the child said nothing of use\n"))
		require.NoError(t, err, kind)
		assert.False(t, u.Observed, kind)
		assert.Empty(t, u.Values, kind)
	}
	_, err := HeadlessUsage(harness.Claude, []byte("{\"type\":\"result\",\"usage\":{\"input_tokens\":-1}}\n"))
	assert.Error(t, err)
	_, err = HeadlessUsage(harness.Grok, []byte("{\"usage\":{\"input_tokens\":1},\n"))
	assert.Error(t, err, "an object that begins a line and will not decode is a failed read")
	_, err = HeadlessUsage(harness.OpenCode, nil)
	assert.Error(t, err)
}

// The failure the child itself reported is a provider cause: an expired login is auth, a
// model the account cannot use is what the harness said.
func TestHeadlessFailureIsReadFromTheCapture(t *testing.T) {
	t.Parallel()
	c, ok := HeadlessFailure(harness.Claude, []byte(claudeErrorCapture))
	require.True(t, ok)
	assert.Equal(t, CauseAuth, c.Class)
	assert.Contains(t, c.Message, "OAuth session expired")
	_, ok = HeadlessFailure(harness.Claude, []byte(claudeOKCapture))
	assert.False(t, ok)

	codexFailed := codexCapture + `{"type":"error","message":"{\"type\":\"error\",\"status\":400,\"error\":{\"message\":\"The 'x' model is not supported when using Codex with a ChatGPT account.\"}}"}` + "\n" + `{"type":"turn.failed","error":{}}` + "\n"
	c, ok = HeadlessFailure(harness.Codex, []byte(codexFailed))
	require.True(t, ok)
	assert.Contains(t, c.Message, "not supported")
	_, ok = HeadlessFailure(harness.Codex, []byte(codexCapture))
	assert.False(t, ok)

	c, ok = HeadlessFailure(harness.Grok, []byte(`{"type":"error","message":"Couldn't set model 'x': Invalid params: \"unknown model id\"."}`+"\nError: Couldn't set model\n"))
	require.True(t, ok)
	assert.Contains(t, c.Message, "unknown model id")
	_, ok = HeadlessFailure(harness.Grok, []byte(grokCapture))
	assert.False(t, ok)
}

// A login is read from the status verb each CLI has.
func TestHeadlessLoginIsReadFromTheStatusVerb(t *testing.T) {
	t.Parallel()
	in, said := HeadlessLogin(harness.Claude, []byte("{\n  \"loggedIn\": false,\n  \"authMethod\": \"none\"\n}\n"), 0)
	assert.False(t, in)
	assert.Equal(t, "loggedIn=false authMethod=none", said)
	in, _ = HeadlessLogin(harness.Claude, []byte(`{"loggedIn":true,"authMethod":"claude.ai"}`), 0)
	assert.True(t, in)
	in, said = HeadlessLogin(harness.Codex, []byte("Logged in using ChatGPT\n"), 0)
	assert.True(t, in)
	assert.Equal(t, "Logged in using ChatGPT", said)
	in, _ = HeadlessLogin(harness.Codex, []byte("Not logged in\n"), 1)
	assert.False(t, in)
	in, _ = HeadlessLogin(harness.Grok, []byte("You are logged in with grok.com.\n\nDefault model: grok-4.7\n"), 0)
	assert.True(t, in)
	in, said = HeadlessLogin(harness.Grok, []byte("You are not authenticated.\n\nDefault model: grok-4.6\n"), 0)
	assert.False(t, in)
	assert.Equal(t, "You are not authenticated.", said)
	assert.Equal(t, []string{"auth", "status"}, HeadlessLoginArgv(harness.Claude))
	assert.Equal(t, []string{"login", "status"}, HeadlessLoginArgv(harness.Codex))
	assert.Equal(t, []string{"models"}, HeadlessLoginArgv(harness.Grok))
	assert.Nil(t, HeadlessLoginArgv(harness.OpenCode))
}

// Each harness runs from a private home under the child's data home, never the bench's own
// ~/.<kind>: the child is pointed at it by name where the program reads one, and grok, which
// reads HOME alone, finds it where HOME puts it. Of the bench's login only the credential
// file is named for copying, and claude, whose file is a refreshable OAuth login, names
// none: it is handed its token by name instead.
func TestAHeadlessHarnessRunsFromAPrivateHome(t *testing.T) {
	t.Parallel()
	h := HeadlessHomeOf(harness.Claude, "/home/b", "/s/data")
	assert.Equal(t, HeadlessHome{Source: "/home/b/.claude", Dir: "/s/data/.claude", Env: []string{"CLAUDE_CONFIG_DIR=/s/data/.claude"}, Token: ClaudeTokenEnv}, h)
	h = HeadlessHomeOf(harness.Codex, "/home/b", "/s/data")
	assert.Equal(t, HeadlessHome{Source: "/home/b/.codex", Dir: "/s/data/.codex", Env: []string{"CODEX_HOME=/s/data/.codex"}, Login: []string{"auth.json"}}, h)
	h = HeadlessHomeOf(harness.Grok, "/home/b", "/s/data")
	assert.Equal(t, HeadlessHome{Source: "/home/b/.grok", Dir: "/s/data/.grok", Login: []string{"auth.json"}}, h)
	for _, k := range harness.Headless {
		h := HeadlessHomeOf(k, "/home/b", "/s/data")
		assert.NotEqual(t, h.Source, h.Dir, k)
		assert.True(t, strings.HasPrefix(h.Dir, "/s/data/"), "%s: the home is under the data home, a write of the wall", k)
		for _, f := range h.Login {
			assert.Equal(t, filepath.Base(f), f, "%s: a credential is a file by name, never a path", k)
		}
	}
}

// The fence of an opencode child denies webfetch (FencePermission); a headless child has no
// such config, so each argv turns its harness's own web tools off, whatever the model or the
// prompt, and they are off in every one of the three.
func TestEachHeadlessHarnessRunsWithItsWebToolsOff(t *testing.T) {
	t.Parallel()
	assert.Equal(t, FenceDeny, FencePermission("/j", nil)[FenceWebfetch], "the opencode fence the headless argv matches")
	off := map[string][]string{
		harness.Claude: {"--disallowed-tools", "WebFetch,WebSearch"},
		harness.Codex:  {"-c", `web_search="disabled"`},
		harness.Grok:   {"--disable-web-search"},
	}
	for _, k := range harness.Headless {
		argv, err := HeadlessArgv(k, k, "m", "--web-search on please")
		require.NoError(t, err)
		end := slices.Index(argv, "--")
		if end < 0 {
			end = len(argv) - 1 // grok: the prompt is its last word, `--single=...`
		}
		assert.Contains(t, strings.Join(argv[:end], "\x00"), strings.Join(off[k], "\x00"), "%s: web tools off before the prompt", k)
	}
}

// claude's `.credentials.json` is a refreshable OAuth login: a copy per launch would take the
// refresh a turn makes into the copy, which the next launch discards, and the bench's own
// refresh token would go stale. So claude's private home names no credential file to copy, and
// its login is the long-lived token the run hands it by name (CLAUDE_CODE_OAUTH_TOKEN, a
// --pass secret), which needs no keychain inside the wall. A run that hands no token is told
// so, naming the remedy; the note never carries a value.
func TestClaudesLoginIsAHandedTokenNeverACopyOfItsRefreshableFile(t *testing.T) {
	t.Parallel()
	h := HeadlessHomeOf(harness.Claude, "/home/b", "/s/data")
	assert.Empty(t, h.Login, "claude's refreshable credential file is never copied per launch")
	assert.Equal(t, "CLAUDE_CODE_OAUTH_TOKEN", h.Token)
	for _, k := range []string{harness.Codex, harness.Grok} {
		assert.Empty(t, HeadlessHomeOf(k, "/home/b", "/s/data").Token, "%s reads its login from its file", k)
		assert.Empty(t, HeadlessTokenNote(HeadlessHomeOf(k, "/home/b", "/s/data"), nil), k)
	}

	assert.Empty(t, HeadlessTokenNote(h, []string{"PATH=/bin", "CLAUDE_CODE_OAUTH_TOKEN=fake-token-value"}))
	for _, env := range [][]string{nil, {"PATH=/bin"}, {"CLAUDE_CODE_OAUTH_TOKEN="}, {"CLAUDE_CODE_OAUTH_TOKEN=  "}, {"XCLAUDE_CODE_OAUTH_TOKEN=fake"}} {
		note := HeadlessTokenNote(h, env)
		assert.Contains(t, note, "CLAUDE_CODE_OAUTH_TOKEN", "%q", env)
		assert.Contains(t, note, "--pass CLAUDE_CODE_OAUTH_TOKEN", "%q: the note names the remedy", env)
		assert.NotContains(t, note, "fake", "%q: the note carries no value", env)
		assert.NotContains(t, note, "\n", "%q: one line", env)
	}
}
