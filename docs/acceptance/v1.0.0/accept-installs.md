# v1.0.0 acceptance: a server install and a rollback each done by the verbs, gated by the land self-test

Verdict: HOLD

The requirement, verbatim: "a server install and a rollback each done by the verbs, gated by the
land self-test."

- Requirement: a server install and a rollback each done by the verbs, gated by the land self-test
- Release: v1.0.0
- Measured: 2026-10-07T02:01:33Z
- From: a walled card sandbox to the live sprint server 127.0.0.1:6390 and the live dashboard 127.0.0.1:7390
- Tool: the nova-sprint verbs (the installed binary refused exec; a client built from the branch tip ran every probe), curl, ss
- Verdict: NOT MET

The measurement could not be run, so this record holds rather than judges: the disposition is
HOLD, and the field above says NOT MET only in the sense that no part of the requirement was
met. Per the coordinator's note of 2026-10-05, a wall that blocks the verbs' exec and the
server's socket is not a finding about the system; that is exactly what this attempt hit, so it
is not recorded as a FAIL. No install was done, no rollback was done, and nothing on the live
system was changed.

## Method

The check as briefed: open a 30-minute window from the first sample, install the server build
named in the release reason by the verb `nova-sprint server switch <binary>`, roll back by the
verb `nova-sprint server switch --rollback`, record the build before and after, the land
self-test each swap ran, and the time the server was unanswering. Every verb runs as
`NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan timeout 300 nova-sprint <verb>`.

The window opens only when the coordinator's release reason, read live with
`nova-sprint log --card accept-installs`, says the install window is open. That read could not
be obtained: the live sprint server at 127.0.0.1:6390 answered nothing (no listener in this
sandbox; connection refused), and the installed `nova-sprint` binary refused exec
(Permission denied, exit 126). A binary built in the job from the current tip of
`sprint/mechanical-2026-10-02` (commit a65b5cba44ab) runs fine inside the sandbox and was used
for every probe instead; a switch from this sandbox could not reach the live server's binary or
its store (the target defaults to the caller's own binary or NOVA_SPRINT_SERVER_BIN, and
NOVA_SPRINT_REDIS is unset here), so even the install verb's local run changes nothing on the
live system. Only its read-only `--dry-run` form was attempted.

Build measured: nothing readable on the live system (the installed binary's `--version` and
`where --json` are both unreachable). The candidate build, printed by the tip-built client:
`nova-sprint v1.0.1-0.20261007015135-a65b5cba44ab linux/amd64 go1.26.6`.

Window: not opened. Earliest reply from the live system: 2026-10-07T01:57:15Z (the dashboard's
Date header); stamped probe runs 2026-10-07T02:01:33Z and 2026-10-07T02:06:57Z. The first verb
attempt was between them. No 30-minute sampling loop ran, because there was nothing to sample.

Commands run, in order (R = the tip-built client; $JOB = the job directory; all verb calls carry
NOVA_SPRINT_SERVER=127.0.0.1:6390 and NOVA_SPRINT_ACTOR=rowan):

```
nova-sprint --version                                          # Permission denied
timeout 300 nova-sprint --version                              # Permission denied (exit 126)
nova-bus log --max 0                                           # Permission denied
nova-update help                                               # Permission denied
nice -n 19 go build -o $JOB/scratch/ns-tip ./cmd/nova-sprint   # built
$JOB/scratch/ns-tip --version                                  # v1.0.1-0.20261007015135-a65b5cba44ab
$JOB/scratch/ns-tip help; help server; server switch -h; selftest land -h; selftest -h
ss -ltn                                                        # no 6390 listener; *:7390 present
curl -s -m 5 -v http://127.0.0.1:6390/                         # connection refused
curl -s -m 5 http://127.0.0.1:6390/verbs -o /dev/null          # connection refused
curl -s -m 5 http://127.0.0.1:7390/api/sprint                  # HTTP 200, Server: Caddy, body empty
curl -s -m 5 http://127.0.0.1:7390/                            # HTTP 200, body empty
R where --json                                                 # the server did not answer
R stats --json                                                 # the server did not answer
R log --json --since 2026-10-07T01:55:00Z                      # the server did not answer
R log --card accept-installs                                   # the server did not answer: release reason unreadable
R server switch --dry-run R                                    # REFUSED: the shadow tick failed; nothing switched
timeout 300 nova-sprint --version                              # Permission denied (final re-probe)
R where --json                                                 # the server did not answer (final re-probe)
curl -s -m 5 http://127.0.0.1:6390/verbs                       # refused (final re-probe)
curl -s -m 5 http://127.0.0.1:7390/api/sprint                  # HTTP 200, 0 bytes (final re-probe)
```

