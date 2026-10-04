# Acceptance record — nova-sprint v1.0.0, check 5 of 6: installs

Card: accept-installs.w2, attempt 2, epoch 15, stream sprint-next. Delivered as a sprint job
per docs/FRIENDS.md to Rowan's friend; harness: opencode, model z-ai/glm-5.3-flash (tier
flash), inside the job's landlock wall. Repository mas-bandwidth/nova-tools, branch
sprint/accept-installs.w2.g4.e15 at bf078ca7d54a07958e51bdb39b9e693fc1010a66, verified
equal to the remote tip of origin/sprint/mechanical-2026-10-02 by `git rev-parse` this
attempt.

Attempt 1 (accept-installs.w1, pushed 2d5626f02b31528084e55e0de7f0cd8002a9df6e to
sprint/accept-installs.w1.g1.e15) held on the same wall; this attempt re-ran every read
from scratch and the blockers are unchanged.

## Verdict: HOLD

The measurement could not be run. The wall this card runs inside refuses to execute every
nova tool and cannot reach the sprint server, so Rowan's release reason, the window-open
check, the install and the rollback were all unreadable again. Nothing was measured; no
install or rollback was attempted, by verb or by hand.

## Blockers, precisely

1. Exec blocked for every nova tool. Each of `nova-sprint --version`, `nova-sprint where
   --json`, `nova-sprint log --card accept-installs`, `nova-sprint help`, `nova-bus log
   --max 0` and `nova-update help` fails before any verb runs, always the same way:
   `timeout: failed to execute process: Permission denied (os error 13)`. The binaries
   live under `/home/ubuntu/.local/bin`, which the wall keeps read-noexec; the harness's
   sandbox note reads `backend=landlock ... read=3 read-noexec=2 net=nopromise` (abi 8
   clamped to 6). Re-verified this attempt at 23:20:50 UTC, one command at a time.
2. The window never opened. It opens only when Rowan's release reason for this card
   (`nova-sprint log --card accept-installs`) says the install window is open; that read is
   one of the exec-blocked calls (blocker 1), so the precondition could not be checked and
   the 30-minute window was not opened. Per the card it was not stretched or restarted.
3. The sprint server answered nothing. TCP to `127.0.0.1:6390` (NOVA_SPRINT_SERVER for
   every verb) is refused: `connect: Connection refused` (probe exit 1); `ss -tln` shows no
   listener on 6390. The dashboard read `curl -s http://127.0.0.1:7390/api/sprint` returned
   HTTP 200 with `Content-Length: 0` from Caddy — a connection, but no sprint data in the
   body.

## Window

Start: none (never opened; blocker 2). End: none. First evidence stamp:
`Sun Oct  4 11:20:42 PM UTC 2026`; last evidence stamp:
`Sun Oct  4 11:20:57 PM UTC 2026`. No sample was taken; no sample passed; there is no
worst value, because there is no value.

## Build measured

Unknown. `nova-sprint --version` and `nova-sprint where --json` are exec-blocked
(blocker 1), and the build named in the release reason is unreadable with them.

## Commands run (each read-only; each with its full output)

Every nova invocation ran as
`NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan timeout 300 <tool> ...`, one at
a time, output filtered with `grep -v SECRETS` (no line was dropped by the filter). All
times UTC.

| Stamp | Command | Full output |
| --- | --- | --- |
| 23:20:42 | `date -u; git rev-parse HEAD; git rev-parse origin/sprint/mechanical-2026-10-02` | `Sun Oct  4 11:20:42 PM UTC 2026` / `bf078ca7d54a07958e51bdb39b9e693fc1010a66` / `bf078ca7d54a07958e51bdb39b9e693fc1010a66` |
| 23:20:50 | `timeout 300 nova-sprint --version` | `timeout: failed to execute process: Permission denied (os error 13)` |
| 23:20:50 | `timeout 300 nova-sprint where --json` | `timeout: failed to execute process: Permission denied (os error 13)` |
| 23:20:50 | `timeout 300 nova-sprint log --card accept-installs` | `timeout: failed to execute process: Permission denied (os error 13)` |
| 23:20:50 | `timeout 300 nova-sprint help \| head -1` | `timeout: failed to execute process: Permission denied (os error 13)` |
| 23:20:50 | `timeout 300 nova-bus log --max 0` | `timeout: failed to execute process: Permission denied (os error 13)` |
| 23:20:50 | `timeout 300 nova-update help` | `timeout: failed to execute process: Permission denied (os error 13)` |
| 23:20:56 | `curl -s -m 10 http://127.0.0.1:7390/api/sprint` | empty body (curl verbose: `HTTP/1.1 200 OK`, `Server: Caddy`, `Content-Length: 0`) |
| 23:20:57 | `exec 3<>/dev/tcp/127.0.0.1/6390` | `connect: Connection refused` |

Raw transcript: `scratch/blocker-evidence-w2.txt` in the job directory
(`accept-installs.w2/scratch/blocker-evidence-w2.txt`), kept outside the repository per
PATHS.

## Per criterion

The PASS line, verbatim: "a server install and a rollback each done by the verbs, gated by
the land self-test."

- Install of the server build named in the release reason, by the verb: not measured.
  Samples taken 0, samples passing 0. The install verbs exist to be found in
  `nova-sprint help` / `nova-update help`; both reads are exec-blocked (blocker 1).
- Rollback to the build that was running before, by the verb: not measured.
  Samples taken 0, samples passing 0.
- Land self-test gating each: not measured. Its name, its result line and the proof that a
  failing self-test would refuse (the verb's help or its log line) are all reads through
  the blocked binaries.
- Time the server was unanswering: not measured. No install or rollback ran, so no
  unanswering interval exists to time.

## What was not measured

Everything the PASS line names: the install, the rollback, the land self-test each verb
runs and its refusal on failure, the build before and after each step, and the time the
server was unanswering; plus the window reads themselves (`where --json`, `stats --json`,
`log --json --since`, `nova-bus log --max 0`, the dashboard). Nothing was started, stopped
or killed; no server was started; the live system was not touched. The card's other gates
were not run either: `TestAcceptanceRecordsAreWellFormed` is a placeholder name this card
does not run.

## Way forward

The blockers are environmental, not sprint state, and are the same two attempt 1 recorded:
this record says nothing about the reliability of nova-sprint installs. Re-run this card
from a seat whose wall can execute `/home/ubuntu/.local/bin` (read-exec, not read-noexec)
and reach `127.0.0.1:6390`, or have the member read `nova-sprint log --card
accept-installs` from outside the wall and state whether the install window is open before
re-dealing. The sentinel nova-sprint-v1-0-0-acceptance stays unmet: this card lands HOLD,
not PASS.
