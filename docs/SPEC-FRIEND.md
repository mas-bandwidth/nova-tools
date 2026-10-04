# SPEC-FRIEND: what a friend runs to be part of the team

The tool is `nova-friend`; the rules are `internal/friend`; the machine is
`tla/Friend.tla`. The contract was settled on 2026-10-04 between the owner and
the coordinator: "everything that a friend needs in any harness, to be a part
of the team", "it must be this way when they start up next time, not just now,
but always", and "like a network connection: client/server and the coordinator
is the server; both sides need to know they are connected, continually".

## The pattern in one sentence

One daemon per friend, started by launchd and never by the model, parks on the
friend's nova-bus2 stream and pushes each message into the running session as
a turn; it beats to the sprint server while that loop runs and only then; it
answers the coordinator's ping at once and pushes the ping in, and the
session's own answer, a turn carrying the ping's nonce, is the only thing that
makes the friend up.

## The data

- The bus: the friend's stream `bus2:to:<friend>` (SPEC-BUS2.md). A message is
  pending from the read until the session's turn ends at exit 0, so a daemon
  that dies mid-turn is handed the message again once its claim opens, fifteen
  minutes after the read (`ClaimAfter`, SPEC-BUS2.md: longer than the longest
  turn, so a live daemon mid-turn is never handed its message twice).
