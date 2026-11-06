# nova-fleet

nova-fleet puts the same software on every machine you own. It is the fleet's Ansible plays in one binary: the
plays, roles, templates and defaults are embedded, a run extracts them to a private temporary directory, points
`ansible-playbook` at your inventory (`nova-config inventory` unless you name another) and exits 0 when it passed,
1 when it failed (ansible-playbook's own status named on stderr), 2 when it could not run.

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
collection. On macOS `brew install ansible`; on Linux a venv (Debian and Ubuntu ship python3 without it, so there the
line starts `sudo apt-get update -qq && sudo apt-get install -y python3-venv && `; `nova-fleet check` prints the line for this machine, and says so when
python3 has no venv module):

```
python3 -m venv ~/.local/ansible && ~/.local/ansible/bin/pip install ansible-core
nova-fleet show requirements > requirements.yml && ansible-galaxy collection install -r requirements.yml
```

## Fleet with Tailscale, go!

The whole path, in order. Each step's output says what to do next.

1. `nova-fleet check` on the control node: one line per prerequisite, `CHECK OK sops 3.13.3` or `CHECK MISSING
   age-keygen: sudo apt-get update -qq && sudo apt-get install -y age` (the fix for this OS, a command pasteable as it
   stands with any reason after `#`, in paste order: `git` and `go` before the `nova-*` build lines), then the tailnet (a `CHECK NOTE` when this node is not on it: `up` joins it), the secrets store, key, seat and pins,
   `TAILSCALE_API_KEY` (`--runners URL` adds `gh` and its login, `gh auth login`), and `NOVA_PG_DSN` once a fleet
   exists. Run each fix line, then `nova-fleet check` again until it ends `CHECK OK missing=0` (exit 0; 1 while
   anything is missing; the collections of `requirements.yml` count, `ansible.posix` for the push of the store). A
   green check ends `CHECK OK missing=0 next: nova-fleet up --check ...`. Every play verb runs the part of this check
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
nova-fleet up --check --tools v2.0.0 store-1=203.0.113.7 web-1=203.0.113.8
nova-fleet up --tools v2.0.0 store-1=203.0.113.7 web-1=203.0.113.8
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
nova-fleet up --check --tools v2.0.0 store-1=203.0.113.7 web-1=203.0.113.8 mac-1=mac-1.local/darwin/arm64
nova-fleet up --tools v2.0.0 --runners https://github.com/OWNER/REPO store-1=203.0.113.7 web-1=203.0.113.8
nova-fleet up --tools v2.0.0 store-1=203.0.113.7 -- -e nova_pg_version=17
```

(203.0.113.x are placeholder addresses; use your own.)

**What is yours, once, before the first run:**

1. The Tailscale account, and an API access token from it ("tailscale" below: what is yours, once).
2. The control node: ansible-core, `nova-config`, `nova-secrets`, `nova-bus`, `tailscale` (`up` joins it to the
   tailnet and tags it `tag:control`), `psql`, `git`, `gpg`, `sops`, `age`, `age-keygen`, `rsync` and `ssh` on PATH (`gh` too for runners, logged in with `gh auth login`;
   `nova-fleet check` lists what is missing with the line that installs it); a secrets store with this node's seat
   and its pins outside it ([secrets/README.md](../secrets/README.md)): `NOVA_SECRETS_STORE`, `NOVA_SECRETS_KEY`,
   `NOVA_SECRETS_SEAT` and `NOVA_SECRETS_PINS`, each defaulting as nova-secrets does (`~/.config/nova-secrets/store`,
   `~/.config/nova-secrets/<short hostname>.key`, `~/.config/nova-secrets/pins`, the short hostname). `check` and
   `up` use the same defaults and put them in the environment of what they run; a node laid out that way gets no
   store, key, seat or pins line, and a fresh node with none of them gets none either: the next step makes them.
3. The token in the seat: `nova-secrets set TAILSCALE_API_KEY` (it asks; paste the token, one line
   `tskey-api-...`, and press enter). On a fresh node it first makes the store, a recovery key, this node's key, the
   pins and the seat, and says to back up the recovery key. It expires (90 days by default): a run with an expired one stops at the tailnet step with
   `HTTP 401: TAILSCALE_API_KEY was refused; ...` and the line that makes a new one.
4. Each Linux machine reachable as root over ssh at its address, with the control node's key, and its host key
   already in this node's `known_hosts` (ssh to it once by hand; `up` prompts for nothing). A Mac: its login and
   passwordless sudo set up at its console, and the Tailscale app's VPN approved once. Once a machine is on the
   tailnet, `up` reaches it as its login at its tailnet name, with the Tailscale SSH host key `tailscale status`
   reports pinned in `known_hosts` (`UP HOSTKEY <machine> OK pinned=<n>`); it never turns host key checking off.

**The order.** On a new fleet the first machine named holds the fleet's stores; on a fleet that exists, the store
machine is the one nova-config's rows say ("Adding a machine" below). Nothing here is a choice to make again:

| # | Step | What runs | Why here |
|---|---|---|---|
| 1 | control | `nova-fleet check`: every helper (with `--runners`, `gh` and its login), the store, key, seat and pins, `TAILSCALE_API_KEY` read from the seat, `NOVA_PG_DSN` when a fleet exists; anything missing stops the run with its fix line | a run never starts that a missing secret would stop later |
| 2 | tailnet | through the Tailscale API v2 with `TAILSCALE_API_KEY` as Bearer auth (`fleet/tailnet.go`): `GET /tailnet/-/acl` (HuJSON, its ETag), the fleet's needs (`nova-fleet show tailscale-acl`) merged in as a JSON Patch applied by `github.com/tailscale/hujson` (comments and formatting kept), `POST` with `If-Match` only when that added something (a 412 reads again and retries once); this node joined with a minted `tag:control` key when it is not on the tailnet, else `POST /device/<id>/tags` adding `tag:control`; each machine on the tailnet (the one peer whose MagicDNS label is its name; two such peers are refused) given `tag:nova` (and `tag:runner`) the same way; a machine that is this node itself (`tailscale status` Self, by the same label) is already on the tailnet and gets neither; for each machine not on it, `POST /tailnet/-/keys` mints a single-use, pre-authorized key good for an hour, held in memory and given only to that machine's play as `TAILSCALE_AUTHKEY` in its environment. Receipt `UP TAILNET OK policy=<merged\|unchanged> control=<tagged\|already\|joined> keys=<n minted> retagged=<n>`; under `--check`, `UP TAILNET WOULD ...` after GETs only | the tags exist before a device carries one, and the keys before any join |
| 3 | reach | one `ssh -o BatchMode=yes ... true` per machine: as its login at its tailnet name once it has a row (join closed the address given then), else as root at the address given; `UP REACH <machine> OK <who>@<where>` | a run never starts a play on a machine it cannot reach |
| 4 | first | on a new fleet's first machine, from a hand inventory: `tailscale` (no address recorded, firewall later), then `postgres` (nova-config's roles, grants and tables, loaded on the machine as `nova_admin`) and `redis`, their passwords sealed into your seat as `NOVA_PG_PASSWORD` and `NOVA_BUS_PASSWORD` | nova-config's rows live in this PostgreSQL, so it comes before any row |
| 5 | join | `join` for each machine (`services,bench` for the first; the others `bench`; `runner` with `--runners`): its row, its login with passwordless sudo, its tailnet name and host key, its firewall, its seat; with `--runners`, a row that lacks the `runner` role gets it (`UP ROLE <machine> OK runner`). This node itself, named as a machine, is skipped (`UP JOIN <machine> SKIPPED this control node ...`): join's peer check cannot see the node it runs on, so its row must exist already (else the run stops with the `nova-config machine add` line) | the store exists now |
| 6 | grant | `nova-secrets grant <--grant names> --to <machine> --pub <its seat key>` for each machine: the values written to that seat's grants file, encrypted to the seat's key (read from the machine over the tailnet with `age-keygen -y`, found in `nova_bin_dir` when `-- -e nova_bin_dir=...` sets it, then `~/.local/bin`; public half only, and pinned in `NOVA_SECRETS_PINS`) and the recovery key; your seat is never a recipient ([secrets/SPEC.md](../secrets/SPEC.md)) | the seats exist now |
| 7 | store | `git push` of your secrets store to its upstream, if it has one (else a NOTE): a copy off this node; the machines never read it. When the upstream refuses the push (`[remote rejected]`: a protected branch that takes changes only through a pull request), the commits are pushed to the branch `nova-fleet-up/<short commit>` instead, `UP STORE PUSH REFUSED: <upstream> is protected (...); pushed branch <name>; open a PR from it` is printed, the run goes on, and it exits 1 (`UP STORE FAILED`) at the end; open and merge that PR. Any other push failure stops the run here | |
| 8 | refresh | `secrets-refresh` with `nova_seal_store` set to your clone: the encrypted store pushed over the tailnet with rsync to `~/.local/share/nova/secrets/` on each machine (no `.git`, stale files deleted, 0700/0600) | each seat opens what step 6 granted; a rerun of `up`, or `nova-fleet secrets-refresh -- -e nova_seal_store=S`, pushes again |
| 9 | health | `health` on every machine (Linux; a Mac is skipped) | the guards are in place before anything long-running starts |
| 10 | tools | `install` on every machine, from the release `--tools vX.Y.Z` or a local build `--tools DIR` | the services run these binaries |
| 11 | services | `services` | each seat holds its granted values now |
| 12 | runners | `github-runners`, `--runner-count` per machine (only with `--runners`); the play asks GitHub for its token with this node's `gh` login | |
| 13 | bus | `nova-bus who` over the store machine's Redis with the fleet's `NOVA_BUS_PASSWORD`; it names no inbox and sends nothing as a person (receipt `UP BUS OK <redis> present=<n>`) | the fleet is reached end to end |

Each play prints its own receipts; each step adds `UP <step> OK` (or `WOULD-...` under `--check`), and the run ends
with one line per machine, `UP MACHINE <name> OK address=<address> steps=<the steps it passed>`, and on a new fleet
`UP DONE next: export NOVA_PG_DSN=...`, the line the next `up` and nova-config read the fleet from. A failed step
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
step 5 has granted `NOVA_BUS_PASSWORD` and step 7 has pushed it.

**Adding a machine.** A fleet that exists is read from nova-config, never guessed from the command line: export
`NOVA_PG_DSN` (the line `up` printed, `postgres://nova_config@<store machine>:5432/nova`; `check` notes it once your
seat holds `NOVA_PG_PASSWORD`), then name the new machines, and the ones you want converged again if you like. A
first run that stopped after PostgreSQL resumes without it: the first machine named is the store `up` made, and its
rows are read from there.

