# nova-tools: a guide for AIs

You are an AI with a shell on one of your human's machines, and you have been
asked whether these tools are useful. This page lets you find out by running
them. Every block below runs as written, in order, in one bash session, and
leaves nothing behind once you run the last one. Each section says what the
first try prints, the refusals you are most likely to meet, and where the full
guide for that tool is.

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

Each tool works on its own. Use one without the others.

## Get the binaries and check they run

Build them with Go from a clone. The blocks below use a throwaway directory,
`$work`, and put the tools on PATH from there.

```sh
work=$(mktemp -d) && cd "$work"
git clone -q https://github.com/mas-bandwidth/nova-tools
for t in bus cairn config reserve sandbox secrets; do
  go build -C nova-tools -o "$work/bin/nova-$t" "./$t"
done
export PATH="$work/bin:$PATH"
for t in bus cairn config reserve sandbox secrets; do nova-$t -h > /dev/null && echo "nova-$t ok"; done
```

Each tool prints `nova-<tool> ok`: `-h` prints its usage and exits 0. A
GitHub release of nova-tools ships the same binaries, named
`<tool>_<tag>_<os>_<arch>`, with a `SHA256SUMS` file to check them against.

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
nova-bus send --as codex --to claude "hello" "are you there?"
sleep 1
cat "$work/claude.log" "$work/claude.inbox"
nova-bus who
kill "$hook_pid"
```

`send` prints `claude <id>`. The hook's stderr says
`HOOK OK name=claude waiting=0`, and its stdout has one tab-separated line:
the id, `codex`, `-` (this message answers none), `hello` and `are you there?`.
`who` prints `WHO OK present=1` and then `claude` with its uptime. To hook a
session in, run the hook as a background job of your harness and read its
lines as they arrive.

The refusals you will meet first:

- `missing ... NOVA_BUS_REDIS (host:port)` (exit 2): set `NOVA_BUS_REDIS`.
- `cannot reach redis at <host:port> (NOVA_BUS_REDIS): connection refused`
  (exit 1): the address is wrong or Redis is down.
- `name already hooked: <name> after 40s` (exit 1): another hook holds that
  name. `nova-bus who` shows who is present.

The whole tool: [bus/README.md](../bus/README.md), and how it is tested:
[bus/SPEC.md](../bus/SPEC.md).

## Set up and run a fleet of machines connected by tailscale

A **fleet** here is the machines you list in nova-config. Each machine row
names how Ansible reaches it: put its tailscale name in `--address` and its
login in `--user`, and Ansible connects over SSH through tailscale. A
**service** row is a program a machine keeps running. This block starts a
throwaway PostgreSQL, adds this machine and one service, and prints the
inventory:

```sh
initdb -D "$work/pg" -U nova -A trust > /dev/null
pg_ctl -D "$work/pg" -o "-p 54330 -k $work -c listen_addresses=127.0.0.1" -l "$work/pg.log" -w start > /dev/null
createdb -h 127.0.0.1 -p 54330 -U nova nova
export NOVA_PG_DSN=postgres://nova@127.0.0.1:54330/nova
nova-config schema | PGOPTIONS="-c client_min_messages=warning" psql -q "$NOVA_PG_DSN"
nova-config machine add here --address localhost --user "$(id -un)" --os linux --arch amd64 --runners 1 --actor you
nova-config service add hello --machine here --argv '["sh","-c","echo hello; sleep 60"]' --keepalive true --actor you
nova-config inventory --host here
```

The two writes print `CONFIG ADD kind=machine name=here rev=1` and
`CONFIG ADD kind=service name=hello rev=2`. The inventory is the JSON
Ansible reads for that host: `ansible_host`, `ansible_user`, `runners` and the
`nova_services` list. `initdb` and `pg_ctl` are often outside PATH, for example
in `/usr/lib/postgresql/<version>/bin`.

Now let Ansible plan the service. Ansible comes from pip here; the inventory
script runs `nova-config inventory`. `ansible_connection=local` stands in for
SSH so the plan runs on this machine:

```sh
python3 -m venv "$work/venv" && "$work/venv/bin/pip" install -q ansible-core
export PATH="$work/venv/bin:$PATH"
printf '#!/bin/sh\nexec nova-config inventory "$@"\n' > "$work/inventory" && chmod +x "$work/inventory"
cd "$work/nova-tools"
ansible-playbook -i "$work/inventory" fleet/services.yml -e ansible_connection=local -e nova_service_only=hello --check | grep -E 'SERVICES|RESTART'
```

The last line is the play's **receipt**, its one-line summary for the host:
`SERVICES host=here place=... records=1 enabled=1 written=1 retired=0 only=hello (check: nothing changed)`.
Without `--check` the play writes a systemd user unit (or a launchd job on
macOS) for each row and starts it.

The refusals you will meet first:

- `NOVA_PG_DSN carries a password; remove it and set NOVA_PG_PASSWORD` (exit 2):
  the password goes in `NOVA_PG_PASSWORD`, never in the DSN.
- `<host> has no nova_services: ...`: your inventory is not nova-config's;
  give every host a `nova_services` list.
- `<host> has no service rows, and this run would retire <n> unit(s): ...`:
  the play refuses to empty a host by accident. Pass
  `-e nova_service_retire_all=true` only if that is what you mean.

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

The plan says `here: 1 wanted, would register [...]` with the runner's name
and labels. To apply it, store a registration token, then run without
`--check`; `-K` asks for the sudo password the service files need:

```text
gh api -X POST repos/OWNER/REPO/actions/runners/registration-token -q .token | nova-secrets set GITHUB_RUNNER_TOKEN
ansible-playbook -i "$work/inventory" fleet/github-runners.yml -e github_runner_url=https://github.com/OWNER/REPO -e github_runner_scope=repo -K
```

The token is passed on stdin, so it never appears on a command line or in
logs. While the task runs it is inside Ansible's per-task module file under
`~/.ansible/tmp` (a mode 0700 directory, readable by the login user and root
only), and it is removed when the task ends. A registration token expires
after an hour.

The refusals you will meet first:

- `github-runner: give -e github_runner_url=... -e github_runner_scope=repo, ...`:
  name the repository or organization, and its scope.
- The same line when a host has no `runners` value: set it on the machine row,
  or pass `-e runner_count=N`.
- `LABEL-DRIFT <name>: ...`: a runner's labels changed. Retire it with
  `-e github_runner_state=absent`, then run again.

The plays: [fleet/README.md](../fleet/README.md).

## Deploy secrets to machines in your fleet automatically

A **store** is a git repository of encrypted files. A **seat** is a name for
one machine or job that reads values: it has one age key and one file of
values. `exec` decrypts the seat's file and starts a command with only the
values it names:

```sh
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
`got 13 bytes`. A git commit identity must be set for `init`, `seat add` and
`set`, because each one commits.

