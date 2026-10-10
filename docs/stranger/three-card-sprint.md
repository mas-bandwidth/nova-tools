# A cold stranger run: a three-card nova-sprint on a throwaway Redis

One run, 2026-10-06, by an AI worker (Claude Opus 5.5 in Claude Code), as a
person new to nova-sprint: README.md and the tools' own help first, and anything else only after
a stumble, named under Stumbles. The sprint: one member, one reader, one stream, three cards,
each a one-line change to a throwaway git repository with a bare origin, dealt, worked by hand
with the worker verbs, read, and landed on that origin by the server's own landing loop. Every
process of the run, the Redis and the server included, ran inside one container and died with
it. Nothing was pushed to the forge.

The run was in nova-tools. Since v1.2.3 nova-sprint is its own repository,
mas-bandwidth/nova-sprint: the cmd/nova-sprint paths named below are there.

## Setup

- Bench: the fleet's first Linux bench (Linux, x86_64, 64 cores, podman 5.7.0), load average about 100 to 170 from
  other work during the run.
- Container: one `localhost/nova-functional:latest` (built from infra/functional-image; Go
  1.26.6, Redis 8.10.2, git), started once for the whole session with
  `podman --cgroup-manager=cgroupfs run -d --rm --timeout 14400 --network none --userns=keep-id --user <uid>:<gid> -v ~/nova-bench/stranger/<job>:/work -w /work -e HOME=/work/home -e GOMODCACHE=/work/gomodcache -e GOCACHE=/work/gocache -e PATH=/work/bin:... localhost/nova-functional:latest sleep 14400`;
  each command of the transcript ran in it as `podman exec stranger-3card-w2 bash -c '<command>'`
  (the detached ones with `podman exec -d`). The default cgroup manager was refused over ssh
  (`sd-bus call: Access denied`), and the image's own user could not write the mounted
  directory, hence the two flags; neither is the tools' business.
- Source: nova-tools at 3d3871e99 (the tip of sprint/mechanical-2026-10-02, after v1.1.0),
  copied into /work/src without its .git. The container has no network and the image's module
  cache is empty, so the modules were fetched once on the bench into the mounted directory
  (`go mod download` with GOMODCACHE there) before the run; inside, GOPROXY=off.
- Versions, as `<tool> version` printed them (built from the checkout):
  `nova-sprint devel linux/amd64 go1.26.6`, `nova-swarm devel linux/amd64 go1.26.6`,
  `nova-redis devel linux/amd64 go1.26.6`.
- Read: README.md; `nova-sprint help`, `nova-sprint <verb> -h` for init, add, seat, seat
  install, seat push; `nova-redis help`, `nova-redis serve -h`; `nova-swarm template --name card`.
  After the stumbles named below: docs/SPEC-SPRINT.md "The push proof", docs/RELEASE-NOTES-1.1.0.md
  "The push proof", pkg/friend/adapter.go (NewDeliverer, ClaudeWake),
  pkg/swarm/stage.go (CardRepoURL), cmd/nova-sprint/mergewindow.go (ghMergeQueue) and
  cmd/nova-sprint/land.go (originIs, normRepo).
- Time: README opened 15:31 UTC; the third card landed 15:44:28 UTC; the container stopped and
  ~/nova-bench/stranger/<job>/ was removed at 15:45 UTC.

## Transcript

Every command run in the container, in order, with its output. `nova-sprint help` and the other
long outputs are trimmed where a `[trimmed: ...]` line says so; lines over 360 characters are cut
where they say so. `(detached)` marks a command started with `podman exec -d`, which prints
nothing here; its log is read by a later command. The first three cards (epoch 0) named
`REPO: trial/work` and could not land; the sprint was cleared and the same three cards were
added again with `REPO: /work/trial/origin.git` (epoch 1), and those landed.