```
export NOVA_PG_DSN=postgres://nova_config@store-1.<tailnet>.ts.net:5432/nova
nova-fleet up --check --tools v2.0.0 web-2=203.0.113.9
nova-fleet up --tools v2.0.0 web-2=203.0.113.9
```

The store machine is the row with the role `services`; no machine named becomes "first", and PostgreSQL and Redis are
not installed again. A machine with a row is reached as its row's user at its row's address, and `join` runs its
login play not at all (its login exists, and the policy lets no one in as root over the tailnet); a machine without one is
reached as root at the address given, as on the first run.

## The plays

| Play | Purpose | Needs |
|---|---|---|
| `github-runners` | GitHub Actions runners on every machine of the runner group, registered to one repository or organization | `github_runner_url`, `github_runner_scope`; `gh` on the control node logged in with admin on the repository |
| `health` | guards that let a Linux machine get itself out of trouble, and a disk report | nothing |
| `install` | install one nova-tools release, by tag, on the machines named | `nova_tools_version` (a tag `vX.Y.Z`; no default) |
| `join` | a new machine joins the fleet: its row, its login, the tailnet, the tools a seat needs, its seat | the machine as the argument; its row's fields; `NOVA_PG_DSN` and `NOVA_PG_PASSWORD` in the environment, and `TAILSCALE_AUTHKEY` for a machine not on the tailnet (`up` mints it) |
| `postgres` | the PostgreSQL nova-config keeps the fleet in, on each machine of the services group | `nova_seal_*` to seal its password |
| `redis` | the Redis nova-bus talks to, on each machine of the services group | `nova_seal_*` to seal its password |
| `seat` | each machine's seat in the secrets store | `nova_seal_store` |
| `secrets-refresh` | the control node's encrypted store pushed to every machine with `ansible.posix.synchronize` (`verify_host: true`, no `.git`, stale files deleted, 0700/0600); with `nova_secrets_repo` set, a timer pulls a clone fast-forward instead | `nova_seal_store` (the control node's clone) to push; `nova_secrets_repo` for pull |
| `services` | one supervised unit per service record, on the machine the record names | the inventory's `nova_services` |
| `tailscale` | each machine on the tailnet, its address the MagicDNS name, its firewall closed to all but the tailnet | `TAILSCALE_AUTHKEY` in the environment for a machine not on the tailnet (`up` mints it) |

