# nova-bus

nova-bus sends short messages between named inboxes kept as Redis streams. A process hooks in to its inbox and reads
every message as one line on stdout, so anything that can watch a process's output can receive them.

## The verbs

```
nova-bus send --as <me> --to <name>[,<name>] [--re <id>] [--new] <subject> <body | ->
nova-bus hook --as <name> [--harness claude|grok|antigravity] [its flags] [--exec <command> | --codex-thread <id>] [--service [stop]]
nova-bus recv --as <name> [--timeout <d>]
nova-bus ack --as <name> --id <id>
nova-bus who
nova-bus show agents | codex-agents
nova-bus show codex-wake --as <name> --codex-thread <thread>
nova-bus hook --as <name> --codex-thread <thread> --resolve <id>=woken|retry
nova-bus pending --as <name> [--list]
```

**send** appends one message (from, to, subject, body, re) to the stream `bus:<name>` of each recipient and
prints `<name> <id>` for each. A body of `-` is read from stdin. `--re <id>` names the message this one answers.
`--as` is required on both verbs: on send it is the sender, on hook the inbox. **You have to hook in before you
can send**: a verb that acts as you (`send`, `ack`, `pending`) refuses until you are hooked in (a hook holds your
name, or a `recv` receiver does), exit 1, and tells you how, with the recipe for the harness it
detects from your environment (`CODEX_THREAD_ID`: the `--codex-thread` service line; `ANTIGRAVITY_*` or `GEMINI_*`: the
Antigravity hook line; `GROK_HOME`: the Grok line; otherwise the Monitor command), then `run: nova-bus show
agents` for the full text. So everybody always hooks in, and every answer has a reader. `who`, `show`, `hook`
and `recv` stay open. A name no hook (or recv) has ever read is refused before anything is sent, exit 1, `no hook has ever held <name>, so nothing would read it`: a typo
would otherwise sit unread forever. `--new` sends to it anyway, for the first message to a name about to hook in.

**hook** delivers every message still waiting for `<name>`, including ones an earlier hook delivered but did not
finish marking handled, then blocks and delivers each new one as it arrives, into the session that runs it, waking
the session when it is idle. The wake is the point of the bus.

### How your session gets woken

One recipe per harness. **Claude Code and Muse Code** run the hook under the session's Monitor; **Codex** is woken
with `codex queue` while its chat is idle; **Grok** tails a wake file under its Monitor;
**Antigravity** is sent to with `agentapi send-message`. The hook checks its harness at start, before it claims the
name: a harness that is not there (a missing directory or workspace, a bad id) is refused with exit 2, one that
does not answer with exit 1. With `grok` and `antigravity`, stdout carries one receipt per message,
`HOOK WAKE OK harness=<h> id=<id>` (`confirmed=no reason=<r>` when the send was cut off and may have arrived: the
message is marked handled, so a turn is never started twice), or `HOOK WAKE FAILED harness=<h> id=<id> reason=<r>`
on stderr: a wake that surely failed ends the hook, exit 1, with the message unhandled and every later one behind
it, so the order is kept; `nova-bus pending --as <name> --list` shows them, and the next hook (the service restarts
it) delivers them. There is no retry loop. The hook refreshes its presence before every delivery and bounds each
wake to 8 s, so a slow harness never lets the name lapse.

`nova-bus show agents` and `nova-bus show codex-agents` print the lines for your standing instructions file
(CLAUDE.md, AGENTS.md) that make every fresh session do the right thing unasked.

**Claude Code and Muse Code** (`--harness claude`, the default). The Monitor tool's command is the hook itself,
nothing wrapped around it:

```
Monitor: NOVA_BUS_REDIS=<host:port> nova-bus hook --as <name>
```

