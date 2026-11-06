# nova-fleet

nova-fleet puts the same software on every machine you own. It is the fleet's Ansible plays in one binary: the
plays, roles, templates and defaults are embedded, a run extracts them to a private temporary directory, points
`ansible-playbook` at your inventory (`nova-config inventory` unless you name another) and exits with its status.

```
nova-fleet plays                                  # what is in the binary
nova-fleet show install                           # read a play before you run it
nova-fleet install --check --diff -- -e nova_tools_version=v1.0.0 -l <machine>
nova-fleet install -- -e nova_tools_version=v1.0.0 -l <machine>
```

`--check` and `--diff` may come before `--`; everything after `--` goes to `ansible-playbook` as written (`-e`, `-l`,
`-v`, `--tags`). `nova-fleet help` and `nova-fleet <play> -h` print the rest.

## Install

Take `nova-fleet` from a release like the other tools, or build it:

```
git clone --depth 1 https://github.com/mas-bandwidth/nova-tools && cd nova-tools && go build -o ~/.local/bin/nova-fleet ./fleet
```

The control machine
needs ansible-core 2.15 or later on PATH; a Windows host in the inventory also needs the `ansible.windows`
collection:

```
python3 -m venv ~/.local/ansible && ~/.local/ansible/bin/pip install ansible-core
ansible-galaxy collection install -r <extracted>/requirements.yml   # nova-fleet prints the path when it is missing
```

## The plays

| Play | Purpose | Needs |
|---|---|---|
| `github-runners` | GitHub Actions runners on every machine of the runner group, registered to one repository or organization | `github_runner_url`, `github_runner_scope`; `GITHUB_RUNNER_TOKEN` in nova-secrets; `gh` with admin on the repository |
| `install` | install one nova-tools release, by tag, on the machines named | `nova_tools_version` (a tag `vX.Y.Z`; no default) |
| `services` | one supervised unit per service record, on the machine the record names | the inventory's `nova_services` |

```
nova-fleet install -- -e nova_tools_version=v1.0.0 -l bench
nova-fleet services --check --diff
nova-fleet github-runners -- -e github_runner_url=https://github.com/OWNER/REPO -e github_runner_scope=repo
```

`nova-fleet plays` is the list in the binary you hold; `nova-fleet show <play>` prints one.

## Inventory

By default the inventory is a script in the extracted directory that execs `nova-config inventory` (found on PATH),
and `ANSIBLE_INVENTORY_UNPARSED_FAILED=true` is set so a failing script fails the run rather than reading as an
empty fleet. nova-config (`config/README.md`) keeps the machines and services as rows; `nova-config inventory`
prints each machine as a host in the groups its roles name (`bench`, `runner`, `services`, so `-l bench` runs on
every bench), with its service rows as the host variable `nova_services` (always present; empty means none).

To use your own inventory instead, name a file or script:

```
nova-fleet services --inventory ./hosts.ini --check
nova-fleet install --inventory ./my-inventory.py -- -e nova_tools_version=v1.0.0
```

Any inventory works if each host that runs `services` carries `nova_services`, a list of rows: `name`, `argv` (a
list), and optionally `keys` (secret names) with `seat`, `every` (seconds), `enabled`, `env` (an object) and `log`.

## Overriding the defaults

`group_vars/all.yml` holds generic defaults: no value names a machine, a person or a site. Put your values beside
your inventory: a host variable from the inventory, a `group_vars/<group>.yml` or `host_vars/<machine>.yml` there,
or `-e name=value` overrides them. A `group_vars/all.yml` beside the inventory does not, because the play's own is
read after it: use a group name.

```
# group_vars/bench.yml
nova_secrets_store: /srv/nova/secrets
nova_secrets_key_dir: /srv/nova/keys
nova_service_path: "{{ nova_bin_dir }}:/usr/local/bin:/usr/bin:/bin"
```

| Variable | Default |
|---|---|
| `nova_home` | the login's home |
| `nova_bin_dir` | `~/.local/bin` |
| `nova_service_path` | `<nova_bin_dir>` then the usual system directories |
| `nova_secrets_store` | `~/.local/share/nova/secrets` |
| `nova_secrets_key_dir` | `~/.config/nova-secrets` |
| `nova_sops` | `/opt/homebrew/bin/sops` on macOS, else `<nova_bin_dir>/sops` |
| `nova_launchd_domain` | `auto` (`gui`, `system`) |
| `nova_systemd_scope` | `user` (`system`) |

## install

