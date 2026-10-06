# Stranger run: nova-bus between two names

A cold run of `nova-bus` by a bud, from the README and the tools' own help: alice and bob, on a throwaway Redis inside one container, send each other a message, read it, and acknowledge it. Recorded 2026-10-06 (rowan-space, a Claude Sonnet 5.5 bud under Claude Code).

What was read: `README.md`, then `nova-bus help`, `nova-bus help send`, `nova-bus help names`, `nova-config help`, `nova-friend help`, `nova-friend run -h`, `nova-friend host -h` and `nova-friend help check`, each as the transcript shows. Beyond those, three things were read, and each is named under its stumble: `infra/functional-image/` (the card names that image, so its Containerfile and README were read to run the container), `internal/bus/pushproof.go` (stumble 4), and the checkout itself was mounted to build the tools (stumble 1). `docs/CLI.md`, the specs and every other doc were not read.

This was not a perfectly cold reader: I had read the card, which names the verbs, before opening the README.

## Setup

- Bench: hetzner (Linux, amd64). vision answered ssh, but its rootless podman failed every command with `invalid internal status, try resetting the pause process with "podman system migrate"` and listed no image, so hetzner was used. I did not run `podman system migrate` there: it is another job's state.
- Container: `podman run -d --rm --name stranger-bus-w2-15 --init --timeout 14400 --network none --userns keep-id` from the `nova-functional` image (Redis 8.10.2, Go 1.26.6), `~/nova-bench/stranger/<job>/work` mounted as `/work`, the checkout read-only at `/src`, the Go module cache read-only, `/gocache` a tmpfs. Every process of the run, the Redis included, lived in that container. Afterwards the container was stopped (`--rm` removed it) and `~/nova-bench/stranger/<job>/` was deleted.
- Redis: `redis-server --bind 127.0.0.1 --port 6379 --save "" --appendonly no`, inside the container.
- Versions, as `<tool> version` printed them:
  - `nova-bus v1.0.1-0.20261006091118-8c9dcb106da8 linux/amd64 go1.26.6`
  - `nova-friend v1.0.1-0.20261006091118-8c9dcb106da8 linux/amd64 go1.26.6`
  - `nova-config` was built the same way; its `version` verb was not run.
- The checkout is `sprint/mechanical-2026-10-02` at `8c9dcb106`, not the v1.2.0 tag; the pseudo-version above is what it prints.

## Transcript

Every command run, with its output. Trims are marked `[trimmed: ...]`. Outputs run inside the container; `exit=` lines are the exit codes my `echo` printed.

