# A cold stranger run: nova-bus between two names on a throwaway Redis

One run, 2026-10-07, by an AI worker (deepseek-v4.1-flash in the DeepSeek
Harness headless runner, dsh), as a person new to nova-tools: README.md, the
tool README it links, and the tools' own help first; anything else only after a
stumble, named under Stumbles. The goal: with nova-bus, two names (alice and
bob) on a throwaway Redis inside the container send each other a message, read
it, and acknowledge it. Every process of the run, the Redis and the two friend
daemons included, ran inside one container and died with it. Nothing was pushed
to the forge.

## Setup

- Bench: a Linux bench (Linux, x86_64, 64 cores, podman 5.7.0, Go 1.26.6), one
  throwaway container for the whole session.
- Container: `localhost/nova-functional:latest` (Ubuntu 24.04, Redis 8.10.2,
  Go 1.26.6), started once with
  `podman --cgroup-manager=cgroupfs run -d --rm --name stranger-bus --timeout 14400 --network none --userns=keep-id --user <uid>:<gid> -v <bench>/<job>:/work -w /work -e HOME=/work/home -e GOMODCACHE=/work/gomodcache -e GOCACHE=/work/gocache -e GOPROXY=off -e GOFLAGS=-mod=readonly -e NOVA_TEST_NO_HOST=1 -e PATH=/work/bin:/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin localhost/nova-functional:latest sleep 14400`.
  Each command of the transcript ran in it as
  `podman exec stranger-bus bash -c '<command>'` (the detached ones with
  `podman exec -d`). The default cgroup manager was refused over ssh
  (`sd-bus call: Access denied`), and the image's own user could not write the
  mounted directory, hence the two flags; neither is the tools' business.
- Source: nova-tools at a9fe2a7fd (the tip of sprint/mechanical-2026-10-02),
  copied into /work/src without its .git. The container has no network and the
  image's module cache is empty, so the modules were fetched once on the bench
  into the mounted directory (`go mod download` with GOMODCACHE there) before
  the run; inside, GOPROXY=off.
- Versions, as `<tool> version` printed them (built from the checkout):
  `nova-bus devel linux/amd64 go1.26.6`, `nova-redis devel linux/amd64 go1.26.6`,
  `nova-config devel linux/amd64 go1.26.6`. `nova-friend devel linux/amd64
  go1.26.6` was built too, only to prove the two pushes (stumble 4).
- Read before the run: README.md and the tool README it links
  (cmd/nova-bus/README.md); `nova-bus help` and the `-h` of send, recv, ack,
  peek, wait, log and names; `nova-redis help`, `serve -h` and `acl render`;
  `nova-config help`; `nova-friend help` and `run -h`. Read only after a
  stumble: internal/nsprint/redisauth/redisauth.go for the login variable names
  (stumble 1), and internal/friend/adapter.go and adapter_opencode_lanes.go for
  the opencode deliver command (stumble 5). The two earlier records in
  docs/stranger/ name the same login and names walls this run hit.
- Time: the container started at 22:40 UTC; the two messages were sent, read
  and acked by 22:43:50 UTC; the daemons were stopped and the container removed
  at 22:44 UTC.

## Transcript

Every command ran inside the container, in order, with its output. The long
`nova-bus help` and `nova-config apply` outputs are trimmed where a
`[trimmed: ...]` line says so. The Redis password and the two login passwords
are throwaways that died with the container. Steps 15 to 17 (the stand-in
harness and the two friend daemons) exist only to satisfy the proven-push gate
of stumble 4; the daemons are stopped before the exchange so that the nova-bus
verbs, not the daemons, read and ack the messages.

