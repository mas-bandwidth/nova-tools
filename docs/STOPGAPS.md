# The stopgaps, and the verbs that retire them

The coordinator ran these scripts on the seat's machine on 2026-10-04 and 2026-10-05 to keep the sprint
moving while the verbs that do their jobs were built. They live outside the tree; nothing here
deletes them. A stopgap is retired when its verb has landed and this table carries the proof that
the verb does the job: the test that pins it and one real run of it. Until then the row says what
is owed, and the stopgap keeps running.

`nova-sprint seat check` reads the process table of the machine it runs on (the seat's) and
prints, after the MACHINERY lines, one line for each stopgap it finds running:

```
STOPGAP <name> still running pid=<pid,...> card=<card> verb="<verb>" note="its verb is owed: ..."
STOPGAP <name> still running pid=<pid,...> card=<card> verb="<verb>" remedy="kill <pid ...>; remove <name> and what starts it"
```

A stopgap whose row is retired and is still running is DOWN (the check exits 1) until it is
removed; one whose verb is owed is a note. A stopgap is found by its file name: the program
itself, or the script an interpreter (sh, bash, zsh, python, python3, env) runs. The served check
(the server answering `seat check` in its own step) reads no process table and prints no STOPGAP
line.

The rows below are `sprint.Stopgaps` (internal/sprint/seat_check.go), in the same order, and
`TestEveryStopgapNamesItsVerbAndProof` (internal/sprint/stopgaps_test.go) holds the two together.
It refuses a row with no card or no verb; a retired row with no test, a test not found in the
package it names, or no real run; and an owed row that does not say what is owed. Retiring a
stopgap is one change: fill Test and Real run, set Status to `retired`, and set `Retired: true`
on its `sprint.Stopgaps` entry.

Test is `<package> <TestName>`; Real run is the command run on a real seat or friend, the date,
and the line it printed.

| Stopgap | Card | Verb | Test | Real run | Status |
|---|---|---|---|---|---|
| runner.zsh | claude-oneshot-lanes | nova-friend run --harness claude (one-shot lanes) | ./internal/friend TestClaudeOneShotLanePassesConfigDir | - | owed: a real run; the verb landed (e67fdb9da) but the buds still run cards through runner.zsh and their daemons read mode=batch (nova-friend status of a bud, 2026-10-06 07:50 EDT) |
| deliver.py | deliver-is-the-daemons-duty-in-order | nova-friend run; nova-sprint deliver <friend> [--once] | ./internal/friend TestTheDaemonStagesBeforeItWritesTheBrief | - | owed: the card has not landed on sprint/mechanical-2026-10-02 |
| deliver-loop.sh | deliver-is-the-daemons-duty-in-order | nova-friend run; nova-sprint deliver <friend> [--once] | ./internal/friend TestTheDaemonStagesBeforeItWritesTheBrief | - | owed: the card has not landed on sprint/mechanical-2026-10-02 |
| finish-loop.py | collect-is-a-verb-and-the-daemons-duty | nova-sprint collect | - | - | owed: the card has not landed on sprint/mechanical-2026-10-02, and its test is not named yet |
| zhi-beat.sh | liveness-is-the-session-pong-not-an-app | nova-friend run (a friend is up on her session's pong) | ./internal/friend TestAFriendWhoseSessionPongsIsUpWithNoHarnessProcess | - | owed: a real run; the verb landed (3872a11dd) and no run of zhi's daemon up on her pong alone is recorded |
| note-when-delivered.sh | deliver-is-the-daemons-duty-in-order | nova-friend run; nova-sprint deliver <friend> [--once] | ./internal/friend TestTheDaemonStagesBeforeItWritesTheBrief | - | owed: the card has not landed on sprint/mechanical-2026-10-02 |
| twin-widen.py | twin-is-a-verb | nova-sprint twin <card> --paths <extra,...> | ./internal/sprint TestTwinReplacesACardAndItsDependentsFollow | - | owed: the card has not landed on sprint/mechanical-2026-10-02 |
| twin-behind.py | twin-is-a-verb | nova-sprint twin <card> --before <card> | ./internal/sprint TestTwinReplacesACardAndItsDependentsFollow | - | owed: the card has not landed on sprint/mechanical-2026-10-02 |
| graft-audit.py | twin-is-a-verb | nova-sprint twin <card> --carry | ./internal/sprint TestTwinReplacesACardAndItsDependentsFollow | - | owed: the card has not landed on sprint/mechanical-2026-10-02 |
| seat-model.py | view-seat-is-the-coordinators-model | nova-sprint view seat --json | ./cmd/nova-sprint TestViewSeatIsTheDashboardsOwnSnapshot | - | owed: the card has not landed on sprint/mechanical-2026-10-02 |
