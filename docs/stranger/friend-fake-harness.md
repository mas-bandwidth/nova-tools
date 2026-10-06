# A cold run of nova-friend with a fake harness

A bud with no memory of the project ran a nova-friend daemon for a made-up
friend whose harness is a fake it wrote, delivered one job to it over nova-bus
on a throwaway Redis, and read the report come back. Rules of the run: only
README.md, the tools' own help (`<tool> help`, `<tool> <verb> -h`) and the
refusals the tools printed; no source, no specs, no docs/CLI.md. Where that was
not enough, the Stumbles section says so and says what was read instead.

## Setup

- Bench: a Linux x86_64 bench host with podman. One container, `podman run -d --rm
  --timeout 14400 --network none` from `localhost/nova-functional`, with
  `~/nova-bench/stranger/<job>/` mounted at `/work`. Redis, both daemons and the
  fake ran inside it on loopback and died with it; the bench directory is
  removed when the run ends.
- Read before the run: README.md and the briefing. AGENTS.md was read as the
  card's first step requires; it says nothing of nova-friend. Nothing else of
  the tree was read, docs/USAGE.md included.
- Install: README says `go install github.com/mas-bandwidth/nova-tools/cmd/<tool>@v1.0.0`;
  the container has no network, so the tools were built from a source checkout
  copied into `/work/src` with the module cache copied beside it (Stumbles 1).
- Versions, as printed by `<tool> version`:
  - `nova-bus devel linux/amd64 go1.26.6`
  - `nova-friend devel linux/amd64 go1.26.6`
  - `nova-config` was built the same way (its `version` was not run).
- Redis: `redis-server` from the image, port 6379 on 127.0.0.1, no persistence.
- The made-up friends are ada (sender) and bob (receiver); both run the daemon
  with harness `opencode`, which is the only name a fake can take (Stumbles 5).
  The fake is a Go program built as `opencode` onto PATH; `nova-friend run`
  executes it. Its source, whole:

```go
// fake opencode: the smallest harness the nova-friend daemon will drive.
// `session list` names one session; `run` executes the pong line a SESSION
// CHECK carries, and for a delivered message writes outbox/ and sends a
// report back to the sender over nova-bus.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var (
	pong = regexp.MustCompile(`\S*nova-friend pong [^\n]*`)
	recv = regexp.MustCompile(`RECV OK id=(\S+) from=(\S+) to=(\S+) .*subject="([^"]*)"\n\n((?s).*)`)
)