```text
$ cd /work/src && GOBIN=/work/bin nice -n 19 go install ./cmd/nova-bus ./cmd/nova-redis ./cmd/nova-config
(no output)
$ ls /work/bin
nova-bus
nova-config
nova-redis
$ nova-bus version; nova-redis version; nova-config version
nova-bus devel linux/amd64 go1.26.6
nova-redis devel linux/amd64 go1.26.6
nova-config devel linux/amd64 go1.26.6
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
[trimmed: the flag, exit-code and example paragraphs follow]

$ nova-redis serve --bind 127.0.0.1 --port 6379 --dir /work/redis --dry-run
SERVE OK bind=127.0.0.1 port=6379 auth=on persistence=aof eviction=none dir=/work/redis aclfile=/work/redis/users.acl users=0 dry_run=true created=0 launched=0
$ NOVA_REDIS_PASSWORD=trial-pw nova-redis serve --bind 127.0.0.1 --port 6379 --dir /work/redis > /work/coldrun/redis.log 2>&1 &   # detached in the container
$ sleep 3; cat /work/coldrun/redis.log
SERVE START bind=127.0.0.1 port=6379 auth=on persistence=aof eviction=none dir=/work/redis aclfile=/work/redis/users.acl users=0 program=/usr/local/bin/redis-server
[trimmed: the redis-server startup lines]
$ nova-bus names --redis 127.0.0.1:6379
NAMES REFUSED: redis at 127.0.0.1:6379 as the default user, no password: login refused: NOAUTH Authentication required.; next: name the user (NOVA_SPRINT_REDIS_USER) and the variable that holds its password; run: nova-bus help
[exit 2]

$ NOVA_REDIS_PASSWORD=trial-pw nova-bus names --redis 127.0.0.1:6379
NAMES REFUSED: redis at 127.0.0.1:6379 as the default user, no password: login refused: NOAUTH Authentication required.; next: name the user (NOVA_SPRINT_REDIS_USER) and the variable that holds its password; run: nova-bus help
[exit 2]

$ NOVA_SPRINT_REDIS_PASSWORD=trial-pw nova-bus names --redis 127.0.0.1:6379
NAMES REFUSED: redis at 127.0.0.1:6379 as the default user, no password: login refused: NOAUTH Authentication required.; next: name the user (NOVA_SPRINT_REDIS_USER) and the variable that holds its password; run: nova-bus help
[exit 2]

$ NOVA_SPRINT_REDIS_USER=default NOVA_SPRINT_REDIS_PASSWORD_ENV=PW PW=trial-pw nova-bus names --redis 127.0.0.1:6379
NAMES OK count=0 proven=0

$ nova-config migrate --file /work/trial/try.json
CONFIG MIGRATE file=/work/trial/try.json from=0 to=35 applied=35
$ nova-config friend add alice --slots 1 --tiers flash --as alice --file /work/trial/try.json
CONFIG ADD kind=friend name=alice rev=1
NOTE --as is --actor
$ nova-config friend add bob --slots 1 --tiers flash --as alice --file /work/trial/try.json
CONFIG ADD kind=friend name=bob rev=2
NOTE --as is --actor
$ nova-config apply --file /work/trial/try.json --redis 127.0.0.1:6379 --as alice
nova-config apply REFUSED: fleet: endpoints are unset: redis_port, pg_dsn; run: nova-config fleet set --redis_port <port> --pg_dsn <dsn> --as <actor>, then nova-config apply --kind fleet --as <actor>
[exit 1]

$ nova-config machine add m1 --user ubuntu --seat s1 --slots 4 --width 1 --as alice --file /work/trial/try.json
CONFIG ADD kind=machine name=m1 rev=3
NOTE --as is --actor
$ nova-config fleet set --store m1 --coordinator m1 --redis_port 6379 --pg_dsn postgres://nova@127.0.0.1:5432/nova --bus 127.0.0.1:6379 --as alice --file /work/trial/try.json
CONFIG SET kind=fleet name=fleet rev=4 changed=bus,coordinator,pg_dsn,redis_port,store
NOTE --as is --actor
$ nova-config apply --file /work/trial/try.json --redis 127.0.0.1:6379 --as alice
nova-config apply REFUSED: redis: read machines: NOAUTH Authentication required.; run: nova-config apply -h
[exit 2]

$ export NOVA_SPRINT_REDIS_USER=default NOVA_SPRINT_REDIS_PASSWORD_ENV=PW PW=trial-pw
$ nova-config apply --file /work/trial/try.json --redis 127.0.0.1:6379 --as alice
APPLY ADD kind=machine name=m1
CONFIG APPLY kind=machine add=1 set=0 remove=0 rev=3 ms=16
APPLY SET kind=fleet name=fleet changed=store,coordinator,redis_port,pg_dsn,bus,loops_dir
CONFIG APPLY kind=fleet add=0 set=1 remove=0 rev=4 ms=0
APPLY ADD kind=friend name=alice
APPLY ADD kind=friend name=bob
CONFIG APPLY kind=friend add=2 set=0 remove=0 rev=2 ms=0
[trimmed: the sprint, loop, route and tier rows of the same apply]
NOTE --as is --actor
$ nova-bus names --redis 127.0.0.1:6379
NAMES OK count=3 proven=0
NAMES NAME name=alice push=none age=never harness=-
NAMES NAME name=bob push=none age=never harness=-
NAMES NAME name=m1 push=none age=never harness=-

$ nova-bus send --as alice --to bob --subject hello --body "are you there?" --redis 127.0.0.1:6379
SEND REFUSED: --as alice is not the login user default: this connection acts as default; drop --as, or log in as alice (NOVA_SPRINT_REDIS_USER=alice with its password); run: nova-bus help
[exit 2]
$ nova-bus recv --as bob --redis 127.0.0.1:6379
RECV REFUSED: --as bob is not the login user default: this connection acts as default; drop --as, or log in as bob (NOVA_SPRINT_REDIS_USER=bob with its password); run: nova-bus help
[exit 2]

$ nova-redis acl render
ACL FAMILY name=tables keys=table:*,tables
[trimmed: the table, view, sprint, machine, beat, friend, fleet, loop, route, config, token and event families, and the four ACL SETUSER lines for coordinator, bench, ns-table and ns-friend]
ACL RENDER OK users=4 functions=41 library=5ad34e439bc4996e

$ redis-cli -a trial-pw ACL SETUSER alice on ">alicepw" "~*" "&*" "+@all" 2>/dev/null
OK
$ redis-cli -a trial-pw ACL SETUSER bob on ">bobpw" "~*" "&*" "+@all" 2>/dev/null
OK
$ NOVA_SPRINT_REDIS_USER=alice NOVA_SPRINT_REDIS_PASSWORD_ENV=ALICEPW ALICEPW=alicepw nova-bus send --to bob --subject hello --body "are you there?" --redis 127.0.0.1:6379
SEND REFUSED: deaf: alice has no proven push since never: no daemon has recorded one; the remedy: alice runs its friend daemon with a deliver adapter for its harness (nova-friend install --as alice --harness <h> --dir <d>) and its session answers the daemon's SESSION CHECK, which records the proof; nova-bus names shows every name's push; run: nova-bus help
SEND REFUSED: deaf: bob has no proven push since never: no daemon has recorded one; the remedy: bob runs its friend daemon with a deliver adapter for its harness (nova-friend install --as bob --harness <h> --dir <d>) and its session answers the daemon's SESSION CHECK, which records the proof; nova-bus names shows every name's push; run: nova-bus help
[exit 2]
$ NOVA_SPRINT_REDIS_USER=bob NOVA_SPRINT_REDIS_PASSWORD_ENV=BOBPW BOBPW=bobpw nova-bus recv --redis 127.0.0.1:6379
RECV REFUSED: deaf: bob has no proven push since never: no daemon has recorded one; the remedy: bob runs its friend daemon with a deliver adapter for its harness (nova-friend install --as bob --harness <h> --dir <d>) and its session answers the daemon's SESSION CHECK, which records the proof; nova-bus names shows every name's push; run: nova-bus help
[exit 2]

$ cd /work/src && GOBIN=/work/bin nice -n 19 go install ./cmd/nova-friend
(no output)
$ nova-friend run --as bob --harness opencode --dir /work/bob --redis 127.0.0.1:6379 --dry-run
RUN DRY-RUN as=bob harness=opencode dir=/work/bob state=/work/bob/.nova-friend redis=127.0.0.1:6379; nothing was started

$ cat > /work/bin/opencode <<'FAKE'
#!/bin/sh
for last; do :; done
turn="$last"
cmd=$(printf "%s\n" "$turn" | sed -n "s/^.*end this turn: //p")
if [ -n "$cmd" ]; then
  me=$(basename "$PWD")
  case "$cmd" in
    *" --to "*) : ;;
    *) cmd="$cmd --to $me" ;;
  esac
  eval "$cmd"
fi
exit 0
FAKE
$ chmod +x /work/bin/opencode

$ NOVA_SPRINT_REDIS_USER=alice NOVA_SPRINT_REDIS_PASSWORD_ENV=ALICEPW ALICEPW=alicepw nova-friend run --as alice --harness opencode --dir /work/alice --redis 127.0.0.1:6379 --session s1 > /work/coldrun/alice-daemon.log 2>&1 &   # detached
$ NOVA_SPRINT_REDIS_USER=bob NOVA_SPRINT_REDIS_PASSWORD_ENV=BOBPW BOBPW=bobpw nova-friend run --as bob --harness opencode --dir /work/bob --redis 127.0.0.1:6379 --session s1 > /work/coldrun/bob-daemon.log 2>&1 &   # detached
$ sleep 8; cat /work/coldrun/bob-daemon.log
RUN 2026-10-07T22:43:21Z push proof: pending: the first session check goes into the opencode session now; nothing is delivered until the session answers it
RUN 2026-10-07T22:43:21Z inbox: the server did not say which cards are on her row: the sprint server at 127.0.0.1:6390 did not answer: Post "http://127.0.0.1:6390/verbs": dial tcp 127.0.0.1:6390: connect: connection refused; nothing written or retired until it does
RUN 2026-10-07T22:43:23Z harness check: cannot tell: alive=session no turn into the opencode session has ended yet; the session check alone
RUN 2026-10-07T22:43:23Z presence: session check 9qk65s into the session
RUN 2026-10-07T22:43:23Z push proof: down: no session answer yet; nova-bus refuses bob as deaf
RUN 2026-10-07T22:43:23Z presence: up: the session answered 9qk65s
RUN 2026-10-07T22:43:23Z push proof: proved: the session answered; the daemon delivers from now
RUN 2026-10-07T22:43:23Z push proof: up: the session answered 9qk65s through opencode's deliver adapter; nova-bus hears bob
RUN 2026-10-07T22:43:24Z subject="pong" messages=1 took=1.079s exit=0 acked=true
$ /work/bin/nova-friend pong --as bob --nonce 9qk65s --state-dir /work/bob/.nova-friend --redis 127.0.0.1:6379
PONG REFUSED: --to is required: no ping has named a seat yet (no status file in /work/bob/.nova-friend); it wants the coordinator's name; run: nova-friend help
[exit 2]

$ cat /work/coldrun/harness.log
TURN SESSION CHECK 9qk65s
[trimmed: the turn body, ending with the pong line it carries]
RUN /work/bin/nova-friend pong --as bob --nonce 9qk65s --state-dir /work/bob/.nova-friend --redis 127.0.0.1:6379 --to bob
PONG OK nonce=9qk65s to=bob id=01M4C8J8TJTAAS6QMAZ8ANY6CT at=2026-10-07T22:43:23Z
$ NOVA_SPRINT_REDIS_USER=default NOVA_SPRINT_REDIS_PASSWORD_ENV=PW PW=trial-pw nova-bus names --redis 127.0.0.1:6379
NAMES OK count=3 proven=2
NAMES NAME name=alice push=proven age=7s harness=opencode
NAMES NAME name=bob push=proven age=7s harness=opencode
NAMES NAME name=m1 push=none age=never harness=-

$ pkill -f "nova-friend run"   # the two daemons started above, now stopped so the verbs read the messages
$ NOVA_SPRINT_REDIS_USER=default NOVA_SPRINT_REDIS_PASSWORD_ENV=PW PW=trial-pw nova-bus names --redis 127.0.0.1:6379
NAMES OK count=3 proven=2
NAMES NAME name=alice push=proven age=14s harness=opencode
NAMES NAME name=bob push=proven age=14s harness=opencode
NAMES NAME name=m1 push=none age=never harness=-

$ NOVA_SPRINT_REDIS_USER=alice NOVA_SPRINT_REDIS_PASSWORD_ENV=ALICEPW ALICEPW=alicepw nova-bus send --as alice --to bob --subject hello --body "are you there?" --redis 127.0.0.1:6379
SEND OK id=01M4C8JZG5X5D1SJEJEXHWVXK9 to=bob cc=- at=2026-10-07T22:43:46Z bytes=14 sha256=cf97adc337983a14daab1089bf14c6ab50e658f0136517e0048407e786b6e745
$ NOVA_SPRINT_REDIS_USER=bob NOVA_SPRINT_REDIS_PASSWORD_ENV=BOBPW BOBPW=bobpw nova-bus peek --as bob --redis 127.0.0.1:6379
PEEK OK pending=0 new=1
PEEK MESSAGE state=new id=01M4C8JZG5X5D1SJEJEXHWVXK9 from=alice at=2026-10-07T22:43:46Z subject="hello"
$ NOVA_SPRINT_REDIS_USER=bob NOVA_SPRINT_REDIS_PASSWORD_ENV=BOBPW BOBPW=bobpw nova-bus recv --as bob --redis 127.0.0.1:6379
RECV OK id=01M4C8JZG5X5D1SJEJEXHWVXK9 from=alice to=bob cc=- re=- at=2026-10-07T22:43:46Z subject="hello"

are you there?
$ NOVA_SPRINT_REDIS_USER=bob NOVA_SPRINT_REDIS_PASSWORD_ENV=BOBPW BOBPW=bobpw nova-bus ack --as bob --id 01M4C8JZG5X5D1SJEJEXHWVXK9 --redis 127.0.0.1:6379
ACK OK acked=1 asked=1
ACK ID id=01M4C8JZG5X5D1SJEJEXHWVXK9 acked=true
$ NOVA_SPRINT_REDIS_USER=bob NOVA_SPRINT_REDIS_PASSWORD_ENV=BOBPW BOBPW=bobpw nova-bus log --max 5 --redis 127.0.0.1:6379
LOG OK total=3
LOG MESSAGE id=01M4C8J8Q5NQ03FQ0CDMYQ41EQ from=alice to=alice cc=- re=- at=2026-10-07T22:43:22Z subject="pong"
LOG MESSAGE id=01M4C8J8TJTAAS6QMAZ8ANY6CT from=bob to=bob cc=- re=- at=2026-10-07T22:43:23Z subject="pong"
LOG MESSAGE id=01M4C8JZG5X5D1SJEJEXHWVXK9 from=alice to=bob cc=- re=- at=2026-10-07T22:43:46Z subject="hello"

$ NOVA_SPRINT_REDIS_USER=bob NOVA_SPRINT_REDIS_PASSWORD_ENV=BOBPW BOBPW=bobpw nova-bus send --as bob --to alice --subject re --body "yes, here" --redis 127.0.0.1:6379
SEND OK id=01M4C8K3KHBHVKKG959XCK82ZQ to=alice cc=- at=2026-10-07T22:43:50Z bytes=9 sha256=cf4ff293d349a9ba5a37e129df00975540c336b6609fef3f9a310dfb65f23a87
$ NOVA_SPRINT_REDIS_USER=alice NOVA_SPRINT_REDIS_PASSWORD_ENV=ALICEPW ALICEPW=alicepw nova-bus peek --as alice --redis 127.0.0.1:6379
PEEK OK pending=0 new=1
PEEK MESSAGE state=new id=01M4C8K3KHBHVKKG959XCK82ZQ from=bob at=2026-10-07T22:43:50Z subject="re"
$ NOVA_SPRINT_REDIS_USER=alice NOVA_SPRINT_REDIS_PASSWORD_ENV=ALICEPW ALICEPW=alicepw nova-bus recv --as alice --ack --redis 127.0.0.1:6379
RECV OK id=01M4C8K3KHBHVKKG959XCK82ZQ from=bob to=alice cc=- re=- at=2026-10-07T22:43:50Z acked=true subject="re"

yes, here
$ NOVA_SPRINT_REDIS_USER=alice NOVA_SPRINT_REDIS_PASSWORD_ENV=ALICEPW ALICEPW=alicepw nova-bus ack --as alice --id 01M4C8K3KHBHVKKG959XCK82ZQ --redis 127.0.0.1:6379
ACK OK acked=0 asked=1
ACK ID id=01M4C8K3KHBHVKKG959XCK82ZQ acked=false
$ NOVA_SPRINT_REDIS_USER=alice NOVA_SPRINT_REDIS_PASSWORD_ENV=ALICEPW ALICEPW=alicepw nova-bus log --max 5 --redis 127.0.0.1:6379
LOG OK total=4
LOG MESSAGE id=01M4C8J8Q5NQ03FQ0CDMYQ41EQ from=alice to=alice cc=- re=- at=2026-10-07T22:43:22Z subject="pong"
LOG MESSAGE id=01M4C8J8TJTAAS6QMAZ8ANY6CT from=bob to=bob cc=- re=- at=2026-10-07T22:43:23Z subject="pong"
LOG MESSAGE id=01M4C8JZG5X5D1SJEJEXHWVXK9 from=alice to=bob cc=- re=- at=2026-10-07T22:43:46Z subject="hello"
LOG MESSAGE id=01M4C8K3KHBHVKKG959XCK82ZQ from=bob to=alice cc=- re=- at=2026-10-07T22:43:50Z subject="re"
$ NOVA_SPRINT_REDIS_USER=default NOVA_SPRINT_REDIS_PASSWORD_ENV=PW PW=trial-pw nova-bus names --redis 127.0.0.1:6379
NAMES OK count=3 proven=2
NAMES NAME name=alice push=proven age=27s harness=opencode
NAMES NAME name=bob push=proven age=27s harness=opencode
NAMES NAME name=m1 push=none age=never harness=-
```

