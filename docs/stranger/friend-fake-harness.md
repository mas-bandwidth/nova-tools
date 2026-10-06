# A stranger runs nova-friend with a fake harness

A cold run of nova-tools, lens stranger: deliver one job over nova-bus to a made-up friend, whose
harness is a fake written in Go, and see the report come back. Run by rowan-space (Rowan's bud;
model Claude Sonnet 5.5, harness Claude Code) on 2026-10-06.

Provenance: this record is carried, with the stumble count corrected, from branch
`sprint/stranger-friend-fake-harness-r-r3.w1.g1.e15` (head 6cc350d4d). The attempts that carried it
(r-r3.w2 and r-r3.w3, epoch 15) did not repeat the container run; the transcript is the earlier run's.

## Setup

- Bench: vision (Linux x86_64, podman 5.7.0, go1.26.6), ONE throwaway container for the whole run:
  `podman run -d --rm --timeout 14400 --network none` from `localhost/nova-functional:latest` (the
  functional image, which carries Redis), with `~/nova-bench/stranger/sff-r3w1/` mounted as `/work`.
  The container was stopped and the directory removed at the end.
- Tools: built from a checkout of nova-tools at the card's base tip (README's `go install ...@v1.0.0`
  could not run, see the first stumble). `nova-friend version` and `nova-bus version` print
  `devel linux/amd64 go1.26.6`; `nova-config version` was not run.
- Redis: `redis-server` on 127.0.0.1:6379 inside the container, no persistence.
- Names: friends `ada` (the sender) and `bob` (the made-up friend), a machine `m1`, held in a local
  `try.json` by `nova-config --file`, applied into the Redis. No PostgreSQL.
- Harness: a Go program installed as `opencode` first on PATH. The daemon starts it as
  `opencode run --session s1 --dir <dir> <text>`; it answers the pong line in a SESSION CHECK turn,
  and for a message whose subject starts `job` it writes `<dir>/outbox/<id>/REPORT.md` and sends the
  report back on the bus. Its source is under Transcript's end.
- Read: README.md, then `<tool> help` and `<tool> <verb> -h` for nova-friend, nova-bus, nova-config.
  One stumble sent me to `docs/SPEC-FRIEND.md` (lines 436-462, 826-840, 880-895, named below).
  I did not read the source.

## Transcript

Every command run in the container, with its exact output. Trimmed only where marked. `$` lines are
typed; times are the container's UTC (the wall clock on the bench was four hours behind).

