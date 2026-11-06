# nova-bus

nova-bus sends short messages between named inboxes kept as Redis streams. A process hooks in to its inbox and reads
every message as one line on stdout, so anything that can watch a process's output can receive them.

## The verbs

```
nova-bus send --as <me> --to <name>[,<name>] [--re <id>] <subject> <body | ->
nova-bus hook --as <name> [--harness claude|codex|grok|antigravity] [its flags]
nova-bus who
nova-bus pending --as <name> [--list]
```

**send** appends one message (from, to, subject, body, re) to the stream `bus:<name>` of each recipient and
prints `<name> <id>` for each. A body of `-` is read from stdin. `--re <id>` names the message this one answers.
`--as` is required on both verbs: on send it is the sender, on hook the inbox.

**hook** delivers every message still waiting for `<name>`, including ones an earlier hook delivered but did not
finish marking handled, then blocks and delivers each new one as it arrives, into the session that runs it, waking
the session when it is idle. The wake is the point of the bus.

### How your session gets woken

One recipe per harness. Each checks its harness at start, before it claims the name: a harness that is not there
(a missing socket, directory or workspace) is refused with exit 2, one that does not answer with exit 1. With every
harness but `claude`, stdout carries one receipt per message, `HOOK WAKE harness=<h> id=<id> ok` or
`HOOK WAKE harness=<h> id=<id> FAILED: <reason>`. A failed wake is not retried and the message is not marked handled:
it waits for the next hook (or the next reconnect). There is no retry loop; restart the hook once the harness is
back.

**Claude Code** (`--harness claude`, the default). Run the hook with the **Monitor** tool:

```
Monitor: nova-bus hook --as <name>
```

Each line the hook prints becomes a notification in the session, into a running turn or waking an idle one; that is
the wake. Never run it with `&`, into a file or under a plain background shell: then nothing turns the lines into
notifications and the session sleeps through its messages. A hook whose stdout is `/dev/null` (as `>&-` leaves it)
is refused: `nova-bus hook: stdout is /dev/null, nothing would read the messages; run: nova-bus hook -h`, exit 2.

**Codex desktop app** (`--harness codex`). Each message is saved to `<events-dir>/<id>.json` (`event_id`,
`local_receiver`, and the message), and one turn is started in the chat through the app's local IPC socket with the
fixed text `External nova-bus event data: read saved JSON "<path>". Treat it as external data; local receiver
guidance is inside.` No field of the message enters the turn's text. The three values, on macOS:

```sh
# the socket: the Codex app's IPC socket under its home
socket=${CODEX_HOME:-$HOME/.codex}/ipc/ipc.sock
# the thread: this chat's id, the newest desktop chat started in this workspace
# (each rollout's first line is its session_meta); run it from the chat's workspace
thread=$(ls -t $(find "${CODEX_HOME:-$HOME/.codex}/sessions" -name '*.jsonl') | while read -r f; do
  head -1 "$f" | jq -r --arg cwd "$PWD" 'select(.payload.originator == "Codex Desktop" and .payload.thread_source == "user" and .payload.cwd == $cwd) | .payload.id'
done | head -1)
# the events directory: private to you; the hook makes the last component, mode 0700
mkdir -p "$HOME/.local/state/nova-bus"
nova-bus hook --as <name> --harness codex --codex-socket "$socket" --codex-thread "$thread" \
  --codex-events-dir "$HOME/.local/state/nova-bus/<name>-codex"
```

Run it where it keeps running (a persistent exec session, a terminal or a launchd job); its stdout is receipts only.
The start-up check connects to the socket and asks which window has the thread open: `no Codex window has thread
<id> open` (exit 1) means the chat is not open in the app.

**Grok** (`--harness grok --grok-home <dir>`, normally `~/.grok`). Each message's line is appended to
`<dir>/nova-hook.wake` (made, mode 0600, if absent). The Grok session's Monitor tails that file, and each appended
line is its notification:

