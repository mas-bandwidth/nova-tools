# The fleet

<<<<<<< HEAD
`fleet/` is how the fleet is managed: which machines there are and what they run (nova-config), how a new machine
joins (`join.yml`, `tailscale.yml`, `seat.yml`), the stores the tools talk to (`redis.yml` for the bus, `postgres.yml`
for nova-config), the services machines keep running (`services.yml`), the secrets clone they keep current
(`secrets-refresh.yml`), the guards that keep a machine healthy (`health.yml`), the nova-tools release each one holds
(`nova-tools-install.yml`), the GitHub Actions runners on them (`github-runners.yml`), and the reservations that keep
builds and tests from piling onto one box (`nova-reserve`). It is Ansible plus two Go tools, nothing else; nothing here
names a machine or a person. Those are rows in nova-config and the variables you give a run.

```
fleet/
  join.yml                          a new machine: its row, its login, the tailnet, the tools a seat needs, its seat
  tailscale.yml                     Tailscale installed and up, the MagicDNS name as the address, the firewall
  tailscale-acl.json                a starter tailnet policy to paste into the admin console
  seat.yml                          the seat key made on the machine, its rule added to the secrets store
  redis.yml                         the bus's Redis: ACL users, passwords made on the machine, live ACL changes
  postgres.yml                      nova-config's PostgreSQL: roles, grants, nightly dump
  secrets-refresh.yml               each machine's secrets clone pulled on a timer
  health.yml                        journal on disk, panic reboot, killable ssh sessions, log and memory ceilings, disk report
  nova-tools-install.yml            install one release, by tag
  services.yml                      one launchd or systemd unit per service row
  github-runners.yml                GitHub Actions runners on the runner group
  roles/                            the roles the plays run: one per play, github-runner, seal and preflight
  templates/nova-service.*.j2       the unit templates of services.yml
  group_vars/all.yml                the generic defaults the plays share
  retired-tools.txt                 binaries nova-tools shipped and ships no more
  requirements.yml                  the Ansible collection a Windows host needs
  ci/inventory                      the stub inventory CI checks the plays against
reserve/                            nova-reserve, reservations on a machine (Go)
```

Every play is idempotent: run it twice and the second run changes nothing, so `--check --diff` after a run is clean.
Every play prints one receipt line per machine (`JOIN ...`, `TAILNET ...`, `SEAT ...`, `REDIS ...`, `PG ...`,
`SECRETS-REFRESH ...`, `HEALTH ...`) that says what it found and what it did; under `--check` the receipt ends
`(check: nothing changed)` and says `WOULD-...` for each change a run would make. A secret is never a variable: it
reaches a run as an environment variable from `nova-secrets exec` on the control node, and a machine only on a
command's stdin.

Tailscale, Redis, PostgreSQL and Ansible are the ground every play stands on. No play offers a path without any of
them: the machines meet over the tailnet, the bus runs on the Redis `redis.yml` keeps, the fleet's rows live in the
PostgreSQL `postgres.yml` keeps, and the plays are how all of it is applied.

## Words used here

- **The control node** is the one machine you run the plays from. It holds the Ansible checkout, the nova-tools
  binaries and a seat of its own, and it reaches every machine.
- **The secrets store** is a git repository of sops-encrypted files, one per seat (`docs/SPEC-SECRETS.md`). `S` in
  a command is the path of your clone of it on the control node.
- **A seat** is a name in the store with an age key that opens it. `SEAT` is the seat's name and `K` its key file
  (`~/.config/nova-secrets/<seat>.key`). Every machine has a seat; the control node's holds the values the plays
  need (`TAILSCALE_AUTHKEY`, `NOVA_PG_PASSWORD`).
- **Sealing** a password puts it into a seat: the play reads the password file it made on the machine and hands it to
  `nova-secrets set` on the control node on stdin. The value is never on a command line, in a log or in a file in
  the clear. `redis.yml` and `postgres.yml` seal when given `nova_seal_store` (S), `nova_seal_seat` (SEAT) and
  `nova_seal_key` (K): all three, or none.
- **The bus** is `nova-bus`, the tools' message channel: Redis streams and presence keys under `bus:` on the Redis
  that `redis.yml` keeps. A client names the Redis with `NOVA_BUS_REDIS=<host>:<port>` and logs in with
  `NOVA_BUS_USER` and `NOVA_BUS_PASSWORD`.
- **The config store** is the PostgreSQL that `postgres.yml` keeps and nova-config reads. `<pg-host>` is its
  machine's address (its MagicDNS name once it is on the tailnet). nova-config finds it by `NOVA_PG_DSN` and logs in
  with `NOVA_PG_PASSWORD`.

## Prerequisites

The plays need nova-tools v2.0.0-rc1 or newer: they use `nova-secrets set`, `nova-secrets names` and the `seat add`
of v2. A v1 release has neither. Each play checks this before it changes anything: a nova-tools program whose `help`
lacks the v2 verbs (`nova-secrets set`, `nova-config schema`, `nova-bus who`) stops the run with `PREFLIGHT STOP
<program> is older than v2.0.0-rc1`.

