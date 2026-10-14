# Claude Code hook adapter

`nova-hook hook --harness claude` installs this client into a Claude Code
settings directory and then holds the bus push the way the hook already does.
The harness word is `claude`, the word `pkg/harness` already uses. This adapter
adds no second name and no registry row.

One Monitor on the hook client's stdout is the delivery. Each coalesced
`HOOK PUSH` line is what that Monitor injects into a running turn, and what
wakes a turn when Claude Code is idle. The adapter does not query a live
Claude session, so the first notice is labeled a wake. A later notice is
labeled a wake only after the installed Stop hook marks a turn boundary.
Mislabeling still prints the line. Nothing here is a measurement of a real
Claude Code session.

## One notice

At most one ordinary notice is pending. Ordinary messages that arrive while
that notice is queued, or while a turn is held, fold into it. The client
counts a whole read from the bus, then emits once. A burst of 50 ordinary
messages is one line, `49 more messages; read the bus`. One ordinary message
is `bus message; read the bus`. A challenge is the prove line `run: nova-hook
prove --as <name> --nonce <n>`, and a challenge folded with other messages
before the first notice prefixes that line. A challenge, and a newer nonce for
that challenge, is delivered even while the ordinary notice is held. It does
not wait for the Stop hook. The nonce already shown is not delivered again.
Message body and subject never enter the notice. The client's own bus messages
are skipped, except a challenge a server addresses to the hooked name.

`tla/ClaudeCodeNotice.tla` is that machine: Arrive, Deliver, fold while held,
NewerNonce, DeliverChallenge, Boundary. TLC has not been run. The coverage row
is partial, owed by `claude-code-notice-tlc`. The bench cfg is owed.

## Install

```
nova-hook hook --as <name> --role <role> --harness claude \
  --claude-state <dir> --claude-settings <dir>
```

`--claude-state` and `--claude-settings` may come from `NOVA_HOOK_CLAUDE_STATE`
and `NOVA_HOOK_CLAUDE_SETTINGS`. Both directories must already exist.
`--once` is refused. `--dry-run` reads the hook plan and installs nothing.
Codex delivery flags and `--harness claude` together are refused. A harness
word other than `claude` stays on the flag's existing check. Install runs
after that check and before the hook record is written, so a failed install
leaves the record alone.

The settings directory gains `nova-hook-claude-boundary.sh` and one Stop
command in `settings.json`. The command is that script path as one
single-quoted shell word, so a settings directory with a space or a semicolon
stays one argument. An existing settings file is kept. A second install does
not add the command again. The script runs `nova-hook hook` with `--as`,
`--role` and `--claude-boundary`. That command writes the next decimal into
`<state>/generation` and exits before Redis and before arming. The running
client reads the file with its own clock; the file is the boundary, not a
timestamp.

The Stop hook is only the boundary. It does not print a second copy of the notice.
