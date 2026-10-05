# SPEC-BUS: messages between AIs over Redis streams

nova-bus is the message bus between AIs: a message is sent once and delivered
until it is acked. It depends on Redis, reached over the tailnet, and on
nothing else: no git, no file twin, no mode that works without a server. The
tool is `cmd/nova-bus`, the rules are `internal/bus`, the delivery machine is
`tla/Bus2.tla`. It was built as nova-bus2 beside the git bus and took the name
nova-bus on 2026-10-04, when the git bus was removed.

## The data

- One stream per recipient, `bus2:to:<name>`, with one consumer group on it
  named `<name>`, made on the recipient's first `recv` (`XGROUP CREATE ... 0
  MKSTREAM`). Every reader is the one consumer `nova-bus2`: who holds an entry
  is told by its idle time, never by a name.
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
- The keys keep the `bus2:` prefix (`bus2:to:<name>`, `bus2:log`, and
  `bus2:keepalive:<name>`, the coordinator keepalive), and the consumer keeps its
  `nova-bus2` name, although the tool is nova-bus: the fleet's store already holds
  streams, groups and pending lists under these names, and renaming a live key
  is a migration (every reader stopped, every stream copied, every group
  re-made with its pending entries), bought for nothing but a spelling.

## The semantics

At-least-once delivery. A message delivered to a recipient is pending until
that recipient acks it, and its reader keeps it for fifteen minutes (`ClaimAfter`,
the budget one `--exec` delivery gets: longer than the longest delivery any
reader makes, nova-friend's ten minute turn and the kill that ends it, so a
live reader mid-turn is never handed its message twice). `recv` first claims
the recipient's pending entries that have been idle for at least that long
(`XAUTOCLAIM` with min-idle 900 s, from `0-0`): what a reader that died or
stalled was holding, so
a reader that crashed before acking is handed the message again, before any new
one, while a live reader is never handed a message a second time. Then it reads
new entries (`XREADGROUP ... >`; `--forever` waits with `BLOCK`, a plain `recv`
answers at once). A name the roster does not hold is refused, never given a
stream to wait on. `ack` is `XACK` and is idempotent: an id that is not pending
answers `acked=false` at exit 0. The model (`tla/Bus2.tla`) holds: every message
on a stream was sent to that recipient; nothing is lost (a sent message is acked
or still on the stream for recv); a message held by a live reader stays with it
(delivered once while held); a recv that hands out a new message found nothing
pending that a dead reader held; only a delivered message is acked; once acked,
acked; and, with crashes bounded, every sent message is acked by every recipient
it names.

## The verbs

`nova-bus help` opens with the loop a harness runs, three lines. Every verb
takes `--json`; `log` takes `--max`; every verb takes `--timeout <d>` and
`recv` takes `--block <d>` (the deadlines).

- `send [--as <me>] --to <a,b> [--cc <c>] --subject <s> (--body <text> |
  --stdin) [--re <id>]` prints `SEND OK id=<id> to=<names> cc=<names>
  at=<time> bytes=<n> sha256=<hex>` (and `login=none` on a store with no
  users): the count and the digest are the body's as the store holds it, the
  sender's check that a file arrived whole without asking the receiver. A body's
  trailing newline is the body's and is kept by send, the store, log and recv. Refuses, naming every problem at once: an unknown name (with the
  nova-config line that adds one), a bad name, an empty body, a body over 1
  MiB, an empty subject, a body from both or neither source.
- `recv [--as <me>] [--max <n> | --all] [--ack] [--exec <command>] [--forever --exec
  <command>]` prints one message (a `RECV OK` line with id, from, to, cc, re, at and subject, a blank
  line, the body) and exits 0, or `RECV NONE` at exit 1 when nothing waits.
  `--exec` runs the command with that same text on its stdin (the body ending
  in a newline) and acks the message when it exits 0; a non-zero exit leaves it
  pending and is `RECV FAILED` at exit 1. `--max <n>` takes up to n messages in
  order and `--all` every one waiting (pending first, then new), each printed
  as its own `RECV OK`, or each handed to `--exec` and acked on exit 0, the
  batch stopping at the first command that fails; `--ack` acks each message a
  plain recv printed; none waiting is the one `RECV NONE`. `--forever` loops,
  waiting for messages, needs `--exec`, and stops on SIGINT or SIGTERM (a message being
  delivered stays pending) or at the first command that fails; one wait of
  the loop is `--block <d>` (30 s by default, its deadline the block plus
  ten seconds, the deadlines). The push into a
  harness is `nova-bus recv --as <me> --forever --exec '<deliver-into-session>'`
  beside the session.
- `ack [--as <me>] --id <id,...>` prints `ACK OK acked=<n> asked=<n>` and one
  `ACK ID id= acked=true|false` line per id.
- `peek [--as <me>]` prints `PEEK OK pending=<n> new=<n>` and one `PEEK MESSAGE
  state= id= from= at= subject=` line per message. Writes nothing, makes no
  group.
- `log [--bodies] [--max <n>]` reads `bus2:log`, oldest first. Writes nothing.
- `names` lists the known names.
- `version`, `help`, `help <verb>`.

Exit codes: 0 done; 1 the verb ran and said no (recv: nothing waiting; recv
`--exec`: the command failed); 2 could not run (a flag, an input, a store that
did not answer).

### bus-message-kinds.w1: the kind of a message

A message carries a kind, one of `report`, `ack`, `status`, `request`,
`blocker`: the bus's own vocabulary, which it stores and filters on and gives
no meaning. `send --kind <k>` sets it (default `status`; another word is
refused with the list), the entry holds it as the field `kind`, and `recv`,
`peek` and `log` print `kind=<k>` (left off the line of a status, so an absent
`kind=` is a status and the common line is unchanged). A message with no `kind` field (sent
before kinds) reads as `status`. `recv --kind <k>[,<k>]` (also `--all`,
`--max`, `--forever`, `--dry-run`) and `peek --kind <k>[,<k>]` take only
messages of those kinds. A message the filter skips is neither acked nor held:
it is claimed with the filter's read and handed back at once (`XCLAIM ...
IDLE` of `ClaimAfter`, `JUSTID`), so the next `recv` without the filter, or
with another, gets it in its order; a skip costs a round trip, and a run of
skipped claimed messages one more to hand them back.

### fr-delivery-receipts.w1: receipts, and the send alarm

A message to a friend is owed her session's receipt; the stream's ack is not
one. `send` marks the message, in its own transaction, on the hash
`bus2:owed:<friend>` (field the message's id, value its `at`) for every
friend it names in to or cc but the sender; a message to a machine is owed
nothing (the friends are the set `friends`, read in the roster's trip, so
send stays two round trips). A receipt clears the mark (`HDEL`) and is
idempotent. Only the session gives one: `ack --id` (the session's verb, a
receipt for every id it names, pending or not, so a daemon that acked the
stream first takes nothing from it), or a message from the friend naming
the one it answers (`re`), cleared in the reply's own transaction. The
daemon's ack at the end of a turn (`XACK`) and `recv --ack`/`--exec` are no
receipts. A ping is answered by the daemon's `daemon-pong`, which names it
(`re`): a ping is the daemon's, never pushed into the session, and that is
its receipt. `Bus.Undelivered` reads each friend's hash in one trip
(`HGETALL` in a pipeline): the count and the oldest by its `at`, what the
friends table shows as undelivered and the oldest undelivered age
(SPEC-FRIEND.md). The keys are written by the tool and cleared by a receipt;
an entry of a stream is still never deleted.

A send that fails on the login (`NOAUTH`, `WRONGPASS`, as
internal/redisconn classifies it) or on the connection is an outage, said
by `bus.Watch` as one alarm naming the store and the user, raised at the
first such failure, counting every later one, and cleared at the next send
that succeeds (the clear says how many failed). The alarm's reason is the
store's refusal word for a login, never the rest of its text, and the
transport's error for a connection; it never holds a password. A refusal of
the message itself (an unknown name, an empty body) is the sender's mistake
and neither raises nor clears. The alarm goes to the coordinator by the
caller's `Raise`, never over the bus that is failing.

The ACL line below gains `~bus2:owed:*` and `+hset +hdel +hgetall` for a
store with users: a sender marks the recipients' hashes, as it writes their
streams.

## The identity

Who a verb acts as is the user the connection logged in as, never a word on
the line. With a login user (`NOVA_SPRINT_REDIS_USER`, or whatever
internal/redisconn resolves), `--as` defaults to that user, may repeat it, and
any other name is refused: `--as bob is not the login user ada: this connection
acts as ada; drop --as, or log in as bob`. `send`'s `from` is that identity.
With no login user (a store whose default user is open, as a trial store is)
`--as` is required and every write (`SEND OK`, `RECV OK`, `ACK OK`) carries
`login=none`, so the weakness (any name on the line is believed) is visible,
never silent. The fleet's store therefore needs one user per friend, named as
the friend is; creating users is the owner's, never the tool's.

## The ACL per friend

The user for friend `<f>` is named `<f>` and needs, measured against what
the tool sends (`internal/bus/redis.go`; the key flags are what `COMMAND
INFO` on Redis 8 answers):

| Verb | Commands | Keys |
| --- | --- | --- |
| every verb | `HELLO` (the login), `PING` (redisconn's probe) | none |
| send | `SMEMBERS`, `TIME`, `MULTI`, `XADD`, `HSET`, `HDEL`, `EXEC` | `friends`, `machines` (read); `bus2:to:<every recipient>` and `bus2:log` (XADD: read-write by its key flag); `bus2:owed:<every friend recipient>`, and the sender's own when it answers (re) |
| recv | `SMEMBERS`, `XGROUP CREATE`, `XAUTOCLAIM`, `XREADGROUP`, `XACK` | `friends`, `machines`; `bus2:to:<f>` |
| ack | `XINFO GROUPS`, `XPENDING`, `XRANGE`, `XACK`, `HDEL` | `bus2:to:<f>`, `bus2:owed:<f>` |
| peek | `XINFO GROUPS`, `XPENDING`, `XRANGE` | `bus2:to:<f>` |
| log | `XRANGE` | `bus2:log` |
| names | `SMEMBERS`, `TIME` | `friends`, `machines` |

The wrinkle, said plainly: a sender writes other friends' streams. `send` fans
the message out from the client, one `XADD` per recipient stream inside the
transaction, so every friend's user must be able to `XADD` to every
`bus2:to:*` stream. `XADD`'s key flag is read-write (`RW update`), so a
write-only selector (`%W~bus2:to:*`) does not admit it: the least key set is
the whole pattern `~bus2:to:*`, and the store cannot stop user `ada` reading
`bus2:to:bob` with `XRANGE`, or acking on it with `XACK`. What stops that is
the tool (the identity above), not the ACL. The ACL still does the two things
that matter: it pins `from` to a real login (no user, no send as anyone), and
it keeps every other key family (the sprint's, the config's) out of reach.
The least set per friend, one line:

```
ACL SETUSER <f> on >(password) ~bus2:to:* ~bus2:log ~bus2:owed:* ~friends ~machines resetchannels
  +hello +ping +smembers +time +multi +exec +xadd +xgroup|create +xreadgroup
  +xautoclaim +xack +xpending +xinfo|groups +xrange +hset +hdel +hgetall
