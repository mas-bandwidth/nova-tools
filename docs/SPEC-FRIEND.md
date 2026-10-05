# SPEC-FRIEND: what a friend runs to be part of the team

The tool is `nova-friend`; the rules are `internal/friend`; the machine is
`tla/Friend.tla`. The contract was settled on 2026-10-04 between the owner and
the coordinator: "everything that a friend needs in any harness, to be a part
of the team", "it must be this way when they start up next time, not just now,
but always", and "like a network connection: client/server and the coordinator
is the server; both sides need to know they are connected, continually".

## The pattern in one sentence

One daemon per friend, started by launchd and never by the model, parks on the
friend's nova-bus stream and, whenever the session is free, pushes every
message waiting into the running session as one turn; it beats to the sprint
server while that loop runs and only then; it answers the coordinator's ping
at once and never makes a turn of it, and the session's own answer, the pong
line that rides at the head of its next turn, is the only thing that makes the
friend up.

## The data

- The bus: the friend's stream `bus2:to:<friend>` (SPEC-BUS.md). A message is
  pending from the read until the session's turn ends at exit 0, so a daemon
  that dies mid-turn is handed the message again once its claim opens, fifteen
  minutes after the read (`ClaimAfter`, SPEC-BUS.md). A turn may run longer
  than that: the daemon reads nothing while a turn runs (it only peeks), so a
  live daemon mid-turn is never handed its own message twice.
- The files, one writer each. The state files live in the state directory,
  `~/.nova-friend/<friend>` under the home directory unless `--state-dir` names
  another, never on the friend's volume (a background process on this platform
  may not touch a removable volume without the person's permission; measured
  2026-10-04, the mkdir refused with "operation not permitted"): `status.json`
  (the daemon: its state, rewritten whole every five seconds and when it
  changes; a reader calls the daemon up while the file is under thirty seconds
  old), `pong.json` (the `pong` verb: the session's last answer), `deliver.log`
  (the daemon: one line per delivery). The queue file is under the friend's
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
  or seen by a peek while a turn is running. It proves transport. The ping is
  then acked by the daemon and never pushed in as a turn (the finding of
  2026-10-04: pings every few seconds were turns of their own and buried the
  work).
- `pong <nonce> queue=<n> working=<n> width=<n>`: the session's answer, sent by
  `nova-friend pong`, recorded in the pong file. While a challenge is open, the
  exact pong line for the current nonce rides at the head of the next turn
  that carries messages, and the session runs it first. It proves the AI. The
  coordinator reads it from the friend's own stream (the message's `from`),
  never from the body: a pong is forgeable only by the friend's login.
- Width is the nova-config friend row's, given to the daemon at install.

## The machine (tla/Friend.tla)

The connection: `connected` while a ping arrived within the window (three
minutes, one number on both sides); `silent` after a window without one, said
exactly once per outage, and `coordinator back` once when pings resume, naming
the seat. The machine says each once; the daemon never makes a turn of either:
the word collapses to the latest state and rides at the head of the next turn
that carries messages, and a `coordinator back` the session never needed (it
never heard `silent`) is dropped. The challenge: `quiet`; `challenged` from a
ping until the session's pong with that nonce; `deaf` after a window challenged
with no pong, until a pong. Only the current nonce answers: a stale or replayed
pong changes nothing. Up is `quiet` with at least one pong; the daemon's beat
never makes a friend up.

The invariants, each with a reversed witness that TLC catches: a friend is up
only after a session pong; a challenge is open for less than a window; the
outage is said exactly once; only the current nonce ends a challenge; and a
challenge ends.

## The loop (internal/friend/daemon.go)