Its first lines are the start, on stderr: `HOOK HARNESS OK ...` (another harness only), `HOOK WAIT name=<name>
held_since=... ttl=30s budget=40s` while a dead hook's key lapses, then `HOOK OK name=<name> waiting=<n>` once it holds the name, then
`next: nova-bus send --as <name> --to <name> <subject> <body>`: the line that names how to
answer. From then each message is one line on stdout, `<id><TAB><from><TAB><re or ->
<TAB><subject><TAB><body>`, for example `1762400000000-0	alpha	-	hi	are you there?`, and each line becomes a
notification in the session, into a running turn or waking an idle one; that is the wake. A line starting `HOOK `
(`HOOK RECONNECTED`, `HOOK BAD`) is status, not mail. One hook per name: a
second one for the same name waits 40 s and exits 1 `name already hooked`. A Monitor lives at most 30 minutes:
when it expires, or the hook ends, run the same Monitor command again (re-arm it), every time; a hook whose reader is gone (the Monitor or the session
ended) sees it within a second, exits 1 `stdout closed: its reader is gone`, and frees the name, so the next hook
claims it at once. Never run the hook with `&`, into a file or under a plain background shell: then nothing turns
the lines into notifications and the session sleeps through its messages. A hook whose stdout is `/dev/null` (as
`>&-` leaves it) is refused: `nova-bus hook: stdout is /dev/null, nothing would read the messages; run: nova-bus
hook -h`, exit 2. `nova-bus show agents` prints these lines for CLAUDE.md.

**Codex** (`--codex-thread <id>`). Wake only when asleep; otherwise deliver normally. One line, run once from
the chat:

```
NOVA_BUS_REDIS=<host:port> nova-bus hook --as <name> --codex-thread "$CODEX_THREAD_ID" --service
```

Keep the computer awake, the desktop app running, and Redis available. The `codex` executable on the service's
PATH must support `queue`. `HOOK SERVICE OK` confirms the receiver started; `HOOK WAKE OK` confirms a queue
operation succeeded. Neither is a model acknowledgment: use a reply referencing the original bus id to verify
that the recipient read it. A message can be delivered again after a failure; use its id to avoid doing the same
work twice.

This service replaces the child-only receiver recipe. Stop the old receiver before starting it; one inbox has
one receiver. Child messages can reach an active parent, but do not start a new turn in an idle parent chat.
Do not add `--resume` to the queue command or resume the same chat in a second app-server.

The thread id is the chat's: `CODEX_THREAD_ID` in any command the chat runs, or the uuid ending its rollout file's
name, `${CODEX_HOME:-~/.codex}/sessions/<y>/<m>/<d>/rollout-<start>-<id>.jsonl`. The service saves each message as
`<id>.json` in `~/.local/state/nova-bus/<name>-codex-<thread>/` (mode 0600), also printed as `events=...` in
`HOOK HARNESS OK`. The queued text names
the saved file and the reply command; the message body stays in the file. The service invokes `codex queue`
with separate arguments, without a shell or `--resume`, using the installer's `PATH` and `CODEX_HOME`.

The hook waits until the rollout indicates the last turn completed and has been unchanged for 2 seconds.
While a turn is running it reports `HOOK WAKE LATER`; when the chat is next observed idle, one catch-up wake
names the messages that accumulated. A previous queued wake whose text has not yet appeared in the rollout
holds the next wake back, including after a service restart. This is an observation of the rollout, not an
atomic lock on the chat: a user can start a turn between the check and queueing. The implementation and its
formal model must not be read as a guarantee that this race is impossible.

A failed event save leaves the message on the bus and ends the hook. Once the message is saved, a durable
intent records an impending queue attempt. Exit 0 with the CLI's `Queued message` receipt records queue
acceptance. A failure to start the executable can be retried up to three times, only while the chat is observed
idle; exhaustion records `HOOK WAKE FAULT result=failed`. Restarting the hook gives each such definite failure
one further attempt. A nonzero exit, timeout, cancellation, missing success receipt, or interrupted intent has
an **unknown** outcome. It is not automatically retried: the hook reconciles it when the queued text appears
in the rollout, or keeps the saved files and fault for explicit resolution. Later messages can still proceed.

Inspect local wake state independently of the Redis inbox:

```sh
nova-bus show codex-wake --as <name> --codex-thread <thread>
```

The output lists waiting messages, intents, and faults with their saved files. Its `woken` field is an id
checkpoint, not proof that every message through that id was read: inspect the fault rows and model replies.
`nova-bus pending` counts bus
messages, so it can be empty while local wake work remains. The service log is
`~/.local/state/nova-bus/<name>.log`. On a thread change, `result=thread-changed` names outstanding files from the
old thread; they remain available for recovery rather than being silently moved to a different conversation.