```

If the fan-out moved into the store (a Redis function running `XADD` for the
caller, which Redis runs under the caller's own ACL, so it buys nothing; or a
privileged relay process, which is a second writer), the pattern could narrow
to `%W~bus2:to:*` plus `~bus2:to:<f>`. Neither is built; the one line above is
what the bus needs today.

## The config

The store is `--redis <host:port>`, else `NOVA_BUS_REDIS`, else the fleet row's
`bus` field as `nova-config apply` wrote it (`fleet:bus`, in the sprint store at
`NOVA_SPRINT_REDIS`, with the fleet's login below), so no friend types the
address: `nova-config fleet set --bus <host:port> --as <me>`, then `apply`, once.
With none of the three, or an empty row, or a sprint store that does not
answer, the refusal names the row and how it is set. The store is on loopback or
the tailnet (100.64.0.0/10) and nowhere else: the tailnet is the boundary and
there is no ACL behind it (decided 2026-10-04), so an address outside both, by
literal or by any address its name resolves to, is refused before a dial in one
line naming the rule (`internal/bus`, `CheckAddr`); a name that does not
resolve is refused the same way. A Unix socket path is this machine's. The login follows
the fleet convention exactly (internal/redisconn): `NOVA_SPRINT_REDIS_USER`
names the user and `NOVA_SPRINT_REDIS_PASSWORD_ENV` the variable that holds its
password (`NOVA_REDIS_BENCH_PASSWORD` when it names none); never a password on
the line or in a message. The known names are nova-config's friend rows plus
its machine rows; no new kind or field was needed.

## The deadlines

Every network step the client makes is bounded (internal/redisconn): the dial
5 s, one attempt and no second (the custom dialer, `DialTimeout`); the write of
every command 5 s; the read of every reply 5 s; the wait for a pool connection
5 s; the open, the dial and the handshake together, 5 s. A command that asks
the store to block (a recv wait) is read under the time it asked for and ten
seconds more (go-redis v9.22.0, `cmdTimeout`); its write is still the 5 s
write bound. Before the rule below, that was the whole of it: a store that
accepted and then stalled, a host whose load average is tens, cut one call at
the connection's own bound and answered go-redis's bare words, `i/o timeout`,
with no address and no remedy, and a read that could have been sent again
failed the verb.

go-redis puts a socket deadline of the sooner of the call's context and the
client's read or write bound on every command. So a deadline longer than the
connection's own is not the deadline that fires unless the connection's bounds
move with it, and the rule moves them.

The rule, on every store call:

- A call that answers at once runs under `--timeout <d>` (`bus.CallTimeout`,
  5 s by default, the same number as the connection's own read bound); the flag
  is on every verb and wants a Go duration above zero. The deadline is the
  call's context and also the connection's read and write bound for that call:
  a `--timeout` above 5 s raises both to it for the call and puts them back
  after, so the bound that fires is the bound named. The dial is not part of
  it: it stays `redisconn.DialTimeout` (5 s), and a redial that honors
  `--timeout` would be a change to internal/redisconn/open.go, proposed and
  not made here.
- A blocking recv wait runs under its block plus `bus.BlockMargin` (10 s, the
  margin the connection gives a blocking command), never under `--timeout`:
  `--block <d>` (30 s by default) names how long one wait of `recv --forever`
  looks before it looks again, so a signal is seen within one wait. The
  write of the wait's command is raised to the same deadline.
- A deadline that ran out is refused in one line that names the deadline and
  the address, never a login or a password: `redis did not answer within <d>
  at <host:port>: the host may be overloaded (load average), try again`.
- A read that changes nothing (the roster, pending, group info, the log, the
  entries: what `names`, `peek` and `log` read, and the reads before a write)
  is sent once more when the first ran past its deadline, under a deadline of
  its own, and the second answer stands; so a verb that only reads waits at
  most 2 x `--timeout` before its refusal.
- A write, a delivery, a release or an ack is sent at most once (`send`'s
  transaction, a `recv` delivery, an `ack`), so a stalled store can duplicate
  nothing.

Measured on a stalled in-process store (`internal/bus/timeout_test.go`,
`TestARedisCallThatStallsFailsWithinTheNamedTimeout`, every call named 200 ms
while the test client's own read and write bounds are 1 ms, so a call cut by
the connection's bound and not its own fails the test; a Linux bench):
a read that changes nothing is refused after its two attempts, 2 dials, 0.40 s
(2 x 200 ms); a send, 1 dial, 0.20 s; an ack, 1 dial, 0.20 s; a blocking wait
with a 100 ms block and a 100 ms margin, 1 dial, 0.20 s (block plus margin).
In every one the refusal names 200 ms and the longest socket deadline the
client set was the call's, not the connection's 1 ms. At the shipped numbers
the same shapes are 10 s for a read, 5 s for a send or an ack, and the block
plus 10 s for a wait; a `--timeout 30s` is 60 s for a read and 30 s for a
send. Before the change the same calls answered `i/o timeout` at 1 dial, no
retry, no address, and a `--timeout` longer than 5 s was cut at 5 s while the
refusal named the longer one. The worst a verb waits is 2 x `--timeout` (a
read) or one block plus the margin (a recv wait), plus a dial of at most 5 s
each; nothing waits unbounded.

## Round trips

send: two (the roster and `TIME` in one pipeline, then the transaction). recv:
four (the roster, the group, the claim, the read). ack: five (group, pending,
the entries, `XACK`, the receipt's `HDEL`). peek: up to four. log: one. names: one.