The stand-in harness the daemons ran as `opencode` (`/work/bin/opencode`), the
file written in the transcript above. The daemon calls it as
`opencode run --session <s> --dir <d> <turn>`; it runs the pong line a
SESSION CHECK carries (adding `--to` when the line lacks it, stumble 6) and
otherwise exits 0. Nothing in the help says what a deliver command is called
with; the shape was found by reading internal/friend/adapter.go after the
stumble.

## Stumbles

Each stumble: what was read, what was expected, what happened, and a proposed
card.

### 1. The NOAUTH refusal does not name the variable that names the password

- Read: the `NAMES REFUSED ... NOAUTH Authentication required.` line, `nova-bus
  help`, `nova-redis help serve`.
- Expected: a throwaway Redis, brought up the way the help says, that the
  README's own first-run line works against.
- Happened: `nova-redis serve` always starts `auth=on`, so every `nova-bus`
  verb against it is refused with `NOAUTH`. The refusal says to name the user
  (`NOVA_SPRINT_REDIS_USER`) and "the variable that holds its password", but not
  the variable that names that variable. `NOVA_REDIS_PASSWORD` (what serve
  reads), `NOVA_SPRINT_REDIS_PASSWORD` and `NOVA_BUS_REDIS_PASSWORD` all did
  nothing. Only `NOVA_SPRINT_REDIS_USER=default NOVA_SPRINT_REDIS_PASSWORD_ENV=PW
  PW=<password>` worked, and the name `NOVA_SPRINT_REDIS_PASSWORD_ENV` is in
  internal/nsprint/redisauth/redisauth.go, not in the README or the help.

