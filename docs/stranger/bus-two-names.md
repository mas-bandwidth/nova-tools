# Stranger run: nova-bus, two names

A cold run of nova-bus by a stranger, from the README and the tools' own help alone. The goal: two names, alice and bob, on a throwaway Redis inside a container, send each other a message, read it, and acknowledge it. [internal/docs/stranger_bus_test.go](../../internal/docs/stranger_bus_test.go) holds this file to its four sections, a transcript with a command line, and a proposed card on every stumble.

## Setup

- Run by: rowan-space, a bud of Rowan, Claude Sonnet 5.5 (`claude-sonnet-5-5`) under the Claude Agent SDK harness. The card was dealt to the friend rowan-next; the run is this bud's.
- Date: 2026-10-05, clocks in EDT.
- Bench: hetzner (Linux amd64, podman 4.9.3). vision answers but has no podman, so it could not host the container; the Go tests of this card ran on vision.
- One throwaway container for the whole session: `podman run -d --rm --timeout 14400 --network none` of `localhost/nova-functional:ctx-7e545976810241cb` (the functional image of infra/functional-image, which carries redis-server and Go 1.26.6), with a work directory mounted at /work, a Go build cache, and the image's module cache. `sleep 14400` held it open and every command below was a `podman exec` into it, so the Redis and every process belong to the container; it was stopped at the end and the bench directory was removed.
- Source: the nova-tools tree at `7a1152a4b` (branch sprint/mechanical-2026-10-02) was mounted at /work/nova-tools, because the container has no network (see S1).
- Versions printed: `nova-bus version` printed `nova-bus devel linux/amd64 go1.26.6`; `nova-config version` printed `nova-config devel linux/amd64 go1.26.6`. The Redis server's version was not printed.
- Read: README.md in full, `nova-bus help`, `nova-bus send -h`, `nova-bus recv -h`, `nova-config help` and `nova-config friend add -h`. Not read: the source, the specs, docs/CLI.md, docs/USAGE.md or any other doc. infra/functional-image was read only to run the container, as the card says.

## Transcript

Every command run in the container, with its exact output. `[exit N]` is the exit status. Output trimmed only where marked `[trimmed]`; the long help screens are cut to the lines the run used.

