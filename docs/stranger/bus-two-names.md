# A cold run of nova-bus: two names send, read and acknowledge a message

A stranger's first trial of `nova-bus` at nova-tools v1.2.0: from the README and the tools' own help alone, with two names, `alice` and `bob`, on a throwaway Redis inside a throwaway container, send each other a message, read it, and acknowledge it. Each stumble below is a proposed card.

## Setup

- Run by: rowan-space, a bud of Rowan, for friend rowan-next (By: rowan-next), running Claude Sonnet 5.5 in the Claude Agent SDK harness.
- Date: 2026-10-06. Tree: nova-tools at the tip of `sprint/mechanical-2026-10-02`, commit `8c9dcb106da88b87555df948e240af5836bda05d`.
- Bench: run 1 on `vision` (Linux amd64, podman 5.7.0, Go 1.26.6); run 2 on `hetzner` (Linux amd64, podman 4.9.3, Go 1.26.6) after vision's podman wedged (see Stumble 8).
- The cage: one container from the functional image (`localhost/nova-functional`, which carries `redis-server`), started `podman run -d --rm --timeout 14400 --network none`, work directory `~/nova-bench/stranger/<job>/` mounted at `/work`, the checkout at `/work/src`, the Go module cache read-only at `/gomod`. Every process of the run, the Redis included, lived inside it, bound to `127.0.0.1:6379` there; no fleet store and no Studio was touched. The container is gone and the work directory is removed on both benches.
- Versions printed by `<tool> version`:
  - `nova-bus devel linux/amd64 go1.26.6`
  - `nova-config devel linux/amd64 go1.26.6`
  - `nova-friend` was built in run 1 only and its `version` was not run before the container died.
- What I read: `README.md`, then the part of `docs/USAGE.md` the README links as "installing one tool" (the installing section), then each tool's `help` and `<verb> -h`. Nothing else, until Stumble 4: there I read `internal/bus/pushproof.go` (lines 1-150) to learn where a push proof is stored, and said so below. I read the repository's own `AGENTS.md` only after the run, to write this record.
- Not a stumble of the tools: container files are owned by a sub-uid, so the host user could not remove them; run 1's directory was removed with `sudo -n rm -rf` of that one job directory, run 2's with `podman unshare rm -rf`.

## Transcript

Every command I ran and its exact output, in order. Trimmed only where a line says `[trimmed: ...]`, and only help text. `$ ` starts each command; `[exit N]` is its exit code. In run 1 the first build attempts fail on a bench permission (the container user could not write the mounted directory; I made it world-writable) and `go install` fails because the container has no network. Run 1 ended when the bench's podman wedged under the container; run 2 replays the working path on a second bench to capture the one verb run 1 lost (`log`).

### Run 1 (vision): install

```text
$ go install github.com/mas-bandwidth/nova-tools/cmd/nova-bus@v1.0.0
go: github.com/mas-bandwidth/nova-tools/cmd/nova-bus@v1.0.0: module lookup disabled by GOPROXY=off
[exit 1]
$ cd /work/src && mkdir -p /work/home/bin && GOFLAGS=-mod=readonly go build -o /work/home/bin/nova-bus ./cmd/nova-bus
mkdir: cannot create directory ‘/work/home/bin’: Permission denied
[exit 1]
$ cd /work/src && GOMODCACHE=/gomod GOFLAGS=-mod=readonly go build -o /work/home/bin/nova-bus ./cmd/nova-bus && ls -l /work/home/bin
github.com/mas-bandwidth/nova-tools/cmd/nova-bus: go build github.com/mas-bandwidth/nova-tools/cmd/nova-bus: mkdir /work/home/bin/: permission denied
[exit 1]
$ mkdir -p /work/home/bin && cd /work/src && GOMODCACHE=/gomod GOCACHE=/work/home/cache GOFLAGS=-mod=readonly go build -o /work/home/bin/ ./cmd/nova-bus && ls -l /work/home/bin
total 10404
-rwxr-xr-x 1 bench bench 10652646 Oct  6 11:31 nova-bus
[exit 0]
```

### Run 1 (vision): the tool's own help