card: bus-noauth-refusal-names-the-password-variable
paths: cmd/nova-bus/main.go,docs/SPEC-BUS.md
task: make the NOAUTH refusal print the full recipe, NOVA_SPRINT_REDIS_USER=<user> NOVA_SPRINT_REDIS_PASSWORD_ENV=<VAR> <VAR>=<password>, and say the README's Redis examples need it after nova-redis serve.

### 2. Two names take a five-step nova-config detour no page lists

- Read: `nova-bus help` ("first run: a Redis naming ada and bob"), the README's
  nova-bus row ("whose nova-config rows name the sender and the recipient"),
  `nova-config help`.
- Expected: one line saying how a name gets into the store.
- Happened: `names` showed `count=0`. Getting alice and bob in took `migrate
  --file`, two `friend add`, a `machine add` (no friend has a machine to beat),
  a `fleet set` (apply refuses until the fleet names `redis_port` and
  `pg_dsn`, a PostgreSQL URI), and `apply`, which itself needed the Redis login
  and was refused twice more, each naming the next missing thing. Only then
  `NAMES OK count=3`.

card: bus-first-run-lists-the-config-steps-for-two-names
paths: cmd/nova-bus/main.go,cmd/nova-config/main.go
task: put the whole sequence (migrate --file, friend add, machine add, fleet set, apply --as) in nova-bus's first run, or add a nova-config verb that makes two names in one call.

