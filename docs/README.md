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
- `fleet/` holds Ansible plays that install the tools, keep programs running,
  and run GitHub Actions runners on many machines. It needs Ansible.

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
for t in bus cairn config reserve sandbox secrets; do
  go build -C nova-tools -o "$work/bin/nova-$t" "./$t"
done
export PATH="$work/bin:$PATH"
for t in bus cairn config reserve sandbox secrets; do nova-$t -h > /dev/null && echo "nova-$t ok"; done
```

Each tool prints `nova-<tool> ok`: `-h` prints its usage and exits 0. A GitHub
release from v2.0.0-rc1 on ships the same six binaries, built for Linux amd64
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
nova-bus hook --as claude > "$work/claude.inbox" 2> "$work/claude.log" &
hook_pid=$!
sleep 1
nova-bus send --as codex --to claude --new "hello" "are you there?"
sleep 1
cat "$work/claude.log" "$work/claude.inbox"
nova-bus who
kill "$hook_pid"
```

`send` prints `claude <id>` (`--new` because a name no hook has read yet is
otherwise refused, so a typo never sits unread). The hook's stderr says
`HOOK OK name=claude waiting=0`, and its stdout has one tab-separated line:
the id, `codex`, `-` (this message answers none), `hello` and `are you there?`.
`who` prints `WHO OK present=1` and then `claude` with its uptime. One hook
holds a name at a time, and a line printed by a background job does not by
itself start a turn in your harness. How a session is woken by its hook is in
[bus/README.md](../bus/README.md#how-your-session-gets-woken).

The refusal you will meet first is
`cannot reach redis at <host:port> (NOVA_BUS_REDIS): connection refused`
(exit 1): the address is wrong or Redis is down. The others are in
[bus/README.md](../bus/README.md#exit-codes-and-refusals).

The whole tool: [bus/README.md](../bus/README.md); how it is tested:
[bus/SPEC.md](../bus/SPEC.md).

## Set up and run a fleet of machines connected by tailscale

A **fleet** here is the machines you list in nova-config. They are joined by
Tailscale, and Ansible reaches each one over SSH through it: a machine row
holds its tailscale name in `--address` and its login in `--user`. A
**service** row is a program a machine keeps running. A real fleet needs
Tailscale, Redis, PostgreSQL and Ansible, and none of the four is optional;
this walkthrough stays on one machine, with a throwaway PostgreSQL and
`ansible_connection=local` in place of SSH.

This block starts the PostgreSQL, adds this machine and one service, and prints
the inventory. `--actor` names who made a change, and the history keeps it. The
row's `os` and `arch` come from `uname`, because the runner labels later come
from the row. On Debian and Ubuntu, apt puts `initdb` and `pg_ctl` in
`/usr/lib/postgresql/<version>/bin`, outside PATH, so the first line adds the
newest one; on macOS Homebrew's are already on PATH, and the line changes
nothing:

```sh
pg_bin=$(ls -d /usr/lib/postgresql/*/bin 2> /dev/null | tail -1); [ -n "$pg_bin" ] && export PATH="$pg_bin:$PATH"
initdb -D "$work/pg" -U nova -A trust > /dev/null
pg_ctl -D "$work/pg" -o "-p 54330 -k $work -c listen_addresses=127.0.0.1" -l "$work/pg.log" -w start > /dev/null
createdb -h 127.0.0.1 -p 54330 -U nova nova
export NOVA_PG_DSN=postgres://nova@127.0.0.1:54330/nova
nova-config schema | PGOPTIONS="-c client_min_messages=warning" psql -q "$NOVA_PG_DSN"
nova-config machine add here --address localhost --user "$(id -un)" --os "$(uname -s | tr '[:upper:]' '[:lower:]')" --arch "$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')" --runners 1 --actor you
nova-config service add hello --machine here --argv '["sh","-c","echo hello; sleep 60"]' --keepalive true --actor you
nova-config inventory --host here
```

The two writes print `CONFIG ADD kind=machine name=here rev=1` and
`CONFIG ADD kind=service name=hello rev=2`. The inventory is the JSON
Ansible reads for that host: `ansible_host`, `ansible_user`, `runners` and the
`nova_services` list.

Now let Ansible plan the service. Ansible comes from pip here; the inventory
script runs `nova-config inventory`. `ansible_connection=local` stands in for
SSH so the plan runs on this machine. On Debian and Ubuntu `python3 -m venv`
fails until `python3-venv` is installed, because those systems ship Python's
venv support as a separate package, so the first line installs it when the
venv cannot be made. Without sudo, use `python3 -m venv --without-pip "$work/venv"`
and then `pip --python "$work/venv/bin/python" install ansible-core` with any
pip you already have.

```sh
python3 -m venv "$work/venv" || { rm -rf "$work/venv"; sudo apt install -y python3-venv && python3 -m venv "$work/venv"; }
"$work/venv/bin/pip" install -q ansible-core
export PATH="$work/venv/bin:$PATH"
printf '#!/bin/sh\nexec nova-config inventory "$@"\n' > "$work/inventory" && chmod +x "$work/inventory"
cd "$work/nova-tools"
ansible-playbook -i "$work/inventory" fleet/services.yml -e ansible_connection=local -e nova_service_only=hello --check | grep -E 'SERVICES|RESTART'
```

On Linux it prints two lines, as Ansible prints a message:

```text
    "msg": "WOULD-RESTART hello on here: its unit file changed"
    "msg": "SERVICES host=here place=/home/<you>/.config/systemd/user records=1 enabled=1 written=1 retired=0 only=hello (check: nothing changed)"
```

On macOS it prints one:

```text
    "msg": "SERVICES host=here place=/Users/<you>/Library/LaunchAgents records=1 enabled=1 written=1 retired=0 only=hello (check: nothing changed)"
```

The `WOULD-RESTART` line says the systemd unit would be written and started;
on macOS that step runs only for a job launchd already holds, so a check run
prints none. The `SERVICES` line is the play's **receipt**, its one-line
summary for the host. ansible-core 2.21 and later also print
`[DEPRECATION WARNING]` lines about `INJECT_FACTS_AS_VARS` on stderr; they do
not change the result. Without `--check` the play writes a systemd user unit
on Linux, or a launchd job under `~/Library/LaunchAgents` on macOS, for each
row and starts it.

The refusal you will meet first is
`NOVA_PG_DSN carries a password; remove it and set NOVA_PG_PASSWORD` (exit 2):
the password goes in `NOVA_PG_PASSWORD`, never in the DSN. The others are in
[config/README.md](../config/README.md#refusals).

The whole tool: [config/README.md](../config/README.md) and
[config/SPEC.md](../config/SPEC.md); the plays: [fleet/README.md](../fleet/README.md).

## Run GitHub continuous integration runners across your fleet

`fleet/github-runners.yml` installs GitHub Actions runners on every machine
whose row has `--runners` above zero, registers them to one repository or
organization, and runs each as a service that starts at boot. Plan it first:

```sh
ansible-playbook -i "$work/inventory" fleet/github-runners.yml -e ansible_connection=local \
  -e github_runner_url=https://github.com/OWNER/REPO -e github_runner_scope=repo --check | grep 'would register'
```

The plan prints, on Linux amd64:

```text
    "msg": "here: 1 wanted, would register [1], would retire [], labels amd64,bench,linux,runner"
```

and on macOS arm64:

```text
    "msg": "here: 1 wanted, would register [1], would retire [], labels arm64,bench,darwin,runner"
```

`[1]` lists the runner numbers it would register; runner 1 is named `here-1`.
The labels are the host's inventory groups, then the `os` and `arch` of its
machine row. `bench` is there because a machine row without `--roles` gets the
role `bench`, and `runner` because its `runners` value is above zero. On macOS
the play then fails after that line with `sudo: a password is required` (exit
2, which the `grep` above hides), because its look at `/Library/LaunchDaemons`
runs as root even under `--check`; add `-K` on a Mac to let the check finish.

To apply it, store a registration token, then run without `--check`; `-K`
asks for the sudo password the service files need. The token goes in a
nova-secrets store on this machine; this uses the store and seat key that the
secrets section below creates:

```text
export NOVA_SECRETS_STORE="$work/store" NOVA_SECRETS_KEY="$work/keys/here.key" NOVA_SECRETS_SEAT=here
gh api -X POST repos/OWNER/REPO/actions/runners/registration-token -q .token | nova-secrets set GITHUB_RUNNER_TOKEN
ansible-playbook -i "$work/inventory" fleet/github-runners.yml -e github_runner_url=https://github.com/OWNER/REPO -e github_runner_scope=repo -K
```

The token is passed on stdin, so it never appears on a command line or in
logs. While the task runs it is inside Ansible's per-task module file under
`~/.ansible/tmp` (a mode 0700 directory, readable by the login user and root
only), and it is removed when the task ends. A registration token expires
after an hour.

The refusal you will meet first is
`github-runner: give -e github_runner_url=... -e github_runner_scope=repo, ...`:
name the repository or organization and its scope, and give each host a
`runners` value or pass `-e runner_count=N`.

The plays: [fleet/README.md](../fleet/README.md).

## Deploy secrets to machines in your fleet automatically

A **store** is a git repository of encrypted files. A **seat** is a name for
one machine or job that reads values: it has one age key and one file of
values. `exec` decrypts the seat's file and starts a command with only the
values it names. `init`, `seat add` and `set` each make a git commit in the
store, so git needs an author. The first line gives this session one when git
has none:

```sh
git config user.email > /dev/null || export GIT_AUTHOR_NAME=ai GIT_AUTHOR_EMAIL=ai@example.com GIT_COMMITTER_NAME=ai GIT_COMMITTER_EMAIL=ai@example.com
mkdir -p "$work/keys"
age-keygen -o "$work/keys/here.key" 2> /dev/null
age-keygen -o "$work/keys/recovery.key" 2> /dev/null
chmod 600 "$work/keys/here.key"
nova-secrets init --store "$work/store" --recovery "$(age-keygen -y "$work/keys/recovery.key")"
nova-secrets seat add here "$(age-keygen -y "$work/keys/here.key")" --store "$work/store"
printf 'example-token\n' | nova-secrets set API_TOKEN --store "$work/store" --key "$work/keys/here.key" --as here
nova-secrets exec --only API_TOKEN --store "$work/store" --key "$work/keys/here.key" --as here -- sh -c 'echo "got ${#API_TOKEN} bytes"'
```

The last command prints `SECRETS EXEC OK as=here keys=1 cmd=sh` on stderr and
`got 13 bytes`.

To deploy, give a service row a seat and the names it needs. The services play
then wraps the program in `nova-secrets exec --only <names>`, so the value
reaches the program when the unit starts and is in no file Ansible writes:

```sh
nova-config service add api --machine here --argv '["sh","-c","test -n \"$API_TOKEN\" && echo token present; sleep 60"]' --seat here --keys API_TOKEN --keepalive true --actor you
ansible-playbook -i "$work/inventory" fleet/services.yml -e ansible_connection=local -e nova_service_only=api \
  -e nova_secrets_store="$work/store" -e nova_secrets_key_dir="$work/keys" --check --diff | grep -E 'ExecStart|<string>(.*/nova-secrets|--only|API_TOKEN)</string>'
```

The diff shows the unit starting `nova-secrets exec` with `--only API_TOKEN`
in front of the program: on Linux one `ExecStart=` line, on macOS three
`<string>` lines of the plist's `ProgramArguments`. The two `-e` values point
the unit at the store and key directory this session made; without them each
machine's unit opens `~/.local/share/nova/secrets` with
`~/.config/nova-secrets/<seat>.key`, as
[group_vars/all.yml](../fleet/group_vars/all.yml) says. Each machine needs the
store, its seat's key and sops at those places.

The refusal you will meet first is
`SECRETS SET REFUSED: key file ... must be a regular file of mode 0600 owned by you`
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
CHECK OK backend=landlock abi=4 net=enforceable note=landlock abi 4; seccomp available; net-deny landlock_tcp+seccomp; signals scoped to the wall at abi 6+
```

On macOS:

```text
sh: /Users/<you>/sandbox-escape.txt: Operation not permitted
exit 1
CHECK OK backend=sandbox-exec abi=- net=enforceable note=the wall is the profile it applies (/usr/bin/sandbox-exec)
```
`nova-sandbox probe` tests each of the sandbox's limits on this machine, one
line per step.

The refusal you will meet first is `nova-sandbox: bad_read: ...` (exit 2): a
grant is relative or missing, or the command is outside every grant. For a Go
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
`APPEND OK session=today entry=lesson-1`. The search prints three lines, shown
here shortened:

```text
SEARCH OK hits=1 k=3 channels=bm25 files=0 entries=1 skipped=0 chunks=1: query="bench unit tests"
SEARCH CAL score=0.97 score-channel=bm25 probe=unrelated-control
SEARCH HIT rank=1 score=0.86 ... session=today entry=lesson-1 stamp=<time>
```

A search that finds nothing prints a `SEARCH NOTE` instead of a hit, saying
the search is by keywords only.

The `HIT` line is the entry, with the time it was saved. The `CAL` line is the
score of a deliberately unrelated query, for comparison. The same `append`
again prints `duplicate=true` and writes nothing.

The refusal you will meet first is
`nova-cairn append: no such session "<id>" ...; open it first` (exit 2): run the
`open` the line names. The others are in
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
nova-secrets seat list --store "${NOVA_SECRETS_STORE:-$work/store}"  # here file=yes rule=yes
ansible-playbook --version | head -1                                 # ansible-playbook [core <version>]
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
