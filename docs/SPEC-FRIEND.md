# SPEC-FRIEND: what a friend runs to be part of the team

The tool is `nova-friend`; the rules are `internal/friend`; the machine is
`tla/Friend.tla`. The contract was settled on 2026-10-04 between the owner and
the coordinator: "everything that a friend needs in any harness, to be a part
of the team", "it must be this way when they start up next time, not just now,
but always", and "like a network connection: client/server and the coordinator
is the server; both sides need to know they are connected, continually".

## The local child load gate

Friend work runs as fleet cards. A `nova-swarm member` may configure the raw
one-minute host-load thresholds `--max-load` and `--warn-load`. Immediately
before it creates a new local child, it reads that load through `hostload.Source`:
above the maximum it refuses the launch and names the measured load and bound;
at or above the warning threshold through the maximum it warns with both values
and starts. Zero disables the corresponding check, and both default to zero, so
existing fleet members keep their previous admission behavior. An unavailable
load reading also leaves that behavior unchanged. The warning threshold requires
an enabled maximum and cannot exceed it; near is configured, never inferred.

## The pattern in one sentence

One daemon per friend, started by launchd and never by the model, parks on the
friend's nova-bus stream and, whenever the session is free, pushes every
message waiting into the running session as one turn; it beats to the sprint
server while that loop runs and its session answers, and only then; it answers
the coordinator's ping at once and never makes a turn of it, and the session's
own answer to a nonce is the only thing that makes the friend up (Presence,
below): the daemon answering is never the session.

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
  is under the home directory, beside the state directory. The binary is the
  same wall, one step earlier: a binary under `/Volumes` starts and then does
  nothing. `install` copies it to `~/.nova-friend/bin/nova-friend` before
  writing the plist, and the plist names the copy. A copy that cannot be made
  (no home directory off `/Volumes`, or the copy fails) is refused: no plist
  is written and the agent is not loaded. A binary already off `/Volumes` is
  named as it is. The log path is a separate rule and stays under the home
  directory.

## The protocol

- `PING <nonce>`: from the coordinator, subject `PING <nonce>`, body the nonce,
  `seat=<coordinator> since=<RFC3339>`, and the one `nova-friend pong` line the
  session runs. The nonce is six random characters.
- `daemon-pong <nonce>`: the daemon's answer, sent the moment the ping is read,
  or seen by a peek while a turn is running. It proves transport. The ping is
  then acked by the daemon and never pushed in as a turn (the finding of
  2026-10-04: pings every few seconds were turns of their own and buried the
  work). It is the daemon's, named so, and never counts as presence.
- `SESSION CHECK <nonce>`: the daemon's own question to its session (Presence,
  below), delivered into the session as a turn; answered by the same `pong`
  line with that nonce.
- `pong <nonce> queue=<n> working=<n> width=<n>`: the session's answer, sent by
  `nova-friend pong`, recorded in the pong file. While a challenge is open, the
  exact pong line for the current nonce rides at the head of the next turn
  that carries messages, and the session runs it first. It proves the AI. The
  coordinator reads it from the friend's own stream (the message's `from`),
  never from the body: a pong is forgeable only by the friend's login.
- Width is the nova-config friend row's, given to the daemon at install.

### session-pong.w1: a wake check is answered by the session

A wake check is a PING whose body carries a `wake=1` line (`nova-friend ping
--wake`; `WakePingText`, `IsWake`). The daemon answers it at once with
`daemon-pong` as any ping, and when the session is free (no turn running, no
message waiting) pushes in the exact pong line for the current nonce as its own
short turn (`WakeTurnText`, `startWake`), with no message and no word about the
coordinator, once per wake ping; while the session is mid-turn, or messages
wait, the line rides at the head of the next turn that carries messages as
above, and that pays the wake check. A plain ping is still never a turn, and a
passive harness, or a one-shot daemon, pushes no wake turn. The two answers are
kept apart: status.json carries `last_daemon_pong` beside the session's
`last_pong`, and `nova-friend status` prints `daemon_pong_age` and
`session_pong_age` as two facts; `wait-pong` accepts only the session's pong.
The machine stays the one owner of `quiet`, `challenged` and `deaf`: a daemon
pong ends nothing, so a wake check the session does not answer within the
window is `deaf` while the daemon pongs (tla/Friend.tla: `WakeTurn`,
`OnlySessionPongEnds`, `OwedOnlyWhileAsked`; the reversed witness
`MCFriendBrokenDaemonPongEnds` lets the daemon's pong end a wake challenge and
TLC catches it). The finding of 2026-10-04: every daemon ponged while a
friend's session sat idle from 2:40 to 4:34 PM, and an idle session with no
message waiting was never asked at all.

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
never makes a friend up. The friend's presence, the daemon's own check of its
session, is beside this machine (Presence, below), not part of it.

The invariants, each with a reversed witness that TLC catches: a friend is up
only after a session pong; a challenge is open for less than a window; the
outage is said exactly once; only the current nonce ends a challenge; and a
challenge ends.

## Presence (internal/friend/presence.go)

