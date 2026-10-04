# Acceptance record — nova-sprint v1.0.0, check 1 of 6: friends E2E

Card: accept-friends-e2e.w2, attempt 2, epoch 15, stream sprint-next. Delivered as a sprint
job per docs/FRIENDS.md to Rowan's friend; harness: opencode, model
deepseek/deepseek-v4.1-flash (tier flash), inside the job's landlock wall. Repository
mas-bandwidth/nova-tools, branch sprint/accept-friends-e2e.w2.g3.e15 at
bf078ca7d54a07958e51bdb39b9e693fc1010a66, verified equal to the remote tip of
origin/sprint/mechanical-2026-10-02 by `git rev-parse` this attempt.

Attempt 1 (accept-friends-e2e.w1, pushed c4d94ba4b1089ccabc29701f7473f10c6841e6c7 to
sprint/accept-friends-e2e.w1.g2.e15) held on the same wall; this attempt re-ran every read
from scratch and the blockers are unchanged.

## Verdict: HOLD

The measurement could not be run. The wall this card runs inside refuses to execute every
nova tool, so the friends table, the bus log and the dashboard were unreadable and the
sprint server could not be reached; the window never opened and not one E2E sample was
taken. No bus message was sent (the send path is exec-blocked and there is no reachable
bus), no turn was read and no reply was looked for. The live system was not touched.

## The PASS line, verbatim

"the E2E probe (bus message -> turn in the open chat -> reply) passes for every friend not
held, 10 of 10, for 2 hours; presence matches each friend's own count."

## Blockers, precisely

1. Exec blocked for every nova tool. Each of `nova-sprint --version`, `nova-sprint help`,
   `nova-sprint where --json --cards`, `nova-sprint stats --json`,
   `nova-sprint log --json --since`, `nova-sprint help friend`, `nova-bus help` and
   `nova-bus log --max 0` fails before any verb runs, always the same way:
   `timeout: failed to execute process: Permission denied (os error 13)` (rc=126). The
   binaries live under `/home/ubuntu/.local/bin`; `file` reports them `executable ...
   no read permission`, and the harness's sandbox note reads
   `backend=landlock abi=8 used=6 read=3 read-noexec=2 write=4 net=nopromise` (abi 8
   clamped to 6). Re-verified this attempt at 2026-10-04T23:24:29Z, one command at a time.
   `nova-bus2` (the pre-rename name) is not installed at all:
   `timeout: failed to execute process: No such file or directory (os error 2)` (rc=127).
2. The friend list is unreadable, so the friends "not held" could not even be named. The
   friends table is the friend rows of `nova-sprint where --json` (tables.friends), which
   is blocker 1. No friend name was read at the window's start or at its end.
3. The window never opened. The card measures a 2-hour window from the first sample; no
   sample could be taken (blockers 1 and 4), so the window has no start and no end. It was
   not stretched or restarted.
4. The bus path is unreachable. `nova-bus send` (the only write this card makes) is
   exec-blocked; `nova-bus log --max 0` is exec-blocked; `nova-bus help` is exec-blocked,
   so no probe verb could be found in it and no `nova-sprint help friend` probe verb could
   be found either. No probe was sent and no reply was searched for.
5. The sprint server answered nothing. TCP to `127.0.0.1:6390` (NOVA_SPRINT_SERVER for
   every verb) is refused: `curl -w http_code` is `000` (exit 7) and a bash `/dev/tcp`
   open prints `connect: Connection refused`; `ss -ltn` shows no listener on 6390. The
   dashboard read `curl -s http://127.0.0.1:7390/api/sprint` returned HTTP 200 with
   `Content-Length: 0` from Caddy — a connection, but no sprint data in the body.

## Window

Start: none (never opened; blocker 3). End: none. First evidence stamp:
`2026-10-04T23:24:29Z`; last evidence stamp: `2026-10-04T23:24:29Z`. No sample was taken;
no sample passed; there is no worst value and no worst time, because there is no value.

## Build measured

Unknown. `nova-sprint --version` and `nova-sprint help` are exec-blocked (blocker 1), so
the build of nova-sprint v1.0.0 running on the live system could not be read.

## Commands run (each read-only; each with its full output)

Every nova invocation ran as
`NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan timeout 300 <tool> ...`, one at
a time; no output line contained the word SECRETS, so nothing was dropped by a filter.
All times UTC.