To resolve a fault, stop the receiver first, retain its saved files, and check the original ids against the
existing chat and queue outcome. Use `woken` only after confirming receipt; use `retry` only after confirming
that the original request cannot still run, or deliberately accepting a possible duplicate. An absent reply
alone does not establish that nothing was queued.

```sh
NOVA_BUS_REDIS=<host:port> nova-bus hook --as <name> --service stop
nova-bus hook --as <name> --codex-thread <thread> --resolve <id>=woken
# Or, after establishing that another attempt is appropriate:
# nova-bus hook --as <name> --codex-thread <thread> --resolve <id>=retry
NOVA_BUS_REDIS=<host:port> nova-bus hook --as <name> --codex-thread <thread> --service
```

Resolution applies to the wake batch containing that id. `retry` requests one further attempt; `woken` records
the confirmed outcome. Keep deduplication by message id in the receiving workflow: queue acceptance, model
receipt, and completion of the requested work are separate events.

Under Codex a foreground hook is refused because it would block the chat; use `--service`. `recv` remains
available to child receivers, but the standard idle-wake setup is the service above. `nova-bus show
codex-agents` prints its instructions for AGENTS.md.

**Grok** (`--harness grok --grok-home <dir>`, normally `~/.grok`). Each message is saved to `<dir>/nova-bus/<id>.json`
and a line naming that file is appended to `<dir>/nova-hook.wake` (made, mode 0600, if absent). The Grok session's
Monitor tails that file first, and each appended line is its notification:

```
Monitor: tail -n 0 -F ~/.grok/nova-hook.wake
```

then the hook runs beside the session as a service: `nova-bus hook --as <name> --harness grok --grok-home ~/.grok
--service`. A wake with nothing holding the file open (no Monitor yet) fails and ends the hook: start the Monitor
first.

**Antigravity** (`--harness antigravity`). Each message is saved to `<events-dir>/<id>.json` and sent into the
conversation as a new turn with the workspace's own `language_server agentapi send-message`, with the fixed text
`nova-bus saved event <id> in <path>. Read that JSON file and follow local_receiver.`

Antigravity wakes an idle conversation only on a background task's exit, a subagent's message, or a SYSTEM_MESSAGE
file under `~/.gemini/antigravity/brain/<conversation-id>/.system_generated/messages/`. The stdout lines of a running
task never wake it, so running the hook as a task of the conversation and reading its output is not a wake; the
wake is `agentapi send-message`, whose message v1 confirmed by the file it leaves in that `messages/` directory.

The hook takes as candidates every `language_server` of this user in `ps` that carries a `--csrf_token`, those whose
`--workspace_id` is the workspace (the path or its symlink target) first. Antigravity 2.29 runs one hub server with
no `--workspace_id` at all, so the one taken is the first whose agentapi answers `get-conversation-metadata
<session>`, on its port from `lsof` (or `--antigravity-server <port>`). The program run is the process's own
executable, never its command line, and must be this user's or root's and closed to others. The token passes to
agentapi only in the environment and is cut out of every error. None answering is exit 1, naming how many candidates
were tried. A send that fails finds the server once more (Antigravity restarted) and sends again:

```sh
nova-bus hook --as <name> --harness antigravity --antigravity-dir "$PWD" \
  --antigravity-session <conversation id> --antigravity-events-dir "$HOME/.gemini/antigravity/nova-bus-<name>" --service
```

The conversation id is the directory name under `~/.gemini/antigravity/brain/` of the conversation.

**`--exec <command>`** (with the default harness) runs a command of yours per message instead of printing it: the
line on its stdin, the fields in `NOVA_BUS_ID`, `NOVA_BUS_FROM`, `NOVA_BUS_RE`, `NOVA_BUS_SUBJECT` and
`NOVA_BUS_BODY`, under the 8 s limit. Exit 0 is the delivery (`HOOK EXEC OK id=<id>`), and only then is the message
marked handled; any other exit is `HOOK EXEC FAILED id=<id> exit=<n>` on stderr and ends the hook with the message
unhandled.

### The service

