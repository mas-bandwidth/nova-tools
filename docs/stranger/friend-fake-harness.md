# Stranger run: nova-friend with a fake harness

A cold run by a bud (rowan-space, Claude Sonnet 5.5 under Claude Code) who knew
`README.md` and the tools' own help, and nothing else. The goal: run a
`nova-friend` for a made-up friend whose harness is a fake written in Go,
deliver one job over `nova-bus` on a throwaway Redis inside one container, and
see the report come back. Every stumble below is a moment of guessing,
retrying, reading something other than README and help, or being misled, and
carries a proposed card for the coordinator.

What was read besides README and help, in order, all named again where they
matter: `infra/functional-image/Containerfile` and its `README.md` (the brief
names that image; needed to build and start the container), and `git tag` and
`git ls-remote --tags` on the checkout (to learn why the install failed).
Nothing else: no source, no spec, no `docs/CLI.md`.

## Setup

- Bench: vision (Linux, podman 5.7.0), never the Studio. One container for the
  whole run: `podman run -d --rm --name stranger-friend-r2 --init --timeout 14400
  --network none -v <bench>/work:/work -w /work nova-functional sleep 14400`, the
  image built from `infra/functional-image` (`podman build -t nova-functional`,
  4m26s). Inside it: go1.26.6, Redis server v=8.10.2, user `bench` (uid 10001).
  The container is stopped and `~/nova-bench/stranger/<job>/` removed at the end.
- Checkout: mas-bandwidth/nova-tools at `sprint/mechanical-2026-10-02`, commit
  `6999ddcfe9ef16811164853c5a09b77b70d91d96` (`git describe`:
  `v1.0.0-6213-g6999ddcfe`).
- Versions printed by `<tool> version`: `nova-friend devel linux/amd64 go1.26.6`
  and `nova-bus devel linux/amd64 go1.26.6`. `nova-config` was built the same
  way and its `version` was not run.
- Tools: README's `go install ...@v1.0.0` does not work for this tool or in this
  container (stumbles 1 and 2), so the three binaries were built from the
  checkout in short networked containers (`GOPROXY=https://proxy.golang.org`,
  `GOMODCACHE` in the work dir) and run in the offline one.
- Redis: `redis-server --bind 127.0.0.1 --port 6379 --save "" --appendonly no
  --daemonize yes` on loopback inside the container, as the bus help says.
- The fake harness, `/work/fake/main.go`, built as `/work/bin/opencode` (the
  name the daemon runs; stumble 3). It was written into the container through
  `podman exec -i ... sh -c 'cat > /work/fake/main.go'` because the mounted work
  dir was not writable from the host side (stumble 8); those writes are not in
  the transcript, and two of its edits were lost to a `pkill -f` of mine that
  matched its own shell (nothing ran past the `pkill`; the commands were run
  again and are the ones shown). The source of the last version:

```go
package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

func logf(format string, a ...any) {
	f, _ := os.OpenFile("/work/fake.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	fmt.Fprintf(f, "%s "+format+"\n", append([]any{time.Now().UTC().Format(time.RFC3339)}, a...)...)
	f.Close()
}

func main() {
	args := os.Args[1:]
	logf("ARGS %q", args)
	if len(args) >= 2 && args[0] == "session" && args[1] == "list" {
		wd, _ := os.Getwd()
		fmt.Printf(`[{"id":"ses_fake1","directory":%q,"title":"fake","updated":1}]`+"\n", wd)
		return
	}
	if len(args) >= 1 && args[0] == "run" {
		prompt := args[len(args)-1]
		if strings.HasPrefix(prompt, "RECV OK") && strings.Contains(prompt, `subject="job:`) {
			report(prompt)
		}
		const mark = "end this turn: "
		if i := strings.Index(prompt, mark); i >= 0 && strings.Contains(prompt, "SESSION CHECK") {
			cmd := strings.Fields(strings.TrimSpace(prompt[i+len(mark):]))
			if !strings.Contains(strings.Join(cmd, " "), "--to") {
				cmd = append(cmd, "--to", "ada")
			}
			out, err := exec.Command(cmd[0], cmd[1:]...).CombinedOutput()
			logf("PONG err=%v out=%q", err, out)
		}
	}
}

// report answers a delivered job with one bus reply from the friend the
// job was sent to, quoting the job's id as --re.
func report(prompt string) {
	field := func(k string) string {
		for _, w := range strings.Fields(strings.SplitN(prompt, "\n", 2)[0]) {
			if strings.HasPrefix(w, k+"=") {
				return strings.TrimPrefix(w, k+"=")
			}
		}
		return ""
	}
	body := "hello (report for job " + field("id") + ")"
	out, err := exec.Command("nova-bus", "send", "--redis", "127.0.0.1:6379", "--as", field("to"), "--to", field("from"),
		"--re", field("id"), "--subject", "report: say hello", "--body", body).CombinedOutput()
	logf("REPORT err=%v out=%q", err, out)
}
```

## Transcript

Every command run in the container, with its output exactly as printed. Lines
starting `#` are notes of mine. Trimmed only where marked `[trimmed: ...]`. The
first daemons run a logging-only fake, then the final fake; the job and its
report are the last commands.