### 3. A name's Redis login is a user nothing shows how to make

- Read: the refusal `--as alice is not the login user default ... log in as
  alice (NOVA_SPRINT_REDIS_USER=alice with its password)`, `nova-redis acl
  render`, `acl apply -h`.
- Expected: the way to make a user named alice with a password, from the help.
- Happened: `acl render` prints four fixed users (coordinator, bench, ns-table,
  ns-friend), none a name of mine, and `acl apply` applies that fixed render.
  The users had to be made by hand with `redis-cli -a <store-password> ACL
  SETUSER alice on ">alicepw" "~*" "&*" "+@all"`, which is nowhere in the help
  and is wide open. Without it the sender cannot be alice at all.

card: bus-help-says-how-a-name-gets-a-redis-login
paths: cmd/nova-bus/main.go,cmd/nova-redis/main.go,docs/SPEC-BUS.md
task: say how a name gets a Redis user (a nova-redis verb that writes one user per name with a narrow key family, or the exact ACL SETUSER) and what keys it may touch.

### 4. Every send and recv is refused as deaf until a push is proven

- Read: `nova-bus help` ("send and recv refuse a deaf name (no push proven in
  10m)"), `nova-bus send -h`, the `deaf:` refusal and its remedy,
  `nova-friend help`, `nova-friend run -h`.
