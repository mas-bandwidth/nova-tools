# Acceptance record — nova-sprint v1.0.0, check 6 of 6: dashboard

Card: accept-dashboard.w2, attempt 2, epoch 15, stream sprint-next. Delivered as a sprint
job per docs/FRIENDS.md to Rowan's friend; harness: opencode, model deepseek-v4.1-flash
(tier flash), inside the job's landlock wall. Repository mas-bandwidth/nova-tools, branch
sprint/accept-dashboard.w2.g3.e15 at bf078ca7d54a07958e51bdb39b9e693fc1010a66, the current
remote tip of origin/sprint/mechanical-2026-10-02 (verified equal by `git rev-parse HEAD`
and `git ls-remote origin sprint/mechanical-2026-10-02`).

## Verdict: HOLD

The measurement could not be run. The wall this card runs inside refuses to execute the
installed nova tools, the coordinator's sprint server at `127.0.0.1:6390` answers nothing,
the dashboard serves an empty body, and the live store refuses this checkout's build. No
snapshot was taken; no number on the dashboard could be read or traced. Nothing was
started, stopped or killed; the live system was not touched.

## The PASS line, verbatim

"every number on the dashboard traced to the store."

## Blockers, precisely

1. Exec blocked for the installed nova tools. Every invocation of `nova-sprint` and
   `nova-bus` fails before any verb runs, always the same way:
   `timeout: failed to execute process: Permission denied (os error 13)` (or, invoked
   directly, `bash: /home/ubuntu/.local/bin/nova-sprint: Permission denied`). The binaries
   are `-rwxr-xr-x ubuntu` under `/home/ubuntu/.local/bin`, which the wall mounts
   read-noexec; the harness's sandbox note reads
   `backend=landlock ... read=3 read-noexec=2 net=nopromise` (abi 8 clamped to 6). `nova-bus2`
   does not exist (`No such file or directory (os error 2)`), so the bus log has no reader
   at all.
2. The coordinator's sprint server answered nothing. TCP to `127.0.0.1:6390` (the
   `NOVA_SPRINT_SERVER` this card names for every verb) is refused:
   `connect: Connection refused`. No process listens on `6390` (`ss -ltn` shows only
   `7390` and `6379`). A client built from the checkout fails the same way:
   `dial tcp 127.0.0.1:6390: connect: connection refused`. The only reachable server, the
   member's `100.76.29.55:6390`, refuses reads: `where: the server runs the workers' verbs
   only: take, finish, read, queue, fleet beat, friend beat`.
3. The dashboard serves no numbers. `curl -s -i http://127.0.0.1:7390/api/sprint` returns
   `HTTP/1.1 200 OK`, `Server: Caddy`, `Content-Length: 0` — an empty body, no JSON, no
   field, no number. There is nothing on the dashboard to trace.
4. The live store refuses this checkout's build. The sprint store is live at
   `127.0.0.1:6379`, but reading it with a client built from the checkout is refused:
   `nova-sprint where REFUSED: the store at 127.0.0.1:6379 holds nova_sprint library
   90266b4d1604bed6, and this build is 11dc308260975247; run: nova-redis fn load --addr
   127.0.0.1:6379`. The live library and the checkout's library differ, so the store's
   values cannot be read from inside this wall either.

## Window

Start: none (never opened; the read path is blocked, blockers 1–4). End: none. No snapshot
was taken; no sample passed; there is no worst value, because there is no value. First
evidence stamp: `Sun Oct  4 11:23:02 PM UTC 2026`; last evidence stamp:
`Sun Oct  4 11:29:18 PM UTC 2026`. The card's 30-minute window was not stretched or
restarted, because it could not begin.

## Build measured

Unknown. The installed `nova-sprint --version` and `nova-sprint help` are exec-blocked
(blocker 1), so the build the live dashboard reads is unreadable. A binary built from the
checkout reports `nova-sprint v1.0.1-0.20261004231144-bf078ca7d54a linux/amd64 go1.26.6`
(store library `11dc308260975247`), which is not the build the live store holds (library
`90266b4d1604bed6`); the v1.0.0 build the card names was not reachable.

## Commands run (each read-only; each with its full output)

Every nova invocation ran as
`NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan timeout 300 <tool> ...`, one at
a time, output filtered with `grep -v SECRETS` (no line was dropped by the filter). All
times UTC.

