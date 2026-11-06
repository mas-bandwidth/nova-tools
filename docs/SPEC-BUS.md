# SPEC-BUS: nova-bus

nova-bus sends short messages between named inboxes kept in Redis, and hooks a
process in to one inbox: every message arrives as one line on that process's
stdout. It is one Go directory, `bus/`, depending on the standard library and
go-redis only (`bus/imports_test.go` holds it to that). Anything that can watch
a process's output can receive messages; anything that can run a command can
send them.

## Verbs

```
nova-bus send --as <me> --to <name>[,<name>] [--re <id>] <subject> <body | ->
nova-bus hook --as <name> [--harness claude|codex|grok|antigravity] [its flags]
nova-bus who
nova-bus pending --as <name> [--list]
nova-bus help [<verb>] | -h | --help | <verb> -h
```

- **send** appends one message to the inbox of each recipient, in the order
  given and once per name however often it is listed, and prints
  `<name> <id>` for each. The subject may not be empty. A body of `-` is read
  from stdin, less exactly one trailing newline. `--re <id>` names the message
  this one answers and must be an id as hook prints it, `<ms>-<seq>`.
- **hook** delivers every message still waiting for `<name>`, then blocks and
  delivers each new one as it arrives, into the session that runs it, waking
  it when idle (see Waking a session), and holds `<name>`'s presence while it
  runs. It runs until killed. With `--harness claude` (the default) its stdout
  carries only message lines; with any other harness, only receipts. On stderr
  it says `HOOK HARNESS OK harness=<h> ...` once a harness other than claude
  answered, and `HOOK OK name=<name> waiting=<n>` once it holds the name (once
  per process, not on each reconnect).
- **who** prints `WHO OK present=<n>`, then `<name><TAB><uptime>` for each
  present name, sorted by name; uptime is a Go duration to the second
  (`1h2m3s`), or `?` when the key does not hold a start time (including a key
  that is not a string).
- **pending** prints `PENDING OK waiting=<n>`, the number of messages waiting
  for `<name>` that no hook has acked; with `--list`, then
  `<id><TAB><from><TAB><subject>` for each, oldest first. It consumes nothing.
- **help**, `-h`, `--help` or no arguments print the whole usage to stdout;
  `help <verb>`, `<verb> -h` and `<verb> --help` print that verb's usage.
  Help exits 0 and writes nothing to stderr.

There is no ack verb: hook acks each message after delivering it, and that is
the one way a message is marked handled.

## Waking a session

The wake is the bus: a message that sits unseen because its session is idle is
not delivered. Each harness has one mechanism, which puts a message into a
running turn and wakes an idle session alike (from v1.6.29 `nova-hook`):

- **claude**: the hook runs under the session's Monitor tool, which makes each
  stdout line a notification. The hook does nothing more; run with `&` or into
  a file, nothing wakes. A hook whose stdout is the `/dev/null` device (same
  rdev; the Go runtime reopens a closed fd 1 there) would ack every message
  unread, so it is refused at start, exit 2.
- **codex** (`--codex-socket <path> --codex-thread <id> --codex-events-dir
  <dir>`): the message is saved as `<dir>/<id>.json` (`event_id`,
  `local_receiver`, `message`), then, on the Codex desktop app's IPC socket
  (frames of a 4-byte little-endian length and one JSON object):
  `initialize` (version 0), `thread-owner-discovery` (version 1; the owner
  must report `supportsUntrustedAppInput`), and `thread-follower-start-turn`
  (version 2, to that owner, `inheritThreadSettings`) with the one fixed text
  naming the file. A `client-discovery-request` from the app is answered
  `canHandle: false`. The socket and its directory must be this user's, not
  symlinks, not group or world writable; the events directory is made (only
  its last component) mode 0700 and must be private.
- **grok** (`--grok-home <dir>`): the message's line is appended to
  `<dir>/nova-hook.wake`, which the Grok session's Monitor tails.