```
nova-fleet install -- -e nova_tools_version=v1.0.0 -l bench
nova-fleet services --check --diff
nova-fleet github-runners -- -e github_runner_url=https://github.com/OWNER/REPO -e github_runner_scope=repo
```

`nova-fleet plays` is the list in the binary you hold; `nova-fleet show <play>` prints one, and `nova-fleet show
tailscale-acl` what `up` merges into the tailnet policy. Every play is idempotent and prints one receipt line per machine; under
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
| `nova_bin_dir` | `~/.local/bin` | where they go (Windows: `<profile>\.local\bin`); v2 binaries replace v1's of the same name there, so a v1 sprint still running from it stops working after `up` (accepted) |
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
the host's own os (`linux`, `darwin`, `windows`) and arch (`amd64`, `arm64`) as its facts report them (a machine row's
`os` and `arch` fields say how it is reached, never what it is labelled), plus `github_runner_labels`; so a bench
machine's runner is labelled `amd64,bench,linux,runner`, and a job selects it with `runs-on: [self-hosted, linux]`.
Linux (amd64, arm64; WSL2 with systemd counts as Linux), macOS and native Windows (the runner's own service;
`ansible.windows` and an administrator login). On macOS the runner is a LaunchDaemon dropped to the login, not a
LaunchAgent: a LaunchAgent runs only inside a logged-in GUI session, and a CI machine comes back from a reboot with
none. Only a run needs sudo for that file (`/Library/LaunchDaemons` is world-readable, so `--check` reads and diffs it
as the login and finishes without a password). Linux and macOS write the service files with sudo, so the login needs
passwordless sudo: `join.yml` sets it up on Linux (`/etc/sudoers.d/90-nova-<login>`); on macOS set it at the console.
No command here asks for a sudo password: an interactive prompt is a stall for an AI running the play.

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
nova-fleet github-runners --check --diff -- -e github_runner_url=https://github.com/OWNER/REPO -e github_runner_scope=repo
nova-fleet github-runners -- -e github_runner_url=https://github.com/OWNER/REPO -e github_runner_scope=repo
```

**The check.** After the run, GitHub's own list of runners is read with `gh api <repos|orgs>/<path>/actions/runners`
on the control node (a `gh` login with admin on the repository or organization), giving a new runner a minute to come
online, and each machine prints one line: `RUNNERS <machine> want=<n> online=<n> listed=<names>`. A wanted runner that
is not online fails the run; `verified=no reason=...` says why the list could not be read. `-e
github_runner_verify=false` skips it.

**What it does.** Per runner: the pinned release, checked against GitHub's digest, unpacked in
`<home>/actions-runner/<owner>-<repo>-<i>` (Windows `C:\actions-runner`); registered as `<machine>-<i>` unless
`.runner` is there; one service per runner, enabled at boot. Labels are fixed at registration: changed labels are
reported as `LABEL-DRIFT` and the runner is left alone. A changed unit restarts its runner, ending any job on it. A
machine whose count drops has its highest-numbered runners retired and removed from GitHub, with a removal
token (`.../actions/runners/remove-token`, asked for the same way). A second run changes nothing.

**Plan.** Under `--check` each machine prints `RUNNER-PLAN <machine> want=<n> register=<names> retire=<names>
labels=<labels>`, the runners named `<machine>-<i>` as GitHub will list them.

**Rollback.** Retiring asks gh for a **removal** token the same way; run with `-e github_runner_state=absent`:

```
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
   one whose user is not `nova_join_user` stops the join by name. The address's SSH host key is pinned into the
   control node's `known_hosts`: the key you give as `nova_join_host_key` (from the provider's console; this proves
   which machine it is), else what the address answers at this first connection (trust on first use: this proves only that it is the
   same machine from here on). A nova-config row write plus a join admits a machine to the fleet: the tailnet-peer
   check and `nova_join_host_key` are the mitigation, and push access to the config store is the trust boundary.
