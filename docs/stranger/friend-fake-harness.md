# Friend, fake harness

Cold run of one nova-friend whose harness is a small Go program, on a throwaway Redis, with one job delivered and the report observed on the bus. Friends are made-up: pip receives, ada coordinates. Passwords in this record are throwaway strings that lived only inside the container.

## Setup

Bench: one container on vision (`ssh glenn@vision`), name `stranger-ffh-r2w3`, id `097c00f67b885095615623d60e0c6214999e94feaf0fd8f76726f16d8543465a`. Started `podman run --rm --timeout 14400 --network none` from `localhost/nova-functional:latest` (image id `c4ea1aae0428`), user `bench`, workdir `/work`. Mounts: the nova-tools checkout read-only at `/src`, the bench directory read-write at `/work`, the functional module cache, and Go build and temp caches. Command: `sleep 14400`. Every process of the run, Redis included, was a child of that container.

Host clock at the first command inside the container: Tue Oct 6 05:46:49 UTC 2026.

```
Linux 097c00f67b88 7.0.0-34-generic #34-Ubuntu SMP PREEMPT_DYNAMIC Wed Sep  2 14:29:37 UTC 2026 x86_64 x86_64 x86_64 GNU/Linux
```

Versions printed by `<tool> version`, after the binaries were built from the mounted checkout (the module-proxy install is a stumble below):

```
$ go version
go version go1.26.6 linux/amd64

$ nova-friend version
nova-friend devel linux/amd64 go1.26.6

$ nova-bus version
nova-bus devel linux/amd64 go1.26.6

$ nova-redis version
nova-redis devel linux/amd64 go1.26.6

$ nova-config version
nova-config devel linux/amd64 go1.26.6
```

`nova-config version` was printed at 06:00:48 UTC, after that binary was installed. Redis itself printed `Redis version=8.10.2` in its start log. The sprint server flag was `127.0.0.1:1` on every real `nova-friend run`, so a beat could not leave the container. The store was `127.0.0.1:6379` inside the container.

## Transcript

Commands ran inside the container. A line that begins with `$` is the command. Output is what the command printed. A line that says `[trimmed: ...]` is the only place output was cut.

```
$ go version
go version go1.26.6 linux/amd64
exit:0

$ go install github.com/mas-bandwidth/nova-tools/cmd/nova-friend@v1.0.0
go: github.com/mas-bandwidth/nova-tools/cmd/nova-friend@v1.0.0: module lookup disabled by GOPROXY=off
exit:1

$ cd /src
/src

$ go install ./cmd/nova-friend ./cmd/nova-bus ./cmd/nova-redis
exit:0

$ nova-friend version
nova-friend devel linux/amd64 go1.26.6
exit:0

$ nova-bus version
nova-bus devel linux/amd64 go1.26.6
exit:0

$ nova-redis version
nova-redis devel linux/amd64 go1.26.6
exit:0
```

`nova-friend help` (3260 bytes, saved) says the daemon parks on nova-bus, delivers through the harness, keeps state in `<dir>/.nova-friend/`, and lists verbs `run, install, uninstall, check, ping, ping-install, ping-uninstall, pong, wait-pong, status, serve, version`. The example is `nova-friend install --as bob --harness opencode --dir ./bob --dry-run`. Bare `nova-friend` exits 2: `FRIEND REFUSED: no verb and no file given`.

`nova-friend run -h` and `nova-friend <verb> -h` were captured together (69964 bytes). Each verb appears twice, once from `help` and once from `-h`.

[trimmed: that file, except the lines this run used]

```
The loop launchd runs (install writes it). It starts only on a push proof: a harness with no deliver
command (claude, the surveyed ones) is refused at once, exit 2, the adapter card its remedy; then one SESSION
CHECK goes in through the harness and its pong must reach the bus within 5m0s, else exit 2 with the remedy

In one-shot mode width lanes run, each its own session seeded from the friend's AGENTS.md and
REPORT.md and RESULT.md to write, one bus line to send), the waiting messages riding along, and hands the
next only when the turn ends; a card with no RESULT.md after two turns is set aside and reported. A claude
lane is a process per card instead (env CLAUDE_CONFIG_DIR=<config_dir> claude -p <the brief>, stdin

  --harness <string>  the harness the session runs in: opencode, codex, claude, antigravity, dsh, gemini, grok, copilot, cursor, amp, goose, kiro, cline, aider, roo, windsurf, zed, warp (required)
  --server <string>  the sprint server, host:port (default: NOVA_SPRINT_SERVER, else 127.0.0.1:6390)
```

