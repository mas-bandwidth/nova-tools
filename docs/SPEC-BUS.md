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
nova-bus help
```

- **send** appends one message to the inbox of each recipient and prints
  `<name> <id>` for each. A body of `-` is read from stdin. `--re <id>` names
  the message this one answers.
- **hook** prints every message still waiting for `<name>`, then blocks and
  prints each new one as it arrives, and holds `<name>`'s presence while it
  runs. It runs until killed or interrupted.
- **who** prints `<name><TAB><uptime>` for each name whose hook is running,
  sorted by name; uptime is a Go duration to the second (`1h2m3s`), or `?` if
  the stored start time cannot be read.
- **pending** prints the number of messages waiting for `<name>` that no hook
  has acked; with `--list`, then `<id><TAB><subject>` for each, oldest first.
  It consumes nothing.
- **help** prints the usage. Every verb exits 0 on success and 1 on a refusal
  or a Redis error; a flag a verb does not take is refused.

There is no ack verb: hook acks each message after printing its line, and that
is the one way a message is marked handled.

A name may not contain `:`. Every verb but who and help needs `--as`.

## Environment

| Variable | |
|---|---|
| `NOVA_BUS_REDIS` | the Redis address, `host:port` (required) |
| `NOVA_BUS_PASSWORD` | the Redis password (optional; never printed) |

Each process keeps one Redis connection and runs its commands in turn.

## Wire format

- The inbox of `<name>` is the stream `bus:<name>`. A message is one entry
  with the fields `from`, `to` (the comma list as given), `subject`, `body`
  and `re` (empty when none), all strings.
- Every hook reads with the consumer group `hook` and the consumer `hook`. The
  group is created at id `0` on the first hook (`XGROUP CREATE ... 0
  MKSTREAM`), so messages sent before any hook are delivered too.
- A hook's line is tab-separated:

  ```
  <id>	<from>	<re or ->	<subject>	<body>
  ```

  Backslash, newline, carriage return and tab inside a field are written as
  `\\`, `\n`, `\r` and `\t`, so a line never splits. pending escapes the
  subject the same way.
- Presence of `<name>` is the string key `bus:presence:<name>`, whose value is
  the hook's start time in RFC 3339 UTC to the second.

## Delivery

A hook first reads its pending entries (`XREADGROUP ... 0`: handed to an
earlier hook and never acked), then new ones (`XREADGROUP ... >`, at most 100 at
a time). For each entry it writes the line to stdout and only then acks it
(`XACK`). So:

- a restarted hook resumes where the last one stopped;
- delivery is at least once: a hook killed after writing a line and before
  acking it leaves the entry pending, and the next hook prints it again;
- an acked message is never printed again.

pending counts the entries the group holds unacked (`XINFO GROUPS`,
`XPENDING`) plus every entry after the group's last-delivered id (`XRANGE`);
with no group yet, the whole stream.

Run one hook per name. Two hooks on the same name split its messages between
them, and either's clean exit deletes the presence the other holds until its
next refresh.

## Presence

Presence means one thing: a hook process is alive for this name. There is no
challenge, no proof and no role.

- On start, before it reads, the hook sets the key with a TTL of 30 s
  (`SET ... EX 30`).
- It sets the key again whenever 10 s have passed since the last set. Each read
  waits at most 1 s, so a quiet hook refreshes on time and a signal is acted
  on within a second.
- On a clean exit (SIGINT, SIGTERM, or stdout failing) it deletes the key.
- who scans `bus:presence:*` and reads each key; one that expires in between
  is left out.

Uptime is the reader's clock minus the hook's start time; clocks that disagree
skew it, and a negative uptime shows as `0s`.

`bus/tla/Presence.tla` models the key (with its TTL), a hook process that
starts, refreshes, exits and crashes, and expiry. It checks that a name is
present only if a hook set the key within the TTL, that a running hook is
always present, that a clean exit leaves the name absent, and that a crashed
hook's presence goes away. Its reversed witnesses, which TLC must fail:
`PresenceBrokenNoExpire.cfg` (set without a TTL, never refreshed: still shown
present) and `PresenceBrokenNoRefresh.cfg` (never refreshed: a running hook
drops out). `bus/tla/Bus.tla` models delivery the same way, with
`BusBroken*.cfg` as its witnesses.

## Failure

- **A hook dies before acking.** The message stays pending and is printed again
  by the next hook on that name.
- **A hook crashes or its machine is lost.** Its presence lapses within 30 s;
  until then who still lists it.
- **Redis is unreachable.** send and pending and who exit 1 with the error. A
  hook logs `nova-bus: <error>; reconnecting` to stderr and retries every
  second; it cannot refresh meanwhile, so its presence may lapse until it is
  back.
- **stdout fails** (the reader went away). The hook deletes its presence and
  exits 1.

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