- **antigravity** (`--antigravity-dir <workspace> --antigravity-session <id>
  --antigravity-events-dir <dir> [--antigravity-server <port>]`): the message
  is saved as for codex, then `<language_server> agentapi send-message
  --title=nova-bus <session> <fixed text>` runs with the port and the CSRF
  token in its environment. The candidates are every `language_server` of
  this user in `ps` with a `--csrf_token`, those whose `--workspace_id` is the
  workspace's (path or symlink target) first; Antigravity 2.29 runs one hub
  server with no `--workspace_id`. The one taken is the first whose agentapi
  answers `get-conversation-metadata <session>` on a port from `lsof` (or
  the one named); none answering is exit 1 naming the candidate count. The
  hook's stdout never wakes Antigravity: an idle conversation wakes only on a
  task's exit, a subagent's message, or a SYSTEM_MESSAGE file under
  `~/.gemini/antigravity/brain/<id>/.system_generated/messages/`.

The wake text is fixed: only the validated name, id and path enter it, never a
field of the message. The harness is checked at start, before the name is
claimed: missing (socket, directory, workspace, a bad id) is exit 2, not
answering is exit 1. Each wake prints one receipt on stdout,
`HOOK WAKE harness=<h> id=<id> ok` or `HOOK WAKE harness=<h> id=<id> FAILED:
<reason>` (the reason escaped as a line is). A failed wake is not retried and
not acked: the hook reads its pending list past it (`XREADGROUP` from the last
pending id taken), so it neither blocks the pending pass nor is tried twice
by one hook; the next hook, or the next reconnect, delivers it again. What was
v1's recovery, lanes, challenges, chat following and consumption tracking is
not here.

## Exit codes and refusals

| Code | When |
|---|---|
| 0 | done; help; a hook stopped by SIGINT or SIGTERM |
| 1 | refused or failed: Redis unreachable or erring, `SEND FAILED`, `name already hooked`, stdout failing |
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
argument to hook, who or pending ("takes no arguments"). An unknown verb (`ack` is one) gets
`nova-bus: unknown verb "<verb>"; verbs: send, hook, who, pending, help; run:
nova-bus help`. go-redis's own logging is silenced: every message on stderr is
nova-bus's.

A runtime failure keeps the one-line shape, `nova-bus <verb>: <what>`, exit 1.
An unreachable server is said plainly: `cannot reach redis at <host:port>
(NOVA_BUS_REDIS): connection refused` (or `no such host`, `timed out`, or
`connection lost` for a dropped connection: EOF, reset, broken pipe); for
send it follows `SEND FAILED to <name>:`. A hook that loses Redis logs
`nova-bus hook: <what>; reconnecting` once, then at most once a minute, while
it retries every second.

## Environment

| Variable | |
|---|---|
| `NOVA_BUS_REDIS` | the Redis address, `host:port` (required); Redis 6.2 or later |
| `NOVA_BUS_USER` | the Redis ACL user (optional): with it the client sends `AUTH <user> <password>`; without it, `AUTH <password>` as Redis's `default` user |
| `NOVA_BUS_PASSWORD` | the Redis password (optional; never printed) |

`NOVA_BUS_USER` without `NOVA_BUS_PASSWORD` is refused (exit 2): go-redis
would send no `AUTH` at all and run as `default`. The user needs the keys
`bus:*` and the commands nova-bus sends (streams, `GET`/`SET`/`DEL`, `SCAN`,
`EVALSHA`/`EVAL`, `MULTI`/`EXEC`):
`~bus:* +@stream +@string +@keyspace +@transaction +eval +evalsha +@connection -@dangerous`.
The functional test runs a server with exactly that user and `default` off.

Each process keeps one Redis connection and runs its commands in turn. The
client never retries a command by itself.

## Wire format

- The inbox of `<name>` is the stream `bus:<name>`. A message is one entry
  with the fields `from`, `to` (the recipients, trimmed, each once, joined by
  commas), `subject`, `body`
  and `re` (empty when none), all strings.
