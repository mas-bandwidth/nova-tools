# nova-fleet

nova-fleet puts the same software on every machine you own. It is the fleet's Ansible plays in one binary: the
plays, roles, templates and defaults are embedded, a run extracts them to a private temporary directory, points
`ansible-playbook` at your inventory (`nova-config inventory` unless you name another) and exits 0 when it passed,
1 when it failed (ansible-playbook's own status named on stderr) or matched no host, 2 when it could not run.

Use the release selected in the [installation guide](../../docs/README.md#install-one-tool)
as `NOVA_TOOLS_VERSION`; replace `<machine>` with a name from your inventory.

```sh
nova-fleet plays                                  # what is in the binary
nova-fleet show install                           # read a play before you run it
nova-fleet install --check --diff -- -e nova_tools_version="$NOVA_TOOLS_VERSION" -l <machine>
nova-fleet install -- -e nova_tools_version="$NOVA_TOOLS_VERSION" -l <machine>
```

`--check` and `--diff` may come before `--`; everything after `--` goes to `ansible-playbook` as written (`-e`, `-l`,
`-v`, `--tags`). `nova-fleet help` and `nova-fleet <play> -h` print the rest.

## Install

[Install one tool](../../docs/README.md#install-one-tool), choosing `tool=fleet`.
The [shared guide](../../docs/README.md) covers choosing tools and trying them together.
This installs the command on your control node; the `install` play deploys tools to the inventory machines.
On Windows, run it under WSL2: ansible-core has no native Windows controller.

The control machine needs ansible-core 2.15 or later on PATH. On macOS `brew install ansible`; on Linux a venv (Debian and Ubuntu ship python3 without it, so there the
line starts `sudo apt-get update -qq && sudo apt-get install -y python3-venv && `; `nova-fleet check` prints the line for this machine, and says so when
python3 has no venv module):

```
python3 -m venv ~/.local/ansible && ~/.local/ansible/bin/pip install ansible-core
export PATH="$HOME/.local/ansible/bin:$PATH"
nova-fleet show requirements > requirements.yml && ansible-galaxy collection install -r requirements.yml
```

## Fleet with Tailscale, go!

The whole path, in order. Each step's output says what to do next.

1. `nova-fleet check` on the control node: one line per prerequisite, `CHECK OK sops 3.13.3` or `CHECK MISSING
   age-keygen: sudo apt-get update -qq && sudo apt-get install -y age` (the fix for this OS, a command pasteable as it
   stands with any reason after `#`, in paste order: `git` and `go` before the `nova-*` build lines), then the tailnet (a `CHECK NOTE` when this node is not on it: `up` joins it; on it, one `CHECK traffic <peer> tx=<GB> rx=<GB>` per peer this node has exchanged bytes with, from `tailscale status`, and `CHECK WARN traffic` for a peer over 100 GB either way: something fetches or mirrors on a clock), the secrets store, key, seat and pins,
   `TAILSCALE_API_KEY` (`--runners URL` adds `gh` and its login, `gh auth login`), and `NOVA_PG_DSN` once a fleet
   exists. Run each fix line, then `nova-fleet check` again until it ends `CHECK OK missing=0` (exit 0; 1 while
   anything is missing; the collections of `requirements.yml` count, `ansible.posix` for the push of the store). A
   green check ends `CHECK OK missing=0` and the line `next: nova-fleet up --check ...`. Every play verb runs the part of this check
   it needs (ansible-playbook, the collection, nova-config) before anything else, and refuses with the same lines.
   Tailscale, PostgreSQL, Redis and Ansible are not optional: `check` requires Tailscale and
   Ansible on this node, `up` puts PostgreSQL and Redis on the first machine and requires them after, and no variable
   of nova-fleet skips one.
   The sops it installs is the one the plays pin: one line, `nova_sops_version` in `group_vars/all.yml` (with its
   digests in `nova_sops_sha256`), sets the version for `check`'s install line and for `join` alike.
2. The one thing a human gives, once: a Tailscale API access token (admin console > Settings > Keys > Generate API
   access token), stored as `TAILSCALE_API_KEY` with the line `check` prints. Everything else in Tailscale `up` does
   with it ("tailscale" below).
3. `nova-fleet up --check ...` with your machines, then `nova-fleet up ...` ("up" below).

```
nova-fleet check
nova-fleet up --check --tools "$NOVA_TOOLS_VERSION" store-1=203.0.113.7 web-1=203.0.113.8
nova-fleet up --tools "$NOVA_TOOLS_VERSION" store-1=203.0.113.7 web-1=203.0.113.8
```

The addresses here are placeholders (RFC 5737 documentation addresses, which answer nothing): put your own machines'
in their place.

## up

`nova-fleet up` takes a fleet from nothing to running in one fixed order. Its inputs are only what you have: the
machines' ssh addresses, the Tailscale API access token in your seat, and, for runners, the repository or organization.
`--grant NAME[,NAME]` names the values each machine's seat is granted (default `NOVA_BUS_PASSWORD`, the one its
services and nova-bus open). After `--`, `-e name=value` pairs reach every play (a variable `up` sets itself, or one
that would skip Tailscale, PostgreSQL, Redis or Ansible, is refused).

```
nova-fleet up --check --tools "$NOVA_TOOLS_VERSION" store-1=203.0.113.7 web-1=203.0.113.8 mac-1=mac-1.local/darwin/arm64
nova-fleet up --tools "$NOVA_TOOLS_VERSION" --runners https://github.com/OWNER/REPO store-1=203.0.113.7 web-1=203.0.113.8
nova-fleet up --tools "$NOVA_TOOLS_VERSION" store-1=203.0.113.7 -- -e nova_pg_version=17
```

(203.0.113.x are placeholder addresses; use your own.)

**What is yours, once, before the first run:**

1. The Tailscale account, and an API access token from it ("tailscale" below: what is yours, once).
2. The control node: ansible-core, `nova-config`, `nova-secrets`, `nova-bus`, `tailscale` (`up` joins it to the
   tailnet and tags it `tag:control`), `psql`, `git`, `gpg`, `sops`, `age`, `age-keygen`, `rsync` and `ssh` on PATH (`gh` too for runners, logged in with `gh auth login`;
   `nova-fleet check` lists what is missing with the line that installs it); a secrets store with this node's seat
   and its pins outside it ([cmd/nova-secrets/README.md](../nova-secrets/README.md)): `NOVA_SECRETS_STORE`, `NOVA_SECRETS_KEY`,
   `NOVA_SECRETS_SEAT` and `NOVA_SECRETS_PINS`, each defaulting as nova-secrets does (`~/.config/nova-secrets/store`,
   `~/.config/nova-secrets/<short hostname>.key`, `~/.config/nova-secrets/pins`, the short hostname). `check` and
   `up` use the same defaults and put them in the environment of what they run; a node laid out that way gets no
   store, key, seat or pins line, and a fresh node with none of them gets none either: the next step makes them.
3. The token in the seat: `nova-secrets set TAILSCALE_API_KEY` (it asks; paste the token, one line
   `tskey-api-...`, and press enter). On a fresh node it first makes the store, a recovery key, this node's key, the
   pins and the seat, and says to back up the recovery key. It expires (90 days by default): a run with an expired one stops at the tailnet step with
   `HTTP 401: TAILSCALE_API_KEY was refused; ...` and the line that makes a new one.
4. Each Linux machine reachable over ssh at its address as the first login its provider gives, and its host key
   already in this node's `known_hosts` (ssh to it once by hand; `up` prompts for nothing). The first login is
   `root` unless `--first-login <login>` names another for every machine, or `<name>=<login>@<address>` for one
   (`ubuntu`, `debian`, `ec2-user`, `admin`: a login other than root must sudo without a password, which `up` checks
   before any play). Its key is ssh's own (the control node's key, or the agent's) unless `--first-key <file>`
   names one. `join` makes the fleet's login (`--user`, default `nova`) through it, and from then on nothing uses
   the first login or its key. A Mac: its login and
   passwordless sudo set up at its console, and the Tailscale app's VPN approved once. Once a machine is on the
   tailnet, `up` reaches it as its login at its tailnet name, by the control node's key (`join` authorizes it) and
   with the host key the `tailscale` play pinned for that name in `known_hosts` (a machine that still serves
   Tailscale SSH publishes its keys in `tailscale status`, and `up` pins those too: `UP HOSTKEY <machine> OK
   pinned=<n>`); it never turns host key checking off.

**The order.** On a new fleet the first machine named holds the fleet's stores; on a fleet that exists, the store
machine is the one nova-config's rows say ("Adding a machine" below). Nothing here is a choice to make again:

| # | Step | What runs | Why here |
|---|---|---|---|
| 1 | control | `nova-fleet check`: every helper (with `--runners`, `gh` and its login), the store, key, seat and pins, `TAILSCALE_API_KEY` read from the seat, `NOVA_PG_DSN` when a fleet exists; anything missing stops the run with its fix line | a run never starts that a missing secret would stop later |
| 2 | tailnet | through the Tailscale API v2 with `TAILSCALE_API_KEY` as Bearer auth (`cmd/nova-fleet/tailnet.go`): `GET /tailnet/-/acl` (HuJSON, its ETag), the fleet's needs (`nova-fleet show tailscale-acl`) merged in as a JSON Patch applied by `github.com/tailscale/hujson` (comments and formatting kept), `POST` with `If-Match` only when that added something (a 412 reads again and retries once); this node joined with a minted `tag:control` key when it is not on the tailnet, else `POST /device/<id>/tags` adding `tag:control`; each machine on the tailnet (the one peer whose MagicDNS label is its name; two such peers are refused) given `tag:nova` (and `tag:runner`) the same way; a machine that is this node itself (`tailscale status` Self, by the same label) is already on the tailnet and gets neither; for each machine not on it, `POST /tailnet/-/keys` mints a single-use, pre-authorized key good for an hour, held in memory and given only to that machine's play as `NOVA_FLEET_AUTHKEY` in its environment (a name of up's own: `nova-secrets exec` drops every inherited name its seat holds, and a seat may hold a `TAILSCALE_AUTHKEY`). Receipt `UP TAILNET OK policy=<merged\|unchanged> control=<tagged\|already\|joined> keys=<n minted> retagged=<n>`; under `--check`, `UP TAILNET WOULD ...` after GETs only. Then one `UP TAILNET MACHINE-SSH <rule> (kept, ...)` line for each ssh rule kept in the policy that names `tag:nova` or `tag:runner` in both its src and its dst: a rule of a shape the fleet did not write is never removed, and it acts only on a machine serving Tailscale SSH, which `ssh-names` fails | the tags exist before a device carries one, and the keys before any join |
| 3 | reach | one `ssh -o BatchMode=yes ... true` per machine: as its login at its tailnet name once it has a row (join closed the address given then), else as its first login at the address given (with `-i <file> -o IdentitiesOnly=yes` under `--first-key`), and a first login other than root must answer `sudo -n true`; `UP REACH <machine> OK <who>@<where>` | a run never starts a play on a machine it cannot reach, or one whose first login cannot make the fleet's login |
| 4 | first | on a new fleet's first machine, from a hand inventory: `tailscale` (no address recorded, firewall later), then `postgres` (nova-config's roles, grants and tables, loaded on the machine as `nova_admin`) and `redis`, their passwords sealed into your seat as `NOVA_PG_PASSWORD` and `NOVA_BUS_PASSWORD` | nova-config's rows live in this PostgreSQL, so it comes before any row |
| 5 | join | `join` for each machine (`services,bench` for the first; the others `bench`; `runner` with `--runners`): its row, its login with passwordless sudo, its tailnet name and host key, its firewall, its seat; with `--runners`, a row that lacks the `runner` role gets it (`UP ROLE <machine> OK runner`). This node itself, named as a machine, is skipped (`UP JOIN <machine> SKIPPED this control node ...`): join's peer check cannot see the node it runs on, so its row must exist already (else the run stops with the `nova-config machine add` line) | the store exists now |
| 6 | grant | `nova-secrets grant <--grant names> --to <machine> --pub <its seat key>` for each machine but this control node (its own seat holds the values; `UP GRANT <machine> SKIPPED`): the values written to that seat's grants file, encrypted to the seat's key (read from the machine over the tailnet with `age-keygen -y`, found in `nova_bin_dir` when `-- -e nova_bin_dir=...` sets it, then `~/.local/bin`; public half only, and pinned in `NOVA_SECRETS_PINS`) and the recovery key; your seat is never a recipient ([secrets guide](../nova-secrets/README.md)) | the seats exist now |
| 7 | store | `git push` of your secrets store to its upstream, if it has one (else a NOTE): a copy off this node; the machines never read it. When the upstream refuses the push (`[remote rejected]`: a protected branch that takes changes only through a pull request), the commits are pushed to the branch `nova-fleet-up/<short commit>` instead, `UP STORE PUSH REFUSED: <upstream> is protected (...); pushed branch <name>; open a PR from it` is printed, the run goes on, and it exits 1 (`UP STORE FAILED`) at the end; open and merge that PR. Any other push failure stops the run here | |
| 8 | refresh | `secrets-refresh` with `nova_seal_store` set to your clone: the encrypted store pushed over the tailnet with rsync to `~/.local/share/nova/secrets/` on each machine (no `.git`, stale files deleted, 0700/0600) | each seat opens what step 6 granted; a rerun of `up`, or `nova-fleet secrets-refresh -- -e nova_seal_store=S`, pushes again |
| 9 | health | `health` on every machine of the bench, runner and services groups (Linux; a Mac is skipped) | the guards are in place before anything long-running starts |
| 10 | tools | `install` on every machine of the bench, runner and services groups, from the release `--tools vX.Y.Z` or a local build `--tools DIR`; a machine with no role (a seat only) gets no software | the services run these binaries |
| 11 | services | `services` | each seat holds its granted values now |
| 12 | runners | `github-runners` on every machine named except this control node, `--runner-count` per machine (default `cores`: one per `github_runner_cores_per_runner`, 4, cores, at least 1) (only with `--runners`; with only this control node named, no runner is registered and the step fails); the play asks GitHub for its token with this node's `gh` login | |
| 13 | bus | `nova-bus who` over the store machine's Redis with the fleet's `NOVA_BUS_PASSWORD`; it names no inbox and sends nothing as a person (receipt `UP BUS OK <redis> present=<n>`) | the fleet is reached end to end |

Each play prints its own receipts; each step adds `UP <step> OK` (or `WOULD-...` under `--check`), and the run ends
with one line per machine, `UP MACHINE <name> OK address=<address> steps=<the steps it passed>`, and on a new fleet
`UP DONE` and the line `next: export NOVA_PG_DSN=...`, which the next `up` and nova-config read the fleet from. A failed step
prints `UP <step> FAILED exit=<n>` (n is ansible-playbook's status, or the failed command's) and `up` exits 1; every
step is idempotent, so the same command resumes. Exit 2 means it could not run (a refusal, a missing prerequisite).

**--check** runs every step under `ansible-playbook --check --diff` and reads the secrets all the same. On a new
fleet a check cannot put the first machine on the tailnet, so PostgreSQL, Redis and every step after them cannot be
checked against anything: they are named (`UP FIRST postgres, redis WOULD-RUN (check: the machine is not on the
tailnet yet)`, `UP JOIN WOULD-RUN ...`), never reported green. A machine that answers no ssh is `UP <machine>
UNREACHABLE <what was tried>`; the later steps are still named for the others, and the check ends `UP CHECK FAILED
unreachable=<n>`, exit 1. The grant step reads the `--grant` values from your seat and prints `UP GRANT <machine>
WOULD-GRANT <names>`; a value it cannot read stops it with the read's reason. Nothing is pushed, applied, granted or
sent under `--check`; the one thing it writes on this node is a host key pin.

A machine's seat holds no values until they are granted to it: join's bus check on a new machine is a `NOTE` until
step 6 has granted `NOVA_BUS_PASSWORD` and step 8 has pushed it.

**Adding a machine.** A fleet that exists is read from nova-config, never guessed from the command line: export
`NOVA_PG_DSN` (the line `up` printed, `postgres://nova_config@<store machine>:5432/nova`; `check` notes it once your
seat holds `NOVA_PG_PASSWORD`), then name the new machines, and the ones you want converged again if you like. A
first run that stopped after PostgreSQL resumes without it: the first machine named is the store `up` made, and its
rows are read from there.

```
export NOVA_PG_DSN=postgres://nova_config@store-1.<tailnet>.ts.net:5432/nova
nova-fleet up --check --tools "$NOVA_TOOLS_VERSION" web-2=203.0.113.9
nova-fleet up --tools "$NOVA_TOOLS_VERSION" web-2=203.0.113.9
```

The store machine is the row with the role `services`; no machine named becomes "first", and PostgreSQL and Redis are
not installed again. A machine with a row is reached as its row's user at its row's address, and `join` runs its
login play not at all (its login exists, and the policy lets no one in as root over the tailnet); a machine without one is
reached as its first login at the address given, as on the first run:

```
nova-fleet up --tools "$NOVA_TOOLS_VERSION" --first-login ubuntu --first-key ~/.ssh/provider_key web-2=203.0.113.9
nova-fleet up --tools "$NOVA_TOOLS_VERSION" web-2=ubuntu@203.0.113.9 web-3=203.0.113.10
```

A machine that answers nowhere stops the run before any play, naming what was tried and the fix:
`nova-fleet up: web-2 does not answer ssh: ... (tried root@203.0.113.9); run: ssh root@203.0.113.9 true  # a provider
whose first login is not root: --first-login <login> or web-2=<login>@203.0.113.9, and --first-key <file> for its key`.
A first login that answers and cannot sudo without a password stops it too, under `--check` as well:
`nova-fleet up: web-2 answers ssh as ubuntu at 203.0.113.9, and ubuntu cannot sudo without a password (...): join
makes the fleet's login through it; run: ssh -t ubuntu@203.0.113.9 sudo visudo -f /etc/sudoers.d/90-ubuntu  # the one
line: ubuntu ALL=(ALL) NOPASSWD:ALL`.

## Rented machines

A box rented by the hour is a `rented` row of the record (cmd/nova-config/README.md, "Rented boxes"), in a table no
play reads, so an `aws` box (ssh from the control node only) is never in an inventory: what it is for, who rented it and when, when it is to be returned, its id at the provider and its hourly price with
where the price came from. `rented` lists them from the record, so none bills unseen:

```sh
nova-secrets exec --only NOVA_PG_PASSWORD -- nova-fleet rented
```

```text
RENTED spill-1 provider=aws id=i-0abc1234 req=- asked=- market=spot age=1h30m rate=0.60 price_from=provider cost=0.90 return_by=2026-11-13T09:00:00Z purpose="load rows"
RENTED bench-x provider=latitude id=sv_1 req=- asked=- market=- age=3h00m rate=4.06 price_from=recalled cost=12.18 return_by=2026-11-13T04:00:00Z PAST purpose="XDP pair"
RENTED TOTAL machines=2 rate=4.66 cost=13.08 unknown=0 past=1
```

`cost` is the hourly price times the hours since `rented_at` (a stopped box bills less: its disk only); `cost=unknown`
is a box with no price or no `rented_at`, left out of the total and counted in `unknown`; `price_from=recalled` is a price not read from the provider; `PAST`
marks a box kept beyond its `return_by`. `--json` prints the same as one object, `{"machines": [...], "total":
{...}}`. A record that cannot be read is one line naming nova-config's own error; a nova-config on PATH older than the
rented rows has no `rented` verb, and its refusal is passed on, never read as a fleet with none rented.

## The plays

| Play | Purpose | Needs |
|---|---|---|
| `audit` | every Linux bench and runner checked against `runner-requirements.tsv`, as the login CI jobs run as; read-only, a machine with a gap fails naming it | nothing; `-l` the machines |
| `ci-rules` | the CI rules on every GitHub Actions runner a machine runs, whoever registered it (bench, runner and services groups) | nothing; `-l` the machines |
| `github-runners` | GitHub Actions runners on every machine of the runner group, as its record's runner_scopes name them | the machine records' `runner_scopes` (or `github_runner_url`, `github_runner_scope` for one url); `gh` on the control node logged in with admin on each repository or organization |
| `health` | guards that let a Linux machine get itself out of trouble, a disk report and clean, and a Mac's Go build cache trim (bench, runner and services groups) | nothing |
| `install` | install one nova-tools release, by tag, on the machines named (bench, runner and services groups) | `nova_tools_version` (a tag `vX.Y.Z`; no default) |
| `netwatch` | a Linux machine that loses its network or the tailnet recovers by itself, and a hung one resets (bench, runner and services groups) | nothing; `-l` the machines |
| `join` | a new machine joins the fleet: its row, its login, the tailnet, the tools a seat needs, its seat | the machine as the argument; its row's fields; `NOVA_PG_DSN` and `NOVA_PG_PASSWORD` in the environment, and `NOVA_FLEET_AUTHKEY` (`up` mints it) or `TAILSCALE_AUTHKEY` for a machine not on the tailnet |
| `postgres` | the PostgreSQL nova-config keeps the fleet in, on each machine of the services group | `nova_seal_*` to seal its password |
| `redis` | the Redis nova-bus talks to, on each machine of the services group | `nova_seal_*` to seal its password |
| `runner-packages` | the packages a CI runner's tests need, from `nova_runner_packages` (a CI toolchain by default), on each machine of the bench and runner groups (a sprint card's gate needs what a CI job needs); packages only, nothing started. `join` runs it for a bench or runner | nothing; `-l` the machines |
| `sdk-toolchain` | the pinned language toolchains in each bench and runner login's `~/sdk` (Go, Node, Dart, Erlang/Elixir, Java, .NET 10 and 8, linters, fuzzers, provers), Rust through a pinned rustup; as the login, only what is missing. `join` runs it for a bench or runner | nothing; `-l` the machines |
| `seat` | each machine's seat in the secrets store | `nova_seal_store` |
| `secrets-refresh` | the control node's encrypted store pushed to every machine with `ansible.posix.synchronize` (`verify_host: true`, no `.git`, stale files deleted, 0700/0600); no machine runs a timer (pull mode is retired; one left is removed) | `nova_seal_store` (the control node's clone) to push |
| `services` | one supervised unit per service record, on the machine the record names (bench, runner and services groups) | the inventory's `nova_services` |
| `ssh-names` | `ssh <machine>` by its tailnet name: each login's ssh config and the fleet's host keys from the record, then every machine-to-machine login checked refused and the control node's checked open (all machines; one asleep or away is reported offline) | nothing |
| `windows-runners` | a WSL machine held running from boot, and its Windows-side GitHub Actions runners from its record's `windows_runners`, through WSL interop | the record's `windows_runners` and `windows_admin_runners`; `gh` on the control node logged in with admin on each repository or organization; each admin runner's password in the machine's seat |
| `tailscale` | each machine on the tailnet, its address the MagicDNS name, its firewall closed to all but the tailnet | `TAILSCALE_AUTHKEY` in the environment for a machine not on the tailnet (`up` mints it) |