```
$ podman run -d --rm --timeout 14400 --network none --name sff-r3w1 -v ~/nova-bench/stranger/sff-r3w1:/work:Z -w /work localhost/nova-functional:latest sleep 14400
b9214559802ed7c30533eb9c703c12549eb85875ac5ce32242613b9e35dc735a
$ podman exec sff-r3w1 sh -c "which go redis-server redis-cli; go version; id -u"
/usr/local/go/bin/go
/usr/local/bin/redis-server
/usr/local/bin/redis-cli
go version go1.26.6 linux/amd64
10001
$ go install github.com/mas-bandwidth/nova-tools/cmd/nova-friend@v1.0.0
go: github.com/mas-bandwidth/nova-tools/cmd/nova-friend@v1.0.0: module lookup disabled by GOPROXY=off
$ go install ./cmd/nova-friend      # in a checkout copied into the container's work directory
go: downloading gopkg.in/yaml.v3 v3.0.1
internal/redisconn/classify.go:14:2: module lookup disabled by GOPROXY=off
internal/redisconn/open.go:17:2: module lookup disabled by GOPROXY=off
[trimmed: three more identical lines]
$ cp -r ~/go/pkg/mod ~/nova-bench/stranger/sff-r3w1/go/pkg/mod   # on the bench, outside the container
$ GOPATH=/work/go GOBIN=/work/bin go install ./cmd/nova-friend ./cmd/nova-bus ./cmd/nova-config
failed to initialize build cache at /work/gocache: mkdir /work/gocache: permission denied
$ chmod -R a+rwX ~/nova-bench/stranger/sff-r3w1   # on the bench, outside the container
$ GOPATH=/work/go GOMODCACHE=/work/go/pkg/mod GOBIN=/work/bin GOCACHE=/work/gocache go install ./cmd/nova-friend ./cmd/nova-bus ./cmd/nova-config
$ nova-friend version; nova-bus version
nova-friend devel linux/amd64 go1.26.6
nova-bus devel linux/amd64 go1.26.6
$ nova-friend help
nova-friend: what a friend runs to be part of the team: the wake loop, the beat, and the proof of life, as one daemon

how it works: one launchd agent per friend (install) runs the daemon (run): it parks on the friend's
nova-bus stream and, when the session is free, pushes every waiting message in as one turn (the
harness's deliver command), beats to the sprint server while the session answers, answers the
coordinator PING at once (daemon-pong); presence is the session's word on the bus, never a process.
state: <dir>/.nova-friend/ (--state-dir moves it), the queue: <dir>/inbox/QUEUE.json.

usage:
  nova-friend run --as <me> --harness <h> --dir <d> [--session <id>] [--server <addr>] [--width <n>] [--silent-stop <d>] [--broken-after <n>] [--coordinator <seat>] [--state-dir <d>] [--redis <addr>] [--profile <p>] [--config-dir <d>] [--deny-self <d,...>] [--wall-jobs <d,...>] [--wall-reads <d,...>] [--dry-run]
  nova-friend install --as <me> --harness <h> --dir <d> [--session <id>] [--server <addr>] [--width <n>] [--silent-stop <d>] [--broken-after <n>] [--coordinator <seat>] [--state-dir <d>] [--redis <addr>] [--config-dir <d>] [--model <provider/model>] [--secrets NAME[,NAME] --seat <seat>] [--launchd-log <file>] [--dry-run]
[trimmed: the rest of the banner: the verbs' usage lines, exit codes, example block]
$ nova-friend run -h; nova-friend help run
usage: nova-friend run [flags]
from `nova-friend help`:
[trimmed: the 143-line `nova-friend run` help, printed twice (-h and help run): flags, wall and lane paragraphs, exit codes]
$ nova-friend help harness; echo rc=$?; mkdir -p /work/ada /work/bob; nova-friend run --as bob --harness opencode --dir /work/bob --dry-run; echo rc=$?; nova-friend run --as bob --harness fake --dir /work/bob --dry-run; echo rc=$?; nova-bus help | head -60
FRIEND REFUSED: "harness" is no verb and no file; the verbs are run, install, uninstall, check, ping, ping-install, ping-uninstall, pong, wait-pong, status, serve, version, and a file is given by its path (./harness); run: nova-friend help
rc=2
RUN FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written
rc=1
RUN REFUSED: --harness "fake" is no harness; it wants one of opencode, codex, claude, antigravity, dsh, gemini, grok, copilot, cursor, amp, goose, kiro, cline, aider, roo, windsurf, zed, warp; run: nova-friend help
rc=2
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
$ nova-friend help install | head -80; nova-friend run --as bob --harness opencode --dir /work/bob --redis 127.0.0.1:6379 --dry-run -h 2>&1 | tail -3; nova-friend help status | head -30
usage: nova-friend install [flags]
[trimmed: `nova-friend help install`: settings per harness, launchd agent paragraphs, flags]
$ redis-server --port 6379 --bind 127.0.0.1 --save "" --appendonly no --daemonize yes --dir /work --logfile /work/redis.log; sleep 1; redis-cli ping; export NOVA_BUS_REDIS=127.0.0.1:6379; nova-bus names; echo rc=$?; nova-bus send --as ada --to bob --subject hello --body hi; echo rc=$?; nova-bus help send | tail -25
PONG
NAMES OK count=0 proven=0
rc=0
SEND REFUSED: ada is no known name; the names are nova-config's friend and machine rows (nova-bus names lists them); add one with nova-config friend add ada --slots 1 --tiers flash --as <you>, then nova-config apply; run: nova-bus help
SEND REFUSED: bob is no known name; the names are nova-config's friend and machine rows (nova-bus names lists them); add one with nova-config friend add bob --slots 1 --tiers flash --as <you>, then nova-config apply; run: nova-bus help
rc=2
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
$ which postgres pg_ctl initdb psql; ls /usr/lib/postgresql 2>&1; nova-config help | head -50; nova-config help friend | head -40; nova-config help apply | head -30
/usr/bin/psql
16
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
[trimmed: `nova-config help`, `help friend`, `help apply`: kinds, usage lines, store options]
$ cd /work; nova-config friend add -h | head -30; nova-config migrate --file try.json; nova-config friend add ada --slots 1 --tiers flash --as ada --file try.json; nova-config friend add bob --slots 1 --tiers flash --as ada --file try.json; nova-config apply --file try.json --redis 127.0.0.1:6379 --as ada; nova-bus names --redis 127.0.0.1:6379
usage: nova-config friend add [flags]
effect: store write: one row and its history row, in PostgreSQL or the --file; --dry-run prints the change (CONFIG DRY-RUN) and writes nothing
required: --slots --tiers (and --as); every other field takes its default
example: nova-config friend add f1 --slots 4 --tiers flash,pro --roles builder --as a1 --file try.json
flags:
  --as <name>  the name a write is recorded under in the history (env NOVA_FRIEND)
  --config_dir <text>  text: the absolute directory a claude one-shot lane runs with as CLAUDE_CONFIG_DIR, her account's login and settings; unset (the default, or --config_dir '') for any other harness; nova-friend run refuses a claude friend in one-shot mode without it
  --dry-run  print the change the write would record (CONFIG DRY-RUN, from the same checks) and write nothing; it still reads the store
  --file <path>  a local JSON file standing in for PostgreSQL, at path (migrate --file <path> makes it): the same rows, refusals and history, to try the tool with no database; never the fleet's store
  --json  print one JSON object (internal/tool's result shape) instead of the lines
  --mode <batch|one-shot>  batch|one-shot: how her daemon hands her work: batch (the default: every waiting message in one turn) or one-shot (width lanes, each its own session, handed one card per turn)
  --pg <dsn>  the PostgreSQL dsn, postgres://user@host:port/db with no password (env NOVA_PG_DSN); NOVA_PG_PASSWORD_ENV holds the name of the variable that holds the password, NOVA_PG_PASSWORD when it is unset, and never the password itself; exclusive with --file
  --roles <list>  list: comma list of builder, may-hold, reader (who coordinates is the sprint row's)
  --seat <seat>  the seat profile in seats.tsv supplying the PostgreSQL DSN and password variable name (env NOVA_SEAT); exclusive with --file
  --slots <number>  required; number: her desired slots, under the ceiling of the machine her beat reports; no machine's width
  --tiers <list>  required; list: which tiers she can do: comma list of flash, frontier, heavy, pro
  --width <number>  number: the jobs she works at once, the width nova-sprint friend sync sets on her friends row; at least 1, 8 by default
exit codes: 0 done, 1 refused (the verb ran and the store said no; migrate --dry-run:
ready=no, nothing attempted), 2 could not run (usage, or a store that did not answer);
machine self: 2 not a row, 3 unreadable
CONFIG MIGRATE file=try.json from=0 to=34 applied=34
CONFIG ADD kind=friend name=ada rev=1
CONFIG ADD kind=friend name=bob rev=2
nova-config apply REFUSED: fleet: endpoints are unset: redis_port, pg_dsn; run: nova-config fleet set --redis_port <port> --pg_dsn <dsn> --as <actor>, then nova-config apply --kind fleet --as <actor>
NAMES OK count=0 proven=0
$ cd /work; nova-config apply --kind friend --file try.json --redis 127.0.0.1:6379 --as ada; nova-bus names --redis 127.0.0.1:6379; nova-bus send --as ada --to bob --subject hello --body hi --redis 127.0.0.1:6379
APPLY ADD kind=friend name=ada
nova-config apply REFUSED: friend ada has no beat naming a machine and the fleet names no coordinator machine to charge her slots to; run: nova-config fleet set --coordinator <machine>, then apply; run: nova-config apply --dry-run
NAMES OK count=0 proven=0
SEND REFUSED: ada is no known name; the names are nova-config's friend and machine rows (nova-bus names lists them); add one with nova-config friend add ada --slots 1 --tiers flash --as <you>, then nova-config apply; run: nova-bus help
SEND REFUSED: bob is no known name; the names are nova-config's friend and machine rows (nova-bus names lists them); add one with nova-config friend add bob --slots 1 --tiers flash --as <you>, then nova-config apply; run: nova-bus help
$ cd /work; nova-config machine add -h | head -14; nova-config fleet set -h | head -14
usage: nova-config machine add [flags]
from `nova-config help`:
[trimmed: flag lists of `machine add -h` and `fleet set -h`]
$ cd /work; nova-config machine add m1 --user nova --seat s1 --slots 8 --width 4 --as ada --file try.json; nova-config fleet set --coordinator m1 --store m1 --redis_port 6379 --pg_dsn postgres://x@127.0.0.1:5432/x --as ada --file try.json; nova-config apply --file try.json --redis 127.0.0.1:6379 --as ada; nova-bus names --redis 127.0.0.1:6379
CONFIG ADD kind=machine name=m1 rev=3
CONFIG SET kind=fleet name=fleet rev=4 changed=coordinator,pg_dsn,redis_port,store
APPLY ADD kind=machine name=m1
CONFIG APPLY kind=machine add=1 set=0 remove=0 rev=3 ms=2
APPLY SET kind=fleet name=fleet changed=store,coordinator,redis_port,pg_dsn,loops_dir
CONFIG APPLY kind=fleet add=0 set=1 remove=0 rev=4 ms=0
APPLY ADD kind=friend name=ada
APPLY ADD kind=friend name=bob
CONFIG APPLY kind=friend add=2 set=0 remove=0 rev=2 ms=1
APPLY SET kind=sprint name=sprint changed=decide_bounce,decide_review
CONFIG APPLY kind=sprint add=0 set=1 remove=0 rev=0 ms=0
CONFIG APPLY kind=loop add=0 set=0 remove=0 rev=0 ms=0
CONFIG APPLY kind=route add=0 set=0 remove=0 rev=0 ms=0
APPLY ADD kind=tier name=flash
APPLY ADD kind=tier name=heavy
APPLY ADD kind=tier name=pro
CONFIG APPLY kind=tier add=3 set=0 remove=0 rev=0 ms=0
NAMES OK count=3 proven=0
NAMES NAME name=ada push=none age=never harness=-
NAMES NAME name=bob push=none age=never harness=-
NAMES NAME name=m1 push=none age=never harness=-
$ cd /work/fake && GOPATH=/work/go GOMODCACHE=/work/go/pkg/mod GOCACHE=/work/gocache go build -o /work/fakebin/opencode . && ls /work/fakebin
opencode
$ cd /work; export PATH=/work/fakebin:$PATH NOVA_BUS_REDIS=127.0.0.1:6379; (nohup nova-friend run --as bob --harness opencode --dir /work/bob --session s1 --width 1 > /work/bob-daemon.log 2>&1 &); sleep 8; cat /work/bob-daemon.log; cat /work/fake.log; nova-friend status --as bob --dir /work/bob
06:12:47 argv=["opencode" "run" "--session" "s1" "--dir" "/work/bob" "SESSION CHECK idu5ti\nThe daemon has had no bus message from this session for a while and asks whether the session is alive; only an answer from inside this session counts, and none within 5m0s puts you down. Answer now, before anything else, with one command, then end this turn: /work/bin/nova-friend pong --as bob --nonce idu5ti --state-dir /work/bob/.nova-friend --redis 127.0.0.1:6379 --to bob\n"] cwd=/work/bob
STATUS NONE: no daemon has run as bob (no status file in /work/.nova-friend/bob); run: nova-friend install --as bob --harness <h> --dir /work/bob
$ cd /work/fake && GOPATH=/work/go GOMODCACHE=/work/go/pkg/mod GOCACHE=/work/gocache go build -o /work/fakebin/opencode . && pkill nova-friend; sleep 1; > /work/fake.log; export PATH=/work/fakebin:$PATH NOVA_BUS_REDIS=127.0.0.1:6379; cd /work; (nohup nova-friend run --as bob --harness opencode --dir /work/bob --session s1 --width 1 > /work/bob-daemon.log 2>&1 &); sleep 8; cat /work/bob-daemon.log; cat /work/fake.log; nova-friend status --as bob --dir /work/bob --state-dir /work/bob/.nova-friend; nova-bus names
RUN 2026-10-06T06:13:05Z push proof: CHECK OK harness=opencode took=35.902982ms
RUN 2026-10-06T06:13:05Z inbox: the server did not say which cards are on her row: the sprint server at 127.0.0.1:6390 did not answer: Post "http://127.0.0.1:6390/verbs": dial tcp 127.0.0.1:6390: connect: connection refused; nothing written or retired until it does
RUN 2026-10-06T06:13:05Z harness check: cannot tell: opencode runs no standing process (each turn starts /work/fakebin/opencode); the session check alone
RUN 2026-10-06T06:13:05Z presence: a session check is owed; it waits for the turn under way
RUN 2026-10-06T06:13:05Z push proof: down: no session answer yet; nova-bus refuses bob as deaf
RUN 2026-10-06T06:13:05Z subject="pong" messages=1 took=13ms exit=0 acked=true
RUN 2026-10-06T06:13:06Z presence: session check unhv7w into the session
06:13:05 argv=["opencode" "run" "--session" "s1" "--dir" "/work/bob"]
--- text ---
SESSION CHECK 7pxze2
The daemon has had no bus message from this session for a while and asks whether the session is alive; only an answer from inside this session counts, and none within 5m0s puts you down. Answer now, before anything else, with one command, then end this turn: /work/bin/nova-friend pong --as bob --nonce 7pxze2 --state-dir /work/bob/.nova-friend --redis 127.0.0.1:6379 --to bob

--- end ---
pong: PONG OK nonce=7pxze2 to=bob id=01M47XG8RF8WRVXSF6V84VKPCP at=2026-10-06T06:13:05Z
 err=<nil>
06:13:05 argv=["opencode" "run" "--session" "s1" "--dir" "/work/bob"]
--- text ---
RECV OK id=01M47XG8RF8WRVXSF6V84VKPCP from=bob to=bob cc=- re=- at=2026-10-06T06:13:05Z subject="pong"

pong 7pxze2 queue=0 working=0 width=0

--- end ---
06:13:06 argv=["opencode" "run" "--session" "s1" "--dir" "/work/bob"]
--- text ---
SESSION CHECK unhv7w
The daemon has had no bus message from this session for a while and asks whether the session is alive; only an answer from inside this session counts, and none within 5m0s puts you down. Answer now, before anything else, with one command, then end this turn: /work/bin/nova-friend pong --as bob --nonce unhv7w --state-dir /work/bob/.nova-friend --redis 127.0.0.1:6379

--- end ---
pong: PONG REFUSED: --to is required: no ping has named a seat yet (no status file in /work/bob/.nova-friend); it wants the coordinator's name; run: nova-friend help
 err=exit status 2
STATUS OK daemon=up harness=opencode status_age=3s connection=connected seat=- last_ping=2026-10-06T06:13:05Z ping_age=8s challenge=quiet nonce=- last_pong=2026-10-06T06:13:05Z session_pong_age=8s daemon_pong_age=- pongs=0 queue=0 working=0 width=1 beats=0 last_beat=- delivered=1 session=ok mode=batch held=- inbox=- missing=- presence=down last_session=- status=up presence_reason="no session answer yet" why="session answer 8s" evidence="harness unknown; session answer 8s; no limit; 0 undelivered; last result 8s exit=0" harness_seen="-"
STATUS NOTE the inbox: the sprint server at 127.0.0.1:6390 did not answer: Post "http://127.0.0.1:6390/verbs": dial tcp 127.0.0.1:6390: connect: connection refused
STATUS NOTE the daemon is up and the session is not (no session answer yet): the friend is down, and no beat goes to the sprint server until the session answers a session check
STATUS NOTE the last beat failed: not beating: the session is down (no session answer yet); the daemon answering is not the session
NAMES OK count=3 proven=0
NAMES NAME name=ada push=none age=never harness=-
NAMES NAME name=bob push=down age=7s harness=opencode
NAMES NAME name=m1 push=none age=never harness=-
$ cd /work/fake && GOPATH=/work/go GOMODCACHE=/work/go/pkg/mod GOCACHE=/work/gocache go build -o /work/fakebin/opencode . && pkill nova-friend; sleep 1; > /work/fake.log; export PATH=/work/fakebin:$PATH NOVA_BUS_REDIS=127.0.0.1:6379; cd /work; for f in ada bob; do (nohup nova-friend run --as $f --harness opencode --dir /work/$f --session s1 --width 1 > /work/$f-daemon.log 2>&1 &); done; sleep 12; nova-bus names; grep -c SESSION /work/fake.log
NAMES OK count=3 proven=2
NAMES NAME name=ada push=proven age=10s harness=opencode
NAMES NAME name=bob push=proven age=9s harness=opencode
NAMES NAME name=m1 push=none age=never harness=-
4
$ cd /work/fake && GOPATH=/work/go GOMODCACHE=/work/go/pkg/mod GOCACHE=/work/gocache go build -o /work/fakebin/opencode . && echo built; export NOVA_BUS_REDIS=127.0.0.1:6379; nova-bus send --as ada --to bob --subject "job one" --body "say hello"; sleep 4; cat /work/bob/outbox/*/REPORT.md; nova-bus recv --as ada --max 5; tail -30 /work/bob-daemon.log; nova-bus log --bodies --max 6
built
SEND OK id=01M47XHF8WW1YCBM1586BTKW59 to=bob cc=- at=2026-10-06T06:13:44Z bytes=9 sha256=3cad3da3dbbe1c106b3219f6c278a565f411cee74f0848956aff65d3df4846a7 login=none
Verdict: LAND

fake harness did job 01M47XHF8WW1YCBM1586BTKW59
RECV NONE: nothing for ada
RUN 2026-10-06T06:13:23Z push proof: CHECK OK harness=opencode took=31.879906ms
RUN 2026-10-06T06:13:23Z inbox: the server did not say which cards are on her row: the sprint server at 127.0.0.1:6390 did not answer: Post "http://127.0.0.1:6390/verbs": dial tcp 127.0.0.1:6390: connect: connection refused; nothing written or retired until it does
RUN 2026-10-06T06:13:23Z harness check: cannot tell: opencode runs no standing process (each turn starts /work/fakebin/opencode); the session check alone
RUN 2026-10-06T06:13:23Z presence: a session check is owed; it waits for the turn under way
RUN 2026-10-06T06:13:23Z push proof: down: no session answer yet; nova-bus refuses bob as deaf
RUN 2026-10-06T06:13:23Z subject="pong" messages=1 took=18ms exit=0 acked=true
RUN 2026-10-06T06:13:24Z presence: session check dvbqcl into the session
RUN 2026-10-06T06:13:25Z presence: up: the session answered dvbqcl
RUN 2026-10-06T06:13:25Z push proof: up: the session answered dvbqcl through opencode's deliver adapter; nova-bus hears bob
RUN 2026-10-06T06:13:44Z subject="job one" messages=1 took=575ms exit=0 acked=true
LOG OK total=7
LOG MESSAGE id=01M47XG8RF8WRVXSF6V84VKPCP from=bob to=bob cc=- re=- at=2026-10-06T06:13:05Z subject="pong" body="pong 7pxze2 queue=0 working=0 width=0\n"
LOG MESSAGE id=01M47XGTPD2KB9B6JH04JC60VC from=bob to=bob cc=- re=- at=2026-10-06T06:13:23Z subject="pong" body="pong bh5o3d queue=0 working=0 width=0\n"
LOG MESSAGE id=01M47XGTQ8RVNR5SJXPPABVH5E from=ada to=ada cc=- re=- at=2026-10-06T06:13:23Z subject="pong" body="pong 6x9ak3 queue=0 working=0 width=0\n"
LOG MESSAGE id=01M47XGVQ8QD3X5BSAG12G2A14 from=bob to=ada cc=- re=- at=2026-10-06T06:13:24Z subject="pong" body="pong dvbqcl queue=0 working=0 width=0\n"
LOG MESSAGE id=01M47XGVQMG2T9NZPMGC0KE59D from=ada to=ada cc=- re=- at=2026-10-06T06:13:24Z subject="pong" body="pong 5hbd70 queue=0 working=0 width=0\n"
LOG MESSAGE id=01M47XHF8WW1YCBM1586BTKW59 from=ada to=bob cc=- re=- at=2026-10-06T06:13:44Z subject="job one" body="say hello"
LOG MORE kind=message shown=6 total=7 --max <n> raises the ceiling, --max 0 lists all
$ tail -3 /work/ada-daemon.log; grep -A8 "report" /work/fake.log | tail -22; nova-bus log --bodies --max 0 | tail -3; ls -R /work/bob/outbox; nova-friend status --as bob --dir /work/bob --state-dir /work/bob/.nova-friend | head -1
RUN 2026-10-06T06:13:24Z push proof: up: the session answered 5hbd70 through opencode's deliver adapter; nova-bus hears ada
RUN 2026-10-06T06:13:24Z subject="pong | pong" messages=2 took=15ms exit=0 acked=true
RUN 2026-10-06T06:13:44Z subject="report 01M47XHF8WW1YCBM1586BTKW59" messages=1 took=296ms exit=0 acked=true
report: SEND OK id=01M47XHF99PV27QWZXC3BDDKF5 to=ada cc=- kind=report at=2026-10-06T06:13:44Z bytes=63 sha256=cd5974dee55194d4fb4eda215c54aa840f7fea4d1870f82182b2a233a89b7cbf login=none
 err=<nil>
06:13:44 argv=["opencode" "run" "--session" "s1" "--dir" "/work/ada"]
--- text ---
RECV OK id=01M47XHF99PV27QWZXC3BDDKF5 from=bob to=ada cc=- re=01M47XHF8WW1YCBM1586BTKW59 at=2026-10-06T06:13:44Z subject="report 01M47XHF8WW1YCBM1586BTKW59"

Verdict: LAND

fake harness did job 01M47XHF8WW1YCBM1586BTKW59

--- end ---
LOG REFUSED: --redis is required: NOVA_BUS_REDIS is unset, and with no NOVA_SPRINT_REDIS the fleet's bus row (nova-config fleet set --bus <host:port>, then apply) cannot be read either; refusing to guess; run: nova-bus help
/work/bob/outbox:
01M47XHF8WW1YCBM1586BTKW59

/work/bob/outbox/01M47XHF8WW1YCBM1586BTKW59:
REPORT.md
```

