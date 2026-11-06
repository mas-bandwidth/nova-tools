# The fleet

`fleet/` is how the fleet is managed: which machines there are and what they run (nova-config), the services they keep
running (`services.yml`), the nova-tools release each one holds (`nova-tools-install.yml`), the GitHub Actions runners
on them (`github-runners.yml`), and the reservations that keep
builds and tests from piling onto one box (`nova-reserve`). It is Ansible plus two Go tools, nothing else; nothing here
names a machine or a person. Those are rows in nova-config.

```
fleet/
  nova-tools-install.yml            install one release, by tag
  roles/nova-tools-install/         the role the play runs
  services.yml                      one launchd or systemd unit per service row
  github-runners.yml                GitHub Actions runners on the runner group
  roles/github-runner/              the role the play runs
  templates/nova-service.*.j2       the unit templates
  group_vars/all.yml                the generic defaults both plays read
  retired-tools.txt                 binaries nova-tools shipped and ships no more
  requirements.yml                  the Ansible collection a Windows host needs
  ci/inventory                      the stub inventory CI checks services.yml against
reserve/                            nova-reserve, reservations on a machine (Go)
```

## The machine and service model

nova-config (`config/README.md`) keeps the fleet as rows in PostgreSQL, each write with a history row. Two kinds
matter here:

- `machine`: a host, its login `user` and its `roles`. The role names are the inventory's groups: `bench`, `runner`
  and `services`, so `-l bench` runs a play on every bench.
- `service`: a program a machine keeps running; with `--every N` it runs every N seconds instead: `machine`, `argv` (a JSON array), `keys` (secret
  names, never values; a row with keys also names the `seat` that opens them), `every` (seconds; none means kept
  alive), `enabled`, `env` (an object of environment variables) and `log` (a path, default
  `~/.local/state/nova/services/<name>.log`).

`nova-config inventory` prints them as an Ansible dynamic inventory: each machine is a host in the groups its roles
name, and its service rows are the host variable `nova_services`. It always emits `nova_services`; an empty list is valid and
means no services. Use it as an inventory script, and make a failing script fail loudly (Ansible otherwise falls back
to treating the output as an empty inventory):

```
printf '#!/bin/sh\nexec nova-config inventory "$@"\n' > inventory && chmod +x inventory
export ANSIBLE_INVENTORY_UNPARSED_FAILED=true
```

The plays take their inventory from that and nothing else, so a change to the fleet is a nova-config write followed
by a play run. `ansible-galaxy collection install -r fleet/requirements.yml` installs `ansible.windows`, which only a
Windows host in the inventory needs.

## Overriding the defaults

`fleet/group_vars/all.yml` holds generic defaults: no value names a machine, a person or a site. A fleet whose layout
differs puts its values beside its inventory, not here:

```
inventory                 the nova-config script
group_vars/bench.yml      one group's values
host_vars/<machine>.yml   one machine's values
```

```
# group_vars/bench.yml
nova_secrets_store: /srv/nova/secrets
nova_secrets_key_dir: /srv/nova/keys
nova_service_path: "{{ nova_bin_dir }}:/usr/local/bin:/usr/bin:/bin"
```

A host variable from the inventory, a `group_vars/<group>.yml` or a `host_vars/<machine>.yml` beside the inventory
overrides `fleet/group_vars/all.yml`; so does `-e name=value` on the command line. A `group_vars/all.yml` beside the
inventory does not, because the playbook's own is read after it: use a group name instead. Defaults:

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

## Installing a release

A release is a git tag `v*` on this repository. `.github/workflows/release.yml` builds the six tools (nova-bus,
nova-cairn, nova-config, nova-sandbox, nova-secrets, nova-reserve) for linux/amd64, linux/arm64, darwin/arm64 and windows/amd64 with
`CGO_ENABLED=0 -trimpath`, names each asset `<tool>_<tag>_<os>_<arch>[.exe]` (for example
`nova-reserve_v1.0.0_linux_amd64`), writes one `SHA256SUMS`, and creates the GitHub release with the workflow's own
`GITHUB_TOKEN`. Nothing else holds a credential.

```
ansible-playbook -i ./inventory fleet/nova-tools-install.yml -e nova_tools_version=v1.0.0 -l <machine> --check --diff
ansible-playbook -i ./inventory fleet/nova-tools-install.yml -e nova_tools_version=v1.0.0 -l <machine>
```

The role reads the release's `SHA256SUMS`, downloads each asset and checks it against its digest before it is put in
place. A binary that already holds the digest is left alone, so a second run changes nothing and `--check --diff` is
clean. There is no default version: a run never installs "latest" by accident. A platform the release does not ship
is refused before anything is written. The Windows tasks are included only for a Windows host, so
`ansible-galaxy collection install -r fleet/requirements.yml` (ansible.windows) is needed only when a Windows host is
in the inventory.