```text
$ go install github.com/mas-bandwidth/nova-tools/cmd/nova-friend@v1.0.0; echo exit=$?
go: github.com/mas-bandwidth/nova-tools/cmd/nova-friend@v1.0.0: module lookup disabled by GOPROXY=off
exit=1

# [outside the run container, one short networked container, GOPROXY=https://proxy.golang.org, GOBIN=/work/bin]
$ go install github.com/mas-bandwidth/nova-tools/cmd/nova-friend@v1.0.0 .../nova-bus@v1.0.0
go: downloading github.com/mas-bandwidth/nova-tools v1.0.0
go: github.com/mas-bandwidth/nova-tools/cmd/nova-friend@v1.0.0: module github.com/mas-bandwidth/nova-tools@v1.0.0 found, but does not contain package github.com/mas-bandwidth/nova-tools/cmd/nova-friend
exit=1
ls: cannot access '/work/bin': No such file or directory

$ export PATH=/work/bin:$PATH; nova-friend version; nova-bus version
bash: line 1: nova-friend: command not found
bash: line 1: nova-bus: command not found

# [outside the run container: v1.0.0 has no nova-friend and no v1.2.0 tag exists; build both from the branch-tip checkout in one short networked container]
$ go build -o /work/bin/ ./cmd/nova-friend ./cmd/nova-bus
go: could not create module cache: mkdir /work/gomod: permission denied
exit=1
ls: cannot access '/work/bin': No such file or directory

$ export PATH=/work/bin:$PATH; nova-friend version; nova-bus version
bash: line 1: nova-friend: command not found
bash: line 1: nova-bus: command not found

$ chmod -R a+rwX work   # on the bench host; the mounted work dir was not writable by the container's bench user
$ go build -o /work/bin/ ./cmd/nova-friend ./cmd/nova-bus   # same short networked container as above, with GOMODCACHE=/work/gomod
go: downloading go.uber.org/atomic v1.11.0
go: downloading github.com/cespare/xxhash/v2 v2.3.0
exit=0
nova-bus
nova-friend

$ export PATH=/work/bin:$PATH; nova-friend version; nova-bus version
nova-friend devel linux/amd64 go1.26.6
nova-bus devel linux/amd64 go1.26.6

$ export PATH=/work/bin:$PATH; nova-friend help
nova-friend: what a friend runs to be part of the team: the wake loop, the beat, and the proof of life, as one daemon

how it works: one launchd agent per friend (install) runs the daemon (run): it parks on the friend's
nova-bus stream and, when the session is free, pushes every waiting message in as one turn (the
harness's deliver command), beats to the sprint server while the session answers, answers the
coordinator PING at once (daemon-pong, never presence); presence is the session's answer to a nonce.
state: ~/.nova-friend/<me>/ (or --state-dir), the queue: <dir>/inbox/QUEUE.json.

usage:
  nova-friend run --as <me> --harness <h> --dir <d> [--session <id>] [--server <addr>] [--width <n>] [--silent-stop <d>] [--broken-after <n>] [--coordinator <seat>] [--state-dir <d>] [--redis <addr>] [--profile <p>] [--config-dir <d>] [--deny-self <d,...>] [--wall-jobs <d,...>] [--wall-reads <d,...>] [--dry-run]
  nova-friend install --as <me> --harness <h> --dir <d> [--session <id>] [--server <addr>] [--width <n>] [--silent-stop <d>] [--broken-after <n>] [--coordinator <seat>] [--state-dir <d>] [--redis <addr>] [--config-dir <d>] [--model <provider/model>] [--secrets NAME[,NAME] --seat <seat>] [--launchd-log <file>] [--dry-run]
  nova-friend uninstall --as <me> [--dry-run]
  nova-friend check [--as <coordinator>] [<friend>...] [--since <duration>] [--shown <file|->] [--json]
  nova-friend ping --as <coordinator> --to <friend> [--nonce <n>] [--since <RFC3339>] [--wake] [--redis <addr>] [--dry-run]
  nova-friend pong --as <me> --nonce <n> [--to <coordinator>] [--queue <n>] [--working <n>] [--width <n>] [--state-dir <d>] [--redis <addr>] [--dry-run]
  nova-friend wait-pong --from <friend> --nonce <n> [--timeout <d>] [--redis <addr>]
  nova-friend status --as <me> --dir <d> [--state-dir <d>]
  nova-friend serve --as <coordinator> [--redis <addr>] [--dry-run]
  nova-friend version
  nova-friend help [<verb>]

Every verb but run, serve takes --json: the same result as one JSON object on stdout. A verb that lists takes --max <n> (default 20, 0 lists all) and says MORE for the rest. `<verb> -h` lists a verb's flags.

exit codes: 0 done, 1 the verb ran and said no (wait-pong: no pong in time; status: no daemon; check: the session did not answer), 2 could not run (a flag, an input, a store or a server that did not answer).

example:
  nova-friend install --as bob --harness opencode --dir ./bob --dry-run
  nova-friend uninstall --as bob --dry-run
  nova-friend ping --as ada --to bob --nonce abc123
  nova-friend pong --as bob --nonce abc123 --to ada --queue 2 --working 1 --width 4
  nova-friend wait-pong --from bob --nonce abc123 --timeout 2s
  nova-friend status --as bob --dir ./bob

$ export PATH=/work/bin:$PATH; nova-friend run -h; nova-friend help run
usage: nova-friend run [flags]
from `nova-friend help`:
  nova-friend run --as <me> --harness <h> --dir <d> [--session <id>] [--server <addr>] [--width <n>] [--silent-stop <d>] [--broken-after <n>] [--coordinator <seat>] [--state-dir <d>] [--redis <addr>] [--profile <p>] [--config-dir <d>] [--deny-self <d,...>] [--wall-jobs <d,...>] [--wall-reads <d,...>] [--dry-run]
[trimmed: 132 lines; `run -h` and `help run` print the same ~65 lines of paragraph and the flag list, one after the other; its flag list and exit codes are in the stumbles below where they matter]

$ export PATH=/work/bin:$PATH; mkdir -p /work/ada && nova-friend run --as ada --harness opencode --dir /work/ada --redis 127.0.0.1:6379 --dry-run; echo exit=$?; nova-friend run --as ada --harness fake --dir /work/ada --dry-run; echo exit=$?; nova-friend install -h 2>&1 | tail -25
RUN DRY-RUN as=ada harness=opencode dir=/work/ada state=/home/bench/.nova-friend/ada redis=127.0.0.1:6379; nothing was started
exit=0
RUN REFUSED: --harness "fake" is no harness; it wants one of opencode, codex, claude, antigravity, dsh, gemini, grok, copilot, cursor, amp, goose, kiro, cline, aider, roo, windsurf, zed, warp; run: nova-friend help
exit=2
NOTE: a CHECK FAIL is a NOTE, except a session the harness cannot drive (dsh under an agent preset), which
is refused at exit 2 with its remedy and the agent booted out again. A harness with no deliver command
(claude, the surveyed ones) is refused before anything is written, the adapter card its remedy.
flags:
  --as <string>  your name, a nova-config friend row (required)
  --broken-after <int>  turns in a row the provider refuses the same way before the session is broken
  --config-dir <string>  harness claude: the friend's own config directory, made and named in the agent (default: CLAUDE_CONFIG_DIR)
  --coordinator <string>  who is told of a broken session when no ping has named the seat
  --dir <string>  the friend's working directory: the session's, and where the state files live (required)
  --dry-run  print what the verb would write and write nothing
  --harness <string>  the harness the session runs in: opencode, codex, claude, antigravity, dsh, gemini, grok, copilot, cursor, amp, goose, kiro, cline, aider, roo, windsurf, zed, warp (required)
  --json  print the result as one JSON object instead of lines
  --launchd-log <string>  launchd's stdout and stderr file (default: ~/Library/Logs/nova-friend-<me>.log)
  --model <string>  harness opencode: the model, provider/model, written into <dir>/opencode.json (default: left as it is)
  --redis <string>  the bus store's Redis address, host:port (default: NOVA_BUS_REDIS)
  --seat <string>  the machine's nova-secrets seat the secrets are opened as (nova-config machine show <self>: seat); wanted with --secrets
  --secrets <string>  the names of the secrets the session needs, comma-separated (never values); wraps the daemon in nova-secrets exec
  --server <string>  the sprint server, host:port (default: NOVA_SPRINT_SERVER, else 127.0.0.1:6390)
  --session <string>  the session to deliver into (default: the harness's newest session in --dir)
  --silent-stop <duration>  stop a turn that has printed nothing for this long; a turn that prints runs on
  --state-dir <string>  where the state files live (default: ~/.nova-friend/<me>)
  --width <int>  the friend's width, from the nova-config friend row; 0 is unknown
  --within <duration>  how long the delivery check after loading waits for the session's pong
exit codes: 0 done, 1 the verb ran and said no (wait-pong: no pong in time; status: no daemon; check: the session did not answer), 2 could not run (a flag, an input, a store or a server that did not answer).
effect: local write: writes files on this machine: writes the harness's settings and the launchd agent com.nova.friend-<me>, and loads it

$ export PATH=/work/bin:$PATH; nova-bus help
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

$ export PATH=/work/bin:$PATH; (redis-server --bind 127.0.0.1 --port 6379 --save "" --appendonly no --daemonize yes --dir /work >/dev/null); sleep 1; redis-cli ping; nova-bus names; echo exit=$?; nova-bus send --redis 127.0.0.1:6379 --as ada --to bob --subject hello --body "are you there?"; echo exit=$?
PONG
NAMES REFUSED: --redis is required: NOVA_BUS_REDIS is unset, and with no NOVA_SPRINT_REDIS the fleet's bus row (nova-config fleet set --bus <host:port>, then apply) cannot be read either; refusing to guess; run: nova-bus help
exit=2
SEND REFUSED: ada is no known name; the names are nova-config's friend and machine rows (nova-bus names lists them); add one with nova-config friend add ada --slots 1 --tiers flash --as <you>, then nova-config apply; run: nova-bus help
SEND REFUSED: bob is no known name; the names are nova-config's friend and machine rows (nova-bus names lists them); add one with nova-config friend add bob --slots 1 --tiers flash --as <you>, then nova-config apply; run: nova-bus help
exit=2

# [outside the run container, networked: go build -o /work/bin/ ./cmd/nova-config]
go: downloading github.com/jackc/pgpassfile v1.0.0
go: downloading golang.org/x/sync v0.23.0
exit=0

$ export PATH=/work/bin:$PATH; nova-config help
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
  nova-config apply [--pg <dsn> | --file <path>] [--redis <addr>] [--as <name>]
                    [--kind <kind>] [--dry-run] [--json]
  nova-config inventory [--redis <addr> | --fixture <file>] [--list | --host <name>]
                        [--timeout <duration>]
  nova-config <kind> add <name> --<field> <value> ... --as <name> [--dry-run] [--json]
  nova-config <kind> set <name> --<field> <value> ... --as <name> [--dry-run] [--json]
[trimmed: 78 lines; the rest of the usage, the store paragraph, the kinds list and the example block of `nova-config help`]

$ export PATH=/work/bin:$PATH; cd /work; R=127.0.0.1:6379; nova-config migrate --file try.json; nova-config friend add ada --slots 1 --tiers flash --as ada --file try.json; nova-config friend add bob --slots 1 --tiers flash --as ada --file try.json; nova-config apply --file try.json --redis $R --as ada; echo exit=$?; nova-bus names --redis $R
CONFIG MIGRATE file=try.json from=0 to=33 applied=33
CONFIG ADD kind=friend name=ada rev=1
CONFIG ADD kind=friend name=bob rev=2
nova-config apply REFUSED: fleet: endpoints are unset: redis_port, pg_dsn; run: nova-config fleet set --redis_port <port> --pg_dsn <dsn> --as <actor>, then nova-config apply --kind fleet --as <actor>
exit=1
NAMES OK count=0 proven=0

$ export PATH=/work/bin:$PATH; cd /work; R=127.0.0.1:6379; nova-config fleet set --redis_port 6379 --pg_dsn postgres://nobody@127.0.0.1:5432/none --as ada --file try.json; nova-config apply --file try.json --redis $R --as ada; echo exit=$?; nova-bus names --redis $R
CONFIG SET kind=fleet name=fleet rev=3 changed=pg_dsn,redis_port
CONFIG APPLY kind=machine add=0 set=0 remove=0 rev=0 ms=0
APPLY SET kind=fleet name=fleet changed=redis_port,pg_dsn,loops_dir
CONFIG APPLY kind=fleet add=0 set=1 remove=0 rev=3 ms=8
APPLY ADD kind=friend name=ada
nova-config apply REFUSED: friend ada has no beat naming a machine and the fleet names no coordinator machine to charge her slots to; run: nova-config fleet set --coordinator <machine>, then apply; run: nova-config apply --dry-run
exit=1
NAMES OK count=0 proven=0

$ export PATH=/work/bin:$PATH; cd /work; R=127.0.0.1:6379; nova-config machine add m1 --user bench --seat s1 --slots 8 --width 4 --as ada --file try.json; nova-config fleet set --coordinator m1 --as ada --file try.json; nova-config apply --file try.json --redis $R --as ada; echo exit=$?; nova-bus names --redis $R
CONFIG ADD kind=machine name=m1 rev=4
CONFIG SET kind=fleet name=fleet rev=5 changed=coordinator
APPLY ADD kind=machine name=m1
CONFIG APPLY kind=machine add=1 set=0 remove=0 rev=4 ms=1
APPLY SET kind=fleet name=fleet changed=coordinator
CONFIG APPLY kind=fleet add=0 set=1 remove=0 rev=5 ms=0
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
exit=0
NAMES OK count=3 proven=0
NAMES NAME name=ada push=none age=never harness=-
NAMES NAME name=bob push=none age=never harness=-
NAMES NAME name=m1 push=none age=never harness=-

$ mkdir -p /work/fake && cd /work/fake && cat > go.mod <<EOF
module fake

go 1.26
EOF
cat > main.go <<EOF
package main

import (
	"fmt"
	"io"
	"os"
	"time"
)

func main() {
	in, _ := io.ReadAll(os.Stdin)
	f, _ := os.OpenFile("/work/fake.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	fmt.Fprintf(f, "%s ARGS %q\nCWD %v\nSTDIN %q\n---\n", time.Now().UTC().Format(time.RFC3339), os.Args, func() string { d, _ := os.Getwd(); return d }(), in)
	f.Close()
}
EOF
go build -o /work/bin/opencode . && ls -l /work/bin/opencode
-rwxr-xr-x 1 bench bench 2579953 Oct  6 03:14 /work/bin/opencode

$ export PATH=/work/bin:$PATH; cd /work; (nohup nova-friend run --as ada --harness opencode --dir /work/ada --redis 127.0.0.1:6379 --state-dir /work/state-ada > /work/ada.out 2>&1 &) ; sleep 6; cat /work/ada.out; echo ---; cat /work/fake.log; nova-friend status --as ada --dir /work/ada --state-dir /work/state-ada; echo exit=$?
RUN REFUSED: no push proof: CHECK FAIL harness=opencode stage=deliver why="opencode session list: not a JSON list: unexpected end of JSON input"; the daemon did not start; run: open the friend's opencode session in /work/ada, then prove it answers: nova-friend check --as ada --harness opencode --dir /work/ada
---
2026-10-06T03:14:59Z ARGS ["opencode" "session" "list" "--format" "json"]
CWD /work/ada
STDIN ""
---
STATUS NONE: no daemon has run as ada (no status file in /work/state-ada); run: nova-friend install --as ada --harness <h> --dir /work/ada
exit=1

$ cd /work/fake && python3 - <<EOF
import re
s=open("main.go").read()
s=s.replace("\tf.Close()\n","\tf.Close()\n\tif len(os.Args) > 2 && os.Args[1] == \"session\" && os.Args[2] == \"list\" {\n\t\tfmt.Println(\`[{\"id\":\"ses_fake1\",\"directory\":\"/work/ada\",\"title\":\"fake\",\"updated\":1}]\`)\n\t}\n")
open("main.go","w").write(s)
EOF
go build -o /work/bin/opencode . && export PATH=/work/bin:$PATH && cd /work && : > fake.log && (nohup nova-friend run --as ada --harness opencode --dir /work/ada --redis 127.0.0.1:6379 --state-dir /work/state-ada > /work/ada.out 2>&1 &) ; sleep 8; cat /work/ada.out; echo ---; cat /work/fake.log
---
2026-10-06T03:15:11Z ARGS ["opencode" "session" "list" "--format" "json"]
CWD /work/ada
STDIN ""
---
2026-10-06T03:15:11Z ARGS ["opencode" "run" "--session" "ses_fake1" "--dir" "/work/ada" "SESSION CHECK uaf4eh\nThe daemon has had no bus message from this session for a while and asks whether the session is alive; only an answer from inside this session counts, and none within 5m0s puts you down. Answer now, before anything else, with one command, then end this turn: /work/bin/nova-friend pong --as ada --nonce uaf4eh --state-dir /work/state-ada --redis 127.0.0.1:6379 --to ada\n"]
CWD /work/ada
STDIN ""
---

$ pkill -f "[n]ova-friend run --as ada"; echo killed=$?; ps -eo pid,args | grep [n]ova-friend
killed=1

$ cd /work/fake && go build -o /work/bin/opencode . && echo built; export PATH=/work/bin:$PATH; cd /work; : > fake.log; (nohup nova-friend run --as ada --harness opencode --dir /work/ada --redis 127.0.0.1:6379 --state-dir /work/state-ada > /work/ada.out 2>&1 &); sleep 8; cat /work/ada.out; echo ---; cat fake.log | cut -c1-300; nova-friend status --as ada --dir /work/ada --state-dir /work/state-ada
built
RUN 2026-10-06T03:15:40Z push proof: CHECK OK harness=opencode took=49.874872ms
RUN 2026-10-06T03:15:40Z inbox: the server did not say which cards are on her row: the sprint server at 127.0.0.1:6390 did not answer: Post "http://127.0.0.1:6390/verbs": dial tcp 127.0.0.1:6390: connect: connection refused; nothing written or retired until it does
RUN 2026-10-06T03:15:40Z harness check: cannot tell: opencode runs no standing process (each turn starts /work/bin/opencode); the session check alone
RUN 2026-10-06T03:15:40Z presence: a session check is owed; it waits for the turn under way
RUN 2026-10-06T03:15:40Z push proof: down: no session answer yet; nova-bus refuses ada as deaf
RUN 2026-10-06T03:15:40Z subject="pong" messages=1 took=31ms exit=0 acked=true
RUN 2026-10-06T03:15:41Z presence: session check 1buhlp into the session
---
2026-10-06T03:15:40Z ARGS ["session" "list" "--format" "json"]
2026-10-06T03:15:40Z ARGS ["run" "--session" "ses_fake1" "--dir" "/work/ada" "SESSION CHECK r8i3mn\nThe daemon has had no bus message from this session for a while and asks whether the session is alive; only an answer from inside this session counts, and none within 5m0s puts you down. Answer now, b
2026-10-06T03:15:40Z PONG err=<nil> out="PONG OK nonce=r8i3mn to=ada id=01M47KBD81V560NP0CVY70GPDP at=2026-10-06T03:15:40Z\n"
2026-10-06T03:15:40Z ARGS ["session" "list" "--format" "json"]
2026-10-06T03:15:40Z ARGS ["run" "--session" "ses_fake1" "--dir" "/work/ada" "RECV OK id=01M47KBD81V560NP0CVY70GPDP from=ada to=ada cc=- re=- at=2026-10-06T03:15:40Z subject=\"pong\"\n\npong r8i3mn queue=0 working=0 width=0\n"]
2026-10-06T03:15:41Z ARGS ["session" "list" "--format" "json"]
2026-10-06T03:15:41Z ARGS ["run" "--session" "ses_fake1" "--dir" "/work/ada" "SESSION CHECK 1buhlp\nThe daemon has had no bus message from this session for a while and asks whether the session is alive; only an answer from inside this session counts, and none within 5m0s puts you down. Answer now, b
2026-10-06T03:15:41Z PONG err=exit status 2 out="PONG REFUSED: --to is required: no ping has named a seat yet (no status file in /work/state-ada); it wants the coordinator's name; run: nova-friend help\n"
STATUS OK daemon=up harness=opencode status_age=3s connection=connected seat=- last_ping=2026-10-06T03:15:40Z ping_age=8s challenge=quiet nonce=- last_pong=2026-10-06T03:15:40Z session_pong_age=8s daemon_pong_age=- pongs=0 queue=0 working=0 width=1 beats=0 last_beat=- delivered=1 session=ok mode=batch held=- inbox=- missing=- presence=down last_session=- status=up presence_reason="no session answer yet" why="session answer 8s" evidence="harness unknown; session answer 8s; no limit; undelivered not counted; last result 8s exit=0"
STATUS NOTE the inbox: the sprint server at 127.0.0.1:6390 did not answer: Post "http://127.0.0.1:6390/verbs": dial tcp 127.0.0.1:6390: connect: connection refused
STATUS NOTE the daemon is up and the session is not (no session answer yet): the friend is down, and no beat goes to the sprint server until the session answers a session check
STATUS NOTE the last beat failed: not beating: the session is down (no session answer yet); the daemon answering is not the session

$ export PATH=/work/bin:$PATH; cd /work; : > fake.log; nova-bus send --redis 127.0.0.1:6379 --as bob --to ada --subject "job: say hello" --body "Write hello to your outbox."; echo exit=$?; sleep 4; cat fake.log; echo ---; tail -3 ada.out; nova-bus peek --redis 127.0.0.1:6379 --as bob; nova-bus log --redis 127.0.0.1:6379 --bodies
SEND REFUSED: deaf: bob has no proven push since never: no daemon has recorded one; the remedy: bob runs its friend daemon with a deliver adapter for its harness (nova-friend install --as bob --harness <h> --dir <d>) and its session answers the daemon's SESSION CHECK, which records the proof; nova-bus names shows every name's push; run: nova-bus help
SEND REFUSED: deaf: ada has no proven push since 12s: its daemon holds no answered SESSION CHECK from the session (no session answer yet); the remedy: ada runs its friend daemon with a deliver adapter for its harness (nova-friend install --as ada --harness <h> --dir <d>) and its session answers the daemon's SESSION CHECK, which records the proof; nova-bus names shows every name's push; run: nova-bus help
exit=2
---
RUN 2026-10-06T03:15:40Z push proof: down: no session answer yet; nova-bus refuses ada as deaf
RUN 2026-10-06T03:15:40Z subject="pong" messages=1 took=31ms exit=0 acked=true
RUN 2026-10-06T03:15:41Z presence: session check 1buhlp into the session
PEEK OK pending=0 new=0
LOG OK total=1
LOG MESSAGE id=01M47KBD81V560NP0CVY70GPDP from=ada to=ada cc=- re=- at=2026-10-06T03:15:40Z subject="pong" body="pong r8i3mn queue=0 working=0 width=0\n"

$ cd /work/fake && go build -o /work/bin/opencode . && echo built; export PATH=/work/bin:$PATH; cd /work; mkdir -p bob; : > fake.log; (nohup nova-friend run --as bob --harness opencode --dir /work/bob --redis 127.0.0.1:6379 --state-dir /work/state-bob > /work/bob.out 2>&1 &); sleep 8; nova-bus names --redis 127.0.0.1:6379
built
NAMES OK count=3 proven=1
NAMES NAME name=ada push=down age=31s harness=opencode
NAMES NAME name=bob push=proven age=5s harness=opencode
NAMES NAME name=m1 push=none age=never harness=-

$ ps -eo pid,args | grep "^ *[0-9]* nova-friend"
   2316 nova-friend run --as bob --harness opencode --dir /work/bob --redis 127.0.0.1:6379 --state-dir /work/state-bob

$ export PATH=/work/bin:$PATH; cd /work; : > fake.log; (nohup nova-friend run --as ada --harness opencode --dir /work/ada --redis 127.0.0.1:6379 --state-dir /work/state-ada > /work/ada.out 2>&1 &); sleep 6; nova-bus names --redis 127.0.0.1:6379
NAMES OK count=3 proven=2
NAMES NAME name=ada push=proven age=4s harness=opencode
NAMES NAME name=bob push=proven age=22s harness=opencode
NAMES NAME name=m1 push=none age=never harness=-

$ export PATH=/work/bin:$PATH; cd /work; : > fake.log; nova-bus send --redis 127.0.0.1:6379 --as bob --to ada --subject "job: say hello" --body "Write hello to your outbox."; echo exit=$?; sleep 4; cat fake.log; echo ---; tail -3 ada.out; nova-bus log --redis 127.0.0.1:6379 --bodies --max 3
SEND OK id=01M47KCYKX3G38ME4BWAEDNSGB to=ada cc=- at=2026-10-06T03:16:30Z bytes=27 sha256=43b02a3da1a99b6bb6393d991228c353ad383c7622b7024055918c9149e511cc login=none
exit=0
2026-10-06T03:16:30Z ARGS ["session" "list" "--format" "json"]
2026-10-06T03:16:30Z ARGS ["run" "--session" "ses_fake1" "--dir" "/work/ada" "RECV OK id=01M47KCYKX3G38ME4BWAEDNSGB from=bob to=ada cc=- re=- at=2026-10-06T03:16:30Z subject=\"job: say hello\"\n\nWrite hello to your outbox.\n"]
---
RUN 2026-10-06T03:16:23Z push proof: up: the session answered klfv4o through opencode's deliver adapter; nova-bus hears ada
RUN 2026-10-06T03:16:23Z subject="pong" messages=1 took=14ms exit=0 acked=true
RUN 2026-10-06T03:16:30Z subject="job: say hello" messages=1 took=31ms exit=0 acked=true
LOG OK total=6
LOG MESSAGE id=01M47KBD81V560NP0CVY70GPDP from=ada to=ada cc=- re=- at=2026-10-06T03:15:40Z subject="pong" body="pong r8i3mn queue=0 working=0 width=0\n"
LOG MESSAGE id=01M47KC4KFFMRV531WK9HYYPDM from=bob to=bob cc=- re=- at=2026-10-06T03:16:04Z subject="pong" body="pong n8xwwc queue=0 working=0 width=0\n"
LOG MESSAGE id=01M47KC5M0AS2JDEC6F4TXRC1M from=bob to=ada cc=- re=- at=2026-10-06T03:16:05Z subject="pong" body="pong bfyq0x queue=0 working=0 width=0\n"
LOG MORE kind=message shown=3 total=6 --max <n> raises the ceiling, --max 0 lists all

$ export PATH=/work/bin:$PATH; cd /work/fake && gofmt -l . ; go vet ./... && go build -o /work/bin/opencode . && echo built; cd /work; : > fake.log; nova-bus send --redis 127.0.0.1:6379 --as bob --to ada --subject "job: say hello" --body "Write hello to your outbox."; sleep 5; cat fake.log | cut -c1-400; nova-bus peek --redis 127.0.0.1:6379 --as bob; tail -4 bob.out
built
SEND OK id=01M47KDCFHS3G3P4Y3V4BE82K5 to=ada cc=- at=2026-10-06T03:16:45Z bytes=27 sha256=43b02a3da1a99b6bb6393d991228c353ad383c7622b7024055918c9149e511cc login=none
2026-10-06T03:16:45Z ARGS ["session" "list" "--format" "json"]
2026-10-06T03:16:45Z ARGS ["run" "--session" "ses_fake1" "--dir" "/work/ada" "RECV OK id=01M47KDCFHS3G3P4Y3V4BE82K5 from=bob to=ada cc=- re=- at=2026-10-06T03:16:45Z subject=\"job: say hello\"\n\nWrite hello to your outbox.\n"]
2026-10-06T03:16:45Z REPORT err=<nil> out="SEND OK id=01M47KDCG0KWYF9Y2AMWZDTXAX to=bob cc=- at=2026-10-06T03:16:45Z bytes=49 sha256=b2d2ea48afd3b05cfac23ac61159ebde28ab04fe47a28502351f9d8847a17217 login=none\n"
2026-10-06T03:16:45Z ARGS ["session" "list" "--format" "json"]
2026-10-06T03:16:45Z ARGS ["run" "--session" "ses_fake1" "--dir" "/work/bob" "RECV OK id=01M47KDCG0KWYF9Y2AMWZDTXAX from=ada to=bob cc=- re=01M47KDCFHS3G3P4Y3V4BE82K5 at=2026-10-06T03:16:45Z subject=\"report: say hello\"\n\nhello (report for job 01M47KDCFHS3G3P4Y3V4BE82K5)\n"]
PEEK OK pending=0 new=0
RUN 2026-10-06T03:16:05Z presence: session check bfyq0x into the session
RUN 2026-10-06T03:16:06Z presence: up: the session answered bfyq0x
RUN 2026-10-06T03:16:06Z push proof: up: the session answered bfyq0x through opencode's deliver adapter; nova-bus hears bob
RUN 2026-10-06T03:16:45Z subject="report: say hello" messages=1 took=494ms exit=0 acked=true

$ export PATH=/work/bin:$PATH; cd /work; R=127.0.0.1:6379; nova-bus log --redis $R --bodies --max 0 | grep -v pong; nova-friend status --as ada --dir /work/ada --state-dir /work/state-ada | head -1 | cut -c1-260; cat ada.out | tail -3; date -u
LOG OK total=8
LOG MESSAGE id=01M47KCYKX3G38ME4BWAEDNSGB from=bob to=ada cc=- re=- at=2026-10-06T03:16:30Z subject="job: say hello" body="Write hello to your outbox."
LOG MESSAGE id=01M47KDCFHS3G3P4Y3V4BE82K5 from=bob to=ada cc=- re=- at=2026-10-06T03:16:45Z subject="job: say hello" body="Write hello to your outbox."
LOG MESSAGE id=01M47KDCG0KWYF9Y2AMWZDTXAX from=ada to=bob cc=- re=01M47KDCFHS3G3P4Y3V4BE82K5 at=2026-10-06T03:16:45Z subject="report: say hello" body="hello (report for job 01M47KDCFHS3G3P4Y3V4BE82K5)"
STATUS OK daemon=up harness=opencode status_age=4s connection=connected seat=- last_ping=2026-10-06T03:16:22Z ping_age=32s challenge=quiet nonce=- last_pong=2026-10-06T03:16:23Z session_pong_age=31s daemon_pong_age=- pongs=0 queue=0 working=0 width=1 beats=0 l
RUN 2026-10-06T03:16:23Z subject="pong" messages=1 took=14ms exit=0 acked=true
RUN 2026-10-06T03:16:30Z subject="job: say hello" messages=1 took=31ms exit=0 acked=true
RUN 2026-10-06T03:16:45Z subject="job: say hello" messages=1 took=625ms exit=0 acked=true
Tue Oct  6 03:16:54 UTC 2026
```