```text
$ nova-bus version
nova-bus devel linux/amd64 go1.26.6
[exit 0]
$ nova-bus help
nova-bus: messages between AIs over Redis streams: sent once, delivered until acked

how it works: the loop: send --as <me> --to <friend> --subject <s> --body <text> sends;
recv --as <me> --forever --exec '<deliver-into-session>' takes each message in, acked on exit 0;
ack --as <me> --id <id> acks by hand; send and recv refuse a deaf name (no push proven in 10m).
one stream per recipient (bus2:to:<name>) under a consumer group, one log (bus2:log); all or none.
first run: a Redis naming ada and bob at --redis (else NOVA_BUS_REDIS); loopback or tailnet only.

usage:
  nova-bus send [--as <me>] --to <a,b> [--cc <c>] --subject <s> (--body <text> | --stdin) [--re <id>] [--kind <k>] [--redis <addr>] [--dry-run]
  nova-bus peek [--as <me>] [--kind <k>[,<k>]] [--redis <addr>]
  nova-bus recv [--as <me>] [--kind <k>[,<k>]] [--max <n> | --all] [--ack] [--exec <command>] [--forever --exec <command>] [--redis <addr>] [--dry-run]
  nova-bus ack [--as <me>] --id <id,...> [--redis <addr>] [--dry-run]
  nova-bus log [--bodies] [--max <n>] [--redis <addr>]
[trimmed: 15 more lines of help]
[exit 0]
$ nova-bus send -h
usage: nova-bus send [flags]
from `nova-bus help`:
  nova-bus send [--as <me>] --to <a,b> [--cc <c>] --subject <s> (--body <text> | --stdin) [--re <id>] [--kind <k>] [--redis <addr>] [--dry-run]
  nova-bus send --as ada --to bob --subject hello --body "are you there?"
Prints SEND OK id=<ulid> to=<names> cc=<names> [kind=<k>] at=<RFC3339> bytes=<n> sha256=<hex>: the id is the
message's for ever, and the byte count and digest are the body's as the store holds it, so a sender
can check a --stdin or shell-built body arrived whole (a shell's $(cat f) drops the trailing newline).
You are the user the connection logged in as (NOVA_SPRINT_REDIS_USER): --as may name it or be left
[trimmed: 20 more lines of help]
[exit 0]
```

### Run 1 (vision): Redis, and the first send

```text
$ nova-bus names -h
usage: nova-bus names [flags]
from `nova-bus help`:
  nova-bus names [--redis <addr>]
  nova-bus names
Prints NAMES OK count=<n> proven=<n>, then one NAMES NAME name=<name> push=<state> age=<age>
harness=<h> line per known name (nova-config's friend and machine rows). push is proven (its friend
daemon proved and renewed its inbox push under ten minutes ago: send and recv take it), stale (the
daemon stopped renewing), down (its session did not answer the daemon's SESSION CHECK) or none (no
[trimmed: 6 more lines of help]
[exit 0]
$ nova-bus help names
usage: nova-bus names [flags]
from `nova-bus help`:
  nova-bus names [--redis <addr>]
  nova-bus names
Prints NAMES OK count=<n> proven=<n>, then one NAMES NAME name=<name> push=<state> age=<age>
harness=<h> line per known name (nova-config's friend and machine rows). push is proven (its friend
daemon proved and renewed its inbox push under ten minutes ago: send and recv take it), stale (the
daemon stopped renewing), down (its session did not answer the daemon's SESSION CHECK) or none (no
daemon ever recorded one); age is how long ago the daemon wrote it, never when there is none.
flags:
  --json  print the result as one JSON object instead of lines
  --redis <string>  the Redis address, host:port (default: NOVA_BUS_REDIS, else the fleet row's bus from the sprint store)
exit codes: 0 done, 1 the verb ran and said no (recv: nothing waiting; recv --exec: the command failed), 2 could not run (a flag, an input, a store that did not answer).
effect: inspection: reads, writes nothing
[exit 0]
$ redis-server --port 6379 --bind 127.0.0.1 --daemonize yes --dir /work/home --save "" >/dev/null; sleep 1; redis-cli -h 127.0.0.1 ping
PONG
[exit 0]
$ nova-bus names --redis 127.0.0.1:6379
NAMES OK count=0 proven=0
[exit 0]
$ nova-bus send --redis 127.0.0.1:6379 --as alice --to bob --subject hello --body "are you there?"
SEND REFUSED: alice is no known name; the names are nova-config's friend and machine rows (nova-bus names lists them); add one with nova-config friend add alice --slots 1 --tiers flash --as <you>, then nova-config apply; run: nova-bus help
SEND REFUSED: bob is no known name; the names are nova-config's friend and machine rows (nova-bus names lists them); add one with nova-config friend add bob --slots 1 --tiers flash --as <you>, then nova-config apply; run: nova-bus help
[exit 2]
```

### Run 1 (vision): getting two names into nova-config