| Variable | Default | |
|---|---|---|
| `nova_tools_version` | none, required | the tag, `vX.Y.Z` |
| `nova_tools_names` | the six tools | which tools to install (and which `argv[0]` services.yml prefixes) |
| `nova_bin_dir` | `~/.local/bin` | where they go (Windows: `<profile>\.local\bin`) |
| `nova_tools_release_url` | the GitHub releases of this repository | `<base>/<tag>/<asset>` |
| `nova_tools_restart`, `nova_tools_restart_commands` | false, empty | argv lists run once, only if a binary changed |
| `nova_tools_remove_enabled` | false | retire the binaries below |
| `nova_tools_remove` | the retired list | names to retire |

**The retire list.** With `-e nova_tools_remove_enabled=true`, after a successful install each name in
`nova_tools_remove` found in the bin directory is moved to `<nova_bin_dir>/retired-<date>/`, never deleted; a name
not there is skipped, so this is idempotent too. A name that is also being installed is refused. The default list is
the rows of `fleet/retired-tools.txt` that say `retired` or `rowan-tools`: nova-check, nova-ci,
nova-decide, nova-doctor, nova-fuse, nova-local, nova-loop, nova-memory, nova-redis, nova-self-talk, nova-slot,
nova-table, nova-tokens, nova-up, nova-update, nova-version. Another product's binaries (nova-sprint, nova-card,
nova-work, nova-swarm, nova-hook, nova-friend) are never in it. An operator who wants more gone names the whole list:

```
-e nova_tools_remove_enabled=true -e nova_tools_remove='["nova-sprint","nova-card"]'
```

Retiring is for Unix machines; on Windows it is skipped.

## services.yml

One supervised unit per service row, on the machine the row names: a LaunchAgent (or LaunchDaemon) on macOS, a systemd
user unit (or system unit) on Linux, with a `.timer` beside the service for a row that runs every n seconds.

```
ansible-playbook -i ./inventory fleet/services.yml --check --diff
ansible-playbook -i ./inventory fleet/services.yml
```

The unit runs the row's `argv` exactly. If `argv[0]` is one of `nova_tools_names`, it is prefixed with
`nova_bin_dir`; no other word is changed. The row
`{"argv":["nova-bus","hook","--as","reader"],"env":{"NOVA_BUS_REDIS":"redis-host:6379"}}` runs
`<nova_bin_dir>/nova-bus hook --as reader` with `NOVA_BUS_REDIS=redis-host:6379` in its environment. The unit's
`PATH` includes `nova_bin_dir`, and each `env` entry is a unit environment variable (an `Environment=` line on Linux,
an `EnvironmentVariables` entry on macOS); a row's `env` wins over `HOME` and `PATH`. Env values are strings: a JSON `true` reaches the program as
`True`, so write the string you mean. Output is appended to the row's `log` (a `~/` is the login's home), whose directory
the play creates.

A bare `argv[0]` that is not a tool name (`sleep`, say) is found on the unit's `PATH` by systemd. launchd does not
search `PATH`, so on macOS the play resolves it with `command -v` against the unit's `PATH` and writes the absolute
path into the plist; if it is not found the play fails before anything is written.

A row whose `keys` is not empty is wrapped in
`nova-secrets exec --store <store> --as <seat> --key <key_dir>/<seat>.key --sops <sops> --only <keys> --`, so the
values reach the command and never the unit file. `<seat>` and `<keys>` are the row's; `<store>`, `<key_dir>` and
`<sops>` are `nova_secrets_store`, `nova_secrets_key_dir` and `nova_sops` (see the table above). A row with no keys
runs its `argv` unchanged. Rows without `every` are kept alive; `enabled` defaults to true. `enabled false` writes
the unit and does not load it.

- Every unit carries a mark. A marked unit no row names, and a marked unit in the other place (user versus system), is
  retired: unloaded and removed. `--check` lists each as `WOULD-RETIRE`. A unit of another tool is left alone and
  named in a `NOTE`; `nova_retire_units` names any to retire.
- Units written by this play's earlier name, `loops.yml` (`com.nova.loop.<name>.plist`, `nova-loop-<name>.service|timer`, marked `from the loop record`), are retired by the first run, before the new units are loaded (stop old, then load new, so a timer or a kept-alive singleton never runs twice); the log directory changed too (`~/.local/state/nova/loops` to `.../services`).
- **A host with no rows retires nothing by itself.** An empty `nova_services` is valid, but a run that would retire
  every unit it ever wrote on that host (all the marked `nova-service-*` and `nova-loop-*` units) refuses instead,
  naming the units. If the empty list is what you mean, say so with `-e nova_service_retire_all=true`; otherwise
  check the inventory (`nova-config service list`) first. Units named in `nova_retire_units` are explicit and do not
  count.
- A unit whose file changed is restarted (`RESTART`, `WOULD-RESTART` under `--check`).
- `-e nova_service_only=a,b` renders and restarts only those rows and retires nothing. `-e nova_service_restart=a,b`
  restarts those rows even if their unit did not change; this is how a hung service is restarted, never by hand. A name
  no row carries is refused before anything is written.
- On Linux a changed unit that was not restarted fails the play; the receipt ends each host. The receipt is one line,
  `SERVICES host=<machine> place=<unit directory> records=<rows> enabled=<rows enabled> written=<unit files created or
  changed> retired=<units retired>`, then ` only=<names>` when `nova_service_only` was given and
  ` (check: nothing changed)` under `--check`.

