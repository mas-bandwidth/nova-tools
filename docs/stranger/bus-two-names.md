# A cold stranger run: nova-bus between two names on a throwaway Redis

One run, 2026-10-08, by an AI worker (deepseek-v4.1-flash in the DeepSeek
Harness headless runner, dsh), as a person new to nova-tools: README.md, the
tool README it links, and the tools' own help first; anything else only after a
stumble, named under Stumbles. The goal: with nova-bus, two names (alice and
bob) on a throwaway Redis inside the container send each other a message, read
it, and acknowledge it. Every process of the run, the Redis and the two friend
daemons included, ran inside one container and died with it. Nothing was pushed
to the forge.

## Setup

- Bench: a Linux bench (Linux, x86_64, 64 cores, podman 5.7.0, Go 1.26.6). The
  transcript's first block is the setup run on the bench (host): the source
  copied into the mounted work directory, the modules fetched into it, and the
  one throwaway container started.
- Container: `localhost/nova-functional:latest` (Ubuntu 24.04.5, Redis 8.10.2,
  Go 1.26.6), one container for the whole session, started on the bench with
  `podman --cgroup-manager=cgroupfs run -d --rm --name stranger-bus --timeout 14400 --network none --userns=keep-id --user <uid>:<gid> -v <bench>/<job>:/work -w /work -e HOME=/work/home -e GOMODCACHE=/work/gomodcache -e GOCACHE=/work/gocache -e GOPROXY=off -e GOFLAGS=-mod=readonly -e NOVA_TEST_NO_HOST=1 -e PATH=/work/bin:/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin localhost/nova-functional:latest sleep 14400`. Each command of the second
  block ran in it as `podman exec stranger-bus bash -lc '<command>'` (the
  detached ones with `podman exec -d`). The image's own user (uid 10001) could
  not write the mounted directory, hence `--user`; that is not the tools'
  business.
- Source: nova-tools at 23864704a (the tip of sprint/mechanical-2026-10-02),
  copied into /work/src without its .git. The container has no network and the
  image's module cache is empty, so the modules were fetched once on the bench
  into the mounted directory (`go mod download` with GOMODCACHE there) before
  the run; inside, GOPROXY=off.
- Versions, as `<tool> version` printed them (built from the checkout):
  `nova-bus devel linux/amd64 go1.26.6`, `nova-redis devel linux/amd64 go1.26.6`, `nova-config devel linux/amd64 go1.26.6`. `nova-friend devel linux/amd64 go1.26.6` was built too, only to prove the two pushes (stumble 4).
- Read before the run: README.md and the tool README it links
  (cmd/nova-bus/README.md); `nova-bus help` and the `-h` of send, recv, ack,
  peek, wait, log and names; `nova-redis help`, `serve -h` and `acl -h`;
  `nova-config help`; `nova-friend help` and `run -h`. Read only after a
  stumble: pkg/nsprint/redisauth/redisauth.go for the login variable names
  (stumble 1), docs/stranger/friend-fake-harness.md after stumble 1 and
  docs/stranger/three-card-sprint.md after stumble 2, and
  pkg/friend/adapter.go and adapter_opencode_lanes.go for the opencode
  deliver command (stumble 5). The two earlier records name the same login and
  names walls this run hit.
- Time: the container started at 01:06 UTC; the two messages were sent, read
  and acked by 01:08:33 UTC; the redis and the container were removed after
  that, and the bench's scratch directory with them.

## Transcript

The transcript covers every command of the run, in order, with its output. Its
first block is the bench (host) setup, before the container; its second block
is the session inside the container, where each line ran as
`podman exec stranger-bus bash -lc '<command>'` (the detached ones with
`podman exec -d`). The long `nova-bus help`, `nova-redis acl render` and
`nova-config apply` outputs are trimmed where a `[trimmed: ...]` line says so.
The Redis password and the two login passwords are throwaways that died with
the container. Steps 15 to 17 (the stand-in harness and the two friend daemons)
exist only to satisfy the proven-push gate of stumble 4; the daemons are
stopped before the exchange so that the nova-bus verbs, not the daemons, read
and ack the messages.

