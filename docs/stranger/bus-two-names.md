# Stranger run: nova-bus between two names

A cold run by a bud (deepseek/deepseek-v4.1-flash, in the DeepSeek Harness) who
knew nova-tools only from `README.md`, the README each tool links, and the
tools' own help. The goal: with `nova-bus`, two names (alice and bob) on a
throwaway Redis inside the container send each other a message, read it, and
acknowledge it, as a person new to nova-tools would. Where the run could not go
on from those pages alone, that is a stumble below, with what was read to get
past it.

## Setup

- Bench: a Linux bench (x86_64, Go 1.26.6), one throwaway container
  (`podman run --rm --timeout 14400 --network none`) from the functional image
  `localhost/nova-functional:latest` (Ubuntu 24.04.5, Redis 8.10.2), with a
  scratch directory mounted as `/work`. The Redis and every `nova-bus` process
  ran inside it; the container was stopped and the scratch directory removed at
  the end.
- Source: the checkout of `sprint/mechanical-2026-10-02` (the container has no
  network, so the release install could not run; see stumble 1), built inside
  the container with `go build -o /work/bin/<tool> ./cmd/<tool>`.
- Versions, as `<tool> version` printed them: `nova-bus devel linux/amd64 go1.26.6`,
  `nova-redis devel linux/amd64 go1.26.6`; `redis-server` was
  `Redis server v=8.10.2`.
- Read: `README.md`; `cmd/nova-bus/README.md` and `cmd/nova-redis/README.md`
  (the README links them); `nova-bus help`, `nova-bus wait -h` and
  `nova-redis help`, the three help pages the transcript records. Beyond that,
  only to get past stumbles 2 and 3: the Go source of
  `internal/bus/redis.go`, `internal/bus/names.go`, `internal/bus/pushproof.go`
  and `cmd/nova-bus/firstrun_test.go`.
- Store: one throwaway `redis-server` on a unix socket inside the container,
  started by the recipe in `nova-redis help`, with no users (`login=none`); the
  two names were put in the `friends` set and their push proofs in the
  `bus2:push` hash with `redis-cli`, as the transcript shows.

## Transcript

Every command is run inside the container; `...` marks a trim. The store's
messages, ids and `at=` values are the run's own.