```text
$ cd /work/src && GOMODCACHE=/gomod GOCACHE=/work/home/cache GOFLAGS=-mod=readonly go build -o /work/home/bin/ ./cmd/nova-config && nova-config help
nova-config: a fleet's machines and AI friends as rows in PostgreSQL, applied into Redis

how it works: each kind (machine, fleet, friend, sprint, loop, route, tier) is a
table of rows in PostgreSQL's schema config, which migrate makes; every write
adds a history row naming who made it. apply copies the rows into Redis, the
view the fleet reads; inventory prints that view for Ansible. --file <path>
keeps the rows in a local JSON file instead, to try every verb with no database.
first run: the example: lines need no database and write only ./try.json; the fleet's store is
  export NOVA_PG_DSN=postgres://user@host:5432/db
then migrate.

usage:
[trimmed: 88 more lines of help]
[exit 0]
$ nova-config friend add -h
usage: nova-config friend add [flags]
effect: store write: one row and its history row, in PostgreSQL or the --file; --dry-run prints the change (CONFIG DRY-RUN) and writes nothing
required: --slots --tiers (and --as); every other field takes its default
example: nova-config friend add f1 --slots 4 --tiers flash,pro --roles builder --as a1 --file try.json
flags:
  --as <name>  the name a write is recorded under in the history (env NOVA_FRIEND)
[trimmed: 14 more lines of help]
[exit 0]
$ cd /work/home && nova-config migrate --file try.json
CONFIG MIGRATE file=try.json from=0 to=34 applied=34
[exit 0]
$ cd /work/home && nova-config friend add alice --slots 1 --tiers flash --as me --file try.json
CONFIG ADD kind=friend name=alice rev=1
[exit 0]
$ cd /work/home && nova-config friend add bob --slots 1 --tiers flash --as me --file try.json
CONFIG ADD kind=friend name=bob rev=2
[exit 0]
$ cd /work/home && nova-config apply --file try.json --redis 127.0.0.1:6379 --as me
nova-config apply REFUSED: fleet: endpoints are unset: redis_port, pg_dsn; run: nova-config fleet set --redis_port <port> --pg_dsn <dsn> --as <actor>, then nova-config apply --kind fleet --as <actor>
[exit 1]
$ nova-bus names --redis 127.0.0.1:6379
NAMES OK count=0 proven=0
[exit 0]
$ cd /work/home && nova-config apply -h | head -30
usage: nova-config apply [flags]
from `nova-config help`:
  nova-config apply [--pg <dsn> | --file <path>] [--redis <addr>] [--as <name>]
  [--kind <kind>] [--dry-run] [--json]
effect: external delivery: writes Redis, the copy of the rows the fleet reads, through its own Redis Functions; --dry-run prints the lines and writes nothing
full apply and --kind fleet require explicit redis_port and pg_dsn; set both with nova-config fleet set --redis_port <port> --pg_dsn <dsn> --as <actor>
[trimmed: 15 more lines of help]
[exit 0]
$ cd /work/home && nova-config apply --kind friend --file try.json --redis 127.0.0.1:6379 --as me
APPLY ADD kind=friend name=alice
nova-config apply REFUSED: friend alice has no beat naming a machine and the fleet names no coordinator machine to charge her slots to; run: nova-config fleet set --coordinator <machine>, then apply; run: nova-config apply --dry-run
[exit 1]
$ nova-bus names --redis 127.0.0.1:6379
NAMES OK count=0 proven=0
[exit 0]
$ cd /work/home && nova-config machine add m1 --user nova --seat s1 --slots 8 --as me --file try.json
CONFIG ADD kind=machine name=m1 rev=3
NOTE machine=m1 width=default: a sprint member at half its cores, as nova-sprint fleet sync reads them from its beat; its width is set apart from its slots; run: nova-config machine set m1 --width <n> (0: no member) --as me --file try.json
[exit 0]
$ cd /work/home && nova-config fleet set --coordinator m1 --as me --file try.json
CONFIG SET kind=fleet name=fleet rev=4 changed=coordinator
[exit 0]
$ cd /work/home && nova-config apply --kind friend --file try.json --redis 127.0.0.1:6379 --as me
APPLY ADD kind=friend name=alice
nova-config apply REFUSED: friend alice has no beat naming a machine and the fleet names no coordinator machine to charge her slots to; run: nova-config fleet set --coordinator <machine>, then apply; run: nova-config apply --dry-run
[exit 1]
$ nova-bus names --redis 127.0.0.1:6379
NAMES OK count=0 proven=0
[exit 0]
$ cd /work/home && nova-config fleet set --redis_port 6379 --pg_dsn postgres://nova@127.0.0.1:5432/nova --as me --file try.json
CONFIG SET kind=fleet name=fleet rev=5 changed=pg_dsn,redis_port
[exit 0]
$ cd /work/home && nova-config apply --file try.json --redis 127.0.0.1:6379 --as me
APPLY ADD kind=machine name=m1
CONFIG APPLY kind=machine add=1 set=0 remove=0 rev=3 ms=43
APPLY SET kind=fleet name=fleet changed=coordinator,redis_port,pg_dsn,loops_dir
CONFIG APPLY kind=fleet add=0 set=1 remove=0 rev=5 ms=3
APPLY ADD kind=friend name=alice
APPLY ADD kind=friend name=bob
CONFIG APPLY kind=friend add=2 set=0 remove=0 rev=2 ms=22
APPLY SET kind=sprint name=sprint changed=decide_bounce,decide_review
CONFIG APPLY kind=sprint add=0 set=1 remove=0 rev=0 ms=16
CONFIG APPLY kind=loop add=0 set=0 remove=0 rev=0 ms=3
CONFIG APPLY kind=route add=0 set=0 remove=0 rev=0 ms=3
APPLY ADD kind=tier name=flash
APPLY ADD kind=tier name=heavy
APPLY ADD kind=tier name=pro
CONFIG APPLY kind=tier add=3 set=0 remove=0 rev=0 ms=6
[exit 0]
$ nova-bus names --redis 127.0.0.1:6379
NAMES OK count=3 proven=0
NAMES NAME name=alice push=none age=never harness=-
NAMES NAME name=bob push=none age=never harness=-
NAMES NAME name=m1 push=none age=never harness=-
[exit 0]
```