```
$ go install github.com/mas-bandwidth/nova-tools/cmd/nova-bus@v1.0.0
go: github.com/mas-bandwidth/nova-tools/cmd/nova-bus@v1.0.0: module lookup disabled by GOPROXY=off
[exit 1]
$ cd /work/nova-tools && GOBIN=/work/bin go install ./cmd/nova-bus && ls /work/bin
nova-bus
[exit 0]
$ export PATH=/work/bin:$PATH; nova-bus version; nova-bus help
nova-bus devel linux/amd64 go1.26.6
nova-bus: messages between AIs over Redis streams: sent once, delivered until acked

how it works: the loop: send --as <me> --to <friend> --subject <s> --body <text> sends;
recv --as <me> --forever --exec '<deliver-into-session>' takes each message in, acked on exit 0;
ack --as <me> --id <id> acks by hand after a plain recv; names: nova-config friend and machine rows.
one stream per recipient (bus2:to:<name>) under a consumer group, one log (bus2:log); all or none.
first run: a Redis naming ada and bob at --redis (else NOVA_BUS_REDIS); loopback or tailnet only.
[trimmed: the usage block, exit codes and examples]
[exit 0]
$ export PATH=/work/bin:$PATH; redis-server --port 6379 --bind 127.0.0.1 --daemonize yes --dir /work --save "" >/dev/null; sleep 1; redis-cli ping
PONG
[exit 0]
$ export PATH=/work/bin:$PATH; nova-bus send --redis 127.0.0.1:6379 --as alice --to bob --subject hello --body "are you there?"
SEND REFUSED: alice is no known name; the names are nova-config's friend and machine rows (nova-bus names lists them); add one with nova-config friend add alice --slots 1 --tiers flash --as <you>, then nova-config apply; run: nova-bus help
SEND REFUSED: bob is no known name; the names are nova-config's friend and machine rows (nova-bus names lists them); add one with nova-config friend add bob --slots 1 --tiers flash --as <you>, then nova-config apply; run: nova-bus help
[exit 2]
$ export PATH=/work/bin:$PATH; nova-bus names --redis 127.0.0.1:6379; nova-bus send -h
NAMES OK count=0
[trimmed: the send help, from "usage: nova-bus send [flags]" to its "effect:" line]
[exit 0]
$ cd /work/nova-tools && GOBIN=/work/bin go install ./cmd/nova-config && export PATH=/work/bin:$PATH && nova-config version && nova-config help | head -60
nova-config devel linux/amd64 go1.26.6
nova-config: a fleet's machines and AI friends as rows in PostgreSQL, applied into Redis

how it works: each kind (machine, fleet, friend, sprint, loop, route, tier) is a
table of rows in PostgreSQL's schema config, which migrate makes; every write
adds a history row naming who made it. apply copies the rows into Redis, the
view the fleet reads; inventory prints that view for Ansible. --file <path>
keeps the rows in a local JSON file instead, to try every verb with no database.
[trimmed: the rest of the first 60 lines of the help]
[exit 0]
$ export PATH=/work/bin:$PATH; cd /work; nova-config friend add -h
usage: nova-config friend add [flags]
effect: store write: one row and its history row, in PostgreSQL or the --file; --dry-run prints the change (CONFIG DRY-RUN) and writes nothing
required: --slots --tiers (and --as); every other field takes its default
example: nova-config friend add f1 --slots 4 --tiers flash,pro --roles builder --as a1 --file try.json
[trimmed: the flags list]
[exit 0]
$ export PATH=/work/bin:$PATH; cd /work; nova-config migrate --file ./fleet.json; nova-config friend add alice --slots 1 --tiers flash --as alice --file ./fleet.json
CONFIG MIGRATE file=./fleet.json from=0 to=32 applied=32
CONFIG ADD kind=friend name=alice rev=1
[exit 0]
$ export PATH=/work/bin:$PATH; cd /work; nova-config friend add bob --slots 1 --tiers flash --as alice --file ./fleet.json; nova-config apply --file ./fleet.json --redis 127.0.0.1:6379 --as alice
CONFIG ADD kind=friend name=bob rev=2
nova-config apply REFUSED: fleet: endpoints are unset: redis_port, pg_dsn; run: nova-config fleet set --redis_port <port> --pg_dsn <dsn> --as <actor>, then nova-config apply --kind fleet --as <actor>
[exit 1]
$ export PATH=/work/bin:$PATH; cd /work; nova-bus names --redis 127.0.0.1:6379
NAMES OK count=0
[exit 0]
$ export PATH=/work/bin:$PATH; cd /work; nova-config apply --file ./fleet.json --redis 127.0.0.1:6379 --as alice --kind friend; nova-bus names --redis 127.0.0.1:6379
APPLY ADD kind=friend name=alice
nova-config apply REFUSED: friend alice has no beat naming a machine and the fleet names no coordinator machine to charge her slots to; run: nova-config fleet set --coordinator <machine>, then apply; run: nova-config apply --dry-run
NAMES OK count=0
[exit 0]
$ export PATH=/work/bin:$PATH; cd /work; nova-config fleet set --coordinator box --as alice --file ./fleet.json
nova-config fleet set REFUSED: --coordinator box names no machine row; run: nova-config machine list --file ./fleet.json
[exit 1]
$ export PATH=/work/bin:$PATH; cd /work; nova-config machine add box --user bench --seat box --slots 4 --as alice --file ./fleet.json && nova-config fleet set --coordinator box --as alice --file ./fleet.json
CONFIG ADD kind=machine name=box rev=3
NOTE machine=box width=default: a sprint member at half its cores, as nova-sprint fleet sync reads them from its beat; its width is set apart from its slots; run: nova-config machine set box --width <n> (0: no member) --as alice --file ./fleet.json
CONFIG SET kind=fleet name=fleet rev=4 changed=coordinator
[exit 0]
$ export PATH=/work/bin:$PATH; cd /work; nova-config apply --file ./fleet.json --redis 127.0.0.1:6379 --as alice --kind friend; nova-bus names --redis 127.0.0.1:6379
APPLY ADD kind=friend name=alice
nova-config apply REFUSED: friend alice has no beat naming a machine and the fleet names no coordinator machine to charge her slots to; run: nova-config fleet set --coordinator <machine>, then apply; run: nova-config apply --dry-run
NAMES OK count=0
[exit 0]
$ export PATH=/work/bin:$PATH; cd /work; A="--file ./fleet.json --redis 127.0.0.1:6379 --as alice"; nova-config fleet set --redis_port 6379 --pg_dsn postgres://bench@127.0.0.1:5432/nova --as alice --file ./fleet.json && nova-config apply $A
CONFIG SET kind=fleet name=fleet rev=5 changed=pg_dsn,redis_port
APPLY ADD kind=machine name=box
CONFIG APPLY kind=machine add=1 set=0 remove=0 rev=3 ms=1
APPLY SET kind=fleet name=fleet changed=coordinator,redis_port,pg_dsn
CONFIG APPLY kind=fleet add=0 set=1 remove=0 rev=5 ms=0
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
$ export PATH=/work/bin:$PATH; nova-bus names --redis 127.0.0.1:6379
NAMES OK count=3
NAMES NAME name=alice
NAMES NAME name=bob
NAMES NAME name=box
[exit 0]
$ export PATH=/work/bin:$PATH; export NOVA_BUS_REDIS=127.0.0.1:6379; nova-bus send --as alice --to bob --subject hello --body 'are you there?'
SEND OK id=01M46SEGFR3BAPGY6E9M2XS93E to=bob cc=- at=2026-10-05T19:42:59Z bytes=14 sha256=cf97adc337983a14daab1089bf14c6ab50e658f0136517e0048407e786b6e745 login=none
[exit 0]
$ export PATH=/work/bin:$PATH; export NOVA_BUS_REDIS=127.0.0.1:6379; nova-bus peek --as bob
PEEK OK pending=0 new=1
PEEK MESSAGE state=new id=01M46SEGFR3BAPGY6E9M2XS93E from=alice at=2026-10-05T19:42:59Z subject="hello"
[exit 0]
$ export PATH=/work/bin:$PATH; export NOVA_BUS_REDIS=127.0.0.1:6379; nova-bus recv --as bob
RECV OK id=01M46SEGFR3BAPGY6E9M2XS93E from=alice to=bob cc=- re=- at=2026-10-05T19:42:59Z login=none subject="hello"

are you there?
[exit 0]
$ export PATH=/work/bin:$PATH; export NOVA_BUS_REDIS=127.0.0.1:6379; nova-bus peek --as bob; nova-bus recv -h
PEEK OK pending=1 new=0
PEEK MESSAGE state=pending id=01M46SEGFR3BAPGY6E9M2XS93E from=alice at=2026-10-05T19:42:59Z subject="hello"
[trimmed: the recv help, from "usage: nova-bus recv [flags]" to its "effect:" line]
[exit 0]
$ export PATH=/work/bin:$PATH; export NOVA_BUS_REDIS=127.0.0.1:6379; nova-bus ack --as bob --id 01M46SEGFR3BAPGY6E9M2XS93E
ACK OK acked=1 asked=1 login=none
ACK ID id=01M46SEGFR3BAPGY6E9M2XS93E acked=true
[exit 0]
$ export PATH=/work/bin:$PATH; export NOVA_BUS_REDIS=127.0.0.1:6379; nova-bus peek --as bob; nova-bus recv --as bob
PEEK OK pending=0 new=0
RECV NONE: nothing for bob
[exit 1]
$ export PATH=/work/bin:$PATH; export NOVA_BUS_REDIS=127.0.0.1:6379; nova-bus send --as bob --to alice --kind ack --re 01M46SEGFR3BAPGY6E9M2XS93E --subject 'hello back' --body 'yes, got it'; nova-bus recv --as alice --ack; nova-bus peek --as alice; nova-bus log --bodies
SEND OK id=01M46SEPMZGEMC3TN2PTB3S1CS to=alice cc=- kind=ack at=2026-10-05T19:43:05Z bytes=11 sha256=529a4131132f9143c776a33d3eb861620a79388b939e5221186c4a1ff1656406 login=none
RECV OK id=01M46SEPMZGEMC3TN2PTB3S1CS from=bob to=alice cc=- re=01M46SEGFR3BAPGY6E9M2XS93E kind=ack at=2026-10-05T19:43:05Z login=none acked=true subject="hello back"

yes, got it
PEEK OK pending=0 new=0
LOG OK total=2
LOG MESSAGE id=01M46SEGFR3BAPGY6E9M2XS93E from=alice to=bob cc=- re=- at=2026-10-05T19:42:59Z subject="hello" body="are you there?"
LOG MESSAGE id=01M46SEPMZGEMC3TN2PTB3S1CS from=bob to=alice cc=- re=01M46SEGFR3BAPGY6E9M2XS93E kind=ack at=2026-10-05T19:43:05Z subject="hello back" body="yes, got it"
[exit 0]
```

