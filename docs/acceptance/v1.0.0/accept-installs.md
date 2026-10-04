# Acceptance record — nova-sprint v1.0.0, check 5 of 6: installs

Card: accept-installs.w3, attempt 3, epoch 15, stream sprint-next. Delivered as a sprint job
per docs/FRIENDS.md to Rowan's friend; harness: opencode, model opencode/qwen3.8-max (tier
pro), inside the job's landlock wall. Repository mas-bandwidth/nova-tools, branch
sprint/accept-installs.w3.g3.e15 at bf078ca7d54a07958e51bdb39b9e693fc1010a66, verified
equal to the remote tip of origin/sprint/mechanical-2026-10-02 by `git rev-parse` and
`git ls-remote` this attempt (the checkout came pre-staged; per JOB.md nothing was cloned).

Attempt 1 (accept-installs.w1, pushed 2d5626f02b31528084e55e0de7f0cd8002a9df6e to
sprint/accept-installs.w1.g1.e15) and attempt 2 (accept-installs.w2, pushed
bf3ec1922405682accd0e8ae78ac3501f90bef3a to sprint/accept-installs.w2.g4.e15 — both tips
re-verified by `git ls-remote` this attempt) held on the same wall. This attempt re-ran
every read from scratch, one at a time, and the blockers are unchanged.

## Verdict: HOLD

The measurement could not be run. The wall this card runs inside refuses to execute every
nova tool, and the sprint server is not there to be measured: Rowan's release reason, the
window-open check, the install and the rollback were all unreadable or unrunnable again.
Nothing was measured; no install or rollback was attempted, by verb or by hand; nothing was
started, stopped or killed.

## Blockers, precisely

1. Exec blocked for every nova tool. Each of `nova-sprint --version`, `nova-sprint help`,
   `nova-sprint where --json` (with and without `--cards`), `nova-sprint stats --json`,
   `nova-sprint log --card accept-installs`, `nova-sprint log --json --since ...`,
   `nova-bus log --max 0` and `nova-update help` fails before any verb runs: through the
   card's wrapper as `timeout: failed to execute process: Permission denied (os error 13)`
   (exit 126), invoked directly as
   `/usr/bin/bash: line 1: /home/ubuntu/.local/bin/nova-sprint: Permission denied`
   (exit 126). `file` reports all three binaries as `writable, executable, regular file,
   no read permission`, and even reading the first bytes is refused — the denial is the
   wall's, not the mode bits'. `/home/ubuntu/.local/bin` cannot even be listed. The
   harness's sandbox note reads `backend=landlock abi=8 used=6 read=3 read-noexec=2
   net=nopromise` (abi 8 clamped to 6).
2. The window never opened. It opens only when Rowan's release reason for this card
   (`nova-sprint log --card accept-installs`) says the install window is open; that read is
   one of the exec-blocked calls (blocker 1) and the only other live source, the dashboard,
   returned an empty body (blocker 3), so the precondition could not be checked and the
   30-minute window was not opened. Per the card it was not stretched or restarted.
3. The sprint server is not there. TCP to `127.0.0.1:6390` (NOVA_SPRINT_SERVER for every
   verb) is refused: `curl -s -m 5 http://127.0.0.1:6390/api/sprint` exits 7 and the raw
   probe `exec 3<>/dev/tcp/127.0.0.1/6390` fails `connect: Connection refused`. `ss -tln`
   lists every listener on the machine and none is on 6390 (7390 is there). The dashboard
   read `curl -s http://127.0.0.1:7390/api/sprint` returned HTTP 200 from Caddy with
   `Content-Length: 0`; the body measured 0 bytes — a connection, but no sprint data.
4. No alternative path rescues the measurement. `nova-bus2` does not exist on this machine
   (`No such file or directory`, exit 127 — the rename has fully reached). Raw state reads
   (a redis-cli exists and 6379 listens) are not a source this card sanctions, and were not
   used. Even if the release reason were readable, the install and rollback could not run:
   the verbs are exec-blocked (blocker 1), the server to install against is absent
   (blocker 3), and the card forbids starting a server or substituting a hand step. Network
   to GitHub works from inside the wall (`git ls-remote`, `git fetch` succeed), so the wall
   blocks the nova tooling and the local server port specifically — this record itself
   ships normally.