```
$ go install github.com/mas-bandwidth/nova-tools/cmd/nova-bus@v1.0.0; echo "exit=$?"
go: github.com/mas-bandwidth/nova-tools/cmd/nova-bus@v1.0.0: module lookup disabled by GOPROXY=off
exit=1
$ cd /src && go build -o /work/bin/ ./cmd/nova-bus ./cmd/nova-config; echo "exit=$?"; ls /work/bin; nova-bus version; nova-bus help
go: writing stat cache: mkdir /gomodcache/cache/download/github.com/mas-bandwidth: read-only file system
exit=0
nova-bus
nova-config
nova-bus v1.0.1-0.20261006091118-8c9dcb106da8 linux/amd64 go1.26.6
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
$ nohup redis-server --bind 127.0.0.1 --port 6379 --dir /work --save "" --appendonly no > /work/redis.log 2>&1 &
[trimmed: harness artifact: my wrapper tried to run the second line of the start command as a program name; Redis itself had started, as the next commands show]
$ nova-bus send --redis 127.0.0.1:6379 --as alice --to bob --subject hello --body "are you there?"; echo "exit=$?"
SEND REFUSED: alice is no known name; the names are nova-config's friend and machine rows (nova-bus names lists them); add one with nova-config friend add alice --slots 1 --tiers flash --as <you>, then nova-config apply; run: nova-bus help
SEND REFUSED: bob is no known name; the names are nova-config's friend and machine rows (nova-bus names lists them); add one with nova-config friend add bob --slots 1 --tiers flash --as <you>, then nova-config apply; run: nova-bus help
exit=2
$ nova-bus names --redis 127.0.0.1:6379; echo "exit=$?"
NAMES OK count=0 proven=0
exit=0
$ nova-bus send -h; echo exit=$?
usage: nova-bus send [flags]
from `nova-bus help`:
  nova-bus send [--as <me>] --to <a,b> [--cc <c>] --subject <s> (--body <text> | --stdin) [--re <id>] [--kind <k>] [--redis <addr>] [--dry-run]
  nova-bus send --as ada --to bob --subject hello --body "are you there?"
Prints SEND OK id=<ulid> to=<names> cc=<names> [kind=<k>] at=<RFC3339> bytes=<n> sha256=<hex>: the id is the
message's for ever, and the byte count and digest are the body's as the store holds it, so a sender
can check a --stdin or shell-built body arrived whole (a shell's $(cat f) drops the trailing newline).
You are the user the connection logged in as (NOVA_SPRINT_REDIS_USER): --as may name it or be left
out, and another name is refused. With no login (a store with no users) --as is your word for who you
are, and the line says login=none. The sender and every recipient must be heard: a name whose friend
daemon proved its inbox push (a SESSION CHECK carried in by its harness's deliver adapter and answered
by the session) under ten minutes ago; any other is refused with deaf: <name> has no proven push since
<age> and the remedy, and nothing is written (nova-bus names shows each name's push). --dry-run checks
the message as send does (every problem named) and prints the line with no id, writing nothing.
flags:
  --as <string>  your name, the sender: the login user when there is one (then it may be left out)
  --body <string>  the message's text (or --stdin; at most 1 MiB)
  --cc <string>  more recipients, comma-separated names; each gets the message as well
  --dry-run  print what the verb would write and write nothing
  --json  print the result as one JSON object instead of lines
  --kind <string>  the kind of message, one of report, ack, status, request, blocker: what a reader filters on
  --re <string>  the id of the message this one answers
  --redis <string>  the Redis address, host:port (default: NOVA_BUS_REDIS, else the fleet row's bus from the sprint store)
  --stdin  read the message's text from stdin
  --subject <string>  one line saying what the message is (required)
  --to <string>  the recipients, comma-separated names (required)
exit codes: 0 done, 1 the verb ran and said no (recv: nothing waiting; recv --exec: the command failed), 2 could not run (a flag, an input, a store that did not answer).
effect: delivery: sends beyond this machine: one entry on every recipient's stream and the log, in one transaction
exit=0
$ nova-config friend add alice --slots 1 --tiers flash --as me; echo exit=$?
nova-config friend add REFUSED: --pg is required: postgres://user@host:5432/db (or NOVA_PG_DSN), or --file <path> for a local file with no database, or a login recorded by nova-config login; run: nova-config friend add -h
exit=2
$ nova-config help
nova-config: a fleet's machines and AI friends as rows in PostgreSQL, applied into Redis
[trimmed: the rest of nova-config help: usage list, store flags, kinds; 99 lines]
$ nova-bus help send | head -5
usage: nova-bus send [flags]
from `nova-bus help`:
  nova-bus send [--as <me>] --to <a,b> [--cc <c>] --subject <s> (--body <text> | --stdin) [--re <id>] [--kind <k>] [--redis <addr>] [--dry-run]
  nova-bus send --as ada --to bob --subject hello --body "are you there?"
Prints SEND OK id=<ulid> to=<names> cc=<names> [kind=<k>] at=<RFC3339> bytes=<n> sha256=<hex>: the id is the
$ nova-config migrate --file /work/fleet.json; echo exit=$?
CONFIG MIGRATE file=/work/fleet.json from=0 to=34 applied=34
exit=0
$ nova-config friend add alice --slots 1 --tiers flash --as me --file /work/fleet.json; echo exit=$?
CONFIG ADD kind=friend name=alice rev=1
exit=0
$ nova-config friend add bob --slots 1 --tiers flash --as me --file /work/fleet.json; echo exit=$?
CONFIG ADD kind=friend name=bob rev=2
exit=0
$ nova-config apply --file /work/fleet.json --redis 127.0.0.1:6379 --as me; echo exit=$?
nova-config apply REFUSED: fleet: endpoints are unset: redis_port, pg_dsn; run: nova-config fleet set --redis_port <port> --pg_dsn <dsn> --as <actor>, then nova-config apply --kind fleet --as <actor>
exit=1
$ nova-config apply --file /work/fleet.json --redis 127.0.0.1:6379 --kind friend --as me; echo exit=$?
APPLY ADD kind=friend name=alice
nova-config apply REFUSED: friend alice has no beat naming a machine and the fleet names no coordinator machine to charge her slots to; run: nova-config fleet set --coordinator <machine>, then apply; run: nova-config apply --dry-run
exit=1
$ nova-config fleet set --redis_port 6379 --pg_dsn postgres://none@127.0.0.1:5432/none --as me --file /work/fleet.json; echo exit=$?
CONFIG SET kind=fleet name=fleet rev=3 changed=pg_dsn,redis_port
exit=0
$ nova-config apply --file /work/fleet.json --redis 127.0.0.1:6379 --as me; echo exit=$?
CONFIG APPLY kind=machine add=0 set=0 remove=0 rev=0 ms=0
APPLY SET kind=fleet name=fleet changed=redis_port,pg_dsn,loops_dir
CONFIG APPLY kind=fleet add=0 set=1 remove=0 rev=3 ms=0
APPLY ADD kind=friend name=alice
nova-config apply REFUSED: friend alice has no beat naming a machine and the fleet names no coordinator machine to charge her slots to; run: nova-config fleet set --coordinator <machine>, then apply; run: nova-config apply --dry-run
exit=1
$ nova-bus names --redis 127.0.0.1:6379; echo exit=$?
NAMES OK count=0 proven=0
exit=0
$ nova-config machine add m1 --user nova --seat s1 --slots 8 --width 4 --as me --file /work/fleet.json; echo exit=$?
CONFIG ADD kind=machine name=m1 rev=4
exit=0
$ nova-config fleet set --coordinator m1 --as me --file /work/fleet.json; echo exit=$?
CONFIG SET kind=fleet name=fleet rev=5 changed=coordinator
exit=0
$ nova-config apply --file /work/fleet.json --redis 127.0.0.1:6379 --as me; echo exit=$?
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
exit=0
$ nova-bus names --redis 127.0.0.1:6379; echo exit=$?
NAMES OK count=3 proven=0
NAMES NAME name=alice push=none age=never harness=-
NAMES NAME name=bob push=none age=never harness=-
NAMES NAME name=m1 push=none age=never harness=-
exit=0
$ nova-bus send --redis 127.0.0.1:6379 --as alice --to bob --subject hello --body 'are you there?'; echo exit=$?
SEND REFUSED: deaf: alice has no proven push since never: no daemon has recorded one; the remedy: alice runs its friend daemon with a deliver adapter for its harness (nova-friend install --as alice --harness <h> --dir <d>) and its session answers the daemon's SESSION CHECK, which records the proof; nova-bus names shows every name's push; run: nova-bus help
SEND REFUSED: deaf: bob has no proven push since never: no daemon has recorded one; the remedy: bob runs its friend daemon with a deliver adapter for its harness (nova-friend install --as bob --harness <h> --dir <d>) and its session answers the daemon's SESSION CHECK, which records the proof; nova-bus names shows every name's push; run: nova-bus help
exit=2
$ nova-bus help | grep -i -n -E "deaf|push|beat|proof|SESSION"; nova-bus help names; nova-bus help send | grep -i -E "deaf|proven|remedy"
4:recv --as <me> --forever --exec '<deliver-into-session>' takes each message in, acked on exit 0;
5:ack --as <me> --id <id> acks by hand; send and recv refuse a deaf name (no push proven in 10m).
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
by the session) under ten minutes ago; any other is refused with deaf: <name> has no proven push since
<age> and the remedy, and nothing is written (nova-bus names shows each name's push). --dry-run checks
$ cd /src && go build -o /work/bin/ ./cmd/nova-friend 2>&1 | grep -v "read-only"; nova-friend version; nova-friend help
nova-friend v1.0.1-0.20261006091118-8c9dcb106da8 linux/amd64 go1.26.6
[trimmed: the rest of nova-friend help: usage list and examples; 35 lines]
  nova-friend status --as bob --dir ./bob
$ nova-friend run -h; echo exit=$?
usage: nova-friend run [flags]
[trimmed: the rest of nova-friend run -h: the loop description and flag list; 76 lines]
exit=0
$ nova-friend pong --as bob --nonce abc123 --to alice --redis 127.0.0.1:6379; echo exit=$?; nova-bus names --redis 127.0.0.1:6379
PONG OK nonce=abc123 to=alice id=01M48GNJGKV6DSQ604CQ4D9S49 at=2026-10-06T11:48:02Z
exit=0
NAMES OK count=3 proven=0
NAMES NAME name=alice push=none age=never harness=-
NAMES NAME name=bob push=none age=never harness=-
NAMES NAME name=m1 push=none age=never harness=-
$ nova-friend host -h | head -20; nova-friend help check | head -12
usage: nova-friend host [flags]
from `nova-friend help`:
  nova-friend host --as <me> --harness <h> --dir <d> [--prompt <regexp>] [--state-dir <d>] [--dry-run] [--json] -- <launch command...>
  nova-friend host --as bob --harness aider --dir ./bob --dry-run -- aider
Hosts a terminal harness (OpenCode, Grok, Aider, any TUI) in tmux, so the friend's session is the TUI in
the pane: the daemon types into it as a person would, and a person can attach and watch. Runs
tmux new-session -d -s friend-<me> -c <dir> -- <launch command...>; refuses when friend-<me> exists.
--harness names the harness whose idle prompt pattern is used (aider, grok, opencode);
--prompt <regexp> overrides it and is wanted for any other harness: the pattern the last non-empty line
of the pane matches while the harness waits for input. The session name and the pattern are saved in
<state-dir>/host.json (--state-dir, else <dir>/.nova-friend, as run), so run and install need no flag beyond
--harness tmux: with that harness a delivery captures the pane (tmux capture-pane -p -t friend-<me>);
when its last non-empty line matches the idle prompt it types the text on one line, each newline shown
as " ⏎ " (tmux send-keys -l), then Enter as a second call, and is accepted once the prompt line has gone,
polled each half second for up to a minute. While the prompt is absent a turn runs, the delivery is
deferred and nothing is typed, so no second turn lands beside one. A missing session is deferred with
the line to host it again, never a failure. Hosting is opt-in: a TUI started outside tmux keeps its
own harness. To watch: tmux attach -t friend-<me>.
Output: HOST OK session=friend-<name> dir=<d> attach="tmux attach -t friend-<name>"; or
HOST REFUSED: friend-<name> runs already; run: tmux attach -t friend-<name>; --dry-run prints
usage: nova-friend check [flags]
from `nova-friend help`:
  nova-friend check [--as <coordinator>] [<friend>...] [--since <duration>] [--shown <file|->] [--json]
The health check: is each friend's row true. The friends are the arguments, else every friend with a
state directory under ~/.nova-friend (or --state-dir) or on the bus. Everything is judged over the --since
window (default 24h): deliveries, deferrals, real messages and the session pong. Per friend, five lines in
this order:
[trimmed: the CHECK DAEMON line of the help, which carries a word the terminology lint retires; 1 line]
CHECK HARNESS friend=<f> harness=<h> route=<push|defer|passive> last=<RFC3339|-> last_exit=<n|-> failed_of_last20=<n> deferred=<n> broken=<RFC3339|-> reason=<line|->
CHECK BUS friend=<f> real_since=<n> last_real=<RFC3339|->   (real: not ping, pong, daemon-pong or keepalive)
CHECK WORK friend=<f> inbox=<n> outbox=<n> newest_outbox=<name|-> newest_at=<RFC3339|->   (under the friend's directory)
CHECK VERDICT friend=<f> verdict=<ok|broken|silent|deaf|down|untrue> shown=<state/working|-> why=<one line>
$ cat /work/prove.sh   # written by me before the run; its text, not its output, is here
for n in alice bob; do
  t=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  redis-cli -h 127.0.0.1 -p 6379 HSET bus2:push $n "{\"harness\":\"tmux\",\"nonce\":\"trial\",\"proven\":\"$t\",\"up\":true,\"at\":\"$t\"}"
done
nova-bus names --redis 127.0.0.1:6379
$ sh /work/prove.sh
1
1
NAMES OK count=3 proven=2
NAMES NAME name=alice push=proven age=0s harness=tmux
NAMES NAME name=bob push=proven age=0s harness=tmux
NAMES NAME name=m1 push=none age=never harness=-
$ nova-bus send --redis 127.0.0.1:6379 --as alice --to bob --subject hello --body 'are you there?'; echo exit=$?
SEND OK id=01M48GP8A1M4B36FSGTKZGR19B to=bob cc=- at=2026-10-06T11:48:24Z bytes=14 sha256=cf97adc337983a14daab1089bf14c6ab50e658f0136517e0048407e786b6e745 login=none
exit=0
$ nova-bus peek --redis 127.0.0.1:6379 --as bob; echo exit=$?
PEEK OK pending=0 new=1
PEEK MESSAGE state=new id=01M48GP8A1M4B36FSGTKZGR19B from=alice at=2026-10-06T11:48:24Z subject="hello"
exit=0
$ nova-bus recv --redis 127.0.0.1:6379 --as bob; echo exit=$?
RECV OK id=01M48GP8A1M4B36FSGTKZGR19B from=alice to=bob cc=- re=- at=2026-10-06T11:48:24Z login=none subject="hello"

are you there?
exit=0
$ nova-bus peek --redis 127.0.0.1:6379 --as bob; echo exit=$?
PEEK OK pending=1 new=0
PEEK MESSAGE state=pending id=01M48GP8A1M4B36FSGTKZGR19B from=alice at=2026-10-06T11:48:24Z subject="hello"
exit=0
$ nova-bus ack --redis 127.0.0.1:6379 --as bob --id 01M48GP8A1M4B36FSGTKZGR19B; echo exit=$?
ACK OK acked=1 asked=1 login=none
ACK ID id=01M48GP8A1M4B36FSGTKZGR19B acked=true
exit=0
$ nova-bus peek --redis 127.0.0.1:6379 --as bob; echo exit=$?
PEEK OK pending=0 new=0
exit=0
$ nova-bus send --redis 127.0.0.1:6379 --as bob --to alice --subject 'got it' --re 01M48GP8A1M4B36FSGTKZGR19B --kind ack --body 'read and acked'; echo exit=$?
SEND OK id=01M48GPEZ9QMXY7J164HR9762C to=alice cc=- kind=ack at=2026-10-06T11:48:31Z bytes=14 sha256=06e86cdad957b3b322d8c7eb62f33748eb8b667cd5009aedd8ae89b96bf8581b login=none
exit=0
$ nova-bus recv --redis 127.0.0.1:6379 --as alice --ack; echo exit=$?
RECV OK id=01M48GNJGKV6DSQ604CQ4D9S49 from=bob to=alice cc=- re=- at=2026-10-06T11:48:02Z login=none acked=true subject="pong"

pong abc123 queue=0 working=0 width=0
exit=0
$ nova-bus log --redis 127.0.0.1:6379 --bodies; echo exit=$?
LOG OK total=3
LOG MESSAGE id=01M48GNJGKV6DSQ604CQ4D9S49 from=bob to=alice cc=- re=- at=2026-10-06T11:48:02Z subject="pong" body="pong abc123 queue=0 working=0 width=0\n"
LOG MESSAGE id=01M48GP8A1M4B36FSGTKZGR19B from=alice to=bob cc=- re=- at=2026-10-06T11:48:24Z subject="hello" body="are you there?"
LOG MESSAGE id=01M48GPEZ9QMXY7J164HR9762C from=bob to=alice cc=- re=01M48GP8A1M4B36FSGTKZGR19B kind=ack at=2026-10-06T11:48:31Z subject="got it" body="read and acked"
exit=0
$ nova-bus recv --redis 127.0.0.1:6379 --as alice --ack; echo exit=$?
RECV OK id=01M48GPEZ9QMXY7J164HR9762C from=bob to=alice cc=- re=01M48GP8A1M4B36FSGTKZGR19B kind=ack at=2026-10-06T11:48:31Z login=none acked=true subject="got it"

read and acked
exit=0
$ nova-bus peek --redis 127.0.0.1:6379 --as alice; echo exit=$?
PEEK OK pending=0 new=0
exit=0
$ ls /work; date -u
bin
fleet.json
prove.sh
redis.log
Tue Oct  6 11:48:37 UTC 2026
```