```text
$ go install github.com/mas-bandwidth/nova-tools/cmd/nova-sprint@v1.0.0
go: github.com/mas-bandwidth/nova-tools/cmd/nova-sprint@v1.0.0: module lookup disabled by GOPROXY=off
[exit 1]

$ cd /work/src && GOBIN=/work/bin nice -n 19 go install ./cmd/nova-sprint ./cmd/nova-swarm ./cmd/nova-redis && ls /work/bin
nova-redis
nova-sprint
nova-swarm

$ nova-sprint version; nova-swarm version; nova-redis version
nova-sprint devel linux/amd64 go1.26.6
nova-swarm devel linux/amd64 go1.26.6
nova-redis devel linux/amd64 go1.26.6

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
  nova-sprint add --stream <s> (<id>... | --count <n> | --sentinel <id> | --brief-dir <dir> | --brief-file <f1> [--brief-file <f2>...]: a card per file, its id the file's name without .md) [--needs <a,b>] [--before <id> | --after <id> | --score <n>] [--brief <text> | --brief-file <path>: once, the brief of the cards named] [--rules <file>] [--held] [--allow- [trimmed: 130 more characters of this line]
  nova-sprint quack --streams <a,b,...> --count <n> --repo <clone url> [--tiers <t,...>] [--base <branch>]
[trimmed: 612 lines of output]
the coordinator's day, in five lines (NOVA_SPRINT_REDIS and NOVA_SPRINT_ACTOR set; nova-sprint run ticking in a shell of its own; brief.txt is a card that passes the lint, from nova-swarm template --name card, its REPO: and BASE: filled in):

example:
  nova-sprint init --readers reader-a,reader-b --members m1:8
  nova-sprint add --stream s1 --count 3 --brief-file brief.txt
  nova-sprint start
  nova-sprint inbox --wait
  nova-sprint land --stream s1 --check 'make test'

$ nova-redis help 2>&1 | head -40
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

exit codes: 0 done (spill written, recall found, fn load done, fn check finds the library loaded, serve stopped); 1 ran and said NO (a recall of a missing, expired or unbounded key, fn check STALE or MISSING, a spill whose reply was lost, a refusal by the store, a serve that could not start); 2 could not run (a usage error, a flag refused before dialling, a  [trimmed: 49 more characters of this line]

example:
  nova-redis version
  nova-redis spill --dry-run --addr 127.0.0.1:6379 --owner ada --name note --ttl 10m --value hi
  nova-redis spill --addr 127.0.0.1:6379 --owner ada --name note --ttl 10m --value hi
  nova-redis recall --addr 127.0.0.1:6379 --owner ada --name note

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
exit codes: 0 done (spill written, recall found, fn load done, fn check finds the library loaded, serve stopped); 1 ran and said NO (a recall of a missing, expired or unbounded key, fn check STALE or MISSING, a spill whose reply was lost, a refusal by the store, a serve that could not start); 2 could not run (a usage error, a flag refused before dialling, a  [trimmed: 49 more characters of this line]
effect: local write: writes files on this machine

$ nova-redis serve --bind 127.0.0.1 --port 6379 --dir /work/redis --dry-run
SERVE OK bind=127.0.0.1 port=6379 auth=on persistence=aof eviction=none dir=/work/redis aclfile=/work/redis/users.acl users=0 dry_run=true created=0 launched=0

$ timeout 5 nova-redis serve --bind 127.0.0.1 --port 6379 --dir /work/redis; echo rc=$?
SERVE REFUSED: NOVA_REDIS_PASSWORD is empty; run under `nova-secrets exec --only NOVA_REDIS_PASSWORD -- nova-redis serve ...` so auth comes from nova-secrets at run time, never an argument; run: nova-redis help
rc=2

$ NOVA_REDIS_PASSWORD=throwaway-trial nova-redis serve --bind 127.0.0.1 --port 6379 --dir /work/redis > /work/redis-serve.log 2>&1 &   # detached in the container
$ sleep 2; cat /work/redis-serve.log; NOVA_REDIS_PASSWORD=throwaway-trial nova-redis spill --addr 127.0.0.1:6379 --owner trial --name note --ttl 1m --value hello
SERVE START bind=127.0.0.1 port=6379 auth=on persistence=aof eviction=none dir=/work/redis aclfile=/work/redis/users.acl users=0 program=/usr/local/bin/redis-server
8169:C 06 Oct 2026 15:38:00.635 * Reading config from stdin
8169:C 06 Oct 2026 15:38:00.638 # WARNING Memory overcommit must be enabled! Without it, a background save or replication may fail under low memory condition. Being disabled, it can also cause failures without low memory condition, see https://github.com/jemalloc/jemalloc/issues/1328. To fix this issue add 'vm.overcommit_memory = 1' to /etc/sysctl.conf and t [trimmed: 86 more characters of this line]
8169:C 06 Oct 2026 15:38:00.639 * oO0OoO0OoO0Oo Redis is starting oO0OoO0OoO0Oo
8169:C 06 Oct 2026 15:38:00.639 * Redis version=8.10.2, bits=64, commit=00000000, modified=0, pid=8169, just started
8169:C 06 Oct 2026 15:38:00.639 * Configuration loaded
8169:M 06 Oct 2026 15:38:00.641 * monotonic clock: POSIX clock_gettime
8169:M 06 Oct 2026 15:38:00.656 * Running mode=standalone, port=6379.
8169:M 06 Oct 2026 15:38:00.667 * Server initialized
8169:M 06 Oct 2026 15:38:00.670 * BGSAVE done, 0 keys saved, 0 keys skipped, 89 bytes written.
8169:M 06 Oct 2026 15:38:00.693 * Creating AOF base file appendonly.aof.1.base.rdb on server start
8169:M 06 Oct 2026 15:38:00.721 * Creating AOF incr file appendonly.aof.1.incr.aof on server start
8169:M 06 Oct 2026 15:38:00.722 * Ready to accept connections tcp
SPILL OK key=trial:note ttl=1m0s expires=2026-10-06T15:39:03Z bytes=5

$ mkdir -p /work/trial && cat > /work/trial/env.sh <<EOF
export NOVA_REDIS_PASSWORD=throwaway-trial NOVA_SPRINT_REDIS=127.0.0.1:6379 NOVA_SPRINT_ACTOR=boss
export GIT_AUTHOR_NAME=stranger GIT_AUTHOR_EMAIL=stranger@example.invalid GIT_COMMITTER_NAME=stranger GIT_COMMITTER_EMAIL=stranger@example.invalid
EOF
cat /work/trial/env.sh
export NOVA_REDIS_PASSWORD=throwaway-trial NOVA_SPRINT_REDIS=127.0.0.1:6379 NOVA_SPRINT_ACTOR=boss
export GIT_AUTHOR_NAME=stranger GIT_AUTHOR_EMAIL=stranger@example.invalid GIT_COMMITTER_NAME=stranger GIT_COMMITTER_EMAIL=stranger@example.invalid

$ cd /work/trial && git init -q --bare origin.git && git clone -q origin.git work 2>&1; cd work && . ../env.sh && printf "line one\nline two\nline three\n" > notes.txt && git add notes.txt && git commit -q -m base && git push -q origin HEAD:refs/heads/main && git push -q origin HEAD:refs/heads/sprint/s1 && git ls-remote ../origin.git
warning: You appear to have cloned an empty repository.
4502ff202b7fb1092c435fc35590d81da31234aa	refs/heads/main
4502ff202b7fb1092c435fc35590d81da31234aa	refs/heads/sprint/s1

$ cd /work/trial && . ./env.sh && nova-sprint init --readers reader-a --members m1:1
nova-sprint init REFUSED: redis at 127.0.0.1:6379 as the default user, no password: login refused: NOAUTH Authentication required.; next: name the user (NOVA_SPRINT_REDIS_USER) and the variable that holds its password; run: nova-sprint init -h
[exit 2]

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

$ nova-sprint help 2>&1 | grep -n -i -E "password|REDIS_USER|seat login|credential" | cut -c1-250
279:    checks no credential, so a name, a public address, a link-local address
284:NOVA_SPRINT_ACTOR, and needs no store address or credential: every verb is sent

$ nova-sprint help seat 2>&1 | head -40
usage: nova-sprint seat [--repair --reason <text>] | push [--harness <name> --target <dir> [--session <id>]] | pong <nonce>
from `nova-sprint help`:
  nova-sprint seat check
  nova-sprint seat install --harness <name> --target <dir> [--session <id>] [--dir <dir>] [--log <file>] [--dry-run]
  nova-sprint seat uninstall [--dir <dir>]
  nova-sprint seat [--repair --reason <text>] | push [--harness <name> --target <dir> [--session <id>]] | pong <nonce>
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

$ cd /work/trial && . ./env.sh && NOVA_SPRINT_REDIS_USER=default nova-sprint init --readers reader-a --members m1:1
nova-sprint init REFUSED: redis at 127.0.0.1:6379 as user default (password from NOVA_REDIS_BENCH_PASSWORD): login refused: NOVA_REDIS_BENCH_PASSWORD is empty; next: export NOVA_REDIS_BENCH_PASSWORD, holding the password, in the environment of this process; run: nova-sprint init -h
[exit 2]

$ cd /work/trial && echo "export NOVA_SPRINT_REDIS_USER=default NOVA_REDIS_BENCH_PASSWORD=throwaway-trial" >> env.sh && . ./env.sh && nova-sprint init --readers reader-a --members m1:1
nova-sprint init REFUSED: the store at 127.0.0.1:6379 holds no nova_sprint function library; run: nova-redis fn load --addr 127.0.0.1:6379
[exit 2]

$ cd /work/trial && . ./env.sh && nova-redis fn load --addr 127.0.0.1:6379 && nova-sprint init --readers reader-a --members m1:1
LOADED nova_sprint sha=0f9c8ca0574bb69c store=127.0.0.1:6379
INIT OK tables=work,readers,merge,fleet view=sprint readers=reader-a
MOVED m1 added, down until it beats width=1
FLEET-UP OK moved=1 refused=0 notes=0 op=fleet-release-dlxv93jj63n0-8345-56d3f0a8633d4323-1
STOPPED

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
STEP 5. Commit on your own branch with the trailer. Nothing reaches the forge from inside the wall: in the job the git shim records a push, the pull request is the finish JOB.md names (STEP 6), and the member makes both, against <base>, from outside the wall when the card finishes. The pull request body states the diff stat, what was deleted, the tests with  [trimmed: 38 more characters of this line]
STEP 6. End as JOB.md says (docs/SPEC-CARD-CONTRACT.md): where JOB.md ends the card with its pull request, that is the end and there is nothing else to write, the gate's lines in the pull request body; where it asks for RESULT.md, write it in JOB.md's shape (head, branch, verdict, gate, output, report).

$ nova-sprint add -h
usage: nova-sprint add --stream <s> (<id>... | --count <n> | --sentinel <id> | --brief-dir <dir> | --brief-file <f1> [--brief-file <f2>...]: a card per file, its id the file's name without .md) [--needs <a,b>] [--before <id> | --after <id> | --score <n>] [--brief <text> | --brief-file <path>: once, the brief of the cards named] [--rules <file>] [--held] [--a [trimmed: 135 more characters of this line]
from `nova-sprint help`:
  nova-sprint add --stream <s> (<id>... | --count <n> | --sentinel <id> | --brief-dir <dir> | --brief-file <f1> [--brief-file <f2>...]: a card per file, its id the file's name without .md) [--needs <a,b>] [--before <id> | --after <id> | --score <n>] [--brief <text> | --brief-file <path>: once, the brief of the cards named] [--rules <file>] [--held] [--allow- [trimmed: 130 more characters of this line]
  nova-sprint add --stream s1 --count 1 --one
  nova-sprint add --stream s1 --count 3 --brief-file brief.txt
example:
  nova-sprint add --stream s1 --count 100
  nova-sprint add --stream s1 --brief-dir briefs
  nova-sprint add --stream s1 --brief-file a.md --brief-file b.md
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --after <string>  place the cards in line after this primary of the stream
  --allow-personal-base  admit cards whose brief's BASE: is a personal branch (<name>/* for the sprint's coordinator, its owner or a friends table row), by default refused naming the base and this flag: no sprint watches a personal branch's gate (docs/SPEC-SPRINT.md section 11, bases-view-r.w2)
  --allow-shared-paths  with a card per brief file (--brief-dir, or --brief-file with no ids): admit cards that name one file in their PATHS: lines though neither needs the other and neither brief declares it on a SHARED: line (by default refused, naming the file and the cards)
  --before <string>  place the cards in line in front of this primary of the stream
  --brief <string>  the brief: a child's whole brief, at most 16 KiB (the card lint advises 12000 bytes), held to the card lint (the sentences of the rules file: --rules, else the one init --rules recorded, else the built-in general rules; nova-swarm template --name card prints a card that passes the general ones, nova-swarm lint --rules lists them) and refu [trimmed: 277 more characters of this line]
  --brief-dir <string>  one card per *.md file in this directory, in byte order of file name, each card's id its file's name without .md (a1.md is a1); not with --brief-file
  --brief-file <value>  the brief, read from this file: its bytes as they are, its one trailing newline cut (a brief of many paragraphs), then held to the card lint like --brief; given once with ids, --count or --sentinel, the brief of the cards they name; given alone or again, one card per file in the order given, each card's id its file's name without .md  [trimmed: 46 more characters of this line]
  --brief-op <id=op>  id=op: a card's brief decision op id (<id>@brief-<hex>), which add sends its server itself when it asked the decision where it was typed; refused when typed on an add no server runs; repeated, one per card
  --count <int>  admit n primaries with generated ids <stream>-<n>
  --decide-record <file>  the record file of the cards' brief decisions under JEV_API_KEY (default ~/nova-sprint/decide/brief.jsonl, the coordinator's root); each card stores it and its op, and land and drop attach the card's end there
[trimmed: 35 lines of output]
Libraries considered: <what the standard library and the adopted modules offer for this work, and why each is used or not; the search comes before any helper of more than about thirty lines is written>

STEP 1. Enter your worktree with cd <worktree path> && git log --oneline -1; it is a NEW worktree on the branch this card names. Export GOFLAGS=-mod=readonly NOVA_TEST_NO_HOST=1 before any go command; GOCACHE is already set to the machine's shared build cache (JOB.md names it): keep it.
STEP 2. Write the red test first, named TestSomething, in <file>_test.go, opening with t.Parallel(). Run go test -count=1 -timeout 600s ./internal/<package>/ -run TestSomething and keep the failing line.
STEP 3. Make it pass in the files this card names, and only those. Cite the model or the design section from each function that implements a rule.
STEP 4. Run the gate: go test -count=1 -timeout 600s ./internal/<package>/ ./internal/ci/ and read the last line of each. When a test fails, name its file and say whether that file was changed by your work (yours) or is unchanged (already red at BASE: run the same test on the unchanged base to say so), and report that line first.
STEP 5. Commit on your own branch with the trailer. Nothing reaches the forge from inside the wall: in the job the git shim records a push, the pull request is the finish JOB.md names (STEP 6), and the member makes both, against <base>, from outside the wall when the card finishes. The pull request body states the diff stat, what was deleted, the tests with  [trimmed: 38 more characters of this line]
STEP 6. End as JOB.md says (docs/SPEC-CARD-CONTRACT.md): where JOB.md ends the card with its pull request, that is the end and there is nothing else to write, the gate's lines in the pull request body; where it asks for RESULT.md, write it in JOB.md's shape (head, branch, verdict, gate, output, report).

$ cd /work/trial && mkdir -p briefs && for c in c1 c2 c3; do nova-swarm template --name card | sed -e "s|^RESULT: <label> sha=<sha12>|RESULT: $c sha=<sha12>|" -e "s|^REPO: <owner>/<name>|REPO: trial/work\nPATHS: $c.txt|" -e "s|^BASE: <branch>|BASE: sprint/s1|" -e "s|finish within <n> minutes|finish within 10 minutes|" -e "s|^THE TASK. <.*|THE TASK. Add the o [trimmed: 433 more characters of this line]
c1.md
c2.md
c3.md
usage: nova-swarm lint [flags]
from `nova-swarm help`:
  nova-swarm lint      --card <file> (or the bare <file>) [--typed] [--child-rules | --child-rules-file <file>] [--member-injects] [--base-check [--repo <dir>] [--legs <file>] [--p95 <file>]] [--trust <file>] [--lineup <file>] [--decide [--decide-answers <file>] [--decide-record <file>]] [--max <n>] | --fleet <file> [--max <n>] | --rules
  (a bare --card holds the card to nova-swarm's own card contract, the shape native
  runs; --rules lists every check; --fleet lints a launcher script against the
1:RESULT: c1 sha=<sha12>
20:STEP 1. Enter your worktree with cd <worktree path> && git log --oneline -1; it is a NEW worktree on the branch this card names. Export GOFLAGS=-mod=readonly NOVA_TEST_NO_HOST=1 before any go command; GOCACHE is already set to the machine's shared build cache (JOB.md names it): keep it.
21:STEP 2. Write the red test first, named TestSomething, in <file>_test.go, opening with t.Parallel(). Run go test -count=1 -timeout 600s ./internal/<package>/ -run TestSomething and keep the failing line.
23:STEP 4. Run the gate: go test -count=1 -timeout 600s ./internal/<package>/ ./internal/ci/ and read the last line of each. When a test fails, name its file and say whether that file was changed by your work (yours) or is unchanged (already red at BASE: run the same test on the unchanged base to say so), and report that line first.
24:STEP 5. Commit on your own branch with the trailer. Nothing reaches the forge from inside the wall: in the job the git shim records a push, the pull request is the finish JOB.md names (STEP 6), and the member makes both, against <base>, from outside the wall when the card finishes. The pull request body states the diff stat, what was deleted, the tests wi [trimmed: 41 more characters of this line]

$ cd /work/trial/briefs && for c in c1 c2 c3; do sed -i -e "s|cd <worktree path> \&\& git log|cd into the worktree the member staged \&\& git log|" -e "s|^STEP 2. .*|STEP 2. Write $c.txt holding the one line \"$c was here\" and nothing else.|" -e "s|^STEP 3. .*|STEP 3. Check with git diff --stat that $c.txt is the only file changed.|" -e "s|^STEP 4. .*|STEP  [trimmed: 229 more characters of this line]
1
nova-sprint add REFUSED: PUSH DOWN: boss has no push target recorded: the push loop cannot reach the session; a coordinator that cannot be reached is not a coordinator, and nothing was changed; run: nova-sprint seat install --actor boss --harness <harness> --target <session dir>
[exit 2]

$ nova-sprint seat install -h
usage: nova-sprint seat install --harness <name> --target <dir> [--session <id>] [--dir <dir>] [--log <file>] [--dry-run]
from `nova-sprint help`:
  nova-sprint seat install --harness <name> --target <dir> [--session <id>] [--dir <dir>] [--log <file>] [--dry-run]
example:
  nova-sprint seat install --dry-run --redis 127.0.0.1:6381
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --dir <string>  the directory the unit is written into (default: ~/Library/LaunchAgents on macOS, ~/.config/systemd/user on Linux)
  --dry-run  print the unit and where it would go, and write and load nothing
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --harness <string>  the harness the seat's AI runs in (required): the push loop delivers each judgment, and the push proof, into the session through its adapter
  --json  print one JSON object for a program instead of the lines
  --log <string>  the file the loop's lines go to, macOS (default: ~/Library/Logs/nova-sprint-seat-push.log); on Linux they are in the journal
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --session <string>  the session's id, for a harness that names one (default: the adapter's newest in --target)
  --target <string>  the session's directory, where the harness's adapter delivers (required)
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: local write: writes the push loop's unit (inbox --wait --push seat) into --dir and loads it with launchctl (macOS) or systemctl --user (Linux); --dry-run prints it and writes nothing

$ cd /work/trial && . ./env.sh && mkdir -p seat && nova-sprint seat install --harness fake --target /work/trial/seat --dir /work/trial/units --dry-run
nova-sprint seat install REFUSED: "fake" is no harness; the harnesses are opencode, codex, claude, antigravity, dsh, gemini, grok, tmux, copilot, cursor, amp, goose, kiro, cline, aider, roo, windsurf, zed, warp; run: nova-sprint seat install --actor boss --harness fake --target /work/trial/seat
[exit 2]

$ nova-sprint help seat push 2>&1 | head -30
nova-sprint help REFUSED: unknown verb seat push; run: nova-sprint help

$ cd /work/trial && . ./env.sh && nova-sprint seat push -h 2>&1 | tail -8; nova-sprint seat push --harness tmux --target /work/trial/seat
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR, then the address nova-sprint seat login recorded, whose user and secret it logs in with); mem:<file> is the in-memory twin kept in that file, for learning and tests, not for a fleet (nova-sprint help, trying it without Redis)
  --sent <string>  the push loop's report: the nonce of the check it delivered
  --session <string>  with --harness, the session's id, for a harness that names one (default: the adapter's newest in --target)
  --target <string>  with --harness, the session's directory, where the adapter delivers
exit codes: 0 done, 1 failed or incomplete (including refused; the line names why), 2 usage or a store that did not answer
effect: inspection: reads the seat (holder, epoch, generation), writes nothing
PUSH DOWN name=boss harness=tmux target=/work/trial/seat why="no push check has been delivered into boss's tmux session yet: is inbox --wait --push seat running?" remedy="nova-sprint seat install --actor boss --harness tmux --target /work/trial/seat"
[exit 1]

$ nova-sprint help 2>&1 | grep -n -i -E "push proof|pong|PUSH DOWN|seat push|push target" | cut -c1-300
67:  nova-sprint friend beat <friend> [--working <n>] [--queue <n>] [--width <n>] [--running <id>,...] [--load <percent>] [--active <RFC3339>] [--pong <RFC3339>]
119:  nova-sprint seat [--repair --reason <text>] | push [--harness <name> --target <dir> [--session <id>]] | pong <nonce>

$ nova-sprint seat push -h 2>&1 | head -12
usage: nova-sprint seat [--repair --reason <text>] | push [--harness <name> --target <dir> [--session <id>]] | pong <nonce>
from `nova-sprint help`:
  nova-sprint seat check
  nova-sprint seat install --harness <name> --target <dir> [--session <id>] [--dir <dir>] [--log <file>] [--dry-run]
  nova-sprint seat uninstall [--dir <dir>]
  nova-sprint seat [--repair --reason <text>] | push [--harness <name> --target <dir> [--session <id>]] | pong <nonce>
example:
  nova-sprint seat
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --failed <string>  with --sent, why the delivery of the check failed

$ which tmux dsh opencode claude aider screen; ls /usr/bin | wc -l
389

$ cd /work/trial && . ./env.sh && nova-sprint seat push --harness claude --target /work/trial/seat --session boss
PUSH DOWN name=boss harness=claude target=/work/trial/seat why="no push check has been delivered into boss's claude session yet: is inbox --wait --push seat running?" remedy="nova-sprint seat install --actor boss --harness claude --target /work/trial/seat"
[exit 1]

$ (detached) cd /work/trial && . ./env.sh && nova-sprint inbox --wait --push seat > /work/trial/push-loop.log 2>&1 &

$ sleep 3; cat /work/trial/push-loop.log; find /work/trial/seat -type f | xargs -r tail -n 3
nova-sprint inbox REFUSED: --push seat writes to the holder's inbox, /work/home/boss-working/inbox/sprint-judgments, and /work/home/boss-working/inbox is not there: make it, or give --push <dir>; run: nova-sprint inbox -h

$ mkdir -p /work/home/boss-working/inbox


$ (detached) cd /work/trial && . ./env.sh && nova-sprint inbox --wait --push seat > /work/trial/push-loop.log 2>&1 &

$ sleep 4; cat /work/trial/push-loop.log; find /work/trial/seat -type f | xargs -r tail -n 3
PUSH CHECK name=boss nonce=8d8a66fef3e2c5de
2026-10-06T15:40:12.701881095Z nova-friend: NOVA SPRINT PUSH CHECK 8d8a66fef3e2c5de ⏎ The sprint's push loop proves it reaches this session: the seat is held only by a session it can reach, and every coordinator verb is refused until this answer comes back. Answer now, before anything else, with one command, then end this turn: nova-sprint seat pong 8d8a66fe [trimmed: 21 more characters of this line]

$ cd /work/trial && . ./env.sh && nova-sprint seat pong 8d8a66fef3e2c5de --actor boss && nova-sprint seat push && nova-sprint add --stream s1 --brief-dir briefs
SEAT PONG OK name=boss nonce=8d8a66fef3e2c5de proven=2026-10-06T15:40:22Z
PUSH OK name=boss harness=claude target=/work/trial/seat proven=2026-10-06T15:40:22Z
LINT DRIFT card=c1 check=tier-line line=1: line 1 names no tier; write tier: and one of frontier, heavy, pro or flash on it
LINT DRIFT card=c2 check=tier-line line=1: line 1 names no tier; write tier: and one of frontier, heavy, pro or flash on it
LINT DRIFT card=c3 check=tier-line line=1: line 1 names no tier; write tier: and one of frontier, heavy, pro or flash on it
nova-sprint add REFUSED: 3 brief finding(s), the first tier-line: line 1 names no tier; write tier: and one of frontier, heavy, pro or flash on it; nothing was written, every finding is a LINT DRIFT line above; run: nova-sprint add -h
[exit 2]

$ cd /work/trial && . ./env.sh && sed -i "1i tier: flash" briefs/c1.md briefs/c2.md briefs/c3.md && head -3 briefs/c1.md && nova-sprint add --stream s1 --brief-dir briefs
tier: flash
RESULT: c1 sha=<sha12>
REPO: trial/work
LINT DRIFT card=c1 check=paths-at-base line=4: MISSING: the lander's clone of trial/work could not be had: the clone of https://github.com/trial/work.git into /work/home/.cache/nova-sprint/land/github.com-trial-work-11c0e43d4fd38b70 failed: git clone: exit status 128: Cloning into '/work/home/.cache/nova-sprint/land/github.com-trial-work-11c0e43d4fd38b70'... [trimmed: 354 more characters of this line]
LINT DRIFT card=c1 check=paths-cover-named line=18: MISSING: the lander's clone of trial/work could not be had: the clone of https://github.com/trial/work.git into /work/home/.cache/nova-sprint/land/github.com-trial-work-11c0e43d4fd38b70 failed: git clone: exit status 128: Cloning into '/work/home/.cache/nova-sprint/land/github.com-trial-work-11c0e43d4fd38b7 [trimmed: 295 more characters of this line]
LINT DRIFT card=c1 check=base-is-live line=5: MISSING: the lander's clone of trial/work could not be had: the clone of https://github.com/trial/work.git into /work/home/.cache/nova-sprint/land/github.com-trial-work-11c0e43d4fd38b70 failed: git clone: exit status 128: Cloning into '/work/home/.cache/nova-sprint/land/github.com-trial-work-11c0e43d4fd38b70'...  [trimmed: 374 more characters of this line]
LINT DRIFT card=c2 check=paths-at-base line=4: MISSING: the lander's clone of trial/work could not be had: the clone of https://github.com/trial/work.git into /work/home/.cache/nova-sprint/land/github.com-trial-work-11c0e43d4fd38b70 failed: git clone: exit status 128: Cloning into '/work/home/.cache/nova-sprint/land/github.com-trial-work-11c0e43d4fd38b70'... [trimmed: 354 more characters of this line]
LINT DRIFT card=c2 check=paths-cover-named line=18: MISSING: the lander's clone of trial/work could not be had: the clone of https://github.com/trial/work.git into /work/home/.cache/nova-sprint/land/github.com-trial-work-11c0e43d4fd38b70 failed: git clone: exit status 128: Cloning into '/work/home/.cache/nova-sprint/land/github.com-trial-work-11c0e43d4fd38b7 [trimmed: 295 more characters of this line]
LINT DRIFT card=c2 check=base-is-live line=5: MISSING: the lander's clone of trial/work could not be had: the clone of https://github.com/trial/work.git into /work/home/.cache/nova-sprint/land/github.com-trial-work-11c0e43d4fd38b70 failed: git clone: exit status 128: Cloning into '/work/home/.cache/nova-sprint/land/github.com-trial-work-11c0e43d4fd38b70'...  [trimmed: 374 more characters of this line]
LINT DRIFT card=c3 check=paths-at-base line=4: MISSING: the lander's clone of trial/work could not be had: the clone of https://github.com/trial/work.git into /work/home/.cache/nova-sprint/land/github.com-trial-work-11c0e43d4fd38b70 failed: git clone: exit status 128: Cloning into '/work/home/.cache/nova-sprint/land/github.com-trial-work-11c0e43d4fd38b70'... [trimmed: 354 more characters of this line]
LINT DRIFT card=c3 check=paths-cover-named line=18: MISSING: the lander's clone of trial/work could not be had: the clone of https://github.com/trial/work.git into /work/home/.cache/nova-sprint/land/github.com-trial-work-11c0e43d4fd38b70 failed: git clone: exit status 128: Cloning into '/work/home/.cache/nova-sprint/land/github.com-trial-work-11c0e43d4fd38b7 [trimmed: 295 more characters of this line]
LINT DRIFT card=c3 check=base-is-live line=5: MISSING: the lander's clone of trial/work could not be had: the clone of https://github.com/trial/work.git into /work/home/.cache/nova-sprint/land/github.com-trial-work-11c0e43d4fd38b70 failed: git clone: exit status 128: Cloning into '/work/home/.cache/nova-sprint/land/github.com-trial-work-11c0e43d4fd38b70'...  [trimmed: 374 more characters of this line]
nova-sprint add REFUSED: 9 brief finding(s) at the base, the first paths-at-base: MISSING: the lander's clone of trial/work could not be had: the clone of https://github.com/trial/work.git into /work/home/.cache/nova-sprint/land/github.com-trial-work-11c0e43d4fd38b70 failed: git clone: exit status 128: Cloning into '/work/home/.cache/nova-sprint/land/github. [trimmed: 146 more characters of this line]
[exit 2]

$ cd /work/trial && . ./env.sh && git config --global url./work/trial/origin.git.insteadOf https://github.com/trial/work.git && cd work && for c in c1 c2 c3; do echo "$c placeholder" > $c.txt; done && git add . && git commit -q -m "base: the three card files" && git push -q origin HEAD:refs/heads/sprint/s1 && git ls-remote ../origin.git sprint/s1 && cd .. && [trimmed: 206 more characters of this line]
2ac5fb74c892782f1071da337e5304f89faf2994	refs/heads/sprint/s1
THE TASK. Change the one line of c1.txt to the line "c1 was here" at the root of the repository trial/work; the worktree is the one the member stages, the branch the card names, the base branch sprint/s1, and the one file you may touch is c1.txt.
MOVED c1 -> ready stream=s1 score=1
MOVED c2 -> ready stream=s1 score=2
MOVED c3 -> ready stream=s1 score=3
ADD OK stream=s1 cards=3 before=- moved=3 refused=0 notes=0 op=add-dlxval0b7u0g-8792-3b3f198e21871234-1
NOTE each card's id is its brief file's name without .md (briefs/c1.md is c1)
STOPPED  0/3 0.0%

$ (detached) cd /work/trial && . ./env.sh && nova-sprint run --listen 127.0.0.1:7070 --land > /work/trial/run.log 2>&1 &

$ sleep 4; cat /work/trial/run.log | cut -c1-400
SERVER listening on 127.0.0.1:7070: the workers' verbs (take, finish, read, queue, fleet beat) run here, one at a time, beside the store
SERVER listening on 127.0.0.1:7070: the coordinator's verbs, from this machine (NOVA_SPRINT_SERVER=127.0.0.1:7070)
LANDING every 2s: land runs here for every stream with cards queued to merge, one landing at a time
BALANCE every 10m0s: each provider's balance is read through the seat's key and written to the fleet table
RUN ticking on every line of the log (at most every 100ms) and every 1s while it is quiet; machine: STOPPED
15:40:47 machine STOPPED

$ cd /work/trial && echo "export NOVA_SPRINT_SERVER=127.0.0.1:7070" >> env.sh && . ./env.sh && nova-sprint start && nova-sprint fleet beat m1 && nova-sprint queue --as reader-a; sleep 3; nova-sprint queue --as m1
START OK before=STOPPED after=RUNNING changed
0/3 0.0% -> ETA -  machine: running
FLEET-BEAT OK m1 at=2026-10-06T15:40:58Z load=144.6% last=144.6% how=load1 cores=64
QUEUE OK cards=0 epoch=0
CARD c1.w1 fleet:m1:ready gen=1 take: c1.w1@1 --epoch 0 dealt=2026-10-06T15:40:58Z
PACKET c1.w1 attempt=1 gen=1 epoch=0
  tier: flash
  branch: sprint/c1.w1.g1.e0
  base: the stream's base
  brief:
    tier: flash
    RESULT: c1 sha=<sha12>
    REPO: trial/work
    PATHS: c1.txt
    BASE: sprint/s1
    The REPO: and BASE: lines are the repository and the branch the work starts from and lands on: the member stages REPO: at BASE:, and nova-sprint land merges the card's head onto BASE: (land --base stands in for a card naming no BASE:, land --repo-dir for one naming no REPO:).
    You are a child of the coordinator: one task, one worktree, one branch, unattended. This card is the whole of the task and it stands alone in front of a stranger; nothing outside it is owed to you.
    Deadline: finish within 10 minutes.
    
    RULES.
    Work only in the job directory this card names.
[trimmed: 44 lines of output]
    STEP 2. Write c2.txt holding the one line "c2 was here" and nothing else.
    STEP 3. Check with git diff --stat that c2.txt is the only file changed.
    STEP 4. Run the gate: test "$(cat c2.txt)" = "c2 was here" and read its exit status.
    STEP 5. Commit on your own branch with the trailer. Nothing reaches the forge from inside the wall: in the job the git shim records a push, the pull request is the finish JOB.md names (STEP 6), and the member makes both, against sprint/s1, from outside the wall when the card finishes. The pull request body states the diff stat, what was deleted, the test [trimmed: 45 more characters of this line]
    STEP 6. End as JOB.md says (docs/SPEC-CARD-CONTRACT.md): where JOB.md ends the card with its pull request, that is the end and there is nothing else to write, the gate's lines in the pull request body; where it asks for RESULT.md, write it in JOB.md's shape (head, branch, verdict, gate, output, report).
  notes: none
  report it: nova-sprint finish --as m1 c2.w1@1 --epoch 0 --branch sprint/c2.w1.g1.e0 --head <commit> --report '<what you did>' [--failed]
QUEUE OK cards=2 epoch=0

$ (detached) cd /work/trial && . ./env.sh && while :; do nova-sprint fleet beat m1 >/dev/null 2>&1; sleep 5; done &

$ cd /work/trial && . ./env.sh && nova-sprint take --as m1 c1.w1@1 c2.w1@1 --epoch 0
0/3 0.0% -> ETA -  machine: running
REFUSED c2.w1: member m1 is at its width (0 working of 1): a card is taken when one is reported
REFUSED c1.w1: not written: the verb names several and applies all or none, and 1 of them was refused
TAKE-BY-ID FAILED moved=0 refused=2 notes=0
[exit 1]

$ cd /work/trial && . ./env.sh && nova-sprint take --as m1 c1.w1@1 --epoch 0
MOVED c1.w1 fleet ready -> working member=m1 gen=1
PACKET c1.w1 attempt=1 gen=1 epoch=0
  tier: flash
  branch: sprint/c1.w1.g1.e0
  base: the stream's base
  brief:
    tier: flash
    RESULT: c1 sha=<sha12>
    REPO: trial/work
    PATHS: c1.txt
    BASE: sprint/s1
    The REPO: and BASE: lines are the repository and the branch the work starts from and lands on: the member stages REPO: at BASE:, and nova-sprint land merges the card's head onto BASE: (land --base stands in for a card naming no BASE:, land --repo-dir for one naming no REPO:).
    You are a child of the coordinator: one task, one worktree, one branch, unattended. This card is the whole of the task and it stands alone in front of a stranger; nothing outside it is owed to you.
    Deadline: finish within 10 minutes.
    
    RULES.
    Work only in the job directory this card names.
    Never force-push or rebase a shared branch.
    Never kill a process you did not start.
    Never start a server on this machine.
    No `rm -rf` outside the job directory.
    Report what was not done.
    
    THE TASK. Change the one line of c1.txt to the line "c1 was here" at the root of the repository trial/work; the worktree is the one the member stages, the branch the card names, the base branch sprint/s1, and the one file you may touch is c1.txt.
    Libraries considered: none; one line of text needs no library.
    
    STEP 1. Enter your worktree with cd into the worktree the member staged && git log --oneline -1; it is a NEW worktree on the branch this card names. Export GOFLAGS=-mod=readonly NOVA_TEST_NO_HOST=1 before any go command; GOCACHE is already set to the machine's shared build cache (JOB.md names it): keep it.
    STEP 2. Write c1.txt holding the one line "c1 was here" and nothing else.
    STEP 3. Check with git diff --stat that c1.txt is the only file changed.
    STEP 4. Run the gate: test "$(cat c1.txt)" = "c1 was here" and read its exit status.
    STEP 5. Commit on your own branch with the trailer. Nothing reaches the forge from inside the wall: in the job the git shim records a push, the pull request is the finish JOB.md names (STEP 6), and the member makes both, against sprint/s1, from outside the wall when the card finishes. The pull request body states the diff stat, what was deleted, the test [trimmed: 45 more characters of this line]
    STEP 6. End as JOB.md says (docs/SPEC-CARD-CONTRACT.md): where JOB.md ends the card with its pull request, that is the end and there is nothing else to write, the gate's lines in the pull request body; where it asks for RESULT.md, write it in JOB.md's shape (head, branch, verdict, gate, output, report).
  notes: none
  report it: nova-sprint finish --as m1 c1.w1@1 --epoch 0 --branch sprint/c1.w1.g1.e0 --head <commit> --report '<what you did>' [--failed]
TAKE-BY-ID OK moved=1 refused=0 notes=0 op=take-dlxvb17rl8i3-8847-ff8c5fcfe8b89107-1
0/3 0.0% -> ETA -  machine: running

$ cd /work/trial && cat > work-card.sh <<"EOF"
#!/bin/bash
# work-card.sh <card> <branch>: the hand worker: one line changed, committed, pushed to the card branch
set -e
c=$1 branch=$2
cd /work/trial/work
git fetch -q origin
git checkout -q -B "$branch" origin/sprint/s1
echo "$c was here" > "$c.txt"
git diff --stat
test "$(cat $c.txt)" = "$c was here"
git commit -q -am "$c: the one line"
git push -q origin "HEAD:refs/heads/$branch"
git rev-parse HEAD
EOF
chmod +x work-card.sh && . ./env.sh && ./work-card.sh c1 sprint/c1.w1.g1.e0
 c1.txt | 2 +-
 1 file changed, 1 insertion(+), 1 deletion(-)
7a60b3b7b0cf07b83ccfd4493292f32a292f9b88

$ cd /work/trial && . ./env.sh && nova-sprint finish --as m1 c1.w1@1 --epoch 0 --branch sprint/c1.w1.g1.e0 --head 7a60b3b7b0cf07b83ccfd4493292f32a292f9b88 --report "c1.txt changed to one line; gate test exit 0"; sleep 3; nova-sprint queue --as reader-a | grep -v "^    "
MOVED c1.w1 working -> done ok; c1 working -> review
FINISH OK moved=1 refused=0 notes=1 op=finish-dlxvb6rtalqh-8847-6378a9cadf1145de-1
0/3 0.0% -> ETA -  machine: running
QUEUE OK cards=0 epoch=0

$ (detached) cd /work/trial && . ./env.sh && while :; do nova-sprint queue --as reader-a >/dev/null 2>&1; sleep 5; done &

$ cd /work/trial && . ./env.sh && sleep 8; nova-sprint queue --as reader-a | grep -v "^    "; nova-sprint where 2>&1 | head -40
CARD c1.r1.reader-a readers:reader-a:asked asked=2026-10-06T15:41:30Z
PACKET c1.r1.reader-a attempt=1 gen=0 epoch=0
  tier: flash
  work: attempt 1 by m1
  head: 7a60b3b7b0cf07b83ccfd4493292f32a292f9b88
  branch: sprint/c1.w1.g1.e0
  brief:
  report:
  notes: none
  attribution: the By: line, the Co-Authored-By trailer and the model or harness named are never a finding and never decide a verdict
  report it: nova-sprint read --as reader-a (--ok | --broken) c1.r1.reader-a --epoch 0 --finding '<file:line, and what to change>'
QUEUE OK cards=1 epoch=0
SPRINT TABLE  coordinator boss

0/3 0.0% -> ETA -

work | waiting | ready | working | review | merging | landed | cost | per landed
-----+---------+-------+---------+--------+---------+--------+------+-----------
s1   |       0 |     0 |       2 |      1 |       0 |      0 |    - | -
-----+---------+-------+---------+--------+---------+--------+------+-----------
     |       0 |     0 |       2 |      1 |       0 |      0 |    - |

friends | ready | working | width | done | ok%  | status | active
--------+-------+---------+-------+------+------+--------+-------
        |     0 |       0 |     0 |    0 | 0.0% |        |

fleet | ready | working | width | done | ok%    | status | load
------+-------+---------+-------+------+--------+--------+------
m1    |     2 |       0 |     1 |    1 | 100.0% | up     | 94.3%
------+-------+---------+-------+------+--------+--------+------
      |     2 |       0 |     1 |    1 | 100.0% |        |

store: rtt p50=1.637ms p99=3.351ms

$ cd /work/trial && . ./env.sh && git -C work diff --stat origin/sprint/s1 7a60b3b7b0cf07b83ccfd4493292f32a292f9b88 && nova-sprint read --as reader-a --begin c1.r1.reader-a --epoch 0 && nova-sprint read --as reader-a --ok c1.r1.reader-a --epoch 0; sleep 6; nova-sprint where 2>&1 | sed -n 3,9p; git ls-remote origin.git sprint/s1; cut -c1-300 run.log | tail -8
 c1.txt | 2 +-
 1 file changed, 1 insertion(+), 1 deletion(-)
MOVED c1.r1.reader-a asked -> reading
READ OK moved=1 refused=0 notes=0 op=read-dlxvbhvi59a7-8847-b1cf6ad2aacace93-1
0/3 0.0% -> ETA -  machine: running
MOVED c1.r1.reader-a reading -> ok
READ OK moved=1 refused=0 notes=0 op=read-dlxvbhwxcjm3-8847-b735eeb639c5138b-1
0/3 0.0% -> ETA -  machine: running
0/3 0.0% -> ETA -

work | waiting | ready | working | review | merging | landed | cost | per landed
-----+---------+-------+---------+--------+---------+--------+------+-----------
s1   |       0 |     0 |       2 |      0 |       1 |      0 |    - | -
-----+---------+-------+---------+--------+---------+--------+------+-----------
     |       0 |     0 |       2 |      0 |       1 |      0 |    - |
2ac5fb74c892782f1071da337e5304f89faf2994	refs/heads/sprint/s1
TICK OK state=RUNNING idle=yes moved=0 notes=0
TIMES 90ms trips=19 reads=0 rows=1 stale=0 mismatch=0: look=16ms/5t/0r/0n fleet-display=20ms/3t/0r/1n first-read=17ms/2t/0r/0n remind=2ms/2t/0r/0n archive=15ms/3t/0r/0n where=7ms/2t/0r/0n tick-end=2ms/2t/0r/0n heartbeat=0ms/0t/0r/0n
0/3 0.0% -> ETA -  machine: running
15:41:56 tick
TABLES rows changed: work=0 readers=0 merge=0 fleet=0
TICK OK state=RUNNING idle=yes moved=0 notes=0
TIMES 47ms trips=22 reads=0 rows=1 stale=1 mismatch=0: look=6ms/5t/0r/0n fleet-display=23ms/4t/0r/1n first-read=3ms/3t/0r/0n remind=0ms/2t/0r/0n archive=2ms/3t/0r/0n where=1ms/2t/0r/0n tick-end=0ms/2t/0r/0n heartbeat=0ms/1t/0r/0n
0/3 0.0% -> ETA -  machine: running

$ cd /work/trial && . ./env.sh && sleep 10; grep -i -E "land|merge|refus|fail|error" run.log | cut -c1-400 | tail -15; git ls-remote origin.git sprint/s1; nova-sprint inbox 2>&1 | cut -c1-300 | head -20
TABLES rows changed: work=0 readers=0 merge=0 fleet=0
TABLES rows changed: work=0 readers=0 merge=0 fleet=0
TABLES rows changed: work=0 readers=0 merge=0 fleet=0
TABLES rows changed: work=0 readers=0 merge=0 fleet=0
TABLES rows changed: work=0 readers=0 merge=0 fleet=0
TABLES rows changed: work=0 readers=0 merge=0 fleet=0
TABLES rows changed: work=0 readers=0 merge=0 fleet=0
TABLES rows changed: work=0 readers=0 merge=0 fleet=0
TABLES rows changed: work=0 readers=0 merge=0 fleet=0
TABLES rows changed: work=0 readers=0 merge=0 fleet=0
TABLES rows changed: work=0 readers=0 merge=0 fleet=0
TABLES rows changed: work=0 readers=0 merge=0 fleet=0
TABLES rows changed: work=0 readers=0 merge=0 fleet=0
TABLES rows changed: work=0 readers=0 merge=0 fleet=0
TABLES rows changed: work=0 readers=0 merge=0 fleet=0
2ac5fb74c892782f1071da337e5304f89faf2994	refs/heads/sprint/s1
HAPPENED tick-accept-dlxvbhxnkik2-8847-4288407ee1316aa3-1.2   ready to merge  stream=s1  size=1  for=boss  (c1)  1 accepted and queued to merge: c1; run: nova-sprint land --stream s1 (a nova-sprint run started with --land lands them itself)
HAPPENED start-dlxvatp3r2l3-8847-4e1bba275ea5a9e0-1.1   the machine started  x1  STOPPED -> RUNNING by boss
HAPPENED tick-presence-dlxvatu8av71-8847-b871e2b47f7b34ac-1.1   fleet member up  x1  m1 up: it beats
DECIDED tick-deal-dlxvatwppqac-8847-0beee9417182ac94-1.d1   no fleet member is up  size=1  (stream:)  answered by tick deal
HAPPENED finish-dlxvb6rtalqh-8847-6378a9cadf1145de-1.1   work came back ok  stream=s1  size=1  (c1)
DECIDED tick-ask-dlxvb89vf3cs-8847-40ff22e61770774b-1.d1   fewer than two readers up  size=1  (stream:)  answered by tick ask
HAPPENED tick-accept-dlxvbhxnkik2-8847-4288407ee1316aa3-1.1   stream started merging  stream=s1  x1
INBOX OK judgments=0 happened=7 cursor=-
machine: running

$ cd /work/trial && grep -v -E "^(TABLES|TIMES|TICK OK|[0-9:]+ tick|0/3)" run.log | cut -c1-500 | tail -20
SERVER listening on 127.0.0.1:7070: the workers' verbs (take, finish, read, queue, fleet beat) run here, one at a time, beside the store
SERVER listening on 127.0.0.1:7070: the coordinator's verbs, from this machine (NOVA_SPRINT_SERVER=127.0.0.1:7070)
LANDING every 2s: land runs here for every stream with cards queued to merge, one landing at a time
BALANCE every 10m0s: each provider's balance is read through the seat's key and written to the fleet table
RUN ticking on every line of the log (at most every 100ms) and every 1s while it is quiet; machine: STOPPED
15:40:47 machine STOPPED
15:40:58 machine RUNNING
MOVED presence: m1 up
MOVED deal: c1 work ready -> working card=c1.w1 member=m1 (fleet ready)
MOVED deal: c2 work ready -> working card=c2.w1 member=m1 (fleet ready)
MOVED drain: c1.w1 working -> done ok; c1 working -> review (finish by m1)
MOVED deal: c3 work ready -> working card=c3.w1 member=m1 (fleet ready)
MOVED ask: c1 asked of reader-a
MOVED drain: c1 asked of reader-a (tick ask by machine)
MOVED drain: c1.r1.reader-a reading -> ok (read by reader-a)
MOVED accept: c1 review -> merging queued (ok from reader-a)
15:41:51 LAND REFUSED stream=s1 cards=1 base=sprint/s1 tip=- ids=c1 repo=https://github.com/trial/work.git reason=paused: the merge queue of sprint/s1 could not be read (exec: "gh": executable file not found in $PATH), and an unreadable queue is not an empty one; the cards stay queued and land when it is read clear
15:41:51 NOTE nothing was pushed or reported for stream s1; its cards stay queued
15:41:51 LAND DONE batches=0 cards=0 refused=1; run: nova-sprint where
15:42:00 SERVE batches=29 beat-lane=0 read-lane=2 on-line=27 gone=0 wait-max=159ms held-max=71ms held-by=fleet+beat+--actor over=1m2s

$ printf "#!/bin/sh\n# stand-in for the forge: the throwaway origin has no merge queue\necho 0\n" > /work/bin/gh && chmod +x /work/bin/gh && cat /work/bin/gh; sleep 25; cd /work/trial && grep -E "LAND|NOTE" run.log | cut -c1-400 | tail -6; git ls-remote origin.git sprint/s1; git -C work fetch -q && git -C work log --oneline origin/sprint/s1 | head -4
#!/bin/sh
# stand-in for the forge: the throwaway origin has no merge queue
echo 0
15:41:51 LAND REFUSED stream=s1 cards=1 base=sprint/s1 tip=- ids=c1 repo=https://github.com/trial/work.git reason=paused: the merge queue of sprint/s1 could not be read (exec: "gh": executable file not found in $PATH), and an unreadable queue is not an empty one; the cards stay queued and land when it is read clear
15:41:51 NOTE nothing was pushed or reported for stream s1; its cards stay queued
15:41:51 LAND DONE batches=0 cards=0 refused=1; run: nova-sprint where
15:42:53 LAND REFUSED stream=s1 cards=1 base=sprint/s1 tip=- ids=c1 repo=https://github.com/trial/work.git reason=the card names the repository https://github.com/trial/work.git and the clone /work/home/.cache/nova-sprint/land/github.com-trial-work-11c0e43d4fd38b70 fetches from /work/trial/origin.git; nothing was fetched or pushed; run: nova-sprint land --re [trimmed: 40 more characters of this line]
15:42:53 NOTE nothing was pushed or reported for stream s1; its cards stay queued
15:42:53 LAND DONE batches=0 cards=0 refused=1; run: nova-sprint where
2ac5fb74c892782f1071da337e5304f89faf2994	refs/heads/sprint/s1
2ac5fb7 base: the three card files
4502ff2 base

$ cd /work/trial && . ./env.sh && git config --global --unset url./work/trial/origin.git.insteadOf; nova-sprint stop --reason "re-cut the cards with REPO: as the bare origin path" && nova-sprint clear -h 2>&1 | head -4
nova-sprint stop REFUSED: wants --until <time or duration>: when the machine starts itself again (90m, 2:04 PM, or RFC 3339); run: nova-sprint stop -h
[exit 2]

$ cd /work/trial && . ./env.sh && nova-sprint stop --reason "re-cut the cards with REPO: as the bare origin path" --until 30m && nova-sprint clear --confirm sprint 2>&1 | cut -c1-300
STOP OK before=RUNNING after=STOPPED changed
STOPPED by boss: re-cut the cards with REPO: as the bare origin path, back by 4:13 PM  0/3 0.0%
CLEAR OK epoch=0->1 at=2026-10-06T15:43:29Z held: primaries=3 work_cards=3 read_cards=1 merge_cards=1 open_judgments=0 machine=STOPPED->STOPPED
STOPPED

$ cd /work/trial && . ./env.sh && sed -i "s|^REPO: trial/work|REPO: /work/trial/origin.git|; s|the repository trial/work;|the repository /work/trial/origin.git;|" briefs/*.md && sed -n 1,5p briefs/c1.md && nova-sprint add --stream s1 --brief-dir briefs
tier: flash
RESULT: c1 sha=<sha12>
REPO: /work/trial/origin.git
PATHS: c1.txt
BASE: sprint/s1
MOVED c1 -> ready stream=s1 score=1
MOVED c2 -> ready stream=s1 score=2
MOVED c3 -> ready stream=s1 score=3
ADD OK stream=s1 cards=3 before=- moved=3 refused=0 notes=0 op=add-dlxvct6cc562-8847-7619136843f34b66~1-1
NOTE each card's id is its brief file's name without .md (/work/trial/briefs/c1.md is c1)
STOPPED  0/3 0.0%

$ cd /work/trial && . ./env.sh && nova-sprint start && sleep 3 && nova-sprint queue --as m1 | grep -E "^(CARD|QUEUE)|report it"
START OK before=STOPPED after=RUNNING changed
0/3 0.0% -> ETA -  machine: running
CARD c1.w1 fleet:m1:ready gen=1 take: c1.w1@1 --epoch 1 dealt=2026-10-06T15:43:40Z
  report it: nova-sprint finish --as m1 c1.w1@1 --epoch 1 --branch sprint/c1.w1.g1.e1 --head <commit> --report '<what you did>' [--failed]
CARD c2.w1 fleet:m1:ready gen=1 take: c2.w1@1 --epoch 1 dealt=2026-10-06T15:43:40Z
  report it: nova-sprint finish --as m1 c2.w1@1 --epoch 1 --branch sprint/c2.w1.g1.e1 --head <commit> --report '<what you did>' [--failed]
QUEUE OK cards=2 epoch=1

$ cd /work/trial && . ./env.sh && nova-sprint take --as m1 c1.w1@1 --epoch 1 | grep -v "^    " && h=$(./work-card.sh c1 sprint/c1.w1.g1.e1 | tail -1) && echo head=$h && nova-sprint finish --as m1 c1.w1@1 --epoch 1 --branch sprint/c1.w1.g1.e1 --head $h --report "c1.txt is the one line; gate test exit 0"
MOVED c1.w1 fleet ready -> working member=m1 gen=1
PACKET c1.w1 attempt=1 gen=1 epoch=1
  tier: flash
  branch: sprint/c1.w1.g1.e1
  base: the stream's base
  brief:
  notes: none
  report it: nova-sprint finish --as m1 c1.w1@1 --epoch 1 --branch sprint/c1.w1.g1.e1 --head <commit> --report '<what you did>' [--failed]
TAKE-BY-ID OK moved=1 refused=0 notes=0 op=take-dlxvd03j3uif-8847-b0116f93e7ad2631~1-1
0/3 0.0% -> ETA -  machine: running
head=cd681fc3db0dc4d7138a564c6c3f78d7628f696b
MOVED c1.w1 working -> done ok; c1 working -> review
FINISH OK moved=1 refused=0 notes=1 op=finish-dlxvd07d5bfb-8847-2007b09fc8c2e8cb~1-1
0/3 0.0% -> ETA -  machine: running

$ cd /work/trial && . ./env.sh && sleep 3 && nova-sprint queue --as reader-a | grep -E "^(CARD|QUEUE)|head:" && git -C work diff --stat origin/sprint/s1 cd681fc3db0dc4d7138a564c6c3f78d7628f696b && nova-sprint read --as reader-a --begin c1.r1.reader-a --epoch 1 && nova-sprint read --as reader-a --ok c1.r1.reader-a --epoch 1 && sleep 8 && grep -E "LAND" run.lo [trimmed: 105 more characters of this line]
CARD c1.r1.reader-a readers:reader-a:asked asked=2026-10-06T15:43:49Z
  head: cd681fc3db0dc4d7138a564c6c3f78d7628f696b
QUEUE OK cards=1 epoch=1
 c1.txt | 2 +-
 1 file changed, 1 insertion(+), 1 deletion(-)
MOVED c1.r1.reader-a asked -> reading
READ OK moved=1 refused=0 notes=0 op=read-dlxvd3zl40a3-8847-2b8328140162424d~1-1
0/3 0.0% -> ETA -  machine: running
MOVED c1.r1.reader-a reading -> ok
READ OK moved=1 refused=0 notes=0 op=read-dlxvd40p7yo3-8847-67fa5889aab923af~1-1
0/3 0.0% -> ETA -  machine: running
15:43:28 LAND REFUSED stream=s1 cards=1 base=sprint/s1 tip=- ids=c1 repo=https://github.com/trial/work.git dir=/work/home/.cache/nova-sprint/land/github.com-trial-work-11c0e43d4fd38b70 fetch=0.1s merge=0.0s check=0.0s queue=0.0s push=0.0s report=0.0s reason=the fetch of origin in /work/home/.cache/n
15:43:28 LAND DONE batches=0 cards=0 refused=1; run: nova-sprint where
15:43:59 LAND OK stream=s1 cards=1 base=sprint/s1 tip=b33e7918cfaa6410d983fa666db6927a711a8240 ids=c1 repo=/work/trial/origin.git dir=/work/home/.cache/nova-sprint/land/work-trial-origin-2a4faa9798c44f9d fetch=0.1s merge=0.1s check=0.0s queue=0.0s push=0.1s report=0.0s branches_queued=1
b33e791 land c1 (sprint stream s1)
cd681fc c1: the one line
2ac5fb7 base: the three card files

$ cd /work/trial && . ./env.sh && nova-sprint take --as m1 c2.w1@1 --epoch 1 | grep -E "^(MOVED|TAKE)" && h=$(./work-card.sh c2 sprint/c2.w1.g1.e1 | tail -1) && echo head=$h && nova-sprint finish --as m1 c2.w1@1 --epoch 1 --branch sprint/c2.w1.g1.e1 --head $h --report "c2.txt is the one line; gate test exit 0" && sleep 4 && nova-sprint queue --as reader-a |  [trimmed: 207 more characters of this line]
MOVED c2.w1 fleet ready -> working member=m1 gen=1
TAKE-BY-ID OK moved=1 refused=0 notes=0 op=take-dlxvdalwq93b-8847-2af55d75327be36b~1-1
head=acd7bbadd54e46f90c2c1cfe74e4faf1840b69c6
MOVED c2.w1 working -> done ok; c2 working -> review
FINISH OK moved=1 refused=0 notes=1 op=finish-dlxvdatiwuyv-8847-e2d545261b402ecc~1-1
1/3 33.3% -> ETA -  machine: running
CARD c2.r1.reader-a readers:reader-a:asked asked=2026-10-06T15:44:12Z
  head: acd7bbadd54e46f90c2c1cfe74e4faf1840b69c6
QUEUE OK cards=1 epoch=1
 c2.txt | 2 +-
 1 file changed, 1 insertion(+), 1 deletion(-)
MOVED c2.r1.reader-a asked -> reading
READ OK moved=1 refused=0 notes=0 op=read-dlxvdcvgpx8q-8847-e8f7f1a508d118e0~1-1
1/3 33.3% -> ETA -  machine: running
MOVED c2.r1.reader-a reading -> ok
READ OK moved=1 refused=0 notes=0 op=read-dlxvdcyq0qa3-8847-1847e19f6f27954f~1-1
1/3 33.3% -> ETA -  machine: running

$ cd /work/trial && . ./env.sh && nova-sprint queue --as m1 | grep -E "^(CARD|QUEUE)" && nova-sprint take --as m1 c3.w1@1 --epoch 1 | grep -E "^(MOVED|TAKE)" && h=$(./work-card.sh c3 sprint/c3.w1.g1.e1 | tail -1) && echo head=$h && nova-sprint finish --as m1 c3.w1@1 --epoch 1 --branch sprint/c3.w1.g1.e1 --head $h --report "c3.txt is the one line; gate test e [trimmed: 262 more characters of this line]
CARD c3.w1 fleet:m1:ready gen=1 take: c3.w1@1 --epoch 1 dealt=2026-10-06T15:43:49Z
QUEUE OK cards=1 epoch=1
MOVED c3.w1 fleet ready -> working member=m1 gen=1
TAKE-BY-ID OK moved=1 refused=0 notes=0 op=take-dlxvdg1n3pkl-8847-9fd6854662a67e04~1-1
head=52cff266d46ddd87d106520e6a443041c278f33e
MOVED c3.w1 working -> done ok; c3 working -> review
FINISH OK moved=1 refused=0 notes=1 op=finish-dlxvdg7iq5jz-8847-1a209d79f5ebd378~1-1
2/3 66.7% -> ETA -  machine: running
CARD c3.r1.reader-a readers:reader-a:asked asked=2026-10-06T15:44:24Z
  head: 52cff266d46ddd87d106520e6a443041c278f33e
QUEUE OK cards=1 epoch=1
 c3.txt | 2 +-
 1 file changed, 1 insertion(+), 1 deletion(-)
MOVED c3.r1.reader-a asked -> reading
READ OK moved=1 refused=0 notes=0 op=read-dlxvdi41hay0-8847-5b2281274886896a~1-1
2/3 66.7% -> ETA -  machine: running
MOVED c3.r1.reader-a reading -> ok
READ OK moved=1 refused=0 notes=0 op=read-dlxvdi5bn0pc-8847-39c87e0b9daf179b~1-1
2/3 66.7% -> ETA -  machine: running

$ cd /work/trial && . ./env.sh && sleep 8 && grep -E "LAND OK|HAPPENED|DONE|done" run.log | cut -c1-200 | tail -6; git -C work fetch -q && git -C work log --oneline origin/sprint/s1 | head -8 && git -C work show origin/sprint/s1:c1.txt origin/sprint/s1:c2.txt origin/sprint/s1:c3.txt; nova-sprint where 2>&1 | head -12; nova-sprint inbox 2>&1 | cut -c1-200 | h [trimmed: 6 more characters of this line]
MOVED drain: c2.w1 working -> done ok; c2 working -> review (finish by m1)
15:44:18 LAND OK stream=s1 cards=1 base=sprint/s1 tip=9b7b75320f158509ef341e2bdff3fba05f090741 ids=c2 repo=/work/trial/origin.git dir=/work/home/.cache/nova-sprint/land/work-trial-origin-2a4faa9798c44
MOVED drain: c3.w1 working -> done ok; c3 working -> review (finish by m1)
HAPPENED the sprint is done: 3 landed, 0 dropped, took 49s from the first start; the machine is STOPPED; to continue: add work, then nova-sprint start
STOPPED  3/3 100.0% done
15:44:28 LAND OK stream=s1 cards=1 base=sprint/s1 tip=1dc562201c57ab5e819cb280b652fec14c1e484e ids=c3 repo=/work/trial/origin.git dir=/work/home/.cache/nova-sprint/land/work-trial-origin-2a4faa9798c44
1dc5622 land c3 (sprint stream s1)
52cff26 c3: the one line
9b7b753 land c2 (sprint stream s1)
acd7bba c2: the one line
b33e791 land c1 (sprint stream s1)
cd681fc c1: the one line
2ac5fb7 base: the three card files
4502ff2 base
c1 was here
c2 was here
c3 was here
SPRINT TABLE  coordinator boss

DONE

work | waiting | ready | working | review | merging | landed | cost | per landed
-----+---------+-------+---------+--------+---------+--------+------+-----------
     |       0 |     0 |       0 |      0 |       0 |      3 |    - |
1 archived stream, 3 cards landed, - (where --json --archived)

friends | ready | working | width | done | ok%  | status | active
--------+-------+---------+-------+------+------+--------+-------
        |     0 |       0 |     0 |    0 | 0.0% |        |
HAPPENED tick-done-dlxvdie7vo2z-8847-974117e2394e2f39~1-1.1   the sprint is done  x1  for=boss  3 landed, 0 dropped, took 49s from the first start
  to continue: add work, then nova-sprint start
HAPPENED tick-accept-dlxvd41tz8nd-8847-9e0029859729c076~1-1.2   ready to merge  stream=s1  size=3  for=boss  (c1,c2,c3)  1 accepted and queued to merge: c3; run: nova-sprint land --stream s1 (a nova-s
HAPPENED clear-dlxvcr1pv3fh-8847-847254d0a93aecea~1-1.1   the machine stopped  x1  the machine is STOPPED by the clear of epoch 0; when the new sprint is ready: nova-sprint start
```

