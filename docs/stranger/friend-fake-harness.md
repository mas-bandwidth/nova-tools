# Stranger run: nova-friend with a fake harness

A cold run by a bud (rowan-space, Claude Sonnet 5.5 under Claude Code) from
README.md and the tools' own help. Goal: run a nova-friend for a made-up friend
whose harness is a fake written in Go, deliver one job over nova-bus on a
throwaway Redis, and see the report come back. The PATHS in each proposed card are guesses; I did not read the source. Read beyond README and help: see
each stumble.

## Setup

- Bench: vision (Linux x86_64, podman 5.7.0, Go 1.26.6), one container
  `podman run --rm --timeout 14400 --network none --user root` from the
  functional image (`localhost/nova-functional`), the run directory mounted as
  `/work`, the checkout mounted read-only as `/src`. Container and run directory
  removed afterwards.
- Checkout: nova-tools at 6999ddcfe9ef16811164853c5a09b77b70d91d96
  (sprint/mechanical-2026-10-02).
- Redis: the image's `redis-server` on 127.0.0.1:6379 inside the container.
- Versions, as printed:

```
$ nova-friend version
nova-friend v1.0.1-0.20261006030628-6999ddcfe9ef linux/amd64 go1.26.6
$ nova-bus version
nova-bus v1.0.1-0.20261006030628-6999ddcfe9ef linux/amd64 go1.26.6
```

## Transcript

Every command run in the container (`PATH=/work/bin`, `HOME=/work/home`), output
exact except where marked `[trimmed]` (long help text cut to the lines used).

Install, as the README says:

```
$ go install github.com/mas-bandwidth/nova-tools/cmd/nova-friend@v1.0.0
go: github.com/mas-bandwidth/nova-tools/cmd/nova-friend@v1.0.0: module lookup disabled by GOPROXY=off
```

The same on the bench host (network on):

```
$ go install github.com/mas-bandwidth/nova-tools/cmd/nova-friend@v1.0.0
go: downloading github.com/mas-bandwidth/nova-tools v1.0.0
go: github.com/mas-bandwidth/nova-tools/cmd/nova-friend@v1.0.0: module github.com/mas-bandwidth/nova-tools@v1.0.0 found, but does not contain package github.com/mas-bandwidth/nova-tools/cmd/nova-friend
$ go install github.com/mas-bandwidth/nova-tools/cmd/nova-friend@latest
go: github.com/mas-bandwidth/nova-tools/cmd/nova-friend@latest: module github.com/mas-bandwidth/nova-tools@latest found (v1.0.0), but does not contain package github.com/mas-bandwidth/nova-tools/cmd/nova-friend
```

Building from the checkout inside the container, offline:

```
$ go install ./cmd/nova-friend ./cmd/nova-bus ./cmd/nova-config
go: downloading golang.org/x/mod v0.41.0
go: downloading github.com/rogpeppe/go-internal v1.16.0
internal/redisconn/classify.go:14:2: module lookup disabled by GOPROXY=off
internal/redisconn/open.go:17:2: module lookup disabled by GOPROXY=off
internal/sandbox/wrap_linux.go:37:2: module lookup disabled by GOPROXY=off
internal/friend/settings_dsh.go:8:2: module lookup disabled by GOPROXY=off
[trimmed: four more lines of the same]
```