A hook started inside your session blocks that session; the service runs it beside you. `--service` installs, or
updates, a per-user unit the system keeps running: `~/Library/LaunchAgents/nova-bus.hook.<name>.plist` on macOS
(KeepAlive, ThrottleInterval 10), a systemd `--user` unit `nova-bus-hook-<name>.service` elsewhere (Restart=always).
The unit runs exactly the hook command, with `NOVA_BUS_REDIS`, `NOVA_BUS_USER` and `NOVA_BUS_PASSWORD` in its
environment and never on its command line, written mode 0600, logging to `~/.local/state/nova-bus/<name>.log`. The
harness is checked in the installing process before any unit is written; then the unit starts, and the command
waits up to 45 s for its `HOOK OK name=<name>` in the log and prints `HOOK SERVICE OK name=<name> unit=<unit>
log=<log> changed=true`. Run again with the same flags it changes nothing (`changed=false`); with new flags (a new
session id) it rewrites and restarts the unit. `--service stop` stops and removes it (`state=stopped`, or
`state=absent`). Without `launchctl` or `systemctl` it refuses, exit 2. Not for `claude` without `--exec` or `--codex-thread`: that
wake is the Monitor of the session itself. The foreground form, the same command without `--service`, is for
diagnosis.

### The line

With `--harness claude` one message is one line, tab-separated:

```
<id>	<from>	<re or ->	<subject>	<body>
```

Newlines, carriage returns, tabs and backslashes inside a field are written as `\n`, `\r`, `\t` and `\\`. A message
is marked handled (XACK) only after transport delivery (its line written to stdout, or its wake answered), so a
restarted hook resumes exactly where it stopped. Delivery is at least once: a message delivered just before the hook
was killed, and not yet marked, is delivered again by the next hook; a marked message is never delivered again. The hook runs until killed and
reconnects by itself when the connection drops; a plain kill at any moment is safe.

While a hook runs it holds the key `bus:presence:<name>` (TTL 30 s, set again every 10 s, value its start time and a
token of its own) and deletes it, only if it still holds its token, on SIGINT, SIGTERM or a failed stdout. One hook
per name: a hook that finds another hook's token present waits up to 40 s for it to lapse (a restart after a
crash), then claims the name, or exits 1 if the other hook is still refreshing it. **who** prints `WHO OK present=<n>`, then `<name><TAB><uptime>` for each present name, sorted; a crashed hook
drops out within 30 s. **pending** prints `PENDING OK waiting=<n>` for `<name>`'s unacked messages, and with `--list` their
`<id><TAB><from><TAB><subject>` (the subject only, never the body), consuming nothing. To read one waiting
message whole without a hook, use **recv**, which acks nothing. Once recv has printed a message it holds the name
naming it (`who` shows `<name><TAB><uptime><TAB>recv holding <id>`) for 15 minutes, or until `ack` or the next
`recv`: a slow relay, its ack and the parent's reply all pass the hook-in gate; a reader that died leaves the
claim to lapse, and the next recv hands the message out again. **recv** prints one message (the oldest unhandled first, as hook
orders them) and exits 0 without marking it handled, or `RECV NONE name=<name> waited=<d>` on stderr and exit 1 when
`--timeout` passes with none; it records `<name>`'s presence like a hook but takes no lock, and does not coexist
with a hook for the same name (they would take the same messages). **ack** marks a received message handled:
`ACK OK id=<id>`, or `ACK FAILED ...` exit 1 when it was not received or is handled already. hook acks after
printing by itself; recv and ack are the same two steps as two commands, for a receiver that relays in between.

`nova-bus help` prints the usage.

## Environment