On the bench (host), before the container:

```text
$ mkdir -p <bench>/<job>/{src,bin,coldrun,home}
(no output)

$ rsync -a --exclude .git <checkout>/ <bench>/<job>/src/
(no output)

$ cd <bench>/<job>/src && GOMODCACHE=<bench>/<job>/gomodcache GOFLAGS=-mod=readonly go mod download
(no output)

$ podman --cgroup-manager=cgroupfs run -d --rm --name stranger-bus --timeout 14400 --network none --userns=keep-id --user <uid>:<gid> -v <bench>/<job>:/work -w /work -e HOME=/work/home -e GOMODCACHE=/work/gomodcache -e GOCACHE=/work/gocache -e GOPROXY=off -e GOFLAGS=-mod=readonly -e NOVA_TEST_NO_HOST=1 -e PATH=/work/bin:/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin localhost/nova-functional:latest sleep 14400
f826020257a72aa4c856b8182279226f28d9a7e0c28e8d8be96dfd9d3ad8e826

$ podman ps --format '{{.Names}} {{.Status}}'
stranger-bus Up Less than a second
```

Inside the container, in order:

```text
$ cd /work/src && go install github.com/mas-bandwidth/nova-tools/cmd/nova-bus@latest
go: github.com/mas-bandwidth/nova-tools/cmd/nova-bus@latest: module lookup disabled by GOPROXY=off
[exit 1]

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

$ mkdir -p /work/trial
(no output)
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
CONFIG APPLY kind=machine add=1 set=0 remove=0 rev=3 ms=12
APPLY SET kind=fleet name=fleet changed=store,coordinator,redis_port,pg_dsn,bus,loops_dir
CONFIG APPLY kind=fleet add=0 set=1 remove=0 rev=4 ms=0
APPLY ADD kind=friend name=alice
APPLY ADD kind=friend name=bob
CONFIG APPLY kind=friend add=2 set=0 remove=0 rev=2 ms=1
[trimmed: the sprint, loop, route and tier rows of the same apply]
NOTE --as is --actor
$ nova-bus names --redis 127.0.0.1:6379
NAMES OK count=3 proven=0
NAMES NAME name=alice push=none age=never harness=-
NAMES NAME name=bob push=none age=never harness=-
NAMES NAME name=m1 push=none age=never harness=-

$ NOVA_SPRINT_REDIS_USER=default NOVA_SPRINT_REDIS_PASSWORD_ENV=PW PW=trial-pw nova-bus send --as alice --to bob --subject hello --body "are you there?" --redis 127.0.0.1:6379
SEND REFUSED: --as alice is not the login user default: this connection acts as default; drop --as, or log in as alice (NOVA_SPRINT_REDIS_USER=alice with its password); run: nova-bus help
[exit 2]
$ NOVA_SPRINT_REDIS_USER=default NOVA_SPRINT_REDIS_PASSWORD_ENV=PW PW=trial-pw nova-bus recv --as bob --redis 127.0.0.1:6379
RECV REFUSED: --as bob is not the login user default: this connection acts as default; drop --as, or log in as bob (NOVA_SPRINT_REDIS_USER=bob with its password); run: nova-bus help
[exit 2]

$ nova-redis acl render
ACL FAMILY name=tables keys=table:*,tables
[trimmed: the remaining family lines and the four ACL SETUSER lines for coordinator, bench, ns-table and ns-friend]
ACL RENDER OK users=4 functions=41 library=5ad34e439bc4996e

$ redis-cli -a trial-pw ACL SETUSER alice on ">alicepw" "~*" "&*" "+@all"
Warning: Using a password with '-a' or '-u' option on the command line interface may not be safe.
OK
$ redis-cli -a trial-pw ACL SETUSER bob on ">bobpw" "~*" "&*" "+@all"
Warning: Using a password with '-a' or '-u' option on the command line interface may not be safe.
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
$ sleep 10; cat /work/coldrun/bob-daemon.log
RUN 2026-10-08T01:08:00Z push proof: pending: the first session check goes into the opencode session now; nothing is delivered until the session answers it
RUN 2026-10-08T01:08:00Z inbox: the server did not say which cards are on her row: the sprint server at 127.0.0.1:6390 did not answer: Post "http://127.0.0.1:6390/verbs": dial tcp 127.0.0.1:6390: connect: connection refused; nothing written or retired until it does
RUN 2026-10-08T01:08:01Z harness check: cannot tell: alive=session no turn into the opencode session has ended yet; the session check alone
RUN 2026-10-08T01:08:01Z presence: session check 111iu1 into the session
RUN 2026-10-08T01:08:01Z push proof: down: no session answer yet; nova-bus refuses bob as deaf
RUN 2026-10-08T01:08:01Z presence: up: the session answered 111iu1
RUN 2026-10-08T01:08:01Z push proof: proved: the session answered; the daemon delivers from now
RUN 2026-10-08T01:08:01Z push proof: up: the session answered 111iu1 through opencode's deliver adapter; nova-bus hears bob
RUN 2026-10-08T01:08:02Z subject="pong" messages=1 took=1.02s exit=0 acked=true

$ /work/bin/nova-friend pong --as bob --nonce 111iu1 --state-dir /work/bob/.nova-friend --redis 127.0.0.1:6379
PONG REFUSED: --to is required: no ping has named a seat yet (no status file in /work/bob/.nova-friend); it wants the coordinator's name; run: nova-friend help
[exit 2]

$ cat /work/bob/.nova-friend/deliver.log
2026-10-08T01:08:00Z push proof: pending: the first session check goes into the opencode session now; nothing is delivered until the session answers it
2026-10-08T01:08:00Z inbox: the server did not say which cards are on her row: the sprint server at 127.0.0.1:6390 did not answer: Post "http://127.0.0.1:6390/verbs": dial tcp 127.0.0.1:6390: connect: connection refused; nothing written or retired until it does
2026-10-08T01:08:01Z harness check: cannot tell: alive=session no turn into the opencode session has ended yet; the session check alone
2026-10-08T01:08:01Z presence: session check 111iu1 into the session
2026-10-08T01:08:01Z push proof: down: no session answer yet; nova-bus refuses bob as deaf
2026-10-08T01:08:01Z presence: up: the session answered 111iu1
2026-10-08T01:08:01Z push proof: proved: the session answered; the daemon delivers from now
2026-10-08T01:08:01Z push proof: up: the session answered 111iu1 through opencode's deliver adapter; nova-bus hears bob
2026-10-08T01:08:02Z subject="pong" messages=1 took=1.02s exit=0 acked=true

$ pkill -f "nova-friend run"   # the two daemons started above, now stopped so the verbs read the messages
[exit 143: the pattern also matched the shell that ran it; the two daemons were stopped]
$ NOVA_SPRINT_REDIS_USER=default NOVA_SPRINT_REDIS_PASSWORD_ENV=PW PW=trial-pw nova-bus names --redis 127.0.0.1:6379
NAMES OK count=3 proven=2
NAMES NAME name=alice push=proven age=31s harness=opencode
NAMES NAME name=bob push=proven age=31s harness=opencode
NAMES NAME name=m1 push=none age=never harness=-

$ NOVA_SPRINT_REDIS_USER=alice NOVA_SPRINT_REDIS_PASSWORD_ENV=ALICEPW ALICEPW=alicepw nova-bus send --as alice --to bob --subject hello --body "are you there?" --redis 127.0.0.1:6379
SEND OK id=01M4CGW2PP6Z6XZAYTQ5V64110 to=bob cc=- at=2026-10-08T01:08:33Z bytes=14 sha256=cf97adc337983a14daab1089bf14c6ab50e658f0136517e0048407e786b6e745
$ NOVA_SPRINT_REDIS_USER=bob NOVA_SPRINT_REDIS_PASSWORD_ENV=BOBPW BOBPW=bobpw nova-bus peek --as bob --redis 127.0.0.1:6379
PEEK OK pending=0 new=1
PEEK MESSAGE state=new id=01M4CGW2PP6Z6XZAYTQ5V64110 from=alice at=2026-10-08T01:08:33Z subject="hello"
$ NOVA_SPRINT_REDIS_USER=bob NOVA_SPRINT_REDIS_PASSWORD_ENV=BOBPW BOBPW=bobpw nova-bus recv --as bob --redis 127.0.0.1:6379
RECV OK id=01M4CGW2PP6Z6XZAYTQ5V64110 from=alice to=bob cc=- re=- at=2026-10-08T01:08:33Z subject="hello"

are you there?
$ NOVA_SPRINT_REDIS_USER=bob NOVA_SPRINT_REDIS_PASSWORD_ENV=BOBPW BOBPW=bobpw nova-bus ack --as bob --id 01M4CGW2PP6Z6XZAYTQ5V64110 --redis 127.0.0.1:6379
ACK OK acked=1 asked=1
ACK ID id=01M4CGW2PP6Z6XZAYTQ5V64110 acked=true
$ NOVA_SPRINT_REDIS_USER=bob NOVA_SPRINT_REDIS_PASSWORD_ENV=BOBPW BOBPW=bobpw nova-bus log --max 5 --redis 127.0.0.1:6379
LOG OK total=3
LOG MESSAGE id=01M4CGV3GBACDGJHTFYQP7FXQK from=alice to=alice cc=- re=- at=2026-10-08T01:08:01Z subject="pong"
LOG MESSAGE id=01M4CGV3HPTKGAFB3KSK4F4F81 from=bob to=bob cc=- re=- at=2026-10-08T01:08:01Z subject="pong"
LOG MESSAGE id=01M4CGW2PP6Z6XZAYTQ5V64110 from=alice to=bob cc=- re=- at=2026-10-08T01:08:33Z subject="hello"

$ NOVA_SPRINT_REDIS_USER=bob NOVA_SPRINT_REDIS_PASSWORD_ENV=BOBPW BOBPW=bobpw nova-bus send --as bob --to alice --subject re --body "yes, here" --redis 127.0.0.1:6379
SEND OK id=01M4CGW2QZ90KY8WEA8HX8VXHZ to=alice cc=- at=2026-10-08T01:08:33Z bytes=9 sha256=cf4ff293d349a9ba5a37e129df00975540c336b6609fef3f9a310dfb65f23a87
$ NOVA_SPRINT_REDIS_USER=alice NOVA_SPRINT_REDIS_PASSWORD_ENV=ALICEPW ALICEPW=alicepw nova-bus peek --as alice --redis 127.0.0.1:6379
PEEK OK pending=0 new=1
PEEK MESSAGE state=new id=01M4CGW2QZ90KY8WEA8HX8VXHZ from=bob at=2026-10-08T01:08:33Z subject="re"
$ NOVA_SPRINT_REDIS_USER=alice NOVA_SPRINT_REDIS_PASSWORD_ENV=ALICEPW ALICEPW=alicepw nova-bus recv --as alice --ack --redis 127.0.0.1:6379
RECV OK id=01M4CGW2QZ90KY8WEA8HX8VXHZ from=bob to=alice cc=- re=- at=2026-10-08T01:08:33Z acked=true subject="re"

yes, here
$ NOVA_SPRINT_REDIS_USER=alice NOVA_SPRINT_REDIS_PASSWORD_ENV=ALICEPW ALICEPW=alicepw nova-bus ack --as alice --id 01M4CGW2QZ90KY8WEA8HX8VXHZ --redis 127.0.0.1:6379
ACK OK acked=0 asked=1
ACK ID id=01M4CGW2QZ90KY8WEA8HX8VXHZ acked=false
$ NOVA_SPRINT_REDIS_USER=alice NOVA_SPRINT_REDIS_PASSWORD_ENV=ALICEPW ALICEPW=alicepw nova-bus log --max 5 --redis 127.0.0.1:6379
LOG OK total=4
LOG MESSAGE id=01M4CGV3GBACDGJHTFYQP7FXQK from=alice to=alice cc=- re=- at=2026-10-08T01:08:01Z subject="pong"
LOG MESSAGE id=01M4CGV3HPTKGAFB3KSK4F4F81 from=bob to=bob cc=- re=- at=2026-10-08T01:08:01Z subject="pong"
LOG MESSAGE id=01M4CGW2PP6Z6XZAYTQ5V64110 from=alice to=bob cc=- re=- at=2026-10-08T01:08:33Z subject="hello"
LOG MESSAGE id=01M4CGW2QZ90KY8WEA8HX8VXHZ from=bob to=alice cc=- re=- at=2026-10-08T01:08:33Z subject="re"
$ NOVA_SPRINT_REDIS_USER=default NOVA_SPRINT_REDIS_PASSWORD_ENV=PW PW=trial-pw nova-bus names --redis 127.0.0.1:6379
NAMES OK count=3 proven=2
NAMES NAME name=alice push=proven age=32s harness=opencode
NAMES NAME name=bob push=proven age=31s harness=opencode
NAMES NAME name=m1 push=none age=never harness=-

$ podman stop stranger-bus
stranger-bus
$ rm -rf <bench>/<job>
```