```
$ nova-friend wall -h
Usage of wall:
  -config-dir string
    the friend's CLAUDE_CONFIG_DIR, and the HOME inside the wall
  -deny value
    a path no write inside the wall reaches, the coordinator's self; ~/ is under HOME (repeatable, at least one)
  -dir string
    the friend's working directory
  -job value
    a job directory outside --dir, writable inside the wall (repeatable)
  -profile string
    the wall profile: friend (default "friend")
  -read value
    a directory the harness reads, beyond the system roots (repeatable)
exit:0

$ nova-friend help wall
FRIEND REFUSED: "wall" is no verb and no file; the verbs are run, install, uninstall, check, ping, ping-install, ping-uninstall, pong, wait-pong, status, serve, version, and a file is given by its path (./wall); run: nova-friend help
exit:2

$ nova-friend run --as pip --harness fake --dir /work/pip --redis 127.0.0.1:6379 --server 127.0.0.1:6379 --deny-self /work/not-self --dry-run
RUN REFUSED: --harness "fake" is no harness; it wants one of opencode, codex, claude, antigravity, dsh, gemini, grok, copilot, cursor, amp, goose, kiro, cline, aider, roo, windsurf, zed, warp; run: nova-friend help
exit:2

$ nova-friend run --as pip --harness opencode --dir /work/pip --redis 127.0.0.1:6379 --server 127.0.0.1:6379 --deny-self /work/not-self --mode one-shot --dry-run
RUN DRY-RUN as=pip harness=opencode dir=/work/pip state=/work/pip/.nova-friend redis=127.0.0.1:6379; nothing was started
exit:0
```

That dry-run pointed `--server` at the store port. Later real runs used `--server 127.0.0.1:1`.

`nova-redis serve -h` (the usage kept below; the flag list is trimmed) does not say a password is required:

```
usage: nova-redis serve [flags]
  nova-redis serve --bind <addr>[,<addr>...] --port <port> --dir <store-dir> [--users <name>[,<name>...]] [--dry-run]
```

[trimmed: the rest of serve -h,  the bind/dir/port flag paragraphs]

`nova-bus help` (1887 bytes) and `nova-bus send -h` were read. send takes `--as`, `--to`, `--redis`, `--subject`, `--body`, `--kind`.

The fake harness is a Go program at `/work/fake` (module `fakeopencode`), built to `/work/bin/opencode`, first on `PATH`. It answers `session list` with one JSON session whose directory is the working directory, and `run` by executing the command the turn says to run exactly, then writing `REPORT.md` and `RESULT.md` under the `outbox:` line of the delivered text.

```
$ go build -o /work/bin/opencode .
go: go.mod file not found in current directory or any parent directory; see 'go help modules'
build_exit:1

$ /work/bin/opencode session list --format json
bash: line 125: /work/bin/opencode: No such file or directory
session_exit:127

$ go mod init fakeopencode
go: creating new go.mod: module fakeopencode
go: to add module requirements and sums:
go mod tidy
mod_exit:0

$ go build -o /work/bin/opencode .
build_exit:0

$ /work/bin/opencode session list --format json
[{"id":"ses_fake","directory":"/work/pip","updated":1}]
session_exit:0
```

The session-list command above was run with the working directory `/work/pip`.

```
$ nova-redis serve --bind 127.0.0.1 --port 6379 --dir /work/redis
SERVE REFUSED: NOVA_REDIS_PASSWORD is empty; run under `nova-secrets exec --only NOVA_REDIS_PASSWORD -- nova-redis serve ...` so auth comes from nova-secrets at run time, never an argument; run: nova-redis help

$ redis-cli -p 6379 ping
Could not connect to Redis at 127.0.0.1:6379: Connection refused

$ NOVA_REDIS_PASSWORD=bench-throwaway nova-redis serve --bind 127.0.0.1 --port 6379 --dir /work/redis
SERVE START bind=127.0.0.1 port=6379 auth=on persistence=aof eviction=none dir=/work/redis aclfile=/work/redis/users.acl users=0 program=/usr/local/bin/redis-server
Redis version=8.10.2, bits=64, commit=00000000, modified=0, pid=1676, just started
Ready to accept connections tcp
serve_pid:1667

$ redis-cli -p 6379 ping
NOAUTH Authentication required.

$ redis-cli -p 6379 -a bench-throwaway ping
Warning: Using a password with '-a' or '-u' option on the command line interface may not be safe.
PONG
```