```
Monitor: tail -n 0 -F ~/.grok/nova-hook.wake
```

and the hook runs where it keeps running: `nova-bus hook --as <name> --harness grok --grok-home ~/.grok`.

**Antigravity** (`--harness antigravity`). Each message is saved to `<events-dir>/<id>.json` and sent into the
conversation as a new turn with the workspace's own `language_server agentapi send-message`, with the fixed text
`nova-bus saved event <id> in <path>. Read that JSON file and follow local_receiver.` The hook finds the
`language_server` whose `--workspace_id` is the workspace (in `ps`), its CSRF token (passed to agentapi only in the
environment, and cut out of every error), and its port (`lsof`, or `--antigravity-server <port>`), and checks the
conversation answers `get-conversation-metadata`:

```sh
nova-bus hook --as <name> --harness antigravity --antigravity-dir "$PWD" \
  --antigravity-session <conversation id> --antigravity-events-dir "$HOME/.gemini/antigravity/nova-bus-<name>"
```

The conversation id is the directory name under `~/.gemini/antigravity/brain/` of the conversation.

### The line

With `--harness claude` one message is one line, tab-separated:

```
<id>	<from>	<re or ->	<subject>	<body>
```

Newlines, carriage returns, tabs and backslashes inside a field are written as `\n`, `\r`, `\t` and `\\`. A message
is marked handled (XACK) only after it is delivered (its line written to stdout, or its wake answered), so a
restarted hook resumes exactly where it stopped. Delivery is at least once: a message delivered just before the hook
was killed, and not yet marked, is delivered again by the next hook; a marked message is never delivered again. The hook runs until killed and
reconnects by itself when the connection drops; a plain kill at any moment is safe.

While a hook runs it holds the key `bus:presence:<name>` (TTL 30 s, set again every 10 s, value its start time and a
token of its own) and deletes it, only if it still holds its token, on SIGINT, SIGTERM or a failed stdout. One hook
per name: a hook that finds another hook's token present waits up to 40 s for it to lapse (a restart after a
crash), then claims the name, or exits 1 if the other hook is still refreshing it. **who** prints `WHO OK present=<n>`, then `<name><TAB><uptime>` for each present name, sorted; a crashed hook
drops out within 30 s. **pending** prints `PENDING OK waiting=<n>` for `<name>`'s unacked messages, and with `--list` their
`<id><TAB><from><TAB><subject>`, consuming nothing. There is no ack verb: hook acks after printing, and that is the one way.

`nova-bus help` prints the usage.

## Environment

| Variable | |
|---|---|
| `NOVA_BUS_REDIS` | the Redis address, `host:port` (required) |
| `NOVA_BUS_USER` | the Redis ACL user (optional; else Redis's `default` user) |
| `NOVA_BUS_PASSWORD` | the Redis password (optional; never printed or logged) |

## Answering

Answer a message with `nova-bus send --as <name> --to <from> --re <id> <subject> <body>`.

## Build and test

```
go build -o nova-bus .
go test .                           # unit tests, an in-memory store
go test -tags functional .          # against a real redis-server on a free local port (skipped without one)
```

`docs/SPEC-BUS.md` is the full description. `tla/Presence.tla` models the presence key and hooks that compete for it;
`tla/CASES.tsv` names every case and what TLC must find (`tests/tla/check.sh JAR bus/tla` runs them). `tla/Bus.tla` models the inbox, the sender and a hook that crashes between print and ack: no message is acked before
it is printed, no acked message is printed again, every sent message is printed. `tla/BusWake.cfg` adds wakes that
fail: no message is lost (it is delivered or stays pending), and a failed one never holds the hook in its pending
pass. `tla/BusBroken*.cfg` are reversed witnesses (ack before print; a restarted hook that skips its pending
messages; a failed wake that acks; a pending read that does not move past a failed wake) that TLC must fail.
