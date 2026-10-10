# Stranger run: a nova-friend with a fake harness

A cold run by a bud (Claude Sonnet 5.5, in Claude Code) who knew
nova-tools only from `README.md`, the page it links for installing
(`docs/USAGE.md#installing`) and the tools' own help. The goal: run a
`nova-friend` for a made-up friend whose harness is a fake written in Go,
deliver one job to it over `nova-bus` on a throwaway Redis, and see the report
come back. Where the run could not go on from those pages alone, that is a
stumble below, with what was read to get past it.

The transcript below is the record of that run as an earlier attempt of this
card made it; a later attempt carried it forward and re-ran none of its
commands.

## Setup

- Bench: a Linux bench (x86_64, Go 1.26.6), one throwaway container
  (`podman run --rm --timeout 14400 --network none`) from the functional image
  `localhost/nova-functional:latest` (Ubuntu 24.04, Redis 8.10.2), with a
  scratch directory mounted as `/work`. The Redis, both daemons and the fake all
  ran inside it; the container was stopped and the scratch directory removed at
  the end.
- Source: the checkout of `sprint/mechanical-2026-10-02` (a stranger's
  `go install ...@v1.0.0` could not give `nova-friend`, see the first stumble),
  built inside the container with `go install ./cmd/<tool>`.
- Versions, as `<tool> version` printed them: `nova-friend devel linux/amd64 go1.26.6`,
  `nova-bus devel linux/amd64 go1.26.6`. `nova-redis`, `nova-config` were built
  the same way.