The fake harness as it stood for the last delivery (the third version; the first only logged its argv):

```go
// fake opencode: `opencode run --session <id> --dir <dir> <text>`.
// Answers a pong line it is handed; for a delivered message with a
// subject "job ...", writes <dir>/outbox/<id>/REPORT.md and replies on the bus.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	log, _ := os.OpenFile("/work/fake.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o666)
	defer log.Close()
	text := os.Args[len(os.Args)-1]
	dir := "."
	for i, a := range os.Args {
		if a == "--dir" && i+1 < len(os.Args) {
			dir = os.Args[i+1]
		}
	}
	fmt.Fprintf(log, "%s argv=%q\n--- text ---\n%s\n--- end ---\n", time.Now().Format("15:04:05"), os.Args[:len(os.Args)-1], text)
	for _, l := range strings.Split(text, "\n") {
		if i := strings.Index(l, "/work/bin/nova-friend pong "); i >= 0 {
			f := strings.Fields(l[i:])
			if !strings.Contains(l, " --to ") {
				f = append(f, "--to", "ada")
			}
			out, err := exec.Command(f[0], f[1:]...).CombinedOutput()
			fmt.Fprintf(log, "pong: %s err=%v\n", out, err)
		}
	}
	if !strings.Contains(text, `subject="job`) {
		return
	}
	var id, from string
	for _, w := range strings.Fields(strings.SplitN(text, "\n", 2)[0]) {
		if v, ok := strings.CutPrefix(w, "id="); ok {
			id = v
		}
		if v, ok := strings.CutPrefix(w, "from="); ok {
			from = v
		}
	}
	out := filepath.Join(dir, "outbox", id)
	os.MkdirAll(out, 0o777)
	report := "Verdict: LAND\n\nfake harness did job " + id + "\n"
	os.WriteFile(filepath.Join(out, "REPORT.md"), []byte(report), 0o666)
	b, err := exec.Command("/work/bin/nova-bus", "send", "--as", filepath.Base(dir), "--to", from, "--re", id,
		"--kind", "report", "--subject", "report "+id, "--body", report).CombinedOutput()
	fmt.Fprintf(log, "report: %s err=%v\n", b, err)
}
```

Delivery as it ended: ada sent `job one` to bob; bob's daemon pushed it into the fake as one turn
(`subject="job one" messages=1 took=575ms exit=0 acked=true`); the fake wrote
`/work/bob/outbox/01M47XHF8WW1YCBM1586BTKW59/REPORT.md` and sent the report on the bus; ada's daemon
pushed it into ada's fake (`subject="report 01M47XHF8WW1YCBM1586BTKW59" ... acked=true`), and the
turn's text carried `Verdict: LAND` and the job's id.

## Stumbles

PATHS below name directories, not files: a cold run does not read the source, so a coordinator narrows them.

### The README's install line cannot run in a container with no network, and names v1.0.0

- Read: README.md, "Try one on a small example": `go install github.com/mas-bandwidth/nova-tools/cmd/nova-memory@v1.0.0`.
- Expected: a binary in the container.
- Happened: `module lookup disabled by GOPROXY=off`; then building a checkout also failed, the
  dependencies (yaml, go-redis) are not in the container; I copied the bench's module cache into the
  mount, and found the image's GOMODCACHE and GOCACHE point at directories I cannot write. The
  README pins v1.0.0 while the tools I built print `devel`, so the pin cannot be checked; no line says how to build offline.
card: stranger-readme-offline-install
- PATHS: README.md,docs/USAGE.md,internal/docs/readme_catalogue_test.go
- Task: say in the README how to build a tool with no network, and keep the pinned version equal to the release the tools print.

### `nova-friend` help never says how a harness is called, so a fake has nothing to implement

- Read: `nova-friend help`, `help run`, `help install`, `run --harness fake`.
- Expected: the help names the contract of a harness: the command the daemon starts, its arguments,
  what it must print or write.
- Happened: `--harness` takes one of eighteen fixed names; `fake` is refused, so the fake has to
  take a real name (`opencode`) and be found on PATH. Nothing says the daemon runs `opencode run
  --session <id> --dir <dir> <text>`; I learned it from `docs/SPEC-FRIEND.md` and by logging my
  fake's argv. The help speaks of "the harness's deliver command" without naming one.
card: friend-help-names-the-deliver-command
- PATHS: cmd/nova-friend/**,internal/friend/**,docs/CLI.md
- Task: in `nova-friend help run`, print each harness's deliver command line (the argv the daemon starts) and say a fake on PATH under the harness's name stands in for it.

### The report a harness must write is only described for one-shot lanes

- Read: `nova-friend help run`.
- Expected: for the job I deliver, a stated report the harness writes ("the outbox report the help
  says it must").
- Happened: the help names `REPORT.md` and `RESULT.md` only for one-shot lanes fed from
  `inbox/QUEUE.json` (which needs a sprint server). A batch delivery is just a turn of text; no
  report path or format is named. I chose `<dir>/outbox/<id>/REPORT.md` and a bus reply myself.
card: friend-help-says-what-a-batch-turn-owes
- PATHS: cmd/nova-friend/**,internal/friend/**,docs/SPEC-FRIEND.md
- Task: say in `nova-friend help run` what a batch turn owes (nothing but its exit code, or a reply) and where a lane's REPORT.md and RESULT.md go.

### The SESSION CHECK's pong line sometimes has no `--to`, and then fails when run as given

- Read: the turn text the daemon pushed (my fake's log).
- Expected: the line printed in a turn runs as it stands (`nothing to fill in`).
- Happened: the first check's line carried `--to bob` (bob's own name, so the answer went to bob);
  the second carried no `--to`, and run verbatim printed `PONG REFUSED: --to is required: no ping
  has named a seat yet`. My fake had to guess a coordinator (`ada`) and append it. The cause is
  that no ping had yet named a seat; the daemon could say `--coordinator` is wanted, and it did
  not.
card: friend-pong-line-names-a-seat
- PATHS: cmd/nova-friend/**,internal/friend/**
- Task: make the pong line in a SESSION CHECK always carry a runnable `--to` (the `--coordinator` seat, else refuse at start naming it).

### `nova-friend run --dry-run` fails with an internal message, though the help documents it

- Read: `nova-friend help run`: "`--dry-run` checks the flags and the harness and prints the daemon it would run (RUN DRY-RUN as= harness= dir= state= redis=)".
- Expected: a `RUN DRY-RUN` line.
- Happened: `RUN FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written` at exit 1, naming a Go field a stranger cannot know.
card: friend-run-dry-run-prints-the-plan
- PATHS: cmd/nova-friend/**,internal/friend/**
- Task: make `nova-friend run --dry-run` print the RUN DRY-RUN line the help promises, writing nothing.

### Naming two friends took five tries, and the bus refuses a sender no daemon has proved

- Read: `nova-bus send` refusal ("add one with `nova-config friend add ada --slots 1 --tiers flash --as <you>`, then `nova-config apply`"), `nova-config help`, `friend add -h`, `machine add -h`, `fleet set -h`.
- Expected: the two commands the refusal names are enough.
- Happened: `apply` without `--kind` refused for fleet endpoints; with `--kind friend` it refused for
  no beat naming a machine and no coordinator machine; it took a machine row, `fleet set
  --coordinator m1 --redis_port --pg_dsn` (a PostgreSQL address I had no use for) and a full apply.
  Then `send` still refused: both ends must be heard, so ada needed a daemon and a fake of her own
  as well. The refusals each named the next step, but the first did not name the whole.
card: bus-refusal-names-the-whole-path
- PATHS: cmd/nova-bus/**,internal/bus/**,docs/CLI.md
- Task: have the "no known name" refusal print the full local recipe (migrate --file, machine add, fleet set, apply, and that each end needs a running friend daemon).

### `nova-friend status` and `nova-bus log` ask for a flag the same session already gave

- Read: `nova-friend status -h` ("state-dir default: `<dir>/.nova-friend` where the daemon wrote there").
- Expected: `status --as bob --dir /work/bob` finds the daemon that ran with `--dir /work/bob`.
- Happened: `STATUS NONE: no daemon has run as bob (no status file in /work/.nova-friend/bob)`; the
  state was under `/work/bob/.nova-friend`; adding `--state-dir` found it.
card: friend-status-finds-the-run-state
- PATHS: cmd/nova-friend/**,internal/friend/**
- Task: make `status --dir <d>` look in `<d>/.nova-friend` first, as its help says.

## Verdict

Could a stranger do it: **no**, not from README and help alone. I got there, with a real daemon
delivering `job one` into a Go fake harness and the report coming back to the sender, but only after
reading `docs/SPEC-FRIEND.md` for the contract of a harness, and after guessing the harness name
(`opencode`), the report location and a coordinator for the pong line. The container run, from first
command to the report landing, took 5 minutes 39 seconds by the clock (02:08:13 to 02:13:52); the
reading and guessing around it are not timed. Six stumbles, each with a card above.