2. **The login** (Linux, connecting as root over the address given; skipped once the row's address is the tailnet
   name, since the login exists then and the policy lets no one in as root over the tailnet). The row's user: no
   password, in `nova_join_sudo_group` with
   passwordless sudo (`/etc/sudoers.d/90-nova-<login>`, checked by `visudo` before it lands), root's
   `authorized_keys` copied to it (or the control node's file `nova_join_authorized_keys` names), linger on, the
   hostname set to the machine name. A darwin machine has no root login: set its login and sudo up at its console.
3. **The tailnet**: `tailscale` (below). From here on the plays reach the machine over the tailnet.
4. **The tools a seat needs** (as the login): `nova_bin_dir` on the login PATH; on Linux the pinned `age` and `sops`
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

A Windows machine gets 1 and 3.

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
| `nova_join_root_user` | `root` | the login a fresh Linux machine is reached with (`up` passes the login it reached the machine as) |
| `nova_join_sudo_group` | `sudo` | the group the login joins (`wheel` on Red Hat) |
| `nova_join_authorized_keys` | root's on the machine | a file on the control node to install instead |
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
   `tag:runner` and `tag:control` on every port (`ip: ["*"]`), and an ssh rule accepting `autogroup:admin` into
   `tag:nova` and `tag:runner` as `autogroup:nonroot` and `root`. The members reach the store's ports
   (`tcp:5432`, `tcp:6379`) on `tag:control` too, for a fleet whose store is the control node itself, so the store
   does not depend on an allow-all rule staying in the policy. Nothing the policy has is removed or replaced, so
   the default policy's allow-all `acls` rule still allows all. It is written (`POST` with `If-Match`, HuJSON) only
   when the merge added something; a 412 reads it again and retries once; a second run writes nothing.
2. This node tagged `tag:control` (`POST /api/v2/device/<Self.ID>/tags`, its tags kept); a node not on the tailnet is
   first joined with a minted `tag:control` key (`tailscale up --reset --auth-key=file:/dev/stdin`, the key on stdin;
   `sudo` on Linux). A device of yours tagged this way is owned by the tag from then on, not by your account.
3. Each machine named that is on the tailnet (the one peer whose MagicDNS label, the DNS name before its first dot,
   is the machine's name; two such peers stop the run, naming both, and nothing is tagged) given `tag:nova` (and `tag:runner` with `--runners`) the same way; for
   each that is not, a key from `POST /api/v2/tailnet/-/keys`: single use, pre-authorized, those tags, an hour. It is
   held in memory and reaches only that machine's play, as `TAILSCALE_AUTHKEY` in its environment.

**What the play does, per machine:**

1. Installs Tailscale: Tailscale's apt repository on Debian and Ubuntu (the same key and source line its own install
   script writes), Homebrew's cask on macOS, winget on Windows.
2. `tailscale up --reset --auth-key=file:/dev/stdin --hostname=<machine> --advertise-tags=tag:nova[,tag:runner] --ssh`
   (`--ssh` on Linux; the macOS and Windows clients do not serve Tailscale SSH). `tag:runner` is added for a machine in
   the `runner` group. The key comes from `TAILSCALE_AUTHKEY` in the control node's environment (`up` puts the one it minted there; run
   alone, give one) and reaches the
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
nova-secrets exec --only NOVA_PG_PASSWORD --store S --key K --as SEAT -- \
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
| `nova_pg_seal_role`, `nova_pg_seal_name` | `nova_config`, `NOVA_PG_PASSWORD` | |

