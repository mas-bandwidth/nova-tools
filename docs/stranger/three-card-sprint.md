# Cold stranger run: a three-card sprint

## Setup

- Bench: one Linux bench (x86_64). Its name is left out. podman 5.7.0. The default cgroup manager refused (`crun: sd-bus call: Access denied ... interactive authentication`), so the container was started with `--cgroup-manager=cgroupfs --events-backend=file` as well as the required `--rm --timeout 14400 --network none`.
- Container: `localhost/nova-functional:latest` (the functional image: Go 1.26.6, redis-server 8.10.2, git 2.43.0), one container named for this run, `--userns=keep-id:uid=10001,gid=10001`, the bench directory mounted at `/work`, `HOME=/work/home`. Every command below ran inside it. Redis, the sprint server, and git were its children. It was stopped at the end, which removed it, and the bench directory was removed.
- Source: a checkout of nova-tools at `ba3d867c0e3178af6f93d5cf8f8ad6b12c23f8a6`, copied to `/work/src` before the container started. The module cache was filled on the bench host, because `--network none` cannot fetch modules.
- Versions, as `<tool> version` printed them:
  - `nova-sprint v1.0.1-0.20261006133824-ba3d867c0e31 linux/amd64 go1.26.6`
  - `nova-swarm v1.0.1-0.20261006133824-ba3d867c0e31 linux/amd64 go1.26.6`
  - `nova-redis v1.0.1-0.20261006133824-ba3d867c0e31 linux/amd64 go1.26.6`
- Read before any verb: README.md. It links the usage guide, the command reference, the specs, and the coordinator runbook. Those were not opened. The verbs were then run from `nova-sprint help`, `nova-redis help`, `nova-swarm template --name card`, and `<verb> -h`.
- The three cards were worked by hand with the worker verbs. The fake harness named by the earlier stranger card is not named by README or help.