## Stumbles

### S1: the README's install line cannot run in a container with no network, and names a version the tree is not

- Read: README.md, "Try one on a small example": `go install github.com/mas-bandwidth/nova-tools/cmd/nova-memory@v1.0.0`, which I took with `nova-bus` in place of `nova-memory`.
- Expected: nova-bus installed.
- Happened: `module lookup disabled by GOPROXY=off`, because the container runs `--network none`. To get past it I built from a source checkout mounted into the container (`go install ./cmd/nova-bus`), which the README mentions only for testdata. The README also says "the Nova Tools 1.0.0 commands" while the card is a v1.2.0 run, and both built tools print `devel` for their version, so a source build cannot say which release it is.
card: stranger-readme-install-from-checkout
- PATHS: README.md
- Task: say in the README's install section how to build a tool from a checkout (`go install ./cmd/<tool>`) when the module proxy is unreachable, and which version the section describes.

### S2: nothing says how to start the throwaway Redis

- Read: README.md ("a separate running Redis instance", "throwaway Redis") and `nova-bus help` ("first run: a Redis naming ada and bob at --redis").
- Expected: a line giving the command that starts a throwaway Redis on loopback.
- Happened: I guessed `redis-server --port 6379 --bind 127.0.0.1 --daemonize yes --dir /work --save ""`. It worked, but it was a guess, and a stranger without redis-server on the machine has no pointer to it.
card: stranger-readme-throwaway-redis
- PATHS: README.md
- Task: add the one command that starts a loopback, no-persistence Redis for a trial, and say where redis-server comes from.

