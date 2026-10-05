# A stranger sends a message between two names with nova-bus

A cold run of `nova-bus` on a throwaway Redis, 2026-10-05, lens stranger: two names, `alice` and `bob`, send each other a message, read it and acknowledge it, working from `README.md` and the tools' own help.

## Setup

- Bench: `hetzner` (vision has no podman). One container for the whole session, from the functional image (`localhost/nova-functional:ctx-7e545976810241cb`, built from `infra/functional-image`, carries Redis 8.10.2): `podman run -d --rm --name stranger-bus-w4 --timeout 14400 --network none --init --memory 4g --cap-drop all --security-opt no-new-privileges`, the checkout mounted read only at `/src`, the Go module cache read only at `/gomodcache`, and `~/nova-bench/stranger/<job>/work` at `/work`. Redis ran inside it on `127.0.0.1:6379`; no fleet store, no Studio. The container was stopped and the bench directory removed when the run ended.
- Worker: Claude Sonnet 5.5 in Claude Code, acting as the stranger. The run is of the tree at `origin/sprint/mechanical-2026-10-02` (7a1152a4b).
- Versions, as printed: `nova-bus devel linux/amd64 go1.26.6` and `nova-config devel linux/amd64 go1.26.6` (see the stumble about `devel`). `redis-server` 8.10.2 comes from the image manifest.
- What was read: `README.md` and the output of `nova-bus help`, `nova-config help`; no source, no spec, no `docs/CLI.md`, no `docs/USAGE.md`. Before starting, the card required reading `AGENTS.md` and the functional image's README for the container flags; that is bench knowledge, not tool knowledge, and is not counted as a stumble. The first reading of the README was by the worker, who already knew the project exists; the run is as cold as one worker can make it, not a blank mind.
- Transcript notes: each command is run by `podman exec` as the unprivileged `bench` user; `[exit N]` is the status the harness printed after the command. `/work` is owned by the host user and is not writable by `bench` (rootless uid mapping), so binaries went to `$HOME/bin` and the build cache to `/tmp/gocache`; that is the bench setup, not the tool. The Redis server was started detached with `podman exec -d`; its command is shown, it prints nothing. Nothing is trimmed.

## Transcript