## Stumbles

### 1. README installs `v1.0.0`, which has no `nova-friend`

- Read: README, "Try one on a small example": `go install github.com/mas-bandwidth/nova-tools/cmd/nova-memory@v1.0.0`, "These are the Nova Tools 1.0.0 commands", and the table row for nova-friend and nova-bus.
- Expected: the same line with `nova-friend` installs it.
- Happened: `module github.com/mas-bandwidth/nova-tools@v1.0.0 found, but does not contain package github.com/mas-bandwidth/nova-tools/cmd/nova-friend`. The brief says v1.2.0; the remote has no tag past `v1.0.0`; the binary built from the branch prints `devel`. Read: `git ls-remote --tags` to see why.
card: readme-install-names-a-release-with-the-tool
- PATHS: README.md
- Task: README's install line either names a release that carries the tool the table row recommends, or says which tools are not in 1.0.0 and how to build them.

### 2. The install line cannot run where a stranger is told to be offline

- Read: README ("build just that tool with Go 1.26.6 or newer") and the brief's `--network none` container, whose image sets `GOPROXY=off`.
- Expected: README's command works in the container the run was given, or says it needs the network.
- Happened: `module lookup disabled by GOPROXY=off`. I built in a second, networked container into the work dir and ran the binaries in the offline one.
card: readme-install-says-it-needs-a-module-proxy
- PATHS: README.md, docs/USAGE.md
- Task: say in the install section that `go install` and `go build` fetch modules, and give the one offline route (a checkout and a filled module cache).