### S3: `nova-bus` cannot send until the names exist, and its refusal does not lead to a path with no database

- Read: `nova-bus help`, then the refusal of `send`: "add one with nova-config friend add alice --slots 1 --tiers flash --as <you>, then nova-config apply".
- Expected: a short way to give alice and bob a name on a throwaway Redis.
- Happened: the refusal names the verbs but not where the rows live; `nova-config` needs PostgreSQL unless `--file` is given, and `--file` was found only by reading all of `nova-config help`. The README's nova-bus row says the Redis must have rows naming the sender and recipient, with no steps.
card: stranger-bus-names-without-postgres
- PATHS: cmd/nova-bus, README.md
- Task: make the send refusal and the README row give the whole no-database path (`migrate --file`, `friend add --file`, `apply --file --redis`), the commands that worked in this transcript.

### S4: `nova-config apply` refuses four times for a bus trial, and its remedy does not hold

- Read: each REFUSED line of `nova-config apply` and `fleet set`.
- Expected: after `friend add` twice, `apply` puts alice and bob in Redis.
- Happened: the first refusal asked for `redis_port` and `pg_dsn` (a Postgres that does not exist in a `--file` trial; I gave a made-up DSN); `apply --kind friend` then asked for a coordinator machine; the remedy `fleet set --coordinator box` was itself refused until a machine row existed; and `apply --kind friend` alone kept refusing with the same text after the coordinator was set, because it does not apply the fleet row, so only a full `apply` worked. Four refusals on the way to two names.
card: stranger-config-apply-names-only
- PATHS: cmd/nova-config
- Task: let `nova-config apply --kind friend` for a `--file` store succeed with the machine and fleet rows it needs applied with it, or have the refusal name the machine row, the fleet row and the full `apply` in one line.

### S5: `nova-bus names` says OK with count=0

- Read: `NAMES OK count=0` on a Redis nobody had configured.
- Expected: a hint that no names exist yet.
- Happened: OK, exit 0, no pointer to `nova-config`, so the first two checks (before and after the first `friend add` plus a refused `apply`) looked healthy.
card: stranger-bus-names-empty-says-so
- PATHS: cmd/nova-bus
- Task: when `names` finds none, print a NOTE line saying no names are applied and naming `nova-config apply`.

## Verdict

Could a stranger do it: yes, with five stumbles. Alice sent bob a message, bob peeked, received and acknowledged it, bob replied with `--kind ack --re <id>`, alice received it with `--ack`, and `nova-bus log --bodies` showed both messages. The send, peek, recv, ack and log verbs needed no stumble; every stumble is in getting a name onto the Redis (S3, S4, S5), the install (S1) and the Redis itself (S2).

Minutes taken: under two minutes of command time on the bench clock, 15:42:11 EDT (first command) to 15:43:05 EDT (the log). The time spent reading and thinking between commands, and the container setup before the first command, is not on that clock and was not measured.