A release is a git tag `v*` on this repository: seven binaries (nova-bus, nova-cairn, nova-config, nova-sandbox,
nova-secrets, nova-reserve, nova-fleet) for linux/amd64, linux/arm64, darwin/arm64 and windows/amd64, each asset
`<tool>_<tag>_<os>_<arch>[.exe]`, with one `SHA256SUMS`. The play reads `SHA256SUMS`, downloads each asset and checks
its digest before putting it in place. A binary that already holds the digest is left alone, so a second run changes
nothing and `--check --diff` is clean. There is no default version, and a platform the release does not ship is
refused before anything is written.

| Variable | Default | |
|---|---|---|
| `nova_tools_version` | none, required | the tag, `vX.Y.Z` |
| `nova_tools_names` | the seven tools | which tools to install (and which `argv[0]` services prefixes) |
| `nova_bin_dir` | `~/.local/bin` | where they go (Windows: `<profile>\.local\bin`) |
| `nova_tools_release_url` | this repository's GitHub releases | `<base>/<tag>/<asset>` |
| `nova_tools_restart`, `nova_tools_restart_commands` | false, empty | argv lists run once, only if a binary changed |
| `nova_tools_remove_enabled` | false | retire the binaries below |
| `nova_tools_remove` | the retired list | names to retire |

A release from before nova-fleet shipped has no nova-fleet asset; install one with
`-e '{"nova_tools_names":["nova-bus","nova-cairn","nova-config","nova-sandbox","nova-secrets","nova-reserve"]}'`.

**A local build.** With no release yet, or to try a change, build the binaries on the control node laid out as a
release is and install from that directory (Linux and macOS hosts; each file is checked against its `SHA256SUMS` line
before it is copied):

```
v=local; mkdir -p dist
for t in bus cairn config fleet reserve sandbox secrets; do
  for p in linux/amd64 linux/arm64 darwin/arm64; do
    GOOS=${p%/*} GOARCH=${p#*/} CGO_ENABLED=0 go build -trimpath -o dist/nova-${t}_${v}_${p%/*}_${p#*/} ./$t
  done
done
(cd dist && sha256sum nova-* > SHA256SUMS)          # macOS: shasum -a 256 nova-* > SHA256SUMS
nova-fleet install -- -e nova_tools_version=local -e nova_tools_local_dir=$PWD/dist -l <machine>
```

`nova-fleet up --tools $PWD/dist` does the same for every machine.

**Retiring.** With `-e nova_tools_remove_enabled=true`, after a successful install each name in `nova_tools_remove`
found in the bin directory is moved to `<nova_bin_dir>/retired-<date>/`, never deleted; a name not there is skipped.
A name also being installed is refused. The default list is the rows of `retired-tools.txt` that say `retired` or
`rowan-tools`; another product's binaries are never in it. To retire more, name the whole list:
`-e nova_tools_remove_enabled=true -e nova_tools_remove='["nova-sprint","nova-card"]'`. Unix machines only.

## services

One supervised unit per service row, on the machine the row names: a LaunchAgent (or LaunchDaemon) on macOS, a
systemd user unit (or system unit) on Linux, with a `.timer` beside it for a row that runs every n seconds.

```
nova-fleet services --check --diff
nova-fleet services -- -e nova_service_restart=reader
```

- The unit runs the row's `argv` exactly; if `argv[0]` is one of `nova_tools_names` it is prefixed with
  `nova_bin_dir`. `{"argv":["nova-bus","hook","--as","reader"],"env":{"NOVA_BUS_REDIS":"redis-host:6379"}}` runs
  `<nova_bin_dir>/nova-bus hook --as reader` with that variable set. `PATH` includes `nova_bin_dir`; a row's `env`
  wins over `HOME` and `PATH`. Env values are strings: write the string you mean. Output appends to the row's `log`.
- launchd does not search `PATH`, so on macOS a bare `argv[0]` is resolved with `command -v` against the unit's
  `PATH`; not found fails the play before anything is written.
- A row with `keys` is wrapped in `nova-secrets exec --store <store> --as <seat> --key <key_dir>/<seat>.key --sops
  <sops> --only <keys> --`, so values reach the command and never the unit file.
- Rows without `every` are kept alive; `enabled` defaults to true; `enabled false` writes the unit and does not load it.
- Every unit carries a mark. A marked unit no row names, or one in the other place (user versus system), is retired;
  `--check` lists it as `WOULD-RETIRE`. A unit of another tool is left alone and named in a `NOTE`;
  `nova_retire_units` names any to retire. Units of the play's earlier name (`loops.yml`) are retired on the first run.
- A host with no rows retires nothing by itself: a run that would retire every marked unit on it refuses, naming
  them, unless `-e nova_service_retire_all=true`.
- A unit whose file changed is restarted (`RESTART`, `WOULD-RESTART` under `--check`). `-e nova_service_only=a,b`
  renders and restarts only those rows and retires nothing; `-e nova_service_restart=a,b` restarts those rows even if
  unchanged. A name no row carries is refused before anything is written.
