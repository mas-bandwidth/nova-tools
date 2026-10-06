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
- One hash `bus2:push`, field `<name>`, value the name's inbox push proof as
  JSON (`harness`, `nonce`, `proven`, `up`, `reason`, `at`), written by the
  friend daemon and read by `send`, `recv` and `names` (below,
  bus-requires-inbox-push-proof).
- One string key per sender and send token, `bus2:sent:<from>:<token>`, holding
  the token's record as JSON (`fingerprint`, `id`, `at`), written in the
  send's own atomic step and expiring at the token's cleanup (below,
  a-lost-send-response-is-safe-to-retry.w1).
- Nothing is ever deleted by the tool. Trimming is a later decision. The one
  key the store removes is a token's record, by its own expiry.
- The keys keep the `bus2:` prefix (`bus2:to:<name>`, `bus2:log`, and
  `bus2:keepalive:<name>`, the coordinator keepalive), and the consumer keeps its
  `nova-bus2` name, although the tool is nova-bus: the fleet's store already holds
  streams, groups and pending lists under these names, and renaming a live key
  is a migration (every reader stopped, every stream copied, every group
  re-made with its pending entries), bought for nothing but a spelling.

## The semantics

At-least-once delivery, said plainly: a reader may be handed one message
more than once (a reader that died before its ack, a skipped kind handed
back), and tells a second delivery by the message's `id`; the bus never
promises exactly once to a consumer. What it does promise since
a-lost-send-response-is-safe-to-retry.w1 is the other end: a send carrying a
token makes one logical message however often it is retried (below).
A message delivered to a recipient is pending until
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
takes `--json`; `log` takes `--max`.

- `send [--as <me>] --to <a,b> [--cc <c>] --subject <s> (--body <text> |
  --stdin) [--re <id>]` prints `SEND OK id=<id> to=<names> cc=<names>
  at=<time> bytes=<n> sha256=<hex>` (and `login=none` on a store with no
  users): the count and the digest are the body's as the store holds it, the
  sender's check that a file arrived whole without asking the receiver. A body's
  trailing newline is the body's and is kept by send, the store, log and recv. Refuses, naming every problem at once: an unknown name (with the
  nova-config line that adds one), a bad name, an empty body, a body over 1
  MiB, an empty subject, a body from both or neither source. Then, the message
  being whole, a sender or recipient with no proven inbox push, one `deaf:`
  line each, writing nothing (bus-requires-inbox-push-proof, below).
  `--token <t> [--token-life <d>] [--token-cleanup <d>]` makes the send safe
  to retry (a-lost-send-response-is-safe-to-retry.w1, below).
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
  delivered stays pending) or at the first command that fails. The push into a
  harness is `nova-bus recv --as <me> --forever --exec '<deliver-into-session>'`
  beside the session. A recipient with no proven inbox push is refused
  (`deaf:`), `--dry-run` and `--forever` alike; a loop whose proof goes stale
  stops at its next read with that refusal.
- `ack [--as <me>] --id <id,...>` prints `ACK OK acked=<n> asked=<n>` and one
  `ACK ID id= acked=true|false` line per id.
- `peek [--as <me>]` prints `PEEK OK pending=<n> new=<n>` and one `PEEK MESSAGE
  state= id= from= at= subject=` line per message. Writes nothing, makes no
  group.
- `log [--bodies] [--max <n>]` reads `bus2:log`, oldest first. Writes nothing.
- `names` prints `NAMES OK count=<n> proven=<n>` and one line per known name:
  `NAMES NAME name= push=<proven|stale|down|none> age=<age|never> harness=<h>`.
- `version`, `help`, `help <verb>`.

Exit codes: 0 done; 1 the verb ran and said no (recv: nothing waiting; recv
`--exec`: the command failed); 2 could not run (a flag, an input, a store that
did not answer).

### a-lost-send-response-is-safe-to-retry.w1: a send under a token is one message

The finding (a review of the bus, item 4): a send made a fresh id and appended with
`XADD *` on every call, and answered nothing useful when the transaction's
response failed, so a sender whose write committed and whose answer was lost
(a cut connection, a deadline on the way home) could only send again, and
that made a second logical message on every stream.