- The files, one writer each. The state files live in the state directory,
  `~/.nova-friend/<friend>` under the home directory unless `--state-dir` names
  another, never on the friend's volume (a background process on this platform
  may not touch a removable volume without the person's permission; measured
  2026-10-04, the mkdir refused with "operation not permitted"): `status.json`
  (the daemon: its state, rewritten whole every five seconds and when it
  changes; a reader calls the daemon up while the file is under thirty seconds
  old), `pong.json` (the `pong` verb: the session's last answer), `deliver.log`
  (the daemon: one line per delivery), and `session.json` (the local
  `sleep`/`wake` commands and daemon: the selected coordinator name and the
  persistent asleep marker). The queue file is under the friend's
  working directory: `inbox/QUEUE.json` (the coordinator and the session: one
  record per task with `id`, `state` of queued, working or done, and
  `deliverable`).
- The launchd agent `com.nova.friend-<friend>`: RunAtLoad, KeepAlive, a five
  second throttle. launchd opens its own log before the daemon runs and cannot
  open one on a network volume (EX_CONFIG, measured 2026-10-03), so that log
  is under the home directory, beside the state directory.

## The protocol

- `PING <nonce>`: from the coordinator, subject `PING <nonce>`, body the nonce,
  `seat=<coordinator> since=<RFC3339>`, and the one `nova-friend pong` line the
  session runs. The nonce is six random characters.
- `daemon-pong <nonce>`: the daemon's answer, sent the moment the ping is read,
  or seen by a peek while a turn is running. It proves transport.
- `pong <nonce> queue=<n> working=<n> width=<n>`: the session's answer, sent by
  `nova-friend pong` as the session's own turn and recorded in the pong file.
  It proves the AI. The coordinator reads it from the friend's own stream (the
  message's `from`), never from the body. The `from` field is routing metadata,
  not authentication; the bus store currently has no authentication.
- Width is the nova-config friend row's, given to the daemon at install.

## The machine (tla/Friend.tla)

The connection: `connected` while a ping arrived within the window (three
minutes, one number on both sides); `silent` after a window without one, said
to the session as a turn exactly once per outage, and `coordinator back` once
when pings resume, naming the seat. The challenge: `quiet`; `challenged` from a
ping until the session's pong with that nonce; `deaf` after a window challenged
with no pong, until a pong. A repeated current nonce refreshes the connection
but neither restarts the challenge deadline nor reopens an answered challenge.
Only the current nonce answers: a stale or replayed
pong changes nothing. Up is `quiet` with at least one pong; the daemon's beat
never makes a friend up.

The invariants, each with a reversed witness that TLC catches: a friend is up
only after a session pong; a challenge is open for less than a window; the
outage is said exactly once; only the current nonce ends a challenge; and a
challenge ends.

`run` and `install` accept `--coordinator <name>` separately from the sprint
server's `--server <addr>`. A run saves an explicit coordinator in
`session.json`; install carries the option into the launch agent, and its run
saves it when the agent starts. The coordinator must be a valid nova-bus2 name
(lowercase ASCII letters, digits and hyphens, up to 64 characters), because
automatic wake compares it with a nova-bus2 message's `From` value.
`nova-friend sleep --as <me>` records a local asleep marker; it requires a
coordinator name, supplied with `--coordinator` or already saved in
`session.json`. The daemon reports that marker with
`nova-sprint friend beat --asleep <me>` on its next beat. `nova-friend wake
--as <me>` explicitly clears the marker and needs no coordinator, so it can
recover a saved asleep state with missing coordinator configuration. Neither
command sends a wake message or proves the harness can wake. Startup itself
does not clear an asleep marker; a matching coordinator message can wake the
session while the daemon recovers pending work. If the saved coordinator is
absent, startup refuses and says to run `wake` or supply `--coordinator`.

## Asleep behavior (A3 implementation checkpoint)

The durable session record contains one asleep bit, one configured coordinator,
and at most one `WakeBarrier` stream-entry ID. A message whose `From` equals
the configured coordinator can wake the session, regardless of its subject or
body. `From` is routing metadata, not authentication, and there is no freshness
check: a stale or failed redelivery from that coordinator can wake the session
again. While its ID remains the saved active `WakeBarrier`, an entry already
used to wake the session is not reused as a second wake after local sleep or
daemon restart. Once a non-`Deferred` completion clears that barrier, a later
redelivery can wake again; this does not guarantee exactly-once or fresh wake
signals.

While asleep, the daemon continues its beat with `asleep=true`; a ping still
gets a `daemon-pong` marked `asleep=true` if it does not itself wake the
session. A matching coordinator message first commits awake state; if that
message is a ping, its `daemon-pong` reports awake. The active daemon may read
ordinary messages into its own pending-entry list, but does not deliver them,
charge a failure, or acknowledge them. It keeps entry IDs in memory and fetches
a body when a delivery starts. The bus's ordinary 15-minute claim behavior is
unchanged. Active-daemon startup first recovers every page of the daemon's own
pending-entry list, in batches of 128, before dispatching
work; a recovery error is retried without dispatch. It does not recover
another consumer's entries.

After a matching coordinator message wakes the session, that message's entry
is the single durable barrier: it gets the first non-`Deferred` delivery
attempt, then held entries run in numeric stream-ID order. Synthetic daemon
notices precede ordinary held entries, except that the barrier stays first. A
`Deferred` result does not clear the barrier, count as a delivery failure, or
acknowledge the entry. Local sleep/wake and daemon restart preserve the barrier
until its delivery completes non-`Deferred`. If local sleep arrives while that
retry is deferred, the daemon parks it; a later wake retries the barrier first.
Any non-`Deferred` completion clears the barrier, including a failed turn; that
failure uses the normal retry count, and a third non-`Deferred` failure
requests the usual give-up acknowledgement. Reserving a delivery under the
session-state lock is its start boundary; harness I/O begins after unlock, so a
later sleep does not cancel an already reserved turn. One daemon owns a state
directory at a time. If persisting a completed barrier clear returns an error,
the daemon stops before dispatching later held entries.

Sleep pauses the friend machine's unanswered-challenge clock, while its
transport-connection timer can still advance, and keeps `Up` false. A daemon
beat or ping response while asleep does not make the session up. Passive
harnesses only peek: they do not claim, acknowledge, or deliver a native turn.
For a matching `From`, they persist the resulting awake state but remember the
observed entry ID only in process memory. They suppress repeats for that entry
during one process lifetime; because the message remains unconsumed, it may be
observed again after restart.

The separate `tla/MCFriendSleep.tla` model is a finite safety projection. Its
new cases are drafts awaiting Zhi's parse/TLC review; no run record is claimed
here. It abstracts store/file failures, locking and cross-layer refinement,
and does not cover pagination beyond 1,000 entries, authorization, native
delivery, or liveness. Selected Go tests cover ACK-failure and state-error
cases, but this projection does not prove those paths executed or establish
cross-layer behavior.

## The loop (internal/friend/daemon.go)

Each second: the clock is stepped; when the session is free, one read of the
stream (a ping is answered by the daemon at once and the message is handed to
the adapter, which blocks for the whole turn, and acked when the turn ends at
exit 0; any other exit leaves it pending, handed in again when its claim opens,
and the third failure acks it with `given_up=true` on the record, so a message
the session cannot take never comes back for ever; a delivery the adapter
defers, `Deferred`, the session unable to take a turn now with nothing wrong,
such as a Codex thread open in the app holding its writer lock, is neither a
failure nor an ack: the message stays in the daemon's hand, tried again every
ten seconds, `RecheckEvery`, and counted toward nothing, so a chat open all
day loses no message, and the record says so at the first deferral and once a
minute after), else one peek, so a ping that
lands during a long turn is still answered at once by the daemon and pushed
in once the session is free; the worker's result; one beat to the sprint
server (`friend beat [--asleep] <friend>`; the coordinator name is session
configuration, while queue, working and width flags are owed on the server's
side); the pong file, while a challenge is
open; the status file.

The deliver adapter runs the harness directly, never through a shell, as its
own session leader; past the ten minute budget the whole process group is
signalled, SIGTERM then SIGKILL, so a harness that forks leaves no orphan.
The delivery routes include OpenCode: `opencode run --session <id> --dir
<dir> <text>`, the newest session of the directory when none is named. A ping
pushed in carries, at its head, the exact `pong` line for this friend (the
binary by path, the name, the directory, the store, the nonce), so a small
model has one line to run and nothing to fill in.

Codex delivers by resume, not into the open chat: `codex exec resume
--skip-git-repo-check <thread> <text>` resumes the saved thread in a new codex
process, so the thread's model answers with the friend's whole context, and
the record labels every such turn "answered by resume, not by the open
chat". The open chat itself is out of reach: the Codex desktop app
(ChatGPT.app) runs its app-server on a stdio pair it owns and listens on no
socket, and while a thread is open there the app holds its writer lock
(`~/.codex/thread-writer-locks/<thread>.lock`), which refuses a resume
("thread <id> already has an active writer", measured 2026-10-04 on an open
thread, exit 1). Without --session, the adapter resolves the newest saved thread with the
same working directory from session_meta headers and session_index updated_at
under CODEX_HOME (otherwise ~/.codex); without a matching thread it refuses
with a remedy rather than spawning codex. It probes that exact thread's writer
lock first (the same flock codex takes). While held, it returns Deferred without
running codex: no failure is counted and nothing is acked or given up, even
after 1,000 deferrals. The daemon retries every ten seconds. The probe releases
its brief exclusive lock before resume, so a writer can still acquire it in
that window; this observation is not a reservation. Reaching the open chat needs the app on the shared local daemon:
the app connects to `~/.codex/app-server-control/app-server-control.sock`
instead of its own stdio server only when launched with
`CODEX_APP_SERVER_USE_LOCAL_DAEMON=1` and a daemon is already up (`codex
app-server daemon start`; read from the app bundle, unverified); then `codex
queue --thread <id> --message <text>` reaches the open chat, and the adapter
should move to it.

Claude has no deliver command yet: its daemon is
passive, taking nothing off the stream (the session's own blocking read does), peeking so a ping is
still answered by the daemon at once, beating, and recording a push it
cannot deliver; so the tool is honest, and the beat and the daemon pong are
real for them.

### Antigravity

Antigravity (Google's agent IDE, 2.19.1 as measured) has no deliver command
of its own, but its agents message each other through the language server,
and that channel takes a message from outside: `agentapi send-message
--title=nova-friend <conversation> <text>`, `agentapi` being the wrapper under
`~/.gemini/antigravity/bin` that execs the app's `language_server`. The
server writes the text into the conversation's mailbox,
`~/.gemini/antigravity/brain/<conversation>/.system_generated/messages/`, as
a high-priority message, its watcher starts a turn on it, and the session
marks it read in `read.json` there as it takes it. Measured 2026-10-04 08:52
ET: sent at :28, the turn's first step at :32, the friend's "got it" on
nova-bus2 at :35.

The adapter finds everything each delivery, so a restarted app is found
again: the server's pid and CSRF token off `ps -axo user=,pid=,args=` for the daemon's own user (the
`language_server` with `--override_ide_name antigravity` and its
`--csrf_token`), its listening ports from `lsof -Fn`, and the port that
answers `get-conversation-metadata` for the conversation (the other is TLS).
Without `--session`, the conversation is the newest root conversation whose
workspace is the friend's directory (or its real path), from the harness's
`conversation_summaries.db`, read immutable through `sqlite3 -json`; that
table is written when a turn ends, so it names the session and never the
turn. The command runs through `/usr/bin/env` with
`ANTIGRAVITY_LS_ADDRESS` and `ANTIGRAVITY_CSRF_TOKEN` set, the text an
argument; the token is already on the server's own command line, readable
by every process of the login, so the delivery exposes nothing the harness
does not.

The ack: `agentapi` exits 0 on an error too (a wrong conversation, a missing
token print `"error"` in its JSON), so the JSON is read and its exit code is
not. The delivery is exit 0 once a new message titled exactly `nova-friend` has appeared in the mailbox
and `read.json` marks it read, polled every half second for two minutes; past
that it is exit 1 with the message id, the message still in the mailbox for
the session's next turn, and the daemon redelivers (a duplicate, never a
loss). The turn the message starts runs on after the ack: a second message
queues in the mailbox rather than waiting for the turn, which is the
harness's own order for its agents. The mailbox and `agentapi` are the
harness's internals for its subagents and scheduled tasks, not a documented
API; a release that moves them breaks this adapter, and the functional test
(`NOVA_FRIEND_ANTIGRAVITY_DIR`) says so.
Grok, the Grok Build TUI (xAI's `grok`), has a delivery adapter through the
only door the open window has. The harness has no deliver verb, no leader
socket unless leader mode is on, and `grok -p <text> --resume <id>` runs the
turn in a second process over the same transcript, not in the window the
friend is in. What the window has is its monitor tool: a background task
whose every new output line becomes a notification in the conversation and
wakes the agent for a turn (the harness's guide, `20-background-tasks.md`).
The friend's session runs one over a wake file, `tail -n 0 -F <file>.wake`,
and a line appended to that file arrives as a `<monitor-event>` user turn
(measured 2026-10-04 in a friend's session). The adapter finds the window open
in `--dir` in the harness's `~/.grok/active_sessions.json` (pid and cwd), the
`tail` under that pid in `ps -axww -o pid=,ppid=,args=`, and appends the text
as one line, `nova-friend: <text>` with each newline shown as ` ⏎ ` (the
monitor makes an event per line, and a flood of lines is how the harness
stops a monitor). `--session`, for grok, names the wake file, which must be
the one tailed. The monitor uses `tail -n 0 -F <file>.wake`; its wake path
must be absolute and contain no whitespace. Since `ps` does not preserve
argument boundaries, ambiguous or multiple operands refuse with
`the monitor's wake path must be absolute` rather than selecting a truncated
path. The delivery is accepted at exit 0 once the line is in the
file under a running tail; the turn runs after the adapter returns, since
nothing hands its end back, so a second message can land during a turn and
is the next event. Refused, with the line to run in the session, when no
window is open in the directory or the window runs no monitor over a wake
file; a stale pid in `active_sessions.json` is no window.

## Identity

The friend's name comes from one place, `install --as`, written into the
agent's command line; the daemon never takes a name from a message. The `pong`
verb refuses a name that is not the one the daemon in that directory runs as.
The optional coordinator name is local configuration for asleep-state
reporting, not a credential or authorization fence. Binding friend names to the
store's login is owed: the bus store runs with no authentication tonight
(SPEC-BUS2.md), and `nova-sprint friend beat <name>` beats whatever name it is
sent.

## What is weak, and known

A background process on this platform needs the user's permission to touch a
removable volume (the TCC service for removable volumes; measured 2026-10-04:
the daemon, and a plain `touch` launchd starts, both refused with "operation
not permitted" where the same commands from a shell succeed, and tccd logged
the access request). `install` resolves the agent binary's symlinks and refuses
an executable under `/Volumes` before it writes the plist or invokes
`launchctl`; the executable must live on local disk. The permission needed to
run a harness whose working directory is on a removable volume is granted to
the binary in the system's privacy settings, by the person, never by the tool,
and a rebuilt binary is a new one to it. The state files are out of its way,
under the home directory; until permission is granted the daemon beats and
answers the daemon pong but cannot run the harness on the volume, and the
record says so.

The server side of the ping (the coordinator pinging every friend each window
from the sprint's run loop, and the table's `awake` and `deaf` columns) is not
here; `ping` and `wait-pong` run the canary by hand. The beat carries no
numbers until `friend beat` takes them. A session that reads the bus itself
(the stub harnesses) proves nothing to the daemon until it runs `pong`.

## The harness survey (2026-10-04)

The survey covers all major harnesses. The rule of the
survey: a push is a turn landing in the friend's EXISTING session, which the
model answers; a resume with a new prompt counts when the same session
continues (the same id, the same record); an app-server, IPC or MCP that takes
a user turn counts; a URL scheme that opens a new chat does not. Installed
means on the survey machine, that day. Claude, Codex, Grok and Antigravity are other
children's; OpenCode is above.

| harness | installed here | push route | command or frame | proven | needs from the owner |
|---|---|---|---|---|---|
| dsh (DeepSeek Harness) | yes: `/Applications/DeepSeek Harness.app`, v0.2.0-rc.2, CLI at `Contents/Resources/runtime/cli/bin/dsh`, nothing on PATH | the headless profile adopts a persisted session (`~/.dsh/sessions/<key>/<id>`), shared with the desktop app | `dsh headless --session-id <id> -` in the friend's dir, text on stdin; newest `session-*` of `<key>` when none is named | yes, 2026-10-04 on a throwaway session: adopted (turn 2 in the same record, 9 KB to 16 KB); unknown id exit 1; another directory exit 1 ("recorded in"); the turn itself stopped at the provider: `MISSING_CREDENTIAL`, 0 tokens spent | store DEEPSEEK_API_KEY for the headless profile (the web Models page, or the daemon's environment); the desktop app's key is not seen by it |
| gemini (Gemini CLI) | yes: `/opt/homebrew/bin/gemini` 0.46.0 (brew gemini-cli) | `--resume <uuid>` keeps the session id and chat file (`ChatRecordingService.initialize`, read in the bundle); `latest` is the project's newest | `gemini --skip-trust --resume <id\|latest> --prompt=<text>` in the friend's dir | mechanics only, 2026-10-04: a session file was written under `~/.gemini/tmp/<project>/chats/`, `--resume <bad uuid>` exits 42; the turn itself never ran: the account answered 429 `rateLimitExceeded`, then `IneligibleTierError: this client is no longer supported for Gemini Code Assist for individuals`; no token spent | a GEMINI_API_KEY in the daemon's environment, or a Code Assist tier that still serves the CLI (the individual tier no longer does, 8:58 AM ET) |
| copilot (GitHub Copilot CLI) | no | programmatic mode resumes a session: `-p` with `--resume`; session state under `~/.copilot/session-state/`; an SDK talks JSON-RPC to `copilot --headless` | `copilot -p <text> --resume <id> --allow-all-tools -s` | no | `curl -fsSL https://gh.io/copilot-install \| bash` and `copilot login` |
| cursor (cursor-agent, the app) | no (no `agent`, no Cursor.app) | the CLI resumes a chat by id; the app has no documented IPC into an open chat | `agent -p --resume <chatId> --output-format text <text>` | no | `curl https://cursor.com/install -fsS \| bash` and `agent login` (or CURSOR_API_KEY) |
| amp | no | execute mode into a thread | `amp -x --thread-id <id> <text>` | no | install and AMP_API_KEY (`sgamp_...`) |
| goose | no | `goose run` resumes a named session | `goose run -n <name> -r -t <text>` | no | install and a provider key (`goose configure`) |
| kiro (kiro-cli, the app) | no | `kiro-cli chat` resumes a session by id; the app has no documented IPC | `kiro-cli chat --resume-id <id> --no-interactive <text>` | no | install and `kiro-cli login` (Builder ID) |
| cline (CLI, VS Code) | no (no VS Code, no `cline`) | the CLI runs a session by id against the Cline hub (`CLINE_HUB_ADDRESS`, 127.0.0.1:25463) | `cline --id <session> --auto-approve true <text>` | no | `npm i -g cline` and `cline auth` |
| aider | no | none: no session to adopt; `--restore-chat-history` replays a (summarised) history file into a new process | — | — | — |
| roo (Roo Code) | no | none from outside: `RooCodeAPI.sendMessage` is in-process VS Code, for other extensions; the IPC socket starts and cancels tasks | — | — | — |
| windsurf | no | none documented into a running Cascade | — | — | — |
| zed | no | none: Zed is the ACP client; no CLI, scheme or IPC reaches a thread | — | — | — |
| warp (Oz) | no | none local: `oz run message send` is between cloud runs; `oz agent run` starts a new run | — | — | — |

The two installed harnesses have adapters (`adapter_dsh.go`, `adapter_gemini.go`);
the rest are `Stub` with a surveyed reason (`adapter_refused.go`): known to
`install --harness`, passive, refusing every delivery with the one-line reason above.
Not measured: whether `dsh headless` adopts a session the desktop app holds
open (a `session.lock` sits in every session directory), and whether the
desktop app shows the pushed turn live or on its next load.