- Each host ends with one receipt line: `SERVICES host=<machine> place=<unit dir> records=<n> enabled=<n>
  written=<n> retired=<n>`, then ` only=<names>` and ` (check: nothing changed)` where they apply.

## github-runners

GitHub Actions runners on every machine of the `runner` group: as many as the machine row's `runners` says, each
registered to one repository or organization, run as a service that starts at boot, labelled with the machine's
roles, `os` and `arch`. Linux (amd64, arm64; WSL2 with systemd counts as Linux), macOS (a LaunchDaemon dropped to the
login) and native Windows (the runner's own service; `ansible.windows` and an administrator login). Linux and macOS
write the service files with sudo, so the login needs passwordless sudo: `join.yml` sets it up on Linux
(`/etc/sudoers.d/90-nova-<login>`); on macOS set it at the console. Never `-K`: an interactive prompt is a stall for an
AI running the play.

**Public repositories.** A self-hosted runner runs the code of any workflow that reaches it, and on a public repository
that includes pull requests from forks. Use these runners on private repositories, or restrict which workflows and
events may use them.

| Variable | |
|---|---|
| `github_runner_url` | no default. `https://github.com/<owner>/<repo>`, or `https://github.com/<org>` |
| `github_runner_scope` | no default. `repo` or `org`; refused if the url is the other shape |
| `github_runner_labels` | extra labels, a list (default none), added to the roles, `os` and `arch` |
| `runner_count` | overrides the row's `runners` for one run; 0 retires every runner. A host with neither is refused |
| `github_runner_state` | `present` (default) or `absent`, which retires every runner of that url |
| `github_runner_deregister` | true: retiring removes the runner from GitHub (needs a removal token) |
| `github_runner_version`, `github_runner_sha256` | the pinned actions/runner release and its digests |

**The token.** The play reads `GITHUB_RUNNER_TOKEN` on the control node with `nova-secrets exec --only
GITHUB_RUNNER_TOKEN` and hands it to the runner on its standard input: never an argument, an environment variable on
the host, the inventory or a file. A registration token expires in an hour, so store it just before the run (set
`NOVA_SECRETS_STORE` and `NOVA_SECRETS_KEY` as [secrets/SPEC.md](../secrets/SPEC.md) says, or pass
`-e '{"github_runner_secrets_args":["--store","S","--key","K"]}'`):

```
gh api -X POST repos/OWNER/REPO/actions/runners/registration-token -q .token | nova-secrets set GITHUB_RUNNER_TOKEN
nova-fleet github-runners --check --diff -- -e github_runner_url=https://github.com/OWNER/REPO -e github_runner_scope=repo
nova-fleet github-runners -- -e github_runner_url=https://github.com/OWNER/REPO -e github_runner_scope=repo
```

For an organization the path is `orgs/ORG/actions/runners/registration-token`. The token is read when a runner needs
registering or removing, under `--check` too, so a green check is never followed by a run that fails on a missing
token. A read that fails says why (nova-secrets' own refusal: no store, no key, no such name) and the command that
stores it.

**The check.** After the run, GitHub's own list of runners is read with `gh api <repos|orgs>/<path>/actions/runners`
on the control node (a `gh` login with admin on the repository or organization), giving a new runner a minute to come
online, and each machine prints one line: `RUNNERS <machine> want=<n> online=<n> listed=<names>`. A wanted runner that
is not online fails the run; `verified=no reason=...` says why the list could not be read. `-e
github_runner_verify=false` skips it.

**What it does.** Per runner: the pinned release, checked against GitHub's digest, unpacked in
`<home>/actions-runner/<owner>-<repo>-<i>` (Windows `C:\actions-runner`); registered as `<machine>-<i>` unless
`.runner` is there; one service per runner, enabled at boot. Labels are fixed at registration: changed labels are
reported as `LABEL-DRIFT` and the runner is left alone. A changed unit restarts its runner, ending any job on it. A
machine whose count drops has its highest-numbered runners retired and removed from GitHub, which needs a removal
token (`.../actions/runners/remove-token`, stored the same way). A second run changes nothing.

**Rollback.** `-e github_runner_state=absent` stops and removes the services, removes the runners from GitHub and
deletes their directories; with `-e github_runner_deregister=false` no token is read and the GitHub entries stay for
you to delete.

## up

`nova-fleet up` takes a fleet from nothing to running in one fixed order. Its inputs are only what you have: the
machines' ssh addresses, the Tailscale auth key in your seat, and, for runners, the repository or organization.

```
nova-fleet up --check --tools v2.0.0 store-1=203.0.113.7 web-1=203.0.113.8 mac-1=mac-1.local/darwin/arm64
nova-fleet up --tools v2.0.0 --runners https://github.com/OWNER/REPO store-1=203.0.113.7 web-1=203.0.113.8
```

**What is yours, once, before the first run:**

1. The Tailscale account and its policy, and a reusable, pre-authorized auth key tagged `tag:nova` (and `tag:runner`
   for runners) (the "Tailscale" section below).
2. The control node: ansible-core, `nova-config`, `nova-secrets`, `nova-bus`, `tailscale` (on the tailnet), `psql`,
   `git`, `sops` and `age` on PATH (`gh` too for runners); a secrets store with this node's seat
   ([secrets/SPEC.md](../secrets/SPEC.md)); `NOVA_SECRETS_STORE`, `NOVA_SECRETS_KEY` and `NOVA_SECRETS_SEAT` set.
3. The key in the seat: `nova-secrets set TAILSCALE_AUTHKEY --as <seat>` (the value on stdin), and for runners
   `GITHUB_RUNNER_TOKEN` as in "github-runners".
4. Each Linux machine reachable as root over ssh at its address, with the control node's key. A Mac: its login and
   passwordless sudo set up at its console, and the Tailscale app's VPN approved once.

**The order.** The first machine named holds the fleet's stores. Nothing here is a choice to make again:

| # | Step | What runs | Why here |
|---|---|---|---|
| 1 | control | every helper and variable above found (all missing ones named at once); `TAILSCALE_AUTHKEY` (and `GITHUB_RUNNER_TOKEN`) read from the seat | a run never starts that a missing secret would stop later |
| 2 | first | on the first machine, from a hand inventory as root: `tailscale` (no address recorded, firewall later), `postgres` and `redis` (their passwords sealed into your seat as `NOVA_PG_PASSWORD` and `NOVA_BUS_PASSWORD`), then `nova-config schema` applied | nova-config's rows live in this PostgreSQL, so it comes before any row |
| 3 | join | `join` for each machine, the first too (`services,bench`; the others `bench`; `runner` with `--runners`): its row, its login with passwordless sudo, its tailnet name, its firewall, its seat | the store exists now |
| 4 | store | `git push` of your secrets store, if it has an upstream (else a NOTE) | the new seats reach the machines' clones |
| 5 | tools | `install` on every machine, from the release `--tools vX.Y.Z` or a local build `--tools DIR` | the services run these binaries |
| 6 | services | `services` | |
| 7 | runners | `github-runners`, `--runner-count` per machine (only with `--runners`) | |
| 8 | bus | a message sent to the inbox `nova-fleet-up` over the first machine's Redis and read back | the fleet is reached end to end |

Each play prints its own receipts; each step adds `UP <step> OK` (or `WOULD-...` under `--check`), and the run ends
with one line per machine: `UP MACHINE <name> OK address=<address> steps=<the steps it passed>`. A failed step prints
`UP <step> FAILED exit=<n>` and stops; every step is idempotent, so the same command resumes.

**--check** runs every step under `ansible-playbook --check --diff` and reads the secrets all the same. On a fleet
whose PostgreSQL does not exist yet, the steps after the first machine's plays cannot be checked against anything;
they are named (`UP FIRST WOULD-RUN ...`), never reported green. Nothing is pushed, applied or sent under `--check`.

A machine's seat holds no values until you set them from a key that opens it (`nova-secrets set NAME --as <seat>`):
join's bus check on a new machine is a `NOTE` until its seat holds `NOVA_BUS_PASSWORD`.

## Refusals

Each is one line on stderr, exit 2, and nothing is run:

| You ran | It says |
|---|---|
| an unknown play or verb | the verbs and the plays, and `run: nova-fleet help` |
| an unknown flag before `--`, or a word before `--` | `ansible-playbook flags go after --` |
| `--inventory` naming nothing | the path, and to name an existing file or script |
| no `--inventory` and no nova-config on PATH | `install nova-tools or run: nova-fleet <play> --inventory <file\|script>` |
| no ansible-playbook on PATH | `install ansible-core 2.15+ and run: ansible-galaxy collection install -r <extracted>/requirements.yml` |

Otherwise the exit is ansible-playbook's. Before running, nova-fleet prints the ansible-playbook, inventory and play
paths it uses on stderr. The extracted directory is removed when ansible-playbook exits (kept only for the missing
ansible-playbook remedy).

## Build and test

```
go build -o nova-fleet ./fleet
go test ./fleet                       # unit: the embedded tree, purposes, arguments, refusals (no ansible)
go test -tags functional ./fleet      # ansible-core on PATH: the plays run through the binary and templar
```

Add a play by adding `<name>.yml` here with a `# purpose:` line at its top; the binary embeds the whole directory.