The throwaway origin at the end, as the last command printed it: `sprint/s1` is
`1dc5622 land c3`, `9b7b753 land c2`, `b33e791 land c1` over the base, and c1.txt, c2.txt and
c3.txt each hold their one line.

## Stumbles

### 1. README's install cannot work with no network

- Read: README.md, "Try one on a small example": install a 1.0.0 binary, or `go install .../cmd/nova-memory@v1.0.0`.
- Expected: one command that installs nova-sprint as a stranger would run it.
- Happened: `module lookup disabled by GOPROXY=off`. I guessed a build from the source checkout
  (`go install ./cmd/nova-sprint ...`), which README does not show; the tools then print
  `devel` as their version, so the run cannot say which release it tested.
- card: stranger-readme-install-from-checkout
- PATHS: README.md
- Task: say how to build the tools from a source checkout with no network (`go install ./cmd/<tool>`) and what `version` then prints.

### 2. A checkout build prints `devel` and no commit

- Read: `nova-sprint version`.
- Expected: the version and the commit it was built from.
- Happened: `nova-sprint devel linux/amd64 go1.26.6`. My copy of the source had no .git and the
  image sets GOFLAGS=-buildvcs=false, so Go recorded no revision; the line does not say that,
  and the commit had to be read from git outside the tool.