**Check.** `PG OK <machine> version=18 db=nova listen=<100.x>:5432 roles=3 timer=enabled changed=nothing` and `PG
REACH from=control`; a run with a gap ends `PG FAIL` with what is wrong. **Rollback.** `systemctl disable --now
nova-pg-dump.timer`; remove `conf.d/nova.conf`, restore `pg_hba.conf` and restart the cluster. The database, the
roles and the dumps are left.

## secrets-refresh

The usual way a machine's copy of the store stays fresh is a push from the control node (rsync over the ssh the
play has, the machine's host key verified as everywhere else; rsync on the machine comes from join): `nova-fleet up` step 6
copies the encrypted store over the ssh it already has (rsync: no `.git`, stale files deleted, 0700/0600) to each
machine's `nova_secrets_store`, and running `up` again pushes again. No machine needs read access to the store's
repository. The pull timer below is the optional form, for an adopter who gives `nova_secrets_repo` and lets each
machine read that repository.

The optional pull form: each machine's clone of the secrets store (`nova_secrets_store`) pulled fast-forward only every
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
| ansible-playbook, its `ansible.posix` collection, or (with no `--inventory`) nova-config missing | one `CHECK MISSING <name>: <the pasteable fix line>` per gap on stdout, all at once, then `this control node lacks <n> of what the play needs ...; run: nova-fleet check` |

Otherwise the exit is 0 when ansible-playbook passed and 1 when it did not, with `nova-fleet <play>: FAILED
ansible-playbook exit=<n>` naming its status on stderr (2 never means a failed host here); an ansible-playbook killed
by a signal is `killed by signal <N> (exit 128+N)`. Before running, nova-fleet prints the ansible-playbook, inventory
and play paths it uses on stderr. The extracted directory is removed on every
exit, a refusal included.

## Build and test

```
go build -o nova-fleet ./fleet
go test ./fleet                       # unit: the embedded tree, purposes, arguments, refusals (no ansible)
go test -tags functional ./fleet      # ansible-core on PATH: the plays run through the binary and templar
```

Add a play by adding `<name>.yml` here with a `# purpose:` line at its top; the binary embeds the whole directory.
