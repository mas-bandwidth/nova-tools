# Antigravity desktop wake adapter (`nova-hook hook --harness antigravity`)

`nova-hook hook --harness antigravity` hooks a recipient identity into its
running Antigravity desktop session and delivers hook challenges and bus
messages directly into the target conversation's `.system_generated/messages`
mailbox via the Antigravity `agentapi` client.

## Setup and Installation

Running `nova-hook hook --harness antigravity` (with `--antigravity-dir <workspace>`
or `--antigravity-session <uuid>` / `--session <uuid>`) installs and verifies
its local native setup before arming any hook record or lease:

1. **Private events directory**: Creates and validates the canonical `0700`
   saved-events directory (default `<home>/.gemini/antigravity/nova-hook/<as>`,
   or `--antigravity-events-dir <dir>`) and acquires the exclusive process lock
   (`pkg/filelock`) so two live hook clients cannot share the same receiver.
2. **`agentapi` wrapper**: Ensures `<home>/.gemini/antigravity/bin/agentapi`
   exists and is executable (`0755`). If missing, `nova-hook` writes the binary
   wrapper delegating to the running Antigravity `language_server` binary with
   `agentapi "$@"`.
3. **Server and port discovery**: Discovers the running Antigravity
   `language_server` process for the workspace (`pid` and `--csrf_token` via
   `ps`) and its listening TCP ports (`lsof`), plus any optional candidate port
   from `--antigravity-server`.
4. **Conversation resolution and probe**: Resolves the target conversation UUID
   from `--antigravity-session` (`--session`) or selects the newest root
   conversation for `--antigravity-dir` from
   `<home>/.gemini/antigravity/conversations/conversation_summaries.db` via
   `sqlite3 -json`, then probes candidate ports with
   `agentapi get-conversation-metadata <conversation>` using
   `ANTIGRAVITY_CLI_ALIAS=agy`, `ANTIGRAVITY_AGENT_PORT=<port>`,
   `ANTIGRAVITY_LS_ADDRESS=localhost:<port>`,
   `ANTIGRAVITY_CSRF_TOKEN=<token>`, and `GEMINI_CLI_CSRF_TOKEN=<token>`.
5. **Mailbox preparation and state line**: Prepares
   `<home>/.gemini/antigravity/brain/<conversation>/.system_generated/messages`
   (`0700`) and prints:
   ```text
   HOOK STATE state=antigravity-installed conversation=<uuid> port=<port> events_dir=<dir>
   ```

Invalid flags or local directory permissions are refused before writing a hook
record or lease. If the startup probe (`ps`, `lsof`, `sqlite3`, or `agentapi
get-conversation-metadata`) fails or times out, `nova-hook` emits
`HOOK STATE state=probe-retry` and retries with bounded exponential backoff
inside the client until the probe answers or a signal stops the process (never
exiting with a refusal under the never-unhooked rule). `--once` is refused;
`--dry-run` validates flags and paths without contacting the local language
server or writing saved event files.

## Saved Event Envelope and Delivery

Each external event or startup recovery batch is saved atomically as
`<events_dir>/<validated-event-id>.json` (`0600`, `atomicfile.NoReplace()`, at
most 64 KiB) before invoking `agentapi`. Only the validated event ID and
canonical saved path enter the native notice argument:

```text
nova-hook saved event <id> in <savedPath>. Read that JSON file and follow local_receiver.
```

External message subjects, bodies, senders, and nonces never enter command
arguments or shell text; they remain inside the saved JSON file under
`external_message` or `recovery_messages`.

`nova-hook` invokes:

```text
agentapi send-message --title=nova-hook <conversation> <notice>
```

which writes a `MESSAGE_PRIORITY_HIGH` `<SYSTEM_MESSAGE>` JSON file into
`<home>/.gemini/antigravity/brain/<conversation>/.system_generated/messages`.
Antigravity delivers this message at the next tool boundary during a running
turn and wakes an idle conversation within seconds.

If the `language_server` restarts mid-run with a new PID, CSRF token, or TCP
port, `deliverAntigravitySaved` re-discovers the server via `ps` and `lsof`,
re-probes `get-conversation-metadata`, and retries delivery once on the new
port. If a command times out without creating a mailbox file and no model
`--re` answer reconciles the event, `nova-hook` treats the outcome as
`ErrOutcomeUnknown`, revokes its bus route proof (`hooks.Unprove`), and stops
renewing its lease without blindly retrying.

## Burst Coalescing and Immediate Mid-Turn Challenge Delivery

Like the Codex desktop route (`PR #5772`), the Antigravity route tracks whether
a delivered turn is currently pending model action (`r.pending`) and folds
bursts of arriving ordinary bus messages into a single summary notice:

```text
N more messages from <senders>; read the bus
```

delivered once as `summary-<nonce>` when `codexTurnBoundary` observes that the
model answered the pending message, advanced its answers/owed set, or advanced
its bus consumer-group cursor.

Because Antigravity injects `MESSAGE_PRIORITY_HIGH` `<SYSTEM_MESSAGE>` items
directly into a running turn between tool steps, incoming `HOOK CHALLENGE`
events (`kind="challenge"` or `HOOK CHALLENGE nonce=<n>`) bypass ordinary
message coalescing and are delivered immediately into the running turn so the
model can run `nova-hook prove` before the 90-second challenge deadline.
Proving a mid-turn challenge satisfies the liveness check without prematurely
flushing folded ordinary messages while the ordinary turn is still running.
