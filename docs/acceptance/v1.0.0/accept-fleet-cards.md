# nova-sprint v1.0.0 acceptance, check 2 of 6: fleet cards

Verdict: FAIL

The measurement could not be run from this checkout. This record is a FAIL: the
two-hour window was never opened, so no criterion is met. It is not a HOLD only
because the record must carry `Verdict: PASS` or `Verdict: FAIL`; the blockers
below are precise and the sprint should read this as "not measured", not as
"measured and bad".

PASS (verbatim): "script and functional cards pass on every member, the functional
tier in containers, no environment failure family for 2 hours."

Window start (attempted): 2026-10-04T23:37:25Z
Window end (attempted): 2026-10-04T23:38:10Z
The 2-hour window was not opened and not sampled. These two stamps bound the only
samples taken (one pass of the read sources, each refused); they are not the start
and end of a window.

Build measured: nova-sprint v1.0.0 (the version the card names). The installed
binary's own version string was not read: `nova-sprint --version` cannot execute
in this sandbox (see Blocker 1). The measured server is the live sprint server at
100.76.29.55:6390, as its worker members name it; its dashboard reads
100.76.29.55:7390.

## Every command run

Each command was run from
`~/rowan-working/tmp/slots/accept-fleet-cards.w2.g3.e15/jobs/accept-fleet-cards.w2`,
one at a time, with its raw output kept in `scratch/attempts.log` and
`scratch/dashboard_sprint.json`.

1. `NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan timeout 300 nova-sprint --version`
   -> `timeout: failed to execute process: Permission denied (os error 13)`, rc=126.
2. `curl -sS -m 5 -o /dev/null -w '%{exitcode} %{http_code}' http://127.0.0.1:6390/`
   -> `curl: (7) Failed to connect to 127.0.0.1 port 6390 ... Could not connect to server`, rc=7, http=000.
3. `curl -sS -m 60 -H 'Content-Type: application/json' -d '{"verbs":[["where","--json","--cards","--actor","rowan"]]}' http://100.76.29.55:6390/verbs`
   -> the server refused `where` as a coordinator verb (raw line below).
4. `curl -sS -m 60 ... '{"verbs":[["stats","--json","--actor","rowan"],["log","--json","--since","2026-10-04T21:37:00Z","--actor","rowan"]]}' http://100.76.29.55:6390/verbs`
   -> the server refused `stats` and `log` the same way (raw lines below).
