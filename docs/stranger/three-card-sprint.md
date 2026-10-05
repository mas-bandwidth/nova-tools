# A stranger runs a three-card sprint

A cold run of nova-sprint by a bud of Rowan (rowan-space, Claude Opus 5.5 in Claude Code), on 2026-10-05: one member, one reader, one stream, three one-line cards on a throwaway git repository, dealt, worked by hand with the worker verbs, read and landed on a throwaway origin by `nova-sprint run --land`, all inside one container. The rules of the run: read the top-level README.md and the tools' own help only; a moment that needed anything else, a guess or a retry is a stumble below, with what was read to get past it.

## Setup

- Bench: hetzner (Linux 6.8.0-139-generic x86_64). vision answered but has no container runtime (`podman: command not found`), so the run went to hetzner.
- Container: `podman run -d --rm --timeout 14400 --network none --user 0` of `localhost/nova-functional:ctx-7e545976810241cb` (infra/functional-image: Go 1.26.6, redis-server 8.10.2, git), with `~/nova-bench/stranger/stranger-three-card-sprint-r.w1~15/` mounted at `/work` and `HOME=/work/home`. Every command below ran inside it through `podman exec`; the Redis and the sprint server were started inside it with `podman exec -d` and died with it. The container was stopped at the end (`--rm` removed it) and the bench directory removed.
- `--user 0`: the image's user `bench` (uid 10001) could not write the mount under rootless podman; root in the container is the host user.
- Source: a checkout of mas-bandwidth/nova-tools at `a9ecf2b048f9` (rowan/integration-2026-10-04), copied into `/work/nova-tools`. README.md says "build just that tool with Go"; the container has no network, so the module cache was filled on the host first (`go mod download` from the host's own cache into `/work/gomodcache`, `GOMODCACHE` and `GOCACHE` pointed under `/work`).
- Versions, as `<tool> version` printed them: `nova-sprint devel linux/amd64 go1.26.6`, `nova-swarm devel linux/amd64 go1.26.6`, `nova-redis devel linux/amd64 go1.26.6`, `nova-friend devel linux/amd64 go1.26.6`. A build from a checkout prints `devel`, so the run cannot say from the tools that this is nova-tools v1.2.0 or nova-sprint v1.0.0; the commit above is the version.
- The fake harness of stranger-friend-fake-harness is not in this tree (no file or branch of that name), so the three cards were worked by hand with `fleet beat`, `queue`, `take` and `finish`, and read with `read`.
- The throwaway repository: `/work/t/origin.git` (bare, the origin) and its clone `/work/t/work`, base branch `sprint/s1`. Nothing was pushed anywhere else.
- Read before the run: README.md, and the card's own brief. The repository's AGENTS.md was opened before the run for the working rules of the change (the brief asks it); nothing in it was used in the run.

## Transcript

Every command, in order, with its output as printed. `[exit n]` is the exit status. Trims are marked `[trimmed: ...]`: long help, the idle ticks of the server log, and the brief text the packets repeat. Lines in `[outside the container: ...]` are file edits made from the host shell into the mounted `/work`. The `sleep`s between a write and the read that followed it are left out.

```text
$ date -u; uname -srm; go version
Mon Oct  5 19:27:09 UTC 2026
Linux 6.8.0-139-generic x86_64
go version go1.26.6 linux/amd64
[exit 0]

$ go install ./cmd/nova-sprint ./cmd/nova-swarm ./cmd/nova-redis ./cmd/nova-friend ./cmd/nova-bus ./cmd/nova-table ./cmd/nova-config && ls /work/home/go/bin
nova-bus
nova-config
nova-friend
nova-redis
nova-sprint
nova-swarm
nova-table
[exit 0]

$ export PATH=$HOME/go/bin:$PATH

$ nova-sprint version
nova-sprint devel linux/amd64 go1.26.6
[exit 0]

$ nova-swarm version
nova-swarm devel linux/amd64 go1.26.6
[exit 0]

$ nova-redis version
nova-redis devel linux/amd64 go1.26.6
[exit 0]

$ nova-friend version
nova-friend devel linux/amd64 go1.26.6
[exit 0]

$ nova-sprint help
nova-sprint: a sprint of work cards, dealt to a fleet of workers and read before they land

how it works: one store (Redis or a twin file) holds the work, readers, merge
and fleet tables and the sprint view. A card is one unit of work in a stream.
Each tick deals ready cards to members (machines with a width), sends finished
work to readers and queues passed work for merging by stream. Decisions it
cannot make go to the coordinator's inbox.
first run: no Redis needed; the store is the file sprint.twin:
  export NOVA_SPRINT_REDIS=mem:sprint.twin NOVA_SPRINT_ACTOR=boss
Follow the card flow under "trying it without a Redis", ticking by hand.
For a real fleet, "A real fleet" explains the server and clients; the example:
block shows the coordinator's day on that store.
[trimmed: 590 more lines of `nova-sprint help`]
[exit 0]

$ nova-redis help
nova-redis: run a local Redis store, and keep short-lived named values in it

how it works: serve runs redis-server on loopback or tailnet addresses only, with its data in --dir.
spill writes a value under <owner>:<name> with a required expiry; recall reads it back.
fn load and fn check install and verify the functions nova-table and nova-sprint call.
The password is read from the variable NOVA_REDIS_PASSWORD_ENV names, else NOVA_REDIS_PASSWORD.
first run: the --dry-run line needs no store; spill and recall need a Redis at 127.0.0.1:6379.

usage:
  nova-redis serve --bind <addr>[,<addr>...] --port <port> --dir <store-dir> [--dry-run]
  nova-redis spill --addr <host:port> [--user <name>] [--password-env <NAME>] --owner <owner> --name <name> --ttl <duration> --value <text> [--dry-run]
  nova-redis recall --addr <host:port> [--user <name>] [--password-env <NAME>] --owner <owner> --name <name>
  nova-redis fn load --addr <host:port> [--user <name>] [--password-env <NAME>]
  nova-redis fn check --addr <host:port> [--user <name>] [--password-env <NAME>]
  nova-redis acl render
  nova-redis acl check --addr <host:port> [--user <name>] [--password-env <NAME>]
  nova-redis acl apply --addr <host:port> [--user <name>] [--password-env <NAME>] [--password-env-for <user>=<VARIABLE>]... [--dry-run]
  nova-redis version
  nova-redis help [<verb>]

Every verb but serve, fn load, fn check, acl render, acl check, acl apply takes --json: the same result as one JSON object on stdout. A verb that lists takes --max <n> (default 20, 0 lists all) and says MORE for the rest. `<verb> -h` lists a verb's flags.

exit codes: 0 done (spill written, recall found, fn load done, fn check finds the library loaded, serve stopped); 1 ran and said NO (a recall of a missing, expired or unbounded key, fn check STALE or MISSING, a spill whose reply was lost, a refusal by the store, a serve that could not start); 2 could not run (a usage error, a flag refused before dialling, a store that did not answer or a login it refused).

example:
  nova-redis version
  nova-redis spill --dry-run --addr 127.0.0.1:6379 --owner ada --name note --ttl 10m --value hi
  nova-redis spill --addr 127.0.0.1:6379 --owner ada --name note --ttl 10m --value hi
  nova-redis recall --addr 127.0.0.1:6379 --owner ada --name note
[exit 0]

$ nova-redis serve -h
usage: nova-redis serve [flags]
from `nova-redis help`:
  nova-redis serve --bind <addr>[,<addr>...] --port <port> --dir <store-dir> [--dry-run]
--dry-run validates --bind, --port and --dir's absolute shape and prints the binding,
the port, the store directory and the persistence, auth and eviction rules it would use.
It defers every effect: it creates no directory, reads no password or environment,
looks up no redis-server and launches nothing.
flags:
  --bind <string>  comma-separated IP addresses to listen on, loopback (127.0.0.1, ::1) or tailnet (100.64.0.0/10, fd7a:115c:a1e0::/48) only
  --dir <string>  the absolute path of the store directory (AOF and RDB files), created 0700 when missing
  --dry-run  print what the verb would write and write nothing
  --port <string>  the TCP port to listen on, 1 to 65535 (6379 is Redis's own)
exit codes: 0 done (spill written, recall found, fn load done, fn check finds the library loaded, serve stopped); 1 ran and said NO (a recall of a missing, expired or unbounded key, fn check STALE or MISSING, a spill whose reply was lost, a refusal by the store, a serve that could not start); 2 could not run (a usage error, a flag refused before dialling, a store that did not answer or a login it refused).
effect: local write: writes files on this machine
[exit 0]

$ nova-redis serve --bind 127.0.0.1 --port 6379 --dir /work/redis --dry-run
SERVE OK bind=127.0.0.1 port=6379 auth=on persistence=aof eviction=none dir=/work/redis dry_run=true created=0 launched=0
[exit 0]

$ nova-redis serve --bind 127.0.0.1 --port 6379 --dir /work/redis > /work/redis.log 2>&1 &   # backgrounded

$ cat /work/redis.log | head -20
SERVE REFUSED: NOVA_REDIS_PASSWORD is empty; run under `nova-secrets exec --only NOVA_REDIS_PASSWORD -- nova-redis serve ...` so auth comes from nova-secrets at run time, never an argument; run: nova-redis help
[exit 0]

$ export NOVA_REDIS_PASSWORD=throwaway-in-container
$ nova-redis serve --bind 127.0.0.1 --port 6379 --dir /work/redis > /work/redis.log 2>&1 &   # backgrounded

$ cat /work/redis.log | head -20
SERVE START bind=127.0.0.1 port=6379 auth=on persistence=aof eviction=none dir=/work/redis program=/usr/local/bin/redis-server
3634:C 05 Oct 2026 19:27:57.317 * Reading config from stdin
3634:C 05 Oct 2026 19:27:57.317 # WARNING Memory overcommit must be enabled! Without it, a background save or replication may fail under low memory condition. Being disabled, it can also cause failures without low memory condition, see https://github.com/jemalloc/jemalloc/issues/1328. To fix this issue add 'vm.overcommit_memory = 1' to /etc/sysctl.conf and then reboot or run the command 'sysctl vm.overcommit_memory=1' for this to take effect.
3634:C 05 Oct 2026 19:27:57.317 * oO0OoO0OoO0Oo Redis is starting oO0OoO0OoO0Oo
3634:C 05 Oct 2026 19:27:57.317 * Redis version=8.10.2, bits=64, commit=00000000, modified=0, pid=3634, just started
3634:C 05 Oct 2026 19:27:57.317 * Configuration loaded
3634:M 05 Oct 2026 19:27:57.318 * monotonic clock: POSIX clock_gettime
3634:M 05 Oct 2026 19:27:57.318 * Running mode=standalone, port=6379.
3634:M 05 Oct 2026 19:27:57.319 * Server initialized
3634:M 05 Oct 2026 19:27:57.319 * BGSAVE done, 0 keys saved, 0 keys skipped, 89 bytes written.
3634:M 05 Oct 2026 19:27:57.333 * Creating AOF base file appendonly.aof.1.base.rdb on server start
3634:M 05 Oct 2026 19:27:57.353 * Creating AOF incr file appendonly.aof.1.incr.aof on server start
3634:M 05 Oct 2026 19:27:57.353 * Ready to accept connections tcp
[exit 0]

$ nova-sprint init -h
usage: nova-sprint init [--readers <a,b,...>] [--members <m1[:<width>],m2,...>] [--coordinator <name>] [--owner <name>] [--rules <file>]
from `nova-sprint help`:
  nova-sprint init [--readers <a,b,...>] [--members <m1[:<width>],m2,...>] [--coordinator <name>] [--owner <name>] [--rules <file>]
[trimmed: 18 more lines of `nova-sprint init -h`]
[exit 0]

$ export NOVA_SPRINT_REDIS=127.0.0.1:6379 NOVA_SPRINT_ACTOR=boss

$ nova-sprint init --readers reader-a --members m1:2
nova-sprint init REFUSED: redis at 127.0.0.1:6379 as the default user, no password: login refused: NOAUTH Authentication required.; next: name the user (NOVA_SPRINT_REDIS_USER) and the variable that holds its password; run: nova-sprint init -h
[exit 2]

$ nova-sprint help | grep -n -i -E "password|_USER|login|credential"
267:    checks no credential, so a name, a public address, a link-local address
272:NOVA_SPRINT_ACTOR, and needs no store address or credential: every verb is sent
[exit 0]

$ nova-redis help acl
usage: nova-redis acl <render|check|apply> [flags]
  nova-redis acl render
  nova-redis acl check --addr <host:port> [--user <name>] [--password-env <NAME>]
  nova-redis acl apply --addr <host:port> [--user <name>] [--password-env <NAME>] [--password-env-for <user>=<VARIABLE>]... [--dry-run]
`nova-redis acl <verb> -h` lists a verb's flags.
exit codes: 0 done (spill written, recall found, fn load done, fn check finds the library loaded, serve stopped); 1 ran and said NO (a recall of a missing, expired or unbounded key, fn check STALE or MISSING, a spill whose reply was lost, a refusal by the store, a serve that could not start); 2 could not run (a usage error, a flag refused before dialling, a store that did not answer or a login it refused).
[exit 0]

$ NOVA_SPRINT_REDIS_USER=default nova-sprint init --readers reader-a --members m1:2
nova-sprint init REFUSED: redis at 127.0.0.1:6379 as user default (password from NOVA_REDIS_BENCH_PASSWORD): login refused: NOVA_REDIS_BENCH_PASSWORD is empty; next: export NOVA_REDIS_BENCH_PASSWORD, holding the password, in the environment of this process; run: nova-sprint init -h
[exit 2]

$ NOVA_SPRINT_REDIS_USER=default NOVA_REDIS_BENCH_PASSWORD=$NOVA_REDIS_PASSWORD nova-sprint init --readers reader-a --members m1:2
nova-sprint init REFUSED: the store at 127.0.0.1:6379 holds no nova_sprint function library; run: nova-redis fn load --addr 127.0.0.1:6379
[exit 2]

$ export NOVA_SPRINT_REDIS_USER=default NOVA_REDIS_BENCH_PASSWORD=$NOVA_REDIS_PASSWORD

$ nova-redis fn load --addr 127.0.0.1:6379
LOADED nova_sprint sha=054aafe5df7a4aeb store=127.0.0.1:6379
[exit 0]

$ nova-sprint init --readers reader-a --members m1:2
INIT OK tables=work,readers,merge,fleet view=sprint readers=reader-a
MOVED m1 added, down until it beats width=2
FLEET-UP OK moved=1 refused=0 notes=0 op=fleet-release-dlx5idr61nbn-3688-89f1aa3e682831cd-1
STOPPED
[exit 0]

$ git config --global user.name stranger && git config --global user.email stranger@example.invalid && git config --global init.defaultBranch main && mkdir -p /work/t && cd /work/t && git init -q --bare origin.git && git clone -q origin.git work 2>&1; cd work && printf "one\ntwo\nthree\n" > lines.txt && git add lines.txt && git commit -q -m base && git push -q origin HEAD:main && git push -q origin HEAD:sprint/s1 && git -C /work/t/origin.git branch -a
warning: You appear to have cloned an empty repository.
* main
  sprint/s1
[exit 0]

$ nova-swarm template --name card
RESULT: <label> sha=<sha12>
REPO: <owner>/<name>
BASE: <branch>
The REPO: and BASE: lines are the repository and the branch the work starts from and lands on: the member stages REPO: at BASE:, and nova-sprint land merges the card's head onto BASE: (land --base stands in for a card naming no BASE:, land --repo-dir for one naming no REPO:).
You are a child of the coordinator: one task, one worktree, one branch, unattended. This card is the whole of the task and it stands alone in front of a stranger; nothing outside it is owed to you.
Deadline: finish within <n> minutes.

RULES.
Work only in the job directory this card names.
Never force-push or rebase a shared branch.
Never kill a process you did not start.
Never start a server on this machine.
No `rm -rf` outside the job directory.
Report what was not done.

THE TASK. <What is wrong or wanted, in a paragraph a stranger can act on, and the file or package the work lives in: internal/<package>/<file>.go. Name the worktree path, the branch, the base branch, and every file you may touch.>
Libraries considered: <what the standard library and the adopted modules offer for this work, and why each is used or not; the search comes before any helper of more than about thirty lines is written>

STEP 1. Enter your worktree with cd <worktree path> && git log --oneline -1; it is a NEW worktree on the branch this card names. Export GOFLAGS=-mod=readonly NOVA_TEST_NO_HOST=1 before any go command; GOCACHE is already set to the machine's shared build cache (JOB.md names it): keep it.
STEP 2. Write the red test first, named TestSomething, in <file>_test.go, opening with t.Parallel(). Run go test -count=1 -timeout 600s ./internal/<package>/ -run TestSomething and keep the failing line.
STEP 3. Make it pass in the files this card names, and only those. Cite the model or the design section from each function that implements a rule.
STEP 4. Run the gate: go test -count=1 -timeout 600s ./internal/<package>/ ./internal/ci/ and read the last line of each. When a test fails, name its file and say whether that file was changed by your work (yours) or is unchanged (already red at BASE: run the same test on the unchanged base to say so), and report that line first.
STEP 5. Commit on your own branch with the trailer. Nothing reaches the forge from inside the wall: in the job the git shim records a push, the pull request is the finish JOB.md names (STEP 6), and the member makes both, against <base>, from outside the wall when the card finishes. The pull request body states the diff stat, what was deleted, the tests with what each pins, and what was not done.
STEP 6. End as JOB.md says (docs/SPEC-CARD-CONTRACT.md): where JOB.md ends the card with its pull request, that is the end and there is nothing else to write, the gate's lines in the pull request body; where it asks for RESULT.md, write it in JOB.md's shape (head, branch, verdict, gate, output, report).
[exit 0]

[outside the container, into the mounted /work: briefs/c1.md, c2.md, c3.md written from the card template; c1.md is shown under the transcript]

$ nova-sprint preflight -h
usage: nova-sprint preflight --brief-dir <dir> [--repo-dir <dir>]
from `nova-sprint help`:
  nova-sprint preflight --brief-dir <dir> [--repo-dir <dir>]
[trimmed: 13 more lines of `nova-sprint preflight -h`]
[exit 0]

$ nova-sprint preflight --brief-dir /work/briefs
PREFLIGHT c1 FAIL PATHS: lines.txt overlaps c2 (lines.txt)
PREFLIGHT c1 FAIL PATHS: lines.txt overlaps c3 (lines.txt)
PREFLIGHT c1 FAIL TEST: no test named (TEST: [-tags <tags>] <package> <TestName>)
PREFLIGHT c2 FAIL PATHS: lines.txt overlaps c1 (lines.txt)
PREFLIGHT c2 FAIL PATHS: lines.txt overlaps c3 (lines.txt)
PREFLIGHT c2 FAIL TEST: no test named (TEST: [-tags <tags>] <package> <TestName>)
PREFLIGHT c3 FAIL PATHS: lines.txt overlaps c1 (lines.txt)
PREFLIGHT c3 FAIL PATHS: lines.txt overlaps c2 (lines.txt)
PREFLIGHT c3 FAIL TEST: no test named (TEST: [-tags <tags>] <package> <TestName>)
NOTE preflight read no --repo-dir: TEST and BASE are unchecked
PREFLIGHT FAIL briefs=3 failed=3
[exit 1]

[outside the container: briefs/c<n>.md changed to touch one file each, f<n>.txt line 1, PATHS: f<n>.txt]

$ cd /work/t/work && for w in one two three; do n=$((n+1)); echo $w > f$n.txt; done && git add f1.txt f2.txt f3.txt && git commit -q -m "three files" && git push -q origin HEAD:main HEAD:sprint/s1 && git log --oneline
9d15a7b three files
86ac759 base
[exit 0]

$ nova-sprint preflight --brief-dir /work/briefs --repo-dir /work/t/work
PREFLIGHT c1 FAIL TEST: no test named (TEST: [-tags <tags>] <package> <TestName>)
PREFLIGHT c1 FAIL BASE: sprint/s1 is not in /work/t/work
PREFLIGHT c2 FAIL TEST: no test named (TEST: [-tags <tags>] <package> <TestName>)
PREFLIGHT c2 FAIL BASE: sprint/s1 is not in /work/t/work
PREFLIGHT c3 FAIL TEST: no test named (TEST: [-tags <tags>] <package> <TestName>)
PREFLIGHT c3 FAIL BASE: sprint/s1 is not in /work/t/work
PREFLIGHT FAIL briefs=3 failed=3
[exit 1]

$ git -C /work/t/work fetch -q origin && git -C /work/t/work branch -a
* main
  remotes/origin/main
  remotes/origin/sprint/s1
[exit 0]

$ nova-sprint preflight --brief-dir /work/briefs --repo-dir /work/t/work
PREFLIGHT c1 FAIL TEST: no test named (TEST: [-tags <tags>] <package> <TestName>)
PREFLIGHT c1 FAIL BASE: sprint/s1 is not in /work/t/work
PREFLIGHT c2 FAIL TEST: no test named (TEST: [-tags <tags>] <package> <TestName>)
PREFLIGHT c2 FAIL BASE: sprint/s1 is not in /work/t/work
PREFLIGHT c3 FAIL TEST: no test named (TEST: [-tags <tags>] <package> <TestName>)
PREFLIGHT c3 FAIL BASE: sprint/s1 is not in /work/t/work
PREFLIGHT FAIL briefs=3 failed=3
[exit 1]

$ nova-sprint add -h
usage: nova-sprint add --stream <s> (<id>... | --count <n> | --sentinel <id> | --brief-dir <dir> | --brief-file <f1> [--brief-file <f2>...]: a card per file, its id the file's name without .md) [--needs <a,b>] [--before <id> | --after <id> | --score <n>] [--brief <text> | --brief-file <path>: once, the brief of the cards named] [--rules <file>] [--held] [--allow-shared-paths] [--one: a single card is meant] [--replaces <old-id>[,<old-id>]: the one card is their twin]
from `nova-sprint help`:
  nova-sprint add --stream <s> (<id>... | --count <n> | --sentinel <id> | --brief-dir <dir> | --brief-file <f1> [--brief-file <f2>...]: a card per file, its id the file's name without .md) [--needs <a,b>] [--before <id> | --after <id> | --score <n>] [--brief <text> | --brief-file <path>: once, the brief of the cards named] [--rules <file>] [--held] [--allow-shared-paths] [--one: a single card is meant] [--replaces <old-id>[,<old-id>]: the one card is their twin]
[trimmed: 60 more lines of `nova-sprint add -h`]
[exit 0]

$ git -C /work/t/work branch -q sprint/s1 origin/sprint/s1 && nova-sprint preflight --brief-dir /work/briefs --repo-dir /work/t/work
PREFLIGHT c1 FAIL TEST: no test named (TEST: [-tags <tags>] <package> <TestName>)
PREFLIGHT c2 FAIL TEST: no test named (TEST: [-tags <tags>] <package> <TestName>)
PREFLIGHT c3 FAIL TEST: no test named (TEST: [-tags <tags>] <package> <TestName>)
PREFLIGHT FAIL briefs=3 failed=3
[exit 1]

$ nova-sprint add --stream s1 --brief-dir /work/briefs
MOVED c1 -> ready stream=s1 score=1
MOVED c2 -> ready stream=s1 score=2
MOVED c3 -> ready stream=s1 score=3
ADD OK stream=s1 cards=3 before=- moved=3 refused=0 notes=0 op=add-dlx5irnnmn1w-3822-e8cc505e8f704af0-1
NOTE each card's id is its brief file's name without .md (/work/briefs/c1.md is c1)
STOPPED  0/3 0.0%
[exit 0]

$ nova-sprint start
START OK before=STOPPED after=RUNNING changed
nothing is ticking: run: nova-sprint run
0/3 0.0% -> ETA -  machine: running
[exit 0]

$ nova-sprint run -h
usage: nova-sprint run [--answer-rules=false] [--idle-alarm=false] [--listen <address:port>] [--land] [--decide <dir>]
from `nova-sprint help`:
  nova-sprint run [--answer-rules=false] [--idle-alarm=false] [--listen <address:port>] [--land] [--decide <dir>]
[trimmed: 25 more lines of `nova-sprint run -h`]
[exit 0]

$ nova-sprint run --listen 127.0.0.1:7400 --land > /work/run.log 2>&1 &   # backgrounded

$ head -30 /work/run.log
SERVER listening on 127.0.0.1:7400: the workers' verbs (take, finish, read, queue, fleet beat) run here, one at a time, beside the store
SERVER listening on 127.0.0.1:7400: the coordinator's verbs, from this machine (NOVA_SPRINT_SERVER=127.0.0.1:7400)
BALANCE every 10m0s: each provider's balance is read through the seat's key and written to the fleet table
LANDING every 2s: land runs here for every stream with cards queued to merge, one landing at a time
RUN ticking on every line of the log (at most every 100ms) and every 1s while it is quiet; machine: running
19:29:02 machine RUNNING
19:29:02 tick
TABLES rows changed: work=0 readers=0 merge=0 fleet=0
NOTE the twin read merge, fleet whole: not held after an earlier read of it was cut short
TICK OK state=RUNNING idle=no moved=0 notes=1
TIMES 10ms trips=104 reads=2 rows=9 stale=0 mismatch=0: look=0ms/5t/0r/0n fleet-display=0ms/3t/0r/1n first-read=0ms/5t/2r/2n work/deal=1ms/15t/0r/2n work/accept=0ms/3t/0r/0n readers/ask=0ms/3t/0r/0n merge/resume=1ms/8t/0r/2n fleet/presence=0ms/8t/0r/2n check=0ms/3t/0r/0n deadlines=0ms/3t/0r/0n rule-return=0ms/3t/0r/0n rule-resume=0ms/3t/0r/0n rule-rework=0ms/3t/0r/0n rule-late=0ms/3t/0r/0n rule-brief=0ms/3t/0r/0n overdue=0ms/3t/0r/0n idle=0ms/3t/0r/0n done=0ms/3t/0r/0n remind=0ms/2t/0r/0n where=0ms/6t/0r/0n tick-end=0ms/15t/0r/0n heartbeat=0ms/1t/0r/0n
0/3 0.0% -> ETA -  machine: running
[trimmed: 18 more lines, the same idle tick repeating]
[exit 0]

$ export NOVA_SPRINT_SERVER=127.0.0.1:7400

$ nova-sprint fleet beat m1
FLEET-BEAT OK m1 at=2026-10-05T19:29:12Z load=6.4% last=6.4% how=load1 cores=16
[exit 0]

$ nova-sprint queue --as m1
CARD c1.w1 fleet:m1:ready gen=1 take: c1.w1@1 --epoch 0 dealt=2026-10-05T19:29:13Z
PACKET c1.w1 attempt=1 gen=1 epoch=0
  branch: sprint/c1.w1.g1.e0
  base: the stream's base
  brief:
    [trimmed: 21 lines of the brief, as written in the brief file]
  notes: none
  report it: nova-sprint finish --as m1 c1.w1@1 --epoch 0 --branch sprint/c1.w1.g1.e0 --head <commit> --report '<what you did>' [--failed]
CARD c2.w1 fleet:m1:ready gen=1 take: c2.w1@1 --epoch 0 dealt=2026-10-05T19:29:13Z
PACKET c2.w1 attempt=1 gen=1 epoch=0
  branch: sprint/c2.w1.g1.e0
  base: the stream's base
  brief:
    [trimmed: 21 lines of the brief, as written in the brief file]
  notes: none
  report it: nova-sprint finish --as m1 c2.w1@1 --epoch 0 --branch sprint/c2.w1.g1.e0 --head <commit> --report '<what you did>' [--failed]
CARD c3.w1 fleet:m1:ready gen=1 take: c3.w1@1 --epoch 0 dealt=2026-10-05T19:29:13Z
PACKET c3.w1 attempt=1 gen=1 epoch=0
  branch: sprint/c3.w1.g1.e0
  base: the stream's base
  brief:
    [trimmed: 21 lines of the brief, as written in the brief file]
  notes: none
  report it: nova-sprint finish --as m1 c3.w1@1 --epoch 0 --branch sprint/c3.w1.g1.e0 --head <commit> --report '<what you did>' [--failed]
QUEUE OK cards=3 epoch=0
[exit 0]

$ nova-sprint fleet beat m1 && nova-sprint take --as m1 c1.w1@1 c2.w1@1 c3.w1@1 --epoch 0
FLEET-BEAT OK m1 at=2026-10-05T19:29:19Z load=6.4% last=0.7% how=cpu cores=16
0/3 0.0% -> ETA -  machine: running
REFUSED c3.w1: member m1 is at its width (0 working of 2): a card is taken when one is reported
REFUSED c1.w1: not written: the verb names several and applies all or none, and 1 of them was refused
REFUSED c2.w1: not written: the verb names several and applies all or none, and 1 of them was refused
TAKE-BY-ID FAILED moved=0 refused=3 notes=0
[exit 1]

$ cd /work/t/work && for i in 1 2 3; do w=$(cat f$i.txt); git checkout -q -B c$i origin/sprint/s1 && echo $w-done > f$i.txt && git commit -q -am c$i && git push -q origin HEAD:sprint/c$i.w1.g1.e0 && echo c$i $(git rev-parse HEAD); done
c1 41977d1e85a45ea20362ed955b3e7fa0fdf4c5b9
c2 25d34df2542d7dcc54073943ef5ea989205d5744
c3 27688e2950e4e3fcb3097fef2f96e13ffa193ad3
[exit 0]

$ nova-sprint fleet beat m1 >/dev/null && nova-sprint take --as m1 c1.w1@1 c2.w1@1 --epoch 0
MOVED c1.w1 fleet ready -> working member=m1 gen=1
MOVED c2.w1 fleet ready -> working member=m1 gen=1
PACKET c1.w1 attempt=1 gen=1 epoch=0
  branch: sprint/c1.w1.g1.e0
  base: the stream's base
  brief:
    [trimmed: 21 lines of the brief, as written in the brief file]
  notes: none
  report it: nova-sprint finish --as m1 c1.w1@1 --epoch 0 --branch sprint/c1.w1.g1.e0 --head <commit> --report '<what you did>' [--failed]
PACKET c2.w1 attempt=1 gen=1 epoch=0
  branch: sprint/c2.w1.g1.e0
  base: the stream's base
  brief:
    [trimmed: 21 lines of the brief, as written in the brief file]
  notes: none
  report it: nova-sprint finish --as m1 c2.w1@1 --epoch 0 --branch sprint/c2.w1.g1.e0 --head <commit> --report '<what you did>' [--failed]
TAKE-BY-ID OK moved=2 refused=0 notes=0 op=take-dlx5j7ro3ojz-3846-6de8afe4c1ee3cd1-1
0/3 0.0% -> ETA -  machine: running
[exit 0]

$ nova-sprint finish --as m1 c1.w1@1 --epoch 0 --branch sprint/c1.w1.g1.e0 --head 41977d1e85a45ea20362ed955b3e7fa0fdf4c5b9 --report "f1.txt line 1 one -> one-done"
MOVED c1.w1 working -> done ok; c1 working -> review
FINISH OK moved=1 refused=0 notes=1 op=finish-dlx5j7uzeora-3846-a34b071791b006c3-1
0/3 0.0% -> ETA -  machine: running
[exit 0]

$ nova-sprint finish --as m1 c2.w1@1 --epoch 0 --branch sprint/c2.w1.g1.e0 --head 25d34df2542d7dcc54073943ef5ea989205d5744 --report "f2.txt line 1 two -> two-done"
MOVED c2.w1 working -> done ok; c2 working -> review
FINISH OK moved=1 refused=0 notes=1 op=finish-dlx5j7y1yljd-3846-6e3a84bfd8e56a9d-1
0/3 0.0% -> ETA -  machine: running
[exit 0]

$ nova-sprint fleet beat m1 >/dev/null && nova-sprint take --as m1 c3.w1@1 --epoch 0
MOVED c3.w1 fleet ready -> working member=m1 gen=1
PACKET c3.w1 attempt=1 gen=1 epoch=0
  branch: sprint/c3.w1.g1.e0
  base: the stream's base
  brief:
    [trimmed: 21 lines of the brief, as written in the brief file]
  notes: none
  report it: nova-sprint finish --as m1 c3.w1@1 --epoch 0 --branch sprint/c3.w1.g1.e0 --head <commit> --report '<what you did>' [--failed]
TAKE-BY-ID OK moved=1 refused=0 notes=0 op=take-dlx5j81lisqp-3846-0c395a8b0a1799e6-1
0/3 0.0% -> ETA -  machine: running
[exit 0]

$ nova-sprint finish --as m1 c3.w1@1 --epoch 0 --branch sprint/c3.w1.g1.e0 --head 27688e2950e4e3fcb3097fef2f96e13ffa193ad3 --report "f3.txt line 1 three -> three-done"
MOVED c3.w1 working -> done ok; c3 working -> review
FINISH OK moved=1 refused=0 notes=1 op=finish-dlx5j85147zk-3846-ea757d5d92d0ec8d-1
0/3 0.0% -> ETA -  machine: running
[exit 0]

$ nova-sprint queue --as reader-a | grep -v "^    "
QUEUE OK cards=0 epoch=0
[exit 0]

$ nova-sprint read --as reader-a --begin --epoch 0 --max 0
READ OK moved=0 refused=0 notes=0
NOTE reader-a read nothing: it holds no read card asked; run: nova-sprint queue --as reader-a
0/3 0.0% -> ETA -  machine: running
[exit 0]

$ nova-sprint read --as reader-a --ok --epoch 0 --max 0
READ OK moved=0 refused=0 notes=0
NOTE reader-a read nothing: it holds no read card reading; run: nova-sprint queue --as reader-a
0/3 0.0% -> ETA -  machine: running
[exit 0]

$ nova-sprint where
SPRINT TABLE  coordinator boss

0/3 0.0% -> ETA -

work | waiting | ready | working | review | merging | landed | cost | per landed
-----+---------+-------+---------+--------+---------+--------+------+-----------
s1   |       0 |     0 |       0 |      3 |       0 |      0 |    - | -
-----+---------+-------+---------+--------+---------+--------+------+-----------
     |       0 |     0 |       0 |      3 |       0 |      0 |    - |

friends | ready | working | width | done | ok%  | status | active
--------+-------+---------+-------+------+------+--------+-------
        |     0 |       0 |     0 |    0 | 0.0% |        |

fleet | ready | working | width | done | ok%    | status | load
------+-------+---------+-------+------+--------+--------+-----
m1    |     0 |       0 |     2 |    3 | 100.0% | up     | 3.6%
------+-------+---------+-------+------+--------+--------+-----
      |     0 |       0 |     2 |    3 | 100.0% |        |

store: rtt p50=0.289ms p99=0.294ms
[exit 0]

$ nova-sprint queue --as reader-a | grep -v "^    "
CARD c1.r1.reader-a readers:reader-a:asked asked=2026-10-05T19:29:32Z
PACKET c1.r1.reader-a attempt=1 gen=0 epoch=0
  work: attempt 1 by m1
  head: 41977d1e85a45ea20362ed955b3e7fa0fdf4c5b9
  branch: sprint/c1.w1.g1.e0
  brief:
  report:
  notes: none
  attribution: the By: line, the Co-Authored-By trailer and the model or harness named are never a finding and never decide a verdict
  report it: nova-sprint read --as reader-a (--ok | --broken) c1.r1.reader-a --epoch 0 --finding '<file:line, and what to change>'
CARD c2.r1.reader-a readers:reader-a:asked asked=2026-10-05T19:29:32Z
PACKET c2.r1.reader-a attempt=1 gen=0 epoch=0
  work: attempt 1 by m1
  head: 25d34df2542d7dcc54073943ef5ea989205d5744
  branch: sprint/c2.w1.g1.e0
  brief:
  report:
  notes: none
  attribution: the By: line, the Co-Authored-By trailer and the model or harness named are never a finding and never decide a verdict
  report it: nova-sprint read --as reader-a (--ok | --broken) c2.r1.reader-a --epoch 0 --finding '<file:line, and what to change>'
CARD c3.r1.reader-a readers:reader-a:asked asked=2026-10-05T19:29:32Z
PACKET c3.r1.reader-a attempt=1 gen=0 epoch=0
  work: attempt 1 by m1
  head: 27688e2950e4e3fcb3097fef2f96e13ffa193ad3
  branch: sprint/c3.w1.g1.e0
  brief:
  report:
  notes: none
  attribution: the By: line, the Co-Authored-By trailer and the model or harness named are never a finding and never decide a verdict
  report it: nova-sprint read --as reader-a (--ok | --broken) c3.r1.reader-a --epoch 0 --finding '<file:line, and what to change>'
QUEUE OK cards=3 epoch=0
[exit 0]

$ nova-sprint inbox
HAPPENED start-dlx5itmgbvwp-3831-076b9c49e0debf52-1.1   the machine started  x1  STOPPED -> RUNNING by boss
HAPPENED tick-presence-dlx5j1mp73sp-3846-a1d3cf6e590d5698-1.1   fleet member up  x1  m1 up: it beats
DECIDED tick-deal-dlx5j1o767re-3846-2774c1180a9e8257-1.d1   no fleet member is up  size=1  (stream:)  answered by tick deal
HAPPENED finish-dlx5j7uzeora-3846-a34b071791b006c3-1.1   work came back ok  stream=s1  size=3  (c1,c2,c3)
DECIDED tick-ask-dlx5ja1c4o9y-3846-20cb1c7b513cdd4b-1.d1   fewer than two readers up  size=1  (stream:)  answered by tick ask
INBOX OK judgments=0 happened=5 cursor=-
machine: running
[exit 0]

$ nova-sprint card c1 | head -40
c1   stream s1   review   attempt 1   head 41977d1e85a45ea20362ed955b3e7fa0fdf4c5b9   branch sprint/c1.w1.g1.e0   first in line in s1

now:
  reader reader-a holds c1.r1.reader-a (asked), 9s of 30m0s running

brief:
    [trimmed: 21 lines of the brief, as written in the brief file]

timeline:
  19:28:52  c1 added to s1 by boss

  attempt 1
  19:29:13  attempt 1 dealt to m1
  19:29:27  2 cards: m1 took attempt 1 (with c2.w1)
  19:29:27  m1 finished attempt 1: ok, head 41977d1e85a45ea20362ed955b3e7fa0fdf4c5b9 on sprint/c1.w1.g1.e0. "f1.txt line 1 one -> one-done"
  19:29:27  c1 is back for review
  19:29:32  reader-a asked to read attempt 1 by the machine

reports and findings:
  19:29:27  attempt 1, report by m1 (ok):
[exit 0]

$ cd /work/t/work && git fetch -q origin && for i in 1 2 3; do git diff --stat origin/sprint/s1 origin/sprint/c$i.w1.g1.e0; done
 f1.txt | 2 +-
 1 file changed, 1 insertion(+), 1 deletion(-)
 f2.txt | 2 +-
 1 file changed, 1 insertion(+), 1 deletion(-)
 f3.txt | 2 +-
 1 file changed, 1 insertion(+), 1 deletion(-)
[exit 0]

$ nova-sprint read --as reader-a --begin --epoch 0 --max 0
MOVED c1.r1.reader-a asked -> reading
READ OK moved=1 refused=0 notes=0 op=read-dlx5jgzms8ju-3846-a6beac6fab416714-1
0/3 0.0% -> ETA -  machine: running
[exit 0]

$ nova-sprint read --as reader-a --ok c1.r1.reader-a c2.r1.reader-a c3.r1.reader-a --epoch 0
MOVED c1.r1.reader-a reading -> ok
MOVED c2.r1.reader-a asked -> ok
MOVED c3.r1.reader-a asked -> ok
READ OK moved=3 refused=0 notes=0 op=read-dlx5jh379bqs-3846-6c52c05f2cfbbf47-1
0/3 0.0% -> ETA -  machine: running
[exit 0]

$ nova-sprint where
SPRINT TABLE  coordinator boss

0/3 0.0% -> ETA -

work | waiting | ready | working | review | merging | landed | cost | per landed
-----+---------+-------+---------+--------+---------+--------+------+-----------
s1   |       0 |     0 |       0 |      0 |       3 |      0 |    - | -
-----+---------+-------+---------+--------+---------+--------+------+-----------
     |       0 |     0 |       0 |      0 |       3 |      0 |    - |

friends | ready | working | width | done | ok%  | status | active
--------+-------+---------+-------+------+------+--------+-------
        |     0 |       0 |     0 |    0 | 0.0% |        |

fleet | ready | working | width | done | ok%    | status | load
------+-------+---------+-------+------+--------+--------+-----
m1    |     0 |       0 |     2 |    3 | 100.0% | up     |
------+-------+---------+-------+------+--------+--------+-----
      |     0 |       0 |     2 |    3 | 100.0% |        |

store: rtt p50=0.275ms p99=0.294ms
[exit 0]

$ grep -E "^(LAND|MERGE|REFUSED|ERROR)|LAND" /work/run.log | head -30
LANDING every 2s: land runs here for every stream with cards queued to merge, one landing at a time
19:29:48 LAND REFUSED stream=s1 cards=3 base=sprint/s1 tip=- ids=c1..c3 repo=https://github.com/stranger/throwaway.git reason=paused: the merge queue of sprint/s1 could not be read (exec: "gh": executable file not found in $PATH), and an unreadable queue is not an empty one; the cards stay queued and land when it is read clear
19:29:48 LAND DONE batches=0 cards=0 refused=1; run: nova-sprint where
[exit 0]

$ nova-sprint land --stream s1 --repo-dir /work/t/work --dry-run
LAND OK stream=s1 cards=3 base=sprint/s1 tip=- ids=c1..c3 repo=https://github.com/stranger/throwaway.git dir=/work/t/work branches_queued=3 dry_run=yes
LAND DONE batches=1 cards=3 refused=0 dry_run=yes; run: nova-sprint where
[exit 0]

$ grep -c "LAND REFUSED" /work/run.log
1
[exit 0]

$ nova-sprint land --stream s1 --repo-dir /work/t/work
LAND REFUSED stream=s1 cards=3 base=sprint/s1 tip=- ids=c1..c3 repo=https://github.com/stranger/throwaway.git reason=paused: the merge queue of sprint/s1 could not be read (exec: "gh": executable file not found in $PATH), and an unreadable queue is not an empty one; the cards stay queued and land when it is read clear
NOTE nothing was pushed or reported for stream s1; its cards stay queued
LAND DONE batches=0 cards=0 refused=1; run: nova-sprint where
[exit 1]

[outside the container: REPO: in briefs/c*.md changed from stranger/throwaway to /work/t/origin.git]

$ nova-sprint brief c1 --brief-file /work/briefs/c1.md
0/3 0.0% -> ETA -  machine: running
REFUSED c1: c1 is merging: a card dealt, working, in review, merging or landed keeps its brief; nothing was changed; run: nova-sprint return c1 --reason '<why>', then nova-sprint rework c1 --fix '<what changes>' (the next attempt's fix), or nova-sprint drop c1 --reason '<why>', then nova-sprint add --stream s1 <new id> --brief-file <path>
BRIEF FAILED moved=0 refused=1 notes=0
[exit 1]

$ nova-sprint drop c1 c2 c3 --reason "REPO: named a GitHub slug; the throwaway origin is the bare repository /work/t/origin.git"
MOVED c1 merging -> off the table (REPO: named a GitHub slug; the throwaway origin is the bare repository /work/t/origin.git)
MOVED c2 merging -> off the table (REPO: named a GitHub slug; the throwaway origin is the bare repository /work/t/origin.git)
MOVED c3 merging -> off the table (REPO: named a GitHub slug; the throwaway origin is the bare repository /work/t/origin.git)
DROP OK moved=3 refused=0 notes=0 op=drop-dlx5k1p8q3c5-3846-bbfeb2eda716affc-1
0/3 0.0% -> ETA -  machine: running
[exit 0]

[outside the container: briefs2/d1.md, d2.md, d3.md copied from briefs/c*.md with the ids c1..c3 renamed d1..d3]

$ nova-sprint add --stream s1 --brief-dir /work/briefs2
MOVED d1 -> ready stream=s1 score=1
MOVED d2 -> ready stream=s1 score=2
MOVED d3 -> ready stream=s1 score=3
ADD OK stream=s1 cards=3 before=- moved=3 refused=0 notes=0 op=add-dlx5k1slbirp-3846-db14cf7ac92b5bf1-1
NOTE each card's id is its brief file's name without .md (/work/briefs2/d1.md is d1)
STOPPED  0/3 0.0%
[exit 0]

$ nova-sprint inbox | head -3
JUDGMENT machine:stopped ! the machine is STOPPED and moves are due  size=0  waited=0s  due=19:30:37
  the machine is STOPPED and 3 moves are due
  start:
[exit 0]

$ nova-sprint start
START OK before=STOPPED after=RUNNING changed
0/3 0.0% -> ETA -  machine: running
[exit 0]

$ nova-sprint fleet beat m1 >/dev/null; sleep 2; nova-sprint queue --as m1 | grep -E "^(CARD|QUEUE)"
CARD d1.w1 fleet:m1:ready gen=1 take: d1.w1@1 --epoch 0 dealt=2026-10-05T19:30:38Z
CARD d2.w1 fleet:m1:ready gen=1 take: d2.w1@1 --epoch 0 dealt=2026-10-05T19:30:38Z
CARD d3.w1 fleet:m1:ready gen=1 take: d3.w1@1 --epoch 0 dealt=2026-10-05T19:30:38Z
QUEUE OK cards=3 epoch=0
[exit 0]

$ cd /work/t/work && for i in 1 2 3; do git checkout -q -B d$i origin/sprint/s1 && sed -i 1s/$/-done/ f$i.txt && git commit -q -am d$i && git push -q origin HEAD:sprint/d$i.w1.g1.e0 && echo d$i $(git rev-parse HEAD) $(cat f$i.txt); done
d1 6312194a63e45516271994edc34d816c87da95d7 one-done
d2 18f19016773339cb8ada5b0b6f8cd890dc4b5806 two-done
d3 67f6fc0939f302a9d657fef6ed7b7568eb55b706 three-done
[exit 0]

$ nova-sprint fleet beat m1 >/dev/null && nova-sprint take --as m1 d1.w1@1 d2.w1@1 --epoch 0 | grep -v "^    "
MOVED d1.w1 fleet ready -> working member=m1 gen=1
MOVED d2.w1 fleet ready -> working member=m1 gen=1
PACKET d1.w1 attempt=1 gen=1 epoch=0
  branch: sprint/d1.w1.g1.e0
  base: the stream's base
  brief:
  notes: none
  report it: nova-sprint finish --as m1 d1.w1@1 --epoch 0 --branch sprint/d1.w1.g1.e0 --head <commit> --report '<what you did>' [--failed]
PACKET d2.w1 attempt=1 gen=1 epoch=0
  branch: sprint/d2.w1.g1.e0
  base: the stream's base
  brief:
  notes: none
  report it: nova-sprint finish --as m1 d2.w1@1 --epoch 0 --branch sprint/d2.w1.g1.e0 --head <commit> --report '<what you did>' [--failed]
TAKE-BY-ID OK moved=2 refused=0 notes=0 op=take-dlx5kao21rgt-3846-8c37974066167d4d-1
0/3 0.0% -> ETA -  machine: running
[exit 0]

$ nova-sprint finish --as m1 d1.w1@1 --epoch 0 --branch sprint/d1.w1.g1.e0 --head 6312194a63e45516271994edc34d816c87da95d7 --report "f1.txt line 1 one -> one-done"
MOVED d1.w1 working -> done ok; d1 working -> review
FINISH OK moved=1 refused=0 notes=1 op=finish-dlx5kara8txr-3846-9d7b3cdbb9a312ce-1
0/3 0.0% -> ETA -  machine: running
[exit 0]

$ nova-sprint finish --as m1 d2.w1@1 --epoch 0 --branch sprint/d2.w1.g1.e0 --head 18f19016773339cb8ada5b0b6f8cd890dc4b5806 --report "f2.txt line 1 two -> two-done"
MOVED d2.w1 working -> done ok; d2 working -> review
FINISH OK moved=1 refused=0 notes=1 op=finish-dlx5kauoecv7-3846-3227336600efd279-1
0/3 0.0% -> ETA -  machine: running
[exit 0]

$ nova-sprint fleet beat m1 >/dev/null && nova-sprint take --as m1 d3.w1@1 --epoch 0 | grep -v "^    "
MOVED d3.w1 fleet ready -> working member=m1 gen=1
PACKET d3.w1 attempt=1 gen=1 epoch=0
  branch: sprint/d3.w1.g1.e0
  base: the stream's base
  brief:
  notes: none
  report it: nova-sprint finish --as m1 d3.w1@1 --epoch 0 --branch sprint/d3.w1.g1.e0 --head <commit> --report '<what you did>' [--failed]
TAKE-BY-ID OK moved=1 refused=0 notes=0 op=take-dlx5kaxvn4g0-3846-bbf5b64f820f6092-1
0/3 0.0% -> ETA -  machine: running
[exit 0]

$ nova-sprint finish --as m1 d3.w1@1 --epoch 0 --branch sprint/d3.w1.g1.e0 --head 67f6fc0939f302a9d657fef6ed7b7568eb55b706 --report "f3.txt line 1 three -> three-done"
MOVED d3.w1 working -> done ok; d3 working -> review
FINISH OK moved=1 refused=0 notes=1 op=finish-dlx5kb17gdtu-3846-5edc7f74aee4aa64-1
0/3 0.0% -> ETA -  machine: running
[exit 0]

$ nova-sprint queue --as reader-a | grep -E "^(CARD|QUEUE)"
QUEUE OK cards=0 epoch=0
[exit 0]

$ nova-sprint queue --as reader-a | grep -E "^(CARD|QUEUE)"
CARD d1.r1.reader-a readers:reader-a:asked asked=2026-10-05T19:30:55Z
CARD d2.r1.reader-a readers:reader-a:asked asked=2026-10-05T19:30:55Z
CARD d3.r1.reader-a readers:reader-a:asked asked=2026-10-05T19:30:55Z
QUEUE OK cards=3 epoch=0
[exit 0]

$ cd /work/t/work && git fetch -q origin && for i in 1 2 3; do git diff origin/sprint/s1 origin/sprint/d$i.w1.g1.e0 | grep "^[-+][a-z]"; done
-one
+one-done
-two
+two-done
-three
+three-done
[exit 0]

$ nova-sprint read --as reader-a --begin d1.r1.reader-a d2.r1.reader-a d3.r1.reader-a --epoch 0
MOVED d1.r1.reader-a asked -> reading
MOVED d2.r1.reader-a asked -> reading
MOVED d3.r1.reader-a asked -> reading
READ OK moved=3 refused=0 notes=0 op=read-dlx5kfobirs3-3846-5aad4a8a6babc8f4-1
0/3 0.0% -> ETA -  machine: running
[exit 0]

$ nova-sprint read --as reader-a --ok d1.r1.reader-a d2.r1.reader-a d3.r1.reader-a --epoch 0
MOVED d1.r1.reader-a reading -> ok
MOVED d2.r1.reader-a reading -> ok
MOVED d3.r1.reader-a reading -> ok
READ OK moved=3 refused=0 notes=0 op=read-dlx5kfrkp7c8-3846-13ae3caf5dc606f4-1
0/3 0.0% -> ETA -  machine: running
[exit 0]

$ grep -E "LAND" /work/run.log | tail -6
LANDING every 2s: land runs here for every stream with cards queued to merge, one landing at a time
19:29:48 LAND REFUSED stream=s1 cards=3 base=sprint/s1 tip=- ids=c1..c3 repo=https://github.com/stranger/throwaway.git reason=paused: the merge queue of sprint/s1 could not be read (exec: "gh": executable file not found in $PATH), and an unreadable queue is not an empty one; the cards stay queued and land when it is read clear
19:29:48 LAND DONE batches=0 cards=0 refused=1; run: nova-sprint where
19:31:04 LAND OK stream=s1 cards=3 base=sprint/s1 tip=93fe3f457c806ec1138c3f963595019938b3be6e ids=d1..d3 repo=/work/t/origin.git dir=/work/home/.cache/nova-sprint/land/work-t-origin-eeee3b3be9f21c85 fetch=0.0s merge=0.0s check=0.0s queue=0.0s push=0.0s report=0.0s branches_queued=3
[exit 0]

$ nova-sprint where
SPRINT TABLE  coordinator boss

DONE

work | waiting | ready | working | review | merging | landed | cost | per landed
-----+---------+-------+---------+--------+---------+--------+------+-----------
s1   |       0 |     0 |       0 |      0 |       0 |      3 |    - | -
-----+---------+-------+---------+--------+---------+--------+------+-----------
     |       0 |     0 |       0 |      0 |       0 |      3 |    - |

friends | ready | working | width | done | ok%  | status | active
--------+-------+---------+-------+------+------+--------+-------
        |     0 |       0 |     0 |    0 | 0.0% |        |

fleet | ready | working | width | done | ok%    | status | load
------+-------+---------+-------+------+--------+--------+-----
m1    |     0 |       0 |     2 |    6 | 100.0% | up     |
------+-------+---------+-------+------+--------+--------+-----
      |     0 |       0 |     2 |    6 | 100.0% |        |

store: rtt p50=0.231ms p99=0.299ms
[exit 0]

$ git -C /work/t/origin.git log --oneline --graph sprint/s1 | head -12; git -C /work/t/origin.git show sprint/s1:f1.txt sprint/s1:f2.txt sprint/s1:f3.txt
*   93fe3f4 land d3 (sprint stream s1)
|\  
| * 67f6fc0 d3
* |   f86ab8d land d2 (sprint stream s1)
|\ \  
| * | 18f1901 d2
| |/  
* |   f3c8cb3 land d1 (sprint stream s1)
|\ \  
| |/  
|/|   
| * 6312194 d1
one-done
two-done
three-done
[exit 0]

$ nova-sprint inbox
HAPPENED tick-done-dlx5k1pe13nh-3846-5231ccc2430ed939-1.1   the sprint is done  x2  for=boss  3 landed, 3 dropped, took 2m8s from the first start
  to continue: add work, then nova-sprint start
HAPPENED tick-accept-dlx5jh3a9yzl-3846-12b98af778c207c8-1.2   ready to merge  stream=s1  size=6  for=boss  (c1,c2,c3,d1,d2,d3)  3 accepted and queued to merge: d1 d2 d3; run: nova-sprint land --stream s1 (a nova-sprint run started with --land lands them itself)
HAPPENED start-dlx5itmgbvwp-3831-076b9c49e0debf52-1.1   the machine started  x2  STOPPED -> RUNNING by boss
HAPPENED tick-presence-dlx5j1mp73sp-3846-a1d3cf6e590d5698-1.1   fleet member up  x2  m1 up: it beats
DECIDED tick-deal-dlx5j1o767re-3846-2774c1180a9e8257-1.d1   no fleet member is up  size=1  (stream:)  answered by tick deal
HAPPENED finish-dlx5j7uzeora-3846-a34b071791b006c3-1.1   work came back ok  stream=s1  size=6  (c1,c2,c3,d1,d2,d3)
DECIDED tick-ask-dlx5ja1c4o9y-3846-20cb1c7b513cdd4b-1.d1   fewer than two readers up  size=1  (stream:)  answered by tick ask
HAPPENED tick-accept-dlx5jh3a9yzl-3846-12b98af778c207c8-1.1   stream started merging  stream=s1  x2
HAPPENED tick-presence-dlx5jspzq3j2-3846-3b57dcb5d0f15c76-1.1   fleet member down  x1  m1 down: no beat for 45s
HAPPENED merge-dlx5kgmgyqw2-3846-9ee4963c04a66954-1.1   batch landed  stream=s1  size=3  (d1,d2,d3)  ci green
HAPPENED merge-dlx5kgmgyqw2-3846-9ee4963c04a66954-1.2   stream landed  stream=s1  x1
INBOX OK judgments=0 happened=11 cursor=-
machine: DONE
[exit 0]
```

The brief c1.md as first added (c2 and c3 the same, on f2.txt "two" and f3.txt "three"); the cards that landed, d1 to d3, differ in the id and in `REPO: /work/t/origin.git`:

```text
RESULT: c1 sha=<sha12>
REPO: stranger/throwaway
BASE: sprint/s1
PATHS: f1.txt
Deadline: finish within 10 minutes.

RULES.
Work only in the job directory this card names.
Never force-push or rebase a shared branch.
Never kill a process you did not start.
Never start a server on this machine.
No `rm -rf` outside the job directory.
Report what was not done.

THE TASK. In f1.txt of the throwaway repository, line 1 reads "one"; change it to "one-done". Touch no other file or line. The branch is the one the card names, from base sprint/s1.
Libraries considered: none; a one-line text edit.

STEP 1. Check out sprint/s1 in your worktree.
STEP 2. Change line 1 of f1.txt from "one" to "one-done".
STEP 3. Commit with the message c1 and push to the branch the card names.
STEP 4. Report the head.
```

## Stumbles

Each names what was read, what was expected, what happened, and a proposed card. The PATHS of a card were found by searching the tree after the run, not during it.

### 1. The README names release 1.0.0, and a source build cannot say its version

- Read: README.md, "Try one on a small example": "These are the Nova Tools 1.0.0 commands", `go install ...@v1.0.0`.
- Expected: the README to name the release the tools are at (the card says nova-tools v1.2.0), and `<tool> version` to say it.
- Happened: README.md names 1.0.0 only. Built from the checkout as the README allows, every tool printed `devel linux/amd64 go1.26.6`, so the run cannot show which version it ran.
- card: readme-names-current-release
  PATHS: README.md, internal/buildinfo/buildinfo.go
  Task: README.md names the current release in its install lines, and a build from a checkout prints the module's version and commit, not only `devel`.

### 2. nova-redis serve needs a password that README and help do not mention

- Read: README.md's nova-redis row ("serve also needs redis-server"), `nova-redis help`, `nova-redis serve -h`.
- Expected: `nova-redis serve --bind 127.0.0.1 --port 6379 --dir /work/redis` to start a throwaway Redis, as `--dry-run` said it would (`auth=on`, with no word on where the password comes from).
- Happened: `SERVE REFUSED: NOVA_REDIS_PASSWORD is empty; run under nova-secrets exec ...`. The help's "how it works" names the variable for the client verbs only; serve -h does not. For a throwaway Redis, exporting a throwaway value was a guess that worked.
- card: redis-serve-help-names-password
  PATHS: cmd/nova-redis/main.go, cmd/nova-redis/serve.go
  Task: `nova-redis serve -h` and its `--dry-run` line name NOVA_REDIS_PASSWORD as required, and the refusal gives the throwaway form (`NOVA_REDIS_PASSWORD=<value> nova-redis serve ...`) beside the nova-secrets one.

### 3. nova-sprint's login is undocumented, and its password variable is a fleet name

- Read: `nova-sprint help`, `nova-sprint init -h`, then the refusals.
- Expected: nova-sprint to log in to the Redis nova-redis serve started, with the password nova-redis took (NOVA_REDIS_PASSWORD).
- Happened: help and `init -h` name no user or password (`help | grep -i password` finds only "checks no credential"). The first refusal said "name the user (NOVA_SPRINT_REDIS_USER) and the variable that holds its password" without naming that variable; with the user named `default` (a guess), the second named `NOVA_REDIS_BENCH_PASSWORD`, a fleet name. Two retries and a guess.
- card: sprint-login-documented
  PATHS: cmd/nova-sprint/main.go, internal/nsprint/redisauth/redisauth.go
  Task: `nova-sprint help` and every store verb's -h name the login (NOVA_SPRINT_REDIS_USER, the password variable and its default), the first refusal names the password variable, and a Redis started by `nova-redis serve` is reached with NOVA_REDIS_PASSWORD with no fleet variable.

### 4. The help's real-fleet walkthrough leaves out `nova-redis fn load`

- Read: `nova-sprint help`, "A real fleet".
- Expected: init to work on a fresh Redis, as on the twin.
- Happened: `init REFUSED: the store at 127.0.0.1:6379 holds no nova_sprint function library; run: nova-redis fn load --addr 127.0.0.1:6379`. The refusal said exactly what to do, so this cost one retry; the walkthrough could say it first.
- card: sprint-help-fleet-fn-load
  PATHS: cmd/nova-sprint/main.go
  Task: the "A real fleet" part of `nova-sprint help` lists the store's first steps, `nova-redis serve`, the login, and `nova-redis fn load`, before `nova-sprint run --listen`.

### 5. preflight refuses briefs that add admits

- Read: `nova-sprint preflight -h`, `nova-swarm template --name card`.
- Expected: preflight to pass briefs that `add` accepts.
- Happened: preflight failed every card with `TEST: no test named (TEST: [-tags <tags>] <package> <TestName>)`, a Go test line the throwaway repository cannot have and the card template does not show; `add --brief-dir` then admitted the same briefs. It also said `BASE: sprint/s1 is not in /work/t/work` while the clone held `remotes/origin/sprint/s1`; only a local branch of that name passed it.
- card: preflight-matches-add
  PATHS: cmd/nova-sprint/preflight.go
  Task: preflight fails a brief only for what add refuses, a missing TEST: line is a note, and BASE: is found as a local branch or as origin/<base>.

### 6. The card template is shaped for a Go repository

- Read: `nova-swarm template --name card`, the brief lint in `nova-sprint add -h`.
- Expected: a card a stranger fills in for any repository.
- Happened: the template's steps name GOFLAGS, `internal/<package>/<file>.go`, `<file>_test.go`, `./internal/ci/`, JOB.md and a pull request. For a one-line text change which lines the lint needs was a guess (the RULES block and the "Libraries considered:" line were kept; STEP lines rewritten). add accepted it, so the guess was right.
- card: card-template-any-repo
  PATHS: internal/swarm/templates.go
  Task: the card template marks which of its lines the lint requires and which are an example for a Go repository, so a card for any repository is written without guessing.

### 7. The take refusal said there was room

- Read: `nova-sprint queue --as m1`, `take -h` through `nova-sprint help`.
- Expected: `take` of the three cards the deal had placed on m1 to succeed, or say how many it can take.
- Happened: the deal put three cards on a member of width 2 (it holds twice its width, ready and working); `take` of all three was refused `member m1 is at its width (0 working of 2)`, which reads as room, and the two others were refused with it. Taking two, then the third after a finish, worked.
- card: take-width-refusal-counts
  PATHS: cmd/nova-sprint/verbs.go, internal/sprint/steps_work.go
  Task: a take over the member's width says how many were asked, how many it may take now (width minus working), and to take that many.

### 8. The reader's packet shows an empty brief and report

- Read: `nova-sprint queue --as reader-a`.
- Expected: the packet a reader reads from to carry the card's brief and the worker's report.
- Happened: every read packet printed `brief:` and `report:` with nothing under them, while `card c1` showed both; the reader had to go to `card <id>` and the diff. The first `queue --as reader-a` also listed nothing, since it is the reader's beat and the reads are asked on the next tick; the help says so.
- card: read-packet-carries-brief
  PATHS: cmd/nova-sprint/verbs.go
  Task: `queue --as <reader>` prints the card's brief and the worker's report in each read packet, as the work packet prints the brief.

### 9. Landing on a throwaway origin is not in the help

- Read: `nova-sprint run -h` (`--land`: "land's defaults: each card's REPO: and BASE: lines"), the card template (`REPO: <owner>/<name>`), `nova-sprint land` in the help (`--repo-dir` "for one naming no REPO:").
- Expected: `run --land` to land the read cards on the bare origin of the throwaway repository.
- Happened: `REPO: stranger/throwaway` was taken as `https://github.com/stranger/throwaway.git`, and the land paused: `the merge queue of sprint/s1 could not be read (exec: "gh": executable file not found in $PATH)`. `land --repo-dir /work/t/work` by hand paused the same way, since the repository's address stays the GitHub one. Nothing in README or help says how to name a repository on no forge. To get past it I read docs/SPEC-SPRINT.md, "The lander's pause" ("a repository on no forge with a merge queue, a path or a bare clone, has none") and cmd/nova-sprint/mergewindow.go (`forgeRepo`), and set `REPO: /work/t/origin.git`. The fix then cost a drop and re-add (stumble 10).
- card: help-repo-path-for-local-origin
  PATHS: cmd/nova-sprint/main.go, internal/swarm/templates.go
  Task: the help and the card template say REPO: takes a path or a bare clone as well as <owner>/<name>, and that a path is landed with no forge asked; a land paused on gh for a card whose REPO: was a guess names that form.

### 10. A wrong REPO: line on a read card has no correcting verb

- Read: the refusal of `nova-sprint brief c1`, `nova-sprint help` on DONE.
- Expected: one verb to correct the REPO: line of the three queued cards.
- Happened: `brief` refused a merging card and named return, rework or drop and add. Drop and add under new ids (d1 to d3) worked, but the drop left nothing open, so the tick declared the sprint done and stopped the machine; the add printed `STOPPED` and needed `nova-sprint start` (the help says work added after DONE leaves it STOPPED). The record counts "3 landed, 3 dropped" for a three-card sprint.
- card: correct-repo-line-in-place
  PATHS: cmd/nova-sprint/verbs.go
  Task: a REPO: or BASE: line of a card in review or merging that has not landed can be corrected in place by the coordinator, its reads standing, without a drop.

### 11. Output that misled

- Read: the server log, the inbox.
- Expected: lines that describe this run.
- Happened: on a real Redis the server's first tick said `NOTE the twin read merge, fleet whole ...`; the landing said `batch landed ... ci green` with no check named and none run (`check=0.0s`); the inbox recorded `DECIDED ... fewer than two readers up ... answered by tick ask` and `no fleet member is up ... answered by tick deal` while one reader was all a flash card needs and m1 had not beaten yet; and the group `ready to merge size=6 (c1,c2,c3,d1,d2,d3)` counts the three dropped cards.
- card: run-lines-name-the-store
  PATHS: internal/sprint/store/tick.go, internal/sprint/steps_merge.go, internal/sprint/notes.go
  Task: the tick's note names the store it read (not "the twin" on Redis), a landing with no check says `check=none` and not `ci green`, and a note's group counts only the cards it is about now.

## Verdict

Could a stranger do it from README and help alone: no. Every step up to the read went from the help, with guesses and retries (stumbles 2 to 7), and the refusals pointed the way at each one. The landing did not: no line of README or help says a card's REPO: may be a path, and finding that took the spec and the source (stumble 9). With that one line the run lands from the help alone.

Minutes taken: 4 minutes 9 seconds from the first command in the container (19:27:09 UTC) to the last (19:31:18 UTC), the three cards landed at 19:31:04, and about 10 minutes from the container's first start to its removal. That is an AI reading the help at its own speed; `nova-sprint help` alone is over 600 lines, and a person reading it first would take far longer.
