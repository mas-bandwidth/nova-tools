# The stopgaps

The coordinator's hand scripts of 2026-10-04 and 2026-10-05, run on the seat's machine while
the verbs that do their jobs were owed. Each row names the script as it runs, the
card that replaces it, the verb that card makes, and the proof that the verb does the job: the
test (the card's `TEST:`) and one real run of the verb doing the script's work on the sprint.

A row's state is **owed** while its card has not landed on the base, **landed** once it has, and
**retired** once a real run is recorded as well. Nothing in the tree is deleted for a stopgap: the
scripts live outside it (the coordinator's scratchpad, and `runner.zsh` in each bud's directory),
and a retired one is removed by stopping it and deleting it there.

The seat check's report (`sprint.JudgeSeatCheck`), given the seat's machine's process table,
prints after its `MACHINERY` lines one line per stopgap found alive:

```
STOPGAP <name> still running pids=<pid,...> state=<owed|landed|retired> card=<card> verb="<verb>" [remedy="kill <pid> ..."]
```

An owed or landed stopgap's line is a note (the verb is not yet proven, so the script still does
the job); a retired one still running is DOWN, its remedy the `kill`, and the check exits 1 until
it is gone. A process is the script when the script is its program, or the first word after an
interpreter's flags (`sh`, `bash`, `zsh`, `dash`, `python*`); a shell whose `-c` text names a
script, or a `find` or `tail` naming one, is not it (`sprint.StopgapScript`).

Owed: `nova-sprint seat check` does not yet read the process table. The judging, the parse of
`ps -axww -o pid=,args=` (`sprint.ProcsFromPS`) and the line are in `internal/sprint`; the
command's outside that runs `ps` and fills the measure (`cmd/nova-sprint/machinery.go`) is outside
the PATHS of card the-stopgaps-retire and is a twin's work. Until it lands a real seat check prints
no `STOPGAP` line.

The rows are `sprint.Stopgaps` (internal/sprint/seat_check.go). `TestEveryStopgapNamesItsVerbAndProof`
reads this table, holds it to `sprint.Stopgaps` row for row, and refuses a row with no card, no
verb or no test, a landed row whose test is not in the tree, and a retired row with no real run.

| Stopgap | What it did | Card | Verb | Test | Landed | Real run |
|---|---|---|---|---|---|---|
| runner.zsh | a bud's card runner: one headless `claude -p` per dealt brief, at most one frontier card at once, pauses the daemon on a usage limit | claude-oneshot-lanes | nova-friend run --harness claude (one-shot lanes, the row's config dir) | TestAClaudeLaneRunsEachCardAsAProcessAndReadsItsOutbox | e67fdb9da | owed: 23 runner.zsh processes still run every bud's cards (2026-10-06 07:58) |
| deliver.py | writes each card held on a friend's row into that friend's inbox in the daemon's layout, staging the checkout before the brief | deliver-is-the-daemons-duty-in-order | nova-sprint deliver <friend> [--once]; the nova-friend daemon's delivery | TestTheDaemonStagesBeforeItWritesTheBrief | no | owed |
| deliver-loop.sh | runs deliver.py for every friend every 20 s | deliver-is-the-daemons-duty-in-order | the nova-friend daemon's delivery, every sync | TestTheDaemonStagesBeforeItWritesTheBrief | no | owed |
| note-when-delivered.sh | appends the coordinator's note to a brief once it is delivered | deliver-is-the-daemons-duty-in-order | the delivered brief carries the coordinator's notes (the packet's notes) | TestTheDaemonStagesBeforeItWritesTheBrief | no | owed |
| finish-loop.py | every 30 s, finishes each working card whose outbox REPORT.md is written (LAND with its Head, HOLD or FAIL as failed) | collect-is-a-verb-and-the-daemons-duty | nova-sprint collect [<friend>...] [--dead-lanes]; the nova-friend daemon's collection | TestCollectFinishesEveryOutboxReportOfAWorkingCard | no | owed |
| graft-audit.py | grafts an audit written in a lane that could not push onto its repo, pre-writes the LAND report and twins the card | collect-is-a-verb-and-the-daemons-duty | nova-sprint collect, from a job the daemon staged (deliver-is-the-daemons-duty-in-order) | TestCollectFinishesEveryOutboxReportOfAWorkingCard | no | owed |
| zhi-beat.sh | beats for Zhi while her session's pong is fresh, because no harness app runs | liveness-is-the-session-pong-not-an-app | the nova-friend daemon beats while the session pongs, with no harness app | TestAFriendWhoseSessionPongsIsUpWithNoHarnessProcess | 3872a11dd | owed: zhi-beat.sh still runs (2026-10-06 07:58) |
| twin-widen.py | drops a card refused on PATHS and adds its twin with PATHS widened | twin-is-a-verb | nova-sprint twin <card> --paths <extra,...> | TestTwinReplacesACardAndItsDependentsFollow | no | owed |
| twin-behind.py | drops a card red on its base and adds a twin carrying the branch that waits for the base fix | twin-is-a-verb | nova-sprint twin <card> --carry --needs <card> | TestTwinReplacesACardAndItsDependentsFollow | no | owed |
| seat-model.py | prints the coordinator's model of the sprint from the dashboard's JSON | view-seat-is-the-coordinators-model | nova-sprint view seat --json | TestViewSeatIsTheDashboardsOwnSnapshot | no | owed |

The Landed column is the commit that landed the card on the base, or `no`; the Real run column is
`owed` (with what was seen, when anything was) until a run of the verb doing the job is recorded,
as the place and time it ran and what it did. A row moves to retired by writing that run here and
in `sprint.Stopgaps` together.
