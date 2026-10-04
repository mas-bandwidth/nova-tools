# accept-friends-e2e

Verdict: HOLD

PASS line (the bar, not met; the window was not run): the E2E probe (bus message -> turn in the open chat -> reply) passes for every friend not held, 10 of 10, for 2 hours; presence matches each friend's own count.

Window start (UTC): 2026-10-04T23:32:02Z (`date -u` of the first sample; the first `nova-sprint --version` attempt in the same sample returned rc 126).
Window end (UTC): 2026-10-04T23:35:51Z (`date -u` of the last sample, when every probe path was confirmed blocked). The 2 hour window was never started, never stretched and never restarted: no probe can be sent, so a failed 2 hour window was not manufactured.

Build measured: the installed binaries could not be read for a build (open returns EACCES). Dashboard snapshot `build` field, same at both ends: `19c41193`. Diagnostic CLI built from source inside the job directory: `nova-sprint v1.0.1-0.20261004231144-bf078ca7d54a linux/amd64 go1.26.6`. Checkout measured from: `bf078ca7d54a07958e51bdb39b9e693fc1010a66` (`sprint/mechanical-2026-10-02` as staged; origin's tip of that branch has since moved to `70587807db83810ecb356be2cdcdefbc7e497382`, not used). Reporter: Rowan, harness opencode, model qwen3.8-max. Attempt 4; attempt 3 held with the same exec and connection blockers (`b043d4ccfefef6e0dae09ca84ec0ac816b8f0f37`), and this attempt confirms all of them still stand and adds three findings: a from-source CLI runs inside the wall and still cannot reach any coordinator; the staged build's `nova-bus` has no `log` verb and no probe verb and its `send` does not take the card's flags; the slot's enforced harness deadline is 40m0s, shorter than the 2 hour window.

## Why HOLD (blockers, in order of proof)

1. The installed CLIs cannot be executed in this sandbox: `/home/ubuntu/.local/bin/nova-sprint` and `/home/ubuntu/.local/bin/nova-bus` are mode 0755 owner ubuntu, yet every open/exec returns errno 13 (EACCES, rc 126); `file` reports "no read permission". `nova-bus2` is not on PATH (rc 127). Confirmed at 23:32, 23:34:24 and 23:35:51 UTC.
2. The card's coordinator address has no server: `127.0.0.1:6390` refuses connections (curl rc 7; `ss -tln` shows no 6390 listener). A CLI built from the staged source and run with the card's exact env gets `connection refused ... (NOVA_SPRINT_SERVER=127.0.0.1:6390)` for `where --json`, `where --json --cards`, `stats --json` and `log --json --since`. So even a fully runnable CLI cannot read the friends table, the stats or the log at the address the card names. Starting a server is forbidden by the card's rules.
3. The only reachable sprint server is the worker server the live members use (`ps`: `nova-swarm member --as space --server 100.76.29.55:6390`), and it refuses every read this card needs: `POST /verbs` with `where` returns `the server runs the workers' verbs only: take, finish, read, queue, fleet beat, friend beat; nothing was changed`. The built CLI gets the same refusal with `NOVA_SPRINT_SERVER=100.76.29.55:6390`.
4. The card's dashboard read `curl -s http://127.0.0.1:7390/api/sprint` returns HTTP 200 with an empty body (Content-Length: 0): Caddy on :7390 serves the JSON only for Host `100.115.99.19` / `69.67.149.151` (rewrite `/api/sprint` -> `/api/sprint.json`); the loopback Host matches no site. With the Host header the same curl returns the full 81288-byte snapshot (`build` 19c41193). The snapshot carries no probe replies; it is not a substitute for `where --json` and cannot decide the PASS line.
5. No probe verb exists that this card could run: `nova-bus help` (staged build) names no probe verb and no `log` verb (`BUS REFUSED: unknown verb "log"; the verbs are draft, prepare, send, reply, inbox, receipt, close, wait, check, names, version`); its `send` requires `--bus <dir>` and `--remote <name>` with the note in a file, not the card's `--to/--subject/--body` shape, and the card gives no bus directory. `nova-sprint help friend` names only `friend sync|beat|down|up|clean`. The installed `nova-bus`, whose verb shape the card quotes, cannot be executed (blocker 1).
6. The window cannot fit this session even if 1-5 were fixed: the harness process running this slot was started at 23:31 UTC with `--deadline 40m0s` (`ps`: `nova-swarm native ... --label accept-friends-e2e.w4 ... --deadline 40m0s`), a hard stop near 00:11 UTC. The bar requires 10 probes per friend, one every 12 minutes, across 2 hours. The card forbids stretching or restarting the window; a partial window cannot produce PASS or an honest FAIL record of a completed window.

No bus message was sent; these would have been the card's only writes. No process was killed, no server started, nothing outside the job directory was written (the from-source build wrote only `$JOB/scratch/bin` and the shared GOCACHE).

## Commands run

Every sprint verb was attempted as `NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan timeout 300 nova-sprint <verb>`, one at a time; the installed binary never started, so the same verb was repeated through the from-source build at `$JOB/scratch/bin/nova-sprint` (same env) as a diagnostic. A read that failed is a counted failure, not a skip. All output was passed through the card's forbidden-word filter, so no evidence line contains that word.

1. `date -u` (first sample): `Sun Oct  4 11:32:02 PM UTC 2026`.
2. `command -v nova-sprint nova-bus nova-bus2` -> `/home/ubuntu/.local/bin/nova-sprint`, `/home/ubuntu/.local/bin/nova-bus`, `nova-bus2` absent.
3. `stat -c '%n mode=%a owner=%U size=%s'` of both installed binaries: mode 755, owner ubuntu, sizes 19206304 and 4497568. `file` of both: "writable, executable, regular file, no read permission".
4. `nova-sprint --version`, `nova-sprint help`, `nova-bus help` (installed, with and without `timeout`): rc 126, `Permission denied`, at 23:32, 23:34:24, 23:35:51. `nova-bus2 help`: rc 127, command not found.
5. `curl -sS -m 3 http://127.0.0.1:6390/`: rc 7 connection refused, at 23:34:24 and 23:35:51. `ss -tln`: listeners on 43201, 6379, 2019, 12345, 7390; none on 6390.
6. `curl -sS -m 5 -D - http://127.0.0.1:7390/api/sprint`: HTTP 200, `Server: Caddy`, `Content-Length: 0`, at 23:34:24 and 23:35:51.
7. `curl -sS -m 8 -H 'Host: 100.115.99.19' http://127.0.0.1:7390/api/sprint`: HTTP 200, 81288 bytes, `build` 19c41193, `fetchedAt` 2026-10-04T23:34:26.126005+00:00 (start) and 2026-10-04T23:35:50.377210+00:00 (end). `curl http://100.76.29.55:7390/api/sprint`: same 81288 bytes.
8. `curl http://127.0.0.1:2019/config/`: Caddy serves `:7390` for hosts `100.115.99.19` and `69.67.149.151` from `/var/www/sprint/site`, rewriting `/api/sprint` to `/api/sprint.json`; no loopback host site (explains 6).
9. `curl http://100.76.29.55:6390/`: `the sprint server takes POST /verbs`. `POST /verbs` body `{"verbs":[["where","--actor","rowan","--json"]]}`: HTTP 200, result code 2, stderr `nova-sprint server: where: the server runs the workers' verbs only: take, finish, read, queue, fleet beat, friend beat; nothing was changed`.
10. `ps aux | grep -E "nova-swarm member|accept-friends-e2e.w4"`: members on `--server 100.76.29.55:6390` since 18:54; this slot's `nova-swarm native ... --deadline 40m0s --label accept-friends-e2e.w4` started 23:31.
11. From-source build in the job dir (`GOCACHE=/home/ubuntu/rowan-working/tmp/cache/go-build`, `nice -n 19 go build ./cmd/nova-sprint ./cmd/nova-bus`): `sprint_build_ok`, `bus_build_ok`, 23:34:54 -> 23:34:57. `$JOB/scratch/bin/nova-sprint --version`: `nova-sprint v1.0.1-0.20261004231144-bf078ca7d54a linux/amd64 go1.26.6`, rc 0.
12. Built CLI, card env, card address: `where --json`, `where --json --cards`, `stats --json`, `log --json --since 2026-10-04T23:34:00Z`: each rc 2, `the sprint server at 127.0.0.1:6390 did not answer: Post "http://127.0.0.1:6390/verbs": dial tcp 127.0.0.1:6390: connect: connection refused (NOVA_SPRINT_SERVER=127.0.0.1:6390)`.
13. Built CLI, worker address: `NOVA_SPRINT_SERVER=100.76.29.55:6390 ... where --json`: rc 2, `nova-sprint server: where: the server runs the workers' verbs only: take, finish, read, queue, fleet beat, friend beat; nothing was changed`.
14. Built `nova-bus log --max 0`: `BUS REFUSED: unknown verb "log"; the verbs are draft, prepare, send, reply, inbox, receipt, close, wait, check, names, version`. Built `nova-bus help`: no probe verb; `send` shapes require `--bus <dir>` and `--remote <name>`. Built `nova-sprint help friend`: `friend sync|beat|down|up|clean` only; no probe verb.
15. `git ls-remote origin refs/heads/sprint/accept-friends-e2e.w4.g3.e15 refs/heads/sprint/mechanical-2026-10-02`: the STATUS branch does not exist on origin yet; `sprint/mechanical-2026-10-02` is at `70587807db83810ecb356be2cdcdefbc7e497382`.

No bus message was sent. No probe verb was named by any help that could be run, so the fallback `nova-bus send` applied; its staged-build shape needs `--bus <dir>`/`--remote` the card does not give, and the installed binary that takes the card's shape cannot be executed, so no send was attempted.

## Friends not held

Named from the dashboard snapshot's friends table (`data.tables.friends`, the pull's copy of `tables.friends`), because `nova-sprint where --json` could not be run at the card's address (command 12) and the worker server refuses `where` (command 13). Read at the window's start sample (`fetchedAt` 23:34:26Z) and again at its end sample (`fetchedAt` 23:35:50Z); the not-held set is identical at both ends.