So a send may carry a **token**: the caller's word for one logical send, the
same on every retry of it (`send --token <t>`, `Message.Token`; letters,
digits, `.`, `_`, `:` and `-`, at most 128 bytes; the token is the sender's
and is not on the entry). The send's **fingerprint** is the SHA-256 of its
arguments as the check normalised them: from, to and cc (sorted, each once),
subject, re, kind (spelled out) and body, each length-prefixed. The send
writes, in one atomic step (`AddOnce`: a script, which Redis runs alone), the
token's record at `bus2:sent:<from>:<token>` (`SET ... PX <cleanup>`) with the
entries on every stream and the receipt marks, unless the record is already
there, when it writes nothing and answers the record. Then:

- **the same arguments within the token's life**: the answer is the original
  message, its `id` and `at`, so `SEND OK` prints the first send's line again,
  byte for byte. One logical message per recipient, one owed receipt per
  friend: a retry after the recipient's session gave its receipt marks
  nothing owed again.
- **other arguments under the same token**: refused, naming the message that
  went (`the token "<t>" already sent <id> at <at> with other arguments`),
  writing nothing.
- **past the token's life, before its cleanup**: refused the same way
  (`past its life of <d>: the message went, and is not sent again`), never
  sent again.
- **after the cleanup**: the store has dropped the record, and the token is
  new: a send under it is a new message.

The settings: the life, `Bus.TokenLife` (`--token-life`, default
`DefaultTokenLife`, 24 h), and the cleanup, `Bus.TokenCleanup`
(`--token-cleanup`, default `DefaultTokenCleanup`, 7 days, never before the
life ends: a record is kept as long as a retry under it is honoured). The
cleanup is the key's expiry; nothing sweeps.

The record lives in the store, never in the process: a sender that restarts
retries and gets the original. A token's record is its sender's: another
sender's same word is another key. The push gate (below) refuses a message
to a deaf name, but a retry whose record is there writes nothing and answers
the original whoever is deaf now (one `GET` more, only when the gate refuses).
A send without a token is the send as before: every call a new message, and a
lost response retried is a second one. A write that failed before it
committed left no record, and its retry is the first send.

The machine is `tla/BusSendOnce.tla`: a sender that retries after lost
answers, a store that writes record and message in one step, the life and
the cleanup; its invariants say one message per token while the record
lives, a retry's answer is the original, and a changed argument is never
written. Its TLC instances (the passing one, and reversed witnesses for a
check apart from the write, a retry that makes a new id, a store that drops
the record inside the life, and an answer without the fingerprint) were
measured on a bench and land with their rows in the TLC catalog in a change
of their own; until then the module is the spec and is not run by the gate.

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

### bus-requires-inbox-push-proof: a name is on the bus only while something proven can hear it

The finding of 2026-10-05: "nova-bus is useless if the friend using it is deaf and is
not listening to messages sent back." A note sat forty minutes unread while
neither the sender nor the coordinator had a push into its session, and the
bus took every message. So the push is mandatory and enforced: `nova-bus
send --as <me>` and `recv --as <me>` refuse until `<me>` has a proven inbox
push younger than ten minutes (`bus.PushFresh`), and `send --to <x>` (and
`--cc`) refuses a recipient without one, in one line each, all at once,
writing nothing:

```
deaf: <x> has no proven push since <age|never>: <why>; the remedy: <x> runs its friend daemon with a
deliver adapter for its harness (nova-friend install --as <x> --harness <h> --dir <d>) and its session
answers the daemon's SESSION CHECK, which records the proof; nova-bus names shows every name's push
```