### 3. `nova-friend` help does not say how a fake harness is plugged in

- Read: `nova-friend help`, `nova-friend run -h` (identical to `help run`), `nova-friend install -h`.
- Expected: a line saying what the daemon runs for a harness and what it reads back, so the smallest program that honours it can be written.
- Happened: `--harness` is one of 18 names; `fake` is refused (`RUN REFUSED: --harness "fake" is no harness`). The help never says the daemon runs the harness's own binary by name from PATH, nor its argv, nor the shape it parses. I wrote a fake that logs its argv, called `opencode`, and learned by running: `opencode session list --format json` (it wants a JSON list, `[{"id":"ses_fake1","directory":"<dir>"}]` was accepted; an empty answer is `CHECK FAIL ... not a JSON list`), then `opencode run --session <id> --dir <dir> "<prompt>"` with the whole turn as the last argument; the SESSION CHECK prompt carries the exact `nova-friend pong ...` command to run. Only the dry-run line `harness check: cannot tell: opencode runs no standing process (each turn starts /work/bin/opencode)` hints that PATH is used. A stranger cannot write a harness from the help.
card: friend-help-states-the-deliver-contract
- PATHS: cmd/nova-friend (the help text), docs/CLI.md
- Task: give `nova-friend help run` a short "writing a harness" block: the binary name looked up, the two calls with their argv and the JSON accepted, and the pong command the check prompt carries.

