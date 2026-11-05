# The fleet

`fleet/` is how the fleet is managed: which machines there are and what they run (nova-config), the loops they keep
running (`loops.yml`), the nova-tools release each one holds (`nova-tools-install.yml`), and the load slots that keep
builds and tests from piling onto one box (`nova-slot`). It is Ansible plus two Go tools, nothing else; nothing here
names a machine, a seat or a person. Those are rows in nova-config.

```
fleet/
  nova-tools-install.yml            install one release, by tag
  roles/nova-tools-install/         the role the play runs
  loops.yml                         one launchd or systemd unit per loop row
  templates/nova-loop.*.j2          the unit templates
  group_vars/all.yml                the defaults both plays read
  retired-tools.txt                 binaries nova-tools shipped and ships no more
slot/                               nova-slot, the load slots (Go)
```

## The machine and loop model

nova-config (`config/README.md`) keeps the fleet as rows in PostgreSQL, each write with a history row. Three kinds
matter here:

- `machine`: a host, its login `user`, its `seat`, how many load `slots` it has, its `roles`.
- `loop`: a command one machine runs forever or every n seconds: `machine`, `argv` (a JSON array), `seat`, `keys`
  (secret names, never values), `every` or `keepalive`, `enabled`.
- `fleet`: the one fleet row (`loops_dir`, `bus`, and the store endpoints).

`nova-config inventory` prints them as an Ansible dynamic inventory: each machine is a host, its loop rows are the
host variable `nova_loops`. Use it as an inventory script:

```
printf '#!/bin/sh\nexec nova-config inventory "$@"\n' > inventory && chmod +x inventory
export ANSIBLE_INVENTORY_UNPARSED_FAILED=true
```

The plays take their inventory from that and nothing else, so a change to the fleet is a nova-config write followed
by a play run. Defaults for the plays are in `fleet/group_vars/all.yml`; a host's variables from the inventory, a
`group_vars/<group>.yml` or a `host_vars/<machine>.yml` beside the inventory override them.

## Installing a release

A release is a git tag `v*` on this repository. `.github/workflows/release.yml` builds the five tools (nova-bus,
nova-config, nova-sandbox, nova-secrets, nova-slot) for linux/amd64, linux/arm64, darwin/arm64 and windows/amd64 with
`CGO_ENABLED=0 -trimpath`, names each asset `<tool>_<tag>_<os>_<arch>[.exe]` (for example
`nova-slot_v1.0.0_linux_amd64`), writes one `SHA256SUMS`, and creates the GitHub release with the workflow's own
`GITHUB_TOKEN`. Nothing else holds a credential.

```
ansible-playbook -i ./inventory fleet/nova-tools-install.yml -e nova_tools_version=v1.0.0 -l <machine> --check --diff
ansible-playbook -i ./inventory fleet/nova-tools-install.yml -e nova_tools_version=v1.0.0 -l <machine>
```

The role reads the release's `SHA256SUMS`, downloads each asset and checks it against its digest before it is put in
place. A binary that already holds the digest is left alone, so a second run changes nothing and `--check --diff` is
clean. There is no default version: a run never installs "latest" by accident. A platform the release does not ship
is refused before anything is written.

| Variable | Default | |
|---|---|---|
| `nova_tools_version` | none, required | the tag, `vX.Y.Z` |
| `nova_tools_names` | the five tools | which tools to install |
| `nova_bin_dir` | `~/.local/bin` | where they go (Windows: `<profile>\.local\bin`) |
| `nova_tools_release_url` | the GitHub releases of this repository | `<base>/<tag>/<asset>` |
| `nova_tools_restart`, `nova_tools_restart_commands` | false, empty | argv lists run once, only if a binary changed |
| `nova_tools_remove_enabled` | false | retire the binaries below |
| `nova_tools_remove` | the retired list | names to retire |

**The retire list.** With `-e nova_tools_remove_enabled=true`, after a successful install each name in
`nova_tools_remove` found in the bin directory is moved to `<nova_bin_dir>/retired-<date>/`, never deleted; a name
not there is skipped, so this is idempotent too. A name that is also being installed is refused. The default list is
the tools nova-tools shipped and ships no more (`fleet/retired-tools.txt`, with the date and where each went):
nova-cairn, nova-card, nova-work, nova-sprint, nova-check, nova-ci, nova-decide, nova-doctor, nova-friend, nova-fuse,
nova-hook, nova-local, nova-memory, nova-redis, nova-self-talk, nova-swarm, nova-table, nova-tokens, nova-up,
nova-update, nova-version, nova-loop. Retiring is for Unix machines; on Windows it is skipped.

## loops.yml

One supervised unit per loop row, on the machine the row names: a LaunchAgent (or LaunchDaemon) on macOS, a systemd
user unit (or system unit) on Linux, with a `.timer` beside the service for a row that runs every n seconds.

```
ansible-playbook -i ./inventory fleet/loops.yml --check --diff
ansible-playbook -i ./inventory fleet/loops.yml
```

The unit runs the row's `argv` as given; a bare program name is the installed tool in `nova_bin_dir`, and `~/` is the
login's home. A row that names secrets (`keys`) is wrapped in `nova-secrets exec --only <names> --` (with the seat,
store, key and sops it needs, from `group_vars/all.yml`), so the values reach the command and never the unit file.
`enabled false` writes the unit and does not load it.

- Every unit carries a mark. A marked unit no row names, and a marked unit in the other place (user versus system), is
  retired: unloaded and removed. `--check` lists each as `WOULD-RETIRE`. A unit of another tool is left alone and
  named in a `NOTE`; `nova_retire_units` names any to retire.
- A unit whose file changed is restarted (`RESTART`, `WOULD-RESTART` under `--check`).
- `-e nova_loop_only=a,b` renders and restarts only those rows and retires nothing. `-e nova_loop_restart=a,b`
  restarts those rows even if their unit did not change; this is how a hung loop is restarted, never by hand. A name
  no row carries is refused before anything is written.
- On Linux a changed unit that was not restarted fails the play; the receipt line `LOOPS host=...` ends each host.

## Load slots

`nova-slot` runs a command holding one of N load slots on the machine, so builds, tests and benches queue instead of
timing each other out. It is the same tool on Linux, macOS and Windows: OS file locks in one directory
(`$NOVA_SLOT_DIR`, else `/tmp/nova-slot`; `C:\ProgramData\nova-slot` on Windows), freed by the kernel when a holder
dies. N defaults to the CPU count divided by 4; a machine's `slots` row says what it should be.

```
nova-slot --slots 3 --wait 15m -- go test ./...
nova-slot --exclusive -- ./bench
nova-slot hold --slots 3          # prints HELD, holds until stdin closes
nova-slot status
```

Every caller on a machine uses the same directory. `slot/README.md` has the flags, exit codes and the TLA+ models.

## Not yet here

What the owner wants in `fleet/` that does not exist yet. None of it is written, and none of it is promised by the
files above:

- **ssh setup and hardening role.** Keys, `sshd` settings, per-machine access, applied and checked from the inventory.
- **tailscale role.** Install and join each machine to the fleet's network, with the host name from the machine row.
- **Health and self-heal role.** Checking each machine (disk, load, loops alive, tools at the right version) and
  repairing what can be repaired without a person. The disk guard that used to be a loop here has moved to
  nova-sprint.
- **GitHub CI runner role.** Install, register and update the self-hosted runners that `.github/workflows/ci.yml`
  runs on, from the machine rows (`runners`).

Until they exist, those are done by hand, and the hand work is the thing to automate next.