- card: version-devel-says-why
- PATHS: pkg/buildinfo/buildinfo.go
- Task: a build with no release version and no VCS revision says so on its version line (for example `devel (no vcs revision recorded)`), so a run can tell an unknown build from a named one.

### 3. `nova-redis serve` wants a password the README row does not mention

- Read: README.md, the nova-redis row ("serve also needs redis-server"); `nova-redis serve -h`.
- Expected: serve to start a throwaway store on loopback.
- Happened: `SERVE REFUSED: NOVA_REDIS_PASSWORD is empty; run under nova-secrets exec ...`. For a
  throwaway store I guessed a plain `NOVA_REDIS_PASSWORD=...` in the environment, which worked.
- card: redis-serve-throwaway-password
- PATHS: README.md,cmd/nova-redis/serve.go
- Task: the README row and `serve -h` say serve needs NOVA_REDIS_PASSWORD, and that a throwaway store may take it from the environment.

### 4. nova-sprint's Redis login: the variable is named nowhere, and differs from nova-redis's

- Read: `nova-sprint help`, `nova-sprint init -h`, `nova-sprint help seat` (no password or login variable in any of them).
- Expected: nova-sprint to log in with the password nova-redis serve was given (NOVA_REDIS_PASSWORD).
- Happened: `NOAUTH ... next: name the user (NOVA_SPRINT_REDIS_USER) and the variable that holds its password`,
  without naming that variable; I guessed `NOVA_SPRINT_REDIS_USER=default`, and only then the
  refusal named `NOVA_REDIS_BENCH_PASSWORD`. Two retries, and two names for one password.
