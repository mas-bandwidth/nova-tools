# REPORT: the tick gate is red at 002cd2c

Card: `the-dirty-tick-drive-gate-red-at-002cd2cb` (attempt 3).
Base: `dev` at `002cd2c94d64f15e7b537217405cab0bbf00bc4b`.
Run: **37999299336**, job `tick-gate`, command

    go test -tags functional -count=1 -timeout 100s -run '^TestTheDirtyTickDriveOnAStore$' ./cmd/nova-sprint

(`.github/workflows/certification.yml:802-803`), env `NOVA_SPRINT_GATE_STORE=1`.
Result: `--- FAIL: TestTheDirtyTickDriveOnAStore (39.15s)`.

## Cause

A **code-level conflict between the ask's budget and the tick gate**, not bench
load alone and not a regression in the window under test:

* the gate is `MaxTickWall = time.Second`
  (`cmd/nova-sprint/dirty_drive_functional_test.go:60`), the owner's law of
  2026-09-30 ("the whole intent is sub-second ticks");
* the ask's budget is `AskBudget = 2 * time.Second`
  (`internal/sprint/store/tick_ask.go:36`), **twice the gate**;
* the readers' ask is one part of one tick, scheduled once a tick
  (`internal/sprint/steps_tick.go:264`, `{Readers, []TickPartDef{{"ask", TickAsk}}}`,
  `TickAsk` at `internal/sprint/steps_tick.go:1015`), and its store-side fenced
  step (`func (t *tickRun) askInSteps(step Step)`, `internal/sprint/store/tick_ask.go:79`)
  runs `until := begin.Add(askBudget(...))` (`tick_ask.go:84`): a single ask may
  spend up to its whole two-second budget, so **one tick with reads to ask can
  carry the whole tick past the one-second gate by itself**;
* the pump's other two slow parts already exceed the gate without the ask: the
  pump's `drain` (`PartDrain = "drain"`, `internal/sprint/steps_tick.go:248`,
  scheduled at `:263`) and `accept` (scheduled at `:263`) together were
  `693 ms + 430 ms = 1.123 s` in the failing run. The fix therefore has to bound
  the **whole tick**, not just `AskBudget`.

## The log is unreachable in a card