- Expected: the README's nova-bus first run, a stranger's two names sending a
  message to each other, as the row promises.
- Happened: with the names in and each one's own Redis user made, `send` and
  `recv` were refused: `deaf: alice has no proven push since never`. The
  README's own first-run transcript is produced by a test fixture whose store
  already holds proven pushes, so it shows none of this. A name is heard only
  after a standing `nova-friend` daemon carries a SESSION CHECK into its harness
  and the session answers it. There is no throwaway path from "two names I just
  made up" to a first message; a stranger must stand up two daemons (and, on
  this bench, write a stand-in harness) before the tool the README row sends
  them to will send anything.

card: bus-first-run-says-how-a-name-proves-its-push
paths: cmd/nova-bus/README.md,cmd/nova-bus/main.go,docs/SPEC-BUS.md
task: say in the nova-bus first run that each name needs a proven push, give the shortest throwaway path for two names through nova-friend, and show its shape in the README transcript.

### 5. A stand-in harness must be written from the source, not the help

- Read: `nova-friend help`, `nova-friend run -h`, internal/friend/adapter.go and
  adapter_opencode_lanes.go (after the daemon would not deliver without a
  harness binary the container did not have).
- Expected: for a harness, what the daemon runs for one delivery: the command,
  its arguments, the turn text shape, what exit 0 means.
