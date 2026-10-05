# The coordinator's tools, and the nova verbs that replace them

A stranger sets the sprint up from the docs and the tools, with nothing hidden: nothing the coordinator
needs may be a tool only one coordinator has, and nothing that ships is bash, zsh or Python. This page is
the list of what one coordinator ran on 2026-10-04 that is not a nova verb: one row per wrapper, script and
loop, what it does in one line, either the nova verb lines that do the same or `card: <proposed id>`
with the verb it needs and the PATHS that card would edit, and its status. The page is the register of
the coordinator's stopgaps: no row leaves it, and each one says how it goes away (below, "How a row goes
away"). A verb line here is checked against the tool's own verb table and FlagSet by
`TestEveryCoordinatorToolMapsToARealNovaVerb` (`internal/docs/coordinator_tools_test.go`), so a line that
names a verb or a flag the tool does not carry fails the build; every row's replacement and status are
checked by `TestEveryCoordinatorStopgapNamesTheVerbThatReplacesIt` (`internal/docs/coordtools_test.go`). The runbook is [SPRINT-COORDINATOR.md](SPRINT-COORDINATOR.md); the seat's wrapper for the
unserved verbs is [SPRINT-COORDINATOR-SEAT.md](SPRINT-COORDINATOR-SEAT.md); loop rows are
[FLEET.md](FLEET.md).