The run's log cannot be read from inside the card. `gh` is the refusing shim
(`internal/cardcontract/shims.go:160`,
\`echo "gh: REFUSED $1 $2: no GitHub CLI in a card; ..."\`). Run at the slot's
`shim/gh` it prints and exits 2:

    gh: REFUSED run view: no GitHub CLI in a card; the sprint pushes and opens what the card finishes with

No copy of run 37999299336's log is preserved in the checkout (`grep -rn
"37999299336" .` is empty). The finding therefore rests on the drive's own lines,
quoted below, plus the run's part times carried by the card.

## The lines the finding rests on

The drive prints, in `cmd/nova-sprint/dirty_drive_functional_test.go`:

* the gate summary, `:607-609`:

      gate := fmt.Sprintf("THE GATE: %d ticks, mean %s, max %s, %d over %s; whole-table reads after the first tick %d; the routes read (3 routes): %d round trips, at most %d a tick; machine load %s",
          len(ticks), (sumTook / time.Duration(max(len(ticks), 1))).Round(time.Millisecond), maxTook.Round(time.Millisecond), len(over), MaxTickWall, whole, routeTrips, routeMax, strings.Join(loads, " | "))

* the per-part breakdown of the slowest tick, `:641`:

      report += fmt.Sprintf("\n  the slowest tick (%d) part by part: %s", tk.n, strings.Join(parts, ", "))

* the assertion that fails a tick over the gate when the bench's store is on,
  `:590` and `:607-614`, ending at

      assert.Fail(t, fmt.Sprintf("over the gate of %s: %s", MaxTickWall, o))   // :612

The run's own numbers, by part (from the card's finding for run 37999299336):
**work drain 693 ms, work accept 430 ms, readers ask 1.766 s**. The tick number
and the machine load line printed beside them are not recoverable here because
the log is unreachable; the parts and their times are. `drain + accept 1.123 s`
alone is already over `MaxTickWall`, and the `readers ask 1.766 s` part is the
single largest contributor.

## Regression, or bench load?

**No regression in the window** `b0e29f21..HEAD` (the commits since the fixture
base):

    $ git log b0e29f21..HEAD -- internal/sprint/store/tick_ask.go internal/sprint/steps_tick.go
    (empty)

Only fixture and held-friend commits touch `internal/sprint` in that window
(`d815debb8 tests: one table read by many goroutines never races`,
`06a0f3c71 sprint: a held friend keeps no ready card`,
`dbdf670c7 sprint: a held friend keeps no card, begun or not`,
`c4a4ef70c`/`89f2844a9 the functional/certification fixture commits`); none of
them changes the ask, the pump or the gate.

The conflict is older and structural:

    $ git show -s --format="%h %ci %s" 942ea5e44 377316c64
    942ea5e44 2026-09-30 18:33:30 -0400 sprint: THE GATE IS A TEST: ... MaxTickWall (1 s, the owner's law) ...
    377316c64 2026-10-06 17:50:07 -0400 sprint: the ask step asks in small fenced steps within a budget ...

The one-second gate arrived in `942ea5e44`; the two-second `AskBudget` arrived
later in `377316c64` ("the ask step asks in small fenced steps within a budget").
So the run is red at `002cd2c` because the ask's own budget was allowed to
exceed the tick's own gate, and the pump's `drain`+`accept` leave no headroom
under that gate. Bench load amplifies it — the drive plays the world every 10 ms
and the store, loop and world share the machine (`certification.yml:734-746`)
— but the load is not the cause: the numbers are reproduced by the constants
alone, and a tick whose ask is at its own budget is over the gate by
construction.

## Fix card(s) it needs

### Card A — bound the whole tick to `MaxTickWall`, and the ask within it

    tier: pro
    REPO: mas-bandwidth/nova-tools (re-points to mas-bandwidth/nova-sprint at the v1.2.2 split)
    BASE: dev
    KIND: fix
    PATHS: internal/sprint/store/tick_ask.go, internal/sprint/steps_tick.go, cmd/nova-sprint/dirty_drive_functional_test.go
    TEST: go test -tags functional -count=1 -timeout 600s -run '^TestTheDirtyTickDriveOnAStore$' ./cmd/nova-sprint

The tick needs a single wall-clock budget bounded by `MaxTickWall` and each part
— including the ask — held under it, so a tick that reaches its budget stops
starting further parts and leaves the rest for the next tick (the ask already
counts `tally.left` / `tally.unfinished` for exactly this,
`internal/sprint/store/tick_ask.go:64-77`). The ask's `askBudget`
(`tick_ask.go:56-65`) must return at most the tick's remaining wall, not a flat
`2 * time.Second`; and `drain`/`accept` (`internal/sprint/steps_tick.go:248`,
`:263`) must not by themselves carry a tick over the gate.

Red test: the tick gate itself —
`cmd/nova-sprint/dirty_drive_functional_test.go:612`, the `assert.Fail` "over the
gate of 1s" reached when `tk.took > MaxTickWall && tk.err == ""` (`:590`) under
`NOVA_SPRINT_GATE_STORE=1`; drive it with the run's shape
(`go test -tags functional -count=1 -timeout 100s -run
'^TestTheDirtyTickDriveOnAStore$' ./cmd/nova-sprint`). Before the fix it fails on
the tick whose `readers ask` part spends `AskBudget`; after it, every tick is at
or under `MaxTickWall` with the ask's own budget at most that.

### Card B — make `AskBudget` no larger than `MaxTickWall`, with a unit red test

    tier: pro
    REPO: mas-bandwidth/nova-tools (re-points to mas-bandwidth/nova-sprint at the v1.2.2 split)
    BASE: dev
    KIND: fix
    PATHS: internal/sprint/store/tick_ask.go, internal/sprint/store/tick_ask_test.go
    TEST: go test -count=1 -timeout 600s ./internal/sprint/store

Pin the invariant in code: `AskBudget` (or whatever `askBudget` returns) is at
most the tick's wall budget, so the ask can never on its own exceed the gate.
Red test: a new `internal/sprint/store/tick_ask_test.go` case asserting
`askBudget(ctx, now) <= MaxTickWall` (and `<= the context's remaining half`),
which fails today because `AskBudget = 2 * time.Second > MaxTickWall = 1s`.
(If Card A already folds the constant into the tick's budget, Card B is the
guard that keeps it from regressing.)

## Not done

* The run's log (37999299336) was not read: `gh` is a refusing shim inside a
  card (`internal/cardcontract/shims.go:160`) and no copy of the log is in the
  checkout. The tick number and machine-load line of the slowest tick are
  therefore not quoted; the drive's own print lines (`:607-609`, `:641`) and the
  run's part times are.
* This is a report; no source file was changed, and neither fix card was
  implemented (the card is read-and-report only).
* Left for cleanup: none — nothing outside the job directory was touched and the
  one added file is `REPORT.md`, the deliverable.