## Transcript

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
$ cd /work/src && nice -n 19 go build -o /work/bin/ ./cmd/nova-sprint ./cmd/nova-swarm ./cmd/nova-redis && nova-sprint version && nova-swarm version && nova-redis version
nova-sprint v1.0.1-0.20261006133824-ba3d867c0e31 linux/amd64 go1.26.6
nova-swarm v1.0.1-0.20261006133824-ba3d867c0e31 linux/amd64 go1.26.6
nova-redis v1.0.1-0.20261006133824-ba3d867c0e31 linux/amd64 go1.26.6
[exit 0]
```

```
$ nova-sprint help
```

[trimmed: 632 lines. The opening says a first run needs no Redis and names `NOVA_SPRINT_REDIS=mem:sprint.twin`. "A real fleet" says to run `nova-sprint run --listen <address>:<port> --land` beside the store, then set `NOVA_SPRINT_SERVER=127.0.0.1:<port>`. A later block, "trying it without a Redis", says a bare `origin.git` stands for the forge. Neither block mentions a store password or a seat push proof.]

```
[exit 0]
```

```
$ nova-redis serve --bind 127.0.0.1 --port 6379 --dir /work/redis --dry-run
SERVE OK bind=127.0.0.1 port=6379 auth=on persistence=aof eviction=none dir=/work/redis aclfile=/work/redis/users.acl users=0 dry_run=true created=0 launched=0
[exit 0]
```

```
$ nova-redis serve --bind 127.0.0.1 --port 6379 --dir /work/redis
SERVE REFUSED: NOVA_REDIS_PASSWORD is empty; run under `nova-secrets exec --only NOVA_REDIS_PASSWORD -- nova-redis serve ...` so auth comes from nova-secrets at run time, never an argument; run: nova-redis help
```

```
$ NOVA_REDIS_PASSWORD=throwaway nova-redis serve --bind 127.0.0.1 --port 6379 --dir /work/redis
SERVE START bind=127.0.0.1 port=6379 auth=on persistence=aof eviction=none dir=/work/redis aclfile=/work/redis/users.acl users=0 program=/usr/local/bin/redis-server
```

[trimmed: the server's own banner, 14 lines, ending `Ready to accept connections tcp`]

```
$ redis-cli -h 127.0.0.1 ping
NOAUTH Authentication required.
[exit 0]
```

```
$ NOVA_REDIS_PASSWORD=throwaway redis-cli -h 127.0.0.1 -a throwaway ping
Warning: Using a password with '-a' or '-u' option on the command line interface may not be safe.
PONG
[exit 0]
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
$ NOVA_SPRINT_REDIS=127.0.0.1:6379 NOVA_SPRINT_ACTOR=boss nova-sprint where
nova-sprint where REFUSED: redis at 127.0.0.1:6379 as the default user, no password: login refused: NOAUTH Authentication required.; next: name the user (NOVA_SPRINT_REDIS_USER) and the variable that holds its password; run: nova-sprint where -h
[exit 2]
```

```
$ NOVA_SPRINT_REDIS=127.0.0.1:6379 NOVA_SPRINT_ACTOR=boss NOVA_REDIS_PASSWORD=throwaway nova-sprint where
nova-sprint where REFUSED: redis at 127.0.0.1:6379 as the default user, no password: login refused: NOAUTH Authentication required.; next: name the user (NOVA_SPRINT_REDIS_USER) and the variable that holds its password; run: nova-sprint where -h
[exit 2]
```

```
$ NOVA_SPRINT_REDIS=127.0.0.1:6379 NOVA_SPRINT_ACTOR=boss NOVA_SPRINT_REDIS_USER=default NOVA_REDIS_PASSWORD=throwaway nova-sprint where
nova-sprint where REFUSED: redis at 127.0.0.1:6379 as user default (password from NOVA_REDIS_BENCH_PASSWORD): login refused: NOVA_REDIS_BENCH_PASSWORD is empty; next: export NOVA_REDIS_BENCH_PASSWORD, holding the password, in the environment of this process; run: nova-sprint where -h
[exit 2]
```

```
$ NOVA_SPRINT_REDIS=127.0.0.1:6379 NOVA_SPRINT_ACTOR=boss NOVA_SPRINT_REDIS_USER=default NOVA_REDIS_BENCH_PASSWORD=throwaway nova-sprint where
nova-sprint where: this store: no sprint here yet: init makes its tables; run: nova-sprint init --coordinator <name>
[exit 1]
```

The same four variables are set for every later store command. `NOVA_SPRINT_ACTOR=boss`.

```
$ nova-sprint init --readers reader-a --members m1:3 --coordinator boss --owner boss
INIT OK tables=work,readers,merge,fleet view=sprint readers=reader-a
MOVED m1 added, down until it beats width=3
FLEET-UP OK moved=1 refused=0 notes=0 op=fleet-release-dlxukpq7snbw-1987-56bc77f018aa847b-1
STOPPED
[exit 0]
```

```
$ mkdir -p /work/run && cd /work/run && git config --global user.name stranger && git config --global user.email stranger@example.invalid && git init -q --bare origin.git && git clone -q origin.git work && printf 'one\n\ntwo\n\nthree\n' > work/lines.txt && git -C work add lines.txt && git -C work commit -q -m base && git -C work push -q origin HEAD:sprint/s1 && git -C origin.git branch -a
warning: You appear to have cloned an empty repository.
  sprint/s1