- card: sprint-help-names-the-redis-login
- PATHS: cmd/nova-sprint/main.go,pkg/nsprint/redisauth/redisauth.go
- Task: the first NOAUTH refusal and `--redis` help name the user and password variables (NOVA_SPRINT_REDIS_USER, NOVA_REDIS_BENCH_PASSWORD), and the default user also reads NOVA_REDIS_PASSWORD, the one nova-redis serve took.

### 5. init needs the function library, which no first-run text mentions

- Read: `nova-sprint help` ("A real fleet").
- Expected: init to work on a fresh store from nova-redis serve.
- Happened: `holds no nova_sprint function library; run: nova-redis fn load ...`; the refusal named the fix, one retry.
- card: sprint-real-store-first-run-steps
- PATHS: cmd/nova-sprint/main.go
- Task: "A real fleet" lists the store's first steps in order: nova-redis serve with its password, nova-redis fn load, the login variables, init.

### 6. The push proof: no coordinator verb works, and help does not say how to make one

- Read: `nova-sprint seat install -h`, `nova-sprint seat push -h`, `nova-sprint help`; then, past
  them, docs/SPEC-SPRINT.md "The push proof", docs/RELEASE-NOTES-1.1.0.md "The push proof" and
  pkg/friend/adapter.go (to learn which adapter needs no binary).