Not held (8), each named: emma (up, working 1), freddy (up, working 0), johnny (up, working 2), rowan-next (down, working 0), rowan-personal (down, working 0), rowan-space (down, working 0), stella (up, working 0), zhi (up, working 3 at start, 2 at end).

Held, not probed: alex (held, working 2), rowan (held, working 0), rowan-mas (held, working 0).

## Criteria

Criterion 1, E2E probe. Bar: 10 of 10 probes pass for every friend not held - a bus message, a turn in that friend's open chat, and the friend's reply on the bus within 10 minutes - 10 probes per friend, one every 12 minutes across 2 hours.

Measured: probes taken 0, probes passing 0, for each of emma, freddy, johnny, rowan-next, rowan-personal, rowan-space, stella, zhi (0 of 80 overall). Worst value: 0 of 10 per friend, from the window start 2026-10-04T23:32:02Z, because no send could be executed (blockers 1, 2, 5) and no window could be completed (blocker 6). No send time, reply time or round trip exists.

Criterion 2, presence. Bar: at every probe, the friends table and the dashboard status and working count match the count the friend itself states in its reply.

Measured: probes 0, presence comparisons 0, matches 0. The dashboard status and working count were readable twice (start and end samples above) and changed only in zhi's working count (3 -> 2) and emma's (1 -> 1); the friends table itself (`where --json`) was never readable. The friend's own count was never asked and never received, because no probe was sent. Worst value: no comparison possible, from 2026-10-04T23:32:02Z.

