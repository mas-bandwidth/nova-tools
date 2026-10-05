# A cold stranger run: a three-card nova-sprint on a throwaway Redis

One run, by an AI new to nova-sprint, of a whole sprint: a throwaway Redis, `init` with one member and one reader, three cards in one stream (each a one-line change to a throwaway git repository with a bare origin), the nova-sprint server with landing, the cards dealt, worked by hand with the worker verbs, read, and landed on the throwaway origin. The inputs allowed were README.md and the tools' own help; every place that was not enough is a stumble below, with what was read to get past it.

## Setup

- Bench: the second Linux bench of the card's two (x86_64, 16 cores; its name is left out because the tree's generality check refuses machine names in docs). The first answered but has no podman, so the container ran here.
- Container: one throwaway container for the whole session, from the functional image (`localhost/nova-functional:ctx-7e545976810241cb`: Go 1.26.6, redis-server 8.10.2, git), run as `podman run -d --rm --timeout 14400 --network none --name stranger-r-t-w2 --userns=keep-id:uid=10001,gid=10001 -v <work>:/work:Z -w /work -e HOME=/work/home -e GOMODCACHE=/work/gomod -e GOCACHE=/work/gocache -e GOFLAGS=-mod=readonly -e GOTOOLCHAIN=local <image> sleep 14400`, with `~/nova-bench/stranger/<job>/` as `/work`. Every command in the transcript ran inside it through `podman exec`; the Redis, the server and every git process were its children. It was stopped at the end (`podman stop`, which removed it) and the work directory removed.
- Source: a checkout of nova-tools at `95e000140c51819270b0a70b0d9f8e4534c15705` (the sprint base, 2026-10-05) copied to `/work/src` before the container started, and its module cache filled on the bench host with `go mod download` into `/work/gomod`, because a `--network none` container cannot fetch modules (stumble 1).
- Versions, as `<tool> version` printed them (the checkout carried this card's two uncommitted files, hence `+dirty`):
  - `nova-sprint v1.0.1-0.20261005223947-95e000140c51+dirty linux/amd64 go1.26.6`
  - `nova-swarm v1.0.1-0.20261005223947-95e000140c51+dirty linux/amd64 go1.26.6`
  - `nova-redis v1.0.1-0.20261005223947-95e000140c51+dirty linux/amd64 go1.26.6`
- Read before the run: README.md (it links no other README). The card's own step had the runner read the repository's AGENTS.md first, which is the coding standard and says nothing about running a sprint.
- The worker verbs were run by hand (`fleet beat`, `queue`, `take`, `finish`, `read`); the fake harness of the earlier fake-harness stranger run is named by neither README nor help, so a stranger would not find it.

## Transcript

Commands run from `/work` unless they change directory; the build ran in `/work/src` and the per-card git commands in `/work/run/work`, the clone of the throwaway origin. The run began at 2026-10-05T23:13:25Z and the last card landed at 23:19:41Z. Long outputs are cut where a `[trimmed here: ...]` line says so, and one word of one commit message is shown as `[...]`, because the tree's generality check refuses it as a name.

```
$ go version
go version go1.26.6 linux/amd64
[exit 0]
```

```
$ go install github.com/mas-bandwidth/nova-tools/cmd/nova-sprint@v1.0.0
go: github.com/mas-bandwidth/nova-tools/cmd/nova-sprint@v1.0.0: module lookup disabled by GOPROXY=off
[exit 1]
```

```
$ git log -1 --format="%H %cd" && mkdir -p /work/bin && nice -n 10 go build -o /work/bin/ ./cmd/nova-sprint ./cmd/nova-swarm ./cmd/nova-redis && ls /work/bin
95e000140c51819270b0a70b0d9f8e4534c15705 Mon Oct 5 18:39:47 2026 -0400
nova-redis
nova-sprint
nova-swarm
[exit 0]
```

```
$ nova-sprint version; nova-swarm version; nova-redis version
nova-sprint v1.0.1-0.20261005223947-95e000140c51+dirty linux/amd64 go1.26.6
nova-swarm v1.0.1-0.20261005223947-95e000140c51+dirty linux/amd64 go1.26.6
nova-redis v1.0.1-0.20261005223947-95e000140c51+dirty linux/amd64 go1.26.6
[exit 0]
```

```
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
For one verb's usage, examples, flags and exit codes:
  nova-sprint help <verb> (or <verb> -h)
For one group's help: nova-sprint help <group> (fleet, friend, reader, goal,
stream, lane, merge-window).

usage:
  nova-sprint init [--readers <a,b,...>] [--members <m1[:<width>],m2,...>] [--coordinator <name>] [--owner <name>] [--rules <file>]
  nova-sprint add --stream <s> (<id>... | --count <n> | --sentinel <id> | --brief-dir <dir> | --brief-file <f1> [--brief-file <f2>...]: a card per file, its id the file's name without .md) [--needs <a,b>] [--before <id> | --after <id> | --score <n>] [--brief <text> | --brief-file <path>: once, the brief of the cards named] [--rules <file>] [--held] [--allow-shared-paths] [--one: a single card is meant] [--replaces <old-id>[,<old-id>]: the one card is their twin] [--allow-personal-base]
[trimmed here: 594 lines of the same output]
  nova-sprint start
  nova-sprint inbox --wait
  nova-sprint land --stream s1 --check 'make test'
[exit 0]
```

```
$ nova-redis help
nova-redis: run a local Redis store, and keep short-lived named values in it

how it works: serve runs redis-server on loopback or tailnet addresses only, with its data in --dir.
spill writes a value under <owner>:<name> with a required expiry; recall reads it back.
fn load and fn check install and verify the functions nova-table and nova-sprint call.
The password is read from the variable NOVA_REDIS_PASSWORD_ENV names, else NOVA_REDIS_PASSWORD.
first run: the --dry-run line needs no store; spill and recall need a Redis at 127.0.0.1:6379.

usage:
  nova-redis serve --bind <addr>[,<addr>...] --port <port> --dir <store-dir> [--users <name>[,<name>...]] [--dry-run]
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
```

```
$ nova-redis serve -h
usage: nova-redis serve [flags]
from `nova-redis help`:
  nova-redis serve --bind <addr>[,<addr>...] --port <port> --dir <store-dir> [--users <name>[,<name>...]] [--dry-run]
The store's ACL users live in <store-dir>/users.acl (mode 0600), which nova-redis acl apply
writes through with ACL SAVE, so a restart keeps them; serve writes its default user
(the password's SHA-256, never the password) before each launch. --users names the users
that file must hold: a store whose file lacks one is refused and nothing starts.
--dry-run validates --bind, --port and --dir's absolute shape, checks --users against
the ACL file, and prints the binding, the port, the store directory, the ACL file and
the persistence, auth and eviction rules it would use. It defers every effect: it
creates no directory, writes no file, reads no password or environment, looks up no
redis-server and launches nothing.
flags:
  --bind <string>  comma-separated IP addresses to listen on, loopback (127.0.0.1, ::1) or tailnet (100.64.0.0/10, fd7a:115c:a1e0::/48) only
  --dir <string>  the absolute path of the store directory (AOF, RDB and ACL files), created 0700 when missing
  --dry-run  print what the verb would write and write nothing
  --port <string>  the TCP port to listen on, 1 to 65535 (6379 is Redis's own)
  --users <string>  comma-separated ACL users the store's ACL file must hold (those nova-redis acl apply set); a store missing one is refused
exit codes: 0 done (spill written, recall found, fn load done, fn check finds the library loaded, serve stopped); 1 ran and said NO (a recall of a missing, expired or unbounded key, fn check STALE or MISSING, a spill whose reply was lost, a refusal by the store, a serve that could not start); 2 could not run (a usage error, a flag refused before dialling, a store that did not answer or a login it refused).
effect: local write: writes files on this machine
[exit 0]
```

```
$ nova-redis serve --bind 127.0.0.1 --port 6379 --dir /work/redis --dry-run
SERVE OK bind=127.0.0.1 port=6379 auth=on persistence=aof eviction=none dir=/work/redis aclfile=/work/redis/users.acl users=0 dry_run=true created=0 launched=0
[exit 0]
```

```
$ nova-redis serve --bind 127.0.0.1 --port 6379 --dir /work/redis > /work/redis-serve.log 2>&1 &    (started in the background)
```

```
$ sleep 2; cat /work/redis-serve.log
SERVE REFUSED: NOVA_REDIS_PASSWORD is empty; run under `nova-secrets exec --only NOVA_REDIS_PASSWORD -- nova-redis serve ...` so auth comes from nova-secrets at run time, never an argument; run: nova-redis help
[exit 0]
```

```
$ nova-sprint init -h
usage: nova-sprint init [--readers <a,b,...>] [--members <m1[:<width>],m2,...>] [--coordinator <name>] [--owner <name>] [--rules <file>]
from `nova-sprint help`:
  nova-sprint init [--readers <a,b,...>] [--members <m1[:<width>],m2,...>] [--coordinator <name>] [--owner <name>] [--rules <file>]
  nova-sprint init --readers reader-a,reader-b --members m1
  nova-sprint init --readers reader-a,reader-b --members m1:8
example:
  nova-sprint init --readers reader-a,reader-b,reader-c --members m1:64,m2:64
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --attempts <string>  the sprint's attempt cap: how many attempts one brief may run before the card is the coordinator's as a brief defect; 1 to 100 (default 4; later: nova-sprint set --attempts <n>)
  --coordinator <string>  the sprint's coordinator, the one actor who releases sentinels (default: the actor); the seat then moves by coordinator <name>
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --members <string>  fleet members to bring up, comma separated, each <name> or <name>:<width>, its width the most work cards it runs at once; it holds 2 times that, ready and working (default 64)
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --owner <string>  the sprint's owner, who may give the seat and whose name a take of it carries (coordinator --take --approved-by); set once, never changed (else NOVA_SPRINT_OWNER)
  --readers <string>  the readers' rows, comma separated
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --rules <string>  the child rules file every brief is held to: one required sentence per line, its path recorded for the sprint (default: the built-in general rules; add --rules <file> overrides it for one add)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
[exit 0]
```

```
$ env NOVA_REDIS_PASSWORD=throwaway nova-redis serve --bind 127.0.0.1 --port 6379 --dir /work/redis > /work/redis-serve.log 2>&1 &    (started in the background)
```

```
$ sleep 2; cat /work/redis-serve.log; redis-cli -h 127.0.0.1 ping 2>&1 || true
SERVE START bind=127.0.0.1 port=6379 auth=on persistence=aof eviction=none dir=/work/redis aclfile=/work/redis/users.acl users=0 program=/usr/local/bin/redis-server
3505:C 05 Oct 2026 23:14:50.654 * Reading config from stdin
3505:C 05 Oct 2026 23:14:50.654 # WARNING Memory overcommit must be enabled! Without it, a background save or replication may fail under low memory condition. Being disabled, it can also cause failures without low memory condition, see https://github.com/jemalloc/jemalloc/issues/1328. To fix this issue add 'vm.overcommit_memory = 1' to /etc/sysctl.conf and then reboot or run the command 'sysctl vm.overcommit_memory=1' for this to take effect.
3505:C 05 Oct 2026 23:14:50.654 * oO0OoO0OoO0Oo Redis is starting oO0OoO0OoO0Oo
3505:C 05 Oct 2026 23:14:50.654 * Redis version=8.10.2, bits=64, commit=00000000, modified=0, pid=3505, just started
3505:C 05 Oct 2026 23:14:50.654 * Configuration loaded
3505:M 05 Oct 2026 23:14:50.654 * monotonic clock: POSIX clock_gettime
3505:M 05 Oct 2026 23:14:50.655 * Running mode=standalone, port=6379.
3505:M 05 Oct 2026 23:14:50.656 * Server initialized
3505:M 05 Oct 2026 23:14:50.656 * BGSAVE done, 0 keys saved, 0 keys skipped, 89 bytes written.
3505:M 05 Oct 2026 23:14:50.664 * Creating AOF base file appendonly.aof.1.base.rdb on server start
3505:M 05 Oct 2026 23:14:50.680 * Creating AOF incr file appendonly.aof.1.incr.aof on server start
3505:M 05 Oct 2026 23:14:50.680 * Ready to accept connections tcp
NOAUTH Authentication required.
[exit 0]
```

```
$ NOVA_REDIS_PASSWORD=throwaway nova-sprint where --redis 127.0.0.1:6379
nova-sprint where REFUSED: redis at 127.0.0.1:6379 as the default user, no password: login refused: NOAUTH Authentication required.; next: name the user (NOVA_SPRINT_REDIS_USER) and the variable that holds its password; run: nova-sprint where -h
[exit 2]
```

```
$ nova-sprint seat -h; nova-sprint help seat
usage: nova-sprint seat [--repair --reason <text>]
from `nova-sprint help`:
  nova-sprint seat check
  nova-sprint seat install [--dir <dir>] [--log <file>] [--dry-run]
  nova-sprint seat uninstall [--dir <dir>]
  nova-sprint seat [--repair --reason <text>]
example:
  nova-sprint seat
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --reason <string>  with --repair, why the key is repaired, recorded in the log (required)
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --repair  write the coordinator key from the seat's record when they differ: the record's holder or the owner, with --reason; logged with who and why
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: inspection: reads the seat (holder, epoch, generation), writes nothing
usage: nova-sprint seat [--repair --reason <text>]
[trimmed here: 15 lines of the same output]
  --repair  write the coordinator key from the seat's record when they differ: the record's holder or the owner, with --reason; logged with who and why
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: inspection: reads the seat (holder, epoch, generation), writes nothing
[exit 0]
```

```
$ NOVA_SPRINT_REDIS_USER=default NOVA_REDIS_PASSWORD=throwaway nova-sprint where --redis 127.0.0.1:6379
nova-sprint where REFUSED: redis at 127.0.0.1:6379 as user default (password from NOVA_REDIS_BENCH_PASSWORD): login refused: NOVA_REDIS_BENCH_PASSWORD is empty; next: export NOVA_REDIS_BENCH_PASSWORD, holding the password, in the environment of this process; run: nova-sprint where -h
[exit 2]
```

```
$ NOVA_SPRINT_REDIS_USER=default NOVA_REDIS_BENCH_PASSWORD=throwaway nova-sprint where --redis 127.0.0.1:6379
nova-sprint where REFUSED: the store at 127.0.0.1:6379 holds no nova_sprint function library; run: nova-redis fn load --addr 127.0.0.1:6379
[exit 2]
```

```
$ nova-redis fn load --addr 127.0.0.1:6379
FAILED nova_sprint sha=0f9c8ca0574bb69c store=127.0.0.1:6379 err=redis at 127.0.0.1:6379 as the default user, no password: login refused: NOAUTH Authentication required. remedy="log in as a user that may run FUNCTION LIST and FUNCTION LOAD: check --user (NOVA_REDIS_USER) and the password in NOVA_REDIS_PASSWORD, then nova-redis fn load --addr 127.0.0.1:6379"
[exit 2]
```

```
$ NOVA_REDIS_PASSWORD=throwaway nova-redis fn load --addr 127.0.0.1:6379
LOADED nova_sprint sha=0f9c8ca0574bb69c store=127.0.0.1:6379
[exit 0]
```

```
$ printf 'export NOVA_SPRINT_REDIS=127.0.0.1:6379 NOVA_SPRINT_REDIS_USER=default NOVA_REDIS_BENCH_PASSWORD=throwaway NOVA_SPRINT_ACTOR=boss\n' > /work/env.sh
[exit 0]
```

```
$ . /work/env.sh; nova-sprint where
nova-sprint where: this store: no sprint here yet: init makes its tables; run: nova-sprint init --coordinator <name>
[exit 1]
```

```
$ mkdir -p /work/run && cd /work/run && git config --global user.name stranger && git config --global user.email stranger@example.invalid && git init -q --bare origin.git && git clone -q origin.git work 2>&1 && printf "one\ntwo\nthree\n" > work/lines.txt && git -C work add lines.txt && git -C work commit -q -m base && git -C work push -q origin HEAD:sprint/s1 && git -C origin.git branch -a
warning: You appear to have cloned an empty repository.
  sprint/s1
[exit 0]
```

```
$ . /work/env.sh; nova-sprint init --readers reader-a --members m1:3 --coordinator boss --owner boss
INIT OK tables=work,readers,merge,fleet view=sprint readers=reader-a
MOVED m1 added, down until it beats width=3
FLEET-UP OK moved=1 refused=0 notes=0 op=fleet-release-dlxaca95fliv-3599-b962342c74dd6699-1
STOPPED
[exit 0]
```

```
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
```

```
$ mkdir -p /work/run/briefs && cd /work/run/briefs && for pair in "line-one:one:ONE" "line-two:two:TWO" "line-three:three:THREE"; do id=${pair%%:*}; rest=${pair#*:}; old=${rest%%:*}; new=${rest#*:}; cat > $id.md <<EOF
RESULT: $id sha=<sha12>
REPO: local/origin
BASE: sprint/s1
You are a child of the coordinator: one task, one worktree, one branch, unattended. This card is the whole of the task and it stands alone in front of a stranger; nothing outside it is owed to you.
Deadline: finish within 10 minutes.

RULES.
Work only in the job directory this card names.
Never force-push or rebase a shared branch.
Never kill a process you did not start.
Never start a server on this machine.
No \`rm -rf\` outside the job directory.
Report what was not done.

THE TASK. In lines.txt at the root of the repository, change the line "$old" to "$new"; touch no other line and no other file.
PATHS: lines.txt
Libraries considered: none; a one-line text edit.

STEP 1. Enter your worktree and run git log --oneline -1.
STEP 2. Edit lines.txt as THE TASK says and commit on your branch.
STEP 3. Report the head commit.
EOF
done; ls
line-one.md
line-three.md
line-two.md
[exit 0]
```

```
$ . /work/env.sh; cd /work/run; nova-sprint add --stream s1 --brief-dir briefs
MOVED line-one -> ready stream=s1 score=1
MOVED line-three -> ready stream=s1 score=2
MOVED line-two -> ready stream=s1 score=3
ADD OK stream=s1 cards=3 before=- moved=3 refused=0 notes=0 op=add-dlxachgwg7ay-3617-7844c8365a65b5fd-1
NOTE each card's id is its brief file's name without .md (briefs/line-one.md is line-one)
STOPPED  0/3 0.0%
[exit 0]
```

```
$ nova-sprint run -h
usage: nova-sprint run [--answer-rules=false] [--idle-alarm=false] [--listen <address:port>] [--land] [--decide <dir>]
from `nova-sprint help`:
  nova-sprint run [--answer-rules=false] [--idle-alarm=false] [--listen <address:port>] [--land] [--decide <dir>]
  nova-sprint run --listen <address>:<port> --land
  ticks; serves the workers' verbs (take, finish, read, queue, fleet beat) on
  <address>:<port>, this machine's address on the fleet's private network (it
  checks no credential, so a name, a public address, a link-local address
  and an every-network address such as 0.0.0.0 are refused); serves the
  coordinator's verbs on 127.0.0.1:<port>; with --land
  lands what the readers passed, so land is not run by hand beside it.
example:
  nova-sprint run
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --answer-rules  answer the mechanical judgments by rule, recorded "answered by rule <name>" (work came back failed: redealt, then a tier up; a card at its bound: a tier up, heavy to a friend; a late card: a wait once with progress, else returned and redealt; a conflict in a file no ledger owns: returned, redone on the tip, resumed; the same finding twice: marked a brief defect); nova-config's sprint row answer_rules_off turns single rules off; --answer-rules=false leaves every judgment to the coordinator (run answers by default, a tick by hand only with --answer-rules); nova-sprint rules prints what they would answer now
  --cpuprofile <string>  write a CPU profile of the loop's first ticks to this file (see --profile-ticks)
  --decide <dir>  also keep the record of the sprint's attempt and grade decisions in this dir (nova-decide's layer 2: attempt.jsonl, grade.jsonl): the finishes' attempt decisions recorded, every card graded before its first deal with JEV_API_KEY from this environment, and each decision's outcome attached when its card lands or is dropped, every 5s
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --idle-alarm  when the fleet works under half its width for 5m0s while cards wait, push the coordinator one note (the inbox, and inbox --push) naming the roots the waiting cards are behind, the most cards first, once an episode, and one more when it recovers (run: on by default; a tick by hand only with --idle-alarm)
  --json  print one JSON object for a program instead of the lines
  --land  also land what the readers passed, every 2s, one landing at a time, as the coordinator (land's defaults: each card's REPO: and BASE: lines); land is then not run by hand
  --listen <address:port>  also be the sprint's server: the workers' verbs on this address:port (this machine's address on the fleet's private network; a name, a public address, a link-local address, and an every-network address are refused), where nova-swarm member --server <address>:<port> sends them, and the coordinator's verbs on 127.0.0.1 at the same port, where NOVA_SPRINT_SERVER=127.0.0.1:<port> sends them
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --profile-ticks <int>  the ticks --cpuprofile covers; the profile is written after the last of them
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --tick-deadline <duration>  give up a tick that has not ended in this long: print the stacks and exit so the supervisor starts the loop again (0: wait for ever)
exit codes: 0 stopped (an interrupt), 2 usage or a store that did not answer, 3 its binary was replaced on disk (its supervisor starts the new one)
[exit 0]
```

```
$ bash -c ". /work/env.sh; nova-sprint run --listen 127.0.0.1:7400 --land" > /work/run/server.log 2>&1 &    (started in the background)
```

```
$ sleep 3; cat /work/run/server.log | head -30
SERVER listening on 127.0.0.1:7400: the workers' verbs (take, finish, read, queue, fleet beat) run here, one at a time, beside the store
SERVER listening on 127.0.0.1:7400: the coordinator's verbs, from this machine (NOVA_SPRINT_SERVER=127.0.0.1:7400)
LANDING every 2s: land runs here for every stream with cards queued to merge, one landing at a time
BALANCE every 10m0s: each provider's balance is read through the seat's key and written to the fleet table
RUN ticking on every line of the log (at most every 100ms) and every 1s while it is quiet; machine: STOPPED
23:15:57 machine STOPPED
[exit 0]
```

```
$ printf 'export NOVA_SPRINT_SERVER=127.0.0.1:7400 NOVA_SPRINT_ACTOR=boss\n' > /work/coord.sh
[exit 0]
```

```
$ . /work/coord.sh; nova-sprint start
START OK before=STOPPED after=RUNNING changed
0/3 0.0% -> ETA -  machine: running
[exit 0]
```

```
$ sleep 3; . /work/coord.sh; nova-sprint where
SPRINT TABLE  coordinator boss

0/3 0.0% -> ETA -

work | waiting | ready | working | review | merging | landed | cost | per landed
-----+---------+-------+---------+--------+---------+--------+------+-----------
s1   |       0 |     3 |       0 |      0 |       0 |      0 |    - | -
-----+---------+-------+---------+--------+---------+--------+------+-----------
     |       0 |     3 |       0 |      0 |       0 |      0 |    - |

friends | ready | working | width | done | ok%  | status | active
--------+-------+---------+-------+------+------+--------+-------
        |     0 |       0 |     0 |    0 | 0.0% |        |

fleet | ready | working | width | done | ok%  | status | load
------+-------+---------+-------+------+------+--------+-----
m1    |     0 |       0 |     3 |    0 | 0.0% | down   |
------+-------+---------+-------+------+------+--------+-----
      |     0 |       0 |     0 |    0 | 0.0% |        |

store: rtt p50=0.311ms p99=0.311ms
[exit 0]
```

```
$ nova-sprint fleet beat -h
usage: nova-sprint fleet beat <member> [--load <percent>]
from `nova-sprint help`:
  nova-sprint fleet beat <member> [--load <percent>]
example:
  nova-sprint fleet beat m1
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --cores <int>  the machine's logical cores the beat reports, instead of this machine's own (a test's, or another meter's); a member with the default width takes half
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --load <string>  the load as a percent of all the machine's cores, instead of measuring it (a test's, or another meter's)
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
[exit 0]
```

```
$ NOVA_SPRINT_SERVER=127.0.0.1:7400 nova-sprint fleet beat --as m1
nova-sprint fleet beat REFUSED: unknown flag --as; the flags of fleet beat are --actor, --cores, --epoch, --json, --load, --max, --op, --redis; run: nova-sprint help fleet beat
[exit 2]
```

```
$ NOVA_SPRINT_SERVER=127.0.0.1:7400 nova-sprint fleet beat m1 --actor m1
FLEET-BEAT OK m1 at=2026-10-05T23:16:24Z load=4.0% last=4.0% how=load1 cores=16
[exit 0]
```

```
$ sleep 2; NOVA_SPRINT_SERVER=127.0.0.1:7400 nova-sprint queue --as m1
CARD line-one.w1 fleet:m1:ready gen=1 take: line-one.w1@1 --epoch 0 dealt=2026-10-05T23:16:25Z
PACKET line-one.w1 attempt=1 gen=1 epoch=0
  branch: sprint/line-one.w1.g1.e0
  base: the stream's base
  brief:
    RESULT: line-one sha=<sha12>
    REPO: local/origin
    BASE: sprint/s1
    You are a child of the coordinator: one task, one worktree, one branch, unattended. This card is the whole of the task and it stands alone in front of a stranger; nothing outside it is owed to you.
    Deadline: finish within 10 minutes.
    
    RULES.
    Work only in the job directory this card names.
    Never force-push or rebase a shared branch.
    Never kill a process you did not start.
    Never start a server on this machine.
    No `rm -rf` outside the job directory.
    Report what was not done.
    
    THE TASK. In lines.txt at the root of the repository, change the line "one" to "ONE"; touch no other line and no other file.
[trimmed here: 62 lines of the same output]
  notes: none
  report it: nova-sprint finish --as m1 line-two.w1@1 --epoch 0 --branch sprint/line-two.w1.g1.e0 --head <commit> --report '<what you did>' [--failed]
QUEUE OK cards=3 epoch=0
[exit 0]
```

```
$ NOVA_SPRINT_SERVER=127.0.0.1:7400 nova-sprint take --as m1 --max 3 --epoch 0
MOVED line-one.w1 fleet ready -> working member=m1 gen=1
MOVED line-three.w1 fleet ready -> working member=m1 gen=1
MOVED line-two.w1 fleet ready -> working member=m1 gen=1
PACKET line-one.w1 attempt=1 gen=1 epoch=0
  branch: sprint/line-one.w1.g1.e0
  base: the stream's base
  brief:
    RESULT: line-one sha=<sha12>
    REPO: local/origin
    BASE: sprint/s1
    You are a child of the coordinator: one task, one worktree, one branch, unattended. This card is the whole of the task and it stands alone in front of a stranger; nothing outside it is owed to you.
    Deadline: finish within 10 minutes.
    
    RULES.
    Work only in the job directory this card names.
    Never force-push or rebase a shared branch.
    Never kill a process you did not start.
    Never start a server on this machine.
    No `rm -rf` outside the job directory.
    Report what was not done.
[trimmed here: 63 lines of the same output]
  report it: nova-sprint finish --as m1 line-two.w1@1 --epoch 0 --branch sprint/line-two.w1.g1.e0 --head <commit> --report '<what you did>' [--failed]
TAKE OK moved=3 refused=0 notes=0 op=take-dlxad39xkrb1-3632-9bfb5ca0168dbb1e-1
0/3 0.0% -> ETA -  machine: running
[exit 0]
```

```
$ git fetch -q origin && git checkout -q -B sprint/line-one.w1.g1.e0 origin/sprint/s1 && sed -i 's/^one$/ONE/' lines.txt && git commit -q -am 'line-one: one -> ONE' && git push -q origin HEAD:sprint/line-one.w1.g1.e0 && NOVA_SPRINT_SERVER=127.0.0.1:7400 nova-sprint finish --as m1 line-one.w1@1 --epoch 0 --branch sprint/line-one.w1.g1.e0 --head $(git rev-parse HEAD) --report 'changed one to ONE in lines.txt'
MOVED line-one.w1 working -> done ok; line-one working -> review
FINISH OK moved=1 refused=0 notes=1 op=finish-dlxad82v81ro-3632-8a1dadfdbab35cd9-1
0/3 0.0% -> ETA -  machine: running
[exit 0]
```

```
$ git fetch -q origin && git checkout -q -B sprint/line-three.w1.g1.e0 origin/sprint/s1 && sed -i 's/^three$/THREE/' lines.txt && git commit -q -am 'line-three: three -> THREE' && git push -q origin HEAD:sprint/line-three.w1.g1.e0 && NOVA_SPRINT_SERVER=127.0.0.1:7400 nova-sprint finish --as m1 line-three.w1@1 --epoch 0 --branch sprint/line-three.w1.g1.e0 --head $(git rev-parse HEAD) --report 'changed three to THREE in lines.txt'
MOVED line-three.w1 working -> done ok; line-three working -> review
FINISH OK moved=1 refused=0 notes=1 op=finish-dlxad8bwo2kw-3632-b622ede3bd30da0d-1
0/3 0.0% -> ETA -  machine: running
[exit 0]
```

```
$ git fetch -q origin && git checkout -q -B sprint/line-two.w1.g1.e0 origin/sprint/s1 && sed -i 's/^two$/TWO/' lines.txt && git commit -q -am 'line-two: two -> TWO' && git push -q origin HEAD:sprint/line-two.w1.g1.e0 && NOVA_SPRINT_SERVER=127.0.0.1:7400 nova-sprint finish --as m1 line-two.w1@1 --epoch 0 --branch sprint/line-two.w1.g1.e0 --head $(git rev-parse HEAD) --report 'changed two to TWO in lines.txt'
MOVED line-two.w1 working -> done ok; line-two working -> review
FINISH OK moved=1 refused=0 notes=1 op=finish-dlxad8jqfgyy-3632-8a1db685d42a948c-1
0/3 0.0% -> ETA -  machine: running
[exit 0]
```

```
$ sleep 2; NOVA_SPRINT_SERVER=127.0.0.1:7400 nova-sprint queue --as reader-a
QUEUE OK cards=0 epoch=0
[exit 0]
```

```
$ . /work/coord.sh; nova-sprint where
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
m1    |     0 |       0 |     3 |    3 | 100.0% | up     |
------+-------+---------+-------+------+--------+--------+-----
      |     0 |       0 |     3 |    3 | 100.0% |        |

store: rtt p50=0.279ms p99=0.311ms
[exit 0]
```

```
$ . /work/coord.sh; nova-sprint inbox
HAPPENED start-dlxacse7iw1y-3632-a11b4e3e0fae5824-1.1   the machine started  x1  STOPPED -> RUNNING by boss
HAPPENED tick-presence-dlxaczuunptu-3632-cd4add82febe4685-1.1   fleet member up  x1  m1 up: it beats
DECIDED tick-deal-dlxaczwicu8n-3632-921ad1f7f839e321-1.d1   no fleet member is up  size=1  (stream:)  answered by tick deal
HAPPENED finish-dlxad82v81ro-3632-8a1dadfdbab35cd9-1.1   work came back ok  stream=s1  size=3  (line-one,line-three,line-two)
DECIDED tick-ask-dlxadcr6lngk-3632-7d66f4b03d735006-1.d1   fewer than two readers up  size=1  (stream:)  answered by tick ask
INBOX OK judgments=0 happened=5 cursor=-
machine: running
[exit 0]
```

```
$ nova-sprint read -h
usage: nova-sprint read --as <reader> (--begin | --ok | --broken) [<card>...] --epoch <n> [--max <n>] [--finding <text>] [--usage <text>] | --as <reader> --return <card> --reason <text> --epoch <n> [--usage <text>]
from `nova-sprint help`:
  nova-sprint read --as <reader> (--begin | --ok | --broken) [<card>...] --epoch <n> [--max <n>] [--finding <text>] [--usage <text>] | --as <reader> --return <card> --reason <text> --epoch <n> [--usage <text>]
  nova-sprint read --as reader-a --begin --epoch 0
  nova-sprint read --as reader-a --ok --epoch 0
example:
  nova-sprint read --as reader-a --ok --max 5 --epoch 0
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --as <string>  the reader; use read-card IDs from queue --as <reader>; several readers, comma separated, each reporting its own named read cards in one step
  --begin  asked -> reading
  --broken  the read found it broken
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --finding <string>  what the read found; with --broken it names the file (file:line), the line, or the card's STEP or RULE the work breaks, and what to change, or the read is refused
  --json  print one JSON object for a program instead of the lines
  --limit <value>  alias of --max, a whole number, accepted for one release
  --max <int>  listed items of each kind (0 is all); when given, the first n of the reader's queue (omitted, 1)
  --ok  the read found it good
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --reason <string>  with --return: why the read has no verdict (it reaches the inbox)
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --return <string>  hand back a read the reader holds and has no verdict on: not a read; the next tick asks it of another reader free at the attempt, or of this reader again; no finding against the work
  --usage <string>  with --ok, --broken or --return: what the read spent, one line (the reader passes its child's tokens, wall and cost): kept on the read card, timed and priced
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
[exit 0]
```

```
$ nova-sprint help reader
usage:
  nova-sprint reader add <reader>... [--tiers <flash[,pro,heavy,frontier]|all|default>]
  nova-sprint reader set <reader>... --tiers <flash[,pro,heavy,frontier]|all|default>
  nova-sprint reader away <reader>...
  nova-sprint reader up <reader>...
  nova-sprint reader remove <reader>...
  nova-sprint reader retire <reader>...

The readers: a reader is a row of the readers table, which the coordinator
declares (init --readers, reader add); no beat and no loop record makes one. A
reader with its row says it is there by asking for its own queue (queue --as
<reader> is its beat); the queue of a name with no row writes none and answers
reader false, and the reader loop says MEMBER NOT A READER. A reader is up while its last beat is under 15s old, away
when it beat and has lapsed, down when it has never beaten; reader away holds
one away whatever it beats and reader up releases the hold (the old words of
hold <reader> --return and unhold <reader>: its state reads held). A flash card is
read once and a pro card twice, by two different readers, one read at a time
(the second asked once the first comes back ok), each read on a route of the
card's tier. The ask deals a read to a reader up only: a read asked of a
reader that is not up is asked of another at the next tick, and a card that
[trimmed here: 12 lines of the same output]
it back.

nova-sprint help reader <verb> (or nova-sprint reader <verb> -h) prints a verb's flags, examples and exit codes.
[exit 0]
```

```
$ export NOVA_SPRINT_SERVER=127.0.0.1:7400; nova-sprint queue --as reader-a >/dev/null; sleep 3; nova-sprint queue --as reader-a
CARD line-one.r1.reader-a readers:reader-a:asked asked=2026-10-05T23:16:53Z
PACKET line-one.r1.reader-a attempt=1 gen=0 epoch=0
  work: attempt 1 by m1
  head: a61b352720d32b5bdecea5f9ea886b6d4cbd9719
  branch: sprint/line-one.w1.g1.e0
  brief:
    RESULT: line-one sha=<sha12>
    REPO: local/origin
    BASE: sprint/s1
    You are a child of the coordinator: one task, one worktree, one branch, unattended. This card is the whole of the task and it stands alone in front of a stranger; nothing outside it is owed to you.
    Deadline: finish within 10 minutes.
    
    RULES.
    Work only in the job directory this card names.
    Never force-push or rebase a shared branch.
    Never kill a process you did not start.
    Never start a server on this machine.
    No `rm -rf` outside the job directory.
    Report what was not done.
    
[trimmed here: 74 lines of the same output]
  attribution: the By: line, the Co-Authored-By trailer and the model or harness named are never a finding and never decide a verdict
  report it: nova-sprint read --as reader-a (--ok | --broken) line-two.r1.reader-a --epoch 0 --finding '<file:line, and what to change>'
QUEUE OK cards=3 epoch=0
[exit 0]
```

```
$ for c in one three two; do git diff --stat origin/sprint/s1 origin/sprint/line-$c.w1.g1.e0; git diff -U0 origin/sprint/s1 origin/sprint/line-$c.w1.g1.e0 | tail -2; done
 lines.txt | 2 +-
 1 file changed, 1 insertion(+), 1 deletion(-)
-one
+ONE
 lines.txt | 2 +-
 1 file changed, 1 insertion(+), 1 deletion(-)
-three
+THREE
 lines.txt | 2 +-
 1 file changed, 1 insertion(+), 1 deletion(-)
-two
+TWO
[exit 0]
```

```
$ export NOVA_SPRINT_SERVER=127.0.0.1:7400; nova-sprint read --as reader-a --begin line-one.r1.reader-a line-three.r1.reader-a line-two.r1.reader-a --epoch 0 && nova-sprint read --as reader-a --ok line-one.r1.reader-a line-three.r1.reader-a line-two.r1.reader-a --epoch 0
MOVED line-one.r1.reader-a asked -> reading
MOVED line-three.r1.reader-a asked -> reading
MOVED line-two.r1.reader-a asked -> reading
READ OK moved=3 refused=0 notes=0 op=read-dlxadq8ik982-3632-0343edbfc1f4efaa-1
0/3 0.0% -> ETA -  machine: running
MOVED line-one.r1.reader-a reading -> ok
MOVED line-three.r1.reader-a reading -> ok
MOVED line-two.r1.reader-a reading -> ok
READ OK moved=3 refused=0 notes=0 op=read-dlxadq8mzwkh-3632-2925e666e18468f1-1
0/3 0.0% -> ETA -  machine: running
[exit 0]
```

```
$ sleep 6; . /work/coord.sh; nova-sprint where | sed -n 1,10p; nova-sprint inbox; tail -15 /work/run/server.log
SPRINT TABLE  coordinator boss

0/3 0.0% -> ETA -

work | waiting | ready | working | review | merging | landed | cost | per landed
-----+---------+-------+---------+--------+---------+--------+------+-----------
s1   |       0 |     0 |       0 |      0 |       3 |      0 |    - | -
-----+---------+-------+---------+--------+---------+--------+------+-----------
     |       0 |     0 |       0 |      0 |       3 |      0 |    - |

HAPPENED tick-accept-dlxadqa9koyn-3632-c5d5743b24557f9b-1.2   ready to merge  stream=s1  size=3  for=boss  (line-one,line-three,line-two)  3 accepted and queued to merge: line-one line-three line-two; run: nova-sprint land --stream s1 (a nova-sprint run started with --land lands them itself)
HAPPENED start-dlxacse7iw1y-3632-a11b4e3e0fae5824-1.1   the machine started  x1  STOPPED -> RUNNING by boss
HAPPENED tick-presence-dlxaczuunptu-3632-cd4add82febe4685-1.1   fleet member up  x1  m1 up: it beats
DECIDED tick-deal-dlxaczwicu8n-3632-921ad1f7f839e321-1.d1   no fleet member is up  size=1  (stream:)  answered by tick deal
HAPPENED finish-dlxad82v81ro-3632-8a1dadfdbab35cd9-1.1   work came back ok  stream=s1  size=3  (line-one,line-three,line-two)
DECIDED tick-ask-dlxadcr6lngk-3632-7d66f4b03d735006-1.d1   fewer than two readers up  size=1  (stream:)  answered by tick ask
HAPPENED tick-presence-dlxadkaibuur-3632-c2f37a209150bc88-1.1   fleet member down  x1  m1 down: no beat for 45s
HAPPENED tick-accept-dlxadqa9koyn-3632-c5d5743b24557f9b-1.1   stream started merging  stream=s1  x1
INBOX OK judgments=0 happened=8 cursor=-
machine: running
[trimmed here: 12 lines of the same output]
TICK OK state=RUNNING idle=yes moved=0 notes=0
TIMES 2ms trips=16 reads=0 rows=1 stale=0 mismatch=0: look=0ms/5t/0r/0n fleet-display=0ms/3t/0r/1n first-read=0ms/2t/0r/0n remind=0ms/2t/0r/0n where=0ms/2t/0r/0n tick-end=0ms/2t/0r/0n heartbeat=0ms/0t/0r/0n
0/3 0.0% -> ETA -  machine: running
[exit 0]
```

```
$ grep -n -i -E "land|LAND" /work/run/server.log | grep -v "^.*TIMES" | head -20
3:LANDING every 2s: land runs here for every stream with cards queued to merge, one landing at a time
474:23:17:23 LAND REFUSED stream=s1 cards=3 base=sprint/s1 tip=- ids=line-one..line-two repo=https://github.com/local/origin.git reason=paused: the merge queue of sprint/s1 could not be read (exec: "gh": executable file not found in $PATH), and an unreadable queue is not an empty one; the cards stay queued and land when it is read clear
476:23:17:23 LAND DONE batches=0 cards=0 refused=1; run: nova-sprint where
[exit 0]
```

```
$ nova-sprint land -h
usage: nova-sprint land [--stream <s>...] [--repo-dir <clone>] [--base <branch>] [--check <command>] [--dry-run]
from `nova-sprint help`:
  nova-sprint land [--stream <s>...] [--repo-dir <clone>] [--base <branch>] [--check <command>] [--dry-run]
  nova-sprint land --stream s1 --repo-dir work --base sprint/s1
  nova-sprint land --stream s1 --check 'make test'
  merges each queued card's head (--no-ff) in queue order onto a branch cut
  from origin's base, one batch per run of cards naming one REPO: and BASE:
  (--base for a card naming none); runs --check once per batch; pushes, never
  forced, rebuilding once on a moved base; then reports the batch as merge
  --stream s1 --batch <n> does. A head missing or in conflict ends the batch
  before it and is reported as merge --conflict, a red check as --red, a
  second rejected push as --rejected. Each head merged is checked first, by
  script and no model: a head whose diff changes a file outside its brief's
  PATHS, or leaves a stranded sentence fragment or an unmatched backquote in
  prose, ends the batch as a head in conflict does. A card that adds a directory
  owns its catalog row and the AGENTS.md maps; a conflict only in those maps, or
  those maps and added catalog rows, resolves as the ledgers do. A conflict only
  in the generated ledgers lands: the tip's side, then their tests' update run
  (NOVA_CI_UPDATE=1) to a fixed point, one commit; any other conflict stops
  the stream, and after resume land merges the head again. The clone is --repo-dir,
[trimmed here: 39 lines of the same output]
  --repo-dir <string>  the clone to land in, its origin the remote pushed to (default: a clone per repository under the directory each line names)
  --stream <value>  a stream to land (again, or comma separated, for more; default: every stream with cards queued to merge and not stopped)
exit codes: 0 every batch landed (--dry-run: would land), 1 a batch was refused (its line names the next step), 2 usage, a store that did not answer, or a push that landed and was not reported (run land again)
[exit 0]
```

```
$ . /work/env.sh; nova-sprint land --stream s1 --repo-dir /work/run/work --dry-run
LAND OK stream=s1 cards=3 base=sprint/s1 tip=- ids=line-one..line-two repo=https://github.com/local/origin.git dir=/work/run/work branches_queued=3 dry_run=yes
LAND DONE batches=1 cards=3 refused=0 dry_run=yes; run: nova-sprint where
[exit 0]
```

```
$ . /work/env.sh; nova-sprint land --stream s1 --repo-dir /work/run/work
LAND REFUSED stream=s1 cards=3 base=sprint/s1 tip=- ids=line-one..line-two repo=https://github.com/local/origin.git reason=paused: the merge queue of sprint/s1 could not be read (exec: "gh": executable file not found in $PATH), and an unreadable queue is not an empty one; the cards stay queued and land when it is read clear
NOTE nothing was pushed or reported for stream s1; its cards stay queued
LAND DONE batches=0 cards=0 refused=1; run: nova-sprint where
[exit 1]
```

```
$ . /work/coord.sh; nova-sprint drop line-one line-three line-two --reason "REPO: local/origin resolves to github.com; re-added with the bare origin path"
MOVED line-one merging -> off the table (REPO: local/origin resolves to github.com; re-added with the bare origin path)
MOVED line-three merging -> off the table (REPO: local/origin resolves to github.com; re-added with the bare origin path)
MOVED line-two merging -> off the table (REPO: local/origin resolves to github.com; re-added with the bare origin path)
DROP OK moved=3 refused=0 notes=0 op=drop-dlxaeu2ygdne-3632-2ee3960e08d3c1dd-1
0/3 0.0% -> ETA -  machine: running
[exit 0]
```

```
$ cd /work/run && mkdir -p briefs2 && for c in one two three; do sed -e "s#^REPO: local/origin#REPO: /work/run/origin.git#" -e "s#^RESULT: line-$c #RESULT: path-$c #" briefs/line-$c.md > briefs2/path-$c.md; done && head -3 briefs2/path-one.md
RESULT: path-one sha=<sha12>
REPO: /work/run/origin.git
BASE: sprint/s1
[exit 0]
```

```
$ . /work/coord.sh; cd /work/run; nova-sprint add --stream s1 --brief-dir briefs2
MOVED path-one -> ready stream=s1 score=1
MOVED path-three -> ready stream=s1 score=2
MOVED path-two -> ready stream=s1 score=3
ADD OK stream=s1 cards=3 before=- moved=3 refused=0 notes=0 op=add-dlxaexb2m1or-3632-d0f29fddbf425d2e-1
NOTE each card's id is its brief file's name without .md (/work/run/briefs2/path-one.md is path-one)
STOPPED  0/3 0.0%
[exit 0]
```

```
$ . /work/coord.sh; nova-sprint start
START OK before=STOPPED after=RUNNING changed
0/3 0.0% -> ETA -  machine: running
[exit 0]
```

```
$ export NOVA_SPRINT_SERVER=127.0.0.1:7400; nova-sprint fleet beat m1 --actor m1; nova-sprint queue --as reader-a >/dev/null; sleep 3; nova-sprint take --as m1 --max 3 --epoch 0 | grep -E "^(MOVED|TAKE|  branch|  report)"
FLEET-BEAT OK m1 at=2026-10-05T23:19:03Z load=0.6% last=0.6% how=cpu cores=16
MOVED path-one.w1 fleet ready -> working member=m1 gen=1
MOVED path-three.w1 fleet ready -> working member=m1 gen=1
MOVED path-two.w1 fleet ready -> working member=m1 gen=1
  branch: sprint/path-one.w1.g1.e0
  report it: nova-sprint finish --as m1 path-one.w1@1 --epoch 0 --branch sprint/path-one.w1.g1.e0 --head <commit> --report '<what you did>' [--failed]
  branch: sprint/path-three.w1.g1.e0
  report it: nova-sprint finish --as m1 path-three.w1@1 --epoch 0 --branch sprint/path-three.w1.g1.e0 --head <commit> --report '<what you did>' [--failed]
  branch: sprint/path-two.w1.g1.e0
  report it: nova-sprint finish --as m1 path-two.w1@1 --epoch 0 --branch sprint/path-two.w1.g1.e0 --head <commit> --report '<what you did>' [--failed]
TAKE OK moved=3 refused=0 notes=0 op=take-dlxaf1p6i0bd-3632-f91c5a476f93df0f-1
[exit 0]
```

```
$ git fetch -q origin && git checkout -q -B s1 origin/sprint/s1 && printf "one\n\ntwo\n\nthree\n" > lines.txt && git commit -q -am "base: [...] the lines so three one-line edits merge cleanly" && git push -q origin HEAD:sprint/s1 && git log --oneline -2 origin/sprint/s1
134d463 base: [...] the lines so three one-line edits merge cleanly
2d4ecbf base
[exit 0]
```

```
$ git fetch -q origin && git checkout -q -B sprint/path-one.w1.g1.e0 origin/sprint/s1 && sed -i 's/^one$/ONE/' lines.txt && git commit -q -am 'path-one: one -> ONE' && git push -q origin HEAD:sprint/path-one.w1.g1.e0 && NOVA_SPRINT_SERVER=127.0.0.1:7400 nova-sprint finish --as m1 path-one.w1@1 --epoch 0 --branch sprint/path-one.w1.g1.e0 --head $(git rev-parse HEAD) --report 'changed one to ONE in lines.txt'
MOVED path-one.w1 working -> done ok; path-one working -> review
FINISH OK moved=1 refused=0 notes=1 op=finish-dlxafad9rz3m-3632-85ddd03691a39c0f-1
0/3 0.0% -> ETA -  machine: running
[exit 0]
```

```
$ git fetch -q origin && git checkout -q -B sprint/path-three.w1.g1.e0 origin/sprint/s1 && sed -i 's/^three$/THREE/' lines.txt && git commit -q -am 'path-three: three -> THREE' && git push -q origin HEAD:sprint/path-three.w1.g1.e0 && NOVA_SPRINT_SERVER=127.0.0.1:7400 nova-sprint finish --as m1 path-three.w1@1 --epoch 0 --branch sprint/path-three.w1.g1.e0 --head $(git rev-parse HEAD) --report 'changed three to THREE in lines.txt'
MOVED path-three.w1 working -> done ok; path-three working -> review
FINISH OK moved=1 refused=0 notes=1 op=finish-dlxafal77uzm-3632-bd835dc14d057168-1
0/3 0.0% -> ETA -  machine: running
[exit 0]
```

```
$ git fetch -q origin && git checkout -q -B sprint/path-two.w1.g1.e0 origin/sprint/s1 && sed -i 's/^two$/TWO/' lines.txt && git commit -q -am 'path-two: two -> TWO' && git push -q origin HEAD:sprint/path-two.w1.g1.e0 && NOVA_SPRINT_SERVER=127.0.0.1:7400 nova-sprint finish --as m1 path-two.w1@1 --epoch 0 --branch sprint/path-two.w1.g1.e0 --head $(git rev-parse HEAD) --report 'changed two to TWO in lines.txt'
MOVED path-two.w1 working -> done ok; path-two working -> review
FINISH OK moved=1 refused=0 notes=1 op=finish-dlxafathulf1-3632-cfdd31c32c00ce30-1
0/3 0.0% -> ETA -  machine: running
[exit 0]
```

```
$ export NOVA_SPRINT_SERVER=127.0.0.1:7400; nova-sprint queue --as reader-a >/dev/null; sleep 3; nova-sprint queue --as reader-a | grep -E "^(CARD|QUEUE|  head)"
CARD path-one.r1.reader-a readers:reader-a:asked asked=2026-10-05T23:19:32Z
  head: dee9692ff1e62b6a582aafd7bc148c048e6f7d16
CARD path-three.r1.reader-a readers:reader-a:asked asked=2026-10-05T23:19:32Z
  head: 4d01d78e305183e967a51acdd2aed287c8e50f27
CARD path-two.r1.reader-a readers:reader-a:asked asked=2026-10-05T23:19:32Z
  head: f5015e790341fd5bdedfff130c35471561e44c80
QUEUE OK cards=3 epoch=0
[exit 0]
```

```
$ for h in dee9692 4d01d78 f5015e7; do git diff -U0 origin/sprint/s1 $h | grep -E "^[-+][^-+]"; done
-one
+ONE
-three
+THREE
-two
+TWO
[exit 0]
```

```
$ export NOVA_SPRINT_SERVER=127.0.0.1:7400; C="path-one.r1.reader-a path-three.r1.reader-a path-two.r1.reader-a"; nova-sprint read --as reader-a --begin $C --epoch 0 && nova-sprint read --as reader-a --ok $C --epoch 0
MOVED path-one.r1.reader-a asked -> reading
MOVED path-three.r1.reader-a asked -> reading
MOVED path-two.r1.reader-a asked -> reading
READ OK moved=3 refused=0 notes=0 op=read-dlxafhjvif0p-3632-35eae1c7849c660b-1
0/3 0.0% -> ETA -  machine: running
MOVED path-one.r1.reader-a reading -> ok
MOVED path-three.r1.reader-a reading -> ok
MOVED path-two.r1.reader-a reading -> ok
READ OK moved=3 refused=0 notes=0 op=read-dlxafhk0ag0v-3632-9556fbb076353db9-1
0/3 0.0% -> ETA -  machine: running
[exit 0]
```

```
$ sleep 8; grep -E "LAND" /work/run/server.log | tail -6; . /work/coord.sh; nova-sprint where | sed -n 3,9p
LANDING every 2s: land runs here for every stream with cards queued to merge, one landing at a time
23:17:23 LAND REFUSED stream=s1 cards=3 base=sprint/s1 tip=- ids=line-one..line-two repo=https://github.com/local/origin.git reason=paused: the merge queue of sprint/s1 could not be read (exec: "gh": executable file not found in $PATH), and an unreadable queue is not an empty one; the cards stay queued and land when it is read clear
23:17:23 LAND DONE batches=0 cards=0 refused=1; run: nova-sprint where
23:19:41 LAND OK stream=s1 cards=3 base=sprint/s1 tip=d7c8368bc74a85b093a1bde3580812a6d57410b5 ids=path-one..path-two repo=/work/run/origin.git dir=/work/home/.cache/nova-sprint/land/work-run-origin-863dd05cd4c2f894 fetch=0.0s merge=0.0s check=0.0s queue=0.0s push=0.0s report=0.0s branches_queued=3
DONE

work | waiting | ready | working | review | merging | landed | cost | per landed
-----+---------+-------+---------+--------+---------+--------+------+-----------
s1   |       0 |     0 |       0 |      0 |       0 |      3 |    - | -
-----+---------+-------+---------+--------+---------+--------+------+-----------
     |       0 |     0 |       0 |      0 |       0 |      3 |    - |
[exit 0]
```

```
$ git -C /work/run/origin.git log --oneline --graph sprint/s1 | head -12; git -C /work/run/origin.git show sprint/s1:lines.txt
*   d7c8368 land path-two (sprint stream s1)
|\  
| * f5015e7 path-two: two -> TWO
* |   395dda6 land path-three (sprint stream s1)
|\ \  
| * | 4d01d78 path-three: three -> THREE
| |/  
* |   774198d land path-one (sprint stream s1)
|\ \  
| |/  
|/|   
| * dee9692 path-one: one -> ONE
ONE

TWO

THREE
[exit 0]
```

```
$ . /work/coord.sh; nova-sprint where | head -3; nova-sprint inbox | head -4
SPRINT TABLE  coordinator boss

DONE
HAPPENED tick-done-dlxaeu344nzu-3632-e10f8a99e64fa9e6-1.1   the sprint is done  x2  for=boss  3 landed, 3 dropped, took 3m33s from the first start
  to continue: add work, then nova-sprint start
HAPPENED tick-accept-dlxadqa9koyn-3632-c5d5743b24557f9b-1.2   ready to merge  stream=s1  size=6  for=boss  (line-one,line-three,line-two,path-one,path-three,path-two)  3 accepted and queued to merge: path-one path-three path-two; run: nova-sprint land --stream s1 (a nova-sprint run started with --land lands them itself)
HAPPENED start-dlxacse7iw1y-3632-a11b4e3e0fae5824-1.1   the machine started  x2  STOPPED -> RUNNING by boss
[exit 0]
```

## Stumbles

### 1. The README's install line cannot run in an offline container, and names an older version

Read: README.md, "Try one on a small example". Expected: an install line for nova-sprint that works for the version under test (nova-tools v1.2.0, nova-sprint v1.0.0). Happened: the README says these are the 1.0.0 commands and gives `go install .../cmd/nova-memory@v1.0.0`; the same line for nova-sprint fails with `module lookup disabled by GOPROXY=off` inside a `--network none` container, and the README says nothing about building from a checkout or an offline module cache. Built from the source checkout with a module cache filled on the host; the binaries then print a pseudo-version (`v1.0.1-0....`), not a release.

card: readme-install-from-checkout
- PATHS: README.md
- task: say how to build any tool from a source checkout (`go build -o <dir> ./cmd/<tool>`) and name the current release, so an offline or pre-release install has one documented line.

### 2. `nova-sprint help` is 622 lines, and the real-fleet path is in the middle

Read: `nova-sprint help`. Expected: the banner's first run to lead to a real store when one is wanted. Happened: the banner points at the twin walkthrough first; "A real fleet" is at line 269 of 622 and the landing paragraph at 510; finding them took a search of the text, not a read.

card: sprint-help-real-fleet-group
- PATHS: cmd/nova-sprint/helpwords.go
- task: put the real-fleet steps (store, fn load, init, run --listen --land, member or worker verbs) in a group of their own (`nova-sprint help fleet-start`) named on the banner's first screen.

### 3. `nova-redis serve` refuses a throwaway store without a password, and its remedy needs nova-secrets

Read: `nova-redis help`, `nova-redis serve -h`. Expected: a loopback throwaway store to start, or `serve -h` to say a password is required and where it comes from. Happened: the first serve exited with `NOVA_REDIS_PASSWORD is empty; run under nova-secrets exec ...`; the banner says where the password is read from but `serve -h` does not say one is required, and the remedy needs a sealed secrets store a stranger does not have. Guessed `env NOVA_REDIS_PASSWORD=throwaway`, which worked.

card: redis-serve-says-password-required
- PATHS: cmd/nova-redis/main.go,cmd/nova-redis/serve.go
- task: state in `serve -h` that a password is required and that for a throwaway store exporting NOVA_REDIS_PASSWORD in the serve's environment is enough; name both in the refusal's remedy.

### 4. nova-sprint's help never says how to log in to a store with a password, and names a different variable than nova-redis

Read: `nova-sprint help`, `nova-sprint init -h`, `nova-sprint seat -h`. Expected: one line naming the user and password variables. Happened: the help has no word for password or login; `--redis` mentions only a `seat login` that no usage line shows. The first refusal said `name the user (NOVA_SPRINT_REDIS_USER) and the variable that holds its password` without naming that variable; the second, with the user set, named `NOVA_REDIS_BENCH_PASSWORD`, while nova-redis serve and fn load read `NOVA_REDIS_PASSWORD`. Two retries.

card: sprint-help-store-login
- PATHS: cmd/nova-sprint/helpwords.go
- task: name the store login in the help and in `--redis`'s flag text (NOVA_SPRINT_REDIS_USER and the password variable, with its default name), and make the first NOAUTH refusal name the password variable too.

### 5. A fresh store needs `nova-redis fn load`, which the help does not say

Read: `nova-sprint help` ("A real fleet"). Expected: the steps to bring a new store up. Happened: the refusal `holds no nova_sprint function library; run: nova-redis fn load --addr 127.0.0.1:6379` said it and was right; its command alone failed once for want of the password (the fn load refusal then named NOVA_REDIS_PASSWORD).

card: sprint-help-fn-load-step
- PATHS: cmd/nova-sprint/helpwords.go
- task: list `nova-redis fn load --addr <store>` as the first step of "A real fleet", with the password it needs, before init.

### 6. `fleet beat` takes the member as an argument and refuses `--as`, unlike every other worker verb

Read: `nova-sprint fleet beat -h`. Expected: `--as m1`, as take, finish, queue and read take. Happened: `unknown flag --as`; the member is positional and the actor separate (`fleet beat m1 --actor m1`). One retry.

card: fleet-beat-accepts-as
- PATHS: cmd/nova-sprint/fleet.go
- task: accept `--as <member>` on fleet beat, the member and the actor at once, as the other worker verbs do.

### 7. The judgment said "fewer than two readers up" for a card that needs one

Read: `nova-sprint inbox`, `nova-sprint help reader`. Expected: a judgment naming the reader that was down and how to bring it up. Happened: the finished cards waited with `fewer than two readers up`, though a flash card is read once; the cause was that reader-a had never beaten, and only `help reader` says `queue --as <reader>` is a reader's beat and that it lapses after 15 s.

card: ask-judgment-names-readers
- PATHS: internal/sprint/readers.go
- task: word the judgment with the readers the card needs and the ones down by name, and its remedy `nova-sprint queue --as <reader>` (the beat) or `nova-swarm member --reader`.

### 8. A `REPO: <owner>/<name>` card on a local origin never lands: the lander asks GitHub's merge queue through gh

Read: `nova-swarm template --name card`, `nova-sprint run -h`, `nova-sprint land -h`, the server log; then, to get past it, the source: internal/sprint/mergewindow.go (LandPause), cmd/nova-sprint/mergewindow.go (ghMergeQueue, forgeRepo) and internal/swarm/stage.go (CardRepoURL). Expected: the card template's `REPO: <owner>/<name>` to work against a throwaway origin, with `land --repo-dir` or the run's `--land`. Happened: `local/origin` became `https://github.com/local/origin.git`, and every landing was refused `paused: the merge queue of sprint/s1 could not be read (exec: "gh": executable file not found in $PATH) ... land when it is read clear`, which no action of the coordinator clears; it showed only in the server's log, never in the inbox. The source says a REPO: value that is a path is used as written and asks no merge queue; the three cards were dropped and added again with `REPO: /work/run/origin.git`, and landed.

card: card-repo-local-path
- PATHS: internal/swarm/templates.go,cmd/nova-sprint/helpwords.go
- task: say in the card template and the landing help that REPO: takes a URL or an absolute path as well as owner/name, that owner/name means github.com, and that a path or other host asks no merge queue.

card: land-gh-pause-remedy
- PATHS: internal/sprint/mergewindow.go
- task: give the unreadable-queue pause a remedy (install and log in gh, or name the repository by path or URL), and raise it to the inbox once per stream instead of only the server's log.

### 9. `land --dry-run` said LAND OK; the real land refused on the same cards

Read: `nova-sprint land -h`. Expected: the dry run to show what the real run does. Happened: `LAND OK ... dry_run=yes` and then `LAND REFUSED ... paused: the merge queue ... could not be read`. The dry run asks no merge queue (the source says a nil queue in a dry run asks none), and does not say so.

card: land-dry-run-names-pause
- PATHS: cmd/nova-sprint/land.go
- task: have the dry run print a NOTE that it asks no merge queue, naming the repository the real run would ask, so a pause is foreseen.

### 10. Three one-line edits to adjacent lines of one file would conflict at landing

Read: `nova-sprint help` (the twin walkthrough lands empty commits only). Expected: an example of cards that change content and land together. Happened: none in help; the base was edited mid-run (a blank line between each line of lines.txt, pushed to `sprint/s1` before the cards were worked) so the three merges could not touch one hunk. The runner's own setup mistake, caught before landing; the help gives no example of cards with real changes.

card: sprint-help-three-card-example
- PATHS: cmd/nova-sprint/helpwords.go
- task: extend the walkthrough to three cards that each change their own file and land in one batch, so a first run shows real diffs merging.

### 11. After every card was dropped the machine stopped itself, and add printed only STOPPED

Read: the add's output. Expected: the new cards to be dealt. Happened: the drop left nothing open, the tick declared the sprint done and stopped the machine; the next `add` ended with `STOPPED  0/3 0.0%` and no next command. The help says work added after DONE waits for `nova-sprint start`, but the add's line did not.

card: add-names-start-when-stopped
- PATHS: cmd/nova-sprint/verbs.go
- task: when add lands cards on a STOPPED or DONE machine, print `NOTE the machine is stopped; run: nova-sprint start`.

### 12. The ready-to-merge note counted the dropped cards

Read: `nova-sprint inbox` at the end. Expected: the group of the three landed cards. Happened: `ready to merge  stream=s1  size=6  (line-one,line-three,line-two,path-one,path-three,path-two)`, the three dropped cards still listed in the group though its text says 3 were queued.

card: accept-group-drops-dropped
- PATHS: internal/sprint/steps_tick.go
- task: leave a dropped card out of a HAPPENED group's size and members, or mark it dropped there.

## Verdict

Could a stranger do it: no, not from README and help alone. Everything up to the landing could be done from them, with guesses and retries at the store login (stumbles 3 to 7); the landing of a local origin could not, and was reached only by reading three source files (stumble 8). After that, yes: three cards landed on the throwaway origin (`d7c8368 land path-two (sprint stream s1)`, lines.txt reading ONE, TWO, THREE), the inbox saying `the sprint is done ... 3 landed, 3 dropped`.

Minutes taken: 7 inside the container, from the first command (23:13:25Z) to the last landing (23:19:41Z), and about 10 more before it to copy the source and fill the module cache.