The stand-in harness the daemons ran as `opencode` (`/work/bin/opencode`), the
file written in the transcript above. The daemon calls it as
`opencode run --session s1 <turn>` with the friend's directory as its working
directory (no `--dir`: the adapter's own comment records that opencode's `run`
rejects it); it runs the pong line a SESSION CHECK carries (adding `--to` when
the line lacks it, stumble 6) and otherwise exits 0. Nothing in the help says
what a deliver command is called with; the shape was found by reading
pkg/friend/adapter.go after the stumble.

## Stumbles

Each stumble: what was read, what was expected, what happened, and a proposed
card.

### 1. The NOAUTH refusal does not name the variable that names the password

- Read: the `NAMES REFUSED ... NOAUTH Authentication required.` line, `nova-bus help`, `nova-redis help serve`.
- Expected: a throwaway Redis, brought up the way the help says, that the
  README's own first-run line works against.
- Happened: `nova-redis serve` always starts `auth=on`, so every `nova-bus`
  verb against it is refused with `NOAUTH`. The refusal says to name the user
  (`NOVA_SPRINT_REDIS_USER`) and "the variable that holds its password", but not
  the variable that names that variable. `NOVA_REDIS_PASSWORD` (what serve
  reads), `NOVA_SPRINT_REDIS_PASSWORD` and `NOVA_BUS_REDIS_PASSWORD` all did
  nothing. Only `NOVA_SPRINT_REDIS_USER=default NOVA_SPRINT_REDIS_PASSWORD_ENV=PW PW=<password>` worked, and the name `NOVA_SPRINT_REDIS_PASSWORD_ENV` is in
  pkg/nsprint/redisauth/redisauth.go, not in the README or the help.