```text
$ go install github.com/mas-bandwidth/nova-tools/cmd/nova-bus@latest
go: github.com/mas-bandwidth/nova-tools/cmd/nova-bus@latest: module lookup disabled by GOPROXY=off
exit=1

$ go build -o /work/bin/nova-bus ./cmd/nova-bus
exit=0

$ go build -o /work/bin/nova-redis ./cmd/nova-redis
exit=0

$ nova-bus version
nova-bus devel linux/amd64 go1.26.6
exit=0

$ nova-redis version
nova-redis devel linux/amd64 go1.26.6
exit=0

$ nova-bus wait --as bob --timeout 1s
WAIT REFUSED: --redis is required: NOVA_BUS_REDIS is unset, and with no NOVA_SPRINT_REDIS the fleet's bus row (nova-config fleet set --bus <host:port>, then apply) cannot be read either; refusing to guess; run: nova-bus help
exit=2

$ nova-bus help
nova-bus: messages between AIs over Redis streams: sent once, delivered until acked

how it works: the loop: send --as <me> --to <friend> --subject <s> --body <text> sends;
recv --as <me> --forever --exec '<deliver-into-session>' takes each message in, acked on exit 0;
ack --as <me> --id <id> acks by hand; send and recv refuse a deaf name (no push proven in 10m).
one stream per recipient (bus2:to:<name>) under a consumer group, one log (bus2:log); all or none.
first run: a Redis naming ada and bob at --redis (else NOVA_BUS_REDIS); loopback or tailnet only.

usage:
  nova-bus wait [--as <me>] [--after <id>] [--timeout <duration>] [--skip-subject <prefix,...>] [--wake-file <path>] [--redis <addr>]
  nova-bus send [--as <me>] --to <a,b> [--cc <c>] --subject <s> (--body <text> | --stdin) [--re <id>] [--kind <k>] [--token <t>] [--timeout <duration>] [--redis <addr>] [--dry-run]
  nova-bus peek [--as <me>] [--kind <k>[,<k>]] [--timeout <duration>] [--redis <addr>]
  nova-bus recv [--as <me>] [--kind <k>[,<k>]] [--max <n> | --all] [--ack] [--exec <command>] [--forever --exec <command>] [--timeout <duration>] [--redis <addr>] [--dry-run]
  nova-bus ack [--as <me>] --id <id,...> [--timeout <duration>] [--redis <addr>] [--dry-run]
  nova-bus receipts [--as <me>] [--id <id,...>] [--max <n>] [--timeout <duration>] [--redis <addr>]
  nova-bus overdue [--older <duration>] [--max <n>] [--timeout <duration>] [--redis <addr>]
  nova-bus log [--bodies] [--max <n>] [--timeout <duration>] [--redis <addr>]
  nova-bus names [--timeout <duration>] [--redis <addr>]
  nova-bus version
  nova-bus help [<verb>]

Every verb takes --json: the same result as one JSON object on stdout. A verb that lists takes --max <n> (default 20, 0 lists all) and says MORE for the rest. `<verb> -h` lists a verb's flags.

exit codes: 0 done, 1 the verb ran and said no (recv: nothing waiting; recv --exec: the command failed; wait: nothing came), 2 could not run (a flag, an input, a store that did not answer).

example:
  nova-bus wait --as bob --timeout 1s
  nova-bus send --as ada --to bob --subject hello --body "are you there?"
  nova-bus peek --as bob
  nova-bus recv --as bob --exec true
  nova-bus ack --as bob --id 01ARZ3NDEKTSV4RRFFQ69G5FAV
  nova-bus log --max 5
  nova-bus names
exit=0

$ nova-bus wait -h
usage: nova-bus wait [flags]
from `nova-bus help`:
  nova-bus wait [--as <me>] [--after <id>] [--timeout <duration>] [--skip-subject <prefix,...>] [--wake-file <path>] [--redis <addr>]
  nova-bus wait --as bob --timeout 1s
Prints WAIT ARMED after=<id> first: the cursor the wait starts past, --after <id> when given (a stream
entry id, <ms>-<seq>), else the stream's last id read once at start, 0-0 when the stream is empty.
Re-arm the next run with the id WAIT OK or WAIT NONE printed, and nothing between two runs is missed.
The wait takes nothing: it reads your stream past the cursor with XREAD, never the consumer group,
so a later recv still delivers and acks what it saw. It ends on the first entries past the cursor
that are not from you and whose subject starts with none of --skip-subject's prefixes (matched
without case; default PING,PONG): one WAIT MESSAGE id=<id> from=<name> subject=<s> bytes=<n> line
each, at most 5, then WAIT OK after=<last id seen> at exit 0. Skipped entries move the cursor and
are not printed. --wake-file <path> also ends the wait when a line is appended to the file after the
start (a harness's deliver adapter appends one per message): WAIT WAKE file=<path> line=<first line>
at exit 0. Past --timeout <duration> (a Go duration; 0, the default, is for ever) it is WAIT NONE
after=<cursor> waited=<duration> on standard error at exit 1. --json prints one object when the
wait ends: {"status":"ok","word":"OK|NONE|WAKE","after":<id>,"messages":[{"id":<id>,"from":<name>,
"subject":<s>,"bytes":<n>}],"wake":{"file":<path>,"line":<text>}} (messages is empty and wake left
out when they hold nothing; the ARMED line is the text form's). Exit 2 when a flag is wrong, the
name is not on the roster, or the store does not answer.
example: nova-bus wait --as bob --timeout 1s
flags:
  --after <string>  the stream entry id <ms>-<seq> to wait past; default: the stream's last id read once at start, as WAIT ARMED prints it
  --as <string>  your name, the recipient: the login user when there is one (then it may be left out)
  --json  print the result as one JSON object instead of lines
  --redis <string>  the Redis address, host:port (default: NOVA_BUS_REDIS, else the fleet row's bus)
  --skip-subject <string>  subjects starting with one of these prefixes, comma-separated, are skipped; matched without case
  --timeout <duration>  how long to wait before WAIT NONE, a Go duration (1s, 2m); 0 is for ever
  --wake-file <string>  a file whose lines, appended after the start, also end the wait (one line per message)
exit codes: 0 the wait ended: WAIT OK, entries that counted, or WAIT WAKE, a line on the wake file; 1 WAIT NONE, the timeout ran out; 2 could not run (a flag, an input, a store that did not answer).
effect: inspection: reads, writes nothing
exit=0

$ nova-redis help
nova-redis: run a local Redis store, and keep short-lived named values in it

how it works: serve runs redis-server on loopback or tailnet addresses only, with its data in --dir.
spill writes a value under <owner>:<name> with a required expiry; recall reads it back.
fn load and fn check install and verify the functions nova-table and nova-sprint call.
The password is read from the variable NOVA_REDIS_PASSWORD_ENV names, else NOVA_REDIS_PASSWORD.
first run: --dry-run needs no store; the throwaway recipe below starts a store on a socket.

usage:
  nova-redis serve --bind <addr>[,<addr>...] --port <port> --dir <store-dir> [--users <name>[,<name>...]] [--secrets <dir> --as <seat> --key <file> --sops <path> --secret <NAME>] [--dry-run]
  nova-redis spill --redis <host:port> [--user <name>] [--password-env <NAME>] --owner <owner> --name <name> --ttl <duration> --value <text> [--dry-run]
  nova-redis recall --redis <host:port> [--user <name>] [--password-env <NAME>] --owner <owner> --name <name>
  nova-redis fn load --redis <host:port> [--user <name>] [--password-env <NAME>]
  nova-redis fn check --redis <host:port> [--user <name>] [--password-env <NAME>]
  nova-redis acl render
  nova-redis acl check --redis <host:port> [--user <name>] [--password-env <NAME>]
  nova-redis acl apply --redis <host:port> [--user <name>] [--password-env <NAME>] [--password-env-for <user>=<VARIABLE>]... [--dry-run]
  nova-redis install store --secrets <dir> --as <seat> --key <file> --sops <path> --secret <NAME> [--bind <addr>[,<addr>...]] [--port <port>] [--dir <store-dir>] [--units <dir>] [--log <file>] [--dry-run]
  nova-redis install bus --secrets <dir> --as <seat> --key <file> --sops <path> --secret <NAME> [--bind <addr>[,<addr>...]] [--port <port>] [--dir <store-dir>] [--units <dir>] [--log <file>] [--dry-run]
  nova-redis uninstall store [--units <dir>] [--dry-run]
  nova-redis uninstall bus [--units <dir>] [--dry-run]
  nova-redis version
  nova-redis help [<verb>]

a throwaway store, by hand: (stop it: redis-cli -s "$d/redis.sock" shutdown nosave)
  d=$(mktemp -d)
  redis-server --port 0 --unixsocket "$d/redis.sock" --save '' --appendonly no --daemonize yes
  for _ in $(seq 50); do redis-cli -s "$d/redis.sock" ping >/dev/null 2>&1 && break; sleep 0.1; done
then run spill or recall with --redis "$d/redis.sock" (or a store you may write to).

Every verb but serve, fn load, fn check, acl render, acl check, acl apply, install store, install bus, uninstall store, uninstall bus takes --json: the same result as one JSON object on stdout. A verb that lists takes --max <n> (default 20, 0 lists all) and says MORE for the rest. `<verb> -h` lists a verb's flags.

exit codes: 0 done (spill written, recall found, fn load done, fn check finds the library loaded, serve stopped); 1 ran and said NO (a recall of a missing, expired or unbounded key, fn check STALE or MISSING, a spill whose reply was lost, a refusal by the store, a serve that could not start); 2 could not run (a usage error, a flag refused before dialling, a store that did not answer or a login it refused).

example:
  nova-redis version
  nova-redis spill --dry-run --addr 127.0.0.1:6379 --owner ada --name note --ttl 10m --value hi
  nova-redis spill --addr 127.0.0.1:6379 --owner ada --name note --ttl 10m --value hi
  nova-redis recall --addr 127.0.0.1:6379 --owner ada --name note
exit=0

$ d=/work/run/redis2; mkdir -p "$d"
$ redis-server --port 0 --unixsocket "$d/redis.sock" --save '' --appendonly no --daemonize yes
... a memory-overcommit warning, then:
$ redis-cli -s "$d/redis.sock" ping
PONG

$ export NOVA_BUS_REDIS="$d/redis.sock"

$ nova-bus names
NAMES OK count=0 proven=0
exit=0

$ nova-bus send --as alice --to bob --subject hello --body "are you there?"
SEND REFUSED: alice is no known name; the names are nova-config's friend and machine rows (nova-bus names lists them); add one with nova-config friend add alice --slots 1 --tiers flash --as <you>, then nova-config apply; run: nova-bus help
SEND REFUSED: bob is no known name; the names are nova-config's friend and machine rows (nova-bus names lists them); add one with nova-config friend add bob --slots 1 --tiers flash --as <you>, then nova-config apply; run: nova-bus help
exit=2

$ redis-cli -s "$d/redis.sock" SADD friends alice bob
2

$ nova-bus names
NAMES OK count=2 proven=0
NAMES NAME name=alice push=none age=never harness=-
NAMES NAME name=bob push=none age=never harness=-
exit=0

$ nova-bus send --as alice --to bob --subject hello --body "are you there?"
SEND REFUSED: deaf: alice has no proven push since never: no daemon has recorded one; the remedy: alice runs its friend daemon with a deliver adapter for its harness (nova-friend install --as alice --harness <h> --dir <d>) and its session answers the daemon's SESSION CHECK, which records the proof; nova-bus names shows every name's push; run: nova-bus help
SEND REFUSED: deaf: bob has no proven push since never: no daemon has recorded one; the remedy: bob runs its friend daemon with a deliver adapter for its harness (nova-friend install --as bob --harness <h> --dir <d>) and its session answers the daemon's SESSION CHECK, which records the proof; nova-bus names shows every name's push; run: nova-bus help
exit=2

$ now=$(date -u +%Y-%m-%dT%H:%M:%SZ)
$ proof='{"harness":"fake","nonce":"cold-run","proven":"'$now'","up":true,"at":"'$now'"}'
$ redis-cli -s "$d/redis.sock" HSET bus2:push alice "$proof"
1
$ redis-cli -s "$d/redis.sock" HSET bus2:push bob "$proof"
1

$ nova-bus names
NAMES OK count=2 proven=2
NAMES NAME name=alice push=proven age=0s harness=fake
NAMES NAME name=bob push=proven age=0s harness=fake
exit=0

$ nova-bus send --as alice --to bob --subject hello --body "are you there?"
SEND OK id=01M4C9DNANQNQVVGGA8QYWG5N8 to=bob cc=- at=2026-10-07T22:58:20Z bytes=14 sha256=cf97adc337983a14daab1089bf14c6ab50e658f0136517e0048407e786b6e745 login=none
exit=0

$ nova-bus peek --as bob
PEEK OK pending=0 new=1
PEEK MESSAGE state=new id=01M4C9DNANQNQVVGGA8QYWG5N8 from=alice at=2026-10-07T22:58:20Z subject="hello"
exit=0

$ nova-bus recv --as bob --max 1
RECV OK id=01M4C9DNANQNQVVGGA8QYWG5N8 from=alice to=bob cc=- re=- at=2026-10-07T22:58:20Z login=none subject="hello"

are you there?
exit=0

$ nova-bus ack --as bob --id 01M4C9DNANQNQVVGGA8QYWG5N8
ACK OK acked=1 asked=1 login=none
ACK ID id=01M4C9DNANQNQVVGGA8QYWG5N8 acked=true
exit=0

$ nova-bus log --max 5
LOG OK total=1
LOG MESSAGE id=01M4C9DNANQNQVVGGA8QYWG5N8 from=alice to=bob cc=- re=- at=2026-10-07T22:58:20Z subject="hello"
exit=0

$ nova-bus send --as bob --to alice --subject got-it --body "got it, thanks"
SEND OK id=01M4C9DNCG9WJYNX6QCHCEV9Q3 to=alice cc=- at=2026-10-07T22:58:20Z bytes=14 sha256=3f49bcd863d57ac7426db3cedcbc7220ded908009ac42fbbcad3eda06990187d login=none
exit=0

$ nova-bus recv --as alice --max 1
RECV OK id=01M4C9DNCG9WJYNX6QCHCEV9Q3 from=bob to=alice cc=- re=- at=2026-10-07T22:58:20Z login=none subject="got-it"

got it, thanks
exit=0

$ nova-bus ack --as alice --id 01M4C9DNCG9WJYNX6QCHCEV9Q3
ACK OK acked=1 asked=1 login=none
ACK ID id=01M4C9DNCG9WJYNX6QCHCEV9Q3 acked=true
exit=0

$ nova-bus log --max 5
LOG OK total=2
LOG MESSAGE id=01M4C9DNANQNQVVGGA8QYWG5N8 from=alice to=bob cc=- re=- at=2026-10-07T22:58:20Z subject="hello"
LOG MESSAGE id=01M4C9DNCG9WJYNX6QCHCEV9Q3 from=bob to=alice cc=- re=- at=2026-10-07T22:58:20Z subject="got-it"
exit=0

$ nova-bus names
NAMES OK count=2 proven=2
NAMES NAME name=alice push=proven age=0s harness=fake
NAMES NAME name=bob push=proven age=0s harness=fake
exit=0
```