### 4. The help shows no report a delivered job must produce, and the one it names needs a sprint server

- Read: `nova-friend run -h`: lanes "hand one card a turn from <dir>/inbox/QUEUE.json (its BRIEF.md, the REPORT.md and RESULT.md to write, one bus line to send)", only "in one-shot mode"; the daemon prints `inbox: the server did not say which cards are on her row: the sprint server at 127.0.0.1:6390 did not answer ... nothing written or retired until it does`.
- Expected: a way to deliver one job and see the outbox report the help says is written.
- Happened: the default mode is `batch`; a job is just a bus message pushed into the session as one turn (`RECV OK id=... subject="job: say hello"`), and nothing in the help says what the session should write back. I answered with a bus reply (`nova-bus send --re <id>`) from the fake. The one-shot lane's REPORT.md/RESULT.md could not be tried: there is no way in the help to put a card in `QUEUE.json` without a sprint server, which the README says needs a Redis-backed one.
card: friend-help-shows-a-batch-report-and-a-one-shot-card
- PATHS: cmd/nova-friend (the help text), docs/CLI.md
- Task: add to the `run` help one worked batch turn (the line a session receives and the `nova-bus send --re` that answers it), and say how a one-shot card gets into QUEUE.json without a server.

### 5. `nova-bus send` needs a recipe the bus help only hints at