```
nova-fleet install -- -e nova_tools_version="$NOVA_TOOLS_VERSION" -l bench
nova-fleet services --check --diff
nova-fleet github-runners -- -e github_runner_url=https://github.com/OWNER/REPO -e github_runner_scope=repo
```

`nova-fleet plays` is the list in the binary you hold; `nova-fleet show <play>` prints one, and `nova-fleet show
tailscale-acl` what `up` merges into the tailnet policy. Every play is idempotent and prints one receipt line per machine; under
`--check` the receipt ends `(check: nothing changed)`. A play whose machine groups match no host (no row has the
role, or `-l` names none in them) is refused before it runs, exit 1: `nova-fleet github-runners: no host is in runner
(no machine row has that role, or -l names none in it), so the play would do nothing; run: nova-config machine set
<machine> --roles <its roles>,runner --actor <you>, then nova-fleet github-runners again`. A secret is never a variable: it reaches a run as an
environment variable from `nova-secrets exec` on the control node, and a machine only on a command's stdin, so a
play that needs one runs as `nova-secrets exec --only NAME -- nova-fleet <play> ...` (`nova-fleet up` does this
itself).

## Inventory

By default the inventory is a script in the extracted directory that execs `nova-config inventory` (found on PATH),
and `ANSIBLE_INVENTORY_UNPARSED_FAILED=true` is set so a failing script fails the run rather than reading as an
empty fleet. nova-config (`cmd/nova-config/README.md`) keeps the machines and services as rows; `nova-config inventory`
prints each machine as a host in the groups its roles name (`bench`, `runner`, `services`, so `-l bench` runs on
every bench), with its service rows as the host variable `nova_services` (always present; empty means none).

To use your own inventory instead, name a file or script:

```
nova-fleet services --inventory ./hosts.ini --check
nova-fleet install --inventory ./my-inventory.py -- -e nova_tools_version="$NOVA_TOOLS_VERSION"
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
nova-secrets, nova-reserve, nova-fleet) for linux/amd64, linux/arm64 and darwin/arm64, each asset
`<tool>_<tag>_<os>_<arch>`, with one `SHA256SUMS`. The play reads `SHA256SUMS`, downloads each asset and checks
its digest before putting it in place. A binary that already holds the digest is left alone, so a second run changes
nothing and `--check --diff` is clean. There is no default version, and a platform the release does not ship is
refused before anything is written.

| Variable | Default | |
|---|---|---|
| `nova_tools_version` | none, required | the tag, `vX.Y.Z` |
| `nova_tools_names` | the seven tools | which tools to install (and which `argv[0]` services prefixes) |
| `nova_bin_dir` | `~/.local/bin` | where they go; v2 binaries replace v1's of the same name there, so a v1 sprint still running from it stops working after `up` (accepted) |
| `nova_tools_repo` | `mas-bandwidth/nova-tools` | whose release (`--repo`); `--names` is `nova_tools_names`, empty for every binary listed |
| `nova_tools_release_url` | `nova_tools_repo`'s GitHub releases | `<base>/<tag>/<asset>` |

**Another repository's release.** A dependent tool whose release is laid out the same way (assets
`<name>_<tag>_<os>_<arch>` and one `SHA256SUMS`) installs with `--repo` and `--tag`, getting every binary its
`SHA256SUMS` lists for each machine's platform unless `--names` says which, checked the same way:

```
nova-fleet install --check --repo mas-bandwidth/nova-sprint --tag v2.0.0 --names nova-sprint,nova-sprint-dash bench
```

A release from before nova-fleet shipped has no nova-fleet asset; install one with
`-e '{"nova_tools_names":["nova-bus","nova-cairn","nova-config","nova-sandbox","nova-secrets","nova-reserve"]}'`.

**A local build.** With no release yet, or to try a change, build the binaries on the control node laid out as a
release is and install from that directory (Linux and macOS hosts; each file is checked against its `SHA256SUMS` line
before it is copied):

```
v=local; mkdir -p dist
for t in bus cairn config fleet reserve sandbox secrets; do
  for p in linux/amd64 linux/arm64 darwin/arm64; do
    GOOS=${p%/*} GOARCH=${p#*/} CGO_ENABLED=0 go build -trimpath -o dist/nova-${t}_${v}_${p%/*}_${p#*/} ./cmd/nova-$t
  done
done
(cd dist && sha256sum nova-* > SHA256SUMS)          # macOS: shasum -a 256 nova-* > SHA256SUMS
nova-fleet install -- -e nova_tools_version=local -e nova_tools_local_dir=$PWD/dist -l <machine>
```

`nova-fleet up --tools $PWD/dist` does the same for every machine.

**The v1 gh wrapper.** A `<nova_bin_dir>/gh` that names `GH_PUSH_TOKEN` is v1's push-credential wrapper, which runs gh
under a secrets store v2 does not have, ahead of the real gh on PATH; the play removes it, with each git credential
helper entry of the login that names it (such a helper can only fail; other entries stay). Only the helpers' names are
read, never their values. Any other `gh` there is left alone.

**opencode.** After the tools, the bench group gets the opencode nova-sprint's workers run (`roles/opencode`):
`nova_opencode_version` (1.18.20; 1.x, since 2.x starts a background service on a fixed port) from sst/opencode's
release archive for the platform, checked against `nova_opencode_sha256` (the digests GitHub publishes for each asset),
unpacked as `<nova_bin_dir>/opencode.bin`, with `<nova_bin_dir>/opencode` a wrapper that sets `nova_opencode_env` and
execs it. The archive is kept in `~/.cache/nova/opencode`, so a second run downloads and changes nothing. Every service
unit's PATH starts with `nova_bin_dir` (`nova_service_path`), so a worker finds this one ahead of Homebrew's or
`/usr/local/bin`'s, which are left alone, except the hand copies of this same opencode each bench had there before the
role (`nova_opencode_hand_dir`): a file identical to the role's `opencode.bin`, or a hand wrapper with an identical
`opencode.bin` beside it, is removed. One line per machine: `OPENCODE <host> version=<v> path=<wrapper>`; a Windows
machine is skipped.

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
- A Linux unit sets `LimitMEMLOCK=` `nova_service_memlock` (`1G`): the kernel charges io_uring's locked pages to the
  login across all its processes, and systemd's 8 MiB default refuses concurrent rings. A user unit is held to its user
  manager's limit, which the health play sets to the same size.
- A row with `keys` is wrapped in `nova-secrets exec --store <store> --as <seat> --key <key_dir>/<seat>.key --sops
  <sops> --only <keys> --`, so values reach the command and never the unit file.
- Rows without `every` are kept alive; `enabled` defaults to true; `enabled false` writes the unit and does not load it.
- Every unit carries a mark. A marked unit no row names, or one in the other place (user versus system), is retired;
  `--check` lists it as `WOULD-RETIRE`. A unit of another tool is left alone and named in a `NOTE`;
  `nova_retire_units` names any to retire.
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

GitHub Actions runners on every machine of the `runner` group, each registered to one repository or organization and
run as a service that starts at boot.

**The record.** A run without `-e github_runner_url` makes each machine's runners exactly its record's `runner_scopes`
(`nova-config machine set <machine> --runner_scopes '<url>=<count>[:<label>+<label>],...'`; cmd/nova-config/README.md),
with the record's `runner_env`: each scope's `count` runners, labelled as below plus the scope's labels. A runner on the
machine that no scope wants (a directory under `<home>/actions-runner` or a `github-runner` unit or daemon named
`<slug>-<i>`: above a scope's count, or of a url the record does not name) is retired and removed from GitHub, its url
read from its `.runner`, and prints `RUNNER-STRAY <machine> <slug>-<i> url=<url>`. Then GitHub's list of every scope the
fleet's records name is read: an entry named `<machine>-<i>` that the record does not want is deleted from GitHub when
it is offline (its machine side is gone) and left when online, each printing `GITHUB-STRAY <machine> <name> <path>
<status>`. Each machine ends with `RUNNERS-RECORD <machine> scopes=<n> want=<n> retired=<names>`. A record with
`runners` above 0 and no `runner_scopes` is refused, not read as none. A Linux unit is the whole of its service: a
drop-in directory beside it (`<unit>.service.d`) is removed. Before a runner is stopped or restarted onto a changed
unit, the job it is running ends (`tasks/wait-idle.yml`: a `Runner.Worker` from its `bin`, polled every
`github_runner_idle_delay` (15) seconds `github_runner_idle_retries` (240) times; still busy then, the run fails and
the runner keeps running). `-e '{"github_runner_hold": ["<dir>"]}'` holds runners by directory name (one running a
job nothing resumes, such as a multi-hour model check): their changed file is written, nothing waits on or restarts
them, and the run prints `RUNNER-HELD <machine> <dir>` for each, the restart owed to a later run. `-e github_runner_url` with `-e github_runner_scope` is a run for that one url instead, with
`runners` (or `runner_count`) as its count; the machine's other runners are left as they are.

**Labels:** a runner's labels are
`fleet` (`github_runner_base_labels`, which the Windows side carries too), its machine's inventory groups (the row's
roles: `bench`, `runner`, `services`; `ungrouped` and `all` are left out), the host's own os (`linux`, `darwin`) and
arch (`amd64`, `arm64`) as its facts report them (a machine row's `os` and `arch` fields say how it is reached, never
what it is labelled), plus `github_runner_labels`; so a bench machine's runner is labelled
`amd64,bench,fleet,linux,runner`, and a job selects it with `runs-on: [self-hosted, Linux, fleet]`.
Linux (amd64, arm64; WSL2 with systemd counts as Linux) and macOS. On macOS the runner is a LaunchDaemon dropped to the login, not a
LaunchAgent: a LaunchAgent runs only inside a logged-in GUI session, and a CI machine comes back from a reboot with
none. Only a run needs sudo for that file (`/Library/LaunchDaemons` is world-readable, so `--check` reads and diffs it
as the login and finishes without a password). The daemon's `ProcessType` is `Interactive`: launchd throttles the CPU and
I/O of a job with no `ProcessType` as background work, and a CI job inherits its runner's limits. Linux and macOS write the service files with sudo, so the login needs
passwordless sudo: `join.yml` sets it up on Linux (`/etc/sudoers.d/90-nova-<login>`); on macOS set it at the console. On Debian and Ubuntu the play installs `libpam-systemd` where `dpkg-query` says it is missing: without pam_systemd the login's user manager (`user@<uid>`) never starts, and no user unit can run.
No command here asks for a sudo password: an interactive prompt is a stall for an AI running the play.
A stop or restart ends the whole runner: on Linux the unit sets `RUNNER_MANUALLY_TRAP_SIG=1`, so the release's `run.sh`
passes the stop to its listener as SIGINT and waits for it (a running job is cancelled by the runner, not killed), and
`KillMode=mixed` kills whatever is left in the unit once `run.sh` exits, or after `TimeoutStopSec` (5 minutes). On macOS
launchd kills what is left of the job's process group.
A Linux unit sets `LimitMEMLOCK=infinity`: the kernel charges locked memory (io_uring rings) to the login across all its
runners, and systemd's 8 MiB default refuses that. It also sets `TMPDIR`, by default `<home>/.cache/runner-tmp`
(`github_runner_tmpdir`), one directory for all the login's runners that the play makes (the login's, `0700`) before
the units start: a job's temporary files land there and not in `/tmp`, and the health play's disk clean reads the
directory from the units. A macOS daemon gets no `TMPDIR` unless `github_runner_env` names one, so its jobs use `/tmp`,
which macOS cleans. A macOS daemon starts with `/opt/homebrew/bin` on its PATH, before the
system's, so a job finds Homebrew's formulae and its `python3`.

**The runner login.** The runners run as `github_runner_user`: on Linux `ghrunner`, a login of their own, so
a CI job has no root and no way to get it; on WSL and macOS the login the play connects as (WSL's interop gives any
login root). `-e github_runner_user=<the fleet
login>` keeps a Linux host's runners as the fleet login for a run (for every run, and every play that reads it, set `github_runner_user` in its `host_vars` file); another name on macOS fails the run. The play makes the login (home
`/home/<login>`, `github_runner_home`, mode `0750`) with its password locked, in no group but its own and `kvm` (where
the host has one), refuses an account of that name that is not a runner login already (a uid below 1000, or any other
group), writes `/etc/ssh/sshd_config.d/90-nova-runner-login.conf` (`DenyUsers <login>`, checked with `sshd -t`, then
with `sshd -T`), refuses the login's own connections to ssh on any tailnet address (Tailscale SSH reads no
`DenyUsers`: an nftables table, `/etc/nova-runner-login.nft`, loaded at boot by `nova-runner-login-net.service`), and
fails the run unless `sudo -l -U <login>` says it is not allowed to run sudo. It refuses a WSL host whose interop is on (any login runs `wsl.exe -u root` through it, `RUNNER-LOGIN-WSL-INTEROP`), closes the fleet login's home (`0750`), and fails the run on any path the login can write or owns outside its home, the kernel's directories, each snap image's /dev (read-only and mounted nodev) and the shared scratch places, on every local filesystem, found by `find` run as the login (`RUNNER-LOGIN-WRITABLE`; nothing found is changed). The runners'
directories, release cache and `TMPDIR` are then in its home, written as the login (root follows no link it could
plant there; root's only act there is deleting a runner directory by name), and the runners' default `PATH` starts
with `/opt/nova/bin` (`nova_shared_dir`): root's copies of the fleet login's tools, which it runs and cannot change (made here, refreshed by `install` on a runner whose runners are another login's). Its `~/sdk` names `/opt/nova/sdk`, the fleet login's shared SDK, once `sdk-toolchain` has put one there (`nova_sdk_shared`, on for a runner whose runners are another login's), so a workflow's `$HOME/sdk` works for it as for the fleet login. A runner whose unit runs it from another directory (the fleet
login's, before the switch) moves: `RUNNER-MOVE <runner> from=<dir> to=<dir> user=<login>`, then, once its job ends,
its unit stops, it registers in the new directory under the same name (`config.sh --replace`, with a fresh token), the
unit restarts it there and the old directory is deleted; a move that fails starts the runner where it ran and fails
the run with `RUNNER-MOVE-FAILED`. A held runner is not moved: the run fails with `RUNNER-HELD` naming it. The
record's `runner_env` must name nothing in the fleet login's home (the run fails naming the keys, before the machine
is changed). With `nova_runner_vm_kernel` (on) an x86_64 host gives it the VM tier: root's copies of the kernel
images in `/opt/nova/vm-kernels` (a kernel hook adds each new one), its `~/vm-kernels` naming them, and the run fails
with `VM-TIER-MISSING` while vng, qemu, a writable `/dev/kvm`, the running kernel's image or its modules are missing
for it; a host with no VM tier sets `nova_runner_vm_kernel: false` in its `host_vars` file. A host moves to the runner
login in four runs, each `--check --diff` first: `install` (the shared tools and the shared SDK's directory),
`sdk-toolchain` (the shared SDK, while `~/sdk` stays in use until it is whole), `runner-packages`, then `github-runners`
once the record's `runner_env` names the runner login's paths.

**Public repositories.** A self-hosted runner runs the code of any workflow that reaches it, and on a public repository
that includes pull requests from forks. Use these runners on private repositories, or restrict which workflows and
events may use them.

| Variable | |
|---|---|
| `github_runner_url` | no default. `https://github.com/<owner>/<repo>`, or `https://github.com/<org>` |
| `github_runner_scope` | no default. `repo` or `org`; refused if the url is the other shape |
| `github_runner_labels` | extra labels, a list (default none), added to the machine's labels |
| `runner_count` | one url: overrides the row's `runners` for one run; 0 retires every runner; `cores` is one runner per `github_runner_cores_per_runner` (4) of the row's `cores`, else the host's own, at least 1. A host with neither is refused |
| `github_runner_state` | one url: `present` (default) or `absent`, which retires every runner of that url |
| `github_runner_deregister` | true: retiring removes the runner from GitHub (needs a removal token) |
| `github_runner_env` | the service's environment, `{NAME: value}`, inherited by every job; default the machine record's `runner_env` (the inventory's `nova_runner_env`; `nova-config machine set <machine> --runner_env '<json-object>'`, cmd/nova-config/README.md), so every run for any url writes it. A `PATH` replaces the default (`nova_bin_dir` first, then the system's); a `TMPDIR` replaces `github_runner_tmpdir`. `-e github_runner_env` replaces the record's for one run; there a path under the login's home may be written `{{ ansible_facts['user_dir'] }}/...`, rendered per machine (the record holds literal values). [docs/examples/github-runners-env.yml](../../docs/examples/github-runners-env.yml) is a vars file with the url, scope, labels and an environment for Ubuntu runners, for `-e @github-runners-env.yml` (read from the directory you run in) |
| `github_runner_tmpdir` | a Linux runner's `TMPDIR` when the environment names none: `<home>/.cache/runner-tmp` |
| `github_runner_user` | the login the runners run as: `ghrunner` on Linux, a login of its own ("The runner login"); the play's login on WSL and macOS; on Linux the play's login keeps the old way |
| `github_runner_home` | that login's home: the play's login's, or `/home/<login>` |
| `github_runner_base_labels` | `[fleet]`: the labels every runner gets, Linux, macOS and the Windows side |
| `github_runner_version`, `github_runner_sha256` | the pinned actions/runner release and its digests |
| `github_runner_ci_limit_seconds` | 120: the per-commit job limit every runner enforces ("CI rules") |
| `github_runner_ci_push_branches` | `[main, sprint/*]`: the branches a push may run CI on, as globs where `*` and `?` stop at a `/` (tags always may); any other branch gets CI through its pull request |