- Every hook reads with the consumer group `hook` and the consumer `hook`. The
  group is created at id `0` on the first hook, so messages sent before any
  hook are delivered too.
- A hook's line is tab-separated:

  ```
  <id>	<from>	<re or ->	<subject>	<body>
  ```

  Backslash, newline, carriage return and tab inside a field are written as
  `\\`, `\n`, `\r` and `\t`, so a line never splits. pending escapes the
  subject the same way.
- Presence of `<name>` is the string key `bus:presence:<name>`, whose value is
  the hook's start time (RFC 3339 UTC, to the second), a tab, and a random
  token that process made at start.

## Delivery

A hook reads its pending entries first (`XREADGROUP ... 0`, then past the
last one taken: handed to a hook and never acked), then new ones
(`XREADGROUP ... >`, up to 100 at a time). For each entry it delivers it
(writes the line, or wakes the session), then acks it and deletes it (`XACK` and `XDEL`
in one `MULTI`), so an inbox holds only what is waiting. The ack runs to the end
even if a signal arrives after the write, and no further line is written once
one has. So:

- a restarted hook resumes where the last one stopped;
- delivery is at least once: a hook killed after writing a line and before
  acking it leaves the entry pending, and the next hook prints it again;
- an acked message is never printed again;
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

Presence means one thing: a hook process is alive for this name.

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
- SIGPIPE is ignored, so a reader that goes away (`nova-bus hook ... | head -1`)
  makes the next write fail with EPIPE: the hook releases its presence, leaves
  the unprinted message pending and exits 1.
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
- **Redis is unreachable.** send, pending and who exit 1. A hook logs
  `nova-bus: <error>; reconnecting` to stderr and retries every second; its
  presence may lapse meanwhile, and it claims again when Redis is back.

## Models

`bus/tla/Presence.tla` models the key with its TTL and hooks that compete for
one name: start, refresh, a stdout that blocks and unblocks, a clean exit
whose DEL may fail, a crash, expiry, and a start that waits for the key. It
checks: a name is present only if
a hook set the key within the TTL; a running hook not stuck on stdout is
present with its own token; a hook never starts while another's token is
present; the key is deleted only by the hook whose token it holds; a hook
that exited or crashed is absent within the TTL; a waiting hook whose rival
crashed sees the name claimed; and every wait ends in a claim or a refusal. A stuck hook is allowed to
drop out, and `PresenceStuckStrict.cfg` shows that it does. The reversed
witnesses remove the TTL, the refresh, the claim's check, the release's check
and the wait, one each. `Presence.cfg` checks two hooks; `PresenceThree.cfg` checks three (about 3.9M states, minutes on many workers, so it is run by hand rather than by `tests/tla/check.sh`). `bus/tla/Bus.tla` models delivery. `bus/tla/CASES.tsv` names
every case with the outcome TLC must reach; `tests/tla/check.sh JAR bus/tla` runs
them.

## An example session

Two names, alpha and beta, with `NOVA_BUS_REDIS` set in both shells.

```
beta$  nova-bus hook --as beta
alpha$ nova-bus who
beta	4s
alpha$ nova-bus send --as alpha --to beta "build" "main is green"
beta 1730800000000-0
beta:  1730800000000-0	alpha	-	build	main is green
beta$  nova-bus send --as beta --to alpha --re 1730800000000-0 "re: build" "thanks"
alpha 1730800005000-0
alpha$ nova-bus pending --as alpha --list
1
1730800005000-0	re: build
alpha$ nova-bus hook --as alpha
1730800005000-0	beta	1730800000000-0	re: build	thanks
```

The line marked `beta:` is what beta's hook prints. alpha had no hook running,
so beta's answer waited in alpha's inbox, and pending showed it without
consuming it; alpha's hook printed it as soon as it started.

## Build and test

```
go build -o nova-bus ./bus
go test ./bus                     # unit tests: an in-memory store and a fake clock
go test -tags functional ./bus    # a real redis-server on a free local port
```