The proof is the friend daemon's SESSION CHECK round trip (SPEC-FRIEND.md,
presence): a check carrying a fresh nonce goes into the session through the
harness's deliver adapter, and the session's own pong carrying that nonce
comes back on the bus. The daemon writes the proof on `bus2:push` from the
presence it saves (`friend.PushProver`, set as the SessionCheck's `Save`):

- **up** when the session answered, with the harness and the nonce it rests
  on; renewed every minute (`friend.PushRenewEvery`) while the presence stays
  up, so a live daemon's proof never reads stale;
- **down** at once when the presence goes down (a check unanswered within
  `SessionBound`, or a daemon that has not yet been answered), with the reason;
- **down** always for a passive harness (no deliver command: the check goes on
  the stream and nothing pushes into the session), whatever its pongs say.

Its `at` is the store's time (`TIME`), and freshness is read against the
store's time too. `names` reads each name's state: `proven` (up, under ten
minutes), `stale` (its daemon stopped renewing: a dead daemon reads deaf
within ten minutes), `down`, `none` (no daemon ever wrote one). The gate is
`bus.Hearing`, the Store nova-bus opens: a message's write (`AddAll` with
streams) is refused unless its sender and every recipient are heard at its
`at`, after `Send` has named the message's own problems; a recv's group
(`EnsureGroup`) is refused unless the recipient is heard; one `HGETALL` each.
The friend daemon's own sends (its SESSION CHECK on a passive stream, its
`daemon-pong`, its pong verb) go through the bare store: the proof is theirs
to make. `peek`, `ack` and `log` are not gated, so a deaf name can still look
and clean up.

What the proof does not say, plainly: between two checks (ten minutes quiet,
then the five-minute bound) a session that stopped hearing still reads up, so
deafness is seen within `SessionQuiet + SessionBound` of the last answer, and
within ten minutes of a daemon that died. A name with no friend daemon (a
machine row, a coordinator seat run without one) has no way to a proof and is
refused until it runs one. The ACL cannot stop a friend writing another's
field of `bus2:push`, as it cannot stop her reading another's stream; the tool
is the boundary (the ACL per friend, below).

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
| send | `SMEMBERS`, `TIME`, `HGETALL`, `MULTI`, `XADD`, `HSET`, `HDEL`, `EXEC`; with a token `EVALSHA` (and `EVAL` the first time), the script's `GET`, `SET`, `XADD`, `HSET`, `HDEL`, and `GET` when the push gate refuses a retry | `friends`, `machines` (read); `bus2:push` (read); `bus2:to:<every recipient>` and `bus2:log` (XADD: read-write by its key flag); `bus2:owed:<every friend recipient>`, and the sender's own when it answers (re); `bus2:sent:<f>:*` with a token |
| recv | `SMEMBERS`, `TIME`, `HGETALL`, `XGROUP CREATE`, `XAUTOCLAIM`, `XREADGROUP`, `XACK` | `friends`, `machines`; `bus2:push` (read); `bus2:to:<f>` |
| ack | `XINFO GROUPS`, `XPENDING`, `XRANGE`, `XACK`, `HDEL` | `bus2:to:<f>`, `bus2:owed:<f>` |
| peek | `XINFO GROUPS`, `XPENDING`, `XRANGE` | `bus2:to:<f>` |
| log | `XRANGE` | `bus2:log` |
| names | `SMEMBERS`, `TIME`, `HGETALL` | `friends`, `machines`, `bus2:push` |
| the friend daemon's push proof | `SMEMBERS`, `TIME`, `MULTI`, `HSET`, `EXEC` | `friends`, `machines`; `bus2:push` |

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
ACL SETUSER <f> on >(password) ~bus2:to:* ~bus2:log ~bus2:owed:* ~bus2:push ~bus2:sent:<f>:* ~friends ~machines resetchannels
  +hello +ping +smembers +time +multi +exec +xadd +xgroup|create +xreadgroup
  +xautoclaim +xack +xpending +xinfo|groups +xrange +hset +hdel +hgetall
  +eval +evalsha +get +set
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

## Round trips

send: two (the roster and `TIME` in one pipeline, then the transaction, or
with a token the script; a third, `EVAL`, the first time a connection's server
has not the script, and a `GET` when the push gate refuses a retry). recv:
four (the roster, the group, the claim, the read). ack: five (group, pending,
the entries, `XACK`, the receipt's `HDEL`). peek: up to four. log: one. names: one.