func main() {
	wd, _ := os.Getwd()
	me := filepath.Base(wd)
	log, _ := os.OpenFile("/work/fake.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	defer log.Close()
	fmt.Fprintf(log, "ARGV[%s] %q\n", me, os.Args[1:])
	if len(os.Args) > 2 && os.Args[1] == "session" && os.Args[2] == "list" {
		fmt.Printf("[{\"id\":\"ses_fake1\",\"directory\":%q,\"title\":\"fake\"}]\n", wd)
		return
	}
	prompt := os.Args[len(os.Args)-1]
	if line := pong.FindString(prompt); line != "" {
		if !strings.Contains(line, "--to ") {
			line += " --to " + me // the line the daemon carries has no --to before the first ping
		}
		out, err := exec.Command("sh", "-c", line).CombinedOutput()
		fmt.Fprintf(log, "PONG %q -> %q err=%v\n", line, out, err)
		fmt.Println("answered the session check")
		return
	}
	m := recv.FindStringSubmatch(prompt)
	if m == nil || m[4] == "pong" || strings.HasPrefix(m[4], "report: ") {
		return
	}
	id, from, subject, body := m[1], m[2], m[4], strings.TrimSpace(m[5])
	os.MkdirAll(filepath.Join(wd, "outbox"), 0o755)
	report := fmt.Sprintf("fake harness of %s read job %s from %s: %q\ndone at %s\n", me, id, from, body, time.Now().UTC().Format(time.RFC3339))
	os.WriteFile(filepath.Join(wd, "outbox", "REPORT-"+id+".md"), []byte(report), 0o644)
	out, err := exec.Command("nova-bus", "send", "--as", me, "--to", from, "--kind", "report", "--re", id,
		"--subject", "report: "+subject, "--body", report).CombinedOutput()
	fmt.Fprintf(log, "REPORT %s -> %q err=%v\n", id, out, err)
	fmt.Printf("%s", out)
}
```

## Transcript

Commands are the lines starting `$ `; everything else is the tool's output as
printed. Part A is the discovery on the way (the help texts are trimmed where
marked, nothing else is). Part B is the clean end-to-end rerun from empty state,
driven by one script run under `bash -x`, so each command is echoed with `$ `
in place of the trace prefix.

### Part A: finding the way

```
$ go install github.com/mas-bandwidth/nova-tools/cmd/nova-bus@v1.0.0
go: github.com/mas-bandwidth/nova-tools/cmd/nova-bus@v1.0.0: module lookup disabled by GOPROXY=off
$ go install ./cmd/nova-bus ./cmd/nova-friend   # in /work/src, module cache copied in
$ nova-friend help
[trimmed: the whole help; it lists run, install, check, ping, pong, status, serve; its example is `nova-friend install --as bob --harness opencode --dir ./bob --dry-run`]
$ nova-friend run --as bob --harness opencode --dir ./fr/bob --dry-run
RUN FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written
$ nova-friend help harness
FRIEND REFUSED: "harness" is no verb and no file; the verbs are run, install, uninstall, check, ping, ping-install, ping-uninstall, pong, wait-pong, status, serve, version, and a file is given by its path (./harness); run: nova-friend help
$ redis-server --port 6379 --bind 127.0.0.1 --save "" --appendonly no --daemonize yes --dir /work --logfile /work/redis.log
$ nova-bus names --redis 127.0.0.1:6379
NAMES OK count=0 proven=0
$ nova-bus send --as ada --to bob --subject hello --body hi --redis 127.0.0.1:6379
SEND REFUSED: ada is no known name; the names are nova-config's friend and machine rows (nova-bus names lists them); add one with nova-config friend add ada --slots 1 --tiers flash --as <you>, then nova-config apply; run: nova-bus help
SEND REFUSED: bob is no known name; the names are nova-config's friend and machine rows (nova-bus names lists them); add one with nova-config friend add bob --slots 1 --tiers flash --as <you>, then nova-config apply; run: nova-bus help
$ nova-config help
[trimmed: the whole help; the line that mattered: "--file <path> keeps the rows in a local JSON file instead, to try every verb with no database"]
$ nova-config apply --file try.json --redis 127.0.0.1:6379 --as ada
nova-config apply REFUSED: fleet: endpoints are unset: redis_port, pg_dsn; run: nova-config fleet set --redis_port <port> --pg_dsn <dsn> --as <actor>, then nova-config apply --kind fleet --as <actor>
$ nova-config apply --kind friend --file try.json --redis 127.0.0.1:6379 --as ada
APPLY ADD kind=friend name=ada
nova-config apply REFUSED: friend ada has no beat naming a machine and the fleet names no coordinator machine to charge her slots to; run: nova-config fleet set --coordinator <machine>, then apply; run: nova-config apply --dry-run
$ nova-bus send --as ada --to bob --subject hello --body hi --redis 127.0.0.1:6379
SEND REFUSED: deaf: ada has no proven push since never: no daemon has recorded one; the remedy: ada runs its friend daemon with a deliver adapter for its harness (nova-friend install --as ada --harness <h> --dir <d>) and its session answers the daemon's SESSION CHECK, which records the proof; nova-bus names shows every name's push; run: nova-bus help
SEND REFUSED: deaf: bob has no proven push since never: no daemon has recorded one; the remedy: bob runs its friend daemon with a deliver adapter for its harness (nova-friend install --as bob --harness <h> --dir <d>) and its session answers the daemon's SESSION CHECK, which records the proof; nova-bus names shows every name's push; run: nova-bus help
$ nova-friend run --as bob --harness opencode --dir ./fr/bob --redis 127.0.0.1:6379
RUN REFUSED: no push proof: CHECK FAIL harness=opencode stage=deliver why="opencode session list: exec: \"opencode\": executable file not found in $PATH"; the daemon did not start; run: open the friend's opencode session in ./fr/bob, then prove it answers: nova-friend check --as bob --harness opencode --dir ./fr/bob
$ nova-friend run --as bob --harness opencode --dir ./fr/bob --redis 127.0.0.1:6379   # a fake opencode that prints ses_fake1
RUN REFUSED: no push proof: CHECK FAIL harness=opencode stage=deliver why="opencode session list: not a JSON list: invalid character 's' looking for beginning of value"; the daemon did not start; run: open the friend's opencode session in ./fr/bob, then prove it answers: nova-friend check --as bob --harness opencode --dir ./fr/bob
$ nova-friend run --as bob --harness opencode --dir ./fr/bob --redis 127.0.0.1:6379   # fake now prints [{"id":"ses_fake1","directory":"/work/fr/bob","title":"fake"}]
RUN REFUSED: no push proof: CHECK FAIL harness=opencode stage=deliver why="no opencode session for ./fr/bob; start one there, or name one with --session"; the daemon did not start; run: open the friend's opencode session in ./fr/bob, then prove it answers: nova-friend check --as bob --harness opencode --dir ./fr/bob
$ nova-friend run --as bob --harness opencode --dir ./fr/bob --session ses_fake1 --redis 127.0.0.1:6379   # the fake logs its argv to /work/fake.log
$ cat /work/fake.log
ARGV ["session" "list" "--format" "json"]
ARGV ["run" "--session" "ses_fake1" "--dir" "./fr/bob" "SESSION CHECK ccs98y\nThe daemon has had no bus message from this session for a while and asks whether the session is alive; only an answer from inside this session counts, and none within 5m0s puts you down. Answer now, before anything else, with one command, then end this turn: /work/bin/nova-friend pong --as bob --nonce ccs98y --state-dir /work/.nova-friend/bob --redis 127.0.0.1:6379 --to bob\n"]
$ # a later SESSION CHECK, run as the line says:
PONG REFUSED: --to is required: no ping has named a seat yet (no status file in /work/.nova-friend/bob); it wants the coordinator's name; run: nova-friend help
```

### Part B: the clean rerun

```
$ cd /work
$ redis-server --port 6379 --bind 127.0.0.1 --save '' --appendonly no --daemonize yes --dir /work --logfile /work/redis.log
$ sleep 1
$ export NOVA_BUS_REDIS=127.0.0.1:6379
$ nova-config migrate --file try.json
CONFIG MIGRATE file=try.json from=0 to=33 applied=33
$ nova-config friend add ada --slots 1 --tiers flash --as ada --file try.json
CONFIG ADD kind=friend name=ada rev=1
$ nova-config friend add bob --slots 1 --tiers flash --as ada --file try.json
CONFIG ADD kind=friend name=bob rev=2
$ nova-config machine add m1 --user nova --seat s1 --slots 8 --width 4 --as ada --file try.json
CONFIG ADD kind=machine name=m1 rev=3
$ nova-config fleet set --coordinator m1 --store m1 --redis_port 6379 --pg_dsn postgres://nova@127.0.0.1:5432/nova --bus 127.0.0.1:6379 --as ada --file try.json
CONFIG SET kind=fleet name=fleet rev=4 changed=bus,coordinator,pg_dsn,redis_port,store
$ nova-config apply --file try.json --redis 127.0.0.1:6379 --as ada
APPLY ADD kind=machine name=m1
CONFIG APPLY kind=machine add=1 set=0 remove=0 rev=3 ms=12
APPLY SET kind=fleet name=fleet changed=store,coordinator,redis_port,pg_dsn,bus,loops_dir
CONFIG APPLY kind=fleet add=0 set=1 remove=0 rev=4 ms=0
APPLY ADD kind=friend name=ada
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
$ mkdir -p fr/ada fr/bob
$ nova-friend run --as ada --harness opencode --dir ./fr/ada --session ses_fake1
$ sleep 8
$ nova-friend run --as bob --harness opencode --dir ./fr/bob --session ses_fake1
$ nova-bus names
NAMES OK count=3 proven=2
NAMES NAME name=ada push=proven age=6s harness=opencode
NAMES NAME name=bob push=proven age=6s harness=opencode
NAMES NAME name=m1 push=none age=never harness=-
$ nova-bus send --as ada --to bob --subject 'job 1' --body 'count the words in: the quick brown fox'
SEND OK id=01M47QH7ACWYR6DHPKZNWY0JX2 to=bob cc=- at=2026-10-06T04:28:45Z bytes=39 sha256=63853ccc64e00ff32b4502cd8c30d13d2936f16b99b3c0212165d2addbeaf3cf login=none
$ sleep 6
$ cat fr/bob/outbox/REPORT-01M47QH7ACWYR6DHPKZNWY0JX2.md
fake harness of bob read job 01M47QH7ACWYR6DHPKZNWY0JX2 from ada: "count the words in: the quick brown fox"
done at 2026-10-06T04:28:45Z
$ nova-bus log --bodies --max 0
LOG OK total=6
LOG MESSAGE id=01M47QGZGNDMDRS6756P0SQ2W8 from=ada to=ada cc=- re=- at=2026-10-06T04:28:37Z subject="pong" body="pong 2awo99 queue=0 working=0 width=0\n"
LOG MESSAGE id=01M47QGZGNWEF70KC5HCBYT7T8 from=bob to=bob cc=- re=- at=2026-10-06T04:28:37Z subject="pong" body="pong weobhw queue=0 working=0 width=0\n"
LOG MESSAGE id=01M47QH0H7XPKMHCGCG78HK6BE from=ada to=ada cc=- re=- at=2026-10-06T04:28:38Z subject="pong" body="pong dfk9v2 queue=0 working=0 width=0\n"
LOG MESSAGE id=01M47QH0HD1HNEGD82AGFMZ7RK from=bob to=bob cc=- re=- at=2026-10-06T04:28:38Z subject="pong" body="pong c7h9ly queue=0 working=0 width=0\n"
LOG MESSAGE id=01M47QH7ACWYR6DHPKZNWY0JX2 from=ada to=bob cc=- re=- at=2026-10-06T04:28:45Z subject="job 1" body="count the words in: the quick brown fox"
LOG MESSAGE id=01M47QH7AS7WE5MG6A3XG3RDGA from=bob to=ada cc=- re=01M47QH7ACWYR6DHPKZNWY0JX2 kind=report at=2026-10-06T04:28:45Z subject="report: job 1" body="fake harness of bob read job 01M47QH7ACWYR6DHPKZNWY0JX2 from ada: \"count the words in: the quick brown fox\"\ndone at 2026-10-06T04:28:45Z\n"
$ nova-friend status --as bob --dir ./fr/bob
STATUS OK daemon=up harness=opencode status_age=6s connection=connected seat=- last_ping=2026-10-06T04:28:37Z ping_age=14s challenge=quiet nonce=- last_pong=2026-10-06T04:28:38Z session_pong_age=13s daemon_pong_age=- pongs=0 queue=0 working=0 width=1 beats=0 last_beat=- delivered=3 session=ok mode=batch held=- inbox=- missing=- presence=up last_session=2026-10-06T04:28:46Z status=up why="session answer 13s" evidence="harness unknown; session answer 13s; no limit; 0 undelivered; last result 6s exit=0"
STATUS NOTE the inbox: the sprint server at 127.0.0.1:6390 did not answer: Post "http://127.0.0.1:6390/verbs": dial tcp 127.0.0.1:6390: connect: connection refused
STATUS NOTE the last beat failed: the sprint server at 127.0.0.1:6390 did not answer: Post "http://127.0.0.1:6390/verbs": dial tcp 127.0.0.1:6390: connect: connection refused
$ nova-friend check --as ada bob
CHECK DAEMON friend=bob agent=none pid=- status=ok connection=connected challenge=quiet pong_age=13s presence=up seen_age=5s
CHECK HARNESS friend=bob harness=opencode route=push last=2026-10-06T04:28:45Z last_exit=0 failed_of_last20=0 deferred=0 broken=- reason=-
CHECK BUS friend=bob real_since=1 last_real=2026-10-06T04:28:45Z
CHECK WORK friend=bob inbox=0 outbox=0 newest_outbox=- newest_at=-
CHECK VERDICT friend=bob verdict=down shown=- why="down by presence"
CHECK OK friends=1 ok=0 broken=0 deaf=0 silent=0 down=1 untrue=0
$ grep -h subject= daemon-bob.out daemon-ada.out
RUN 2026-10-06T04:28:37Z subject="pong" messages=1 took=19ms exit=0 acked=true
RUN 2026-10-06T04:28:38Z subject="pong" messages=1 took=25ms exit=0 acked=true
RUN 2026-10-06T04:28:45Z subject="job 1" messages=1 took=638ms exit=0 acked=true
RUN 2026-10-06T04:28:37Z subject="pong" messages=1 took=18ms exit=0 acked=true
RUN 2026-10-06T04:28:38Z subject="pong" messages=1 took=20ms exit=0 acked=true
RUN 2026-10-06T04:28:45Z subject="report: job 1" messages=1 took=677ms exit=0 acked=true
$ kill %1 %2
$ redis-cli shutdown nosave
```

The report is the last `LOG MESSAGE` line of the bus log (`kind=report`,
`re=` the job's id), and `fr/bob/outbox/REPORT-<id>.md` is the fake's own copy.
The first full run of the fake also answered reports with reports, which looped
over the bus until the daemons were stopped; the fake in this file ignores
subjects starting `report: `.

## Stumbles

### 1. The install line cannot run offline, and names another version

- Read: README, "Try one on a small example".
- Expected: the printed `go install` line installs a tool at the version the
  tools are at (the run is of 1.2.0).
- Happened: the line names v1.0.0, and in a container with no network it ends
  `module lookup disabled by GOPROXY=off`. I built from a source checkout with a
  copied module cache instead. Both tools then print `devel` from `version`, so
  no version is recorded for the run.
- card: readme-install-names-current-version-and-offline-build
- PATHS: README.md
- Task: README's install section names the current release and gives the
  checkout build line for a machine with no network, and says what `version`
  prints from a checkout.

### 2. `nova-friend run --dry-run` fails on the flag help documents

- Read: `nova-friend help run` ("--dry-run checks the flags and the harness and
  prints the daemon it would run").
- Expected: `RUN DRY-RUN as= harness= dir= state= redis=`.
- Happened: `RUN FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written`, exit 1. The text reads as a tool-internal assertion, not a refusal.
- card: friend-run-dry-run-prints-the-daemon
- PATHS: cmd/nova-friend (named by the tool's own error; the source was not read)
- Task: `nova-friend run --dry-run` prints the RUN DRY-RUN line its help promises.

### 3. A bus trial needs a config chain the README does not show

- Read: README's nova-bus row ("a separate running Redis instance whose
  nova-config rows name the sender and the recipient"), then `nova-bus send`
  refusals, then `nova-config help`.
- Expected: the row's command works after one setup step I can find.
- Happened: three refusals in a row before the names existed: friends unknown;
  `apply` refused for fleet endpoints `redis_port, pg_dsn` unset although the
  store is a local file (a Postgres DSN is demanded for a trial with no
  Postgres); then "friend has no beat naming a machine", which needs a machine
  row and `fleet set --coordinator`. Each refusal named the next step, so it
  was passable, but the working sequence (migrate, two friends, one machine,
  fleet set, apply) is nowhere in README or help.
- card: bus-trial-config-chain-in-help
- PATHS: README.md
- Task: README's nova-bus row, or `nova-bus help`, carries the exact
  nova-config `--file` sequence that makes two names a throwaway Redis accepts,
  with no Postgres.

### 4. The README's first nova-bus command cannot succeed for a stranger

- Read: README's nova-bus row, then `nova-bus send -h`.
- Expected: the quoted `nova-bus send --as ada --to bob ...` sends.
- Happened: `SEND REFUSED: deaf: ada has no proven push`, and the same for bob:
  both ends must run a friend daemon whose session answered a check in the last
  ten minutes. `send -h` says so; README does not.
- card: bus-readme-row-names-the-deaf-rule
- PATHS: README.md
- Task: the nova-bus row says a sender and a recipient each need a proven
  friend daemon before the first send.

### 5. Nothing says how to supply a harness that is not one of the eighteen

- Read: `nova-friend help`, `help run`, `run -h`, `check -h`, `install` help.
- Expected: the help describes what the daemon runs for a harness and what that
  program reads and writes, so a fake can be written. The card's premise is that
  the help says what the report must be.
- Happened: the help lists eighteen harness names and says nothing of the
  contract. I learned it from refusals and by logging the fake's argv: a program
  named `opencode` on PATH is called `opencode session list --format json` and
  must print a JSON list; then `opencode run --session <id> --dir <d> "<prompt>"`
  once per delivery, where the prompt is the pushed turn. A session is matched to
  `--dir` by a field I did not find (`directory` did not match; I named the
  session with `--session` instead). A SESSION CHECK turn carries one shell line
  to run; a message turn carries `RECV OK id= from= to= ... subject=` and the
  body.
- card: friend-help-names-the-harness-contract
- PATHS: cmd/nova-friend (help text; the source was not read)
- Task: `nova-friend help` has a "writing a harness" paragraph: the program
  name, its subcommands, the JSON a session list returns (field names), the
  turn text, and a worked fake in Go, not a shell example.

### 6. The SESSION CHECK pong line is refused when run as printed

- Read: the SESSION CHECK turn the daemon delivered (stumble 5).
- Expected: running the line the check carries answers the check.
- Happened: the first check carried `--to bob`; a later one carried no `--to`
  and running it verbatim gave `PONG REFUSED: --to is required: no ping has named
  a seat yet`. The fake appends `--to <itself>` when the line has none; a
  harness that runs the line as given fails the proof.
- card: friend-session-check-line-carries-to
- PATHS: cmd/nova-friend
- Task: a SESSION CHECK turn's pong line always runs as printed, with `--to`
  when no ping has named a seat.

### 7. The outbox report the card expected is not in the help

- Read: `nova-friend help run` (one-shot mode: "the REPORT.md and RESULT.md to
  write, one bus line to send"), `check -h` ("CHECK WORK ... outbox=<n>").
- Expected: help says where a harness writes a report for a delivered job.
- Happened: in the default batch mode the help names no report at all; the only
  report named is one-shot mode's, whose QUEUE.json and BRIEF.md format the help
  does not show. The fake wrote `fr/bob/outbox/REPORT-<id>.md` and sent a
  `--kind report` message back, and the second one is what came back over the
  bus. `check` then said `CHECK WORK friend=bob inbox=0 outbox=0` while the
  directory held a report, so the outbox it counts is not the one I wrote.
- card: friend-help-says-where-the-report-goes
- PATHS: cmd/nova-friend
- Task: help names the batch-mode report (a bus message, kind report, re= the
  job) and the directory `CHECK WORK` counts, and shows the one-shot QUEUE.json.

### 8. `check` says down while `status` says up

- Read: `nova-friend status -h`, `check -h`.
- Expected: one verdict for one friend.
- Happened: `STATUS ... presence=up status=up` and `CHECK DAEMON ...
  presence=up`, then `CHECK VERDICT friend=bob verdict=down why="down by
  presence"`, exit 1, with delivery working. The sprint server on 6390 was not
  running; whether that is the cause is not said.
- card: friend-check-verdict-agrees-with-status
- PATHS: cmd/nova-friend
- Task: `check`'s verdict says which fact made it down, and agrees with status
  when presence is up.

### 9. A friend with no sprint server has no quiet way to run

- Read: `nova-friend help run` (`--server` default 127.0.0.1:6390).
- Expected: a bus-only friend runs without noise.
- Happened: every `status` carries two NOTE lines about the refused sprint
  server and the daemon's RUN log repeats the inbox failure each second;
  nothing says to ignore them or how to say "no sprint server".
- card: friend-run-without-a-sprint-server
- PATHS: cmd/nova-friend
- Task: `run --server none` (or the default when nothing listens) runs bus-only
  and says so once.

### 10. `nova-bus log --max 5` hides the newest message

- Read: `nova-bus help`, `LOG MORE` line.
- Expected: the newest messages first, or a flag for it, since the report is
  the thing I came to read.
- Happened: the five oldest of six were listed (four pongs, the job), and the
  report was behind `LOG MORE`; `--max 0` showed it.
- card: bus-log-lists-newest-first
- PATHS: cmd/nova-bus
- Task: `nova-bus log` lists newest first, or takes `--reverse`.

### 11. The container's work directory is not writable by its user

- Read: the card; the image's `id`.
- Expected: `/work`, the mount, is writable by the image's user.
- Happened: the image user is `bench` (uid 10001) and `/work` is root-owned, so
  every command ran as `podman exec --user root`; as bench `go install` ended
  `mkdir /work/bin/: permission denied`.
- card: functional-image-work-dir-writable
- PATHS: infra/functional-image/Containerfile
- Task: the functional image's work directory is writable by its user (or the
  run line names `--userns keep-id`).

## Verdict

could a stranger do it: no
minutes: 10

A stranger got a job from one friend to another and the report back, but not
from README and help alone: the fake's contract (stumble 5) was found by
logging argv, and the nova-config chain (stumble 3) by following refusals. The
ten minutes are an estimate from the bench clock: 04:24 UTC had passed when the
README and the first help texts were read, and the report reached the bus at
04:28:45 UTC in the clean rerun; the write-up is not counted.