- Read: README's nova-bus row ("Use a separate running Redis instance whose nova-config rows name the sender and the recipient") and `nova-bus help` ("first run: a Redis naming ada and bob").
- Expected: the one setup line for a throwaway Redis.
- Happened: `send` refused (`ada is no known name; ... nova-config friend add ada --slots 1 --tiers flash --as <you>, then nova-config apply`). Then `apply` refused twice more, each naming the next missing piece: `fleet: endpoints are unset: redis_port, pg_dsn` (a Postgres DSN is demanded even with `--file`; I gave a dummy), then `friend ada has no beat naming a machine and the fleet names no coordinator machine` (a machine row and `fleet set --coordinator`). Five commands and three refusals before `nova-bus names` listed anyone. Every refusal named the next command, which is what got me through, but the first-run lines never show the whole recipe. After that `send` refused again as `deaf` for the sender and the recipient until each had a running daemon whose session answered (the bus help does say "no push proven in 10m").
card: bus-first-run-has-a-throwaway-recipe
- PATHS: docs/CLI.md, cmd/nova-bus (the help text)
- Task: `nova-bus help` first run shows the nova-config lines (`migrate --file`, two `friend add`, `machine add`, `fleet set --redis_port --pg_dsn --coordinator`, `apply`) that make a throwaway Redis name two friends, and says both ends need a proven daemon.