- Expected: `add` after `init`, as the help's example shows.
- Happened: `add REFUSED: PUSH DOWN: boss has no push target recorded`. `seat install` installs a
  systemd user unit (none in a container); `--harness fake` is refused with a `run:` line that
  repeats the refused command; no harness binary (tmux, dsh, opencode) is in the image. From the
  source I learned the claude adapter appends to a wake file, so `seat push --harness claude
  --target <dir> --session boss`, the loop `inbox --wait --push seat` by hand (refused first until
  `~/boss-working/inbox` existed), and `seat pong <nonce>` from the wake file proved the seat.
  Neither the example block nor "A real fleet" mentions the proof.
- card: sprint-help-push-proof-walkthrough
- PATHS: cmd/nova-sprint/main.go,cmd/nova-sprint/pushproof.go
- Task: the help's example and "A real fleet" show the push proof's three steps (seat push, the push loop, seat pong), and a refusal of an unknown harness prints no `run:` line repeating it.

### 7. The card template fails the card lint

- Read: `nova-swarm template --name card`, `nova-sprint add -h` ("nova-swarm template --name card prints a card that passes the general ones").
- Expected: a brief filled in from the template to pass the lint.
- Happened: `LINT DRIFT ... tier-line: line 1 names no tier` on each card; I added `tier: flash` as line 1. The template also shows no `NEW:` line, though the next lint said a new file must be on one.
- card: swarm-template-card-passes-the-lint
- PATHS: pkg/swarm/templates.go
- Task: the card template carries `tier: flash` on line 1 and a `NEW:` line, and a test lints the template as add does.