5. `curl -sS -m 8 http://127.0.0.1:7390/api/sprint`
   -> HTTP 200, Content-Length 0 (the card's dashboard source carries no data here).
6. `curl -sS -m 10 http://100.76.29.55:7390/api/sprint`
   -> HTTP 200, 81787 bytes; the fleet snapshot below is read from it. This is the
   dashboard's cached `where`, not the required `where --json` and not a window.

Every raw sample was filtered for credential-like lines before it was kept.

## Per criterion: measured value against its bar

The PASS line is five criteria. None was measured; there are no passing samples.

| # | Criterion | Bar | Measured | Samples taken | Samples passing | Worst value and when |
|---|-----------|-----|----------|---------------|-----------------|----------------------|
| 1 | script cards finished ok, every member | >= 1 per member in window | not measured | 0 of the required 24 (every 5 min for 2 h) | 0 | none; `where --json --cards` refused at 23:37:59Z |
| 2 | functional cards finished ok, every member | >= 1 per member in window | not measured | 0 of 24 | 0 | none; `where --json --cards` refused at 23:37:59Z |
| 3 | functional tier ran in containers | every functional card finished in window shows its container run | not measured | 0 | 0 | none; no functional card could be read |
| 4 | no environment failure family | zero such failures for 2 h | not measured | 0 | 0 | none; `log --json --since` refused at 23:37:59Z |
| 5 | no member with zero cards finished | every up member finished >= 1 | not measured | 0 | 0 | none; per-member finishes could not be read |

An environment failure family is a failure whose family names the environment (a
missing tool, a container that would not start, a disk or path fault), not the
card's own work; none could be classified because no failure line could be read.

## Raw sample lines that decide each criterion

The deciding lines are the refusals. All are kept in full.

- Local client (criterion 1, 2, 3, 4, 5):
  `timeout: failed to execute process: Permission denied (os error 13)`
- The card's configured server (criterion 1, 2, 3, 4, 5):
  `curl: (7) Failed to connect to 127.0.0.1 port 6390 after 0 ms: Could not connect to server`
- `where` (criterion 1, 2, 5):
  `{"results":[{"code":2,"stdout":"","stderr":"nova-sprint server: where: the server runs the workers' verbs only: take, finish, read, queue, fleet beat, friend beat; nothing was changed\n"}]}`
- `stats` (criterion 1, 2):
  `{"results":[{"code":2,"stdout":"","stderr":"nova-sprint server: stats: the server runs the workers' verbs only: take, finish, read, queue, fleet beat, friend beat; nothing was changed\n"}]}`
- `log` (criterion 4):
  `{"results":[{"code":2,"stdout":"","stderr":"nova-sprint server: log: the server runs the workers' verbs only: take, finish, read, queue, fleet beat, friend beat; nothing was changed\n"}]}`
- The card's dashboard source (criterion 1-5):
  `http=200 size=0` for `http://127.0.0.1:7390/api/sprint`

There are no passing sample lines. The first and last passing sample: none.

## What was not measured

Everything the PASS line asks: no fleet member's script or functional card count
was read; no finished-ok, failed or failure-family count was read; no functional
card's container run was confirmed; no 2-hour continuity was observed; and no
member was checked for zero cards finished. No window start or end from a real
window exists. The per-member rows, tier split and container evidence in
`nova-sprint where --json --cards` and `nova-sprint log --json --since` were never
obtained, because both reads were refused (above).

## Blockers (why the measurement could not be run)

1. The installed client cannot execute in this sandbox. Landlock denies
   `/home/ubuntu/.local/bin/nova-sprint` (and `nova-bus`): every run prints
   `Permission denied (os error 13)` at rc=126, and the file is unreadable
   (`file`: no read permission). This is the same denial the first attempt hit,
   at its old path `/home/nova/.local/bin/nova-sprint`. No sprint verb ran here.
2. The card's server address is not this machine's. `NOVA_SPRINT_SERVER=127.0.0.1:6390`
   is not listening on this host (`curl` rc=7); `ss -ltn` shows no 6390 listener.
   The live sprint server is at `100.76.29.55:6390`, and its members are launched
   as `nova-swarm member --server 100.76.29.55:6390`.
3. The reachable server serves workers, not the coordinator. `100.76.29.55:6390`
   accepts `POST /verbs` and refuses `where`, `stats` and `log` with "the server
   runs the workers' verbs only". Those three are the read sources the card names,
   so the fleet rows, per-tier counts and log cannot be read from here.
4. The card's dashboard source carries nothing here. `127.0.0.1:7390/api/sprint`
   is HTTP 200 with a zero-length body; the populated dashboard is the remote
   `100.76.29.55:7390`.
5. The window does not fit the run. The card's window is 2 hours; this run's
   native harness was launched with `--deadline 20m0s`. Even with a working
   client, the required window could not be completed before the harness stops
   the card.
6. The rules forbid the obvious workaround. "Never start a server on this
   machine" rules out standing up a local `nova-sprint run --listen` to serve the
   coordinator's verbs, and the client binary cannot be built from this checkout
   without first reading the installed one it replaces.

## Fleet snapshot (not the required window; recorded for the sprint)

Read from `100.76.29.55:7390/api/sprint` at `2026-10-04T19:38:10-04:00`
(23:38:10Z). This is the dashboard's cached `where`, not `nova-sprint where
--json`, and it is one instant, not 2 hours. It is recorded so the blocker is
legible, not as evidence for any criterion.

Members `status=up` at this instant: batman (done=123 ok=91 failed=32), hetzner
(done=678 ok=480 failed=198), space (done=1016 ok=720 failed=296), superman
(done=135 ok=30 failed=105), vision (done=434 ok=318 failed=116).
Down and so not measured: captain (status=down, done=0), hulk (status=down,
done=0). Not up: studio (status=held, done=274 ok=153 failed=121).
Summary at this instant: `1226/2519 48.7% held=671 -> ETA 4d22h`.

## What was done

- Read the card, the checkout, the live listeners, the worker-facing server and
  the remote dashboard.
- Ran every read source the card names, one at a time, and kept each raw refusal
  in `scratch/attempts.log`.
- Wrote this record and committed it on the card's branch, so the FAIL is the
  evidence and the sentinel's need stays unmet.
