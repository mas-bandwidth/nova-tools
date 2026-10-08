package friend

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClaudeAsyncBashHookPreservesInputAndNormalPermissionFlow(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, input string
	}{
		{"timeout omitted", `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"checksum","description":"long check","custom":{"retain":[1,true]}}}`},
		{"explicit timeout", `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"checksum","timeout":180000,"run_in_background":false,"custom":"keep"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ClaudeAsyncBashHook([]byte(tc.input))
			var response struct {
				HookSpecificOutput struct {
					HookEventName      string                     `json:"hookEventName"`
					PermissionDecision string                     `json:"permissionDecision"`
					UpdatedInput       map[string]json.RawMessage `json:"updatedInput"`
				} `json:"hookSpecificOutput"`
			}
			require.NoError(t, json.Unmarshal(got, &response))
			assert.Equal(t, "PreToolUse", response.HookSpecificOutput.HookEventName)
			assert.Empty(t, response.HookSpecificOutput.PermissionDecision, "input rewrite does not approve a command")
			var original struct {
				ToolInput map[string]json.RawMessage `json:"tool_input"`
			}
			require.NoError(t, json.Unmarshal([]byte(tc.input), &original))
			for field, value := range original.ToolInput {
				if field != "run_in_background" {
					assert.JSONEq(t, string(value), string(response.HookSpecificOutput.UpdatedInput[field]), field)
				}
			}
			wantFields := len(original.ToolInput) + 1
			if _, present := original.ToolInput["run_in_background"]; present {
				wantFields--
			}
			assert.Equal(t, wantFields, len(response.HookSpecificOutput.UpdatedInput), "only the background field changes")
			assert.Equal(t, "true", string(response.HookSpecificOutput.UpdatedInput["run_in_background"]))
		})
	}
}

func TestClaudeAsyncBashHookLeavesOtherAndBackgroundCallsAlone(t *testing.T) {
	t.Parallel()
	for _, input := range []string{
		`{"hook_event_name":"PreToolUse","tool_name":"Read","tool_input":{"file_path":"/tmp/a"}}`,
		`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"checksum","run_in_background":true}}`,
	} {
		assert.Empty(t, ClaudeAsyncBashHook([]byte(input)))
	}
}

func TestClaudeAsyncBashHookDeniesUnusableInputWithoutApproving(t *testing.T) {
	t.Parallel()
	for _, input := range []string{
		"", "{", `[]`, `{"tool_name":"Bash","tool_input":{"command":"checksum"}}`,
		`{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"checksum"}}`,
		`{"hook_event_name":"PreToolUse","tool_name":"Bash"}`,
		`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":""}}`,
		`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"checksum","run_in_background":"yes"}}`,
		`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"checksum","run_in_background":null}}`,
	} {
		var response struct {
			HookSpecificOutput struct {
				PermissionDecision string `json:"permissionDecision"`
				UpdatedInput       any    `json:"updatedInput"`
			} `json:"hookSpecificOutput"`
		}
		require.NoError(t, json.Unmarshal(ClaudeAsyncBashHook([]byte(input)), &response))
		assert.Equal(t, "deny", response.HookSpecificOutput.PermissionDecision, input)
		assert.Nil(t, response.HookSpecificOutput.UpdatedInput, input)
	}
}
