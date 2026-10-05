# Stranger run: nova-bus between two names

A cold run of `nova-bus` by a bud new to nova-tools: alice and bob send each other a message, read it and acknowledge it, on a throwaway Redis inside one container. Run 2026-10-05 by rowan-mas (a Claude Sonnet 5.5 worker in Claude Code), card stranger-bus-two-names.w2, attempt 2.

Not fully cold, and said so: before the run I read `AGENTS.md` (the card's STEP 1 requires it) and I knew the card named `docs/CLI.md` as off limits. During the run I read only `README.md` (the first stumble below says what I read to get past it) and the tools' `help` and `-h` output.

## Setup

- Bench: hetzner (Linux amd64, Go 1.26.6, podman 4.9.3). vision has no podman, so it was not used.
- One container for the whole run: `podman run -d --rm --timeout 14400 --network none` from `localhost/nova-functional:ctx-7e545976810241cb` (the functional image: `redis-server`, Go, user `bench`), with `~/nova-bench/stranger/<job>/` mounted at `/work`. Redis ran inside it on `127.0.0.1:6379`. The container was restarted once, early, to add a module-cache mount (stumble 2); everything after that, including every command of the exchange, ran in the second container. Both are gone, and the work directory is removed.
- Versions printed (built from a source checkout of branch `rowan/integration-2026-10-04`, commit 60f8fc6e7, because the README's install line cannot run here):
  - `nova-bus version`: `nova-bus devel linux/amd64 go1.26.6`
  - `nova-config version`: `nova-config devel linux/amd64 go1.26.6`
  - `redis-server`: 8.10.2 (the image's; the run did not print it)

## Transcript

Every command was run inside the container, from the source checkout `/work/src` unless it starts with `cd /work`. `[exit N]` lines are added by the recording script. Nothing was trimmed except the long `nova-bus help` and `nova-config help` and `recv -h` / `send -h` pages, which are cut where marked.

```
$ go install github.com/mas-bandwidth/nova-tools/cmd/nova-bus@v1.0.0
go: github.com/mas-bandwidth/nova-tools/cmd/nova-bus@v1.0.0: module lookup disabled by GOPROXY=off
[exit 1]
$ nova-bus help
sh: 1: nova-bus: not found
[... trimmed: the rest of the help page ...]
[exit 127]
$ GOBIN=/work/bin go install ./cmd/nova-bus && ls /work/bin
go: downloading github.com/redis/go-redis/v9 v9.22.0
internal/redisconn/classify.go:14:2: module lookup disabled by GOPROXY=off
internal/redisconn/open.go:17:2: module lookup disabled by GOPROXY=off
[exit 1]
$ nova-bus version
sh: 1: nova-bus: not found
[exit 127]
$ nova-bus help
sh: 1: nova-bus: not found
[... trimmed: the rest of the help page ...]
[exit 127]
$ go env GOMODCACHE GOPROXY GOFLAGS; ls /home/bench/go 2>&1 | head
/gomodcache
off
-mod=readonly
ls: cannot access '/home/bench/go': No such file or directory
[exit 0]
--- container restarted with host module cache mounted read-only at /gomodcache (not in README) ---
$ GOBIN=/work/bin go install ./cmd/nova-bus && ls /work/bin
nova-bus
[exit 0]
$ nova-bus version
nova-bus devel linux/amd64 go1.26.6
[exit 0]
$ nova-bus help
nova-bus: messages between AIs over Redis streams: sent once, delivered until acked

how it works: the loop: send --as <me> --to <friend> --subject <s> --body <text> sends;
recv --as <me> --forever --exec '<deliver-into-session>' takes each message in, acked on exit 0;
ack --as <me> --id <id> acks by hand after a plain recv; names: nova-config friend and machine rows.
one stream per recipient (bus2:to:<name>) under a consumer group, one log (bus2:log); all or none.
first run: a Redis naming ada and bob at --redis (else NOVA_BUS_REDIS); loopback or tailnet only.

[... trimmed: the rest of the help page ...]
[exit 0]
$ redis-server --port 6379 --bind 127.0.0.1 --save "" --appendonly no --dir /work &   # started detached inside the container
$ redis-cli -h 127.0.0.1 ping
PONG
[exit 0]
$ nova-bus send --redis 127.0.0.1:6379 --as alice --to bob --subject hello --body "are you there?"
SEND REFUSED: alice is no known name; the names are nova-config's friend and machine rows (nova-bus names lists them); add one with nova-config friend add alice --slots 1 --tiers flash --as <you>, then nova-config apply; run: nova-bus help
SEND REFUSED: bob is no known name; the names are nova-config's friend and machine rows (nova-bus names lists them); add one with nova-config friend add bob --slots 1 --tiers flash --as <you>, then nova-config apply; run: nova-bus help
[exit 2]
$ nova-bus names --redis 127.0.0.1:6379
NAMES OK count=0
[exit 0]
$ nova-bus send -h
usage: nova-bus send [flags]
from `nova-bus help`:
  nova-bus send [--as <me>] --to <a,b> [--cc <c>] --subject <s> (--body <text> | --stdin) [--re <id>] [--kind <k>] [--redis <addr>] [--dry-run]
  nova-bus send --as ada --to bob --subject hello --body "are you there?"
Prints SEND OK id=<ulid> to=<names> cc=<names> [kind=<k>] at=<RFC3339> bytes=<n> sha256=<hex>: the id is the
message's for ever, and the byte count and digest are the body's as the store holds it, so a sender
[... trimmed: the rest of the help page ...]
[exit 0]
$ GOBIN=/work/bin go install ./cmd/nova-config && nova-config version
nova-config devel linux/amd64 go1.26.6
[exit 0]
$ nova-config help
nova-config: a fleet's machines and AI friends as rows in PostgreSQL, applied into Redis

how it works: each kind (machine, fleet, friend, sprint, loop, route, tier) is a
table of rows in PostgreSQL's schema config, which migrate makes; every write
adds a history row naming who made it. apply copies the rows into Redis, the
view the fleet reads; inventory prints that view for Ansible. --file <path>
keeps the rows in a local JSON file instead, to try every verb with no database.
first run: the example: lines need no database and write only ./try.json; the fleet's store is
[... trimmed: the rest of the help page ...]
[exit 0]
$ cd /work && nova-config migrate --file try.json
CONFIG MIGRATE file=try.json from=0 to=32 applied=32
[exit 0]
$ cd /work && nova-config friend add alice --slots 1 --tiers flash --as alice --file try.json
CONFIG ADD kind=friend name=alice rev=1
[exit 0]
$ cd /work && nova-config friend add bob --slots 1 --tiers flash --as alice --file try.json
CONFIG ADD kind=friend name=bob rev=2
[exit 0]
$ cd /work && nova-config apply --file try.json --redis 127.0.0.1:6379 --as alice
nova-config apply REFUSED: fleet: endpoints are unset: redis_port, pg_dsn; run: nova-config fleet set --redis_port <port> --pg_dsn <dsn> --as <actor>, then nova-config apply --kind fleet --as <actor>
[exit 1]
$ cd /work && nova-config fleet set --redis_port 6379 --pg_dsn postgres://alice@127.0.0.1:5432/nova --as alice --file try.json
CONFIG SET kind=fleet name=fleet rev=3 changed=pg_dsn,redis_port
[exit 0]
$ cd /work && nova-config apply --file try.json --redis 127.0.0.1:6379 --as alice
CONFIG APPLY kind=machine add=0 set=0 remove=0 rev=0 ms=0
APPLY SET kind=fleet name=fleet changed=redis_port,pg_dsn
CONFIG APPLY kind=fleet add=0 set=1 remove=0 rev=3 ms=3
APPLY ADD kind=friend name=alice
nova-config apply REFUSED: friend alice has no beat naming a machine and the fleet names no coordinator machine to charge her slots to; run: nova-config fleet set --coordinator <machine>, then apply; run: nova-config apply --dry-run
[exit 1]
$ nova-bus names --redis 127.0.0.1:6379
NAMES OK count=0
[exit 0]
$ cd /work && nova-config machine add m1 --user bench --seat s1 --slots 2 --as alice --file try.json
CONFIG ADD kind=machine name=m1 rev=4
NOTE machine=m1 width=default: a sprint member at half its cores, as nova-sprint fleet sync reads them from its beat; its width is set apart from its slots; run: nova-config machine set m1 --width <n> (0: no member) --as alice --file try.json
[exit 0]
$ cd /work && nova-config fleet set --coordinator m1 --as alice --file try.json
CONFIG SET kind=fleet name=fleet rev=5 changed=coordinator
[exit 0]
$ cd /work && nova-config apply --file try.json --redis 127.0.0.1:6379 --as alice
APPLY ADD kind=machine name=m1
CONFIG APPLY kind=machine add=1 set=0 remove=0 rev=4 ms=0
APPLY SET kind=fleet name=fleet changed=coordinator
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
$ nova-bus names --redis 127.0.0.1:6379
NAMES OK count=3
NAMES NAME name=alice
NAMES NAME name=bob
NAMES NAME name=m1
[exit 0]
$ nova-bus send --redis 127.0.0.1:6379 --as alice --to bob --subject hello --body 'are you there?'
SEND OK id=01M44X5TCN09AQDBCR8WKK8TAH to=bob cc=- at=2026-10-05T02:09:39Z bytes=14 sha256=cf97adc337983a14daab1089bf14c6ab50e658f0136517e0048407e786b6e745 login=none
[exit 0]
$ nova-bus peek --redis 127.0.0.1:6379 --as bob
PEEK OK pending=0 new=1
PEEK MESSAGE state=new id=01M44X5TCN09AQDBCR8WKK8TAH from=alice at=2026-10-05T02:09:39Z subject=hello
[exit 0]
$ nova-bus recv -h
usage: nova-bus recv [flags]
from `nova-bus help`:
  nova-bus recv [--as <me>] [--kind <k>[,<k>]] [--max <n> | --all] [--ack] [--exec <command>] [--forever --exec <command>] [--redis <addr>] [--dry-run]
  nova-bus recv --as bob --exec true
Prints one message: a line RECV OK id=<id> from=<name> to=<names> cc=<names> re=<id> [kind=<k>] at=<RFC3339>
subject=<s> (login=none when the connection has no login user), a blank line, the body; or RECV
[... trimmed: the rest of the help page ...]
[exit 0]
$ nova-bus recv --redis 127.0.0.1:6379 --as bob
RECV OK id=01M44X5TCN09AQDBCR8WKK8TAH from=alice to=bob cc=- re=- at=2026-10-05T02:09:39Z login=none subject=hello

are you there?
[exit 0]
$ nova-bus ack --redis 127.0.0.1:6379 --as bob --id 01M44X5TCN09AQDBCR8WKK8TAH
ACK OK acked=1 asked=1 login=none
ACK ID id=01M44X5TCN09AQDBCR8WKK8TAH acked=true
[exit 0]
$ nova-bus send --redis 127.0.0.1:6379 --as bob --to alice --kind ack --re 01M44X5TCN09AQDBCR8WKK8TAH --subject 'Re: hello' --body 'yes, I am here'
SEND OK id=01M44X601HK12921GDX2B8D0ZA to=alice cc=- kind=ack at=2026-10-05T02:09:45Z bytes=14 sha256=104ea0a2d32080d48b5372f8d2046780e50ec71df6ac4f6714c507a8bc03f1c3 login=none
[exit 0]
$ nova-bus recv --redis 127.0.0.1:6379 --as alice --exec cat
RECV OK id=01M44X601HK12921GDX2B8D0ZA from=bob to=alice cc=- re=01M44X5TCN09AQDBCR8WKK8TAH kind=ack at=2026-10-05T02:09:45Z login=none acked=true exec_exit=0 subject="Re: hello"
RECV OK id=01M44X601HK12921GDX2B8D0ZA from=bob to=alice cc=- re=01M44X5TCN09AQDBCR8WKK8TAH kind=ack at=2026-10-05T02:09:45Z login=none subject="Re: hello"

yes, I am here
[exit 0]
$ nova-bus peek --redis 127.0.0.1:6379 --as bob
PEEK OK pending=0 new=0
[exit 0]
$ nova-bus log --redis 127.0.0.1:6379 --bodies
LOG OK total=2
LOG MESSAGE id=01M44X5TCN09AQDBCR8WKK8TAH from=alice to=bob cc=- re=- at=2026-10-05T02:09:39Z subject=hello body="are you there?"
LOG MESSAGE id=01M44X601HK12921GDX2B8D0ZA from=bob to=alice cc=- re=01M44X5TCN09AQDBCR8WKK8TAH kind=ack at=2026-10-05T02:09:45Z subject="Re: hello" body="yes, I am here"
[exit 0]
```

## Stumbles

Each entry: what I read, what I expected, what happened, and the card to propose. Card ids are proposals for the coordinator.

### 1. The README's install line is v1.0.0 and needs the network

- Read: `README.md`, "Try one on a small example": `go install github.com/mas-bandwidth/nova-tools/cmd/nova-memory@v1.0.0`, adapted to `nova-bus`.
- Expected: a `nova-bus` binary of the release I was testing (1.2.0).
- Happened: `module lookup disabled by GOPROXY=off` (the container has no network, by the card's rule), and the line pins 1.0.0 in any case. I had to build from a source checkout instead. The README's first link, "installing one tool" (`docs/USAGE.md#installing`), is a doc, not a README, so I did not follow it; a stranger would.
- card: `readme-install-current-release`
- PATHS: README.md
- Task: the install line names the current release (or says how to build from a checkout), and says what an offline machine needs.

### 2. A build from a checkout needs a module cache the container does not have

- Read: nothing but the failure, then `go env GOMODCACHE` inside the container (`/gomodcache`, `GOPROXY=off`).
- Expected: `go install ./cmd/nova-bus` builds in the functional image.
- Happened: `module lookup disabled by GOPROXY=off` for `github.com/redis/go-redis/v9`. The image expects a module cache mounted at `/gomodcache`; nothing I was allowed to read says so. I restarted the container with the bench's module cache mounted read-only there. This is a bench fact, not a README fact, and I did not read `infra/functional-image/README.md` for it.
- card: `functional-image-modcache-mount`
- PATHS: infra/functional-image/README.md
- Task: say in the image README that a no-network run mounts a populated module cache at `/gomodcache`, with the `podman run` line.

### 3. `nova-bus version` says `devel`

- Read: `nova-bus version`.
- Expected: a release number to put in this record.
- Happened: `nova-bus devel linux/amd64 go1.26.6`. A source build cannot say which release it is.
- card: `version-source-build-names-commit`
- PATHS: internal/buildinfo
- Task: a source build prints the module version or commit from build info instead of `devel` where it can.

### 4. No recipe for the throwaway Redis

- Read: the README row ("a separate running Redis instance"), `nova-bus help` ("first run: a Redis naming ada and bob at --redis; loopback or tailnet only").
- Expected: the command that starts such a Redis.
- Happened: neither says how to start one. I used `redis-server --port 6379 --bind 127.0.0.1 --save "" --appendonly no --dir /work` from my own knowledge, which is a guess a stranger without Redis experience cannot make.
- card: `bus-first-run-redis-recipe`
- PATHS: README.md
- Task: the nova-bus row or first-run section gives one `redis-server` line for a loopback throwaway and says how to stop it.

### 5. The first send is refused, and getting names took nine commands over four refusals

- Read: the README row ("a separate running Redis instance whose nova-config rows name the sender and the recipient"), `nova-bus help`, then each refusal's text, `nova-config help`.
- Expected: the README's `send` example, with alice and bob, to work or to say in one place what to do.
- Happened: `SEND REFUSED: alice is no known name ... add one with nova-config friend add alice --slots 1 --tiers flash --as <you>, then nova-config apply`. That line is right but incomplete for a trial: it has no `--file`, and without a PostgreSQL the only way is `--file try.json` plus `migrate`, which I found in `nova-config help`. Then `apply` refused for unset fleet endpoints (`redis_port`, `pg_dsn`), so I invented a `pg_dsn` for a Postgres that does not exist in the run; then refused again for no coordinator machine, so I invented a machine row `m1` with a made-up `--user` and `--seat` and set it as coordinator. Only then did `nova-bus names` show alice and bob. The trial needed none of those rows for itself.
- card: `bus-first-run-names-recipe`
- PATHS: README.md
- Task: the nova-bus section lists the exact commands that give a throwaway Redis two names, tested by the docs tests.
- card: `config-apply-friends-without-fleet-rows`
- PATHS: cmd/nova-config
- Task: let a trial seed friend names into Redis (apply --kind friend, or one verb) without fleet endpoints, a coordinator machine or a Postgres DSN.

### 6. A refused `apply` printed `APPLY ADD` lines before it refused

- Read: the output of `nova-config apply`, third attempt.
- Expected: a refusal writes nothing, or says what it already wrote.
- Happened: it printed `APPLY ADD kind=friend name=alice` and then `REFUSED`. `nova-bus names` afterwards still said `count=0`, so nothing had been written, and the ADD line was a plan, not a deed. I did not know that until I ran `names`.
- card: `config-apply-refusal-output-plan-not-deed`
- PATHS: cmd/nova-config
- Task: when apply refuses, print no ADD/SET lines as done, or say `nothing written` on the refusal line.

### 7. `recv --exec cat` seems to deliver twice

- Read: `nova-bus recv -h`: "runs the command with that same text on its stdin".
- Expected: one printed message.
- Happened: two `RECV OK` lines for one id, the first from the verb (with `acked=true exec_exit=0`), the second the text `cat` echoed. The help describes it, but the output cannot be told from a duplicate delivery without the help.
- card: `bus-recv-exec-output-labels-command-stdout`
- PATHS: cmd/nova-bus
- Task: mark the verb's own line when `--exec` is set, or document an `--exec cat` example showing both lines.

## Verdict

Could a stranger do it: no. From the README and the tools' help alone, a stranger cannot get from "install" to two names exchanging a message: the install line does not run, there is no recipe for the Redis, and names need nine nova-config commands whose order only the refusals reveal. The exchange itself (`send`, `peek`, `recv`, `ack`, a reply with `--re` and `--kind ack`, `log --bodies`) went through first time once the names existed, and its help was clear. Stumbles 1, 2 and 4 needed my own knowledge of Go and Redis; 5 and 6 were solved by following the refusal text.

Minutes taken: about 1 by the container's clock, from the restart at 02:08:57 UTC to the last `ack` at 02:09:45 UTC. That counts only commands, not the time I spent reading the README and help, so it understates what a human would need. The earlier build failures (stumbles 1 and 2) ran in the first container, before the restart, and are not in that figure.