Built on the bench host instead (`CGO_ENABLED=0 go install ./cmd/nova-friend ./cmd/nova-bus ./cmd/nova-config`,
`GOBIN` the run directory's `bin/`), then in the container:

```
$ nova-friend help
nova-friend: what a friend runs to be part of the team: the wake loop, the beat, and the proof of life, as one daemon
[trimmed: usage lines; the text says a harness with no deliver command is refused, and lists "opencode, codex, claude, antigravity, dsh, gemini, grok, ..."]
$ nova-friend help run
[trimmed: 60 lines; one-shot lanes read <dir>/inbox/QUEUE.json, "its BRIEF.md, the REPORT.md and RESULT.md to write"; the flags]
$ nova-friend run --as bob --harness claude --dir /work/bob --redis 127.0.0.1:6379 --dry-run
RUN REFUSED: no deliver command for claude: nothing the bus holds for the friend reaches her session; the daemon did not start; run: the adapter card: give internal/friend a deliver command for claude (NewDeliverer), or run the friend under a harness that has one: opencode, codex, antigravity, dsh, gemini, grok
$ nova-friend run --as bob --harness opencode --dir /work/bob --redis 127.0.0.1:6379 --dry-run
RUN DRY-RUN as=bob harness=opencode dir=/work/bob state=/home/bench/.nova-friend/bob redis=127.0.0.1:6379; nothing was started
```

Redis and the names:

```
$ redis-server --bind 127.0.0.1 --port 6379 --save "" --appendonly no --daemonize yes --logfile /work/redis.log
$ redis-cli ping
PONG
$ nova-bus names --redis 127.0.0.1:6379
NAMES OK count=0 proven=0
$ nova-bus send --redis 127.0.0.1:6379 --as ada --to bob --subject hello --body hi
SEND REFUSED: ada is no known name; the names are nova-config's friend and machine rows (nova-bus names lists them); add one with nova-config friend add ada --slots 1 --tiers flash --as <you>, then nova-config apply; run: nova-bus help
SEND REFUSED: bob is no known name; the names are nova-config's friend and machine rows (nova-bus names lists them); add one with nova-config friend add bob --slots 1 --tiers flash --as <you>, then nova-config apply; run: nova-bus help
$ nova-config migrate --file try.json
CONFIG MIGRATE file=try.json from=0 to=33 applied=33
$ nova-config friend add ada --slots 1 --tiers flash --as ada --file try.json
CONFIG ADD kind=friend name=ada rev=1
$ nova-config friend add bob --slots 1 --tiers flash --mode one-shot --width 1 --as ada --file try.json
CONFIG ADD kind=friend name=bob rev=2
$ nova-config apply --file try.json --redis 127.0.0.1:6379 --as ada
nova-config apply REFUSED: fleet: endpoints are unset: redis_port, pg_dsn; run: nova-config fleet set --redis_port <port> --pg_dsn <dsn> --as <actor>, then nova-config apply --kind fleet --as <actor>
$ nova-config apply --kind friend --file try.json --redis 127.0.0.1:6379 --as ada
APPLY ADD kind=friend name=ada
nova-config apply REFUSED: friend ada has no beat naming a machine and the fleet names no coordinator machine to charge her slots to; run: nova-config fleet set --coordinator <machine>, then apply; run: nova-config apply --dry-run
$ nova-config machine add m1 --user nova --seat s1 --slots 8 --width 4 --as ada --file try.json
CONFIG ADD kind=machine name=m1 rev=3
$ nova-config fleet set --coordinator m1 --store m1 --redis_port 6379 --pg_dsn postgres://nobody@127.0.0.1:5432/x --as ada --file try.json
CONFIG SET kind=fleet name=fleet rev=4 changed=coordinator,pg_dsn,redis_port,store
$ nova-config apply --file try.json --redis 127.0.0.1:6379 --as ada
APPLY ADD kind=machine name=m1
CONFIG APPLY kind=machine add=1 set=0 remove=0 rev=3 ms=2
APPLY SET kind=fleet name=fleet changed=store,coordinator,redis_port,pg_dsn,loops_dir
CONFIG APPLY kind=fleet add=0 set=1 remove=0 rev=4 ms=0
APPLY ADD kind=friend name=ada
APPLY ADD kind=friend name=bob
CONFIG APPLY kind=friend add=2 set=0 remove=0 rev=2 ms=0
[trimmed: sprint, loop, route and tier lines]
$ nova-bus names --redis 127.0.0.1:6379
NAMES OK count=3 proven=0
NAMES NAME name=ada push=none age=never harness=-
NAMES NAME name=bob push=none age=never harness=-
NAMES NAME name=m1 push=none age=never harness=-
$ nova-bus send --redis 127.0.0.1:6379 --as ada --to bob --subject hello --body hi
SEND REFUSED: deaf: ada has no proven push since never: no daemon has recorded one; the remedy: ada runs its friend daemon with a deliver adapter for its harness (nova-friend install --as ada --harness <h> --dir <d>) and its session answers the daemon's SESSION CHECK, which records the proof; nova-bus names shows every name's push; run: nova-bus help
```

The fake harness. The first version (`fake/main.go`, argv, stdin and NOVA/OPENCODE
env appended to `/work/fake.log`, prints one line) was built as `bin/opencode`,
because the daemon runs the harness named by `--harness` from PATH; the help
never says so. Probing it:

```
$ nova-friend run --as bob --harness opencode --dir /work/bob --redis 127.0.0.1:6379 --mode one-shot --width 1
RUN REFUSED: no push proof: CHECK FAIL harness=opencode stage=deliver why="opencode session list: not a JSON list: invalid character 'k' in literal false (expecting 'l')"; the daemon did not start; run: open the friend's opencode session in /work/bob, then prove it answers: nova-friend check --as bob --harness opencode --dir /work/bob
$ cat /work/fake.log
ARGV ["opencode" "session" "list" "--format" "json"]
```

Answering `[{"id":"ses_fake1","directory":"/work/bob"}]` to that:

```
RUN REFUSED: no push proof: CHECK FAIL harness=opencode stage=deliver why="no opencode session for /work/bob; start one there, or name one with --session"; the daemon did not start; ...
```

(the keys the daemon wants were not guessable; `--session ses_fake1` skips the list.) With `--session`:

```
$ cat /work/fake.log
ARGV ["opencode" "run" "--session" "ses_fake1" "--dir" "/work/bob" "SESSION CHECK m71vde\nThe daemon has had no bus message from this session for a while and asks whether the session is alive; only an answer from inside this session counts, and none within 5m0s puts you down. Answer now, before anything else, with one command, then end this turn: /work/bin/nova-friend pong --as bob --nonce m71vde --state-dir /work/home/.nova-friend/bob --redis 127.0.0.1:6379 --to bob\n"]
```

The fake was extended to run the `nova-friend pong ...` command found in a
SESSION CHECK prompt. The first check line carried `--to bob`, a later one did not:

```
PONG REFUSED: --to is required: no ping has named a seat yet (no status file in /work/home/.nova-friend/bob); it wants the coordinator's name; run: nova-friend help
RUN 2026-10-06T03:22:40Z inbox: the server did not say which cards are on her row: the sprint server at 127.0.0.1:6390 did not answer: Post "http://127.0.0.1:6390/verbs": dial tcp 127.0.0.1:6390: connect: connection refused; nothing written or retired until it does
RUN 2026-10-06T03:22:40Z lane 1: no session: opencode session list exited 125; tried again in 1m0s
```

One-shot mode needs a sprint server for its cards, so the run went on in batch
mode, the fake adding `--to <name>` when missing, a daemon for each of ada and bob:

```
$ nova-friend run --as bob --harness opencode --dir /work/bob --session ses_bob --redis 127.0.0.1:6379 --mode batch
$ nova-friend run --as ada --harness opencode --dir /work/ada --session ses_ada --redis 127.0.0.1:6379 --mode batch
$ nova-bus names --redis 127.0.0.1:6379
NAMES OK count=3 proven=2
NAMES NAME name=ada push=proven age=6s harness=opencode
NAMES NAME name=bob push=proven age=6s harness=opencode
NAMES NAME name=m1 push=none age=never harness=-
$ nova-bus send --redis 127.0.0.1:6379 --as ada --to bob --subject job-1 --body "write your report to /work/outbox/job-1/REPORT.md"
SEND OK id=01M47KS6ZZJ5F0X9EV66TX78FQ to=bob cc=- at=2026-10-06T03:23:12Z bytes=49 sha256=f9a803092ee5a56ddc4b134c72e09bf7968f4a6a9108777c6a844eb44d47eaa7 login=none
$ cat /work/fake.log
ARGV ["opencode" "run" "--session" "ses_bob" "--dir" "/work/bob" "RECV OK id=01M47KS6ZZJ5F0X9EV66TX78FQ from=ada to=bob cc=- re=- at=2026-10-06T03:23:12Z subject=\"job-1\"\n\nwrite your report to /work/outbox/job-1/REPORT.md\n"]
$ tail -1 /work/bob.out
RUN 2026-10-06T03:23:12Z subject="job-1" messages=1 took=574ms exit=0 acked=true
```

The fake then wrote the report named in the body and replied to the sender over
the bus (job-2):

```
$ nova-bus send --redis 127.0.0.1:6379 --as ada --to bob --subject job-2 --body "write your report to /work/outbox/job-2/REPORT.md"
SEND OK id=01M47KSKF76Y6X96RJY1EZJZ2Z to=bob cc=- at=2026-10-06T03:23:25Z bytes=49 sha256=4828c9828d3798971996e8eddf41fd0d63dd5da3c6e8fad00b9a00bf90839409 login=none
$ cat /work/outbox/job-2/REPORT.md
Verdict: LAND
fake harness did the job 01M47KSKF76Y6X96RJY1EZJZ2Z
$ tail -1 /work/bob.out
RUN 2026-10-06T03:23:25Z subject="job-2" messages=1 took=486ms exit=0 acked=true
$ nova-bus log --bodies --max 0 --redis 127.0.0.1:6379 | grep -E "job-|report"
LOG MESSAGE id=01M47KS6ZZJ5F0X9EV66TX78FQ from=ada to=bob cc=- re=- at=2026-10-06T03:23:12Z subject="job-1" body="write your report to /work/outbox/job-1/REPORT.md"
LOG MESSAGE id=01M47KSKF76Y6X96RJY1EZJZ2Z from=ada to=bob cc=- re=- at=2026-10-06T03:23:25Z subject="job-2" body="write your report to /work/outbox/job-2/REPORT.md"
LOG MESSAGE id=01M47KSKFGVB0MMNCPDCHPB8KZ from=bob to=ada cc=- re=01M47KSKF76Y6X96RJY1EZJZ2Z at=2026-10-06T03:23:25Z subject="report" body="report written to /work/outbox/job-2/REPORT.md"
```

The final fake (`bin/opencode`, Go, built on the bench host):

```go
package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

func main() {
	in, _ := io.ReadAll(os.Stdin)
	f, _ := os.OpenFile("/work/fake.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	defer f.Close()
	fmt.Fprintf(f, "ARGV %q\nSTDIN %q\nENV %s\n---\n", os.Args, string(in), strings.Join(filterEnv(), " "))
	if len(os.Args) > 2 && os.Args[1] == "session" && os.Args[2] == "list" {
		fmt.Println(`[{"id":"ses_fake1","directory":"/work/bob"}]`)
		return
	}
	prompt := os.Args[len(os.Args)-1]
	for _, line := range strings.Split(prompt, "\n") {
		if strings.HasPrefix(line, "SESSION CHECK") {
			continue
		}
		if i := strings.Index(line, "/work/bin/nova-friend pong "); i >= 0 {
			argv := strings.Fields(line[i:])
			if !strings.Contains(line, " --to ") {
				argv = append(argv, "--to", argv[3])
			}
			out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
			fmt.Fprintf(f, "PONG %s err=%v\n", out, err)
			fmt.Print(string(out))
		}
	}
	if strings.HasPrefix(prompt, "RECV OK ") {
		report(prompt)
	}
	fmt.Println("fake harness saw the turn")
}

// report answers a delivered job: the body names the report path; write it
// there and say so to the sender on the bus.
func report(prompt string) {
	from := field(prompt, "from=")
	id := field(prompt, "id=")
	body := prompt[strings.Index(prompt, "\n\n")+2:]
	const key = "write your report to "
	i := strings.Index(body, key)
	if i < 0 {
		return
	}
	path := strings.TrimSpace(body[i+len(key):])
	_ = os.MkdirAll(path[:strings.LastIndex(path, "/")], 0o755)
	_ = os.WriteFile(path, []byte("Verdict: LAND\nfake harness did the job "+id+"\n"), 0o644)
	out, err := exec.Command("/work/bin/nova-bus", "send", "--redis", "127.0.0.1:6379", "--as", "bob", "--to", from, "--re", id, "--subject", "report", "--body", "report written to "+path).CombinedOutput()
	fmt.Print(string(out), err)
}

func field(s, k string) string {
	i := strings.Index(s, k)
	if i < 0 {
		return ""
	}
	rest := s[i+len(k):]
	return strings.Fields(rest)[0]
}

func filterEnv() []string {
	var out []string
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "NOVA") || strings.HasPrefix(e, "OPENCODE") {
			out = append(out, e)
		}
	}
	return out
}
```


## Stumbles

The PATHS in each proposed card are guesses; I did not read the source. Read beyond README and help: `git tag`, `CHANGELOG.md` headings (to learn why
v1.0.0 lacked nova-friend); the checkout's `AGENTS.md` and `docs/` were not
read during the run.

### 1. README's install line cannot install nova-friend

What I read: the README, "Try one on a small example": `go install .../cmd/nova-memory@v1.0.0`.
Expected: the same line with `nova-friend` installs it. Happened: v1.0.0 and
@latest contain no `cmd/nova-friend`; the card names v1.2.0, the checkout
builds `v1.0.1-0.2026...`. README says "These are the Nova Tools 1.0.0 commands"
while its table lists nova-friend, so the table promises a tool the
install line cannot give.
card: readme-install-names-a-release-with-the-tool
PATHS: README.md,internal/docs/readme_catalogue_test.go
Task: say which release carries each tool in the README, or point the install line at one that does.

### 2. The README gives no way to install without the network

What I read: README install line, `go install` (`GOPROXY=off` in the container).
Expected: a build that works inside a no-network container from a checkout.
Happened: `go install ./cmd/nova-friend` offline fails on modules
(golang.org/x/mod v0.41.0, go-internal v1.16.0) that the functional image's
module cache lacks; I built on the host and copied the binaries in.
card: functional-image-carries-the-module-cache
PATHS: infra/functional-image/Containerfile,infra/functional-image/README.md
Task: make the functional image's module cache hold every module go.mod requires, so a checkout builds offline.

### 3. Nothing says what a fake harness is or how the daemon calls one

What I read: `nova-friend help`, `help run`, `run -h`, `install` help.
Expected: the contract of a harness (the command run, the prompt, what it must
write back). Happened: the help names fixed harnesses only; the way in is to
put a program named after one of them (`opencode`) on PATH and learn its calls by
logging them (`opencode session list --format json`, `opencode run --session <id>
--dir <dir> "<prompt>"`). No help line shows a harness that is a program you
write, and the JSON shape of `session list` is not given (my guess was refused).
card: friend-help-shows-a-harness-you-write
PATHS: cmd/nova-friend/main.go,docs/CLI.md
Task: add to `nova-friend help run` the calls a harness gets (argv, prompt, the pong command) and a minimal Go example.

### 4. The help does not say a job needs both ends proven

What I read: `nova-bus help`, the `SEND REFUSED` lines.
Expected: sending one message after naming ada and bob. Happened, in order:
unknown names; `nova-config apply` refused for fleet endpoints; refused for no
coordinator machine; the sender ada deaf. Each refusal says the next command,
which worked, but the chain (friend rows, machine row, fleet set with a made-up
pg_dsn, apply, a daemon for the sender too) is nowhere in the help as a whole,
and the README's `nova-bus send` row says only "a Redis whose nova-config rows name the sender and the recipient".
card: bus-first-run-names-the-whole-chain
PATHS: cmd/nova-bus/main.go
Task: put the full first-run chain (nova-config rows, apply, both daemons proven) in `nova-bus help`.

### 5. The SESSION CHECK prompt can omit `--to`

What I read: the prompts the daemon sent the fake.
Expected: each prompt's pong command answers. Happened: the first prompt ends
`--to bob`, a later one did not and `nova-friend pong` refused it
(`--to is required: no ping has named a seat yet`); a session following the
prompt word for word fails the check.
card: session-check-prompt-names-the-seat
PATHS: internal/friend/session_check.go
Task: always put `--to` in the pong command of a SESSION CHECK prompt.

### 6. One-shot mode and the job report are not reachable without a sprint server

What I read: `help run` (one-shot lanes, QUEUE.json, "the REPORT.md and RESULT.md to write").
Expected: a bus message delivered to one-shot bob appears as a card with report paths.
Happened: the daemon asks a sprint server at 127.0.0.1:6390 for cards, which
the README row for nova-friend does not mention; with none it prints
`inbox: the server did not say which cards are on her row` and `lane 1: no session ... exit 125`.
I fell back to batch mode; the report path was my own convention in the message body.
card: friend-help-says-one-shot-needs-the-sprint-server
PATHS: cmd/nova-friend/main.go
Task: say in `help run` that one-shot lanes need the sprint server and that batch delivers the bus message as the prompt.

### 7. The mode line reads backwards

What I read: `nova-config friend add -h` (`--mode`), then the daemon's output.
Expected: one line naming the mode in force and why. Happened: bob was added
with `--mode one-shot` and run with `--mode one-shot`, and the daemon printed
`mode: one-shot, from batch (the friend row)`; I could not tell whether the row
or the flag had won.
card: friend-mode-line-names-its-winner
PATHS: internal/friend/run.go
Task: print which of the flag and the row set the mode, in one line.

## Verdict

could a stranger do it: no.

A stranger from README and help alone does not get there: the install line
cannot give nova-friend, the harness contract is not in the help, and the
bus needs a chain of config rows nobody lists in one place. With the stumbles
read past, the job went over nova-bus to a fake Go harness and its report came
back in under 10 minutes by the bench clock (container started 03:20Z, job-2
report 03:23Z), most of it spent learning how the daemon calls a harness by
logging its calls; the install detour and reading came on top.