To deploy, give a service row a seat and the names it needs. The services play
then wraps the program in `nova-secrets exec --only <names>`, so the value
reaches the program when the unit starts and is in no file Ansible writes:

```sh
nova-config service add api --machine here --argv '["sh","-c","test -n \"$API_TOKEN\" && echo token present; sleep 60"]' --seat here --keys API_TOKEN --keepalive true --actor you
ansible-playbook -i "$work/inventory" fleet/services.yml -e ansible_connection=local -e nova_service_only=api --check --diff | grep 'ExecStart'
```

The diff shows the unit's `ExecStart` running `nova-secrets exec` with
`--only API_TOKEN` in front of the program. Keep `{#` out of a row's `argv`:
the play reads `argv` as an Ansible template, where `{#` opens a comment.
Each machine needs the store, its seat's key and sops. The play's defaults
for where they live are in [group_vars/all.yml](../fleet/group_vars/all.yml).

The refusals you will meet first:

- `SECRETS SET REFUSED: key file ... must be a regular file of mode 0600 owned by you`
  (exit 2; 125 from `exec`): run `chmod 600` on the key file.
- `SECRETS EXEC REFUSED: unknown secret name(s) <names>: ...` (exit 125): that
  name was never `set` for this seat. The line ends with the `nova-secrets names`
  command that lists what the seat holds.
- `nova-secrets set: value is over 65536 bytes` (exit 2): a value is a key or
  a token, not a file.

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

The refusals you will meet first:

- `capacity in use (...); retry when free` (exit 1): `set` waits for no holder.
- `no slot within <d>: ...` (exit 1): every slot stayed taken for the whole
  `--wait`. Wait longer, or look at `status`.
- `<cmd>: command not found on PATH=...` (exit 127): the command is not on PATH.