No secret, key or password is on this page: secrets are named, never valued, and every `<...>` is filled
from the machine (SPRINT-COORDINATOR.md section 1 reads each value from the sprint server's unit). The
paths are written with placeholders, since a doc names no person, friend or machine: `<seat>` is the
coordinator's name, `<seat-home>` its working directory, `<seat-bin>` the `bin/` there, `<workshop-bin>` the
`bin/` of the coordinator's own tool repository, `<m>` the coordinator machine, `<friend>` a friend.

## How the list was made

On the coordinator machine, 2026-10-04: every `com.nova.*` unit in `~/Library/LaunchAgents` that
`launchctl list` shows loaded, every running process started from `<seat-bin>` or `<workshop-bin>`, every
tool those name in turn, and every tool named by SPRINT-COORDINATOR.md, SPRINT-COORDINATOR-SEAT.md and the
role prompts of the coordinator's tool repository. The `.before`, `.bak`, `.orig`, `.prev` and dated copies
were skipped. Each unit's ProgramArguments were read, and each script below in its header and the lines that
act; the longer ones (the dashboard server, mirror-refresh, nova-loop) not line by line.

## The tools in use

| Tool | What it does | Replaced by | Status |
|---|---|---|---|
| `<seat-bin>/ns.sh` | The coordinator's seat wrapper: runs any `nova-sprint` verb under `nova-secrets exec` with the coordinator Redis user's password, straight against the store; the actor defaults to the seat's name. | The store login as a setting of the tool (landed with seat-store-login-built-in): `nova-sprint seat login` once, then every verb straight against the store with no wrapper and no password in the environment; and every served verb through the server, with no secret: `NOVA_SPRINT_SERVER=127.0.0.1:<port> NOVA_SPRINT_ACTOR=<coordinator> nova-sprint where` (and `inbox`, `card`, `log` and every coordinator verb the same way) | card: adopt-coordinator-verbs |
| `<seat-home>/tmp/buswatch/watch.sh` | The coordinator's wake, run in the background of the coordinator's session: exits with one line on the first of a bus message to the coordinator, a new judgment file, a STOPPED the coordinator did not ask for, a backlog (fleet under half its width, review over 40, merging over 60, ready 0 with work waiting), merges queued with no land pass for 15 minutes, a friend down twice, or ten minutes gone. | card: coordinator-wake-alarms; needs `nova-sprint inbox --wait --alarms`; PATHS: cmd/nova-sprint/inboxwait.go,internal/sprint/idle.go,docs/SPEC-SPRINT.md; covered today: the judgment files by `nova-sprint inbox --wait --push seat`, the idle fleet by `nova-sprint run --idle-alarm`, the bus by `nova-bus recv --as <coordinator> --forever --exec <command>`; the backlog, merge, friend-down and unasked-stop alarms by nothing | card: coordinator-wake-alarms |
| `~/nova-bench/dashboard/server.py` (`com.nova.dashboard.local`, `com.nova.dashboard.public`) | A Python HTTP server that runs `where --json` through a copy of ns.sh (`~/nova-bench/dashboard/bin/ns.sh`) at most once a second and serves the sprint page on port 7390: one unit on the tailnet address, one on loopback reading the first. | `NOVA_SPRINT_SERVER=127.0.0.1:<port> nova-sprint dashboard --listen 127.0.0.1:7390,<tailnet-address>:7390 --logo <file> --every 1s`, one process for both addresses, as a nova-config loop row (FLEET.md) | card: adopt-coordinator-verbs |
| `com.nova.friend-sync` | A zsh loop under `nova-secrets exec` that every 15 s reads the seat from `nova-sprint where --json` with jq and runs `nova-sprint friend sync --root $HOME --actor <seat>`, printing only a change between ok and failing. | `nova-sprint friend sync` as a loop, acting as the seat it reads at each pass (landed with friend-sync-every): `install` with `--every 15s` writes it as this machine's own service, and in a terminal the verb takes `--every 15s` alone. The flag is in `friend sync -h`; the verb table's synopsis line (cmd/nova-sprint/verbs.go) does not list it yet, so this page names the verb without it | card: adopt-coordinator-verbs |
| `com.nova.loop.friend-beat-<friend>` (five units) | Five zsh loops, one per friend, that run `nova-sprint friend beat <friend>` every second while a process matching the friend's harness app is alive; FLEET.md documents the same shell loop. | card: friend-beat-while-harness; needs `nova-friend run --beat-while <process>`; PATHS: cmd/nova-friend/main.go,internal/friend/daemon.go,docs/FLEET.md; covered today: `nova-friend run --as <friend> --harness <h> --dir <d>` beats every second while the daemon runs, whether or not the harness app is open, and one beat is `nova-sprint friend beat <friend>` | card: friend-beat-while-harness |
| `com.nova.loop.friend-ping-<seat>` | A zsh loop that every 10 minutes runs `nova-friend ping --as <seat> --to <friend>` for six friends, appending to `~/Library/Logs/friend-ping-<seat>.log`. | card: wake-ping-every; needs `nova-friend ping --as <coordinator> --to-friends --every <duration>`; PATHS: cmd/nova-friend/main.go,internal/friend/launchd.go,docs/FRIENDS.md,docs/SPEC-FRIEND.md and new files beside them; on the sprint, not landed on 2026-10-05; one ping today: `nova-friend ping --as <coordinator> --to <friend>` | card: wake-ping-every |
| `<seat-bin>/disk-guard` (`com.nova.loop.disk-guard-<m>`) | A bash loop (an interim for an issue of the coordinator's tool repository) that every 60 s writes the data volume's free GiB to a marker file and, under a bench seat's Redis password, to the key `host:<host>:disk_free_g`; `--check` exits 3 under a 200 GiB floor, and under 100 GiB it boots out every `com.nova.loop.*` unit. | `nova-swarm disk-guard --root <dir> --scan <dir> --disk-floor 200` with `--stop-floor 100` as the guard's loop row: it frees disk, warns under 200 GiB and, under 100 GiB, stops every loop unit of the login but the disk-guard ones (`--stop-floor`, this page's card, in `disk-guard -h`; the usage banner's synopsis line in cmd/nova-swarm/main.go does not list it yet, so this page names the verb without it); `nova-swarm member --disk-floor <GiB>` starts no card under the floor. Not carried: the `disk_free_g` key and the marker file, which nothing in nova-tools reads, and `--check`'s exit 3, which the member's own floor does | card: adopt-coordinator-verbs |
| `~/.local/bin/mirror-refresh` (`com.nova.loop.mirror-refresh-<m>`; source `<workshop-bin>/mirror-refresh`) | A bash loop that every 60 s clones or fetches eleven of the organisation's repositories (heads and pull-request refs) into `~/nova-bench/mirror/<repo>.git`, the private ones through per-repository deploy-key aliases with a sealed-token fallback, so a card's `git clone --reference` finds them. | card: mirror-refresh-verb; needs `nova-swarm mirror --dir <dir> --repos <a,b> --url <template> --every <duration>`; PATHS: cmd/nova-swarm/main.go,docs/CLI.md and a new mirror file and its test beside main.go; the verb's dispatch and usage line are in main.go, which this page's card could not edit, and a mirror file with no dispatch is dead code | card: mirror-refresh-verb |
| `~/.local/bin/nova-loop` (installed by the `fleet/loops.yml` of the coordinator's tool repository) | A bash single-instance wrapper: takes `~/nova-bench/run/<name>.lock` holding the loop's own pid, refuses a second copy with exit 3, takes a lock whose holder is dead, writes restart metrics for node_exporter, then execs the command. nova-tools' own unit templates do not use it. | `nova-loop run --name <loop> --run-dir ~/nova-bench/run --metrics <dir> -- <command> [args...]` (this page's card, cmd/nova-loop): the same lock, `<run-dir>/<loop>.lock` holding the pid, a second copy refused at exit 3 while the holder lives, a dead holder's lock taken under an flock so two starts cannot both take it, the start counted in `<dir>/nova_loop_<loop>.prom` (`nova_loop_starts_total`, `nova_loop_last_start_seconds`), then exec. A unit's arguments change from the bash wrapper's to the line above | card: adopt-coordinator-verbs |
| `com.nova.loop.sprint-table-live` | Runs `nova-sprint table --layout live --loop 1` under nova-loop and `nova-secrets exec` to write the live table to a file in the seat's session directory; nova-sprint has no `table` verb, so the unit exits 2 at every start. | The live table as a view of the dashboard (this page's card): `NOVA_SPRINT_SERVER=127.0.0.1:<port> nova-sprint dashboard --listen 127.0.0.1:7390 --every 1s` serves it at `/table`, as `nova-sprint where` prints it, read at most once per `--every`; a session reads it with `curl -s http://127.0.0.1:7390/table` in place of the file. In a terminal: `NOVA_SPRINT_SERVER=127.0.0.1:<port> nova-sprint where --watch --every 1s`. The unit is removed | card: adopt-coordinator-verbs |
| `<seat-bin>/nova-friend` (`com.nova.friend-<seat>`; the daemons of the seat's buds, running without a unit) | A nova-friend binary built into the seat's bin and run as `nova-friend run` for the coordinator's own session and for each bud, with `--state-dir` in the bud's working directory. | the released binary: `nova-friend install --as <me> --harness <h> --dir <d> --server <addr> --width <n>`, which writes the unit that runs `nova-friend run --as <me> --harness <h> --dir <d> --state-dir <d>` | card: adopt-coordinator-verbs |
| `com.nova.friend-<friend>` (five units) | Hand-written units running integration builds of nova-friend under other names (`~/.local/bin/nova-friend-int`, `~/.local/bin/nova-friend-<friend>`) as `run --as <friend> --harness <h>`, three of them pinned to a session id. | the released binary: `nova-friend install --as <friend> --harness <h> --dir <d> --session <id> --server <addr> --width <n>` | card: adopt-coordinator-verbs |
| `com.nova.loop.seat-push-<seat>` | A `zsh -c` line that execs `nova-secrets exec` with the coordinator Redis user's password around `nova-sprint inbox --wait --push seat`, so each new judgment is written once into the seat's inbox directory. | `NOVA_SPRINT_SERVER=127.0.0.1:<port> NOVA_SPRINT_ACTOR=<coordinator> nova-sprint inbox --wait --push seat`, a client of the server with no secret and no shell, as a nova-config loop row (FLEET.md) | card: adopt-coordinator-verbs |
| `com.nova.loop.sprint-server-<m>` | The sprint server, a hand-written unit: `nova-sprint run --listen --land --decide` under `nova-secrets exec` with the coordinator Redis user, the owner's name and the API keys the answer loop and the routes use. | already nova verbs, as a nova-config loop row: `nova-secrets exec --store <store> --as <seat> --key <key file> --sops <sops> --only <NAME,...> --require <NAME> -- env NOVA_SPRINT_REDIS=<host:port> NOVA_SPRINT_REDIS_USER=<user> NOVA_SPRINT_REDIS_PASSWORD_ENV=<NAME> nova-sprint run --listen <address>:<port> --land --decide <dir>` | retired |
| `com.nova.loop.member-<m>` | The coordinator machine's member, a hand-written unit: `nova-swarm member` under `nova-secrets exec` with the bench Redis user and the providers' keys passed to the children. | already nova verbs, as a nova-config loop row: `nova-secrets exec --store <store> --as <seat> --key <key file> --sops <sops> --only <NAME,...> -- env NOVA_SPRINT_REDIS_USER=<user> NOVA_SPRINT_REDIS_PASSWORD_ENV=<NAME> nova-swarm member --as <m> --server <address>:<port> --harness <path> --root <dir> --pass <NAME,...>` | retired |
| `com.nova.loop.disk-guard` | `nova-swarm disk-guard` every 900 s over the member's root, the run directory and the runners' Go caches, a hand-written unit. | already a nova verb, as a nova-config loop row: `nova-swarm disk-guard --root <dir> --scan <dir> --cache <glob>` | retired |

## How a row goes away

A row's Status is one of two, and nothing else:

- `retired`: the coordinator runs the nova verb in the tool's place. The row stays as the record of what
  the verb replaced. A hand-written unit that runs the verb is retired: its form, a nova-config loop row,
  is FLEET.md's.
- `card: <id>`: the card that retires the row. A row whose verb does not exist yet names the card that
  builds it, the same id its Replaced-by cell proposes. A row whose verb has landed names
  `adopt-coordinator-verbs`, proposed on 2026-10-05 and not yet cut: the coordinator's pass that, for each
  such row, replaces the unit or the script with the verb (its loop row, or the service `seat install`,
  `friend sync install` or `nova-friend install` writes), checks the verb does the tool's work, removes
  the tool, and marks the row `retired` here.

`TestEveryCoordinatorStopgapNamesTheVerbThatReplacesIt` fails on a row with no Status or another one, a
row whose Replaced-by cell names no nova verb the tools carry and proposes no card, a retired row whose
replacement is still a proposed card, and a card row whose Status names another card than the one its
replacement proposes. A tool the coordinator starts running gets a row, with its verb or its card, the
day it starts.

## Not in the table, and why

- Not the coordinator's tools: `com.nova.redis` and `com.nova.bus2-redis` run `redis-server` with a config
  file, and `com.nova.node-exporter` runs `node_exporter`; each is a program the fleet installs (FLEET.md).
- Not loaded on 2026-10-04: one friend's `com.nova.friend-<friend>`, `com.nova.loop.wake-serve-<seat>` (its
  `nova-wake` is not installed, and with it the seat's receive-thread script is not run), and every unit file
  ending in `~`.
- Not in use: no loaded unit, no running process, no in-use tool and no coordinator doc or role prompt names
  them. Among them the ones the card that made this page named, and their neighbours: `friend-queue`,
  `friend-row`, `fleet-fn`, `friend-bus-keeper`, `hold-to-fix`, `idle-watch`, `jev-eval`, `jev-loop`,
  `land-lane`, `land-loop-tools`, `land-loop-schema`, `blocked-resolve`, `harvest-priority`, `ok-to-friend`,
  `route-health`, `with-secrets.sh` and each one's `-control` script. A tool here that starts again gets a row.
- The role prompts of the coordinator's tool repository name no coordinator tool: the tools they name (its
  GitHub, voice-gate and retired-list tools) belong to the release, sweep and patrol roles.
- The stopgaps page the card named (STOPGAPS, under docs) is not in nova-tools, nor found by a search of the organisation's code; the stopgaps it
  would list are the rows above.
