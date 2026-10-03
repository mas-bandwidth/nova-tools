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

A friend's working directory, how her jobs arrive and are reported, and how
their clones are removed once done, is docs/FRIENDS.md. The friends are
nova-config's friend rows: `nova-sprint friend sync --actor ada`
copies their names into the sprint's friends table, and each friend says it is
there by beating from its own machinery, beside its harness, every few seconds
(the same window and misses as a member's beat; `where` shows it `up`, `down`,
or `held` while `nova-sprint friend down <friend>` holds it). The same sync
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
loop. On any harness the wrapper that starts the friend adds one line before
it, with `NOVA_SPRINT_SERVER` (the run loop's loopback address) or
`NOVA_SPRINT_REDIS` set for the friend:

```
while :; do nova-sprint friend beat friend-a >/dev/null 2>&1; sleep 5; done &
trap 'kill $!' EXIT
```

so the beat stops when the friend's harness does, and the friend is down three
windows later.

The inventory reads the store `NOVA_SPRINT_REDIS` names (or `--redis`); export
it, and `NOVA_MACHINE` when the machine running the play is a row, before the
first play. `ANSIBLE_INVENTORY_UNPARSED_FAILED=true` (or `[inventory]
unparsed_is_failed = True` in `ansible.cfg`) makes a failing inventory fail
the run instead of running the play on no hosts.

A run limited with `--limit` names `localhost` too (`--limit
bench-a,localhost`): the build in `tools.yml` and the render in `redis.yml`
run on the machine running the play, and without it they are skipped with "no
hosts matched".

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
runs it as the role `nova_pg_dsn` names (the config role), so a schema whose
tables another role made (an admin role at setup) is refused before any
migration is applied, and the refusal is the play's failure line: it names
the role, each table it does not own with its owner, and ends in `run:` and
one `ALTER TABLE config."<table>" OWNER TO "<role>";` per table. Run those once,
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

## loops.yml

One unit per record of `nova_loops`, from the record's fields and the host's
layout: the command is the record's `argv`, word for word (a bare program is the installed
tool, `~/` the login's home) behind `nova-secrets exec --as <seat> --only
<keys> --require=<key>...` when the record names keys; its output goes to the
record's log under `~/nova-bench/loops/`, which the play creates. Every unit
gets `NOVA_SPRINT_REDIS=<store>:<redis_port>` from the applied fleet row. For
a `nova-swarm member`, inventory removes an older endpoint assignment from the
rendered `/usr/bin/env` prefix while preserving its Redis user, password
variable name and every other word. This compatibility projection does not
rewrite Postgres: remove that old assignment from the loop row with
`nova-config loop set <loop> --argv '<argv>' --as <actor>`, then apply the loop
kind. The endpoint remains effective from the unit environment during that
cleanup.

- darwin: `com.nova.loop.<name>.plist` (`templates/nova-loop.plist.j2`) in
  `~/Library/LaunchAgents` (GUI domain) or `/Library/LaunchDaemons` (system,
  `UserName` the login). A kept-alive record has `KeepAlive`; a periodic one
  `StartInterval`. A record with `enabled: false` is written with `Disabled`
  and not loaded. A changed unit is booted out and bootstrapped again.
- linux: `nova-loop-<name>.service` (`templates/nova-loop.service.j2`) in
  `~/.config/systemd/user` (user scope, linger on) or `/etc/systemd/system`. A
  kept-alive record is that service, restarted whenever it ends; a periodic
  one is a oneshot service started by `nova-loop-<name>.timer`
  (`templates/nova-loop.timer.j2`, `OnUnitActiveSec` the record's `every`). A
  record with `enabled: false` is written, stopped and disabled.

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
and waits `nova_member_stop_timeout` (7260 s, a minute above the member's
longest drain) before it kills anything, and the member drains on that SIGTERM:
it takes no new card, lets its running cards finish and reports them, then
exits (at once when it runs none). So the restart is the drain: the play waits
for it (on darwin until launchd no longer holds the member) and then starts the
new unit. A unit written before the drain settings was started without them: on
linux the daemon reload before the restart gives the stop its new settings; on
darwin the old plist's bootout gives the member launchd's default 20 s, after
which the member is killed and its children, abandoned with its group, are
adopted by the new member while they live.

The inventory carries `nova_loops` only once the loop kind has been applied to
the store; until then `loops.yml` refuses each machine by name instead of
reading the absence as "no loops" and retiring every unit.