Each second: the clock is stepped; when the session is free, every message
waiting is read off the stream (a ping is answered by the daemon at once and
acked, never pushed in), and one turn is started with all of them, oldest
first, at most 32 messages or 256 KiB (`MaxBatch`, `BatchBytes`; the rest is
the next turn): one envelope listing each message's id, from and subject, with
the message as `nova-bus recv` prints it, the pong line first while a challenge
is open, and the daemon's latest word about the coordinator; a single message
with nothing else is its `recv` text alone. The adapter blocks for the whole
turn; exit 0 acks every message it carried, together (for Codex queue, exit 0 is the
command accepting the input, not the turn ending). Any other exit leaves
them pending, handed in again when their claims open, and the third failure
acks a message with `given_up=true` on the record, so a message the session
cannot take never comes back for ever. A delivery the adapter defers,
`Deferred`, the session unable to take a turn now with nothing wrong, such as
both Codex delivery commands being unavailable, is neither a failure nor
an ack: the turn stays in the daemon's hand, tried again every ten seconds,
`RecheckEvery`, and counted toward nothing, so a chat open all day loses no
message, and the record says so at the first deferral and once a minute after.
While a turn runs: one peek, so a ping that lands during a long turn is still
answered at once by the daemon; never a second turn. Then the worker's result;
one beat to the sprint server (`friend beat <friend>`, a plain beat: the queue,
working and width flags are owed on the server's side); the pong file, while a
challenge is open; the status file.

No clock bounds a turn: a turn that prints keeps running however long it
takes. A turn that has printed nothing, on stdout or stderr, for `--silent-stop`
(twenty minutes by default) is stopped, its process group signalled, and the
record says so with the reason (`stopping: no output for 20m0s`, then
`stopped=` on the turn's line); its messages count one failed delivery each.
The finding of 2026-10-04: a fixed ten-minute cap killed a friend's real work
mid-turn.

A session the provider refuses is broken, not its messages. An adapter that
sees the turn's output (OpenCode, Codex, DSH, Gemini) reads a provider's JSON
error, `"type":"<x>_error"`, from a failed turn and answers `ProviderRefused`
with the session and the reason; `rate_limit_error`, `overloaded_error` and
`api_error` pass by themselves and are ordinary failures. A refused turn
counts toward nothing a message owns: its messages stay pending, never given
up. The same refusal on `--broken-after` turns in a row (three by default) marks
the session broken: the daemon delivers nothing more into it, only peeks, so
pings are still answered and every message stays pending; the status file and
`status` say `session=broken session_id= broken_at= reason=`; and the
coordinator is told once on the bus, `friend <name>: session <id> broken:
<reason>`, to the seat the last ping named, else `--coordinator`. It stays
broken until the daemon restarts (install again, or `launchctl kickstart -k`),
which is how a renewed session is taken up. The finding of 2026-10-04:
a friend's session refused every turn with `invalid_request_error` for two hours
and nothing said so.

A harness at its usage limit or out of credits is down until its reset,
woken after it, and its measured usage rides on its beat
(`internal/friend/limit.go`; the finding of 2026-10-04: a friend's harness
stopped on "Insufficient AI Credits ... will refresh 6:52 PM" while her row
read up with six working cards, and four Claude accounts ran out of their
weekly usage unseen). `Limits.Watch` reads every command an adapter runs:
Claude Code's stream-json `rate_limit_event` (`rate_limit_info`: `status`
allowed, allowed_warning or rejected; `isUsingOverage`; `unifiedWindows`
`five_hour` and `seven_day`, each `utilization` 0 to 1 and `resetsAt` in epoch
seconds), the last one anywhere in the output, is the measured usage, and a
rejection is a limit until the spent window resets; otherwise a line in the
last 2 KiB that says a limit or credits (`insufficient ... credits`, `usage
limit`, `limit reached`, `hit your limit`, `quota exceeded`) with its reset
beside it (a clock time, today or else tomorrow in the daemon's zone; `in N
hours`; an epoch after `limit reached|`) is a limit. A line with no reset is
no limit, and a provider's transient `rate_limit_error` is no limit, so a
reply that only talks about limits sends no one down. A harness on paid
overage reads down, until the spent window resets, unless the owner allows
overage for that friend (`AllowOverage`). Each new limit calls `Down` once
with its reset (`friend down --until`, the status cell `down (<reason>, until
<time>)`). `Limits.Gate` holds the session: a delivery while it is down is
`Deferred` without running the harness, so its messages stay in hand, counted
toward nothing; a turn that hits a limit is `Deferred` the same way; the
first delivery after the reset is a wake turn (`WakeText`) whose output must
carry its nonce, six fresh characters each try, so only the session that ran
this turn answers it; then `Up` with the nonce, and the message goes in. The
usage is on the beat as `--five-hour <pct> --seven-day <pct>`
(`Usage.BeatFlags`) for pacing to read.
`TestALimitedHarnessIsDownUntilItsResetThenWoken`. Owed outside this layer
(What is weak).

The deliver adapter runs the harness directly, never through a shell, as its
own session leader, its stdin `/dev/null` when there is no text for it (a
headless `opencode run` with stdin left open hangs at init, measured
2026-10-04); a stopped turn's whole process group is signalled, SIGTERM then
SIGKILL, so a harness that forks leaves no orphan.
Six adapters are real: OpenCode, `opencode run --session <id> --dir <dir>
<text>`, the newest session of the directory when none is named; Codex,
Antigravity and Grok, each below; and DSH and Gemini, from the harness survey
at the end. A turn while a challenge is open carries, at its head, the exact `pong` line for
this friend (the binary by path, the name, the directory, the store, the
nonce), so a small model has one line to run and nothing to fill in. Claude
has no deliver command yet: its daemon is passive, taking nothing off the
stream (the session's own blocking read does), peeking so a ping is still
answered by the daemon at once, beating, and recording a push it cannot
deliver; so the tool is honest, and the beat and the daemon pong are real
for it.

### Codex

The adapter resolves the named thread, or the newest saved thread for its
working directory from session headers and the session index under
CODEX_HOME (otherwise ~/.codex). It probes that exact thread's writer lock.
When held, the first command is `codex queue --thread <id> --message <text>`:
the text is a literal argument, never a shell command. Queue acceptance is
logged as "queued for open chat", explicitly "accepted, not answered".
The open app can defer running queued input until its current turn ends.
An accepted queue command does not promise an immediate model answer.

When the lock is free, the first command is
`codex exec resume --skip-git-repo-check <id> <text>`; its record says
"answered by resume, not by the open chat". The lock probe is an observation,
not a reservation. If the first command exits nonzero or cannot execute, the
adapter tries the other route once. Only when both fail does it return
Deferred, keeping the bus message pending and charging no failed delivery.
No matching saved thread is a refusal with a remedy, rather than delivery
to a guessed thread. The adapter does not modify how the user launches the app.

Measured with codex-cli 0.153.4 and ChatGPT 26.930.21537 build 12776 on
2026-10-04: a queue test initially produced no input while the chat's turn
was active. Its test text arrived in the same open thread at 17:03:00.797 UTC,
after the prior turn completed at 17:03:00.757 and the next turn started at
17:03:00.774. The session replied on the bus at 17:03:03. The app and its own
stdio app-server remained the same processes throughout. The earlier
conclusion that queue could not reach that app is withdrawn: observing no
input during an active turn did not establish failure.

That app process retained CODEX_APP_SERVER_USE_LOCAL_DAEMON=1 from an earlier
experiment. The measurement therefore does not prove that the flag is
required or that a launch without it is equivalent. The adapter requires no
such flag; ordinary-launch and idle-session behavior remain separate live
checks. A later end-to-end check was sent at 17:11:00 and answered by the
session at 17:11:10, but its transport was not independently identified.

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

Ten consecutive open-session runs measured on 2026-10-04 (Antigravity 2.19.1,
session `fa76bcf6-e79d-42c2-be38-aa95ded5247c`, transcript
`~/.gemini/antigravity/brain/fa76bcf6-e79d-42c2-be38-aa95ded5247c/.system_generated/logs/transcript.jsonl`),
each sent via `nova-friend ping` and verified via
`nova-friend wait-pong --nonce <n> --timeout 60s` (accepting only the session pong):
- Run 1 (nonce `51qjmx`): sent at 2026-10-04T14:20:04Z, turn seen at 2026-10-04T14:20:08Z (line 4272), pong at 2026-10-04T14:20:11Z (line 4274), 7 s
- Run 2 (nonce `cbpukf`): sent at 2026-10-04T14:20:36Z, turn seen at 2026-10-04T14:20:36Z (line 4282), pong at 2026-10-04T14:20:39Z (line 4284), 3 s
- Run 3 (nonce `jtzfzq`): sent at 2026-10-04T14:21:19Z, turn seen at 2026-10-04T14:21:19Z (line 4288), pong at 2026-10-04T14:21:22Z (line 4290), 3 s
- Run 4 (nonce `7qohp5`): sent at 2026-10-04T14:22:25Z, turn seen at 2026-10-04T14:22:25Z (line 4294), pong at 2026-10-04T14:22:29Z (line 4296), 4 s
- Run 5 (nonce `ddawfw`): sent at 2026-10-04T14:23:16Z, turn seen at 2026-10-04T14:23:16Z (line 4300), pong at 2026-10-04T14:23:18Z (line 4302), 2 s
- Run 6 (nonce `1e4ybj`): sent at 2026-10-04T14:24:16Z, turn seen at 2026-10-04T14:24:16Z (line 4310), pong at 2026-10-04T14:24:20Z (line 4312), 4 s
- Run 7 (nonce `c5xm91`): sent at 2026-10-04T14:25:16Z, turn seen at 2026-10-04T14:25:16Z (line 4316), pong at 2026-10-04T14:25:23Z (line 4318), 7 s
- Run 8 (nonce `6f5s7d`): sent at 2026-10-04T14:26:16Z, turn seen at 2026-10-04T14:26:16Z (line 4326), pong at 2026-10-04T14:26:19Z (line 4328), 3 s
- Run 9 (nonce `qblpsq`): sent at 2026-10-04T14:27:16Z, turn seen at 2026-10-04T14:27:16Z (line 4332), pong at 2026-10-04T14:27:19Z (line 4334), 3 s
- Run 10 (nonce `95q7zb`): sent at 2026-10-04T14:28:16Z, turn seen at 2026-10-04T14:28:20Z (line 4346), pong at 2026-10-04T14:28:24Z (line 4348), 8 s
All ten runs passed within 2–8 seconds (well under the 60 s threshold).

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
does not. The app needs no special launch (no wrapper, no custom flags).
When the app is not running (no language server in `ps` for the daemon's
user), the delivery returns `Deferred` (`no antigravity language server is
running: is Antigravity open?`), so the message stays pending in the daemon's
hand, retried every ten seconds (`RecheckEvery`) and never counted toward
failure attempts or acked.

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

Grok, the Grok Build TUI (xAI's `grok`), is another real adapter, by the
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
is the next event. Deferred, never failed and never dropped, while no
monitor runs: no window is open in the directory, or the window runs no
monitor over a wake file (a stale pid in `active_sessions.json` is no
window). Nothing is written. The reason carries the one line the session
runs, `monitor `tail -n 0 -F <file>.wake`` (`--session` names the file).
The message stays pending and is tried again. `install --harness grok`
prints that line. `status` prints `route=push` when a tail runs under the
window's pid and `route=defer` with that line when none does. A wake path
that is not absolute, or that the process listing cannot show whole, is
still a refusal and nothing is written.

### fr-delivery-receipts.w1: receipts, the table's columns, the send alarm

The finding of 2026-10-04: the sprint server's notes to friends failed on
`WRONGPASS` for its bus user for two hours (54 failures in 30 minutes) and
only a log line said so; earlier a friend's harness got no note for 80
minutes while her beat said up. A beat proves the daemon, and the daemon's
ack proves the turn ran; neither proves the session read the message. So
every message to a friend is owed her session's receipt (SPEC-BUS.md,
fr-delivery-receipts.w1): her session runs `nova-bus ack --as <f> --id
<ids>` once it has read them, or answers one (`re`); `ReceiptLine`
(internal/friend/deliver.go) is that exact line for a turn's text, from its
`RECV OK id=` lines. The daemon's own ack at exit 0 is unchanged and is no
receipt.

The friends table's two columns per friend are `DeliveryCells` over
`bus.Undelivered`: undelivered, the count owed, and oldest, the oldest
owed message's age (`Age`: `45s`, `12m`, `1h20m`, `3d`; `-` when none).

The sprint server sends its notes through a `Courier`
(internal/friend/deliver.go): the bus, or a dial per note (`Open`), with a
`bus.Watch` on each result, so a login refused or a store not reached is
one alarm to the coordinator naming the store and the user, raised at the
first failure and cleared at the next success.

Not wired by this card (outside its paths, owed): the daemon putting
`ReceiptLine` in each turn (daemon.go's `Batch`); `nova-bus recv --ack`
giving a receipt for a session that reads the bus itself; the friends
table's columns (cmd/nova-sprint/friends.go); the sprint server's
`sendBus` becoming a `Courier` whose `Raise` and `Clear` write the alarm
where the coordinator reads it (a sprint note, like the idle alarm); and
the model (tla/Bus2.tla gaining the owed set, with a reversed witness for
a daemon ack that clears it).

## One-shot lanes (internal/friend/lanes.go)

A friend's delivery mode is a column of her nova-config friend row, `mode`,
`batch` (the default, and what every row before migration 0030 has) or
`one-shot`: `nova-config friend set bob --mode one-shot --width 1`. The
owner, 2026-10-04: "one-shot friends are configured via nova-config", and,
for a flash-class friend who answers each turn in seconds and stops, "we could
have one shot friends, have one-shots, per-lane ... so [she] can still be
wide, it's just 8 [of her]". `nova-config apply` writes the mode beside the
width into `friend:<f>:desired`, `nova-sprint friend sync` copies both onto
her sprint roster row, and her beat answers them (`FRIEND-BEAT OK ...
row_mode=<mode> row_width=<n>`), so the daemon reads her row every second
from the beat it already sends and a change takes effect without a restart
(the change of mode waits for the other mode's turns to end). `run --mode`
overrides the row, for a test.

In one-shot mode the daemon runs `width` lanes. Each lane is its own session
of the friend in the same harness and directory, opened by the daemon when
the lane starts, seeded from her own identity files (`AGENTS.md` and
`memory/`, in the working directory or its `<friend>/`), and kept for the
lane's life in `lanes.json` in the state directory, so a restart keeps it. A
free lane takes the next card of `inbox/QUEUE.json`, in the file's order,
that is `queued`, delivered (`inbox/<id>~<epoch>/BRIEF.md`, the highest
epoch), not done (no `outbox/<id>~<epoch>/RESULT.md`), and not held by
another lane or set aside, and hands it as one turn with three steps: do the
card from its brief; write its `REPORT.md` and `RESULT.md`; send one bus line
(the exact `nova-bus send` to the coordinator, printed in the turn). Bus
messages ride only inside a card's turn, oldest first, with the pong line and
the word about the coordinator; with no card to ride with they wait, pending.
The lane waits for the turn to end and looks for the card's `RESULT.md`:
there, the card is done and the lane takes the next; absent, the same card
is handed again once, and after `CardTurns` (two) turns without it the card
is set aside (recorded in `lanes.json`, never handed again by this daemon),
and the coordinator is told once on the bus, `friend <name>: card <id> not
finished after 2 turns (lane <n>): <reason>`. The reason is the last turn's:
a permission the harness refused, a turn stopped silent, the provider's
refusal, an exit code, or a turn that ended with no `RESULT.md`. Lanes never
share a turn, and a lane never runs two. A lane beyond a width since lowered
finishes its card and takes no other. The silence watch, the provider's
refusal streak and the broken session are the batch turn's, across every
lane. The friend daemon stamps progress on the sprint for each card whose
one-shot turn printed since that card's last stamp (`stampProgress`, at most
every `ProgressEvery`; docs/SPEC-SPRINT.md section 8, the rules table's row
late), so the late rule never returns a printing card for want of a stamp.

Only a harness that can open a session and deliver into a named one has
lanes (`LaneHarness`; OpenCode today: `opencode run --dir <dir> <seed>` with no
`--session` opens one, found as the session the listing of the directory
gained, and `opencode run --session <id>` takes each card), or a harness
that runs each card as a process of its own (`CardRunner`; Claude). On any
other harness a one-shot row is delivered in batch, said once in the record.

A claude lane opens no session: each card is one headless run in the
friend's directory, `env CLAUDE_CONFIG_DIR=<config_dir> claude -p <the
brief> --output-format stream-json --verbose`, stdin from `/dev/null`, the
card's `BRIEF.md` the whole prompt (no bus message, pong line or notice rides
with it; messages stay pending, the daemon reading nothing for a claude
session, and pings are answered by the daemon as ever). `config_dir` is the
friend row's (nova-config, docs/SPEC-CONFIG.md), so each friend is her own
account: its login and its settings, the permission mode a headless run
works under among them; `run --config-dir` overrides the row. The run's
output goes to the record and is never read for the result: the card's
`REPORT.md` and `RESULT.md` in its outbox are. A run that leaves either out
is a failed attempt whatever its exit (`claude -p exited <n> and <outbox>
holds no ...`), handed again and set aside as above. stream-json prints as
the run works, so the silence watch stops only a run that has stalled. A
claude row in one-shot mode with no `config_dir` runs no lane: the daemon
records `mode: one-shot REFUSED: friend <name> is a claude friend in
one-shot mode with no config_dir ...; run: nova-config friend set <name>
--config_dir <her account's absolute config directory>, or nova-friend run
--config-dir <dir>` once, and stays in batch, which for claude delivers
nothing.

OpenCode's headless run auto-rejects any tool call that would prompt (measured
2026-10-04, twice on one friend: `external_directory` for a path through the
symlink in the home directory, and another refusal that ended a turn in 12
seconds). Before each turn the adapter writes the friend's directory into her
project config, `<dir>/opencode.json`, under `permission.external_directory`
as `<path>/**: allow`, for the directory as given, its real path, and the
home directory's `<friend>-working` symlink when there is one, merged into
what the file holds and written only when it changes (a config that cannot
be written is said in the record and the turn goes ahead). The schema is
OpenCode's documented permission map; it is not yet measured on a live lane.
A refusal the turn's output still shows is the lane's `rejected=` on the
record and the card's reason.

Open design question, not built (the owner, 2026-10-04 1:45 PM: "tbd."): a
per-friend `tier` on the row (flash, pro, heavy, frontier), defaulted from a
small table of known models (a flash model is a one-shot by nature), giving
smart defaults the row's `mode` and `width` override, and the deal giving a
friend no card above her tier.

## Identity

The friend's name comes from one place, `install --as`, written into the
agent's command line; the daemon never takes a name from a message. The `pong`
verb refuses a name that is not the one the daemon in that directory runs as.
Binding the name to the store's login is owed: the bus store runs with no
authentication tonight (SPEC-BUS.md), and `nova-sprint friend beat <name>`
beats whatever name it is sent.

## What is weak, and known

A background process on this platform needs the user's permission to touch a
removable volume (the TCC service for removable volumes; measured 2026-10-04:
the daemon, and a plain `touch` launchd starts, both refused with "operation
not permitted" where the same commands from a shell succeed, and tccd logged
the access request). The permission is granted to the binary in the system's
privacy settings, by the person, never by the tool, and a rebuilt binary is a
new one to it. The state files are out of its way, under the home directory;
until it is granted the daemon beats and answers the daemon pong but cannot
run the harness on the volume, and the record says so.

The server side of the ping (the coordinator pinging every friend each window
from the sprint's run loop, and the table's `awake` and `deaf` columns) is not
here; `ping` and `wait-pong` run the canary by hand. The beat carries no
numbers until `friend beat` takes them. A session that reads the bus itself
(the stub harnesses) proves nothing to the daemon until it runs `pong`.

The limit layer (`limit.go`) is built and tested, not yet wired: the daemon
and `nova-friend run` do not yet wrap the adapter's Exec in `Limits.Watch` or
the Deliverer in `Limits.Gate` (a wrap must keep the `LaneHarness` lanes and
the OpenCode `Allow` setting, and gate the lanes' turns), do not call `Down`
and `Up` at the sprint (a friend's own `down --until` and `up`, which the
server today takes as the coordinator's hold), and the beat sends no flags;
`friend beat` takes no `--five-hour` or `--seven-day` until the sprint
records them for pacing. Claude has no deliver command, so its
`rate_limit_event` is read only once a Claude run's output passes through
`Watch`. The state machine (up, down until a reset, waking on a nonce) wants
its TLA+ module beside `tla/Friend.tla`.

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
A session that has selected an agent preset is refused by the one-shot runner
whatever the text (exit 1, "runs under agent preset ..., which the one-shot
runner does not compose"; measured 2026-10-04 on a "minimal" session), so the
adapter answers Deferred: the message stays pending, never given up, and the
reason tells the friend to start a session without a preset or read the bus with
`nova-bus recv`.