- Read: `README.md`; `docs/USAGE.md` section Installing (linked from the
  README's first paragraph after the table); `help` and `<verb> -h` of
  nova-friend, nova-bus, nova-redis, nova-config. Beyond that, only to get past
  stumble 5, one grep of the Go source for the Redis login variable names.
- Two daemons, ada (the sender) and bob (the friend), each `nova-friend run`
  with harness `opencode` and its own directory; `opencode` on `PATH` is the
  fake, `fake3/main.go` below.

## Transcript

Every command is run inside the container; `...` marks a trim. The Redis
password and the two friend passwords are throwaways that died with the
container.

```text
$ go install github.com/mas-bandwidth/nova-tools/cmd/nova-friend@v1.0.0; echo exit=$?
go: github.com/mas-bandwidth/nova-tools/cmd/nova-friend@v1.0.0: module github.com/mas-bandwidth/nova-tools@v1.0.0 found, but does not contain package github.com/mas-bandwidth/nova-tools/cmd/nova-friend
exit=1
$ go install ./cmd/nova-friend ./cmd/nova-bus; echo exit=$?
exit=0
$ nova-friend version; nova-bus version
nova-friend devel linux/amd64 go1.26.6
nova-bus devel linux/amd64 go1.26.6
$ nova-friend help
nova-friend: what a friend runs to be part of the team: the wake loop, the beat, and the proof of life, as one daemon
...
$ nova-friend run --as ada --harness fake --dir /work/ada --dry-run; echo exit=$?
RUN REFUSED: --harness "fake" is no harness; it wants one of opencode, codex, claude, antigravity, dsh, gemini, grok, copilot, cursor, amp, goose, kiro, cline, aider, roo, windsurf, zed, warp; run: nova-friend help
exit=2
$ nova-friend run --as ada --harness opencode --dir /work/ada --dry-run; echo exit=$?
RUN FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written
exit=1
$ nova-bus help
nova-bus: messages between AIs over Redis streams: sent once, delivered until acked
...
first run: a Redis naming ada and bob at --redis (else NOVA_BUS_REDIS); loopback or tailnet only.
...
$ nova-bus names --redis 127.0.0.1:6379; echo exit=$?
NAMES REFUSED: redis at 127.0.0.1:6379 as the default user, no password: unreachable: dial tcp 127.0.0.1:6379: connect: connection refused; next: start the store or correct the address, which was given to this tool; run: nova-bus help
exit=2
$ go install ./cmd/nova-redis; nova-redis serve --bind 127.0.0.1 --port 6379 --dir /work/redis --dry-run
SERVE OK bind=127.0.0.1 port=6379 auth=on persistence=aof eviction=none dir=/work/redis aclfile=/work/redis/users.acl users=0 dry_run=true created=0 launched=0
$ NOVA_REDIS_PASSWORD=trial-pw nova-redis serve --bind 127.0.0.1 --port 6379 --dir /work/redis > /work/redis.log 2>&1 &
$ cat /work/redis.log
SERVE START bind=127.0.0.1 port=6379 auth=on persistence=aof eviction=none dir=/work/redis aclfile=/work/redis/users.acl users=0 program=/usr/local/bin/redis-server
...
5319:M 06 Oct 2026 06:10:18.826 * Ready to accept connections tcp
$ nova-bus names --redis 127.0.0.1:6379; echo exit=$?
NAMES REFUSED: redis at 127.0.0.1:6379 as the default user, no password: login refused: NOAUTH Authentication required.; next: name the user (NOVA_SPRINT_REDIS_USER) and the variable that holds its password; run: nova-bus help
exit=2
$ NOVA_REDIS_PASSWORD=trial-pw nova-bus names --redis 127.0.0.1:6379; echo exit=$?
NAMES REFUSED: redis at 127.0.0.1:6379 as the default user, no password: login refused: NOAUTH Authentication required.; next: name the user (NOVA_SPRINT_REDIS_USER) and the variable that holds its password; run: nova-bus help
exit=2
$ NOVA_SPRINT_REDIS_PASSWORD_ENV=PW PW=trial-pw nova-bus names --redis 127.0.0.1:6379; echo exit=$?
NAMES REFUSED: redis at 127.0.0.1:6379 as the default user, no password: login refused: NOAUTH Authentication required.; next: name the user (NOVA_SPRINT_REDIS_USER) and the variable that holds its password; run: nova-bus help
exit=2
$ export NOVA_SPRINT_REDIS_USER=default NOVA_SPRINT_REDIS_PASSWORD_ENV=PW PW=trial-pw; nova-bus names --redis 127.0.0.1:6379; echo exit=$?
NAMES OK count=0 proven=0
exit=0
$ nova-bus send --as ada --to bob --subject hello --body "are you there?" --redis 127.0.0.1:6379; echo exit=$?
SEND REFUSED: --as ada is not the login user default: this connection acts as default; drop --as, or log in as ada (NOVA_SPRINT_REDIS_USER=ada with its password); run: nova-bus help
exit=2
$ go install ./cmd/nova-config; nova-config help
nova-config: a fleet's machines and AI friends as rows in PostgreSQL, applied into Redis
...
$ nova-config migrate --file try.json && nova-config friend add bob --slots 1 --tiers flash --as ada --file try.json; nova-config friend add ada --slots 1 --tiers flash --as ada --file try.json; nova-config apply --file try.json --redis 127.0.0.1:6379
CONFIG MIGRATE file=try.json from=0 to=34 applied=34
CONFIG ADD kind=friend name=bob rev=1
CONFIG ADD kind=friend name=ada rev=2
nova-config apply REFUSED: --as is required: the name the write is recorded under (or NOVA_FRIEND); run: nova-config apply -h
exit=2
$ nova-config apply --file try.json --redis 127.0.0.1:6379 --as ada; echo exit=$?
nova-config apply REFUSED: fleet: endpoints are unset: redis_port, pg_dsn; run: nova-config fleet set --redis_port <port> --pg_dsn <dsn> --as <actor>, then nova-config apply --kind fleet --as <actor>
exit=1
$ nova-config apply --file try.json --redis 127.0.0.1:6379 --as ada --kind friend; echo exit=$?
APPLY ADD kind=friend name=ada
nova-config apply REFUSED: friend ada has no beat naming a machine and the fleet names no coordinator machine to charge her slots to; run: nova-config fleet set --coordinator <machine>, then apply; run: nova-config apply --dry-run
exit=1
$ nova-config machine add m1 --user ubuntu --seat s1 --slots 4 --width 1 --as ada --file try.json; nova-config fleet set --store m1 --coordinator m1 --redis_port 6379 --pg_dsn postgres://nova@127.0.0.1:5432/nova --bus 127.0.0.1:6379 --as ada --file try.json; nova-config apply --file try.json --redis 127.0.0.1:6379 --as ada; echo exit=$?; nova-bus names --redis 127.0.0.1:6379
CONFIG ADD kind=machine name=m1 rev=3
CONFIG SET kind=fleet name=fleet rev=4 changed=bus,coordinator,pg_dsn,redis_port,store
...
CONFIG APPLY kind=tier add=3 set=0 remove=0 rev=0 ms=1
exit=0
NAMES OK count=3 proven=0
NAMES NAME name=ada push=none age=never harness=-
NAMES NAME name=bob push=none age=never harness=-
NAMES NAME name=m1 push=none age=never harness=-
$ (wrote fake1/main.go, a Go program that records its argv, cwd, stdin and NOVA* environment to /work/fake.log; go build -o /work/bin/opencode .)
$ nova-friend run --as bob --harness opencode --dir /work/bob --redis 127.0.0.1:6379 --session s1 > /work/bob-daemon.log 2>&1 &
$ cat /work/fake.log
ARGV ["opencode" "run" "--session" "s1" "--dir" "/work/bob" "SESSION CHECK rrd6yb\nThe daemon has had no bus message from this session for a while and asks whether the session is alive; only an answer from inside this session counts, and none within 5m0s puts you down. Answer now, before anything else, with one command, then end this turn: /work/bin/nova-friend pong --as bob --nonce rrd6yb --state-dir /work/bob/.nova-friend --redis 127.0.0.1:6379 --to bob\n"]
CWD /work/bob
STDIN ""
$ nova-friend status --as bob --dir /work/bob
STATUS NONE: no daemon has run as bob (no status file in /work/home/.nova-friend/bob); run: nova-friend install --as bob --harness <h> --dir /work/bob
exit=1
$ (wrote fake2/main.go, then fake3/main.go: the fake below; go build -o /work/bin/opencode .)
$ redis-cli -a $PW ACL SETUSER ada on ">adapw" "~*" "&*" "+@all"; redis-cli -a $PW ACL SETUSER bob on ">bobpw" "~*" "&*" "+@all"
OK
OK
$ NOVA_SPRINT_REDIS_USER=ada PW=adapw nova-friend run --as ada --harness opencode --dir /work/ada --redis 127.0.0.1:6379 --session s1 > /work/ada-daemon.log 2>&1 &
$ NOVA_SPRINT_REDIS_USER=bob PW=bobpw nova-friend run --as bob --harness opencode --dir /work/bob --redis 127.0.0.1:6379 --session s1 > /work/bob-daemon.log 2>&1 &
$ cat /work/bob-daemon.log
PONG OK nonce=au6ptf to=bob id=01M47XG4SWDYCTTBCWFTVA6JMK at=2026-10-06T06:13:01Z
RUN 2026-10-06T06:13:01Z push proof: CHECK OK harness=opencode took=20.684181ms
RUN 2026-10-06T06:13:01Z inbox: the server did not say which cards are on her row: the sprint server at 127.0.0.1:6390 did not answer: Post "http://127.0.0.1:6390/verbs": dial tcp 127.0.0.1:6390: connect: connection refused; nothing written or retired until it does
RUN 2026-10-06T06:13:01Z harness check: cannot tell: opencode runs no standing process (each turn starts /work/bin/opencode); the session check alone
RUN 2026-10-06T06:13:01Z presence: a session check is owed; it waits for the turn under way
RUN 2026-10-06T06:13:01Z push proof: down: no session answer yet; nova-bus refuses bob as deaf
RUN 2026-10-06T06:13:01Z subject="pong" messages=1 took=12ms exit=0 acked=true
RUN 2026-10-06T06:13:02Z presence: session check can9yc into the session
PONG REFUSED: --to is required: no ping has named a seat yet (no status file in /work/bob/.nova-friend); it wants the coordinator's name; run: nova-friend help
$ nova-friend ping --as ada --to bob --nonce n1 --redis 127.0.0.1:6379; nova-friend wait-pong --from bob --nonce n1 --timeout 5s --redis 127.0.0.1:6379
PING OK nonce=n1 id=01M47XGMTGVSF6AYNBQH8F0N4F to=bob at=2026-10-06T06:13:17Z
PING NOTE wait for it: nova-friend wait-pong --from bob --nonce n1
WAIT-PONG NONE daemon=true: no pong n1 from bob within 5s
WAIT-PONG NOTE the daemon answered and the session did not: deaf
$ nova-bus names --redis 127.0.0.1:6379
NAMES OK count=3 proven=1
NAMES NAME name=ada push=proven age=9s harness=opencode
NAMES NAME name=bob push=down age=25s harness=opencode
NAMES NAME name=m1 push=none age=never harness=-
$ (fake3 now retries a refused pong line with --to <its own name>; rebuilt; both daemons restarted on clean directories)
$ nova-bus names --redis 127.0.0.1:6379
NAMES OK count=3 proven=2
NAMES NAME name=ada push=proven age=11s harness=opencode
NAMES NAME name=bob push=proven age=11s harness=opencode
NAMES NAME name=m1 push=none age=never harness=-
$ NOVA_SPRINT_REDIS_USER=ada PW=adapw nova-bus send --to bob --subject job --body "write me a report" --redis 127.0.0.1:6379
SEND OK id=01M47XHS6J503P00ZY2D0A13YH to=bob cc=- at=2026-10-06T06:13:55Z bytes=17 sha256=b8aef518234fe0f0168ca16a10dbc1ad9238f30140ad732f7563cb5032ca48ff
$ tail -3 /work/bob-daemon.log
RUN 2026-10-06T06:13:39Z subject="pong" messages=1 took=25ms exit=0 acked=true
SEND OK id=01M47XHS6YJ8W4R5A8FPRNMGBG to=ada cc=- kind=report at=2026-10-06T06:13:55Z bytes=90 sha256=2ba66bd37bc980b9dcd7a577365e16683057cad01b20594a55664b80851fd308
RUN 2026-10-06T06:13:55Z subject="job" messages=1 took=987ms exit=0 acked=true
$ tail -3 /work/ada-daemon.log
RUN 2026-10-06T06:13:39Z push proof: up: the session answered asvmqt through opencode's deliver adapter; nova-bus hears ada
RUN 2026-10-06T06:13:39Z subject="pong" messages=1 took=14ms exit=0 acked=true
RUN 2026-10-06T06:13:55Z subject="report" messages=1 took=74ms exit=0 acked=true
$ cat /work/bob/outbox/01M47XHS6J503P00ZY2D0A13YH/REPORT.md
Verdict: LAND

fake harness did job 01M47XHS6J503P00ZY2D0A13YH for ada: write me a report
$ nova-friend check bob --state-dir /work/bob/.nova-friend
CHECK DAEMON friend=bob agent=none pid=- status=none connection=- challenge=- pong_age=- presence=down seen_age=-
...
CHECK DAEMON friend=--state-dir agent=none pid=- status=none connection=- challenge=- pong_age=- presence=down seen_age=-
...
CHECK DAEMON friend=/work/bob/.nova-friend agent=none pid=- status=none connection=- challenge=- pong_age=- presence=down seen_age=-
...
CHECK OK friends=3 ok=0 broken=0 deaf=0 silent=0 down=3 untrue=0
exit=1
$ nova-friend status --as bob --dir /work/bob | head -1
STATUS OK daemon=up harness=opencode status_age=2s connection=connected seat=- last_ping=2026-10-06T06:13:38Z ping_age=29s challenge=quiet nonce=- last_pong=2026-10-06T06:13:39Z session_pong_age=29s daemon_pong_age=- pongs=0 queue=0 working=0 width=1 beats=0 last_beat=- delivered=3 session=ok mode=batch held=- inbox=- missing=- presence=up last_session=2026-10-06T06:13:56Z status=up why="session answer 11s" evidence="harness unknown; session answer 11s; no limit; undelivered not counted; last result 12s exit=0" ...
```

The fake harness, as it stood for the job above (`fake3/main.go`). The daemon
calls it as `opencode run --session <s> --dir <d> <turn>`; it found that out
from the recording fake, not from the help.

```go
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	turn := os.Args[len(os.Args)-1]
	dir, _ := os.Getwd()
	me := filepath.Base(dir)
	trace(me, "TURN", turn)

	if strings.HasPrefix(turn, "SESSION CHECK") {
		_, cmd, _ := strings.Cut(turn, "end this turn: ")
		f := strings.Fields(cmd)
		out, err := exec.Command(f[0], f[1:]...).CombinedOutput()
		if err != nil {
			// a line with no --to is refused until a ping has named the seat: answer to ourselves
			out, err = exec.Command(f[0], append(f[1:], "--to", me)...).CombinedOutput()
		}
		trace(me, "PONG", fmt.Sprintf("%s err=%v", out, err))
		fmt.Print(string(out))
		return
	}

	head, body, _ := strings.Cut(turn, "\n\n")
	field := func(k string) string {
		for _, w := range strings.Fields(head) {
			if v, ok := strings.CutPrefix(w, k+"="); ok {
				return strings.Trim(v, `"`)
			}
		}
		return ""
	}
	id, from, subject := field("id"), field("from"), field("subject")
	switch subject {
	case "pong":
		return // the daemon's own proof of life, not a job
	case "job":
		out := filepath.Join(dir, "outbox", id)
		os.MkdirAll(out, 0o755)
		report := "Verdict: LAND\n\nfake harness did job " + id + " for " + from + ": " + strings.TrimSpace(body) + "\n"
		os.WriteFile(filepath.Join(out, "REPORT.md"), []byte(report), 0o644)
		res, err := exec.Command("nova-bus", "send", "--to", from, "--subject", "report", "--kind", "report",
			"--re", id, "--body", report, "--redis", "127.0.0.1:6379").CombinedOutput()
		trace(me, "REPLY", fmt.Sprintf("%s err=%v", res, err))
		fmt.Print(string(res))
	}
}