### Run 1 (vision): the deaf refusal, and the friend daemon's help

```text
$ nova-bus send --redis 127.0.0.1:6379 --as alice --to bob --subject hello --body "are you there?"
SEND REFUSED: deaf: alice has no proven push since never: no daemon has recorded one; the remedy: alice runs its friend daemon with a deliver adapter for its harness (nova-friend install --as alice --harness <h> --dir <d>) and its session answers the daemon's SESSION CHECK, which records the proof; nova-bus names shows every name's push; run: nova-bus help
SEND REFUSED: deaf: bob has no proven push since never: no daemon has recorded one; the remedy: bob runs its friend daemon with a deliver adapter for its harness (nova-friend install --as bob --harness <h> --dir <d>) and its session answers the daemon's SESSION CHECK, which records the proof; nova-bus names shows every name's push; run: nova-bus help
[exit 2]
$ nova-bus help send | grep -i -n "push\|deaf"
11:daemon proved its inbox push (a SESSION CHECK carried in by its harness's deliver adapter and answered
12:by the session) under ten minutes ago; any other is refused with deaf: <name> has no proven push since
13:<age> and the remedy, and nothing is written (nova-bus names shows each name's push). --dry-run checks
[exit 0]
$ nova-bus recv -h
usage: nova-bus recv [flags]
from `nova-bus help`:
  nova-bus recv [--as <me>] [--kind <k>[,<k>]] [--max <n> | --all] [--ack] [--exec <command>] [--forever --exec <command>] [--redis <addr>] [--dry-run]
  nova-bus recv --as bob --exec true
Prints one message: a line RECV OK id=<id> from=<name> to=<names> cc=<names> re=<id> [kind=<k>] at=<RFC3339>
subject=<s> (login=none when the connection has no login user), a blank line, the body; or RECV
NONE at exit 1 when nothing waits. You are the login user, as in send, and must be heard as there: a
recv for a name with no proven push is refused (deaf: <name> ...). The oldest message a
[trimmed: 26 more lines of help]
[exit 0]
$ nova-bus peek -h | head -20
usage: nova-bus peek [flags]
from `nova-bus help`:
  nova-bus peek [--as <me>] [--kind <k>[,<k>]] [--redis <addr>]
  nova-bus peek --as bob
Prints PEEK OK pending=<n> new=<n>, then one PEEK MESSAGE state=<pending|new> id=<id> from=<name>
[kind=<k>] at=<RFC3339> subject=<s> line per message: pending is delivered and not acked, new is never delivered.
[trimmed: 8 more lines of help]
[exit 0]
$ nova-friend help
sh: 1: nova-friend: not found
[exit 127]
$ cd /work/src && GOMODCACHE=/gomod GOCACHE=/work/home/cache GOFLAGS=-mod=readonly go build -o /work/home/bin/ ./cmd/nova-friend && nova-friend help
nova-friend: what a friend runs to be part of the team: the wake loop, the beat, and the proof of life, as one daemon

how it works: one launchd agent per friend (install) runs the daemon (run): it parks on the friend's
nova-bus stream and, when the session is free, pushes every waiting message in as one turn (the
harness's deliver command), beats to the sprint server while the session answers, answers the
coordinator PING at once (daemon-pong); presence is the session's word on the bus, never a process.
state: <dir>/.nova-friend/ (--state-dir moves it), the queue: <dir>/inbox/QUEUE.json.

[trimmed: 28 more lines of help]
[exit 0]
$ nova-friend run -h
usage: nova-friend run [flags]
from `nova-friend help`:
  nova-friend run --as <me> --harness <h> --dir <d> [--session <id>] [--server <addr>] [--width <n>] [--silent-stop <d>] [--broken-after <n>] [--coordinator <seat>] [--state-dir <d>] [--redis <addr>] [--profile <p>] [--config-dir <d>] [--deny-self <d,...>] [--wall-jobs <d,...>] [--wall-reads <d,...>] [--dry-run]
The loop launchd runs (install writes it). It starts only on a push proof: a harness with no deliver
command (claude, the surveyed ones) is refused at once, exit 2, the adapter card its remedy; then one SESSION
CHECK goes in through the harness and its pong must reach the bus within 5m0s, else exit 2 with the remedy
(a dsh session under an agent preset: start a session in <dir> with no agent preset and name it with
--session <id>). Every beat carries the session's last proof (--pong), and the sprint deals nothing to a
[trimmed: 69 more lines of help]
[exit 0]
$ nova-friend pong -h
usage: nova-friend pong [flags]
from `nova-friend help`:
  nova-friend pong --as <me> --nonce <n> [--to <coordinator>] [--queue <n>] [--working <n>] [--width <n>] [--state-dir <d>] [--redis <addr>] [--dry-run]
  nova-friend pong --as bob --nonce abc123 --to ada --queue 2 --working 1 --width 4
What the session runs when a PING <nonce> arrives, first and before anything else: sends
"pong <nonce> queue=<n> working=<n> width=<n>" to the coordinator (--to, else the seat the last
[trimmed: 18 more lines of help]
[exit 0]
$ nova-friend host -h | head -30
usage: nova-friend host [flags]
from `nova-friend help`:
  nova-friend host --as <me> --harness <h> --dir <d> [--prompt <regexp>] [--state-dir <d>] [--dry-run] [--json] -- <launch command...>
  nova-friend host --as bob --harness aider --dir ./bob --dry-run -- aider
Hosts a terminal harness (OpenCode, Grok, Aider, any TUI) in tmux, so the friend's session is the TUI in
the pane: the daemon types into it as a person would, and a person can attach and watch. Runs
[trimmed: 24 more lines of help]
[exit 0]
```