## Raw lines that decide it

Failing reads, in full (installed exec, start sample 23:34:24 and end recheck 23:35:51):

```
=== sample1 Sun Oct  4 11:34:24 PM UTC 2026 ===
/home/ubuntu/.local/bin/nova-sprint mode=755 owner=ubuntu size=19206304
/home/ubuntu/.local/bin/nova-bus mode=755 owner=ubuntu size=4497568
/home/ubuntu/.local/bin/nova-sprint: writable, executable, regular file, no read permission
/home/ubuntu/.local/bin/nova-bus:    writable, executable, regular file, no read permission
timeout: failed to execute process: Permission denied (os error 13)
rc:126
/usr/bin/bash: line 1: nova-bus2: command not found
curl: (7) Failed to connect to 127.0.0.1 port 6390 after 0 ms: Could not connect to server
HTTP/1.1 200 OK
Server: Caddy
Date: Sun, 04 Oct 2026 23:34:24 GMT
Content-Length: 0
```

```
=== end sample Sun Oct  4 11:35:51 PM UTC 2026 ===
curl: (7) Failed to connect to 127.0.0.1 port 6390 after 0 ms: Could not connect to server
HTTP/1.1 200 OK
Server: Caddy
Date: Sun, 04 Oct 2026 23:35:51 GMT
Content-Length: 0
timeout: failed to execute process: Permission denied (os error 13)
rc:126
timeout: failed to execute process: Permission denied (os error 13)
rc:126
```

