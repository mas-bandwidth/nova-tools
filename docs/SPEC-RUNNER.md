# nova-runner

`nova-runner` keeps one friend at her row's width. It is a Go program. It runs
a one-shot harness (opencode, claude, or another command on `PATH`) and it does
not ask a coordinator to decide the next lane. The code is `cmd/nova-runner`
(`width.go` is the state machine, `runner.go` is the process, `main.go` is the
verb). The checks are `TestWidth16With10BusyFillsSixWorkOrTwelveReads`,
`TestWidthLoweredDoesNotKillLanes`,
`TestNoReportFailsWithHarnessFaultAndThreeAlikeRaiseOneJudgment`,
`TestRunnerVersionChangeDrainsThenExecs`, `TestTickFillsFailsAndFollowsWithoutAClock`
and `TestHelpExplainsTheLoop`.

The machine is pure. `Next` takes a `World` and returns a `Step`. It does not
read a clock, a socket, or a file. `RowDue` compares two times the caller
passes. The tests pass fixed times and do not sleep.

## The loop

Every second (`Runner.Tick`, and `Runner.Run` which calls it on a one-second
ticker):

1. **Width.** `nova-config friend show <friend>` at most every 10s
   (`RowEvery`, `RowDue`). The row's `width` is the only max. `tiers`, `mode`
   and `runner_version` are read with it (`ParseRow`). A width change is the
   next `Next`: new lanes start only while the busy half-slots are under the
   new width, and a lane that is already running stays until it ends. Nothing
   in `Step` kills a lane.

2. **Fill.** While busy half-slots are under `2 * width`, take cards from
   `nova-sprint queue --as friend.<friend> --json` in that JSON's order. The
   sprint puts reads first; the runner does not reorder. A work lane costs two
   half-slots and a read lane costs one, so width 16 with eight work lanes and
   four reads busy (ten slots, twenty half-slots) takes six more work cards or
   twelve reads. A card that does not fit is left on the queue and a later card
   that does fit may start, so a leftover half-slot can take a read. Columns
   `ready`, `working`, `reading` and `asked` are her queue; any other column is
   not. The runner stages with `friend.Stager` (`jobs/<job>/JOB.md` and the
   checkout, the same stage nova-friend uses) and writes `inbox/<job>/BRIEF.md`
   from the packet when that file is absent. It then runs the harness one-shot
   (`harnessArgv`): `opencode run [--model <m>] <prompt>`, or
   `claude -p <prompt> [--model <m>]`. The model is the card's, else
   `--model-<tier>` for that tier. The card's deadline, when it names one, is a
   `SIGTERM` to that lane's own session (`capLane`) and to no other process.
   The child is told to write `outbox/<job>/REPORT.md` and `RESULT.md`. Finish
   is `nova-sprint finish --as friend.<friend> <card>@<gen> --epoch <n>` with
   `--head` for a `LAND` whose head is a 40-hex sha, and `--failed` otherwise
   (`finishArgv`, `landOf`).

3. **Beat.** `nova-sprint friend beat <friend> --working <n> --queue <m>
   --width <w> --running <ids>` at the end of every tick (`beatArgv`).
   `--working` is the number of lanes, not a guess. `--queue` is the cards in
   this tick's queue that are not lanes. `--width` is the row's width and is
   omitted only when the row has not named one, because `friend beat` refuses a
   width under 1. Nothing else beats for her.

4. **Report.** A lane that ends with no `REPORT.md`, or with one that has no
   `Verdict:` line, finishes `FAIL` with `harness fault: <first error line>`
   (`HarnessFault`). An empty error line is `harness fault: (no error line)`.
   Three of the same line in a row, with no report between them, raise one
   judgment (`Judgment`). A fourth in that same streak does not raise another.
   A different line, or a lane that did write a report, starts a new streak.
   The judgment is `nova-bus send --as <friend> --to <seat>`. If the send
   fails, the next tick raises it again. The runner does not stop.

5. **Follow.** When `runner_version` is set and is not this binary's
   (`-X main.version`, default `dev`), `Follow` is true. `Next` starts nothing
   and sets `Drain`. Lanes already running are not killed. When the last one
   has ended, `Exec` is that version: `nova-update install nova-runner@<version>`
   and then `exec` of `nova-runner` on `PATH` with the same arguments
   (`execInstalled`). A `runner_version` of empty or `-` is the row naming
   none, and the runner does not follow. Each harness is started with
   `Setsid`, so it is its own session leader. A signal to the runner, and the
   exec, do not signal the lane. The runner execs only after its lanes have
   ended; the session is what keeps a lane alive if the runner is signalled
   while it is draining.

`mode` is recorded and is not a second cap. The width is the only max. `tiers`
is recorded and is not a second filter: a card the queue did not list is not
started, and a card it did list is not refused here for its tier.

A finish the sprint refuses stays pending and is retried next tick. Pending
cards are not started again. The state file `<dir>/runner/state.json` keeps the
lanes (pid and card), the pending finishes, the fault streak and the last row,
so a restart adopts a live pid instead of starting the card twice.

## The row

`nova-config friend show <friend>` prints one `FRIEND` line of `key=value`
fields. The runner reads:

| field | use |
|---|---|
| `width` | the only max, a whole number of at least 1. No width is a refusal to start, not a default. |
| `tiers` | recorded. Comma-separated. `-` is none. |
| `mode` | recorded (`one-shot` or `batch`). Not a cap. |
| `runner_version` | when it differs from the binary, drain and exec. `-` or absent is none. |

## What it never does

- It never keeps a width of its own. There is no flag for a max, and a missing
  row width does not become 1.
- It never starts a card the queue did not give her. It does not call `take`,
  and it does not scan a directory for briefs.
- It never kills a lane because the width dropped, because a version changed,
  or because the runner is stopping. The only signal it sends a lane is that
  lane's own deadline.