[trimmed: Redis memory-overcommit warning and the AOF/BGSAVE lines between start and Ready, and every later BGSAVE]

```
$ nova-friend run --as pip --harness opencode --dir /work/pip --redis 127.0.0.1:6379 --server 127.0.0.1:1 --deny-self /work/not-self
pip_pid:1688
RUN 2026-10-06T05:52:57Z store: redis at 127.0.0.1:6379 as the default user, no password: login refused: NOAUTH Authentication required.; next: name the user (NOVA_SPRINT_REDIS_USER) and the variable that holds its password; opening again in 1s
RUN 2026-10-06T05:52:58Z store: redis at 127.0.0.1:6379 as the default user, no password: login refused: NOAUTH Authentication required.; next: name the user (NOVA_SPRINT_REDIS_USER) and the variable that holds its password; opening again in 2s
RUN 2026-10-06T05:53:00Z store: redis at 127.0.0.1:6379 as the default user, no password: login refused: NOAUTH Authentication required.; next: name the user (NOVA_SPRINT_REDIS_USER) and the variable that holds its password; opening again in 4s
RUN 2026-10-06T05:53:04Z store: redis at 127.0.0.1:6379 as the default user, no password: login refused: NOAUTH Authentication required.; next: name the user (NOVA_SPRINT_REDIS_USER) and the variable that holds its password; opening again in 8s
RUN 2026-10-06T05:53:12Z store: redis at 127.0.0.1:6379 as the default user, no password: login refused: NOAUTH Authentication required.; next: name the user (NOVA_SPRINT_REDIS_USER) and the variable that holds its password; opening again in 16s
```

That process was killed (`kill 1688`, exit 0). It had been started by this run.

```
$ NOVA_SPRINT_REDIS_USER=default NOVA_REDIS_BENCH_PASSWORD=bench-throwaway nova-friend run --as pip --harness opencode --dir /work/pip --redis 127.0.0.1:6379 --server 127.0.0.1:1 --deny-self /work/not-self
pip_pid:1793
PONG REFUSED: pip is no known name; the names are nova-config's friend and machine rows (nova-bus names lists them); add one with nova-config friend add pip --slots 1 --tiers flash --as <you>, then nova-config apply; run: nova-friend help
exit status 2
RUN REFUSED: no push proof: CHECK FAIL harness=opencode stage=deliver why="opencode's deliver command exited 1"; the daemon did not start; run: open the friend's opencode session in /work/pip, then prove it answers: nova-friend check --as pip --harness opencode --dir /work/pip
```

The deliver text the fake logged for that check:

```
SESSION CHECK dug467
The daemon has had no bus message from this session for a while and asks whether the session is alive; only an answer from inside this session counts, and none within 5m0s puts you down. Answer now, before anything else, with one command, then end this turn: /home/bench/go/bin/nova-friend pong --as pip --nonce dug467 --state-dir /work/pip/.nova-friend --redis 127.0.0.1:6379 --to pip
```

```
$ go install ./cmd/nova-config
exit:0

$ nova-config help
```

[trimmed: nova-config help. It names `--file` as a local JSON stand-in, and the verbs migrate, friend, machine, fleet, apply.]

