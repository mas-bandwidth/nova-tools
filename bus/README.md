# nova-bus

nova-bus sends short messages between named inboxes kept as Redis streams. A process hooks in to its inbox and reads
every message as one line on stdout, so anything that can watch a process's output can receive them.

## The verbs

```
nova-bus send --as <me> --to <name>[,<name>] [--re <id>] <subject> <body | ->
nova-bus hook --as <name>
nova-bus who
nova-bus pending --as <name> [--list]
```

**send** appends one message (from, to, subject, body, re) to the stream `bus:<name>` of each recipient and
prints `<name> <id>` for each. A body of `-` is read from stdin. `--re <id>` names the message this one answers.
`--as` is required on both verbs: on send it is the sender, on hook the inbox.

**hook** prints every message still waiting for `<name>`, including ones an earlier hook printed but did not finish
marking handled, then blocks and prints each new one as it arrives. One message is one line, tab-separated:

```
<id>	<from>	<re or ->	<subject>	<body>
```

Newlines, carriage returns, tabs and backslashes inside a field are written as `\n`, `\r`, `\t` and `\\`. A message
is marked handled (XACK) only after its line is written to stdout, so a restarted hook resumes exactly where it
stopped. Delivery is at least once: a message whose line was written just before the hook was killed, and not yet
marked, is printed again by the next hook; a marked message is never printed again. The hook runs until killed and
reconnects by itself when the connection drops; a plain kill at any moment is safe.

While a hook runs it holds the key `bus:presence:<name>` (TTL 30 s, set again every 10 s, value its start time) and
deletes it on SIGINT or SIGTERM. **who** prints `<name><TAB><uptime>` for each present name, sorted; a crashed hook
drops out within 30 s. **pending** prints how many messages wait for `<name>` unacked, and with `--list` their
`<id><TAB><subject>`, consuming nothing. There is no ack verb: hook acks after printing, and that is the one way.

Run one hook per name; two hooks on the same name split its messages.

`nova-bus help` prints the usage.

## Environment

| Variable | |
|---|---|
| `NOVA_BUS_REDIS` | the Redis address, `host:port` (required) |
| `NOVA_BUS_PASSWORD` | the Redis password (optional; never printed or logged) |

## Hooking a session in

In Claude Code, run `nova-bus hook --as <name>` under the Monitor tool: each line it prints is one message, delivered
to the session as it arrives. Answer with `nova-bus send --as <name> --to <from> --re <id> <subject> <body>`.

Any other harness does the same with whatever it has that watches a process's stdout: start
`nova-bus hook --as <name>`, treat each line as a message, and restart the process if it exits.

## Build and test

```
go build -o nova-bus .
go test .                           # unit tests, an in-memory store
go test -tags functional .          # against a real redis-server on a free local port (skipped without one)
```

`docs/SPEC-BUS.md` is the full description. `tla/Presence.tla` models the presence key and a hook that starts,
refreshes, exits and crashes; `tla/PresenceBroken*.cfg` are its reversed witnesses. `tla/Bus.tla` models the inbox, the sender and a hook that crashes between print and ack: no message is acked before
it is printed, no acked message is printed again, every sent message is printed. `tla/BusBroken*.cfg` are reversed
witnesses (ack before print; a restarted hook that skips its pending messages) that TLC must fail.
