# nova-tools: a guide for AIs

You are an AI with a shell on one of your human's machines, and you have been
asked whether these tools are useful. This page lets you find out by running
them. Every block below runs as written, in order, in one bash session, on
Linux or macOS; where the two print different lines, both are shown and
labelled. The last block removes everything the session made, except the Go, pip and Ansible
caches in your home directory (`~/.cache/go-build`, `~/go/pkg/mod`,
`~/.cache/pip` and `~/.ansible`). Each section says what the first try prints,
the refusal you are most likely to meet, and where the full guide for that
tool is.

## The tools

- `nova-bus` sends short messages between named inboxes in Redis, so AIs in
  different harnesses can reach each other. It needs Redis 6.2 or later.
- `nova-config` keeps your machines, and the programs each one runs, as rows in
  PostgreSQL, and prints them as Ansible inventory. It needs PostgreSQL.
- `nova-secrets` keeps encrypted values in a git repository and gives a
  command only the values it names. It needs sops, age and git.
- `nova-reserve` limits how many jobs run at once on one machine, using file
  locks. It needs nothing else.
- `nova-sandbox` runs one command that can touch only the directories you
  grant it. It needs Linux 5.13 or later (for the kernel's Landlock rules), or
  macOS.
- `nova-cairn` saves what a session wants to keep, as plain files, and
  searches it alongside Markdown notes. It needs nothing else.
- `nova-fleet` runs the fleet's Ansible plays from one binary: Tailscale,
  PostgreSQL, Redis, the tools, the services and GitHub Actions runners on
  every machine. It needs Ansible, Tailscale, and ssh to the machines.

Each tool runs on its own, and this walkthrough starts and stops a throwaway
Redis and PostgreSQL on this one machine. A fleet of machines is different: it
needs Tailscale, Redis, PostgreSQL and Ansible, and none of the four is
optional.

## Get the binaries and check they run

Build them with Go from a clone. The blocks
below use a throwaway directory, `$work`, and put the tools on PATH from there.

```sh
work=$(mktemp -d) && cd "$work"
git clone -q https://github.com/mas-bandwidth/nova-tools
for t in bus cairn config fleet reserve sandbox secrets; do
  go build -C nova-tools -o "$work/bin/nova-$t" "./$t"
done
export PATH="$work/bin:$PATH"
for t in bus cairn config fleet reserve sandbox secrets; do nova-$t -h > /dev/null && echo "nova-$t ok"; done
```

Each tool prints `nova-<tool> ok`: `-h` prints its usage and exits 0. A GitHub
release from v2.0.0-rc1 on ships the same seven binaries, built for Linux amd64
and arm64, macOS arm64 and Windows amd64. Each file is named
`nova-<tool>_<tag>_<os>_<arch>`, with `.exe` on Windows, beside one
`SHA256SUMS` file to check them against.

## Talk to each other across models and harnesses

Any process that can run a command can send a message, and any process that
can read a stream of lines can receive one. A **hook** is the receiver: it
prints each message for its name as one line, until it is stopped. Start a
throwaway Redis, hook in as `claude`, and send to it as `codex`:

```sh
port=$((20000 + RANDOM % 20000))
redis-server --port "$port" --bind 127.0.0.1 --save '' --appendonly no --dir "$work" --daemonize yes > /dev/null
export NOVA_BUS_REDIS=127.0.0.1:$port
sleep 1
nova-bus hook --as claude > "$work/claude.inbox" 2> "$work/claude.log" &   # a demo on throwaway names:
nova-bus hook --as codex > "$work/codex.inbox" 2> "$work/codex.log" &     # your own hook runs under the Monitor
sleep 1
nova-bus send --as codex --to claude "hello" "are you there?"
sleep 1
cat "$work/claude.log" "$work/claude.inbox"
nova-bus who
kill %1 %2
redis-cli -p "$port" shutdown nosave
```

Both names hook in first, because a verb that acts as a name (`send`, `ack`,
`pending`) refuses until that name is hooked in, and says how to hook in. The
two hooks here are background jobs into files only because this is a demo on
throwaway names; your own hook runs as the Monitor tool's command, as
[bus/README.md](../bus/README.md#how-your-session-gets-woken) says.
`send` prints `claude <id>`. claude's hook says `HOOK OK name=claude
waiting=0` on stderr, and its stdout has one tab-separated line: the id,
`codex`, `-` (this message answers none), `hello` and `are you there?`.
`who` prints `WHO OK present=2` and then `claude` and `codex` with their
uptimes. One hook holds a name at a time, and a line printed by a background
job does not by itself start a turn in your harness. How a session is woken by its hook is in
[bus/README.md](../bus/README.md#how-your-session-gets-woken).

The refusal you will meet first is
`cannot reach redis at <host:port> (NOVA_BUS_REDIS): connection refused`
(exit 1): the address is wrong or Redis is down. The others are in
[bus/README.md](../bus/README.md#exit-codes-and-refusals).

The whole tool: [bus/README.md](../bus/README.md); how it is tested:
[bus/SPEC.md](../bus/SPEC.md).

## Set up and run a fleet of machines connected by tailscale

A **fleet** is a set of machines joined by Tailscale, described as rows in
nova-config's PostgreSQL, talking over a Redis, and converged by Ansible plays.
`nova-fleet` is those plays in one binary. Tailscale, Redis, PostgreSQL and
Ansible are all required: `check` wants Tailscale and Ansible on this machine,
`up` puts PostgreSQL and Redis on the first machine, and nothing skips one. The
path is `nova-fleet check`, then your human's steps in the Tailscale console,
then `nova-fleet up --check`, then `nova-fleet up`.

`check` needs Ansible on PATH. On Debian and Ubuntu `python3 -m venv` fails
until `python3-venv` is installed, because those systems ship Python's venv
support as a separate package, so the first line installs it when the venv
cannot be made. Without sudo, use `python3 -m venv --without-pip "$work/venv"`
and then `pip --python "$work/venv/bin/python" install ansible-core` with any
pip you already have.

```sh
python3 -m venv "$work/venv" || { rm -rf "$work/venv"; sudo apt-get update -qq && sudo apt-get install -y python3-venv && python3 -m venv "$work/venv"; }
"$work/venv/bin/pip" install -q ansible-core
export PATH="$work/venv/bin:$PATH"
```

`check` also needs this machine's **control seat**: a nova-secrets store, the
seat's age key, and a **pins** directory, the public keys the control node keeps
outside the store so no edit to the store can change who a secret reaches. A
**seat** is one identity that reads values, with one file and one key. The
control seat holds the values the fleet needs, starting with the Tailscale auth
key. `init`, `seat add` and `set` each make a git commit, so git needs an
author; the first line gives this session one when git has none:

```sh
git config user.email > /dev/null || export GIT_AUTHOR_NAME=ai GIT_AUTHOR_EMAIL=ai@example.com GIT_COMMITTER_NAME=ai GIT_COMMITTER_EMAIL=ai@example.com
mkdir -p "$work/keys"
age-keygen -o "$work/keys/here.key" 2> /dev/null
age-keygen -o "$work/keys/recovery.key" 2> /dev/null
chmod 600 "$work/keys/here.key"
nova-secrets init --store "$work/store" --pins "$work/pins" --recovery "$(age-keygen -y "$work/keys/recovery.key")"
nova-secrets seat add here "$(age-keygen -y "$work/keys/here.key")" --store "$work/store"
export NOVA_SECRETS_STORE="$work/store" NOVA_SECRETS_KEY="$work/keys/here.key" NOVA_SECRETS_SEAT=here NOVA_SECRETS_PINS="$work/pins"
nova-fleet check || echo "exit $?"
```

`check` prints one line per prerequisite, `CHECK OK <name> <version>`, then
the tailnet, the store, the key, the seat and the pins. On a machine that has
the helpers and is on a tailnet, two lines are left, and the run ends
`CHECK FAILED missing=2`, exit 1:

```text
CHECK MISSING tag:control: sudo tailscale up --advertise-tags=tag:control --auth-key=<a key allowed that tag>  # or tag this node in the admin console: ...
CHECK MISSING TAILSCALE_AUTHKEY: nova-secrets set TAILSCALE_AUTHKEY --as here  # the value on stdin; the read said: SECRETS EXEC REFUSED reason=no-name: ...
```

Each `CHECK MISSING` line ends with the command that fixes it. These two are
your human's, once, in the Tailscale admin console: the account with MagicDNS
on; the policy, pasted from `nova-fleet show tailscale-acl`, with their account
where it says `owner@example.com`; a reusable, pre-authorized auth key tagged
`tag:nova` (and `tag:runner` for runners); and this machine on the tailnet
tagged `tag:control`. They hand you the auth key, and you store it:

```text
printf '%s\n' '<the auth key>' | nova-secrets set TAILSCALE_AUTHKEY --as here
nova-fleet check
```

Once `check` ends `CHECK OK missing=0`, name the machines, each reachable as
root over ssh at its address with this machine's key and its host key already
in `known_hosts`. The first one named holds the fleet's PostgreSQL and Redis.
`--check` plans; the same line without it runs:

```text
nova-fleet up --check --tools v2.0.0 store-1=203.0.113.7 web-1=203.0.113.8
nova-fleet up --tools v2.0.0 store-1=203.0.113.7 web-1=203.0.113.8
```

The addresses are placeholders; put your machines' in their place. `up` runs
in a fixed order (check, reach, the first machine's Tailscale, PostgreSQL and
Redis, join, grant, push the store, health, tools, services, runners, a bus
message read back), prints `UP <step> OK` per step and `UP MACHINE <name> OK
...` per machine, and stops at the first `UP <step> FAILED exit=<n>`; the same
command resumes. Every step and what it prints is in
[fleet/README.md](../fleet/README.md#up).

To see a play work on this machine alone, give nova-config a throwaway
PostgreSQL, add this machine and one **service** row (a program a machine keeps
running), and let `nova-fleet services --check` plan it. `--actor` names who
made a change, and the history keeps it. The row's `os` and `arch` come from
`uname`. On Debian and Ubuntu, apt puts `initdb` and `pg_ctl` in
`/usr/lib/postgresql/<version>/bin`, outside PATH, so the first line adds the
newest one; on macOS Homebrew's are already on PATH. `--inventory` names a
script that prints `nova-config inventory`, and `ansible_connection=local`
stands in for ssh:

```sh
pg_bin=$(ls -d /usr/lib/postgresql/*/bin 2> /dev/null | tail -1); [ -n "$pg_bin" ] && export PATH="$pg_bin:$PATH"
initdb -D "$work/pg" -U nova -A trust > /dev/null
pg_ctl -D "$work/pg" -o "-p 54330 -k $work -c listen_addresses=127.0.0.1" -l "$work/pg.log" -w start > /dev/null
createdb -h 127.0.0.1 -p 54330 -U nova nova
export NOVA_PG_DSN=postgres://nova@127.0.0.1:54330/nova
nova-config schema | PGOPTIONS="-c client_min_messages=warning" psql -q "$NOVA_PG_DSN"
nova-config machine add here --address localhost --user "$(id -un)" --os "$(uname -s | tr '[:upper:]' '[:lower:]')" --arch "$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')" --runners 1 --actor you
nova-config service add hello --machine here --argv '["sh","-c","echo hello; sleep 60"]' --keepalive true --actor you
printf '#!/bin/sh\nexec nova-config inventory "$@"\n' > "$work/inventory" && chmod +x "$work/inventory"
nova-fleet services --check --inventory "$work/inventory" -- -e ansible_connection=local -e nova_service_only=hello | grep -E 'SERVICES|RESTART'
```

The two writes print `CONFIG ADD kind=machine name=here rev=1` and
`CONFIG ADD kind=service name=hello rev=2`. The play prints its **receipt**,
one summary line per host, as Ansible prints a message. On Linux:

```text
    "msg": "SERVICES host=here place=/home/<you>/.config/systemd/user records=1 enabled=1 written=1 retired=0 would-restart=hello only=hello (check: nothing changed)"
```

On macOS:

```text
    "msg": "SERVICES host=here place=/Users/<you>/Library/LaunchAgents records=1 enabled=1 written=1 retired=0 only=hello (check: nothing changed)"
```

`would-restart=hello` says the systemd unit would be written and started; on
macOS that part is absent, because the play restarts only a job launchd
already holds. ansible-core 2.21 and later also print
`[DEPRECATION WARNING]` lines about `INJECT_FACTS_AS_VARS` on stderr; they do
not change the result. Without `--check` the play writes a systemd user unit
on Linux, or a launchd job under `~/Library/LaunchAgents` on macOS, for each
row and starts it.

The refusal you will meet first is
`NOVA_PG_DSN carries a password; remove it and set NOVA_PG_PASSWORD` (exit 2):
the password goes in `NOVA_PG_PASSWORD`, never in the DSN. The others are in
[config/README.md](../config/README.md#refusals) and
[fleet/README.md](../fleet/README.md#refusals).

The whole tool: [fleet/README.md](../fleet/README.md), with
[config/README.md](../config/README.md) and [config/SPEC.md](../config/SPEC.md)
for the rows.

## No fleet yet? Mock one with VMs

If you have no machines to point `nova-fleet` at, stand up fresh Linux VMs
(for example Debian 12 guests) on a host you control, with root ssh from this
machine's key and their host keys in `known_hosts`. Join them to a throwaway
tailnet, run `nova-fleet check`, `up --check` and `up` against them exactly as
for a real fleet, then destroy the VMs and remove the throwaway keys, store and
tailnet auth key when you are done.

## Run GitHub continuous integration runners across your fleet

The `github-runners` play installs GitHub Actions runners on every machine
whose row has `runners` above zero, registers them to one repository or
organization, and runs each as a service that starts at boot; `nova-fleet up
--runners URL` runs it as its runners step. `check --runners URL` adds what it
needs to the list:

```sh
nova-fleet check --runners https://github.com/OWNER/REPO | grep -E 'gh|GITHUB_RUNNER_TOKEN'
```

```text
CHECK FAILED missing=3
CHECK OK gh <version>
CHECK MISSING GITHUB_RUNNER_TOKEN: nova-secrets set GITHUB_RUNNER_TOKEN --as here  # the value on stdin; the read said: SECRETS EXEC REFUSED reason=no-name: ...
```

The `CHECK FAILED` line is on stderr, so the grep leaves it through; the
`tag:control` line from before is still counted.

Under --check, github-runners reads GITHUB_RUNNER_TOKEN on the control node
before it plans, so a green check is never followed by a run that fails on a
missing token: store a token first (gh api ... -q .token | nova-secrets set
GITHUB_RUNNER_TOKEN), then check. The token is passed on stdin, so it never
appears on a command line or in logs; a registration token expires after an
hour. With your human's repository:

```text
gh api -X POST repos/OWNER/REPO/actions/runners/registration-token -q .token | nova-secrets set GITHUB_RUNNER_TOKEN
nova-fleet github-runners --check --diff -- -e github_runner_url=https://github.com/OWNER/REPO -e github_runner_scope=repo
nova-fleet github-runners -- -e github_runner_url=https://github.com/OWNER/REPO -e github_runner_scope=repo
```

To see the plan on this machine with the throwaway rows above, store a
placeholder token first. The plan prints one `RUNNER-PLAN` line per machine;
the `RUNNERS` line after it reads GitHub's own list of runners, which a
placeholder repository does not have:

```sh
printf 'example-token\n' | nova-secrets set GITHUB_RUNNER_TOKEN
nova-fleet github-runners --check --inventory "$work/inventory" -- -e ansible_connection=local -e github_runner_url=https://github.com/OWNER/REPO -e github_runner_scope=repo | grep -E 'RUNNER'
```

On Linux amd64:

```text
    "msg": "RUNNER-PLAN here want=1 register=here-1 retire=- labels=amd64,bench,linux,runner (check: nothing changed)"
    "msg": "RUNNERS here want=1 verified=no reason=gh: Not Found (HTTP 404)  (check: nothing changed)"
```

On macOS arm64 the labels are `arm64,bench,darwin,runner`. The labels are the
host's inventory groups, then the `os` and `arch` of its machine row: `bench`
because a machine row without `--roles` gets that role, `runner` because its
`runners` value is above zero. A job selects the runner with
`runs-on: [self-hosted, linux]`. The service files need passwordless sudo on
Linux and macOS (`join` sets it up on Linux; on a Mac your human sets it at
the console); no command asks for a password.

Retiring a runner removes it from GitHub, which needs a **removal token**, a
different one from the registration token, stored the same way:

```text
gh api -X POST repos/OWNER/REPO/actions/runners/remove-token -q .token | nova-secrets set GITHUB_RUNNER_TOKEN
nova-fleet github-runners -- -e github_runner_url=https://github.com/OWNER/REPO -e github_runner_scope=repo -e github_runner_state=absent
```

The refusal you will meet first is
`github-runner: give -e github_runner_url=... -e github_runner_scope=repo, ...`:
name the repository or organization and its scope, and give each host a
`runners` value or pass `-e runner_count=N`. The others are in
[fleet/README.md](../fleet/README.md#github-runners).

## Deploy secrets to machines in your fleet automatically

Each machine has its own seat, and its key never leaves it. The control seat
holds the values every machine needs, and `grant` copies named values into a
machine's **grants file**, sealed to that machine's pinned key and the recovery
key only; the control node's own key cannot open what it wrote. `nova-fleet
up` does this for every machine (its grant step, default `NOVA_BUS_PASSWORD`),
then pushes the encrypted store to each machine over the tailnet (its refresh
step), so a value set once on the control node reaches every machine without
anyone copying it by hand. This block plays a machine named `db` here: on a
real fleet `db`'s key is made on `db`, and only its public half comes back:

```sh
printf 'example-bus-password\n' | nova-secrets set NOVA_BUS_PASSWORD
age-keygen -o "$work/keys/db.key" 2> /dev/null
chmod 600 "$work/keys/db.key"
nova-secrets seat add db "$(age-keygen -y "$work/keys/db.key")" --store "$work/store"
nova-secrets grant NOVA_BUS_PASSWORD --to db --pub "$(age-keygen -y "$work/keys/db.key")"
nova-secrets grant NOVA_BUS_PASSWORD --to db
nova-secrets exec --only NOVA_BUS_PASSWORD --key "$work/keys/db.key" --as db -- sh -c 'echo "db got ${#NOVA_BUS_PASSWORD} bytes"'
nova-secrets exec --only NOVA_BUS_PASSWORD --as db -- true || echo "exit $?"
```

```text
SECRETS SET OK as=here name=NOVA_BUS_PASSWORD file=here.yaml committed; run: git -C <store> push
SECRETS SEAT ADD OK seat=db committed next=nova-secrets\x20set\x20DB_PASSWORD\x20--as\x20here\x20--store\x20/tmp/tmp.k3Jd9x/store\x20--key\x20/tmp/tmp.k3Jd9x/keys/here.key\x20--pins\x20/tmp/tmp.k3Jd9x/pins
SECRETS GRANT OK from=here to=db names=1 changed=1 committed; run: git -C <store> push
SECRETS GRANT OK from=here to=db names=1 changed=0 unchanged
SECRETS EXEC STARTING as=db keys=1 cmd=sh
db got 20 bytes
SECRETS EXEC REFUSED reason=not-recipient: key age1... is not the key in .sops.yaml for db (age1...): ...
exit 125
```

The first `grant` to a seat takes `--pub`, the machine's public key, and pins
it; later grants check the pin. The same grant again changes nothing. The last
line is the control node's own key being refused for `db`'s file, by design.

On a machine, a service row's `seat` and `keys` make the services play wrap the
program in `nova-secrets exec --only <keys>`, so the granted value reaches the
program when its unit starts and is in no file Ansible writes:

```sh
nova-config service add api --machine here --argv '["sh","-c","test -n \"$NOVA_BUS_PASSWORD\" && echo password present; sleep 60"]' --seat db --keys NOVA_BUS_PASSWORD --keepalive true --actor you
nova-fleet services --check --diff --inventory "$work/inventory" -- -e ansible_connection=local -e nova_service_only=api \
  -e nova_secrets_store="$work/store" -e nova_secrets_key_dir="$work/keys" | grep -E 'ExecStart|<string>(.*/nova-secrets|--only|NOVA_BUS_PASSWORD)</string>'
```

The diff shows the unit starting `nova-secrets exec` with
`--only NOVA_BUS_PASSWORD` in front of the program: on Linux one `ExecStart=`
line, on macOS three `<string>` lines of the plist's `ProgramArguments`. The
two `-e` values point the unit at this session's store and key directory;
on a fleet, `up` puts the store at `~/.local/share/nova/secrets` and the key
at `~/.config/nova-secrets/<seat>.key` on each machine, which the play uses by
default.

The refusal you will meet first is
`SECRETS SET REFUSED reason=no-key: key file ... must be a regular file of mode 0600 owned by you`
(exit 2, or 125 from `exec`): run `chmod 600` on the key file. The others are in
[secrets/README.md](../secrets/README.md#refusals).

The whole tool: [secrets/README.md](../secrets/README.md).

## Share machines without stepping on each other

A **slot** is one place in a machine's limit on jobs that run at once. A job
waits for a free slot, holds it while it runs, and frees it when it exits,
even if it is killed. Everyone who shares the limit uses the same directory:

```sh
export NOVA_RESERVE_DIR="$work/reserve"
nova-reserve set --slots 2
nova-reserve --why "first job" -- sleep 1 &
nova-reserve --why "second job" -- sleep 1
wait
nova-reserve status
```

`set` prints `CAPACITY SET slots=2`. Each job prints
`nova-reserve: HELD mode=shared slot=<n>/2 ...` on stderr before its command
runs, and `status` ends with `free=2/2` once both have finished.

The refusal you will meet first is `no slot within <d>: ...` (exit 1): every
slot stayed taken for the whole `--wait`. Wait longer, or look at `status`. The
others are in [reserve/README.md](../reserve/README.md#refusals-and-what-to-do-next).

The whole tool: [reserve/README.md](../reserve/README.md) and
[reserve/SPEC.md](../reserve/SPEC.md).

## Run untrusted work inside a sandbox

A **grant** is a directory you name with `--read` or `--write`. The command can
read and run the system directories, and touch nothing else:

```sh
mkdir -p "$work/job" && cd "$work/job"
nova-sandbox --write "$work/job" --net-deny -- sh -c "echo ok > out.txt"
cat out.txt
nova-sandbox --write "$work/job" -- sh -c "echo no > '$HOME/sandbox-escape.txt'" || echo "exit $?"
nova-sandbox check
cd "$work"
```

The block starts inside the grant, because a shell started outside it cannot
read its own working directory. The first command writes `ok` inside the
grant. The second is denied by the kernel, and the run exits with the
command's own status. On Linux:

```text
sh: 1: cannot create /home/<you>/sandbox-escape.txt: Permission denied
exit 2
CHECK OK backend=landlock abi=4 net=enforceable note=landlock abi 4; seccomp available; net-deny landlock_tcp+seccomp; signals scoped to the wall at abi 6+ next=nova-sandbox\x20probe
```

On macOS:

```text
sh: /Users/<you>/sandbox-escape.txt: Operation not permitted
exit 1
CHECK OK backend=sandbox-exec abi=- net=enforceable note=the wall is the profile it applies (/usr/bin/sandbox-exec) next=nova-sandbox\x20probe
```
`nova-sandbox probe` tests each of the sandbox's limits on this machine, one
line per step.

The refusal you will meet first is `nova-sandbox: bad_read: ...` (exit 2): a
grant is relative or missing, or the command is outside every grant. A working
directory outside every grant is `SANDBOX REFUSED reason=cwd` (exit 125, not 2):
cd into a grant or `--read` that directory. For a Go
toolchain outside `/usr`, add `--read "$(go env GOROOT)"`. A Go build also needs
`--env TMPDIR --env GOCACHE --env GOMODCACHE --env GOPROXY`, each set to a path
inside a `--write` (`GOMODCACHE` inside a `--read`). `HOME` is kept. The others are in
[sandbox/README.md](../sandbox/README.md#refusals).

The whole tool: [sandbox/README.md](../sandbox/README.md) and
[sandbox/SPEC.md](../sandbox/SPEC.md).

## Retain memory across sessions

A **store** here is a directory of saved sessions. A **session** is a named
group of entries, and an **entry** is the words you chose to keep. A later
session searches them, beside any Markdown notes. `--publish never` records
that you do not mean to publish the session; cairn itself sends nothing
anywhere. `--source` is a pointer you give, such as a directory or a file
name, kept with each entry and never opened. `--channels bm25` asks for
keyword ranking, and `--k 3` for at most three hits:

```sh
nova-cairn open --store "$work/cairn" --session today --publish never --source "$work"
nova-cairn append --store "$work/cairn" --session today --entry lesson-1 --text "The bench needs NOVA_TEST_NO_HOST=1 for the unit tests."
nova-cairn memory search --root "$work/cairn" --channels bm25 --k 3 bench unit tests
```

`open` prints a line starting `OPEN OK`, and `append` one starting
`APPEND OK session=today entry=lesson-1` and ending `opened=false`; a
session's first entry ends with `next=<the index command>`. The search prints three lines, shown
here shortened:

```text
SEARCH OK hits=1 k=3 channels=bm25 files=0 entries=1 skipped=0 chunks=1: query="bench unit tests"
SEARCH CAL score=0.29 score-channel=bm25 probe=unrelated-control
SEARCH HIT rank=1 score=0.86 ... session=today entry=lesson-1 stamp=<time>
```

A search that finds nothing prints `SEARCH MISS every query term is out of
vocabulary for this corpus` and a `SEARCH NOTE` saying the search is by
keywords only.

The `HIT` line is the entry, with the time it was saved. The `CAL` line is the
score of a deliberately unrelated query, for comparison. The same `append`
again prints `duplicate=true` and writes nothing.

The refusal you will meet first is
`nova-cairn append: store "<dir>" does not exist; open it first, or fix --store`
(exit 2): run the `open` the line names, or correct `--store`. An `append` to a
store that exists but has no such session opens the session itself
(`opened=true`). The others are in
[cairn/README.md](../cairn/README.md#refusals).

The whole tool: [cairn/README.md](../cairn/README.md) and
[cairn/SPEC.md](../cairn/SPEC.md).

## Is it working?

Paste this in the session above, before you clean up. Elsewhere, set
`NOVA_BUS_REDIS`, `NOVA_PG_DSN`, `NOVA_SECRETS_STORE` and `NOVA_RESERVE_DIR`
first. Each line
prints the first line shown beside it.

```sh
nova-bus -h | head -1           # nova-bus: messages between named inboxes, kept in Redis.
nova-config -h | head -1        # nova-config: a fleet's machines and services, kept as rows in PostgreSQL.
nova-secrets -h | head -1       # nova-secrets: sops-encrypted secrets in a git store, handed to one command at a time.
nova-reserve status | head -1   # capacity=<n> set_by_pid=<pid> set_at=<time>
nova-sandbox check | head -1    # CHECK OK backend=landlock ... (Linux) or backend=sandbox-exec ... (macOS)
nova-cairn version | head -1    # nova-cairn <version> (dev when built from a clone)
nova-bus who | head -1          # WHO OK present=<n>
nova-config machine list | head -1                                   # MACHINE name=here address=localhost user=<you> ...
nova-secrets seat list --store "${NOVA_SECRETS_STORE:-$work/store}"  # db file=no rule=yes, then here file=yes rule=yes
nova-fleet check | head -1                                           # CHECK OK ansible-playbook <version>
```

## Clean up

Each step runs whether or not the one before it worked:

```sh
cd /
pg_ctl -D "$work/pg" stop > /dev/null || echo "postgres was not running"
redis-cli -p "$port" shutdown nosave 2> /dev/null || echo "redis was not running"
rm -rf "$work"
```

Command-line rules shared by every tool: [CLI-STYLE.md](CLI-STYLE.md).
Security boundaries and how to report a problem: [SECURITY.md](SECURITY.md).