**The token.** The play asks GitHub for it on the control node with that node's own `gh` login, `gh api -X POST
<repos|orgs>/<path>/actions/runners/registration-token -q .token` (`remove-token` to retire), never a pasted token,
and hands it to the runner on its standard input: never an argument, the inventory or a file. On the machine it exists
in one place, the environment of `config.sh` until that exits (the runner reads `ACTIONS_RUNNER_INPUT_TOKEN`); the task
that carries it is pipelined (`ansible_pipelining: true`), so the module runs from the ssh pipe and is never written
to the machine's disk. A registration that fails prints `RUNNER REGISTER <name> rc=<n> <the cause, token redacted>`,
never `censored` alone. The token is asked for when a runner needs registering or removing, under `--check` too, so a
green check is never followed by a run that fails on the login; a refusal prints `GITHUB RUNNER TOKEN <machine>: gh
api on the control node failed, rc=<n>: <gh's stderr>` and `gh auth login`. `-e github_runner_gh=<path>` names
another gh.

```
nova-fleet github-runners --check --diff -- -l MACHINE
nova-fleet github-runners -- -l MACHINE
nova-fleet github-runners --check --diff -- -e github_runner_url=https://github.com/OWNER/REPO -e github_runner_scope=repo
nova-fleet github-runners -- -e github_runner_url=https://github.com/OWNER/REPO -e github_runner_scope=repo
```

**The check.** After the run, GitHub's own list of runners is read with `gh api <repos|orgs>/<path>/actions/runners`
on the control node (a `gh` login with admin on the repository or organization), giving a new runner a minute to come
online, and each machine prints one line: `RUNNERS <machine> want=<n> online=<n> listed=<names>`. A wanted runner that
is not online fails the run; `verified=no reason=...` says why the list could not be read. `-e
github_runner_verify=false` skips it.

**What it does.** Per runner: the pinned release, checked against GitHub's digest, unpacked in
`<home>/actions-runner/<owner>-<repo>-<i>`; registered as `<machine>-<i>` unless
`.runner` is there; one service per runner, enabled at boot. The labels a runner was registered with are recorded in
its `.labels`. A registered runner that lacks a wanted label (or has no `.labels`) gets it from the control node's gh,
`gh api -X POST <repos|orgs>/<path>/actions/runners/<id>/labels` with the body on stdin, never by registering again,
so it keeps its session and the jobs queued for it; `.labels` is then rewritten from what GitHub holds, and the run
prints `LABEL-ADD <runner> added=<labels> labels=<labels>`. A runner GitHub does not list fails the run, its labels
untouched. A label it holds beyond the wanted is reported as `LABEL-EXTRA` and kept: pass it in `github_runner_labels`,
or retire the runner (`-e github_runner_state=absent`) and converge again to drop it. A changed unit restarts its
runner once the job it is running has ended. A
machine whose count drops has its highest-numbered runners retired and removed from GitHub, with a removal
token (`.../actions/runners/remove-token`, asked for the same way). A second run changes nothing.

**Plan.** Under `--check` each machine prints `RUNNER-PLAN <machine> want=<n> register=<names> retire=<names>
labels=<labels>`, the runners named `<machine>-<i>` as GitHub will list them, and each registered runner that lacks a
label prints `LABEL-PLAN <runner>: lacks <labels>` (`no record of its labels` without `.labels`); nothing is asked of
GitHub.

**CI rules.** Every runner the play sets up enforces the CI rules ("CI rules" below); its receipt is the
`CI-RULES` lines.

**Rollback.** Retiring asks gh for a **removal** token the same way; run with `-e github_runner_state=absent`:

```
nova-fleet github-runners --check -- -e github_runner_url=https://github.com/OWNER/REPO -e github_runner_scope=repo -e github_runner_state=absent
nova-fleet github-runners -- -e github_runner_url=https://github.com/OWNER/REPO -e github_runner_scope=repo -e github_runner_state=absent
```

That stops and removes the services, removes the runners from GitHub and deletes their directories. With `-e
github_runner_deregister=false` no token is read and the GitHub entries stay for you to delete. A removal token also
expires in an hour.

## Windows runners of a WSL machine

A WSL machine's Windows side runs its own runners (a repository's or organization's Windows CI). They are
a field of the machine's record and a play, like everything else on the fleet:

```
nova-config machine set bench-1 --windows_runners 'https://github.com/<owner>/<repo>=3:<label>,https://github.com/<org>=2:<label>' --actor alice
nova-fleet windows-runners --check -- -l bench-1
nova-fleet windows-runners -- -l bench-1
```

`windows_runners` is `<url>=<count>[:<label>+<label>]`, comma-separated (cmd/nova-config/README.md); the inventory hands it to
the play as `nova_windows_runners`. A machine without it gets no runners (only the keepalive below); one with it that is not WSL with interop is
refused. **The channel** is the one `ci-rules` already uses: from the WSL login, `powershell.exe` by its full path
runs the role's `files/runner-windows.ps1`, staged to `C:\nova-stage` (below, "The stage directory"). It acts as the Windows account
that started WSL, elevated; no Windows login, password or key is involved, and no port is opened. **Each runner** is
`<machine>-win-<slug>-<i>` in `C:\actions-runner\<slug>-<i>` (`<slug>` is `<owner>-<repo>`, or `org-<org>` so an
organization never shares a slug with a repository), the pinned release checked against its digest (a directory left
by an interrupted unpack is moved aside and unpacked again), a Windows service run as `NETWORK SERVICE` (`config.cmd --runasservice`) that starts
at boot. **Labels** are `fleet` and the machine's name plus the record's extras (`github_runner_wsl_labels`); GitHub adds
`self-hosted`, `X64` and `Windows`. Unlike the Linux runners they carry no roles, os or arch, so a job that selects
`[self-hosted, bench]` never lands on Windows. Each runner's `.env` gets the job `PATH` (`github_runner_windows_path`),
its own `GOCACHE` and `DOTNET_INSTALL_DIR`, and then the CI rules. **The token** comes from `tasks/token.yml` as for the
other runners (one per repository or organization) and reaches `config.cmd` on stdin, as `ACTIONS_RUNNER_INPUT_TOKEN`:
never an argument, a file or a log line. A runner is whole when its directory holds `.runner`, its Windows service
exists and GitHub lists its name; a whole runner is left alone, so a second run registers nothing. A half registration
(`.runner` but no service or no GitHub entry) is repaired: its `.runner` and credentials move into the runner
directory's own `retired-<yyyymmdd>\`, its service is removed, and it registers again with `--replace`. A run that
cannot read GitHub's list for a target refuses, rather than take a whole runner for a half one. Each machine prints `GITHUB-RUNNERS host=<machine> windows=<n> registered=<n> online=<n>`
from its directories and GitHub's own list; a run where any is short fails. `--check` reads both, asks for the tokens it
would need, and prints `RUNNER-PLAN <machine> windows=<n> register=<names> repair=<names> admin=<name>@<account>,... unsealed=<names> labels=<labels>`.

### Windows runners run as a local administrator

A job that must change the machine (Application Verifier writes `HKLM\...\Image File Execution Options`) cannot run
as `NETWORK SERVICE`, and giving `NETWORK SERVICE` that right would let every CI job on the machine reach SYSTEM. Such
a job gets its own runner instead, in a second field of the record:

```
nova-config machine set bench-1 --windows_admin_runners 'https://github.com/<owner>/<repo>=ci-admin:<label>@<workflow>.yml' --actor alice
nova-fleet windows-runners --check -- -l bench-1
nova-fleet windows-runners -- -l bench-1
```