card: bus-noauth-refusal-names-the-password-variable
paths: cmd/nova-bus/main.go,docs/SPEC-BUS.md
task: make the NOAUTH refusal print the full recipe, NOVA_SPRINT_REDIS_USER=<user> NOVA_SPRINT_REDIS_PASSWORD_ENV=<VAR> <VAR>=<password>, and say the README's Redis examples need it after nova-redis serve.

### 2. Two names take a five-step nova-config detour no page lists

- Read: `nova-bus help` ("first run: a Redis naming ada and bob"), the README's
  nova-bus row ("whose nova-config rows name the sender and the recipient"),
  `nova-config help`.
- Expected: one line saying how a name gets into the store.
- Happened: `names` showed `count=0`. Getting alice and bob in took a
  `mkdir -p /work/trial` (migrate will not make the file's directory), `migrate --file`, two `friend add`, a `machine add` (no friend has a machine to beat),
  a `fleet set` (apply refuses until the fleet names `redis_port` and
  `pg_dsn`, a PostgreSQL URI), and `apply`, which itself needed the Redis login
  and was refused twice more, each naming the next missing thing. Only then
  `NAMES OK count=3`.

card: bus-first-run-lists-the-config-steps-for-two-names
paths: cmd/nova-bus/main.go,cmd/nova-config/main.go
task: put the whole sequence (migrate --file, friend add, machine add, fleet set, apply --as) in nova-bus's first run, or add a nova-config verb that makes two names in one call.

### 3. A name's Redis login is a user nothing shows how to make

- Read: the refusal `--as alice is not the login user default ... log in as alice (NOVA_SPRINT_REDIS_USER=alice with its password)`, `nova-redis acl render`, `acl -h`.
- Expected: the way to make a user named alice with a password, from the help.
- Happened: `acl render` prints four fixed users (coordinator, bench, ns-table,
  ns-friend), none a name of mine, and `acl apply` applies that fixed render.
  The users had to be made by hand with `redis-cli -a <store-password> ACL SETUSER alice on ">alicepw" "~*" "&*" "+@all"`, which is nowhere in the help
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

- Read: `nova-friend help`, `nova-friend run -h`, pkg/friend/adapter.go and
  adapter_opencode_lanes.go (after the daemon would not deliver without a
  harness binary the container did not have).
- Expected: for a harness, what the daemon runs for one delivery: the command,
  its arguments, the turn text shape, what exit 0 means.
- Happened: `--harness` is one of a fixed list, so a made-up harness cannot be
  named; the stand-in has to answer to a real name (`opencode`) on `PATH`. The
  help speaks of "the harness's deliver command" and prints none. The source
  shows the call is `opencode run --session <id> <turn>` with the friend's
  directory as the working directory (there is no `--dir`; opencode's `run`
  rejects it), and the turn text of a SESSION CHECK ends with the pong line the
  session must run. None of that is in the help.

card: friend-help-shows-each-harness-deliver-command
paths: cmd/nova-friend/main.go,docs/SPEC-FRIEND.md
task: print per harness the exact deliver command, the turn text shape (SESSION CHECK and RECV OK) and the exit-code meaning, so a stand-in can be written from the help.

### 6. The SESSION CHECK's pong line is refused as written

- Read: the turn text the daemon carried, `nova-friend pong -h`.
- Expected: the line the check carries, run exactly as written, answers it.
- Happened: the carried line `/work/bin/nova-friend pong --as bob --nonce 111iu1 --state-dir /work/bob/.nova-friend --redis 127.0.0.1:6379` is refused:
  `PONG REFUSED: --to is required: no ping has named a seat yet (no status file in /work/bob/.nova-friend); it wants the coordinator's name; run: nova-friend help` (the invocation and its exit 2 stand in the transcript). The stand-in
  had to add `--to <itself>`. No ping had named a seat, so the one command the
  check asks for cannot run.

card: session-check-line-always-names-its-coordinator
paths: cmd/nova-friend/main.go,cmd/nova-friend/pushproof_test.go
task: make the pong line of every SESSION CHECK carry --to, so running it as written is enough.

## Verdict

could a stranger do it: no, not from README.md and the help alone. It took
about two minutes of bench time from the container's first command to the acked
reply (01:06 to 01:08:33 UTC), after the README and the help had been read, and
four things had to be read or done outside them: the login variable names in
pkg/nsprint/redisauth/redisauth.go (stumble 1), the five-step nova-config
detour (stumble 2), `redis-cli ACL SETUSER` (stumble 3) and a stand-in harness
plus two friend daemons (stumbles 4, 5 and 6). With the two pushes proven, the
exchange itself is clean: `send`, `peek`, `recv`, `ack` and `log` print exactly
what the README's first-run transcript shows, and `ack` of an already-acked id
is false at exit 0 as documented. What stops a stranger is not the bus but the
way in: the store's login, the names, each name's Redis user, and the
proven-push gate that the README's first run silently presupposes.