```
$ nova-config migrate --file /work/try.json
CONFIG MIGRATE file=/work/try.json from=0 to=34 applied=34
exit:0

$ nova-config friend add pip --slots 1 --tiers flash --width 1 --as ada --file /work/try.json
CONFIG ADD kind=friend name=pip rev=1
exit:0

$ nova-config friend add ada --slots 1 --tiers flash --width 1 --as ada --file /work/try.json
CONFIG ADD kind=friend name=ada rev=2
exit:0

$ nova-config apply --kind friend --file /work/try.json --redis 127.0.0.1:6379 --as ada
APPLY ADD kind=friend name=ada
nova-config apply REFUSED: friend ada has no beat naming a machine and the fleet names no coordinator machine to charge her slots to; run: nova-config fleet set --coordinator <machine>, then apply; run: nova-config apply --dry-run
exit:1

$ nova-bus names --redis 127.0.0.1:6379
NAMES OK count=0 proven=0
exit:0

$ nova-config machine add bench --user bench --seat trial --slots 4 --width 1 --as ada --file /work/try.json
CONFIG ADD kind=machine name=bench rev=3
exit:0

$ nova-config fleet set --coordinator bench --store bench --redis_port 6379 --as ada --file /work/try.json
CONFIG SET kind=fleet name=fleet rev=4 changed=coordinator,redis_port,store
exit:0

$ nova-config apply --kind friend --file /work/try.json --redis 127.0.0.1:6379 --as ada
APPLY ADD kind=friend name=ada
nova-config apply REFUSED: friend ada has no beat naming a machine and the fleet names no coordinator machine to charge her slots to; run: nova-config fleet set --coordinator <machine>, then apply; run: nova-config apply --dry-run
exit:1

$ nova-config fleet set --pg_dsn postgres://bench@127.0.0.1:5432/try --as ada --file /work/try.json
CONFIG SET kind=fleet name=fleet rev=5 changed=pg_dsn
exit:0

$ nova-config apply --kind machine --file /work/try.json --redis 127.0.0.1:6379 --as ada
APPLY ADD kind=machine name=bench
CONFIG APPLY kind=machine add=1 set=0 remove=0 rev=3 ms=1
exit:0

$ nova-config apply --kind fleet --file /work/try.json --redis 127.0.0.1:6379 --as ada
APPLY SET kind=fleet name=fleet changed=store,coordinator,redis_port,pg_dsn,loops_dir
CONFIG APPLY kind=fleet add=0 set=1 remove=0 rev=5 ms=1
exit:0

$ nova-config apply --kind friend --file /work/try.json --redis 127.0.0.1:6379 --as ada
APPLY ADD kind=friend name=ada
APPLY ADD kind=friend name=pip
CONFIG APPLY kind=friend add=2 set=0 remove=0 rev=2 ms=2
exit:0

$ nova-bus names --redis 127.0.0.1:6379
NAMES OK count=3 proven=0
NAMES NAME name=ada push=none age=never harness=-
NAMES NAME name=bench push=none age=never harness=-
NAMES NAME name=pip push=none age=never harness=-
exit:0
```

The `pg_dsn` value was a local URI written into the file store so `--kind fleet` apply would accept the row. Nothing dialed it.

```
$ nova-friend run --as pip --harness opencode --dir /work/pip --redis 127.0.0.1:6379 --server 127.0.0.1:1 --deny-self /work/not-self
pip_pid:2180
PONG OK nonce=1dce40 to=pip id=01M47WGWK53VY4YKWX6DPKP8YM at=2026-10-06T05:55:57Z
fake harness: turn done
RUN 2026-10-06T05:55:57Z push proof: CHECK OK harness=opencode took=37.612ms
RUN 2026-10-06T05:55:57Z inbox: the server did not say which cards are on her row: the sprint server at 127.0.0.1:1 did not answer: Post "http://127.0.0.1:1/verbs": dial tcp 127.0.0.1:1: connect: connection refused; nothing written or retired until it does
RUN 2026-10-06T05:55:57Z harness check: cannot tell: opencode runs no standing process (each turn starts /work/bin/opencode); the session check alone
RUN 2026-10-06T05:55:57Z presence: a session check is owed; it waits for the turn under way
RUN 2026-10-06T05:55:57Z push proof: down: no session answer yet; nova-bus refuses pip as deaf
fake harness: turn done
RUN 2026-10-06T05:55:57Z subject="pong" messages=1 took=34ms exit=0 acked=true
RUN 2026-10-06T05:55:58Z presence: session check mus4yl into the session
PONG REFUSED: --to is required: no ping has named a seat yet (no status file in /work/pip/.nova-friend); it wants the coordinator's name; run: nova-friend help
exit status 2
RUN 2026-10-06T05:55:58Z presence: session check mus4yl exit=1 error=<nil>; the bound runs

$ nova-bus names --redis 127.0.0.1:6379
NAMES OK count=3 proven=0
NAMES NAME name=ada push=none age=never harness=-
NAMES NAME name=bench push=none age=never harness=-
NAMES NAME name=pip push=down age=9s harness=opencode
```