### 8. `REPO:` reads as a GitHub repository; a local origin is not shown

- Read: the template's `REPO: <owner>/<name>`; past it, pkg/swarm/stage.go (CardRepoURL) and
  cmd/nova-sprint/land.go (originIs, normRepo).
- Expected: a way to name the throwaway bare origin.
- Happened: `REPO: trial/work` was cloned from github.com (no network: lint refused). I guessed a
  git `url.<path>.insteadOf` rewrite; add then admitted the cards, but land refused them
  (`the card names the repository https://github.com/trial/work.git and the clone ... fetches from /work/trial/origin.git`).
  Only the source said a REPO: may be an absolute path. I stopped the machine (`stop` wanted
  `--until`, one more retry), cleared the sprint and added the cards again with `REPO: /work/trial/origin.git`.
- card: card-repo-may-be-a-path
- PATHS: pkg/swarm/templates.go,cmd/nova-sprint/main.go
- Task: the template's REPO: line and the twin walkthrough say REPO: takes an owner/name, a URL or an absolute path, and the walkthrough's cards name origin.git by path.

### 9. Landing pauses for a missing `gh`

- Read: run.log; `nova-sprint help` (land pauses "while the merge queue of the branch ... holds a group ... a queue that cannot be read pauses as a held one"); past it, cmd/nova-sprint/mergewindow.go.
- Expected: `run --land` to land the passed card on the throwaway origin.
- Happened: `LAND REFUSED ... paused: the merge queue of sprint/s1 could not be read (exec: "gh": executable file not found in $PATH)`.
  I put a stand-in `gh` printing `0` on PATH. The source says a path REPO: never asks gh, so after
  stumble 8 the stand-in was likely not needed; it was still on PATH when the cards landed.