The verbs this check would run once reachable, as found in the tip binary's own help:

- Install: `nova-sprint server switch <binary>` — before anything on disk changes it runs
  `<binary> tick --shadow` against the store, read-only, under `--tick-deadline`, and "refuses
  the swap, nothing changed, when it exits non-zero, panics or misses the deadline"; on pass it
  prints `SHADOW TICK OK binary=... epoch=... size=... took=... wall=...`, switches the server
  binary, keeps the previous one at `<target>.prev`, and rolls back on land failure inside
  `--window` (default 15m). Its exit table names the refusal: "1 failed or refused (the
  candidate's shadow tick failed: nothing changed)".
- Rollback: `nova-sprint server switch --rollback` — restores `<target>.prev` and prints
  `SERVER SWITCH ROLLED BACK target <path> restored from <path>.prev`.
- The land self-test verb beside the gate: `nova-sprint selftest land [--binary <path>]` —
  "lands a canned card on a scratch clone with this binary, writes nothing to the sprint",
  answering SELFTEST OK or SELFTEST FAILED at exit 0 or 1.

Nothing was started, stopped or killed by hand; no verb got far enough to ask for a hand step.

## Results

Per criterion of the PASS line, measured value against its bar, with the raw counts:

| criterion | bar | measured | counts |
| --- | --- | --- | --- |
| install done by the verb | one `server switch <tip binary>` against the live server, exit 0, `SERVER SWITCH OK` | not run | install verbs run against an answering server: 0 of 0; dry-run attempts: 1, REFUSED |
| install gated by the land self-test | the gate's pass line recorded, with proof a failing gate refuses | not run live | live gate runs: 0; refusal demonstrated locally (not a measurement): 1 — the shadow tick refused and nothing was switched |
| rollback done by the verb | `server switch --rollback` against the live server, exit 0, `SERVER SWITCH ROLLED BACK` | not run | rollback verbs run: 0 |
| rollback gated | same gate on the rollback | not run | 0 |
| server back on the original build at the end | `nova-sprint --version` and `where --json` equal before and after | unmeasurable | live build reads: 0 |
| time the server unanswering | measured inside the open window | not measured | sampling loop runs: 0; the window never opened |

Probe counts over the whole attempt (first exec attempt ~2026-10-07T01:56Z to final re-probe
2026-10-07T02:06:57Z): installed-verb exec attempts 5 (nova-sprint 3, nova-bus 1, nova-update
1), passing: 0 — all Permission denied, exit 126; sprint verb calls against
127.0.0.1:6390 (where, stats, log --since, log --card, where again) 6, server answered: 0 —
connection refused at every one, the worst case every sample and the first failure stamped
2026-10-07T02:01:33Z; TCP listeners on 6390: 0; dashboard reads of 127.0.0.1:7390 3
(/api/sprint twice, / once), returning sprint data: 0 — all HTTP 200 with a 0-byte body served
by Caddy, no JSON; switch attempts beyond dry run: 0 made.

## Raw

The deciding lines, as printed ($JOB is the job directory):