[exit 0]
```

`lines.txt` on `sprint/s1` was `one`, a blank line, `two`, a blank line, `three`. Three briefs were written under `/work/run/briefs` (`line-one.md`, `line-two.md`, `line-three.md`), each from `nova-swarm template --name card` with `REPO: /work/run/origin.git`, `BASE: sprint/s1`, and `PATHS: lines.txt`, and a task to change one of those lines to `ONE`, `TWO`, or `THREE`.

```
$ cd /work/run && nova-sprint preflight --brief-dir briefs
PREFLIGHT line-one FAIL TEST: no test named (TEST: [-tags <tags>] <package> <TestName>)
PREFLIGHT line-three FAIL TEST: no test named (TEST: [-tags <tags>] <package> <TestName>)
PREFLIGHT line-two FAIL TEST: no test named (TEST: [-tags <tags>] <package> <TestName>)
NOTE preflight read no --repo-dir: TEST and BASE are unchecked
PREFLIGHT FAIL briefs=3 failed=3
[exit 1]
```

```
$ cd /work/run && nova-sprint add --stream s1 --brief-dir briefs
nova-sprint add REFUSED: PUSH DOWN: boss has no push target recorded: the push loop cannot reach the session; a coordinator that cannot be reached is not a coordinator, and nothing was changed; run: nova-sprint seat install --actor boss --harness <harness> --target <session dir>
[exit 2]
```

```
$ nova-sprint start
nova-sprint start REFUSED: PUSH DOWN: boss has no push target recorded: the push loop cannot reach the session; a coordinator that cannot be reached is not a coordinator, and nothing was changed; run: nova-sprint seat install --actor boss --harness <harness> --target <session dir>
[exit 2]
```

```
$ nova-sprint seat install --dry-run --harness grok --target /work/home/session
SEAT INSTALL DRY-RUN unit=/work/home/.config/systemd/user/nova-sprint-seat-push.service; nothing was written or loaded
```

[trimmed: the unit text, ExecStart `nova-sprint inbox --wait --push seat`]

```
[exit 0]
```

```
$ nova-sprint seat push --harness grok --target /work/home/session
PUSH DOWN name=boss harness=grok target=/work/home/session why="no push check has been delivered into boss's grok session yet: is inbox --wait --push seat running?" remedy="nova-sprint seat install --actor boss --harness grok --target /work/home/session"
[exit 1]
```

```
$ nova-sprint inbox --wait --push seat --timeout 8s
PUSH DOWN name=boss nonce=141c8595534a362e why="deferred: no grok session is open: open /work/home/.grok/active_sessions.json: no such file or directory; in that session: monitor `tail -n 0 -F <file>.wake`"
```

That loop was left running. A shell in `/work/home/session` was then left running `tail -n 0 -F /work/home/session/seat.wake`, and `/work/home/.grok/active_sessions.json` was written as one object, that shell's pid and cwd `/work/home/session`. The loop's next check arrived in the wake file. What was read, and how the file is shaped, is stumble 7.

```
$ nova-sprint seat pong f1f35dd0705d1b32 --actor boss
SEAT PONG OK name=boss nonce=f1f35dd0705d1b32 proven=2026-10-06T15:12:09Z
[exit 0]
```

```
$ nova-sprint seat push
PUSH OK name=boss harness=grok target=/work/home/session proven=2026-10-06T15:12:09Z
[exit 0]
```

```
$ nova-sprint add --stream s1 --brief-dir /work/run/briefs
MOVED line-one -> ready stream=s1 score=1
MOVED line-three -> ready stream=s1 score=2
MOVED line-two -> ready stream=s1 score=3
ADD OK stream=s1 cards=3 before=- moved=3 refused=0 notes=0 op=add-dlxuoub242u2-2172-27dc59fbe32b81b1-1
NOTE each card's id is its brief file's name without .md (briefs/line-one.md is line-one)
STOPPED  0/3 0.0%
[exit 0]
```

```
$ nova-sprint start
START OK before=STOPPED after=RUNNING changed
nothing is ticking: run: nova-sprint run
0/3 0.0% -> ETA -  machine: running
[exit 0]
```

```
$ nova-sprint run --listen 127.0.0.1:7400 --land
```

Started in the background. Its own stdout was not kept. Later `where` and `inbox` lines are what it did. Worker and coordinator commands below set `NOVA_SPRINT_SERVER=127.0.0.1:7400`.

```
$ nova-sprint fleet beat --as m1
nova-sprint fleet beat REFUSED: unknown flag --as; the flags of fleet beat are --actor, --cores, --epoch, --json, --load, --max, --op, --redis; run: nova-sprint help fleet beat
[exit 2]
```

```
$ nova-sprint fleet beat m1
FLEET-BEAT OK m1 at=2026-10-06T15:12:31Z load=196.2% last=196.2% how=load1 cores=64
[exit 0]
```

```
$ nova-sprint queue --as m1
CARD line-one.w1 fleet:m1:ready gen=1 take: line-one.w1@1 --epoch 0 dealt=2026-10-06T15:12:33Z
PACKET line-one.w1 attempt=1 gen=1 epoch=0
  branch: sprint/line-one.w1.g1.e0
