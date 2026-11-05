# The fleet

`fleet/` is how the fleet is managed: which machines there are and what they run (nova-config), the loops they keep
running (`loops.yml`), the nova-tools release each one holds (`nova-tools-install.yml`), and the reservations that keep
builds and tests from piling onto one box (`nova-reserve`). It is Ansible plus two Go tools, nothing else; nothing here
names a machine or a person. Those are rows in nova-config.

```
fleet/
  nova-tools-install.yml            install one release, by tag
  roles/nova-tools-install/         the role the play runs
  loops.yml                         one launchd or systemd unit per loop row
  templates/nova-loop.*.j2          the unit templates
  group_vars/all.yml                the generic defaults both plays read
  retired-tools.txt                 binaries nova-tools shipped and ships no more
  requirements.yml                  the Ansible collection a Windows host needs
  ci/inventory.yml                  the stub inventory CI checks loops.yml against
reserve/                            nova-reserve, reservations on a machine (Go)
```

## The machine and loop model

nova-config (`config/README.md`) keeps the fleet as rows in PostgreSQL, each write with a history row. Two kinds
matter here:

- `machine`: a host, its login `user` and its `roles`. The role names are the inventory's groups: `bench`, `runner`
  and `services`, so `-l bench` runs a play on every bench.
- `loop`: a command one machine runs forever or every n seconds: `machine`, `argv` (a JSON array), `keys` (secret
  names, never values; a row with keys also names the `seat` that opens them), `every` (seconds; none means kept
  alive), `enabled`, `env` (an object of environment variables) and `log` (a path, default
  `~/.local/state/nova/loops/<name>.log`).