### 6. The SESSION CHECK pong command was refused when run as printed

- Read: the daemon's own prompt to the session: "Answer now ... with one command: nova-friend pong --as ada --nonce <n> --state-dir ... --redis ...".
- Expected: the printed command answers the check.
- Happened: on the first check it carried `--to ada` and answered (`PONG OK`). On the second check, 1s later, the fake ran the printed command and got `PONG REFUSED: --to is required: no ping has named a seat yet (no status file in /work/state-ada)`. The friend stayed `presence=down`, `nova-bus` kept refusing `ada` as deaf, and I had to restart the daemon. My fake then added `--to ada` itself when the printed command lacked it.
card: friend-session-check-command-always-names-to
- PATHS: cmd/nova-friend
- Task: make every SESSION CHECK prompt print a `pong` command that runs as printed (with `--to`), or make `pong` fall back to the seat the daemon knows.

### 7. A refusal's remedy names flags the usage line does not list

- Read: `RUN REFUSED: no push proof: CHECK FAIL ...; run: open the friend's opencode session in /work/ada, then prove it answers: nova-friend check --as ada --harness opencode --dir /work/ada`, against `nova-friend help`: `check [--as <coordinator>] [<friend>...] [--since <duration>] [--shown <file|->] [--json]`.
- Expected: the remedy's command is one the help lists.
- Happened: not run (I went on with `run`). `--harness` and `--dir` are not in `check`'s usage line, and `--as` there is "<coordinator>", not the friend.
card: friend-remedy-command-matches-check-usage
- PATHS: cmd/nova-friend
- Task: hold the remedy text printed for a failed push proof to the flags `nova-friend check -h` lists, in a test.

