# nova-check convergence — specification

Glenn, 2026-09-15: **convergence is the health metric** — the contraction ratio per
stream, every tick. Rowan answered *are we converging?* by hand on 2026-09-18: six
windows read out of six different places, an hour of it, and the answer was a
paragraph nobody could diff against the next one. This verb is that hour, mechanised.

`docs/SPEC.md`'s **Conventions** govern — exit codes, the one-line grammar, the field
law, no guessed paths — and [SPEC.md's `## nova-check`](SPEC.md) holds the record-layer
verbs this file does not restate. `help` prints this line, byte for byte:

```
nova-check convergence --repo <owner/name> --ledger <md> --receipts <dir> --retired <file> --since <RFC3339|24h>
```

## What a stream is

A **stream** is one number that a converging family drives in one direction, measured
now and measured at `--since`. Its **ratio** is always `now / before`, whichever way the
stream is meant to move; its **trend** is `contracting` when the number moved the way
that stream converges, `widening` when it moved the other way, `flat` when it did not
move. Seven streams, each read from a real source through a seam, so every test here is
fake-driven and none of them reaches a network, a clock or a bench.

| stream | measure | converging |
| --- | --- | --- |
| `LANDING` | gate rounds per integration batch | fewer |
| `CLASSES` | `## The class tests` entries in `docs/SPEC-CI.md` | more |
| `SCRIPTS` | scripts still in `--bin` | fewer |
| `PRS` | open pull requests | fewer |
| `EDGES` | dogfood edges with no issue filed | fewer |
| `FLEET` | machines off the one build | fewer |
| `LEDGER` | pit-stop ledger rows not yet PASS | fewer |

1. **Every path comes from a flag, and the verb takes no positional argument.**
   `--repo <owner/name>` is the forge repository, `--ledger <md>` the pit-stop ledger,
   `--receipts <dir>` the dogfood receipts, `--retired <file>` the retired-scripts
   README, `--since <RFC3339|24h>` the far edge of the window — an RFC3339 instant, or
   a Go duration meaning *that long before now*. A missing one is *refusing to guess*,
   exit 2, naming every flag that was missing in one run and what each one wants.
2. **A stream whose source was not named is ABSENT, never guessed and never silently
   dropped.** `--bin <dir>` feeds `SCRIPTS`, `--repo-dir <dir>` (a clone of `--repo`)
   feeds `CLASSES`, `--versions <tsv>` and `--certs <tsv>` feed `FLEET`, `--batch-logs
   <dir>` is the second source for `LANDING`'s rounds. Without its source a stream
   prints `now=- before=- ratio=- trend=absent` with the flag that would have fed it,
   is counted on the final line's `absent=` list, and is not counted in `streams=`. A
   stream is never inferred from the working directory.
3. **One line per stream, the four fields first.** `CONVERGENCE <stream> now=<x>
   before=<y> ratio=<r> trend=contracting|flat|widening` and then that stream's own
   `key=value` extras, `measure=` among them. Every value is one whitespace-free token
   through `internal/oneline`. An integral number prints as an integer, a fractional one
   to two decimals, and a number the verb does not have prints `-`.
4. **`LANDING` is the batches and what they cost.** `now` is the mean gate rounds per
   integration batch merged in `[--since, now]`; `before` is the same mean over the
   window of equal length immediately before `--since`. A batch is a merged pull request
   whose title starts `integration-`. Its rounds come from its body — the largest `round
   <n>` it names — or, when `--batch-logs <dir>` is given, from that directory's
   `<pr>-round-<n>.log` files, the largest `<n>` per pull request; the logs win where
   both answer, because a body is written by hand. Extras: `batches=`, `per-hour=`,
   `prev-batches=`, `prev-per-hour=`.
5. **`CLASSES` counts the index, not the tests.** `now` is the number of `###` entries
   under `## The class tests` in `docs/SPEC-CI.md` as it stands; `before` is the same
   count in `git show <sha>:docs/SPEC-CI.md`, `<sha>` the last commit in `--repo-dir` at
   or before `--since`. It is the one stream that converges UPWARDS: a class made
   mechanical is a class that cannot come back, so more entries is `contracting`.
6. **`SCRIPTS` is the finish line the sprint named.** `now` is the scripts left in
   `--bin` — a regular file whose name ends `.sh`, `.py`, `.pl`, `.rb`, `.zsh`, `.bash`,
   or whose first two bytes are `#!`, with `--bin`'s own subdirectories unread;
   `before` is `now` plus the rows of `--retired` dated inside the window. A retired
   row's date is the nearest dated heading or paragraph above it; a row under no date is
   counted in `undated=` and in neither window. Extras: `retired-in-window=`, `undated=`.
7. **`PRS` is the queue, both ends.** `now` is the open pull requests; `before` is the
   pull requests that were open at `--since` — those still open that were created before
   it, plus those created before it and closed inside the window. Extras: `closed=`,
   `opened=`.
8. **`EDGES` is the gate, and the rounds that fed it.** `now` is the open edges: a
   receipt that records an edge (`internal/dogfood`'s rule — not ok, or `Edges:` in the
   notes) with no issue filed. `before` is the open edges among receipts written before
   `--since`. A round is one `--by` name that wrote a receipt inside the window;
   `--by <name>` (repeatable) narrows the reading to those names. Extras: `rounds=`,
   `not-ok=`, `not-ok-per-round=`, `receipts=`.
9. **`FLEET` is one build across the machines.** `--versions <tsv>` is a
   `nova-version snapshot` — the header `name<TAB>stamp<TAB>revision<TAB>platform` — or a
   fleet roll-up of them, the header `machine<TAB>stamp`; either way `now` is the units
   NOT on the majority stamp, so a fleet on one build is zero. `--certs <tsv>`, the
   header `name<TAB>status`, adds `certified=<k/n>`. `before` comes from `--state`'s last
   tick and from nowhere else, because a snapshot is a photograph of one instant.
   Extras: `units=`, `stamps=`, `certified=`.
10. **`LEDGER` is what the pit stop still owes.** `now` is the `--ledger` table rows
    whose result cell is not closed; a cell is closed when it holds `PASS` and holds none
    of `TODO`, `PARTIAL` or `NEEDS WORK`, so `FAIL then PASS` is closed and
    `PARTIAL: …` is not. `before` comes from `--state`'s last tick. Extras: `rows=`,
    `open=`.
11. **The final line is the verdict, and the exit code is the streak.**
    `CONVERGENCE OK|WARN streams=<n> contracting=<k> widening=<list|-> absent=<list|->`.
    `WARN` whenever any measured stream is widening. The exit code is **1 only when a
    stream has widened on two consecutive ticks**, which is a fact about history and so
    is read from and written to `--state <file>`: one JSON object holding, per stream,
    the last value, the tick that wrote it and the consecutive widening count. Without
    `--state` nothing is remembered, no streak can be two, and the verb exits 0 with
    every widening stream still named on the line.
12. **`--json` prints the same reading, once, as one object.** The stream rows, their
    numbers, ratios and trends, the verdict, and the absent streams with the flag each
    one wanted. `--json` replaces the lines; it never adds to them.
13. **Every outside read is a seam, bounded, and returns DATA.** The forge is
    `gh pr list` behind a `Forge` interface, git is `git -C <repo-dir>` behind a `Git`
    interface, and the clock is injected. Each child gets `--timeout <n>` seconds
    (default 60) and is killed and named when it runs past it. A pull request title, a
    body, a log name, a ledger cell and a version stamp are all text from a host: they
    are counted and printed through `internal/oneline`, never executed and never read as
    an instruction. No verb here writes to `--repo`, `--repo-dir`, `--bin` or the forge.
14. **The refusals.** A `--since` that is neither RFC3339 nor a Go duration names both
    spellings; a `--since` in the future names the clock; a `--versions` or `--certs`
    with a header this spec does not list names the file and the header it wanted; an
    unreadable `--ledger`, `--retired` or `--receipts` names the path. Each is exit 2,
    and prints no stream line — a partial reading of convergence is the thing this verb
    exists to replace.

### Red tests this section demands

Numbered, one sentence each, every fake standing where the real thing is a forge, a git,
a bench or a clock; nothing below reaches a network.

1. `TestConvergencePrintsOneLinePerStream`: a fake forge, a fake git, a fixture ledger, receipts, retired README and bin yield exactly seven `CONVERGENCE <stream>` lines and one verdict line, in the fixed stream order.
2. `TestATrendIsTheDirectionTheStreamConverges`: a stream with fewer open edges than at `--since` is `contracting`, one with more is `widening`, an unchanged one is `flat`, and `CLASSES` inverts all three because it converges upwards.
3. `TestRatioIsAlwaysNowOverBefore`: a stream at 3 now and 4 before prints `ratio=0.75` whichever way it converges, and a `before` of zero prints `ratio=-` rather than an infinity.
4. `TestLandingReadsRoundsFromTheBodyAndTheLogs`: two fake merged `integration-` pull requests, one whose body names `round 3` and one with three `<pr>-round-<n>.log` files, give the mean of the two, and the logs win over a body that disagrees.
5. `TestLandingComparesTheWindowWithTheOneBefore`: batches merged before `--since` are the `before` mean and never the `now` mean, and `per-hour=` divides by the window's own length.
6. `TestClassesCountsTheIndexEntriesAtBothRevisions`: a fake git answering an older `SPEC-CI.md` with two fewer `###` entries under `## The class tests` prints `before` two lower, and `###` entries outside that section are not counted.
7. `TestScriptsCountsWhatIsLeftAndWhatTheWindowRetired`: a fake bin of two `.sh` files, one shebang file with no extension, one binary and one subdirectory counts three, and a retired README with two rows dated in the window and one outside gives `before=5`.
8. `TestRetiredRowsInheritTheNearestDateAbove`: rows under a dated heading take that date, a row under no date at all is counted in `undated=` and in neither window.
9. `TestPRsCountsWhatWasOpenAtSince`: an open pull request created inside the window is in `now` and not in `before`, and one created before `--since` and closed inside it is in `before` and not in `now`.
10. `TestEdgesIsTheGateAndTheRounds`: receipts with `Edges:` in the notes and no issue count as open, one with an issue does not, `before` reads only receipts written before `--since`, and `--by` narrows the rounds to the named friends.
11. `TestFleetIsTheUnitsOffTheMajorityStamp`: a snapshot of five rows at one stamp is `now=0`, one row at a second stamp is `now=1`, and both headers — `name/stamp/revision/platform` and `machine/stamp` — read the same.
12. `TestFleetAndLedgerTakeTheirBeforeFromTheState`: with no state file both are `before=-` and `trend=flat`, and with a state file holding the last tick they compare against it.
13. `TestLedgerCountsTheRowsNotYetPass`: `FAIL then PASS` is closed, `PARTIAL: …`, `TODO` and `NEEDS WORK` are open, and a non-table line is neither.
14. `TestAStreamWithNoSourceIsAbsentNotZero`: with no `--bin`, `--repo-dir` or `--versions`, those three streams print `trend=absent` naming their flag, `streams=` counts four and the verdict names them in `absent=`.
15. `TestExitOneOnlyOnTheSecondConsecutiveWidening`: one widening tick against a state file is `WARN` at exit 0, a second at the same stream is exit 1, and a contracting tick in between resets the streak.
16. `TestWithNoStateNothingIsRemembered`: two widening runs with no `--state` are both exit 0, and neither writes a file.
17. `TestConvergenceRefusesAMissingFlag`: each of `--repo`, `--ledger`, `--receipts`, `--retired` and `--since` omitted is exit 2 printing `refusing to guess` and naming the flag, and omitting all five names all five in one run.
18. `TestConvergenceRefusesASinceItCannotRead`: `--since yesterday` and `--since 2026-13-40T00:00Z` each name both spellings, a `--since` after the injected clock names the clock, and neither prints a stream line.
19. `TestConvergenceRefusesAVersionsFileItDoesNotKnow`: a `--versions` with an unknown header, and a row of the wrong arity, are each exit 2 naming the file and the two headers it reads.
20. `TestEveryFieldSurvivesAHostileValue`: a pull request title, a ledger cell, a stamp and a `--by` name each holding a newline, an `=` and a bidi override print as one field on one line.
21. `TestJSONCarriesTheSameReadingAsTheLines`: `--json` parses to the same per-stream numbers, ratios, trends, verdict and absent list that the lines print, and prints no `CONVERGENCE` line.
22. `TestEveryChildIsBounded`: a fake forge and a fake git that hang past `--timeout` are each exit 2 naming the child and the deadline, and no stream line is printed.