```
$ go install github.com/mas-bandwidth/nova-tools/cmd/nova-bus@v1.0.0
go: github.com/mas-bandwidth/nova-tools/cmd/nova-bus@v1.0.0: module lookup disabled by GOPROXY=off
[exit 1]
$ cd /src && GOBIN=/work/bin GOCACHE=/tmp/gocache GOFLAGS=-mod=mod go install ./cmd/nova-bus 2>&1 | tail -5; ls /work/bin
github.com/mas-bandwidth/nova-tools/cmd/nova-bus: go install github.com/mas-bandwidth/nova-tools/cmd/nova-bus: mkdir /work/bin/: permission denied
ls: cannot access '/work/bin': No such file or directory
[exit 2]
$ cd /src && GOBIN=/work/bin GOCACHE=/tmp/gocache go install ./cmd/nova-bus; ls -l /work/bin
github.com/mas-bandwidth/nova-tools/cmd/nova-bus: go install github.com/mas-bandwidth/nova-tools/cmd/nova-bus: mkdir /work/bin/: permission denied
ls: cannot access '/work/bin': No such file or directory
[exit 2]
$ cd /src && GOBIN=$HOME/bin GOCACHE=/tmp/gocache go install ./cmd/nova-bus; ls -l $HOME/bin
total 10248
-rwxr-xr-x 1 bench bench 10486611 Oct  5 19:41 nova-bus
[exit 0]
$ export PATH=$HOME/bin:$PATH; nova-bus version; nova-bus help
nova-bus devel linux/amd64 go1.26.6
nova-bus: messages between AIs over Redis streams: sent once, delivered until acked

how it works: the loop: send --as <me> --to <friend> --subject <s> --body <text> sends;
recv --as <me> --forever --exec '<deliver-into-session>' takes each message in, acked on exit 0;
ack --as <me> --id <id> acks by hand after a plain recv; names: nova-config friend and machine rows.
one stream per recipient (bus2:to:<name>) under a consumer group, one log (bus2:log); all or none.
first run: a Redis naming ada and bob at --redis (else NOVA_BUS_REDIS); loopback or tailnet only.

usage:
  nova-bus send [--as <me>] --to <a,b> [--cc <c>] --subject <s> (--body <text> | --stdin) [--re <id>] [--kind <k>] [--redis <addr>] [--dry-run]
  nova-bus peek [--as <me>] [--kind <k>[,<k>]] [--redis <addr>]
  nova-bus recv [--as <me>] [--kind <k>[,<k>]] [--max <n> | --all] [--ack] [--exec <command>] [--forever --exec <command>] [--redis <addr>] [--dry-run]
  nova-bus ack [--as <me>] --id <id,...> [--redis <addr>] [--dry-run]
  nova-bus log [--bodies] [--max <n>] [--redis <addr>]
  nova-bus names [--redis <addr>]
  nova-bus version
  nova-bus help [<verb>]

Every verb takes --json: the same result as one JSON object on stdout. A verb that lists takes --max <n> (default 20, 0 lists all) and says MORE for the rest. `<verb> -h` lists a verb's flags.

exit codes: 0 done, 1 the verb ran and said no (recv: nothing waiting; recv --exec: the command failed), 2 could not run (a flag, an input, a store that did not answer).

example:
  nova-bus send --as ada --to bob --subject hello --body "are you there?"
  nova-bus peek --as bob
  nova-bus recv --as bob --exec true
  nova-bus ack --as bob --id 01ARZ3NDEKTSV4RRFFQ69G5FAV
  nova-bus log --max 5
  nova-bus names
[exit 0]
$ redis-server --bind 127.0.0.1 --port 6379 --save '' --appendonly no   # started detached inside the container
$ export PATH=$HOME/bin:$PATH; redis-cli ping; nova-bus names --redis 127.0.0.1:6379; nova-bus send --as alice --to bob --subject hello --body "are you there?" --redis 127.0.0.1:6379
PONG
NAMES OK count=0
SEND REFUSED: alice is no known name; the names are nova-config's friend and machine rows (nova-bus names lists them); add one with nova-config friend add alice --slots 1 --tiers flash --as <you>, then nova-config apply; run: nova-bus help
SEND REFUSED: bob is no known name; the names are nova-config's friend and machine rows (nova-bus names lists them); add one with nova-config friend add bob --slots 1 --tiers flash --as <you>, then nova-config apply; run: nova-bus help
[exit 2]
$ cd /src && GOBIN=$HOME/bin GOCACHE=/tmp/gocache go install ./cmd/nova-config && export PATH=$HOME/bin:$PATH && nova-config version && nova-config help
nova-config devel linux/amd64 go1.26.6
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
  nova-config help [<verb>]
  nova-config version
  nova-config kinds [--json]
  nova-config migrate [--pg <dsn> | --file <path>] [--print] [--dry-run] [--json]
  nova-config status [--pg <dsn> | --file <path>] [--redis <addr>] [--json]
  nova-config apply [--pg <dsn> | --file <path>] [--redis <addr>] [--as <name>]
                    [--kind <kind>] [--dry-run] [--json]
  nova-config inventory [--redis <addr> | --fixture <file>] [--list | --host <name>]
                        [--timeout <duration>]
  nova-config <kind> add <name> --<field> <value> ... --as <name> [--dry-run] [--json]
  nova-config <kind> set <name> --<field> <value> ... --as <name> [--dry-run] [--json]
  nova-config <kind> remove <name> --as <name> [--dry-run] [--json]
  nova-config <kind> list [--json]
  nova-config <kind> show <name> [--json]
  nova-config <kind> history <name> [--json]
  nova-config machine width <name> [--json]
  nova-config machine self [--check] [--json]
  nova-config fleet set|show|history        one row each, no name:
                                            fleet and sprint have no add, remove or list
  nova-config sprint set|show|history
  nova-config <kind> <verb> -h              the verb's flags (required ones marked),
                                            its effect and a worked example

The store is --pg <dsn> (or NOVA_PG_DSN; the password is never on the line:
NOVA_PG_PASSWORD_ENV holds the name of the variable that holds the password,
NOVA_PG_PASSWORD when it is unset, and never the password itself), or --file
<path>, or --seat <name> (or NOVA_SEAT) which supplies the DSN and password
variable name from the seat profile. --redis is host:port (NOVA_SPRINT_REDIS,
then NOVA_REDIS_ADDR, then the seat's address). --as is the name a write is
recorded under (NOVA_FRIEND, or the seat name).
Lose Redis: run nova-config apply.

Fleet apply and inventory require explicit redis_port and pg_dsn; set both
with nova-config fleet set --redis_port <port> --pg_dsn <dsn> --as <actor>.

exit codes: 0 done, 1 refused (the verb ran and the store said no; migrate --dry-run:
ready=no, nothing attempted), 2 could not run (usage, or a store that did not answer);
machine self: 2 not a row, 3 unreadable

kinds (nova-config <kind> add -h describes each field):
  machine  a machine of the fleet, named by its tailnet host: the login, the seat, its ceiling, its
           runners, the sprint member's width on it, and whether it is a TLC record machine
           add needs --user --seat --slots; also --runners --width --tla --note
  fleet    the one row of fleet-wide facts: the store and coordinator machines, Redis port, explicit
           password-free Postgres URI and the bus store's address
           set takes --store --coordinator --redis_port --pg_dsn --bus
  friend   an AI friend: her slots, which tiers she can do, her roles, and her width, the jobs she
           works at once, and her delivery mode
           add needs --slots --tiers; also --roles --width --mode
  sprint   the one row of sprint-global facts: which friend coordinates and the decide_* bars, each
           a probability in [0,1]; nova-config sprint set -h says what each bar decides
           set takes --coordinator --decide_bounce --decide_review --decide_score_bar
           --decide_attempt_no_result --decide_attempt_nothing_to_do --decide_grade
           --decide_gate_flaky --decide_gate_preexisting --decide_judgment_bar --decide_brief_bar
           --answer_rules_off
  loop     a supervised loop on one machine: its command, the seat and secret names it opens, and
           how it runs (every n seconds or kept alive); a nova-swarm member's width, a reader's too,
           is its machine row's, never the argv's
           add needs --machine --argv; also --seat --keys --every --keepalive --enabled
  route    a route of a model tier: the provider and model a card of that tier runs on, its token
           budget and deadline; the tier's array orders its routes; frontier cards are never dealt
           from routes, they escalate to the coordinator
           add needs --tier --provider --model --deadline; also --harness --tokens --usd --enabled
           --first --price_input --price_cache_read --price_cache_write --price_output
           --reasoning_as_output --long_context --price_input_long --price_output_long
           --price_request --billing --gateway_percent --price_source --price_as_of --note
  tier     a model tier's route array: the deal takes routes[index mod len] for each card of the
           tier, a route named twice taking two turns; one row each for flash and pro and heavy,
           created by migrate
           set takes --routes

example:
  nova-config migrate --file try.json
  nova-config machine add m1 --user nova --seat s1 --slots 8 --width 4 --as a1 --file try.json
  nova-config machine set m1 --width 6 --as a1 --file try.json
  nova-config machine list --file try.json
  nova-config machine history m1 --file try.json
[exit 0]
$ export PATH=$HOME/bin:$PATH; cd /tmp; nova-config migrate --file try.json; nova-config friend add alice --slots 1 --tiers flash --as me --file try.json; nova-config friend add bob --slots 1 --tiers flash --as me --file try.json; nova-config apply --file try.json --redis 127.0.0.1:6379 --as me
CONFIG MIGRATE file=try.json from=0 to=32 applied=32
CONFIG ADD kind=friend name=alice rev=1
CONFIG ADD kind=friend name=bob rev=2
nova-config apply REFUSED: fleet: endpoints are unset: redis_port, pg_dsn; run: nova-config fleet set --redis_port <port> --pg_dsn <dsn> --as <actor>, then nova-config apply --kind fleet --as <actor>
[exit 1]
$ export PATH=$HOME/bin:$PATH; cd /tmp; nova-config apply --kind friend --file try.json --redis 127.0.0.1:6379 --as me; nova-bus names --redis 127.0.0.1:6379
APPLY ADD kind=friend name=alice
NAMES OK count=0
nova-config apply REFUSED: friend alice has no beat naming a machine and the fleet names no coordinator machine to charge her slots to; run: nova-config fleet set --coordinator <machine>, then apply; run: nova-config apply --dry-run
[exit 0]
$ export PATH=$HOME/bin:$PATH; cd /tmp; nova-config machine add m1 --user nova --seat s1 --slots 8 --width 4 --as me --file try.json; nova-config fleet set --coordinator m1 --as me --file try.json; nova-config apply --kind friend --file try.json --redis 127.0.0.1:6379 --as me; nova-bus names --redis 127.0.0.1:6379
CONFIG ADD kind=machine name=m1 rev=3
CONFIG SET kind=fleet name=fleet rev=4 changed=coordinator
APPLY ADD kind=friend name=alice
nova-config apply REFUSED: friend alice has no beat naming a machine and the fleet names no coordinator machine to charge her slots to; run: nova-config fleet set --coordinator <machine>, then apply; run: nova-config apply --dry-run
NAMES OK count=0
[exit 0]
$ export PATH=$HOME/bin:$PATH; cd /tmp; nova-config apply --dry-run --file try.json --redis 127.0.0.1:6379 --as me; nova-config fleet set --store m1 --redis_port 6379 --pg_dsn postgres://me@127.0.0.1:5432/nova --as me --file try.json; nova-config apply --file try.json --redis 127.0.0.1:6379 --as me; nova-bus names --redis 127.0.0.1:6379
nova-config apply REFUSED: fleet: endpoints are unset: redis_port, pg_dsn; run: nova-config fleet set --redis_port <port> --pg_dsn <dsn> --as <actor>, then nova-config apply --kind fleet --as <actor>
CONFIG SET kind=fleet name=fleet rev=5 changed=pg_dsn,redis_port,store
APPLY ADD kind=machine name=m1
CONFIG APPLY kind=machine add=1 set=0 remove=0 rev=3 ms=0
APPLY SET kind=fleet name=fleet changed=store,coordinator,redis_port,pg_dsn
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
NAMES OK count=3
NAMES NAME name=alice
NAMES NAME name=bob
NAMES NAME name=m1
[exit 0]
$ export PATH=$HOME/bin:$PATH; nova-bus send --as alice --to bob --subject hello --body 'are you there?' --redis 127.0.0.1:6379; nova-bus peek --as bob --redis 127.0.0.1:6379; nova-bus recv --as bob --redis 127.0.0.1:6379
SEND OK id=01M46SCYMBPPB9P8TBF9ZT6046 to=bob cc=- at=2026-10-05T19:42:08Z bytes=14 sha256=cf97adc337983a14daab1089bf14c6ab50e658f0136517e0048407e786b6e745 login=none
PEEK OK pending=0 new=1
PEEK MESSAGE state=new id=01M46SCYMBPPB9P8TBF9ZT6046 from=alice at=2026-10-05T19:42:08Z subject="hello"
RECV OK id=01M46SCYMBPPB9P8TBF9ZT6046 from=alice to=bob cc=- re=- at=2026-10-05T19:42:08Z login=none subject="hello"

are you there?
[exit 0]
$ export PATH=$HOME/bin:$PATH; nova-bus peek --as bob --redis 127.0.0.1:6379; nova-bus ack --as bob --id 01M46SCYMBPPB9P8TBF9ZT6046 --redis 127.0.0.1:6379; nova-bus peek --as bob --redis 127.0.0.1:6379; nova-bus send --as bob --to alice --subject re:hello --re 01M46SCYMBPPB9P8TBF9ZT6046 --body 'yes, here' --redis 127.0.0.1:6379; nova-bus recv --as alice --ack --redis 127.0.0.1:6379; nova-bus log --max 5 --redis 127.0.0.1:6379
PEEK OK pending=1 new=0
PEEK MESSAGE state=pending id=01M46SCYMBPPB9P8TBF9ZT6046 from=alice at=2026-10-05T19:42:08Z subject="hello"
ACK OK acked=1 asked=1 login=none
ACK ID id=01M46SCYMBPPB9P8TBF9ZT6046 acked=true
PEEK OK pending=0 new=0
SEND OK id=01M46SD274QSM4SDCAG98DYZ4V to=alice cc=- at=2026-10-05T19:42:11Z bytes=9 sha256=cf4ff293d349a9ba5a37e129df00975540c336b6609fef3f9a310dfb65f23a87 login=none
RECV OK id=01M46SD274QSM4SDCAG98DYZ4V from=bob to=alice cc=- re=01M46SCYMBPPB9P8TBF9ZT6046 at=2026-10-05T19:42:11Z login=none acked=true subject="re:hello"

yes, here
LOG OK total=2
LOG MESSAGE id=01M46SCYMBPPB9P8TBF9ZT6046 from=alice to=bob cc=- re=- at=2026-10-05T19:42:08Z subject="hello"
LOG MESSAGE id=01M46SD274QSM4SDCAG98DYZ4V from=bob to=alice cc=- re=01M46SCYMBPPB9P8TBF9ZT6046 at=2026-10-05T19:42:11Z subject="re:hello"
[exit 0]
```