| Where | What | Which plays |
|---|---|---|
| control node | `ansible-core` 2.15 or newer (2.15 and 2.21 are run), Python 3 | all |
| control node | `nova-config`, `nova-secrets` (v2), `sops`, `age-keygen`, `git` | join, tailscale, seat, redis and postgres (sealing) |
| control node | `rsync`, and the `ansible.posix` collection (`ansible-galaxy collection install -r fleet/requirements.yml`) | join, secrets-refresh (the push of the store) |
| control node | `gpg` | postgres.yml with `nova_pg_source` `pgdg` or `auto` (it checks the repository key) |
| control node | `tailscale`, on the same tailnet as the machines, tagged `tag:control` | join.yml's check, every run over the tailnet |
| control node | `ssh-keyscan` and `ssh-keygen` (OpenSSH) | join, tailscale: the machine's host key pinned into `~/.ssh/known_hosts` |
| control node | `psql` | reading the config store by hand; no play uses it |
| machine | Debian 12 or newer, or Ubuntu 22.04 or newer, with `sudo` and `python3` | all |
| machine | `rsync` | secrets-refresh.yml in push mode (join.yml installs it on Debian and Ubuntu; macOS has it) |
| machine | `git` | secrets-refresh.yml in pull mode only (it installs it on Debian and Ubuntu; on macOS it comes with the Command Line Tools) |
| machine | `age-keygen`, `sops` | seat.yml (join.yml installs both on Linux; on macOS `brew install age sops`) |

Each play checks the control node first. A missing or too-old program stops the run with `PREFLIGHT STOP` and its
name, before anything changes. The variables `nova_control_config`, `nova_control_secrets` and `nova_control_sops`
point at a program that is not on PATH.

Host keys are pinned, never skipped. At the first connection to a machine (join.yml, or tailscale.yml run alone) the
control node runs `ssh-keyscan` for its ed25519 key and puts it in `~/.ssh/known_hosts` under the address given; once
tailscale.yml records the MagicDNS name it pins that name too (Tailscale SSH serves its own host key under it). No
play sets `StrictHostKeyChecking=no`; leave `host_key_checking` at its default in your `ansible.cfg`. A name already in
`known_hosts` is left as it is, so a changed key (a rebuilt machine) is refused by ssh until you remove the old line
with `ssh-keygen -R <name>`.

## The first machine

join.yml and tailscale.yml write the machine's row into the config store, so the first machine, the one that will
hold the config store, goes up in this order. Each step runs from the control node, with a one-machine inventory
that names the machine (`-i 'db-1,'`) and its address and login (`-e ansible_host=203.0.113.7 -e ansible_user=root`).

1. On the control node: the store and its seat. Run `nova-secrets init --store S --recovery <age public key>`, make
   K with `age-keygen -o K`, run `nova-secrets seat add SEAT "$(age-keygen -y K)" --store S`, then put the Tailscale
   auth key in it: `nova-secrets set TAILSCALE_AUTHKEY --store S --key K --as SEAT` (the key on stdin).
2. The tailnet, without the row: `nova-secrets exec --only TAILSCALE_AUTHKEY --store S --key K --as SEAT --
   ansible-playbook -i 'db-1,' -e ansible_host=<public address> -e ansible_user=root fleet/tailscale.yml
   -e nova_tailscale_record_address=false -e nova_firewall=false`.
3. The config store and the bus: `ansible-playbook -i 'db-1,' -e ansible_host=<public address> -e ansible_user=root
   fleet/postgres.yml -e nova_store_hosts=db-1 -e nova_seal_store=S -e nova_seal_seat=SEAT -e nova_seal_key=K`, then
   the same with `fleet/redis.yml`. Both listen on the machine's tailnet address. postgres.yml loads nova-config's
   tables and seals `NOVA_PG_PASSWORD` into SEAT; redis.yml seals `NOVA_BUS_PASSWORD`. Then run `git -C S push`.
4. The machine joins like every other one: `join.yml` (below), under `nova-secrets exec --only
   TAILSCALE_AUTHKEY,NOVA_PG_PASSWORD`, with `NOVA_PG_DSN=postgres://nova_config@db-1.<tailnet>.ts.net:5432/nova`,
   `-e nova_join_address=<public address>` and `-e nova_join_roles=services`. The tailnet step finds it Running and
   records its MagicDNS name as its address, and the firewall closes the public address.

Every other machine starts at step 4. Its `nova_join_address` is the address the control node first reaches it on as
root; the tailnet step replaces it with the machine's MagicDNS name, and from then on the plays log in over Tailscale
SSH as the row's user (the policy lets no one in as root over the tailnet).

## The machine and service model
=======
Install a chosen release and keep recurring commands running on your machines.
Use [nova-config](../config/README.md) for the inventory, or supply your own.
Use [nova-reserve](../reserve/README.md) when jobs need to share capacity.

A machine can keep a program running or run it every N seconds. Config calls
that program a **service**. Its row holds the program, arguments, environment,
log path and choice of continuous or scheduled running.

## Before you start
>>>>>>> 961730a02 (docs: human-readable per-tool docs (docs/v2 rebased onto main))

Start with hosts that have an operating system, SSH access and the network
connections your commands use.

Install Ansible on the control node. For Windows targets, install the collection
with `ansible-galaxy collection install -r fleet/requirements.yml` and configure
the host connection. Unix targets do not need the Windows collection.