Failing reads, in full (from-source CLI at the card's address and the worker address, and the bus verbs, 23:35:17-23:35:37):

```
nova-sprint where: the sprint server at 127.0.0.1:6390 did not answer: Post "http://127.0.0.1:6390/verbs": dial tcp 127.0.0.1:6390: connect: connection refused (NOVA_SPRINT_SERVER=127.0.0.1:6390); nothing is known of what ran: once it answers, read the sprint (nova-sprint where, log) before running it again; the server is the run loop: run: nova-sprint run --listen <host:port>
nova-sprint server: where: the server runs the workers' verbs only: take, finish, read, queue, fleet beat, friend beat; nothing was changed
BUS REFUSED: unknown verb "log"; the verbs are draft, prepare, send, reply, inbox, receipt, close, wait, check, names, version; run: nova-bus help
{"results":[{"code":2,"stdout":"","stderr":"nova-sprint server: where: the server runs the workers' verbs only: take, finish, read, queue, fleet beat, friend beat; nothing was changed\n"}]}
```

Deadline and server evidence (ps, 23:34:48, trimmed of harness/card paths and passed through the forbidden-word filter):

```
ubuntu    851956 ... SNl  23:31   0:01 /home/ubuntu/.local/bin/nova-swarm native  --model opencode/qwen3.8-max   --slot /home/ubuntu/rowan-working/tmp/slots/accept-friends-e2e.w4.g3.e15 --root /home/ubuntu/rowan-working/tmp --deadline 40m0s --tokens 2000000 --label accept-friends-e2e.w4  --stage-timeout 2m0s
ubuntu   2899051 ... Ssl  18:54   7:33 /home/ubuntu/.local/bin/nova-swarm member --as space --server 100.76.29.55:6390  --root /home/ubuntu/rowan-working/tmp
```

Passing probe lines: none. Count 0. No first or last passing sample exists. The only passing reads were the two dashboard snapshots with the Host header (not the card's loopback curl, and not a probe):

```
dash-start code:200 bytes:81288 build=19c41193 fetchedAt=2026-10-04T23:34:26.126005+00:00
dash-end   code:200 bytes:81288 build=19c41193 fetchedAt=2026-10-04T23:35:50.377210+00:00
```

Dashboard friend rows, first sample (fetchedAt 23:34:26Z) and last sample (fetchedAt 23:35:50Z):

```
alex	held	working=2          alex	held	working=2
emma	up	working=1          emma	up	working=1
freddy	up	working=0          freddy	up	working=0
johnny	up	working=2          johnny	up	working=2
rowan	held	working=0          rowan	held	working=0
rowan-mas	held	working=0      rowan-mas	held	working=0
rowan-next	down	working=0    rowan-next	down	working=0
rowan-personal	down	working=0  rowan-personal	down	working=0
rowan-space	down	working=0    rowan-space	down	working=0
stella	up	working=0          stella	up	working=0
zhi	up	working=3              zhi	up	working=2
```

## What was not measured

The 2 hour probe loop was not run; no window was started, stretched or restarted. No bus message was sent, no open-chat turn was observed, no reply was waited for, no round trip was timed. `nova-sprint where --json`, `where --json --cards`, `stats --json` and `log --json --since` were not answered by any server: refused at the card's address, rejected by the worker server. `nova-bus log --max 0` was not run against the installed binary (EACCES) and the verb does not exist in the staged build; `nova-bus2 log` was not run (not installed). Presence was never compared to a friend's own working count, because no probe asked for one. The installed binaries' build was not read (open EACCES); `19c41193` is the dashboard snapshot's build field, not a binary read. The from-source build ran no test (`TestAcceptanceRecordsAreWellFormed` is a placeholder and this card does not run it). No server was started, no process killed, no friend held or unheld, nothing written outside the job directory.