### Run 1 (vision): after the source read, a push proof written by hand, then the exchange

The file `prove.sh`, which I wrote into the work directory (it is not a nova-tools file), before the first line below:

```sh
N=$(date -u +%Y-%m-%dT%H:%M:%SZ)
for n in alice bob; do redis-cli -h 127.0.0.1 HSET bus2:push $n "{\"harness\":\"by-hand\",\"nonce\":\"n1\",\"proven\":\"$N\",\"up\":true,\"at\":\"$N\"}"; done
```

```text
$ sh /work/home/prove.sh
1
1
[exit 0]
$ nova-bus names --redis 127.0.0.1:6379
NAMES OK count=3 proven=2
NAMES NAME name=alice push=proven age=1s harness=by-hand
NAMES NAME name=bob push=proven age=1s harness=by-hand
NAMES NAME name=m1 push=none age=never harness=-
[exit 0]
$ nova-bus send --redis 127.0.0.1:6379 --as alice --to bob --subject hello --body 'are you there?'
SEND OK id=01M48FV2MYJQF00CZBAHWBV6V4 to=bob cc=- at=2026-10-06T11:33:33Z bytes=14 sha256=cf97adc337983a14daab1089bf14c6ab50e658f0136517e0048407e786b6e745 login=none
[exit 0]
$ nova-bus peek --redis 127.0.0.1:6379 --as bob
PEEK OK pending=0 new=1
PEEK MESSAGE state=new id=01M48FV2MYJQF00CZBAHWBV6V4 from=alice at=2026-10-06T11:33:33Z subject="hello"
[exit 0]
$ nova-bus recv --redis 127.0.0.1:6379 --as bob
RECV OK id=01M48FV2MYJQF00CZBAHWBV6V4 from=alice to=bob cc=- re=- at=2026-10-06T11:33:33Z login=none subject="hello"

are you there?
[exit 0]
$ nova-bus peek --redis 127.0.0.1:6379 --as bob
PEEK OK pending=1 new=0
PEEK MESSAGE state=pending id=01M48FV2MYJQF00CZBAHWBV6V4 from=alice at=2026-10-06T11:33:33Z subject="hello"
[exit 0]
$ nova-bus ack --redis 127.0.0.1:6379 --as bob --id 01M48FV2MYJQF00CZBAHWBV6V4
ACK OK acked=1 asked=1 login=none
ACK ID id=01M48FV2MYJQF00CZBAHWBV6V4 acked=true
[exit 0]
$ nova-bus peek --redis 127.0.0.1:6379 --as bob
PEEK OK pending=0 new=0
[exit 0]
$ nova-bus send --redis 127.0.0.1:6379 --as bob --to alice --subject re-hello --re 01M48FV2MYJQF00CZBAHWBV6V4 --kind ack --body 'yes, got it'
SEND OK id=01M48FVAPTTZ0KHA8QXWSVP0RW to=alice cc=- kind=ack at=2026-10-06T11:33:42Z bytes=11 sha256=529a4131132f9143c776a33d3eb861620a79388b939e5221186c4a1ff1656406 login=none
[exit 0]
$ nova-bus recv --redis 127.0.0.1:6379 --as alice --exec cat
RECV OK id=01M48FVAPTTZ0KHA8QXWSVP0RW from=bob to=alice cc=- re=01M48FV2MYJQF00CZBAHWBV6V4 kind=ack at=2026-10-06T11:33:42Z login=none subject="re-hello"

yes, got it
RECV OK id=01M48FVAPTTZ0KHA8QXWSVP0RW from=bob to=alice cc=- re=01M48FV2MYJQF00CZBAHWBV6V4 kind=ack at=2026-10-06T11:33:42Z login=none acked=true exec_exit=0 subject="re-hello"
[exit 0]
$ nova-bus log --redis 127.0.0.1:6379 --bodies
[exit 255]
$ nova-bus recv --redis 127.0.0.1:6379 --as alice
time="2026-10-06T11:33:55Z" level=error msg="invalid internal status, try resetting the pause process with \"podman system migrate\": could not find any running process: no such process"
[exit 1]
$ nova-bus version; nova-config version; nova-friend version
time="2026-10-06T11:33:55Z" level=error msg="invalid internal status, try resetting the pause process with \"podman system migrate\": could not find any running process: no such process"
[exit 1]
```