### 8. The container recipe leaves the work dir unwritable to the container user

- Read: `infra/functional-image/README.md` (the run flags) and the brief's `-v <dir>:/work`.
- Expected: the `bench` user (uid 10001) can write to the mounted work dir.
- Happened: with rootless podman, `go build` in the container: `could not create module cache: mkdir /work/gomod: permission denied`, and `rsync` from the host into the same dir failed (`mkstemp ... Permission denied`). I used `chmod -R a+rwX` on the host and wrote files through `podman exec -i ... cat >`.
card: functional-image-readme-names-a-writable-work-mount
- PATHS: infra/functional-image/README.md
- Task: add to the Run section the mount option (`:Z`, `--userns=keep-id`, or the `chmod`) that makes a work dir writable by `bench` under rootless podman, and say which to use.

### 9. `-h` and `help <verb>` print the same long text twice

- Read: `nova-friend run -h; nova-friend help run`.
- Expected: `-h` lists the flags; `help run` gives the story.
- Happened: both printed the same 65-line block (one long paragraph, then the flags), so the first screen of help for the one verb a new friend must use is a wall; the fact I needed (the harness is run by name) is not in it (stumble 3). The README's nova-friend row shows only `install ... --dry-run`, a launchd agent that does nothing on Linux (read, not run); the Linux door is `run`.
card: friend-run-help-leads-with-the-first-run
- PATHS: cmd/nova-friend
- Task: put a three-line first run (Redis, two daemons, one send) at the top of `nova-friend help run`, and make `-h` the flag list only.

## Verdict

Could a stranger do it from README and help alone: **no**. The goal was reached (a `nova-friend` daemon for each of two friends, one job delivered by `nova-bus` and a report back: the log shows `job: say hello` bob to ada at 03:16:45Z, the fake's `REPORT ... SEND OK`, and `report: say hello` ada to bob with `re=` the job's id, both deliveries `exit=0 acked=true`), but only after reading the infra README and the git tags, building from a checkout instead of the README's install, and learning the harness contract by running a logging fake and watching its argv. Of the nine stumbles, three (1, 2, 3) each stop a stranger cold and a fourth (4) leaves the outbox report the brief asked for untried.

Minutes taken: about 9 of bench clock: the 4m26s image build, then the container from 03:12:47Z to 03:16:54Z (4m07s) to the report coming back. Time spent reading and thinking between commands is not counted. The report that came back was a bus reply, not an outbox `REPORT.md`.