## Window

Start: none (never opened; blocker 2). End: none. First evidence stamp:
`Sun Oct  4 11:24:19 PM UTC 2026`; last evidence stamp:
`Sun Oct  4 11:26:07 PM UTC 2026`. No sample was taken; no sample passed; there is no
worst value, because there is no value.

## Build measured

Unknown. `nova-sprint --version` and `nova-sprint where --json` are exec-blocked
(blocker 1), and the build named in the release reason is unreadable with them.

## Commands run (each read-only; each with its full output)

Every nova invocation ran as
`NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan timeout 300 <tool> ...`, one at
a time, output filtered with `grep -v SECRETS` (no line was dropped by the filter). All
times UTC on Sun Oct 4 2026.

| Stamp | Command | Full output |
| --- | --- | --- |
| 23:24:19 | `date -u` (first evidence line) | `Sun Oct  4 11:24:19 PM UTC 2026` |
| 23:24:19 | `NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan timeout 300 nova-sprint --version` | `timeout: failed to execute process: Permission denied (os error 13)`, exit 126 |
| 23:24:19 | `NOVA_SPRINT_SERVER=127.0.0.1:6390 NOVA_SPRINT_ACTOR=rowan timeout 300 nova-sprint help \| head -1` | `timeout: failed to execute process: Permission denied (os error 13)` |
| 23:24:19 | `nova-sprint --version` (direct, no wrapper) | `/usr/bin/bash: line 1: /home/ubuntu/.local/bin/nova-sprint: Permission denied`, exit 126 |
| 23:24:19 | `git rev-parse --abbrev-ref HEAD; git rev-parse HEAD; git rev-parse origin/sprint/mechanical-2026-10-02` | `sprint/accept-installs.w3.g3.e15` / `bf078ca7d54a07958e51bdb39b9e693fc1010a66` / `bf078ca7d54a07958e51bdb39b9e693fc1010a66` |
| 23:24:28 | `timeout 300 nova-sprint log --card accept-installs` (the release-reason read) | `timeout: failed to execute process: Permission denied (os error 13)`, exit 126 |
| 23:24:28 | `timeout 300 nova-sprint where --json` | `timeout: failed to execute process: Permission denied (os error 13)`, exit 126 |
| 23:24:28 | `timeout 300 nova-sprint where --json --cards` | `timeout: failed to execute process: Permission denied (os error 13)`, exit 126 |
| 23:24:28 | `timeout 300 nova-sprint stats --json` | `timeout: failed to execute process: Permission denied (os error 13)`, exit 126 |
| 23:24:28 | `timeout 300 nova-sprint log --json --since "Sun Oct  4 11:24:11 PM UTC 2026"` | `timeout: failed to execute process: Permission denied (os error 13)`, exit 126 |
| 23:24:28 | `timeout 300 nova-sprint help` (install/rollback verb discovery) | `timeout: failed to execute process: Permission denied (os error 13)`, exit 126 |
| 23:24:28 | `timeout 300 nova-bus log --max 0` | `timeout: failed to execute process: Permission denied (os error 13)`, exit 126 |
| 23:24:28 | `timeout 300 nova-bus2 log` (pre-rename name) | `timeout: failed to execute process: No such file or directory (os error 2)`, exit 127 |
| 23:24:28 | `timeout 300 nova-update help` (release install path discovery) | `timeout: failed to execute process: Permission denied (os error 13)`, exit 126 |
| 23:24:33 | `curl -s -m 5 http://127.0.0.1:6390/api/sprint` | empty; exit 7 (connection refused) |
| 23:24:33 | `exec 3<>/dev/tcp/127.0.0.1/6390` | `/usr/bin/bash: connect: Connection refused`, exit 1 |
| 23:24:34 | `curl -sv -m 5 http://127.0.0.1:7390/api/sprint` | `HTTP/1.1 200 OK`, `Server: Caddy`, `Content-Length: 0`, empty body |
| 23:24:34 | `curl -s -m 5 http://127.0.0.1:7390/api/sprint \| wc -c` | `0` |
| 23:24:34 | `ss -tln` | 21 listeners, none on 6390; `*:7390` present (full listing in the transcript) |
| 23:24:34 | `ls -la /home/ubuntu/.local/bin` | `ls: cannot open directory '/home/ubuntu/.local/bin': Permission denied` |
| 23:24:34 | `file .../nova-sprint .../nova-bus .../nova-update` | each: `writable, executable, regular file, no read permission` |
| 23:24:34 | `head -c 100 /home/ubuntu/.local/bin/nova-sprint` | `head: cannot open ... for reading: Permission denied` |
| 23:24:34 | `command -v nova-sprint nova-bus nova-bus2 nova-update` | the three under `/home/ubuntu/.local/bin`; `nova-bus2` absent |
| 23:24:34 | sandbox note (`harness-output.log`, SANDBOX lines) | `backend=landlock abi=8 used=6 read=3 read-noexec=2 write=4 net=nopromise` (abi 8 clamped to 6) |
| 23:26:06 | `git ls-remote origin` (mechanical tip, this and both prior card branches) | `2d5626f02...` w1, `bf3ec1922...` w2, `bf078ca7d5...` mechanical; this card's branch not yet on origin |
| 23:26:07 | `date -u` (last evidence stamp) | `Sun Oct  4 11:26:07 PM UTC 2026` |