`kill 2180` (this run's daemon), exit 0. Restarted both friends with `--coordinator ada`, still logged in as Redis user `default`:

```
$ setsid nova-friend run --as pip --harness opencode --dir /work/pip --redis 127.0.0.1:6379 --server 127.0.0.1:1 --deny-self /work/not-self --coordinator ada
pip_pid:2258

$ setsid nova-friend run --as ada --harness opencode --dir /work/ada --redis 127.0.0.1:6379 --server 127.0.0.1:1 --deny-self /work/not-self --coordinator ada
ada_pid:2259
```

Both logs reached `push proof: up` (pip nonce `ebp5bv`, ada nonce `8z7z7b`). The shell that started them echoed a shortened label with an ellipsis; the argv above is what ran.

```
$ nova-bus names --redis 127.0.0.1:6379
NAMES OK count=3 proven=2
NAMES NAME name=ada push=proven age=1s harness=opencode
NAMES NAME name=bench push=none age=never harness=-
NAMES NAME name=pip push=proven age=0s harness=opencode
```

First job, still logged in as Redis user `default`. Body:

```
job: stranger-one
outbox: /work/pip/outbox/stranger-one
reply: nova-bus send --as pip --to ada --redis 127.0.0.1:6379 --kind report --subject report-stranger-one
```

```
$ nova-bus send --as ada --to pip --redis 127.0.0.1:6379 --kind request --subject job-stranger-one --body <the three lines above>
SEND REFUSED: --as ada is not the login user default: this connection acts as default; drop --as, or log in as ada (NOVA_SPRINT_REDIS_USER=ada with its password); run: nova-bus help
send_exit:2
```

Outbox files were absent. `nova-bus log` at that moment: `LOG OK total=5`, five pongs, no job.

```
$ nova-redis acl render
ACL RENDER OK users=4 functions=41 library=0f9c8ca0574bb69c
exit:0

$ nova-redis acl apply --dry-run --addr 127.0.0.1:6379 --password-env-for ada=ADA_PW --password-env-for pip=PIP_PW
ACL WOULD-SET user=coordinator role=coordinator
ACL WOULD-SET user=bench role=member
ACL WOULD-SET user=ns-table role=table
ACL WOULD-SET user=ns-friend role=friend
ACL APPLY OK dry-run=true users=4 set=0 would=4 library=0f9c8ca0574bb69c store=127.0.0.1:6379
exit:0
```

[trimmed: four long `ACL SETUSER` rule lines for coordinator, bench, ns-table, ns-friend. The names ada and pip are absent.]

```
$ redis-cli -p 6379 -a bench-throwaway --no-auth-warning ACL SETUSER ada on '>bench-ada' '~*' '&*' '+@all'
OK
exit:0

$ redis-cli -p 6379 -a bench-throwaway --no-auth-warning ACL SETUSER pip on '>bench-pip' '~*' '&*' '+@all'
OK
exit:0
```

`kill 2258 2259` (this run's daemons). Restarted logged in as themselves:

```
$ NOVA_SPRINT_REDIS_USER=pip NOVA_REDIS_BENCH_PASSWORD=bench-pip setsid nova-friend run --as pip --harness opencode --dir /work/pip --redis 127.0.0.1:6379 --server 127.0.0.1:1 --deny-self /work/not-self --coordinator ada
pip_pid:2476
RUN 2026-10-06T05:57:38Z push proof: up: the session answered le143c through opencode's deliver adapter; nova-bus hears pip

$ NOVA_SPRINT_REDIS_USER=ada NOVA_REDIS_BENCH_PASSWORD=bench-ada setsid nova-friend run --as ada --harness opencode --dir /work/ada --redis 127.0.0.1:6379 --server 127.0.0.1:1 --deny-self /work/not-self --coordinator ada
ada_pid:2477
RUN 2026-10-06T05:57:37Z push proof: up: the session answered 1w1ao4 through opencode's deliver adapter; nova-bus hears ada
```

[trimmed: the CHECK OK, connection-refused beat, and "push proof: down" lines that precede each "push proof: up"]

```
$ NOVA_SPRINT_REDIS_USER=ada NOVA_REDIS_BENCH_PASSWORD=bench-ada nova-bus send --as ada --to pip --redis 127.0.0.1:6379 --kind request --subject job-stranger-one --body <the three lines above>
SEND OK id=01M47WM4K4B6FMQE3HQ26ASGKC to=pip cc=- kind=request at=2026-10-06T05:57:43Z bytes=161 sha256=53e56e7710439370325e5e70965577b32d01f0d55842510bbc2b7636fa9fb9c5
send_exit:0
```

Pip's daemon then printed the reply refusal, and the outbox files were already there:

```
SEND REFUSED: takes no positional arguments, got "LAND" (flags come before arguments); run: nova-bus help
exit status 2
RUN 2026-10-06T05:57:43Z subject="job-stranger-one" messages=1 took=653ms exit=1 deliveries=1/3

$ cat /work/pip/outbox/stranger-one/REPORT.md
Verdict: LAND

the fake harness read the delivered job and wrote this report

$ cat /work/pip/outbox/stranger-one/RESULT.md
RESULT: stranger-one
```

`nova-bus log --bodies --max 4` printed `LOG OK total=10` and `LOG MORE ... shown=4 total=10`. The job was past that ceiling.

The fake was rebuilt so the reply argv keeps `--body` as one argument (`Verdict: LAND`), then a second job was sent. Body:

```
job: stranger-two
outbox: /work/pip/outbox/stranger-two
reply: nova-bus send --as pip --to ada --redis 127.0.0.1:6379 --kind report --subject report-stranger-two
```

```
$ NOVA_SPRINT_REDIS_USER=ada NOVA_REDIS_BENCH_PASSWORD=bench-ada nova-bus send --as ada --to pip --redis 127.0.0.1:6379 --kind request --subject job-stranger-two --body <the three lines above>
SEND OK id=01M47WSPQXD5JAN8Z9Z5D23DE4 to=pip cc=- kind=request at=2026-10-06T06:00:46Z bytes=161 sha256=af74877d265826eea3d7557d415beecce61e515a2dc9df398662af8ece4c4222
send_exit:0

$ cat /work/pip/outbox/stranger-two/REPORT.md
Verdict: LAND

the fake harness read the delivered job and wrote this report

$ cat /work/pip/outbox/stranger-two/RESULT.md
RESULT: stranger-two
```

Pip's log:

```
SEND OK id=01M47WSPRPMQXAKJKR4FQ8FTF3 to=ada cc=- kind=report at=2026-10-06T06:00:46Z bytes=13 sha256=b1a2ee320bf816ed822614be42f09ad15bf5a9014fc9148873f90af9432757b4
fake harness: turn done
RUN 2026-10-06T06:00:46Z subject="job-stranger-two" messages=1 took=578ms exit=0 notice="coordinator silent" acked=true
```

Ada's log:

```
RUN 2026-10-06T06:00:46Z subject="report-stranger-two" messages=1 took=199ms exit=0 notice="coordinator silent" acked=true
```

```
$ NOVA_SPRINT_REDIS_USER=ada NOVA_REDIS_BENCH_PASSWORD=bench-ada nova-bus names --redis 127.0.0.1:6379
NAMES OK count=3 proven=2
NAMES NAME name=ada push=proven age=12s harness=opencode
NAMES NAME name=bench push=none age=never harness=-
NAMES NAME name=pip push=proven age=12s harness=opencode

$ NOVA_SPRINT_REDIS_USER=ada NOVA_REDIS_BENCH_PASSWORD=bench-ada nova-bus log --redis 127.0.0.1:6379 --bodies --max 0
LOG OK total=12
LOG MESSAGE id=01M47WM4K4B6FMQE3HQ26ASGKC from=ada to=pip cc=- re=- kind=request at=2026-10-06T05:57:43Z subject="job-stranger-one" body="job: stranger-one\noutbox: /work/pip/outbox/stranger-one\nreply: nova-bus send --as pip --to ada --redis 127.0.0.1:6379 --kind report --subject report-stranger-one"
LOG MESSAGE id=01M47WSPQXD5JAN8Z9Z5D23DE4 from=ada to=pip cc=- re=- kind=request at=2026-10-06T06:00:46Z subject="job-stranger-two" body="job: stranger-two\noutbox: /work/pip/outbox/stranger-two\nreply: nova-bus send --as pip --to ada --redis 127.0.0.1:6379 --kind report --subject report-stranger-two"
LOG MESSAGE id=01M47WSPRPMQXAKJKR4FQ8FTF3 from=pip to=ada cc=- re=- kind=report at=2026-10-06T06:00:46Z subject="report-stranger-two" body="Verdict: LAND"
```

[trimmed: the nine earlier pong lines in that log, total=12]

`nova-bus log --as ada` was refused first (`unknown flag --as`); the log above is the retry without `--as`, with the login in the environment. `nova-bus names --as ada` was refused the same way, then retried without `--as`.

```
$ nova-friend version
nova-friend devel linux/amd64 go1.26.6
$ nova-bus version
nova-bus devel linux/amd64 go1.26.6
$ nova-redis version
nova-redis devel linux/amd64 go1.26.6
$ nova-config version
nova-config devel linux/amd64 go1.26.6
$ go version
go version go1.26.6 linux/amd64
```

Clock at the report on the bus: Tue Oct 6 06:00:46 UTC 2026. The log command finished at 06:00:48 UTC.

## Stumbles

### Install from the README needs a module proxy

Read: the `go install` error, and README.md's install example `go install github.com/mas-bandwidth/nova-tools/cmd/nova-memory@v1.0.0`. Expected that example to install nova-friend. Happened: `module lookup disabled by GOPROXY=off`, exit 1, because the container has `--network none`. Built from the mounted checkout with `go install ./cmd/nova-friend ./cmd/nova-bus ./cmd/nova-redis`.

card: install-offline-module
PATHS: README.md
task: say an offline image builds the cmds from the checkout

### The functional image tag is not in the top README

Read: `infra/functional-image/README.md`, because the card named that directory and the top README does not name an image. Expected the top README to name the image a stranger runs. Happened: the tag `localhost/nova-functional:latest` is in the functional-image README.

card: image-tag-not-in-readme
PATHS: README.md
task: name the functional image tag next to the install example

### Help will not point --harness at a Go program

Read, after `--harness fake` was refused and the only process-per-card example was the claude shell line `claude -p`: `internal/friend/adapter.go`, `internal/friend/adapter_opencode_lanes.go`, `internal/friend/daemon.go`, `internal/friend/presence.go`, `internal/friend/pushproof.go`, `internal/friend/pushproof_start.go`, `internal/friend/conformance.go`, `cmd/nova-friend/main.go`, `internal/bus/pushproof.go`, `internal/bus/bus.go`. Expected help to show the argv a stand-in harness answers, in Go. Happened: `--harness` accepts a fixed name list, and the deliver command for opencode is `opencode session list --format json` then `opencode run --session <id> --dir <dir> <text>`. The fake was named `opencode` and placed first on `PATH`. The shell example in help is the stumble that licensed those reads.

card: harness-is-a-fixed-name
PATHS: cmd/nova-friend internal/friend
task: show the deliver argv a stand-in harness answers, as a Go program

### A one-file Go program does not build

Read: the `go build` error only. Expected `go build` of one file to produce the binary. Happened: `go.mod file not found in current directory or any parent directory`. `go mod init fakeopencode`, then the build, exit 0.

card: fake-needs-a-module
PATHS: the bench program only
task: a one-file harness example should say it needs a module

### serve starts only with a password the help did not require

Read: the refusal line, and `nova-redis serve -h` (already captured; it does not mention the password). Expected README's throwaway on `127.0.0.1:6379` to listen. Happened: `SERVE REFUSED: NOVA_REDIS_PASSWORD is empty`. Set `NOVA_REDIS_PASSWORD=bench-throwaway` in the container only. Redis reached Ready. `redis-cli ping` was `NOAUTH` until `-a`. Did not run nova-secrets and did not use a fleet store.

card: serve-wants-a-password
PATHS: cmd/nova-redis README.md
task: serve -h should say the password variable before the first refusal

### The client password is a different variable

Read: `internal/nsprint/redisauth/redisauth.go`, after the daemon kept saying no password. Expected `NOVA_REDIS_PASSWORD` to be the password nova-friend sends. Happened: the daemon logged `as the default user, no password: login refused: NOAUTH` and `name the user (NOVA_SPRINT_REDIS_USER) and the variable that holds its password`. The client password variable is `NOVA_REDIS_BENCH_PASSWORD`. Exported `NOVA_SPRINT_REDIS_USER=default` and `NOVA_REDIS_BENCH_PASSWORD=bench-throwaway`.

card: friend-password-is-a-different-variable
PATHS: internal/nsprint/redisauth/redisauth.go cmd/nova-friend
task: help must name the client user and password variables next to the serve password variable

### A name needs friend, machine, and fleet rows

Read: `nova-config help`, `nova-config friend add -h`, `nova-config apply -h`, `nova-config machine add -h`, `nova-config fleet set -h`, after pong named those verbs. Expected the README's ada/bob send on `127.0.0.1:6379` to accept names once Redis was up. Happened: `PONG REFUSED: pip is no known name`. `friend add` plus `apply --kind friend` still refused: `friend ada has no beat naming a machine and the fleet names no coordinator machine`. A machine row, a fleet coordinator, and a `pg_dsn` on the fleet row, each applied, made `nova-bus names` list ada, pip, and bench.

card: friend-rows-before-a-name
PATHS: cmd/nova-config README.md
task: one help example that makes two names on a local file and applies them to 127.0.0.1:6379

### The standing session check drops --to

Read: `cmd/nova-friend/main.go` (`checkTo` and `answerTo`) and `internal/friend/presence.go`, after the second check failed. Expected the pong line that worked at startup to be the line the standing check uses. Happened: the startup check included `--to pip` and printed `PONG OK`. The later check, with no `--coordinator`, omitted `--to`, and pong printed `PONG REFUSED: --to is required`. Passed `--coordinator ada`. Later checks included `--to ada`, and both friends reached `push proof: up`.

card: session-check-drops-to
PATHS: cmd/nova-friend/main.go internal/friend/presence.go
task: the standing session-check command must include the same --to the startup check uses

### The login user is not the friend

Read: the send refusal, `nova-redis acl render` output, and `nova-redis acl apply -h`. Expected `--as ada` to send once ada was a nova-config name and her push proof was up. Happened: `SEND REFUSED: --as ada is not the login user default`. acl render's users are coordinator, bench, ns-table, and ns-friend. A dry-run apply with password env vars for ada and pip still would set only those four. Created Redis users ada and pip with `ACL SETUSER` (throwaway passwords `bench-ada` and `bench-pip`), restarted both daemons logged in as themselves, then `SEND OK`.

card: login-user-is-not-the-friend
PATHS: cmd/nova-bus cmd/nova-redis
task: a throwaway friend must send as her nova-config name without a hand-built ACL user

### The first reply split the report body

Read: pip's daemon log and the fake's reply call. Expected the outbox report and one bus report whose body is the first line of REPORT.md. Happened: the outbox files were written, and the reply command was `SEND REFUSED: takes no positional arguments, got "LAND"`. The fake joined the reply words and then split them, so `Verdict: LAND` became a positional. Rebuilt the fake to pass `--body` as one argument and sent `job-stranger-two`. The bus then showed `from=pip to=ada` `kind=report` `subject="report-stranger-two"` `body="Verdict: LAND"`, and ada's daemon acked that subject.

card: reply-body-is-a-positional
PATHS: the bench program only
task: a help example of the report body must keep a body that contains a space

## Verdict

Could a stranger do it from the README and the tools' own help: no.

The job did arrive, the outbox report was written, and the second send put `Verdict: LAND` on the bus from pip to ada. Getting there took the reads named in the stumbles: the deliver argv, the client password variable, the session-check `--to`, and a Redis user whose name is the friend. Help alone stops at a fixed harness list, a shell example, an empty-password refusal, and a name the store does not know.

Minutes: 14, from 05:46:49 UTC to the report message at 06:00:46 UTC.
