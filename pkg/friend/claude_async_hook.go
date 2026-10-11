package friend

import (
	"bytes"
	"encoding/json"
)

// ClaudeAsyncBashHook is the pure PreToolUse response for an opt-in Claude Code
// project hook (docs/CLAUDE-ASYNC-BASH-CANDIDATE.md). Every Bash
// call requests a native background task so an omitted timeout cannot
// hold the session when background tasks are enabled. The response changes only tool input; permission rules
// still decide whether the command may run.
func ClaudeAsyncBashHook(input []byte) []byte {
	var event struct {
		HookEventName string                     `json:"hook_event_name"`
		ToolName      string                     `json:"tool_name"`
		ToolInput     map[string]json.RawMessage `json:"tool_input"`
	}
	if err := json.Unmarshal(input, &event); err != nil || event.HookEventName != "PreToolUse" || event.ToolName == "" {
		return claudeAsyncDeny("the hook input is not a PreToolUse event; the command was not started")
	}
	if event.ToolName != "Bash" {
		return nil
	}
	if event.ToolInput == nil {
		return claudeAsyncDeny("the Bash tool input is missing; the command was not started")
	}
	var command string
	if err := json.Unmarshal(event.ToolInput["command"], &command); err != nil || command == "" {
		return claudeAsyncDeny("the Bash command is missing; the command was not started")
	}
	if raw, ok := event.ToolInput["run_in_background"]; ok {
		var background bool
		if err := json.Unmarshal(raw, &background); err != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return claudeAsyncDeny("the Bash background flag is malformed; the command was not started")
		}
		if background {
			return nil
		}
	}
	event.ToolInput["run_in_background"] = json.RawMessage("true")
	response, _ := json.Marshal(map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName": "PreToolUse",
			"updatedInput":  event.ToolInput,
		},
	}) // ignored: maps contain only strings and valid RawMessages from a decoded JSON object
	return response
}

func claudeAsyncDeny(reason string) []byte {
	response, _ := json.Marshal(map[string]any{
		"hookSpecificOutput": map[string]string{
			"hookEventName":            "PreToolUse",
			"permissionDecision":       "deny",
			"permissionDecisionReason": reason,
		},
	}) // ignored: a map of fixed strings always encodes
	return response
}