func trace(me, k, v string) {
	f, _ := os.OpenFile("/work/fake-"+me+".log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	defer f.Close()
	fmt.Fprintf(f, "%s %q\n", k, v)
}
```

## Stumbles

Each stumble: what was read, what was expected, what happened, and a proposed
card.

### 1. The install line gives a release that has no nova-friend

- Read: `README.md` "Try one on a small example" and `docs/USAGE.md#installing`.
- Expected: the way to get `nova-friend`, the tool the README table sends a
  person to for "be reachable as a friend".
- Happened: both pages install at `@v1.0.0` ("These are the Nova Tools 1.0.0
  commands"); `go install .../cmd/nova-friend@v1.0.0` fails with "does not
  contain package". The container has no network, so a built checkout was the
  only way on. Neither page says which release carries which tool, or which
  tools a friend needs next to `nova-friend` (`nova-bus`, `nova-redis`,
  `nova-config`).

card: readme-install-names-the-release-that-has-the-tool
paths: README.md,docs/USAGE.md
task: say which release carries each tool and that a friend needs nova-friend, nova-bus, nova-redis and nova-config.

### 2. `version` says `devel` for a build from a checkout

- Read: `nova-friend version`, `nova-bus version`.
- Expected: the release the tree is at (the run was told v1.2.0).
- Happened: `nova-friend devel linux/amd64 go1.26.6`; nothing says which
  release or commit the binary is.

card: version-names-the-commit-of-a-checkout-build
paths: pkg/buildinfo,pkg/binstamp
task: print the commit, and the nearest release tag, when a binary is built from a checkout without a stamp.

### 3. `run --dry-run` fails with an internal-sounding line

- Read: `nova-friend help run` ("--dry-run checks the flags and the harness and
  prints ... RUN DRY-RUN as= harness= dir= state= redis=").
- Expected: the `RUN DRY-RUN` line.
- Happened: with `--harness opencode` and no `opencode` on `PATH` yet, exit 1
  and `RUN FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written`. That is a guard message, not an
  answer: it does not say the harness binary was missing, or that the dry run
  did not run.

card: friend-run-dry-run-says-what-it-checked
paths: cmd/nova-friend/main.go,cmd/nova-friend/dryrun_test.go
task: make `run --dry-run` print the RUN DRY-RUN line, or name the missing harness binary, never the Call.DryRun guard.

### 4. A fake harness has no documented contract; the deliver command was found by recording it

- Read: `nova-friend help`, `help run`, `install -h`.
- Expected: for a harness, what the daemon runs for a delivery (arguments,
  stdin, working directory, what exit code means) and what a session must do.
- Happened: `--harness` is one of 18 names, so a made-up harness cannot be
  named; the fake has to stand in for a real one (`opencode`) on `PATH`.
  The help speaks of "the harness's deliver command" without printing any. I
  wrote a Go program that records its argv and learned it is
  `opencode run --session <s> --dir <d> "<turn>"`, exit 0 acks. The turn text
  of a SESSION CHECK carries the pong command; a delivered message arrives as
  `RECV OK id= from= ... subject=` then a blank line and the body. None of
  that is in the help. `install` is launchd-only, so on Linux `run` is the
  only way, and the help for `run` says "The loop launchd runs".

card: friend-help-shows-each-harness-deliver-command
paths: cmd/nova-friend/main.go,docs/SPEC-FRIEND.md
task: print per harness the exact deliver command, the turn text shape (SESSION CHECK and RECV OK) and the exit-code meaning, and say `run` is the Linux way.

### 5. The Redis login: the refusal does not name the variable

- Read: README (`--redis 127.0.0.1:6379` with no login), `nova-bus help`,
  `nova-redis help serve`.
- Expected: a throwaway Redis the examples work against.
- Happened: `nova-redis serve` always starts with `auth=on`, so every README
  example against it is refused with `NOAUTH`. The refusal says "name the user
  (NOVA_SPRINT_REDIS_USER) and the variable that holds its password", but not
  the variable that names that variable. `NOVA_REDIS_PASSWORD` (what serve
  reads), `NOVA_SPRINT_REDIS_PASSWORD` and `NOVA_BUS_REDIS_PASSWORD` did
  nothing. What I read to get past it: a grep of the Go source, which gave
  `NOVA_SPRINT_REDIS_PASSWORD_ENV`. Only then did `nova-bus names` answer.

card: bus-refusal-names-the-password-variable
paths: cmd/nova-bus/main.go,docs/SPEC-BUS.md
task: make the NOAUTH refusal print the full recipe, NOVA_SPRINT_REDIS_USER=<u> NOVA_SPRINT_REDIS_PASSWORD_ENV=<VAR> <VAR>=<password>, and the README's Redis rows say the example needs it after nova-redis serve.

### 6. "A Redis naming ada and bob" takes several steps no help lists

- Read: `nova-bus help` ("first run: a Redis naming ada and bob"),
  `nova-config help`.
- Expected: one line saying how a name gets into the store.
- Happened: `names` showed `count=0`. The names are nova-config friend rows,
  applied. With `--file try.json` that took `migrate`, two `friend add`, then
  `apply` refused three times in a row, each naming the next missing thing:
  `--as`, then the fleet's `redis_port` and `pg_dsn` (a PostgreSQL URI, for a
  run with no PostgreSQL), then a coordinator machine (so a `machine add`).
  Only then `NAMES OK count=3`.

card: bus-first-run-lists-the-config-steps-for-two-names
paths: cmd/nova-bus/main.go,cmd/nova-config/main.go
task: put the whole sequence (migrate --file, friend add, machine add, fleet set, apply --as) in nova-bus's first run line, or add a nova-config verb that makes two names in one call.

### 7. A friend's login is a Redis user nothing tells you how to make

- Read: the refusal `--as ada is not the login user default ... log in as ada (NOVA_SPRINT_REDIS_USER=ada with its password)`, `nova-redis acl render` and
  `acl apply -h`.
- Expected: the way to make user `ada` with a password.
- Happened: `acl render` prints four fixed users (coordinator, bench, ns-table,
  ns-friend), none a friend's name. I made the users with
  `redis-cli ACL SETUSER ada on ">adapw" "~*" "&*" "+@all"`, which is nowhere
  in the help, and is wide open. Without it the sender cannot be ada.

card: bus-help-says-how-a-friend-gets-a-redis-login
paths: cmd/nova-bus/main.go,docs/SPEC-BUS.md
task: say how a friend's Redis user is made (a nova-redis verb, or the exact ACL SETUSER) and what it may touch.

### 8. The SESSION CHECK carries a pong line that is refused

- Read: the daemon log and the turn text.
- Expected: the carried line, run exactly as written, answers the check ("the
  session runs the exact nova-friend pong line it carries").
- Happened: the first check carried `--to bob` and passed; the next
  (presence) check carried no `--to`, and the pong was refused: `PONG REFUSED: --to is required: no ping has named a seat yet`. The friend stayed
  `push=down` and nova-bus refused it as deaf. A ping from ada named the seat,
  but the daemon answered that ping itself (`daemon-pong`), and the session
  check was already spent. The fake had to retry with `--to <itself>`.

card: session-check-line-always-names-its-coordinator
paths: cmd/nova-friend/main.go,cmd/nova-friend/pushproof_test.go
task: make the pong line of every SESSION CHECK carry --to, so running it as written is enough.

### 9. The daemon's own pong comes back as a turn

- Read: the fake's log.
- Expected: a turn is a message from someone else.
- Happened: the pong the session sent (`subject="pong"`, to itself) was
  delivered back as a RECV OK turn, acked, one per check. A harness that treats
  each turn as a job does a job for it. Nothing in the help says to ignore it.

card: friend-help-says-a-pong-comes-back-as-a-turn
paths: cmd/nova-friend/main.go,docs/SPEC-FRIEND.md
task: say in `help run` that a session's own pong is delivered back as a turn with subject "pong", and what a harness should do with it.

### 10. What a job must write is not in the help for the batch mode

- Read: `nova-friend help run`.
- Expected: where the report goes and how it comes back.
- Happened: the only report the help names is the one-shot mode's (a lane
  reads `<dir>/inbox/QUEUE.json`, writes the card's `REPORT.md` and
  `RESULT.md`), and that mode needs the sprint server, which was not run (the
  daemon logged `the sprint server at 127.0.0.1:6390 did not answer` at every
  start, harmlessly). In batch mode (the default) a delivered message is just
  a turn; I chose to write `<dir>/outbox/<id>/REPORT.md` and reply with
  `nova-bus send --kind report --re <id>`. That is my guess. One-shot mode was
  not exercised.

card: friend-help-says-what-a-batch-job-writes
paths: cmd/nova-friend/main.go,docs/SPEC-FRIEND.md
task: say in `help run` what a batch-mode session writes for a delivered job and how the sender sees the answer, and that the sprint-server line is optional without a sprint.

### 11. `check` takes the flags after a friend as friends, and reads the wrong state

- Read: `nova-friend check -h` ("nova-friend check [--as <coordinator>]
  [<friend>...] [--since <duration>] ...").
- Expected: the health of bob, whose daemon was up.
- Happened: `check bob --state-dir /work/bob/.nova-friend` counted three
  friends, `bob`, `--state-dir` and the path, and judged bob `down` (it looks
  in `~/.nova-friend`, here `/work/home`, not the daemon's `--dir`). The
  usage line shows `--state-dir` nowhere. `status --as bob --dir /work/bob`
  found the daemon and said `presence=up`. Before the first beat, `status`
  said `no status file in /work/home/.nova-friend/bob` although the daemon's
  files are under `<dir>/.nova-friend`.

card: friend-check-refuses-a-flag-after-a-friend
paths: cmd/nova-friend/check.go,cmd/nova-friend/check_test.go
task: refuse an argument that starts with `-` as a friend, and let check take --dir the way status does.

## Verdict

could a stranger do it: no, not from the README and the help alone. It took
about 9 minutes of bench time from the first container command to the report
coming back (06:06 to 06:14 UTC), after the README and help had been read, with
three things read or done outside them: `docs/USAGE.md` (the README sends you
there), one grep of the source for the Redis password variable (stumble 5) and
`redis-cli ACL SETUSER` (stumble 7). The pieces do work: with both friends
heard, one `nova-bus send` reached bob's fake harness, which wrote its report to
`outbox/` and replied on the bus, and the sender's daemon delivered the reply
back to ada. What stops a stranger is not the daemon but the way in: an install
line for a release without the tool, a Redis login nothing explains, a names
setup that takes three refusals to learn, and no description of what a harness is
called with. Of the eleven stumbles, 4, 5, 6, 7 and 8 each stop the run.