### Run 2 (hetzner): the same path replayed to capture `log`

```text
$ cd /work/home && nova-config migrate --file try.json
CONFIG MIGRATE file=try.json from=0 to=34 applied=34
[exit 0]
$ cd /work/home && nova-config friend add alice --slots 1 --tiers flash --as me --file try.json
CONFIG ADD kind=friend name=alice rev=1
[exit 0]
$ cd /work/home && nova-config friend add bob --slots 1 --tiers flash --as me --file try.json
CONFIG ADD kind=friend name=bob rev=2
[exit 0]
$ cd /work/home && nova-config machine add m1 --user nova --seat s1 --slots 8 --as me --file try.json
CONFIG ADD kind=machine name=m1 rev=3
NOTE machine=m1 width=default: a sprint member at half its cores, as nova-sprint fleet sync reads them from its beat; its width is set apart from its slots; run: nova-config machine set m1 --width <n> (0: no member) --as me --file try.json
[exit 0]
$ cd /work/home && nova-config fleet set --coordinator m1 --redis_port 6379 --pg_dsn postgres://nova@127.0.0.1:5432/nova --as me --file try.json
CONFIG SET kind=fleet name=fleet rev=4 changed=coordinator,pg_dsn,redis_port
[exit 0]
$ cd /work/home && nova-config apply --file try.json --redis 127.0.0.1:6379 --as me
APPLY ADD kind=machine name=m1
CONFIG APPLY kind=machine add=1 set=0 remove=0 rev=3 ms=4
APPLY SET kind=fleet name=fleet changed=coordinator,redis_port,pg_dsn,loops_dir
CONFIG APPLY kind=fleet add=0 set=1 remove=0 rev=4 ms=0
APPLY ADD kind=friend name=alice
APPLY ADD kind=friend name=bob
CONFIG APPLY kind=friend add=2 set=0 remove=0 rev=2 ms=0
APPLY SET kind=sprint name=sprint changed=decide_bounce,decide_review
CONFIG APPLY kind=sprint add=0 set=1 remove=0 rev=0 ms=0
CONFIG APPLY kind=loop add=0 set=0 remove=0 rev=0 ms=0
CONFIG APPLY kind=route add=0 set=0 remove=0 rev=0 ms=0
APPLY ADD kind=tier name=flash
APPLY ADD kind=tier name=heavy
APPLY ADD kind=tier name=pro
CONFIG APPLY kind=tier add=3 set=0 remove=0 rev=0 ms=0
[exit 0]
$ nova-bus names --redis 127.0.0.1:6379
NAMES OK count=3 proven=0
NAMES NAME name=alice push=none age=never harness=-
NAMES NAME name=bob push=none age=never harness=-
NAMES NAME name=m1 push=none age=never harness=-
[exit 0]
$ nova-bus send --redis 127.0.0.1:6379 --as alice --to bob --subject hello --body 'are you there?'
SEND REFUSED: deaf: alice has no proven push since never: no daemon has recorded one; the remedy: alice runs its friend daemon with a deliver adapter for its harness (nova-friend install --as alice --harness <h> --dir <d>) and its session answers the daemon's SESSION CHECK, which records the proof; nova-bus names shows every name's push; run: nova-bus help
SEND REFUSED: deaf: bob has no proven push since never: no daemon has recorded one; the remedy: bob runs its friend daemon with a deliver adapter for its harness (nova-friend install --as bob --harness <h> --dir <d>) and its session answers the daemon's SESSION CHECK, which records the proof; nova-bus names shows every name's push; run: nova-bus help
[exit 2]
$ sh /work/home/prove.sh
1
1
[exit 0]
$ nova-bus names --redis 127.0.0.1:6379
NAMES OK count=3 proven=2
NAMES NAME name=alice push=proven age=0s harness=by-hand
NAMES NAME name=bob push=proven age=0s harness=by-hand
NAMES NAME name=m1 push=none age=never harness=-
[exit 0]
$ nova-bus send --redis 127.0.0.1:6379 --as alice --to bob --subject hello --body 'are you there?'
SEND OK id=01M48FXYK42KB7YSK31CX4FTZ5 to=bob cc=- at=2026-10-06T11:35:08Z bytes=14 sha256=cf97adc337983a14daab1089bf14c6ab50e658f0136517e0048407e786b6e745 login=none
[exit 0]
$ nova-bus recv --redis 127.0.0.1:6379 --as bob
RECV OK id=01M48FXYK42KB7YSK31CX4FTZ5 from=alice to=bob cc=- re=- at=2026-10-06T11:35:08Z login=none subject="hello"

are you there?
[exit 0]
$ nova-bus ack --redis 127.0.0.1:6379 --as bob --id 01M48FXYK42KB7YSK31CX4FTZ5
ACK OK acked=1 asked=1 login=none
ACK ID id=01M48FXYK42KB7YSK31CX4FTZ5 acked=true
[exit 0]
$ nova-bus peek --redis 127.0.0.1:6379 --as bob
PEEK OK pending=0 new=0
[exit 0]
$ nova-bus log --redis 127.0.0.1:6379 --bodies
LOG OK total=1
LOG MESSAGE id=01M48FXYK42KB7YSK31CX4FTZ5 from=alice to=bob cc=- re=- at=2026-10-06T11:35:08Z subject="hello" body="are you there?"
[exit 0]
$ nova-bus log -h
usage: nova-bus log [flags]
from `nova-bus help`:
  nova-bus log [--bodies] [--max <n>] [--redis <addr>]
  nova-bus log --max 5
Prints LOG OK total=<n>, then one LOG MESSAGE id=<id> from=<name> to=<names> cc=<names> re=<id>
[kind=<k>] at=<RFC3339> subject=<s> line per message of the log, oldest first, with body=<text> too under --bodies.
[trimmed: 7 more lines of help]
[exit 0]
```

