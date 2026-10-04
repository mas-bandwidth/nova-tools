Verdict: FAIL

PASS (verbatim): no judgment older than 15 minutes, the idle alarm fires and clears correctly, friend ready queues stay at 2x width.

Window start (UTC): 2026-10-04T23:21:41Z
Window end (UTC): 2026-10-04T23:23:42Z

Build measured: the installed nova-sprint (`/home/ubuntu/.local/bin/nova-sprint`) and nova-bus could not be
executed in this sandbox — every invocation returned "Permission denied (os error 13)" — so the
prescribed client invocation (`NOVA_SPRINT_SERVER=127.0.0.1:6390`) could not run at all. As a
corroborating probe only, this checkout's own nova-sprint at commit
bf078ca7d54a07958e51bdb39b9e693fc1010a66 (library build `11dc308260975247`) was compiled into
`scratch/nova-sprint` (outside the repository, not committed) and run in store mode against the
Redis at 127.0.0.1:6379; it was refused because the store holds nova_sprint library `90266b4d1604bed6`
(a different build — the one the installed binary was built from), i.e. the store was written by the
installed nova-sprint, not by this checkout's build. The dashboard behind http://127.0.0.1:7390
is a Caddy gateway that returns HTTP 200 with an empty body for every path
(`/`, `/api`, `/api/sprint`, `/healthz`, `/ready` all `200 size=0`).

Harness-level report status: HOLD — the measurement could not be run. No live nova-sprint system
(server, friends, judgments, idle alarm) was reachable from this sandbox; the card directs that a
window where the measurement could not be run is reported HOLD rather than stretched to a pass.

Every command run (one-shot probes done before the sampling loop, then repeated as the per-sample
command set below):

1.  `nova-sprint --version` (installed) -> `/home/ubuntu/.local/bin/nova-sprint: Permission denied`
2.  `nova-sprint help` -> same permission denied
3.  `NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan timeout 300 nova-sprint where --json --cards` -> `timeout: failed to execute process: Permission denied (os error 13)`
4.  `NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan timeout 300 nova-sprint stats --json` -> same permission denied
5.  `NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan timeout 300 nova-sprint log --json --since 2026-10-04T23:21:41Z` -> same permission denied
6.  `curl -s http://127.0.0.1:7390/api/sprint` -> `HTTP 200 size=0` (Caddy, empty body; `/`, `/api`, `/api/sprint`, `/healthz`, `/ready` all `200 size=0`)
7.  `nova-bus log --max 0` (installed) -> `Permission denied (os error 13)`
8.  `nova-bus2 log --max 0` -> `No such file or directory` (the `nova-bus2` rename has not reached this install)
9.  `timeout 5 bash -c 'cat < /dev/null > /dev/tcp/127.0.0.1/6390'` -> `Connection refused` (nothing listening on 6390)
10. `redis-cli -h 127.0.0.1 -p 6379 --scan` -> 16 keys total, all under prefix `quack` plus `ci:nova-tools:...` / `pr:nova-tools:4241` CI state; NO keys for judgments, friends, the idle alarm, or fleet members
11. `redis-cli -h 127.0.0.1 -p 6379 smembers sprint:quack:cards` -> `task:quack-007` (one card)
12. `redis-cli -h 127.0.0.1 -p 6379 hgetall task:quack-007` -> stream=quack, state=merging, where=merging, gates=ci:green,base:behind,scope:ok; a single test fixture card, no judgments
13. `ps aux | grep -iE 'nova-sprint|nova-bus'` / `ss -tlnp` -> no nova-sprint run/listen process; 127.0.0.1:6390 not listening
14. `NOVA_SPRINT_REDIS=127.0.0.1:6379 NOVA_SPRINT_ACTOR=rowan timeout 300 <repo-build> nova-sprint where --json` -> `REFUSED: the store at 127.0.0.1:6379 holds nova_sprint library 90266b4d1604bed6, and this build is 11dc308260975247; run: nova-redis fn load --addr 127.0.0.1:6379`

Per sample (the prescribed command set run once per minute; every sample failed to read the sprint):
- `NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan timeout 300 nova-sprint where --json --cards`
- `NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan timeout 300 nova-sprint stats --json`
- `curl -s http://127.0.0.1:7390/api/sprint`
- `timeout 5 bash -c 'cat < /dev/null > /dev/tcp/127.0.0.1/6390'` (server liveness)
- `NOVA_SPRINT_REDIS=127.0.0.1:6379 NOVA_SPRINT_ACTOR=rowan timeout 300 <repo-build> nova-sprint where --json` (store-mode corroboration)

Per criterion (measured value against the bar; raw counts: samples taken = 3, samples passing = 0):

1) Judgments — no open judgment older than 15 minutes at any sample.
   Measured value: not obtained. Every sample failed to read judgments (installed nova-sprint is
   non-executable here; store-mode was refused on library-version mismatch; no server on 6390).
   Worst value: unmeasurable — no oldest-open-judgment age was returned at any sample.
   Bar: PASS requires every sample to yield an oldest-open-judgment age under 15 minutes.
   Decision: FAIL — 0 of 3 samples read a judgment age.

