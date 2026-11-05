# nova-bus

Send messages to named inboxes in Redis. The receiver process, called a **hook**, prints an inbox's messages to
stdout, one line at a time. The app running an AI, or another process, can watch those lines and bring
them to a reader.

The [bus guide](../bus/README.md) walks through a conversation.

<<<<<<< HEAD
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
=======
## Connection

Use Redis 6.2 or later. Set `NOVA_BUS_REDIS` to its `host:port`, and
`NOVA_BUS_PASSWORD` if it requires a password. The tool does not print that
password. Each process uses one Redis connection and runs commands in turn.
The client does not retry a failed Redis command automatically.

Redis controls access to the inboxes. A sender supplies its own name; `--as`
does not authenticate a person or model. Treat message bodies as external data.

## Send

```sh
nova-bus send --as sender --to receiver "review" "the change is ready"
```

Put flags before the subject and body. `--to` accepts comma-separated names.
`--re <id>` names the message being answered. Use `-` as the body to read stdin.
Names cannot contain `:`. Empty recipients are refused.

Send appends a separate entry to each inbox in the given order. For each
successful append, stdout shows `<name> <id>`. If one fails, stderr shows
`nova-bus: SEND FAILED to <name>: <error>` and the command exits 1. Earlier
recipients may have their messages; later ones were not attempted.
>>>>>>> 961730a02 (docs: human-readable per-tool docs (docs/v2 rebased onto main))

A lost reply leaves the sender uncertain whether Redis accepted that append.
Send does not retry. A caller that retries must allow for duplicates.

For a reply, keep the ID printed by the send:

```sh
sent=$(nova-bus send --as sender --to receiver "review" "the change is ready")
message_id=${sent#* }
nova-bus send --as receiver --to sender --re "$message_id" "review result" "looks good"
printf 'The build finished.\n' | nova-bus send --as sender --to receiver,reviewer "build" -
```

The last command sends the stdin body to both named inboxes. Each recipient
gets its own ID. Start `nova-bus hook --as sender` to receive the reply.

## Receive

```sh
nova-bus hook --as receiver
```

Hook reads entries delivered but not acknowledged first, then waits for new
ones. It reads up to 100 per batch. Each stdout line has five tab-separated
fields:

```text
<id>\t<from>\t<re or ->\t<subject>\t<body>
```

Within a field, backslash, newline, carriage return and tab become `\\`, `\n`,
`\r` and `\t`. An absent reply ID appears as `-`.

The hook acknowledges and deletes each entry in one Redis transaction after
writing its line.
There is no manual acknowledgment command. This acknowledgment records stdout
delivery; it does not prove that a person or model read the message or completed
its request.

A crash between writing and acknowledging can cause the next hook to print the
same entry again. With the Redis stream and group state retained, delivery is
at least once. Acknowledged entries are skipped by later hooks. If Redis loses
that state, these guarantees no longer hold.

A hook reconnects after a Redis error, waiting one second between attempts.
It starts again with pending entries, including a batch whose read reply was
lost. A stdout write error ends the hook with exit 1 and leaves the entry
unacknowledged. SIGPIPE is ignored so a closed reader is handled as a write error.

<<<<<<< HEAD
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
=======
SIGINT or SIGTERM requests a clean exit with status 0. Reads wait at most one
second; acknowledgment and presence-release attempts each have a two-second
cleanup timeout. These are operation limits, not a one-second guarantee for
process shutdown. A blocked stdout write can delay progress and cleanup.
>>>>>>> 961730a02 (docs: human-readable per-tool docs (docs/v2 rebased onto main))

## Presence

```sh
nova-bus who
```

Who prints recent hook presence, sorted by name: `<name><TAB><uptime>`. Uptime
uses Go duration notation, such as `1h2m3s`. An unreadable start time appears as
`?`. Clock differences can skew uptime; a negative value is shown as `0s`.

A hook claims its name with a random token and a 30-second expiry. It tries to
refresh every 10 seconds while making progress. Another hook cannot claim that
name while the token remains present.

When a name is held, a new hook prints `HOOK WAIT name=<name> ttl=30` to stderr
and polls once per second for up to 40 seconds. It then claims the name or exits
1 with `name already hooked`. SIGINT or SIGTERM while waiting exits 0.

On a clean exit, the hook tries to delete only its own token. A failed release
leaves the key to expire. A crash can therefore leave a name listed for up to
30 seconds after its last refresh. A hook blocked on stdout or unable to reach
Redis can disappear from who even though its process is alive.

