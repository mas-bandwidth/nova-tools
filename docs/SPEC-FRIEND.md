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
  that dies mid-turn is handed the message again when launchd restarts it.
- The files, under the friend's working directory, one writer each:
  `.nova-friend/status.json` (the daemon: its state, rewritten whole every five
  seconds and when it changes; a reader calls the daemon up while the file is
  under thirty seconds old), `.nova-friend/pong.json` (the `pong` verb: the
  session's last answer), `.nova-friend/deliver.log` (the daemon: one line per
  delivery), `inbox/QUEUE.json` (the coordinator and the session: one record
  per task with `id`, `state` of queued, working or done, and `deliverable`).
- The launchd agent `com.nova.friend-<friend>`: RunAtLoad, KeepAlive, a five
  second throttle. launchd opens its own log before the daemon runs and cannot
  open one on a network volume (EX_CONFIG, measured 2026-10-03), so that log
  is under the home directory; the daemon's record is on the friend's volume.

## The protocol

- `PING <nonce>`: from the coordinator, subject `PING <nonce>`, body the nonce,
  `seat=<coordinator> since=<RFC3339>`, and the one `nova-friend pong` line the
  session runs. The nonce is six random characters.
- `daemon-pong <nonce>`: the daemon's answer, sent the moment the ping is read,
  or seen by a peek while a turn is running. It proves transport.
- `pong <nonce> queue=<n> working=<n> width=<n>`: the session's answer, sent by
  `nova-friend pong` as the session's own turn and recorded in the pong file.
  It proves the AI. The coordinator reads it from the friend's own stream (the
  message's `from`), never from the body: a pong is forgeable only by the
  friend's login.
- Width is the nova-config friend row's, given to the daemon at install.

## The machine (tla/Friend.tla)

The connection: `connected` while a ping arrived within the window (three
minutes, one number on both sides); `silent` after a window without one, said
to the session as a turn exactly once per outage, and `coordinator back` once
when pings resume, naming the seat. The challenge: `quiet`; `challenged` from a
ping until the session's pong with that nonce; `deaf` after a window challenged
with no pong, until a pong. Only the current nonce answers: a stale or replayed
pong changes nothing. Up is `quiet` with at least one pong; the daemon's beat
never makes a friend up.

The invariants, each with a reversed witness that TLC catches: a friend is up
only after a session pong; a challenge is open for less than a window; the
outage is said exactly once; only the current nonce ends a challenge; and a
challenge ends.

## The loop (internal/friend/daemon.go)

Each second: the clock is stepped; when the session is free, one read of the
stream (a ping is answered by the daemon at once and the message is handed to
the adapter, which blocks for the whole turn, and acked when the turn ends at
exit 0; any other exit leaves it pending), else one peek, so a ping that
lands during a long turn is still answered at once by the daemon and pushed
in once the session is free; the worker's result; one beat to the sprint
server (`friend beat <friend>`, a plain beat: the queue, working and width
flags are owed on the server's side); the pong file, while a challenge is
open; the status file.

The deliver adapter runs the harness directly, never through a shell, as its
own session leader; past the ten minute budget the whole process group is
signalled, SIGTERM then SIGKILL, so a harness that forks leaves no orphan.
Two adapters are real: OpenCode, `opencode run --session <id> --dir <dir>
<text>`, the newest session of the directory when none is named; and
Antigravity, below. A ping pushed in carries, at its head, the exact `pong`
line for this friend (the binary by path, the name, the directory, the store,
the nonce), so a small model has one line to run and nothing to fill in.
Codex, Claude and DSH have no deliver command yet: their daemon is passive,
taking nothing off the stream (the session's own blocking read does), peeking
so a ping is still answered by the daemon at once, beating, and recording a
push it cannot deliver; so the tool is honest, and the beat and the daemon
pong are real for them.

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
again: the server's pid and CSRF token off `ps -axo pid=,args=` (the
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
not. The delivery is exit 0 once a new message has appeared in the mailbox
and `read.json` marks it read, polled every half second for two minutes; past
that it is exit 1 with the message id, the message still in the mailbox for
the session's next turn, and the daemon redelivers (a duplicate, never a
loss). The turn the message starts runs on after the ack: a second message
queues in the mailbox rather than waiting for the turn, which is the
harness's own order for its agents. The mailbox and `agentapi` are the
harness's internals for its subagents and scheduled tasks, not a documented
API; a release that moves them breaks this adapter, and the functional test
(`NOVA_FRIEND_ANTIGRAVITY_DIR`) says so.

## Identity

The friend's name comes from one place, `install --as`, written into the
agent's command line; the daemon never takes a name from a message. The `pong`
verb refuses a name that is not the one the daemon in that directory runs as.
Binding the name to the store's login is owed: the bus store runs with no
authentication tonight (SPEC-BUS2.md), and `nova-sprint friend beat <name>`
beats whatever name it is sent.

## What is weak, and known

A background process on this platform needs the user's permission to touch a
removable volume (the TCC service for removable volumes; measured 2026-10-04:
the daemon, and a plain `touch` launchd starts, both refused with "operation
not permitted" where the same commands from a shell succeed, and tccd logged
the access request). The permission is granted to the binary in the system's
privacy settings, by the person, never by the tool, and a rebuilt binary is a
new one to it. Until it is granted the daemon beats and answers the daemon
pong but can neither write its state files on the volume nor run the harness
there; the record says so once a minute.

The server side of the ping (the coordinator pinging every friend each window
from the sprint's run loop, and the table's `awake` and `deaf` columns) is not
here; `ping` and `wait-pong` run the canary by hand. The beat carries no
numbers until `friend beat` takes them. A session that reads the bus itself
(the stub harnesses) proves nothing to the daemon until it runs `pong`.