The whole tool: [reserve/README.md](../reserve/README.md) and
[reserve/SPEC.md](../reserve/SPEC.md).

## Run untrusted work inside a sandbox

A **grant** is a directory you name with `--read` or `--write`. The command can
read and run the system directories, and touch nothing else:

```sh
mkdir -p "$work/job"
nova-sandbox --write "$work/job" --net-deny -- sh -c "echo ok > '$work/job/out.txt'"
cat "$work/job/out.txt"
nova-sandbox --write "$work/job" -- sh -c "echo no > '$HOME/sandbox-escape.txt'" || echo "exit $?"
nova-sandbox check
```

The first command writes `ok` inside its grant. The second is denied by the
kernel: `sh` prints a `Permission denied` line and the run exits 2, the
command's own status. `check` prints a line starting `CHECK OK backend=`.
`nova-sandbox probe` runs a fuller test of the wall on this machine.

The refusals you will meet first:

- `nova-sandbox: bad_read: ...` (exit 2): a grant is relative or missing, or
  the command is outside every grant. For a Go toolchain outside `/usr`, add
  `--read "$(go env GOROOT)"`.
- `SANDBOX REFUSED reason=not_found: ...` (exit 127): the command is not on PATH.
- `SANDBOX REFUSED reason=net_unenforceable: ...` (exit 125): this kernel cannot
  close the network. `nova-sandbox check` says why.

The whole tool: [sandbox/README.md](../sandbox/README.md) and
[sandbox/SPEC.md](../sandbox/SPEC.md).

## Retain memory across sessions

A **store** here is a directory of saved sessions. A **session** is a named
group of entries, and an **entry** is the words you chose to keep. A later
session searches them, beside any Markdown notes:

```sh
nova-cairn open --store "$work/cairn" --session today --publish never --source "$work"
nova-cairn append --store "$work/cairn" --session today --entry lesson-1 --text "The bench needs NOVA_TEST_NO_HOST=1 for the unit tests."
nova-cairn memory search --root "$work/cairn" --channels bm25 --k 3 bench unit tests
```

`open` prints a line starting `OPEN OK`, and `append` one starting
`APPEND OK session=today entry=lesson-1`. The search prints `SEARCH OK hits=1`
and then a `SEARCH HIT rank=1 ...` line that ends with `entry=lesson-1` and its
stamp. The same `append` again prints
`duplicate=true` and writes nothing.

The refusals you will meet first:

- `nova-cairn append: no such session "<id>" ...; open it first` (exit 2): run
  the `open` the line names.
- `APPEND FAILED session=<id> entry=<id>: entry "<id>" already holds different words`
  (exit 1): use a new `--entry` id.
- Words that are not valid UTF-8 are refused (exit 2).

The whole tool: [cairn/README.md](../cairn/README.md) and
[cairn/SPEC.md](../cairn/SPEC.md).

## Is it working?

Paste this in the session above, before you clean up. Elsewhere, set
`NOVA_BUS_REDIS`, `NOVA_PG_DSN` and `NOVA_SECRETS_STORE` first. Each line
prints the first line shown beside it.

```sh
nova-bus -h | head -1           # nova-bus: messages between named inboxes, kept in Redis.
nova-config -h | head -1        # nova-config: a fleet's machines and services, kept as rows in PostgreSQL.
nova-secrets -h | head -1       # nova-secrets: sops-encrypted secrets in a git store, handed to one command at a time.
nova-reserve status | head -1   # capacity=<n> set_by_pid=<pid> set_at=<time>
nova-sandbox check | head -1    # CHECK OK backend=landlock abi=<n> net=enforceable ...
nova-cairn version | head -1    # nova-cairn <version> (dev when built from a clone)
nova-bus who | head -1          # WHO OK present=<n>
nova-config machine list | head -1                                   # MACHINE name=here address=localhost user=<you> ...
nova-secrets seat list --store "${NOVA_SECRETS_STORE:-$work/store}"  # here file=yes rule=yes
ansible-playbook --version | head -1                                 # ansible-playbook [core <version>]
```

## Clean up

```sh
cd / && pg_ctl -D "$work/pg" stop > /dev/null && redis-cli -p "$port" shutdown nosave 2> /dev/null; rm -rf "$work"
```

Command-line rules shared by every tool: [CLI-STYLE.md](CLI-STYLE.md).
Security boundaries and how to report a problem: [SECURITY.md](SECURITY.md).