Raw transcript: `scratch/blocker-evidence-w3.txt` in the job directory
(`accept-installs.w3/scratch/blocker-evidence-w3.txt`), kept outside the repository per
PATHS.

## Per criterion

The PASS line, verbatim: "a server install and a rollback each done by the verbs, gated by
the land self-test."

- Install of the server build named in the release reason, by the verb: not measured.
  Samples taken 0, samples passing 0, no worst value. The verb-discovery reads
  (`nova-sprint help`, `nova-update help`) are exec-blocked and the build name lives in the
  unreadable release reason. Deciding failing lines, in full:
  `timeout: failed to execute process: Permission denied (os error 13)` and
  `/usr/bin/bash: connect: Connection refused`. Passing lines: 0 (none exist).
- Rollback to the build that was running before, by the verb: not measured. Samples taken
  0, samples passing 0, no worst value. Same deciding lines as above; there was no install
  to roll back from.
- Land self-test gating each, with its pass recorded: not measured. Samples taken 0,
  samples passing 0. The self-test's name, its result line and the proof that a failing
  self-test would refuse (the verb's help or its log line) are all reads through the
  blocked binaries.
- Server back on the original build at the end: not measured. Samples taken 0, samples
  passing 0. No build before or after exists to compare; `nova-sprint --version` and
  `where --json` are exec-blocked.
- Time the server was unanswering: not measured. No install or rollback ran, so no
  unanswering interval exists to time. (The server's total absence, blocker 3, is a wall
  fact, not a measured install outage, and is not offered as one.)

## What was not measured

Everything the PASS line names: the install, the rollback, the land self-test each verb
runs and its refusal on failure, the build before and after each step, and the time the
server was unanswering; plus the window reads themselves (`where --json`, `stats --json`,
`log --json --since`, `nova-bus log --max 0`, the dashboard's sprint data — the dashboard
answered with 0 bytes). Nothing was started, stopped or killed; no server was started; no
hand step was substituted; the live system was not touched. The card's nominal gate
`TestAcceptanceRecordsAreWellFormed` does not exist in this checkout (`grep` over
internal/ci finds no such test) and per the card is a placeholder this record card does not
run; the record is judged by its evidence.

## Way forward

The blockers are environmental, not sprint state: this record says nothing about the
reliability of nova-sprint installs, and three attempts on three seats have now hit the
identical wall. Re-run this card from a seat whose wall can execute `/home/ubuntu/.local/bin`
(read-exec, not read-noexec) and where `127.0.0.1:6390` has a listener, or have the member
read `nova-sprint log --card accept-installs` from outside the wall, confirm the install
window is open and name the build, and re-deal with a reachable server. The sentinel
nova-sprint-v1-0-0-acceptance stays unmet: this card lands HOLD, not PASS, and the 1.0.0
release does not go to Glenn on the strength of it.
