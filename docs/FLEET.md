# The fleet plays

A fleet is machines on a tailnet, each a row in nova-config, and the plays
under `fleet/` put every machine in the state those rows say: the build of the
tools in its bin directory, the store's function library and ACL, and one
supervised unit per loop. The plays read nothing but the inventory
`nova-config inventory` prints (the applied state in Redis) and the defaults in
`fleet/group_vars/all.yml`; every change goes through a Go tool (`nova-update
release build` and `install`, `nova-config migrate`, `nova-redis fn` and
`acl`). Every play has a `--check` form that changes nothing and says what a
run would do.

| play | what it converges | on |
|---|---|---|
| `fleet/tools.yml` | the build `nova_version` of the checkout `nova_source` in `~/.local/bin`, the build fact, the retired tools gone, the schema migrated, the function library loaded; the pinned TLC jar on the record machines | every machine; the build on the machine running the play; migrate and `fn load` on `store_deployer`; the jar on `tla` (`--tags tla` runs that alone) |
| `fleet/redis.yml` | the store's ACL users, rendered from the library and the key families | the render on the machine running the play; check and apply on `store_deployer` |
| `fleet/loops.yml` | one launchd or systemd unit per loop record, none for a record that is gone | every machine |
| `fleet/container-runtime.yml` | rootless podman, the fleet's container runtime, and its version recorded in `~/.config/nova/podman` | every bench: Linux (apt on Ubuntu and Debian, dnf on Fedora) and macOS (brew, and a podman machine) |

## Adding a member