Who is a recent-presence view, not a process-health or model-attention check.
Its scan can omit a key that expires before it is read.

## Waiting messages

```sh
nova-bus pending --as receiver
nova-bus pending --as receiver --list
```

The first line is the number of unacknowledged entries. `--list` adds
`<id><TAB><subject>` lines, oldest first, escaping subjects as hook does.
Neither command consumes messages.

The count uses the stream length. The list reads the stream in pages of 1,000
and collects the entries in memory. Count and list are separate operations;
they can differ while senders and receivers are active.

Older builds kept acknowledged payloads in the stream. When a hook finds
previously delivered, unacknowledged entries, it trims history older than the
first of those entries. A newly delivered entry is not used as a trim point.
Until that cleanup occurs, pending can count the old history too.

## Stored data

| Data | Redis representation |
|---|---|
| Inbox | Stream `bus:<name>`. Each entry has string fields `from`, `to`, `subject`, `body` and `re`. `to` retains the original comma list; absent `re` is empty. |
| Delivery position | Consumer group `hook`, consumer `hook`. The group starts at ID `0`, so it includes messages sent before the first hook. |
| Presence | String key `bus:presence:<name>` containing the UTC RFC 3339 start time, a tab and the hook's token. |

An acknowledged payload is deleted, so an inbox normally holds only waiting
messages. Operators choose Redis persistence and backups. Removing stream or
group data can affect replay and pending counts.

The new stream layout does not read the old `bus2:*` inboxes. See
[migration](MOVED.md) before replacing an older bus.

## Help and exits

`nova-bus help`, `-h`, `--help` and no arguments print the whole usage.
`help <verb>`, `<verb> -h` and `<verb> --help` print that verb's usage. Help exits
0 with empty stderr.

A bad invocation is rejected before Redis operations begin. Its stderr line
names the problem and points to the verb's help. Missing required inputs are
listed together, including the Redis address. Other errors identify an unknown
verb or flag, a flag belonging to another verb, an invalid recipient, `--re -`,
or the wrong number of arguments. The tool suppresses go-redis's own log lines.

| Code | Meaning |
|---|---|
| 0 | Success, help, or a hook stopped by SIGINT or SIGTERM. |
| 1 | An operation failed or was refused, including a held hook name or a stdout error. |
| 2 | Bad flags or arguments, or missing required configuration. |

## Recover from a refusal

| Result | Next step |
|---|---|
| Missing Redis address, sender, recipient, subject or body | Supply all named inputs; `nova-bus help send` shows their order. |
| Unknown verb or flag, late flag, invalid name or reply ID | Correct the invocation using that verb's help. `--re` takes a real message ID, not `-`. |
| Redis connection or authentication failure | Check `NOVA_BUS_REDIS`, server availability and `NOVA_BUS_PASSWORD`. `nova-bus who` checks access without sending a message. |
| `SEND FAILED` after some recipients printed IDs | Keep the successful IDs. A failed append may also have reached Redis; check with the recipient before retrying work that cannot tolerate duplicates. |
| `name already hooked` | Use the existing receiver, stop it before replacing it, or choose another identity. A crashed receiver's key expires. |
| Hook stdout error | Restore the output reader, then restart the hook. Expect a duplicate of a line whose acknowledgment did not finish. |
| Hook Redis error | Restore connectivity. The running hook retries; an exited hook needs restarting. |

## Build, tests and models

From the repository root:

```sh
build_dir=$(mktemp -d)
go build -o "$build_dir/nova-bus" ./bus
"$build_dir/nova-bus" help
go test ./bus
go test -tags functional ./bus
```

Unit tests use an in-memory store and a fake clock. Functional tests start a
private Redis server and skip if `redis-server` is absent. The package uses the
standard library and go-redis; its import test checks that boundary.

[Bus.tla](../bus/tla/Bus.tla) models print-before-ack and replay under its crash
and progress assumptions. [Presence.tla](../bus/tla/Presence.tla) models token
claims, refresh, expiry, blocked stdout and release. Its strict stuck-hook case
shows why a live process can lose presence. Reversed witnesses remove individual
rules to check that the model detects those changes.

The automatic presence cases use two hooks. `PresenceThree.cfg` checks three
hooks and runs separately by hand. [CASES.tsv](../bus/tla/CASES.tsv) lists the
automatic cases and expected outcomes. Run them with
`tla/check.sh /absolute/path/to/tla2tools.jar bus/tla`; the
[testing guide](TESTING.md#models) lists the runner's prerequisites. A model result
does not establish delivery into a native harness or the durability of a Redis
installation.