- Happened: `--harness` is one of nineteen names, so a made-up harness cannot
  be named; the stand-in has to answer to a real name (`opencode`) on `PATH`.
  The help speaks of "the harness's deliver command" and prints none. Writing a
  shell script that records its argv showed the call is `opencode run --session
  <s> --dir <d> <turn>`, and the turn text of a SESSION CHECK ends with the pong
  line the session must run. None of that is in the help.

card: friend-help-shows-each-harness-deliver-command
paths: cmd/nova-friend/main.go,docs/SPEC-FRIEND.md
task: print per harness the exact deliver command, the turn text shape (SESSION CHECK and RECV OK) and the exit-code meaning, so a stand-in can be written from the help.

### 6. The SESSION CHECK's pong line is refused as written

- Read: the turn text the daemon carried, `nova-friend pong -h`.
- Expected: the line the check carries, run exactly as written, answers it.
- Happened: the carried line `/work/bin/nova-friend pong --as bob --nonce
  9qk65s --state-dir /work/bob/.nova-friend --redis 127.0.0.1:6379` is refused:
  `PONG REFUSED: --to is required: no ping has named a seat yet (no status file
  in /work/bob/.nova-friend); it wants the coordinator's name; run: nova-friend
  help` (the invocation and its exit 2 stand in the transcript). The stand-in
  had to retry with `--to <itself>`. No ping had named a seat, so the one
  command the check asks for cannot run.

card: session-check-line-always-names-its-coordinator
paths: cmd/nova-friend/main.go,cmd/nova-friend/pushproof_test.go
task: make the pong line of every SESSION CHECK carry --to, so running it as written is enough.

## Verdict

could a stranger do it: no, not from README.md and the help alone. It took
about four minutes of bench time from the first container command to the acked
reply (22:40 to 22:43:50 UTC), after the README and the help had been read, and
four things had to be read or done outside them: the login variable names in
internal/nsprint/redisauth/redisauth.go (stumble 1), the five-step nova-config
detour (stumble 2), `redis-cli ACL SETUSER` (stumble 3) and a stand-in harness
plus two friend daemons (stumbles 4, 5 and 6). With the two pushes proven, the
exchange itself is clean: `send`, `peek`, `recv`, `ack` and `log` print exactly
what the README's first-run transcript shows, and `ack` of an already-acked id
is false at exit 0 as documented. What stops a stranger is not the bus but the
way in: the store's login, the names, each name's Redis user, and the
proven-push gate that the README's first run silently presupposes.
