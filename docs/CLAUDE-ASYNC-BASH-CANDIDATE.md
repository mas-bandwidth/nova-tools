# Claude async Bash candidate

The coordinator's bus wait can complete while an unrelated foreground Bash
call still holds the Claude session. The native completion notification then
waits for that call to return before the LLM reads it. A background bus wait
alone therefore does not establish busy push independence.

This is an **opt-in candidate**, not an installed hook or an acceptance pass.
`nova-friend hook --harness claude` consumes Claude Code's `PreToolUse` event
on stdin and emits only hook protocol JSON. For every Bash call without
`run_in_background: true`, it returns `hookSpecificOutput.updatedInput` with
the original `tool_input` fields preserved and that flag set to `true`. It
returns no `permissionDecision`, so normal permission checks still apply to
the rewritten input. If the event is malformed, it denies the call before it
can start. Other tools and already-background Bash calls receive no hook
decision. Applying this to every Bash call is deliberate: an omitted timeout
can still leave a command in the foreground for minutes. The hook neither
executes the command nor reads its output.

For a project that explicitly opts in, build `nova-friend` from the checked-out
source and configure it in that project's `.claude/settings.local.json`. For
example, from a project containing this source, after its source gates pass:

```sh
mkdir -p .claude/hooks
go build -o .claude/hooks/nova-friend ./cmd/nova-friend
```

```json
{
  "hooks": {
    "PreToolUse": [{
      "matcher": "Bash",
      "hooks": [{
        "type": "command",
        "command": "${CLAUDE_PROJECT_DIR}/.claude/hooks/nova-friend",
        "args": ["hook", "--harness", "claude"]
      }]
    }]
  }
}
```

The `args` field selects Claude Code's exec form, so a project path containing
spaces remains one executable path. This follows Claude Code's documented
project hook schema. The helper's
response for a Bash call is shaped as follows, with all original input fields
carried through:

```json
{
  "hookSpecificOutput": {
    "hookEventName": "PreToolUse",
    "updatedInput": {
      "command": "long command",
      "run_in_background": true
    }
  }
}
```

Claude Code returns a native background task ID promptly; the command keeps
running and its output belongs to that task. The session should use the
task's completion notification and output path rather than start a blocking
foreground wait for the result. Claude Code can stop background tasks when
the session exits, and foreground subagents can stop their own tasks when
they finish, so this policy is for the main interactive session's Bash calls.
`CLAUDE_CODE_DISABLE_BACKGROUND_TASKS=1` and bare mode disable the facility.

This hook cannot yield a foreground non-Bash tool, a foreground subagent, or
an explicit blocking wait on another task. A doctor check can verify the
configured hook and local Claude capability, but it cannot certify LLM
receipt. Keep the foreground busy acceptance case failed. A new busy trial
must show the native waiter completes, the LLM answers before the unrelated
background job finishes, and that job later completes with its output intact.

Documentation: [background Bash commands](https://code.claude.com/docs/en/interactive-mode#background-bash-commands),
[PreToolUse input and decisions](https://code.claude.com/docs/en/hooks#pretooluse),
[background task lifetime](https://code.claude.com/docs/en/tools-reference#background-commands).