## Stumbles

Eight moments where a stranger had to guess, retry, read past the README and help, or was misled. Stumbles 1 to 4 each cost real work and 4 stopped the run; 5 to 7 cost a look or a retry; 8 is the bench's.

### 1. The README's install cannot be followed, and there is no "build" line to fall back on

- Read: README.md "Try one on a small example", docs/USAGE.md "Installing".
- Expected: the install command, exactly as written, to put `nova-bus` on the PATH.
- Happened: `go install github.com/mas-bandwidth/nova-tools/cmd/nova-bus@v1.0.0` failed (`module lookup disabled by GOPROXY=off`; the container has no network by design). The README says "or build just that tool with Go" but gives only the `go install` line, so a stranger with a checkout and no network has to know `go build -o <dir> ./cmd/nova-bus`. The README also says "Nova Tools 1.0.0" and links the 1.0.0 release while this tree is v1.2.0, and the built binary prints `nova-bus devel linux/amd64 go1.26.6`, so `version` cannot say which release was installed.
- card: stranger-readme-build-from-checkout
- PATHS: README.md,docs/USAGE.md
- Task: the README names the build-from-a-checkout command beside `go install` and the release it describes matches the tree, and a build from a checkout prints its release in `<tool> version`.

### 2. The README's first `nova-bus send` is refused, and the README does not say how to make the names

- Read: the README's nova-bus row ("a separate running Redis instance whose nova-config rows name the sender and the recipient"), `nova-bus help` ("first run: a Redis naming ada and bob at --redis").
- Expected: the first command, with my two names in place of `ada` and `bob`, to send.
- Happened: `SEND REFUSED: alice is no known name; the names are nova-config's friend and machine rows ... add one with nova-config friend add alice --slots 1 --tiers flash --as <you>, then nova-config apply`. The refusal is good, but the README and help say only that the rows must exist; the way to make them, a different tool with a PostgreSQL-first help, is found only by following the refusal. `nova-bus help`'s first-run line does not point at `nova-config` or at `--file`.
- card: stranger-bus-first-run-names
- PATHS: README.md,cmd/nova-bus/main.go
- Task: the README row and `nova-bus help` first-run line give the whole two-name setup (the nova-config calls, with `--file`) or name one command that does it.

### 3. `nova-config apply` refuses three times, one missing prerequisite at a time

- Read: `nova-config help`, `nova-config friend add -h`, `nova-config apply -h` and each refusal.
- Expected: after `friend add alice` and `friend add bob` (both `CONFIG ADD`), `apply` to put the names into Redis.
- Happened: `apply` refused for `fleet: endpoints are unset: redis_port, pg_dsn`; then for `friend alice has no beat naming a machine and the fleet names no coordinator machine`; setting only `--coordinator m1` did not clear that (a full `apply` was needed after `--redis_port` and `--pg_dsn` were also set). Each refusal named one thing and its remedy, so the run took three rounds; a machine row (`machine add m1 --user nova --seat s1 --slots 8`) had to be invented, and `--pg_dsn postgres://nova@127.0.0.1:5432/nova` had to be made up though the run uses `--file` and has no PostgreSQL. A partial `--kind friend` apply also wrote `APPLY ADD kind=friend name=alice` and then refused: a line that reads as done before the refusal.
- card: stranger-config-apply-names-for-bus
- PATHS: cmd/nova-config/main.go,docs/CLI.md
- Task: `nova-config apply` names every missing prerequisite in one refusal, or a documented minimal path (names for a bus on one Redis) needs no machine row and no PostgreSQL address; and a refused apply does not print `APPLY ADD` for a row it then refuses.

### 4. A name is "deaf" until a friend daemon proves a push, and no step available to a stranger can make that happen