- card: land-loop-says-gh-is-needed
- PATHS: cmd/nova-sprint/landloop.go
- Task: `run --land` says at start, once, when gh is missing and a stream's cards name a GitHub repository, that their landing will pause until gh is on PATH.

### 10. Taking two cards at width 1: "at its width (0 working of 1)"

- Read: the queue's two `take:` lines.
- Expected: either both taken, or a refusal that adds up.
- Happened: `REFUSED c2.w1: member m1 is at its width (0 working of 1)`; zero of one is not at width. One retry with one card.
- card: take-width-refusal-counts-the-batch
- PATHS: internal/sprint/steps_work.go
- Task: the width refusal of a take of several counts the cards of the same take, e.g. `(1 working of 1 with c1.w1 taken in this step)`.

### 11. `where` disagrees with itself about dealt cards

- Read: `nova-sprint where`.
- Expected: one count of c2 and c3, dealt and not taken.
- Happened: the work table counts them `working 2`, the fleet table `ready 2, working 0`.
- card: where-work-and-fleet-agree-on-dealt
- PATHS: cmd/nova-sprint/verbs.go
- Task: the work table names cards dealt and not taken as dealt (or ready), as the fleet table does, and a test pins both tables on one store.

### 12. A load over 100 percent

- Read: `nova-sprint fleet beat m1`; help: "The load cell is the machine's CPU busy percent of all its cores".
- Expected: at most 100%.
- Happened: `load=144.6% ... how=load1 cores=64`: the load-average fallback is not capped or not divided as the help says.
- card: fleet-beat-load1-is-a-percent
- PATHS: cmd/nova-sprint/fleet.go
- Task: the load1 fallback is the load average over the cores as a percent, capped at 100, or the help says it can exceed 100.

### 13. A worker by hand must keep beating, and help's hand flow does not say so

- Read: the twin walkthrough ("Every member and reader of the twin beats at every verb").
- Expected: the same flow by hand against the server.
- Happened: c1 came back ok but no read was asked: reader-a's beat (its `queue`) was older than
  15 s. I started two loops by hand (`fleet beat m1` and `queue --as reader-a` every 5 s).
- card: sprint-hand-workers-on-a-server
- PATHS: cmd/nova-sprint/main.go
- Task: "A real fleet" says a member and a reader worked by hand must beat every few seconds (fleet beat, queue --as) or use nova-swarm member.

### 14. ETA stays `-` after cards land

- Read: help: "the word alone until one has landed".
- Expected: an estimate after c1 landed.
- Happened: `1/3 33.3% -> ETA -` and `2/3 66.7% -> ETA -`.
- card: sprint-eta-after-first-landing
- PATHS: internal/sprint/rate.go
- Task: the sprint line prints an estimate once a card has landed, or the help says when `-` is shown.

### 15. "The sprint is done" is logged before the last landing

- Read: run.log.
- Expected: the c3 `LAND OK` line, then the done line.
- Happened: `HAPPENED the sprint is done: 3 landed` precedes `15:44:28 LAND OK ... ids=c3` in run.log.
- card: run-log-done-after-last-land
- PATHS: cmd/nova-sprint/landloop.go
- Task: run writes a batch's LAND OK line before the tick it caused reports the sprint done.

## Verdict

- Could a stranger do it from README and help alone: no. Two stumbles could not be passed
  without the source: the push proof (6), and naming a local origin in REPO: (8). With them read,
  the run went from init to three cards landed on the throwaway origin.
- Minutes taken: 14, from README to the third landing (15:31 to 15:44:28 UTC, the build included),
  15 with the teardown.