```

[trimmed: the same shape for line-three and line-two, and each packet's brief, which is the file that was added]

```
QUEUE OK cards=3 epoch=0
[exit 0]
```

```
$ nova-sprint take --as m1 --max 3 --epoch 0
MOVED line-one.w1 fleet ready -> working member=m1 gen=1
MOVED line-three.w1 fleet ready -> working member=m1 gen=1
MOVED line-two.w1 fleet ready -> working member=m1 gen=1
TAKE OK moved=3 refused=0 notes=0 op=take-dlxup2ob4fzs-2188-d976a898d04b2a6f-1
0/3 0.0% -> ETA -  machine: running
[exit 0]
```

Each card's packet named its branch and the finish line. In `/work/run/work`, for line-one, line-three, then line-two:

```
$ git checkout -B sprint/line-one.w1.g1.e0 origin/sprint/s1 && sed -i 's/^one$/ONE/' lines.txt && git commit -am 'line-one: one -> ONE' && git push origin HEAD:sprint/line-one.w1.g1.e0
[exit 0]
```

```
$ nova-sprint finish --as m1 line-one.w1@1 --epoch 0 --branch sprint/line-one.w1.g1.e0 --head 29ae5064913c5620b0826c14a4cf820bddcccad2 --report 'changed one to ONE in lines.txt'
MOVED line-one.w1 working -> done ok; line-one working -> review
FINISH OK moved=1 refused=0 notes=1 op=finish-dlxupb16z8if-2188-19d92e72ed507b22-1
[exit 0]
```

```
$ nova-sprint finish --as m1 line-three.w1@1 --epoch 0 --branch sprint/line-three.w1.g1.e0 --head e88c231e7c682f04be396ed8ae2b826aa1f7f0b4 --report 'changed three to THREE in lines.txt'
MOVED line-three.w1 working -> done ok; line-three working -> review
FINISH OK moved=1 refused=0 notes=1 op=finish-dlxupb6t0tj5-2188-ec21c2f2fa8458bf-1
[exit 0]
```

```
$ nova-sprint finish --as m1 line-two.w1@1 --epoch 0 --branch sprint/line-two.w1.g1.e0 --head 70973739f62d3643585e57635b0110d49f1057ac --report 'changed two to TWO in lines.txt'
MOVED line-two.w1 working -> done ok; line-two working -> review
FINISH OK moved=1 refused=0 notes=1 op=finish-dlxupba2i3fs-2188-5a9f5efef50e6ecd-1
[exit 0]
```

```
$ nova-sprint queue --as reader-a
QUEUE OK cards=0 epoch=0
[exit 0]
```

A few seconds later the inbox held one judgment and the reader queue held the three reads.

```
$ nova-sprint inbox
JUDGMENT tick-ask-dlxuphzebuf9-2188-f425b5b1a887691f-1.1 ! fewer than two readers up  alias=j3  size=0  waited=15s  due=15:23:07
  fewer than two readers up: reader-a away
  reader up:
    nova-sprint reader up '<reader>'
  reader add:
    nova-sprint reader add '<reader>'
  wait:
    nova-sprint wait tick-ask-dlxuphzebuf9-2188-f425b5b1a887691f-1.1 --for 30m
```

[trimmed: six HAPPENED and DECIDED lines, including "work came back ok" for the three cards and "fewer than two readers up" answered by tick ask]

```
INBOX OK judgments=1 happened=6 cursor=-
[exit 0]
```

```
$ nova-sprint queue --as reader-a
CARD line-one.r1.reader-a readers:reader-a:asked asked=2026-10-06T15:12:52Z
PACKET line-one.r1.reader-a attempt=1 gen=0 epoch=0
  head: 29ae5064913c5620b0826c14a4cf820bddcccad2
  branch: sprint/line-one.w1.g1.e0
```

[trimmed: the same for line-three (`e88c231e7c682f04be396ed8ae2b826aa1f7f0b4`) and line-two (`70973739f62d3643585e57635b0110d49f1057ac`)]

```
QUEUE OK cards=3 epoch=0
[exit 0]
```

```
$ git diff --stat origin/sprint/s1 origin/sprint/line-one.w1.g1.e0
 lines.txt | 2 +-
 1 file changed, 1 insertion(+), 1 deletion(-)
```

The three diffs were `-one`/`+ONE`, `-two`/`+TWO`, `-three`/`+THREE`.

```
$ nova-sprint read --as reader-a --begin line-one.r1.reader-a line-three.r1.reader-a line-two.r1.reader-a --epoch 0
MOVED line-one.r1.reader-a asked -> reading
MOVED line-three.r1.reader-a asked -> reading
MOVED line-two.r1.reader-a asked -> reading
READ OK moved=3 refused=0 notes=0 op=read-dlxuq3uwhaw2-2188-419c20964c65d6ab-1
[exit 0]
```

```
$ nova-sprint read --as reader-a --ok line-one.r1.reader-a line-three.r1.reader-a line-two.r1.reader-a --epoch 0
MOVED line-one.r1.reader-a reading -> ok
MOVED line-three.r1.reader-a reading -> ok
MOVED line-two.r1.reader-a reading -> ok
READ OK moved=3 refused=0 notes=0 op=read-dlxuq3vyvay8-2188-6c68974f36ad9d2f-1
[exit 0]
```

About eight seconds later:

```
$ nova-sprint where
SPRINT TABLE  coordinator boss