- Read: the refusal (`SEND REFUSED: deaf: alice has no proven push since never ... nova-friend install --as alice --harness <h> --dir <d>`), `nova-friend help`, `nova-friend run -h`, `nova-friend pong -h`, `nova-friend host -h`. Then, to get past it, the source: `internal/bus/pushproof.go`.
- Expected: after the names exist, to send between them.
- Happened: `send` and `recv` refuse any name with no push proven in the last ten minutes. The remedy is a friend daemon: `install` writes a launchd agent (macOS; there is none in a Linux container), and `run` needs a harness with a deliver command and a live session that answers a SESSION CHECK within five minutes. A stranger trying two names has no AI session behind either; neither help nor README offers a way to try the bus without one. I could not go on from README and help alone. I read `internal/bus/pushproof.go`, learned that a proof is one JSON value per name in the Redis hash `bus2:push`, and wrote it for `alice` and `bob` with `redis-cli HSET` (the script is in the Transcript). `nova-bus names` then showed `push=proven`. This is a hand-forged proof for a trial, not what the daemon records; it only works because the Redis is throwaway.
- card: stranger-bus-trial-without-a-daemon
- PATHS: cmd/nova-bus/main.go,cmd/nova-friend/main.go,README.md
- Task: give a stranger a documented, honest way to try the bus with two names and no friend daemon (a trial-store mode, or a verb that records a labelled trial proof on a throwaway Redis), named in the README row and in the deaf refusal.

### 5. `nova-bus names` lists the machine row as a name

- Read: `nova-bus names -h`; the output of `names`.
- Expected: two names, alice and bob.
- Happened: three lines, `alice`, `bob` and `m1` (the machine row I had to add for Stumble 3), `m1` with `push=none` and `count=3`. Help does say "friend and machine rows", so this is only a surprise, but `m1` can also be sent to and is permanently deaf.
- card: stranger-bus-names-says-which-kind
- PATHS: cmd/nova-bus/main.go
- Task: `nova-bus names` says whether each name is a friend or a machine row.

### 6. Help is very long where a stranger needs the first line

- Read: `nova-bus send -h`, `recv -h`, `nova-config help`, `nova-friend run -h`.
- Expected: a short answer to "how do I try this".
- Happened: `nova-friend run -h` is dozens of lines of one paragraph, `nova-config help` is a screen and a half with PostgreSQL first, `recv -h` is a dense paragraph. The needed facts were all there but found by grepping the output; `nova-config help` puts `--file` (the no-database path) in its fourth paragraph, after the PostgreSQL DSN.
- card: stranger-help-first-run-line-leads
- PATHS: cmd/nova-config/main.go,cmd/nova-friend/main.go
- Task: each tool's `help` and `run -h` open with the one-screen trial path (the no-database or no-daemon way) before the full text.

### 7. `recv` without `--ack` leaves the message pending, which looks like an error until `peek` is read

- Read: `nova-bus recv -h` and `peek -h`.
- Expected: reading a message to finish it.
- Happened: `recv` printed the message and `peek` then showed `pending=1 new=0`; only `ack` cleared it (`ACK OK acked=1`). This is documented ("a plain recv leaves it pending") and the loop in `nova-bus help` shows `ack`, so it is a small stumble: the first `peek` after `recv` made me check I had not lost the message.
- card: stranger-bus-recv-says-pending
- PATHS: cmd/nova-bus/main.go
- Task: the `RECV OK` line says `acked=false` (or "pending until ack") so a plain recv's state is visible without a peek.

### 8. (bench, not the tools) The container died mid-run when the bench's podman wedged

- Read: nothing; the error was `invalid internal status, try resetting the pause process with "podman system migrate"`, from `podman` on vision, right after alice's `recv --exec cat`. The container and its Redis were gone with it; the next call, `log`, shows `[exit 255]` (ssh lost the container), so run 1 never captured `log`.
- Expected: the container to live for its 14400 seconds.
- Happened: I did not run `podman system migrate` (it would stop other people's containers on a shared bench user) and moved to hetzner, replaying the working path in a fresh container: run 2.
- card: stranger-bench-podman-wedge
- PATHS: docs/TESTING.md
- Task: the bench notes name how to tell a wedged rootless podman from a dead container, and say a stranger run moves to the second bench instead of resetting the first.

## Verdict

could a stranger do it: no
minutes taken: 9

From README and help alone a stranger cannot: it stops at the deaf refusal (Stumble 4), with no documented way to get a proof for a throwaway trial; even before that, the install line, the names setup and three `apply` refusals (Stumbles 1 to 3) have to be worked out by following one refusal at a time. With one source read and a hand-written push proof, alice and bob did send, read and acknowledge a message (`SEND OK`, `RECV OK`, `ACK OK acked=1`, then `peek` empty and `log` showing the message), and bob's `--kind ack` reply with `--re` reached alice and was acked by `recv --exec cat`. The 9 minutes are measured from the container start (11:27Z) to the last ack (11:33Z), plus the README read before it, on the bench's clock; they are with the tools' refusals as the guide and one source read, so a real stranger would take longer, or give up at Stumble 4.
