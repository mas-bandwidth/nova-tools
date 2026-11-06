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

Take `nova-fleet` from a release like the other tools (`nova-fleet_<tag>_<os>_<arch>[.exe]` for linux/amd64,
linux/arm64, darwin/arm64 and windows/amd64, in the release's `SHA256SUMS`; `install` puts it on every machine), or
build it. On Windows `plays`, `show` and `help` work, and running a play is refused with the reason: ansible-core has
no native Windows controller, so a Windows machine is a host the plays reach, never the one running them (use a
Linux, macOS or WSL control node).

```
git clone --depth 1 https://github.com/mas-bandwidth/nova-tools && cd nova-tools && go build -o ~/.local/bin/nova-fleet ./fleet
```

The control machine needs ansible-core 2.15 or later on PATH; a Windows host in the inventory also needs the `ansible.windows`
collection:

```
python3 -m venv ~/.local/ansible && ~/.local/ansible/bin/pip install ansible-core
ansible-galaxy collection install -r <extracted>/requirements.yml   # nova-fleet prints the path when it is missing
```

## up

`nova-fleet up` takes a fleet from nothing to running in one fixed order. Its inputs are only what you have: the
machines' ssh addresses, the Tailscale auth key in your seat, and, for runners, the repository or organization.

```
nova-fleet up --check --tools v2.0.0 store-1=203.0.113.7 web-1=203.0.113.8 mac-1=mac-1.local/darwin/arm64
nova-fleet up --tools v2.0.0 --runners https://github.com/OWNER/REPO store-1=203.0.113.7 web-1=203.0.113.8
```

**What is yours, once, before the first run:**

1. The Tailscale account and its policy, and a reusable, pre-authorized auth key tagged `tag:nova` (and `tag:runner`
   for runners) ("tailscale" below: what is yours, once).
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

## The plays

| Play | Purpose | Needs |
|---|---|---|
| `github-runners` | GitHub Actions runners on every machine of the runner group, registered to one repository or organization | `github_runner_url`, `github_runner_scope`; `GITHUB_RUNNER_TOKEN` in nova-secrets; `gh` with admin on the repository |
| `health` | guards that let a Linux machine get itself out of trouble, and a disk report | nothing |
| `install` | install one nova-tools release, by tag, on the machines named | `nova_tools_version` (a tag `vX.Y.Z`; no default) |
| `join` | a new machine joins the fleet: its row, its login, the tailnet, the tools a seat needs, its seat | the machine as the argument; its row's fields; `TAILSCALE_AUTHKEY`, `NOVA_PG_DSN` and `NOVA_PG_PASSWORD` in the environment |
| `postgres` | the PostgreSQL nova-config keeps the fleet in, on each machine of the services group | `nova_seal_*` to seal its password |
| `redis` | the Redis nova-bus talks to, on each machine of the services group | `nova_seal_*` to seal its password |
| `seat` | each machine's seat in the secrets store | `nova_seal_store` |
| `secrets-refresh` | every machine's clone of the secrets store pulled fast-forward on a timer | `nova_secrets_repo` for a machine with no clone |
| `services` | one supervised unit per service record, on the machine the record names | the inventory's `nova_services` |
| `tailscale` | each machine on the tailnet, its address the MagicDNS name, its firewall closed to all but the tailnet | `TAILSCALE_AUTHKEY` in the environment |

```
nova-fleet install -- -e nova_tools_version=v1.0.0 -l bench
nova-fleet services --check --diff
nova-fleet github-runners -- -e github_runner_url=https://github.com/OWNER/REPO -e github_runner_scope=repo
```

`nova-fleet plays` is the list in the binary you hold; `nova-fleet show <play>` prints one, and `nova-fleet show
tailscale-acl` the starter tailnet policy. Every play is idempotent and prints one receipt line per machine; under
`--check` the receipt ends `(check: nothing changed)`. A secret is never a variable: it reaches a run as an
environment variable from `nova-secrets exec` on the control node, and a machine only on a command's stdin, so a
play that needs one runs as `nova-secrets exec --only NAME -- nova-fleet <play> ...` (`nova-fleet up` does this
itself).

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
- A unit whose file changed is restarted, and named in the receipt (`restarted=<names>`, `would-restart=<names>` under
  `--check`). `-e nova_service_only=a,b`
  renders and restarts only those rows and retires nothing; `-e nova_service_restart=a,b` restarts those rows even if
  unchanged. A name no row carries is refused before anything is written.
- Each host ends with one receipt line: `SERVICES host=<machine> place=<unit dir> records=<n> enabled=<n>
  written=<n> retired=<n>`, then ` restarted=<names>` (` would-restart=`), ` only=<names>` and ` (check: nothing
  changed)` where they apply.

## github-runners

GitHub Actions runners on every machine of the `runner` group: as many as the machine row's `runners` says, each
registered to one repository or organization, run as a service that starts at boot. **Labels:** a runner's labels are
its machine's inventory groups (the row's roles: `bench`, `runner`, `services`; `ungrouped` and `all` are left out),
its `os` (`linux`, `darwin`, `windows`) and its `arch` (`amd64`, `arm64`), plus `github_runner_labels`; so a bench
machine's runner is labelled `amd64,bench,linux,runner`, and a job selects it with `runs-on: [self-hosted, linux]`. Linux (amd64, arm64; WSL2 with systemd counts as Linux), macOS (a LaunchDaemon dropped to the
login) and native Windows (the runner's own service; `ansible.windows` and an administrator login). Linux and macOS
write the service files with sudo, so the login needs passwordless sudo: `join.yml` sets it up on Linux
(`/etc/sudoers.d/90-nova-<login>`); on macOS set it at the console. No command here asks for a sudo password: an
interactive prompt is a stall for an AI running the play.

**Public repositories.** A self-hosted runner runs the code of any workflow that reaches it, and on a public repository
that includes pull requests from forks. Use these runners on private repositories, or restrict which workflows and
events may use them.

| Variable | |
|---|---|
| `github_runner_url` | no default. `https://github.com/<owner>/<repo>`, or `https://github.com/<org>` |
| `github_runner_scope` | no default. `repo` or `org`; refused if the url is the other shape |
| `github_runner_labels` | extra labels, a list (default none), added to the machine's labels |
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

**Plan.** Under `--check` each machine prints `RUNNER-PLAN <machine> want=<n> register=<names> retire=<names>
labels=<labels>`, the runners named `<machine>-<i>` as GitHub will list them.

**Rollback.** Retiring needs a **removal** token, a different one from the registration token: store it as
`GITHUB_RUNNER_TOKEN` the same way, and it is read the same way (under `--check` too, and a failed read says why),
then run with `-e github_runner_state=absent`:

```
gh api -X POST repos/OWNER/REPO/actions/runners/remove-token -q .token | nova-secrets set GITHUB_RUNNER_TOKEN
nova-fleet github-runners --check -- -e github_runner_url=https://github.com/OWNER/REPO -e github_runner_scope=repo -e github_runner_state=absent
nova-fleet github-runners -- -e github_runner_url=https://github.com/OWNER/REPO -e github_runner_scope=repo -e github_runner_state=absent
```

That stops and removes the services, removes the runners from GitHub and deletes their directories. With `-e
github_runner_deregister=false` no token is read and the GitHub entries stay for you to delete. A removal token also
expires in an hour.

## join

A new machine, from nothing to a member: its nova-config row, its login, the tailnet, the tools a seat needs and its
seat, then a check that it is reachable. Name the machine as the argument, `nova-fleet join <machine>` (it
passes `-e nova_join_host=<machine>`), never `-l`: a machine with no row yet is in no inventory, so the first play adds
the row and reads the inventory again.

1. **The row** (control node). `nova-config machine add` where the row is missing. A row that exists is never edited;
   one whose user is not `nova_join_user` stops the join by name.
2. **The login** (Linux, connecting as root). The row's user: no password, in `nova_join_sudo_group` with
   passwordless sudo (`/etc/sudoers.d/90-nova-<login>`, checked by `visudo` before it lands), root's
   `authorized_keys` copied to it (or the control node's file `nova_join_authorized_keys` names), linger on, the
   hostname set to the machine name. A darwin machine has no root login: set its login and sudo up at its console.
3. **The tailnet**: `tailscale` (below). From here on the plays reach the machine over the tailnet.
4. **The tools a seat needs** (as the login): `nova_bin_dir` on the login PATH; on Linux the pinned `age` and `sops`
   release binaries, checked against their published digests. A darwin machine takes them from Homebrew
   (`brew install age sops`).
5. **The seat**: `seat` (below).
6. **The check**: the control node's `tailscale status` shows the machine as an online peer, and `nova-bus who` from
   the machine reaches the bus. Without `nova_bus_redis`, or before the seat holds `NOVA_BUS_PASSWORD`, the bus check
   is a `NOTE`, not a failure.

A Windows machine gets 1 and 3.

```
nova-secrets exec --only TAILSCALE_AUTHKEY,NOVA_PG_PASSWORD --store S --key K --as SEAT -- \
  env NOVA_PG_DSN=postgres://nova_config@<store>:5432/nova \
  nova-fleet join web-1 --check --diff -- -e nova_join_user=fleet -e nova_join_address=203.0.113.7 \
    -e nova_join_os=linux -e nova_join_arch=amd64 -e nova_seal_store=$HOME/secrets
```

The same without `--check --diff` joins it. A join that stopped (a failure, a seat waiting for its push) is resumed by
running the same command again.

| Variable | Default | |
|---|---|---|
| `nova_join_host` | none, required | the machine's name (lowercase, digits, `-`): the argument of `nova-fleet join` |
| `nova_join_user`, `nova_join_address`, `nova_join_os`, `nova_join_arch` | none | the row's fields; required only when the row is missing |
| `nova_join_cores` | unset | the row's cores |
| `nova_join_roles` | `bench` | the row's roles (`bench`, `runner`, `services`) |
| `nova_join_actor` | `$USER` | nova-config's `--actor` |
| `nova_join_root_user` | `root` | the login a fresh Linux machine is reached with |
| `nova_join_sudo_group` | `sudo` | the group the login joins (`wheel` on Red Hat) |
| `nova_join_authorized_keys` | root's on the machine | a file on the control node to install instead |
| `nova_join_hostname` | `true` | set the hostname to the machine name |
| `nova_bus_redis` | unset | `host:port` of the bus, for the check |
| `nova_age_version`, `nova_sops_version` and their `*_sha256` | the pinned releases | |
| `nova_control_config`, `nova_control_secrets`, `nova_control_sops` | `nova-config`, `nova-secrets`, `sops` | the control node's binaries |

**Check.** The receipts `JOIN ROW`, `TAILNET`, `FIREWALL`, `JOIN TOOLS`, `SEAT` and `JOIN CHECK <machine> peer=online
bus=WHO OK present=<n>`; a second run's `--check --diff` changes nothing. **Rollback.** `nova-config machine remove
<name> --actor <you>`, the device removed in the Tailscale admin console, the seat's rule reverted in the store
(`git revert`), and on the machine `userdel -r <login>` and `rm /etc/sudoers.d/90-nova-<login>`.

## tailscale

Each machine on the tailnet, its address in nova-config its MagicDNS name, and its firewall closed to everything but
the tailnet. `join` runs it; run it alone for machines that already have a row.

**What is yours, once, in the Tailscale admin console:**

1. The account and the tailnet. MagicDNS on (the default).
2. The policy: paste `nova-fleet show tailscale-acl` into Access controls and put your account where it says
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
   firewall on (sudo: the login needs passwordless sudo, set at the console). Windows is left as it is.

`redis` and `postgres` listen on the machine's tailnet address only, read from `tailscale ip -4`.

```
nova-secrets exec --only TAILSCALE_AUTHKEY,NOVA_PG_PASSWORD --store S --key K --as SEAT -- \
  env NOVA_PG_DSN=postgres://nova_config@<store>:5432/nova \
  nova-fleet tailscale --check --diff -- -l web-1
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

## seat

Each machine's seat in the secrets store. The seat is `nova_seat`, the machine's name unless a host variable says
otherwise: the name `nova-secrets --as` and `services` open it by.

1. On the machine: `<nova_secrets_key_dir>/<seat>.key` made by `age-keygen` where it is missing; never over an
   existing file, never copied. Only the public half leaves the machine.
2. On the control node, in your clone of the store (`nova_seal_store`): where `.sops.yaml` has no rule for the seat,
   `nova-secrets seat add <seat> <public half> --store <clone>`, which writes the rule and commits it. A rule that
   names a different key (a rebuilt machine's old seat) stops the run by name: replacing a seat is yours.

```
nova-fleet seat --check --diff -- -l web-1 -e nova_seal_store=$HOME/secrets
nova-fleet seat -- -l web-1 -e nova_seal_store=$HOME/secrets
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

## redis

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
nova-fleet redis --check --diff
nova-fleet redis -- -e nova_seal_store=$HOME/secrets -e nova_seal_seat=<seat> \
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

## postgres

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
nova-fleet postgres --check --diff
nova-fleet postgres -- -e nova_seal_store=$HOME/secrets -e nova_seal_seat=<seat> \
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

## secrets-refresh

Each machine's clone of the secrets store (`nova_secrets_store`) pulled fast-forward only every
`nova_secrets_refresh_every` seconds, so a seat never opens a stale store. The pull reads no value, prints no
credential and logs one line per run (time, commit, `ok` or `FAIL rc=<n>`) to `nova_secrets_refresh_log`. Linux: a
systemd user timer (linger on), or a crontab line where the login has no user manager; macOS: a LaunchAgent in the
login's GUI domain. With `nova_secrets_repo`, a machine with no clone gets one; without it, such a machine is skipped
and named. The machine that writes the store sets `nova_secrets_refresh: false`, which removes its timer.

```
nova-fleet secrets-refresh --check --diff
nova-fleet secrets-refresh
```

| Variable | Default | |
|---|---|---|
| `nova_secrets_refresh` | `true` | `false` removes the timer |
| `nova_secrets_refresh_every` | `600` | seconds |
| `nova_secrets_refresh_log` | `~/.local/state/nova/secrets-refresh.log` | |
| `nova_secrets_repo` | empty | the store's git URL, to clone where there is none |

**Check.** `SECRETS-REFRESH <machine> via=systemd-user-timer every=600s last: <time> head=<commit> ok`. **Rollback.**
`-e nova_secrets_refresh=false`.

## health

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
nova-fleet health --check --diff
nova-fleet health
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