`windows_admin_runners` is `<url>=<account>:<label>[+<label>]@<workflow>`, comma-separated, one runner per entry;
`<workflow>` is the file name in the repository's `.github/workflows` of the one workflow the runner runs, and is
required. The inventory hands it to the play as `nova_windows_admin_runners`, each entry with `secret`, the nova-secrets
name of the account's password (`NOVA_WINRUNNER_<ACCOUNT>_PASSWORD`, the account upper-cased with `-` as `_`), and
`workflow`, `<owner>/<repo>/.github/workflows/<workflow>@refs/heads/main`. **The url is a repository**, never an
organization: every repository of an organization could send an organization's runner jobs. **The runner** is
`<machine>-win-<slug>-<account>` in `C:\nova-admin-runners\<slug>-<account>` (`github_runner_wsl_admin_dir`; nova-config
refuses two entries whose `<slug>-<account>`, compared without case, would be the same directory), with its own work
folder and the CI rules of the other Windows runners, and the same registration, repair and counts: it is one more
runner in the `windows=` of the receipt. Its job `PATH` is the other runners' without the ccache directory, and its
`.env` holds only the role's lines, `CCACHE_DISABLE=1` among them (every runner writes its ccache, so a job here would
link what any of them put there), `CCACHE_CONFIGPATH` and `CCACHE_CONFIGPATH2` pointing into its sealed directory (a
disabled ccache still reads its configuration, whose `compiler=` picks what runs, and its system file is
`%ALLUSERSPROFILE%\ccache\ccache.conf`, which any job may make) and the CI rules' hooks; any other line is dropped. **Its
labels** are only the entry's (GitHub adds `self-hosted`, `Windows` and `X64`), and must be its own: nova-config refuses
on the write, and the inventory refuses when the store holds one, a label (compared without case) that a job of the same
repository could select on another runner: any label of the machine's own `windows_runners` or `runner_scopes`, its
name, and with `runner_scopes` its roles, os and arch; on any machine with a runner of the repository or its owner, that
runner's labels and the machine's name; any other admin runner's label of the repository, on any machine; and one GitHub
gives runners (`self-hosted`, `windows`, `x64`, `linux`, `macos`). A write to the other runner is refused the same way.
The run also fails, before anything changes, on one of the role's Windows labels (`fleet`, the machine's name). So no
job meant for another runner lands on it. **The account** is
the local user `<account>`, made by the play with a fixed description (`$AccountMarker` in the script): in
`Administrators` (by its SID, `S-1-5-32-544`), allowed to log on as a service, denied interactive and
remote-interactive logon, its password never expiring and not changeable by the user. A user of that name without the
description (a person's, or a built-in one) is never changed: the run fails with `ACCOUNT-NOT-OURS`; nova-config
refuses Windows' built-in names and the machine's user. A service runs with its account's full administrator token
(services are not filtered by UAC).

**The directory** is the admin's alone, and it is never opened, not even for a moment. Every other runner's job runs
as `NETWORK SERVICE`, which may add files to any folder under `C:\ProgramData`, and under `C:\` a folder inherits write
access for every signed-in account: a file such a job put in the admin runner's directory, or a binary it changed
there, would run as an administrator, and so would a `Directory.Build.targets` or `NuGet.Config` it put in any folder
above it (MSBuild and NuGet read them from every folder above a checkout). So the admin root is directly under `C:\`,
where on a default Windows those accounts may make a folder but not a file, and the install and logon steps prove that
first (`[NovaTree]::Above`): no folder above the admin root may let `NETWORK SERVICE`, SERVICE, LOCAL, Authenticated
Users, Users or Everyone (the openers) make, change or delete a file in it, hold a folder one of them owns (made by one
of them) or whose owner cannot be read, or hold a file one of them may change; else `ABOVE-OPEN <path> ...` fails the
run. (captain's `C:\` gives Authenticated Users Modify on the drive itself, read on 2026-11-12, so an admin runner
there refuses with `ABOVE-OPEN C:\` until `C:\` is back to Windows' default: Authenticated Users may only make a
folder in it.) A folder an opener has made in `C:\` before a job starts (such as `C:\.cargo`, which cargo reads above
a checkout) is refused by the job-started hook, below; one made while the job runs is not caught. So an admin
workflow runs no tool that reads its configuration from a folder above the checkout (cargo, npm and node, pip): MSBuild
and NuGet read files there, which `C:\` does not let those accounts make. Then the install step makes `C:\nova-admin-runners` and the runner's directory sealed in
the call that creates them: owned by `Administrators`, nothing inherited, full control for SYSTEM and `Administrators`,
and on the runner's directory its account. One that was already there and is not sealed so was made by someone else
and is never used or changed: `PLANTED <path> ...` fails the run and names the command that deletes it. Nothing in
the directory is ever reset or repaired. The runner zip is copied from the cache into the directory, that copy is
hashed against the pin, and the runner is extracted from that same copy. Registration passes `--disableupdate` and not
`--runasservice` (which would grant `NETWORK SERVICE` the directory and start the listener as `NETWORK SERVICE`); the
logon step makes the service. On every run, before the service is given the account, the whole tree is proved: the two
directories hold only those entries, and everything under the runner's directory is owned by SYSTEM, `Administrators`
or the account, inherits, and has no entry of its own; else `ACL-OPEN <path> ...` fails the run. Every file the copied
zip lists must still be that file, with nothing else in `bin` or `externals`, else `TAMPERED <path> missing|changed|extra`.
A new pin replaces `bin` and `externals` from a new copy while the runner is idle (`UPGRADE-OWED <dir> busy` while a
job runs). Every entry of its job `PATH` (the machine's `PATH` when `github_runner_windows_path` is empty), and the
folders of the tools a build may run from outside it (`github_runner_win_admin_tools`: the ccache folder and its
`msvc`, whose `cl.exe` MSBuild runs when a build sets `CLToolPath` there), must be owned and writable by none of the
openers, and so must every file in it, and no folder above it may be owned by one or let one delete in it or change
its permissions (where an entry is missing, the folder above it, where it could be made): else `PATH-WRITABLE <entry>
sid=<sid>|owner=<sid> at=<folder>` or `PATH-WRITABLE <entry> file=<file> ...` fails the run. The runner-packages play
seals the ccache folder for this (owned by `Administrators`, nothing inherited, read and run for Authenticated Users and
Users), before it copies the binaries in and again after, and unpacks the release on the WSL side, which no Windows job
reaches. The checks themselves (`NovaTree` in `runner-windows.ps1`) are tested on fake ACLs by
`TestFunctionalWindowsSecurityDecisions` (`testdata/winsec`).

**The password** is a value in the machine's own seat, never typed and never shown. The play reads the seat's names
first (`nova-secrets names`, no value read); a seat without it stops the run before anything changes, naming the
commands to run on the control node: a generated value piped into `set` (the `Nw1-` prefix meets Windows' complexity
rule), a `grant` to the machine's seat, the store's push, and the refresh:

```
{ printf 'Nw1-'; openssl rand -hex 24; } | nova-secrets set NOVA_WINRUNNER_CI_ADMIN_PASSWORD --store S --key K --pins P --as control
nova-secrets grant NOVA_WINRUNNER_CI_ADMIN_PASSWORD --to bench-1 --pins P --store S --key K --as control
git -C S push
nova-fleet secrets-refresh -- -e nova_seal_store=S
```

On the machine the value reaches `powershell.exe` only in its environment: `nova-secrets exec --only <secret> -- env
WSLENV=<secret> powershell.exe ...`, where `WSLENV` hands that one variable through interop. The script removes it from
its environment as it reads it and hands it only to method calls inside its own process (a `SecureString` built a
character at a time, `LogonUser`, `CreateService`, `ChangeServiceConfig`), never to a cmdlet parameter, which module
logging would record; the tasks are `no_log`. `config.cmd --windowslogonpassword` is never used, since it would put the
password in an argument. Two lines per admin runner: `ACCOUNT <machine> <account> account=created|updated|ok
rights=ok|+<right>,... admin=added|ok` (an existing account whose stored password does not log it on as a service gets
the stored one: `updated`), then `LOGON <machine> <dir> ok|set|created account=.\<account> restart=no|done|busy
acl=protected service=<name>` (`created`: the service was made, with the name `config.cmd` gives one). The service's
logon (account and password) is set on every run, so a run cut off between the two
steps never leaves the service on an old password. It is restarted only while no job runs, and only when the account
step said `updated` or `created` (an account made anew, its service left from the old one), the service had logged on
as another account, or its listener runs as another user;
`restart=busy` leaves that to the next run. `--check` reads the names and prints the plan; it changes no account.

**Removing an entry** from `windows_admin_runners` does not retire its runner or its account: the play no longer
looks at them. Retire one by hand. Take a removal token with the control node's gh (`gh api -X POST
repos/<owner>/<repo>/actions/runners/remove-token -q .token`) and hand it to `config.cmd remove` in the runner
directory on its stdin, as `ACTIONS_RUNNER_INPUT_TOKEN` (never an argument). Then, in an elevated PowerShell on the
machine, delete the service (`sc.exe delete <service>`; `Get-CimInstance Win32_Service` finds it by the directory in
its path), the directory (`Remove-Item -Recurse -Force C:\nova-admin-runners\<slug>-<account>`) and the
account (`Remove-LocalUser -Name <account>`).

**Which jobs run on it** is decided on the machine, in the runner's sealed directory, never by the workflows it
guards. The install step writes the entry's workflow to `.nova-ci-rules\admin-workflow`, and the job-started hook
(`nova-ci-rules.ps1 started`, on a runner holding that file or whose hook runs as an administrator) refuses, before
anything else runs, every job but a `push`, `schedule` or `workflow_dispatch` job of `refs/heads/main` whose
`GITHUB_WORKFLOW_REF` is that workflow at `refs/heads/main` (owner and repository compared without case, the rest
exactly); with no `admin-workflow` it refuses every job. It also refuses every job while a folder above the runner
is owned by an opener (or its owner cannot be read, a hidden system folder aside), or cannot be listed. A refusal is
`::error::CI rule: <why>`, and the hook then ends the job's `Runner.Worker`: the runner adds the hook as a pre-job step,
and a failed one fails the job but still lets its `if: always()` and `if: failure()` steps run (actions/runner,
`JobExtension.cs`), so ending the worker is what keeps every step of a refused job from running. So a pull request, a branch, a tag or another workflow never runs as an
administrator, whatever label it names. GitHub still gives the runner `self-hosted`, `Windows` and `X64`, so in a
repository with an admin runner every ordinary Windows job should name a label of its own runners as well (such as
`fleet` or a `windows_runners` label): a job that lands on the admin runner by those labels is refused, not run.

### The stage directory

Every script the role runs elevated on the Windows side (`runner-windows.ps1`, and `nova-ci-rules.ps1` with its two
hooks) is staged in `C:\nova-stage` (`github_runner_wsl_stage_dir`), never under `C:\ProgramData`. There every user may
add files to a folder, so a job of an ordinary runner (`NETWORK SERVICE`) could make the stage folder first, or put
its own script in it, and the play would run that script as an administrator. The role's `tasks/wsl-stage.yml` makes the
directory sealed in the one call that creates it: owned by `Administrators`, nothing inherited, full control for SYSTEM
and `Administrators` only. It copies the scripts in through `/mnt/c`, and every elevated call is
`<stage check> && powershell.exe ... -File C:\nova-stage\<script>`. The stage check is `files/stage-check.ps1`, handed
to `powershell.exe` as `-EncodedCommand` so it is never a file anyone could change. It proves, the moment before each
call:

- every folder above `C:\nova-stage` is owned by SYSTEM, `Administrators` or TrustedInstaller, and no one else (not
  `NETWORK SERVICE`, SERVICE, LOCAL, Authenticated Users, Users, Everyone, This Organization or a service's own SID)
  may delete it, rename it, delete what is in it, or change its permissions or owner. Adding a file or a folder to
  `C:\` is not refused: it lands beside the stage directory, never in it (captain's `C:\` gives Authenticated Users
  Modify, which is that and no more);
- the directory is as the play makes it;
- everything in it is a file, not a link, owned by SYSTEM, `Administrators` or the account running the play, with
  only the entries it inherits from the directory (SYSTEM and `Administrators`);
- each script has the sha256 of the role's own file on the control node.

A refusal fails the task and nothing is run; nothing found is ever changed or repaired:

- `STAGE-OPEN <folder> sid=<sid>|owner=<sid>|unreadable=<why>`: someone else could take the stage directory's path;
- `STAGE-PLANTED <path> owner=<sid>|inherited=yes|ace=<sid>|folder=no|file=no|unreadable=<why>`: the directory, or
  something in it, was not made by the play. Because `C:\` lets any account make a folder, a job of an ordinary runner
  can make `C:\nova-stage` before the play does; the play then refuses until an administrator deletes it. Delete it
  in an elevated cmd with `rmdir /s /q C:\nova-stage`, which removes a link inside it without following it (Windows
  PowerShell's `Remove-Item -Recurse` follows one and deletes what it points at), and run again;
- `STAGE-CHANGED <path> missing|sha256=<hex>`: a staged script is not the role's.

`C:\ProgramData\nova-runner-stage` and `C:\ProgramData\nova-ci-rules-stage` are never read, run or trusted; the play
leaves them where they are on a machine that has them, and an administrator may delete them by hand (`rmdir /s /q`).
The decisions in `stage-check.ps1` are tested on fake ACLs by `TestFunctionalStageCheck` (`testdata/stagecheck`); the
part that reads the real disk (`StageDisk`) runs only on Windows, when the play runs.

### A plain Windows host

A machine with no WSL (its record's `os` is `windows`, its `user` an administrator, its `address` where its OpenSSH
answers with PowerShell as the shell) gets the same runners the same way. nova-config's inventory gives it
`ansible_shell_type: powershell` and puts it in the group `windows` only, never `bench`, `runner` or `services`
whatever its roles; `secrets-refresh`, `ssh-names`, `seat` and `tailscale` skip it, so no play written for Linux or
macOS reaches it (its CI rules are kept by this play, not by `ci-rules`). The play stages the same scripts into the
same sealed directory with `tasks/windows-stage.yml` (the same `stage-check.ps1`, run with
`ansible.windows.win_command`; `win_copy` stages) and runs the same `powershell.exe` command lines with `win_command`.
`win_command` cannot join two programs with `&&`, so each elevated call is the task straight after a task that runs
the stage check; the directory it proved is one only SYSTEM and Administrators can change. The token is on stdin only
(pipelined, `no_log`). It gets no keepalive and no page file step. The `ansible.windows` collection is in
`requirements.yml`.

### The Windows page file at a fixed size

`nova-fleet windows-runners -- -l <machine> -e nova_windows_pagefile_mb=<n>` turns off Windows' page file management
and fixes `C:\pagefile.sys` at `<n>` MB (initial = maximum). Without it the page file is left as it is.

Why: a system-managed page file shrinks when it is idle, and the commit limit shrinks with it. The WSL VM and every
Windows job draw on that limit, so jobs fail for memory while RAM is free. On captain on 2026-11-12 the limit fell
from 123.6 GB to 72.5 GB.

It prints `PAGEFILE <machine> ok|set size_mb=<n> active_mb=<allocated now> restart=owed|no`, then `COMMIT <machine>
limit_gb= free_gb=`. `restart=owed` means Windows has not applied the size yet. Nothing here restarts the machine:
a person picks the moment. `--check` prints `PAGEFILE-PLAN`.

### WSL held running from boot

WSL starts a distro only when something on Windows asks for one, and stops it when the last `wsl.exe` holding it ends
(a WSL update from Windows Update does this). So the same play gives every WSL machine with interop, whether or not
its record names `windows_runners`, one scheduled task on the Windows side: `WSL2 keepalive (<distro>)`, which runs
`wsl.exe -d <distro> -u <login> --exec /bin/sleep infinity` (under `conhost.exe --headless`) as the Windows account that
started WSL, logon type S4U: it runs with nobody logged in, and no password is stored. Its boot trigger repeats every
minute with `IgnoreNew`, so WSL is up at boot and is started again within a minute after a WSL update, a `wsl
--shutdown` or a reboot inside the distro; inside it systemd starts the services as at any boot (a login's units need
lingering, which `join` sets). The distro is the running one whose `/etc/machine-id` is this machine's
(`github_runner_wsl_machine_id_file`), read through `\\wsl.localhost`; none is `NO-DISTRO` and fails the run. A task
of that name is set when what it does differs and started when it is not running; one line per machine: `KEEPALIVE
<machine> <distro> ok|set|started|set,started task=<name>`. `--check` reads nothing on the Windows side and prints
`KEEPALIVE-PLAN <machine> machine-id=<id> user=<login>`.

## CI rules

Every runner nova-fleet sets up enforces the fleet's CI rules itself, so a repository cannot opt out by forgetting a
line of YAML. The `github-runners` play applies them to each runner it sets up; the `ci-rules` play applies them to
every runner running on the machines named, whoever registered it (`nova-fleet ci-rules -- -l <machine>,...`).

| Event | Rule |
|---|---|
| `push` | runs only on a branch matching `github_runner_ci_push_branches` (default `main` and `sprint/*`, one segment) or a tag; any other ref is refused before the first step: `::error::CI rule: push CI runs only on main, sprint/* and tags, not <ref>; ...` (a card branch gets CI through its pull request) |
| `push`, `pull_request`, `merge_group` | the job finishes within `github_runner_ci_limit_seconds` (default 120) of job wall time, counted from the end of the job-started hook; past it every process under the job's `Runner.Worker` is stopped and killed, each second until the job ends, and the job fails at the job-completed hook: `::error::CI rule: per-commit jobs finish in 2m; move longer work to a nightly (schedule) workflow` |
| `schedule`, `workflow_dispatch` | nightly or manual: no limit |
| any, on a Mac drawing from its battery | refused at the job-started hook, before the first step (`pmset -g batt` says `'Battery Power'`): a laptop on battery is about to be carried and may sleep with the job in flight; on AC, or without `pmset`, nothing changes. `::error::CI rule: <host> is on battery power ...; rerun it and another runner takes it` |

**Measured, never enforced** (`.sh` hooks: Linux and macOS). At its end every job prints, into its own log and onto
the host's `~/.local/state/nova/ci-jobs.log`:
- On Linux, `MEMORY-PEAK host= runner= repo= workflow= job= run= secs= peak_mib= anon_mib=`: the largest memory.current
  and memory.stat `anon` of the runner's cgroup, sampled each second while the job ran.
- For a job over 100 s, `JOB-LOAD` with the same fields plus `load1= holders= why=`: the machine's load and its
  nova-reserve holders at the job's end.

Measuring runs in a subshell whose failure is dropped, so it never fails or delays a job. The sampler runs only under a
real `Runner.Worker` and ends with it.

The hooks enforce only these rows. That unit tests use no real time, sleep or network, and that unit and functional jobs
run in parallel, is the rule repositories follow; no hook checks it.

**How.** The runner's own job hooks (GitHub's "Running scripts before or after a job"): `ACTIONS_RUNNER_HOOK_JOB_STARTED`
and `ACTIONS_RUNNER_HOOK_JOB_COMPLETED` in the runner's `.env`, pointing at `job-started` and `job-completed` (`.sh`, or
`.ps1` on Windows) in `<runner dir>/.nova-ci-rules/`, beside `nova-ci-rules.sh` (`.ps1`), `limit` (the seconds) and `push-branches` (the globs, blank separated). The
started hook arms a watchdog, a detached process that the completed hook stops; a job that ends in time leaves
nothing behind. A hook the `.env` held before is kept as `NOVA_CI_CHAIN_JOB_STARTED` / `_COMPLETED` and run from ours, in its
own process by its extension (after the push check, before the watchdog is armed, so waiting for a lock is not job
time). Each started hook first stops any watchdog and verdict an earlier job left, so a runner that died mid-job
never fails the next one. A chained hook naming a file that is not there is
dropped from the `.env`, never chained (the unchain rule). The limit and the push branches are one value
each for the fleet (`group_vars` or `-e`), never per repository.

**Applying.** `nova-ci-rules apply` writes the hooks and the `.env`, and restarts a runner whose `.env` changed (the
runner reads `.env` only at start) by what supervises it: its systemd unit (system or user, found from the listener's
cgroup), its launchd job (found by `WorkingDirectory`), or its Windows service. Before the restart every process of a systemd unit gets SIGINT, so
the listener ends even where the unit is `KillMode=process`; a restart counts only once a new listener is seen. A
runner running a job is left alone and says `restart=busy`: run the play again. Linux applies as root, and then the
hooks and `limit` belong to root, so a job cannot rewrite them (only `state/`, the watchdog's, is the runner login's).
macOS applies as root from `github-runners` (its runners are LaunchDaemons) and as the login from `ci-rules`, so there a
runner of another login says `denied=` and a LaunchDaemon `restart=needs-root`. A WSL machine also applies `nova-ci-rules.ps1` to the Windows side's
`actions.runner.*` services. One line per runner: `CI-RULES <machine> <dir> hooks=<ok|set> restart=<no|done|busy|stopped|failed>`.

## join

A new machine, from nothing to a member: its nova-config row, its login, the tailnet, the tools a seat needs and its
seat, then a check that it is reachable. Name the machine as the argument, `nova-fleet join <machine>` (it
passes `-e nova_join_host=<machine>`), never `-l`: a machine with no row yet is in no inventory, so the first play adds
the row and reads the inventory again.

1. **The row** (control node). `nova-config machine add` where the row is missing. A row that exists is never edited;
   one whose user is not `nova_join_user` stops the join by name. The address's SSH host key is pinned into the
   control node's `known_hosts`: the key you give as `nova_join_host_key` (from the provider's console; this proves
   which machine it is), else what the address answers at this first connection (trust on first use: this proves only that it is the
   same machine from here on). A nova-config row write plus a join admits a machine to the fleet: the tailnet-peer
   check and `nova_join_host_key` are the mitigation, and push access to the config store is the trust boundary.
2. **The login** (Linux, connecting as `nova_join_root_user` over the address given: root, or the provider's first
   login, which the play becomes root through with sudo; skipped once the row's address is the tailnet
   name, since the login exists then and the policy lets no one in as root over the tailnet). The row's user: no
   password, in `nova_join_sudo_group` with
   passwordless sudo (`/etc/sudoers.d/90-nova-<login>`, checked by `visudo` before it lands), its `authorized_keys`
   exactly the first login's (or those of the control node's file `nova_join_authorized_keys` names) and the control
   node's public key, linger on, the hostname set to the machine name. Tailscale SSH is off (`tailscale` below), so
   that key is how the control node logs in from here on, and no other machine's login gets in. The key is
   `nova_control_ssh_pubkey`, else the key `ssh-add -L` lists on the control node when it lists exactly one; an
   agent holding several is refused rather than guessed from, so the variable names the key. It is read at the start
   of the join, and without one key the join stops there, before the row is written: `CONTROLKEY STOP <machine>: no
   one public key for the control node to log in with (<why>)`. A darwin machine has no root
   login: set its login and sudo up at its console.
3. **The tailnet**: `tailscale` (below). From here on the plays reach the machine over the tailnet.
4. **The tools a seat needs** (as the login): `nova_bin_dir` on the login PATH, and `NOVA_SECRETS_STORE`
   (`nova_secrets_store`, where the store is pushed) and `NOVA_SECRETS_SEAT` (`nova_seat`) exported in the login
   profile and, for `ssh <machine> '<cmd>'`, first in `~/.bashrc` (`~/.zshenv` for zsh), with the directory of
   `sops` on PATH (`/opt/homebrew/bin` on a Mac), so a bare
   `nova-secrets exec --only NAME -- <cmd>` there opens the machine's own seat in the pushed store (the private-clone
   recipe is in `nova-secrets help exec`)
   (the key defaults to `~/.config/nova-secrets/<seat>.key`; the short hostname need not be the seat); on Linux the pinned `age` and `sops`
   release binaries, checked against their published digests. A darwin machine takes them from Homebrew
   (`brew install age sops`).
5. **The tailnet check** (control node): the row's address is now the machine's MagicDNS name and the control node's
   `tailscale status` shows it as an online peer; anything else stops the join before a seat is made (skipped with
   `nova_tailnet=false`).
6. **The seat**: `seat` (below): the key made on the machine, its rule in the store, its public half pinned on the
   control node (`nova_seal_pins/<seat>.pub`, outside the store).
7. **The grant** (control node): with `nova_seal_seat` and `nova_seal_key`, the control seat and its key,
   `nova-secrets grant` writes the new seat's grants file with the names in `nova_join_grant` (`NOVA_BUS_PASSWORD`
   unless you list others), sealed to the pinned machine key and the pinned recovery key only: the control key
   cannot open it. The values pass from one sops to the other in memory, and a refused grant stops the join with its
   one-line reason. The control node's pins live in `nova_seal_pins`, outside the store: `recovery.pub` comes from
   `nova-secrets init --pins`, or once from `nova-secrets set ... --pins <dir> --recovery <the recovery public key>`;
   a grant without it is refused (`reason=no-pin`), and the join stops with that line. Then the store is pushed to
   the machine (`secrets-refresh`, push mode, when `nova_seal_store` is given) with a copy of the recovery pin
   (`nova_secrets_pins/recovery.pub`, exported as `NOVA_SECRETS_PINS` in its login profile) for its own `set`.
   Without `nova_seal_seat` and `nova_seal_key` nothing is granted, and the receipt says so; `up` grants and pushes
   again in its own steps, which then change nothing.
8. **The check**: the control node's `tailscale status` shows the machine as an online peer, and `nova-bus who` from
   the machine reaches the bus. Without `nova_bus_redis`, or before the seat holds `NOVA_BUS_PASSWORD`, the bus check
   is a `NOTE`, not a failure.
9. **The toolchain** (a bench or runner): `runner-packages`, `sdk-toolchain` (Linux and macOS), then `audit` against
   `runner-requirements.tsv`, which stops the join, `FLEET-AUDIT FAILED host=<machine> missing=<names>`, on anything
   still missing (`join --check` installs nothing, so there it prints `FLEET-AUDIT WOULD-FAIL` instead). A machine of
   neither group skips this step; one on a platform the SDK has no pins for (Windows, Linux arm64, an Intel Mac) skips
   `sdk-toolchain` by name.

A Windows machine gets 1 and 3 (and `runner-packages` with nothing to install, as a runner).

```
nova-secrets exec --only NOVA_PG_PASSWORD --store S --key K --as SEAT -- \
  env NOVA_PG_DSN=postgres://nova_config@<store>:5432/nova \
  nova-fleet join web-1 --check --diff -- -e nova_join_user=fleet -e nova_join_address=203.0.113.7 \
    -e nova_join_os=linux -e nova_join_arch=amd64 -e nova_seal_store=$HOME/secrets \
    -e nova_seal_seat=control -e nova_seal_key=$HOME/.config/nova-secrets/control.key
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
| `nova_join_root_user` | `root` | the login a fresh Linux machine is reached with: root, or a first login the play becomes root through with sudo (`up` passes the login it reached the machine as: `--first-login`, or `<name>=<login>@<address>`) |
| `nova_join_sudo_group` | `sudo` | the group the login joins (`wheel` on Red Hat) |
| `nova_join_authorized_keys` | the first login's on the machine | a file on the control node to install instead (the control node's key is added either way) |
| `nova_control_ssh_pubkey` | the one key `ssh-add -L` lists on the control node (several are refused) | the control node's public key line, authorized for the login |
| `nova_join_hostname` | `true` | set the hostname to the machine name |
| `nova_join_host_key` | unset | the machine's host key (`ssh-ed25519 AAAA...`) from the provider's console; without it the first connection's key is pinned |
| `nova_tailnet` | `true` | `false` skips the tailnet checks (a private network); the rest of the join is unchanged |
| `nova_seal_seat`, `nova_seal_key` | unset | the control seat and its key file on the control node; without them nothing is granted |
| `nova_seal_pins` | `~/.config/nova-secrets/pins` | the control node's pins: `recovery.pub` and one `<seat>.pub` per machine |
| `nova_join_grant` | `[NOVA_BUS_PASSWORD]` | the names granted into the new seat |
| `nova_bus_redis` | unset | `host:port` of the bus, for the check |
| `nova_age_version`, `nova_sops_version` and their `*_sha256` | the pinned releases | |
| `nova_control_config`, `nova_control_secrets`, `nova_control_sops` | `nova-config`, `nova-secrets`, `sops` | the control node's binaries |

**Check.** The receipts `JOIN ROW`, `HOSTKEY`, `TAILNET`, `FIREWALL`, `JOIN TOOLS`, `JOIN PEER`, `SEAT`,
`JOIN GRANT <seat> from=<control seat> to=<seat> names=<n> changed=<n> committed|unchanged`, `JOIN PIN`,
`SECRETS-REFRESH` and `JOIN CHECK <machine> peer=online bus=WHO OK present=<n>`; a second run's `--check --diff`
changes nothing. **Rollback.** `nova-config machine remove <name> --actor <you>`, the device removed in the Tailscale
admin console, the seat's rule and its grants file reverted in the store (`git revert`), on the control node
`rm <nova_seal_pins>/<seat>.pub <nova_seal_pins>/<seat>.ledger` and `ssh-keygen -R <address>` for the address given
and the MagicDNS name, and on the machine `userdel -r <login>` and `rm /etc/sudoers.d/90-nova-<login>`. A rejoin
under the same name is refused by the seat pin until it is removed.

## tailscale

Each machine on the tailnet, its address in nova-config its MagicDNS name, and its firewall closed to everything but
the tailnet. `join` runs it; run it alone for machines that already have a row.

**What is yours, once, in the Tailscale admin console:** the account, with MagicDNS on (the default), and one API
access token: Settings > Keys > Generate API access token, stored once with `nova-secrets set TAILSCALE_API_KEY` (paste
it at the prompt, one line `tskey-api-...`). It acts with its maker's rights (an admin's), so the merge
needs no other grant. It expires (90 days by default); a run with an expired one stops with the line that makes a new
one. On a Mac machine, open the Tailscale app once at its console and approve its VPN configuration.

**What `up` does with it** (its `tailnet` step, before anything reaches a machine):

1. The policy: `GET /api/v2/tailnet/-/acl` as HuJSON with its ETag, then the fleet's needs added as a JSON Patch
   (RFC 6902) applied with `github.com/tailscale/hujson`, so its comments and formatting survive. Each of
   `tag:nova`, `tag:runner` and `tag:control` gets `autogroup:admin` as an owner if it lacks it (so the token can
   apply them; other owners stay); the fleet's grants and ssh rules are appended where an equal rule (the same JSON)
   is not already there. Among them, so your own devices keep reaching every tagged machine whatever the policy was
   built on (allow-all, or `autogroup:self`, which no tagged device is): a grant from `autogroup:admin` to `tag:nova`,
   `tag:runner` and `tag:control` on every port (`ip: ["*"]`), an ssh rule accepting `autogroup:admin` into
   `tag:nova` and `tag:runner` as `autogroup:nonroot` and `root`, and one into `tag:control` as `autogroup:nonroot`
   only with Tailscale's `check` action (you confirm in a browser, then it holds for a while). The one tag with an
   ssh rule is `tag:control`, into `tag:nova` and `tag:runner` as a non-root login. Nothing reaches `tag:control`
   without a person: no tag has its port 22 or an ssh rule into it. Nothing is opened to an untagged or member
   device. The members reach the store's ports (`tcp:5432`, `tcp:6379`) on `tag:control` too, for a fleet whose
   store is the control node itself, so the store does not depend on an allow-all rule staying in the policy.
   One kind of rule is removed, wherever it stands: the fleet's own machine-to-machine ssh rule, an `accept` whose
   `src` holds only fleet tags and names `tag:nova` or `tag:runner`, whose `dst` holds only `tag:nova` and
   `tag:runner`, as `autogroup:nonroot` only (Tailscale SSH takes the source node for the person, so that rule lets a
   CI job on one machine log in to another as its login, which sudoes with no password). An ssh rule of any other
   shape that still names `tag:nova` or `tag:runner` on both sides (a person in its `src`, `root`, `check`) is
   someone's own: it is kept and reported (`UP TAILNET MACHINE-SSH`), and it acts only on a machine serving
   Tailscale SSH, which `ssh-names` fails. Nothing else the policy has is removed or replaced, so the
   default policy's allow-all `acls` rule still allows all. It is written (`POST` with `If-Match`, HuJSON) only when
   the merge added or removed something (under `--check`, `policy=would-merge`); a 412 reads it again and retries
   once; a second run writes nothing.

   **Machine to machine is refused, by design.** The rule is "CI jobs should not have root or sudo, nor any way to
   get it" (the fleet's owner, 2026-11-10). A CI job runs as a machine's login, and that login sudoes with no
   password, so a machine that can log in to another gives a job root there. No machine's login can: Tailscale SSH
   is off (`nova_tailscale_ssh`), no ssh rule names a machine's tag, and a login's `authorized_keys` holds its
   provider's first keys and the control node's key, never another machine's. The control node is the one way in,
   and a transfer between machines goes through it. `ssh-names` checks both every run.
2. This node tagged `tag:control` (`POST /api/v2/device/<Self.ID>/tags`, its tags kept); a node not on the tailnet is
   first joined with a minted `tag:control` key (`tailscale up --reset --auth-key=file:/dev/stdin`, the key on stdin;
   `sudo` on Linux). A device of yours tagged this way is owned by the tag from then on, not by your account.
3. Each machine named that is on the tailnet (the one peer whose MagicDNS label, the DNS name before its first dot,
   is the machine's name; two such peers stop the run, naming both, and nothing is tagged) given `tag:nova` (and `tag:runner` with `--runners`) the same way; for
   each that is not, a key from `POST /api/v2/tailnet/-/keys`: single use, pre-authorized, those tags, an hour. It is
   held in memory and reaches only that machine's play, as `NOVA_FLEET_AUTHKEY` in its environment.

**What the play does, per machine:**

1. Installs Tailscale: Tailscale's apt repository on Debian and Ubuntu (the same key and source line its own install
   script writes), Homebrew's cask on macOS. A Mac whose running daemon is the open-source `tailscaled` (Homebrew's
   formula, run as root) gets no cask: the play drives it as it drives Linux, with the `tailscale` beside the daemon
   and sudo.
2. `tailscale up --reset --auth-key=file:/dev/stdin --hostname=<machine> --advertise-tags=tag:nova[,tag:runner]`, so
   Tailscale SSH is off (`--ssh` is added only with `nova_tailscale_ssh` true, on Linux and on the open-source
   `tailscaled`; the macOS app does not serve Tailscale SSH). `tag:runner` is added for a machine in
   the `runner` group. The key comes from `NOVA_FLEET_AUTHKEY` in the control node's environment (`up` puts the one it minted there), else `TAILSCALE_AUTHKEY` (run
   alone, give one) and reaches the
   machine on the command's stdin only. A machine already Running with the same tags is not joined again; a hostname or
   Tailscale SSH setting that differs is set in place with `tailscale set`, with no key (Tailscale SSH is only ever
   turned on there). On a machine that serves Tailscale SSH, turning it off moves the way in to sshd, so before
   anything changes the play checks, on the machine, that OpenSSH's sshd answers on its tailnet IPv4 address, port 22,
   by its banner (from the control node that address would reach Tailscale SSH, which answers port 22 over the
   tailnet while it is on; the firewall lets the tailnet in, see 4), and that `sshd -T -C
   user=<login>,host=<control node's name>,addr=<its tailnet address>` lets this login in by key
   (PubkeyAuthentication yes, AllowUsers and DenyUsers); else `TAILNET STOP <machine>: Tailscale SSH is on, and
   turning it off would leave the control node no way in: <why>`, nothing changed. Then it adds the control node's
   public key to the login's `authorized_keys` (as `join` does; without one key, `CONTROLKEY STOP`). The switch
   itself is the play's last step, run detached because this run's own connection may be a Tailscale SSH session it
   ends: a root job turns Tailscale SSH off and, `nova_tailscale_ssh_deadline` seconds later, tries to create the
   decision file `nova_tailscale_ssh_keep`; if it creates it, it turns Tailscale SSH back on. The play drops its
   connection, reconnects through sshd, checks `RunSSH` is false, logs in from the control node by key on a fresh
   connection (`ssh -o ControlPath=none`), then tries to create the same file. The create is exclusive, so exactly
   one side decides: the play reports `TAILNET SSH-OFF <machine> runssh=false key-login=ok kept` only when it created
   the file and `RunSSH` still reads off. A login that fails, or lands after the deadline, stops the run with
   `TAILNET STOP`, and the machine turns Tailscale SSH back on by itself. The job holds `nova_tailscale_ssh_pending`
   (in `/var/run`, which a reboot clears) while it runs, and a later run waits for it to go before it reads Tailscale
   SSH's state, so a rerun inside the deadline does not read a switch that is about to be undone. Where `netwatch`
   is installed the job also holds `nova_tailscale_ssh_hold` in its hold directory, so netwatch does not reboot the
   machine inside the window. A reboot or a kill of the job there by anything else ends the job before its revert:
   Tailscale SSH stays off, and sshd, checked above, is the way in.
3. Records `Self.DNSName` (`<machine>.<tailnet>.ts.net`) as the machine's address: `nova-config machine set <name>
   --address <dns name>`, then reads the inventory again, so the firewall play, every run after it, the bus and the
   services reach the machine by that name.
4. The firewall. Linux: ufw denies incoming on every interface but `tailscale0`, allows outgoing and UDP 41641
   (Tailscale's direct connections), and each rule `nova_firewall_allow_in` lists (`ufw allow <rule>`, added when
   `ufw --dry-run` does not say it exists). The list comes from `-e`, so a run that should keep a machine's rule
   names that machine with it: `nova-fleet up ... web-2=203.0.113.9 -- -e '{"nova_firewall_allow_in": ["in on eno2
   proto udp from 10.0.0.0/24"]}'`. A rule already added stays until `ufw delete allow <rule>` on the machine. It is turned on only when the run itself comes in on the tailnet (or is local),
   so it cannot cut its own connection; otherwise it stops with `FIREWALL HELD` and says why. macOS: the application
   firewall on (sudo: the login needs passwordless sudo, set at the console).

`redis` and `postgres` listen on the machine's tailnet address only, read from `tailscale ip -4`.

```
nova-secrets exec --only NOVA_PG_PASSWORD --store S --key K --as SEAT -- \
  env NOVA_PG_DSN=postgres://nova_config@<store>:5432/nova \
  nova-fleet tailscale --check --diff -- -l web-1
```

| Variable | Default | |
|---|---|---|
| `nova_tailscale_tags` | `tag:nova`, plus `tag:runner` in the `runner` group | the tags the key must be allowed |
| `nova_tailscale_ssh` | `false` | Tailscale SSH (Linux, and a Mac on the open-source `tailscaled`); off, because it lets in a source node, not a key, so a CI job on one machine could log in to another and sudo there |
| `nova_control_ssh_pubkey` | the one key `ssh-add -L` lists on the control node (several are refused) | the public key `join` authorizes for the login, and this play before it turns Tailscale SSH off |
| `nova_tailscale_ssh_deadline` | `120` | seconds after the switch at which the detached job claims the decision file and turns Tailscale SSH back on, unless the play claimed it first |
| `nova_tailscale_ssh_keep` | `/var/tmp/nova-tailscale-ssh-off.keep` | the decision file: whoever creates it first, the play or the job, decides |
| `nova_tailscale_ssh_pending` | `/var/run/nova-tailscale-ssh-off.pending` | held by the job while it runs; a later run waits for it to go |
| `nova_tailscale_ssh_hold` | `/run/nova-netwatch/hold.d/tailscale-ssh-off` | netwatch's hold, held by the job while it runs when that directory exists |
| `nova_sshd` | `/usr/sbin/sshd` | the sshd whose `-T` settings are checked |
| `nova_tailscale_record_address` | `true` | write the MagicDNS name into nova-config |
| `nova_firewall` | `true` | ufw on Linux, the application firewall on macOS |
| `nova_firewall_allow_udp` | `[41641]` | UDP ports open on every interface |
| `nova_firewall_allow_in` | `[]` | more ufw allow rules (the words after `ufw allow`); anything but letters, digits, spaces and `. / : _ -` stops the play with `FIREWALL STOP` |
| `nova_tailscale_apt_url` | `https://pkgs.tailscale.com/stable` | |

**Check.** `TAILNET <machine> dns=<name> ip=<100.x> tags=tag:nova address=kept` and `FIREWALL <machine> ufw as
declared`; `tailscale status` on the control node lists the machine. **Rollback.** On the machine `tailscale logout`
and `ufw disable` (macOS: `socketfilterfw --setglobalstate off`); `nova-config machine set <name> --address <old
address>`.

## ssh-names

`ssh <machine>` by its tailnet name, from every machine of the fleet, over the tailnet: the name resolves everywhere,
and only the control node's login gets in. The play runs on every machine of the inventory, as its login, and writes
three things there:

- `~/.ssh/config.d/nova-fleet`: one block per machine of the inventory, `Host <name> <address>` with `HostName` the
  record's address, `User` the record's login and `UserKnownHostsFile ~/.ssh/known_hosts
  ~/.ssh/nova-fleet.known_hosts`. The address is the machine's MagicDNS name (the `tailscale` play records it), so
  `ssh <name>` resolves through MagicDNS and travels over the tailnet whatever `/etc/hosts` maps the short name to.
- `~/.ssh/nova-fleet.known_hosts`: each machine's ed25519 host key, as `<name>,<address> ssh-ed25519 ...`, read from
  the machine itself (`/etc/ssh/ssh_host_ed25519_key.pub`) over the play's own connection, whose host key the control
  node checks. Tailscale SSH presents the same key. A machine that does not answer this run keeps the line the file
  held. It sits outside `config.d`, so a login that includes `~/.ssh/config.d/*` never reads it as config.
- `Include ~/.ssh/config.d/nova-fleet` as the first line of `~/.ssh/config` (moved there from elsewhere at the top
  level, CRLF lines too; the file made if there is none; through a symlink, the file it names); nothing else in the
  file is changed. An `Include` of it inside a `Host` or `Match` block applies to that block only; the file is left
  alone and the run prints `SSH-NAMES <machine> INCLUDE LEFT ALONE` with the line: move it by hand.

Before anything is written anywhere, every machine checks the whole record and every key read against the tailnet as
the control node's `tailscale status --json` shows it, and the run is refused (`SSH-NAMES REFUSED ...`, naming the
machine and the field) on: an address that is not `<label>.<the tailnet's MagicDNS suffix>` or an IP; a user that is
not a login name (`[a-z_][a-z0-9_-]*`); a host key that is not one the tailnet publishes for that machine; a tailnet
that cannot be read. A machine that does not serve Tailscale SSH publishes no host key, so its key is written
unchecked and named in the receipt. A write limited with `-l` is refused too: every machine's file holds every
machine's key, so a write runs on all of them (`--check` takes `-l`).

ssh takes the first value it reads for an option, so for a fleet name the `HostName` and `User` of this file win over
a hand-written `Host` block of the same name later in `~/.ssh/config`, and that block's other options (an
`IdentityFile`, a `HostKeyAlias`, `ControlMaster`) still apply. A machine added to the record has its name everywhere
on the next run.

The names say where a machine is; they let no one in. A login from one machine to another is refused by design: the
fleet's rule is "CI jobs should not have root or sudo, nor any way to get it" (the fleet's owner, 2026-11-10), a CI
job runs as a machine's login, and that login sudoes with no password, so a machine that could log in to another
would give a job root there (`tailscale` above). Tailscale SSH is off and no ssh rule names a machine's tag, so a
target's sshd decides, and a login's `authorized_keys` holds the control node's key and its provider's first keys,
never another machine's; this play writes no `authorized_keys`. The control node is the one way in: a transfer
between machines goes through it.

Then two checks, all pairs at once, never under `--check`:

- The mesh: from each machine but the control node (`tag:control` on the tailnet), `ssh -o BatchMode=yes -o
  ConnectTimeout=10 -o StrictHostKeyChecking=yes -o UserKnownHostsFile=~/.ssh/nova-fleet.known_hosts <name> true` to
  every other machine that answered this run, the control node included. Each must be refused by the far side:
  `Permission denied (publickey...` from sshd, or `tailnet policy does not permit` from Tailscale SSH. A login that
  succeeds fails the run, naming the pair (`SSH-NAMES MESH <machine> OPEN <machine>-><target>`), and so does a host
  key that does not match the fleet's known hosts (`SSH-NAMES MESH <machine> HOSTKEY <target>`): that refusal is the
  client's and says nothing about the far side. A pair that times out, or fails another way (no route, no sshd, a
  local error), is reported and does not fail the run: it is no login.
- The control node: from the control node, `ssh -o BatchMode=yes -o ConnectTimeout=10 -o StrictHostKeyChecking=yes -l
  <the record's login> <the record's address> true` to every machine that answered this run but itself, with its own
  `known_hosts`. Each must log in by key; one that does not fails the run with ssh's own last line
  (`SSH-NAMES CONTROL FAILED ...`). A machine whose node still publishes host keys in `tailscale status` still
  serves Tailscale SSH, which lets in a node and not a key, so its login proves nothing: it is reported
  `tailscale-ssh` and fails the run (run `nova-fleet tailscale` on it).

A machine that did not answer (a laptop asleep or away) is `OFFLINE`, skipped, and never fails the run.

```
nova-fleet ssh-names --check --diff
nova-fleet ssh-names
```

**Check.** Per machine `SSH-NAMES <machine> machines=<n> online=<n> config=<written|same> known_hosts=<written|same>
include=<set|first|inside-a-block> unchecked=<names>`, `SSH-NAMES MESH <machine> refused=<n> open=<pairs>
timeout=<names> unreached=<names> offline=<names>` (none for the control node), and once `SSH-NAMES CONTROL ok=<n>
failed=<names> offline=<names>`; a machine that did not answer prints `SSH-NAMES <machine> OFFLINE`. Under `--check`
nothing is written and no pair is tried. **Rollback.** Remove the `Include` line, `~/.ssh/config.d/nova-fleet` and
`~/.ssh/nova-fleet.known_hosts`.

## seat

Each machine's seat in the secrets store. The seat is `nova_seat`, the machine's name unless a host variable says
otherwise: the name `nova-secrets --as` and `services` open it by.

1. On the machine: `<nova_secrets_key_dir>/<seat>.key` made by `age-keygen` where it is missing; never over an
   existing file, never copied. Only the public half leaves the machine.
2. On the control node, in your clone of the store (`nova_seal_store`): where `.sops.yaml` has no rule for the seat,
   `nova-secrets seat add <seat> <public half> --store <clone>`, which writes the rule and commits it. The public
   half is also pinned outside the store, `<nova_seal_pins>/<seat>.pub`, the key `nova-secrets grant` seals to,
   before the rule is committed. A rule or a pin that names a different key (a rebuilt machine's old seat) stops the
   run by name: replacing a seat is yours.

```
nova-fleet seat --check --diff -- -l web-1 -e nova_seal_store=$HOME/secrets
nova-fleet seat -- -l web-1 -e nova_seal_store=$HOME/secrets
git -C $HOME/secrets push
```

Then fill the seat from the control seat on the control node, `nova-secrets grant NAME --to <seat> --pins P --as
<control seat> --store S --key K` (no `--pub`: the pin is there), which is what `join.yml` does, or set a value of its
own on the machine with its own key and the recovery pin join.yml put there (`nova-secrets set NAME --as <seat>
--store S --key K --pins P`).

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
the roles of `nova_pg_roles` and the schema each owns (by default nova-config's: `nova_admin` owns the database,
`nova_config` owns schema `config`, `nova_read` reads it), nova-config's grants and default privileges on `config`,
and a nightly `pg_dump -Fc` kept `nova_pg_dump_keep_days`. It never drops a role, a schema or a database. Each role's
password is made once on the machine (`<nova_pg_dir>/<role>.pass`, root 0600) and proven by a login over the tailnet
address; only a role whose proof fails has its password set. With the three `nova_seal_*` variables, each role's
password that names a `secret` is set into that seat under that name (by default `nova_config`'s, as
`NOVA_PG_PASSWORD`, the name nova-config reads), then granted to the role's `seats` with `nova-secrets grant`.

```
nova-fleet postgres --check --diff
nova-fleet postgres -- -e nova_seal_store=$HOME/secrets -e nova_seal_seat=<seat> \
  -e nova_seal_key=$HOME/.config/nova-secrets/<seat>.key
nova-config schema | psql "postgres://nova_admin@<store>:5432/nova"      # the tables, once and after an upgrade
```

Another tool's role is one more entry. A list given with `-e` replaces the default, so it keeps nova-config's three:

```
nova-fleet postgres -- -e nova_seal_store=$HOME/secrets -e nova_seal_seat=<seat> \
  -e nova_seal_key=$HOME/.config/nova-secrets/<seat>.key -e '{"nova_pg_roles": [
    {"role": "nova_admin", "flags": "LOGIN CREATEDB CREATEROLE", "want": "t|t|t"},
    {"role": "nova_config", "schema": "config", "secret": "NOVA_PG_PASSWORD"},
    {"role": "nova_read"},
    {"role": "nova_sprint", "schema": "sprint", "secret": "NOVA_SPRINT_PG_PASSWORD", "seats": ["<seat>"]}]}'
```

The schema is applied as the database owner (`nova_admin`), never as `nova_config`: it starts with `CREATE SCHEMA IF
NOT EXISTS config`, which needs CREATE on the database, so as `nova_config` it stops with `permission denied for
database nova` and rolls back. Upgrading a store made by v1 to the v2 tables is the same one line, as the owner:
`nova-config schema | psql -1 -v ON_ERROR_STOP=1 "postgres://nova_admin@<store>:5432/nova"`.

| Variable | Default | |
|---|---|---|
| `nova_pg_version` | `18` | one major across the fleet |
| `nova_pg_source` | `auto` | `distro`, `pgdg`, or `auto` (distro when apt offers the package) |
| `nova_pg_listen` | the tailnet address | |
| `nova_pg_port`, `nova_pg_db` | `5432`, `nova` | |
| `nova_pg_allow_cidr` | `100.64.0.0/10` | the range tailscale assigns |
| `nova_pg_dir` | `/var/lib/nova-pg` | passwords and dumps |
| `nova_pg_dump_on_calendar`, `nova_pg_dump_keep_days` | `*-*-* 03:15:00 UTC`, `14` | |
| `nova_pg_roles` | nova-config's three | each `{role, schema, secret, seats}`: the role, the schema it owns (`""` none), the nova-secrets name its password is sealed as (`""` none), the seats that name is granted to; optional `flags`, `want` (default `LOGIN NOCREATEDB NOCREATEROLE`, `t\|f\|f`). A name that is not a plain identifier, or one given twice, stops the run first |

**Check.** `PG OK <machine> version=18 db=nova listen=<100.x>:5432 roles=3 timer=enabled changed=nothing` and `PG
REACH from=control`; a run with a gap ends `PG FAIL` with what is wrong. **Rollback.** `systemctl disable --now
nova-pg-dump.timer`; remove `conf.d/nova.conf`, restore `pg_hba.conf` and restart the cluster. The database, the
roles and the dumps are left.

## secrets-refresh

A machine's copy of the store stays fresh by a push from the control node (rsync over the ssh the play has, the
machine's host key verified as everywhere else; rsync on the machine comes from join): `nova-fleet up` step 8 copies
the encrypted store (no `.git`, stale files deleted, 0700/0600) to each machine's `nova_secrets_store`, and running
`up` again, or `nova-fleet secrets-refresh -- -e nova_seal_store=S`, pushes again. No machine needs read access to the
store's repository, and nothing on a machine fetches the store on a clock.

The machine that writes the store sets `nova_secrets_refresh: false`, which changes nothing there.

```
nova-fleet secrets-refresh --check --diff -- -e nova_seal_store=S
nova-fleet secrets-refresh -- -e nova_seal_store=S
```

| Variable | Default | |
|---|---|---|
| `nova_seal_store` | none, required | the control node's clone of the store, to push; without it a machine with `nova_secrets_refresh: true` stops before anything changes, and a directory without `.sops.yaml` and `recovery.pub` is refused before the push |
| `nova_secrets_refresh` | `true` | `false` skips the push |

**Check.** `SECRETS-REFRESH <machine> mode=push files=<n> pushed` (or `same`). **Rollback.** Push the previous
commit of the store: check it out in the clone and run again.

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
- The login's user manager may lock `nova_service_memlock` (`user@<uid>.service.d/90-nova-memlock.conf`): a user
  unit cannot go past its manager's limit, and the services play gives its units that `LimitMEMLOCK`. A running
  manager's limit is raised in place (`prlimit`), so none of its units restarts.
- The login's ssh sessions, and what they start (nova-reserve's commands among them), may lock `nova_login_memlock`
  (`unlimited`, soft and hard; `/etc/security/limits.d/90-nova-memlock.conf`, read by pam_limits): what the runner
  units' `LimitMEMLOCK=infinity` gives CI, so an io_uring step locks the same on a bench as in CI. The login's user
  slice `MemoryMax` still bounds it. The next session has it; `nova-fleet health -- -l <machine> --tags memlock`
  writes this file and nothing else.
- Leftovers of older tooling are removed where found (a unit stopped and disabled first): rowan-tools'
  `90-nova-host-memory.conf` drop-ins (ssh, tailscaled, user.slice), v1's member and reader `20-memory.conf`
  drop-ins, a `nova-pg-dump.timer` with no `nova-pg-dump.service`, the hand `~/.config/systemd/user/service.d/90-nova-memlock.conf`,
  v1's `actions.runner.mas-bandwidth-nova-sprint.*` units and an unregistered `~/actions-runner-nova-sprint-*`.
- `nova-disk-report` every 6 hours from `nova-disk-report.timer`: one line in the journal for /, the home filesystem
  and swap, `DISK-WARN` at priority warning over `nova_health_disk_warn_pct`.
- `nova-disk-clean` every hour from `nova-disk-clean.timer`, as the login: a top-level entry it owns in
  `nova_health_disk_clean_dirs` (`/tmp`) or a runner's `TMPDIR` goes whole once nothing under it changed (ctime, which an unpack cannot set back) for
  `nova_health_disk_clean_age_min`, it holds no socket, FIFO, `.git`, quarantine, pid or lock file, and no process of
  the login works in it, runs from it, maps it or holds a file in it (checked again just before the removal). A root
  that is `/`, the home or above it, or under a runner's `_work` or `actions-runner*` is refused (`CLEAN-REFUSED`); a file
  of `nova_health_disk_clean_caches` (Go's build cache) unchanged that long goes too. On a filesystem over
  `nova_health_disk_warn_pct` the age is `nova_health_disk_clean_pressed_min`. One `CLEAN` line per removal and a
  `CLEAN-DONE` line in the journal. A runner login of its own (github-runners, "The runner login") gets its own
  `nova-disk-clean-<login>.timer`, run as it, for `/tmp`, its runners' `TMPDIR`s and its Go build cache: the clean
  removes only what its user owns.

```
nova-fleet health --check --diff
nova-fleet health
journalctl -t nova-disk-report --since -1d        # the disk lines
journalctl -t nova-disk-clean --since -1d         # what the clean removed
cat ~/Library/Logs/nova-go-cache-clean.log         # a Mac: what the trim removed
```

On a Mac the role does one thing: `nova-go-cache-clean`, hourly from the LaunchDaemon `com.nova.go-cache-clean` run as
the login, removes the files of the Go build cache `go env GOCACHE` names in the login's shell that are unchanged for
`nova_health_disk_clean_age_min` (`nova_health_disk_clean_pressed_min` on a volume over `nova_health_disk_warn_pct`).
No temp or working directory is touched; `nova_health_disk_clean: false` turns it off, and a cache under `/Volumes`
gets no daemon (launchd cannot read a removable volume without a privacy grant; Go trims its own cache). `DISK_CLEAN_DRY=1` with the
daemon's environment says `WOULD` and removes nothing.

| Variable | Default | |
|---|---|---|
| `nova_health_journal`, `nova_health_journal_max_use` | `true`, `4G` | |
| `nova_health_panic_reboot` | `10` | seconds; `0` is off |
| `nova_health_ssh_killable`, `nova_health_ssh_unit` | `true`, `ssh` | `sshd` on Red Hat |
| `nova_health_log_maxsize`, `nova_health_log_trim`, `nova_health_log_files` | `1G`, 4 GiB, syslog and kern.log | `0` trim is off |
| `nova_health_user_memory_max_pct` | `85` | `0` is no limit |
| `nova_health_disk_report`, `nova_health_disk_warn_pct`, `nova_health_disk_on_calendar` | `true`, `80`, every 6 hours | |
| `nova_health_disk_clean`, `nova_health_disk_clean_dirs`, `nova_health_disk_clean_caches` | `true`, `/tmp`, `~/.cache/go-build` | |
| `nova_health_disk_clean_age_min`, `nova_health_disk_clean_pressed_min`, `nova_health_disk_clean_on_calendar` | `2880`, `360`, hourly | minutes |

**Check.** `HEALTH <machine> journal=persistent panic=10 ssh_oom=0 slice_max=<bytes> user_memlock=<bytes> | DISK <machine> root=<n>%
swap=<n>% [i40e=in-tree|out-of-tree zc=yes|no ports=<iface>=<version>,...]`: the last part only on a machine with an Intel
700-series NIC, reported and never changed. Those machines run the in-tree i40e: AF_XDP zero copy is the mode, and Intel's
out-of-tree driver compiles it out (`src/i40e.h` undefines `HAVE_AF_XDP_ZC_SUPPORT` for SUSE, Ubuntu and kernels >= 5.2;
the fork mas-bandwidth/ethernet-linux-i40e is upstream 2.30.18 unchanged). `zc=yes` means the loaded module carries the
zero-copy path (`i40e_xsk_wakeup` in `/proc/kallsyms`). **Rollback.** Remove the file of the guard (named above), then `systemctl daemon-reload` and restart the
daemon it configures (`systemd-journald`, `ssh`) or `sysctl --system`; `systemctl disable --now nova-disk-report.timer`.

## netwatch

A Linux machine that loses its network or the tailnet gets itself back, with no person and no power cycle; one that
hangs is reset by its hardware watchdog. A darwin machine is skipped.

Every `nova_netwatch_tick_seconds` (30) the timer `nova-netwatch@<interface>.timer` runs one tick of
`/usr/local/sbin/nova-netwatch <interface>`, for each of `nova_netwatch_interfaces` (default: the interface of the
default IPv4 route); an instance on any other interface is stopped and disabled. A tick is a miss when either
signal fails:

- the LAN: the default gateway does not answer one ping and the interface received no packet since the last tick (a
  router outage, where packets still arrive, is no miss);
- the tailnet, read on `nova_netwatch_tailnet_interface` (default: the default route's) while the LAN works. It is
  **local** when tailscale does not answer its status or is in a state other than `Running`, or the rest of the
  tailnet sees the machine offline and no peer answers `tailscale ping` (up to `nova_netwatch_tailnet_tries` of
  `nova_netwatch_tailnet_peers`, else of the peers the status lists online), while the coordination server
  (`nova_netwatch_control_url`, else the ControlURL of `tailscale debug prefs`) answers an HTTPS request over the plain
  internet. It is **remote** when the coordination server does not answer, the machine needs a new login
  (`NeedsLogin`, `NeedsMachineAuth`), only its own node shows offline while peers answer, or no peer answers while its
  own node shows online (a relay outage, or the peers down: a fault every machine would see at once). `Stopped` (a
  `tailscale down` by hand) and a tailnet with no peer online are no miss.

After `nova_netwatch_threshold` misses in a row the tick climbs, waiting `nova_netwatch_backoff` times the rung's
number of ticks after each rung:

| Rung | Action | When |
|---|---|---|
| 1 | `systemctl restart tailscaled` (on the tailnet's interface; elsewhere nothing) | any miss |
| 2 | `ethtool -r <interface>` | the LAN misses |
| 3 | `ip link set <interface> down`, then up | the LAN misses |
| 4 | the interface's driver unloaded and loaded again (`modprobe -r`, `modprobe`) | the LAN misses |
| 5 | `systemctl reboot` | after rung 4 for a LAN miss, after rung 1 for a local tailnet miss |

A tailnet miss with the LAN working never touches the card. A remote one never reboots: after rung 1 it raises the
alert and does nothing more, so an outage of the coordination server does not reboot every machine at once; if the
server comes back and the peers still do not answer, the miss is local and the machine reboots. Rungs 2 to 4 have no
busy guard, because they run only while the LAN is down, when a job on the machine has lost its network anyway. The
reboot never runs while a protected job runs: a process matching one of `nova_netwatch_busy_patterns` (by default,
on a runner a GitHub Actions job, `Runner.Worker`; on a bench a held reservation, `nova-reserve ... run`), or any
file in `/run/nova-netwatch/hold.d` (`touch /run/nova-netwatch/hold.d/<name>` to hold it, remove the file to
release it).

The reboot budget is `nova_netwatch_max_reboots` reboots in `nova_netwatch_window` seconds and the same number in one
outage. When the next reboot would pass either, the watch raises the alert and halts: no rung and no reboot until a
healthy tick, or until a person removes the alert file, which starts the outage over. A fault that never clears
costs at most the budget, however long it lasts.

The alert, `/var/lib/nova-netwatch/ALERT.<interface>` (its first word the kind: `halt`, `busy` or `remote`) and a
journal line at priority crit, is raised by the budget, by `nova_netwatch_busy_wait_limit` deferrals (the deferral
goes on) and by a remote tailnet miss; a healthy tick clears it and logs `RECOVERED`. A state file that is not
numbers (a cut-short write, an older format) is reset with a `STATE reset` line.

The hang guard: `kernel.softlockup_panic` and `kernel.hung_task_panic` (`/etc/sysctl.d/91-nova-lockup.conf`, off by
default), and, when `/dev/watchdog0` exists, systemd driving the hardware watchdog
(`/etc/systemd/system.conf.d/90-nova-watchdog.conf`), its driver loaded at boot by `nova-watchdog-module.service`
when `nova_netwatch_watchdog_module` names one. `kernel.panic` and `kernel.panic_on_oops` are the health play's; this
play reports them and sets neither.

The model is `cmd/nova-fleet/tla/NetWatch.tla`, and `NetWatchTrace.tla` checks the ticks the script takes in its
tests against it.

```
nova-fleet netwatch --check --diff -- -l bench-1
nova-fleet netwatch -- -l bench-1
journalctl -t nova-netwatch --since -1d           # the ladder's lines
```

| Variable | Default | |
|---|---|---|
| `nova_netwatch_interfaces` | `[]` | empty is the default route's interface |
| `nova_netwatch_tailnet_interface`, `nova_netwatch_tailnet_peers`, `nova_netwatch_tailnet_tries` | `""`, `[]`, `3` | |
| `nova_netwatch_control_url` | `""` | empty is the ControlURL tailscale names |
| `nova_netwatch_tick_seconds`, `nova_netwatch_threshold`, `nova_netwatch_backoff` | `30`, `4`, `2` | |
| `nova_netwatch_busy_patterns` | by group, from `nova_netwatch_busy_patterns_by_group` | pgrep -f extended regular expressions |
| `nova_netwatch_busy_backoff`, `nova_netwatch_busy_wait_limit` | `4`, `10` | |
| `nova_netwatch_max_reboots`, `nova_netwatch_window` | `2`, `21600` | |
| `nova_netwatch_dry` | `false` | `true` logs each action (`DRY`) instead of taking it |
| `nova_netwatch_watchdog_module`, `nova_netwatch_runtime_watchdog` | `""`, `2min` | `sp5100_tco` on AMD mini PCs |
| `nova_netwatch_softlockup_panic`, `nova_netwatch_hung_task_panic` | `0`, `0` | |

**Check.** `NETWATCH <machine> <interface> timer=active state=<misses rung wait busywaits lastrx> alert=no
tailnet=<interface> dry=False watchdog=<interval> device=yes panic=<n> oops=<n> softlockup=0 hungtask=0`, then
` stopped=<interfaces>` when it stopped other instances.
**Rollback.** `systemctl disable --now nova-netwatch@<interface>.timer`, remove `nova-netwatch@.service`,
`nova-netwatch@.timer` and `nova-watchdog-module.service` from `/etc/systemd/system`,
`/etc/systemd/system.conf.d/90-nova-watchdog.conf` (then `systemctl daemon-reexec`) and
`/etc/sysctl.d/91-nova-lockup.conf`.

## audit

What a Linux bench or runner needs for the fleet's CI work is one file, `runner-requirements.tsv`, one requirement per
line: its name, the architectures it applies to (`any`, `amd64`, `arm64`, with `not-wsl` to leave out WSL machines and
`vm-tier` to leave out a machine whose `nova_runner_vm_kernel` is false),
the play that installs it (`runner-packages`, `sdk-toolchain`, `github-runners`, `base` for the Ubuntu image itself, or
`none` where no play installs it yet),
and a bash command that exits 0 when it is met. The pins in it, versions and sha256, are the ones the CI work checks
for itself. A new requirement is a new line.

The `audit` play checks every line on each Linux machine of the `bench` and `runner` groups, as the login CI jobs run as
(`github_runner_user` where that login exists, else the fleet login), with the PATH a runner's jobs start with and that
login's home (the PATH of the machine record's `nova_runner_env`, or the login's tools directory before the system's),
each check bounded to 120 seconds. It only reads, under `--check` or not. Per machine:

```
FLEET-AUDIT MISSING host=<machine> item=<name> install=<play> check=<command>
FLEET-AUDIT NOT-APPLICABLE host=<machine> item=<name> arch=<arch> declared=<arch list>
FLEET-AUDIT host=<machine> login=<login> arch=<arch> ok=<n> missing=<n> not-applicable=<n>
```

Beside the file, the audit reads the locked-memory limit an io_uring step gets three ways, as the fleet login over
its own ssh session: that session's (`ulimit -l`, soft and hard), that of a command `nova-reserve run` starts (in a
directory of its own, so it takes and waits for none of the machine's slots), and the runners' (each loaded
`github-runner-*` unit's `LimitMEMLOCK`, each running `Runner.Listener`'s `/proc/<pid>/limits`). Where they differ
or one is below `nova_login_memlock`:

```
FLEET-AUDIT MISSING host=<machine> item=memlock ssh=<soft>/<hard> reserve=<soft>/<hard> runner=<values|none> need=<soft>/<hard> install=<plays>
```

(bytes, or `unlimited`; `health` fixes the session's, `tools` a missing nova-reserve, `github-runners` a runner's). Under
`join` it is left out, as `health` and `tools` come after it in `up`; `-e nova_audit_memlock=false`
leaves it out.

Then the first steps of a CI job, once for each login an active `github-runner-*` unit runs as, with the environment
a job of that runner starts from: its main process's (`/proc/<MainPID>/environ`, read as that login) with the runner's
`.env` on top. In a throwaway directory under its `TMPDIR`, removed after: rustup's toolchain install as
the write it needs (a temporary file in `rustup show home`'s `tmp`, the directories it writes writable), a `go build`
of an empty module, a CMake configure and build through `ccache` where it is on PATH, and every cache directory the
environment names (`CCACHE_DIR`, `CARGO_HOME` or `~/.cargo`, `GOCACHE`, `NUGET_PACKAGES`, `HEX_HOME`, `MIX_HOME`,
`OPAMROOT` and the like) writable. Each step is bounded to 180 seconds. A step that fails, a unit with no `User=`, an
environment that cannot be read and a probe that never answers (`job-run`):

```
FLEET-AUDIT MISSING host=<machine> item=job-<step> unit:<unit> login:<login> install=<plays> error=<its last lines>
```

A machine with no active runner unit runs no CI job: `FLEET-AUDIT NOT-APPLICABLE host=<machine> item=job reason=no
active github-runner unit`. `-e nova_audit_job=false` leaves the job out.

A machine with anything missing fails: `FLEET-AUDIT FAILED host=<machine> missing=<names>`. The fix is the play
the line names, run on that machine. A machine that is not Linux prints `FLEET-AUDIT SKIPPED`. `-e
nova_audit_requirements=<file>` checks another file of the same form.

```
nova-fleet audit -- -l bench
```

## runner-packages

The packages a CI runner's tests need, on each machine of the `bench` and `runner` groups (a bench runs the same gates), from `nova_runner_packages`
(`group_vars/all.yml`): a list per OS, Homebrew formulae on `darwin` (as the login; brew refuses root) and apt
packages on `debian`. The default is the toolchain of a fleet whose repositories build C, C++, Go, .NET, Java, OCaml
and Python and test them: on macOS `postgresql@18` (the functional tier), `cmake`, `libsodium`, `pkgconf` and `ccache` (the compiler cache); on
Debian and Ubuntu the compilers, assemblers and build systems (`build-essential`, `clang-18`, `lld`, `cmake`,
`ninja-build`, `ccache`, `nasm`), the libraries the tests link (libsodium, libbpf, libxdp, liburing, libcurl, MPFR,
zlib, OpenGL), the runtimes (`golang-go`, `dotnet-sdk-10.0`, Mono, a Java runtime, `opam`, Python's pip and venv),
the analysers and provers (`cppcheck`, `valgrind`, `cbmc`, `cadical`, `z3`, `gappa`, `sollya`), the store clients
(`postgresql-client-18`, `redis-tools`, `sqlite3`), containers, VMs and emulation (`podman`, `bubblewrap`, `virtme-ng`,
`qemu-system-x86`, `qemu-user`, `qemu-system-s390x` with the s390x cross compilers, and the s390x libraries a
big-endian build links, `libsodium-dev:s390x`, `libbpf-dev:s390x`, `libxdp-dev:s390x` and `libstdc++6:s390x`, from the
foreign architecture `s390x`), `bats`, `tcpdump`, GNU `time`, the archivers, the base tools CI steps call (`curl`, `git`,
`gnupg`, `iproute2`, `openssh-client`, `python3`) and `cloud-image-utils`; the list in `group_vars/all.yml` is
grouped that way. Every name resolves on Ubuntu 26.04; a Debian release or an older Ubuntu lacks some of the versioned
ones (`clang-18`, `postgresql-client-18`, `dotnet-sdk-10.0`), and there apt fails the task naming the package, and the
fix is your own list, below, without it. Debian has no Ubuntu ports archive, so there the play refuses the foreign
architecture; the fix is your own list without the `:s390x` names, run with the architecture turned off. A package already
installed is adopted as it is: never reinstalled, upgraded or auto-updated, so a run over it reports `changed=0`.
Only packages: no server is started, no `brew services`, no unit; the tests run their own `initdb` and `pg_ctl` (with
`LC_ALL=C`). CI never installs: the macOS functional job refuses when `initdb` is missing and names this play.

```
nova-fleet runner-packages --check -- -l bench      # WOULD-INSTALL apt-get install <name> (brew install --formula <name>), per missing one
nova-fleet runner-packages -- -l bench
```

A list given with `-e` replaces the default whole, not merged, so another list starts from a copy: a JSON value, or a
vars file after `-e @`, read from the directory you run in (the plays are elsewhere, so a file of yours never shadows
one). A list without the `:s390x` packages goes with the architecture off, `-e '{"nova_runner_foreign_architectures": []}'`.

| Variable | Default | |
|---|---|---|
| `nova_runner_packages` | `darwin`: `postgresql@18`, `cmake`, `libsodium`, `pkgconf`, `ccache`; `debian`: the toolchain above (`group_vars/all.yml`) | another OS with a non-empty list is refused |
| `nova_runner_windows_ccache`, `nova_runner_windows_ccache_dir` | `4.14.3` and its zip's sha256; `C:\ProgramData\nova\ccache` | **Windows ccache.** On a WSL machine whose record names `windows_runners` or `windows_admin_runners`, ccache's own Windows release, checked and unpacked on the WSL side, then copied through `/mnt/c` into that directory, sealed before and after (owned by `Administrators`, nothing inherited, read and run for Authenticated Users and Users, so no job can change what every job runs; an admin runner's install refuses it otherwise), as `ccache.exe` and `msvc\cl.exe` (the same binary under the compiler's name: MSBuild runs it in place of cl.exe when a build sets `CLToolExe=cl.exe;CLToolPath=<dir>\msvc` in `CMAKE_VS_GLOBALS`, since the Visual Studio generator honours no compiler launcher; ccache then runs the real cl.exe from the PATH MSBuild sets). The version in `VERSION` beside it. Every run compares the directory with the release (the zip kept on the WSL side): anything else in it, a binary that differs or a link, and the directory is removed and made again, sealed in the one call that creates it (`files/ccache-dir.ps1`, which refuses `CCACHE-DIR-PLANTED` a folder a job made in the gap), so a file or a handle a job put there while it was open is never sealed in; one still there after the seal fails the run. An empty version skips it |
| `nova_runner_foreign_architectures` | `[s390x]` | Ubuntu: dpkg architectures the debian list may name packages of, as `<name>:<arch>` |
| `nova_runner_ports_uri` | `http://ports.ubuntu.com/ubuntu-ports/` | the archive a foreign architecture comes from |
| `nova_runner_vm_kernel` | `true` | Debian/Ubuntu (not WSL): keep each kernel image readable by the login, for a VM of the host's own kernel; a runner login of its own gets root's copies in `/opt/nova/vm-kernels` (its `~/vm-kernels`, a kernel hook for each new image) from `github-runners`, which fails with `VM-TIER-MISSING` naming what the tier lacks (vng, qemu, a writable `/dev/kvm`, the image, the modules) |
| `nova_runner_vm_kernel_dir` | `<home>/vm-kernels` | where the copies go, as `vmlinuz-<version>` |
| `nova_runner_tla_jar`, `nova_runner_tla_jar_sha256` | `/opt/tla/tla2tools.jar`; the sha256 of TLA+ tools v1.7.4 (the one `tests/tla/tla2tools.sha256` pins) | Debian/Ubuntu: **TLC's jar**, below; an empty sha256 skips it |
| `nova_runner_tla_jar_url` | the TLA+ v1.7.4 release's `tla2tools.jar` | where a missing jar is downloaded from |
| `nova_runner_msan_libcxx_prefix`, `nova_runner_msan_libcxx_sha256` | `/opt/msan-libcxx-`; the llvm-project source sha256 of clang 18.1.3 and 21.1.8 | Debian/Ubuntu x86_64: **The MemorySanitizer libc++**, below; an empty map skips it |

Both switches apply on Debian and Ubuntu only; another OS skips them. The play reaches every bench as well as every runner: a WSL2 machine (`/proc/sys/fs/binfmt_misc/WSLInterop`) has no `/boot/vmlinuz-<version>` and no `kvm` group, so the VM tier is skipped there.

**Foreign architectures.** A cross build that links a library of another architecture (`libsodium-dev:s390x`) needs
dpkg multiarch. For each architecture the list names, the play runs `dpkg --add-architecture` where
`dpkg --print-foreign-architectures` lacks it, writes `/etc/apt/sources.list.d/ubuntu-ports-<arch>.sources` (deb822,
`Architectures: <arch>`, the release's suites with `-updates` and `-security`, signed by the Ubuntu archive key), and
restricts the native sources in `/etc/apt/sources.list.d/ubuntu.sources` to the native architecture by adding
`Architectures: <native>` to each, so the main archive is never asked for the foreign architecture nor the ports archive
for the native one. It edits that file only when none of its sources has an `Architectures` line, and refuses when
some do and some do not. It never edits `/etc/apt/sources.list` (the one-line form, which Ubuntu 22.04 uses): a `deb`
line there with no `[arch=...]` is refused, naming the line; restrict it by hand (`deb [arch=amd64] http://...`) and run
again. Other files in `sources.list.d` are left as they are. apt reads its sources again after any of these changes. The
presence check matches `<name>:<arch>` as package_facts names a foreign architecture's package, so an installed one is
adopted like any other. `--check` prints `WOULD-ADD-ARCHITECTURE dpkg --add-architecture <arch>` and diffs the sources.

**The VM tier's kernel.** A CI job that boots a VM of the host's own kernel (virtme-ng: `vng --run
<dir>/vmlinuz-$(uname -r)`) needs that kernel's image readable by the runner's login, and Ubuntu installs
`/boot/vmlinuz-<version>` readable by root only. With `nova_runner_vm_kernel: true` the play writes the kernel hook
`/etc/kernel/postinst.d/zz-nova-vm-kernel`, which copies every kernel image the host installs from then on into
`nova_runner_vm_kernel_dir` (owned by the login, mode 0600), so an upgrade brings its copy with it; copies the running
kernel's image there now; and adds the login to `kvm`, the group `/dev/kvm` is open to. A host whose running kernel has
no image in `/boot`, or that has no `kvm` group, is refused with the package to add. The VM tools themselves are entries
of the debian list (`virtme-ng`, `qemu-system-x86`). Joining `kvm` reaches a runner already running only when it
restarts. Copies of kernels later removed stay until deleted.

**TLC's jar.** A model check runs `java -cp <jar> tlc2.TLC` with the Java runtime of the debian list and the jar at
`nova_runner_tla_jar`. A jar there whose sha256 is the pin is left as it is. One that differs is refused, and nothing is
written: `TLA-JAR-MISMATCH host=<machine> path=<jar> want=<sha256> got=<sha256>`; remove it and run again. A missing jar
is downloaded from `nova_runner_tla_jar_url`, and a download whose sha256 is not the pin is refused before it lands:
the directory root's, 0755, the jar root's, 0644, so the fleet login and a runner login of its own both read it.
`--check` prints `TLA-JAR WOULD-DOWNLOAD`; each machine gets `TLA-JAR host=<machine> present|downloaded <jar> sha256=<pin>`.

**The MemorySanitizer libc++.** A MSan build links a libc++ and libc++abi built with
`LLVM_USE_SANITIZER=MemoryWithOrigins`, at the release of the clang that compiles it (the headers and the compiler move
together), from `<nova_runner_msan_libcxx_prefix><clang version>`. On x86_64 the play asks the system PATH's
`clang++ -dumpversion`; a prefix holding `lib/libc++.so` and `include/c++/v1/__config_site` is left as it is. Otherwise it
downloads that release's `llvm-project-<version>.src.tar.xz`, refused unless its sha256 is the pin, configures the
runtimes with Ninja, builds them as the login under `nova-reserve run` (one slot's share of the machine's CPUs; on a
machine joining, which has no nova tools yet and runs nothing else, half the CPUs at nice 10), installs
them as root and removes the build directory. A clang with no pin is refused: `MSAN-LIBCXX-NO-PIN host=<machine>
clang=<version>`. Each machine builds its own because the library must match its own compiler; on a 48-core bench a
slot's share builds it in about ten minutes. `--check` prints `MSAN-LIBCXX WOULD-BUILD`; each machine gets
`MSAN-LIBCXX host=<machine> clang=<version> present|built <prefix>`.

**Check.** `RUNNER-PACKAGES host=<machine> wanted=<n> present=<n> installed=<n>`, one line per machine.
**Rollback.** The play never removes a package: one taken out of the list stays installed until a play removes it. Nor
does it remove a foreign architecture, a ports source, the `Architectures` lines, the kernel hook, the copies, TLC's jar or the MemorySanitizer libc++.

## sdk-toolchain

The language toolchains a bench's gates and a runner's CI jobs build and test with, in the login's own SDK directory
(`nova_sdk_dir`, `~/sdk`), on each machine of the `bench` and `runner` groups. Every version is pinned in
`roles/sdk-toolchain/defaults/main.yml`; an archive is checked against the publisher's own digest before it is unpacked. Each tool goes into
`~/sdk/<tool>-<version>`, its commands are linked into `~/sdk/bin`, and `~/sdk/env.sh` puts `~/sdk/bin` first on PATH
(on Linux, `~/.bashrc` sources it on its first line, before an early return for a non-interactive shell). It is the one
PATH: on Linux the play removes the hand Go that came before it (the "fleet Go" block in `~/.bashrc`, and `~/go/bin/go`
and `gofmt` linked to an SDK Go, unless `~/.local/bin/go` points at the link) and the hand `~/sdk/bin/gh` link to
gh 2.102.0. The play runs
as the login and never as root; run `runner-packages` first, which brings the compilers, `cmake`, `opam` and LLVM 18
that the source builds need. The play follows every symlink in `nova_sdk_dir` first and works on the real directory,
so `env.sh` and every link in `~/sdk/bin` name real paths: a sandbox that grants the SDK by its real path cannot
traverse a symlink outside its grants.

```
nova-fleet sdk-toolchain --check --diff -- -l bench      # SDK-TOOLCHAIN WOULD-INSTALL tool=<name> version=<v>, per missing one
nova-fleet sdk-toolchain -- -l bench
```

On x86_64 Linux and WSL: Go, Node, Dart, Erlang/OTP, Elixir, a Temurin JDK, .NET 10 with the .NET 8 SDK and runtime in
the same directory, golangci-lint, staticcheck, nilaway, ESBMC, SpotBugs with find-sec-bugs, Deno, Jazzer,
google-java-format, TypeScript, ESLint, sbcl, Lua, luacheck, StyLua, Luau, moo (built from its tag, the commit it was
built from in `~/sdk/moo-v<version>/PIN`), Goose, Lobster, Squirrel (and a second Squirrel built as sq64 with
`SQUSEDOUBLE`, not on PATH, named by `SQ`), CPython 3.11 and 3.15 built from source, each with mypy from hash-locked
wheels (`python3.11`, `python3.15`, as `~/sdk/python-<version>`), clang-format, LLVM (as `~/sdk/llvm`, not on PATH), cppcheck, AFL++ and Frama-C with Alt-Ergo and Why3 in their own opam root (`~/sdk/opam`). A Mac gets the tools
with a `darwin` pin: Go, Node, Dart, Erlang/OTP, Elixir, the JDK, .NET, Lua, StyLua, Luau, moo, Goose, Lobster, the
double Squirrel, CPython 3.11 and 3.15 with mypy, and Rust. Erlang/OTP there is built from the release's source (hex.pm
publishes no macOS build), with crypto and ssl linked statically against Homebrew's `openssl@3` or `openssl@4`, one of which must be on the
host (`postgresql@18` from `runner-packages` brings one), as must the Xcode command-line tools. Rust there is the
release's standalone installer (`static.rust-lang.org`, held to its `.sha256`) with `clippy` and `rustfmt`, giving the
same commands in `~/sdk/bin` as the rustup below. Each tool has one of six kinds: an `archive` or a
`file` (a URL and its sha256 or sha512), `git` (a tag, refused unless it is the pinned commit, then built), `go`
(`go install` of a module version, which Go checks against its checksum database), `npm` (refused unless the lock file
the install writes has the pinned integrity; the package's own dependencies resolve from the registry at install time)
and `script` (a build alone: Frama-C's opam root, at pinned package versions from the opam repository as it is then).

Rust, on Linux, comes through a pinned rustup: `rustup-init` at `nova_sdk_rustup.version`, checked against its
`.sha256`, installs rustup into `~/sdk/rustup-<version>` with the first toolchain (a release, `1.98.1`) as the
default and self-update off; each toolchain then gets its components (`clippy`, `rustfmt`) and targets, and the dated
nightly that `cargo fuzz` runs on is installed beside it. The first toolchain's own binaries (`cargo`, `rustdoc`,
`rustfmt`, `cargo-fmt`, `cargo-clippy`, `clippy-driver`) are linked into `~/sdk/bin`. `~/sdk/bin/rustc` is a wrapper
that runs the toolchain's `rustc` with `-C linker=/usr/bin/cc` (`nova_sdk_rustup.linux.linker`): given a bare `cc`,
rustc starts it by fork with a socketpair that a seccomp sandbox such as `nova-sandbox` refuses, and the link fails
with `could not exec the linker`; cargo's own `-C linker` comes later and wins. `~/sdk/bin/rustup` is a
wrapper that uses this rustup's home unless the caller set `RUSTUP_HOME`, so a CI job that installs its own toolchain
into its own home is unaffected. `cargo-fuzz` is built with `--locked` into `~/sdk/cargo-fuzz-<version>`.

.NET 8: each SDK of `nova_sdk_dotnet_more` (8.0, with its 8.0 runtime) is unpacked from Microsoft's archive, held to
its SHA-512 from `releases.json`, into the dotnet directory beside the 10.0 SDK, keeping the newer `dotnet` host, so
one `~/sdk/bin/dotnet --list-runtimes` lists both and a `net8.0` target runs on .NET 8 rather than rolling forward.

A tool whose probe (`dir`/`probe` in the defaults) is in its directory is adopted as it is: never downloaded,
replaced or upgraded, so a second run reports `changed=0`. A tool with a `run` probe must also pass it: a shell
command run in its directory with `$dest` its real path, under `--check` too. One that fails is broken
(`SDK-TOOLCHAIN BROKEN host=<machine> tool=<name> rc=<n>: <probe>`) and is installed again like a missing one. Erlang/OTP's
holds the root `bin/erl` falls back to inside its own real directory and runs `erl -noshell -eval 'halt().'`, so an
install made through a symlinked SDK is rebuilt. A missing one is fetched into `~/sdk/dl`, unpacked in a
stage beside the SDK and moved into place (a `git` or `script` tool builds into its directory); a directory there
without its probe, or broken, is moved aside to `<dir>.broken-<time>`, never deleted. `env.sh` keeps a dated backup of the file it
replaces. Every link the defaults name is pointed at the pinned version, so a machine whose `~/sdk/bin` named
another version of a tool names the pinned one after a run; the other version's directory is left where it is. A
version bump installs the new directory beside the old and moves the links.

| Variable | Default | |
|---|---|---|
| `nova_sdk_dir` | `<home>/sdk` | the SDK directory, used by its real path |
| `nova_sdk_shared` | on for a Linux runner whose runners are another login's (`github_runner_user`) | Linux: the SDK in `/opt/nova/sdk` (`nova_shared_dir`, made by `github-runners` with a runner login of its own), the login's and readable by every login, with `nova_sdk_dir` a link to it, so a runner login of its own runs the same tools. The shared SDK is installed fresh (its files name their own path) while `~/sdk` stays in use; at the end of the run `~/sdk` becomes a link to it (`SDK-TOOLCHAIN SHARE`) and the old one is deleted. Nothing in it is left writable by other logins, and a run fails on anything in it they cannot read; an old `~/sdk` holding what the shared one lacks (an older version a link names, a directory put there by hand) stays in use and the run fails naming it (`SDK-TOOLCHAIN SHARE-HELD`) |
| `nova_sdk_tools` | the toolchain above | one entry per tool: `name`, `version`, `dir`, `probe`, `links`, `kind`, `build`, `alias`, `wrapper`, `run`, and a `linux` and/or `darwin` pin (a pin's own `build` replaces the tool's on that platform); a list given with `-e` replaces it whole |
| `nova_sdk_dotnet_more` | the .NET 8.0 SDK | more SDKs in the dotnet directory, each a `version` and a `url` and `sha512` per platform |
| `nova_sdk_rustup` | rustup 1.29.1; `1.98.1` with `clippy` and `rustfmt`, and `nightly-2026-10-01` | `version`, the `linux` rustup-init `url`, `sha256` and `host`, the `toolchains` (each `name`, `components`, `targets`; the first is on PATH) and the commands `links` puts in `~/sdk/bin` |
| `nova_sdk_cargo_tools` | `cargo-fuzz` 0.13.1 | crates built with `cargo install --locked` |
| `nova_sdk_env` | `DART`, `NODE`, `JAVA`, `JAVAC`, `ELIXIR`, `MIX` set to their commands | make variables `env.sh` exports |
| `nova_sdk_env_files` | `SQ`: `squirrel-3.2-double/bin/sq`; `LUA`, `LUAU`, `LUAU_ANALYZE`, `MOO`, `MOO_ANALYZE`, `GOOSE`, `LOBSTER`: each the interpreter or analyzer under its tool's directory; `NOVA_SDK`: `.` (the SDK directory, for a Makefile that stages a pinned toolchain from `~/sdk/<tool>-<version>` instead of downloading it) | make variables naming a file under the SDK; `env.sh` exports each only where that file is executable |
| `nova_sdk_shell_rc` | `true` | Linux: `~/.bashrc` sources `env.sh` on its first line, unless a line of it already names `sdk/env.sh` |

**Check.** `SDK-TOOLCHAIN host=<machine> platform=<linux|darwin> tools=<n> present=<n> installed=<n> dotnet=<runtimes>`,
one line per machine (`would-install=<n>` under `--check`, counting the tools; the .NET SDKs and Rust print their own
`WOULD-INSTALL` lines). Another platform is refused, naming `-l '!<machine>'`.
**Rollback.** The play removes nothing: set the old version back in the defaults (with its URL and digest) and run it
again, and the links name the old directory, which is still there.

## Refusals

Each refusal exits 2 before running a play. Invocation errors are one line on
stderr; missing prerequisites also print one diagnostic per gap on stdout:

| You ran | It says |
|---|---|
| an unknown play or verb | the verbs and the plays, and `run: nova-fleet help` |
| an unknown flag before `--`, or a word before `--` | `ansible-playbook flags go after --` |
| `--inventory` naming nothing | the path, and to name an existing file or script |
| `nova-fleet` with no verb or play | `nova-fleet: want a verb or a play; run: nova-fleet help` |
| ansible-playbook, its `ansible.posix` collection, or (with no `--inventory`) nova-config missing | one `CHECK MISSING <name>: <the pasteable fix line>` per gap on stdout, all at once, then `this control node lacks <n> of what the play needs ...; run: nova-fleet check` |

A play whose machine groups match no host is refused with exit 1 before it runs: `no host is in <groups> ...; run:
nova-config machine set <machine> --roles <its roles>,<group> --actor <you>, then nova-fleet <play> again`.
Otherwise the exit is 0 when ansible-playbook passed and 1 when it did not, with `nova-fleet <play>: FAILED
ansible-playbook exit=<n>` naming its status on stderr (2 never means a failed host here); an ansible-playbook killed
by a signal is `killed by signal <N> (exit 128+N)`. Before running, nova-fleet prints the ansible-playbook, inventory
and play paths it uses on stderr. The extracted directory is removed on every
exit, a refusal included.

## Build and test

```sh
build_dir=$(mktemp -d)
go build -o "$build_dir/nova-fleet" ./cmd/nova-fleet
go test ./cmd/nova-fleet                   # unit: the embedded tree, purposes, arguments, refusals (no ansible)
go test -tags functional ./cmd/nova-fleet  # ansible-core on PATH: the plays run through the binary and templar
```

Add a play by adding `<name>.yml` here with a `# purpose:` line at its top; the binary embeds the whole directory.