The first attempt at the exchange is the `send` near the top of the transcript, refused because the names did not exist; the successful exchange is the `send`, `peek`, `recv`, `peek`, `ack`, `peek` and the reply with `recv --ack` that follow the names being applied.

## Stumbles

### The README's install line cannot run without a network and names an older release

- Read: README "Try one on a small example": "These are the Nova Tools 1.0.0 commands … `go install github.com/mas-bandwidth/nova-tools/cmd/nova-memory@v1.0.0`".
- Expected: a line I could run for `nova-bus` that gives the version I was testing (1.2.0).
- Happened: the line for `nova-bus` at `@v1.0.0` fails in the container (`module lookup disabled by GOPROXY=off`; the container has no network, by the rules of the run), and in any case would install 1.0.0, not the tree. I then read the README's "source checkout" sentence and guessed `cd <checkout> && go install ./cmd/nova-bus`, which worked. The README never says that command.
- Proposed fix: say the from-checkout install in one line beside the `@v1.0.0` one, and say which version the page describes.

card: readme-install-from-checkout | PATHS: README.md,internal/docs/readme_catalogue_test.go | one README line for installing a tool from a source checkout, and the release the page describes stated once

### `version` prints `devel`, not a version

- Read: `nova-bus version`, `nova-config version`.
- Expected: `1.2.0` (the brief says nova-tools v1.2.0), or at least a version I could compare with a release.
- Happened: `nova-bus devel linux/amd64 go1.26.6`. A tool built from a checkout carries no version, so I could not tell which release I was running.
- Proposed fix: stamp the version from the repository (the module's tag or `git describe`) when built by `go install ./cmd/...`, not only by the release build.

card: version-in-checkout-build | PATHS: internal/version,cmd/nova-bus/main.go | a checkout build prints the release it was built from instead of `devel`

### The first send is refused, and its fix needs a database or a file the README never names

- Read: the README row ("a separate running Redis instance whose nova-config rows name the sender and the recipient") and `nova-bus help` ("first run: a Redis naming ada and bob at --redis").
- Expected: one command that puts names in the Redis, or a flag on `nova-bus` to name them.
- Happened: `send` was refused with `alice is no known name … add one with nova-config friend add alice --slots 1 --tiers flash --as <you>, then nova-config apply`. That hint omitted the store: the README says `nova-config` needs PostgreSQL, and nothing in the refusal says a local `--file` works. I built `nova-config`, read its help and found `--file <path>` ("to try every verb with no database") by luck of reading the long help. Two builds and a help page stood between me and the first message.
- Proposed fix: the refusal and the README row give the whole path for a throwaway Redis: `nova-config friend add … --file try.json` then `apply --file try.json --redis …`.

card: bus-first-run-names-path | PATHS: cmd/nova-bus,README.md | the unknown-name refusal and the README bus row state the no-database way to name alice and bob on a throwaway Redis

### `nova-config apply` takes three refusals and a guessed fleet row before it names anyone

- Read: `nova-config apply` refusals only; `nova-config help` shows the fleet needs `--redis_port` and `--pg_dsn` for apply.
- Expected: after `friend add alice` and `friend add bob`, `apply` writes the names.
- Happened: (1) `apply` was refused: `fleet: endpoints are unset: redis_port, pg_dsn`. (2) Following the hint's `--kind` I ran `apply --kind friend`, which printed `APPLY ADD kind=friend name=alice` and then `REFUSED: friend alice has no beat naming a machine and the fleet names no coordinator machine`, so a success-looking line and a refusal in one output, and `nova-bus names` still said `count=0`. (3) After `machine add m1` and `fleet set --coordinator m1` the same refusal came back; it only went through after I also guessed `fleet set --store m1 --redis_port 6379 --pg_dsn postgres://me@127.0.0.1:5432/nova` (a Postgres I never used) and ran a plain `apply`. A stranger who only wants two names has to invent a machine, a coordinator and a database address.
- Proposed fix: a way to apply only friends (or names) to Redis with no machine, fleet or Postgres row, or a refusal that names the whole set of rows needed in one message; and no `APPLY ADD` line for a row the apply then refuses.

card: config-apply-names-only | PATHS: cmd/nova-config,internal/config | applying friend rows to a throwaway Redis needs no machine, coordinator or Postgres address, and a refused apply prints no ADD line

## Verdict

could a stranger do it: yes, with the four stumbles above.

minutes: 10, from the first build to the last acknowledged message, read off the file time of the `nova-bus` binary and the timestamps in the transcript (19:41 to 19:42 UTC for the exchange itself; the names took most of the rest, stumbles 3 and 4 above).

A stranger gets two names to send, read and acknowledge a message with `nova-bus` once the names are in Redis, and the tool's own refusals walk them most of the way. What stopped a cold reader was never `nova-bus`: it was getting a binary with a version, and getting two names applied through `nova-config` without a database or a fleet.