```
==== [2026-10-07T02:01:33Z] $ nova-sprint --version
/usr/bin/bash: line 4: /home/ubuntu/.local/bin/nova-sprint: Permission denied
---- exit=126
==== [2026-10-07T02:01:33Z] $ timeout 300 /home/ubuntu/.local/bin/nova-sprint --version
timeout: failed to execute process: Permission denied (os error 13)
---- exit=126
==== [2026-10-07T02:01:33Z] $ nova-bus log --max 0
/usr/bin/bash: line 4: /home/ubuntu/.local/bin/nova-bus: Permission denied
---- exit=126
==== [2026-10-07T02:01:33Z] $ ss -ltn (6390/7390 lines)
LISTEN 0      4096                             *:7390             *:*
==== [2026-10-07T02:01:33Z] $ curl -s -m 5 -v http://127.0.0.1:6390/verbs -o /dev/null
*   Trying 127.0.0.1:6390...
* connect to 127.0.0.1 port 6390 from 127.0.0.1 port 44832 failed: Connection refused
---- exit=7
==== [2026-10-07T02:01:33Z] $ curl -s -m 5 -i http://127.0.0.1:7390/api/sprint
HTTP/1.1 200 OK
Server: Caddy
Date: Wed, 07 Oct 2026 02:01:33 GMT
Content-Length: 0
---- exit=0
==== [2026-10-07T02:01:33Z] $ NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan timeout 300 $JOB/scratch/ns-tip where --json
nova-sprint where: the sprint server at 127.0.0.1:6390 did not answer: Post "http://127.0.0.1:6390/verbs": dial tcp 127.0.0.1:6390: connect: connection refused (NOVA_SPRINT_SERVER=127.0.0.1:6390); nothing is known of what ran: once it answers, read the sprint (nova-sprint where, log) before running it again; the server is the run loop: run: nova-sprint run --listen <host:port>
---- exit=2
==== [2026-10-07T02:01:33Z] $ NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan timeout 300 $JOB/scratch/ns-tip log --card accept-installs
nova-sprint log: the sprint server at 127.0.0.1:6390 did not answer: Post "http://127.0.0.1:6390/verbs": dial tcp 127.0.0.1:6390: connect: connection refused (NOVA_SPRINT_SERVER=127.0.0.1:6390); nothing is known of what ran: once it answers, read the sprint (nova-sprint where, log) before running it again; the server is the run loop: run: nova-sprint run --listen <host:port>
---- exit=2
==== [2026-10-07T02:01:33Z] $ NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan timeout 300 $JOB/scratch/ns-tip server switch --dry-run $JOB/scratch/ns-tip
nova-sprint server switch REFUSED: the shadow tick of $JOB/scratch/ns-tip exited 2: nova-sprint tick REFUSED: --redis <addr> is required (or NOVA_SPRINT_REDIS, NOVA_REDIS_ADDR): a shadow tick plans on a store; run: nova-sprint tick -h; $JOB/scratch/ns-tip is unchanged and the old server keeps running; remedy: verify the candidate binary with $JOB/scratch/ns-tip tick --shadow before switching; run: nova-sprint server switch -h
---- exit=1
==== [2026-10-07T02:06:57Z] $ timeout 300 nova-sprint --version
timeout: failed to execute process: Permission denied (os error 13)
---- exit=126
==== [2026-10-07T02:06:57Z] $ curl -s -m 5 -w " bytes=%{size_download}" -o /dev/null http://127.0.0.1:7390/api/sprint
 bytes=0---- exit=0
```

(The dry-run refusal line is printed with $JOB standing in for the repeated full job path; the
words are as printed.)

## Not measured

- The whole of the PASS line: no server install, no rollback, no land self-test run against the
  live server, no build-before/build-after pair, no unanswering time, no 30-minute sampling
  loop. None of it was measurable from this sandbox, and a HOLD was the briefed end for exactly
  this case.
- The live release reason (`nova-sprint log --card accept-installs`) that gates the window
  opening: the read itself was unreachable.
- The bus log (`nova-bus log --max 0`): the installed binary refused exec; no tip-built bus
  client was substituted, since its store address is not handed to this sandbox either.
- The dashboard's real sprint JSON: 127.0.0.1:7390 answers only an empty 200 (Caddy), so the
  snapshot could not be read as a source.
- The nova-update release-install path (`nova-update help` refused exec): moot, since the
  coordinator's release reason for this attempt named the sprint's own `server switch` verbs.

## Why HOLD and what the next attempt needs

The wall stayed up for this attempt despite the re-brief's expectation. A re-release of this
card should run where the card's own tools work: the installed `nova-sprint` (and `nova-bus`,
`nova-update`) exec'able, the sprint server answering verbs on 127.0.0.1:6390, the dashboard
returning sprint JSON on 127.0.0.1:7390. With those three, the sequence is the one this record
names: read `log --card accept-installs` (window open), `--version` and `where --json`
(build before), `server switch <tip binary>` recording the `SHADOW TICK OK` line, `server
switch --rollback` recording the restored build, `--version` and `where --json` again (back on
the original), with the sampling loop covering the swaps.