| Stamp | Command | Full output |
| --- | --- | --- |
| 23:23:02 | `date -u` | `Sun Oct  4 11:23:02 PM UTC 2026` |
| 23:29:05 | `timeout 300 nova-sprint --version` | `timeout: failed to execute process: Permission denied (os error 13)` |
| 23:29:05 | `timeout 300 nova-sprint help \| head -1` | `timeout: failed to execute process: Permission denied (os error 13)` |
| 23:29:05 | `timeout 300 nova-sprint where --json` | `timeout: failed to execute process: Permission denied (os error 13)` |
| 23:29:05 | `timeout 300 nova-sprint stats --json` | `timeout: failed to execute process: Permission denied (os error 13)` |
| 23:29:05 | `timeout 300 nova-sprint log --json --since 2026-10-04T23:22:00Z` | `timeout: failed to execute process: Permission denied (os error 13)` |
| 23:29:05 | `timeout 300 nova-bus log --max 0` | `timeout: failed to execute process: Permission denied (os error 13)` |
| 23:29:05 | `timeout 300 nova-bus2 log` | `timeout: failed to execute process: No such file or directory (os error 2)` |
| 23:29:05 | `curl -s -m 10 -i http://127.0.0.1:7390/api/sprint` | `HTTP/1.1 200 OK` / `Server: Caddy` / `Content-Length: 0` (empty body) |
| 23:29:05 | `exec 3<>/dev/tcp/127.0.0.1/6390` | `connect: Connection refused` |
| 23:29:17 | `./scratch/nova-sprint --version` | `nova-sprint v1.0.1-0.20261004231144-bf078ca7d54a linux/amd64 go1.26.6` |
| 23:29:17 | `./scratch/nova-sprint where --json` (server `127.0.0.1:6390`) | `dial tcp 127.0.0.1:6390: connect: connection refused` |
| 23:29:17 | `./scratch/nova-sprint where --json --redis 127.0.0.1:6379` | `REFUSED: the store ... holds nova_sprint library 90266b4d1604bed6, and this build is 11dc308260975247` |
| 23:29:17 | `./scratch/nova-sprint stats --json --redis 127.0.0.1:6379` | `REFUSED: the store ... holds nova_sprint library 90266b4d1604bed6, and this build is 11dc308260975247` |
| 23:29:17 | `./scratch/nova-sprint where --json` (server `100.76.29.55:6390`) | `the server runs the workers' verbs only: take, finish, read, queue, fleet beat, friend beat` |
| 23:29:18 | `ss -ltn` (grep `6390\|7390\|6379`) | `127.0.0.1:6379`, `100.115.99.19:6379`, `[::1]:6379`, `*:7390` listen; nothing on `6390` |
| 23:29:18 | `stat -c "%A %U %n" .../nova-sprint .../nova-bus` | `-rwxr-xr-x ubuntu` for both |

Raw transcript: `scratch/blocker-evidence.txt` in the job directory
(`accept-dashboard.w2/scratch/blocker-evidence.txt`), kept outside the repository per PATHS.

## Per criterion

The dashboard was to be read three times, 10 minutes apart, each paired within 5 seconds
with `where --json --cards`, `stats --json` and `log --json --since <window start>`, and
every numeric field of `/api/sprint` matched to the store field or computation it equals.

- Numbers the dashboard serves: none. `/api/sprint` is an empty body (blocker 3), so the
  list of JSON paths is empty and there is nothing to trace. Samples taken 0, samples
  passing 0. There is no worst value and no first/last passing sample.
- Numbers traced to the store: not measured. No dashboard number exists and no store read
  is possible (blockers 1–4). Samples taken 0, samples passing 0.
- Differences explained by log lines between the two reads: not measured. The log is
  unreadable (blocker 1) and no pair of reads exists. Samples taken 0, samples passing 0.
- The window (30 minutes, three snapshots 10 minutes apart): not opened (blockers 1–4).
  Snapshots taken 0 of 3.

## What was not measured

Everything the PASS line names: every numeric field of `/api/sprint`, its JSON path, the
store field or computation it equals, and the side-by-side values at each of the three
snapshots; plus the window reads themselves (`where --json --cards`, `stats --json`,
`log --json --since`, `nova-bus log --max 0`, the dashboard). The three snapshots and the
30-minute window were not run. Nothing was started, stopped or killed; no server was
started; the live system was not touched. The card's placeholder gate
`TestAcceptanceRecordsAreWellFormed` is not run by this card, and no `go` gate is part of
it.

## Way forward

The blockers are environmental, not sprint state: this record says nothing about the
reliability of the nova-sprint dashboard. Re-run this card from a seat whose wall can
execute `/home/ubuntu/.local/bin` (read-exec, not read-noexec), whose coordinator's sprint
server answers on `127.0.0.1:6390`, and whose dashboard `/api/sprint` returns the JSON it
serves; or have the member read `nova-sprint where --json --cards`, `nova-sprint stats
--json` and `nova-sprint log --json --since <start>` from outside the wall and state
whether the dashboard's numbers trace to the store. The sentinel
nova-sprint-v1-0-0-acceptance stays unmet: this card lands HOLD, not PASS.