| Stamp | Command | Full output |
| --- | --- | --- |
| 23:24:29 | `date -u` | `Sun Oct  4 11:24:29 PM UTC 2026` |
| 23:24:29 | `git -C repo rev-parse HEAD` | `bf078ca7d54a07958e51bdb39b9e693fc1010a66` |
| 23:24:29 | `git -C repo rev-parse origin/sprint/mechanical-2026-10-02` | `bf078ca7d54a07958e51bdb39b9e693fc1010a66` |
| 23:24:29 | `timeout 300 nova-sprint --version` | `timeout: failed to execute process: Permission denied (os error 13)` (rc=126) |
| 23:24:29 | `timeout 300 nova-sprint help` | `timeout: failed to execute process: Permission denied (os error 13)` (rc=126) |
| 23:24:29 | `timeout 300 nova-sprint where --json --cards` | `timeout: failed to execute process: Permission denied (os error 13)` (rc=126) |
| 23:24:29 | `timeout 300 nova-sprint stats --json` | `timeout: failed to execute process: Permission denied (os error 13)` (rc=126) |
| 23:24:29 | `timeout 300 nova-sprint log --json --since 2026-10-04T00:00:00Z` | `timeout: failed to execute process: Permission denied (os error 13)` (rc=126) |
| 23:24:29 | `timeout 300 nova-sprint help friend` | `timeout: failed to execute process: Permission denied (os error 13)` (rc=126) |
| 23:24:29 | `timeout 300 nova-bus help` | `timeout: failed to execute process: Permission denied (os error 13)` (rc=126) |
| 23:24:29 | `timeout 300 nova-bus log --max 0` | `timeout: failed to execute process: Permission denied (os error 13)` (rc=126) |
| 23:24:29 | `timeout 300 nova-bus2 log` | `timeout: failed to execute process: No such file or directory (os error 2)` (rc=127) |
| 23:24:29 | `curl -s -m 8 -o /dev/null -w 'http_code=%{http_code}' http://127.0.0.1:6390/` | `http_code=000` (curl exit 7, connection refused) |
| 23:24:29 | `curl -s -m 8 http://127.0.0.1:7390/api/sprint` | empty body (HTTP/1.1 200 OK, `Server: Caddy`, `Content-Length: 0`) |
| 23:24:29 | `bash -c 'exec 3<>/dev/tcp/127.0.0.1/6390'` | `connect: Connection refused` (exit 1) |
| 23:24:29 | `ss -ltn` | no listener on 6390; `*:7390` Caddy only |

Raw transcript: `scratch/blocker-evidence-w2.txt` in the job directory
(`accept-friends-e2e.w2/scratch/blocker-evidence-w2.txt`), kept outside the repository per
PATHS.

## Per criterion

The PASS line names two criteria. Neither was measured.

- **E2E probe (bus message -> turn in the open chat -> reply) passes for every friend not
  held, 10 of 10, for 2 hours.** Not measured. Samples taken 0, samples passing 0.
  Bar: 10 of 10 per friend. Worst value: none. The friends not held could not be named
  (blocker 2) and no probe could be sent or read (blockers 1 and 4). No raw sample line
  decided this criterion, because no sample line exists.
- **Presence matches each friend's own count (status, working count).** Not measured.
  Samples taken 0, samples matching 0. The friends table (`where --json`) and the
  dashboard are both unreadable (blockers 1 and 5), and no friend was asked to state its
  working count in a probe reply (blocker 4). No raw sample line decided this criterion.

Passing sample lines: none. Failing sample lines: none — the failure is total and sits
before the sampling loop, so there are no per-sample lines, passing or failing, to quote.
The deciding raw lines are the exec refusal (blocker 1) and the empty dashboard/refused
port (blocker 5), both reproduced in full in "Commands run" and in the raw transcript.

## What was not measured

Everything the PASS line names: the friend rows not held, the E2E probe for each, the
10-per-friend count over a 2-hour window, the round trip of each probe, and the match
between the friends table / dashboard presence and each friend's own reported working
count. Also not measured: the build of nova-sprint running live, the window's start and
end, and any read source named by the card (`where --json --cards`, `stats --json`,
`log --json --since`, `nova-bus log --max 0` / `nova-bus2 log`, the dashboard
`curl http://127.0.0.1:7390/api/sprint`). Nothing was started, stopped or killed; no
server was started; no bus message was written. The card's other gates were not run either:
`TestAcceptanceRecordsAreWellFormed` is a placeholder name this card does not run.

## Way forward

The blockers are environmental, not sprint state, and are the same ones attempt 1 recorded:
this record says nothing about the reliability of the friends E2E path. Re-run this card
from a seat whose wall can execute `/home/ubuntu/.local/bin` (read-exec, not read-noexec)
and reach `127.0.0.1:6390`, and whose dashboard returns sprint data. The sentinel
nova-sprint-v1-0-0-acceptance stays unmet: this card lands HOLD, not PASS.