## Stumbles

Each stumble: what was read, what was expected, what happened, and a proposed
card.

### 1. The install line needs the network and no page builds from a checkout

- Read: `README.md` "Try one on a small example" (`go install .../cmd/nova-memory@v1.0.0`,
  "or build just that tool with Go 1.26.6 or newer") and the Install block of
  `cmd/nova-bus/README.md` (`go install .../cmd/nova-bus@latest`).
- Expected: a way to get `nova-bus` in a container with no network.
- Happened: `go install ...@latest` printed `module lookup disabled by GOPROXY=off`.
  The README says to build "with Go 1.26.6 or newer", but the line under it is
  another release install (`@v1.0.0`); neither page shows the checkout build a
  reader with the tree in hand needs. I guessed
  `go build -o /work/bin/nova-bus ./cmd/nova-bus`.

card: bus-install-says-how-to-build-a-checkout-offline
paths: README.md,cmd/nova-bus/README.md
task: show the offline checkout build line (`go build -o <dir>/nova-bus ./cmd/nova-bus`) beside the release install, and say which release carries which tool.

### 2. The first run's throwaway store has no setup, and the names come from PostgreSQL

- Read: the nova-bus row of `README.md` ("Use a separate running Redis instance
  whose nova-config rows name the sender and the recipient"),
  `cmd/nova-bus/README.md` "First run" (the store is "a throwaway redis-server
  whose `friends` set names ada and bob (what `nova-config apply` writes for two
  friend rows) ... Run by `cmd/nova-bus/firstrun_test.go`"), `nova-bus help`
  ("first run: a Redis naming ada and bob"), and the send refusal.
- Expected: a command that puts two names on the throwaway store the README
  sends a reader to.
- Happened: `nova-bus names` said `count=0`; `send` refused both names with
  `add one with nova-config friend add <name> --slots 1 --tiers flash --as <you>,
  then nova-config apply` — and `nova-config` stores its rows in PostgreSQL, a
  database for a two-name trial. Nothing in nova-bus's README or help starts the
  Redis either; the throwaway recipe is only in `nova-redis help`. To get past it
  I read `internal/bus/redis.go` and `internal/bus/names.go` and set the key by
  hand: `redis-cli SADD friends alice bob`.

card: bus-first-run-names-a-two-name-throwaway-in-one-command
paths: cmd/nova-bus/README.md,cmd/nova-bus/main.go,docs/SPEC-BUS.md
task: give the throwaway two-name setup as copy-pasteable commands (start Redis, `SADD friends alice bob`, prove the push), or a nova-bus verb that writes a name and its proof, so the first run needs no PostgreSQL.

### 3. A name is not heard until a push is proven, and the proof is an undocumented JSON blob

- Read: `cmd/nova-bus/README.md` ("each with a proven inbox push on `bus2:push`
  (what each one's friend daemon writes ...)"), `nova-bus help` ("send and recv
  refuse a deaf name (no push proven in 10m)"), and the `SEND REFUSED: deaf: ...`
  line.
- Expected: once the two names exist, a message can be sent.
- Happened: with `friends` set, `nova-bus names` said `count=2 proven=0` and
  `send` refused both names as deaf, with the remedy "runs its friend daemon ...
  and its session answers the daemon's SESSION CHECK". A bus-only two-name trial
  has no friend daemon, and nothing says the proof can be written by hand. To get
  past it I read `internal/bus/pushproof.go` and `cmd/nova-bus/firstrun_test.go`
  and hand-wrote the proof into the `bus2:push` hash:
  `{"harness":"fake","nonce":"cold-run","proven":"<now>","up":true,"at":"<now>"}`.
  The proof's field shape is in no help page.

card: bus-help-names-the-push-proof-and-lets-a-name-be-proven-by-hand
paths: cmd/nova-bus/main.go,docs/SPEC-BUS.md
task: document the `bus2:push` proof shape and add `nova-bus prove --as <name> [--harness <h>]` (with `--dry-run`) that writes it, so a bus-only trial hears two names with no daemon.

### 4. `--redis` takes a unix socket path, but the help says only `<addr>`

- Read: `nova-bus help` and `nova-bus wait -h` (`--redis <addr>`), the main
  README ("The Redis examples assume your throwaway instance listens on
  `127.0.0.1:6379`. Supply its address and login when they differ.") and the
  throwaway recipe in `nova-redis help` (a `--port 0 --unixsocket` store).
- Expected: the recipe's socket has no host:port to give, so the two pages do
  not meet.
- Happened: passing the socket path in `NOVA_BUS_REDIS` worked
  (`NAMES OK count=0`), which no help says. A reader who follows "listens on
  127.0.0.1:6379" starts a different store rather than the recipe they were just
  shown.

card: bus-help-says-redis-takes-a-unix-socket-path
paths: cmd/nova-bus/main.go,cmd/nova-redis/README.md
task: say in `--redis`'s help and the nova-bus first run that a unix socket path is accepted, and give the one throwaway recipe both tools use.

## Verdict

could a stranger do it: no, not from the README and the help alone. It took
about 3 minutes of bench time from the first container command to the last
acknowledgement (22:55 to 22:58 UTC), after the README and the help had been
read; most of it was finding the two keys the run needs. The pieces do work:
with the `friends` set and the `bus2:push` proofs in place, alice sent bob a
message, bob peeked, received and acked it, bob sent alice the reply, and alice
received and acked that, all `login=none` on the throwaway store. What stops a
stranger is the way in: an install line that needs the network, a first run
whose store is a test fixture (`cmd/nova-bus/firstrun_test.go`) named but not
given, two names that come from PostgreSQL, and a push proof that has to be
written by hand from the source. Of the four stumbles, 2 and 3 each stop the run.