`nova-sprint fleet add <host> --width <n>` is the one way to add a member end
to end: one verb, one play (`fleet/member.yml`, which includes `fleet/tools.yml`
and `fleet/loops.yml`), and then the check that the member is there before it is
dealt any work. The play converges the machine: the pinned tools (go, sqlite3,
the pinned harness, age, sops, bats), the nova binaries at the machine's adopted
release, the member and reader loop records **added and applied, then their units
rendered from them in the same run**, the route credential through the
sealed-secrets path (the records name the route's keys and the seat, so
`fleet/loops.yml` wraps each unit in `nova-secrets exec --only <keys>`, and the
member and reader argvs and the probe name `--pass` so the card's child is
handed the route's key; no secret value is printed or passed), and a mirror for
every repository a live card names. The verb adds the fleet and
reader rows itself, the member **drained** (width 0) and with no beat of its
own, and runs the play twice for one add. The setup run
(`nova_member_probe=0`) converges the machine and starts the member and reader
loops, and skips the probe; the verb then reads the store for the member's own
recent beat and its reader row up — the setup's own steps are never taken for the
machine's, so a member that has never beaten, or whose only beat is stale, is left
drained and refused. The loops are loaded asynchronously and never wait for a
beat, so the verb polls the store for up to a minute while they start: a fresh
host whose loop is slower than the play's tail is proved in one run, not two.
Only then does the verb deal the member one
probe card outside its width and run the probe pass (`nova_member_probe=1`),
which takes and finishes it. The member is widened only once it is proved: its
beat is genuine and recent, its reader row is up, and it has taken and finished
the probe card other than by the play's own word — the verb reads the store.
Until then the member is not dealt work, and a refusal takes the probe card off
the table and drains the member again. Every step prints one line, done or the
refusal with its remedy; a second run changes nothing; `--dry-run` runs the play
with `--check`, lists every step, and writes nothing.

```
nova-sprint fleet add bench-a --width 64 --source . --inventory ./nova-inventory
nova-sprint fleet add bench-a --width 64 --source . --inventory ./nova-inventory --dry-run
```

A refusal leaves the member drained (width 0), so nothing is dealt it, and the
same command run again finishes what is still missing or stale. The play's
variables `nova_member`, `nova_member_width` and `nova_member_reader` name the
host, its width and its reader (`reader-<host>`); the rest are defaults in
`fleet/group_vars/all.yml`. The verb fills three of them itself, so the
credential and mirror steps do their work rather than standing in: the route
credential names (`nova_member_route_key`, one `<PROVIDER>_API_KEY` per enabled
route, comma separated) come from the store's routes, and the repository mirrors
(`nova_member_repos`) and the url they are fetched from
(`nova_member_mirror_base`) come from the repository every live work card names
on its `REPO:` line. `--route-key`, `--repos` and `--mirror-base` stand in where
the store names none. The loop
records the play adds are ordinary `nova-config` loop rows: the play applies
them and refreshes the inventory before `fleet/loops.yml` reads it, so the
member's and reader's units exist in the run that adds them.


## An adopter's path

Every command runs from a machine with ssh to every machine, in a nova-tools
checkout, with a `nova-config` built from it on the PATH: an older one prints
no `store_deployer` group and no `nova_loops`. Ansible needs blocking stdio:
run it as `</dev/null 2>&1 | cat`, or it may stop with "Non-blocking file
handles".

```
nova-config machine add bench-a --user nova --seat bench-a --slots 2 --width 2 --as ada
nova-config fleet set --store bench-a --coordinator bench-a --redis_port 6380 --pg_dsn postgres://nova_config@localhost:5432/nova --as ada
nova-config loop add member-bench-a --machine bench-a --argv '["nova-swarm","member","--as","bench-a","--server","bench-a:6390","--harness","opencode","--root","nova-bench/member","--identity","ada,Ada Bench,ada@example.com"]' --keepalive true --as ada
nova-config loop add reader-bench-a --machine bench-a --argv '["nova-swarm","member","--as","reader-bench-a","--server","bench-a:6390","--reader","--harness","opencode","--root","nova-bench/reader","--identity","ada,Ada Bench,ada@example.com"]' --keepalive true --as ada
nova-config route add flash-a --tier flash --provider deepseek --model deepseek-v4-flash --tokens 200000 --deadline 900 --as ada
nova-config route add pro-a --tier pro --provider openrouter --model x-ai/grok-4 --tokens 400000 --deadline 1800 --as ada
nova-config apply --as ada
printf '#!/bin/sh\nexec nova-config inventory "$@"\n' > nova-inventory
chmod +x nova-inventory
```

`NOVA_SPRINT_REDIS_USER` names the user, and `NOVA_SPRINT_REDIS_PASSWORD_ENV` names the
variable that holds the password, never the password. A store with ACLs needs that login
in the wrapper's environment, so the wrapper runs under `nova-secrets exec`, for example
`nova-secrets exec --only NOVA_REDIS_BENCH_PASSWORD --require=NOVA_REDIS_BENCH_PASSWORD
-- env NOVA_SPRINT_REDIS_USER=bench NOVA_SPRINT_REDIS_PASSWORD_ENV=NOVA_REDIS_BENCH_PASSWORD
nova-config inventory "$@"`, where `NOVA_REDIS_BENCH_PASSWORD` is the secret that holds the
password. `nova-update release cycle` runs the wrapper with `--list` before any play and
refuses with the wrapper's own line when it cannot list.

```
ANSIBLE_INVENTORY_UNPARSED_FAILED=true ansible-inventory -i ./nova-inventory --list
ansible-playbook -i ./nova-inventory fleet/tools.yml -e nova_version=v1.2.0-dev.abcdef12 -e nova_source=$PWD -e nova_dogfood_receipts=<dir> --check --diff </dev/null 2>&1 | cat
ansible-playbook -i ./nova-inventory fleet/tools.yml -e nova_version=v1.2.0-dev.abcdef12 -e nova_source=$PWD -e nova_dogfood_receipts=<dir> </dev/null 2>&1 | cat
ansible-playbook -i ./nova-inventory fleet/redis.yml --check --diff </dev/null 2>&1 | cat
ansible-playbook -i ./nova-inventory fleet/redis.yml </dev/null 2>&1 | cat
ansible-playbook -i ./nova-inventory fleet/loops.yml --check --diff </dev/null 2>&1 | cat
ansible-playbook -i ./nova-inventory fleet/loops.yml </dev/null 2>&1 | cat
nova-sprint fleet sync --check --actor ada
nova-sprint fleet sync --actor ada
```

The member and reader loops name the sprint's server, `--server`: the run loop
the coordinator starts with `nova-sprint run --listen bench-a:6390`, the one
writer; a member sends its verbs there and opens no store. The member loop
names no width and no model. Its width is its fleet row's
(`nova-config machine` width, set directly with `nova-config machine set <m>
--width <n>` and made the row by `fleet sync`; nothing else, no friend row and
not the machine's slots, takes part; `nova-sprint fleet
up <m> --width n` changes it live), read every tick; each card's model, budget
and deadline are the route the deal drew for it from the routes `apply` writes
(here one flash and one pro: a pro card runs on `openrouter/x-ai/grok-4`, a
flash card on `deepseek/deepseek-v4-flash`, on the same machine), or the
card's own `model:` line. `nova-sprint routes` shows what each route's attempts
did.

A reader is a loop record the same as a member: `nova-swarm member --reader`
under the readers row of its `--as` name (`nova-sprint init --readers
reader-bench-a`, or `nova-sprint reader add reader-bench-a`). It is named for
its machine, `reader-<m>`, one reader per machine, and it runs at the
machine's width, the fleet row's, read with its queue every tick exactly as
the member on the machine reads its own (the owner, 2026-10-02: "why not just
have as many readers as workers per-machine"): no loop row carries a width,
and an argv that spells `--width` is refused by `loop add` and `loop set`. A
machine of width 0 (no member) reads nothing either. It names no model either: the ask draws each read card's route from the
tier of the card it reads, the tier its work was dealt on (flash when line 1
names none), at that tier's rolling index, and
the packet hands the reader its model, budget and deadline; a reader started
with `--model`, `--tokens` and `--deadline` runs its reads on those instead.
A loop record runs a verb its program must still have: a release that retires
the verb leaves the unit exiting at every start (`sprint-table-live` ran
`nova-sprint table` that way until 2026-10-04). The loop kind asks each nova
program in a record's argv `help <verb>` (`internal/config/loop.go`: exit 2
naming verbs is a verb gone; a program not installed where nova-config runs
judges nothing): `CheckLoopVerb` is the refusal of an enabled record whose
verb is gone (enforced by `loop add` and `loop set`), and `DeadLoops` the line
for each in `status`, with its `nova-config loop remove <name>`.

Both loops name the identity every child commits under, `--identity
<owner>,<name>,<email>`, in their argv, so no file is written into a pool by
hand; a loop without it reads the pool's `identity.tsv`.

The coordinator's seat can run one more loop record: `nova-sprint inbox --wait
--push <dir>`, which writes each new judgment (and each note addressed to the
coordinator) once into the coordinator's own inbox directory, so the coordinator
is woken by a file and polls nothing. It is a client of the server like every
other loop (`NOVA_SPRINT_SERVER` names the loopback address `run --listen`
prints; the unit's environment names the store, which a verb sent to the server
never opens), and the files it wrote are its cursor, so a restart by its
supervisor pushes nothing twice:

```
nova-config loop add inbox-push --machine bench-a --argv '["env","NOVA_SPRINT_SERVER=127.0.0.1:6390","NOVA_SPRINT_ACTOR=<coordinator>","nova-sprint","inbox","--wait","--push","<home>/<coordinator>-working/inbox/sprint-judgments","--timeout","1m"]' --keepalive true --as ada
```

The sprint dashboard ([SPEC-SPRINT-DASHBOARD.md](SPEC-SPRINT-DASHBOARD.md)), a page
that is a second view of `where --json`, is one more loop record on the coordinator's
machine and a client of the server like the others: it reads the sprint at most once
per `--every` and only while a page is open. It listens on loopback, and on this
machine's tailnet address when the page is wanted across the fleet's private network;
an every-network or public address is refused, because the page checks no credential
(a public page is a reverse proxy in front of the loopback listener,
[SPEC-SPRINT-DASHBOARD.md](SPEC-SPRINT-DASHBOARD.md)). `--logo` names the image the page shows; the file stays on the
machine, never in the repository:

```
nova-config loop add sprint-dashboard --machine bench-a --argv '["env","NOVA_SPRINT_SERVER=127.0.0.1:6390","nova-sprint","dashboard","--listen","127.0.0.1:7390,<tailnet-address>:7390","--logo","<home>/sprint-logo.webp"]' --keepalive true --as ada
```

It exits 3 when a new build is installed under it, and its unit starts the new one;
`curl -s 127.0.0.1:7390/healthz` prints `ok`.

The public page is served from files on one fleet machine: Caddy's `file_server`
serves `/var/www/sprint/site` with no reverse proxy, and one puller refreshes those files
from the dashboard server about once a second, so the number of viewers never reaches that
server. Whether the page holds a front-page load (2,000 requests per second for ten minutes
from a distant bench machine with zero errors, the server's own p99 under 50 ms, the dashboard
server seeing only the puller, the far vantage's p99 recorded beside it) is v1.0.0's acceptance
record [acceptance/v1.0.0/public-dashboard-load.md](acceptance/v1.0.0/public-dashboard-load.md),
with the raw numbers and the kernel settings it was measured under.

A friend's working directory, how her jobs arrive and are reported, and how
their clones are removed once done, is docs/FRIENDS.md. The friends are
nova-config's friend rows: `nova-sprint friend sync --actor ada`
copies their names into the sprint's friends table, and each friend says it is
there only by evidence from her own session: a wake ping her session answered
within 10 minutes, or a card of hers finished within 30 (`where` shows `up` on
it, `down` without it, with working 0, and `held` while `nova-sprint friend
down <friend>` holds it; docs/SPEC-FRIEND.md, "Presence is her session's
evidence"). Her daemon's beat, every second (`sprint.FriendBeatEvery`), is
recorded and never makes her up. The same sync
reads each friend's working directory, `<root>/<friend>-working` (`--root
<dir>`, else `HOME`, so it runs on the machine that holds them), and writes her
job cards, which `where` counts as the fleet's columns but load: `ready`,
`working`, `width` (1), `done`, `ok%`, `status`. A job is the inbox/outbox
standard: the coordinator delivers `inbox/<job>/` (with its `BRIEF.md`) and
only the coordinator reaches out; the friend makes `outbox/<job>/` when she
starts and writes `outbox/<job>/REPORT.md` when done, with a `Verdict:` line
(any word but HOLD, FAIL, FAILED or BROKEN is ok; no line is ok). A brief to a
friend says so. The sync reads and never writes a friend's directory; run it
after a job is delivered or collected, or every minute from the coordinator's
loop. No loop beats for a friend: the shell beat loops a wrapper once started
beside her harness are retired with no replacement (docs/FRIENDS.md, "The beat loops
are retired, with no replacement").

The inventory reads the store `NOVA_SPRINT_REDIS` names (or `--redis`); export
it, and `NOVA_MACHINE` when the machine running the play is a row, before the
first play. `ANSIBLE_INVENTORY_UNPARSED_FAILED=true` (or `[inventory]
unparsed_is_failed = True` in `ansible.cfg`) makes a failing inventory fail
the run instead of running the play on no hosts.

A run limited with `--limit` names `localhost` too (`--limit
bench-a,localhost`): the build in `tools.yml` and the render in `redis.yml`
run on the machine running the play, and without it they are skipped with "no
hosts matched".

## The coordinator machine's units, installed by verbs

The plays above install the benches. The coordinator's machine runs nine more units a sprint needs,
and each is written and loaded by a verb of the tool it runs, never by hand (card
every-unit-installed-by-a-verb): a launchd agent on macOS and a systemd user unit on Linux, kept alive
and started again at login, that runs the verb itself by the tool's absolute path, with no
`nova-secrets exec`, shell or single-instance wrapper around it and no secret in it.

| unit | the verb that installs it | what it runs |
|---|---|---|
| the sprint's store | `nova-redis install store` | `nova-redis serve` on 6380, its data in `~/nova-bench/redis/store` |
| the friends' bus | `nova-redis install bus` | `nova-redis serve` on 6381, its data in `~/nova-bench/redis/bus` |
| the sprint's server | `nova-sprint install server --listen <address:port>` | `nova-sprint run --listen` |
| the machine's member | `nova-sprint install member --as <m> --server <address:port>` | `nova-swarm member` |
| the seat's push loop | `nova-sprint install seat-push` | `nova-sprint inbox --wait --push seat` |
| the friend sync loop | `nova-sprint install friend-sync --every 15s` | `nova-sprint friend sync --every` |
| the live table | `nova-sprint install table --out <file>` | `nova-sprint where --watch` |
| the disk guard | `nova-swarm install disk-guard` | `nova-swarm disk-guard`, one pass every 15 minutes |
| the mirrors' refresh | `nova-swarm install mirror-refresh` (owed) | `nova-swarm mirror`, one pass every minute |

serve writes redis-server's configuration from its flags (binding, port, store directory under the
bench root, persistence) and reads its password in its own process from the secret its unit names;
the server, the push loop and the table open the store with the seat login (`nova-sprint seat login`). Two secrets are not read that way yet: the server's decision loop (`run --decide`) reads its
API key from its environment, and the member hands its children the providers' keys `--pass` names
from its environment; a unit carries neither, so until those verbs read a login as the store's does,
the service's environment has to give them. `nova-sprint units --check` names each of the nine installed, missing or different, so a
machine a stranger set up is checked against what a sprint needs; a unit written by hand around a
wrapper reads as different. `nova-swarm install disk-guard` writes that unit in the swarm
binary, and the unit runs `nova-swarm disk-guard` itself. The unit text the sprint and redis
verbs share lives in `internal/units`, which a worker's binary may import. `nova-swarm install mirror-refresh` stays owed: the mirror verb `nova-swarm mirror` is written, its
unit is not, and `units --check` says the unit is owed rather than telling a stranger to run it. Until
that unit is written, a mirror refresh is not installed from here. The play's disk-guard row above is the
fleet's copy of the same pass.

## A fixture inventory

`nova-config inventory --fixture <file>` reads the rows from a YAML or JSON
file instead of the store: machines (with `os` and `arch` standing in for the
beat), the fleet row and loops. A file with no `loops` key is a fleet whose
loops were never applied. `fleet/testdata/inventory-fixture.yml` is two
machines and their loops; `fleet/testdata/check-fixture.yml` is the machine
running the play, named `localhost`, with no store deployer, which is what
`TestFleetPlaysPassSyntaxAndCheckOnTheFixture` runs all three plays against
with `--check`.

## Variables

The inventory gives each host `ansible_user`, `nova_seat`, `slots`,
`runners`, `nova_tla` (its row's `tla`; the `tla` group is the hosts where it
is true), `nova_redis_port`, `nova_redis_addr`, the explicit `nova_pg_dsn`,
`nova_os` and `nova_arch` (from its beat, when it has one) and `nova_loops`,
and `all.vars` `nova_store`. The rest are defaults in
`fleet/group_vars/all.yml`. The inventory's host variables override them, and
so do a `group_vars/benches.yml` and `host_vars/<machine>.yml` beside the
inventory; a `group_vars/all.yml` beside the inventory does not (the play's own
is read after it). The host variables ensure an applied nonstandard Redis port
outranks every group default. The Postgres URI is preserved exactly, including
an explicit localhost; it is never derived from the Redis store machine.

| variable | default | what it is |
|---|---|---|
| `nova_home` | the login's `$HOME` | where the bin directory, the release copies, the logs and the secrets layout live |
| `nova_bin_dir` | `~/.local/bin` | where the tools are installed and where a loop's bare program is found |
| `nova_sops`, `nova_sops_candidates` | the first of `~/.local/bin/sops`, `/opt/homebrew/bin/sops`, `/usr/local/bin/sops` that exists | the sops a seat is opened with; set `nova_sops` in `host_vars` for another |
| `nova_secrets_store`, `nova_secrets_key_dir` | `~/nova-bench/secrets`, `~/.config/nova-secrets` | the nova-secrets store and the directory of `<seat>.key` |
| `nova_launchd_domain` | `auto` | darwin: `gui` (a LaunchAgent in the login's GUI domain), `system` (a LaunchDaemon dropped to the login; sudo), or `auto`, which asks launchd whether the login has a GUI domain and takes `system` when it has not |
| `nova_systemd_scope` | `user` | linux: a user unit (with linger) or `system` |
| `nova_retire_units` | `[]` | units of another tool's to retire by name (below) |
| `nova_disk_guard`, `nova_disk_guard_every`, `nova_disk_guard_args` | `true`, `900`, `--scan ~/nova-bench/run --cache ~/runner-*/_cache/go-build` | the disk guard `loops.yml` adds to every machine (below): on or off, its period in seconds, and its arguments after the `--root` of each record |
| `nova_redis_port`, `nova_redis_addr` | the explicit applied fleet row (no port default) | the store |
| `nova_redis_deploy_user`, `nova_redis_deploy_password_key` | `coordinator`, `NOVA_REDIS_COORDINATOR_PASSWORD` | who loads the library, and the secret holding its password in the `store_deployer` seat |
| `nova_redis_admin_user`, `nova_redis_admin_password_key` | `admin`, `NOVA_REDIS_ADMIN_PASSWORD` | who writes the ACL, and its secret |
| `nova_redis_user_password_keys` | `coordinator`, `bench` | the secret holding the password of a user `acl apply` creates |
| `nova_pg_dsn`, `nova_pg_password_key` | the applied fleet row (no inferred DSN), `NOVA_PG_CONFIG_PASSWORD` | the configuration store `tools.yml` migrates; an empty DSN is refused with the set and apply commands |
| `nova_release_out`, `nova_release_gocache` | `~/nova-bench/release-build`, `~/nova-bench/release-gocache` on the machine running the play | where the build is written and its Go cache |
| `nova_version`, `nova_source` | none: `-e` | the build to install and the checkout it is built from |
| `nova_dogfood_receipts` | none: `-e` | the dogfood receipts directory the build's definition-of-done gate reads |
| `nova_tla_dir`, `nova_tla_jar` | `/opt/tla`, `/opt/tla/tla2tools.jar` | where a record machine holds the TLC jar |
| `nova_tla_sha256_file` | `tla/tla2tools.sha256` of the checkout the play runs from | the SHA-256 the jar must have, read on the machine running the play |
| `nova_tla_java_candidates` | `~/sdk/bin/java`, `/usr/bin/java`, `/usr/local/bin/java` | the java a record machine runs TLC with: the first that exists |
| `nova_release_gate_args` | `--cli <nova_source>/docs/CLI.md --receipts <nova_dogfood_receipts>` | the build's gate flags; a waiver replaces them whole with `--no-dogfood-gate --reason <why>`, and then no receipts are named |
| `nova_release_build_args` | `[]` | flags appended to the build after the gate's; `nova-update release cycle` passes `--incremental --gate report --reason <why>` |

An existing schema has neither endpoint before migration 0014 adds their
columns. Bootstrap once with `nova-config migrate --pg
postgres://user@localhost:5432/db`, then run `nova-config fleet set --redis_port
<port> --pg_dsn postgres://user@localhost:5432/db --as <actor>` and
`nova-config apply --kind fleet --as <actor>`. Both endpoints stay unset after
migration until declared. Inventory and fleet apply refuse an unset endpoint
before any member argv is rewritten; inventory never guesses a Redis port.

Every secret is named, never valued: a task that needs one runs its tool under
`nova-secrets exec --only <NAME> --require=<NAME>` as the host's seat, so the
password reaches the tool's environment and no file, line or log. That the
`store_deployer` seat holds the admin and deploy secrets is proven by the
`--check` run of `redis.yml` and `tools.yml`, which log in with them.

## tools.yml

1. Every machine's platform: its beat's `nova_os`-`nova_arch`, else the gathered facts.
2. On the machine running the play, one `nice -n 19 nova-update release build` (`GOMAXPROCS=4`, its own Go cache) for every platform that has no `SHA256SUMS` under `nova_release_out/<version>/` yet; a platform already built is not built again. `--check` prints `WOULD-BUILD ... built=<platforms> missing=<platforms>`. After a successful build it removes the old version directories in `nova_release_out`: it keeps the version just built, the version of the `nova-update` running it, and the 3 newest of the rest (`pruned=<n>` on its `RELEASE BUILD OK` line). The Go cache (`nova_release_gocache`) is the build's own `GOCACHE`, trimmed by Go itself of entries unused for five days, and is not pruned here.
3. On every machine: the platform's directory copied to `~/nova-bench/release/<version>/<platform>/`: when the machine has the installed build's directory and this one is not begun, it is seeded from that directory on the machine (unverified); then one listing measures the sha256 of every file in the stage and only the files whose bytes differ from the release's `SHA256SUMS` are sent, so a missing, rebuilt or corrupt file is sent and an intact reused one is not (`tla/BenchStage.tla`). Then that release's own `nova-update release install`, which verifies the `SHA256SUMS` whole and skips a tool that already answers the version or already holds its bytes, then, last, removes the old version directories under `~/nova-bench/release/`: it keeps the version installed, every version the bin directory's binaries answered before it (so a bad build can be put back), and the 3 newest of the rest; only names that parse as a version are touched, and a removal that fails is counted (`prune-failed=<n>` on the `RELEASE INSTALLED` line) and never fails the install; the tools named in `fleet/retired-tools.txt` (tools nova-tools once shipped and ships no more, by exact name) are removed from the bin directory, and nothing else is: a binary the list does not name (a credential helper, a loop wrapper of the fleet's own, a `.prev` copy) is never touched, whatever its name; the build fact (`~/.config/nova/build`) holds the version. `--check` prints `TOOLS host=<m> ... UP-TO-DATE` or `WOULD-INSTALL` from the installed `nova-update version` or the build fact, and `WOULD-REMOVE <path>` for each retired tool present.
4. On `store_deployer`: `nova-config migrate` (the schema a kind the build adds needs: run before any `nova-config loop add`), then `nova-redis fn load` as the deploy user; `--check` runs `nova-redis fn check` instead.
5. On `tla`, the record machines (rows with `tla=true`; tagged `tla`, so `--tags tla` runs this step alone): the machine is Linux; `nova_tla_dir` exists and is the login's, and the jar at `nova_tla_jar` is the login's (sudo only when either is not); the jar's SHA-256 is the first word of `nova_tla_sha256_file`; java (the first of `nova_tla_java_candidates`) runs. It downloads and copies nothing: a jar that is missing or differs refuses the host with `TLA REFUSED host=<m> jar=<path> want=<sha> got=<sha|none>` and the `scp` that places it; a host that passes prints `TLA OK host=<m> jar=<path> sha256=<sha> java=<path> version=<v>`, and `--check` says the same. `tlacheck run --bench` runs the records there (tla/README.md, "The record machines").

A machinery install during a sprint is one command from the coordinator,
`nova-update release cycle` (docs/CLI.md, SPEC-RELEASE rule 13): this play
with `--check`, then for real, limited to the benches named and `localhost`,
the build `--incremental --gate report --reason <why>`, one `CYCLE BENCH`
line per machine.

The role that runs migrate must own every table in schema config. The play
runs it as `nova_pg_owner` (group_vars: `nova_config`, the role that owns the
schema whole; `nova_pg_owner_dsn` is `nova_pg_dsn` with that user, its password
under `nova_pg_password_key`), never as the seat's own role: on 2026-10-08 a
migrate as `nova_admin` was refused and the store stayed behind the build for
7h45m. On the coordinator the migration runs inside the seat play's window
(`nova-config migrate --window`, the old server and member stopped and seen
gone), after a read-only preflight as the owner before the window: the
candidate's `nova-config migrate --dry-run --json` must answer as the owner,
say `owner=<nova_pg_owner>` (or `none`, a store before its first migration)
and `ready=yes`, or the play refuses before anything is stopped, naming each
table another role owns with its owner, and the refusal ends in `run:` and
one `ALTER TABLE config."<table>" OWNER TO "<role>";` per table for a person
to run in psql; no migration is run by hand before the window. Run those once,
in psql, as a role with the owners' rights (the owner or a superuser), then
run the play again. `nova-config migrate --dry-run` prints the same finding,
applies nothing, and exits 1 when migrate would refuse (`ready=no`), 0 when
it would apply.

## redis.yml

`nova-redis acl render` on the machine running the play prints the users this
build renders (one per role: `coordinator`, the member's `bench`, the table
reader's `ns-table`, the friend's `ns-friend`) with the key families and the
code that names each. On `store_deployer`, `nova-redis acl check` compares the
store's live ACL with them (`ACL OK`, `ACL DRIFT` with what would change, `ACL
MISSING`; users no role renders are named with `NOTE ACL EXTRA` and left as
they are, and the default user's state is named); `--check` stops there. A run
then `nova-redis acl apply`s the users that differ. A user keeps the password
it has; a user the store lacks is created only with the password in the seat's
secret `nova_redis_user_password_keys` names, and the run refuses when it
names none. `docs/CLI.md` ("The store's ACL") has the verbs' lines.

## Containers are podman

The fleet's container runtime is podman, rootless and daemonless, on every
bench; `tools/functionalrun` runs the functional tier in it and takes podman
when it is on `PATH`, docker only when podman is not (it names the one it used
on stderr: `functionalrun: container runtime: podman (<path>)`). Docker is
never installed by a play. `fleet/container-runtime.yml` is the mechanism
(`fleet/roles/container-runtime/README.md`):

- Linux: the distribution's `podman`, `uidmap` (`shadow-utils` on Fedora) and
  `slirp4netns`, with the other rootless helpers, through apt or dnf; a
  `/etc/subuid` and `/etc/subgid` row, linger and cgroup v2 delegation for the
  bench's login; then a probe container proves the limits a run relies on.
- macOS: `brew install podman` and `podman machine init --now`, sized from the
  row's `slots` (two CPUs and 4 GiB per slot, at least 2 and 4 GiB); an
  existing machine is started, never resized. A bench with no brew stops the
  play with that said: the play does not install brew.
- Every bench: `podman --version` is written to `~/.config/nova/podman` and
  printed as `PODMAN host=<m> podman version <v> recorded=<path>`.

A hand-installed podman is a stopgap: run the play, which finds the packages
present and changes nothing but what is missing. `--check --diff` after a real
run reads no change.

```
ansible-playbook -i ./nova-inventory fleet/container-runtime.yml --check --diff </dev/null 2>&1 | cat
ansible-playbook -i ./nova-inventory fleet/container-runtime.yml </dev/null 2>&1 | cat
```

The inventory's group is `functional_runners` (`fleet/inventory.container-runtime.example.ini`).

## loops.yml

One unit per record of `nova_loops`, from the record's fields and the host's
layout: the command is the record's `argv`, word for word (a bare program is the installed
tool, `~/` the login's home) behind `nova-secrets exec --as <seat> --only
<keys> --require=<key>...` when the record names keys; its output goes to the
record's log under the fleet row's `loops_dir` (migration 0033 seeds it to
`~/nova-bench/loops`), which the play creates (on darwin, launchd agents log under the user's home,
`~/Library/Logs/nova-loop-<name>.log`, because launchd cannot open log files on
network volumes such as `/Volumes/nova`). Every unit
gets `NOVA_SPRINT_REDIS=<store>:<redis_port>` from the applied fleet row. For
a `nova-swarm member`, inventory removes an older endpoint assignment from the
rendered `/usr/bin/env` prefix while preserving its Redis user, password
variable name and every other word. This compatibility projection does not
rewrite Postgres: remove that old assignment from the loop row with
`nova-config loop set <loop> --argv '<argv>' --as <actor>`, then apply the loop
kind. The endpoint remains effective from the unit environment during that
cleanup.

A sprint unit reads the API keys it needs in its own process. `nova-sprint run --keys <NAME,...>`
(recorded as `keys.json` beside the seat login) names the decision key and each provider key the
run loop reads; `nova-swarm member --pass <NAME,...>` names the keys a child may be handed, read
from the seat (`NOVA_SEAT`, or the file `NOVA_SWARM_KEYS` names) when the environment does not
already hold them. The child is handed the decision key, when pass names it, and the one provider
key its route needs, never the whole set. A named secret that cannot be read refuses at start,
naming the name and the remedy. The unit's environment does not carry the values, so those loops
need no `nova-secrets exec` wrapper for `JEV_API_KEY` or a provider key.

- darwin: `com.nova.loop.<name>.plist` (`templates/nova-loop.plist.j2`) in
  `~/Library/LaunchAgents` (GUI domain) or `/Library/LaunchDaemons` (system,
  `UserName` the login), with `StandardOutPath` and `StandardErrorPath` logging
  to `~/Library/Logs/nova-loop-<name>.log` under the user's home. A kept-alive
  record has `KeepAlive`; a periodic one
  `StartInterval`. A record with `enabled: false` is written with `Disabled`
  and not loaded. A changed unit is booted out and bootstrapped again.
- linux: `nova-loop-<name>.service` (`templates/nova-loop.service.j2`) in
  `~/.config/systemd/user` (user scope, linger on) or `/etc/systemd/system`. A
  kept-alive record is that service, restarted whenever it ends; a periodic
  one is a oneshot service started by `nova-loop-<name>.timer`
  (`templates/nova-loop.timer.j2`, `OnUnitActiveSec` the record's `every`). A
  record with `enabled: false` is written, stopped and disabled.

A unit's command can be wrapped in `nova-config loop run <name> -- <command>`, the
single-instance lock a bash `nova-loop` wrapper once gave (docs/SPEC-CONFIG.md, "loop run"): a
second copy exits 3, and `--metrics <dir>` writes the restart count node_exporter reads. The plays
do not wrap the units in it yet.

Every unit the play writes carries its mark (`written by fleet/loops.yml from
the loop record <name>`). A marked unit that no record on the machine names,
and every marked unit in the other place (the system place when the machine's
is the login's own, and the other way round), is retired: unloaded and its
file removed, then systemd reloaded. A unit of the same shape that another
tool wrote is left as it is and named in a `NOTE`, unless a record of its name
takes it over (the record's unit replaces it) or `nova_retire_units` names it.
`--check` prints `WOULD-RETIRE <name> on <machine> (<file>)` for each and
changes nothing; every run ends with `LOOPS host=<m> place=<dir> records=<n>
enabled=<n> written=<n> retired=<n>`.

`-e nova_loop_only=<name>,<name>` (or a JSON list) limits a run to those
records (nova-tools#5096 item 24): only their units are rendered, restarted
and counted (`records=` of the receipt, which ends `only=<names>`); every other
unit on the machine is left as it is and nothing is retired, so a pass over the
readers alone touches no member. A name that no record of the run's machines
carries is refused before anything is written.

A unit whose file (or timer) changed is restarted, and the run says so first:
`RESTART <name> on <machine>: its unit file changed; <why>` (`WOULD-RESTART`
under `--check`, which restarts nothing). A member's unit (a record whose argv
runs `nova-swarm member`, a reader's too) is never killed mid-card
(nova-tools#5096 items 25, 26): its unit signals the member alone (systemd
`KillMode=mixed`; launchd signals the job's process and abandons its group)
and waits `nova_member_stop_timeout` (180 s, a minute above the member's
longest drain, which is bounded at two minutes: a member that drains does no
new work, so the restart gives the machine back and the cards still running are
redealt) before it kills anything, and the member drains on that SIGTERM:
it takes no new card, lets its running cards finish and reports them, then
exits (at once when it runs none). So the restart is the drain: the play waits
for it (on darwin until launchd no longer holds the member) and then starts the
new unit. A unit written before the drain settings was started without them: on
linux the daemon reload before the restart gives the stop its new settings; on
darwin the old plist's bootout gives the member launchd's default 20 s, after
which the member is killed and its children, abandoned with its group, are
adopted by the new member while they live.

### The disk guard, on every machine

Beside the records, the play adds one periodic row to every machine,
`disk-guard`: `nova-swarm disk-guard` every `nova_disk_guard_every` seconds
(900), its `--root` each root a record's argv names, then
`nova_disk_guard_args`, logging to `~/nova-bench/loops/disk-guard.log` (on darwin,
launchd logs to `~/Library/Logs/nova-loop-disk-guard.log`). A record
named `disk-guard` on the machine takes its place; `nova_disk_guard: false` in
`host_vars` leaves it out (and retires the unit). Owner's rule, 2026-10-02: "We
must not fill discs again", "cleanup must be auto!". Every per-card or
per-machine artifact the fleet writes, and what removes it, when:

| artifact | what removes it, and when |
|---|---|
| a launch's checkout, `<root>/slots/<launch>/` | the member, while its loop runs: at once when the launch is reported ok, a failed one kept (the newest 5 of the pool); swept at the member's start. A pool whose loop stopped (no process names its root or works in it, nothing moved for 30 minutes): the disk guard's next run, by the same rule; a work launch whose checkout holds commits past its staged one is kept and said on a `KEPT slot` line, every run, until a person removes it |
| a launch's small files and results (`.native.log`, `.card.md`, `.frame.json`, `results/<launch>`) | the member's cleaner, once the sprint's epoch is two past theirs (docs/SPEC-SWARM.md, `member`) |
| a root's Go build cache, `<root>/cache/go-build` | the member's cleaner, held under `--gocache-limit` (20 GiB) while it runs; the disk guard every run, under `--cache-max-gb` (20), whether or not a loop runs |
| the login's Go build cache (`$GOCACHE`, else the user cache directory's `go-build`) and every `--cache` (the CI runners' `_cache/go-build`) | the disk guard every run, under `--cache-max-gb`: entries used longest ago first, never one used in the last two hours, down to the cap less a fifth |
| a module cache (`<root>/cache/go-mod`, the login's `$GOMODCACHE` or `~/go/pkg/mod`) | the disk guard, emptied when over `--modcache-max-gb` (50), no `go` command runs and no process holds a file in it |
| a loop log, `~/nova-bench/loops/*.log` (on darwin, `~/Library/Logs/nova-loop-<name>.log`, which the disk guard does not rotate unless `--logs` points to `~/Library/Logs`) | the disk guard, over `--log-max-mb` (50): copied to `<log>.1` and emptied in place, the copies shifted, the one past `--log-keep` (3) removed |
| a land clone, `<user cache dir>/nova-sprint/land/<repo>-<hash>` (`~/Library/Caches` on darwin, `~/.cache` on linux; never `/tmp`) | the disk guard, unused for `--clone-age` (24h), with no uncommitted work and no process naming it or working in it; land clones it again on its next use |
| a mirror's temporary packs, `~/nova-bench/mirror/<repo>/objects/pack/tmp_pack_*` and `.tmp-*` (an aborted fetch's) | the disk guard, older than an hour, when no process names the mirror or works in it and no git fetch naming no path runs; never `git prune` |
| release copies, `nova_release_out` | `tools.yml`, after a build: all but the built, the running and the 3 newest |

A process works in a path when its working directory or a file it holds open
lies under it (lsof on darwin, `/proc/<pid>/cwd` and `/proc/<pid>/fd` on Linux):
a `git push` or a `make` run inside a land clone names no path on its argument
line, and keeps the clone all the same. A launch agent's PATH leaves out
`/usr/sbin`, where macOS keeps lsof, so the guard takes lsof from PATH, else
from `/usr/sbin/lsof`, `/usr/bin/lsof`, `/sbin/lsof` or `/bin/lsof`, and says
once at its start, on a `NOTE` line, which it took off PATH or that it found
none. A run that cannot read the open files removes nothing that needs them
and ends `INCOMPLETE`.

Each run prints one line per action (`REMOVED`, `TRIMMED`, `CLEANED`,
`ROTATED`, with `freed=<bytes>`, or `KEPT` with why), a `DISK-GUARD WARN` line
for a volume under `--disk-floor` (10 GiB, the member's own: a member there
starts no card), and ends `DISK-GUARD OK freed=<bytes> free=<bytes>`, or
`DISK-GUARD INCOMPLETE ... failed=<n>` and exit 1 when something could not be
read or removed. Nothing under `/tmp` is the guard's: a clone a person or a
child made there by hand is theirs to remove.

The inventory carries `nova_loops` only once the loop kind has been applied to
the store; until then `loops.yml` refuses each machine by name instead of
reading the absence as "no loops" and retiring every unit.