If you use config inventory, provide PostgreSQL with the config schema and
keep its login credentials on the control node. See
[SPEC-CONFIG](SPEC-CONFIG.md#connect-and-create-the-schema).

`services.yml` manages systemd units on Linux and launchd jobs on macOS.

A program using secrets needs an encrypted store, its seat's private key and
sops on that host. Install those prerequisites before applying `services.yml`.
Use age-keygen where you create keys.

## Inventory

Machine rows hold connection settings, OS, architecture, core and runner
counts, roles and notes. Program rows hold command arguments, environment variables,
log paths, secret names and scheduling settings. These are values you set,
not facts gathered from hosts.

The groups are `all`, `bench`, `services` and `runner`. Every machine has
a `nova_services` list, even when empty.

With config and its database connection available on the control node:

```sh
NOVA_INVENTORY=$(mktemp)
printf '#!/bin/sh\nexec nova-config inventory "$@"\n' > "$NOVA_INVENTORY"
chmod +x "$NOVA_INVENTORY"
export ANSIBLE_INVENTORY_UNPARSED_FAILED=true
"$NOVA_INVENTORY" --list
```

<<<<<<< HEAD
The plays take their inventory from that and nothing else, so a change to the fleet is a nova-config write followed
by a play run. `ansible-galaxy collection install -r fleet/requirements.yml` installs `ansible.posix` (the push of
the secrets store) and `ansible.windows` (only a Windows host in the inventory needs it).

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
| `nova_system` | the gathered fact (`ansible_facts['system']`); `-e nova_system=Darwin` or `Linux` forces the platform |
=======
Inspect that output before applying a play. If you supply inventory yourself,
include `nova_services` on every host selected for `services.yml`. An absent list
is not treated as empty.

Defaults are in [group_vars/all.yml](../fleet/group_vars/all.yml).
Host variables and group-specific variables beside your inventory can override
them. An inventory-adjacent `group_vars/all.yml` does not override the
playbook's file of the same name, which Ansible reads later. Use host variables,
group-specific variables or explicit extra variables for those overrides.
>>>>>>> 961730a02 (docs: human-readable per-tool docs (docs/v2 rebased onto main))

## Installing a release

Choose a published tag and a host selection. Set `NOVA_TOOLS_VERSION` and
`NOVA_HOST_LIMIT` to those choices, then check the plan before applying it:

<<<<<<< HEAD
```
ansible-playbook -i ./inventory fleet/nova-tools-install.yml -e nova_tools_version=v2.0.0-rc1 -l <machine> --check --diff
ansible-playbook -i ./inventory fleet/nova-tools-install.yml -e nova_tools_version=v2.0.0-rc1 -l <machine>
=======
```sh
ansible-playbook -i "${NOVA_INVENTORY:?set the inventory script}" fleet/nova-tools-install.yml \
  -e "nova_tools_version=${NOVA_TOOLS_VERSION:?set a published release tag}" \
  -l "${NOVA_HOST_LIMIT:?select the hosts}" --check --diff
ansible-playbook -i "${NOVA_INVENTORY:?set the inventory script}" fleet/nova-tools-install.yml \
  -e "nova_tools_version=${NOVA_TOOLS_VERSION:?set a published release tag}" \
  -l "${NOVA_HOST_LIMIT:?select the hosts}"
>>>>>>> 961730a02 (docs: human-readable per-tool docs (docs/v2 rebased onto main))
```

The role reads `SHA256SUMS` from that release, including in check mode.
It refuses unsupported platforms or missing asset digests. Downloads are
checked against those digests. A matching installed binary is left alone.

On a host without a bin directory, check mode reports the directory change
and skips the downloads. Applying the play creates the directory and installs
the binaries. A check run does not establish that installation succeeded.

The supported release platforms are Linux amd64/arm64, macOS arm64 and
Windows amd64. Asset names are `<tool>_<tag>_<os>_<arch>[.exe]`.
The Windows sandbox binary has no enforcement backend.

| Variable | Default | Effect |
|---|---|---|
| `nova_tools_version` | Required | Choose a release tag. There is no implicit latest release. |
| `nova_tools_names` | Release tools | Select the binaries to install. See [group_vars/all.yml](../fleet/group_vars/all.yml) for the list. |
| `nova_tools_bin_dir` | `nova_bin_dir`, else the login's `~/.local/bin` | Unix destination. |
| `nova_tools_win_bin_dir` | `<profile>\.local\bin` | Windows destination. |
| `nova_tools_release_url` | This repository's GitHub release downloads | Source of checksums and assets. |
| `nova_tools_restart`, `nova_tools_restart_commands` | false, empty | Run supplied argument lists after a binary changes. |
| `nova_tools_remove_enabled`, `nova_tools_remove` | false, retired-name list | Move selected old Unix binaries aside after installation. |

Review `nova_tools_remove` in the [role defaults](../fleet/roles/nova-tools-install/defaults/main.yml)
before enabling removal. It selects the `retired` and `rowan-tools` destinations
from [retired-tools.txt](../fleet/retired-tools.txt). Commands that moved to
nova-sprint are excluded from the default list.

On Unix, selected files move to `<bin>/retired-<date>/` after a successful
installation. Absent files are skipped. A name selected for both installation
and retirement is refused. Windows skips retirement.

To choose a different list, set `nova_tools_remove` explicitly. Review it against
the programs the selected machines use. Restarts and retirement are opt-in;
their defaults are false.

## Running programs

`services.yml` writes systemd services on Linux and launchd units on macOS.
Periodic Linux rows also get a systemd timer.

```sh
ansible-playbook -i "${NOVA_INVENTORY:?set the inventory script}" fleet/services.yml \
  -l "${NOVA_HOST_LIMIT:?select the hosts}" --check --diff
ansible-playbook -i "${NOVA_INVENTORY:?set the inventory script}" fleet/services.yml \
  -l "${NOVA_HOST_LIMIT:?select the hosts}"
```

The row's `argv` supplies the command and its arguments. If its first word is
in `nova_tools_names`, the play prefixes it with `nova_bin_dir`. Other program
names and arguments pass through. On macOS, a bare system program is resolved
with `command -v` using the unit's PATH and written as an absolute path.

Units run from the login's home. They receive HOME and `nova_service_path` as PATH,
then the row's `env` values. A row can override HOME or PATH. Use string values,
such as `{"REVIEW_MODE":"true"}`. Secrets belong in the row's `keys`, not `env`.

For example, this row runs the installed bus hook with its Redis address:

```json
{
  "name": "reader",
  "argv": ["nova-bus", "hook", "--as", "reader"],
  "env": {"NOVA_BUS_REDIS": "redis-host:6379"},
  "keepalive": true
}
```

Stdout and stderr append to the row's `log` on both platforms. The default is
`~/.local/state/nova/services/<name>.log`. A leading `~/` in the log path expands
to the login's home, and the play creates its parent directory.

A row with secret names is wrapped in `nova-secrets exec --only ...`.
The values do not appear in the generated unit. These defaults select the
store and supervision mode:

| Variable | Default |
|---|---|
| `nova_secrets_store` | `<home>/.local/share/nova/secrets` |
| `nova_secrets_key_dir` | `<home>/.config/nova-secrets`; keys are `<seat>.key` |
| `nova_sops` | Linux: `<nova_bin_dir>/sops`; macOS: `/opt/homebrew/bin/sops` |
| `nova_launchd_domain` | `auto`: GUI agent when available, otherwise a system daemon running as the login user |
| `nova_systemd_scope` | `user`; `system` runs a system unit as the login user |

An `enabled false` row writes a disabled unit. A changed enabled unit is
restarted. Units marked by this play are unloaded and removed when no row
names them, or when they are in the other user/system scope.

The first full run also retires marked units written by the former
`loops.yml`, before loading their replacements, so both versions do not run
at once. Old unit names are
`com.nova.loop.<name>.plist` and `nova-loop-<name>.service` or `.timer`.
New units use `service` in those names. Logs now default to
`~/.local/state/nova/services/<name>.log`; explicit row paths are retained.

Unmarked units remain unless named in `nova_retire_units`. A row with an
unmarked unit's name can take it over.

`nova_service_only=a,b` limits the run to those names and retires nothing.
`nova_service_restart=a,b` requests a restart even when the unit file is unchanged.
Unknown selected names are refused before writes. Check mode reports proposed
restarts and retirements.

Each host ends with a `SERVICES host=...` receipt. On Linux, a changed unit that
was not restarted fails the play.

## Sharing build capacity

Choose one reservation directory for callers on each machine, then store
its capacity while no reservation is held.

<<<<<<< HEAD
**The rollback.** Retire every runner of that url on the machines: services stopped and removed, runners removed from
GitHub, directories deleted. Store a removal token first (`.../actions/runners/remove-token`, same `nova-secrets set`);
with `-e github_runner_deregister=false` no token is read and the runner entry stays in GitHub for you to delete.

```
ansible-playbook -i ./inventory fleet/github-runners.yml -e github_runner_url=... -e github_runner_scope=repo -e github_runner_state=absent -K
```

## join.yml

A new machine, from nothing to a member: its nova-config row, its login, the tailnet, the tools a seat needs and its
seat, then a check that it is reachable. Name the machine with `-e nova_join_host`, never `-l`: a machine with no row
yet is in no inventory, so the first play adds the row and reads the inventory again.

1. **The row** (control node). `nova-config machine add` where the row is missing. A row that exists is never edited;
   one whose user is not `nova_join_user` stops the join by name.
2. **The login** (Linux, connecting as root to the address given, its host key pinned first). rsync (the control
   node pushes the secrets store with it), then the row's user: no password, in `nova_join_sudo_group` with
   passwordless sudo (`/etc/sudoers.d/90-nova-<login>`, checked by `visudo` before it lands), root's
   `authorized_keys` copied to it (or the control node's file `nova_join_authorized_keys` names), linger on, the
   hostname set to the machine name. A darwin machine has no root login: set its login and sudo up at its console.
3. **The tailnet**: `tailscale.yml` (below), connecting as the login. From here on the plays reach the machine as
   `<login>@<MagicDNS name>` over Tailscale SSH, and nothing reaches it as root again: the policy lets no root in over
   the tailnet, and the firewall closes the address given.
4. **The tools a seat needs** (as the login): `nova_bin_dir` on the login PATH; on Linux the pinned `age` and `sops`
   release binaries, checked against their published digests. A darwin machine takes them from Homebrew
   (`brew install age sops`).
5. **The seat**: `seat.yml` (below).
6. **The check**: the control node's `tailscale status` shows the machine as an online peer. The join ends there,
   with the seat's rule present. The bus is not checked here: the login has no nova-bus and no store until
   `nova-tools-install.yml` and `secrets-refresh.yml` have run; nova-fleet checks it after those.

A Windows machine gets 1 and 3.

```
nova-secrets exec --only TAILSCALE_AUTHKEY,NOVA_PG_PASSWORD --store S --key K --as SEAT -- \
  env NOVA_PG_DSN=postgres://nova_config@<pg-host>:5432/nova \
  ansible-playbook -i ./inventory fleet/join.yml --check --diff \
    -e nova_join_host=web-1 -e nova_join_user=fleet -e nova_join_address=203.0.113.7 \
    -e nova_join_os=linux -e nova_join_arch=amd64 -e nova_seal_store=$HOME/secrets
```

The same without `--check --diff` joins it.

**Resuming.** Run the same command again. A join that stopped before the tailnet step (the row, the login, or
`tailscale up` itself) starts over as root at the address given; every step is idempotent. A join that stopped after
it, or a machine joined already, is recognised by its row's address being the MagicDNS name: the login step is
skipped (`JOIN LOGIN <machine> joined already`), and the plays from the tailnet step on run as the login over the
tailnet. What cannot be resumed: a machine whose tailnet step recorded its name but whose login was never made (it
is made before the tailnet step, so this takes a hand edit of the row), and a run with
`nova_tailscale_record_address=false` after the firewall closed the address given.

| Variable | Default | |
|---|---|---|
| `nova_join_host` | none, required | the machine's name (lowercase, digits, `-`) |
| `nova_join_user`, `nova_join_address`, `nova_join_os`, `nova_join_arch` | none | the row's fields; required only when the row is missing |
| `nova_join_cores` | unset | the row's cores |
| `nova_join_roles` | `bench` | the row's roles (`bench`, `runner`, `services`) |
| `nova_join_actor` | `$USER` | nova-config's `--actor` |
| `nova_join_root_user` | `root` | the login a fresh Linux machine is reached with |
| `nova_join_sudo_group` | `sudo` | the group the login joins (`wheel` on Red Hat) |
| `nova_join_authorized_keys` | root's on the machine | a file on the control node to install instead |
| `nova_join_hostname` | `true` | set the hostname to the machine name |
| `nova_age_version`, `nova_sops_version` and their `*_sha256` | the pinned releases | |
| `nova_control_config`, `nova_control_secrets`, `nova_control_sops` | `nova-config`, `nova-secrets`, `sops` | the control node's binaries |

**Check.** The receipts `JOIN ROW`, `HOSTKEY`, `TAILNET`, `FIREWALL`, `JOIN TOOLS`, `SEAT` and `JOIN CHECK <machine>
peer=online seat=present`; a second run's `--check --diff` changes nothing. **Rollback.** `nova-config machine remove
<name> --actor <you>`, the device removed in the Tailscale admin console, the seat's rule reverted in the store
(`git revert`), and on the machine `userdel -r <login>` and `rm /etc/sudoers.d/90-nova-<login>`.

## tailscale.yml

Each machine on the tailnet, its address in nova-config its MagicDNS name, and its firewall closed to everything but
the tailnet. `join.yml` runs it; run it alone for machines that already have a row.

**What is yours, once, in the Tailscale admin console:**

1. The account and the tailnet. MagicDNS on (the default).
2. The policy: paste `fleet/tailscale-acl.json` into Access controls and put your account where it says
   `owner@example.com`. It declares `tag:nova`, `tag:runner` and `tag:control`. The fleet's machines reach each other,
   and the control node reaches them, on 22 (Tailscale SSH, as the non-root logins), 6379 (the bus's Redis) and 5432
   (the config store), and nothing else. A member's own device reaches no fleet machine.
3. An auth key: Settings, Keys, generate auth key, **reusable**, **pre-authorized**, **tagged** `tag:nova` (and
   `tag:runner`, for the runners). Keep it in the control node's seat: `nova-secrets set TAILSCALE_AUTHKEY --as SEAT
   --store S --key K` (the value on stdin).
4. The control node on the tailnet as `tag:control`: `sudo tailscale up --advertise-tags=tag:control` there, with a
   key tagged `tag:control` (a separate one-off key). It resolves the MagicDNS names and reaches the three ports.
5. On a Mac: open the Tailscale app once at the console and approve its VPN configuration.

**What the play does, per machine:**

1. Installs Tailscale: Tailscale's apt repository on Debian and Ubuntu (the same key and source line its own install
   script writes), Homebrew's cask on macOS, winget on Windows.
2. On a machine that is not Running: `tailscale up --reset --auth-key=file:/dev/stdin --hostname=<machine> --advertise-tags=tag:nova[,tag:runner] --ssh`
   (`--ssh` on Linux; the macOS and Windows clients do not serve Tailscale SSH). `tag:runner` is added for a machine in
   the `runner` group. The key comes from `TAILSCALE_AUTHKEY` in the control node's environment and reaches the
   machine on the command's stdin only (on Windows, which has no `/dev/stdin`, in a file only the login can read,
   removed after the command). A Running machine is never brought up again: a hostname or Tailscale SSH setting that
   differs is set in place with `tailscale set`, with no key, and tags that differ stop the run with `TAILNET STOP`
   and the command to retag it at its own console (a retag resets the node, which would cut a run that comes in over
   the tailnet).
3. Records `Self.DNSName` (`<machine>.<tailnet>.ts.net`) as the machine's address: `nova-config machine set <name>
   --address <dns name>`, pins that name's host key into the control node's `known_hosts`, then reads the inventory
   again, so the firewall play, every run after it, the bus and the services reach the machine by that name.
4. The firewall. Linux: ufw denies incoming on every interface but `tailscale0`, allows outgoing and UDP 41641
   (Tailscale's direct connections). It is turned on only when the run itself comes in on the tailnet (or is local),
   so it cannot cut its own connection; otherwise it stops with `FIREWALL HELD` and says why. macOS: the application
   firewall on (sudo: add `-K` where the login has no passwordless sudo). Windows is left as it is.

`redis.yml` and `postgres.yml` listen on the machine's tailnet address only, read from `tailscale ip -4`.

```
nova-secrets exec --only TAILSCALE_AUTHKEY,NOVA_PG_PASSWORD --store S --key K --as SEAT -- \
  env NOVA_PG_DSN=postgres://nova_config@<pg-host>:5432/nova \
  ansible-playbook -i ./inventory fleet/tailscale.yml -l web-1 --check --diff
```

| Variable | Default | |
|---|---|---|
| `nova_tailscale_tags` | `tag:nova`, plus `tag:runner` in the `runner` group | the tags the key must be allowed |
| `nova_tailscale_ssh` | `true` | Tailscale SSH (Linux) |
| `nova_tailscale_record_address` | `true` | write the MagicDNS name into nova-config |
| `nova_firewall` | `true` | ufw on Linux, the application firewall on macOS |
| `nova_firewall_allow_udp` | `[41641]` | UDP ports open on every interface |
| `nova_tailscale_apt_url` | `https://pkgs.tailscale.com/stable` | |

**Check.** `TAILNET <machine> dns=<name> ip=<100.x> tags=tag:nova address=kept` and `FIREWALL <machine> ufw as
declared`; `tailscale status` on the control node lists the machine. **Rollback.** On the machine `tailscale logout`
and `ufw disable` (macOS: `socketfilterfw --setglobalstate off`); `nova-config machine set <name> --address <old
address>`.

## seat.yml

Each machine's seat in the secrets store. The seat is `nova_seat`, the machine's name unless a host variable says
otherwise: the name `nova-secrets --as` and `services.yml` open it by.

1. On the machine: `<nova_secrets_key_dir>/<seat>.key` made by `age-keygen` where it is missing; never over an
   existing file, never copied. Only the public half leaves the machine.
2. On the control node, in your clone of the store (`nova_seal_store`), from the same play (so `-l <machine>` adds
   the rule too, one machine at a time): where `.sops.yaml` has no rule for the seat, `nova-secrets seat add <seat>
   <public half> --store <clone>`, which writes the rule and commits it. A rule that names a different key (a rebuilt
   machine's old seat) stops the run by name: replacing a seat is yours.

```
ansible-playbook -i ./inventory fleet/seat.yml -l web-1 -e nova_seal_store=$HOME/secrets --check --diff
ansible-playbook -i ./inventory fleet/seat.yml -l web-1 -e nova_seal_store=$HOME/secrets
git -C $HOME/secrets push
```

Then set the seat's values from a machine that holds a key for it: `nova-secrets set NAME --as <seat> --store S
--key K`.

| Variable | Default | |
|---|---|---|
| `nova_seal_store` | none, required | the control node's clone of the secrets store |
| `nova_seat` | the machine's name | |
| `nova_secrets_key_dir` | `~/.config/nova-secrets` | where `<seat>.key` lives on the machine |
| `nova_age_keygen` | `<nova_bin_dir>/age-keygen`, Homebrew's on macOS | |

**Check.** `SEAT PUB <seat> age1...` and `SEAT <seat> host=<machine> rule=present`; `nova-secrets seat list --store S`
shows `rule=yes`. **Rollback.** Before the push, `git -C S reset --hard HEAD~1`; after it, `git revert` the
`seat <seat>` commit. The key file on the machine is left; delete it by hand if the machine leaves.

## redis.yml

The Redis the bus runs on, on each machine of the `services` group (or the machines `-e nova_store_hosts` names):
the distribution's `redis-server` (7.0 or newer) run by its own unit `nova-redis` on port 6379, listening on the machine's tailnet address only, AOF on (a crash loses
at most a second), `maxmemory` with no eviction. The package's own `redis-server.service` (127.0.0.1:6379, no
password) is stopped and masked, so `nova_redis_unit` is the one Redis on the machine.

- ACL users, each with a password made once on the machine (`<nova_redis_dir>/<user>.pass`, 0600, never regenerated
  while the file exists): `bus` (what nova-bus sends: streams and presence keys under `bus:`, nothing in `@dangerous`),
  `viewer` (read only) and `admin` (this play's own; its password never leaves the machine). `default` is off: a
  client must name its user.
- An ACL change is applied to the running server with `ACL LOAD`; nothing restarts for it. A change of the config or
  the unit restarts the server only after a `SAVE` and a finished AOF rewrite; a failed checkpoint refuses the
  restart. A server older than the admin user is restarted once only with `-e nova_redis_bootstrap=true`.
- With `nova_seal_store`, `nova_seal_seat` and `nova_seal_key` given, the bus password is set into that seat as
  `NOVA_BUS_PASSWORD` (from the machine's file to `nova-secrets set`'s stdin on the control node; never argv, a log or
  a file in the clear). A seat that holds the name already is left as it is. Push the store after.

A client then runs with `NOVA_BUS_REDIS=<machine's tailnet name>:6379`, `NOVA_BUS_USER=bus` and `NOVA_BUS_PASSWORD`
from its seat; as a service row: `--env '{"NOVA_BUS_REDIS":"<host>:6379","NOVA_BUS_USER":"bus"}' --keys
NOVA_BUS_PASSWORD --seat <seat>`.

```
ansible-playbook -i ./inventory fleet/redis.yml --check --diff
ansible-playbook -i ./inventory fleet/redis.yml -e nova_seal_store=$HOME/secrets -e nova_seal_seat=<seat> \
  -e nova_seal_key=$HOME/.config/nova-secrets/<seat>.key
```

| Variable | Default | |
|---|---|---|
| `nova_redis_port` | `6379` | |
| `nova_redis_bind` | the tailnet address | a list replaces it |
| `nova_redis_dir` | `/var/lib/nova-redis` | data, passwords, `users.acl` |
| `nova_redis_unit` | `nova-redis` | |
| `nova_redis_maxmemory` | `2gb` | |
| `nova_redis_users` | bus, viewer, admin | `{name, rules}` each |
| `nova_redis_seal_user`, `nova_redis_seal_name` | `bus`, `NOVA_BUS_PASSWORD` | |
| `nova_seal_store`, `nova_seal_seat`, `nova_seal_key` | none | all three, or no seal; one or two stop the run (`SEAL ... STOP`) |

**Check.** `REDIS <machine> port=6379 bind=<100.x> version=<v> acl=same saved=no restarted=no` and `SEAL
NOVA_BUS_PASSWORD present in seat <seat>`; then `nova-bus who` from a member. **Rollback.** `systemctl disable --now
nova-redis` and `rm /etc/systemd/system/nova-redis.service`, and `systemctl unmask redis-server` for the package's
own; `/var/lib/nova-redis` (the data and the passwords) is left.

## postgres.yml

The config store, on each machine of the `services` group (or the machines `-e nova_store_hosts` names):
`postgresql-<version>` from the release's main or the PostgreSQL project's repository (its key refused unless its fingerprint is the pinned one),
listening on the tailnet address only; `scram-sha-256` from `nova_pg_allow_cidr` for the one database and everything
else rejected. The Debian cluster is adopted as it is: the play owns `conf.d/nova.conf`, `pg_hba.conf`, the database,
nova-config's roles `nova_admin` (owns the database), `nova_config` (owns schema `config`) and `nova_read`, their
grants and default privileges, and a nightly `pg_dump -Fc` kept `nova_pg_dump_keep_days`. It never drops a role or a
database. Each role's password is made once on the machine (`<nova_pg_dir>/<role>.pass`, root 0600) and proven by a
login over the tailnet address; only a role whose proof fails has its password set. With the three `nova_seal_*`
variables, the `nova_config` password is set into that seat as `NOVA_PG_PASSWORD`, the name nova-config reads.

nova-config's tables are loaded by the play: the SQL of `nova-config schema` (the control node's), run on the machine
as `postgres` over the unix socket with `SET ROLE nova_admin`, so no password is used and the tables belong to
`nova_admin` with the grants above. It runs on the first run and again whenever the SQL changes: its sha256 is kept in
`<nova_pg_dir>/schema.sha256`. An upgraded nova-config on the control node means one more run of the play.

```
ansible-playbook -i ./inventory fleet/postgres.yml --check --diff
ansible-playbook -i ./inventory fleet/postgres.yml -e nova_seal_store=$HOME/secrets -e nova_seal_seat=<seat> \
  -e nova_seal_key=$HOME/.config/nova-secrets/<seat>.key
```

| Variable | Default | |
|---|---|---|
| `nova_pg_version` | `18` | one major across the fleet |
| `nova_pg_source` | `auto` | `distro`, `pgdg`, or `auto` (distro when apt offers the package) |
| `nova_pg_listen` | the tailnet address | |
| `nova_pg_port`, `nova_pg_db` | `5432`, `nova` | |
| `nova_pg_allow_cidr` | `100.64.0.0/10` | the range tailscale assigns |
| `nova_pg_dir` | `/var/lib/nova-pg` | passwords and dumps |
| `nova_pg_dump_on_calendar`, `nova_pg_dump_keep_days` | `*-*-* 03:15:00 UTC`, `14` | |
| `nova_pg_schema` | `true` | load `nova-config schema` |
| `nova_pg_seal_role`, `nova_pg_seal_name` | `nova_config`, `NOVA_PG_PASSWORD` | |

**Check.** `PG OK <machine> version=18 db=nova listen=<100.x>:5432 roles=3 timer=enabled changed=nothing` and `PG
REACH from=control`; a run with a gap ends `PG FAIL` with what is wrong. **Rollback.** `systemctl disable --now
nova-pg-dump.timer`; remove `conf.d/nova.conf`, restore `pg_hba.conf` and restart the cluster. The database, the
roles and the dumps are left.

## secrets-refresh.yml

Each machine's copy of the secrets store at `nova_secrets_store`, kept current, so a seat never opens a stale store.
Two modes, chosen by `nova_secrets_repo`:

- **Push** (the default, `nova_secrets_repo` empty). The control node puts the store's files on the machine over the
  SSH the plays already use: `rsync -rltp --delete --exclude=.git --chmod=Du=rwx,Dgo=,Fu=rw,Fgo=`, so the directories
  are 0700 and the files 0600, and there is no git history on the machine. Given `-e nova_seal_store=S` (the control
  node's clone), this play does the push with `ansible.posix.synchronize`, with those settings. No timer runs on the machine, and one left from pull mode is removed. A machine with no store
  fails the run with `SECRETS-REFRESH <machine> mode=push missing`. join.yml installs rsync on each Linux machine.
- **Pull** (`nova_secrets_repo` set to the store's git URL). A machine with no clone gets one (over the machine's own
  git credentials), and a timer pulls it fast-forward only every `nova_secrets_refresh_every` seconds. The pull reads
  no value, prints no credential and logs one line per run (time, commit, `ok` or `FAIL rc=<n>`) to
  `nova_secrets_refresh_log`. Linux: a systemd user timer (linger on), or a crontab line where the login has no user
  manager; macOS: a LaunchAgent in the login's GUI domain. git is installed on Debian and Ubuntu and must be there
  elsewhere.

`nova_secrets_refresh: false` (the machine that writes the store, say) removes any timer and changes nothing else.

```
ansible-playbook -i ./inventory fleet/secrets-refresh.yml -e nova_seal_store=$HOME/secrets --check --diff
ansible-playbook -i ./inventory fleet/secrets-refresh.yml -e nova_seal_store=$HOME/secrets
```

| Variable | Default | |
|---|---|---|
| `nova_secrets_store` | `~/.local/share/nova/secrets` | where the machine keeps the store |
| `nova_seal_store` | unset | push mode: the control node's clone, pushed from |
| `nova_secrets_repo` | empty | empty: push mode; set: the git URL pull mode clones and pulls |
| `nova_secrets_refresh` | `true` | `false` removes any timer |
| `nova_secrets_refresh_every` | `600` | seconds (pull mode) |
| `nova_secrets_refresh_log` | `~/.local/state/nova/secrets-refresh.log` | pull mode |

**Check.** Push mode: `SECRETS-REFRESH <machine> mode=push files=<n> same` (or `pushed` when it changed files); a
second run says `same`. Pull mode: `SECRETS-REFRESH <machine> mode=pull via=systemd-user-timer every=600s last: <time>
head=<commit> ok`. **Rollback.** Push mode: `rm -rf <nova_secrets_store>` on the machine. Pull mode:
`-e nova_secrets_refresh=false`.

## health.yml

Guards that let a Linux machine get out of trouble by itself, and a disk report. Each is one file the play owns;
applying it restarts only the daemon it configures, never the login's units. A darwin machine is skipped.

- The journal kept on disk (`/etc/systemd/journald.conf.d/90-nova-journal.conf`), up to `nova_health_journal_max_use`,
  so the boots before a failure can be read.
- A kernel panic reboots after `nova_health_panic_reboot` seconds, and an oops is a panic
  (`/etc/sysctl.d/90-nova-panic.conf`).
- Sessions over ssh run at OOM score 0 (`ssh.service.d/90-nova-oom.conf`; Debian's unit gives every session -1000, so a
  runaway command leaves the kernel nothing it may kill). sshd keeps -1000 for itself; open sessions survive the restart.
- rsyslog's files rotate at `nova_health_log_maxsize` as well, and a file of `nova_health_log_files` already over
  `nova_health_log_trim` bytes is cut to its first and last 32 MiB (kept beside it as `.flood`) and emptied.
- The login's user slice is killed inside at `nova_health_user_memory_max_pct` of RAM, and a killed user manager comes
  back (`user-<uid>.slice.d/90-nova-memory.conf`, `user@.service.d/90-nova-restart.conf`).
- `nova-disk-report` every 6 hours from `nova-disk-report.timer`: one line in the journal for /, the home filesystem
  and swap, `DISK-WARN` at priority warning over `nova_health_disk_warn_pct`.

```
ansible-playbook -i ./inventory fleet/health.yml --check --diff
ansible-playbook -i ./inventory fleet/health.yml
journalctl -t nova-disk-report --since -1d        # the disk lines
```

| Variable | Default | |
|---|---|---|
| `nova_health_journal`, `nova_health_journal_max_use` | `true`, `4G` | |
| `nova_health_panic_reboot` | `10` | seconds; `0` is off |
| `nova_health_ssh_killable`, `nova_health_ssh_unit` | `true`, `ssh` | `sshd` on Red Hat |
| `nova_health_log_maxsize`, `nova_health_log_trim`, `nova_health_log_files` | `1G`, 4 GiB, syslog and kern.log | `0` trim is off |
| `nova_health_user_memory_max_pct` | `85` | `0` is no limit |
| `nova_health_disk_report`, `nova_health_disk_warn_pct`, `nova_health_disk_on_calendar` | `true`, `80`, every 6 hours | |

**Check.** `HEALTH <machine> journal=persistent panic=10 ssh_oom=0 slice_max=<bytes> | DISK <machine> root=<n>%
swap=<n>%`. A machine in a bad state makes the run fail with `HEALTH FAIL <machine>:` and each thing wrong: a
`DISK-WARN`, and after a run a guard that does not read back as set. Under `--check` only the disk counts. **Rollback.** Remove the file of the guard (named above), then `systemctl daemon-reload` and restart the
daemon it configures (`systemd-journald`, `ssh`) or `sysctl --system`; `systemctl disable --now nova-disk-report.timer`.

## Reservations

`nova-reserve` runs a command holding a reservation of one of N slots on the machine, so builds, tests and benches queue instead of
timing each other out. It is the same tool on Linux, macOS and Windows: OS file locks in one directory
(`$NOVA_RESERVE_DIR`, else `/var/tmp/nova-reserve`; `C:\ProgramData\nova-reserve` on Windows; a local disk), freed by the kernel when a holder
dies. A machine has one capacity, N slots, stored in the directory; `docs/SPEC-RESERVE.md` has the rule.

```
nova-reserve --wait 15m -- go test ./...
nova-reserve --exclusive -- ./bench
nova-reserve hold --for 2h &        # prints HELD, holds until SIGTERM or --for
=======
```sh
nova-reserve set --slots 3
nova-reserve --wait 15m --why "unit tests" -- go test ./...
nova-reserve --exclusive -- ./benchmark
>>>>>>> 961730a02 (docs: human-readable per-tool docs (docs/v2 rebased onto main))
nova-reserve status
```

<<<<<<< HEAD
Every caller on a machine uses the same directory. `reserve/README.md` has the flags, exit codes and the TLA+ models;
`docs/SPEC-RESERVE.md` specifies the reservations.
=======
The first shared reservation sets the capacity. Without `--slots`, its default is the CPU
count divided by four, with a minimum of one. Later callers use the stored
count; `set` changes it only while free.
Set `NOVA_RESERVE_DIR` to choose the shared directory. The [reservation guide](../reserve/README.md)
covers waiting, exits and command lifetime.
>>>>>>> 961730a02 (docs: human-readable per-tool docs (docs/v2 rebased onto main))
