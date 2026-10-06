# The stopgaps

The coordinator's hand scripts of 2026-10-04 and 2026-10-05, run on the seat's machine while the
verbs that do their jobs were owed. Each row names the script as it runs, what it did, the card
that replaces it, the verb that card makes, and the proof that the verb does the job: the test (the
card's `TEST:`), the commit that landed the card on the base, and one real run of the verb doing
the script's work on the sprint.

A row is **owed** while its card has not landed (Landed `no`), **landed** once it has, and
**retired** once a real run is recorded too. Nothing in the tree is deleted for a stopgap: the
scripts live outside it (the coordinator's scratchpad, and `runner.zsh` in each bud's directory),
and a retired one is removed by stopping it and deleting it there.

`nova-sprint seat check` reads the process table of the machine it runs on (`ps -axww -o
pid=,args=`) and prints, after its `MACHINERY` lines and before the summary, one line for each
stopgap found alive, for as long as it is alive:

```
STOPGAP <name> still running pids=<pid,...> state=<owed|landed|retired> card=<card> verb="<verb>" [remedy="kill <pid> ..."]
```

An owed or landed stopgap's line is a note: its verb is not yet seen doing the job, so the script
still does it. A retired one still running is DOWN, its remedy the `kill`, and the check exits 1
until it is gone. A process is the script when the script is its program, or the first word past
an interpreter's flags (`sh`, `bash`, `zsh`, `dash`, `python*`); a shell's `-c` text, or a `grep`,
`tail` or editor that names a script, is not it (`sprint.StopgapScript`). The server's own check
reads no process table.

The rows are `sprint.Stopgaps` (internal/sprint/seat_check.go). `TestEveryStopgapNamesItsVerbAndProof`
reads this table, holds it to `sprint.Stopgaps` row for row, and refuses a row with no card, no
verb or no test, a landed row whose test is not in the tree, and a real run on a row not landed.

| Stopgap | What it did | Card | Verb | Test | Landed | Real run |
|---|---|---|---|---|---|---|
| runner.zsh | a bud's card runner: one headless claude -p per delivered brief, one frontier card at once | claude-oneshot-lanes | nova-friend run with a claude one-shot lane (the row's CLAUDE_CONFIG_DIR) | TestAClaudeOneShotLaneRunsWalledWithTheRowsConfigDir | e67fdb9da | owed |
| deliver.py | writes each card dealt to a friend into her inbox, staging the checkout first | deliver-is-the-daemons-duty-in-order | nova-sprint deliver <friend> [--once]; the nova-friend daemon's delivery | TestTheDaemonStagesBeforeItWritesTheBrief | no | owed |
| deliver-loop.sh | runs deliver.py for every friend on a timer | deliver-is-the-daemons-duty-in-order | the nova-friend daemon's delivery, every sync | TestTheDaemonStagesBeforeItWritesTheBrief | no | owed |
| note-when-delivered.sh | appends the coordinator's note to a brief once it is delivered | deliver-is-the-daemons-duty-in-order | the nova-friend daemon's delivery, the brief written whole | TestTheDaemonStagesBeforeItWritesTheBrief | no | owed |
| finish-loop.py | finishes each working card whose outbox REPORT.md is written | collect-is-a-verb-and-the-daemons-duty | nova-sprint collect [<friend>...] [--dead-lanes]; the nova-friend daemon's outbox pass | TestCollectFinishesEveryOutboxReportOfAWorkingCard | no | owed |
| graft-audit.py | grafts work from a lane that could not push onto its repo and writes its LAND report | collect-is-a-verb-and-the-daemons-duty | nova-sprint collect, from a job the daemon staged | TestCollectFinishesEveryOutboxReportOfAWorkingCard | no | owed |
| zhi-beat.sh | beats for a friend while her session pongs, because no harness app runs | liveness-is-the-session-pong-not-an-app | the nova-friend daemon's beat, carried by the session pong | TestAFriendWhoseSessionPongsIsUpWithNoHarnessProcess | 3872a11dd | owed |
| twin-widen.py | drops a card refused on PATHS and adds its twin with PATHS widened | twin-is-a-verb | nova-sprint twin <card> --paths <extra,...> | TestTwinReplacesACardAndItsDependentsFollow | no | owed |
| twin-behind.py | drops a card red on its base and adds a twin carrying its branch, after the base fix | twin-is-a-verb | nova-sprint twin <card> --carry --needs <fix> | TestTwinReplacesACardAndItsDependentsFollow | no | owed |
| seat-model.py | prints the coordinator's model of the sprint from the dashboard's JSON | view-seat-is-the-coordinators-model | nova-sprint view seat [--json] | TestViewSeatIsTheDashboardsOwnSnapshot | no | owed |

Landed is the commit that landed the card on the base, or `no`. Real run is `owed` until a run of
the verb doing the script's job is recorded: where and when it ran, and what it did. A row moves on
by writing its landing commit, then its run, here and in `sprint.Stopgaps` together; a test name
of a card not yet landed is the card's `TEST:` as written on its branch.
