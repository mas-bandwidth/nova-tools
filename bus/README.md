# nova-bus

nova-bus sends short messages between named inboxes kept as Redis streams. A process hooks in to its inbox and reads
every message as one line on stdout, so anything that can watch a process's output can receive them.

## The two verbs

```
nova-bus send [--as <me>] --to <name>[,<name>] [--re <id>] <subject> <body | ->
nova-bus hook --as <name>
```

**send** appends one message (from, to, subject, body, re, time) to the stream `bus:<name>` of each recipient and
prints `<name> <id>` for each. A body of `-` is read from stdin. `--re <id>` names the message this one answers. The
sender is `--as`, else the environment variable `NOVA_BUS_AS`; with neither, send refuses.

**hook** prints every message still waiting for `<name>`, including ones an earlier hook printed but did not finish
marking handled, then blocks and prints each new one as it arrives. One message is one line, tab-separated:

```
<id>	<from>	<re or ->	<subject>	<body>
```

Newlines, carriage returns, tabs and backslashes inside a field are written as `\n`, `\r`, `\t` and `\\`. A message
is marked handled (XACK) only after its line is written to stdout, so a restarted hook resumes exactly where it
stopped. Delivery is at least once: a message whose line was written just before the hook was killed, and not yet
marked, is printed again by the next hook; a marked message is never printed again. The hook runs until killed and
reconnects by itself when the connection drops. Run one hook per name.

`nova-bus help` prints the usage.

## Environment

| Variable | |
|---|---|
| `NOVA_BUS_REDIS` | the Redis address, `host:port` (required) |
| `NOVA_BUS_PASSWORD` | the Redis password (optional; never printed or logged) |
| `NOVA_BUS_AS` | the sender's name for `send` when `--as` is not given |

## Hooking a session in

In Claude Code, run `nova-bus hook --as <name>` under the Monitor tool: each line it prints is one message, delivered
to the session as it arrives. Answer with `nova-bus send --to <from> --re <id> <subject> <body>`.

Any other harness does the same with whatever it has that watches a process's stdout: start
`nova-bus hook --as <name>`, treat each line as a message, and restart the process if it exits.

## Build and test

```
go build -o nova-bus .
go test .                           # unit tests, an in-memory store
go test -tags functional .          # against a real redis-server on a free local port (skipped without one)
```

`tla/Bus.tla` models the inbox, the sender and a hook that crashes between print and ack: no message is acked before
it is printed, no acked message is printed again, every sent message is printed. `tla/BusBroken*.cfg` are reversed
witnesses (ack before print; a restarted hook that skips its pending messages) that TLC must fail.