2) Idle alarm — raises only when the named condition is true and clears only after it ends;
   at least one raise and clear observed; else HOLD, do not provoke one.
   Measured value: no idle-alarm raise or clear was observable in the window.
   Raw counts: samples taken = 3, samples with an observable alarm event = 0, raises observed = 0,
   clears observed = 0.
   Decision: HOLD per clause — no raise/clear occurred (none could without a running server/store),
   so the alarm was not provoked; the "report HOLD rather than provoke one" branch applies.

3) Friend ready queues — each friend not held holds ready at 2x its width at every sample
   (except within 2 minutes after a deal).
   Measured value: not obtained. The friends table could not be read at any sample
   (server refused / store version-mismatched).
   Worst value: unmeasurable — no friend ready count or width was returned.
   Bar: PASS requires each non-held friend to hold ready at 2x width at every sample.
   Decision: FAIL — 0 of 3 samples read a friends table.

Raw sample lines that decide each criterion (all failing; 0 passing):

--- SAMPLE 1 | 2026-10-04T23:21:41Z ---
[installed nova-sprint where --json --cards] NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan timeout 300 nova-sprint where --json --cards:
timeout: failed to execute process: Permission denied (os error 13)
[installed nova-sprint stats --json] NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan timeout 300 nova-sprint stats --json:
timeout: failed to execute process: Permission denied (os error 13)
[dashboard curl http://127.0.0.1:7390/api/sprint]:
[HTTP 200 size=0]
[server port 127.0.0.1:6390]:
/usr/bin/bash: connect: Connection refused
/usr/bin/bash: line 1: /dev/tcp/127.0.0.1:6390: Connection refused
REFUSED (no server)
[built nova-sprint where --json (store mode NOVA_SPRINT_REDIS=127.0.0.1:6379)]:
nova-sprint where REFUSED: the store at 127.0.0.1:6379 holds nova_sprint library 90266b4d1604bed6, and this build is 11dc308260975247; run: nova-redis fn load --addr 127.0.0.1:6379

--- SAMPLE 2 | 2026-10-04T23:22:41Z ---
[installed nova-sprint where --json --cards] NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan timeout 300 nova-sprint where --json --cards:
timeout: failed to execute process: Permission denied (os error 13)
[installed nova-sprint stats --json] NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan timeout 300 nova-sprint stats --json:
timeout: failed to execute process: Permission denied (os error 13)
[dashboard curl http://127.0.0.1:7390/api/sprint]:
[HTTP 200 size=0]
[server port 127.0.0.1:6390]:
/usr/bin/bash: connect: Connection refused
/usr/bin/bash: line 1: /dev/tcp/127.0.0.1:6390: Connection refused
REFUSED (no server)
[built nova-sprint where --json (store mode NOVA_SPRINT_REDIS=127.0.0.1:6379)]:
nova-sprint where REFUSED: the store at 127.0.0.1:6379 holds nova_sprint library 90266b4d1604bed6, and this build is 11dc308260975247; run: nova-redis fn load --addr 127.0.0.1:6379

--- SAMPLE 3 | 2026-10-04T23:23:42Z ---
[installed nova-sprint where --json --cards] NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan timeout 300 nova-sprint where --json --cards:
timeout: failed to execute process: Permission denied (os error 13)
[installed nova-sprint stats --json] NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan timeout 300 nova-sprint stats --json:
timeout: failed to execute process: Permission denied (os error 13)
[dashboard curl http://127.0.0.1:7390/api/sprint]:
[HTTP 200 size=0]
[server port 127.0.0.1:6390]:
/usr/bin/bash: connect: Connection refused
/usr/bin/bash: line 1: /dev/tcp/127.0.0.1:6390: Connection refused
REFUSED (no server)
[built nova-sprint where --json (store mode NOVA_SPRINT_REDIS=127.0.0.1:6379)]:
nova-sprint where REFUSED: the store at 127.0.0.1:6379 holds nova_sprint library 90266b4d1604bed6, and this build is 11dc308260975247; run: nova-redis fn load --addr 127.0.0.1:6379

What was not measured (because the live system was not reachable; see Window above and HOLD):
- The full 1-hour sampling window: only a 2-minute, 3-sample window was sampled, since every sample failed to read the sprint and the card directs a window that could not be run to be reported HOLD rather than stretched.
- The age of the oldest open judgment on any friend (no judgments readable; store is a single-card quack fixture).
- Any natural idle-alarm raise/clear and the truth of the condition it names (none observable; not provoked).
- Each friend's ready count and width (friends table unreadable; no friends keys exist in the store).
- nova-sprint log --json events (server/store unreachable).
- The natural idle-alarm cycle the window was meant to capture (reported HOLD per the clause).
- The installed nova-sprint build itself, which is the build this card names as "the live system with the installed nova-sprint": it could not be executed under this sandbox (Permission denied).
