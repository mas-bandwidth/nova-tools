package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClaudeHookEntrypointPrintsOnlyProtocolJSON(t *testing.T) {
	t.Parallel()
	input := `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"checksum","description":"long task","custom":[1,2]}}`
	var out, errb bytes.Buffer
	code := run([]string{"hook", "--harness", "claude"}, strings.NewReader(input), &out, &errb, world{})
	require.Equal(t, 0, code)
	assert.Empty(t, errb.String())
	assert.NotContains(t, out.String(), "NOVA-FRIEND")
	var response struct {
		HookSpecificOutput struct {
			UpdatedInput map[string]json.RawMessage `json:"updatedInput"`
		} `json:"hookSpecificOutput"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &response))
	assert.Equal(t, "true", string(response.HookSpecificOutput.UpdatedInput["run_in_background"]))
	assert.Equal(t, `"checksum"`, string(response.HookSpecificOutput.UpdatedInput["command"]))
	assert.Equal(t, `[1,2]`, string(response.HookSpecificOutput.UpdatedInput["custom"]))
}

func TestClaudeHookEntrypointIsSilentForBackgroundAndDeniesMalformed(t *testing.T) {
	t.Parallel()
	var out, errb bytes.Buffer
	code := run([]string{"hook", "--harness", "claude"}, strings.NewReader(`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"checksum","run_in_background":true}}`), &out, &errb, world{})
	assert.Equal(t, 0, code)
	assert.Empty(t, out.String())
	assert.Empty(t, errb.String())
	out.Reset()
	code = run([]string{"hook", "--harness", "claude"}, strings.NewReader("{"), &out, &errb, world{})
	assert.Equal(t, 0, code)
	assert.Contains(t, out.String(), `"permissionDecision":"deny"`)
	assert.NotContains(t, out.String(), `"permissionDecision":"allow"`)
}

func TestClaudeHookEntrypointRejectsOtherHarnessAndOversize(t *testing.T) {
	t.Parallel()
	var out, errb bytes.Buffer
	code := run([]string{"hook", "--harness", "codex"}, strings.NewReader(""), &out, &errb, world{})
	assert.Equal(t, 2, code)
	assert.Empty(t, out.String())
	assert.Contains(t, errb.String(), "--harness wants claude")
	out.Reset()
	errb.Reset()
	code = run([]string{"hook", "--harness", "claude"}, strings.NewReader(strings.Repeat("x", 1<<20+1)), &out, &errb, world{})
	assert.Equal(t, 0, code)
	assert.Contains(t, out.String(), `"permissionDecision":"deny"`)
	assert.Contains(t, errb.String(), "exceeds 1 MiB")
}
