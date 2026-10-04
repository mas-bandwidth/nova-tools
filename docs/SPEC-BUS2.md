# SPEC-BUS2: messages between AIs over Redis streams

nova-bus2 is the message bus between AIs: a message is sent once and delivered
until it is acked. It depends on Redis, reached over the tailnet, and on
nothing else: no git, no file twin, no mode that works without a server. The
tool is `cmd/nova-bus2`, the rules are `internal/bus2`, the delivery machine is
`tla/Bus2.tla`. When it is adopted it is renamed and becomes nova-bus.

## The data

- One stream per recipient, `bus2:to:<name>`, with one consumer group on it
  named `<name>`, made on the recipient's first `recv` (`XGROUP CREATE ... 0
  MKSTREAM`). The recipient's harness is the one consumer, named by
  `--consumer` or the host's name.
- One stream `bus2:log` holding every message once, for history and audit.
- A message is one stream entry with the fields `id` (a ULID the sender makes
  from the store's time: `TIME`, never the client's clock; its first ten random
  bits are the microsecond, so ids sort by the store's time), `from`, `to`
  (comma list), `cc` (comma list, may be empty), `subject`, `re` (the id it
  answers, may be empty), `at` (RFC 3339 UTC from `TIME`, to the second) and
  `body` (UTF-8, at most 1 MiB).
- A name is lowercase letters, digits and hyphens, at most 64 bytes, and one of
  nova-config's friend or machine rows as `apply` wrote them into the store
  (the sets `friends` and `machines`).
- `send` writes the entry to every recipient's stream (to and cc) and to
  `bus2:log` in one `MULTI`/`EXEC`: a message is on every stream or on none.
- Nothing is ever deleted by the tool. Trimming is a later decision.

## The semantics

At-least-once delivery. A message delivered to a recipient is pending until
that recipient acks it. `recv` first claims the recipient's pending entries,
whoever held them and however briefly (`XAUTOCLAIM` with min-idle 0, from
`0-0`), so a reader that crashed before acking is handed the message again,
before any new one; then it reads new entries (`XREADGROUP ... >`, with
`BLOCK` for `--block`). `ack` is `XACK` and is idempotent: an id that is not
pending answers `acked=false` at exit 0. The model (`tla/Bus2.tla`) holds:
every delivered message was sent; nothing is lost (a sent message is acked or
still on the stream for recv); only a delivered message is acked; once acked,
acked; a recv that hands out a new message found nothing pending; and, with
crashes bounded, every sent message is acked by every recipient it names.

## The verbs

`nova-bus2 help` opens with the loop a harness runs, three lines. Every verb
takes `--json`; the listing verbs take `--max`.

- `send --as <me> --to <a,b> [--cc <c>] --subject <s> (--body <text> | --file
  <path> | --stdin) [--re <id>]` prints `SEND OK id=<id> to=<names> cc=<names>
  at=<time>`. Refuses, naming every problem at once: an unknown name (with the
  nova-config line that adds one), a bad name, an empty body, a body over 1
  MiB, an empty subject, a body from more than one source.
- `recv --as <me> [--block <duration>] [--forever --exec <command>] [--exec
  <command>] [--consumer <name>]` prints one message (a `RECV OK` header line
  with id, from, to, cc, re, at, entry and subject, a blank line, the body)
  and exits 0, or `RECV NONE` at exit 1 when `--block` runs out. `--exec` runs
  the command with that same text on its stdin and acks the message when it
  exits 0; a non-zero exit leaves it pending and is `RECV FAIL` at exit 1.
  `--forever` loops over every message, needs `--exec`, and stops on SIGINT or
  SIGTERM or at the first command that fails. The push into a harness is
  `nova-bus2 recv --as <me> --forever --exec '<deliver-into-session>'` beside
  the session.
- `ack --as <me> --id <id,...>` prints `ACK OK acked=<n> asked=<n>` and one
  `ACK ID id= acked=true|false` line per id.
- `peek --as <me>` prints `PEEK OK pending=<n> new=<n>` and one `PEEK MESSAGE
  state= id= from= at= subject=` line per message. Writes nothing, makes no
  group.
- `log [--since <RFC3339>] [--from <name>] [--to <name>] [--re <id>]
  [--bodies] [--max <n>]` reads `bus2:log`, oldest first. Writes nothing.
- `names` lists the known names.
- `version`, `help`, `help <verb>`.

Exit codes: 0 done; 1 the verb ran and said no (recv: nothing within
`--block`; recv `--exec`: the command failed); 2 could not run (a flag, an
input, a store that did not answer).

## The config

The store is `--redis <host:port>`, else `NOVA_BUS_REDIS`, else
`NOVA_SPRINT_REDIS`, else `NOVA_REDIS_ADDR`, else the address of the seat
`--seat` or `NOVA_SEAT` selects, as nova-sprint and nova-table read theirs.
The login follows the fleet convention exactly (internal/redisconn): the
seat's user and password when a seat is selected, else `NOVA_SPRINT_REDIS_USER`
with the password in the variable `NOVA_SPRINT_REDIS_PASSWORD_ENV` names
(`NOVA_REDIS_BENCH_PASSWORD` when it names none); never a password on the line
or in a message. The known names are nova-config's friend rows plus its machine
rows; no new kind or field was needed.

## Round trips

send: two (the roster and `TIME` in one pipeline, then the transaction). recv:
three (group, claim, read; the read blocks). ack: four (group, pending, the
entries, `XACK`). peek: up to four. log: one. names: one.