`nova-config inventory` prints them as an Ansible dynamic inventory: each machine is a host in the groups its roles
name, and its loop rows are the host variable `nova_loops`. It always emits `nova_loops`; an empty list is valid and
means no loops. Use it as an inventory script, and make a failing script fail loudly (Ansible otherwise falls back
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
nova_loop_path: "{{ nova_bin_dir }}:/usr/local/bin:/usr/bin:/bin"
```

A host variable from the inventory, a `group_vars/<group>.yml` or a `host_vars/<machine>.yml` beside the inventory
overrides `fleet/group_vars/all.yml`; so does `-e name=value` on the command line. A `group_vars/all.yml` beside the
inventory does not, because the playbook's own is read after it: use a group name instead. Defaults:

| Variable | Default |
|---|---|
| `nova_home` | the login's home |
| `nova_bin_dir` | `~/.local/bin` |
| `nova_loop_path` | `<nova_bin_dir>` then the usual system directories |
| `nova_secrets_store` | `~/.local/share/nova/secrets` |
| `nova_secrets_key_dir` | `~/.config/nova-secrets` |
| `nova_sops` | `/opt/homebrew/bin/sops` on macOS, else `<nova_bin_dir>/sops` |
| `nova_launchd_domain` | `auto` (`gui`, `system`) |
| `nova_systemd_scope` | `user` (`system`) |

## Installing a release

A release is a git tag `v*` on this repository. `.github/workflows/release.yml` builds the five tools (nova-bus,
nova-config, nova-sandbox, nova-secrets, nova-reserve) for linux/amd64, linux/arm64, darwin/arm64 and windows/amd64 with
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
| `nova_tools_names` | the five tools | which tools to install (and which `argv[0]` loops.yml prefixes) |
| `nova_bin_dir` | `~/.local/bin` | where they go (Windows: `<profile>\.local\bin`) |
| `nova_tools_release_url` | the GitHub releases of this repository | `<base>/<tag>/<asset>` |
| `nova_tools_restart`, `nova_tools_restart_commands` | false, empty | argv lists run once, only if a binary changed |
| `nova_tools_remove_enabled` | false | retire the binaries below |
| `nova_tools_remove` | the retired list | names to retire |

**The retire list.** With `-e nova_tools_remove_enabled=true`, after a successful install each name in
`nova_tools_remove` found in the bin directory is moved to `<nova_bin_dir>/retired-<date>/`, never deleted; a name
not there is skipped, so this is idempotent too. A name that is also being installed is refused. The default list is
the rows of `fleet/retired-tools.txt` that say `retired` or `rowan-tools`: nova-cairn, nova-check, nova-ci,
nova-decide, nova-doctor, nova-fuse, nova-local, nova-loop, nova-memory, nova-redis, nova-self-talk, nova-slot,
nova-table, nova-tokens, nova-up, nova-update, nova-version. Another product's binaries (nova-sprint, nova-card,
nova-work, nova-swarm, nova-hook, nova-friend) are never in it. An operator who wants more gone names the whole list:

```
-e nova_tools_remove_enabled=true -e nova_tools_remove='["nova-sprint","nova-card"]'
```

Retiring is for Unix machines; on Windows it is skipped.

## loops.yml

One supervised unit per loop row, on the machine the row names: a LaunchAgent (or LaunchDaemon) on macOS, a systemd
user unit (or system unit) on Linux, with a `.timer` beside the service for a row that runs every n seconds.

```
ansible-playbook -i ./inventory fleet/loops.yml --check --diff
ansible-playbook -i ./inventory fleet/loops.yml
```

The unit runs the row's `argv` exactly. If `argv[0]` is one of `nova_tools_names`, it is prefixed with
`nova_bin_dir`; no other word is changed. The row
`{"argv":["nova-bus","hook","--as","reader"],"env":{"NOVA_BUS_REDIS":"redis-host:6379"}}` runs
`<nova_bin_dir>/nova-bus hook --as reader` with `NOVA_BUS_REDIS=redis-host:6379` in its environment. The unit's
`PATH` includes `nova_bin_dir`, and each `env` entry is a unit environment variable (an `Environment=` line on Linux,
an `EnvironmentVariables` entry on macOS); a row's `env` wins over `HOME` and `PATH`. Output is appended to the row's
`log`, whose directory the play creates.

A row whose `keys` is not empty is wrapped in
`nova-secrets exec --store <store> --as <seat> --key <key_dir>/<seat>.key --sops <sops> --only <keys> --`, so the
values reach the command and never the unit file. `<seat>` and `<keys>` are the row's; `<store>`, `<key_dir>` and
`<sops>` are `nova_secrets_store`, `nova_secrets_key_dir` and `nova_sops` (see the table above). A row with no keys
runs its `argv` unchanged. Rows without `every` are kept alive; `enabled` defaults to true. `enabled false` writes
the unit and does not load it.

- Every unit carries a mark. A marked unit no row names, and a marked unit in the other place (user versus system), is
  retired: unloaded and removed. `--check` lists each as `WOULD-RETIRE`. A unit of another tool is left alone and
  named in a `NOTE`; `nova_retire_units` names any to retire.
- A unit whose file changed is restarted (`RESTART`, `WOULD-RESTART` under `--check`).
- `-e nova_loop_only=a,b` renders and restarts only those rows and retires nothing. `-e nova_loop_restart=a,b`
  restarts those rows even if their unit did not change; this is how a hung loop is restarted, never by hand. A name
  no row carries is refused before anything is written.
- On Linux a changed unit that was not restarted fails the play; the receipt line `LOOPS host=...` ends each host.

## Reservations

`nova-reserve` runs a command holding a reservation of one of N slots on the machine, so builds, tests and benches queue instead of timing each other out. It is the same tool on Linux, macOS and Windows: OS file locks in one directory
(`$NOVA_RESERVE_DIR`, else `/tmp/nova-reserve`; `C:\ProgramData\nova-reserve` on Windows), freed by the kernel when a holder
dies. N defaults to the CPU count divided by 4.

```
nova-reserve --slots 3 --wait 15m -- go test ./...
nova-reserve --exclusive -- ./bench
nova-reserve hold --slots 3          # prints HELD, holds until stdin closes
nova-reserve status
```

Every caller on a machine uses the same directory. `reserve/README.md` has the flags, exit codes and the TLA+ models;
`docs/SPEC-RESERVE.md` specifies the reservations.

## Not yet here

What the owner wants in `fleet/` that does not exist yet. None of it is written, and none of it is promised by the
files above:

- **ssh setup and hardening role.** Keys, `sshd` settings, per-machine access, applied and checked from the inventory.
- **tailscale role.** Install and join each machine to the fleet's network, with the host name from the machine row.
- **Health and self-heal role.** Checking each machine (disk, load, loops alive, tools at the right version) and
  repairing what can be repaired without a person.
- **GitHub CI runner role.** Install, register and update the self-hosted runners that `.github/workflows/ci.yml`
  runs on, from the machine rows (the `runner` group).

Until they exist, those are done by hand, and the hand work is the thing to automate next.
