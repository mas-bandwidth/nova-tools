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

Take `nova-fleet` from a release like the other tools (or `go build -o nova-fleet ./fleet`). The control machine
needs ansible-core 2.15 or later on PATH; a Windows host in the inventory also needs the `ansible.windows`
collection:

```
python3 -m venv ~/.local/ansible && ~/.local/ansible/bin/pip install ansible-core
ansible-galaxy collection install -r <extracted>/requirements.yml   # nova-fleet prints the path when it is missing
```

## The plays

| Play | Purpose | Needs |
|---|---|---|
| `github-runners` | GitHub Actions runners on every machine of the runner group, registered to one repository or organization | `github_runner_url`, `github_runner_scope`; `GITHUB_RUNNER_TOKEN` in nova-secrets; `-K` |
| `install` | install one nova-tools release, by tag, on the machines named | `nova_tools_version` (a tag `vX.Y.Z`; no default) |
| `services` | one supervised unit per service record, on the machine the record names | the inventory's `nova_services` |

```
nova-fleet install -- -e nova_tools_version=v1.0.0 -l bench
nova-fleet services --check --diff
nova-fleet github-runners -- -e github_runner_url=https://github.com/OWNER/REPO -e github_runner_scope=repo -K
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
use sudo for the service files: add `-K`.

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
`NOVA_SECRETS_STORE` and `NOVA_SECRETS_KEY` as `docs/SPEC-SECRETS.md` says, or pass
`-e '{"github_runner_secrets_args":["--store","S","--key","K"]}'`):

```
gh api -X POST repos/OWNER/REPO/actions/runners/registration-token -q .token | nova-secrets set GITHUB_RUNNER_TOKEN
nova-fleet github-runners --check --diff -- -e github_runner_url=https://github.com/OWNER/REPO -e github_runner_scope=repo -K
nova-fleet github-runners -- -e github_runner_url=https://github.com/OWNER/REPO -e github_runner_scope=repo -K
```

For an organization the path is `orgs/ORG/actions/runners/registration-token`. The token is read only when a runner
needs registering, and never under `--check`.

**What it does.** Per runner: the pinned release, checked against GitHub's digest, unpacked in
`<home>/actions-runner/<owner>-<repo>-<i>` (Windows `C:\actions-runner`); registered as `<machine>-<i>` unless
`.runner` is there; one service per runner, enabled at boot. Labels are fixed at registration: changed labels are
reported as `LABEL-DRIFT` and the runner is left alone. A changed unit restarts its runner, ending any job on it. A
machine whose count drops has its highest-numbered runners retired and removed from GitHub, which needs a removal
token (`.../actions/runners/remove-token`, stored the same way). A second run changes nothing.

**Rollback.** `-e github_runner_state=absent` stops and removes the services, removes the runners from GitHub and
deletes their directories; with `-e github_runner_deregister=false` no token is read and the GitHub entries stay for
you to delete.

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
