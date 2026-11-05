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
nova-bus hook --as <name>
nova-bus who
nova-bus pending --as <name> [--list]
nova-bus help [<verb>] | -h | --help | <verb> -h
```

- **send** appends one message to the inbox of each recipient, in the order
  given, and prints `<name> <id>` for each. A body of `-` is read from stdin.
  `--re <id>` names the message this one answers.
- **hook** prints every message still waiting for `<name>`, then blocks and
  prints each new one as it arrives, and holds `<name>`'s presence while it
  runs. It runs until killed.
- **who** prints `<name><TAB><uptime>` for each present name, sorted by name;
  uptime is a Go duration to the second (`1h2m3s`), or `?` when the key does
  not hold a start time (including a key that is not a string).
- **pending** prints the number of messages waiting for `<name>` that no hook
  has acked; with `--list`, then `<id><TAB><subject>` for each, oldest first.
  It consumes nothing.
- **help**, `-h`, `--help` or no arguments print the whole usage to stdout;
  `help <verb>`, `<verb> -h` and `<verb> --help` print that verb's usage.
  Help exits 0 and writes nothing to stderr.

There is no ack verb: hook acks each message after printing its line, and that
is the one way a message is marked handled.

## Exit codes and refusals

| Code | When |
|---|---|
| 0 | done; help; a hook stopped by SIGINT or SIGTERM |
| 1 | refused or failed: a Redis error, `SEND FAILED`, `name already hooked`, stdout failing |
| 2 | a malformed invocation |

A malformed invocation is refused in one stderr line,
`nova-bus <verb>: <what is wrong>; run: nova-bus <verb> -h`, with nothing on
stdout and nothing done in Redis. Missing inputs are named all at once
(`missing --as <name>, --to <name>, <subject> <body>, NOVA_BUS_REDIS
(host:port)`). Each other case is named on its own: an unknown flag, or a flag
the verb does not take (`unknown flag --x`); a flag without a value; a bad
`--list` value; a name containing `:`; `--re -`; an empty name in `--to`
(`a,,b`); more than a subject and a body; an argument to hook, who or pending
("takes no arguments"). An unknown verb (`ack` is one) gets
`nova-bus: unknown verb "<verb>"; verbs: send, hook, who, pending, help; run:
nova-bus help`. go-redis's own logging is silenced: every message on stderr is
nova-bus's.

## Environment

| Variable | |
|---|---|
| `NOVA_BUS_REDIS` | the Redis address, `host:port` (required); Redis 7 or later |
| `NOVA_BUS_PASSWORD` | the Redis password (optional; never printed) |

Each process keeps one Redis connection and runs its commands in turn. The
client never retries a command by itself.

## Wire format

- The inbox of `<name>` is the stream `bus:<name>`. A message is one entry
  with the fields `from`, `to` (the comma list as given), `subject`, `body`
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

A hook reads its pending entries first (`XREADGROUP ... 0`: handed to a hook
and never acked), then new ones (`XREADGROUP ... >`, up to 100 at a time). For
each entry it writes the line, then acks it and deletes it (`XACK` and `XDEL`
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

pending's count is the group's pending count plus its lag (`XINFO GROUPS`), or
`XLEN` when there is no group yet; constant time. `--list` reads the entries
(`XPENDING`, `XRANGE` 1000 at a time) and is O(entries waiting).

## Presence

Presence means one thing: a hook process is alive for this name.

- **One hook per name.** On start the hook claims the key: one Lua script sets
  it with a 30 s TTL unless it holds another process's value. If it does, the
  hook prints `HOOK WAIT name=<name> ttl=30` to stderr and tries again every
  second for at most 40 s (the TTL plus one refresh). A hook restarted by a
  supervisor after a crash so waits out the dead hook's key and claims the
  name within about 30 s. If the key is still held after 40 s, another hook is
  alive and refreshing it: the hook prints `name already hooked: <name>` and
  exits 1, touching nothing. A signal while waiting exits 0.
- It claims again whenever 10 s have passed. Each read waits at most 1 s, so a
  quiet or a busy hook refreshes on time, and a signal is acted on within a
  second. A refresh that finds another value (this hook lapsed and another
  took the name) ends this hook the same way.
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
and the wait, one each. `Presence.cfg` checks two hooks; `PresenceThree.cfg` checks three (about 3.9M states, minutes on many workers, so it is run by hand rather than by `tla/check.sh`). `bus/tla/Bus.tla` models delivery. `bus/tla/CASES.tsv` names
every case with the outcome TLC must reach; `tla/check.sh JAR bus/tla` runs
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