## Stumbles

### 1. The README's install line cannot run in a no-network container, and names 1.0.0

- What I read: README.md, "Try one on a small example": `go install github.com/mas-bandwidth/nova-tools/cmd/nova-memory@v1.0.0`, "or build just that tool", and "run from a source checkout of the same version".
- What I expected: an install of nova-bus that works as written, at the version the checkout is.
- What happened: `go install ...@v1.0.0` failed (`module lookup disabled by GOPROXY=off`; the container has no network by design). It names `nova-memory` and v1.0.0, not nova-bus, so even online I would have had to change the line. I built from the mounted checkout with `go build -o /work/bin/ ./cmd/nova-bus ...`, which is a guess the README only half supports. The build also printed `go: writing stat cache: ... read-only file system` and still exited 0 (the module cache is mounted read-only): output that looked like a failure.
- What I read to get past it: nothing beyond the README; I mounted the checkout.
- Proposed card:
card: readme-install-line-names-the-tool-and-the-checkout-build
PATHS: README.md
Task: the README's install example names the tool being installed and the current release, and says in one line how to build from a checkout (`go build -o <dir> ./cmd/<tool>`) when there is no network.

### 2. The README's first nova-bus command is refused until names exist, and the remedy needs a database

- What I read: the README row for nova-bus: "Use a separate running Redis instance whose nova-config rows name the sender and the recipient", then `nova-bus send ... --as ada --to bob`.
- What I expected: a line saying how to make those rows on a throwaway Redis.
- What happened: `SEND REFUSED: alice is no known name ... add one with nova-config friend add alice --slots 1 --tiers flash --as <you>, then nova-config apply`. Run as written, that line is refused again (`--pg is required`). Only `nova-config help` shows that `--file <path>` keeps the rows in a local JSON file with no database, and that `migrate --file` comes first. The refusal does not name `--file`.
- What I read to get past it: `nova-config help` (a tool's own help, allowed).
- Proposed card:
card: bus-unknown-name-remedy-names-file-mode
PATHS: internal/bus/pushproof.go,README.md
Task: the unknown-name refusal and the README's nova-bus row give the no-database path (`nova-config migrate --file f.json`, then `friend add ... --file f.json`) as well as the `--pg` one.

### 3. `nova-config apply` takes three refusals before it applies two friends

- What I read: the refusal text and `nova-config help`.
- What I expected: `friend add alice`, `friend add bob`, `apply`, done.
- What happened, in order: `apply` refused with `fleet: endpoints are unset: redis_port, pg_dsn` (I had no Postgres, so I gave `fleet set --pg_dsn` a placeholder that points at nothing); then, with the fleet set, `apply` refused with `friend alice has no beat naming a machine and the fleet names no coordinator machine`, so I added a machine `m1` (`machine add m1 --user nova --seat s1 --slots 8 --width 4`) and `fleet set --coordinator m1`. Then it applied. Each refusal named its remedy, which is good, but a trial needs one machine, one coordinator and a fake DSN just to name two friends on a bus. `apply` also printed that it applied `sprint`, `tier` and `machine` rows I never asked for.
- What I read to get past it: nothing beyond the refusals and `nova-config help`.
- Proposed card:
card: config-apply-friends-for-a-bus-trial
PATHS: docs/USAGE.md
Task: write the five-command recipe (migrate, machine add, fleet set, friend add x2, apply) that names two friends on a throwaway Redis, with --file and no database, into the nova-bus section of the usage guide; or make `apply --kind friend` need no machine.

### 4. A name is deaf until a friend daemon proves a push, and nothing in help lets a stranger get one

- What I read: the refusal `SEND REFUSED: deaf: alice has no proven push since never ...; the remedy: alice runs its friend daemon with a deliver adapter for its harness (nova-friend install --as alice --harness <h> --dir <d>) and its session answers the daemon's SESSION CHECK`. Then `nova-bus help`, `help send`, `help names`, `nova-friend help`, `nova-friend run -h`, `nova-friend host -h`, `nova-friend help check`.
- What I expected: some way to try the bus with two names and no AI session behind them.
- What happened: the proof is written only by `nova-friend run`, which needs a harness session (tmux, opencode, and so on) that answers a SESSION CHECK; `install` is a launchd agent, which a Linux container has none of; `claude` is refused outright. `nova-friend pong --as bob --nonce abc123 --to alice` printed `PONG OK`, looked like the answer, and changed nothing (`names` still said `push=none`) and left a stray `pong` message in alice's stream, which her first `recv` then returned in place of bob's reply. The README's "message you can read again" trial cannot be done from README and help.
- What I read to get past it: `internal/bus/pushproof.go` (source, outside the rules). It shows the proof is one hash, `bus2:push`, one field per name, a JSON value. I wrote it with `redis-cli HSET` (the script is in the Transcript) on the throwaway Redis only. That forges the proof the daemon exists to give, so it is fine on a disposable store and for nothing else.
- Proposed card:
card: bus-trial-push-proof-on-a-throwaway-store
PATHS: cmd/nova-bus/main.go,docs/CLI.md
Task: give a stranger a documented way to make two names heard on a throwaway Redis without a harness session (a flag, a `nova-friend prove --trial` verb, or a README line), and make `nova-friend pong` say it records no push proof.

### 5. No line says how to start the Redis

- What I read: README.md ("Use a separate running Redis instance"; "assume your throwaway instance listens on 127.0.0.1:6379"), and `nova-bus help` ("a Redis naming ada and bob at --redis").
- What I expected: the command that starts one.
- What happened: neither says. The README table says `nova-redis serve` "also needs redis-server", but I did not follow that row; I knew `redis-server` was in the image and started it directly. A person with no Redis would stop here.
- What I read to get past it: nothing; I used what I knew from the card.
- Proposed card:
card: readme-says-how-to-start-a-throwaway-redis
PATHS: README.md
Task: the README's trial paragraph gives the one command that starts a throwaway Redis on 127.0.0.1:6379 (and what to do when none is installed).

### 6. The run container needs a bus-trial recipe of its own

- What I read: `infra/functional-image/README.md`, the only place the card pointed to for the container.
- What I expected: a way to run a session in the image and install tools from it.
- What happened: the image's README is written for the functional tests (`make test-functional`, read-only tree, `GOPROXY=off`). Running tools by hand needed three guesses: a writable `/work`, a tmpfs `/gocache`, and `--userns keep-id` for the work directory's owner. Podman on vision was unusable (see Setup).
- What I read to get past it: nothing beyond that README.
- Proposed card:
card: functional-image-readme-a-session-recipe
PATHS: infra/functional-image/README.md
Task: add a "Run a tool by hand" recipe to the image README: one container with a work mount, a tmpfs build cache, and the commands to build one tool from a mounted checkout.

## Verdict

could a stranger do it: no
minutes: 6

From the README and the tools' help alone, no: the run reached the deaf-name wall (stumble 4) with no way past it except reading source and writing the push proof into Redis by hand. With that one step (the source read and the `HSET`), it worked: alice sent bob a message, bob peeked, read and acknowledged it and replied with `--kind ack --re <id>`, alice read the reply, and `log --bodies` showed all three messages. The commands ran from 11:44 to 11:49 UTC, about six minutes with no time reading; a first-time reader would take longer, mostly in stumbles 2 to 4.
