# Acceptance: friends E2E probe (nova-sprint v1.0.0, check 1 of 6)

Verdict: FAIL

The PASS line, verbatim:

> the E2E probe (bus message -> turn in the open chat -> reply) passes for every
> friend not held, 10 of 10, for 2 hours; presence matches each friend's own
> count.

## Window

- Window start (UTC): 2026-10-04T23:19:59Z
- Window end (UTC): 2026-10-04T23:19:59Z

The window never opened. The measurement could not be run, so there is no
two-hour sample interval to record. The start and end stamps above are the
first and last lines of the single measurement attempt; no probe was sent and
no reply was expected. This record is FAIL because the gate did not pass; the
card's report is HOLD because the gate could not be measured at all.

## The build measured

- Repository: `mas-bandwidth/nova-tools`, branch `sprint/accept-friends-e2e.w1.g2.e15`
  at `bf078ca7d54a07958e51bdb39b9e693fc1010a66` (from `sprint/mechanical-2026-10-02`).
- `nova-sprint --version` (or `nova-sprint help | head -1`): not readable. The
  installed binary at `/home/ubuntu/.local/bin/nova-sprint` exists
  (`-rwxr-xr-x`, owner the running user, 19206304 bytes) but every invocation
  returns `Permission denied (os error 13)`, so the build's version line was
  never obtained. The same holds for `/home/ubuntu/.local/bin/nova-bus`.
- This record was produced by Rowan, harness `opencode`, model
  `deepseek-v4.1-flash`.

## Blocker (why the measurement could not be run)

The card's whole read path is unavailable in the sandbox this card ran in:

1. The installed tools cannot be executed. Both `nova-sprint` and `nova-bus`
   are mode `0755`, owned by the running user, and still fail with
   `Permission denied (os error 13)` / exit `126`. The harness log reports the
   wall as `SANDBOX OK backend=landlock abi=8 used=6 read=3 read-noexec=2
   write=4 net=nopromise`: the two nova binaries are in the wall's
   read-noexec set, so the sandbox refuses to execute them. The card names no
   other way to reach the sprint (the repository's `cmd/nova-sprint` is source,
   and the card measures the installed tool, not a fresh build).
2. The sprint server is not listening. `NOVA_SPRINT_SERVER=127.0.0.1:6390`
   names the only read source; `ss -tlnp` shows no listener on `6390`, `6391`
   or `6392`, and `curl` to `http://127.0.0.1:6390/` returns no connection.
3. The dashboard answers nothing. `curl -s http://127.0.0.1:7390/api/sprint`
   returns HTTP `200` with `size=0` (an empty body), and every other path tried
   (`/`, `/api/team`, `/api/friend/rowan`, `/health`, `/status`) is likewise
   `200`/empty, so no presence data can be read there either.

With no executable client, no listening server and an empty dashboard, the
friends table cannot be read at the window's start or end, no probe can be
sent, no reply can be seen and no presence can be compared.

## Every command run

All sprint verbs were run in the card's form
`NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan timeout 300 nova-sprint <verb>`,
one at a time, read-only.

```
NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan timeout 300 nova-sprint --version
NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan timeout 300 nova-sprint help
NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan timeout 300 nova-sprint where --json
NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan timeout 300 nova-sprint stats --json
timeout 300 nova-bus log --max 0
curl -s -m 10 http://127.0.0.1:7390/api/sprint
ss -tlnp
git rev-parse HEAD
git branch --show-current
date -u
```

Raw log: `scratch/measure.log` in the job directory.

## Per criterion: measured value against its bar

| Criterion | Bar | Measured | Raw counts |
| --- | --- | --- | --- |
| Friends not held, named at window start | each held friend named from `where --json` | not measured | 0 friends named |
| Friends not held, named at window end | each held friend named from `where --json` | not measured | 0 friends named |
| E2E probes, per friend | 10 of 10, one every 12 minutes for 2 hours | not measured | 0 probes sent, 0 replies seen |
| Round trip, per probe | reply within 10 minutes | not measured | 0 round trips recorded |
| Presence matches friend's own count | match at every probe | not measured | 0 comparisons made |

Worst value and when: the worst value is that nothing was measured at all; the
single attempt is stamped `2026-10-04T23:19:59Z`.

## The raw sample lines that decide each criterion

The deciding lines, in full, are the failed reads (the only sample taken):

```
--- build measured: nova-sprint --version ---
timeout: failed to execute process: Permission denied (os error 13)
exit=126

--- read source: nova-sprint where --json ---
timeout: failed to execute process: Permission denied (os error 13)
exit=126

--- read source: nova-sprint stats --json ---
timeout: failed to execute process: Permission denied (os error 13)
exit=126

--- read source: nova-bus log --max 0 ---
timeout: failed to execute process: Permission denied (os error 13)
exit=126

--- dashboard: curl -s http://127.0.0.1:7390/api/sprint ---

http_code=200 size=0

--- sprint server listener check ---
no listener on 639x

--- installed binary exec check ---
/home/ubuntu/.local/bin/nova-sprint EXEC FAIL rc=126
/home/ubuntu/.local/bin/nova-bus EXEC FAIL rc=126
```

Samples taken: 1 attempt (0 probes). Samples passing: 0. There are no passing
samples to give the first and last of, because no criterion ever passed.

## What was not measured

- Whether the E2E probe (bus message -> turn in the open chat -> reply) passes
  for every friend not held; the probe was never sent.
- The 10-of-10-per-friend probe count and the 2-hour window; the window never
  opened.
- The round trip of each probe against the 10-minute reply bound.
- Presence (status and working count) as the friends table and the dashboard
  show it, and whether it matches each friend's own reported working count.
- Which friends are not held: the friends table could not be read, so no friend
  is named here.
- The build measured: `nova-sprint --version` and `nova-sprint help` both
  failed to execute, so the version of the installed tool is unknown.
- Everything else in the PASS line.

No bus message was written: the card allows bus messages as its only writes,
and none could be sent because `nova-bus` cannot execute and no server is
listening. The only write this attempt made was this evidence file and its raw
log.
