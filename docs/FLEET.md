# The fleet

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
  roles/                            the roles the plays run (one per play, github-runner, and seal: a password into a seat)
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

## join.yml

A new machine, from nothing to a member: its nova-config row, its login, the tailnet, the tools a seat needs and its
seat, then a check that it is reachable. Name the machine with `-e nova_join_host`, never `-l`: a machine with no row
yet is in no inventory, so the first play adds the row and reads the inventory again.

1. **The row** (control node). `nova-config machine add` where the row is missing. A row that exists is never edited;
   one whose user is not `nova_join_user` stops the join by name.
2. **The login** (Linux, connecting as root). The row's user: no password, in `nova_join_sudo_group` with
   passwordless sudo (`/etc/sudoers.d/90-nova-<login>`, checked by `visudo` before it lands), root's
   `authorized_keys` copied to it (or the control node's file `nova_join_authorized_keys` names), linger on, the
   hostname set to the machine name. A darwin machine has no root login: set its login and sudo up at its console.
3. **The tailnet**: `tailscale.yml` (below). From here on the plays reach the machine over the tailnet.
4. **The tools a seat needs** (as the login): `nova_bin_dir` on the login PATH; on Linux the pinned `age` and `sops`
   release binaries, checked against their published digests. A darwin machine takes them from Homebrew
   (`brew install age sops`).
5. **The tailnet check** (control node): the row's address is now the machine's MagicDNS name, the control node's
   `tailscale status` shows it as an online peer, and the host key it answers with over the tailnet is the one pinned
   at the row (`nova_join_known_hosts`). Anything else stops the join before the seat is made.
6. **The seat**: `seat.yml` (below): the key made on the machine, its rule in the store, its public half pinned on
   the control node. Then `nova-secrets grant` fills the new seat's grants file with the names in `nova_join_grant`
   (`NOVA_BUS_PASSWORD` unless you list others) from the control seat `nova_seal_seat`, opened with its key
   `nova_seal_key`, sealed to the pinned machine key and the pinned recovery key only: the control key cannot open
   it. The store clone is pushed, the machine gets the recovery pin for its own `set`, and it pulls its own clone
   once and on a timer (`secrets-refresh.yml`). The values pass from one sops to the other in memory, and a refused
   grant stops the join with its one-line reason. The control seat is one `seat add` and `set` like any other
   (secrets/README.md, "A fleet"); no recovery key is needed.
7. **The check**: the control node's `tailscale status` shows the machine as an online peer, and `nova-bus who` from
   the machine reaches the bus. Without `nova_bus_redis`, or when the seat holds no `NOVA_BUS_PASSWORD` because no
   operator seat and key were given, the bus check is a `NOTE`, not a failure.

A Windows machine gets 1 and 3.

```
nova-secrets exec --only TAILSCALE_AUTHKEY,NOVA_PG_PASSWORD --store S --key K --as SEAT -- \
  env NOVA_PG_DSN=postgres://nova_config@<store>:5432/nova \
  ansible-playbook -i ./inventory fleet/join.yml --check --diff \
    -e nova_join_host=web-1 -e nova_join_user=fleet -e nova_join_address=203.0.113.7 \
    -e nova_join_os=linux -e nova_join_arch=amd64 -e nova_seal_store=$HOME/secrets \
    -e nova_seal_seat=control -e nova_seal_key=$HOME/.config/nova-secrets/control.key
```

The same without `--check --diff` joins it. A join that stopped (a failure, a seat waiting for its push) is resumed by
running the same command again.

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
| `nova_bus_redis` | unset | `host:port` of the bus, for the check |
| `nova_seal_seat`, `nova_seal_key` | unset | the control seat and its key file on the control node; without them nothing is granted |
| `nova_seal_pins` | `~/.config/nova-secrets/pins` | the control node's pins: `recovery.pub` and one `<seat>.pub` per machine |
| `nova_join_known_hosts` | `~/.ssh/nova-fleet-known-hosts` | the machines' SSH host keys, pinned at the row and checked after |
| `nova_join_grant` | `[NOVA_BUS_PASSWORD]` | the names granted into the new seat |
| `nova_seal_push` | `true` | push the store clone after the seat and the grant, so the machine can pull them |
| `nova_age_version`, `nova_sops_version` and their `*_sha256` | the pinned releases | |
| `nova_control_config`, `nova_control_secrets`, `nova_control_sops` | `nova-config`, `nova-secrets`, `sops` | the control node's binaries |

**Check.** The receipts `JOIN ROW`, `JOIN HOSTKEY`, `TAILNET`, `FIREWALL`, `JOIN TOOLS`, `JOIN PEER`, `SEAT`, `JOIN GRANT <seat> from=<control seat>
to=<seat> names=<n> changed=<n>` and `JOIN CHECK <machine> peer=online
bus=WHO OK present=<n>`; a second run's `--check --diff` changes nothing. **Rollback.** `nova-config machine remove
<name> --actor <you>`, the device removed in the Tailscale admin console, the seat's rule reverted in the store
(`git revert`), and on the machine `userdel -r <login>` and `rm /etc/sudoers.d/90-nova-<login>`.

## tailscale.yml

Each machine on the tailnet, its address in nova-config its MagicDNS name, and its firewall closed to everything but
the tailnet. `join.yml` runs it; run it alone for machines that already have a row.

**What is yours, once, in the Tailscale admin console:**

1. The account and the tailnet. MagicDNS on (the default).
2. The policy: paste `fleet/tailscale-acl.json` into Access controls and put your account where it says
   `owner@example.com`. It declares `tag:nova` and `tag:runner`, lets those machines reach each other on 22 (Tailscale
   SSH), 6379 (the bus's Redis) and 5432 (nova-config's PostgreSQL) and nothing else, and lets your own devices SSH to
   them. The control node runs nova-config against 5432: make it a `tag:nova` machine, or add `tcp:5432` to your
   devices' grant.
3. An auth key: Settings, Keys, generate auth key, **reusable**, **pre-authorized**, **tagged** `tag:nova` (and
   `tag:runner`, for the runners). Keep it in the control node's seat: `nova-secrets set TAILSCALE_AUTHKEY --as <seat>
   --store S --key K` (the value on stdin).
4. The control node itself on the tailnet, so it resolves MagicDNS names.
5. On a Mac: open the Tailscale app once at the console and approve its VPN configuration.

**What the play does, per machine:**

1. Installs Tailscale: Tailscale's apt repository on Debian and Ubuntu (the same key and source line its own install
   script writes), Homebrew's cask on macOS, winget on Windows.
2. `tailscale up --reset --auth-key=file:/dev/stdin --hostname=<machine> --advertise-tags=tag:nova[,tag:runner] --ssh`
   (`--ssh` on Linux; the macOS and Windows clients do not serve Tailscale SSH). `tag:runner` is added for a machine in
   the `runner` group. The key comes from `TAILSCALE_AUTHKEY` in the control node's environment and reaches the
   machine on the command's stdin only (on Windows, which has no `/dev/stdin`, in a file only the login can read,
   removed after the command). A machine already Running with the same tags is not joined again; a hostname or
   Tailscale SSH setting that differs is set in place with `tailscale set`, with no key.
3. Records `Self.DNSName` (`<machine>.<tailnet>.ts.net`) as the machine's address: `nova-config machine set <name>
   --address <dns name>`, then reads the inventory again, so the firewall play, every run after it, the bus and the
   services reach the machine by that name.
4. The firewall. Linux: ufw denies incoming on every interface but `tailscale0`, allows outgoing and UDP 41641
   (Tailscale's direct connections). It is turned on only when the run itself comes in on the tailnet (or is local),
   so it cannot cut its own connection; otherwise it stops with `FIREWALL HELD` and says why. macOS: the application
   firewall on (sudo: add `-K` where the login has no passwordless sudo). Windows is left as it is.

`redis.yml` and `postgres.yml` listen on the machine's tailnet address only, read from `tailscale ip -4`.

```
nova-secrets exec --only TAILSCALE_AUTHKEY,NOVA_PG_PASSWORD --store S --key K --as SEAT -- \
  env NOVA_PG_DSN=postgres://nova_config@<store>:5432/nova \
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
2. On the control node, in your clone of the store (`nova_seal_store`): where `.sops.yaml` has no rule for the seat,
   `nova-secrets seat add <seat> <public half> --store <clone>`, which writes the rule and commits it. The public
   half is also pinned outside the store, `<nova_seal_pins>/<seat>.pub`, the key `nova-secrets grant` seals to. A
   rule or a pin that names a different key (a rebuilt machine's old seat) stops the run by name: replacing a seat
   is yours.

```
ansible-playbook -i ./inventory fleet/seat.yml -l web-1 -e nova_seal_store=$HOME/secrets --check --diff
ansible-playbook -i ./inventory fleet/seat.yml -l web-1 -e nova_seal_store=$HOME/secrets
git -C $HOME/secrets push
```

Then fill the seat from the control seat on the control node, `nova-secrets grant NAME --to <seat> --pins P --as
<control seat> --store S --key K` (no `--pub`: the pin is there), which is what `join.yml` does, or set a value of its
own on the machine with its own key and the recovery pin join.yml put there.

| Variable | Default | |
|---|---|---|
| `nova_seal_store` | none, required | the control node's clone of the secrets store |
| `nova_seal_pins` | `~/.config/nova-secrets/pins` | where the seat's public key is pinned on the control node |
| `nova_seat` | the machine's name | |
| `nova_secrets_key_dir` | `~/.config/nova-secrets` | where `<seat>.key` lives on the machine |
| `nova_age_keygen` | `<nova_bin_dir>/age-keygen`, Homebrew's on macOS | |

**Check.** `SEAT PUB <seat> age1...` and `SEAT <seat> host=<machine> rule=present pin=present`; `nova-secrets seat list
--store S` shows `rule=yes`. **Rollback.** Before the push, `git -C S reset --hard HEAD~1`; after it, `git revert` the
`seat <seat>` commit. The key file on the machine is left; delete it by hand if the machine leaves.

## redis.yml

The Redis `nova-bus` talks to, on each machine of the `services` group: the distribution's `redis-server` (7.0 or newer)
run by its own unit `nova-redis` on port 6379, listening on the machine's tailnet address only, AOF on (a crash loses
at most a second), `maxmemory` with no eviction.

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
| `nova_redis_port` | `6379` | the distribution's own `redis-server` unit listens on 127.0.0.1 and is left as it is |
| `nova_redis_bind` | the tailnet address | a list replaces it |
| `nova_redis_dir` | `/var/lib/nova-redis` | data, passwords, `users.acl` |
| `nova_redis_unit` | `nova-redis` | |
| `nova_redis_maxmemory` | `2gb` | |
| `nova_redis_users` | bus, viewer, admin | `{name, rules}` each |
| `nova_redis_seal_user`, `nova_redis_seal_name` | `bus`, `NOVA_BUS_PASSWORD` | |
| `nova_seal_store`, `nova_seal_seat`, `nova_seal_key` | none | all three, or no seal |

**Check.** `REDIS <machine> port=6379 bind=<100.x> version=<v> acl=same saved=no restarted=no` and `SEAL
NOVA_BUS_PASSWORD present in seat <seat>`; then `nova-bus who` from a member. **Rollback.** `systemctl disable --now
nova-redis` and `rm /etc/systemd/system/nova-redis.service`; `/var/lib/nova-redis` (the data and the passwords) is
left.

## postgres.yml

The PostgreSQL nova-config keeps the fleet in, on each machine of the `services` group: `postgresql-<version>` from
the release's main or the PostgreSQL project's repository (its key refused unless its fingerprint is the pinned one),
listening on the tailnet address only; `scram-sha-256` from `nova_pg_allow_cidr` for the one database and everything
else rejected. The Debian cluster is adopted as it is: the play owns `conf.d/nova.conf`, `pg_hba.conf`, the database,
nova-config's roles `nova_admin` (owns the database), `nova_config` (owns schema `config`) and `nova_read`, their
grants and default privileges, and a nightly `pg_dump -Fc` kept `nova_pg_dump_keep_days`. It never drops a role or a
database. Each role's password is made once on the machine (`<nova_pg_dir>/<role>.pass`, root 0600) and proven by a
login over the tailnet address; only a role whose proof fails has its password set. With the three `nova_seal_*`
variables, the `nova_config` password is set into that seat as `NOVA_PG_PASSWORD`, the name nova-config reads.

```
ansible-playbook -i ./inventory fleet/postgres.yml --check --diff
ansible-playbook -i ./inventory fleet/postgres.yml -e nova_seal_store=$HOME/secrets -e nova_seal_seat=<seat> \
  -e nova_seal_key=$HOME/.config/nova-secrets/<seat>.key
nova-config schema | psql "postgres://nova_admin@<store>:5432/nova"      # the tables, once and after an upgrade
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
| `nova_pg_seal_role`, `nova_pg_seal_name` | `nova_config`, `NOVA_PG_PASSWORD` | |

**Check.** `PG OK <machine> version=18 db=nova listen=<100.x>:5432 roles=3 timer=enabled changed=nothing` and `PG
REACH from=control`; a run with a gap ends `PG FAIL` with what is wrong. **Rollback.** `systemctl disable --now
nova-pg-dump.timer`; remove `conf.d/nova.conf`, restore `pg_hba.conf` and restart the cluster. The database, the
roles and the dumps are left.

## secrets-refresh.yml

Each machine's clone of the secrets store (`nova_secrets_store`) pulled fast-forward only every
`nova_secrets_refresh_every` seconds, so a seat never opens a stale store. The pull reads no value, prints no
credential and logs one line per run (time, commit, `ok` or `FAIL rc=<n>`) to `nova_secrets_refresh_log`. Linux: a
systemd user timer (linger on), or a crontab line where the login has no user manager; macOS: a LaunchAgent in the
login's GUI domain. With `nova_secrets_repo`, a machine with no clone gets one; without it, such a machine is skipped
and named. The machine that writes the store sets `nova_secrets_refresh: false`, which removes its timer.

```
ansible-playbook -i ./inventory fleet/secrets-refresh.yml --check --diff
ansible-playbook -i ./inventory fleet/secrets-refresh.yml
```

| Variable | Default | |
|---|---|---|
| `nova_secrets_refresh` | `true` | `false` removes the timer |
| `nova_secrets_refresh_every` | `600` | seconds |
| `nova_secrets_refresh_log` | `~/.local/state/nova/secrets-refresh.log` | |
| `nova_secrets_repo` | empty | the store's git URL, to clone where there is none |

**Check.** `SECRETS-REFRESH <machine> via=systemd-user-timer every=600s last: <time> head=<commit> ok`. **Rollback.**
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
swap=<n>%`. **Rollback.** Remove the file of the guard (named above), then `systemctl daemon-reload` and restart the
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
nova-reserve status
nova-reserve set --slots 3           # refused while a slot is held
```

Every caller on a machine uses the same directory. `reserve/README.md` has the flags, exit codes and the TLA+ models;
`docs/SPEC-RESERVE.md` specifies the reservations.