DONE

work | waiting | ready | working | review | merging | landed | cost | per landed
-----+---------+-------+---------+--------+---------+--------+------+-----------
     |       0 |     0 |       0 |      0 |       0 |      3 |    - |
1 archived stream, 3 cards landed, - (where --json --archived)
[exit 0]
```

```
$ nova-sprint inbox
HAPPENED tick-done-dlxuq4ofxksv-2188-2fc9509e414240af-1.1   the sprint is done  x1  for=boss  3 landed, 0 dropped, took 1m33s from the first start
HAPPENED tick-accept-dlxuq3xfcfvp-2188-06bb9fa58ff9f9b4-1.2   ready to merge  stream=s1  size=3  for=boss  (line-one,line-three,line-two)  3 accepted and queued to merge: line-one line-three line-two; run: nova-sprint land --stream s1 (a nova-sprint run started with --land lands them itself)
HAPPENED merge-dlxuq4nnzl7d-2188-b909a00722f2c264-1.1   batch landed  stream=s1  size=3  (line-one,line-three,line-two)  ci green
HAPPENED stream-archive-dlxuq4oobj97-2188-3b0f812d03657f43-1.1   streams archived  x1  every card of s1 landed: archived, off the work and merge tables, its cards and costs kept
INBOX OK judgments=0 happened=12 cursor=-
machine: DONE
[exit 0]
```

```
$ git -C /work/run/origin.git log --format='%H %s' sprint/s1
7a2bf7d5869beb6061f15f2a4fc86ac6ed48651c land line-two (sprint stream s1)
835ba54b9dcbfaabf4dbceafd21f298cc11ff7c1 land line-three (sprint stream s1)
0ce0bcfaf0ed13bc7db60383d2905dedadde47a9 land line-one (sprint stream s1)
70973739f62d3643585e57635b0110d49f1057ac line-two: two -> TWO
e88c231e7c682f04be396ed8ae2b826aa1f7f0b4 line-three: three -> THREE
29ae5064913c5620b0826c14a4cf820bddcccad2 line-one: one -> ONE
0db9a4d6dd087df71f80c743328fe896f84b279e base
[exit 0]
```

```
$ git -C /work/run/origin.git show sprint/s1:lines.txt
ONE

TWO