## github-runners.yml

GitHub Actions runners on every machine of the `runner` group: as many as the machine row's `runners` says, each
registered to one repository or organization, run as a service that starts at boot, labelled with the machine's roles,
`os` and `arch`. Linux (amd64, arm64; WSL2 with systemd on counts as Linux), macOS (a LaunchDaemon dropped to the
login) and native Windows (the runner's own Windows service; `ansible.windows` and an administrator login). Linux and
macOS use sudo for the service files: add `-K`. A Windows host without the collection is refused with the install command
(`ansible-galaxy collection install -r fleet/requirements.yml`).

| Variable | |
|---|---|
| `github_runner_url` | no default. `https://github.com/<owner>/<repo>`, or `https://github.com/<org>` |
| `github_runner_scope` | no default. `repo` or `org`; refused if the url is the other shape |
| `github_runner_labels` | extra labels, a list (default none). Added to the machine's roles, `os` and `arch` |

```
ansible-playbook -i ./inventory fleet/github-runners.yml -e github_runner_url=https://github.com/OWNER/REPO -e github_runner_scope=repo -K
```

**The token.** The play reads `GITHUB_RUNNER_TOKEN` on the control node with `nova-secrets exec --only
GITHUB_RUNNER_TOKEN` and hands it to the runner on its standard input: never an argument or an environment variable on the host (`ps` shows
neither), never the inventory, never a file. Put it in the store the hour you run (a registration token expires in an hour; set `NOVA_SECRETS_STORE` and
`NOVA_SECRETS_KEY` as `docs/SPEC-SECRETS.md` says, or pass `-e '{"github_runner_secrets_args":["--store","S","--key","K"]}'`):

```
gh api -X POST repos/OWNER/REPO/actions/runners/registration-token -q .token | nova-secrets set GITHUB_RUNNER_TOKEN
```

For an organization the path is `orgs/ORG/actions/runners/registration-token`. The token is read only when a runner
needs registering, and never under `--check`.

**What it does.** Per runner: the pinned release, checked against GitHub's published digest, unpacked in
`<home>/actions-runner/<owner>-<repo>-<i>` (Windows `C:\actions-runner`); registered as `<machine>-<i>` unless
`.runner` is already there; one service per runner, enabled at boot (systemd unit on Linux). The labels are fixed at
registration: change them and the play says `LABEL-DRIFT` and leaves the runner alone. A changed unit restarts its
runner, which ends a job running on it. A machine whose count drops has its highest-numbered runners retired, and that removes them from GitHub, which needs a removal token
(`.../actions/runners/remove-token`, stored the same way; `github_runner_deregister` defaults to true).
A host in the `runner` group with no `runners` value and no `-e runner_count=N` is refused (never read as 0, which would retire its runners); 0 is an explicit choice. `-e runner_count=N` overrides the row for one run; `github_runner_version` and `github_runner_sha256` move the pin.

**The check.** A second run changes nothing, and `--check --diff` prints no change:

```
ansible-playbook -i ./inventory fleet/github-runners.yml -e github_runner_url=... -e github_runner_scope=repo -K --check --diff
```

**The rollback.** Retire every runner of that url on the machines: services stopped and removed, runners removed from
GitHub, directories deleted. Store a removal token first (`.../actions/runners/remove-token`, same `nova-secrets set`);
with `-e github_runner_deregister=false` no token is read and the runner entry stays in GitHub for you to delete.

```
ansible-playbook -i ./inventory fleet/github-runners.yml -e github_runner_url=... -e github_runner_scope=repo -e github_runner_state=absent -K
```

## Reservations

`nova-reserve` runs a command holding a reservation of one of N slots on the machine, so builds, tests and benches queue instead of
timing each other out. It is the same tool on Linux, macOS and Windows: OS file locks in one directory
(`$NOVA_RESERVE_DIR`, else `/var/tmp/nova-reserve`; `C:\ProgramData\nova-reserve` on Windows; a local disk), freed by the kernel when a holder
dies. A machine has one capacity, N slots, stored in the directory; `docs/SPEC-RESERVE.md` has the rule.

```
nova-reserve --wait 15m -- go test ./...
nova-reserve --exclusive -- ./bench
nova-reserve hold --for 2h &        # prints HELD, holds until SIGTERM or --for
nova-reserve status
nova-reserve set --slots 3           # refused while a slot is held
```

Every caller on a machine uses the same directory. `reserve/README.md` has the flags, exit codes and the TLA+ models;
`docs/SPEC-RESERVE.md` (lands with the reserve PR) specifies the reservations.

## Not yet here

What the owner wants in `fleet/` that does not exist yet. None of it is written, and none of it is promised by the
files above:

- **ssh setup and hardening role.** Keys, `sshd` settings, per-machine access, applied and checked from the inventory.
- **tailscale role.** Install and join each machine to the fleet's network, with the host name from the machine row.
- **Health and self-heal role.** Checking each machine (disk, load, services alive, tools at the right version) and
  repairing what can be repaired without a person.