| Variable | |
|---|---|
| `NOVA_BUS_REDIS` | the Redis address, `host:port` (required) |
| `NOVA_BUS_USER` | the Redis ACL user (optional; else Redis's `default` user) |
| `NOVA_BUS_PASSWORD` | the Redis password (optional; never printed or logged) |

`NOVA_BUS_USER` without `NOVA_BUS_PASSWORD` is refused (exit 2): go-redis
would send no `AUTH` at all and run as `default`. The user needs the keys
`bus:*` and the commands nova-bus sends (streams, `GET`/`SET`/`DEL`, `SCAN`,
`EVALSHA`/`EVAL`, `MULTI`/`EXEC`):
`~bus:* +@stream +@string +@keyspace +@transaction +eval +evalsha +@connection -@dangerous`.
The functional test runs a server with exactly that user and `default` off.

Each process keeps one Redis connection and runs its commands in turn. The
client never retries a command by itself.

## Answering

Answer a message with `nova-bus send --as <name> --to <from> --re <id> <subject> <body>`.

## Exit codes and refusals

| Code | When |
|---|---|
| 0 | done; help; a hook stopped by SIGINT or SIGTERM |
| 1 | refused or failed: Redis unreachable or erring, `SEND FAILED`, a send to a name no hook has held, `name already hooked`, stdout failing or its reader gone, a harness that does not answer at start, a wake or exec that failed, `RECV NONE`, `ACK FAILED`, `HOOK SERVICE FAILED` |
| 2 | a malformed invocation |

A malformed invocation is refused in one stderr line,
`nova-bus <verb>: <what is wrong>; run: nova-bus <verb> -h`, with nothing on
stdout and nothing done in Redis. Missing inputs are named all at once
(`missing --as <name>, --to <name>, <subject> <body>, NOVA_BUS_REDIS
(host:port)`). Each other case is named on its own: an unknown flag, or a flag
the verb does not take (`unknown flag --x`); a flag without a value; a bad
`--list` value; a name that is not letters, digits, `.`, `_` and `-` (for
`--as` and every `--to` name); a `--re` that is not `<ms>-<seq>`; an empty
name in `--to` (`a,,b`); an empty subject; more than a subject and a body; an
argument to hook, recv, ack, who or pending ("takes no arguments"); a harness
flag the harness does not take, a missing one, a harness or events directory
that is not there or not private, a foreground hook or recv in a Codex main thread, `--exec` with a
harness, `--service` without a service manager or with claude and no
`--exec` or `--codex-thread`, a bad `--id`, a negative `--timeout`, a `show` argument that is not
`agents`, `codex-agents`, or `codex-wake`, missing name/thread fields for `show codex-wake`, or a
`--resolve` value other than `<id>=woken` or `<id>=retry`. An unknown verb gets
`nova-bus: unknown verb "<verb>"; verbs: send, hook, recv, ack, who, pending,
show, help; run: nova-bus help`. go-redis's own logging is silenced: every message on stderr is
nova-bus's.

A runtime failure keeps the one-line shape, `nova-bus <verb>: <what>`, exit 1.
An unreachable server is said plainly: `cannot reach redis at <host:port>
(NOVA_BUS_REDIS): connection refused` (or `no such host`, `timed out`, or
`connection lost` for a dropped connection: EOF, reset, broken pipe); for
send it follows `SEND FAILED to <name>:`. A hook that loses Redis logs
`nova-bus hook: <what>; reconnecting` once, then at most once a minute, while
it retries every second, and prints `HOOK RECONNECTED name=<name> waiting=<n>`
on stdout, where a Monitor sees it, once it holds the name again. Redis's own
words are never shown raw: a missing login is `redis at <addr> wants a login:
set NOVA_BUS_USER and NOVA_BUS_PASSWORD`, a wrong one `refused the login:
check ...`, one short of permission `the login lacks permission on this
stream ... ask whoever runs Redis for the bus ACL`, an address that is not
Redis (a protocol error, or a reply that does not parse) `is not talking
Redis protocol`, and anything else `redis at <addr> (NOVA_BUS_REDIS) answered
with an error; redis: <its words>`; an unreachable one adds `check that
NOVA_BUS_REDIS=<addr> is the fleet's Redis and that it is up`.

## Wire format

- The inbox of `<name>` is the stream `bus:<name>`. A message is one entry
  with the fields `from`, `to` (the recipients, trimmed, each once, joined by
  commas), `subject`, `body`
  and `re` (empty when none), all strings.
- Every hook, and recv, reads with the consumer group `hook` and the consumer
  `hook`. The group is created at id `0` on the first hook or recv, so
  messages sent before it are delivered too; its existence is what send
  checks (`XINFO GROUPS`) before sending to a name without `--new`.
- A hook's line is tab-separated:

  ```
  <id>	<from>	<re or ->	<subject>	<body>
  ```

  Backslash, newline, carriage return and tab inside a field are written as
  `\\`, `\n`, `\r` and `\t`, so a line never splits. pending escapes the
  subject the same way.
- Presence of `<name>` is the string key `bus:presence:<name>`, whose value is
  the hook's start time (RFC 3339 UTC, to the second), a tab, and a random
  token that process made at start; recv sets it with the token `recv` and
  the time of the first recv.

## Delivery

A hook reads its pending entries first (`XREADGROUP ... 0`: handed to a hook
or a recv and never acked), then new ones (`XREADGROUP ... >`, up to 100 at a
time). For each entry it delivers it (writes the line, runs the `--exec`
command, saves the Codex event and prints its line, or wakes the session), then acks it and deletes it (`XACK` and
`XDEL` in one `MULTI`), so an inbox holds only what is waiting. The ack runs
to the end even if a signal arrives after the write, and no further line is
written once one has. recv is the first half (read and print) and ack the
second, as two commands. For the Grok, Antigravity, and exec adapters, a delivery that surely failed (a wake that did not
answer, an exec that exited non-zero) ends the hook with the entry unacked
and nothing behind it delivered, so the order is kept and the next hook
starts from the pending list; a wake whose outcome is unknown is acked, so a
turn is never started twice. Presence is refreshed when due before every
delivery, and a delivery is bounded by 8 s, so a batch never outlives the
key.

For Codex, a successful bus delivery transfers the message to the saved event file; the wake can still be
waiting or faulted. A save failure leaves it on the bus. Queue failures, retries, and explicit resolution follow
the Codex procedure above. Redis acknowledgment does not certify that the model has read or acted on it.

For the bus transport:

- a restarted hook resumes where the last one stopped;
- delivery is at least once: a hook killed after writing a line and before
  acking it leaves the entry pending, and the next hook prints it again;
- an acked message is never printed again;
- an entry send did not write (no `from` or `subject`: something else wrote
  the stream) is not mail: the hook prints `HOOK BAD id=<id> missing=<fields>`
  on stdout and acks it, so it never blocks the inbox; recv says `RECV BAD`
  on stderr, acks it, and exits 1, so the next recv reads on;
- a read whose reply is lost (the connection dropped after the server handed
  the batch) is not retried: the hook reconnects after a second and starts
  again from the pending list, so the batch is printed then.

**No retries.** A send that fails prints `nova-bus: SEND FAILED to <name>:
<error>` and exits 1; the names before it were sent (their lines are on
stdout), the names after it were not. The tool never retries a send: if the
connection dropped mid-command the message may or may not have been
appended, so retrying is the caller's choice, knowing that it can duplicate.

Because an ack deletes its entry, a stream holds exactly the messages no hook
has acked. pending's count is one `XLEN`; `--list` reads the stream with
`XRANGE`, 1000 entries at a time, and is O(entries waiting).

A stream written by an older build, whose acks did not delete, may still hold
acked history. That history is removed the first time a hook finds unacked
entries pending: the first pending entry is the oldest unacked one, so the hook
trims everything older (`XTRIM MINID <that id>`) before printing. A new
(undelivered) entry is never the trim point, because another hook, paused
past its TTL, may hold an older entry it has been handed and not yet printed.
Until then pending counts that history too.

An inbox nobody hooks is never trimmed, by design: its messages wait for
their reader, however long that takes.

## Presence

Presence means one thing: a hook process is alive for this name, or a recv is waiting on it (the value then ends
in `recv`, from the first recv; a recv claims again every 10 s while it waits, and a hook is refused by a recv's key
only until it lapses).

- **One hook per name.** On start the hook claims the key: one Lua script sets
  it with a 30 s TTL unless it holds another process's value. If it does, the
  hook prints `HOOK WAIT name=<name> held_since=<holder's start> ttl=30s
  budget=40s` to stderr and tries again every second for at most 40 s (the TTL
  plus one refresh). A hook restarted by a
  supervisor after a crash so waits out the dead hook's key and claims the
  name within about 30 s. If the key is still held after 40 s, another hook is
  alive and refreshing it: the hook prints `nova-bus hook: name already
  hooked: <name> after 40s; run: nova-bus who` and exits 1, touching nothing.
  A signal while waiting exits 0.
- It claims again whenever 10 s have passed. Each read waits at most 1 s, so a
  quiet or a busy hook refreshes on time, and a signal is acted on within a
  second. A refresh that finds another value (this hook lapsed and another
  took the name) ends this hook with `name taken by another hook while this
  one lapsed`, exit 1.
- On a clean exit (SIGINT, SIGTERM, stdout failing) it releases the key: a
  second script deletes it only if it still holds this process's value. A
  release that fails leaves the key to its TTL.
- After every read (at most 1 s) the hook polls stdout (poll(2) on Darwin,
  ppoll(2) on Linux): a pipe whose reader has gone (the Monitor or the
  session ended) ends it, `stdout closed: its reader is gone`, exit 1, its
  presence released, within a second. SIGPIPE is ignored too, so a write to
  a gone reader fails with EPIPE and ends it the same way, the unprinted
  message left pending.
- who scans `bus:presence:*` and reads each key; one that expires in between
  is left out. Uptime is the reader's clock minus the start time; clocks that
  disagree skew it, and a negative uptime shows as `0s`.

## Failure

- **A hook dies before acking.** The message stays pending and is printed again
  by the next hook on that name.
- **A hook crashes or its machine is lost.** Its presence lapses within 30 s;
  until then who lists it, and a new hook on that name waits for it.
- **stdout blocks** (the reader stopped reading). The hook cannot refresh, so
  its presence lapses within 30 s while the process lives.
- **A Grok/Antigravity wake or exec fails.** `HOOK WAKE FAILED` or `HOOK EXEC FAILED` on
  stderr; the hook exits 1 with that message and every later one unhandled,
  in order (`pending --list` shows them); the next hook, or the service
  restarting it, delivers them. A wake cut off with its outcome unknown is
  acked.
- **A recv's reader dies before its ack.** The message stays pending and the
  next recv prints it again; the reader sees it twice, by its id.
- **Redis is unreachable.** send, recv, ack, pending and who exit 1. A hook logs
  `nova-bus: <error>; reconnecting` to stderr and retries every second; its
  presence may lapse meanwhile, and it claims again when Redis is back.

## An example session

Two names, alpha and beta, with `NOVA_BUS_REDIS` set in both shells. Ids and
uptimes vary.

```
beta$  nova-bus hook --as beta
alpha$ nova-bus who
WHO OK present=1
beta	4s
alpha$ nova-bus send --as alpha --to beta "build" "main is green"
beta 1730800000000-0
beta:  1730800000000-0	alpha	-	build	main is green
beta$  nova-bus send --as beta --to alpha --re 1730800000000-0 "re: build" "thanks"
alpha 1730800005000-0
alpha$ nova-bus pending --as alpha --list
PENDING OK waiting=1
1730800005000-0	beta	re: build
alpha$ nova-bus hook --as alpha
1730800005000-0	beta	1730800000000-0	re: build	thanks
```

The line marked `beta:` is what beta's hook prints. alpha had no hook running,
so beta's answer waited in alpha's inbox, and pending showed it without
consuming it; alpha's hook printed it as soon as it started.

## Build and test

```
go build -o nova-bus .
go test .                           # unit tests, an in-memory store
go test -tags functional .          # against a real redis-server on a free local port (skipped without one)
```

[SPEC.md](SPEC.md) has the tests and models. `tla/Presence.tla` models the presence key and hooks that compete for it;
`tla/CASES.tsv` names every case and what TLC must find (`tests/tla/check.sh JAR bus/tla` runs them). `tla/Bus.tla` models the inbox, the sender and a hook that crashes between print and ack: no message is acked before
it is printed, no acked message is printed again, every sent message is printed. `tla/BusBroken*.cfg` are reversed witnesses (ack before print; a restarted hook that skips its pending
messages; a failed wake that acks; a failed wake that does not end the hook, so the message is never delivered)
that TLC must fail. `tla/BusWake.cfg` adds wakes that fail: every message is still delivered, because the failure
ends the hook and the next one starts from the pending list. `tla/PresenceBrokenSlowBatch.cfg` shows the key lapsing
under a hook whose delivery is not bounded.