The finding of 2026-10-04: three friends read up with eight cards each while
their harness apps were not running at all. The daemon answered every ping
itself, so a closed app looked alive (the owner: "If your detection that they
are down doesn't work WHEN THEY ARE DOWN, that seems like a bad design").
Presence is therefore the session's, never the daemon's:

- The daemon reads the bus log for messages from the friend. A message the
  daemon itself sent (every send goes through the daemon's store, which
  remembers its ids; a `daemon-pong` or `SESSION CHECK` by name, from a run
  before this one) is the daemon's and proves nothing. Any other message from
  the friend is the session's.
- After ten minutes (`SessionQuiet`) with no bus message from the session, the
  daemon delivers a session check carrying a fresh nonce through the friend's
  adapter, into the session, the way a card is delivered: a turn of its own,
  only once no turn is under way (the session's turns hold the adapter shared,
  the check holds it alone, so it is never a second turn in one session; the
  lanes of one-shot mode hold it the same way). A harness with no deliver
  command has the check put on its own stream, which its session reads. The
  check is one line to run with nothing to fill in: `nova-friend pong` with
  the nonce, to the seat (else `--coordinator`), and then end the turn.
- Only a reply carrying that nonce, written by the session, counts: a
  `daemon-pong`, a pong the daemon wrote, another friend's pong, a stale or
  wrong nonce all answer nothing. The bound runs from when the check went in.
- No answer within five minutes (`SessionBound`) and the friend is down, with
  the reason `no session answer`; a check turn still running then is stopped.
  While down, an ordinary message is no proof the check reached the session:
  only the nonce answers, and a fresh check goes in ten minutes after the last.
  The next answer, to the latest nonce, late or not, brings the friend back up.
- A daemon that starts is down, `no session answer yet`, with a check owed at
  once: coming up proves nothing about the session.
- While down, no beat goes to the sprint server: a friend the coordinator has
  never observed is up there on her beat alone, so the beat is held back (the
  status says `not beating: the session is down (<reason>)`). The friend row's
  mode and width arrive with the beat's answer, so while down the daemon
  delivers by the row it last read (batch at `--width` before any).
- The state is in `presence.json` in the state directory, one writer, the
  daemon; `status` prints `presence=up|down`, `last_session=`, and, when down,
  `presence_reason=` (`no session answer`, `no session answer yet`, or
  `no daemon` when the status file is stale).

### The model (tla/FriendPresence.tla)

What the table shows of a friend, and where the friend's cards are,
modelled as a TLA+ module (card fr-presence-model). The world, per friend:
the harness (running, closed), the session (answering, silent), the
provider's limit (none, limited until a reset), the daemon (beating). The
table: the coordinator's hold, the age of the session's last answer to a
nonce, and each card's holder. The table's word is derived at every read:
held is the hold alone; up is a session answer younger than the bound; down
is the rest. The daemon's beat is never read for it. Only a running harness,
a session taking turns and a provider not limiting it can answer.

The model's ping is the session check above (a fresh nonce), its answer the
session's pong with that nonce, and its bound the longest a friend stays up
with no proof from the session: in the code that is `SessionQuiet` plus
`SessionBound`, fifteen minutes, since any bus message from the session
restarts the quiet and the check goes in only after it.

A friend leaves up in two steps only, the coordinator's hold and the tick
(the last answer reaching the bound), and each takes back every card the
friend holds in that same step: the hold's withdrawal and the tick's
rebalance. The dealer deals only to a friend up, and never back to the
friend a card was last taken from.

The rules, each with a reversed witness TLC catches: a friend shown up has a
session answer younger than the bound and a harness running or closed less
than the bound ago; a closed harness is shown down once it has been closed
for the bound; a held or down friend holds no card; the beat alone never
makes a friend up. The liveness: a closed harness is shown down on a clock
that keeps ticking, whatever else never recovers; a card taken back is dealt
to another friend, assuming disruptions are finite, every recovery comes
(the app reopens, the session answers again, the limit resets, the hold is
released) and the checks keep going in.

"A friend shown up has a running harness", read at every state, cannot hold:
the table cannot see the app close, only the answers stop, so for up to the
bound after a close the friend is still shown up (the finding case
`MCFriendPresenceFindingRunningNow`). The bound is the promise.

Where the code today differs: a card stays with a friend who goes down
(internal/sprint/friend_deal.go: "a friend who goes quiet keeps her card");
the hold takes back only the cards not yet started
(internal/sprint/friend_take.go), where the model has no started card; and
a card the hold took back may be dealt to the same friend again (only
`friend take` keeps it off them, `taken_from`). The provider's limit only
stops answers in the model; limit.go's `Down` until the reset is not in it.
## A friend's status, from evidence (internal/friend/status.go)

The owner, 2026-10-04: "Once again I look at friends and I wonder, are
friends actually doing work?" The daemon's beat says only that its loop runs.
A friend's status is decided by one function, `FriendStatus`, from what the
friend did, in order, the first rule that holds deciding it:

1. the harness not running is down (`harness not running`);
2. at a limit is down until the reset (`limit until Mon 1:00 PM`, or
   `weekly limit until ...` when the limit has a name);
3. no session answer within the bound, `AnswerBound`, two windows (six
   minutes: a ping each window and a challenge open for less than one), is
   down (`no session answer 12m`, `no session answer ever`);
4. a bus that cannot deliver to her is down (`bus cannot deliver: <why> (<n>
   undelivered)`);
5. otherwise up (`session answer 40s`).

Messages waiting on her stream are work waiting, never down on their own.
Unknown harness evidence is no evidence and decides nothing; the session
answer is the proof the harness ran. The beat decides nothing.

The evidence, each piece shown beside the status whatever decided it: the
harness (`harness running`, `harness not running`, `harness unknown`); the
age of the session's last answer (the pong file); the limit and its reset
(`limit.json` in the state directory, `{"reason":..,"until":<RFC3339>}`, the
daemon its one writer, no file no limit); the messages waiting on her stream,
pending and new (`bus.Peek`, counted when `--redis` names the store, else
`undelivered not counted`); and the age and exit of her last result (the
newest turn line of `deliver.log`, `<RFC3339> subject=... exit=<n>`). A bus
that cannot deliver is the daemon down (its status file over thirty seconds
old), the session broken, or the store failing.

`nova-friend status` prints it after the daemon's facts: `status=<up|down>
why="<the rule that decided it>" evidence="<each piece, ; between>"`.

What is owed, outside this tool: the harness is known running only for Grok (a
tail under the open window); other harnesses are `unknown` until each has a
probe. No writer of `limit.json` yet: the daemon reads no limit from a turn.
The coordinator's `friend health` carries `--reason` and `--until`, but not
the five pieces of evidence, and the friends table (`nova-sprint where
--json`, the dashboard) shows the observation's word, not this verdict; the
coordinator's daemon sending this verdict through `friend health`, and the
table showing `why` and the evidence beside the status, are the next change,
in `internal/sprint` and `cmd/nova-sprint`.

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

**Last session activity** (2026-10-04: the table said up with 8 working while a friend's
session sat idle from 2:40 to 4:34 PM, and another read working=0 while she was busy; a pong
shows the daemon answers, not that her session moves). The beat carries the newest file
write under her working directory (`NewestWrite`, `Daemon.Activity`): her `outbox`, `inbox`
and `jobs` first, then the rest of the directory, each root once, never descending into
`.git`, `.cache` or `node_modules`. The walk is one stat pass bounded in files (2000) and in
time (50 ms, on the daemon's clock), answers with the newest write it read when it reaches
either, and runs at most once every `ActivityEvery` (10 s); the beats between carry its last
answer, and a daemon with no `Activity` carries none. The beat is `friend beat <friend>
--active <RFC3339>`; the sprint keeps it on her beat record, shows it as the friends
table's `active` column, and raises a `friend idle` alarm when she holds cards and it is
older than the `friend_idle` setting (docs/SPEC-SPRINT.md, last session activity). Where the
harness exposes the session's own turn events, they would be a second source; none is read
yet, so a session that works without writing a file (a long read, a long think) looks idle
after the setting, and the alarm says "written nothing", not "stuck".

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

### The harness check (internal/friend/alive.go)

A session that cannot answer is caught by the challenge only after a window;
a harness that has closed is caught at once. Every adapter answers `Alive`,
from the cheapest true signal it has, the process table (`ps -axww -o
user=,pid=,args=`, through the adapter's own runner, no shell): Codex, the
ChatGPT app (`/Applications/ChatGPT.app/Contents/MacOS/ChatGPT`);
Antigravity, its app; DSH, the DeepSeek Harness app; each its main
executable, matched whole, of the daemon's user, never a helper. Grok, a
window (the TUI process) open in the friend's directory: a pid of
`active_sessions.json` with that cwd, alive in `ps -axww -o pid=,ppid=,args=`.
OpenCode, batch and lanes, and Gemini run no standing process (every turn
starts the runner afresh), so a runner that cannot be found is not running
and a runner that is found cannot tell. An adapter that cannot tell says so
(a stub; a desktop app off macOS; a listing that cannot be read), the record
says it once, and its friend relies on the session check alone.

`Alive` is its own interface, `Aliver`, beside the deliver adapter's, so it is
optional by assertion: every adapter implements it
(`TestEveryAdapterAnswersTheHarnessCheck`), and a Deliverer that does not
cannot tell. `WatchHarness(d, adapter)` takes the bare adapter, the one
`NewDeliverer` returned, never the daemon's `Deliver`: the gates in front of
it (`SessionCheck.Gate`, `Limits.Gate`) answer no `Alive`
(`TestTheWatchReadsTheBareAdapterNotTheGateInFrontOfIt`). `cmd/nova-friend/main.go`
wires it just before `d.Run`: `friend.WatchHarness(d, deliver)`
(`TestRunPutsTheHarnessWatchInFrontOfTheBeat`).

`WatchHarness` puts the check in front of the daemon's beat. Every thirty
seconds (`AliveEvery`) it asks; a harness not running makes the friend down
at once, independent of the challenge: no beat goes to the sprint server
(down after fifteen seconds without one, so within three quarters of a
minute of the close), the beat's error, so the status, says `harness not
running`, and the record says `down:` with what was read. It is up again only
when the harness runs (or cannot be told) and the session has answered the
daemon's current nonce, one the pong file did not hold when the harness
closed; a harness that comes back is not yet a session that answers. Tested
over a fake process table and a fake clock,
`TestAClosedHarnessMakesItsFriendDownWithinAMinute`.

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

### friend-idle-wake-r.w2: idle wake

A friend holding cards whose session writes nothing gets one wake, then the coordinator one
note (`Machine.IdleStep`, `loop.idle`). The cards she holds are the queue file's queued and
working tasks, oldest first (`Daemon.Cards` stands in for it), read with her newest write
(`Daemon.Activity`) at most once a minute (`IdleWalkEvery`); with no `Activity` the watch is off;
the setting is her row's idle setting (`Daemon.IdleAfter`), ten minutes when it says none
(`DefaultIdleAfter`). The watch is `awake`, `woken` or `noted`: awake with no write for the
setting (measured from her newest write, the daemon's start, or the step she first held a
card, whichever is latest) gives one wake turn through the harness's resume, `you hold <n>
cards (<ids>) and your session has written nothing for <setting>; continue the oldest, <id>`,
headed by the pong line while a challenge is open and never carrying the word about the
coordinator; woken for the setting again with no write newer than the one the wake was given
on sends one `blocker` to the coordinator (the seat the last ping named, else
`--coordinator`), `friend <name>: idle <2 x setting> holding <n> cards: <ids>`; noted says
nothing more. A newer write, or holding no card, is awake again, and the next idle stretch
gets its own wake and note. The watch steps only while the session is free in batch mode: in
one-shot mode the lanes hand each card themselves, and a passive harness's wake is said on the
record while its note goes as any other. Tests: `TestAnIdleFriendWithCardsGetsAWakeTurnThenANote`
(a fake clock and harness: ten idle minutes give one wake, twenty one note, a write resets both).

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

## The beat comes from the daemon

A friend's beat is the daemon's alone: the loop above beats once a second
while it runs, and nothing else beats for her (the owner, 2026-10-04: "Golang
nova-tools and nova-sprint verbs only"; "Make the ping loop mechanical!!!!").
The per-friend shell loops that beat for a friend every second whether or not
her session was there, part of why a closed app read up, are retired
(docs/FRIENDS.md, "A friend's beat comes only from her daemon"). The beat
proves the daemon; whether her session answers is the coordinator's
observation (`friend health`). The roster and her cards are moved by the
friend sync loop, `nova-sprint friend sync --every <d>`, a nova-config loop
row kept alive with no shell in its argv (docs/FRIENDS.md, "The friend sync
loop"; cmd/nova-sprint/friend_loop.go).

## The push proof (internal/friend/pushproof_start.go)

A friend nothing pushes into is deaf, and the bus waits on her unread (the
owner, 2026-10-05: "Your inbox MUST push to you."; "It must be mandatory and
enforced"). So the push is proved before the daemon starts, never left to a
log line:

- `nova-friend install` and `run` refuse, before anything is written, loaded
  or delivered, a harness whose adapter has no deliver command (a `Stub`:
  claude and every surveyed harness), exit 2, with the remedy `the adapter
  card: give internal/friend a deliver command for <harness> (NewDeliverer),
  or run the friend under a harness that has one: <the harnesses with one>`.
  `--dry-run` refuses it too.
- `run` then proves the push with the first SESSION CHECK round trip
  (`friend.PushProof` over `Conformance`, the same check `nova-friend check`
  runs) once the store answers and before the loop: the check goes in through
  the adapter, and a pong with its nonce from the friend must reach the bus
  within `ProofWithin`, the session bound, five minutes. None, and run exits 2
  and the daemon does not start: `RUN REFUSED: no push proof: CHECK FAIL ...;
  run: <remedy>`. An adapter that answers the check with a `Deferred`
  carrying a `Remedy` cannot drive the session at all, and that remedy is
  printed: dsh, a session under an agent preset, `start a session in <dir>
  with no agent preset and name it with --session <id>`. Any other failure
  names the session to open and `nova-friend check` to prove it. A pass is
  one RUN line, `push proof: CHECK OK harness= took=`.
- `install` runs the same check after loading the agent: a session the
  adapter cannot drive is refused (exit 2, the remedy above) and the agent is
  booted out and its plist removed, since it would refuse at every start; any
  other failure is a NOTE, as before.
- Every beat carries the session's last proof, `friend beat --pong <RFC3339>`:
  the presence file's `last_heard`, the session's last answer or its own bus
  message. The sprint reads it as the beat's `Proof` (the friend beat record's
  `pong`): a friend whose proof is older than `FriendProofLive` (fifteen
  minutes: the daemon asks after `SessionQuiet` and waits `SessionBound`) is
  down however fresh her beat, so the deal, which deals only to a friend up,
  gives her nothing; a beat with no proof is judged by the beat alone. The
  coordinator's pass raises one `friend deaf` judgment when the proof lapses
  (internal/sprint/coordinator_pass.go).

The proof is the presence model's Ask then Answer within the bound
(tla/FriendPresence.tla), asked once before the loop. The daemon's own
presence still starts down with a check owed, so a session is asked twice at
a start: the proof, then the daemon's first check.

## The daemon writes every card she holds (internal/friend/inbox.go)

On 2026-10-05 from about 18:59 the daemons of two friends held cards on
their rows, taken by the deal in batch mode or by the coordinator's `friend take`, and no
`inbox/<job>/BRIEF.md` was written for them, so no lane ran them; the coordinator wrote the
briefs by hand at 19:40. The cause: the daemon wrote no brief at all. The one writer was
the coordinator's `friend sync` loop, and its pass is all or nothing: it skips every pass
while no seat holds the coordinator, and it stops for every friend at the first refusal. That evening it last wrote the friends' `QUEUE.json` at 18:50 (one friend) and
18:59 (the other), local time, and its log says it failed every pass from 19:08: "schema
config is at version 32 and this binary carries 33". Nothing
on the daemon's side noticed, because nothing on it knew what was on her row: its idle watch
counted her `QUEUE.json`, which nothing prunes (292 tasks, none of them the held cards).

The daemon now reconciles her inbox with her row on every loop (`InboxEvery`, the beat's
second), both ways:

- It asks the sprint server which cards are on her row (`Held`: the worker verb `friend
  cards <friend> --json`, also served at `GET /api/friend/<friend>/cards`, answered
  `{"friend":..,"cards":[{"card","job","col","kind","branch","tier","attempt","gen","epoch","brief"}]}`:
  each card working or ready on her row, whoever put it there (the deal, in batch mode
  ahead of her width; friend take or friend level from another friend's row), with its
  job (`friendJobOf`: `sprint.StoredID` and `.g<gen>`; a read's card id), its packet, and
  its brief as friend sync renders it (`friendBrief`; a read's `friendReadText`). A card
  taken back from her (withdrawn) is not listed. The server's side is
  cmd/nova-sprint/friendcards_verb.go; `TestFriendCardsServesEveryHeldCardWithItsPacket`
  pins it on a twin store end to end: the daemon's write from the answer alone is byte for
  byte friend sync's file, and a card taken from her and dealt to another friend is
  retired from her inbox and written to the other's as its `.g3` job).
- A card ready on her row is hers to take: her session (or her daemon) takes it as
  `take --as friend.<name> <card>@<gen>` through the server, within her lanes and
  while she is up, then stamps `progress` and `finish`es it as `friend.<name>`, as a
  member does (docs/SPEC-SPRINT.md section 1, a friend takes her own ready cards).
  In batch mode the tick's deal takes her ready cards into her free lanes first, so
  a ready card she is told of is one behind her lanes, taken by her next finish;
  a card ready in front of a free lane for ten minutes is the coordinator's
  judgment. The daemon's own `progress` still names her bare (`--as <friend>`,
  internal/friend/lanes.go `ProgressArgv`), which the server refuses for a card on
  `friend.<name>`; it is owed the row's name.
- A held card with no `inbox/<job>/BRIEF.md` is written, whole, never over a file there
  (`atomicfile` NoReplace: friend sync writes the same file the same way, and whichever is
  first writes it). The daemon logs one line per write (`inbox: wrote inbox/<job>/BRIEF.md
  (card <c>, <col> on her row)`); in batch mode the session is told of the briefs in a turn
  of their own, as friend sync's bus message would; in one-shot mode a free lane is handed
  the held cards in the server's order, whether or not her `QUEUE.json` names them.
- A sprint job in her inbox (a `BRIEF.md` headed `STATUS: nova-sprint card` or `WHO:
  friend`) whose card is no longer on her row (dropped, returned, landed), that no lane is
  running, and whose brief was written before the ask began is moved to
  `inbox/retired/<job>` (`<job>.<unix>` when that is taken). A brief written since the ask
  began is friend sync's for a card dealt after the server answered; the next pass decides
  it. A job any other hand put there is never moved.
- The model is the TLA+ module InboxReconcile (a card dealt and taken off her row with no
  event, friend sync writing at any time, the daemon's ask as a snapshot or no answer, then
  its reconcile): `HeldNeverRetired` (a card on her row never has its brief retired),
  `HeldIsWritten` (held leads to written, unless it leaves first) and `LeftIsRetired` hold
  on three cards (TLC, 4949 distinct states, the server answering often enough: strong
  fairness on its answer); with `AskGuard = FALSE`, the retire that ignores when the brief
  was written, TLC finds `HeldNeverRetired` broken in five states: ask, deal, friend sync
  writes, reconcile retires it. The module sits in this card's report until its path is
  given (internal/friend/tla is outside the card's paths).
- A job that is no single path element, a symlinked job and a card sent with no brief are
  not written and counted missing, each said in one line (`inbox: refused ...`, `inbox:
  missing inbox/<job>/BRIEF.md (card <c>, <col> on her row): <why>`) once while it stands,
  not once a loop.
- An answer that does not come writes nothing and retires nothing; it is said once until it
  changes, and the status file carries it (`inbox_error`).

`status.json` carries the last reconcile's counts and `nova-friend status` prints them:
`held=N inbox=N missing=N` (the cards on her row, the sprint jobs in her inbox, the held
cards still without a brief), `-` for each until the server has answered once. While the
server has answered, the idle watch counts her row, never her `QUEUE.json`.

While the server refuses `friend cards` (a refusal it answered, `friend.Refused`, never a
server that did not answer), the daemon reads her row from the server's worker view, `GET
/api/view/worker?as=<friend>` (nova-sprint `view worker`, served today): her work cards
ready and working, each with the inbox path friend sync delivers its brief to
(`~/<friend>-working/inbox/<job>/BRIEF.md`, the job `friendJobOf`), and no brief. On that
answer the daemon counts and retires as above, retiring only work jobs (`STATUS:` briefs:
the view lists none of her reads, so a `WHO: friend` job is never retired on it), and writes
nothing: a held card with no brief is said missing, once, with why, and `status` says the
row is read from the view (`held_from` in `status.json`). The view runs on the server's line
(serveView takes the tick's lock), so it is read once a `ViewEvery` (15 s, friend sync's
period), never every loop, and `friend cards` is asked again once a `ServedEvery` (a minute).
So with no server change, a held card with no brief is seen within 15 s, by count and by a
log line, where on 2026-10-05 it was seen by no one for 40 minutes.

A server without the verb (one older than the card daemon-writes-every-taken-card3, which
adds it) refuses it, and the daemon says so once a `ServedEvery`, each time it asks again,
naming that card (`inbox: the sprint server does not serve friend cards (card
daemon-writes-every-taken-card3 adds it) ...`), never once a loop; with no worker view to
fall back on (`friend.NotServed`) it writes and retires nothing.

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
finished after 2 turns (lane <n>): <reason>`, and the card is finished failed
in the sprint (a lane's end, below). The reason is the last turn's:
a permission the harness refused, a turn stopped silent, the provider's
refusal, an exit code, or a turn that ended with no `RESULT.md`. Lanes never
share a turn, and a lane never runs two. A lane beyond a width since lowered
(or beyond the live cap a rate limit lowered, below) finishes the turn under
way and takes no other; a card it holds between turns goes back to the queue
for a lane within the width. The silence watch, the provider's
refusal streak and the broken session are the batch turn's, across every
lane. The friend daemon stamps progress on the sprint for each card whose
one-shot turn printed since that card's last stamp (`stampProgress`, at most
every `ProgressEvery`; docs/SPEC-SPRINT.md section 8, the rules table's row
late), so the late rule never returns a printing card for want of a stamp.

Only a harness that can open a session and deliver into a named one has
lanes (`LaneHarness`; OpenCode today: `opencode run --dir <dir> <seed>` with no
`--session` opens one, found as the session the listing of the directory
gained, and `opencode run --session <id>` takes each card). On any other
harness a one-shot row is delivered in batch, said once in the record.

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

### rate-limit-backs-off-not-down.w1 — a rate limit backs off; out of funds holds

A rate limit passes in a minute; out of funds does not (the finding of
2026-10-05, 10:34 AM: a friend at 24 lanes hit his provider's input-token
rate limit, the runner held him down as if out of funds and returned 20
cards, and the coordinator cleared the pause by hand and lowered him to 12).
The OpenCode lanes read the last 2 KiB of each turn's output, whatever its
exit, and of a failed session open (`ProviderLimit`, `internal/friend/ratelimit.go`),
before a provider's refusal of the session: a line saying out of funds or
credit (a `402`, `payment required`, `insufficient balance`, `insufficient
... credits`, `out of funds`) is `OutOfFunds`, unless its reset is beside it
(that is the harness's own limit, `Limits` above); else a line saying a rate
limit (a `429` after `status`, `code`, `error` or `HTTP`, `rate limit
reached` or `exceeded`, `rate_limit_error`, `too many requests`, `input token
limit exceeded`, `tokens per min`) is `RateLimited`. A card whose
`RESULT.md` is there is done whatever its output said.

A rate-limited turn keeps its card in the lane's hand, counted toward
nothing (`card=kept`, never `card=again`, never set aside), and its messages
go back pending, counted toward nothing. The lanes' governor (`LaneGovernor`)
pauses every new turn and open for a backoff, 30 s doubling to 10 minutes,
and lowers the live lane cap by a quarter (at least one lane, never below
one): 24, 18, 14, 11 ... Lanes whose turns started before that lowering met
the same episode and change nothing. When the pause ends the lanes resume at
the cap; a lane beyond it takes nothing and hands back a card it holds
between turns. A clean ten minutes, measured (no rate limit, and a lane turn
ended clean in it), raises the cap one lane, up to the row's width; back at
the width the backoff starts over at 30 s. There is no hold and no person in
it: the beat goes on, the friend reads up, the cards stay hers. Each change
is one line on the record (`rate limit: lanes paused <d> until <t>, cap
<a> -> <b> of <w>: <reason>`, `rate limit: lanes resume at cap <c> of <w>`,
`rate limit: cap raised <a> -> <b> of <w> after a clean 10m0s`), and the
status's lanes say `:capped` beyond the cap and `:paused` during a backoff.
Three lowerings within an hour are one judgment: a blocker to the
coordinator, `friend <name>: lane cap lowered 3 times in 1h0m0s by rate
limits, now <c> of <w>`, naming `nova-config friend set <name> --width <c>`
as the way to keep it lower; the next three are another.

Out of funds holds the lanes: no new turn or open until the daemon restarts,
every card kept in its lane's hand, the status's lanes `:held`, said once on
the record (`out of funds: lanes held until the daemon restarts`) and once
to the coordinator as a blocker, `friend <name>: out of funds: <reason>`,
with the `nova-sprint friend down` line that shows it on her row and the
restart once paid. It lowers no cap.
`TestARateLimitBacksOffAndResumesWithoutAHold`,
`TestOutOfFundsHoldsTheLanesWithOneJudgment`,
`TestTheLaneGovernorBacksOffAndRaisesByMeasurement`. Not here: the batch
turn (one session, no lanes) still treats a rate limit as an ordinary failed
turn; a line that says a rate limit with a reset in minutes beside it
(`rate limit reached ... try again in 2 minutes`) is still read by `Limits`
as the harness's limit; and the funds hold does not stop the beat, so her
row reads down only when the coordinator runs the line it is told.

Open design question, not built (the owner, 2026-10-04 1:45 PM: "tbd."): a
per-friend `tier` on the row (flash, pro, heavy, frontier), defaulted from a
small table of known models (a flash model is a one-shot by nature), giving
smart defaults the row's `mode` and `width` override, and the deal giving a
friend no card above her tier.

### lane-end-finishes-the-card.w1 — a lane's end is a finish

The owner, 2026-10-05: "Now let's look at friends. Are they actually doing
work?" Six one-shot runs had ended overnight without writing `REPORT.md`
(killed at a cap, or exited early), so friend sync never saw a finish and the
cards stayed working on the friend's row for up to 14 hours; the coordinator
closed them by hand with `finish --failed`. A lane is done with a card when
its `RESULT.md` or `REPORT.md` is there after a turn, or when the card is set
aside after `CardTurns`; that end is the card's finish (`lane_end.go`):

- the friend wrote `outbox/<job>/REPORT.md`: that is the finish, and friend
  sync reads it as before; the lane writes nothing over it and sends nothing
  (`finish=report` on the record);
- she did not: the lane writes `outbox/<job>/REPORT.md` itself, with
  `Verdict: FAIL`, or `Verdict: HOLD` with `Head: <sha>` when she pushed, and
  one paragraph naming the lane and how the run ended: its exit, its wall, the
  turns it had, the cap that stopped it (`no output for <SilentStop>`) or the
  permission refused or the harness's error, and the pushed head or
  `no pushed head found`. It then sends the failed finish to the sprint
  server, as the friend's row:
  `finish --as friend.<name> <card>@<gen> --epoch <n> --failed [--head <sha>] --branch <b> --report "friend <name> <verdict>: <paragraph>"`,
  the words friend sync would use for the same report
  (`finish=failed sent=server`). A finish the server does not answer within
  `FinishWait`, or refuses, is said on the record
  (`sent=sync finish_error=...`) and left to friend sync, which finishes the
  card from the report the lane wrote.

The card's generation and epoch are read off its job directory,
`<id>~<epoch>[.g<gen>]` (nova-sprint `friendJobOf`; `ParseJob`); a lane hands
the generation the queue names (`cardDir`), and the finish names that
generation, `<card>@<gen>`. The pushed head is the branch the brief's
STATUS line names, read as `refs/remotes/origin/<branch>` (loose or packed,
through a worktree's `.git` file too) in a clone under `jobs/<job>/`: a push
writes it, and no network is asked. A card with a `REPORT.md` is not handed to
a lane.

A lane marks each card it begins as started in `lanes.json` (`started`, keyed
by the job, `<id>~<epoch>[.g<gen>]`: its lane, the card and when) and clears it at the card's end. A daemon starting up
finds every card still marked: the run that held it is gone with the daemon
that ran it (exited, killed or crashed), so it ends each as above, the report's
paragraph saying `the run is gone: the lane daemon started up at <t> and found
the card begun at <t0> with no REPORT.md`, and sets the job aside (`given_up`, by
job, as a set-aside card is) so it is not handed again. A daemon stopping leaves its running cards marked, for the next
one to finish.

The model is `internal/friend/tla/LaneEnd.tla` (TLC on a Linux bench, two cards:
288 distinct states, `NoOrphan`, `HersStands` and `Finished` hold); its
reversed witness `MCLaneEndBrokenNoWrite.cfg`, a lane that writes nothing at a
run's end as before this card, breaks `NoOrphan` in 6 states.

Not done here: the claude one-shot runner that marks a job started outside
nova-friend (the runner that ran the six overnight runs) is not in this
repository, and `take back`'s refusal of a card with a push is in
internal/sprint; both are outside this card. A gone run is known by the
daemon's restart alone: no process id is kept, so a harness that outlived its
daemon is not checked.

### the-daemon-reads-every-outbox-job.w1 — the daemon finishes every report on her row (internal/friend/outbox.go)

The night of 2026-10-05, a friend held eight working cards whose
`outbox/<job>/REPORT.md` said `Verdict: LAND` with a `Head:`, unread from
21:09 for two hours: the daemon finished only the cards its own lanes ran,
and the coordinator's delivery stopgap had written those briefs; the tick
raised "a friend holds working cards and finishes none". Now each reconcile
the server answered (the inbox above) is followed by a pass over her outbox,
whoever wrote the brief:

- Every job in `outbox/` named `<work>~<epoch>[.g<gen>]` (`ParseJob`) with a
  `REPORT.md` (a regular file, never followed through a symlink, at most
  `ReportCap`, 64 KiB, friend sync's cap) is matched to her row by its job, or
  by its card, epoch and generation. A job a lane is running is left to the
  lane's end.
- Its card working on her row, a work card: the report's verdict and head are
  read as friend sync reads them (the first `Verdict:` and `Head:` lines,
  markdown trimmed, the verdict upper case, the head lower case). `LAND` with a
  full sha head is `finish --as friend.<name> <card>@<gen> --epoch <n> --head
  <sha> --branch <b> --report "friend <name> LAND: <first paragraph>"`; `HOLD`,
  `FAIL` and any other verdict, and a `LAND` with no full sha head, are
  `--failed`, a full sha head kept, the report `friend <name> <verdict>: ` and
  the report's first 600 characters on one line. The branch is the row's, else
  the brief's STATUS line. One line on the record per finish (`outbox: finished
  card <c> from outbox/<job>/REPORT.md (Verdict <v>, working on her row):
  finish=ok|failed head=<sha> sent=server`).
- A finish is sent once: a job finished is never sent again, nor noted when its
  card leaves her row. One the server did not answer or refused is said once
  and sent again after `OutboxRetry` (a minute); friend sync may finish it
  first, and the server refuses the second.
- A report with no `Verdict:` line, a report that cannot be read, a card not
  on her row, ready and not working, or a read, is said once while it stands
  (`outbox: left outbox/<job>/REPORT.md: <why>`) and left; the next pass reads
  it again, so a verdict she writes later is finished then.

The model is `internal/friend/tla/OutboxFinish.tla` (TLC on a Linux bench, two
cards, one of them staged by another hand: 324 distinct states,
`FinishOnlyWorking`, `FinishOnlyVerdict` and `Finished` hold); its reversed
witness `MCOutboxFinishBrokenOwnOnly.cfg`, a daemon that finishes only the cards
it staged, breaks `Finished` in 5 states: the other hand's card is dealt, she
writes a verdict, the daemon asks, and nothing finishes it. The test is
`TestTheDaemonFinishesAReportItDidNotStage`.

### friend-lanes-read-c-r2.w1 — the friend's reader row is served by her lane daemon (internal/friend/read_lanes.go)

The owner, 2026-10-05: "We need to get away from these one shot shell scripts", "Reading should
be happening continually, not in bursts", and "Reads are in extra slots per-friend! Read slots
are different from worker cards." A one-shot daemon with a sprint server also serves the
friend's reader row, `reader-<friend>`, the work the hand-written `reader.zsh` loops did:

- **Beat and queue.** Once a `ReadAskEvery` (10 s) it asks `queue --as reader-<friend> --json`;
  that ask is the reader's beat. Its asked cards (`col` asked, each with a packet: tier, head,
  work_branch, attempt, brief, report) are the reads.
- **Read slots.** `row_read_slots=<n>` on her beat's answer (`nova-config friend set <f>
  --read-slots <n>`, default `DefaultReadSlots` = 2) is how many reads run at once. Read slots
  are their own number beside `width`: width card lanes AND read-slots reads run at once, a read
  never takes a card lane and a card never takes a read slot, so a dealt card never waits for a
  read and an asked read never waits for a card. A read begins the step it is asked while a slot
  is free, and the next as soon as one records.
- **A read.** `read --as reader-<f> --begin <card> --epoch <n>` (refused: said in the record, not
  begun); then READ.md, BRIEF.md and WORKER-REPORT.txt are written under `<dir>/reads/<card>/`
  (the clone at the head, the merge-base diff alone, the touched packages' vet and tests on a
  Linux bench, the bench rule, RESULT.md in the shape `head/branch/verdict/gate/report/## Body`),
  and the read runs as a one-shot of her harness inside the lane wall (`ReadHarness.RunRead`: a
  new session whose only turn is the read prompt, on the model of the read's tier; opencode's is
  `opencode run --dir <d> [--model <m>] <prompt>` with no session listing, so reads never queue
  behind lane opens; a harness without it opens the prompt as a lane session). A claude
  account's model per tier is `ReadModels` (frontier claude-fable-5-1, heavy claude-opus-5-5,
  pro claude-sonnet-5-5, flash claude-haiku-4-5-20251001).
- **The record.** RESULT.md saying `verdict: ok|broken` is `read --ok|--broken <card> --epoch <n>
  --finding <report line and body, 3500 bytes> --usage "model=<m> wall=<s>s harness=<h>
  account=<friend>"`; any other end (no RESULT.md, another verdict) is `read --return <card>
  --reason <why> --epoch <n> --usage ...`.
- **Limits.** A rate limit or out of funds met by a read goes to the lanes' governor as a card
  turn's does (`providerLimit`): the read is returned with `usage limit on <friend>: <reason>`,
  the lanes back off or are held, and no read begins while they are. A daemon stopping leaves
  its reads begun.

Tests: TestALaneDaemonReadsAnAskedReadAndRecordsTheVerdict (begun, run with its tier's model,
recorded ok with usage; a run with no RESULT.md is returned), TestAReadSlotIsNeverACardLane
AndNeverWaitsForOne (a dealt card is worked by its lane while a read holds the read slot),
TestAReadThatMeetsAUsageLimitIsReturnedAndTheLanesBackOff.

Not done here: the judgment when an asked read waits past a bound (the sprint tick's, in
internal/sprint, outside this card's paths); `nova-config friend set --read-slots` and the
beat's `row_read_slots=` (card read-slots-delivered-like-cards-w, on its own branch, not in this
base: until it lands the daemon runs `DefaultReadSlots`); a claude one-shot harness's `RunRead`
(card claude-oneshot-lanes-cb, likewise unlanded: a claude daemon cannot run a read until it
lands); a read begun by a daemon that then died is not returned by the next one; the loops are
retired by simp-retire-bud-runners-r and simp-retire-opencode-runners-r.

### buds-in-the-wall-r.w5 — every lane child runs inside a wall profile

A lane's child (the harness run that opens its session and each card's turn)
runs inside the wall of a profile (`internal/sandbox/profile.go`,
`LaneProfile`; docs/SPEC-SANDBOX.md, the subsection of the same name). The
profile is the friend row's, read off the beat as `row_profile=<name>` beside
`row_mode=` and `row_width=`, else `run --profile`, whose default is `friend`,
the one profile there is; a name that is not a profile is refused. The lane
marks its context (`LaneContext`) and the daemon's harness `Exec` (`Wall.Exec`)
runs a lane's command as `nova-friend wall --profile <p> --dir <dir>
--deny <self>... [--config-dir <c>] [--job <j>]... [--read <r>]... -- <harness> <args>`, in the
same directory with the same stdin; a batch turn and anything else not a
lane's runs as it did. The `wall` verb (`RunWall`) is dispatched before the
verb table, which carries no argv after `--`: it builds the profile's wall
for the command, prints nothing on stdout but the command's own (a harness's
answer, a session listing, is read through it unchanged), says each refusal
as one `WALL REFUSED reason=<r> <text>` line on stderr at exit 125, and
answers the command's exit. Inside, `HOME` is the config directory (else the
working directory) and `CLAUDE_CONFIG_DIR` is set when given; the SSH and GPG
agent variables are dropped (`sandbox.ChildEnv`). A daemon that cannot name
its own binary walls nothing: a lane's command is refused, never run outside
the wall. `run` flags: `--profile`, `--config-dir` (default
`CLAUDE_CONFIG_DIR`), `--deny-self` (default `NOVA_FRIEND_DENY_SELF`: the
coordinator's self, which no write inside the wall reaches; a wall that
denies nothing is refused, so a one-shot daemon with none runs no lane),
`--wall-jobs` and `--wall-reads` (comma-separated).

Not yet: nova-sprint's beat does not print `row_profile=` and the friend row
has no `profile` column (internal/sprint and internal/config are outside this
card), so today the profile is `run --profile`. A cancelled turn signals the
wall's group, and the wall passes SIGTERM to the harness's own group; the
SIGKILL after `KillDelay` reaches the wall only.

### delivery-conformance-r.w1 — one delivery check every adapter passes

Every harness adapter makes one promise, and `friend.Conformance`
(internal/friend/conformance.go) checks it end to end: a session check
carrying a fresh nonce (`SessionCheckText`, the daemon's own) goes in through
the adapter's `Deliver`, the session runs the exact `nova-friend pong` line it
carries, and a pong with that nonce, from the friend (the message's from,
never its body) and newer than the check, is on the bus within the window
(`DefaultCheckWithin`, the session bound, five minutes). It is the presence
model's Ask then Answer within the bound (tla/FriendPresence.tla), run once on
demand. A failure names its stage: `deliver` (an error, a `Deferred` or a
nonzero exit from the adapter; a `Stub` says its surveyed reason), `act` (the
pong file never held the nonce: the session did not run the line) or `reply`
(the pong file holds it and no pong reached the bus). The unit tier
(`TestEveryAdapterPassesDeliveryConformance`) runs the same function for every
name in `Harnesses`: the six with a deliver command (opencode, codex, gemini,
dsh, grok, antigravity) over a fake `Exec` (and a temporary home or an
in-memory mailbox where the harness reads files) with bus's Fake for the
store and a clock moved by the check's own waits, and every Stub, claude
among them, failing at `deliver` with its reason; an adapter with a deliver
command and no rig fails the test. `nova-friend check` runs it against the live
session (docs/CLI.md), `install` runs it once after loading the agent and
says the line in a NOTE (a session the adapter cannot drive is refused: The
push proof), `run` runs it before its loop as the push proof, and a nova-config loop record runs it nightly on
each friend's machine (docs/TESTING.md). Not covered: a lane's
`OpenSession`/`DeliverTo` path, and a check delivered while the daemon's own
turn is under way goes in beside it, not after it (the check runs in its own
process and does not hold the daemon's turn).

### Check

`nova-friend check [--as <coordinator>] [<friend>...] [--since <duration>] [--shown <file|->] [--json]`
judges whether each friend's row is true, from evidence, with no consumer's code: any coordinator of
friends runs it. The friends are the arguments, else every friend with a state directory or on the bus.
`--since` (default 24h) is the window; every count and age below is of it, and the window start is
the check's clock minus `--since`.

**The facts**, each gathered through an injected seam (launchctl, the status, presence and pong files,
the log file, the bus store, the directory listing) so the verdict is a function and the tests use fakes:

1. Daemon: the launchd agent (`loaded`, `not-loaded`, `none`) and its pid, the status file's freshness
   (`ok` within `DaemonStale`, `stale`, `none`), connection, challenge, the session pong's age, presence
   and its seen age.
2. Harness: the route (`push`, `defer`, `passive`) and, from the daemon's log, the deliveries (`exit=`
   lines) and deferrals stamped at or after the window start; a line with no stamp is outside every
   window. `last` and `last_exit` are the newest delivery in the window, `failed_of_last20` the failures
   among the newest twenty in the window. `delivered` and `failed` (JSON only) are the window's whole
   counts: the verdict reads them. And the session mark: `broken` and its `reason` when the status says
   the session is broken.
3. Bus: `real_since`, the messages from the friend in the window that are real (not ping, pong,
   daemon-pong or keepalive), and `last_real`.
4. Work: the entries in the friend's `inbox/` (not dotfiles or `QUEUE.json`) and `outbox/`, and the
   newest outbox entry.

**The verdicts**, a pure function of the facts, the first rule that holds:

1. `broken` when the session is marked broken, or `delivered > 0` and `failed == delivered`: every
   delivery in the window failed. One failure among successes is not broken.
2. `deaf` when a delivery in the window succeeded (`delivered > failed`) and no session pong aged
   within the window and no real message in the window came back.
3. `silent` when no delivery was due in the window (`delivered == 0`, no deferral, an empty inbox),
   nothing came back, and the friend is not down.
4. `down` by presence: presence down, no agent, or an agent not loaded with no status.
5. `ok` otherwise.

**`--shown`** is what a consumer shows of each friend, `{"<friend>":{"state":"up|asleep|down","working":<n>}}`
(a consumer passes its own table through it; the tool reads no consumer). A friend is claimed alive
when its state is `up` or `working > 0`. One precedence covers every non-ok verdict: when a claimed-alive
friend's facts verdict is not `ok`, the verdict stays the facts' and the why leads with
`untrue: shown <state>/<working>, ` and the facts' reason (the measured case: shown up, working 1, the
session marked broken: `verdict=broken`, why `untrue: shown up/1, session broken: <reason>`). When the
facts verdict is `ok` but the friend is asleep or its agent is not loaded, the verdict is `untrue`.

The summary is `CHECK OK friends=<n> ok=<n> broken=<n> deaf=<n> silent=<n> down=<n> untrue=<n>`. Exit 0
when every verdict is ok, 1 when any is not, 2 when the check could not run. With `--json` the same
facts and verdicts are one object: `friends[]` of `daemon`, `harness`, `bus`, `work` and `verdict`, and
`summary`. The model is the functions `DecideVerdict` and `factsVerdict`, `ParseLog` and `pongWithin` in
internal/friend/check.go; each cites this section.

## Harness settings (internal/friend/settings.go)

The finding of 2026-10-05: each friend's harness was set up by hand-editing
its config, and each hand edit failed once. One friend's Codex writable root
was a symlink, and she did no work for ten hours. Another's DeepSeek Harness was
on the `minimal` agent preset, and the headless runner refused every turn. A
Grok friend's wake file path and the buds' `CLAUDE_CONFIG_DIR` were typed into
units. Now
`nova-friend install --harness <h>` writes the settings, before the agent, with
one small writer per harness (`settings_<harness>.go`). `nova-friend check
--settings` compares what is there with what install would write. Both go
through one list, so check reports exactly what install writes.

| harness | file | setting install writes |
|---|---|---|
| every one | `--dir` | a real directory (never made, never a symlink) |
| codex | `$CODEX_HOME/config.toml` (else `~/.codex`) | `[sandbox_workspace_write] writable_roots` holds `--dir`, added to the roots there; edited by line, so comments and other keys stay |
| dsh | `$DSH_HOME/profiles/desktop/cordis.patch.yml` (else `~/.dsh`) | the patch entry `agent-preset-registry`, `config.default` and `config.selectedDefault` = `standard`; the desktop profile must exist (the app makes it); edited as a YAML node tree |
| grok | the wake file, `--session`, else `~/.nova-friend/<me>/<me>.wake` | its directory a real directory (made), the file a file (made empty); the agent's `--session` names it, and the NOTE's monitor line names it |
| claude | `--config-dir` (default `CLAUDE_CONFIG_DIR`) | a real directory, made private; the agent's `run --config-dir` names it; install refuses claude without one |
| opencode | `<dir>/opencode.json` | `permission.external_directory["<dir>/**"] = allow` (as `AllowDirs` writes before each turn), and `model` when `--model` names one |

**Rules.** Every path is read first. A symlink where a directory belongs, a
directory that is a file, a missing directory install does not make (the friend's
directory, the DSH desktop profile), or a config file that is a symlink is
refused (`ErrNotRealDir`, exit 2): nothing is written and no agent is loaded.
The refusal names the path and its target. Install never replaces a symlink,
because an atomic write over a dotfile repository's link would cut it. A setting
under a refused path is not read through it, and check shows it as `unread`.
Each written setting is merged into what the file holds and read back; one
that still differs is an error. A second install writes nothing. `--dry-run`
plans each write as `INSTALL PLAN command="write <file> <name>=<value>"` and
refuses as the install would. A real install says each write as `INSTALL WROTE
harness= file= name= value=`.

**Check.** `nova-friend check --settings --as <me> --harness <h> --dir <d>`,
with the flags install took (`--session`, `--config-dir`, `--model`,
`--state-dir`), writes nothing. It prints `CHECK OK harness= settings=<n>
drift=0`, or `CHECK DRIFT harness= settings=<n> drift=<n>` at exit 1 with one
`CHECK DRIFT harness= file= name= want= have=` line per drifted setting and a
NOTE with the install line that writes them. A symlink is
`have="symlink to <target>"`.

**What is not measured.** The DSH preset value `standard` is the owner's hand
fix of 2026-10-04, written into the same key; whether the headless runner
composes a `standard` session was not measured here (the runner refused a
`minimal` one). A session keeps the preset it was opened under, so the friend
opens a new session after install. The Codex key is the documented
`sandbox_workspace_write.writable_roots`; it takes effect only under
`sandbox_mode = "workspace-write"`, which install leaves alone. The health check
(`check` with no `--harness`) does not yet carry settings drift: run
`--settings` per friend.

## The coordinator's ping (cmd/nova-friend serve; internal/friend/keepalive.go)

The server side of the connection, as the owner designed it: the coordinator
is the server, both sides ping each second, and a friend is down after ten
seconds without a pong. `nova-friend serve --as <coordinator>` is the loop,
installed as a nova-config loop row kept alive, never a hand loop or a hand
plist. Each second (`PingEvery`) it reads the coordinator's own stream from
where it left off, in one range: a `daemon-pong <nonce>` or a session
`pong <nonce>` from a friend counts when the nonce is one the loop sent that
friend in the last ten seconds (`DownAfter`), once; the sender is the
message's `from`, never its body, and a stale, replayed or other friend's
nonce changes nothing. Then each friend whose state changed is said, and every
friend row but the coordinator's own is sent a `PING` with a fresh nonce and
the seat line (`since` the loop's start). The friends are nova-config's friend
rows as the bus store holds them (the set `friends`, written by `nova-config
apply`, the roster a send is checked against), read at the start in the trip
that fixes where the stream is read from, and again each minute
(`RowsEvery`), so a friend or a bud added as a row is pinged with no list kept
anywhere else, and a row removed is forgotten. A friend is up on a counted pong and down once ten
seconds pass without one, counted from the last pong, or from when its row was
first read; a ping that could not be sent is named in the down line's reason.
The state is one process's, in memory (a last pong and the nonces of the last
ten seconds per friend), so a restart starts every friend undecided. It prints
`SERVE OK friends= every= down_after=` once, then one line per state change and
never one per ping, `SERVE UP friend= at=` and `SERVE DOWN friend= at=
last_pong=<RFC3339|never> reason=`; a store or rows read that fails is one
`SERVE NOTE` until what it says changes, and one when it clears.

This is the connection, not presence: a daemon-pong proves the friend's daemon
and transport, the way `wait-pong`'s `daemon=` does, and the session's own
answer is still the session check's (Presence, above). Each `PING` reopens the
daemon's challenge, so the challenge's `deaf` is not reached while this loop
runs (Chaos, below); presence is what says a silent session.

## The wake ping loop (cmd/nova-friend ping --wake --to-friends; internal/friend/wakeping.go)

The owner, 2026-10-05: "there is no value in things that are answered just by
the daemon", and "We need to get away from these one shot shell scripts." The
ping that matters is the one only the session answers, the wake ping (`ping
--wake`, the `wake=1` line): the daemon answers at once and pushes the pong line
in as the session's own turn, and only the session's pong ends it. The loop that
sent one to a typed list of friends is a verb: `nova-friend ping --as
<coordinator> --wake --to-friends --every <d> [--within <d>] [--never-wake
<f,...>] [--server <addr>]`.

Each pass reads the friends table from the sprint server's coordinator view
(`GET /api/view/coordinator?all=1`: the rows `f:<friend>` with status up, down
or held, and the seat's holder), so no list of friends is kept anywhere else
(`WakeTargets`). A friend whose status is not up (held by the coordinator, or
down), a friend in `--never-wake`, and the coordinator itself are not pinged.
Each target gets a wake PING with a fresh nonce; the pass then reads the bus log
until every session's own pong for its nonce is there or `--within` (default
`Window`) is out. A `daemon-pong` never counts: a friend whose daemon answered
and whose session did not is deaf. The coordinator, the seat's holder (else
`--as`), is sent one blocker note `wake: deaf: <f,...>` naming every deaf friend
once per change of that set (`DeafChange`): the same set on the next pass says
nothing, a friend added or dropped is a change, and a friend deaf again after
the set emptied is a change. It prints `WAKE OK every= within= never_wake=`
once, `WAKE PASS n= pinged= answered= deaf= at=` each pass, `WAKE DEAF friends=
at=` at each change, `WAKE NOTE` for a table, store or send that failed (the
pass goes again next time), and `WAKE STOP` at a signal. Without `--every` it is
one pass. A pass cut by a signal calls no one deaf.

`ping-install --as <coordinator> --every <d>` installs it as the launchd agent
`com.nova.friend-wake-ping-<as>` (RunAtLoad, KeepAlive, the way `install` runs
the daemon), and `ping-uninstall` boots it out and removes the plist.

The two shell while-loops this replaces (a routine `ping --to <f>` every 600 s over a typed
list, and a `ping --wake` loop over a typed list) live on the coordinator's machine, not in this
repository: the wake loop is this verb; the routine loop has no verb, because nothing it
proves is wanted, and it is stopped there, not here.

Not done, and why: `serve` (above) is a different loop, the connection's own,
each second and answered by the daemon; the daemon's "coordinator silent" window
(`Window`) counts its pings, so deleting it is the owner's decision and is not
made here. The friends table has no never-wake column yet, so `--never-wake`
names the friends; a row field is the sprint store's and nova-config's change.

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
run the harness on the volume, and the record says so. `install` does not
point the agent at a binary on that volume: it copies the binary under the
home directory, or it refuses. The harness and the friend's directory can
still sit on the volume, and that still needs the person's permission.

Presence's TLA+ module is `tla/FriendPresence.tla` (The model, above); the
code is not yet held to it where the two differ. A session check waits for the
turn under way, so a session in a turn longer than ten minutes that sends
nothing on the bus is checked only once that turn ends, and keeps its word
until then. A one-shot friend with no session in its directory at all has
nowhere for the check to go until a lane opens one, and its lanes wait on the
row, which comes with a beat; it stays down until a session exists.

The harness check (internal/friend/alive.go) is wired in `cmd/nova-friend/main.go`
before `d.Run` (`TestRunPutsTheHarnessWatchInFrontOfTheBeat`). The watch's down
and up is not in `tla/FriendPresence.tla` yet.

The server side of the ping is `serve` (The coordinator's ping, above); the
daemon's side still waits a window of three minutes for a ping, not ten
seconds, and pings nobody, so "both sides ping each second" holds on the
coordinator's side only; the table's `awake` and `deaf` columns are not
written from `serve`'s states; the keepalive's TLA+ module beside
`tla/Friend.tla` is owed. The bus trims nothing (SPEC-BUS.md), so each friend
`serve` pings adds two entries a second to the streams and the log (a ping and
its daemon-pong), about 170,000 a day per friend, kept until trimming is decided. The beat carries the
last session activity (above) and no
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

## Chaos: detection proved by breaking it (internal/friend/chaos_functional_test.go)

The owner, 2026-10-04: "If your detection that they are down doesn't work
WHEN THEY ARE DOWN, that seems like a bad design." One functional test,
`TestEveryFriendFailureShowsWithinItsBound` (`go test -tags functional ./internal/friend`),
breaks a friend each way he can fail and asserts the
bound on the friends table and where his cards go. The friend runs the real
daemon over a scratch bus (bus's Fake behind a store that blocks on an empty
read and can have the friend's credential revoked) into a fake harness that
can be closed, silenced or limited, and beats to a twin sprint store
(`store.Mem`) whose tick deals four cards for any friend, two to him and two
to a second friend who never fails. Each case runs in a `testing/synctest`
bubble, so the long bounds are fake time.

| case | bound | cards | landed today | owed by |
|---|---|---|---|---|
| harness closed (every delivery Deferred) | down within 1 minute | new cards to the other friend; none left on him | nothing: his daemon beats, so the table says up | fr-harness-alive |
| session silent (turns taken, nothing said) | down within 15 minutes of his last bus message, pinged once a window | as above | his daemon calls the session `deaf` within the bound; the table says up | fr-session-proof-of-life, fr-status-from-evidence |
| usage limit (every turn fails with the reset time) | down within 1 minute, until the reset, then up once the session answers a nonce | as above | nothing: the table says up throughout | fr-limits-and-credits |
| bus credential revoked (every command WRONGPASS) | one alarm to the coordinator on the first failed send | as above | the status names WRONGPASS at the first failed command; the beat stops, so the table says down within 15 seconds and new cards go to the other friend | fr-delivery-receipts (the alarm) |
| hold (`hold --return`) | held at once | none left on him; his cards dealt to the other friend at the next tick; no new card | all of it | none |

"None left on him" for the down cases is the presence model's invariant (a
held or down friend holds no card, fr-presence-model); today a down friend
keeps the cards dealt to him and the deadline judges them (`FriendDownAfter`,
internal/sprint/presence.go), and no card yet names that withdrawal.

A part the landed code cannot meet is owed: it is checked like the rest, and
when it fails the case fails, each line
`OWED <card>: <what was measured>`, the suite
red until the presence cards land. A landed part that fails always fails. The
fake harness speaks for a limit only in its own words (exit 1 and the reset
time in the error); when fr-limits-and-credits gives the adapters a typed
limit, the fake answers with it.

One finding of the suite: the challenge is reopened by every ping, so a
coordinator that pings more often than once a window never sees a silent
session go `deaf`; the case pings once a window, as the contract has it.

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
| dsh (DeepSeek Harness) | yes: `/Applications/DeepSeek Harness.app`, v0.2.0-rc.2, CLI at `Contents/Resources/runtime/cli/bin/dsh`, nothing on PATH | none into an open desktop session: a session under an agent preset makes `dsh headless` exit 1 before any write, and the adapter returns Deferred (route defer); the session reads the bus itself (`nova-bus recv` or `nova-bus wait`) | `dsh headless --session-id <id> -` in the friend's dir, text on stdin; newest `session-*` of `<key>` when none is named | no live push (route defer). Probed 2026-10-05 on the macOS survey machine against the real friend session `session-e9b0dc0e-b03d-4516-b40e-6a0287ca218b` in `/Volumes/nova/ai/zhi`: `echo "test" \| dsh headless --session-id session-e9b0dc0e-b03d-4516-b40e-6a0287ca218b -` exits 1 with preset `minimal` refusal before any write, transcript `session.v4.jsonl.zstd` hash and timestamp unchanged, `deliver.log` records 1339+ consecutive deferred attempts, desktop app exposes no local listener or IPC socket. 2026-10-04, isolated DSH home, no credentials: under preset `minimal` headless exits 1, transcript SHA-256 unchanged; with no preset the runner appended a turn (turn 2 in the same record, 9 KB to 16 KB), unknown id exit 1, another directory exit 1, the turn stopped at `MISSING_CREDENTIAL` | store DEEPSEEK_API_KEY for the headless profile (the web Models page, or the daemon's environment); the desktop app's key is not seen by it |
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
Measured 2026-10-04 on a disposable session in an isolated DSH home (no
credentials, preset `minimal`): `dsh headless --session-id <id> -` exits 1 with
`runs under agent preset "minimal", which the one-shot runner does not compose`
before any write, and the transcript's SHA-256 is unchanged. For a session with
no preset the runner appended the turn to that session's own record as a
separate process. Probed 2026-10-05 on the macOS survey machine against the
real friend session (`session-e9b0dc0e-b03d-4516-b40e-6a0287ca218b` in
`/Volumes/nova/ai/zhi`): `echo "test" | dsh headless --session-id session-e9b0dc0e-b03d-4516-b40e-6a0287ca218b -`
exits 1 immediately with `dsh: session "session-e9b0dc0e-b03d-4516-b40e-6a0287ca218b" runs under agent preset "minimal", which the one-shot runner does not compose`
before any write; transcript `session.v4.jsonl.zstd` hash and timestamp are
unchanged, `deliver.log` records 1339+ consecutive deferred attempts with this
exact refusal, and DeepSeek Harness exposes no local listening socket or IPC
into the open session. No push route into the open desktop session exists, so the
route is defer: `DSH.Route` answers `defer` with the line the session runs
(`nova-bus wait --as <friend>`), and each refused delivery is a deliver-log
`deferred=` line. `nova-friend status` prints route only for grok, so presence
carries no route for dsh until that wiring (cmd/nova-friend, outside this card's
paths) lands.
A session that has selected an agent preset is refused by the one-shot runner
whatever the text (exit 1, "runs under agent preset ..., which the one-shot
runner does not compose"; measured 2026-10-04 on a "minimal" session), so the
adapter answers Deferred: the message stays pending, never given up, and the
reason tells the friend to start a session without a preset or read the bus with
`nova-bus recv`.