THREE
[exit 0]
```

## Stumbles

### 1. The README install line cannot run offline, and names an older release

Read README.md. Expected `go install github.com/mas-bandwidth/nova-tools/cmd/nova-sprint@v1.0.0` to install the sprint. The container has no network (`GOPROXY=off`), so the lookup was refused, and the checkout's own `version` is `v1.0.1-0.20261006133824-ba3d867c0e31`, not v1.0.0. Built from the checkout instead.

card: readme-install-from-checkout

### 2. `nova-redis serve` refuses a throwaway store until a password variable is set, and points at nova-secrets

Read `nova-redis serve -h` and `nova-redis help`. Expected a loopback store in `--dir` to start. It refused with `NOVA_REDIS_PASSWORD is empty` and the remedy `nova-secrets exec --only NOVA_REDIS_PASSWORD`. A throwaway store has no secrets store. Exporting `NOVA_REDIS_PASSWORD=throwaway` started it.

card: redis-serve-says-password-required

### 3. nova-sprint does not use nova-redis's password variable, and `where -h` does not name the one it uses

Read `nova-sprint where -h`. It names `--redis` and `NOVA_SPRINT_REDIS` only. `NOVA_REDIS_PASSWORD`, which nova-redis had just required, was ignored. The refusal then named `NOVA_SPRINT_REDIS_USER`, and the next refusal named `NOVA_REDIS_BENCH_PASSWORD`. Those two, plus the address and `NOVA_SPRINT_ACTOR`, are what `where` accepted.

card: sprint-help-store-login

### 4. The functions nova-sprint calls are a separate `nova-redis fn load`, which sprint help never says

Read `nova-redis help`, which says `fn load` installs the functions nova-sprint calls. `nova-sprint help` never says to run it. The first load, without the password, refused and named `NOVA_REDIS_PASSWORD`. The second printed `LOADED nova_sprint`.

card: sprint-help-fn-load-step

### 5. `redis-cli ping` printed NOAUTH and exited 0

Expected a refused login to be a non-zero exit. The line was `NOAUTH Authentication required.` and the exit was 0, so a check of the exit alone says the store answered.

card: redis-cli-noauth-exits-zero

### 6. `nova-sprint help` is 632 lines, and the way to run a fleet is in the middle

Expected the first screen to say how a real store, a member, and a stream go together. The first screen is the twin file. "A real fleet" and the bare-origin walkthrough are hundreds of lines later, and neither mentions the push proof that actually blocked `add`.

card: sprint-help-real-fleet-group

### 7. Every coordinator verb is refused until a live session answers a push check, and help never says how

Read `nova-sprint seat install -h`, `nova-sprint seat push -h`, and `nova-sprint inbox -h`. Expected `seat install` or `seat push --harness --target` to be enough, since that is the remedy `add` printed. `seat push` stayed down until `inbox --wait --push seat` delivered a check into a session, and the loop deferred: no `active_sessions.json`, and the session must run `tail -n 0 -F <file>.wake`. Help does not say what that file contains, or that the answer is `nova-sprint seat pong <nonce>`. Read `internal/friend/adapter_grok.go` (the session file, the pid, and the tail), `cmd/nova-sprint/pushproof.go` (`seat pong`), and `internal/sprint/pushproof.go` (`PushCheckText`, and that a failed delivery is retried after a minute). Forged a session inside the container, ponged nonce `f1f35dd0705d1b32`, and only then did `add` run.

card: sprint-help-seat-push-proof

### 8. The card template's REPO line is a forge name

Read `nova-swarm template --name card`. Expected `REPO: <owner>/<name>` to be what `add` wants. That is a forge repository, and this run must not push to one. The only place help says a bare `origin.git` stands for the forge is the twin walkthrough. The briefs used `REPO: /work/run/origin.git`. `add` admitted them, and `land` pushed there.

card: card-template-repo-is-a-forge-name

### 9. `preflight` fails a one-line text card for want of a TEST line, and `add` admits it

Read `nova-sprint preflight -h` by running it. Expected a FAIL to mean the brief would not be admitted. It failed all three for `TEST: no test named`. `add` then admitted the same files with `refused=0`.

card: preflight-requires-a-test-line

### 10. `fleet beat` takes the member as a word and refuses `--as`

Read `nova-sprint help`, whose worker verbs use `--as`. Expected `fleet beat --as m1` to match `queue`, `take`, and `finish`. It exited 2: `unknown flag --as`. `fleet beat m1` printed `FLEET-BEAT OK`.

card: fleet-beat-accepts-as

### 11. The judgment said fewer than two readers were up, and one reader's ok landed the cards

Read the inbox. Expected one reader, as `init --readers reader-a` was given, to be the sprint's shape. The judgment said `fewer than two readers up: reader-a away` and offered `reader up` and `reader add`. The same tick still asked reader-a, and `read --ok` from that one reader was enough: the server's `--land` merged all three and the machine went DONE. The judgment was never answered.

card: ask-judgment-names-readers

### 12. Three one-line edits of one file are not what the help's three-card example is

Read the twin example, `nova-sprint add --stream s1 --count 3 --brief-file brief.txt`, one brief three times. This run needed three different edits of `lines.txt`. Neighboring line edits do not merge, so the base was written with a blank line between them. Help never says that. The landed file is `ONE`, a blank line, `TWO`, a blank line, `THREE`.

card: sprint-help-three-card-example

## Verdict

could a stranger do it: no

minutes taken: 16

The three cards did land on the throwaway origin, inside the one container, with Redis and the server as its children. A stranger who has only the README and the help does not get there: `add` and `start` stay refused until a harness session answers a push check, and nothing in the help says what that session is or that the answer is `seat pong`. The rest of the path (the password variable, `fn load`, `fleet beat` without `--as`) is printed by refusals, one at a time, after the help has already been read.
